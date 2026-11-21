# Agent Sandbox：Spec

> [English](spec.md) | 中文

> 这是一份活文档。理解变了就改它，同时同步 [GLOSSARY.md](../../GLOSSARY.zh.md) 和 [docs/adr/](../../docs/adr/)。术语以 GLOSSARY 为准。这些决定背后的调研在 [docs/research/](../../docs/research/)，汇总见 [research-review.zh.md](research-review.zh.md)。

## 1. 要解决的问题

AI agent 需要一台一次性的隔离计算机来运行它写出来的不可信代码。这里的优化目标不是利用率和吞吐，而是**隔离、内存、冷启动时间**。本项目要做一个这样的平台，并且回答一个问题：**Docker、gVisor、Firecracker 这三种 Runtime，在启动速度、资源开销和隔离强度上分别差多少？为什么？**

这是一个学习项目，"学会隔离"是正式产出，和代码同等重要。

## 2. 目标与不做的事

**目标**
- 通过 REST API 管理 Sandbox 的生命周期，并在 Sandbox 里执行命令、运行代码、读写文件、暴露端口。
- 支持业界的两种用法：**agent 在外面**，把 Sandbox 当作工具；**agent 在里面**，把整个 agent（比如一个 coding agent CLI）放进 Sandbox 里跑。
- 多 Tenant，有 Quota 和 Usage，从第一天起就当作多租户服务来建模。
- Runtime 可以切换，所有 Runtime 跑同一套测试，产出一张对比表。
- 真实的 agent 完成编程任务：一个在 Sandbox 外面，一个在 Sandbox 里面，一个通过一个很薄的 MCP server。

**不做**
- 在平台上构建镜像（Template 只能按镜像地址注册，或者通过 Capture 生成）
- 管理用的 HTTP API（用 Control Plane 的命令行子命令代替）
- 定价、账单、支付
- Python SDK（对外接口是 REST API 和 MCP server）
- 第一版的 GPU：调研的 14 家里只有 3 家提供，而且 GPU 的硬隔离需要裸金属。API 里保留字段，请求了就返回"不支持"
- 只复制文件系统的弱化版 Fork（这个需求由 Capture 和 Stopped 满足）
- 多个 Sandbox 同时写入的 Volume

## 3. 架构

```
                        控制通路
Tenant 的 agent ──REST──▶ Control Plane ──gRPC（第一阶段走 unix socket）──▶ Host Agent
      │                  （SQLite，状态以此为准）                            │
      │ 数据通路：Exec、文件、Process、代码解释器                             ├─ Runtime：Docker / gVisor / Firecracker
      └──REST/SSE + Access Token──▶ Host 数据代理 ──────────────────────── ├─ gRPC over unix socket / vsock ──▶ Guest Agent（每个 Sandbox 里的 PID 1）
                                    （也负责 Exposed Port）

每台 Host 上，也是 Sandbox 唯一允许主动连接的组件（ADR 0005）：
   Egress Proxy（执行 Egress Policy、注入 Secret）· DNS 解析器（只解析白名单里的域名）
   在 Docker Desktop 上，这两个都作为容器跑在 Docker 的虚拟机里；每个 Sandbox 单独接一个 --internal、隔离模式的网络。
```

