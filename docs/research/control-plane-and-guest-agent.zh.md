# Control Plane、Host Agent 与 Guest Agent：开源 sandbox 平台如何组织这几部分

> [English](control-plane-and-guest-agent.md) | 中文

> 调研日期：2026-11-21。所有链接均在当天访问。
> 阅读的版本：
> - **E2B Runtime**（原 `e2b-dev/infra`；Go module 路径仍是 `github.com/e2b-dev/infra`）：`main` 分支 commit [`89e7715`](https://github.com/e2b-dev/runtime/tree/89e771512f7787d46d3402f7c238ec2b4bd650e4)（2026-11-21）。
> - **Daytona**：最后一个开源版本 tag [`v0.190.0`](https://github.com/daytonaio/daytona/tree/v0.190.0)（commit `01c502b`，2026-06-23），另加 Daytona v2 的 [architecture 页面](https://www.daytona.com/docs/architecture.md)。v2 本身闭源，所以 v2 的说法只依据这一页。
> - **kubernetes-sigs/agent-sandbox**：`main` 分支 commit [`1b7d533`](https://github.com/kubernetes-sigs/agent-sandbox/tree/1b7d5336539b9acd44ec431a61f4e83700a0a392)（2026-11-21；最新 tag v1.0.12）。
> - **SWE-ReX**：`main` 分支 commit [`5c995c3`](https://github.com/SWE-agent/SWE-ReX/tree/5c995c365dfb1fd5bc56fda688be5d8538f9931f)（最新 tag v1.4.0）。
>
> 只用一手来源：源代码，以及厂商自己的架构文档。这一轮没有研究 OpenHands 和 microsandbox（OpenHands 把 runtime 移出了主仓库，没时间去追）；与其凭记忆描述，不如不写。
> 标 **【推断】** 的是我的结论，不是来源的原话。术语遵循 [GLOSSARY](../../GLOSSARY.zh.md)；厂商自己的叫法用代码格式。面向用户的语义（定价、文档里写的超时、Secret 的用法）见 [agent-sandbox-usage.zh.md](agent-sandbox-usage.zh.md)，这里不重复。

---

## 1. 摘要

- **E2B 是 push；Daytona 已经转向 pull。** E2B 的 API 通过 gRPC 主动连到每个 orchestrator，并阻塞在 `Create` 上，而 `Create` 又阻塞在 envd 的 `/init` 上。对远程（BYOC）集群它照样 push，每个集群只用一个 edge 入口。Daytona 最后一个开源版本的 runner 已经默认 `API_VERSION=2`：它向 API long-poll 拉取 job（job 是 Postgres 里的行，用 Redis `LPUSH`/`BRPOP` 唤醒），再把结果报回去。Daytona v2 说 "Runners accept no inbound connections"，而且它的 create 在 sandbox 就绪之前就返回。【推断】对我们来说，Control Plane 和 Host Agent 之间的连接方向不是信任边界问题，因为两边都可信。phase 1 保持 push，但把 Host Agent 的监听放到 unix socket 上，让任何 Sandbox 都连不到它；等第一台 Host 位于 NAT 之后，再加一条由 Host 发起的反向隧道。
- **两家厂商的数据面流量都绕过控制面 API。** 路径是 SDK → edge proxy → 节点 → envd/toolbox。我们的 spec 里，每个 Exec 都经过 Control Plane。【推断】这对学习项目没问题，也更简单，但这样 Control Plane ↔ Host 这条通道要承载所有流式输出，所以第一天就要按流来设计。
- **envd 是一个 Connect RPC + REST 服务，进程 API 小而好。** 它提供 `Start`（服务端流，事件为 `start`/`data`/`end`/`keepalive`）、`Connect`（按 PID 或 tag 重新附着）、`List`、`SendInput`/`StreamInput`、`SendSignal`、`Update`（调整 PTY 尺寸），一个带 watcher 的文件系统服务，以及一个 REST `/init`，由 orchestrator 在启动后推送（环境变量、access token、CA bundle、CPU 数）。它由 systemd 启动，而 systemd 是 E2B 的模板构建器装进每个镜像的。它不是 PID 1。
- **envd 进程模型的缺口，我们不该照搬。** 没有客户端附着时产生的输出会被**丢弃**：没有缓冲也没有回放，只有一个 64 个事件的 channel。退出码只保留 30 秒。信号只发给直接子进程，不发给进程组。Daytona 的 daemon 这两点都做得更好：它把 session 命令的输出写进日志文件，可以重读、可以 follow；它用 `kill(-pgid)` 再加进程树遍历来杀进程。我们的 `output?offset=` 设计在这一点上领先 E2B。
- **状态。** E2B 把 Redis 当作*运行中* sandbox 的 source of truth，Postgres 存持久实体。每 20 秒一次的节点同步会杀掉"在节点上但不在 Redis 里"的 sandbox，有 **1 分钟宽限期**；读存储出错时整个节点跳过。Daytona 在 Postgres 里保存 `state`/`desiredState`/`pending`，每 10 秒调和一次。agent-sandbox 通过 Kubernetes API 用 etcd。【推断】单个 Control Plane 用 SQLite 是合理的。最先出问题的是高频写入（activity、心跳），Daytona 已经用 Redis 来缓冲这类写入。
- **计时器在控制面，不在 Host 上。** E2B 的 API 跑一个 evictor，每 50 ms 轮询 Redis 里到期的挂钟 `EndTime`。orchestrator 保存了结束时间，但不据此动作。Daytona 的 proxy 最多每 50 秒向 API 报告一次 activity，API 先缓冲在 Redis sorted set 里，每 10 秒写回 Postgres，auto-stop 的 cron 也是每 10 秒跑一次。两家的开源代码都不把后台进程算作活动。
- **计量：没有一家做 ADR 0004 那样的事。** E2B 的 API 向一个外部 collector 发 `InstanceStarted`/`InstanceStopped`，停止时间夹在 `min(now, EndTime)`。Daytona 在观察到状态变化时开、关 Postgres 里的 "usage period"，时间用的是 API 注意到那一刻的时钟。因此丢失的 runner 会一直计费到平台注意到为止。ADR 0004 比两家都严格；要实现它，需要每台 Host 一个 `last_report_at`，用 Control Plane 的时钟记录。
- **出站。** E2B 给每个 sandbox 一个独立 netns 和 nftables 规则，检查 SNI/Host 的 TCP 防火墙跑**在 orchestrator 进程内部**。它暴露一个插件式的 `EgressProxy` 接口，闭源实现负责解析 secret 标记。CA bundle 通过 envd `/init` 送进 guest。Daytona v0 用每个容器一条只按 CIDR 过滤的 iptables 链。E2B 还允许 sandbox 向 Host 侧服务发起连接（`hyperloop` 的 `/me` 和 `/logs`，以及一个 NFS proxy），这个例外比我们 ADR 0005 允许的更宽。

---

## 2. 逐问题的发现

### Q1. Control Plane ↔ Host 的方向

**E2B：从头到尾都是 push。**
- API 通过 5008 端口的 gRPC 调用每个 orchestrator 上的 `SandboxService.Create/Update/List/Delete/Pause/Checkpoint`（[ARCHITECTURE.md §Orchestrator](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L576-L593)、[orchestrator.proto](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/orchestrator/orchestrator.proto#L373-L380)）。
- 节点发现用 Kubernetes、DNS、静态列表或 Nomad（[ARCHITECTURE.md §Deployment topology](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L1505-L1510)）。
- 创建全程同步。文档原话是 "The API blocks on the gRPC `Create`, which itself blocks on envd's `/init` — when the client gets a response, the sandbox is fully usable"（[§Sandbox creation](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L879-L909)）。
- **远程集群也是 push。** API 用 edge token 连到集群的 *edge endpoint*。代码注释说 "A remote cluster's instances are all reached through the same edge endpoint, routed by the instance ID each call carries"（[remote_connections.go](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/clusters/remote_connections.go#L21-L30)、[cluster.go](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/clusters/cluster.go#L142-L170)）。所以 E2B 解决"节点在别人的云里"的办法是每个集群一个入站入口，而不是每个节点一个。
- 重连语义靠幂等，而不是持久化的 job。有可选的 `Idempotency-Key`，用 Redis 做预留；还有 `Should-Retry` 头，结果未知时为 `false`，因为那时 "a retry could create a second sandbox"（[§Opt-in request idempotency、§Sandbox creation retry hints](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L948-L1014)）。

**Daytona v0.190.0：代码里两种模式都有，默认是 pull。**
- runner 配置把 `API_VERSION` 设为 `default:"2"`（[config.go#L64](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/cmd/runner/config/config.go#L64)）。版本 2 下，runner 启动一个健康检查服务（默认每 30 秒）和一个 poller（[main.go#L245-L292](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/cmd/runner/main.go#L245-L292)）。
- API 一侧："Instead of making direct API calls to the runner, it creates jobs in the database that the v2 runner polls"（[runnerAdapter.v2.ts#L49](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/sandbox/runner-adapter/runnerAdapter.v2.ts#L49)）。job 先写进 Postgres，API 再向每个 runner 的 Redis 列表 `LPUSH`。`pollJobs` 在 `BRPOP` 上等待，超时 30 秒，Redis 出错时退回查数据库（[job.service.ts#L88-L230](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/sandbox/services/job.service.ts#L88-L230)）。
- **pull 模式下重连很干净。** poller 启动时先重新拉取自己 `IN_PROGRESS` 的 job 并重新执行（[poller.go#L53-L66](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/pkg/runner/v2/poller/poller.go#L53-L66)）。结果通过 `UpdateJobStatus` 报回（[executor.go#L211](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/pkg/runner/v2/executor/executor.go#L211)）。
- **即使在 v2 模式下，runner 仍在监听。** `api.NewApiServer` 无条件启动（[main.go#L294](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/cmd/runner/main.go#L294)），proxy 把 toolbox 和 preview 流量转发到 `runner.ProxyUrl/sandboxes/{id}/toolbox/...`（[get_sandbox_target.go#L99](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/proxy/pkg/proxy/get_sandbox_target.go#L99)）。在 v0 里，pull 只覆盖了控制路径。

**Daytona v2（闭源；依据 architecture 页面）。**
- NATS JetStream 承载 job，并把事件流回。runner "pull jobs only from its queue"，这 "lets a runner run behind a firewall"，并且 "Runners accept no inbound connections"（[architecture](https://www.daytona.com/docs/architecture.md)）。
- create 是异步的："Expect a sandbox to reach its new state after a create or delete returns"（同一页）。
- 页面没说 proxy 流量怎样到达一个不接受入站连接的 runner，只提到一个 "runner overlay network"。机制不明。

**kubernetes-sigs/agent-sandbox。** 它没有自己的 Host Agent。controller 通过 Kubernetes API 写 Pod，kubelet *watch* API server，所以天然是 pull（[README](https://github.com/kubernetes-sigs/agent-sandbox/blob/1b7d5336539b9acd44ec431a61f4e83700a0a392/README.md)）。

**权衡**

| | Push（E2B、我们的 ADR 0005） | Pull / 由 Host 发起（Daytona v2、Kubernetes） |
|---|---|---|
| NAT、远程云 | 每台 Host 需要一个可达的端点，或者像 E2B 的 edge 那样每个集群一个 | 在 NAT 和防火墙后也能工作；Host 只需要出站访问 |
| Host 上的攻击面 | 一个监听端口；在 Docker Desktop 上，除非仔细绑定，`host.docker.internal` 会让容器能连到它 | 控制流量没有监听（Daytona v0 仍为数据流量保留了一个） |
| 阻塞式 create 的延迟 | 一次 RPC，阻塞在上面很自然 | long poll 或流会多一跳唤醒。Daytona v2 干脆不阻塞了 |
| 重连与重试 | 调用方重试 + 幂等键；连接断开后结果可能未知 | 持久化的 job 行；runner 重启后重新认领 `IN_PROGRESS` 的工作，所以投递是至少一次，处理函数必须幂等 |
| 通过这条通道流式传 Exec 输出 | 用 gRPC 服务端流很自然 | job 轮询下很别扭；需要反向隧道或单独的数据路径 |

**与我们的设计对比**
- ADR 0005 用"只有更可信的一方发起连接"来论证 push。这条规则讲的是 **Sandbox** 边界。Control Plane 和 Host Agent 都在可信区域里（spec §7），所以这条规则决定不了它们之间的方向。【推断】
- 我们的 create 是阻塞的（spec §4），Exec 经过 Control Plane 流式传输（spec §3）。Daytona v0 风格的 job 轮询和这两点都不搭。【推断】
- **建议【推断】：现在不要翻转方向。**
  - phase 1 跑在一台 Mac 上。把 Host Agent 的 gRPC 放在 **unix socket** 上，而不是 TCP。这样 `host.docker.internal` 就连不到它，spec 自己列出的一个隔离攻击在结构上就被封住了，方向问题也随之消失。
  - 从 phase 3 起，当 Host 是 GCP VM 或 NAT 后面的 Hetzner 机器时，加一条**由 Host 发起的反向隧道**：Host Agent 连到 Control Plane，Control Plane 在这条连接上反过来使用同一个 gRPC 服务，比如用 yamux 一类的多路复用器。这样阻塞式 create 和流式 Exec 都不用改，Host 也不再有控制监听。
  - 只有当 create 改成异步时，才值得照搬持久化的 job 行。

### Q2. Guest Agent 设计

**envd（E2B）**
- **协议。** 它监听 49983 端口，用 chi + **Connect RPC**，外加 REST（[ARCHITECTURE.md §Envd](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L721-L777)）。Connect 在 HTTP/1.1 或 HTTP/2 上同时提供 gRPC、gRPC-Web 和 Connect 三种协议，所以 SDK 能经由一个普通的 HTTPS edge 调用它。
- **进程服务**（[process.proto](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/envd/spec/process/process.proto)）：
  - `Start` 返回一个 `ProcessEvent{start{pid} | data{stdout|stderr|pty} | end{exit_code, exited, status, error} | keepalive}` 的服务端流。
  - `Connect` 按 `ProcessSelector{pid | tag}` 重新附着到一个运行中的进程。
  - `List`、`SendInput`、`StreamInput`（客户端流，"ensures ordering"）、`CloseStdin`、`SendSignal`（只支持 SIGTERM/SIGKILL）、`Update`（调整 PTY 尺寸）。
- **文件系统服务**：`Stat`、`ListDir`、`MakeDir`、`Move`、`Remove`、`WatchDir`（流），以及轮询式 watcher（`CreateWatcher`、`GetWatcherEvents`、`RemoveWatcher`）（[filesystem.proto](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/envd/spec/filesystem/filesystem.proto)）。
- **REST 路由**：`/files` 上传与下载、`/health`、`/metrics`、`/envs`。控制路由 `/init`、`/upgrade` 和 freeze/thaw 钩子标记为 `x-internal`，edge proxy 对它们一律返回 404（[§Envd](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L732-L741)、[ingress.go#L52-L55](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/shared/pkg/proxy/ingress.go#L52-L55)）。
- **鉴权。**
  - 请求带 `X-Access-Token` 头。token 由 API 生成，orchestrator 把 token 的**哈希**写进 Firecracker MMDS，并在启动后通过 `/init` 推送 token 本身。`/init` 免鉴权，因为 token 正是由它送达的，"which is the main reason the proxy refuses it outright"（[§Envd](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L759-L761)、[§Orchestrator](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L631-L633)）。
  - orchestrator 经由 Host 网络在 sandbox 的 slot IP 上用 TCP 连 envd，不是 vsock（同一节）。
- **`/init` 内容。** 环境变量、access token、元数据、在线 CPU 数，以及要信任的 **CA bundle**。envd 把 bundle 写进 guest 的信任库，并提醒 "Applications that cached their trust store must reload it"（[§Envd, Guest trust](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L742-L758)）。
- **注入与启动。**
  - 模板构建器把 Docker 镜像转成 ext4，注入一些文件：hostname、DNS、`envd.service` 和一个预配脚本。然后用 **BusyBox init** 启动一台 Firecracker VM 跑预配脚本，该脚本 "will install systemd, that is later used for proper VM boot"（[builder.go#L113-L124](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/orchestrator/pkg/template/build/builder.go#L113-L124)）。
  - 它为不同发行版准备了 init 配置：systemd、Alpine 用的 OpenRC、NixOS（[distro/init.go](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/orchestrator/pkg/template/build/phases/base/distro/init.go#L11-L19)）。
  - envd 作为 systemd unit 运行，`User=root`、`Restart=always`，并用 `DefaultDependencies=no` 让它尽早启动（[envd.service.tpl](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/orchestrator/pkg/template/build/core/rootfs/files/envd.service.tpl)）。**envd 不是 PID 1。**
  - envd 可以原地热升级：用 `syscall.Exec` 保持同一个 PID，并带上它的进程表和文件描述符（[§Envd, Live upgrade](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L762-L770)）。

**Daytona daemon / toolbox（v0.190.0）**
- **注入。** runner 把 daemon 二进制**只读** bind mount 到 `/usr/local/bin/daytona`，把它设为容器的 `Entrypoint`，并把镜像原来的 entrypoint（或 cmd）作为参数传入（[container_configs.go#L143-L189](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/pkg/docker/container_configs.go#L143-L189)）。因此 daemon **以 PID 1 运行**，并装了一个 SIGCHLD reaper："The daemon runs as PID 1 inside containers, so orphaned processes … get reparented here"（[main.go#L225-L230](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/daemon/cmd/daemon/main.go#L225-L230)）。
- **协议。** 2280 端口上的普通 HTTP REST 加 WebSocket，由 gin 提供。API 面很宽：`/files/*`、`/process/execute`、`/process/session/*`、`/process/pty/*`（WebSocket）、`/process/interpreter/*`、`/git/*`、`/lsp/*`、`/computeruse/*`、`/port` 和 `/proxy`（[server.go#L166-L375](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/daemon/pkg/toolbox/server.go#L166-L375)）。
- **鉴权。** 我在 toolbox 路由上**没找到任何鉴权中间件**。`/init` 收到的 token 只用于遥测（[controller.go#L33](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/daemon/pkg/toolbox/controller.go#L33)、[telemetry.go#L46](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/daemon/pkg/toolbox/telemetry.go#L46)）。访问控制发生在 proxy 和 runner 上。【推断】容器里的任何进程都能驱动这个以 root 运行的 daemon。

**agent-sandbox 和 SWE-ReX：另外两种注入方式**
- agent-sandbox 没有平台 agent，由模板的镜像自带服务。示例 `python-runtime-sandbox` 是 8888 端口上的一个 FastAPI 应用，提供 `/execute`、`/upload` 和 `/download`（[main.py](https://github.com/kubernetes-sigs/agent-sandbox/blob/1b7d5336539b9acd44ec431a61f4e83700a0a392/examples/python-runtime-sandbox/main.py#L117-L230)、[python-sandbox-template.yaml](https://github.com/kubernetes-sigs/agent-sandbox/blob/1b7d5336539b9acd44ec431a61f4e83700a0a392/clients/python/agentic-sandbox-client/python-sandbox-template.yaml#L15-L18)）。
- SWE-ReX 在运行时注入 agent。
  - 容器命令是 `swerex-remote --auth-token <uuid>`。如果镜像里没有它，就退回 `pip install pipx && pipx run swerex-remote`，这要求镜像里有 Python 并且能联网（[docker.py#L117-L131](https://github.com/SWE-agent/SWE-ReX/blob/5c995c365dfb1fd5bc56fda688be5d8538f9931f/src/swerex/deployment/docker.py#L117-L131)）。
  - 每个容器一个新的 UUID，以 `X-API-Key` 头校验（[server.py#L35-L72](https://github.com/SWE-agent/SWE-ReX/blob/5c995c365dfb1fd5bc56fda688be5d8538f9931f/src/swerex/server.py#L35-L72)）。

**与我们的设计对比**
- **在 Docker 上，ADR 0003（由平台注入 agent）最接近 Daytona v0**：只读 bind mount 加覆盖 entrypoint。在 Firecracker 上，它*不是* E2B 的做法：E2B 会改写镜像，包括安装 systemd。【推断】要在 Firecracker 上也保住"官方镜像直接用"，我们的 Guest Agent 在那里也应当是 guest 的 **PID 1**：挂载 `/proc`、`/sys`、`/dev` 和 `/tmp`，回收子进程，再启动镜像的 entrypoint。这样镜像就不需要 init 系统。
- **ADR 0002（一个 agent，用 unix socket 或 vsock）** 与厂商的做法一致。E2B 在每个 sandbox 的网络上用 TCP，Daytona 在 Docker bridge 上用 TCP。我们选 unix socket 或 vsock，agent 根本不暴露在网络上，更严格。【推断】
- **值得照搬的【推断】：**
  1. 一个和 envd 同形的进程 API：`Start` 是一个 `start`/`data`/`end`/`keepalive` 的流；`Connect` 按 PID 或调用方给的 tag；`SendSignal`；`StreamInput`；PTY 调整尺寸。
  2. 一个 `/init` 式调用，由 Host Agent 在启动后**推送**。它应当携带每个 Sandbox 的 token、默认环境变量、ADR 0006 要的每 Sandbox 一个的 CA，以及时钟。这给 spec §7 的"token 保存在 root 进程的内存里"一条具体的送达路径。
  3. 长流上的 keepalive 事件。
  4. 内部路由与面向用户的路由分开，即使它们都走 unix socket。
- 可以考虑用 Connect-Go 代替纯 gRPC。【推断】它对 Host Agent 讲 gRPC，同时调试和写学习笔记时还能直接 curl。

### Q3. Process 与 Exec 模型

**E2B / envd**
- **只有一个原语。** 每条命令，无论前台还是后台，都是一次 `Start`。"后台"是 SDK 的行为：SDK 只是不等待。
- **客户端断开后进程继续运行。** 进程的 context 是 `context.Background()`，注释写着 "We do not want the command to be killed if the request context is cancelled"。唯一的上限是客户端的 `Connect-Timeout-Ms` 头（[start.go#L34-L44](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/envd/internal/services/process/start.go#L34-L44)、[start.go#L229-L241](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/envd/internal/services/process/start.go#L229-L241)）。
- **重新附着**用 `Connect(pid|tag)`（[connect.go](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/envd/internal/services/process/connect.go#L19-L60)）。
- **没有输出回放。**
  - 只有在 `if outMultiplex.HasSubscribers()` 时输出才发布给订阅者，否则字节直接丢掉（[handler.go#L296-L407](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/envd/internal/services/process/handler/handler.go#L296-L407)）。
  - 扇出 channel 缓冲 64 个事件（[handler.go#L32](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/envd/internal/services/process/handler/handler.go#L32)）。
  - 晚来的 `Connect` 只能看到它订阅之后产生的输出。
- **退出状态**保留 `terminatedRetentionTTL = 30 * time.Second`，所以 30 秒内重连的客户端仍能拿到退出码（[service.go#L21-L30](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/envd/internal/services/process/service.go#L21-L30)）。
- **杀进程。**
  - `sysSignal` 调用 `p.cmd.Process.Signal(signal)`，只到达直接子进程（[os_unix.go#L18-L21](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/envd/internal/services/process/handler/os_unix.go#L18-L21)）。这个子进程是一层 `/bin/sh -c '… exec cmd'` 包装，没有设 `Setpgid`（[spawn_unix.go#L20-L57](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/envd/internal/services/process/handler/spawn_unix.go#L20-L57)）。
  - 进程用 `CLONE_INTO_CGROUP` 放进按类型划分的 cgroup（user、PTY、system）（[handler.go#L440-L449](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/envd/internal/services/process/handler/handler.go#L440-L449)）。这些 cgroup 用于暂停时冻结，不用于杀进程。
- **PTY** 用 `creack/pty` 的 `StartWithSize`，支持调整尺寸（[pty_unix.go](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/envd/internal/services/process/handler/pty_unix.go)）。

**Daytona daemon**
- **三种模型**：
  - `/process/execute`：同步，带超时。
  - **Session**：每个 session 一个常驻 `bash`。每条命令有一个 `cmdId`，可以 `async` 也可以同步运行。
  - 走 WebSocket 的 **PTY session**（[server.go#L200-L241](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/daemon/pkg/toolbox/server.go#L200-L241)）。
- **输出持久化。** 每条 session 命令的输出写进 `<configDir>/sessions/<sid>/<cmdId>/output.log`，stdout 和 stderr 用行内前缀区分，退出码写进一个文件。日志接口返回整个文件，或用 WebSocket `follow=true`（[execute.go#L19-L80](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/daemon/pkg/session/execute.go#L19-L80)、[log.go](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/daemon/pkg/toolbox/process/session/log.go#L23-L45)）。因为输出是文件，晚来的读者也能看到全部。
- **进程组。** session 和 `/process/execute` 都设了 `Setpgid: true`（[create.go#L27](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/daemon/pkg/session/create.go#L27)）。终止时先 `kill(-pid, SIGTERM)`，再遍历进程树找出用 `setsid` 逃出去的后代，等一个宽限期，然后 `SIGKILL`（[delete.go#L44-L64](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/daemon/pkg/session/delete.go#L44-L64)）。

**与我们的设计对比**
- 我们的拆分（Exec 在前台、有超时；Process 在后台、用 `output?offset=`）比 E2B 的单一原语**更明确**。我们的 offset 回放补上了 E2B 最大的缺口。Daytona 基于文件的日志说明回放可以做得很简单。【推断】
- **建议的细节【推断】：**
  - **输出存储。** 在 Guest Agent 里每个 Process 一份只追加的日志。用 tmpfs 文件，或者在限定大小的前提下放内存，比如每个流 8 MiB。stdout 和 stderr 各自一个 offset，或者在一个流里加帧。
  - **截断。** 上限导致丢弃旧字节时，返回 `truncated_before`，让 `offset` 仍然有意义。
  - **退出状态。** 一直保留到 Process 被删除或 Sandbox 结束，而不是 30 秒。
  - **进程组。** 每个 Exec 和 Process 都带 `Setpgid` 启动。杀的时候先 `kill(-pgid, SIGTERM)`，等宽限期，再 `SIGKILL`。在 Firecracker 上给每个 Process 一个独立 cgroup，用 `cgroup.kill`，一个都漏不掉。
  - **断开连接。** 明确规定客户端断开时 Exec 怎么办。E2B 让它继续跑；既然我们有单独的 Process 原语，客户端断开就杀掉前台 Exec 更干净，也避免看不见的工作。不管选哪种，都写进 spec §4。
- ticket 06 要求一条 Guest → Host 的单一事件流，带序号。envd 没有这样的流：每个进程有自己的流，Host 用 `List` 轮询。【推断】带序号的事件日志是合理的，但有了每个 Process 的输出日志，它的大部分作用就不需要了。退出事件可以在重连时从 Process 表推导出来。

### Q4. 状态与调和

- **E2B**
  - **Postgres** 存持久状态：team、模板、构建、快照、volume、API key。
  - **Redis** 存 "running-sandbox store (source of truth)"，还有路由记录和缓存。
  - **ClickHouse** 存事件和指标（[§Data stores](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L843-L850)）。
  - 路由记录 `sandbox:routing:{id}` 由 **orchestrator** 在 `MarkRunning` 时写入、在 `MarkStopping` 时删除，并用 `execution_id`/`lifecycle_id` 守护，让过期的生命周期删不掉更新的记录（[§Sandbox routing records](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L1043-L1068)）。
- **E2B 的调和**
  - `keepInSync` 每 `cacheSyncTime = 20 * time.Second` 跑一次（[cache.go#L24](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/orchestrator/cache.go#L24)）。每次节点同步调用 `ServiceInfo` 和 `List`，再调用 `store.Reconcile`。代码注释写的是 "Redis is the source of truth — divergent sandboxes are orphans running on the node but not present in the store. Kill them."（[store.go#L390-L438](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/sandbox/store.go#L390-L438)）。
  - 比 `orphanGracePeriod = time.Minute` 还年轻的孤儿候选会被跳过。读存储出错时，同步会跳过 "the whole node to avoid mass kills"（[redis/main.go#L38](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/shared/pkg/sandboxstore/storage/redis/main.go#L38)、[#L274-L316](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/shared/pkg/sandboxstore/storage/redis/main.go#L274-L316)）。
  - 节点报告已结束的 sandbox 通过 `EndEndedSandbox` 收尾。
  - 节点重启靠 "startup reclaim" 处理，它会拆掉残留的 namespace 和临时目录（[§Orchestrator](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L672-L679)）。
- **Daytona v0**
  - Postgres 里每个 sandbox 有 `state`、`desiredState` 和 `pending` 标志（[sandbox.entity.ts#L33-L105](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/sandbox/entities/sandbox.entity.ts#L33-L105)）。Redis 存锁、activity 缓冲和 job 唤醒。
  - 一个 `sync-states` cron 每 10 秒按 runner 跑一次，持 Redis 锁，把 pending 的 sandbox 推向期望状态（[sandbox.manager.ts#L831-L888](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/sandbox/managers/sandbox.manager.ts#L831-L888)）。
  - runner 的 Docker monitor 会删除已不存在的容器的 iptables 链（[monitor.go#L250-L276](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/pkg/docker/monitor.go#L250-L276)）。
  - 我没找到销毁"数据库不认识的容器"的清扫逻辑。
- **Daytona v2**：Postgres 存实体，NATS JetStream 存 job 和事件（[architecture](https://www.daytona.com/docs/architecture.md)）。
- **agent-sandbox**：通过 Kubernetes API 用 etcd，标准的 controller 调和循环（[sandbox_controller.go](https://github.com/kubernetes-sigs/agent-sandbox/blob/1b7d5336539b9acd44ec431a61f4e83700a0a392/controllers/sandbox_controller.go)）。

**与我们的设计对比**
- 单个 Control Plane 用 SQLite 作 source of truth，和 E2B 的"一个存储是真相，节点向它对齐"是同一形状。对学习项目来说是合理的。【推断】
- **最先出问题的地方【推断】：**
  1. **高频更新造成的写放大。** activity 时间戳、每台 Host 每隔几秒的报告、Usage 事件，都落在 SQLite 唯一的写者上。Daytona 用 Redis 缓冲 activity、每 10 秒写回，就是出于同样的原因。activity 放在内存里，只持久化状态转换和周期快照，用 WAL 模式，所有写入经由一个 goroutine。
  2. **阻塞式 create 期间，跨 Host Agent RPC 持有事务。** 先写 `Starting` 行并提交，再调用 Host，然后提交状态转换。
  3. **单点故障。** Control Plane 宕机时，计时器和调和都停了。E2B 也有同样的性质。
- **该写进 ticket 10 的调和规则【推断】：**
  - 给未知 Sandbox 一个宽限期（E2B 用 1 分钟），以免杀掉正在创建的。
  - 读数据库失败时跳过这台 Host 的调和。
  - 调用 Host 之前先写数据库意图（`Starting`），这样"在机器上但不在数据库里"才真正意味着孤儿。

### Q5. 生命周期计时器

- **E2B：挂钟时间，由 API 执行。**
  - API 的 evictor 每 `pollInterval = 50 * time.Millisecond` 轮询一次 `store.ExpiredItems`，并以有限并发驱逐每个到期的 sandbox（杀掉，或自动暂停）（[evict.go#L24](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/orchestrator/evictor/evict.go#L24)、[#L138-L195](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/orchestrator/evictor/evict.go#L138-L195)）。
  - `KeepAliveFor`（即 SDK 的 `setTimeout`）修改 Redis 里的 `EndTime`。注释说 "The store is the expiry clock"（[keep_alive.go#L22-L52](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/orchestrator/keep_alive.go#L22-L52)）。
  - orchestrator 接收并保存结束时间（`SetEndAt`）。我没找到节点侧据此杀进程的计时器（[sandboxes.go#L749-L752](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/orchestrator/pkg/server/sandboxes.go#L749-L752)）。【推断】API 宕机时，sandbox 会活过它的超时。
  - 在开源代码里没找到 activity 跟踪。
- **Daytona v0：空闲，在 proxy 上观察。**
  - 每个经过 proxy 的请求，proxy 都向 API 报告 activity，只要连接还在就每 50 秒再报一次（[get_sandbox_target.go#L87-L95](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/proxy/pkg/proxy/get_sandbox_target.go#L87-L95)、[#L311-L335](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/proxy/pkg/proxy/get_sandbox_target.go#L311-L335)）。
  - API 按 sandbox 节流，`ZADD` 进一个 Redis sorted set，每 10 秒写回 Postgres（[sandbox-activity.service.ts#L25-L80](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/sandbox/services/sandbox-activity.service.ts#L25-L80)）。
  - `auto-stop-check` cron 每 10 秒跑一次，选出 `lastActivityAt < NOW() - autoStopInterval` 的 sandbox（[sandbox.manager.ts#L97-L130](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/sandbox/managers/sandbox.manager.ts#L97-L130)）。
  - sandbox 里的进程对这条路径是不可见的，这正好对应 v0 文档里的警告：长任务 "may auto-stop mid-process"。
- **agent-sandbox**：一个绝对的 `shutdownTime`，由 controller 执行；`shutdownPolicy` 是 `Retain` 或 `Delete`（[sandbox_types.go#L395-L416](https://github.com/kubernetes-sigs/agent-sandbox/blob/1b7d5336539b9acd44ec431a61f4e83700a0a392/api/v1beta1/sandbox_types.go#L395-L416)）。

**与我们的设计对比**
- 我们的空闲规则把运行中的 Process 和 Exposed Port 流量都算作活动（spec §5、ticket 07）。没有哪家厂商的开源代码实现了这一点。活动信号是分散的：API 调用和 Exec 由 Control Plane 看到，Process、流和端口流量由 Host Agent 和 Guest Agent 看到。
- **建议【推断】：**
  - Host Agent 为每个 Sandbox 维护 `last_activity_at` 和 `active_count`（运行中的 Exec、Process、打开的流、端口连接），两者都放进它的周期报告。
  - Control Plane 拥有截止时间，用一个像 E2B evictor 那样的评估循环，取 `max(cp_seen, host_reported)`。用 ticket 07 已经要求的假时钟来测。
  - 可选的兜底：Host Agent 在 N 分钟连不上 Control Plane 时，在本地执行*最大存活时间*，这样 Control Plane 宕机也不会让 Sandbox 永远跑下去。

### Q6. 计量与 Usage

- **E2B**
  - 在插入和移除时，API 调用一个外部 `AnalyticsCollector` gRPC 服务的 `InstanceStarted`/`InstanceStopped`，这个服务不在仓库里（[analytics.go#L43-L96](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/orchestrator/analytics.go#L43-L96)）。
  - 停止时间由 API 用 `sbxStopTime` 计算，它把时间夹到 `EndTime`，"to prevent stale record evicted late reporting incorrect data"（[analytics.go#L26-L41](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/orchestrator/analytics.go#L26-L41)）。
  - 每次运行带一个 `ExecutionId`。
  - 生命周期事件还写进 ClickHouse `sandbox_events`，它的 `id` 列是 `DEFAULT generateUUIDv4()`。ID 在插入时生成，所以没法给重试的事件去重（[migration](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/clickhouse/migrations/20251017213616_sandbox_events_id.sql#L5)）。
  - orchestrator 写入 cgroup 的 `sandbox_host_stats` 采样（[§Data stores](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L849)）。
- **Daytona v0**
  - 用 Postgres 的 `sandbox_usage_period` 行。观察到 `STATE_UPDATED` 事件时，关闭当前打开的 period 并用 `new Date()`（API 的时钟）开一个新的。`STARTED` 开一个 CPU、内存和磁盘的 period；`STOPPING`/`PAUSING` 开一个只计磁盘的 period；`ERROR`/`DESTROYED` 关闭 period（[usage.service.ts#L70-L156](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/usage/services/usage.service.ts#L70-L156)）。
  - 每个 sandbox 一把 Redis 锁，让这些更新串行。
  - 一个 cron 关闭并重开超过 24 小时的 period（[#L158-L228](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/usage/services/usage.service.ts#L158-L228)）。
  - runner 丢失时，period 在 API 把状态设为 `ERROR` 时结束，也就是在它注意到的时候。
- **Daytona v2**："runners report state changes as events and stream usage to billing"（[architecture](https://www.daytona.com/docs/architecture.md)）。没有更多细节。

**与我们的设计对比**
- ADR 0004 让区间在 Host 最后一次成功报告时结束。它**比两家都严格**：Daytona 计费到它注意到为止，E2B 计费到它注意到或到 `EndTime` 为止。没有厂商与它矛盾，但也没有人验证过它。
- **实现要点【推断】：**
  - 每台 Host 记录 `last_report_at`，用 **Control Plane 的接收时钟**，避开 Host 的时钟漂移。
  - 事件 ID **在源头**生成，即由 Host Agent 为每次状态转换生成，重试时复用同一个 ID。E2B 在插入时生成 UUID 就是反例。
  - 区间只从事件推导，再加上 Host 丢失时 ADR 0004 的截断。
  - Daytona 的 24 小时滚动是限制打开区间的一个便宜办法；反正我们在查询时按天切分（ticket 09）。

### Q7. 调度与超卖

- **E2B**
  - 用 **best-of-K**：随机抽 K=3 个就绪节点，每个节点的得分取 CPU 负载和 hugepage 池负载中较大的那个，选得分最低的（[placement_best_of_K.go#L12-L32](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/orchestrator/placement/placement_best_of_K.go#L12-L32)）。
  - CPU 得分是 `(requested + reserved + α·busy) / (R·cpus)`。默认 **R = 4**（CPU 超卖比）、α = 0.5。
  - 进行中的放置在本地计数（`PlacementMetrics.InProgress()`），避免并发 create 堆到同一个节点上。
  - 内存超卖检查 `placement-memory-max-overcommit` 默认为 **0，即关闭**（[flags.go#L715-L719](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/shared/pkg/featureflags/flags.go#L715-L719)）。内存密度来自 UFFD 的惰性分页和 hugepage 池的记账。
  - 以 `ResourceExhausted` 拒绝的节点仍然可选，放置逻辑带抖动退避（[§Placement](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L206-L238)）。
- **Daytona v0**
  - runner 上报指标：使用率百分比，以及已分配的 CPU、内存和磁盘。API 在七个指标上算一个 **TOPSIS** 得分（[runner.service.ts#L1095-L1160](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/sandbox/services/runner.service.ts#L1095-L1160)）。
  - 默认的"临界"分配是 CPU、内存和磁盘都为 **500%**，所以分配到 5 倍只是降低得分，而不是被拒绝（[configuration.ts#L194-L245](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/config/configuration.ts#L194-L245)）。
  - 放置时取得分高于阈值的前 10 个 runner，**随机选一个**（[runner.service.ts#L361](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/sandbox/services/runner.service.ts#L361)、[#L803-L820](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/sandbox/services/runner.service.ts#L803-L820)）。

**与我们的设计对比**
- 我们的 first-fit、内存不超卖、CPU 比例 1.0（spec §6）比两家都保守得多。为了干净的基准测试，这是对的。【推断】
- **照搬两点【推断】：**
  1. 调度器为**进行中的预留**计数。否则两个并发 create 可能都塞进最后一个空位。
  2. 让 **Host Agent 拒绝**它容纳不下的 create，返回 `ResourceExhausted` 这类有类型的错误。Control Plane 随后尝试下一台 Host，而不是相信一份过时的容量报告。

### Q8. Exposed Port 与入站

- **E2B**
  - 泛域名 DNS 把 `*.domain` 指向 **client-proxy**，一个无状态的 edge。它解析 `<port>-<sandboxId>.<domain>`，查 orchestrator 写的 Redis 记录 `sandbox:routing:{id}`，转发到节点上 `:5007` 的 orchestrator proxy，后者再连 `slotIP:port`（[§Client proxy](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L779-L789)、[§Sandbox traffic](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L1016-L1041)）。
  - 查不到（也就是 sandbox 已暂停）时，它调用 API 的 `ResumeSandbox`，并让并发请求共享同一个 "resume flight"。
  - **token 校验。** 头部是 `e2b-traffic-access-token`，用 `subtle.ConstantTimeCompare` 比较。envd 端口不做这项检查，因为 envd 有自己的鉴权。envd 的内部路径会被拒绝（[ingress.go#L14-L70](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/shared/pkg/proxy/ingress.go#L14-L70)）。
  - 对 IP 形式的 host 和 localhost 支持基于头部的路由（[host.go](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/shared/pkg/proxy/host.go)）。
- **Daytona v0**
  - proxy 解析 `<port>-<sandboxId | signedToken>.<proxy domain>`，toolbox 则解析 `/toolbox/<id>/…`。sandbox 不是 public 时就鉴权；toolbox、terminal 和 recording 端口始终鉴权（[get_sandbox_target.go#L24-L80](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/proxy/pkg/proxy/get_sandbox_target.go#L24-L80)）。
  - 它向 API 查 runner 的 `ProxyUrl` 并缓存 2 分钟，然后转发到 runner。每个经过 proxy 的请求同时也喂给空闲跟踪（Q5）。
- **agent-sandbox**
  - 它的 **sandbox-router** 按 `X-Sandbox-ID`、`X-Sandbox-Namespace` 和 `X-Sandbox-Port` 头路由，而不是按主机名。它通过 informer 缓存或每个 sandbox 的 Service DNS 名解析目标（[sandbox-router/README.md](https://github.com/kubernetes-sigs/agent-sandbox/blob/1b7d5336539b9acd44ec431a61f4e83700a0a392/sandbox-router/README.md)）。
  - 转发前去掉 `Authorization`，以免 "any sandbox" 冒充调用者。
  - 拒绝 `X-Sandbox-Pod-IP` 里的回环、链路本地等地址，作为 SSRF 防御。

**与我们的设计对比**
- ticket 11 把端口 proxy 放在 Host 上。这相当于 E2B 的 orchestrator proxy 那一层，前面没有 edge；只有一台 Host 时没问题。【推断】
- **照搬【推断】：**
  - 用常数时间比较 token。
  - 转发前去掉平台的鉴权头（agent-sandbox 的做法）。
  - 拒绝经端口 proxy 访问 Guest Agent 的路径。
  - 路由表由 Control Plane 推给 proxy，以 Sandbox 和生命周期为键，让过期条目路由不到被复用的 IP（E2B 的 `execution_id`/`lifecycle_id` 守护）。
  - 经 proxy 的连接在打开期间一直算作活动，就像 Daytona 每 50 秒重报那样。

### Q9. Egress Proxy 的位置

- **E2B**
  - 每个 sandbox 有一个网络 "slot"：一个 netns、一对 veth 和一个 tap、一个唯一的 Host IP、NAT，以及它自己的 nftables 出站防火墙（[§Orchestrator, Networking](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L675-L679)）。
  - 域名规则由 `tcpfirewall` 执行，它跑**在 orchestrator 进程内部**。每台 Host 一个 proxy，HTTP（Host 头）、TLS（SNI）和其他流量（只按 CIDR）各用一个端口（[tcpfirewall/proxy.go#L26-L60](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/orchestrator/pkg/tcpfirewall/proxy.go#L26-L60)）。
  - 一个可插拔的 `EgressProxy` 接口（`OnSlotCreate`、`CABundle(envdVersion)`、`HeadFilterRules`）是闭源版本的接缝（[egressproxy.go](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/orchestrator/pkg/sandbox/network/egressproxy.go)）。那个版本 "resolves a marker to a value at sandbox egress"（[§API, Secrets](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L243-L256)）。
  - 出站拦截需要的 CA bundle 由 envd `/init` 送进 guest（Q2）。
  - **Sandbox → Host 服务。** 每个 slot 的 `REDIRECT` 规则把 sandbox 发往一个固定 sandbox 内 IP 的流量送到 Host 侧服务。80 端口是 `hyperloop`，提供 `/me`（按源地址识别调用者）和 `/logs`；111 和 2049 端口是给 volume 用的 NFS proxy（[network.go#L339-L366](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/orchestrator/pkg/sandbox/network/network.go#L339-L366)、[hyperloopserver/handlers/me.go](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/orchestrator/pkg/hyperloopserver/handlers/me.go)）。
- **Daytona v0**
  - 每个容器在 `DOCKER-USER` 里插入一条按源 IP 匹配的 iptables 链。这条链是一个以 `DROP` 结尾的 **CIDR 白名单**，没有域名检查（[netrules/set.go](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/pkg/netrules/set.go#L8-L50)）。
  - v2 里由 runner 执行 Egress Policy，并在 Host 上阻断 sandbox 之间的流量。Secret 在 Postgres 里 "encrypted at rest with AES-256-GCM"，在 NATS 里 "sealed to each runner's key" 传输（[architecture](https://www.daytona.com/docs/architecture.md)）。

**与我们的设计对比**
- 每台 Host 一个独立的 Egress Proxy 进程（ADR 0005、ADR 0006）**比 E2B 更严格**：E2B 的 proxy 与 orchestrator 同进程，这个 proxy 一旦有 bug，orchestrator 进程就暴露了。【推断】
- E2B 每个 slot 的 nftables "重定向到 proxy 端口" 是 Firecracker 上该照搬的机制。在 Mac 上的 Docker 里，等价物是一个仅内部的 Docker 网络，它唯一的出口是 proxy 容器。【推断】
- **照搬 E2B 的 CA 送达方式【推断】。** Host Agent 在 Guest Agent 的 init 调用里交出每个 Sandbox 的 CA，Guest Agent 在启动镜像的 entrypoint **之前**安装它。这样就绕开了 E2B 的 "Applications that cached their trust store must reload it" 这个附加说明。
- **E2B 的 `hyperloop` 和 NFS 重定向正是 ADR 0005 所警告的反例。** Host 服务能被 sandbox 访问到，调用者靠源 IP 识别。保留我们更窄的例外。【推断】

---

## 3. 组件对照表

| 我们的 GLOSSARY 术语 | E2B | Daytona v0.190.0 | Daytona v2（文档） | agent-sandbox | SWE-ReX |
|---|---|---|---|---|---|
| **Control Plane** | `packages/api`（REST、放置、evictor、节点同步）+ Postgres/Redis/ClickHouse | `apps/api`（NestJS）+ Postgres + Redis | API + Postgres + NATS JetStream（+ 可选 Redis），每个 region 一套 | controller + Kubernetes API server/etcd | 客户端进程里的 deployment 类 |
| **Host** | sandbox 节点 | runner 机器 | runner | Kubernetes 节点 | Docker Host 或云服务（Modal、Fargate、Daytona） |
| **Host Agent** | `packages/orchestrator`（gRPC :5008，sandbox proxy :5007） | `apps/runner`（REST API + v2 的 poller/executor/健康检查） | runner（拉取 job） | kubelet + 容器运行时（不属于本项目） | 无 |
| **Guest Agent** | `envd`（:49983，Connect RPC + REST；systemd unit；不是 PID 1） | daemon/toolbox（`/usr/local/bin/daytona`，:2280 REST/WebSocket；PID 1） | daemon（Toolbox API + SSH） | 无；镜像自带的服务（示例：:8888 上的 FastAPI） | `swerex-remote`（FastAPI :8000，`X-API-Key`） |
| **Egress Proxy** | orchestrator 内的 `tcpfirewall` + 每个 slot 的 nftables；`EgressProxy` 插件（闭源版本解析 secret） | 每个容器一条 iptables 链（只按 CIDR） | runner 执行的 Egress Policy | NetworkPolicy（示例 `agent-sandbox-rl-network-policy`） | 无 |
| **端口 proxy / Exposed Port** | `client-proxy`（edge）→ orchestrator proxy；`e2b-traffic-access-token` | `apps/proxy` → runner 的 `ProxyUrl`；preview token 或签名 URL | 无状态 proxy，按主机名和路径路由 | `sandbox-router`（按头部路由） | 发布的容器端口 |
| **Usage** | API → 外部 AnalyticsCollector（开始/停止）；ClickHouse 事件和 Host 统计 | Postgres `sandbox_usage_period` | runner "stream usage to billing" | 无 | 无 |
| **调和** | API 每 20 秒节点同步；孤儿在 1 分钟宽限期后被杀 | 每 10 秒按期望状态跑 `sync-states` cron | 来自 runner 的事件 | controller 调和 | 无 |
| **Exec / Process** | 单一 `Start` 原语；`Connect` 重新附着；不回放 | `/process/execute` + 基于文件日志的 session + PTY | Toolbox API | `/execute`（示例） | `run_in_session`、`execute` |

---

## 4. 对我们设计的影响（所有建议都是 **【推断】**）

| # | 发现 | 影响 | 建议的改动 |
|---|---|---|---|
| 1 | ADR 0005 的"更可信的一方发起连接"决定不了 CP ↔ Host（两边都可信）。Daytona 为 NAT 和防火墙转向 pull；E2B 保持 push，配每个集群一个 edge | **ADR 0005**、spec §3、§7、ticket 01、ticket 16 | 修订 ADR 0005，把"由外向内"限定在 Sandbox 边界。phase 1 把 Host Agent 放在 **unix socket** 上（CP 和 Host 在同一台 Mac），让 `host.docker.internal` 连不到它。记下一个未来选项：给 NAT 后的 Host 用一条由 Host 发起、承载同一 gRPC 服务的反向隧道。create 仍是阻塞式时，不要采用 job 轮询 |
| 2 | envd 在没有订阅者时丢弃输出，退出码只留 30 秒；Daytona 把日志持久化 | spec §4（Process）、ticket 06 | Guest Agent 里每个 Process 一份只追加、有上限的输出日志，带 `truncated_before`。stdout 和 stderr 各自的 offset。退出状态保留到 DELETE 或 Sandbox 结束 |
| 3 | envd 只给直接子进程发信号；Daytona 杀进程组并遍历进程树 | ticket 06、ticket 04 | 每个 Exec 和 Process 都 `Setpgid`；对 `-pgid` 先 SIGTERM，宽限期后 SIGKILL；在 Firecracker 上每个 Process 一个 cgroup，用 `cgroup.kill` |
| 4 | E2B 靠安装 systemd 让镜像可启动；Daytona 让 agent 以 PID 1 运行 | **ADR 0003**、ticket 01、phase 3 | 在 ADR 0003 里写明 Guest Agent 以 **PID 1** 运行（回收子进程；在 Firecracker 上还负责挂载伪文件系统），并以子进程方式启动镜像的 entrypoint。这样任何 Template 都不需要 init 系统 |
| 5 | envd 的 `/init` 由 Host 推送：token、环境变量、CA bundle、CPU 数 | spec §7、ticket 16、ticket 23、ADR 0006 | 加一个从 Host Agent 到 Guest Agent 的 `Init` RPC，在 entrypoint 启动前调用一次。它携带每个 Sandbox 的 token、默认环境变量和每个 Sandbox 的 CA。Guest Agent 在任何用户进程启动前安装 CA |
| 6 | E2B 的调和：孤儿 1 分钟宽限期、读出错时跳过节点、orchestrator 守护的生命周期 ID | ticket 10、spec §5 | 调用 Host 前先写 `Starting` 行。忽略比宽限期年轻的未知容器。数据库出错时跳过这台 Host 的调和。容器同时打上 Sandbox ID **和**生命周期 ID 的标签 |
| 7 | 两家的计时器都在控制面；都不把 Process 算作活动 | ticket 07、spec §5 | Host Agent 为每个 Sandbox 报告 `last_activity_at` 和 `active_count`。CP 的评估器拥有截止时间（假时钟）。可选：CP 不可达时 Host 执行最大存活时间 |
| 8 | 没有厂商实现 ADR 0004；E2B 的事件 ID 在插入时生成 | **ADR 0004**（保留）、ticket 09 | 每台 Host 用 CP 的时钟记录 `last_report_at`。事件 ID 由 Host Agent 为每次转换生成，重试时复用 |
| 9 | E2B 为进行中的放置计数，节点以 `ResourceExhausted` 拒绝 | spec §6、ticket 03 | 调度器为进行中的 create 预留容量。Host Agent 自己做准入检查，返回有类型的拒绝，CP 据此在下一台 Host 上重试 |
| 10 | 端口 proxy：常数时间比较 token、去掉鉴权头、受生命周期守护的路由、拒绝 agent 路径 | ticket 11 | 四点都采用。打开着的经 proxy 连接算作活动 |
| 11 | E2B 的域名过滤与 orchestrator 同进程；它还允许 sandbox → Host 的 `hyperloop`/NFS | ADR 0005、ADR 0006、ticket 12 | Egress Proxy 保持独立进程（不变）。加一个隔离测试：从 Sandbox 只能连到 Egress Proxy 和 DNS 的端口，Volume 上线后也要测（E2B 需要 NFS） |
| 12 | 厂商让数据流量（exec、文件）避开控制面 API | spec §3 | phase 1 仍经过 CP，但把 CP ↔ Host 设计成能承载大量并发服务端流、带背压。在 spec 里注明：edge → Host 的直连路径是扩展时的选项 |
| 13 | SQLite 单写者 vs 高频的 activity 和报告写入 | spec §3 | WAL 模式；一个写 goroutine；activity 留在内存里，只在状态转换或快照时持久化；不跨 RPC 持有事务 |
| 14 | envd 用 Connect RPC（兼容 gRPC，可 curl） | ADR 0002 | 可选：用 connect-go 实现 Guest Agent，这样 Host Agent 讲 gRPC，人也能直接 curl |

**对现有 ADR 构成挑战的发现**
- **ADR 0005。** 它的理由（"只有更可信的一方发起连接"）被用在了 CP ↔ Host 上，而在那里并不成立。Daytona v2 出于运维原因刻意选择了相反的方向。ADR 的核心，也就是 Sandbox → Host，依然成立。在严格程度上，E2B 是一个反例：它允许 sandbox → Host 的 `hyperloop` 和 NFS 连接。
- **ADR 0003。** 在 Docker 上成立，Daytona v0 也是这么做的。在 Firecracker 上，E2B *不*在运行时注入，而是在模板构建时改写镜像，包括安装 systemd。要在那里保住 ADR 0003，Guest Agent 就必须是 PID 1（第 4 行）。
- **ADR 0004** 没有被反驳，但两家都没这么做。两家都计费到平台注意到为止。

---

## 5. 来源（均于 2026-11-21 访问）

**E2B Runtime @ `89e771512f7787d46d3402f7c238ec2b4bd650e4`**
- README：https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/README.md
- 架构文档：https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md
- orchestrator gRPC：https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/orchestrator/orchestrator.proto
- envd process proto：https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/envd/spec/process/process.proto
- envd filesystem proto：https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/envd/spec/filesystem/filesystem.proto
- envd Start / Connect / service / handler / spawn / os / pty：https://github.com/e2b-dev/runtime/tree/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/envd/internal/services/process
- envd systemd unit：https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/orchestrator/pkg/template/build/core/rootfs/files/envd.service.tpl
- 模板构建器：https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/orchestrator/pkg/template/build/builder.go
- init 配置：https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/orchestrator/pkg/template/build/phases/base/distro/init.go
- evictor：https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/orchestrator/evictor/evict.go
- keep-alive：https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/orchestrator/keep_alive.go
- 节点同步循环：https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/orchestrator/cache.go
- 节点同步：https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/orchestrator/nodemanager/sync.go
- store 调和：https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/sandbox/store.go
- Redis 存储：https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/shared/pkg/sandboxstore/storage/redis/main.go
- analytics：https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/orchestrator/analytics.go
- ClickHouse 事件 ID 迁移：https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/clickhouse/migrations/20251017213616_sandbox_events_id.sql
- 放置：https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/orchestrator/placement/placement_best_of_K.go
- feature flags：https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/shared/pkg/featureflags/flags.go
- 远程集群：https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/clusters/remote_connections.go、https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/clusters/cluster.go
- 入站：https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/shared/pkg/proxy/ingress.go
- 主机名路由：https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/shared/pkg/proxy/host.go
- slot 网络：https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/orchestrator/pkg/sandbox/network/network.go
- EgressProxy 接口：https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/orchestrator/pkg/sandbox/network/egressproxy.go
- TCP 防火墙：https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/orchestrator/pkg/tcpfirewall/proxy.go
- hyperloop：https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/orchestrator/pkg/hyperloopserver/handlers/me.go
- orchestrator 的 sandbox 服务：https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/orchestrator/pkg/server/sandboxes.go

**Daytona @ `v0.190.0`**
- runner main：https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/cmd/runner/main.go
- runner 配置：https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/cmd/runner/config/config.go
- poller：https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/pkg/runner/v2/poller/poller.go
- executor：https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/pkg/runner/v2/executor/executor.go
- runner adapter v2：https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/sandbox/runner-adapter/runnerAdapter.v2.ts
- job service：https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/sandbox/services/job.service.ts
- 容器配置：https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/pkg/docker/container_configs.go
- Docker monitor：https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/pkg/docker/monitor.go
- 网络规则：https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/pkg/netrules/set.go
- daemon main：https://github.com/daytonaio/daytona/blob/v0.190.0/apps/daemon/cmd/daemon/main.go
- toolbox server：https://github.com/daytonaio/daytona/blob/v0.190.0/apps/daemon/pkg/toolbox/server.go
- toolbox controller：https://github.com/daytonaio/daytona/blob/v0.190.0/apps/daemon/pkg/toolbox/controller.go
- toolbox 遥测：https://github.com/daytonaio/daytona/blob/v0.190.0/apps/daemon/pkg/toolbox/telemetry.go
- session execute：https://github.com/daytonaio/daytona/blob/v0.190.0/apps/daemon/pkg/session/execute.go
- session create：https://github.com/daytonaio/daytona/blob/v0.190.0/apps/daemon/pkg/session/create.go
- session delete：https://github.com/daytonaio/daytona/blob/v0.190.0/apps/daemon/pkg/session/delete.go
- session 日志：https://github.com/daytonaio/daytona/blob/v0.190.0/apps/daemon/pkg/toolbox/process/session/log.go
- proxy 目标解析：https://github.com/daytonaio/daytona/blob/v0.190.0/apps/proxy/pkg/proxy/get_sandbox_target.go
- activity：https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/sandbox/services/sandbox-activity.service.ts
- sandbox manager：https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/sandbox/managers/sandbox.manager.ts
- sandbox 实体：https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/sandbox/entities/sandbox.entity.ts
- runner service：https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/sandbox/services/runner.service.ts
- 配置：https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/config/configuration.ts
- Usage：https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/usage/services/usage.service.ts
- Daytona v2 architecture 页面（厂商文档；v2 闭源）：https://www.daytona.com/docs/architecture.md

**kubernetes-sigs/agent-sandbox @ `1b7d5336539b9acd44ec431a61f4e83700a0a392`**
- README：https://github.com/kubernetes-sigs/agent-sandbox/blob/1b7d5336539b9acd44ec431a61f4e83700a0a392/README.md
- Sandbox 类型：https://github.com/kubernetes-sigs/agent-sandbox/blob/1b7d5336539b9acd44ec431a61f4e83700a0a392/api/v1beta1/sandbox_types.go
- controller：https://github.com/kubernetes-sigs/agent-sandbox/blob/1b7d5336539b9acd44ec431a61f4e83700a0a392/controllers/sandbox_controller.go
- router：https://github.com/kubernetes-sigs/agent-sandbox/blob/1b7d5336539b9acd44ec431a61f4e83700a0a392/sandbox-router/README.md
- 示例 runtime：https://github.com/kubernetes-sigs/agent-sandbox/blob/1b7d5336539b9acd44ec431a61f4e83700a0a392/examples/python-runtime-sandbox/main.py
- 示例模板：https://github.com/kubernetes-sigs/agent-sandbox/blob/1b7d5336539b9acd44ec431a61f4e83700a0a392/clients/python/agentic-sandbox-client/python-sandbox-template.yaml

**SWE-ReX @ `5c995c365dfb1fd5bc56fda688be5d8538f9931f`**
- Docker deployment：https://github.com/SWE-agent/SWE-ReX/blob/5c995c365dfb1fd5bc56fda688be5d8538f9931f/src/swerex/deployment/docker.py
- server：https://github.com/SWE-agent/SWE-ReX/blob/5c995c365dfb1fd5bc56fda688be5d8538f9931f/src/swerex/server.py
