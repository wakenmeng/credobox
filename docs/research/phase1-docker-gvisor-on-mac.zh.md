# Mac 上的第 1–2 阶段：Docker Desktop 上的 Docker Runtime，以及 gVisor

> [English](phase1-docker-gvisor-on-mac.md) | 中文

> 调研日期：2026-11-21，所有链接均在这一天访问。Docker Desktop 相关事实针对 **Docker Desktop 4.100.0（2026-11-19 发布）**及更早版本，以 [release notes](https://docs.docker.com/desktop/release-notes/) 为准。Docker Desktop 发版频繁，而且恰恰改过本文依赖的行为（VirtioFS 上的 unix socket 在 2026 年 8 月的 4.86.0 才改），所以依赖本文任何结论之前，先看 `docker version` 和 release notes。
> 只用一手来源：Docker 文档和 release notes、Docker 官方博客、GitHub 上的 moby/runc/gVisor 源码、gVisor 文档、Linux 内核文档。二手来源标注 **（二手）**。标 **【推断】** 的是我自己的结论，不是来源原文。术语以 [GLOSSARY](../../GLOSSARY.zh.md) 为准。
> 我没有运行任何 Docker 命令。只能靠实验解决的问题列在 §5，附要跑的命令。

---

## 1. 摘要

- **ADR 0002 的 unix socket 通道在当前 Docker Desktop 上应该能用，但只是最近才行，而且只限 VirtioFS。** Docker Desktop 4.86.0（2026-08-10）写道 "AF_UNIX sockets shared over VirtioFS now work in both directions (host to guest and guest to host)"（[release notes](https://docs.docker.com/desktop/release-notes/)）。旧的 osxfs 文档说 socket 文件根本无法跨越 hypervisor（[osxfs 文档，存档](https://github.com/docker/docker.github.io/blob/a17a06c6a15028011250900c49a1b59620cf1c65/docker-for-mac/osxfs.md)）。要锁定最低 Docker Desktop 版本并实测（§5 E1）。在 gVisor 下，同一通道需要 `runsc --host-uds=create`，因为默认值是 `none`（[runsc flags 源码](https://github.com/google/gvisor/blob/master/runsc/config/flags.go)）。
- **在默认网络上，Sandbox 能访问 Mac 以及 Mac 能访问的一切。** 出站流量由 Mac 上的 `com.docker.backend` 进程重新发起（[networking](https://docs.docker.com/desktop/features/networking/)），`host.docker.internal` "Resolves to the internal IP address of your host"（[how-tos](https://docs.docker.com/desktop/features/networking/networking-how-tos/)）。没有文档说明只绑定 `127.0.0.1` 的服务能否被访问，所以 §5 E2 来测。无论结果如何，修法都一样：永远不要把 Sandbox 放到有默认路由的网络上。
- **不用 macOS iptables 也能强制执行的最小出口设计：** 每个 Sandbox 只接入一个 `--internal` bridge 网络，并用 gateway mode `isolated` 创建（Engine ≥ 28.0）。Egress Proxy 和 DNS resolver 作为**容器跑在 Docker VM 里**，各自同时接入该 internal 网络和一个普通出口网络。internal 网络 "no default route"（[network create](https://docs.docker.com/reference/cli/docker/network/create/)），所以代理不是可选项：绕过它的流量都会失败。从 Engine 26.0 起，只接入 internal 网络的容器，其内嵌 DNS 不再转发给外部服务器（[26.0 notes](https://docs.docker.com/engine/release-notes/26.0/)）；从 26.1.3 起，它可以使用 internal 网络*内部*的 resolver（[26.1 notes](https://docs.docker.com/engine/release-notes/26.1/)）。因此 `--dns <resolver IP>` 就能做到"只用平台 resolver"。
- **Docker Desktop 默认没有磁盘限制。** 这条说法的结论成立，但给出的理由不对。moby 的 overlay2 driver 只有在底层文件系统是带 `pquota` 挂载的 xfs 时才接受 `--storage-opt size`（[overlay.go](https://github.com/moby/moby/blob/master/daemon/graphdriver/overlay2/overlay.go)）。Docker Desktop 还默认使用 containerd image store（[settings](https://docs.docker.com/desktop/settings-and-maintenance/settings/)），而在这条代码路径上，moby 把 `StorageOpt` 传了下去，准备 snapshot 时却没带任何大小（[image_snapshot.go](https://github.com/moby/moby/blob/master/daemon/containerd/image_snapshot.go)）。
- **Freeze：** 用 runc 时，`docker pause` 写 `cgroup.freeze`，即 cgroup v2 freezer（[opencontainers/cgroups fs2/freezer.go](https://github.com/opencontainers/cgroups/blob/main/fs2/freezer.go)）。**用 gVisor 时不是这样**：`runsc pause` 调用 Sentry 自己的 `kernel.Pause()`（[runsc boot/controller.go](https://github.com/google/gvisor/blob/master/runsc/boot/controller.go)），所以 GLOSSARY 里 "This is what `docker pause` does" 只对特定 Runtime 成立。
- **在 Mac 上用 `docker commit` 实现 Capture 可行，但开箱即用时会把 Sandbox 状态泄漏进 Template。** moby 用*容器*的配置（Env、Entrypoint、User……）作为新镜像的配置（[daemon/commit.go](https://github.com/moby/moby/blob/master/daemon/commit.go)），于是 Secret 占位符、代理变量和注入的 entrypoint 都会被固化进去。【推断】写进 trust store 的 per-Sandbox CA 也会被固化，破坏 ADR 0006 的 "one CA per Sandbox"。Volume 不包含在内（[commit](https://docs.docker.com/reference/cli/docker/container/commit/)）。在 gVisor 下，默认的 rootfs overlay 让改动不落到宿主机的 upper layer，所以除非用 `--overlay2=none`，`docker commit` 会漏掉这些改动。
- **加固：** `--cap-drop=ALL`、`no-new-privileges`、`--pids-limit` 都是普通的 engine 功能，【推断】在 Docker Desktop 上应该都能用。【推断】`--cap-drop=ALL` 会弄坏一个需要切换到 `user`、需要管理文件的 root Guest Agent，所以要加回一个最小集合。userns-remap 和 containerd image store 互斥（[containerd store](https://docs.docker.com/engine/storage/containerd/)）。Enhanced Container Isolation 需要 Docker Business（[settings](https://docs.docker.com/desktop/settings-and-maintenance/settings/)）。
- **第 2 阶段的 gVisor：** gVisor 支持 Linux 5.6+ 上的 x86_64 和 ARM64（[install](https://gvisor.dev/docs/user_guide/install/)）。默认平台 systrap "is a better choice when running inside a VM"（[platforms](https://gvisor.dev/docs/architecture_guide/platforms/)），所以 Apple Silicon 上的 Lima、Colima 或 OrbStack Linux VM 不需要嵌套虚拟化也可行。Docker Desktop 没有文档化的方法添加 `runsc`。与 agent 相关的缺口里，Docker-in-gVisor 需要特殊 flag（[docker-in-gvisor](https://gvisor.dev/docs/tutorials/docker-in-gvisor/)），arm64 上 `perf_event_open` 和 `userfaultfd` 未实现（[arm64 syscalls](https://gvisor.dev/docs/user_guide/compatibility/linux/arm64/)）。

---

## 2. 逐题结论

### Q1. Host Agent ↔ Guest Agent 通道（ADR 0002）

**来源怎么说**

- 旧版 osxfs 文档（2016–2017 年前后）："Socket files and named pipes only transmit between containers and between macOS processes"；"no transmission across the hypervisor is supported, yet"（[osxfs.md，存档](https://github.com/docker/docker.github.io/blob/a17a06c6a15028011250900c49a1b59620cf1c65/docker-for-mac/osxfs.md)）。**已过时。**
- Docker Desktop 4.86.0（2026-08-10），在 "For all platforms" 下："AF_UNIX sockets shared over VirtioFS now work in both directions (host to guest and guest to host)."（[release notes](https://docs.docker.com/desktop/release-notes/)）
- Docker Desktop 4.93.0（2026-09-28）修复了 "a security vulnerability that allowed VM-side code to request Unix socket port forwards, which could expose arbitrary host paths to manipulation"（[release notes](https://docs.docker.com/desktop/release-notes/)），未列 CVE 编号。
- Mac 上的文件共享实现是 **VirtioFS**（默认）和 **gRPC FUSE**，Docker VMM 只支持 VirtioFS（[settings](https://docs.docker.com/desktop/settings-and-maintenance/settings/)）。Mac 上的 VMM 有 Docker VMM（仅 Apple silicon）、Apple Virtualization framework，以及已弃用的 HyperKit（[VMM](https://docs.docker.com/desktop/features/vmm/)）。
- Docker Desktop 本来就会把特定的宿主机 socket 转发进容器，例如 SSH agent 的 `/run/host-services/ssh-auth.sock`（[how-tos](https://docs.docker.com/desktop/features/networking/networking-how-tos/)）。
- Docker Desktop "can't route traffic to Linux containers"：Mac 访问不到容器 IP（[how-tos](https://docs.docker.com/desktop/features/networking/networking-how-tos/)）。
- gVisor 下，`--host-uds` "controls permission to access host Unix-domain sockets. Values: none|open|create|all, default: none"。`create` "allows UDS from the host to be created, e.g. bind(2)"，`open` 允许 `connect(2)`（[flags.go](https://github.com/google/gvisor/blob/master/runsc/config/flags.go)、[config.go](https://github.com/google/gvisor/blob/master/runsc/config/config.go)；master 分支 commit `ecba2b3`，2026-11-20）。
- 4.86 之前，有用户报告 VirtioFS 下 socket bind mount 失败（二手：[Colima #997](https://github.com/abiosoft/colima/issues/997)、[Docker 论坛](https://forums.docker.com/t/unix-socket-on-bind-mount/142653)）。

**各方案及其与 ADR 0005 的契合度**

| 方案 | 在 Docker Desktop for Mac 上可行？ | 与 ADR 0005 的契合 | 备注 |
|---|---|---|---|
| **A. Guest Agent 在 bind mount 的 per-Sandbox 目录里监听 unix socket，macOS 上的 Host Agent 去连**（当前方案） | 据 release note，Docker Desktop ≥ 4.86 且用 VirtioFS 时可行；gRPC FUSE 未知（§5 E1） | **好。** Host Agent 主动拨号，自己不监听任何东西。socket 属于 Guest Agent | 【推断】该目录在 Sandbox 内必须只有 root 可写：能替换 socket 的普通用户会接到 Host Agent 的连接和 per-Sandbox token。root Exec（spec 允许 `user: root`）总能做到这一点，所以 token 证明不了 Guest Agent 的身份。这可以接受，因为 token 只用来向 Guest Agent 证明平台的身份，反过来不行。该目录里绝不能出现由宿主机创建的 socket（即 "host to guest" 方向），否则 Sandbox 里的代码就能连上它。gVisor 下需要 `--host-uds=create`（不是 `all`） |
| **B. `docker exec -i <id> /opt/ga/guestagent --stdio`，gRPC 跑在 exec 流上** | 【推断】可行。它只用 Docker API，而 Docker Desktop 本来就通过 unix socket 把 Docker API 从 Mac 代理到 VM（[networking 博客，2022](https://www.docker.com/blog/how-docker-desktop-networking-works-under-the-hood/)） | **好。** 由外向内，根本没有 socket 文件，也没有网络端点 | 【推断】这仍然遵守 ADR 0002，因为所有功能都还在唯一的 Guest Agent 里，exec 只是一根字节管道。用非 TTY 的 exec：TTY 行规程会破坏二进制数据，而非 TTY 的 stdout 带 8 字节帧头的多路复用，需要解复用。每次（重）连接要付一次 exec 的开销。在 runsc 下无需改动。**最通用的后备方案** |
| **C. Host Agent 跑在 VM 里（带 Docker socket 的容器，或经特权 helper 启动的进程），在 VM 一侧路径或 Docker volume 上用普通 unix socket** | 【推断】可行，但属于不受支持的改造：VM 由 Docker Desktop 管理 | 前提是 Host Agent 自己（给 Control Plane 用）的监听端口 Sandbox 访问不到，才算好。若 Host Agent 容器在普通 bridge 上，或用 `--network host` 并监听 `0.0.0.0`，就*能*经 bridge 网关被访问到（Q2） | 放弃了"Host Agent 是 macOS 上的 Go 程序"这一点。如果 Host Agent 必须访问容器 IP（方案 D），反正也得这么做 |
| **D. Guest Agent 在专用的"控制" Docker 网络上监听 TCP** | 只有 Host Agent 也在 VM 里才行：Mac 路由不到容器 IP | **弱。** 除非关闭 ICC，否则该网络上每个 Sandbox 都能访问其他 Sandbox 的 Guest Agent；可关闭 ICC 也会挡住 Host Agent 容器，结果只能每个 Sandbox 一个控制网络 | 不推荐 |
| **E. 把 Guest Agent 端口发布到 Mac（`-p 127.0.0.1:N:port`）** | 可行 | **差。** 与 ticket 11 的 "The container itself maps no host ports" 冲突。【推断】如果经 `host.docker.internal` 能访问 Mac 的 loopback（Q2），NAT 网络上的其他 Sandbox 也能访问它 | 不推荐 |

**【推断】建议：** 主通道保留方案 A，但以最低 Docker Desktop 版本和 E1 通过为前提。在 Docker Runtime 里把方案 B 放在同一个 `dial()` 接口后面作为后备；对于 socket 共享未经验证的 Docker 宿主机，默认就用 B。macOS 上 Control Plane 与 Host Agent 之间用 unix socket 而不是 TCP，这样即使某个 Sandbox 网络配错了，也没有 TCP 目标可打。

### Q2. `host.docker.internal` 与宿主机可达性

**来源怎么说**

- `host.docker.internal` "Resolves to the internal IP address of your host"；`gateway.docker.internal` "Resolves to the gateway IP of the Docker VM"。官方示例用 `curl http://host.docker.internal:8000` 访问 Mac 上的 `python -m http.server 8000`（[how-tos](https://docs.docker.com/desktop/features/networking/networking-how-tos/)）。`http.server` 默认绑定所有网卡，所以这个示例只证明了 `0.0.0.0` 上的服务可达。
- "All outbound container network traffic originates from the `com.docker.backend` process"；"On the host, Docker Desktop's backend process receives the traffic and creates standard TCP/IP connections"（[networking](https://docs.docker.com/desktop/features/networking/)）。2022 年的 Docker 博客描述的是同一设计：宿主机一侧的用户态协议栈 "calls `connect()` itself from the host"（[博客，2022 年 1 月](https://www.docker.com/blog/how-docker-desktop-networking-works-under-the-hood/)）。这个用户态协议栈在 4.19 从 vpnkit 换成了 gVisor 的 netstack（二手/厂商博客：[Docker Desktop 4.19](https://www.docker.com/blog/docker-desktop-4-19/)）。
- Docker Desktop 默认子网是 `192.168.65.0/24`（[settings](https://docs.docker.com/desktop/settings-and-maintenance/settings/)）。
- "Enable host networking"（4.34+）"Allows containers started with `--net=host` to use `localhost` to connect to TCP and UDP services on the host"（[settings](https://docs.docker.com/desktop/settings-and-maintenance/settings/)、[host driver](https://docs.docker.com/engine/network/drivers/host/)）。
- 在 internal 网络上，"an address is assigned to the bridge … containers in the network can access host services listening on that bridge address"；gateway mode `isolated`（仅限 internal 网络）则 "no address is assigned to the bridge"（[port publishing](https://docs.docker.com/engine/network/port-publishing/)）。`isolated` 和 `gw-priority` 都在 Docker Engine 28.0.0 加入（[Engine 28 notes](https://docs.docker.com/engine/release-notes/28/)）。
- 宿主机防火墙可以按 `com.docker.backend` 过滤（[networking](https://docs.docker.com/desktop/features/networking/)）。

**来源没说的：** **只绑定 Mac 上 127.0.0.1** 的服务能否经 `host.docker.internal` 访问。论坛上说法不一（二手：[论坛](https://forums.docker.com/t/connection-refused-on-host-docker-internal/136925)）。【推断】连接是由 Mac 上的进程重新发起的，映射到 Mac loopback 与否是 Docker Desktop 的设计选择，两种都有可能。在 §5 E2 证明相反之前，按可达处理。

**【推断】默认/NAT bridge 上的容器能访问到：**
1. Mac 上绑定 `0.0.0.0` 的服务（有文档），可能还有 Mac 上绑定 `127.0.0.1` 的服务（未知）；
2. Mac 能访问的一切：局域网、只走 VPN 的公司内网主机、互联网；
3. VM 内部的 bridge 网关地址，以及 VM 根网络命名空间里监听 `0.0.0.0` 的任何东西（`--network host` 的容器、Docker Desktop 自己的 VM 服务）；
4. 同一 bridge 上的其他容器（ICC 默认为 `true`；[bridge driver](https://docs.docker.com/engine/network/drivers/bridge/)）。

**怎么挡【推断】：** 按名字屏蔽没用（IP 照样能路由），Docker Desktop 也没有关闭容器访问宿主机的设置。只能从结构上挡：
- `--internal` 网络没有默认路由，于是到 `192.168.65.x`、Mac、局域网都没有通路；
- gateway mode `isolated` 去掉 VM 一侧的 bridge 地址；
- 永远不用 `--network host`；
- 每个 Sandbox 一个 internal 网络，或设 `enable_icc=false`，把 Sandbox 彼此隔开。

*确实*有路由的 Egress Proxy 必须自己先解析域名，**再**拒绝 `host.docker.internal`、`192.168.65.0/24`、Docker 子网、link-local 和 RFC 1918 地址，以防 DNS rebinding。

### Q3. 不用 macOS iptables 的出口管控

**来源怎么说**

- `--internal`："Containers on an internal network may communicate between each other, but not with any other network, as no default route is configured and firewall rules are set up to drop all traffic to or from other networks"（[network create](https://docs.docker.com/reference/cli/docker/network/create/)）。
- 一个容器可以接入多个网络（重复 `--network`），`gw-priority` 决定由哪个网络提供默认网关（[run](https://docs.docker.com/reference/cli/docker/container/run/)、[networking](https://docs.docker.com/engine/network/)）。
- 用户自定义网络上的容器使用 `127.0.0.11` 的内嵌 DNS，它 "forwards external DNS lookups to the DNS servers configured on the host"，可以用 `--dns` 覆盖（[networking](https://docs.docker.com/engine/network/)）。
- CVE-2024-29018 的修复（Engine 26.0）："Do not forward requests to external DNS servers for a container that is only connected to an 'internal' network"（[26.0 notes](https://docs.docker.com/engine/release-notes/26.0/)）。Engine 26.1.3："Fix a regression that prevented the use of DNS servers within a `--internal` network"（[26.1 notes](https://docs.docker.com/engine/release-notes/26.1/)）。
- `HTTP_PROXY` 一类变量只是客户端读取的约定。Docker Desktop 自带的容器代理设置只在 "air-gapped container" 模式下才作用于运行中的容器，该模式需要 Docker Business，并在 `admin-settings.json`（`containersProxy`、`transparentPorts`）里按组织统一配置（[air-gapped containers](https://docs.docker.com/enterprise/security/hardened-desktop/air-gapped-containers/)、[networking](https://docs.docker.com/desktop/features/networking/)）。

**方案对比【推断】**

| 做法 | 能强制？ | 结论 |
|---|---|---|
| 只设 `HTTP(S)_PROXY` 环境变量 | 不能：任何程序都能直接开 socket | 只是便利；放在强制机制*之上* |
| `--internal` 网络 + 双网卡代理容器 | **能。** 除了 internal 网络上的同伴，没有任何路由 | **最小可强制方案** |
| 通过特权 helper 在 Docker VM 里做透明重定向（iptables `REDIRECT`/TPROXY） | 能，规则在就有效 | Docker Desktop 不支持：VM 由它管理，网络变化或重启时 Docker 可能改写规则。适合在第 2 阶段的 Linux VM 里当练习，不适合第 1 阶段 |
| 让代理成为 Sandbox 的默认网关 | Sandbox 里需要 `NET_ADMIN`，或从 VM 动 netns | 不值得 |
| Docker Desktop air-gapped containers | 能，全局生效 | 仅限 Business，且不能按 Sandbox 配置；与 Egress Policy 的几种模式不匹配 |

**最小可强制方案【推断】**

```
docker network create --internal \
  -o com.docker.network.bridge.gateway_mode_ipv4=isolated \
  -o com.docker.network.bridge.enable_icc=true  sbx-<id>          # 每个 Sandbox 一个
docker network create sbx-egress                                   # 普通 NAT bridge
# Egress Proxy + DNS resolver：容器，双网卡，无管理 API
docker network connect sbx-<id> egress-proxy                       # 同时在 sbx-egress 上
docker run --network sbx-<id> --dns <resolver-ip-on-sbx-<id>> \
  -e HTTPS_PROXY=http://<proxy-ip>:3128 -e HTTP_PROXY=... -e NO_PROXY= ...  <template>
```

- **完全开放模式也走代理。** 完全开放变成代理上的一条"允许所有公网地址"规则，于是那些始终封禁的地址（metadata、Host、私网）在每种模式下都继续生效，符合 spec §7 的要求。
- **DNS。** Sandbox 的 `/etc/resolv.conf` 指向 `127.0.0.11`，内嵌服务器把非容器名只转发给 internal 网络上的 `--dns` resolver。直接发往 `8.8.8.8:53` 的查询没有路由。只应答白名单域名（其余一律不递归），就堵住了经 resolver 的 DNS 隧道。
- **残留泄漏。** 内嵌 DNS 仍会应答同网络上的容器名（代理、resolver）；每个 Sandbox 一个网络时，能暴露的也就这些。
- **Egress Proxy 不能是 macOS 进程。** Sandbox 没有到 Mac 的路由，所以代理和 resolver 必须是 VM 里的容器。代理"没有管理 API"（ADR 0005），**那谁来配置它？** 应由 Host Agent 通过*它自己*发起的通道推送配置：把 Q1 的方案 A 或 B 用在代理容器上，或者用只读 bind mount 的配置文件。
- **版本下限：** `isolated` 需要 Docker Engine ≥ 28.0，DNS 行为需要 ≥ 26.1.3。检查 Docker Desktop 自带的 Engine 版本（`docker version`）。

### Q4. 磁盘限制

**来源怎么说**

- `--storage-opt size` "is only available for the `btrfs`, `overlay2`, `windowsfilter`, and `zfs` storage drivers"，对 overlay2 还要求 "the backing filesystem is `xfs` and mounted with the `pquota` mount option"（[run](https://docs.docker.com/reference/cli/docker/container/run/)）。
- moby 源码里，overlay2 在没有 project quota 时返回 `"--storage-opt is supported only for overlay over xfs with 'pquota' mount option"`（[overlay.go](https://github.com/moby/moby/blob/master/daemon/graphdriver/overlay2/overlay.go)）。
- Docker Desktop 的 "Use containerd for pulling and storing images" **默认开启**（[settings](https://docs.docker.com/desktop/settings-and-maintenance/settings/)），此时 engine 用的是 containerd `overlayfs` snapshotter，而不是 overlay2 graph driver（[containerd store](https://docs.docker.com/engine/storage/containerd/)）。
- moby 的 containerd image store 路径里，`CreateRWLayerOpts{StorageOpt: …}` 被传进 `createLayer`，但 snapshot 用 `sn.Prepare(ctx, layerName, parentSnapshot)` 创建，这个选项没被使用（[image_snapshot.go](https://github.com/moby/moby/blob/master/daemon/containerd/image_snapshot.go)；最后修改于 2026-02-21）。【推断】在默认配置的 Docker Desktop 上，`--storage-opt size=` 会被接受，然后静默地不生效。原因并不是"因为 ext4"。§5 E4 来确认。
- 我没找到任何 Docker 文档说明 Docker Desktop VM 的磁盘用什么文件系统。**"ext4" 无法确认。** Docker Desktop 只有一个整个 VM 的 "Disk usage limit"（[settings](https://docs.docker.com/desktop/settings-and-maintenance/settings/)）。
- 对 gVisor，在默认 rootfs overlay 下 Docker 的 `SizeRw` 看不到写入，那个讨论里的解决办法是 `--overlay2=none`（[gVisor #10256](https://github.com/google/gvisor/issues/10256)，2024-11-27 以 "not planned" 关闭）。

**替代方案【推断】**

1. **软限制（第 1 阶段，适用于 upper layer 在宿主机可见的任何 Runtime）：** Host Agent 轮询每个容器可写层的大小（`docker ps --size` / `SizeRw`），超限就终止 Sandbox，原因记为 `disk_limit`。这是事后拦截。Docker Desktop 整个 VM 的磁盘上限保护 Mac。
2. **限定可写路径：** rootfs 设 `--read-only`，再加 `--tmpfs /home/user:size=…` 和 `/tmp:size=…`。限制是硬的，但 tmpfs 页计入内存限额（cgroup v2 memory controller），`docker commit` 不包含 tmpfs，而且很多镜像装包时需要可写的 `/usr` 或 `/var`。这和 Capture 冲突。
3. **把 loop 挂载的文件系统镜像作为 Sandbox 的家目录**（一个 `local` volume，带 `type`/`device`/`o=loop`）。只对该路径是硬限制。需要 VM 内核支持 loop（§5 E4）。
4. **真正的修法（第 2 阶段 VM）：** Lima/Colima VM 里，`/var/lib/docker` 放在以 `pquota` 挂载的 xfs 上，用**经典 overlay2 driver**，关闭 containerd image store。这样 `--storage-opt size=` 就按文档描述的机制生效。

### Q5. Freeze（`docker pause`）

**来源怎么说**

- "On Linux, this uses the freezer cgroup … the process is unaware, and unable to capture, that it is being suspended"（[pause](https://docs.docker.com/reference/cli/docker/container/pause/)）。该页链接的是 cgroup **v1** freezer 文档，后者解释了为什么 SIGSTOP 能被察觉而 freezer 不能（[freezer-subsystem.txt](https://www.kernel.org/doc/Documentation/cgroup-v1/freezer-subsystem.txt)）。
- 在 cgroup v2 上，runc 的 cgroup 库向 `cgroup.freeze` 写入 `"1"`，然后读回状态核对（[opencontainers/cgroups fs2/freezer.go](https://github.com/opencontainers/cgroups/blob/main/fs2/freezer.go)）。
- 内核：向 `cgroup.freeze` 写 1 会停止该 cgroup 及其后代中的所有进程，"and will not run until the cgroup will be explicitly unfrozen"；"Processes in the frozen cgroup can be killed by a fatal signal"；`cgroup.events` 报告 `frozen 1`（[cgroup-v2](https://docs.kernel.org/admin-guide/cgroup-v2.html)）。
- **gVisor 不同。** `runsc pause` → `Sandbox.Pause` → `ContMgrPause` RPC → Sentry 内的 `cm.l.k.Pause()`，即 "pauses all tasks, blocking until they are stopped"（[container.go](https://github.com/google/gvisor/blob/master/runsc/container/container.go)、[sandbox.go](https://github.com/google/gvisor/blob/master/runsc/sandbox/sandbox.go)、[controller.go](https://github.com/google/gvisor/blob/master/runsc/boot/controller.go)）。
- 以上来源都没有明确说冻结期间内存和定时器会怎样。

**【推断】会让我们意外的地方：**
- **内存。** 冻结的页仍然分配着，计在容器的 memory cgroup 上，所以 Usage 把 Frozen 按内存 GB·秒计费是对的。但这些页*没有*被钉住：内存吃紧时内核仍可回收或换出，而 Docker Desktop 的 VM 默认有 1 GB swap（[settings](https://docs.docker.com/desktop/settings-and-maintenance/settings/)）。于是解冻很快，但不一定总是"毫秒级"（§5 E5）。
- **定时器和时钟照常走。** 冻结期间墙上时间和 monotonic 时间都在前进，到期的定时器在解冻后立即触发。`i++; sleep 1` 这类循环计数会停住；按流逝时间算的计数器会跳变；Go 的 `time.Ticker` 会丢掉错过的 tick。ticket 08 的验收测试必须用第一种。
- **网络连接。** 用 runc 时，VM 内核的 TCP 协议栈不会被冻结，它会继续回 ACK、缓存数据，直到接收窗口填满。对端只会通过应用层超时察觉。**Host Agent 与 Guest Agent 之间的 gRPC keepalive 会失败**，因为 Guest Agent 无法回应 HTTP/2 PING。所以 Host Agent 要么对 Frozen 的 Sandbox 暂停 keepalive，要么预期重连并续传（ADR 0005 本来就要求流可续传）。经 Exposed Port 的请求会挂住，所以端口代理应对 Frozen 的 Sandbox 快速拒绝。Egress Proxy 持有的上游连接可能触发空闲超时。gVisor 下，netstack 跑在 Sentry 里，`kernel.Pause()` 期间它是否继续回 ACK 未知（§5 E5）。

### Q6. Capture（`docker commit`）

**来源怎么说**

- "Commits do not include any data contained in mounted volumes"；默认 "the container being committed and its processes will be paused while the image is committed"（`--pause`/`--no-pause`）；`--change` 接受 `CMD|ENTRYPOINT|ENV|EXPOSE|LABEL|ONBUILD|USER|VOLUME|WORKDIR`（[commit](https://docs.docker.com/reference/cli/docker/container/commit/)）。该页没有明确提到 bind mount。
- 可写层保存 "All writes to the container that add new or modify existing data"，volume 和 bind mount 与它分开（[storage drivers](https://docs.docker.com/engine/storage/drivers/)）。
- moby 的 `CreateImageFromContainer`：除非 `NoPause` 否则先暂停；`if c.Config == nil { c.Config = container.Config }`；然后 `merge(newConfig, container.Config)`（[daemon/commit.go](https://github.com/moby/moby/blob/master/daemon/commit.go)，最后修改于 2026-09-25）。换句话说，**容器的运行时配置会变成镜像配置。** 经典 image store 和 containerd image store 都实现了 commit（`daemon/images/image_commit.go`、`daemon/containerd/image_commit.go`）。
- gVisor 下 rootfs overlay 默认开启（`--overlay2=root:self`）："All modifications are made to the overlay, keeping the underlying filesystem unmodified … disable it with `--overlay2=none`"（[filesystem](https://gvisor.dev/docs/user_guide/filesystem/)、[config.go defaultOverlay2](https://github.com/google/gvisor/blob/master/runsc/config/config.go)）。self-backed 的 filestore 位于宿主机 upper layer 中，由一个 whiteout 隐藏（[rootfs overlay 博客](https://gvisor.dev/blog/2023/05/08/rootfs-overlay/)）。gVisor 自己的替代方案是 `runsc tar rootfs-upper`，把 upper layer 写成一个 tar 文件（[rootfs snapshot](https://gvisor.dev/docs/user_guide/rootfs_snapshot/)）。

**【推断】对 ADR 0003 和 ticket 22 的影响：**

- **Guest Agent 二进制：** 只读 bind mount 不在可写层里，所以二进制不会被 capture。但 Docker 会在 rootfs 里创建**挂载点目录**（文件挂载则是一个空文件），这个空路径可能出现在 Template 里。无害，但如果要求完全一致就去掉它（§5 E6）。
- **配置泄漏，真正的问题：** Env（Secret 占位符、`HTTP(S)_PROXY`、`SSL_CERT_FILE`，以及平台设置的一切）、注入的 Entrypoint/Cmd（例如 `/opt/ga/guestagent`）、`User`、`WorkingDir` 和 label 都会被复制进 Template。用这个 captured Template 创建的 Sandbox 会去启动一个已不存在的 Guest Agent 路径，带着过期的占位符，继承过期的设置。**修法：** 调 commit API 时传一个显式的 `Config`，它基于*源 Template* 的镜像配置构建（以后若支持，再加上用户对镜像的改动），永远不要用容器的配置。
- **per-Sandbox CA（ADR 0006）：** 通过写 `/etc/ssl/certs` 或运行 `update-ca-certificates` 安装的 CA 会落在可写层，并被 capture。之后由这个 Template 创建的每个 Sandbox 都会信任另一个 Sandbox 的 CA。**修法：** 把 CA bundle 以只读 bind mount 注入，并让工具指向它（`SSL_CERT_FILE`、`REQUESTS_CA_BUNDLE`、`NODE_EXTRA_CA_CERTS`、`GIT_SSL_CAINFO`），或者用 bind mount 盖住系统 bundle 文件。无论哪种，原文件都留在下层不变。
- **Token：** 如果 per-Sandbox token 经环境变量或 rootfs 里的文件交给 Guest Agent，它就会被 capture。改为通过 bind mount 目录传递（读取后立即 unlink），或者作为 Host Agent 连接上的第一条消息。
- **Volume 和 tmpfs** 不会被 capture。要写进文档：Capture 不包含 Volume 的内容。
- **运行中的进程** 在 commit 期间被冻结。**gVisor 下**，Capture 需要 `--overlay2=none`（文件系统更慢），或者基于 `runsc tar rootfs-upper` 做一个 gVisor 专用的 Capture，再把 tar 导入成镜像层。

### Q7. 加固默认值

**来源怎么说**

- **默认 seccomp** "is an allowlist that denies access to system calls by default"，"disables around 44 system calls out of 300+"。被挡的包括 `mount`、`unshare` 和 `clone` 创建命名空间（"with the exception of `unshare --user`" / "except `CLONE_NEWUSER`"）、`keyctl`/`add_key`/`request_key`、`bpf`、`userfaultfd`、`perf_event_open`、`kexec_load`、`io_uring_*`（[seccomp](https://docs.docker.com/engine/security/seccomp/)）。
- **默认 capability（保留 14 个）：** AUDIT_WRITE、CHOWN、DAC_OVERRIDE、FOWNER、FSETID、KILL、MKNOD、NET_BIND_SERVICE、NET_RAW、SETFCAP、SETGID、SETPCAP、SETUID、SYS_CHROOT。其余都不给，包括 SYS_ADMIN、SYS_PTRACE、NET_ADMIN、BPF（[containers/run](https://docs.docker.com/engine/containers/run/)）。
- **`no-new-privileges`：** "Disable container processes from gaining new privileges"（[run](https://docs.docker.com/reference/cli/docker/container/run/)）。内核层面，setuid/setgid 位和文件 capability 在 `execve` 时失效，该标志会被继承且不可撤销（[no_new_privs](https://docs.kernel.org/userspace-api/no_new_privs.html)）。
- **`--pids-limit`** 对应 `pids.max`，到达上限后 `fork()`/`clone()` "will return -EAGAIN"（[cgroup-v2](https://docs.kernel.org/admin-guide/cgroup-v2.html)）。
- **User namespace：** userns-remap 在 `daemon.json` 里设置，与 `--pid=host`、`--network=host`、`--privileged`（不加 `--userns=host` 时）不兼容（[userns-remap](https://docs.docker.com/engine/security/userns-remap/)）；"The containerd image store is not available when using user namespace remapping"（[containerd store](https://docs.docker.com/engine/storage/containerd/)）。Docker Desktop 可以在 Docker Engine 标签页编辑 `daemon.json`（[settings](https://docs.docker.com/desktop/settings-and-maintenance/settings/)）。没有文档说 Docker Desktop 支持 userns-remap。
- **Enhanced Container Isolation** 需要登录和 Docker Business（[settings](https://docs.docker.com/desktop/settings-and-maintenance/settings/)）。Docker 用 Sysbox 实现它；开启后 "all containers leverage Linux user namespaces"，挂载 Docker socket 会被阻止，控制路径上的 syscall 会被审查，只读 bind mount 不能重新挂成读写（[ECI](https://docs.docker.com/enterprise/security/hardened-desktop/enhanced-container-isolation/)）。它与 Docker Desktop 的 host networking 不兼容（[host driver](https://docs.docker.com/engine/network/drivers/host/)），extension 容器不受它保护（[ECI limitations](https://docs.docker.com/enterprise/security/hardened-desktop/enhanced-container-isolation/limitations/)）。

**【推断】每一项对本项目的价值和坑：**

| 措施 | 挡住什么 | 对我们的坑 |
|---|---|---|
| 默认 seccomp | 内核攻击面：经 `unshare` 的新 mount/user-ns 花招、keyring（未命名空间化）、`bpf`、`io_uring`、`perf_event_open` | 也会挡掉 `userfaultfd`/`perf`，很少有 agent 需要。非特权的 `unshare --user` 仍被允许，所以 VM 内核是否允许非特权 user namespace 很关键（§5 E7） |
| `--cap-drop=ALL` | `NET_RAW`（在 bridge 上构造数据包 / ARP 欺骗）、`MKNOD`、`DAC_OVERRIDE`、`SETUID`…… | **它会弄坏 root Guest Agent。** Exec 时切换到 `user` 需要 SETUID/SETGID，以其他用户身份写文件需要 CHOWN/DAC_OVERRIDE/FOWNER，杀掉用户的 Process 需要 KILL。改用 `--cap-drop=ALL --cap-add=SETUID,SETGID,CHOWN,DAC_OVERRIDE,FOWNER,KILL` |
| `no-new-privileges` | 经 setuid 程序（`sudo`、`su`）和文件 capability 提权 | 依赖 `sudo` 的 Template 会失效，这正是目的。root Exec 走 Guest Agent，不走 `sudo` |
| `--pids-limit` | fork 炸弹 | 选一个给构建工具留余地的值（例如 512–4096） |
| Guest Agent 设为 non-dumpable（`prctl(PR_SET_DUMPABLE, 0)`） | 没有 `CAP_SYS_PTRACE` 的同 uid（root）进程经 `/proc/<pid>/mem` 读取 token | **否则 root Exec 能读 Guest Agent 的内存。** spec 允许 `user: root` |
| userns-remap | 容器里的 root 不是宿主机（VM）的 root | 在 Docker Desktop 上需要经典 image store；它是整个 VM 的 daemon 设置，不能按 Sandbox 配置。值得做一次实验，不宜作为默认 |
| ECI | 同 userns，另加 Docker socket 和 VM 配置保护 | 付费档，之后在 Linux 上无法复现。跳过 |

**背景：** 在 Docker Desktop 上，Linux VM 本身就是容器和 macOS 之间的一道边界。容器逃逸落在 VM 里，而不是 Mac 上，所以隔离测试应把"逃逸到 VM"和"逃逸到 Host"分开记录。

### Q8. 第 2 阶段的 gVisor

**来源怎么说**

- **平台和架构：** "gVisor supports x86_64 and ARM64, and requires Linux 5.6+"（[install](https://gvisor.dev/docs/user_guide/install/)、[FAQ](https://gvisor.dev/docs/user_guide/FAQ/)）。
- **平台选择：** systrap 是默认（2023 年年中起；runsc flag 默认值是 `"systrap"`，见 [flags.go](https://github.com/google/gvisor/blob/master/runsc/config/flags.go)）。systrap "is a better choice when running inside a VM, or on a machine without virtualization support"。KVM 在裸金属上提供 "the best performance"，而在嵌套虚拟化下 "the systrap platform will often provide better performance"。ptrace "is no longer supported and is expected to eventually be removed"（[platforms](https://gvisor.dev/docs/architecture_guide/platforms/)）。
- **Docker Desktop：** gVisor 文档没有提到 macOS 或 Docker Desktop（[install](https://gvisor.dev/docs/user_guide/install/)、[Docker quick start](https://gvisor.dev/docs/user_guide/quick_start/docker/)）。Docker 的 alternative runtimes 文档点名了 `io.containerd.runsc.v1`，但没提 Docker Desktop（[alternative runtimes](https://docs.docker.com/engine/daemon/alternative-runtimes/)），而且 shim 必须在 *VM 里* daemon 的 PATH 上。有一篇社区文章把 runsc 拷进了 Docker Desktop 的 VM（二手：[dev.to](https://dev.to/rimelek/using-gvisors-container-runtime-in-docker-desktop-374m)）。
- **OrbStack** release notes：v2.2.0（2026-06-04）"Improved gVisor (runsc) compatibility"；v2.2.2（2026-08-02）"Fixed file ownership in gVisor (runsc) containers"（[OrbStack release notes](https://docs.orbstack.dev/release-notes)）。OrbStack 的 Docker engine 配置可以用 `orb config docker` 编辑（[OrbStack Docker](https://docs.orbstack.dev/docker/)）。
- **Lima 嵌套虚拟化：** 需要 Apple M3+ 和 macOS 15（仅有二手来源：[DeepWiki 关于 Lima](https://deepwiki.com/lima-vm/lima/16.2-macos-specific-features)、[CNCF 博客关于 Lima 2.3](https://cncf.io/blog/2026/11/08/lima-v2-3-native-on-windows-vmnet-on-linux-and-menu-tui)）。我没能打开 Apple 的 `isNestedVirtualizationSupported` 页面。
- **安全模型：** gVisor 的目标是把 "System API" 攻击面降到最小。"No system call is passed through directly to the host"。Sentry 自己能用的宿主机 syscall 是逐一列明的，它不能打开文件或创建 socket（host networking 或 directfs 除外），文件访问经 Gofer 进行。不在防护范围内的：硬件侧信道（"does not provide protection against hardware side channels"）、资源耗尽（交给宿主机 cgroup）；并且 "A sandbox is not a substitute for a secure architecture"（[security](https://gvisor.dev/docs/architecture_guide/security/)）。
- **网络：** 默认 `--network=sandbox`，即 netstack 跑在 Sentry 里，把链路层数据包写到 Docker 建好的 netns 中的设备（[networking](https://gvisor.dev/docs/user_guide/networking/)）。`--net-raw` 默认为 false，即 raw socket 被禁用（[flags.go](https://github.com/google/gvisor/blob/master/runsc/config/flags.go)）。
- **与 agent 相关的兼容性缺口：**
  - 只实现了 syscall ABI 的一个子集；
  - `io_uring` 默认关闭；
  - sandbox 内的 cgroup 限制不生效；
  - `iptables` 只部分支持；
  - sandbox 里不支持 KVM（[compatibility](https://gvisor.dev/docs/user_guide/compatibility/)）；
  - arm64 上，`perf_event_open` 和 `userfaultfd` 未实现，`ptrace` 部分支持，`bpf` 只支持 CGROUP_DEVICE，`unshare` 不支持 time/cgroup 命名空间，`add_key`/`request_key` 未实现（[arm64 表](https://gvisor.dev/docs/user_guide/compatibility/linux/arm64/)）。
- **Docker in gVisor** 需要 runsc 的 `--net-raw`（Docker 28+ 还要 `--allow-packet-socket-write`）；Docker 29 还需要在 `/var/lib/docker` 挂 `tmpfs`，或用 `--feature containerd-snapshotter=false`。内层 `dockerd` 必须带 `--iptables=false --ip6tables=false` 运行，于是发布端口不可用，变通办法是在里面用 `--network=host`（[docker-in-gvisor](https://gvisor.dev/docs/tutorials/docker-in-gvisor/)）。
- **Checkpoint/restore** 存在（`docker checkpoint create`），只能恢复到同一个容器（[checkpoint/restore](https://gvisor.dev/docs/user_guide/checkpoint_restore/)）。以后可以作为 gVisor 的 Hibernated 状态，不在第 2 阶段范围内。

**【推断】第 2 阶段建议：** 第 2 阶段跑在 Lima（或 Colima）VM，或 OrbStack 的 Linux machine 里，用原版 Docker Engine，从 gVisor 的 apt 源安装 runsc。不要用 Docker Desktop。用 arm64 构建和默认的 systrap 平台。这也给了我们一台自己可控的 VM，可以做 xfs+pquota（Q4）和 iptables（Q3）实验。注册两个 runtime：
- `runsc`，带 `--host-uds=create`（Q1 的通道）；
- `runsc-capture`，带 `--overlay2=none`；或者用 `runsc tar rootfs-upper` 实现 gVisor 的 Capture。

Q3 的出口设计应该可以原样沿用，因为 netstack 的流量仍从 Docker 管理的 veth 出去（§5 E8 验证）。gVisor 下的 Docker-in-Sandbox 需要会削弱隔离的 flag（`--net-raw`），如果要提供，也应是 Template 显式选择开启的选项。

---

## 3. 说法核实

| # | 说法 | 结论 | 依据 |
|---|---|---|---|
| 1 | "`--storage-opt size` doesn't take effect on Docker Desktop for Mac because the VM uses ext4." | **结论大概率成立；理由不对或无法确认。** | size 选项需要 xfs+pquota 上的 overlay2（[run](https://docs.docker.com/reference/cli/docker/container/run/)、[overlay.go](https://github.com/moby/moby/blob/master/daemon/graphdriver/overlay2/overlay.go)）。Docker Desktop 默认用 containerd image store（[settings](https://docs.docker.com/desktop/settings-and-maintenance/settings/)），其 moby 代码路径不应用 `StorageOpt`（[image_snapshot.go](https://github.com/moby/moby/blob/master/daemon/containerd/image_snapshot.go)）。没有文档说 VM 用 ext4。用 §5 E4 验证 |
| 2 | "Inside a container on Docker Desktop, `host.docker.internal` resolves to the Mac host, so a Host Agent listening on a TCP port could be reached from a Sandbox." | 对绑定所有网卡的端口**成立**；对只绑 `127.0.0.1` 的**无法确认** | [how-tos](https://docs.docker.com/desktop/features/networking/networking-how-tos/)、[networking](https://docs.docker.com/desktop/features/networking/)。用 §5 E2 验证 |
| 3 | "`docker pause` freezes processes with the cgroup freezer; memory stays allocated." | **对 runc 成立**（cgroup v2 `cgroup.freeze`）。"内存保持分配"与内核文档一致但文档没明说；仍可能被换出。**对 gVisor 不成立**，它用 Sentry 的 `kernel.Pause()` | [pause](https://docs.docker.com/reference/cli/docker/container/pause/)、[fs2/freezer.go](https://github.com/opencontainers/cgroups/blob/main/fs2/freezer.go)、[cgroup-v2](https://docs.kernel.org/admin-guide/cgroup-v2.html)、[runsc controller.go](https://github.com/google/gvisor/blob/master/runsc/boot/controller.go) |
| 4 | "Capture can be implemented with `docker commit` on the Mac in phase 1." | **成立，但必须改。** 要传显式配置（否则容器的 Env 和 Entrypoint 会被固化），把 CA 和 token 放在 rootfs 之外，并注明不含 Volume。gVisor 默认 overlay 下失效 | [commit](https://docs.docker.com/reference/cli/docker/container/commit/)、[daemon/commit.go](https://github.com/moby/moby/blob/master/daemon/commit.go)、[gVisor filesystem](https://gvisor.dev/docs/user_guide/filesystem/) |
| 5 | "gVisor can run in a Linux VM on the Mac (OrbStack or Lima)." | **原理上成立。** 支持 ARM64，systrap 不需要虚拟化，OrbStack release notes 提到了 runsc 容器。gVisor 没有为 macOS 写文档，Docker Desktop 上也不支持 | [install](https://gvisor.dev/docs/user_guide/install/)、[platforms](https://gvisor.dev/docs/architecture_guide/platforms/)、[OrbStack notes](https://docs.orbstack.dev/release-notes)。用 §5 E8 验证 |

---

## 4. 对我们设计的影响（所有改动建议均为 **【推断】**）

| # | 发现 | 影响 | 建议改动 |
|---|---|---|---|
| 1 | NAT 网络让 Sandbox 有路由到 Mac、局域网和 VM；`--internal` + `isolated` 去掉了它 | spec §3 和 §7；ADR 0005；ticket 12、16、19 | spec §7："每个 Sandbox 只接入它自己的 `--internal` bridge 网络（gateway mode `isolated`）；Egress Proxy 和 DNS resolver 是唯一的其他成员。" ticket 12：增加验收"Sandbox 内 `ip route` 没有默认路由"和"bridge 网关地址上没有任何应答"。ADR 0005 里 "it has to be enforced by the host not listening" 那段，补上"并且不给 Sandbox 路由" |
| 2 | 在 Docker Desktop 上，Egress Proxy 和 resolver 必须是 VM 里的容器 | spec §3 图；GLOSSARY "Egress Proxy"（"The process on each Host"）；ADR 0005 | spec §3："在 Docker Desktop 上，Egress Proxy 和 DNS resolver 作为双网卡容器跑在 Docker VM 里。" 补一句 Host Agent 如何配置它们：通过 Host Agent 发起的连接，或只读 bind mount 的配置，永远不通过监听的管理 API |
| 3 | VirtioFS 上的 unix socket 从 Docker Desktop 4.86 起才可用；gVisor 需要 `--host-uds=create` | ADR 0002；ticket 01；spec §9 | ADR 0002 "Consequences"：最低 Docker Desktop 4.86，且用 VirtioFS。把 `docker exec -i … --stdio` 列为同一接口后面的后备传输（仍然是同一个 Guest Agent）。第 2 阶段：runsc 用 `--host-uds=create`，绝不用 `all`/`open`。ticket 01：增加验收"Docker Desktop < 4.86 或 gRPC FUSE 时，除非配置了 exec 传输，否则 Host Agent 拒绝启动" |
| 4 | `docker commit` 复制容器配置；写进 rootfs 的东西（CA、token）都会被 capture | ticket 22；ADR 0003 和 0006；ticket 23 | ticket 22：commit 时传基于源 Template 镜像配置的显式 `Config`。增加验收"captured Template 的 `Config.Env`/`Entrypoint` 与源 Template 相同"和"从 captured Template 创建的 Sandbox 不信任原 Sandbox 的 CA"。ticket 23 / ADR 0006：per-Sandbox CA 通过只读 bind mount 加 `SSL_CERT_FILE` 一类变量安装，不修改 rootfs 里的 trust store |
| 5 | `--cap-drop=ALL` 与 root Guest Agent 冲突；root Exec 能读 token | ticket 16；spec §7 | ticket 16："去掉所有 capability，再加回 SETUID、SETGID、CHOWN、DAC_OVERRIDE、FOWNER、KILL"，以及"Guest Agent 把自己设为 non-dumpable"。spec §7：写明 token 防的是普通用户，不防 root Exec |
| 6 | 磁盘限制：默认 Docker Desktop 静默忽略 storage-opt | spec §7 "Known gap"；ticket 19、03 | 改写这条缺口："默认的 containerd image store 忽略 `--storage-opt size`"。增加软限制（Host Agent 轮询 `SizeRw`，以 `disk_limit` 为原因终止），让"写满磁盘"测试有个结果。第 2 阶段 VM：用 xfs+pquota 上的 overlay2 做硬限制。注明 gVisor 默认 overlay 下 `SizeRw` 看不到写入 |
| 7 | Freeze 语义：keepalive 会断、时钟照走、gVisor 的 pause 不是 cgroup freezer | ticket 08；GLOSSARY "Frozen"；ticket 11 | GLOSSARY："在 Docker（runc）上就是 `docker pause`，即 cgroup freezer；gVisor 在自己的内核里暂停。" ticket 08：把计数器定义为每次循环加一的计数器；增加"冻结时长超过 keepalive 超时后，Host Agent 的通道能保持或续上"和"对 Frozen Sandbox 的 Exposed Port 请求快速失败"。另外记录内存吃紧时的解冻时间 |
| 8 | Mac 上 Control Plane ↔ Host Agent 若走 TCP，可能被网络配错的 Sandbox 访问到 | spec §7；ticket 16 | 第 1 阶段：macOS 上 Control Plane ↔ Host Agent 用 unix socket。这样"经 `host.docker.internal` 访问 Host Agent"这项隔离测试，同时成为网络和监听两方面的回归测试 |
| 9 | gVisor 的平台、运行位置和 Capture | spec §9 第 2 阶段；未来的 `docs/learn/02-gvisor.md` | spec §9："第 2 阶段在 Lima/Colima VM 或 OrbStack Linux machine 上运行（arm64、systrap），不用 Docker Desktop。" gVisor 上的 Capture：用 `--overlay2=none` 的 runtime，或 `runsc tar rootfs-upper`。除非 Template 显式开启，否则 Docker-in-Sandbox 不在范围内 |
| 10 | Docker Desktop 的 VM 是一道额外边界 | ticket 19、21 | 隔离结果表要区分"逃逸到 VM"和"逃逸到 Host"，免得 Docker 这一列在 Mac 上被高估、在 Linux 上被低估 |

---

## 5. 在 Mac 上要跑的实验

在你要开发所用的那个 Docker Desktop 版本上跑（`docker version`），升级后再跑一遍。命令假定 Mac 上已 `brew install socat`。

**E1. 跨 bind mount 的 unix socket（Q1）。** 在 Settings → General 里把文件共享先设为 VirtioFS 跑一遍，再设为 gRPC FUSE 跑一遍。
```sh
mkdir -p ~/sbx-e1 && chmod 700 ~/sbx-e1
# 我们需要的方向：Guest Agent（容器）监听，Mac 去连
docker run -d --name e1 -v ~/sbx-e1:/run/ga alpine sh -c \
  'apk add -q socat && socat UNIX-LISTEN:/run/ga/ga.sock,fork,mode=600 SYSTEM:"echo pong-from-container"'
sleep 5; ls -l ~/sbx-e1; socat - UNIX-CONNECT:$HOME/sbx-e1/ga.sock      # 预期：pong-from-container
# 生产中绝不能存在的方向：Mac 监听，容器去连
socat UNIX-LISTEN:$HOME/sbx-e1/host.sock,fork SYSTEM:'echo pong-from-mac' &
docker exec e1 socat - UNIX-CONNECT:/run/ga/host.sock                  # 记录 Sandbox 能访问到什么
# gRPC 层延迟：构建 cmd/guestagent 和 cmd/hostagent，经该 socket 计时 1000 次执行 `true` 的一元 Exec
docker rm -f e1; kill %1
```

**E2. 宿主机可达性（Q2）。**
```sh
python3 -m http.server 18001 --bind 127.0.0.1 &   # 只绑 loopback
python3 -m http.server 18002 --bind 0.0.0.0 &     # 绑所有网卡
probe='getent hosts host.docker.internal gateway.docker.internal; ip route;
  for p in 18001 18002; do wget -q -T3 -O- http://host.docker.internal:$p >/dev/null && echo "$p REACHABLE" || echo "$p blocked"; done;
  wget -q -T3 -O- http://1.1.1.1 >/dev/null && echo INTERNET || echo no-internet'
docker run --rm alpine sh -c "$probe"                                            # 默认 bridge
docker network create e2-nat && docker run --rm --network e2-nat alpine sh -c "$probe"
docker network create --internal e2-int && docker run --rm --network e2-int alpine sh -c "$probe"
docker network create --internal -o com.docker.network.bridge.gateway_mode_ipv4=isolated e2-iso \
  && docker run --rm --network e2-iso alpine sh -c "$probe; ip addr"
# VM 里有什么在监听（仅用于诊断；需要特权）
docker run --rm --privileged --pid=host alpine nsenter -t 1 -m -n -- sh -c 'netstat -lntu 2>/dev/null || ss -lntu'
# 在 internal（非 isolated）网络上扫描网关
docker run --rm --network e2-int alpine sh -c 'apk add -q nmap; nmap -Pn -p- $(ip route | awk "/src/{print \$1}" | sed "s#0/.*#1#")'
kill %1 %2
```

**E3. 出口设计（Q3）。**
```sh
docker network create --internal -o com.docker.network.bridge.gateway_mode_ipv4=isolated e3-sbx
docker network create e3-egress
docker run -d --name e3-proxy --network e3-egress ubuntu/squid && docker network connect e3-sbx e3-proxy
docker run -d --name e3-dns --network e3-egress alpine sh -c 'apk add -q dnsmasq && dnsmasq -k --no-resolv --server=1.1.1.1 --log-queries' \
  && docker network connect e3-sbx e3-dns
DNS_IP=$(docker inspect -f '{{(index .NetworkSettings.Networks "e3-sbx").IPAddress}}' e3-dns)
docker run --rm --network e3-sbx --dns $DNS_IP alpine sh -c '
  cat /etc/resolv.conf; nslookup example.com; nslookup example.com 8.8.8.8; ip route;
  wget -q -T3 -O- http://93.184.215.14 >/dev/null && echo DIRECT-IP-LEAK || echo direct-ip-blocked;
  https_proxy=http://e3-proxy:3128 wget -q -T5 -O- https://example.com >/dev/null && echo via-proxy-ok'
docker logs e3-dns | tail   # 确认内嵌 DNS 转发给了平台 resolver
```

**E4. 磁盘限制（Q4）。**
```sh
docker info --format 'driver={{.Driver}} status={{json .DriverStatus}}'
docker run --rm --storage-opt size=1G alpine sh -c 'df -h /; dd if=/dev/zero of=/big bs=1M count=2048 && echo WROTE-2G-LIMIT-IGNORED'
docker run --rm --privileged --pid=host alpine nsenter -t 1 -m -- sh -c 'mount | grep -E "/var/lib/(docker|containerd)"; df -T /var/lib/docker /var/lib/containerd 2>/dev/null'
# loop 镜像方案
docker run --rm --privileged --pid=host alpine nsenter -t 1 -m -- sh -c 'ls /dev/loop* | head -3; grep -w loop /proc/modules /proc/devices'
```

**E5. Freeze（Q5）。**
```sh
docker run -d --name e5 alpine sh -c 'i=0; while true; do i=$((i+1)); echo "$i $(date +%s)"; sleep 1; done'
sleep 5; CID=$(docker inspect -f '{{.Id}}' e5); docker pause e5
docker run --rm --privileged --pid=host alpine nsenter -t 1 -m -- sh -c "find /sys/fs/cgroup -path '*$CID*' -name cgroup.events -exec cat {} \;"
docker stats --no-stream e5; sleep 20; time docker unpause e5; sleep 3; docker logs e5 | tail -6   # 计数器接着走，时间戳跳变
# 冻结期间的 TCP
docker network create e5-net; docker run -d --name e5-srv --network e5-net alpine nc -lk -p 9000
docker run -d --name e5-cli --network e5-net alpine sh -c 'while true; do date; sleep 1; done | nc e5-srv 9000'
docker pause e5-srv; sleep 60; docker unpause e5-srv; docker ps -a --filter name=e5-cli   # 还连着吗？
# 内存压力：冻结一个占用 2 GB 的容器，再跑一个吃内存的程序，然后给 unpause 计时
```
在第 2 阶段的 VM 里用 `--runtime=runsc` 重跑 TCP 部分。

**E6. Capture（Q6）。**
```sh
mkdir -p ~/sbx-e6 && touch ~/sbx-e6/guestagent      # 代替注入的二进制
docker run -d --name e6 -e ANTHROPIC_API_KEY=sbx_secret_x -e HTTPS_PROXY=http://p:3128 \
  -v ~/sbx-e6:/opt/ga:ro --tmpfs /tmp --entrypoint /bin/sh alpine -c 'sleep 1d'
docker exec e6 sh -c 'echo hi > /root/f; echo t > /tmp/t; cp /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/extra.pem'
docker commit e6 e6:cap
docker inspect -f 'Env={{json .Config.Env}} EP={{json .Config.Entrypoint}} Cmd={{json .Config.Cmd}}' e6:cap   # 预期有泄漏
docker run --rm --entrypoint sh e6:cap -c 'ls -la /opt/ga /tmp; cat /root/f; ls /etc/ssl/certs/extra.pem'
docker image history e6:cap
# 经 API 传显式配置（Host Agent 应该这么做）
curl --unix-socket ~/.docker/run/docker.sock -X POST \
  "http://localhost/commit?container=e6&repo=e6&tag=clean" -H 'Content-Type: application/json' \
  -d "$(docker image inspect -f '{{json .Config}}' alpine)"
docker inspect -f '{{json .Config.Env}}' e6:clean
```

**E7. 加固（Q7）。**
```sh
docker info --format 'cgroup={{.CgroupVersion}} kernel={{.KernelVersion}} sec={{json .SecurityOptions}} runtimes={{json .Runtimes}}'
docker run --rm --cap-drop=ALL --security-opt no-new-privileges --pids-limit 64 --user 1000:1000 alpine \
  sh -c 'grep -E "CapEff|CapBnd|NoNewPrivs|Seccomp" /proc/self/status; for i in $(seq 100); do sleep 30 & done; echo spawned'
docker run --rm alpine sh -c 'cat /proc/sys/user/max_user_namespaces; apk add -q util-linux; unshare -U -r id'   # 非特权 userns？
docker run --rm --cap-drop=ALL --cap-add=SETUID,SETGID,CHOWN,DAC_OVERRIDE,FOWNER,KILL alpine \
  sh -c 'adduser -D u; su u -c id'   # 最小集合下 root 能降到 user
# userns-remap：Settings → General 关掉 containerd image store；Docker Engine 标签页加 "userns-remap": "default"；然后
docker info --format '{{json .SecurityOptions}}'; docker run --rm alpine sh -c 'cat /proc/self/uid_map'
```

**E8. Linux VM 里的 gVisor（Q8）。**
```sh
limactl start --name=gv template://docker          # 或：orb create ubuntu gv
limactl shell gv -- sh -c 'uname -m; ls -l /dev/kvm 2>&1'
# 在 VM 里按 https://gvisor.dev/docs/user_guide/install/ 从 gVisor apt 源装 runsc，然后：
sudo runsc install                                                   # 默认 runtime "runsc"
sudo runsc install --runtime runsc-uds -- --host-uds=create
sudo runsc install --runtime runsc-capture -- --overlay2=none
sudo systemctl restart docker
docker run --rm --runtime=runsc alpine dmesg | head -3                  # gVisor 启动横幅
docker run --rm --runtime=runsc -v /tmp/gv:/run/ga alpine sh -c 'apk add -q socat; timeout 3 socat UNIX-LISTEN:/run/ga/s - ; echo rc=$?'  # 预期失败
docker run -d --runtime=runsc-uds --name gvu -v /tmp/gv:/run/ga alpine sh -c 'apk add -q socat; socat UNIX-LISTEN:/run/ga/s,fork SYSTEM:"echo pong"'
sleep 5; sudo socat - UNIX-CONNECT:/tmp/gv/s
for rt in runsc runsc-capture; do docker run -d --runtime=$rt --name c-$rt alpine sleep 1d;
  docker exec c-$rt sh -c 'echo hi > /root/f'; docker commit c-$rt cap:$rt; docker run --rm cap:$rt cat /root/f; done   # runsc：预期文件缺失
docker run -d --runtime=runsc --name gp alpine sh -c 'i=0; while true; do i=$((i+1)); echo $i; sleep 1; done'; docker pause gp; sleep 10; docker unpause gp; docker logs gp | tail -3
# 用 --runtime=runsc 重跑 E2/E3 的探测，确认网络设计可以沿用
```

---

## 6. 来源（均于 2026-11-21 访问）

**Docker Desktop**
- Release notes（4.100.0 = 2026-11-19；4.86.0 VirtioFS 上的 AF_UNIX；4.93.0 socket 转发修复）https://docs.docker.com/desktop/release-notes/
- Networking https://docs.docker.com/desktop/features/networking/（另有 `.md`）；How-tos https://docs.docker.com/desktop/features/networking/networking-how-tos/
- Settings https://docs.docker.com/desktop/settings-and-maintenance/settings/ ；VMM https://docs.docker.com/desktop/features/vmm/
- Enhanced Container Isolation https://docs.docker.com/enterprise/security/hardened-desktop/enhanced-container-isolation/ ；limitations https://docs.docker.com/enterprise/security/hardened-desktop/enhanced-container-isolation/limitations/
- Air-gapped containers https://docs.docker.com/enterprise/security/hardened-desktop/air-gapped-containers/
- Docker 博客 "How Docker Desktop Networking Works Under the Hood"（D. Scott，2022-01-25）https://www.docker.com/blog/how-docker-desktop-networking-works-under-the-hood/ ；Docker Desktop 4.19 博客 https://www.docker.com/blog/docker-desktop-4-19/
- 旧版 osxfs 文档（存档，已过时）https://github.com/docker/docker.github.io/blob/a17a06c6a15028011250900c49a1b59620cf1c65/docker-for-mac/osxfs.md

**Docker Engine 文档**
- `docker run` https://docs.docker.com/reference/cli/docker/container/run/ ；`docker commit` https://docs.docker.com/reference/cli/docker/container/commit/ ；`docker pause` https://docs.docker.com/reference/cli/docker/container/pause/
- `docker network create` https://docs.docker.com/reference/cli/docker/network/create/ ；Networking https://docs.docker.com/engine/network/ ；Bridge driver https://docs.docker.com/engine/network/drivers/bridge/ ；Port publishing https://docs.docker.com/engine/network/port-publishing/ ；Host driver https://docs.docker.com/engine/network/drivers/host/
- Seccomp https://docs.docker.com/engine/security/seccomp/ ；Running containers（capabilities）https://docs.docker.com/engine/containers/run/ ；Security https://docs.docker.com/engine/security/ ；userns-remap https://docs.docker.com/engine/security/userns-remap/
- Storage drivers https://docs.docker.com/engine/storage/drivers/ ；overlayfs driver https://docs.docker.com/engine/storage/drivers/overlayfs-driver/ ；containerd image store https://docs.docker.com/engine/storage/containerd/ ；Alternative runtimes https://docs.docker.com/engine/daemon/alternative-runtimes/
- Engine release notes 26.0 https://docs.docker.com/engine/release-notes/26.0/ ；26.1 https://docs.docker.com/engine/release-notes/26.1/ ；28 https://docs.docker.com/engine/release-notes/28/

**源码（默认分支，2026-11-21 阅读）**
- moby `daemon/graphdriver/overlay2/overlay.go` https://github.com/moby/moby/blob/master/daemon/graphdriver/overlay2/overlay.go
- moby `daemon/containerd/image_snapshot.go`（最后 commit be32017，2026-02-21）https://github.com/moby/moby/blob/master/daemon/containerd/image_snapshot.go
- moby `daemon/commit.go`（最后 commit c7572b3，2026-09-25）https://github.com/moby/moby/blob/master/daemon/commit.go
- opencontainers/cgroups `fs2/freezer.go` https://github.com/opencontainers/cgroups/blob/main/fs2/freezer.go
- gVisor `runsc/config/flags.go` 和 `config.go`（最后 commit ecba2b3，2026-11-20）https://github.com/google/gvisor/blob/master/runsc/config/flags.go ，https://github.com/google/gvisor/blob/master/runsc/config/config.go
- gVisor `runsc/container/container.go`、`runsc/sandbox/sandbox.go`、`runsc/boot/controller.go` https://github.com/google/gvisor/tree/master/runsc
- gVisor issue #10256（overlay 下的 SizeRw）https://github.com/google/gvisor/issues/10256

**gVisor 文档**
- Install https://gvisor.dev/docs/user_guide/install/ ；Docker quick start https://gvisor.dev/docs/user_guide/quick_start/docker/ ；FAQ https://gvisor.dev/docs/user_guide/FAQ/
- Platforms https://gvisor.dev/docs/architecture_guide/platforms/ ；Security model https://gvisor.dev/docs/architecture_guide/security/
- Compatibility https://gvisor.dev/docs/user_guide/compatibility/ ；arm64 syscalls https://gvisor.dev/docs/user_guide/compatibility/linux/arm64/
- Docker in gVisor https://gvisor.dev/docs/tutorials/docker-in-gvisor/ ；Networking https://gvisor.dev/docs/user_guide/networking/ ；Filesystem https://gvisor.dev/docs/user_guide/filesystem/
- Rootfs overlay 博客（2023-05-08）https://gvisor.dev/blog/2023/05/08/rootfs-overlay/ ；Rootfs tar snapshots https://gvisor.dev/docs/user_guide/rootfs_snapshot/ ；Filesystem snapshots https://gvisor.dev/docs/user_guide/fs_snapshot/ ；Checkpoint/restore https://gvisor.dev/docs/user_guide/checkpoint_restore/

**Linux 内核**
- cgroup v2 https://docs.kernel.org/admin-guide/cgroup-v2.html ；cgroup v1 freezer https://www.kernel.org/doc/Documentation/cgroup-v1/freezer-subsystem.txt ；no_new_privs https://docs.kernel.org/userspace-api/no_new_privs.html

**其他 VM 工具**
- OrbStack release notes https://docs.orbstack.dev/release-notes ；OrbStack Docker https://docs.orbstack.dev/docker/
- Lima vz https://lima-vm.io/docs/config/vmtype/vz/

**二手来源（使用处已标注）**
- Colima #997 https://github.com/abiosoft/colima/issues/997 ；Docker 论坛（bind mount 上的 socket）https://forums.docker.com/t/unix-socket-on-bind-mount/142653 ；Docker 论坛（host.docker.internal）https://forums.docker.com/t/connection-refused-on-host-docker-internal/136925
- dev.to，在 Docker Desktop 里用 runsc https://dev.to/rimelek/using-gvisors-container-runtime-in-docker-desktop-374m
- DeepWiki 关于 Lima macOS 功能 https://deepwiki.com/lima-vm/lima/16.2-macos-specific-features ；CNCF 博客关于 Lima 2.3 https://cncf.io/blog/2026/11/08/lima-v2-3-native-on-windows-vmnet-on-linux-and-menu-tui