- 拆分 Control Plane 和 Host Agent：[ADR 0001](../../docs/adr/0001-control-plane-host-agent-split.zh.md)
- 所有 Runtime 共用一个 Guest Agent（第一阶段需要 Docker Desktop ≥ 4.86 并使用 VirtioFS；后备通道是 `docker exec -i --stdio`）：[ADR 0002](../../docs/adr/0002-single-guest-agent-across-runtimes.zh.md)
- 由平台注入 Guest Agent：[ADR 0003](../../docs/adr/0003-platform-injects-guest-agent.zh.md)
- 跨 Sandbox 边界的连接只能由外向内发起；Egress Proxy 和 DNS 解析器是唯一的例外；Control Plane 和 Host Agent 之间谁连谁另行决定：[ADR 0005](../../docs/adr/0005-connections-only-outside-in.zh.md)
- Secret 是占位符，由 Egress Proxy 换成真实值：[ADR 0006](../../docs/adr/0006-secrets-as-placeholders.zh.md)
- 数据通路的流量直接到 Host：[ADR 0007](../../docs/adr/0007-data-path-to-host.zh.md)
- Guest Agent 的生命周期（PID 1、每次启动和恢复都做 Init、non-dumpable）：[ADR 0008](../../docs/adr/0008-guest-agent-lifecycle.zh.md)
- 代码组织：一个 Go module。程序有 `cmd/controlplane`、`cmd/hostagent`、`cmd/guestagent`、`cmd/egressproxy`、`cmd/dnsresolver`、`cmd/mcpserver`。测试框架放 `bench/`，demo agent 放 `demo/`（Go，调用 Anthropic Go SDK，模型用 Claude Sonnet 5.5）。

## 4. 公共 API（草案）

路径和字段在实现时可以调整；能力范围以这里为准。

**控制通路**（Control Plane，用 Tenant 的 API key 鉴权）

| 能力 | 接口 |
|---|---|
| Sandbox | `POST /v1/sandboxes`（阻塞到 Running，或超时后变成 Terminated；返回 Host 的数据入口和 Access Token）、`GET /v1/sandboxes`、`GET /v1/sandboxes/{id}`、`DELETE /v1/sandboxes/{id}` |
| 生命周期 | `POST .../{id}/stop`、`POST .../{id}/start`、`POST .../{id}/freeze`、`POST .../{id}/unfreeze`；`.../hibernate`、`.../resume`、`.../fork` 到对应阶段之前返回"不支持" |
| Capture | `POST .../{id}/capture`：把文件系统保存成一个新的私有 Template |
| 出网 | `PATCH .../{id}/egress`：修改运行中 Sandbox 的 Egress Policy |
| 事件 | `GET .../{id}/events`：生命周期事件和终止原因 |
| Exposed Port | `POST .../{id}/ports/{port}`，返回访问地址和 token |
| Template | `POST /v1/templates`（按镜像地址注册）、`GET /v1/templates` |
| Volume | `POST /v1/volumes`、`GET`、`DELETE`；创建 Sandbox 时指定挂载 |
| Secret | `POST /v1/secrets`（名称、值、必填的目标域名）、`GET`（永远不返回值）、`DELETE` |
| Allowlist Preset | `GET /v1/allowlist-presets` |
| Usage | `GET /v1/usage?from=&to=&group_by=sandbox|volume|template|day`，也能查原始时间段 |

**数据通路**（Host 的数据入口，用该 Sandbox 的 Access Token 鉴权；ADR 0007）

| 能力 | 接口 |
|---|---|
| Exec | `POST .../exec`，同步返回 stdout、stderr、退出码；加 `Accept: text/event-stream` 就改为流式返回 |
| Process | `POST .../processes`、`GET .../processes`、`GET .../processes/{pid}/output?stream=stdout|stderr&offset=`、`DELETE .../processes/{pid}` |
| 文件 | 上传、下载、列目录 |
| 代码解释器 | `POST .../contexts`、`POST .../contexts/{ctx}/run`，输出文本、错误和 PNG |

对 Stopped 的 Sandbox 发任何数据通路请求，都会自动启动它。

**创建参数**：Template、vCPU、内存、磁盘、`gpu`（保留字段）、空闲超时、默认环境变量、Egress Policy（禁止、白名单、全开；白名单里可以混用域名和 Allowlist Preset）、Secret（环境变量名 → Secret，环境变量里放的是占位符）、要挂载的 Volume。

**Exec 参数**：命令、用户（默认 `user`，可指定 root）、工作目录（默认 `/home/user`）、环境变量、超时（默认 60 秒，最多 1 小时）。超过一小时的 agent 任务应该用 Process。客户端断开时，这条 Exec 的整个进程组会被杀掉。

**MCP server**：一个独立程序，只调用公共 API，提供 6 到 8 个工具（创建、exec、读写文件、启动和读取 Process、停止、删除）。

