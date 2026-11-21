# 验证：基准测试、评测 harness 接入与隔离测试

> [English](verification-and-benchmarks.md) | 中文

> 调研日期：2026-11-21。下文所有链接均在这一天访问，除非另有说明。
> 只用一手来源：论文、官方文档、harness 源码、CVE 公告和厂商安全文档。厂商博客只在它说自己产品时算一手来源。凡是我自己测量的内容（镜像大小、任务数量），都会注明，并写出方法。
> 文中 **【推断】** 标出的是我自己的推论，不是来源原文。术语以 [GLOSSARY](../../GLOSSARY.zh.md) 为准。本文不重复 [agent-sandbox-usage.zh.md](agent-sandbox-usage.zh.md) 已经讲过的内容（各家宣传的冷启动数字和客户案例）。

---

## 1. 结论摘要

- **目前没有厂商中立、经过同行评审的 sandbox 服务商对比基准。** 唯一公开、可复现的跨服务商基准是 ComputeSDK 做的。它只测一个指标 "TTI"：在客户端从 `create()` 计时到第一次 `node -v` 成功，不含销毁。它只跑突发（burst）模式，排名用的是 median、p95、p99 的加权分再乘以成功率（[tti.bench.ts](https://github.com/computesdk/benchmarks/blob/master/benchmarks/sandbox/tti.bench.ts)、[scoring.ts](https://github.com/computesdk/benchmarks/blob/master/benchmarks/sandbox/scoring.ts)）。学术界的 runtime 对比（Firecracker NSDI'20、gVisor HotCloud'19、VEE'20、CLOSER'21）都已经是 5–7 年前的工作，用的软件版本后来变化很大。
- **"启动时间" 在每家指的都是不同的区间。** Firecracker 的定义是 InstanceStart → guest `/sbin/init`，用精简内核，并关闭串口控制台（[SPECIFICATION.md](https://github.com/firecracker-microvm/firecracker/blob/main/SPECIFICATION.md)）。E2B 的 "~80ms" 是加载一个进程已经在运行的快照（[how-it-works](https://docs.e2b.dev/template/how-it-works.md)）。Daytona 不说明测量条件。Modal 把 Created → Scheduled → Started → Ready 当作几个不同的事件，并指出基准测试只测到下限（[Modal 博客，2026-06-22](https://modal.com/blog/unpacking-sandbox-startup-latency)）。我们的 harness 需要自己写下定义，§9 给出了一套。
- **对我们来说，Harbor 上的 Terminal-Bench 2.0 是最好的兼容性测试，比 SWE-bench 更好。** Harbor 给自定义 sandbox 留了有文档的接入点：实现 `BaseEnvironment`（start/stop/exec/upload/download），再用 `-e module:Class` 传进去（[custom-sandboxes](https://github.com/harbor-framework/harbor/blob/main/docs-mintlify/sandboxes/custom-sandboxes.mdx)）。TB2 的 89 个任务都是单容器，都带预构建的 `docker_image`，也都有 oracle 解答，所以跑一遍 "oracle sweep" 就能检查环境是否一致，不用调用 LLM（数据是我对 [terminal-bench-2](https://github.com/harbor-framework/terminal-bench-2) 仓库的统计）。
- **SWE-bench harness 没有可插拔的后端。** 它通过 docker-py 直接驱动 Docker，唯一的替代是内置的 Modal 路径（[run_evaluation.py](https://github.com/SWE-bench/SWE-bench/blob/main/swebench/harness/run_evaluation.py)）。它以 `root` 身份、在 `/testbed` 里、带着 `cap_add=["SYS_ADMIN"]` 运行容器，和我们加固后的 Docker 默认配置冲突。镜像以 x86_64 为主，arm64 是 "experimental"（[README](https://github.com/SWE-bench/SWE-bench/blob/main/README.md)）。我抽样的实例镜像压缩后约 1.1–3 GB（我自己测的）。【推断】对我们来说，SWE-bench 适合当第二道检查：在固定的 50 个实例上跑 gold patch 和空 patch 两轮。
- **逃逸探测工具在 2026 年的状况参差不齐。** `amicontained` 实际上已经没人维护：最后一次发布是 2019 年，最后一次推送是 2020 年。`deepce` 还在维护，但它有些利用模式 "will overwrite runC which can be destructive"（[README](https://github.com/stealthcopter/deepce)）。CDK 维护得最活跃（v1.5.6，2026-02），有 `evaluate` 模式，也有 CVE-2019-5736 和 CVE-2022-0492 的 PoC（[CDK](https://github.com/cdk-team/CDK)）。
- **runc 的 CVE 适合做教学测试，而且我们的设计已经去掉了其中一条攻击路径。** CVE-2024-21626 的 "attack 2" 需要对攻击者控制的路径执行 `runc exec --cwd`（[GHSA-xr7r-f8xq-vfvv](https://github.com/opencontainers/runc/security/advisories/GHSA-xr7r-f8xq-vfvv)）。【推断】我们的 Exec 经过 Guest Agent，不走 `runc exec`，所以这条路在我们这里不存在。恶意镜像的变体（"attack 1"）仍然适用，因为 Tenant 可以用镜像地址注册私有 Template。gVisor 和 Firecracker 的信任边界内根本不运行 runc。
- **内存开销取决于你算什么。** RSS 会重复计算共享页，PSS 则把共享页按份摊开（[内核 proc 文档](https://docs.kernel.org/filesystems/proc.html)）。在 gVisor 下，应用内存显示为 shmem 而不是 anon（[gVisor 资源模型](https://gvisor.dev/docs/architecture_guide/resources/)）。Firecracker 出于安全考虑建议关闭 KSM，而 KSM 恰恰是能提高密度的机制（[prod-host-setup](https://github.com/firecracker-microvm/firecracker/blob/main/docs/prod-host-setup.md)）。gVisor 自己的密度基准绕开了这些记账差异：它测量启动 N 个 sandbox 前后主机的可用内存，再除以 N（[gVisor 性能指南](https://gvisor.dev/docs/architecture_guide/performance/)）。
- **从 agent 角度评估 sandbox 的方法只有零散的几种。** Harbor 的 adapter 用 parity 实验：同一个 agent 和模型在两个环境上重复运行，比较 mean ± SEM（[SWE-bench adapter README](https://github.com/harbor-framework/harbor/blob/main/adapters/swebench/README.md)）。Terminal-Bench 报告带 95% CI 的成功率，以及超时和执行时间（[arXiv:2601.11868](https://arxiv.org/abs/2601.11868)）。研究系统把 sandbox 的准备时间当作关键路径上的延迟（[SpecBox](https://arxiv.org/abs/2607.23933)）。我没有找到比较容器、gVisor、microVM 三者 agent 任务成功率的已发表研究。

---

## 2. 现有的 sandbox 和 runtime 基准（Q1）

### 2.1 ComputeSDK benchmarks：唯一的跨服务商基准

- **它是什么。** 一个 MIT 许可的仓库，最近一次推送在 2026-11-21，对 sandbox、浏览器、存储和 AI 网关服务商做基准测试。结果以排行榜形式发布在 computesdk.com（[仓库](https://github.com/computesdk/benchmarks)）。
- **"TTI" 测什么（来自源码）：** "TTI = sandbox create through the first command (`node -v`) succeeding, excluding destroy"。计时在基准进程里进行：调用 `compute.sandbox.create()` 之前记下 `createStart = performance.now()`，`runCommand('node -v')` 以退出码 0 返回后，算出 `ttiMs = createMs + (performance.now() - commandStart)`（[tti.bench.ts](https://github.com/computesdk/benchmarks/blob/master/benchmarks/sandbox/tti.bench.ts)）。创建超时 120 s，命令超时 30 s。
- **负载形态。** 只有突发模式："--concurrency N opens all slots at once"。文件里写着 "Sequential and staggered shapes were retired (2026-08-11)"。定时工作流每周五 13:00 UTC 运行一次，手动触发时默认 100 次迭代、并发 100（[workflow](https://github.com/computesdk/benchmarks/blob/master/.github/workflows/sandbox-tti-benchmarks.yml)）。
- **打分。** median、p95、p99 各自按 `100 × (1 − value/10 000 ms)` 打分，按 0.60/0.25/0.15 加权，结果再乘以成功率（[scoring.ts](https://github.com/computesdk/benchmarks/blob/master/benchmarks/sandbox/scoring.ts)）。
- **同仓库的其他套件。** "DAX" 负载在一个新 sandbox 里构建 OpenCode，分别计时 prepare/download/clone/install/typecheck（[dax.bench.ts](https://github.com/computesdk/benchmarks/blob/master/benchmarks/sandbox/dax.bench.ts)）。可靠性套件在 sandbox 里安装并运行 OpenCode agent CLI（[sandbox-opencode.bench.ts](https://github.com/computesdk/benchmarks/blob/master/benchmarks/reliability/sandbox-opencode.bench.ts)）。`.computesdk/workflows/stealth-*` 下还有快照、pause-resume、CRIU 等工作流。
- **注意事项。**
  - 【推断】ComputeSDK 是一家卖多服务商 SDK 的公司，仓库里还有它自己的 Firecracker 主机服务商（`ec2-firecrab`）。这个基准是开放、可复现的，但并不独立。
  - 【推断】TTI 是从 CI runner 上测的，所以包含 runner 到服务商之间的网络 RTT 和地域距离。它也分不出是内核启动还是快照恢复。
  - 【推断】只有 100 个样本时，p99 基本上就是最大值或第二大的值。

### 2.2 学术界的 runtime 对比

| 研究 | runtime 与版本 | 测了什么 | 文中说明的方法 | 状态 |
|---|---|---|---|---|
| Agache 等，"Firecracker"，NSDI'20（[论文](https://www.usenix.org/system/files/nsdi20-paper-agache.pdf)、[数据](https://github.com/firecracker-microvm/nsdi2020-data)） | Firecracker、Cloud Hypervisor、QEMU；guest 内核 4.14.94；宿主 4.15 | 启动时间；内存开销；IO | 启动时间 = "time between when VMM process is forked and the guest kernel forks its init process"，init 极简，只往一个 IO 端口写一下，并修改各 VMM，让它在这次写入时 `exit()`。串行 500 个样本；1000 个 VM、50 个并发。"end-to-end"（含 API 配置）和 "pre-configured" 分开报告。内存开销 = `pmap` 里的非共享段，不含 VMM 二进制本身。 | 内核和 VMM 版本较旧；方法仍然成立 |
| Young 等，"The True Cost of Containing: A gVisor Case Study"，HotCloud'19（[论文](https://www.usenix.org/system/files/hotcloud19-paper-young.pdf)） | runc 对比 runsc（ptrace、KVM）；Ubuntu 16.04，内核 4.4，Docker 18.09 | 容器生命周期、系统调用、malloc、网络、文件打开、Python import | 生命周期测量要跑 "long enough to achieve steady state since prior work shows container destruction has significant asynchronous overheads"。串行创建加销毁：runc 1.014 s，runsc-KVM 1.117 s。 | **已过时。** gVisor 现在推荐 Systrap platform（[性能指南](https://gvisor.dev/docs/architecture_guide/performance/)），并且有了 directfs（[安全模型](https://gvisor.dev/docs/architecture_guide/security/)）。 |
| Anjali、Caraza-Harter、Swift，"Blending Containers and Virtual Machines: A Study of Firecracker and gVisor"，VEE'20（[论文](https://pages.cs.wisc.edu/~swift/papers/vee20-isolation.pdf)） | LXC、gVisor 20200127（KVM 模式）、Firecracker v0.19.1；宿主 5.4 | 宿主内核代码覆盖率，以及 CPU、网络、内存、IO 性能 | 用 gVisor 的 KVM 模式，并指出 "the official performance guide of gVisor uses ptrace mode … so many of our performance results are different"。IO 测试分别清空 guest 内、宿主上或两边的缓存；"a warm cache within the microVM results in nearly double the throughput as a warm cache in the host"。 | 版本**已过时**；覆盖率方法这个思路仍然有用 |
| Li 等，"Comparative Performance Study of Lightweight Hypervisors Used in Container Environment"，CLOSER'21（[论文](https://www.scitepress.org/Papers/2021/104405/104405.pdf)） | Docker、gVisor（ptrace、KVM）、Kata 1.11（QEMU、Firecracker v0.21.1）、QEMU/KVM | 启动、内存占用、系统调用、网络、IO、nginx、MySQL | 启动：用 `time` 从启动计时 "to the stage of the network stack being successfully initialized"，100 轮。内存 = 100 个容器的平均 private dirty 页，并注明没有计入共享的 `dockerd`（约 40 MB）。超线程开启。 | **已过时**（2020 年的软件） |

### 2.3 项目自己维护的测试套件（可以当我们 harness 的模板）

- **Firecracker CI 性能测试。**
  - `test_boottime.py` 读取一个 "Guest-boot-time" 值，这个值由 boot-timer 设备写出，guest 在 `init` 里触发它。它记录 `time_to_ssh_up`、`build_time`、`resume_time`，以及 `systemd-analyze` 的内核/用户态拆分，每种（vCPU、内存、大页、block/pmem）配置跑 10 次（[test_boottime.py](https://github.com/firecracker-microvm/firecracker/blob/main/tests/integration_tests/performance/test_boottime.py)）。
  - `test_snapshot.py` 把**恢复延迟**（30 次迭代，内存从 128 MiB 到 12 GiB）和**恢复后延迟**分开测。后者是恢复之后内存页按需缺页载入的代价，分有无 UFFD handler 两种情况（[test_snapshot.py](https://github.com/firecracker-microvm/firecracker/blob/main/tests/integration_tests/performance/test_snapshot.py)）。
  - `test_memory_overhead.py` 用 `psutil` 汇总 Firecracker 进程的内存映射，减去 guest 内存区域，同时报告 `size` 和 `rss`，另报 `uss`（[test_memory_overhead.py](https://github.com/firecracker-microvm/firecracker/blob/main/tests/integration_tests/performance/test_memory_overhead.py)）。
- **gVisor `perf.py`。**
  - `startup` 计时 Docker 启动容器，直到第一个请求到达绑定的端口。
  - `density` 在启动大量容器前后读取主机可用内存，再除以容器数，"because some runtimes don't use individual container cgroups"。
  - 指南默认用 ptrace，并称它是 "the highest structural costs by far" 的 platform；测试主机是 GCE n1-standard-4 上的 Debian 9（[性能指南](https://gvisor.dev/docs/architecture_guide/performance/)）。宿主系统**已过时**；页面没有日期，也没有版本。

### 2.4 来源指出的方法论陷阱

| 陷阱 | 来源 | 应对（除非另注，均为【推断】） |
|---|---|---|
| end-to-end 启动和 pre-configured 启动相差几十 ms | Firecracker NSDI'20 图 5 | 两个都报：经 API 的创建，和 Runtime 内部的启动 |
| 内核配置决定启动时间：Ubuntu 自带内核 "takes an additional 900ms"；串口控制台日志最多多花 70 ms；加一个网卡约多 20 ms | Firecracker NSDI'20 §5.1 | 记录 guest 内核配置和启动参数 |
| 宿主内核 6.1 让 `KVM_CREATE_VM` 变慢，程度取决于 cgroup 版本和 CPU | [prod-host-setup](https://github.com/firecracker-microvm/firecracker/blob/main/docs/prod-host-setup.md) | 记录宿主内核、cgroup 版本和漏洞缓解设置 |
| 快照恢复在内存页载入之前就返回了 | Firecracker `test_snapshot.py` | 测 "恢复到第一次 Exec"，不只测恢复调用本身 |
| 销毁是异步的，串行的创建/销毁循环会互相干扰 | HotCloud'19 §3.1 | 在稳态下测量，销毁单独计时 |
| 缓存位置（guest 还是宿主的 page cache）会让 IO 结果相差约 2 倍 | VEE'20 §7 | 写明缓存状态；"冷" 测试前清空缓存 |
| gVisor 的 platform 选择对结果影响很大 | VEE'20 §3；gVisor 指南 | 记录 platform（systrap/KVM/ptrace）和是否启用 directfs |
| 镜像大小决定容器和 Kata 的启动时间 | CLOSER'21 §3.1.1 | 把镜像拉取/解包阶段从 Runtime 启动中拆出来 |
| 共享守护进程（dockerd 约 40 MB）没有算进单个容器的内存 | CLOSER'21 §3.1.2 | 单个 Sandbox 的内存和每台 Host 的固定开销分开报告 |
| 突发、串行、错开三种负载给出的分布不同 | ComputeSDK 停用了其中两种；Firecracker 分别报告串行和并发 | 串行和突发都跑；每个数字都写明来自哪种负载 |
| 闭环负载生成器会掩盖排队延迟（"coordinated omission"） | [wrk2 README](https://github.com/giltene/wrk2) | 测吞吐时用开环的到达时间表，从计划发送时刻开始计时 |
| 基础设施故障被算成任务失败 | [SWE-bench infra_failure.py](https://github.com/SWE-bench/SWE-bench/blob/main/swebench/harness/infra_failure.py) | 打分前先把失败分类：环境 / 不确定 / 任务 |

---

## 3. 各家如何定义 "启动时间"（Q2）

| 谁 | 数字 | 原文给出的区间和条件 | 来源 |
|---|---|---|---|
| Firecracker | ≤125 ms | "from receiving the Firecracker InstanceStart API call to the start of the Linux guest user-space `/sbin/init` process"，1 vCPU / 128 MiB，关闭串口控制台，"minimal kernel and root file system"，在 m5d.metal（关闭 SMT）或 m6g.metal 上 | [SPECIFICATION.md](https://github.com/firecracker-microvm/firecracker/blob/main/SPECIFICATION.md) |
| Firecracker | 8 CPU ms（实际时间 6–60 ms） | VMM 进程启动 "up to API socket availability" | 同上 |
| Firecracker CI | （指标） | boot-timer 设备给出的 `guest_boot_time`；`time_to_ssh_up`；快照 `latency` 取自 `load_snapshot` 指标；恢复后的缺页延迟单独测量 | [test_boottime.py](https://github.com/firecracker-microvm/firecracker/blob/main/tests/integration_tests/performance/test_boottime.py)、[test_snapshot.py](https://github.com/firecracker-microvm/firecracker/blob/main/tests/integration_tests/performance/test_snapshot.py) |
| E2B | ~80 ms | "load the sandbox in ~80ms any time later … with all the processes already running"。这是在 start command 和 ready check 跑完之后拍下的 template 快照。页面没有说计时从哪里开始、到哪里结束。 | [how-it-works](https://docs.e2b.dev/template/how-it-works.md) |
| Daytona | <90 ms；"Sandbox Creation" 71 ms | "from code to execution"。2025-04-28 发布文章的表格（Creation 71 / Execution 67 / Cleanup 59 / Total 197 ms）只说 "measured under typical agentic workloads"。v2 文档："created in under a second, because their runner resumes a pre-built memory snapshot"。 | [Daytona Cloud 发布文章](https://www.daytona.io/dotfiles/introducing-daytona-cloud-the-agent-native-infrastructure)、[v2 architecture](https://www.daytona.com/docs/architecture.md) |
| Modal | "under one second" | 区分 Created（还没分配资源）、Scheduled（已分配给某个 worker）、Started（"container is live, the entrypoint process is running, and network tunnels and Volume mounts are active"）和 Ready（应用初始化完成）。`Sandbox.create` 是异步的。推荐用 readiness probe 和预热池。 | [Modal 博客，2026-06-22](https://modal.com/blog/unpacking-sandbox-startup-latency)、[products/sandboxes](https://modal.com/products/sandboxes) |
| ComputeSDK | （实测） | 客户端 `create()` → 第一次成功的 `node -v`，不含销毁 | [tti.bench.ts](https://github.com/computesdk/benchmarks/blob/master/benchmarks/sandbox/tti.bench.ts) |

【推断】这些厂商数字彼此不可比，和 Firecracker 的规格也不可比。E2B 和 Daytona v2 的数字是恢复一个进程已经在运行的快照。Firecracker 的数字是用调过的内核冷启动，而且不含 VMM 配置。Modal 的 "Ready" 包含我们控制不了的应用层工作。我们建议的定义见 §9.1。

---

## 4. SWE-bench 接入（Q3）

### 4.1 harness 怎么跑一次评测

- **镜像。** 分三层：一个基础镜像、约 60 个环境镜像、每个任务一个实例镜像。缓存级别是在磁盘和速度之间权衡：`instance` 级缓存需要约 2,000 GB，默认的 `env` 级约 100 GB。文档建议至少 120 GB 可用磁盘、16 GB 内存、8 个 CPU（[docker_setup.md](https://github.com/SWE-bench/SWE-bench/blob/main/docs/guides/docker_setup.md)）。
- **容器设置。** harness 为每个实例用 `test_spec.image` 创建一个容器。它以 `user=CONTAINER_USER`（即 `root`）身份运行，工作目录是 `/testbed`，`command="tail -f /dev/null"`，并且 `cap_add=["SYS_ADMIN"]`，理由是 "Docker's default seccomp profile only permits CLONE_NEWUSER with CAP_SYS_ADMIN, which browser sandboxes need"（[run_evaluation.py](https://github.com/SWE-bench/SWE-bench/blob/main/swebench/harness/run_evaluation.py)、[image_builder/constants](https://github.com/SWE-bench/SWE-bench/blob/main/swebench/image_builder/constants/__init__.py)）。
- **评测步骤。** harness 把 patch 拷进容器，依次尝试 `git apply --verbose`、`--3way`、`--reject`，最后是 `patch --fuzz=5`。然后运行 `eval.sh`，解析测试日志，对照该实例的 `FAIL_TO_PASS` 和 `PASS_TO_PASS` 列表打分。`skip_patch` 是内置的 "negative test mode"（[run_evaluation.py](https://github.com/SWE-bench/SWE-bench/blob/main/swebench/harness/run_evaluation.py)）。
- **结果缓存和 v5 CLI 的变化。** 结果按 `run_id` + `instance_id` 缓存，不看 patch 内容。v5 CLI 从单独的 `swe-bench-tasks` 仓库构建镜像，ARM Mac 上文档推荐用 `--task-repo`（[README](https://github.com/SWE-bench/SWE-bench/blob/main/README.md)）。

### 4.2 架构与镜像大小

- **架构支持。** README 推荐 `x86_64`，并说 "Support for `arm64` machines is experimental"（[README](https://github.com/SWE-bench/SWE-bench/blob/main/README.md)）。镜像名是 `sweb.eval.{x86_64|arm64}.{instance_id}`（[image_spec.py](https://github.com/SWE-bench/SWE-bench/blob/main/swebench/image_builder/image_spec.py)）。Epoch AI 的优化镜像仓库构建了 2,294 个 x86_64 镜像中的 2,290 个。arm64 提供 1,819 个，"on a best-effort basis, and have not been tested"（[Epoch，2025-07-10](https://epoch.ai/blog/swebench-docker)）。
- **大小。** Epoch 报告：全部 2,290 个镜像优化前 684 GiB，优化后 67 GiB；Verified（500 个）从 189 GiB 降到 30 GiB（[Epoch](https://epoch.ai/blog/swebench-docker)）。
- **我自己的测量**（Docker Hub registry API，2026-11-21）。我抽样的官方 `swebench/sweb.eval.x86_64.*` 实例镜像都是只有 amd64 的 OCI index，压缩后 1.09–3.07 GB：
  - django-11099：1,152 MB
  - sympy-20590：1,089 MB
  - astropy-12907：1,134 MB
  - scikit-learn-13142：1,481 MB
  - matplotlib-23314：3,072 MB
  - sphinx-8595：1,098 MB

  Docker Hub 上 `swebench` 命名空间共有 4,503 个仓库，其中 281 个名字匹配 "arm64"。

### 4.3 能不能对接自定义 sandbox 后端？

- **SWE-bench harness 本身：没有通用接口。** 它直接调用 docker-py。唯一的替代是 `--modal true`，这条路 "builds its own images remotely"，而且不能和 `--task-repo` 一起用（[run_evaluation.py](https://github.com/SWE-bench/SWE-bench/blob/main/swebench/harness/run_evaluation.py)、[evaluation guide](https://github.com/SWE-bench/SWE-bench/blob/main/docs/guides/evaluation.md)）。
- **SWE-ReX（agent 侧）。**
  - 扩展点是 `AbstractDeployment`（`start`、`stop`、`is_alive`、`runtime`）和 `AbstractRuntime`（`create_session`、`run_in_session`、`execute`、`read_file`、`write_file`、`upload`）。已有 Docker、Modal、Fargate、Daytona 和 "remote" 几种 deployment（[SWE-ReX 源码](https://github.com/SWE-agent/SWE-ReX/tree/main/src/swerex)）。
  - remote 类的 deployment 在 sandbox 里跑一个 `swerex-remote` 服务，通过 HTTP 和它通信（[README](https://github.com/SWE-agent/SWE-ReX)）。【推断】对我们来说，这意味着要用 Exposed Port，或者直接基于 Exec API 实现一个 `AbstractRuntime`。
- **mini-swe-agent（agent 侧）。** environment 是一个很小的类，只有 `execute(action, cwd, timeout) -> dict`，已经实现了 local、Docker、Singularity、bubblewrap 和 SWE-ReX Docker/Modal 几种（[environments](https://github.com/SWE-agent/mini-swe-agent/tree/main/src/minisweagent/environments)）。【推断】它和我们的同步 Exec 一一对应，是代价最小的 agent 接入方式。
- **Harbor 的 SWE-bench adapter（两侧都有）。**
  - 它把 SWE-bench Verified 转换成 Harbor 任务。任务的 Dockerfile 是 `FROM {docker_image}` 再加一步 `curl … uv … install`，`tests/test.sh` 运行一个由 `uv` 管理的解析脚本，导入 `swebench==4.0.3` 的打分函数（[adapter](https://github.com/harbor-framework/harbor/tree/main/adapters/swebench)、[test.sh 模板](https://github.com/harbor-framework/harbor/blob/main/adapters/swebench/src/swebench_adapter/task-template/tests/test.sh)）。
  - 【推断】所以它需要构建镜像，verifier 运行时也需要访问 PyPI。

### 4.4 最小的可信子集

- **官方子集。** 按 Hugging Face 的数据，Lite 有 300 个 test 实例和 23 个 dev 实例（[dataset](https://huggingface.co/datasets/SWE-bench/SWE-bench_Lite)）。仓库文档自相矛盾：`datasets.md` 写 534，`faq.md` 写 300（[datasets.md](https://github.com/SWE-bench/SWE-bench/blob/main/docs/guides/datasets.md)、[faq.md](https://github.com/SWE-bench/SWE-bench/blob/main/docs/faq.md)）。Verified 有 500 个（[dataset](https://huggingface.co/datasets/SWE-bench/SWE-bench_Verified)）。
- **SWE-bench Verified Mini。** 50 个实例，"approximately the same distribution of performance, test pass rates and difficulty as the original dataset"，并且 "requires 5GB instead of 130GB of storage"（[HF 卡片](https://huggingface.co/datasets/MariusHobbhahn/swe-bench-verified-mini)）。这是社区做的子集，选样的说法来自作者本人。
- 【推断】*sandbox* 兼容性测试不是在测模型，所以不需要模型分数上的统计功效，需要的是不同 Runtime 之间逐个实例的一致性。Verified Mini（50 个）是固定、公开、基于 Verified 的最小子集。Lite 现在用处没那么大了，因为 Epoch 和 Harbor 的工具都以 Verified 为目标。

### 4.5 怎么检查 "和官方 Docker 环境一致"

- **Harbor 的 parity 方法。**
  - Harbor 验证 adapter 的方式是：同一个 agent 和模型在两个 harness 上各跑一遍，比较解决率。SWE-bench adapter 的结果：重跑 3 次，mean ± SEM 为 0.545 ± 0.007，官方是 0.563，基于 499 个任务（[adapter README](https://github.com/harbor-framework/harbor/blob/main/adapters/swebench/README.md)）。
  - 同一份 README 列出了 "even with the Oracle agent" 也会失败的任务，例如 `astropy__astropy-8872`、`django__django-10097`。
  - 它还报告了一个由环境引起的失败：`scikit-learn__scikit-learn-14710` "Passes locally; times out on Daytona"，因为 verifier "is effectively single-core and CPU-bound"。
- **【推断】建议我们的检查方式：**
  1. 在官方 harness + 普通 Docker 上，对每个实例跑 **gold patch** 和**空 patch**（即 harness 的 `skip_patch` 模式），得到基线集合 R₀（gold 下解决的）和 N₀（空 patch 下必须失败的）。
  2. 通过我们的公共 API，在每个 Runtime 上跑同样的两轮。
  3. 报告相对基线的逐实例混淆矩阵，而不只是一个百分比。
  4. 每个结果不一致的实例重跑 3 次，归为三类：flaky（在 Docker 上也会翻转）、Runtime 引起、资源引起（超时，参照 Harbor 的 scikit-learn 案例）。
  5. 两种资源设置都报告："和官方运行相同的 vCPU/内存"，以及我们的默认规格。

---

## 5. Terminal-Bench 与 Harbor（Q4）

### 5.1 任务怎么打包

- **任务结构。** Harbor 任务是一个目录，包含 `instruction.md`、`task.toml`、`environment/`（Dockerfile、docker-compose.yaml 或其他规格）、`solution/solve.sh` 和 `tests/test.sh`。verifier 必须写出 `/logs/verifier/reward.txt` 或 `reward.json`（[task overview](https://github.com/harbor-framework/harbor/blob/main/docs-mintlify/tasks/overview.mdx)、[verifier](https://github.com/harbor-framework/harbor/blob/main/docs-mintlify/tasks/verifier.mdx)）。
- **预构建镜像。** 有了 `[environment].docker_image`，服务商可以跳过构建："most providers prefer the prebuilt image over building"（[environment](https://github.com/harbor-framework/harbor/blob/main/docs-mintlify/tasks/environment.mdx)）。
- **Terminal-Bench 2.0。** 从 229 个投稿任务中选出 89 个，用 `harbor run -d terminal-bench@2.0` 运行。论文的实验用 Daytona，"between 32 and 100 containers in parallel"，结果带 95% CI（[arXiv:2601.11868](https://arxiv.org/abs/2601.11868)）。
  - 论文允许 agent 访问互联网。
  - 论文指出了残留的不确定性，来源包括 "hardware differences (e.g., CPU architectures)" 和 "container runtime enforcement"。
- **我对 `harbor-framework/terminal-bench-2` 的统计**（2026-11-21）：
  - 89 个任务都设了 `docker_image`，也都是 `allow_internet = true`。
  - 没有一个用 docker-compose。
  - CPU：84 个任务 × 1，3 个 × 2，2 个 × 4。
  - 内存：69 个任务 × 2 GB，17 个 × 4 GB，3 个 × 8 GB。
  - GPU：0。
  - agent 超时从 600 s 到 12,000 s，有 2 个任务超过 3,600 s。
  - 89 个镜像都**只有 amd64**。压缩后大小：中位数 149 MB，p90 614 MB，最大 8.8 GB；总计 49 GB，共享层在每个镜像里各算一次。
  - 已有后续版本 [terminal-bench-2-1](https://github.com/harbor-framework/terminal-bench-2-1)（90 个任务）。

### 5.2 可插拔的 sandbox 服务商

- **内置 environment。** Harbor 自带约 40 种 environment，包括 `docker`（默认）、`podman`、`daytona`、`e2b`、`modal`、`runloop`、`vercel`、`gke`。还有一个本地 `kata` environment，它的 docstring 注明镜像构建在 runc 下进行，且不支持网络策略（[pre-integrated sandboxes](https://github.com/harbor-framework/harbor/blob/main/docs-mintlify/sandboxes/pre-integrated-sandboxes.mdx)、[kata.py](https://github.com/harbor-framework/harbor/blob/main/src/harbor/environments/kata.py)）。
- **自定义 environment。**
  - "Implement `BaseEnvironment` to add one"，再用 `-e my_sandbox:MySandbox` 运行。必须实现的方法是 `start`、`stop`、`exec(command, cwd, env, timeout_sec, user)`、`upload_file/dir`、`download_file/dir`，可选实现检查凭据的 `preflight()`（[custom-sandboxes](https://github.com/harbor-framework/harbor/blob/main/docs-mintlify/sandboxes/custom-sandboxes.mdx)）。
  - 文档要求："Declare only capabilities the provider enforces"。能力标志包括 `disable_internet`、`network_allowlist`、`network_allowlist_hostnames`、`docker_compose`（[capabilities.py](https://github.com/harbor-framework/harbor/blob/main/src/harbor/environments/capabilities.py)）。
- **oracle 和 nop agent。** `oracle` 运行 `solution/solve.sh`；`nop` 什么都不做（[agents](https://github.com/harbor-framework/harbor/tree/main/src/harbor/agents)）。【推断】oracle sweep 是阳性对照，nop sweep 是阴性对照，都不需要 LLM。
- **两种 agent 位置都覆盖。** "installed" 类 agent（Claude Code、Codex 等）跑在 sandbox 里面；Terminus-2 通过 `exec` 驱动一个 tmux 会话，并自己安装 tmux/asciinema（[agents/installed](https://github.com/harbor-framework/harbor/tree/main/src/harbor/agents/installed)、[terminus_2](https://github.com/harbor-framework/harbor/tree/main/src/harbor/agents/terminus_2)）。
- **gVisor 兼容性缺口的证据。** Harbor 的 Modal environment 写着："gVisor Modal sandboxes lack netlink permissions for creating veth pairs"，所以在 sandbox 里跑 Docker 需要用宿主网络。Modal 对只用 CPU 的 Compose 任务默认改用自己的 VM runtime（[modal.py](https://github.com/harbor-framework/harbor/blob/main/src/harbor/environments/modal.py)）。

**【推断】和我们平台的契合度。**
- **契合的地方。**
  - 单容器、预构建镜像，符合 ADR 0003（"任何 OCI 镜像都能原样注册"）。
  - Harbor 的 `exec` 签名和我们的 Exec 参数一致（user、cwd、env、timeout）。
  - 它的网络能力模型能对应上我们的 Egress Policy 三种模式。
- **不契合的地方。**
  1. **agent 超时超过 Exec 的 1 小时上限。** 有 2 个任务超过，所以 adapter 必须把长时间的 agent 运行改用 Process 启动。
  2. **网络。** 所有 TB2 任务都默认能上网，所以兼容性测试要用 "完全放开" 或范围较宽的白名单策略。这本身也是在真实负载下检验 Egress Proxy 的好机会。
  3. **架构。** 镜像只有 amd64，所以兼容性测试要放在 x86_64 主机上（第 3 阶段，GCP），spec §8 本来就是这么写的。
  4. **构建。** 对于没有预构建镜像的任务，adapter 要在客户端构建镜像再推到 registry，因为平台上构建镜像是非目标。

---

## 6. 隔离测试工具、威胁模型与 CVE（Q5）

### 6.1 2026 年的逃逸探测工具

| 工具 | 维护状况（GitHub，2026-11-21） | 实际检查什么 | 注意 |
|---|---|---|---|
| [amicontained](https://github.com/genuinetools/amicontained) | 最后发布 v0.4.9，2019-11；最后推送 2020-12 | 检测到的 runtime、namespaces、user-ns 映射、AppArmor profile、capability 集合、seccomp 模式，以及逐个尝试得出的被拦截系统调用列表 | runtime 检测是一张固定列表，里面有 `gvisor`，没有任何 microVM 条目（[bpfd proc.go](https://github.com/genuinetools/bpfd/blob/master/proc/proc.go)）。【推断】在 Firecracker 里它应该报 `not-found`，这正好是正确的教学结果。 |
| [deepce](https://github.com/stealthcopter/deepce) | 最后推送 2025-12；没有 release | 枚举：容器 ID、IP/DNS、Docker 版本、可疑挂载、文件里的密码、环境变量、其他容器、对宿主的端口扫描、暴露的 docker.sock。利用：docker 组、特权模式、暴露的 socket。 | "some exploits will overwrite runC which can be destructive"。只用枚举功能。 |
| [CDK](https://github.com/cdk-team/CDK) | v1.5.6，2026-02；最后推送 2026-05 | `cdk evaluate`：capabilities、挂载、网络 namespace、敏感的环境变量/文件/进程、云 metadata API、K8s API。利用模块包括 runc CVE-2019-5736、containerd-shim CVE-2020-15257、docker.sock、mount-cgroup、CVE-2022-0492 user-ns 滥用、procfs、ptrace、devices.allow | 它是一套攻击工具。默认只跑 `evaluate`，利用模块只在一次性 VM 里跑。 |
| [docker-bench-security](https://github.com/docker/docker-bench-security) | 最后发布 2023-12；推送 2026-06 | 宿主和守护进程的 CIS 检查，在宿主上运行 | 属于宿主侧工具，不是 sandbox 内的探测 |

### 6.2 攻击清单应对照的官方威胁模型

- **Docker。** 四个方面：内核 namespaces 和 cgroups、"attack surface of the Docker daemon"、容器配置 profile 的漏洞、内核加固特性。两句关键原文："only trusted users should be allowed to control your Docker daemon"；cgroups 确保 "a single container cannot bring the system down by exhausting one of those resources"。capabilities 采用 "an allowlist instead of a denylist approach"（[Docker security](https://docs.docker.com/engine/security/)）。
- **gVisor。**
  - 首要目标是 "minimize the System API attack vector"。"No system call is passed through directly to the host"。Sentry "is not permitted to open new files, create new sockets…"，例外是启用了宿主网络或 **directfs** 的情况。资源耗尽依靠宿主 cgroups 防护，并且 "does not provide protection against hardware side channels"（[gVisor 安全模型](https://gvisor.dev/docs/architecture_guide/security/)）。
  - 一个实例：gVisor 不受 CVE-2020-14386 影响，因为它没有实现 `PACKET_RX_RING`，而且除非传了 `--net-raw`，raw socket 默认是关闭的（[gVisor 博客，2020-09-18](https://gvisor.dev/blog/2020/09/18/containing-a-real-vulnerability/)）。
- **Firecracker。**
  - "all vCPU threads are considered to be running malicious code as soon as they have been started"。"Firecracker does not perform any network traffic filtering"，所以出网流量必须在宿主上过滤（[design.md](https://github.com/firecracker-microvm/firecracker/blob/main/docs/design.md)）。
  - 生产主机应使用 jailer、cgroups 和 seccomp，关闭 swap，关闭 SMT，关闭 KSM（"side channel issues that rely on page deduplication"），并使用带 Rowhammer 缓解的内存（[prod-host-setup](https://github.com/firecracker-microvm/firecracker/blob/main/docs/prod-host-setup.md)）。

### 6.3 适合做教学测试的容器逃逸 CVE

| CVE | 机制 | Docker（runc） | gVisor | Firecracker（我们的设计） | 能否安全复现？ |
|---|---|---|---|---|---|
| [CVE-2019-5736](https://nvd.nist.gov/vuln/detail/CVE-2019-5736) | 容器里的 root 通过 `/proc/self/exe` 覆写宿主的 runc 二进制。NVD 说攻击者需要 "a new container with an attacker-controlled image"，或者一个自己有写权限、之后会被 exec 进入的已有容器。 | "runc through 1.0-rc6, as used in Docker before 18.09.2" | 【推断】不适用：没有 runc，`/proc/self/exe` 解析在 Sentry 的 VFS 内部 | 【推断】不适用：宿主路径上没有 runc | 只能在一次性 VM 里、固定旧版 Docker 复现；CDK 有 `runc-pwn`。【推断】恶意镜像这个变体对应我们的私有 Template。 |
| [CVE-2024-21626](https://github.com/opencontainers/runc/security/advisories/GHSA-xr7r-f8xq-vfvv) "Leaky Vessels" | 泄漏了指向宿主 `/sys/fs/cgroup` 的 fd。Attack 1：镜像的 `WORKDIR` 是指向 `/proc/self/fd/7` 的符号链接。Attack 2：`runc exec --cwd` 进入一个被换成符号链接的路径。Attack 3a/3b：覆写宿主二进制。 | runc ≤1.1.11 受影响；1.1.12 修复 | 【推断】不适用 | 【推断】不适用 | 可以，在一次性 VM 里用 runc 1.1.11。Attack 1 对应*我们的私有 Template*。【推断】Attack 2 不对应，因为我们 Exec 的 cwd 由 Sandbox 内的 Guest Agent 解析。 |
| [CVE-2025-31133](https://github.com/opencontainers/runc/security/advisories/GHSA-9493-h29p-rfm2)、[-52565](https://github.com/opencontainers/runc/security/advisories/GHSA-qw9x-cqr3-wc7r)、[-52881](https://github.com/opencontainers/runc/security/advisories/GHSA-cgrx-mc8f-2prm)（2025-11） | maskedPaths 的 `/dev/null`、`/dev/console` 挂载竞争，以及 procfs 写入重定向 | 修复前 "all known versions" 都受影响 | 【推断】不适用 | 【推断】不适用 | 较难：需要配合共享挂载制造竞争。更适合当阅读材料。 |
| [CVE-2022-0492](https://nvd.nist.gov/vuln/detail/CVE-2022-0492) | NVD：cgroups v1 的 `release_agent` 可被滥用来 "escalate privileges and bypass the namespace isolation"。CDK 的利用是通过 `unshare` user+cgroup namespace 触发它。 | SWE-bench 源码注释说 Docker 默认 seccomp "only permits CLONE_NEWUSER with CAP_SYS_ADMIN"。【推断】所以加固后的 Docker 挡住了 `unshare` 这条路，cgroup v2 宿主上也没有 v1 的 `release_agent`，而 SWE-bench 式的 `cap_add SYS_ADMIN` 会把它重新打开。 | 【推断】碰到的是 Sentry 自己的 cgroupfs，不是宿主的 | 【推断】只影响 guest 内核 | 可以。是 "bare 对比 hardened" 的干净演示；CDK `abuse-unpriv-userns` |
| [CVE-2020-14386](https://nvd.nist.gov/vuln/detail/CVE-2020-14386) | AF_PACKET ring 溢出；需要 `CAP_NET_RAW`（Docker 默认给） | 未打补丁的内核上受影响，除非去掉 NET_RAW | 不受影响（来源见上） | 只影响 guest 内核 | 不要跑内核漏洞利用，改为测试 "能不能打开 raw socket"。 |
| [runc CVE-2026-119101](https://github.com/opencontainers/runc/security/advisories/GHSA-g2c3-j8g3-7gpw)（2026-11-12） | 恶意镜像让 `runc restore`（CRIU）把一个宿主路径 bind-mount 进容器 | 只在 Docker 上用 CRIU 实现 Hibernate 时相关 | 不适用 | 不适用 | 只作阅读材料。【推断】这是 Docker 上继续把 Hibernate 设为 "unsupported" 的一个理由。 |

**【推断】不同 Runtime 怎样改变结果。** 在 Docker 上，每个 runc 和内核 CVE 都直接落到宿主内核或宿主文件系统上。在 gVisor 上，攻击要先攻破 Sentry，然后面对的是一个受 seccomp 过滤、无特权的 Go 进程（见 2020 年那篇博客）。在 Firecracker 上，攻击要先攻破 guest 内核，然后还有 KVM 和被 jailer 关起来的 VMM。对课程来说，一套真实漏洞利用不如**探测每种攻击是被哪一层挡住的**来得重要：`uname -r` 和 `/proc/version` 分别显示宿主内核、gVisor 版本或 guest 内核；amicontained 显示被拦截的系统调用列表；cgroup 和挂载视图也各不相同。

---

## 7. 公平地测量密度和内存开销（Q6）

- **定义。**
  - RSS 是 "the amount of the mapping that is currently resident in RAM"。PSS 把每页 "by the number of processes sharing it" 分摊。`smaps_rollup` 给出每个进程的汇总值，比 `smaps` 便宜（[内核 proc 文档](https://docs.kernel.org/filesystems/proc.html)）。
  - cgroup v2 的 `memory.current` 是 "the total amount of memory currently being used by the cgroup and its descendants"。`memory.stat` 把它拆成 anon、file（page cache、tmpfs）、kernel、shmem。"A memory area is charged to the cgroup which instantiated it"（[cgroup v2](https://docs.kernel.org/admin-guide/cgroup-v2.html)）。
- **各 Runtime 的陷阱。**
  - **gVisor。** "A single memfd backs all application memory"，记为 shmem，所以 `anon` 一直很低。应该看 `memory.current` 或 `runsc usage`。sandbox 内看到的统计 "are approximations"（[gVisor 资源模型](https://gvisor.dev/docs/architecture_guide/resources/)）。
  - **Firecracker。** guest 内存是 VMM 进程里的一段匿名映射。Firecracker 自己的开销测试减去 guest 区域，报告 `rss`、`size` 和 `uss`（[test_memory_overhead.py](https://github.com/firecracker-microvm/firecracker/blob/main/tests/integration_tests/performance/test_memory_overhead.py)）。规格目标是 VMM 开销 "≤5 MiB"（[SPECIFICATION.md](https://github.com/firecracker-microvm/firecracker/blob/main/SPECIFICATION.md)）。被访问过的 guest 页会一直驻留，直到 balloon 把它们回收。balloon "requires cooperation from a driver in the guest"，支持 free page reporting/hinting（[ballooning.md](https://github.com/firecracker-microvm/firecracker/blob/main/docs/ballooning.md)）。
  - **容器。** 单容器的数字不含共享守护进程。CLOSER'21 用的是 private dirty 页，并注明没有算进约 40 MB 的 `dockerd`。
- **页共享。**
  - KSM 只合并 `MADV_MERGEABLE` 的匿名区域，"originally developed for use with KVM"，并且 "may use a lot of processing power"。节省量看 `pages_sharing` 和 `general_profit`（[KSM 文档](https://docs.kernel.org/admin-guide/mm/ksm.html)）。
  - Firecracker 出于侧信道原因建议关闭 KSM（[prod-host-setup](https://github.com/firecracker-microvm/firecracker/blob/main/docs/prod-host-setup.md)）。
  - 【推断】密度默认在 KSM 关闭的情况下报告；可以另加一列 KSM 开启的结果，并标明这会削弱隔离。
- **【推断】对三种 Runtime 都公平的方法：**
  1. **宿主边际值（主数字）。** 仿照 gVisor 的 `perf.py density`，先记下 `/proc/meminfo` 里的 `MemAvailable` 和 `Committed_AS`。然后启动 N = 10、50、100 个空闲 Sandbox，都用同一个 Template，每个都能响应一次健康检查 Exec。等到稳态，报告 (ΔMemAvailable)/N。这个数字包括 shim、Sentry/Gofer、VMM、guest 内核和 page cache，不需要了解 Runtime 的内部细节。
  2. **归因（用来解释）。** 对每个 Sandbox，汇总它在宿主上所有进程（container shim；runsc sandbox/gofer；firecracker）的 PSS，并读取它的 cgroup 的 `memory.current` 和 `memory.stat`。anon/file/shmem/kernel 分开报告。每台 Host 的固定守护进程（dockerd、containerd、Host Agent、Egress Proxy）单独报告一次，不摊到各个 Sandbox 上。
  3. **负载后的残留。** 跑一个固定负载（例如分配再释放 256 MB，然后 `pip install` 一个包），回到空闲后再测一次。这能看出 microVM 在没有 balloon 或 free page reporting 时永远不还的那部分内存。
  4. **两个密度数字。** *物理密度*：在发生 OOM 或 swap 之前，仍能满足 Exec SLO（例如 p99 低于基线的 2 倍）的最大 N。*可调度密度*：宿主内存除以申请的内存，因为 spec §6 规定内存从不超卖。两者回答的是不同的问题。
  5. **对从快照恢复的 microVM**，必须用 PSS 而不是 RSS，因为多个克隆可能共享以文件为后端的内存页。

---

## 8. 从 agent 角度评估 sandbox（Q7）

- **parity 实验（Harbor）。** 同一个 agent 和模型在两个环境上各重跑 k 次，比较 mean ± SEM，并点名由环境引起的异常任务，例如 Daytona 上那个 CPU 密集导致的超时（[SWE-bench adapter README](https://github.com/harbor-framework/harbor/blob/main/adapters/swebench/README.md)）。Harbor 还维护一份汇总各 adapter 的 [parity_summary.csv](https://github.com/harbor-framework/harbor/blob/main/adapters/parity_summary.csv)。
- **Terminal-Bench 的指标。** 带 95% CI 的解决率、agent 执行时间、模型调用次数和 token 数，以及每个模型的超时率。大多数 trial "in under 20 minutes" 内完成，极端情况 "up to 2 hours"（[arXiv:2601.11868](https://arxiv.org/abs/2601.11868) 附录 A）。
- **SWE-bench 的基础设施故障分类器。** 它通过匹配日志特征（OOM、容器不可用、网络不可达等）把 "环境坏了" 和 "patch 错了" 区分开。它只给建议，从不改变分母（[infra_failure.py](https://github.com/SWE-bench/SWE-bench/blob/main/swebench/harness/infra_failure.py)）。
- **ComputeSDK 的 OpenCode 可靠性测试。** 在 sandbox 里安装 agent CLI，跑一个简单的提示，检查输出；3 次迭代，每次间隔 60 s（[sandbox-opencode.bench.ts](https://github.com/computesdk/benchmarks/blob/master/benchmarks/reliability/sandbox-opencode.bench.ts)）。
- **关于延迟预算的研究。**
  - SpecBox 把按需启动 sandbox 看作 agent 关键路径上的 "cold-start penalty"。它让 sandbox 的启动和模型推理重叠进行，报告相对按需基线把 P99 端到端延迟降低 "by up to 2.9×"（[arXiv:2607.23933](https://arxiv.org/abs/2607.23933)）。
  - AgentSysBench 发现 "non-LLM components dominating latency in 5 of 10 applications"，sandbox 工作集内存峰值达到每会话 28 GB（[arXiv:2608.15127](https://arxiv.org/abs/2608.15127)）。
  - DSec（DeepSeek）用一个统一 SDK 提供 container、microVM、full-VM 几种后端，但没有发布它们之间的直接对比（[arXiv:2609.22978](https://arxiv.org/abs/2609.22978)）。
- **【推断】空白。** 我没有找到关于容器、gVisor、microVM 三者任务成功率的已发表研究。我们的两个 demo agent 可以做一个小规模版本：同一个任务、同一个模型，每个 Runtime 跑 N 次。

---

## 9. 建议的 harness 测量规程（**本节全部是推断**）

### 9.1 定义（每个指标都是一对有名字的时间戳）

服务端时间戳在每个进程内用同一个单调时钟，通过 `GET .../events` 暴露（ticket 17）。客户端时间戳由 harness 记录。

| 指标 | 起点 | 终点 | 说明 |
|---|---|---|---|
| **创建延迟（客户端）** | harness 发出 `POST /v1/sandboxes` | 收到响应（Running） | 我们的创建会阻塞到 Running |
| **创建到首次 Exec（TTFE）** | 同上 | 客户端收到 `true` 的退出码 | 可与 ComputeSDK 的 TTI 对比，但跑的是 `true` 而不是 `node -v` |
| 调度 | Control Plane 接受请求 | 选定 Host，且 Host Agent 确认 | |
| **镜像准备** | Host Agent 开始拉取/解包/转换 rootfs | 完成 | 新增阶段。镜像已缓存时为零；记录镜像 digest 和大小 |
| Runtime 启动 | Host Agent 调用 Runtime | Runtime 报告已启动（容器已启动 / `InstanceStart` 返回 / 快照加载返回） | |
| **内核启动**（仅 Firecracker） | `InstanceStart` | guest `/sbin/init`（boot-timer 设备，或串口/vsock 上的第一个字节） | 和 Firecracker 规格的定义相同 |
| **快照恢复**（Firecracker，第 3 阶段） | 发出快照加载请求 | 加载返回 | 因为缺页是延迟载入的，要和 "恢复到首次 Exec" 一起报告 |
| **Guest Agent 就绪** | Runtime 启动结束 | 第一次带认证的健康检查 RPC 得到响应 | 即 GLOSSARY 里 Starting → Running 的分界 |
| Exec 往返 | 客户端在 Running 的 Sandbox 上发出 Exec `true` | 客户端拿到结果 | 热 Sandbox，串行 |
| 销毁 | 发出 `DELETE` | Terminated，*并且* Host Agent 报告 cgroup/VMM 已经消失 | 销毁是异步的，参照 HotCloud'19 |

### 9.2 冷热状态的处理

每个创建数字都标注一个缓存状态。不同状态绝不混在一起，第一次运行也不丢弃。

- **C0 冷镜像。** Host 上没有镜像；page cache 已清空（`echo 3 > /proc/sys/vm/drop_caches`）。
- **C1 热镜像。** 镜像已缓存；page cache 已清空。
- **C2 热缓存。** 镜像已缓存；一分钟内用同一个 Template 创建过。
- **S（第 3 阶段）。** 从 Template 快照恢复，快照文件在 page cache 里可冷可热。
- **预热池**（如果以后加）。单独报告，不进对比表。

### 9.3 负载形态和样本量

- **串行。** 每个（Runtime × 缓存状态）组合 n = 200，用于 p50 和 p95。要让 p99 有意义，n ≥ 1,000，这样 p99 之上大约有 10 个样本。Firecracker 论文串行用了 500 个样本；只有 100 个样本时，p99 约等于第二大的值。
- **突发。** 并发 10、50、100，各重复 10 次，同时报告成功率。
- **开环吞吐。** 固定到达率（λ = 1、2、5、10 次创建/秒），持续 60 s。延迟从计划发送时刻算起，以避免 coordinated omission，并报告 p99 超过串行 p99 两倍时的 λ。
- **统计。** 报告中位数及其 bootstrap 95% CI，外加 p95、p99、最小/最大值和失败数。Runtime 之间的比较用带 CI 的中位数，不用平均值。

### 9.4 每次运行要记录的环境信息

- **宿主。** CPU 型号、核数、SMT 开关、内存、宿主内核版本、cgroup v1/v2、KSM 开关、THP 模式、swap 开关、CPU 调频策略，以及是否为嵌套虚拟化（Firecracker 在 GCP n2 上的嵌套虚拟化情况）。
- **Runtime。** Docker/containerd/runc 版本；runsc 版本、platform（systrap/KVM/ptrace）、directfs、网络模式；Firecracker 版本、jailer 开关、guest 内核版本、配置哈希和启动参数；seccomp profile 哈希；"bare" 还是 "hardened"。
- **Mac（第 1 阶段）。** Docker Desktop 版本、VM 类型（Apple Virtualization / QEMU）、VM 的 CPU/内存/磁盘、Rosetta 开关、镜像架构。在模拟架构下测得的时间一律不发布。
- **Sandbox 规格。** Template digest 和压缩/解压后的大小，vCPU、内存、磁盘、Egress Policy。
- **harness。** harness 的 git commit、客户端运行位置（同一台 Host 走 loopback，还是远程），以及实测到 Control Plane 的 RTT。

### 9.5 隔离测试套件的输出

每个 攻击 × 配置 报告：
- 结果：已拦截 / 未拦截 / 已知缺口；
- 是哪一层挡住的，例如 "seccomp 对 unshare 返回 EPERM"、"Sentry：系统调用未实现"、"只影响 guest 内核"、"Egress Proxy 返回 403"；
- 证据：确切的命令和输出。

---

## 10. 对我们设计的影响（**每一条建议的改动都是推断**）

| # | 发现（所在章节） | 影响 | 建议的改动 |
|---|---|---|---|
| 1 | Harbor 有文档化的自定义 sandbox 接口；TB2 是 89 个单容器、预构建镜像、带 oracle 解答的任务（§5） | spec §8 "Compatibility"；新 ticket | **把 Harbor 上的 Terminal-Bench 2.0 作为主兼容性测试，SWE-bench 保留为第二道检查。** 新增 ticket："基于我们公共 API 的 Harbor `BaseEnvironment`"。验收：Docker 上的 oracle sweep 作为基线，然后给出 gVisor 和 Firecracker 的逐任务差异；nop sweep 必须全部失败。**oracle/nop 两轮是通过标准**。在 10 个任务的子集上跑真实 agent 是可选项。 |
| 2 | SWE-bench harness 无法对接其他后端；它需要 root、`/testbed` 和 `SYS_ADMIN`（§4） | spec §8；ADR 0003 的 "拿官方镜像直接跑" | 把 "SWE-bench Lite 的一个子集" 改为 "SWE-bench Verified Mini（50 个），gold + 空 patch"。用一个薄 runner：用 `swebench` 库生成 `TestSpec`，把官方实例镜像当作 Template 运行，通过 Exec 以 root 身份执行 `eval_script`，在客户端用 `swebench.harness.grading` 打分。报告相对官方 Docker harness 的逐实例混淆矩阵，并给差异分类（§4.5）。 |
| 3 | SWE-bench 和 TB2 的镜像都只有 amd64（§4.2、§5.1） | spec §9 阶段划分 | 兼容性测试一直放在 x86_64 Linux 上（GCP 上的第 2 阶段和第 3 阶段），不在 arm64 Mac 上跑。在 ticket 18 里写明：模拟架构下测得的数字一律不发布。 |
| 4 | 我们加固的 Docker 去掉了全部 capability；SWE-bench 加了 `SYS_ADMIN` 并以 root 运行（§4.1） | ticket 16；spec §7 | 预期 "hardened" 下会出现兼容性差异。兼容性测试同时报告 "hardened" 和 "bare" 两种 Docker，让表格显示每一步加固在兼容性上的代价。这个代价本身就是课程内容的一部分。 |
| 5 | TB2 需要上网；Harbor 的 SWE-bench verifier 用 `uv` + PyPI（§4.3、§5.1） | ticket 12（Egress Policy）；Allowlist Preset | 兼容性测试先在 `package-registries` + `github` 预设下跑，再在完全放开的策略下跑。记录哪些任务需要哪些域名。这同时也是对 Egress Proxy 的真实负载测试。 |
| 6 | TB2 有 2 个任务的 agent 超时超过 3,600 s；Exec 上限是 1 小时（§5.1） | spec §4；ticket 06 | Harbor adapter 把 `timeout_sec > 3600` 的 `exec` 映射成 Process，并轮询它的输出。API 不用改。 |
| 7 | 每家对 "启动时间" 的定义都不一样（§3） | spec §8 "Performance"；ticket 17、18 | 采用 §9.1 的定义。在 ticket 17 的三个阶段之外加一个**镜像准备**阶段，加上客户端 TTFE，并要求每个数字都带 C0/C1/C2/S 缓存状态标签。 |
| 8 | 快照恢复在内存页载入之前就返回（Firecracker CI）（§2.3） | spec §8 第 3 阶段；spec §9 第 3 阶段 | "恢复调用" 和 "恢复 → 首次 Exec" 分两列报告，和 "冷内核启动" 并列。 |
| 9 | 不同 Runtime 的内存记账方式不同；Firecracker 要求关闭 KSM（§7） | spec §8 密度/内存；ticket 18；ticket 17 | 主数字 = 宿主边际值 ΔMemAvailable/N；归因 = PSS + cgroup `memory.stat`。分别报告物理密度和可调度密度。默认关闭 KSM。由 Host Agent 把宿主 `MemAvailable` 和每个 Sandbox 的 PSS 作为 Prometheus 指标导出，这样 harness 对 Runtime 而言仍是黑盒。 |
| 10 | 负载形态和百分位决定结果；coordinated omission（§2.4） | ticket 18 | p99 用串行 n ≥ 1,000，另跑突发和开环吞吐，并给出 bootstrap CI（§9.3）。 |
| 11 | `amicontained` 无人维护；deepce 的利用会覆写 runc；CDK 在维护（§6.1） | ticket 19；spec §8 "Isolation" | 使用 **CDK `evaluate`**（固定版本）+ **只用 deepce 的枚举** + amicontained（固定版本，用它的系统调用和 capability 列表，并标注 "自 2019 年起无人维护"）。默认套件里绝不跑利用模式。 |
| 12 | Leaky Vessels 的 attack 1 = 恶意镜像的 WORKDIR；私有 Template 来自 Tenant（§6.3） | ticket 19；ticket 13 | 新增**"恶意 Template"**类攻击：WORKDIR 是指向 `/proc/self/fd/N` 的符号链接、替换掉 `/dev/null` 的镜像、镜像里的 setuid 二进制。在已打补丁的 runc 上，它们是回归测试。可选加一个 "实验室" 模式：在一次性 VM 里固定 runc 1.1.11，演示 Docker 上真实的逃逸，以及 gVisor、Firecracker 上不会发生。 |
| 13 | Leaky Vessels 的 attack 2 需要 `runc exec --cwd`；我们 Exec 的 cwd 经过 Guest Agent（§6.3） | ticket 19；ADR 0002/0003 | 新增测试：把 `/home/user` 换成指向 `/proc/self/fd/7` 的符号链接，然后用默认 cwd 执行 Exec。预期：访问不到宿主，因为 cwd 在 Sandbox 内解析。在学习笔记里写明，"唯一入口" 的设计消除了这一类攻击。 |
| 14 | CVE-2022-0492 和 CVE-2020-14386 依赖 `unshare`/`SYS_ADMIN` 和 `NET_RAW`（§6.3） | ticket 19 | 新增 "unshare user+cgroup ns 并写 `release_agent`"（hardened 下被 seccomp 挡住；在 bare + SYS_ADMIN 下演示）和 "打开 AF_PACKET raw socket"（hardened 去掉了 NET_RAW；gVisor 默认拒绝）。两项都不跑内核漏洞利用。 |
| 15 | gVisor 和 Firecracker 的威胁模型都不覆盖硬件侧信道；Firecracker 要求关闭 SMT 和 KSM（§6.2） | ticket 19；spec §7 | 加一行宿主配置检查（SMT、KSM、swap，以及 `/sys/devices/system/cpu/vulnerabilities` 里的内核缓解状态），归为 "配置" 而不是 "攻击"。如实记录已知缺口。 |
| 16 | 信息泄露面因 Runtime 而异（§6.3） | ticket 19 | 新增对 `uname -r` 和 `/proc/version`、`/proc/kcore`、`/proc/timer_list`、`/proc/keys`、`dmesg`、`/sys/firmware` 和 CPU 型号泄露的探测，并写出每种 Runtime 的预期答案。 |
| 17 | 没有已发表的 agent 级容器对比 microVM 研究；Harbor 和 TB 用 parity + CI（§8） | ticket 20、24 | 每个 demo agent 在每个 Runtime 上跑 N = 10 次。记录带 CI 的成功率；每次工具调用的时间按 LLM、网络、sandbox 拆分；Sandbox 数、Exec 数和 Usage；失败按环境 / 不确定 / agent 分类（参照 SWE-bench 的分类器）。 |
| 18 | CRIU 恢复有自己的 runc CVE（2026-11）（§6.3） | spec §4（Docker 上 Hibernate "unsupported"） | Docker 上继续不支持 Hibernate，并在学习笔记里把这条公告列为理由之一。 |

**对 "替换还是并存？" 的回答。** 并存，但调换优先级。TB2 + Harbor 成为主兼容性测试，因为它：(a) 接入的是文档化的接口，而不是只支持 Docker 的 harness；(b) 不需要构建镜像；(c) 有不需要 LLM 的 oracle 和 nop 对照；(d) 覆盖两种 agent 位置；(e) 业界的 sandbox 厂商已经在对接它。SWE-bench Verified Mini 保留为第二道检查，因为它覆盖了系统调用和文件系统都很重的 Python 测试套件，spec §8 原本承诺的也是它，而且它的 gold/空 patch 方法成本很低。

**建议的 ticket 19 攻击清单**（现有清单，**粗体**为新增）：fork bomb · 内存炸弹 · 写满磁盘（Docker Desktop 上的已知缺口）· 通过 `host.docker.internal` 访问 Host Agent · metadata 地址 · 其他 Sandbox · 冒充平台调用 Guest Agent · 提权（**来自 Template 的 setuid 二进制；`no-new-privileges` 检查**）· 逃逸探测（**CDK `evaluate`、deepce 枚举、amicontained；固定版本**）· **恶意 Template：WORKDIR → `/proc/self/fd/N`** · **Exec cwd 符号链接替换** · **`unshare` + cgroup `release_agent`** · **raw socket / NET_RAW** · **内核身份和 `/proc` 信息泄露探测** · 绕过出网白名单 · DNS 隧道 · 借 Egress Proxy 访问 Host Agent / metadata / 私有地址 · 读取 Secret 的真实值 · **宿主配置检查（SMT/KSM/swap/漏洞缓解）**。

---

## 11. 来源（均于 2026-11-21 访问）

**基准与论文**
- ComputeSDK benchmarks 仓库：https://github.com/computesdk/benchmarks ；[tti.bench.ts](https://github.com/computesdk/benchmarks/blob/master/benchmarks/sandbox/tti.bench.ts)；[scoring.ts](https://github.com/computesdk/benchmarks/blob/master/benchmarks/sandbox/scoring.ts)；[TTI workflow](https://github.com/computesdk/benchmarks/blob/master/.github/workflows/sandbox-tti-benchmarks.yml)；[dax.bench.ts](https://github.com/computesdk/benchmarks/blob/master/benchmarks/sandbox/dax.bench.ts)；[sandbox-opencode.bench.ts](https://github.com/computesdk/benchmarks/blob/master/benchmarks/reliability/sandbox-opencode.bench.ts)
- Agache 等，Firecracker，NSDI 2020：https://www.usenix.org/system/files/nsdi20-paper-agache.pdf ；数据：https://github.com/firecracker-microvm/nsdi2020-data
- Young 等，The True Cost of Containing，HotCloud 2019：https://www.usenix.org/system/files/hotcloud19-paper-young.pdf
- Anjali 等，Blending Containers and Virtual Machines，VEE 2020：https://pages.cs.wisc.edu/~swift/papers/vee20-isolation.pdf
- Li 等，Comparative Performance Study of Lightweight Hypervisors，CLOSER 2021：https://www.scitepress.org/Papers/2021/104405/104405.pdf
- Merrill 等，Terminal-Bench，arXiv:2601.11868（ICLR 2026）：https://arxiv.org/abs/2601.11868
- SpecBox，arXiv:2607.23933：https://arxiv.org/abs/2607.23933
- AgentSysBench，arXiv:2608.15127：https://arxiv.org/abs/2608.15127
- DSec，arXiv:2609.22978：https://arxiv.org/abs/2609.22978
- wrk2（coordinated omission）：https://github.com/giltene/wrk2

**厂商与项目的定义**
- Firecracker SPECIFICATION.md：https://github.com/firecracker-microvm/firecracker/blob/main/SPECIFICATION.md
- Firecracker 性能测试：[test_boottime.py](https://github.com/firecracker-microvm/firecracker/blob/main/tests/integration_tests/performance/test_boottime.py)、[test_snapshot.py](https://github.com/firecracker-microvm/firecracker/blob/main/tests/integration_tests/performance/test_snapshot.py)、[test_memory_overhead.py](https://github.com/firecracker-microvm/firecracker/blob/main/tests/integration_tests/performance/test_memory_overhead.py)
- Firecracker 文档：[design.md](https://github.com/firecracker-microvm/firecracker/blob/main/docs/design.md)、[prod-host-setup.md](https://github.com/firecracker-microvm/firecracker/blob/main/docs/prod-host-setup.md)、[ballooning.md](https://github.com/firecracker-microvm/firecracker/blob/main/docs/ballooning.md)
- gVisor：[性能指南](https://gvisor.dev/docs/architecture_guide/performance/)、[安全模型](https://gvisor.dev/docs/architecture_guide/security/)、[资源模型](https://gvisor.dev/docs/architecture_guide/resources/)、[Containing a Real Vulnerability（2020-09-18）](https://gvisor.dev/blog/2020/09/18/containing-a-real-vulnerability/)
- E2B template how-it-works：https://docs.e2b.dev/template/how-it-works.md
- Daytona：[Daytona Cloud 发布文章（2025-04-28）](https://www.daytona.io/dotfiles/introducing-daytona-cloud-the-agent-native-infrastructure)、[v2 architecture](https://www.daytona.com/docs/architecture.md)
- Modal：[Unpacking sandbox startup latency（2026-06-22）](https://modal.com/blog/unpacking-sandbox-startup-latency)、[products/sandboxes](https://modal.com/products/sandboxes)

**评测 harness**
- SWE-bench：[README](https://github.com/SWE-bench/SWE-bench/blob/main/README.md)、[docker_setup.md](https://github.com/SWE-bench/SWE-bench/blob/main/docs/guides/docker_setup.md)、[evaluation.md](https://github.com/SWE-bench/SWE-bench/blob/main/docs/guides/evaluation.md)、[datasets.md](https://github.com/SWE-bench/SWE-bench/blob/main/docs/guides/datasets.md)、[faq.md](https://github.com/SWE-bench/SWE-bench/blob/main/docs/faq.md)、[run_evaluation.py](https://github.com/SWE-bench/SWE-bench/blob/main/swebench/harness/run_evaluation.py)、[image_spec.py](https://github.com/SWE-bench/SWE-bench/blob/main/swebench/image_builder/image_spec.py)、[image_builder constants](https://github.com/SWE-bench/SWE-bench/blob/main/swebench/image_builder/constants/__init__.py)、[infra_failure.py](https://github.com/SWE-bench/SWE-bench/blob/main/swebench/harness/infra_failure.py)
- SWE-bench 数据集：[Lite](https://huggingface.co/datasets/SWE-bench/SWE-bench_Lite)、[Verified](https://huggingface.co/datasets/SWE-bench/SWE-bench_Verified)、[Verified Mini](https://huggingface.co/datasets/MariusHobbhahn/swe-bench-verified-mini)
- Epoch AI，SWE-bench Docker 镜像（2025-07-10）：https://epoch.ai/blog/swebench-docker
- SWE-ReX：https://github.com/SWE-agent/SWE-ReX ；mini-swe-agent environments：https://github.com/SWE-agent/mini-swe-agent/tree/main/src/minisweagent/environments
- Harbor：[仓库](https://github.com/harbor-framework/harbor)、[custom-sandboxes](https://github.com/harbor-framework/harbor/blob/main/docs-mintlify/sandboxes/custom-sandboxes.mdx)、[pre-integrated sandboxes](https://github.com/harbor-framework/harbor/blob/main/docs-mintlify/sandboxes/pre-integrated-sandboxes.mdx)、[task overview](https://github.com/harbor-framework/harbor/blob/main/docs-mintlify/tasks/overview.mdx)、[environment](https://github.com/harbor-framework/harbor/blob/main/docs-mintlify/tasks/environment.mdx)、[verifier](https://github.com/harbor-framework/harbor/blob/main/docs-mintlify/tasks/verifier.mdx)、[capabilities.py](https://github.com/harbor-framework/harbor/blob/main/src/harbor/environments/capabilities.py)、[kata.py](https://github.com/harbor-framework/harbor/blob/main/src/harbor/environments/kata.py)、[modal.py](https://github.com/harbor-framework/harbor/blob/main/src/harbor/environments/modal.py)、[agents](https://github.com/harbor-framework/harbor/tree/main/src/harbor/agents)、[SWE-bench adapter](https://github.com/harbor-framework/harbor/tree/main/adapters/swebench)、[parity_summary.csv](https://github.com/harbor-framework/harbor/blob/main/adapters/parity_summary.csv)
- Terminal-Bench 2.0 任务：https://github.com/harbor-framework/terminal-bench-2 ；2.1：https://github.com/harbor-framework/terminal-bench-2-1

**隔离工具、威胁模型、CVE**
- amicontained：https://github.com/genuinetools/amicontained ；runtime 列表：https://github.com/genuinetools/bpfd/blob/master/proc/proc.go
- deepce：https://github.com/stealthcopter/deepce ；CDK：https://github.com/cdk-team/CDK ；docker-bench-security：https://github.com/docker/docker-bench-security
- Docker Engine security：https://docs.docker.com/engine/security/
- runc 安全公告：[CVE-2024-21626](https://github.com/opencontainers/runc/security/advisories/GHSA-xr7r-f8xq-vfvv)、[CVE-2025-31133](https://github.com/opencontainers/runc/security/advisories/GHSA-9493-h29p-rfm2)、[CVE-2025-52565](https://github.com/opencontainers/runc/security/advisories/GHSA-qw9x-cqr3-wc7r)、[CVE-2025-52881](https://github.com/opencontainers/runc/security/advisories/GHSA-cgrx-mc8f-2prm)、[CVE-2026-119101](https://github.com/opencontainers/runc/security/advisories/GHSA-g2c3-j8g3-7gpw)
- NVD：[CVE-2019-5736](https://nvd.nist.gov/vuln/detail/CVE-2019-5736)、[CVE-2022-0492](https://nvd.nist.gov/vuln/detail/CVE-2022-0492)、[CVE-2020-14386](https://nvd.nist.gov/vuln/detail/CVE-2020-14386)
- Linux 内核文档：[/proc（smaps、PSS）](https://docs.kernel.org/filesystems/proc.html)、[cgroup v2](https://docs.kernel.org/admin-guide/cgroup-v2.html)、[KSM](https://docs.kernel.org/admin-guide/mm/ksm.html)

**我自己的测量**（2026-11-21，方法见 §4.2 和 §5.1）：通过 Docker Hub registry v2 读取了全部 89 个 TB2 `docker_image` 以及 6 个 SWE-bench 实例镜像的 manifest 和 config blob；`task.toml` 字段汇总自 `terminal-bench-2` 仓库 `main` 分支。
