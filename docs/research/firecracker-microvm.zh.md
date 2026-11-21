# 第三阶段：Firecracker Runtime 与 microVM 技术

> [English](firecracker-microvm.md) | 中文

> 调研日期：2026-11-21。除非另有说明，所有链接均于当天访问。
> 只用一手来源：Firecracker 的文档与源码（github.com/firecracker-microvm）、云厂商文档、内核文档与源码、E2B runtime 的源码与文档，以及厂商的工程文章。标注 **【推断】** 的内容是我自己的结论，不是来源原话。
> 本文对应的版本：**Firecracker v1.18.0**（2026-11-09 发布，[releases](https://github.com/firecracker-microvm/firecracker/releases)）；**E2B runtime** 提交 `89e7715`（2026-11-21，[e2b-dev/runtime](https://github.com/e2b-dev/runtime)）。Firecracker 变化很快（2025–2026 年间 snapshot 格式变了三次），所以下文凡是与版本相关的说法，都只对这两个版本成立。
> 术语以 [GLOSSARY](../../GLOSSARY.zh.md) 为准。本文不重复 [agent-sandbox-usage.zh.md](agent-sandbox-usage.zh.md) 里已有的厂商对比。

---

## 1. 摘要

- **在哪跑：GCP n2 开嵌套虚拟化可以用；AWS 从 2026 年 2 月起在普通（非 metal）Intel 实例上也支持了。** GCP 在 Intel VM 上支持嵌套 KVM，AMD VM 中只有 N4D 支持。官方提醒 CPU 密集型负载"might experience a 10% or greater decrease in performance"（[GCP](https://docs.cloud.google.com/compute/docs/instances/nested-virtualization/overview)）。AWS 从 2026-02-16 起在 C8i/M8i/R8i 上支持嵌套虚拟化，2026-06-18 又加入了第 7 代 Intel 系列（[AWS user guide](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/amazon-ec2-nested-virtualization.md)）。Firecracker 自己的测试矩阵只有 `.metal` 主机（[README](https://github.com/firecracker-microvm/firecracker/blob/main/README.md)），我也没找到在嵌套环境下实测 Firecracker 启动或恢复耗时的一手来源。
- **`firecracker-go-sdk` 和 `firecracker-containerd` 都在 2026-11-10 归档了**（"this project is no longer maintained"，[go-sdk](https://github.com/firecracker-microvm/firecracker-go-sdk)、[containerd](https://github.com/firecracker-microvm/firecracker-containerd)）。E2B 是直接调用 Firecracker 的 HTTP API，客户端由 OpenAPI spec 生成。【推断】Host Agent 也应该这么做。
- **E2B 的设计可以概括为一句话："A sandbox is a resumed snapshot."** Template 是一个预先启动好的 VM snapshot。内存通过 `userfaultfd` 按需加载；rootfs 是一个只读 ext4 镜像，每个 sandbox 叠一层 CoW，经 NBD 提供。创建、恢复、fork 都走这同一条路径。Firecracker 不经 jailer 启动，跑在各自的 mount namespace、network namespace 和 cgroup 里（[ARCHITECTURE.md](https://github.com/e2b-dev/runtime/blob/main/docs/ARCHITECTURE.md)、[process.go](https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/sandbox/fc/process.go)）。
- **Snapshot 只能恢复到完全相同的环境上。** 也就是同一 CPU 型号、同样的 guest 可见 CPU 特性（Intel 到 AMD 永远不支持），实际上还要同一 host kernel。snapshot 格式在 v1.11、v1.12、v1.15 各变过一次，每次都要求重新生成 snapshot（[snapshot-support](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md)、[versioning](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/versioning.md)、[CHANGELOG](https://github.com/firecracker-microvm/firecracker/blob/main/CHANGELOG.md)）。【推断】所以 Hibernated 和 Fork 会变成调度约束；升级 Firecracker 时，也必须想好已有的 Hibernated Sandbox 怎么处理。
- **同一个 snapshot 起出来的每个克隆都需要新身份。** Firecracker 的文档列出了这些隐患：RNG 状态（Linux 5.18 起 VMGenID 能帮上忙，但对用户态 RNG "there is no generic solution"）、guest IP 和 TAP 名、墙上时钟、vsock 连接（会被重置）。AWS Lambda SnapStart 还补充了内存里缓存的 ID 和 secret。E2B 的做法是每次恢复后由宿主机推送一次 `/init`，为每个 fork 带上新的 access token，并重新同步时钟。【推断】我们的 Guest Agent 也需要同样的"恢复后重新确立身份"一步，它的每 Sandbox token 要变成每个化身（incarnation）一个。
- **截至 v1.18.0，没有任何 Firecracker 发布版支持设备直通。** PCI 传输在 v1.13 加入，virtio 设备的 PCI 热插拔在 v1.16 加入。VFIO 在 `feature/vfio` 分支上开发，跟踪 issue [#5679](https://github.com/firecracker-microvm/firecracker/issues/5679) 仍是 open 状态。"GPU 需要 Cloud Hypervisor 或 QEMU"今天成立，但有时效性。
- **已公布的数字：** 从 InstanceStart 到 `/sbin/init` ≤125 ms（1 vCPU、128 MiB、调优过的内核、`.metal`），VMM 内存开销 ≤5 MiB，VMM 启动 ≤8 CPU ms（[SPECIFICATION](https://github.com/firecracker-microvm/firecracker/blob/main/SPECIFICATION.md)）。Firecracker **没有**公布 snapshot 恢复延迟，文档只说恢复耗时取决于内存大小、vCPU 数和设备数。
- **对第三阶段的判断【推断】：** 现在的范围（rootfs 转换、vsock、Hibernate、Fork、自动休眠/自动恢复、SWE-bench）对一个阶段来说太大，应该拆开。先做：冷启动、OCI 转换、vsock、网络、jailer、SWE-bench。再做：Template snapshot 和同一 Host 上的 Hibernate。然后做：Fork。推迟：按流量自动休眠/恢复、UFFD、diff snapshot。

---

## 2. 逐题结论

### 2.1 在哪跑

**GCP。**
- 支持范围。嵌套虚拟化依赖 Intel VT-x 或 AMD-V。概览页列的是排除项而不是完整白名单：E2、除 M4/M4N 外的内存优化型、Arm VM，以及"AMD-powered VMs except N4D"。L1 hypervisor 只支持 Linux KVM，"Microsoft Hyper-V isn't supported"（[overview](https://docs.cloud.google.com/compute/docs/instances/nested-virtualization/overview)）。
- 性能。CPU 密集型负载下嵌套 VM "might experience a 10% or greater decrease in performance"，I/O 密集型可能更多（同一页）。
- 开启方法。用 `gcloud compute instances create … --enable-nested-virtualization`（或 `advancedMachineFeatures.enableNestedVirtualization`）。旧的 license key 方式要求最低 CPU 平台为 Intel Haswell（[enabling](https://docs.cloud.google.com/compute/docs/instances/nested-virtualization/enabling)）。
- Firecracker 自己的开发机指南在 GCE 上用嵌套虚拟化：`n1-standard-2` 加 `--min-cpu-platform="Intel Haswell"`，或者直接用 `n2-standard-2`，并说明"`E2` and `N2D` instances will not work"（[dev-machine-setup](https://github.com/firecracker-microvm/firecracker/blob/main/docs/dev-machine-setup.md)）。
- E2B 的 GCP 单机 Terraform 默认用 `n4-standard-4`，并设 `enable_nested_virtualization = true`。其中一条注释说 GCE 的 arm64 机型没有嵌套虚拟化，"metal included (the kernel reports the feature unavailable, 2026-09-14)"（[variables.tf](https://github.com/e2b-dev/runtime/blob/main/embed/terraform/gcp/variables.tf)、[main.tf](https://github.com/e2b-dev/runtime/blob/main/embed/terraform/gcp/main.tf)）。
- 未能确认：L1 VM 里有运行中的 L2 时，GCP 在宿主机维护期间做热迁移是否安全。内核文档说，带着运行中的 L2 迁移 L1 "works as of Linux kernel 5.3 and QEMU 4.2.0 for Intel x86"；而在 AMD 上，一旦 L2 启动，"the L1 guest should no longer be migrated or saved"（[内核文档](https://docs.kernel.org/virt/kvm/x86/running-nested-guests.html)）。【推断】这又是一个选 Intel（n2）而不选 N4D 的理由。

**AWS。**
- **2026-02-16** 起，"customers can create nested virtual machines by running KVM or Hyper-V on virtual EC2 instances"，覆盖所有商业区域的 C8i、M8i、R8i（[What's New，2026-02](https://aws.amazon.com/about-aws/whats-new/2026/02/amazon-ec2-nested-virtualization-on-virtual)）。
- **2026-06-18** 扩展到 C7i、R7i、M7i、I7i、`-flex` 与 `d` 变体、X8i，以及 GovCloud（[What's New，2026-06](https://aws.amazon.com/about-aws/whats-new/2026/06/nested-virtualization-intel-us-gov-cloud/)）。user guide 当前的列表里还有 R7iz 和 I7ie。
- user guide 里的细节：用 `--cpu-options "NestedVirtualization=enabled"` 开启；已有实例必须先停机；"no additional cost"。对性能或延迟敏感的用户，AWS 建议"evaluate bare metal instances"（[user guide](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/amazon-ec2-nested-virtualization.md)）。
- 列表里只有 Intel 系列，没有 Graviton。
- E2B 的 AWS Terraform 写着"was validated on m8i.xlarge"（[aws/variables.tf](https://github.com/e2b-dev/runtime/blob/main/embed/terraform/aws/variables.tf)）。
- Firecracker v1.18.0 还新增了"Nitro Sandboxes"（`PUT /enclave`），作为 EC2 上 KVM 之外的另一种后端（[CHANGELOG](https://github.com/firecracker-microvm/firecracker/blob/main/CHANGELOG.md)）。这种后端"no paused state"，因此没有 snapshot（[snapshot-support](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md)）。【推断】与我们无关。

**Firecracker 自己测什么。**
- README 的矩阵只有 `.metal` 实例：m5n、m6i、m7i、m8i、m6a、m7a，以及 Graviton 2–5。一条脚注说第 8 代 Intel 实例只在"using a 6.1 or 6.18 host kernel"时受支持。
- 没有提到嵌套虚拟化（[README](https://github.com/firecracker-microvm/firecracker/blob/main/README.md)）。
- 受支持的 host kernel 是 5.10、6.1、6.18（[kernel-policy](https://github.com/firecracker-microvm/firecracker/blob/main/docs/kernel-policy.md)）。【推断】云 VM 上发行版自带的内核未必在这个列表里；要用 `uname -r` 对照这张表，并把结果记下来。

**便宜的裸金属（Hetzner）。**
- Hetzner AX 矩阵，2026-11-21 访问，价格不含增值税，随时可能变化（[matrix-ax](https://www.hetzner.com/dedicated-rootserver/matrix-ax/)）：
  - AX41：Ryzen 5 3600，6 核，64 GB，2×512 GB NVMe，€59/月，无开通费，€0.0945/小时。
  - AX42：Ryzen 7 PRO 8700GE，8 核，64 GB DDR5 ECC，€79/月起，开通费 €49。
  - AX102：Ryzen 9 7950X3D，16 核，128 GB，€159/月起，开通费 €129。
  - 页面没有把每行配置明确对应到型号，上面的对应关系是按页面顺序推的。
- 【推断】独立服务器直接暴露 `/dev/kvm`，不经嵌套，所以能提供一个干净的"裸金属"校准点。代价是：
  - 这些是桌面级 CPU 且开着 SMT，而 Firecracker 建议"in production scenarios that require tenant separation"时关闭 SMT（[prod-host-setup](https://github.com/firecracker-microvm/firecracker/blob/main/docs/prod-host-setup.md)）；
  - CPU 型号和任何云主机都不同，snapshot 不能在两者之间迁移；
  - 需要手动开通和配置。

**嵌套对启动和恢复数字的影响。**
- **没有一手来源实测过 Firecracker 的这一点。** Firecracker 文档从不讨论嵌套下的性能。GCP 只给了 CPU 密集型负载"10% or greater"这个泛泛的数字；内核的嵌套 guest 文档只建议检查 `enable_shadow_vmcs` 和 `ept` 是否打开（[内核文档](https://docs.kernel.org/virt/kvm/x86/running-nested-guests.html)）。我找到的第三方博客数字属于二手来源，没有采用。
- 有一个相关机制是有文档的：snapshot 恢复后 KVM 要重建 guest 页表，而 Firecracker 说 2M 大页有帮助，因为它们"cut the KVM exits needed to rebuild page tables after snapshot restore"（[hugepages](https://github.com/firecracker-microvm/firecracker/blob/main/docs/hugepages.md)）。
- 【推断】在嵌套环境里，每一次这样的 exit、每一次按需加载内存时的缺页，都要先经过 L0 再由 L1 的 KVM 处理。所以按需恢复（基于文件或 UFFD）很可能是嵌套虚拟化伤害最大的地方，比 CPU 密集的 guest 代码受影响更大。第三阶段的对比表里，每个 Firecracker 数字旁边都应注明主机类型（嵌套的 n2 还是裸金属），最好再加一次裸金属运行作为校准。

### 2.2 Firecracker 的工具链

| 方案 | 状态（2026-11-21） | snapshot / vsock / 当前 API | 是否适合我们 |
|---|---|---|---|
| `firecracker-go-sdk` | **2026-11-10 归档**："As of November 10 2026, this project is no longer maintained … There are no alternative solutions"。最后一个 tag 是 v1.0.0（2022-09-07）（[repo](https://github.com/firecracker-microvm/firecracker-go-sdk)） | 其 swagger 客户端落后于 API；README 自己就说有些 Firecracker 功能"are not yet supported by the SDK" | **不要用** |
| `firecracker-containerd` | **2026-11-10 归档**；引导用户去用"more modern, secure alternatives"和 Nitro Sandboxes（[repo](https://github.com/firecracker-microvm/firecracker-containerd)） | 不适用 | 不要用 |
| 直接调 HTTP API | Firecracker 在 unix socket 上提供 REST API，有 OpenAPI 文件描述。端点包括 `/machine-config`、`/boot-source`、`/drives/{id}`、`/network-interfaces/{id}`、`/vsock`、`/entropy`、`/actions`、`/vm`、`/snapshot/create`、`/snapshot/load`、`/mmds`（[firecracker.yaml](https://github.com/firecracker-microvm/firecracker/blob/main/src/firecracker/swagger/firecracker.yaml)） | 全部可用，版本由你钉死 | **推荐【推断】**：大约十几个调用，而且能看清 Firecracker 实际在做什么 |
| flintlock | 活跃：2026-11-17 发布 v0.17.0。是一个 gRPC/HTTP 服务，用 OCI 镜像运行 Firecracker 或 Cloud Hypervisor microVM，能"talk to a guest agent in the microVM over a vsock device"（[repo](https://github.com/liquidmetal-dev/flintlock)） | 功能列表没有提到 snapshot | 【推断】它会取代我们 Host Agent 的 Runtime 层；对学习项目来说藏起来的东西太多，而且没有 Hibernate/Fork |
| Kata Containers + Firecracker | Kata 4.4.0（2026-11-17）钉住 Firecracker v1.17.0，需要 thin-pool 上的 containerd `devmapper` snapshotter，因为"a block device as the backing store for a VM is required"（[how-to](https://github.com/kata-containers/kata-containers/blob/main/docs/how-to/how-to-use-kata-containers-with-firecracker.md)、[versions.yaml](https://github.com/kata-containers/kata-containers/blob/main/versions.yaml)） | 【推断】Kata 跑自己的 guest 内 agent，并接管 VM 生命周期 | 与 ADR 0002（唯一 Guest Agent）冲突，还把 vsock 和 snapshot 藏起来了 |

**E2B 的做法。**
- E2B 在 `packages/shared/pkg/fc` 里维护了一份 OpenAPI 文件和一个 go-swagger 生成的客户端（[firecracker.yml](https://github.com/e2b-dev/runtime/blob/main/packages/shared/pkg/fc/firecracker.yml)）。
- 每个 VM 用 `unshare -m -- bash -c "… ip netns exec <ns> firecracker --api-sock … [--enable-pci]"` 启动（[script_builder.go](https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/sandbox/fc/script_builder.go)、[process.go](https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/sandbox/fc/process.go)）。
- Firecracker 二进制是从钉死的提交自行构建的（[fc-versions](https://github.com/e2b-dev/runtime/tree/main/firecracker/fc-versions)）。

### 2.3 OCI 镜像到 rootfs，以及内核

**Firecracker 的文档怎么说。**
- rootfs 是一个由文件支撑的块设备。官方做法：用 `dd` 建文件，`mkfs.ext4` 格式化，再从一个容器里填充内容。
- "The kernel runs `/sbin/init`… expects it to never exit"。
- 文件系统驱动必须编进内核（[rootfs-and-kernel-setup](https://github.com/firecracker-microvm/firecracker/blob/main/docs/rootfs-and-kernel-setup.md)）。
- 文档没有讲只读根文件系统或 overlay。
- 块设备的底层文件"must be pre-formatted with a filesystem the guest kernel supports"（[design](https://github.com/firecracker-microvm/firecracker/blob/main/docs/design.md)）。

**E2B。**
- Template 构建流水线：
  1. 用 `go-containerregistry` 拉镜像。
  2. 用 overlayfs 挂载各层，把合并后的目录树拷出来，再 `mkfs.ext4` 一个新镜像（带 `inline_data`、`^64bit` 等选项）。
  3. 按需 `resize2fs`/`e2fsck`（[oci.go](https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/template/build/core/oci/oci.go)、[ext4.go](https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/template/build/core/filesystem/ext4.go)）。
  4. 注入 `envd`、它的 systemd/OpenRC unit，以及一个**用作 init 的静态 BusyBox**（[rootfs.go](https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/template/build/core/rootfs/rootfs.go)、[fc-busybox](https://github.com/e2b-dev/runtime/tree/main/firecracker/fc-busybox)）。
  5. 每个构建阶段启动一次 VM："base → user → one layer per recipe step → resize disk → finalize → optimize"，每层是一个 pause-diff。
  6. optimize 阶段"records which memory pages a fresh resume touches"，供以后预取使用（[ARCHITECTURE](https://github.com/e2b-dev/runtime/blob/main/docs/ARCHITECTURE.md)）。
- 运行时，"the template rootfs stays read-only; writes go to a per-sandbox COW cache exposed to Firecracker as an NBD block device served by an in-process userspace NBD server"。pause 时，脏块被导出成 diff（同一文档）。

**devicemapper thin-pool。**
- Kata+Firecracker 用的就是它：每个容器镜像层是一个 thin snapshot，以块设备的形式交给 VM（[Kata how-to](https://github.com/kata-containers/kata-containers/blob/main/docs/how-to/how-to-use-kata-containers-with-firecracker.md)）。
- 【推断】thin snapshot 由内核做块级 CoW，不需要用户态 NBD 服务器；代价是要管理 thin-pool，而且有些配置下"must be initialized again after each reboot"（二手来源，未采信）。

**内核。**
- Firecracker 在 `resources/guest_configs` 里发布了 5.10、6.1、6.18 的已验证 guest 配置，文档说这些配置是给 Amazon Linux microVM 内核用的。x86_64 上推荐 `vmlinux`，`bzImage` 也能用但启动更慢（[kernel-policy](https://github.com/firecracker-microvm/firecracker/blob/main/docs/kernel-policy.md)、[rootfs-and-kernel-setup](https://github.com/firecracker-microvm/firecracker/blob/main/docs/rootfs-and-kernel-setup.md)）。
- guest kernel 的支持窗口：
  - 6.1：从 v1.9.0 起支持，最低支持截止日期 **2026-09-02**，这个日期已经过了。
  - 6.18：从 v1.16.1 起支持，至少到 2028-06-01。
- E2B 用 `amazonlinux/linux` 的 `microvm-kernel-*` tag、配上自己的配置构建 6.1.x 内核（[fc-kernels](https://github.com/e2b-dev/runtime/tree/main/firecracker/fc-kernels)）。它的 x86_64 6.1.190 配置打开了 `CONFIG_VMGENID=y`、`CONFIG_HW_RANDOM_VIRTIO=y`、`CONFIG_VIRTIO_VSOCKETS=y`，没有设置 `CONFIG_VSOCKETS_LOOPBACK`（[config](https://github.com/e2b-dev/runtime/blob/main/firecracker/fc-kernels/configs/x86_64/6.1.190.config)）。

**【推断】给我们的建议。**
- 用 Firecracker 的 `microvm-kernel-ci-x86_64-6.18.config` 构建 6.18 guest，并检查上面四个选项。
- 按**镜像 digest** 而不是 tag 缓存 ext4。
- 自己提供 init。大多数 OCI 镜像里没有 `/sbin/init`，所以 Guest Agent（或它前面的一个小 init）必须当 PID 1，或者由它拉起。

### 2.4 经 vsock 的 Guest Agent

除非另注，均出自 [vsock.md](https://github.com/firecracker-microvm/firecracker/blob/main/docs/vsock.md)。

**宿主机发起的连接。**
1. Host Agent 连接 VM 的 `uds_path`。
2. 写入 `CONNECT <port>\n`。
3. Firecracker 回 `OK <assigned_hostside_port>\n`。
4. 之后这个 socket 就是到 guest 内 `<port>` 监听者的字节流。如果 guest 内没人监听，Firecracker 会关闭宿主机这一端的连接。

【推断】在 Go 里，写一个 gRPC `WithContextDialer`，拨 unix socket 并完成这一行握手就够了。

**Guest 发起的连接。**
- guest 连接 CID 2 的端口 P，Firecracker 把连接转发到 `<uds_path>_P` 这个 AF_UNIX socket。
- 如果这个 socket 不存在或没人监听，"a VIRTIO_VSOCK_OP_RST packet will be sent back to the guest"。
- `[2^30, 2^31)` 范围的端口保留给宿主机发起连接时的宿主机端，guest 对这些端口的请求一律 RST。

**让宿主机不监听（ADR 0005）。**
- 只有宿主机上有东西在 `<uds_path>_P` 监听时，Firecracker 才会转发。所以规则很简单：宿主机上任何进程都不创建 `<uds_path>_*`。
- 用 jailer 时，`uds_path` 位于每个 VM 自己的 chroot 里（[jailer](https://github.com/firecracker-microvm/firecracker/blob/main/docs/jailer.md)）。【推断】这让其他宿主机进程误建监听的可能性很小。
- 【推断】隔离测试：在 guest 内对 CID 2 的一批端口（包括 Guest Agent 的端口）发起 `AF_VSOCK` 连接，期望每次都被 reset；在宿主机上断言不存在任何 `v.sock_*` 文件。

**guest 内的加固（来自内核源码，外加推断）。**
- Linux 不允许没有 `CAP_NET_BIND_SERVICE` 的进程绑定 ≤1023（`LAST_RESERVED_PORT`）的 vsock 端口（[af_vsock.c](https://github.com/torvalds/linux/blob/master/net/vmw_vsock/af_vsock.c)、[af_vsock.h](https://github.com/torvalds/linux/blob/master/include/net/af_vsock.h)）。【推断】让 Guest Agent 监听 1024 以下的端口，万一 agent 重启，普通用户也抢不走这个端口。
- guest 内的进程只能通过 loopback 传输访问本机 CID（`VMADDR_CID_LOCAL` = 1，[vm_sockets.h](https://github.com/torvalds/linux/blob/master/include/uapi/linux/vm_sockets.h)）。【推断】像 E2B 那样在 guest 内核里不开 `CONFIG_VSOCKETS_LOOPBACK`，Sandbox 内的代码就完全连不到 Guest Agent 的 vsock 监听；每 Sandbox token 就变成纵深防御，而不是唯一屏障。

**Snapshot。**
- 创建 snapshot 时 Firecracker 会发出 `VIRTIO_VSOCK_EVENT_TRANSPORT_RESET`。恢复后 guest 驱动会关闭已有连接，监听 socket 则保留（[snapshot-support](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md)）。
- v1.16 加了 `vsock_override`，可以在恢复时改 `uds_path`（[CHANGELOG](https://github.com/firecracker-microvm/firecracker/blob/main/CHANGELOG.md)）。
- 【推断】因此 ADR 0005 的"重连并从序号处续传"在每次 Hibernate、恢复和 Fork 时都会被用到，而不只是 Host Agent 重启时。

**E2B 的 agent 不走 vsock。** envd 监听 TCP 49983 端口，"over the host network at the sandbox slot IP"访问。它的控制路由之所以外部访问不到，只是因为边缘代理拒绝了它们。token 通过 `/init` 下发，MMDS 里只放 token 的哈希（[ARCHITECTURE](https://github.com/e2b-dev/runtime/blob/main/docs/ARCHITECTURE.md)）。【推断】我们的 vsock 设计更严格，因为控制通道在 guest 网络上根本不可达。

### 2.5 Snapshot、Hibernate 与 Fork

**全量与增量（diff）snapshot**（[snapshot-support](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md)）。
- 一个 snapshot 包括内存文件、VM 状态文件和磁盘；磁盘由用户自己管理。
- 全量 snapshot "always create a complete, resume-able snapshot"。snapshot 在 v1.13 进入 GA（[CHANGELOG](https://github.com/firecracker-microvm/firecracker/blob/main/CHANGELOG.md)）。
- diff snapshot 只包含被写脏的页，"generally not resumable"，必须用 `snapshot-editor edit-memory rebase` 合并，而且仍处于 developer preview。
- 脏页跟踪（`track_dirty_pages`）用 KVM 的 dirty log，有 CPU 开销，而且"negates most of the benefits of huge pages"。
- 不开脏页跟踪时的后备方案是 `mincore(2)`，它只在"if swap is disabled"时有效。

**内存后端。**
- **File**（默认）：内存以 `MAP_PRIVATE` 映射，按缺页加载；写入落到匿名 CoW 内存。恢复出来的 VM 整个生命周期里，内存文件都不能被改动。
- **Uffd**：Firecracker 通过 unix socket 把 userfaultfd 和内存布局发给用户自己写的 handler，由 handler 用 `UFFDIO_COPY` 处理缺页。handler 一旦挂掉，"Firecracker will hang when a page fault occurs"。balloon 回收会产生 `UFFD_EVENT_REMOVE` 事件，handler 必须处理（[缺页文档](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/handling-page-faults-on-snapshot-resume.md)）。
- 显式 2M 大页必须用 Uffd（[hugepages](https://github.com/firecracker-microvm/firecracker/blob/main/docs/hugepages.md)）。
- CodeSandbox 写过如何克隆运行中的 VM、并通过 UFFD 在 VM 之间共享内存（[CodeSandbox 博客](https://codesandbox.io/blog/cloning-microvms-using-userfaultfd)）。我抓取时该页返回 403，所以这里不引用它的数字。

**开销。**
- Firecracker 没有公布恢复耗时。创建和恢复的耗时"depend on memory size, vCPU count, and device count"。
- 创建时要同步写出全部内存。`sync_snapshot_files` 默认会 fsync：持久但更慢。
- cgroups v1 会导致"high restore latency"，强烈建议用 v2。
- 运维方"must"预留磁盘并设配额，以防 DoS（以上均出自 [snapshot-support](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md)）。
- 【推断】对我们来说，一次 Hibernate 的代价是把整个 guest 内存写一遍再加上 rootfs diff；一个 Hibernated Sandbox 占用的存储大约等于它的内存大小。"Hibernated 计存储"这条 Usage 计量的就是这部分。

**恢复的限制。**
- 恢复要求软硬件配置与原来"identical"。
- 唯一的跨内核例外是 `.metal` m5n/m6i/m6a 从 host kernel 5.10 恢复到 6.1，而且"is not recommended for production"。
- snapshot "are not compatible across CPU architectures and even across CPU models of the same architecture"，Intel 到 AMD 不支持（[versioning](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/versioning.md)）。
- TAP 名、磁盘路径、vsock UDS 等宿主机资源必须位于相同的相对路径，除非用 `network_overrides`、`vsock_override`、`drive_overrides` 覆盖。
- 加载时会检查 snapshot 格式版本。v1.11、v1.12、v1.15 都写明 snapshot "must be regenerated"（[CHANGELOG](https://github.com/firecracker-microvm/firecracker/blob/main/CHANGELOG.md)）。

**克隆隐患及修复。**

| 隐患 | 一手来源怎么说 | 修复 |
|---|---|---|
| RNG 状态相同 | Firecracker 实现了 VMGenID：恢复时它"writes a new identifier and injects a notification"。Linux ≥5.18（ACPI）据此给 CSPRNG 重新播种。vCPU 恢复到重新播种之间仍有一个竞态窗口；对用户态 RNG，"There is no generic solution under the current programming model"（[random-for-clones](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/random-for-clones.md)）。内核侧是 [vmgenid.c](https://github.com/torvalds/linux/blob/master/drivers/virt/vmgenid.c) 里的 `add_vmfork_randomness()`。Lambda SnapStart 恢复时会给内核 RNG 重新播种，但缓存了随机字节的库必须通过 runtime hook 重新播种（[SnapStart uniqueness](https://docs.aws.amazon.com/lambda/latest/dg/snapstart-uniqueness.html)） | guest 内核开 `CONFIG_VMGENID`；挂 virtio-rng（`/entropy`）；做 snapshot 前删掉 `/var/lib/systemd/random-seed`；恢复后执行 `RNDADDENTROPY` + `RNDRESEEDCRNG`（需要 `CAP_SYS_ADMIN`）；bind-mount 一个新的 `boot_id`。E2B 挂了 entropy 设备（[fc/client.go](https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/sandbox/fc/client.go)），内核里也开了 VMGENID |
| TAP 名、IP、MAC 相同 | Firecracker 用原来的 TAP 名恢复，所有克隆的 guest IP 都一样。修复：每个克隆一个 network namespace，里面用同名 TAP，接一根到宿主机的 veth，再做 NAT（出向 `MASQUERADE`，入向从唯一的"clone address" `DNAT`）。恢复后清空 guest 的邻居表。文档没有讨论 MAC，并自称不是"performant or secure"的方案，只是个起点（[network-for-clones](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/network-for-clones.md)） | E2B：每个 guest 都是 `169.254.0.21`，在各自的 netns 里接 tap `169.254.0.22`；tap 用固定 MAC，"to keep resumed guests' cached gateway ARP entries valid"；宿主机侧通过 SNAT/DNAT 拿到唯一的 `10.11.x.y`（[slot.go](https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/sandbox/network/slot.go)、[network.go](https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/sandbox/network/network.go)）。【推断】每个克隆各占一个二层网段时，guest MAC 相同无害 |
| TCP 与 vsock 连接状态 | "Network and vsock packet loss can be expected after resume"，连接状态不保证保留，vsock 连接会被重置（[snapshot-support](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md)） | 把每次恢复都当作一次重连（ADR 0005）。【推断】在文档里写明：Hibernate 和 Fork 会断开 Sandbox 的出站 TCP 连接（到 Egress Proxy 的连接） |
| 时钟相同 | guest 的墙上时钟从 snapshot 时刻继续走，"should be updated on the guest side"。`clock_realtime: true` 需要 host ≥5.16 和 kvm-clock。v1.15 加了 VMClock 设备，其 `vm_generation_counter` 在恢复时变化；poll() 支持进了 Linux 7.0（[snapshot-support](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md)、[CHANGELOG](https://github.com/firecracker-microvm/firecracker/blob/main/CHANGELOG.md)） | E2B 在原地 checkpoint 后重发 `/init`，"to re-sync the guest clock"，并带了 chrony 模板（[ARCHITECTURE](https://github.com/e2b-dev/runtime/blob/main/docs/ARCHITECTURE.md)） |
| 内存里的 secret 与 token 相同 | "Resuming the same state more than once is considered insecure"（[snapshot-support](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md)）。SnapStart："you must generate unique content after initialization. This includes unique IDs, unique secrets, and entropy"。AWS 的论文 *Restoring Uniqueness in MicroVM Snapshots* 为此提出了 `MADV_WIPEONSUSPEND` 和 SysGenID（[arXiv 2102.12892](https://arxiv.org/abs/2102.12892)） | E2B 会签发"for a secured sandbox, an envd access token scoped to the fork"，恢复后经 `/init` 推送（[ARCHITECTURE](https://github.com/e2b-dev/runtime/blob/main/docs/ARCHITECTURE.md)）。**我们的情况**见 §4 第 2 条 |

**E2B 如何实现 Hibernate（它叫"pause"）、恢复和 Fork**（[ARCHITECTURE](https://github.com/e2b-dev/runtime/blob/main/docs/ARCHITECTURE.md)）。
- **Pause。** orchestrator 暂停 VM、做 snapshot，对照 template 对内存（用脏页跟踪）和 rootfs（CoW 缓存）做 diff，先在本地缓存，再异步上传到对象存储。
  - 动手之前先探测 envd 是否健康。
  - 冻结工作负载 cgroup，避免抓到启动到一半的进程。
  - 仅文件系统的 snapshot 还要求先把文件系统静默（quiesce）。
- **恢复。** 恢复"the same path as creation"，调度优先选原节点：本地有缓存就不用读对象存储。
- **Fork。** E2B 先对原 sandbox 做原地 checkpoint（暂停再恢复），再把每个 fork "as a new sandbox rather than a resume"启动，分散到不同节点。每个 fork 有新的 ID、新的 execution ID 和新的 token。
- **自动暂停与自动恢复。** 一个回收扫描任务会把空闲 sandbox 自动 pause。流量打到已 pause 的 sandbox 时，边缘的 `client-proxy` 调用 API 的 `ResumeSandbox`，"one flight shared by concurrent requests"。
- **工作量。** 光是实现文档里 pause/resume 这一节就有约 260 行，涉及限流、各种拒绝路径、降级为仅文件系统、envd 在线升级。【推断】这是它们整个系统里最复杂的部分，对第三阶段的范围是个警示。

### 2.6 网络

**Firecracker 的指南**（[network-setup](https://github.com/firecracker-microvm/firecracker/blob/main/docs/network-setup.md)）。
- 每个 VM 一个 TAP，属主是运行 Firecracker 的用户，每个 VM 分一个 /30。
- 用 nftables 做 masquerade 加一条 forward 规则；`iptables-nft` 已"no longer recommended"。
- guest IP 用 ip 命令或内核参数 `ip=` 配置。显式设置 `guest_mac`，否则 guest 会随机生成一个。
- 克隆场景用 §2.5 讲的命名空间 NAT。

**宿主机必须做的事。**
- "Firecracker does not perform any network traffic filtering"（[design](https://github.com/firecracker-microvm/firecracker/blob/main/docs/design.md)）。
- 生产环境的宿主机应丢弃来自 TAP、发往 `169.254.169.254` 的包，并用限速器或 `tc` 防洪泛（[prod-host-setup](https://github.com/firecracker-microvm/firecracker/blob/main/docs/prod-host-setup.md)）。
- virtio-net 设备自带带宽和操作数限速器（[README](https://github.com/firecracker-microvm/firecracker/blob/main/README.md)）。

**E2B 的每 sandbox 网络**（[ARCHITECTURE](https://github.com/e2b-dev/runtime/blob/main/docs/ARCHITECTURE.md)、[network.go](https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/sandbox/network/network.go)）。
- 每个 sandbox 分到一个"slot"：一个 network namespace，里面有 TAP、一对通到宿主机的 veth、一个唯一的宿主机侧 IP、NAT，以及每 slot 一套 nftables 出向防火墙。
- 这套防火墙包含"SNI/Host-inspecting TCP firewall for domain allow/deny lists"。
- 宿主机上的服务（它们的 HTTP 代理、NFS portmapper、NFS 代理）通过 `iptables -t nat PREROUTING -i <veth> … -j REDIRECT --to-port …` 接入。
- slot 会池化复用。

**【推断】出向流量如何套进 ADR 0005/0006。**
- 在每个 Sandbox 的 netns 里，nftables 只放行到 Egress Proxy 端口的 TCP，以及到平台解析器的 UDP/TCP 53，其余全部丢弃。
- 在宿主机上，`PREROUTING -i <veth>` 把 TCP 80/443 重定向到 Egress Proxy（透明模式，这样它能按 spec 要求读取 SNI 和 Host），把 53 重定向到解析器；元数据地址、Host 自身地址和私有网段在路由前就丢弃。
- 因为每个 guest 在各自的 netns 里 IP 都一样，Fork 时 guest 内无需改任何网络配置。

### 2.7 磁盘与资源限制，以及 jailer

**磁盘。**
- rootfs 是由文件（或 NBD、thin 设备）支撑的块设备，guest 看到的就是这个大小。
- 【推断】guest 没法让设备变大，这正好补上了 spec 为 Docker Desktop 记录的"写满磁盘"缺口。
- 需要限制的反而是**宿主机侧**的 CoW 和 snapshot 存储。Firecracker 要求"provision enough disk and enforce quotas"（[snapshot-support](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md)）。
- 其他手段：jailer 的 `fsize`、`no-file` rlimit，cgroup 块 IO 限速，virtio-block 限速器（[prod-host-setup](https://github.com/firecracker-microvm/firecracker/blob/main/docs/prod-host-setup.md)）。

**CPU 与内存。**
- `/machine-config` 设置 vCPU 数和内存大小。Firecracker "sets no aggressive defaults"，真正起限制作用的是 Firecracker 进程上的 cgroup CPU 和内存控制（同一文档）。
- balloon 是"best effort"的：宿主机"should never rely solely on the statistics"它报上来的数据。不建议在 `vm.overcommit_memory=1` 下运行（[ballooning](https://github.com/firecracker-microvm/firecracker/blob/main/docs/ballooning.md)）。
- E2B 把 `fc_vcpu` 线程放进一个 threaded 子 cgroup，用 `cpu.max` 限制，VMM 自己的线程不受这个配额约束（[ARCHITECTURE](https://github.com/e2b-dev/runtime/blob/main/docs/ARCHITECTURE.md)）。

**jailer**（[jailer.md](https://github.com/firecracker-microvm/firecracker/blob/main/docs/jailer.md)），按步骤：
1. 校验路径，关闭继承来的文件描述符，清理环境变量。
2. 创建 `<chroot_base>/<exec>/<id>/root` 并把二进制拷进去，这样它"will not share memory with any other Firecracker process"。
3. 设置 rlimit（`no-file` 默认 2048）。
4. 创建 cgroup（v1 或 v2）并应用 `--cgroup` 的值。
5. `unshare` 一个 mount namespace，再 `pivot_root` 并 `chroot`。
6. 只 `mknod` 出 `/dev/kvm` 和 `/dev/net/tun`（6.1 上还有 `/dev/userfaultfd`，见[缺页文档](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/handling-page-faults-on-snapshot-resume.md)）。
7. 可选：`setns` 进指定的 netns、daemonize、新建 PID namespace。
8. 降到指定的 uid/gid，然后 exec Firecracker。

"The jailer treats all its inputs as trusted"，它本身以 root 运行。

**生产环境用不用它？**
- Firecracker 说生产环境应使用 jailer，"or equally restrictive process constraints"，最好每个实例一个独立的 uid/gid（[prod-host-setup](https://github.com/firecracker-microvm/firecracker/blob/main/docs/prod-host-setup.md)）。
- Kata 的安装步骤会同时下载 `jailer` 和 `firecracker`（[Kata how-to](https://github.com/kata-containers/kata-containers/blob/main/docs/how-to/how-to-use-kata-containers-with-firecracker.md)）。
- **E2B 不用。** 在我读到的代码里，orchestrator "as root"运行，通过 `unshare -m` 和 `ip netns exec` 启动 Firecracker，只设了 `Setsid` 和 cgroup FD；没有降 uid，也没有 chroot（[process.go](https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/sandbox/fc/process.go)）。
- 【推断】对本项目来说 jailer 值得用：它的每一步都能对应隔离课程里的一个攻击。

### 2.8 Firecracker 自己的安全模型

**威胁模型**（[design](https://github.com/firecracker-microvm/firecracker/blob/main/docs/design.md)）。
- "All vCPU threads are considered to be running malicious code as soon as they have been started"。
- 第一层隔离是 KVM 加上 Firecracker 的虚拟化边界。
- 为了纵深防御，"Firecracker should only run constrained at the process level"，即通过 jailer 施加 seccomp、cgroup、namespace，并降低权限。
- 网络是宿主机的责任。

**Seccomp**（[seccomp](https://github.com/firecracker-microvm/firecracker/blob/main/docs/seccomp.md)）。
- 过滤器在运行任何 guest 代码之前按线程安装：VMM 线程、API 线程、每个 vCPU 线程各一份，只允许"the bare minimum set of system calls and parameters"。
- 过滤器从 `resources/seccomp` 里的 JSON 编译，嵌进二进制。
- 从 v1.11 起用 `libseccomp` 编译（[CHANGELOG](https://github.com/firecracker-microvm/firecracker/blob/main/CHANGELOG.md)）。
- debug 构建没有过滤器；`--no-seccomp` 只用于原型开发。

**Firecracker 假定宿主机做到的事**（[prod-host-setup](https://github.com/firecracker-microvm/firecracker/blob/main/docs/prod-host-setup.md)）：
- 用 jailer 或等效手段运行它，加 cgroup 限制，每个租户一个 Firecracker 进程；
- 及时给 host、guest 内核和微码打补丁，因为"Firecracker is not able to mitigate host's hardware vulnerabilities"；
- 需要隔离租户的地方关闭 SMT；
- 关闭 KSM；
- 关闭 swap（防数据残留）；
- 用 ECC/TRR 内存（防 rowhammer）；
- 生产环境关闭 8250 串口，或限制其输出；
- 限制日志存储，因为 guest 能影响日志量；
- 跑一个看门进程，SIGKILL 卡死的 Firecracker 进程；
- 自己做出向过滤。

### 2.9 设备：PCI 与直通

- **v1.13**：通过 `--enable-pci` 为 virtio 设备提供可选的 PCI 传输。
- **v1.16**：block、pmem、net 设备的 PCI 热插拔与热拔出（developer preview）。
- **未发布**：PCIe 热插拔自动发现（[CHANGELOG](https://github.com/firecracker-microvm/firecracker/blob/main/CHANGELOG.md)）。
- 这种传输需要 guest 内核开 `CONFIG_VIRTIO_PCI`（[kernel-policy](https://github.com/firecracker-microvm/firecracker/blob/main/docs/kernel-policy.md)）。E2B 在配置开启时会传 `--enable-pci`（[script_builder.go](https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/sandbox/fc/script_builder.go)）。
- **VFIO 直通尚未发布。**
  - 跟踪 issue [#5679](https://github.com/firecracker-microvm/firecracker/issues/5679)（"VFIO device passthrough support"）仍是 open。
  - 2026-09 到 2026-11 合并的各"chapter" PR 合进的是 `feature/vfio` 分支，不是 `main`：#6183、#6219、#6241、#6252、#6316。#6356（热插拔）和 #6398（文档）仍是 open。
  - 更早的单个 PR [#5870](https://github.com/firecracker-microvm/firecracker/pull/5870) 未合并就关闭了。（状态于 2026-11-21 通过 GitHub API 核实。）
- GPU 细节留给 GPU 那份调研。

### 2.10 已公布的性能数字

| 指标 | 数字 | 条件 | 来源 |
|---|---|---|---|
| 启动（InstanceStart 到 `/sbin/init`） | ≤ 125 ms | m5d.metal（关超线程）或 m6g.metal，1 vCPU，128 MiB，Firecracker 调优过的 guest 内核，关闭串口控制台，最小 rootfs；每个 PR 都会测 | [SPECIFICATION](https://github.com/firecracker-microvm/firecracker/blob/main/SPECIFICATION.md) |
| VMM 启动（到 API socket 可用） | ≤ 8 CPU ms；墙钟 6–60 ms，通常约 12 ms | 同上硬件 | 同上 |
| VMM 内存开销 | ≤ 5 MiB | 1 vCPU、128 MiB guest，不含 MMDS；vsock 连接很多时可能超出 | 同上 |
| CPU / 网络 / 存储 | 达到裸金属的 95% 以上；14.5 Gbps 占 ≤80% 核（100% 核时 25 Gbps）；增加 0.06 ms 延迟；1 GiB/s 占 ≤70% 核 | 均标为"pending"（CI 尚未测试） | 同上 |
| 启动（2020 年论文） | 预配置的 Firecracker 约 100–125 ms，端到端约 150–175 ms（从图 5 读出）；50 个 VM 并行时 p99：预配置 Firecracker 146 ms，Cloud Hypervisor 158 ms；加一块网卡约多 20 ms | Firecracker v0.20.0，m5d.metal，guest 内核 4.14.94，1 vCPU / 256 MB，最小 init，无网络 | [NSDI '20 论文](https://www.usenix.org/conference/nsdi20/presentation/agache) |
| 内存开销（2020 年论文） | 约 3 MB（Firecracker）、约 13 MB（Cloud Hypervisor）、约 131 MB（QEMU） | 同上设置，用 `pmap` 测量 | 同上 |
| 大页 | 启动时间改善"up to 50%" | 2M hugetlbfs，Firecracker 启动测试 | [hugepages](https://github.com/firecracker-microvm/firecracker/blob/main/docs/hugepages.md) |
| Snapshot 恢复 | **Firecracker 未公布数字** | "depend[s] on memory size, vCPU count, and device count" | [snapshot-support](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md) |
| 恢复对比启动（定性） | 从零启动一个 DSQL query processor 要"a couple hundred milliseconds, probably"，snapshot 恢复则"orders of magnitude faster" | AWS 工程博客，无实测 | [Brooker，2025-09-18](https://brooker.co.za/blog/2025/09/18/firecracker) |

厂商的创建耗时宣传（E2B 的"~80ms"、Daytona 等）已在 [agent-sandbox-usage.zh.md](agent-sandbox-usage.zh.md) §2 中讨论。

---

## 3. 说法核实

| # | 说法（此前凭记忆提出） | 结论 | 来源与说明 |
|---|---|---|---|
| 1 | "Firecracker 需要 KVM；GCP 上可以靠 n2 的嵌套虚拟化拿到；AWS 上传统上需要 `.metal` 实例。" | **成立，但 AWS 部分已过时** | GCP 嵌套虚拟化在 Intel VM 上可用（包括 n2；Firecracker 自己的指南就写了 `n2-standard-2`）（[GCP](https://docs.cloud.google.com/compute/docs/instances/nested-virtualization/overview)、[dev-machine-setup](https://github.com/firecracker-microvm/firecracker/blob/main/docs/dev-machine-setup.md)）。AWS 从 2026-02-16 起在虚拟的 C8i/M8i/R8i 上支持嵌套虚拟化，2026-06-18 起扩展到第 7 代 Intel（[AWS](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/amazon-ec2-nested-virtualization.md)）。Firecracker 的测试矩阵仍然只有 `.metal`。Nitro Sandboxes（v1.18）是不走 KVM 的后端，没有 snapshot |
| 2 | "Firecracker 不支持 GPU 或设备直通，所以 GPU 需要 Cloud Hypervisor 或 QEMU。" | **对已发布版本（≤ v1.18.0）成立；有时效性** | VFIO 在 `feature/vfio` 分支上，跟踪 issue #5679 仍 open（[CHANGELOG](https://github.com/firecracker-microvm/firecracker/blob/main/CHANGELOG.md)、[#5679](https://github.com/firecracker-microvm/firecracker/issues/5679)）。PCI 传输（v1.13）和 virtio PCI 热插拔（v1.16）已发布。第四阶段前要再核实 |
| 3 | "Snapshot 恢复能让 VM 在远低于一秒内起来；厂商的冷启动数字来自 snapshot 恢复，而不是内核启动。" | **后半句成立；前半句合理，但没有一手数字** | E2B："'Creating' a sandbox means restoring one, not booting a kernel"（[README](https://github.com/e2b-dev/runtime)）。Firecracker 没有公布恢复延迟（[snapshot-support](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md)），Brooker 只说比启动"orders of magnitude faster"，没给数字。注意内核冷启动本身就 ≤125 ms（[SPECIFICATION](https://github.com/firecracker-microvm/firecracker/blob/main/SPECIFICATION.md)）。【推断】snapshot 真正省掉的是用户态启动（init、Guest Agent、应用），所以我们的基准测试应该对两条路径都计时到"Guest Agent 就绪" |
| 4 | "Snapshot 恢复后，网络配置和随机数状态需要特别处理。" | **成立，但不完整** | 还需要处理：墙上时钟、vsock 和 TCP 连接（会被重置）、内存中的 secret 与 token、`boot_id`，以及用户态 RNG 库，对后者"there is no generic solution"（[random-for-clones](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/random-for-clones.md)、[network-for-clones](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/network-for-clones.md)、[snapshot-support](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md)、[SnapStart](https://docs.aws.amazon.com/lambda/latest/dg/snapstart-uniqueness.html)） |
| 5 | "把 OCI 镜像转换成 rootfs，就能让所有 Runtime 共用同一个 Template（ADR 0003：平台在转换时把 Guest Agent 拷进 rootfs）。" | **成立，有附加条件** | E2B 正是这么做的：拉取 OCI，展平成 ext4，注入 envd、它的 unit 和一个静态 BusyBox init（[oci.go](https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/template/build/core/oci/oci.go)、[rootfs.go](https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/template/build/core/rootfs/rootfs.go)）。Kata 和 flintlock 也直接使用 OCI 镜像。【推断】附加条件：(a) 平台必须提供 init，因为 OCI 镜像很少自带；(b) 镜像配置（ENV、USER、WORKDIR）得由 Guest Agent 来落实，因为没有容器运行时替它应用；(c) 按 digest 缓存；(d) **Firecracker 上的 Capture** 产出的是 ext4 而不是 OCI 镜像，要保持 Template 是"一个 OCI 镜像引用"，就得把 ext4 的 diff 转回 OCI 层，或者放宽 GLOSSARY；(e) 预启动的 Template snapshot 是 Firecracker 专用的派生产物，不是第二个事实来源 |

---

## 4. 对我们设计的影响

本节全部为 **【推断】**，按重要性排序。

1. **重新划定并调整第三阶段的范围与顺序**（spec §9）。现在的列表基本上就是 E2B 的整个数据面。
   - **3a — 冷启动的 Firecracker Runtime：**
     - OCI 到 ext4 的转换，按 digest 缓存，注入 init 和 Guest Agent；
     - 6.18 guest 内核；
     - vsock 通道；
     - netns、tap、veth 网络，以及到 Egress Proxy 的重定向；
     - jailer、cgroup v2 限制；
     - 重跑性能和隔离测试；
     - **SWE-bench 子集**。它检验的是转换是否保真，不是 snapshot，所以放在这里；从 20–50 个实例起步，ext4 按镜像 digest 缓存。
   - **3b — Template snapshot，以及同一 Host 上的 Hibernate/恢复：** 全量 snapshot、File 内存后端、每个 Sandbox 一份 rootfs 副本（或 dm thin snapshot）。把"冷启动"和"从 Template 恢复"分开测量（spec §8）。
   - **3c — Fork：** 原地 checkpoint、N 次恢复，以及重新确立身份的协议（第 2 条）。
   - **推迟或砍掉：**
     - 空闲自动休眠和按流量自动恢复（第 5 条）；
     - UFFD 按需加载与预取（作为 3b 的加分项）；
     - diff snapshot（developer preview）；
     - 跨 Host 恢复。
2. **新增一个 ADR："每次恢复后都重新确立 Guest Agent 的身份"**，同时更新 spec §7 和 ticket 16。
   - 任何一次 Template 恢复、resume 或 Fork 之后，在 Sandbox 被视为 Running 之前，Host Agent 先打开 vsock 通道发一次重新初始化，内容包括：
     - 一个**新的每化身 token**；
     - 32–64 字节宿主机熵（`RNDADDENTROPY` + `RNDRESEEDCRNG`）；
     - 墙上时钟时间；
     - 新的 `boot_id`；
     - 清空邻居表；
     - 环境变量。
   - 在这一步完成之前，agent 只接受这一个调用，用 snapshot 时的 token 认证。这个 token 只有 Host Agent 知道，这一步之后不再被接受。
   - E2B 的 `/init` 做的就是这件事。它让"Fork 会复制 token"这个问题变得无害：
     - vsock 是每 VM 独立的，而且不开 `VSOCKETS_LOOPBACK` 时，guest 内的代码连不到 agent；
     - 子 Sandbox 即使从内存里读到父 Sandbox 的旧 token（用 root Exec 是可能的），也连不到父 Sandbox；
     - 重新初始化之后，旧 token 也不再被接受。
   - 增加隔离测试：
     - "Fork 子 Sandbox 的 agent 拒绝父 Sandbox 的 token"；
     - "两个 Fork 子 Sandbox 的 `/dev/urandom` 输出不同，Python 的 `random` 和 `uuid4` 也不同"。最后这一项对在 snapshot 之前就播种好的库预计会*失败*，应当像 ADR 0006 的响应脱敏那样，作为已知限制写进文档。
3. **Hibernate 和 Fork 会变成调度约束**（spec §6、GLOSSARY "Hibernated"、ADR 0001）。
   - Hibernated Sandbox 只能在 CPU 型号、host kernel、Firecracker 版本、snapshot 格式都相同的 Host 上恢复。Host 注册时应上报这个"snapshot 兼容键"，Control Plane 按它匹配。
   - 为 Firecracker 升级写一条策略，因为升级会让 snapshot 失效。要么升级前把 Hibernated Sandbox 清空，要么回退到从保存的磁盘冷启动，即 E2B 的 `memory: false` 救援，进程会丢失。
   - spec §5 要写明：恢复时进行中的 TCP 连接可能会断。
4. **不要用 `firecracker-go-sdk`**（spec §3 代码布局）。它已在 2026-11-10 归档。Host Agent 的 Firecracker Runtime 应该是 unix socket REST API 上的一个薄客户端，端点见 §2.2。钉死 Firecracker 版本，从对应版本的 `firecracker.yaml` 生成模型，或者手写那十几个请求类型。
5. **按流量自动恢复会跨越组件边界**（spec §5、ADR 0001、ticket 07 和 11）。
   - 发给 Hibernated Sandbox 的流量到达端口代理时，后面没有运行中的 Sandbox。恢复需要 Control Plane 参与，因为调度和 Quota 可能把它放到另一台 Host 上。E2B 是在边缘代理里通过 API 完成的。
   - 所以 spec §3 里"每 Host 一个端口代理"的设计覆盖不了这一点。要么推迟这个功能（推荐），要么增加一个能调用 Control Plane 的中心入口。
6. **每 Sandbox 的网络设计**（spec §7；后续 Runtime 适用 ticket 12；ticket 19）：
   - 每个 Sandbox 一个 netns；
   - 每个 netns 里 guest IP 和 tap MAC 都固定相同，这样 Fork 时 guest 内不用改配置；
   - 宿主机侧 veth IP 唯一；
   - netns 内 nftables 只放行 Egress Proxy 和解析器；
   - 宿主机侧把 80/443 和 53 REDIRECT 过去；
   - 硬性丢弃 `169.254.169.254`、Host 地址和私有网段。

   隔离测试新增：
   - "guest 用 `AF_VSOCK` 连接 CID 2 的任意端口都会被 reset"；
   - "Host 上不存在任何 `v.sock_*` 监听"。
7. **把 jailer 和 Firecracker 的宿主机清单当作第三阶段的隔离课程**（spec §8、`docs/learn/03-firecracker.md`）。
   - 每个攻击都配上挡住它的那一层：KVM，然后 seccomp，然后 jailer 的 chroot、uid 和 cgroup，然后 netns 和 nftables。
   - 如实说明 E2B 以 root 运行 Firecracker、不用 jailer。
   - 宿主机清单：cgroups v2；关闭 swap、KSM 和串口控制台；SMT 开或关作为一个变量记录下来。
8. **guest 内核用 6.18，不用 6.1**（spec §9）。Firecracker 对 6.1 guest 的最低支持日期（2026-09-02）已经过了。打开 `CONFIG_VMGENID`、`CONFIG_HW_RANDOM_VIRTIO`、`CONFIG_VIRTIO_VSOCKETS`，不开 `CONFIG_VSOCKETS_LOOPBACK`，并挂上 `/entropy` 设备。
9. **在哪跑**（spec §9）。用 GCP n2 加嵌套虚拟化来学习没问题，保持 Intel，不用 N4D。
   - 每个 Firecracker 数字都标注"nested"。
   - 增加一次可选的裸金属校准运行（Hetzner AX 按小时，或 AWS `.metal`）。
   - AWS c8i/m8i 的嵌套虚拟化现在是同等的备选。
   - 对照 Firecracker 支持列表（5.10、6.1 或 6.18）检查 host kernel 并记录。
10. **Usage 与 Quota**（spec §6、ticket 09）。
    - Hibernated 的存储约为内存大小加磁盘 diff，两者都要计量。
    - Fork 的原地 checkpoint 会让父 Sandbox 短暂暂停，要决定这段时间算 Frozen 区间还是仍算 Running。
    - 磁盘配额必须覆盖**宿主机侧**的 CoW 和 snapshot 存储，这是 Firecracker 的要求。
11. **Firecracker 上的 Capture**（ticket 22、GLOSSARY "Template"）。在 3a 之前决定：Capture 产出 OCI 层（ext4 diff 转 tar 再推镜像），从而保住"Template = OCI 引用"；还是产出平台内部的 ext4 Template。不管哪种，验收条件"Guest Agent 二进制和 token 不被固化进去"都意味着导出前要删掉注入的文件。
12. **Process 与事件流的 ticket（06）要加一条 Firecracker 验收：** "Hibernate/恢复之后、Fork 之后，都不丢 Process 退出事件"。每次 snapshot 都会重置 vsock 连接，所以按序号续传的路径是必经之路。

---

## 5. 来源（均于 2026-11-21 访问）

**Firecracker**（除非注明版本，均为 main 分支；最新发布版 v1.18.0）
- README https://github.com/firecracker-microvm/firecracker/blob/main/README.md ；Releases https://github.com/firecracker-microvm/firecracker/releases ；CHANGELOG https://github.com/firecracker-microvm/firecracker/blob/main/CHANGELOG.md ；SPECIFICATION https://github.com/firecracker-microvm/firecracker/blob/main/SPECIFICATION.md
- 设计与威胁模型 https://github.com/firecracker-microvm/firecracker/blob/main/docs/design.md ；生产宿主机配置 https://github.com/firecracker-microvm/firecracker/blob/main/docs/prod-host-setup.md ；Jailer https://github.com/firecracker-microvm/firecracker/blob/main/docs/jailer.md ；Seccomp https://github.com/firecracker-microvm/firecracker/blob/main/docs/seccomp.md
- Snapshot 支持 https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md ；版本 https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/versioning.md ；缺页 / UFFD https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/handling-page-faults-on-snapshot-resume.md ；克隆的随机数 https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/random-for-clones.md ；克隆的网络 https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/network-for-clones.md
- vsock https://github.com/firecracker-microvm/firecracker/blob/main/docs/vsock.md ；网络配置 https://github.com/firecracker-microvm/firecracker/blob/main/docs/network-setup.md ；大页 https://github.com/firecracker-microvm/firecracker/blob/main/docs/hugepages.md ；Balloon https://github.com/firecracker-microvm/firecracker/blob/main/docs/ballooning.md
- 内核策略 https://github.com/firecracker-microvm/firecracker/blob/main/docs/kernel-policy.md ；rootfs 与内核配置 https://github.com/firecracker-microvm/firecracker/blob/main/docs/rootfs-and-kernel-setup.md ；开发机配置 https://github.com/firecracker-microvm/firecracker/blob/main/docs/dev-machine-setup.md ；API spec https://github.com/firecracker-microvm/firecracker/blob/main/src/firecracker/swagger/firecracker.yaml
- VFIO 跟踪 issue https://github.com/firecracker-microvm/firecracker/issues/5679 ；已关闭的 PR https://github.com/firecracker-microvm/firecracker/pull/5870 ；`feature/vfio` 的 PR #6183 #6219 #6241 #6252 #6316（已合入该分支）、#6356 #6398（open），通过 GitHub API 核实
- firecracker-go-sdk（已归档）https://github.com/firecracker-microvm/firecracker-go-sdk ；firecracker-containerd（已归档）https://github.com/firecracker-microvm/firecracker-containerd
- Agache 等，"Firecracker: Lightweight Virtualization for Serverless Applications"，NSDI '20 https://www.usenix.org/conference/nsdi20/presentation/agache

**云厂商与托管**
- GCP 嵌套虚拟化概览 https://docs.cloud.google.com/compute/docs/instances/nested-virtualization/overview ；开启 https://docs.cloud.google.com/compute/docs/instances/nested-virtualization/enabling
- AWS user guide https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/amazon-ec2-nested-virtualization.md ；What's New 2026-02-16 https://aws.amazon.com/about-aws/whats-new/2026/02/amazon-ec2-nested-virtualization-on-virtual ；What's New 2026-06-18 https://aws.amazon.com/about-aws/whats-new/2026/06/nested-virtualization-intel-us-gov-cloud/
- AWS Lambda SnapStart uniqueness https://docs.aws.amazon.com/lambda/latest/dg/snapstart-uniqueness.html
- Hetzner AX 矩阵 https://www.hetzner.com/dedicated-rootserver/matrix-ax/（价格会变；不含增值税）

**内核**
- Running nested guests https://docs.kernel.org/virt/kvm/x86/running-nested-guests.html
- af_vsock.c https://github.com/torvalds/linux/blob/master/net/vmw_vsock/af_vsock.c ；af_vsock.h https://github.com/torvalds/linux/blob/master/include/net/af_vsock.h ；vm_sockets.h https://github.com/torvalds/linux/blob/master/include/uapi/linux/vm_sockets.h ；vmgenid.c https://github.com/torvalds/linux/blob/master/drivers/virt/vmgenid.c

**E2B runtime**（提交 `89e7715`，2026-11-21）
- README https://github.com/e2b-dev/runtime ；ARCHITECTURE https://github.com/e2b-dev/runtime/blob/main/docs/ARCHITECTURE.md ；DEV-LOCAL https://github.com/e2b-dev/runtime/blob/main/DEV-LOCAL.md
- Firecracker 进程 https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/sandbox/fc/process.go ；启动脚本 https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/sandbox/fc/script_builder.go ；API 客户端 https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/sandbox/fc/client.go ；OpenAPI 副本 https://github.com/e2b-dev/runtime/blob/main/packages/shared/pkg/fc/firecracker.yml
- OCI 转 ext4 https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/template/build/core/oci/oci.go ；ext4 https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/template/build/core/filesystem/ext4.go ；rootfs 注入 https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/template/build/core/rootfs/rootfs.go
- 网络 slot https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/sandbox/network/slot.go ；网络配置 https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/sandbox/network/network.go
- 内核 https://github.com/e2b-dev/runtime/tree/main/firecracker/fc-kernels ；内核配置 https://github.com/e2b-dev/runtime/blob/main/firecracker/fc-kernels/configs/x86_64/6.1.190.config ；Firecracker 构建 https://github.com/e2b-dev/runtime/tree/main/firecracker/fc-versions ；BusyBox https://github.com/e2b-dev/runtime/tree/main/firecracker/fc-busybox
- Embed Terraform：GCP https://github.com/e2b-dev/runtime/blob/main/embed/terraform/gcp/variables.tf 、https://github.com/e2b-dev/runtime/blob/main/embed/terraform/gcp/main.tf ；AWS https://github.com/e2b-dev/runtime/blob/main/embed/terraform/aws/variables.tf

**其他一手来源**
- flintlock https://github.com/liquidmetal-dev/flintlock
- Kata Containers 搭配 Firecracker https://github.com/kata-containers/kata-containers/blob/main/docs/how-to/how-to-use-kata-containers-with-firecracker.md ；versions.yaml https://github.com/kata-containers/kata-containers/blob/main/versions.yaml
- Brooker 等，"Restoring Uniqueness in MicroVM Snapshots"，arXiv 2102.12892 https://arxiv.org/abs/2102.12892
- Marc Brooker，博客文章 2025-09-18 https://brooker.co.za/blog/2025/09/18/firecracker
- CodeSandbox，"Cloning microVMs using userfaultfd" https://codesandbox.io/blog/cloning-microvms-using-userfaultfd（我抓取时返回 403，只用来说明这篇文章存在）

**过时或与版本相关、需要再核实的项：** Firecracker 的 snapshot 格式和 guest 内核支持日期；VFIO 状态；AWS 和 GCP 支持嵌套虚拟化的实例列表；Hetzner 价格；E2B 的内部实现（每天都在变，且藏在 feature flag 后面）。