## 5. 生命周期规则

```
Starting ──Init──▶ Running ⇄ Frozen
                     │  ▲
               空闲  ▼  │ start（显式调用，或任何数据通路请求）
                   Stopped ──7 天──▶ Terminated
Running ⇄ Hibernated（阶段 3b）
任何状态 ──▶ Terminated（主动删除 / 超过最长存活 / Frozen 超时 / disk_limit / 失败 / 对账清理）
```

- **Init**：在任何用户进程运行之前，以及每次启动、恢复和 Fork 之后，Host Agent 都会给 Sandbox 一个新的身份（ADR 0008）。Init 完成前，Sandbox 都处于 Starting。
- **空闲**：API 调用、正在执行的 Exec、正在运行的 Process、正在进行的流式输出、Exposed Port 上打开的连接都算活动。Host Agent 上报 `last_activity_at` 和正在进行的操作数，由 Control Plane 掌管截止时间。默认空闲 5 分钟后变成 **Stopped**。可以调整，但不能超过 Quota 里的最长存活时间；忘了关的服务仍然受它约束。
- **Stopped**：保留文件系统和 Sandbox ID，释放 CPU 和内存，保留 7 天后变成 Terminated。重新启动时从头开机，并再做一次 Init。Exposed Port 收到流量时自动启动，这一项推迟，因为需要 Control Plane 参与请求转发。
- **Frozen**：不计空闲，但有单独的最长时限（默认 1 小时），到期就 Terminated。对 Frozen 的 Sandbox 调用 Exec 会返回错误，要先显式解冻。发往 Frozen Sandbox 的 Exposed Port 请求会立即失败。
- **对账**：Control Plane 先写入 Starting，再调用 Host。Host Agent 定期上报实际在跑的 Sandbox，每个都带有生命周期 ID 标签。陌生的容器先给一段宽限期再销毁；数据库里有、机器上没有的，标记为失败并结束 Usage。如果数据库读取失败，这一轮跳过这台 Host。

## 6. Usage、Quota 与调度

- Usage 由 Control Plane 根据生命周期事件推算。事件 ID 由 Host Agent 生成，所以重试的上报只记一次。
  - Running 计 vCPU 秒和内存 GB 秒；
  - Frozen 计内存 GB 秒；
  - Stopped 和 Hibernated 计存储；
  - 每个 Sandbox 计出网字节数；
  - Volume 和 Capture 出来的 Template 计存储；
  - Starting 不计。
- Host 失联时，相关时间段截止到这台 Host 最后一次成功上报的时刻，这个时刻按 Control Plane 的时钟记录：[ADR 0004](../../docs/adr/0004-usage-ends-at-last-host-report.zh.md)。调研过的厂商都不这样做，它们都计费到发现为止。
- Quota 有 6 个维度：同时存在的 Sandbox 数、vCPU 总数、内存总量、Volume 总容量、单个 Sandbox 最长存活时间、创建速率。超额直接拒绝，不排队。
- 调度：Host 注册时上报容量，调度用 first-fit。正在创建中的 Sandbox 也要预留资源。Host 装不下时，返回一个有类型的错误拒绝，Control Plane 再试下一台。内存不超卖，CPU 超卖比例可以按 Host 配置，默认 1.0。
- SQLite：使用 WAL 模式，只有一个写入 goroutine，高频的活动数据放在内存里，不跨 RPC 持有事务。

## 7. 安全模型

信任分三个区域：Control Plane（可信）、Host Agent（可信）、Sandbox 内部（**不可信**）。

