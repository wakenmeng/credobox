# GPU 沙箱化（第 4 阶段）：隔离、passthrough 与启动时间

> [English](gpu-sandboxing.md) | 中文

> 调研日期：2026-11-21。下文所有链接均在这一天访问，除非另有说明。价格是 2026-11-21 读到的按需标价，变动频繁。
> 只用一手来源：NVIDIA 文档和博客、Linux 内核文档和源码、VMM 的文档、源码和 issue（Cloud Hypervisor、QEMU、Firecracker）、gVisor 文档、云厂商的价格页和配额页、厂商工程博客、CVE 记录。少数几处只有二手来源，标为 **（二手）**。
> 文中 **【推断】** 标出的是我自己的推论，不是来源原文。术语以本项目 [GLOSSARY](../../GLOSSARY.zh.md) 为准。本文不重复 [agent-sandbox-usage.zh.md](agent-sandbox-usage.zh.md) 已有的内容，那篇已经在高层面讲过 Modal 的两种 runtime 和 Daytona v2 对 hypervisor 的选择。

---

## 1. 结论摘要

- **容器级 GPU 让所有 Tenant 共用宿主机的 NVIDIA 内核驱动。** NVIDIA Container Toolkit（或者 CDI spec）只是往容器里注入设备节点、驱动库和 hook。gVisor 的 `nvproxy` 在宿主机前面加了一层用户态内核，但它把白名单里的 `ioctl` 转发给的仍是同一个宿主驱动。gVisor 自己的文档说，它对 NVIDIA 驱动本身的漏洞 "much less effective"（[gVisor GPU](https://gvisor.dev/docs/user_guide/gpu/)）。toolkit 的 hook 已经出过两次容器逃逸：CVE-2024-0132 和 CVE-2025-23266（[NVD API](https://services.nvd.nist.gov/rest/json/cves/2.0?cveId=CVE-2025-23266)）。
- **硬隔离意味着通过 VFIO 把整块 GPU 交给一个 VM。** 需要 IOMMU，需要一个 viable 的 IOMMU group，组里每个设备都绑定到 `vfio-pci`（[kernel VFIO](https://docs.kernel.org/driver-api/vfio.html)），还需要支持它的 VMM。Cloud Hypervisor 和 QEMU 支持。**Firecracker 至今没有发布 VFIO 支持。** v1.13 加了 PCI，但 VFIO 还在 `feature/vfio` 分支上，只用 NVMe 测过，而且拒绝 snapshot（[#5679](https://github.com/firecracker-microvm/firecracker/issues/5679)、[#6055](https://github.com/firecracker-microvm/firecracker/pull/6055)）。
- **在云上的 GPU VM 里做不了 VFIO passthrough。** 要把设备分给嵌套 VM，L1 里必须有 IOMMU，而内核的 no-IOMMU 模式明确不能把设备分给 VM（[VFIO Kconfig](https://raw.githubusercontent.com/torvalds/linux/master/drivers/vfio/Kconfig)）。AWS 只在 C/M/R/I/X 系列上支持嵌套虚拟化，其中没有带 GPU 的（[AWS nested](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/amazon-ec2-nested-virtualization.html)）。GCP 没有文档说明提供 vIOMMU。所以需要裸金属。
- **"一次 API 调用、几秒内拿到 GPU" 主要卡在五项开销上**，大致按大小排：
  1. 拿到一台健康的 GPU 机器，要几分钟，Modal 的解法是常备一批空闲 GPU；
  2. 模型权重，受吞吐限制，几 GB/s；
  3. 引擎初始化，比如 CUDA graph 和 `torch.compile`；
  4. VFIO 要 pin 住 VM 内存，耗时随 guest 内存线性增长（1.2 TB 的 guest 要 253 s）；
  5. 大 BAR GPU 的 BAR 映射，每块约 1.5 s，4×H100 在启动时挂上要多花 1–3 分钟。

  来源：[Modal](https://modal.com/blog/truly-serverless-gpus)、[Canonical](https://cpaelzer.github.io/blogs/007-vfio-slow-startup)、[LKML](https://lkml.iu.edu/hypermail/linux/kernel/2411.3/03532.html)。业界的答案是用 `cuda-checkpoint` 和 CRIU CUDA 插件对 CUDA 状态做 checkpoint/restore。Modal 报告这样启动快 4–10 倍。
- **GPU 从一个 Tenant 交给下一个是个真问题，而 NVIDIA 只在 Confidential Computing 模式下写明了内存清洗。** CC 模式下固件在 reset 时 "scrubs the GPU state and memory"（[NVIDIA CC blog](https://developer.nvidia.com/blog/confidential-computing-on-h100-gpus-for-secure-and-trustworthy-ai/)）。`vfio-pci` 在打开和释放设备时会 reset 这个 function（FLR，不行就退到 bus reset）（[vfio_pci_core.c](https://raw.githubusercontent.com/torvalds/linux/master/drivers/vfio/pci/vfio_pci_core.c)）。但我没找到任何 NVIDIA 页面说 CC 模式以外 reset 会清空显存。LeftoverLocals（CVE-2023-4969）影响了 Apple、AMD、Qualcomm、Imagination，NVIDIA 则表示自己不受影响（[Trail of Bits](https://blog.trailofbits.com/2024/01/16/leftoverlocals-listening-to-llm-responses-through-leaked-gpu-local-memory/)）。
- **几种共享方式里，只有整卡和 MIG 有硬件隔离。** time-slicing "no memory or fault-isolation"（[GPU Operator](https://docs.nvidia.com/datacenter/cloud-native/gpu-operator/latest/gpu-sharing.html)）。MPS 的客户端要用同一个 UID，错误隔离只是 "limited"（[MPS](https://docs.nvidia.com/deploy/mps/when-to-use-mps.html)）。MIG 要 Ampere 及以后的数据中心卡。学习者租得起的 L4、T4、A10 都不支持 MIG（[MIG GPUs](https://docs.nvidia.com/datacenter/tesla/mig-user-guide/supported-gpus.html)）。
- **厂商分成两派。**
  - 共享宿主机上跑 gVisor 加 nvproxy：Modal（GPU Sandbox 只能用 gVisor）、GKE Sandbox、Beam，以及在没有嵌套虚拟化时的 Northflank。
  - 独占宿主机上跑 Cloud Hypervisor 加整卡 passthrough：Daytona v2，以及 Fly.io。Fly.io 在 NVIDIA 驱动上耗了好几个月，后来收缩了 GPU 业务，据说 2026-07-31 停止 GPU 服务 **（二手）**。
- **对学习者来说，现实可行的第 4 阶段**分两部分：
  - 4a：在 GCP `g2-standard-4`（按需 $0.7068/h，spot $0.4230/h，us-central1）或 AWS `g4dn.xlarge`（$0.526/h，us-east-1）上跑 Docker 和 gVisor 的 GPU。
  - 4b：在裸金属上用 Cloud Hypervisor（不是 Firecracker）做 VFIO：Hetzner GEX45（每月 €214 加 €209 开通费，或 €0.3429/h；GEX44 已不在价目表上）、AWS `g5g.metal`（$2.744/h，Arm，2×T4G），或 `g4dn.metal`（$7.824/h，8×T4）。

  AWS 的 GPU 配额起始是 0 vCPU，所以用其中任何一个 AWS 选项之前都得先申请提额（[AWS quotas](https://docs.aws.amazon.com/ec2/latest/instancetypes/ec2-instance-quotas.html)）。

---

## 2. 分问题的发现

### 2.1 容器级 GPU：NVIDIA Container Toolkit、CDI 和 gVisor `nvproxy`

**toolkit 怎么把 GPU 暴露出来。**

- NVIDIA Container Runtime "is a thin wrapper around the native runC"。它把 NVIDIA hook 加成 `prestart` hook，从 v1.12.0 起还会修改 OCI spec，"to inject specific devices and mounts"（[architecture](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/arch-overview.html)）。
- 安装指南的前置条件是在运行 toolkit 的机器上 "Install the NVIDIA GPU driver for your Linux distribution"（[install guide](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html)）。
- CDI 是 "an open specification for container runtimes"。从 toolkit v1.18.0 起，systemd 单元 `nvidia-cdi-refresh` 会自动重新生成 `/var/run/cdi/nvidia.yaml`。设备名形如 `nvidia.com/gpu=0`，MIG 设备形如 `nvidia.com/gpu=1:0`。spec 里带着 `create-symlinks`、`update-ldcache`、`enable-cuda-compat` 等 hook（[CDI](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/cdi-support.html)）。
- 【推断】一台 Host 上所有 GPU 容器都和同一个 NVIDIA 内核模块打交道，也就通过它共用同一个 Linux 内核，只有用户态库是每个容器各自一份。这和 Docker 跑 CPU 负载是同一个信任模型，只是多了一大块闭源驱动的攻击面。

**toolkit 本身就是逃逸通道。**

- CVE-2024-0132：toolkit 1.16.1 及更早版本在默认配置下有 TOCTOU，"a specifically crafted container image may gain access to the host file system"。NVD 补充："This does not impact use cases where CDI is used"（[NVD API](https://services.nvd.nist.gov/rest/json/cves/2.0?cveId=CVE-2024-0132)）。
- CVE-2025-23266：问题出在 "some hooks used to initialize the container"，能让 "an attacker … execute arbitrary code with elevated permissions"（[NVD API](https://services.nvd.nist.gov/rest/json/cves/2.0?cveId=CVE-2025-23266)）。
  - 发现者 Wiz 解释了机制：`createContainer` hook 在宿主机上以特权运行，会从镜像继承 `LD_PRELOAD`，并且 "with its working directory set to the container's root filesystem"。
  - Wiz 的结论是 "containers are not a strong security barrier and should not be relied upon as the sole means of isolation"（[Wiz, 2025-07-17](https://www.wiz.io/blog/nvidia-ai-vulnerability-cve-2025-23266-nvidiascape)）。
  - 修复版本（toolkit 1.17.8、GPU Operator 25.3.1）来自新闻报道 **（二手）**，请到 NVIDIA 的安全公告里确认。

**gVisor `nvproxy`**（[gVisor GPU guide](https://gvisor.dev/docs/user_guide/gpu/)）：

- **是什么。** gVisor Sentry 里的一个透传驱动，"forwards `ioctl(2)` calls made to NVIDIA devices" 给宿主驱动，并且 "does not emulate NVIDIA kernel-mode driver (KMD) logic"。NVIDIA 的 ioctl ABI 不稳定，所以 nvproxy 按驱动版本各维护一套 ABI 表。
- **GPU。** 官方支持 T4、A100、A10G、L4、H100。同代微架构的其他卡 "will likely work"。
- **驱动。** 白名单跟着 GKE 的驱动版本走。不在支持列表里的驱动要加 `--nvproxy-allow-unsupported-driver`，未知驱动一律启动失败。`runsc nvproxy list-supported-drivers` 能列出清单。
- **暴露面。** 默认只允许 `compute` 和 `utility` 两种 capability。暴露 `/dev/nvidiactl`、`/dev/nvidia-uvm` 和 `/dev/nvidia#`，但不暴露 `/dev/nvidia-caps/*`，所以用不了 MIG。只放行精选的一组 ioctl。在 KVM 平台上 `cudaMallocManaged()` 不稳定。
- **怎么用。** 加 `--nvproxy`，然后用 `docker run --runtime=runsc --gpus=all`，或者在 containerd ≥ 1.7 上用 CDI。宿主机上仍然需要 `nvidia-container-toolkit`。
- **安全论证，用 gVisor 自己的话。**
  - seccomp 过滤只放行白名单里的 ioctl，所以过去的 Linux 内核 CVE "remain mitigated even when 'nvproxy' is used"。
  - 但 "gVisor is much less effective at mitigating vulnerabilities within the NVIDIA GPU drivers themselves, _because_ gVisor passes through calls"。
  - "There is no validation of things like DMA buffers."
  - "it is imperative that users update NVIDIA drivers in a timely manner."
- **GKE Sandbox。** GKE 在 gVisor 下跑 GPU 负载：
  - H100、A100、L4、T4 是 GA，RTX PRO 6000、GB200、B200、H200 是 preview。
  - "Only CUDA workloads are supported"。
  - "Don't use GPU time-sharing in sandboxed Pods, because the GPU isn't fully isolated between Pods"。
  - "gVisor doesn't mitigate all NVIDIA driver vulnerabilities, but retains protection against Linux kernel vulnerabilities"（[GKE Sandbox](https://docs.cloud.google.com/kubernetes-engine/docs/concepts/sandbox-pods)）。
- **生产用户，按厂商自己的说法：**
  - **Modal。** "GPU Sandboxes are only supported with `runtime="gvisor"`"（[Modal sandboxes](https://modal.com/docs/guide/sandboxes.md)）。Modal 2026-05-12 的工程博客说用户容器跑在 `runsc` 下，说 Modal 的 "`nvproxy` sub-component" 负责和内核态驱动通信，还说 Modal 给它贡献代码（[Modal, truly serverless GPUs](https://modal.com/blog/truly-serverless-gpus)）。
  - **gVisor 的用户页。** 列出了 Beam（"gVisor isolation, GPU acceleration"），以及 Northflank，后者 "when nested virtualization is unavailable" 时用 gVisor 跑 GPU 负载（[gVisor users](https://gvisor.dev/users/)）。
- 【推断】用 gVisor 时，CDI 或 legacy 模式的 NVIDIA hook 仍然在沙箱建立*之前*就在宿主机上跑。所以 gVisor 挡不住 CVE-2025-23266 这类 toolkit hook 漏洞。无论用 Docker 还是 gVisor，给 toolkit 打补丁、优先用 CDI 都同样重要。

### 2.2 VM 级 GPU passthrough（VFIO）

**VFIO 需要什么**（[kernel VFIO](https://docs.kernel.org/driver-api/vfio.html)）：

- **IOMMU，并以 IOMMU group 作为归属单位。** "A group is a set of devices which is isolatable from all other devices in the system"，"Groups are therefore the unit of ownership used by VFIO"。
- **ACS。** 不支持 ACS 的桥会让流量 "without reaching the IOMMU" 就被转走，于是桥后面的设备只能被分进同一个组。
- **viable 的组。** 组里每个设备都必须绑定到 VFIO 驱动（或者不绑任何驱动），这个组才能用。
- **绑定。** 先把 GPU 从 `nvidia`/`nouveau` 解绑，`modprobe vfio-pci`，再把 vendor 和 device ID 写进 `new_id`。GPU 的音频 function 通常在同一组里，也要照样处理。
- **没有 IOMMU 就不能做设备分配。** no-IOMMU 模式会 taint 内核，而且 "Device assignment to virtual machines is also not possible with this mode since there is no IOMMU to provide DMA translation"（[VFIO Kconfig](https://raw.githubusercontent.com/torvalds/linux/master/drivers/vfio/Kconfig)）。
- **内存 pin。** guest 启动之前，VMM 必须把 guest 的全部内存 pin 住并映射进 IOMMU：
  - Firecracker 维护者的说法：passthrough "implies pinning physical memory which would remove our memory oversubscription capabilities"（[#849](https://github.com/firecracker-microvm/firecracker/issues/849)），microVM 将不得不 "start with their full memory mapped"（[#1179](https://github.com/firecracker-microvm/firecracker/issues/1179)）。
  - Firecracker 的 VFIO PR 和 virtio-balloon、virtio-mem 不兼容（[#6055](https://github.com/firecracker-microvm/firecracker/pull/6055)）。
- **BAR。** 数据中心 GPU 暴露的 64-bit BAR 非常大。Alex Williamson 提到 DGX H100 上 "large 128GB BARs"（[LKML](https://lkml.iu.edu/hypermail/linux/kernel/2411.3/03532.html)）。Cloud Hypervisor 的文档写明，设备一多，32-bit MMIO BAR 会耗尽一个 PCI segment 的地址窗口，并提供 `mmio32_aperture_weight` 和 `x_exclude_mmap_bars` 来应对（[CH VFIO](https://github.com/cloud-hypervisor/cloud-hypervisor/blob/main/docs/vfio.md)）。

**哪些 VMM 支持（截至 2026-11-21）：**

| VMM | GPU passthrough | 依据 |
|---|---|---|
| **QEMU** | 支持，由来已久。可以用 legacy VFIO container，也可以用 iommufd 后端：`-device vfio-pci,host=0000:02:00.0[,iommufd=…]` | [QEMU iommufd](https://www.qemu.org/docs/master/devel/vfio-iommufd.html) |
| **Cloud Hypervisor** | 支持。`--device path=/sys/bus/pci/devices/…`，可选 `--platform iommufd=on`。文档里写了 NVIDIA GPUDirect P2P clique（Turing、Ampere、Hopper、Lovelace）。支持用 `ch-remote add-device` 对 VFIO 做 **热插拔**，前提是开启 ACPI。snapshot 和热迁移只在支持迁移的 *variant* 驱动上可用；"Generic `vfio_pci` is not migratable" | [CH VFIO](https://github.com/cloud-hypervisor/cloud-hypervisor/blob/main/docs/vfio.md)、[CH hotplug](https://github.com/cloud-hypervisor/cloud-hypervisor/blob/main/docs/hotplug.md) |
| **Firecracker** | **没有已发布的支持。** v1.13.0："Added PCI support in Firecracker. PCI support is optional." 那是基于 PCI 的 virtio，不是 passthrough。VFIO 跟踪 issue #5679 从 2026-02-11 开着，说 vGPU 和 MIG GPU "will be supported (and we test it internally)"。PR #5870 因 #6055 而关闭；#6055 被改投到 `feature/vfio` 分支，2026-10-21 未合并即关闭，只用 NVMe 测过，"Pre-boot only"，并且拒绝 snapshot。截至 v1.18.0（11 月 9 日）没有任何 release 提到 VFIO。一位维护者 2025 年说："We do not have date yet for official GPU support in Firecracker" | [CHANGELOG](https://github.com/firecracker-microvm/firecracker/blob/main/CHANGELOG.md)、[releases](https://github.com/firecracker-microvm/firecracker/releases)、[#5679](https://github.com/firecracker-microvm/firecracker/issues/5679)、[#5870](https://github.com/firecracker-microvm/firecracker/pull/5870)、[#6055](https://github.com/firecracker-microvm/firecracker/pull/6055)、[#1179](https://github.com/firecracker-microvm/firecracker/issues/1179) |

**嵌套：为什么云上 GPU VM 不能把 GPU 再透传一层。**

- 根据上面的内核规则，没有 IOMMU 的 L1 不能把设备分给 L2。
- AWS 的嵌套虚拟化（2026）只开放给 M7i/M8i、C7i/C8i、R7i/R8i、X8i 和 I7i/I7ie 系列。列表里没有任何 G 或 P 系列，而且 AWS 建议对性能敏感的场景用裸金属（[AWS nested](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/amazon-ec2-nested-virtualization.html)）。
- GCP 的嵌套虚拟化页只把 KVM 列为 L1 hypervisor，排除了 E2、ARM、大部分 AMD 和大部分内存优化型 VM。它对 L2 里的 GPU、IOMMU、VFIO 都只字未提（[GCP nested](https://docs.cloud.google.com/compute/docs/instances/nested-virtualization/overview)）。
- 【推断】这个说法准确的版本是：嵌套 passthrough 要求云厂商给 L1 暴露一个虚拟 IOMMU（QEMU 能给自己的 guest 这样做），而 GCP 和 AWS 都没有文档说明提供它。所以实际上需要裸金属，或者 `.metal` 实例，才能控制真实的 IOMMU。
- 【推断】在云上 GPU VM *里面* 仍然可以共享 GPU，因为这不需要嵌套虚拟化：Docker 加 toolkit，或者 gVisor 加 nvproxy。GKE Sandbox 和 Modal 卖的正是这个。

### 2.3 "一次 API 调用、几秒内拿到 GPU"：启动时间被什么主导

| 阶段 | 来源怎么说 | 厂商或工程师描述的缓解办法 |
|---|---|---|
| **拿到一台 GPU 机器** | 拉起并做健康检查一台新实例要 "minutes to tens of minutes"（[Modal](https://modal.com/blog/truly-serverless-gpus)） | Modal 常备一批跨应用共享的空闲健康 GPU，规模由线性规划决定。开机时做简短健康检查，每周跑一次 `dcgmi diag`（同一来源） |
| **VFIO 绑定** | 一手来源里没找到耗时数据。【推断】这只是一次 sysfs 写入，和下面几项比起来很便宜；应该在 Host 启动时做一次，而不是每个 Sandbox 做一次 | 在 passthrough Host 上把 GPU 常驻绑定在 `vfio-pci` |
| **VMM 内存 pin** | pin 住 guest 内存并映射进 IOMMU 的耗时随 guest 内存线性增长。1.2 TB 的 guest，准备阶段开 VFIO 要 253 s，不开只要 6 s；1 GiB 大页降到 59 s；关掉 THP 更糟（476 s）。128 GiB 的 guest 视内存碎片情况从几秒到约 10 分钟不等（[Canonical 工程师博客, 2024-04-28](https://cpaelzer.github.io/blogs/007-vfio-slow-startup)） | 1 GiB 大页、预分配、正确的 NUMA 布局（同一来源） |
| **guest PCI 枚举时的 BAR 映射** | 4×H100 passthrough 在启动时挂上要多花 "1-3 extra minutes"，启动后再热插拔则是 "under 20 seconds cumulatively"。每块 GPU 每次重新初始化约 1.5 s。大页没有帮助。Williamson："The BAR space is walked, faulted, and mapped"（[LKML, 2024-11-26](https://lkml.iu.edu/hypermail/linux/kernel/2411.3/03532.html)） | 启动后再热插拔。较新的内核会用大页映射 pfnmap 区域（同一讨论串） |
| **guest 驱动加载与初始化** | 不开持久化时，每个作业都要把 GPU "initialized and deinitialized" 一遍，导致 "long load times for each CUDA job, on the order of seconds"（[NVIDIA driver persistence](https://docs.nvidia.com/deploy/driver-persistence/background.html)）。ECC 清洗在 "during driver initialization" 进行（[nvidia-smi](https://docs.nvidia.com/deploy/nvidia-smi/index.html)） | `nvidia-persistenced`，它会 "sleeps with the device files open"（[persistence daemon](https://docs.nvidia.com/deploy/driver-persistence/persistence-daemon.html)） |
| **CUDA context 与模块加载** | "Lazy loading reduces program initialization time by waiting to load CUDA modules until they are needed"。Linux 上从 CUDA 12.2 起默认开启，所有平台从 12.3 起默认开启（[CUDA guide](https://docs.nvidia.com/cuda/cuda-programming-guide/04-special-topics/lazy-loading.html)） | lazy loading；GPU 内存快照（见下） |
| **引擎初始化**（CUDA graph、`torch.compile`） | "between tens of seconds and minutes"；CUDA graph 没有原生的序列化（[Modal](https://modal.com/blog/truly-serverless-gpus)） | GPU 内存快照能把这一步整个跳过（带 `torch.compile` 的 ViT：8.5 s → 2.25 s）（[Modal GPU snapshots](https://modal.com/blog/gpu-mem-snapshots)） |
| **模型权重** | 几 GB/s，所以要几秒到几百秒。受吞吐限制，快照帮不上忙（[Modal](https://modal.com/blog/truly-serverless-gpus)） | 按内容寻址的 lazy 文件系统加分层缓存（page cache、本地 SSD、AZ 缓存、CDN、对象存储）。AZ 内或 RDMA 权重服务器还在 "working on it"（同一来源） |

**GPU 内存快照，以及各个工具的现状：**

- **`cuda-checkpoint`**（[GitHub](https://github.com/NVIDIA/cuda-checkpoint)、[NVIDIA blog 2024-07-02](https://developer.nvidia.com/blog/checkpointing-cuda-applications-with-criu/)）：
  - **原理。** 锁住 CUDA API，等已提交的工作完成，把显存拷到主机内存，然后释放 GPU。"A suspended CUDA process no longer directly refers to any GPU hardware at the OS level"，于是 CRIU 就能 dump 它。
  - **驱动支持。** 从驱动 550 起。570 加了 CRIU 4.0+ 集成，580 加了 GPU 迁移和 "container partial passthrough"，595 加了 Arm，610 加了 `cuIpcGetMemHandle` IPC。
  - **仍不支持。** UVM，以及 `cuMemExportToShareableHandle()` 创建的 IPC 内存。
- **CRIU CUDA 插件**（[CRIU plugins/cuda](https://github.com/checkpoint-restore/criu/tree/criu-dev/plugins/cuda)）：
  - **后端。** CUDA ≥ 13 用 driver-API 后端，r565/r570 用 `cuda-checkpoint` CLI 后端。
  - **换卡恢复。** restore 时可以按 UUID 或序号重新映射 GPU，但目标卡 "must still be compatible and have enough memory"。
  - **不支持。** NVIDIA UVM 托管内存、MIG 和 MPS。
  - **安全警告。** 不以 PID namespace 的 1 号进程为根的不可信负载可能借 `CLONE_UNTRACED` 逃出跟踪，建议用 seccomp 过滤这个 flag。
- **Modal**（[GPU snapshots, 2025-07-30](https://modal.com/blog/gpu-mem-snapshots)、[truly serverless, 2026-05-12](https://modal.com/blog/truly-serverless-gpus)）：
  - **机制。** CUDA checkpoint API（570/575 分支），"fully integrated with our existing gVisor checkpoint/restore system"。快照可以在 GPU 硬件兼容的另一台机器上恢复。
  - **提速。** "typical speedups are between 4-10x"。vLLM 加 Qwen3-0.6B 的平均冷启动从 95.7 s 降到 13.8 s。
  - **限制。** 只支持单 GPU；NCCL 程序可能死锁。CPU 快照对宿主机的 CPU 特性敏感，比如 `g6.12xlarge` 缺 `pclmulqdq`。
- **Daytona v2** 的文档说 GPU Sandbox 的 checkpoint "saves its processes, memory, and GPU memory"，并 "resumes in a new GPU sandbox"，但没说怎么做的（[Daytona sandboxes](https://www.daytona.com/docs/sandboxes.md)）。同一页把 GPU Sandbox 的 pause/resume、fork、内存持久化都标为不支持。【推断】既然 Cloud Hypervisor 不能给通用 `vfio-pci` 设备做 snapshot，这多半是先在 guest 里做 CUDA checkpoint，再做 CPU 侧快照，而不是 VMM 对 passthrough 设备做 snapshot。
- **Cloud Hypervisor 与 VFIO 快照。**
  - 按需（lazy）恢复："VFIO devices are not supported"。
  - restore *之后* 再热插拔 VFIO 设备是可以的，但会让 guest 的每一页都发生写缺页，失去 page cache 共享（[CH snapshot_restore](https://github.com/cloud-hypervisor/cloud-hypervisor/blob/main/docs/snapshot_restore.md)）。
  - 【推断】这提示了一种快速启动 GPU VM 的做法：先恢复一个预先启动好、不带 GPU 的 VM 快照，再热插拔一块已经 reset 过的 GPU，然后在 guest 里用 `cuda-checkpoint` 恢复 CUDA 状态。这个做法未经验证，是 4b 阶段最值得测的东西。

### 2.4 Tenant 之间的多租户安全

- **reset 做了什么。**
  - `vfio-pci` 在打开设备时调用 `pci_try_reset_function()`。释放时先恢复保存的状态，再 reset 一次这个 function。如果设备 "needs_reset (such as by lack of FLR support)"，并且总线上所有设备都没被占用，就退到 `pci_reset_bus()`，也就是 secondary bus reset（[vfio_pci_core.c](https://raw.githubusercontent.com/torvalds/linux/master/drivers/vfio/pci/vfio_pci_core.c)）。
  - `nvidia-smi -r` "can be used to clear GPU HW and SW state"，需要 root，并且要求没有任何应用在用这块卡。那份文档没提清空显存（[nvidia-smi](https://docs.nvidia.com/deploy/nvidia-smi/index.html)）。
- **内存清洗只在 CC 模式下有文档。** NVIDIA 的 H100 CC 博文（2023-08-03）说，开启 CC 模式时以及 Tenant 关机时，宿主机发起一次 reset，并且 "GPU firmware scrubs the GPU state and memory"（[NVIDIA CC blog](https://developer.nvidia.com/blog/confidential-computing-on-h100-gpus-for-secure-and-trustworthy-ai/)）。据说 NVIDIA 工程师在 ACM Queue 的一篇文章里写到 PF-FLR "will scrub memory … before the GPU is handed to the next tenant"（来自搜索摘要；页面返回 403）**（未核实）**。
- 【推断】在 CC 模式以外，我没找到任何 NVIDIA 声明说 FLR 或 SBR 会清空显存。因此平台必须 *测试* 是否有残留数据，而不能假定已经清空：在 Sandbox A 里往显存写一个 canary，销毁 A，把 GPU 交给 Sandbox B，再扫描显存。这个测试应该放进隔离测试套件。
- **LeftoverLocals（CVE-2023-4969）。**
  - GPU 的 local memory 在 kernel 之间不清零，所以同时驻留的进程能读到别人的数据，包括 LLM 的输出。在 Apple、AMD、Qualcomm、Imagination 上得到确认。
  - "NVIDIA: confirmed that their devices are not currently impacted"。
  - 研究者建议避免多租户 GPU 环境，并指出做了物理分区的云 GPU（vGPU、MxGPU）可能不受影响（[Trail of Bits](https://blog.trailofbits.com/2024/01/16/leftoverlocals-listening-to-llm-responses-through-leaked-gpu-local-memory/)、[NVD](https://services.nvd.nist.gov/rest/json/cves/2.0?cveId=CVE-2023-4969)）。
  - 【推断】教训在于这一类漏洞：只要两个 Tenant 同时共用一块 GPU（time-slicing、MPS、在同一块卡上跑 nvproxy），它们之间没清空的硬件状态就会成为 Tenant 之间的信道。
- **MIG 作为隔离边界。**
  - NVIDIA 说每个实例都有 "separate and isolated paths through the entire memory system"。L2 和 DRAM 带宽有保证，MIG 提供 "fault isolation for different clients such as VMs, containers or processes"，并且支持以 passthrough 和 vGPU 方式给 VM 用（[MIG intro](https://docs.nvidia.com/datacenter/tesla/mig-user-guide/introduction.html)）。
  - 支持的卡：A100、A30、H100、H200、H20、B200、B300、GB200（最多 7 个实例），以及 RTX PRO Blackwell（2–4 个实例）（[MIG GPUs](https://docs.nvidia.com/datacenter/tesla/mig-user-guide/supported-gpus.html)）。
  - 注意事项：nvproxy 不暴露 `/dev/nvidia-caps`（[gVisor](https://gvisor.dev/docs/user_guide/gpu/)）。CRIU CUDA 插件不支持 MIG（[CRIU](https://github.com/checkpoint-restore/criu/tree/criu-dev/plugins/cuda)）。Fly.io："MIG gives you a UUID to talk to the host driver, not a PCI device"，所以在他们的 micro-VM 上用不了（[Fly.io](https://fly.io/blog/wrong-about-gpu/)）。
- **Confidential Computing。**
  - H100 有三种 CC 模式：CC-Off、CC-On、CC-DevTools。CC-On 关掉性能计数器以防侧信道。CPU↔GPU 流量走加密的 bounce buffer，速度 "roughly 4 GBytes/sec"（[NVIDIA CC blog](https://developer.nvidia.com/blog/confidential-computing-on-h100-gpus-for-secure-and-trustworthy-ai/)）。
  - R595 release notes 列出了 Hopper（H100、H200 等）和 Blackwell（HGX B200、RTX PRO 6000 Server Edition）。模式有 "Single GPU Passthrough (SPT CC)"、Hopper 多卡的 "Protected PCIe (PPCIe)"，以及 "Blackwell Multiple GPU Passthrough (MPT CC)"。没提 GB200（[R595 TRD1](https://docs.nvidia.com/confidential-computing/release-notes/r595-trd1.md)）。
  - 【推断】CC 永远是透传进 TDX 或 SEV-SNP 机密 VM 的模式。学习者够不着（要支持 CC 的 CPU、H100 或 B200，还要做 attestation），但值得在设计文档里写一节，作为 "Tenant 不信任宿主机" 这一档。
- **Firecracker 最初的两条反对理由（2019）** 至今仍是对这个问题最清楚的表述：内存 pin 让超卖失效；以及 "We can only run 1 customer workload securely per physical GPU"，切换 "long enough … to make it impractical"（[#849](https://github.com/firecracker-microvm/firecracker/issues/849)）。

### 2.5 共享还是独占

| 模式 | 共享了什么 | Tenant 之间有没有硬隔离？ | 来源 |
|---|---|---|---|
| 通过 VFIO 整卡交给 VM | GPU 上什么都不共享。PCIe 拓扑和 IOMMU group 是宿主机的事 | **有**，靠 IOMMU，外加 Tenant 之间的 reset（内存清洗只在 CC 模式下有文档） | [kernel VFIO](https://docs.kernel.org/driver-api/vfio.html)、[NVIDIA CC](https://developer.nvidia.com/blog/confidential-computing-on-h100-gpus-for-secure-and-trustworthy-ai/) |
| 整卡交给一个容器（Docker 或 gVisor） | 宿主机内核和 NVIDIA KMD | 显存不被同时共享。驱动和内核是共享的；gVisor 缩小了内核暴露面，没有缩小驱动暴露面 | [gVisor](https://gvisor.dev/docs/user_guide/gpu/)、[GKE](https://docs.cloud.google.com/kubernetes-engine/docs/concepts/sandbox-pods) |
| MIG | 一块物理 GPU 切成几片，内存通路各自独立 | 按 NVIDIA 的说法是 **硬件分区**，带故障隔离。只有 Ampere 及以后的数据中心卡支持 | [MIG intro](https://docs.nvidia.com/datacenter/tesla/mig-user-guide/introduction.html) |
| vGPU（time-sliced 或 MIG-backed） | NVIDIA 宿主机 vGPU manager 背后的一块中介 GPU | 看底层是哪种。需要 NVIDIA vGPU 软件和 license 服务器。AWS `g6f` 以 "Fractional vGPU VMs" 的形式卖 1/8 到 1/2 块 L4，用 GRID 驱动 | [vGPU docs](https://docs.nvidia.com/vgpu/latest/grid-vgpu-user-guide/index.html)、[AWS G6](https://aws.amazon.com/ec2/instance-types/g6/) |
| time-slicing | 全部 | **没有**："no memory or fault-isolation between replicas" | [GPU Operator](https://docs.nvidia.com/datacenter/cloud-native/gpu-operator/latest/gpu-sharing.html) |
| MPS | 全部；每个用户一个 MPS server | **没有**。地址空间分开，但错误隔离只是 "limited"，客户端要和 server 同一个 UID；`-multiuser-server` 会 "dropping isolation between users" | [MPS](https://docs.nvidia.com/deploy/mps/when-to-use-mps.html) |

【推断】对一个跑不可信代码的多租户 agent 沙箱来说，站得住的选择只有每个 Sandbox 一整块 GPU（容器或 VM），以及每个 Sandbox 一个 MIG 切片。time-slicing 和 MPS 只能在同一个 Tenant *内部* 使用。

### 2.6 谁怎么做（以厂商自述为准）

| 厂商 | GPU 隔离方式 | 来源 |
|---|---|---|
| **Modal** | GPU Sandbox 只能用 gVisor。用户容器跑在 `runsc` 下；Modal 给 `nvproxy` 贡献代码。GPU 内存快照是在 gVisor checkpoint 里调用 CUDA checkpoint API。每个容器最多 8 块 GPU，"always attached to the same physical machine" | [sandboxes](https://modal.com/docs/guide/sandboxes.md)、[truly serverless](https://modal.com/blog/truly-serverless-gpus)、[gpu](https://modal.com/docs/guide/gpu.md) |
| **Daytona v2** | GPU Sandbox 跑在 "Cloud Hypervisor under KVM" 上。"The GPUs of a running GPU sandbox are its own, and no other sandbox uses them at the same time"。提供的 GPU：H100、H200、B200、B300、RTX Pro 6000、RTX 4090、RTX 5090、L40S，以及 AMD MI355X。checkpoint 包含显存；没有 pause、fork、spot | [sandboxes](https://www.daytona.com/docs/sandboxes.md)、[gpu-sandboxes](https://www.daytona.com/docs/gpu-sandboxes.md) |
| **Fly.io** | 平时用 Firecracker，GPU Machine 改用 Cloud Hypervisor，"a very similar Rust codebase that supports PCI passthrough"。GPU 和非 GPU 负载跑在不同的硬件上。做过两次外部安全评估（Atredis、Tetrel）。花了几个月想让 NVIDIA 宿主机驱动把虚拟化 GPU 映射进 Cloud Hypervisor，最终失败（"we hex-edited the closed-source drivers to trick them into thinking our hypervisor was QEMU"）。MIG 和 vGPU 对他们都不可用。产品已收缩（2025-02-14 的博文）。一篇用户论坛帖说 GPU 服务 2026-07-31 结束 **（二手）** | [Fly.io blog](https://fly.io/blog/wrong-about-gpu/)、[community post](https://community.fly.io/t/gpu-migration-fly-io-gpus-will-be-deprecated-as-of-july-31-2026/27110) |
| **GKE Sandbox** | gVisor 加 nvproxy，整卡分配；不建议用 time-sharing | [GKE](https://docs.cloud.google.com/kubernetes-engine/docs/concepts/sandbox-pods) |
| **Beam、Northflank** | 带 GPU 的 gVisor；Northflank 在 "when nested virtualization is unavailable" 时这样用 | [gVisor users](https://gvisor.dev/users/) |
| **RunPod** | Pods 页说 "Runpod runs Docker for you"，并区分 Secure Cloud（T3/T4 数据中心）和 Community Cloud（社区提供方）。**没有任何关于隔离的说明** | [RunPod pods](https://docs.runpod.io/pods/overview) |
| **AWS（作为底座）** | 以 vGPU VM 的形式卖切分的 L4（`g6f`），所以不到一整块卡的共享发生在 hypervisor 里 | [AWS G6](https://aws.amazon.com/ec2/instance-types/g6/) |

【推断】这种分化和 [agent-sandbox-usage.zh.md](agent-sandbox-usage.zh.md) 里 CPU 那边的情况一致：从 gVisor 起家的厂商（Modal）在 GPU 上也继续用 gVisor；从 microVM 起家的厂商（Daytona v2、Fly）为了 GPU 只能换成 Cloud Hypervisor，因为 Firecracker 做不了 passthrough。

### 2.7 学习者能在哪里动手、要花多少钱

所有价格均为 2026-11-21 读取，不含税。

| 选项 | 能拿到什么 | 价格 | 适合做什么 | 来源 |
|---|---|---|---|---|
| GCP `g2-standard-4` | 1× L4，4 vCPU，16 GiB，us-central1 | 按需 **$0.706832/h**；spot **$0.423016/h** | Docker 和 gVisor 的 GPU（4a）。做不了 passthrough（没有文档说明提供 vIOMMU） | [GCP accelerator pricing](https://cloud.google.com/products/compute/pricing/accelerator-optimized) |
| AWS `g4dn.xlarge` | 1× T4，4 vCPU，16 GiB，us-east-1 | **$0.526/h** | 4a；T4 在 nvproxy 官方支持列表里 | [AWS price JSON（us-east-1，2026-11-21 发布）](https://b0.p.awsstatic.com/pricing/2.0/meteredUnitMaps/ec2/USD/current/ec2-ondemand-without-sec-sel/US%20East%20(N.%20Virginia)/Linux/index.json)、[G4 页](https://aws.amazon.com/ec2/instance-types/g4/) |
| AWS `g6.xlarge` | 1× L4，4 vCPU，16 GiB | **$0.8048/h** | 4a | 同一份 JSON、[G6 页](https://aws.amazon.com/ec2/instance-types/g6/) |
| AWS `g6f.large` | 1/8 块 L4（3 GB）的 vGPU | **$0.202/h** | 只能从 guest 这一侧观察 vGPU | 同上 |
| AWS `g4dn.metal` | **裸金属**，8× T4，96 vCPU，384 GiB | **$7.824/h** | VFIO 加 Cloud Hypervisor、多 GPU 调度 | 同一份 JSON |
| AWS `g5g.metal` | **裸金属**，2× T4G，Graviton2（Arm），64 vCPU，128 GiB | **$2.744/h** | 最便宜的带 GPU 的 AWS 裸金属；Arm 会带来额外麻烦（驱动构建、Cloud Hypervisor 的 aarch64 支持）【推断】 | 同一份 JSON、[G5g 页](https://aws.amazon.com/ec2/instance-types/g5g/) |
| Hetzner GEX45 | 独立服务器，RTX PRO 4000 Blackwell SFF 24 GB，i5-13500，64 GB | **每月 €214 + €209 开通费**，或 **€0.3429/h** | 以固定成本做 4b；内核命令行和 IOMMU 都归你控制 | [Hetzner GPU matrix](https://www.hetzner.com/dedicated-rootserver/matrix-gpu/)、[新闻稿 2026-09-01](https://www.hetzner.com/pressroom/hetzner-expands-its-gpu-portfolio-with-the-gex45/) |
| Hetzner GEX63 / GEX131 | RTX PRO 6000 Blackwell Max-Q 96 GB | 每月 €999 / 每月 €1,499 起，开通费与月费相同 | 更大的模型；RTX PRO 6000 支持 MIG（最多 4 个） | 同上、[MIG GPUs](https://docs.nvidia.com/datacenter/tesla/mig-user-guide/supported-gpus.html) |

- **GEX44。** 它已经不在 Hetzner 的 GPU 价目表上（只有 GEX45、63、131、430）。Hetzner 的文档仍在服务器产品线里列着它（[Hetzner docs](https://docs.hetzner.com/robot/dedicated-server/server-lines/gpu-server/)）。
- 【推断】我没找到 Hetzner 讲 IOMMU 或 VT-d 的页面。论坛报告 **（二手）** 说有些 Hetzner 机器上 `/sys/kernel/iommu_groups` 是空的。决定租一个月之前，第一天就要检查：开 `intel_iommu=on`，看 GPU 的 IOMMU group 是否只包含 GPU 和它的音频 function，再把它绑到 `vfio-pci`。消费级主板（i5-13500）的分组可能很粗。
- 【推断】gVisor nvproxy 的官方列表里没有 Blackwell RTX PRO 系列，尽管 GKE Sandbox 已把 RTX PRO 6000 放进 preview。4a 选 T4 或 L4，Hetzner 留给 4b，那时 guest 里跑的是普通 NVIDIA 驱动。
- **配额。**
  - **AWS：** "Running On-Demand G and VT instances" 和 "All G and VT Spot Instance Requests" 的默认值都是 **0**（单位是 vCPU）；P 系列也是 0。`g4dn.metal` 需要 96 vCPU 的配额，`g5g.metal` 需要 64（[AWS quotas](https://docs.aws.amazon.com/ec2/latest/instancetypes/ec2-instance-quotas.html)）。
  - **GCP：** "New accounts and projects have a global GPU quota that applies to all regions"。既要按型号、按区域申请配额，*也* 要申请全局的 `GPUs (all regions)` 配额；而且 "If you're using a Free Trial Cloud Billing account, then you can't request a change to your quota"（[GCP quotas](https://docs.cloud.google.com/compute/resource-usage)）。页面没写默认值是多少。

---

## 3. 说法核实

| # | 说法（之前凭记忆提出） | 结论 | 来源 |
|---|---|---|---|
| 1 | "Firecracker 不支持 GPU；GPU passthrough 需要 Cloud Hypervisor 或带 VFIO 的 QEMU。" | **成立，但有一处细节过时了。** Firecracker 从 v1.13 起有可选的 PCI，VFIO 也在积极开发（#5679、`feature/vfio` 分支、用 NVMe 测过）。但还没有任何东西进入 release，GPU 既没测过，也没有时间表 | [CHANGELOG](https://github.com/firecracker-microvm/firecracker/blob/main/CHANGELOG.md)、[#5679](https://github.com/firecracker-microvm/firecracker/issues/5679)、[#6055](https://github.com/firecracker-microvm/firecracker/pull/6055)、[#1179](https://github.com/firecracker-microvm/firecracker/issues/1179)、[CH VFIO](https://github.com/cloud-hypervisor/cloud-hypervisor/blob/main/docs/vfio.md)、[QEMU](https://www.qemu.org/docs/master/devel/vfio-iommufd.html) |
| 2 | "云上的 GPU VM 已经把 GPU 透传进来了，所以不能再透传进嵌套 VM；需要能控制 IOMMU 的裸金属。" | **实际上成立，但原因可以说得更准确。** 嵌套分配需要 L1 里有 IOMMU，no-IOMMU 模式不能把设备分给 VM。AWS 的 GPU 系列都不提供嵌套虚拟化。GCP 没有文档说明提供 vIOMMU。原理上并非不可能（有 vIOMMU 就可以） | [VFIO Kconfig](https://raw.githubusercontent.com/torvalds/linux/master/drivers/vfio/Kconfig)、[AWS nested](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/amazon-ec2-nested-virtualization.html)、[GCP nested](https://docs.cloud.google.com/compute/docs/instances/nested-virtualization/overview) |
| 3 | "gVisor 通过 nvproxy 支持 NVIDIA GPU，Modal 在用。" | **成立。** 官方支持 T4、A100、A10G、L4、H100，驱动按白名单。Modal：GPU Sandbox 只能用 gVisor，Modal 2026 年的博文点名说自己在用 `nvproxy` 并给它贡献代码 | [gVisor GPU](https://gvisor.dev/docs/user_guide/gpu/)、[Modal sandboxes](https://modal.com/docs/guide/sandboxes.md)、[Modal blog](https://modal.com/blog/truly-serverless-gpus) |
| 4 | "GPU 交给新 Tenant 之前，必须清空显存并 reset GPU；这是个安全问题。" | **作为要求成立，但文档里没说这是自动完成的。** `vfio-pci` 在打开和释放时会 reset（FLR 或 SBR）。NVIDIA 只在 CC 模式下写明了固件会清洗内存。Firecracker 的维护者把 Tenant 切换列为障碍之一 | [vfio_pci_core.c](https://raw.githubusercontent.com/torvalds/linux/master/drivers/vfio/pci/vfio_pci_core.c)、[NVIDIA CC](https://developer.nvidia.com/blog/confidential-computing-on-h100-gpus-for-secure-and-trustworthy-ai/)、[#849](https://github.com/firecracker-microvm/firecracker/issues/849) |
| 5a | GCP `g2-standard-4` ≈ $0.7/h | **成立：** 按需 $0.706832/h（us-central1）；spot $0.423016/h | [GCP pricing](https://cloud.google.com/products/compute/pricing/accelerator-optimized) |
| 5b | AWS `g4dn.xlarge` ≈ $0.53/h | **成立：** $0.526/h（us-east-1） | [AWS price JSON](https://b0.p.awsstatic.com/pricing/2.0/meteredUnitMaps/ec2/USD/current/ec2-ondemand-without-sec-sel/US%20East%20(N.%20Virginia)/Linux/index.json) |
| 5c | AWS `g4dn.metal` ≈ $8/h（8× T4） | **成立：** $7.824/h，8× T4，96 vCPU。还有更便宜的裸金属选项：`g5g.metal`，$2.744/h（2× T4G，Arm） | 同上、[G4](https://aws.amazon.com/ec2/instance-types/g4/)、[G5g](https://aws.amazon.com/ec2/instance-types/g5g/) |
| 5d | Hetzner GEX44 ≈ 每月 €180 | **已不成立。** GEX44 不在当前价目表上。接替它的 GEX45 是每月 €214 加 €209 开通费（不含 VAT），或 €0.3429/h | [Hetzner matrix](https://www.hetzner.com/dedicated-rootserver/matrix-gpu/)、[新闻稿](https://www.hetzner.com/pressroom/hetzner-expands-its-gpu-portfolio-with-the-gex45/) |
| 6 | "新的 GCP 和 AWS 账号默认 GPU 配额是 0，必须申请提额。" | **AWS 成立**（G/VT 和 P 的配额默认都是 0 vCPU）。**GCP 无法从数值上确认。** 文档说新项目有一个全局 GPU 配额，但没写数值；而且 Free Trial 账号不能申请修改配额 | [AWS quotas](https://docs.aws.amazon.com/ec2/latest/instancetypes/ec2-instance-quotas.html)、[GCP quotas](https://docs.cloud.google.com/compute/resource-usage) |

---

## 4. 对本项目设计的影响（**本节全部为【推断】**）

按重要性排序。

1. **spec §9 第 4 阶段：拆成两部分，并改掉运行地点。**
   - **4a，在 Docker 和 gVisor 上做容器级 GPU：**
     - 硬件：一台 GCP `g2-standard-4`（spot 约 $0.42/h）或 AWS `g4dn.xlarge`（$0.526/h）。GPU 必须是 T4 或 L4，这样才在 nvproxy 的支持列表里。
     - 范围：这两个 Runtime 上的 `gpu` 字段变成真实可用，Host 上报 GPU 库存，测试工具用 GPU 负载重跑一遍。
     - 成本：约 20 个 GPU 小时，不到 $15。
   - **4b，用新增的 Cloud Hypervisor Runtime 做 VFIO passthrough：**
     - 硬件：租一个月 Hetzner GEX45（含开通费首月 €423），或在申请配额后按小时用 AWS `g5g.metal` / `g4dn.metal`。
     - Firecracker 保持不带 GPU。阶段表里的 "GCP g2" 只适用于 4a。
     - 在 "运行地点" 一栏加第四条说明：云上 GPU VM 里做不了 passthrough（见 §2.2）。
2. **GPU passthrough 设计文档**（spec 第 4 阶段的交付物）至少应包含：
   - (a) 每种模式的信任模型，用 §2.5 的表。写明容器级 GPU 让各 Tenant 共用 NVIDIA KMD，gVisor 覆盖不了驱动漏洞和 toolkit hook。
   - (b) Host 准备：开 IOMMU，审查 ACS 和 IOMMU group，常驻绑定 `vfio-pci`，为 GPU Sandbox 预留大页。
   - (c) Tenant 交接流程：
     - 释放 GPU，让 `vfio-pci` reset 它（记录用的是 FLR 还是 SBR），隔离起来；
     - 通过扫描 canary **验证** 没有数据残留；
     - 然后才在 Control Plane 里把它标为空闲。

     注明 NVIDIA 只在 CC 模式下写明了内存清洗。
   - (d) 按阶段的启动时间预算：调度、VFIO、内存 pin、guest 启动和 BAR 映射、驱动初始化、CUDA 初始化、权重。
   - (e) 为什么不用 Firecracker，引用 #849、#1179、#5679，并写明触发条件："VFIO 进入正式 release 后再评估"。
   - (f) 快照策略：不对 passthrough GPU 做 VMM 快照，而是用不带 GPU 的 VM 快照，接着热插拔 GPU，再在 guest 里用 `cuda-checkpoint` 恢复。
   - (g) 不在范围内的：MIG、vGPU、CC、多卡 P2P；每项写一段说明为什么。
3. **spec §8 验证：加入 GPU 隔离攻击和 GPU 启动时间分阶段。**
   - **攻击：**
     - 读同一块 GPU 上前一个 Sandbox 留下的显存；
     - 用 `nvidia-smi` 或 NVML 列出或查看其他 Tenant 的 GPU 进程；
     - 使用 `compute,utility` 以外的驱动 capability（`NVIDIA_DRIVER_CAPABILITIES=all`）；
     - toolkit hook 的回归测试：一个设置了 `LD_PRELOAD` 的镜像（CVE-2025-23266 的形态），在 Docker 和 gVisor 上都跑；
     - 在 4b 上从 guest 往外做 DMA（应被 IOMMU 挡住）。
   - **性能：** 把 "Runtime 启动" 拆成 VFIO 与内存 pin、guest 启动、`nvidia` 驱动就绪、首个 CUDA context、权重加载完成。分别测开与不开持久化模式、热插拔与启动时挂载、4K 页与 1 GiB 大页。
4. **GLOSSARY、Quota 和 Usage（spec §6 以及 GLOSSARY 的 "Quota"/"Usage"）。**
   - 把 **GPU** 加为 Quota 的第 7 个维度（GPU 总数，可选按型号）。
   - Usage 在 Running **和 Frozen** 状态都计 GPU 秒，因为冻结的 Sandbox 仍然占着 GPU，别人用不了。
   - Hibernated 只有在 CUDA 状态做过 checkpoint 时才释放 GPU。否则 GPU Sandbox 的 hibernate 应返回 "unsupported"，就像 Daytona v2 对 pause 的处理。
5. **候选 ADR："GPU Sandbox 只用整卡；不在 Tenant 之间做 time-slicing 或 MPS"。** 这直接来自 §2.5 和 GKE 自己的警告。以后可以把 MIG 作为第二种选项放开，但目前 nvproxy 和 CRIU CUDA 插件都不支持 MIG。
6. **候选 ADR："Cloud Hypervisor 是第四个 Runtime，只用于 GPU"。** 它保持 ADR 0002 不变，因为 Cloud Hypervisor 有 vsock，同一个 Guest Agent 照样适用。ADR 0003 要涵盖 guest 里 NVIDIA 驱动的注入：驱动必须和 guest 内核匹配，所以对这个 Runtime，guest 内核和驱动这一对应由平台负责，而不是由 Template 负责。它也会改变 ADR 0001 的容量上报：Host 要按型号、UUID 和 IOMMU group 上报 GPU，外加健康状态。
7. **spec §6 调度：内存超卖本来就关着，正好和 VFIO 一致。** VFIO 会 pin 住全部 guest 内存，所以 GPU Host 还必须预留大页，并对 GPU Sandbox 拒绝 balloon 和 virtio-mem。first-fit 只能把 GPU 请求放到有空闲且 *已验证干净* 的 GPU 的 Host 上。
8. **spec §7 安全模型：加一条驱动补丁规则和一条 CDI 规则。**
   - 按 gVisor 文档的要求，NVIDIA 驱动和 Container Toolkit 要有补丁 SLA。
   - 用 CDI 模式：CVE-2024-0132 不影响 CDI。
   - toolkit 版本锁定在修复 CVE-2025-23266 的版本或更高。
9. **spec §2 非目标：保留 "第一版不做 GPU" 这条非目标。** 但用一句话写明 *为什么*（Firecracker 做不了，而容器级 GPU 不是硬隔离），这样第 4 阶段读起来是有意为之的设计，而不是事后补上的。

---

## 5. 来源（均于 2026-11-21 访问）

**NVIDIA**
- Container Toolkit：architecture https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/arch-overview.html ；CDI https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/cdi-support.html ；install guide https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html
- GPU Operator sharing https://docs.nvidia.com/datacenter/cloud-native/gpu-operator/latest/gpu-sharing.html
- MIG https://docs.nvidia.com/datacenter/tesla/mig-user-guide/introduction.html ；supported GPUs https://docs.nvidia.com/datacenter/tesla/mig-user-guide/supported-gpus.html
- MPS https://docs.nvidia.com/deploy/mps/when-to-use-mps.html ；vGPU user guide https://docs.nvidia.com/vgpu/latest/grid-vgpu-user-guide/index.html
- nvidia-smi https://docs.nvidia.com/deploy/nvidia-smi/index.html ；driver persistence https://docs.nvidia.com/deploy/driver-persistence/background.html 、https://docs.nvidia.com/deploy/driver-persistence/persistence-daemon.html
- CUDA lazy loading https://docs.nvidia.com/cuda/cuda-programming-guide/04-special-topics/lazy-loading.html
- cuda-checkpoint https://github.com/NVIDIA/cuda-checkpoint ；博客 https://developer.nvidia.com/blog/checkpointing-cuda-applications-with-criu/
- Confidential Computing 博客 https://developer.nvidia.com/blog/confidential-computing-on-h100-gpus-for-secure-and-trustworthy-ai/ ；R595 release notes https://docs.nvidia.com/confidential-computing/release-notes/r595-trd1.md

**内核与 VMM**
- VFIO https://docs.kernel.org/driver-api/vfio.html ；Kconfig https://raw.githubusercontent.com/torvalds/linux/master/drivers/vfio/Kconfig ；vfio_pci_core.c https://raw.githubusercontent.com/torvalds/linux/master/drivers/vfio/pci/vfio_pci_core.c
- Cloud Hypervisor VFIO https://github.com/cloud-hypervisor/cloud-hypervisor/blob/main/docs/vfio.md ；hotplug https://github.com/cloud-hypervisor/cloud-hypervisor/blob/main/docs/hotplug.md ；snapshot/restore https://github.com/cloud-hypervisor/cloud-hypervisor/blob/main/docs/snapshot_restore.md
- QEMU iommufd https://www.qemu.org/docs/master/devel/vfio-iommufd.html
- Firecracker CHANGELOG https://github.com/firecracker-microvm/firecracker/blob/main/CHANGELOG.md ；releases https://github.com/firecracker-microvm/firecracker/releases ；issue #849 https://github.com/firecracker-microvm/firecracker/issues/849 、#1179 https://github.com/firecracker-microvm/firecracker/issues/1179 、#5679 https://github.com/firecracker-microvm/firecracker/issues/5679 ；PR #5870 https://github.com/firecracker-microvm/firecracker/pull/5870 、#6055 https://github.com/firecracker-microvm/firecracker/pull/6055
- CRIU CUDA 插件 https://github.com/checkpoint-restore/criu/tree/criu-dev/plugins/cuda
- VFIO 启动时间实测：Canonical 工程师博客 https://cpaelzer.github.io/blogs/007-vfio-slow-startup ；LKML 讨论串 https://lkml.iu.edu/hypermail/linux/kernel/2411.3/03532.html

**gVisor 与 GKE**
- GPU guide https://gvisor.dev/docs/user_guide/gpu/ ；users https://gvisor.dev/users/ ；GKE Sandbox https://docs.cloud.google.com/kubernetes-engine/docs/concepts/sandbox-pods

**安全**
- NVD API：CVE-2024-0132 https://services.nvd.nist.gov/rest/json/cves/2.0?cveId=CVE-2024-0132 ；CVE-2025-23266 https://services.nvd.nist.gov/rest/json/cves/2.0?cveId=CVE-2025-23266 ；CVE-2023-4969 https://services.nvd.nist.gov/rest/json/cves/2.0?cveId=CVE-2023-4969
- Wiz NVIDIAScape https://www.wiz.io/blog/nvidia-ai-vulnerability-cve-2025-23266-nvidiascape ；Trail of Bits LeftoverLocals https://blog.trailofbits.com/2024/01/16/leftoverlocals-listening-to-llm-responses-through-leaked-gpu-local-memory/

**厂商**
- Modal：sandboxes https://modal.com/docs/guide/sandboxes.md ；GPU https://modal.com/docs/guide/gpu.md ；security https://modal.com/docs/guide/security.md ；GPU memory snapshots https://modal.com/blog/gpu-mem-snapshots ；truly serverless GPUs https://modal.com/blog/truly-serverless-gpus
- Daytona v2：https://www.daytona.com/docs/sandboxes.md ；https://www.daytona.com/docs/gpu-sandboxes.md
- Fly.io：https://fly.io/blog/wrong-about-gpu/ ；社区帖（二手）https://community.fly.io/t/gpu-migration-fly-io-gpus-will-be-deprecated-as-of-july-31-2026/27110
- RunPod：https://docs.runpod.io/pods/overview

**云、价格与配额**
- GCP accelerator-optimized pricing https://cloud.google.com/products/compute/pricing/accelerator-optimized ；GCP quotas https://docs.cloud.google.com/compute/resource-usage ；GCP nested virtualization https://docs.cloud.google.com/compute/docs/instances/nested-virtualization/overview
- AWS 按需价格 JSON（us-east-1，Linux；manifest 发布日期 2026-11-21）https://b0.p.awsstatic.com/pricing/2.0/meteredUnitMaps/ec2/USD/current/ec2-ondemand-without-sec-sel/US%20East%20(N.%20Virginia)/Linux/index.json ；实例页 https://aws.amazon.com/ec2/instance-types/g4/ 、https://aws.amazon.com/ec2/instance-types/g5g/ 、https://aws.amazon.com/ec2/instance-types/g6/ ；配额 https://docs.aws.amazon.com/ec2/latest/instancetypes/ec2-instance-quotas.html ；嵌套虚拟化 https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/amazon-ec2-nested-virtualization.html
- Hetzner GPU matrix https://www.hetzner.com/dedicated-rootserver/matrix-gpu/ ；GEX45 新闻稿 https://www.hetzner.com/pressroom/hetzner-expands-its-gpu-portfolio-with-the-gex45/ ；server lines https://docs.hetzner.com/robot/dedicated-server/server-lines/gpu-server/

**时效性与不确定性说明**
- Firecracker 的 VFIO 工作推进很快（`feature/vfio`、#5679），开始 4b 之前要重新核对。
- CVE-2025-23266 的修复版本来自新闻报道，ACM Queue 里 PF-FLR 那句引文来自搜索摘要，两者都没读到一手原文。
- Fly.io 的 GPU 停止日期只出现在一篇用户论坛帖里。
- GCP 文档没写默认 GPU 配额是多少。
- RunPod 没有公开任何隔离细节。