- Control Plane 和 Host Agent 之间：第一阶段用 unix socket 加共享 token；等 Host 跑到 NAT 后面，改为由 Host 主动建立的隧道，加 mTLS（ADR 0005）。
- Host Agent 和 Guest Agent 之间：每次启动都换一个新的随机 token，在 Init 中下发。Guest Agent 只把它放在内存里，并且是 non-dumpable 的。在去掉 `CAP_SYS_PTRACE` 的前提下，普通用户和用 root 执行的命令都读不到它。token 防范的正是这一点（ADR 0008）。
- 跨 Sandbox 边界的连接只能由外向内发起。唯一的例外是 Egress Proxy 和 DNS 解析器，它们没有管理接口。在 Docker Desktop 上，它们作为容器跑在 Docker 的虚拟机里；Host Agent 通过自己发起的连接，或者一个只读挂载的文件，给它们下发配置。
- Docker Desktop 上的网络：每个 Sandbox 只接自己的一个 `--internal` 网络，网关模式为 `isolated`（需要 Docker Engine ≥ 28）。Sandbox 里没有默认路由，网络网关上也没有任何服务响应。
- Docker 加固：普通用户；先去掉全部 capabilities，再加回 SETUID、SETGID、CHOWN、DAC_OVERRIDE、FOWNER、KILL；`no-new-privileges`；限制进程数；限制 CPU 和内存；默认 seccomp；不挂 Docker socket；不用特权模式。
- 出网：默认禁止，和 Cloudflare、OpenAI 托管 shell、Google 一样。白名单可以混用域名和 Allowlist Preset（`llm-providers`、`package-registries`、`github`），并且可以在 Sandbox 运行中修改。Egress Proxy 检查 HTTP 的 Host 头和 HTTPS 的 SNI，只放行 HTTP 和 HTTPS。元数据地址 `169.254.169.254`、Host 本机和内网地址永远禁止访问。
- DNS：Sandbox 只能用平台的 DNS 解析器，它只解析白名单里的域名；其他所有 DNS 流量一律禁止。这样就堵住了 DNS 隧道。
- Secret：Sandbox 里只有占位符，由 Egress Proxy 换成真实值；必须绑定目标域名，换不了就拦截，只替换 header，第一版不改写响应。每个 Sandbox 的 CA 通过只读挂载和 `SSL_CERT_FILE` 之类的环境变量提供，永远不写进信任库（ADR 0006）。
- Capture 时根据源 Template 显式重建镜像配置，所以占位符、代理环境变量、CA 和 Guest Agent 的入口都不会被固化进去。
- 磁盘：Docker Desktop 默认的镜像存储会悄悄忽略 `--storage-opt size`。作为软限制，Host Agent 定期检查每个 Sandbox 可写层的大小，超出就以 `disk_limit` 为原因终止。隔离报告里如实记为软限制，而不是硬限制。

## 8. 验证

测试框架只走公共 API，作为黑盒客户端。测试哪种 Runtime，由 Host Agent 的配置决定。测量规范遵循[验证调研](../../docs/research/verification-and-benchmarks.zh.md)的 §9.1。

- **性能**：
  - 定义好的时间段：客户端到第一次 Exec、镜像准备、调度、Runtime 启动、Init 和 Guest Agent 就绪；从第三阶段起，冷启动内核和快照恢复（以及恢复到第一次 Exec）分开测，因为厂商宣传的数字来自快照恢复。销毁单独测。
  - 每个数字都标明缓存状态。p99 至少要 1000 个串行样本；另外跑突发负载和开环负载，给出 bootstrap 置信区间。
  - 记录完整的运行环境：机器、Host 内核、Runtime 及版本、配置、是否在嵌套虚拟化下。
  - 密度：主要数字是"宿主机可用内存的减少量 ÷ Sandbox 数量"。归因用 PSS 和 cgroup 的 `memory.stat`。物理密度和可调度密度分开报告，默认关闭 KSM。这些由 Host Agent 作为 Prometheus 指标导出，测试框架保持黑盒。
- **隔离**：每个测试是一次攻击，并注明挡住它的是哪一层。这套测试也是学习用的课程表。
  - 攻击清单：fork 炸弹、内存炸弹、写满磁盘、通过 `host.docker.internal` 连 Host Agent、访问元数据地址、访问其他 Sandbox、冒充平台调用 Guest Agent、以 root 身份读取 Guest Agent 的 token、提权、恶意 Template（WORKDIR 软链接到 `/proc/self/fd`、setuid 程序）、替换 Exec 工作目录的软链接、`unshare` 加 cgroup `release_agent`、原始套接字、内核身份和 `/proc` 信息泄露、绕过出网白名单、DNS 隧道、借 Egress Proxy 去连 Host Agent、元数据地址或内网地址、在 Sandbox 里读出 Secret 的真实值、找到默认路由或正在监听的网关。
  - 工具都锁定版本：CDK 只跑 `evaluate`，deepce 只做枚举，amicontained（标注为已停止维护）。默认绝不运行利用模式；复现真实 CVE 是在一次性虚拟机里的选做实验。
  - Docker 要同时跑"裸配置"和"加固后"两组。结果分开记录"逃到了虚拟机"和"逃到了 Host"。Host 配置检查（SMT、KSM、swap、漏洞缓解）作为配置报告，不算攻击。
- **兼容性**（只在 x86_64 Linux 上跑，绝不在 arm64 的 Mac 上模拟运行）：
  - 主要：**通过 Harbor 跑 Terminal-Bench 2.0**，用一个基于我们公共 API 的 `BaseEnvironment`。先在 Docker 上跑一遍 oracle 作为基线，在 gVisor 和 Firecracker 上逐题比对差异，再跑一遍 nop，结果必须全部失败。超过一小时的 Exec 改用 Process。
  - 次要：**SWE-bench Verified Mini（50 题）**，跑标准补丁和空补丁两组，用一个很薄的 runner，结果以逐题混淆矩阵的形式和官方 Docker 测试框架对比。
  - 先在 `package-registries` 和 `github` 预设下跑，再在全开模式下跑；加固和裸配置的 Docker 并排对比。
- **demo agent**：在 Sandbox 外面的、在 Sandbox 里面的（用 Secret 提供 API key），以及通过 MCP server 的。每个在每种 Runtime 上跑 10 次，记录带置信区间的成功率、每次工具调用的耗时（拆成 LLM、网络、sandbox 三部分），并把失败归类为环境问题、任务含糊或 agent 问题。
- **可观测性**：Control Plane 和 Host Agent 都暴露 Prometheus 指标，日志用结构化 JSON。

产出：对比表（Runtime × 指标），每个阶段一篇 `docs/learn/` 笔记，中英两版。

## 9. 阶段

| 阶段 | 内容 | 在哪跑 |
|---|---|---|
| 0 | 用 `/prototype` 跑完 [Docker/gVisor 调研](../../docs/research/phase1-docker-gvisor-on-mac.zh.md) §5 里的 Mac 实验 E1–E8，让第一阶段建立在实测的事实上 | Mac |
| 1 | Docker Runtime 和上面所有功能（包括 Stopped、Capture、Secret、MCP server 和各个 demo agent），性能和隔离两套测试，`docs/learn/01-docker.md` | Mac |
| 2 | gVisor Runtime，范围保持小；选做：用 `runsc checkpoint`/`restore` 做出第一个 Hibernated。在 x86_64 上通过 Harbor 跑 Terminal-Bench 2.0。`docs/learn/02-gvisor.md` | Mac 上的 Lima、Colima 或 OrbStack Linux 虚拟机；x86_64 兼容性测试用 GCP |
| 3a | 冷启动的 Firecracker Runtime：OCI 转 ext4、guest 内核、vsock、每个 Sandbox 一个 network namespace 并路由到 Egress Proxy、jailer，重跑所有测试，SWE-bench Verified Mini | GCP n2，开启嵌套虚拟化（每个数字都标注为嵌套） |
| 3b | 同一台 Host 上的 Template 快照和 Hibernate/恢复，冷启动和快照恢复分开测 | 同上 |
| 3c | Fork | 同上 |
| 4a | 在 Docker 和 gVisor（nvproxy）上做容器级 GPU，用 T4 或 L4 | GCP g2 或 AWS g4dn（总共约 $15） |
| 4b | 用 Cloud Hypervisor 做 VFIO GPU 直通，作为只用于 GPU 的第四种 Runtime，加上 GPU 直通设计文档 | 裸金属（Hetzner GEX45，或 AWS `.metal` 实例） |

第一阶段的任务在 [issues/](issues/README.zh.md) 下。按依赖关系，从没有阻塞的任务开始做。

## 10. 待定事项和后续阶段的输入

在这里记下随时冒出来的问题，弄清楚以后再写回上面各节。

**待定**
- **一个 Volume 同时只读挂到多个 Sandbox**：RL 和评测场景常见共享数据集。
- **从响应里清除 Secret**：第一版不做（ADR 0006）。等"agent 在里面"的 demo 跑通后再看。
- **Exposed Port 收到流量时自动启动、超时自动 Hibernate、收到流量自动恢复**：推迟，因为需要 Control Plane 参与请求转发。
- **托管 harness 模式**（Claude Managed Agents、OpenAI Agents API）：暂不在范围内。
- **Process 的 stdin 和 PTY**：选做。

**第三阶段的输入**（到时结合实测再决定；见 [Firecracker 调研](../../docs/research/firecracker-microvm.zh.md)）
- 自己写一个很薄的 Firecracker REST API 客户端，锁定一个版本；`firecracker-go-sdk` 已于 2026-11-10 归档。
- 快照只能恢复到完全相同的环境上，所以 Host 要上报兼容性标识（CPU 型号、Host 内核、Firecracker 版本、快照格式）；升级 Firecracker 会让已有快照失效，需要制定升级策略。
- Hibernated 的存储大约是内存大小加磁盘差异，两者都要计量；Quota 要覆盖 Host 侧的快照存储。
- 每个 Sandbox 一个 network namespace，里面的 guest IP 和 MAC 都一样；nftables 只放行 Egress Proxy 和 DNS 解析器。
- 把 jailer 和 Firecracker 的生产环境 Host 检查清单作为第三阶段的隔离课程（E2B 没有用 jailer）。
- guest 内核用 6.18，开启 `CONFIG_VMGENID`、virtio-rng 和 vsock，关闭 vsock loopback。
- 选做一次裸金属对照测试，测出嵌套虚拟化的开销。
- 决定 Capture 在 Firecracker 上怎么产出 Template（一个 OCI layer，还是内部的 ext4 Template）。
- 任务 06 增加"Hibernate 或 Fork 前后不丢 Process 退出事件"，因为每次快照都会重置 vsock 连接。

**第四阶段的输入**（见 [GPU 调研](../../docs/research/gpu-sandboxing.zh.md)）
- 只给整张卡，不跨租户做时间切片或 MPS；MIG 以后再说。
- Cloud Hypervisor 作为只用于 GPU 的第四种 Runtime；那时 ADR 0003 要覆盖 guest 内核和 NVIDIA 驱动的配对；Host 上报的信息增加 GPU 型号、UUID、IOMMU 分组和健康状态。
- GPU 成为 Quota 的第七个维度；Running 和 Frozen 状态都计 GPU 秒；GPU Sandbox 的 Hibernate 不支持，除非 CUDA 状态做过 checkpoint。
- GPU Host 需要预留大页，不用 balloon 或 virtio-mem；请求只分配给确认已清理干净的 GPU。
- 设计文档要覆盖：每种共享方式的信任模型；Host 准备工作（IOMMU、ACS 和 IOMMU 分组检查、绑定 vfio-pci、大页）；租户交接协议（重置、隔离一段时间、扫描显存、标记为可用）；每个阶段的启动耗时预算；为什么不用 Firecracker（附"VFIO 发布后重新评估"的触发条件）；快照策略（无 GPU 的虚拟机快照、GPU 热插拔、在 guest 里用 `cuda-checkpoint` 恢复）。
- 隔离测试里的 GPU 攻击：读取上一个 Sandbox 的显存、通过 NVML 看到其他租户、请求额外的驱动能力、一个 `LD_PRELOAD` hook 回归镜像（CVE-2025-23266 那一类）、4b 上的 DMA 逃逸。为 NVIDIA 驱动和 Container Toolkit 制定补丁时限，并使用 CDI 模式。
