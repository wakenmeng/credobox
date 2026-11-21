# Agent sandbox 市场格局与我们路线图的检查

> [English](landscape-and-roadmap.md) | 中文

> 调研日期：2026-11-21。除非另有说明，所有链接均于当天访问。
> 只用一手来源：官方产品页、文档、定价页、changelog 和官方 repo。不是一手来源的会单独标出。
> **【推断】** 表示我自己的结论，不是来源的原话。术语以 [GLOSSARY](../../GLOSSARY.zh.md) 为准；厂商自己的叫法保留在引号或代码格式中。
> 本文建立在 [agent-sandbox-usage.zh.md](agent-sandbox-usage.zh.md) 之上，那篇已深入覆盖 E2B、Daytona、Modal 和 Vercel。这四家在本文只出现在对比表里，相关说法引用那篇文档的来源。

---

## 1. 摘要

- **认真做这件事的大约有 15 家，分三类。** (a) 以 sandbox 为主业的创业公司：E2B、Daytona、Modal、Vercel、Runloop、Morph、Blaxel、Together/CodeSandbox、Fly.io Sprites、Northflank。(b) 超大云厂商的 "code interpreter" 工具：AWS AgentCore Code Interpreter、Azure Container Apps dynamic sessions、Google Agent Platform sandboxes、OpenAI containers。(c) 可自托管的开源项目：kubernetes-sigs/agent-sandbox、Agent Substrate、microsandbox。Cloudflare 的 Sandbox SDK 介于 (a) 和 (b) 之间。创业公司和 Cloudflare 都按秒或按小时对 CPU 和内存计费；云厂商的工具更常见的形态是有上限、预装好、生命周期短。
- **隔离技术已收敛到 VM，gVisor 是稳固的第二选项。**
  - Firecracker：E2B、Vercel、Daytona v2、Cloudflare Containers、Together。
  - Cloud Hypervisor：Northflank、Daytona v2（Windows 和 GPU）、Substrate。
  - gVisor：Modal、Google Agent Platform sandboxes、Northflank（GPU）、Substrate、kubernetes-sigs/agent-sandbox。
  - Hyper-V：Azure。

  这份名单里没有任何一家把普通容器当作不可信代码的边界来卖。
- **基本功能（table stakes）**，几乎每家都有（§3）：带流式输出的 exec、文件读写、后台进程、端口或 preview URL、从 OCI 镜像启动、文件系统级持久化（snapshot，或在 stop/start 之间保留磁盘）、某种 egress 控制、TTL 或空闲超时、按秒计量，以及 Python 和 TypeScript 的 SDK。**在创业公司里接近普及的**：把凭证挡在 sandbox 外的 secrets proxy。
- **较少见（14 家中约 6 家）**：保留内存、亚秒级恢复的 Hibernate（E2B、Blaxel、Morph、Together、Google、Substrate）。**只有少数几家有的差异化功能**：
  - 带内存的 Fork：E2B、Morph、Daytona VM、Google snapshots；
  - GPU：Modal、Daytona、Northflank；
  - Windows 和 macOS：Daytona；
  - BYOC 或部署到自己的 VPC：E2B、Daytona、Northflank、Runloop；
  - 用于 computer use 的桌面：E2B、Daytona、Runloop、Morph、Google。
- **定价分两种。** 一种按*分配的* CPU 收费：E2B、Daytona、Northflank、Runloop、Together，以及 Modal 的 max(request, usage)。另一种只按*实际使用的* CPU 收费，内存按分配量计：Vercel、Cloudflare、AWS。没有一家对 hibernated 或 suspended 状态的 sandbox 收计算费，但都收它的存储费。我们的 Usage 定义与此一致，只是缺少两项几家厂商确实收费的计量：egress 字节数，以及 Capture 出的 Template 占用的存储。
- **Agent 使用 sandbox 主要靠框架适配器，而不是 MCP。**
  - OpenAI Agents SDK 有 8 个 sandbox client；
  - LangChain Deep Agents 列了 30 个 sandbox 集成；
  - AI SDK 7 定义了一个 sandbox 接口；
  - Claude Managed Agents 的 self-hosted sandboxes 列出 Cloudflare、Daytona、Modal、Vercel 为合作方。

  MCP server 仍是少数，但在增加：Daytona、Vercel、Blaxel、Azure（preview）和 microsandbox 都有；E2B 则把自己的那个归档了。
- **路线图结论【推断】。** 作为学习项目，阶段规划是合理的：缺一项基本功能，另有一项权重过高。
  - **缺失**："停下但保留磁盘"、并能自动恢复的状态。几乎每家都有，而 `docker stop`/`start` 几乎免费就能提供。
  - **权重过高**：Frozen。没有厂商把它当卖点，ticket 09（Usage）却被它阻塞。
  - **保留 gVisor 作为 phase 2**：它是主流生产选择，并且支持 checkpoint/restore，可以比 phase 3 更早做出第一个 Hibernate。
  - **加一个薄的 MCP server**：既然 Python SDK 是非目标，这是让真实 agent 用上平台最便宜的办法。

---

## 2. 格局总表

"状态"一栏记录厂商自己页面上的说法；"未说明"指我读过的页面没有提到。

| 产品 | 隔离 | 持久化 | 关键功能 | 计价单位 | 状态 |
|---|---|---|---|---|---|
| **E2B** | Firecracker microVM | pause = 内存 + 磁盘，无限期保留；仅文件系统的 pause；snapshots；Fork；Volumes（private beta） | code interpreter contexts、桌面 sandbox、MCP gateway、secrets proxy、BYOC 与自托管 Embed | vCPU-秒 + GiB-秒（按分配）；磁盘免费 | GA（见[前一篇](agent-sandbox-usage.zh.md)） |
| **Daytona** v0 / v2 | v0：默认容器，可选 VM。v2：Firecracker（Linux）、Cloud Hypervisor（Windows、GPU）、Apple VZ（macOS） | v0：stop/start 保留磁盘、archive、VM 的 pause 和 Fork。v2：image capture、内存 snapshot 恢复为新 sandbox、Volumes | computer use、Windows/macOS、GPU、MCP server（v0 CLI）、BYOC | vCPU-小时 + GiB-小时，按秒计费 | v0 是"current"，v2 是"next"（见前一篇） |
| **Modal** | gVisor 或 VM | 文件系统/目录 snapshots；内存 snapshots（alpha）；Volumes | GPU（仅 gVisor）、secret 注入（beta）、tunnels | core-秒（1 core = 2 vCPU）+ GiB-秒，max(request, usage) | GA；部分功能 alpha/beta |
| **Vercel Sandbox** | Firecracker microVM | 默认持久：stop 时自动做文件系统 snapshot，恢复时自动还原；Drives（public beta） | 防火墙里的 credentials brokering、Vercel MCP 工具 | Active CPU-小时 + 分配内存 GB-小时 + 创建次数 + egress | GA，2026-01-30 |
| **Cloudflare Sandbox SDK / Containers** | Containers："Firecracker microVM with its own kernel and network"（[concepts](https://developers.cloudflare.com/sandbox/concepts/)）；Dynamic Workers：V8 isolate | 实例停止时磁盘随之消失；`snapshotContainer()` 只保存可写根文件系统，30 天过期（public beta）；R2/S3 bucket 挂载（[lifetime](https://developers.cloudflare.com/sandbox/concepts/lifetime/)） | 持久 code interpreter（Python、JS、TS）、PTY 终端、preview URL、注入凭证的 outbound handler、**默认不能上网**（[security](https://developers.cloudflare.com/sandbox/concepts/security/)） | active vCPU-秒 + 分配 GiB-秒 + 磁盘 GB-秒，以 10 ms 为单位计量；休眠时不收费（[pricing](https://developers.cloudflare.com/containers/pricing/)） | 2026-04-13 GA（[changelog](https://developers.cloudflare.com/changelog/post/2026-04-13-containers-sandbox-ga/)）；SDK 1.0 于 2026-09-30 发布，但没写"GA"；snapshots 为 public beta（[changelog](https://developers.cloudflare.com/changelog/post/2026-09-30-sandbox-sdk-1-0/)） |
| **Fly.io Sprites** | "hardware-isolated VM" / "dedicated microVM"；未说明是哪个 hypervisor（[产品页](https://fly.io/sprites)、[docs](https://docs.fly.io/sprites)） | 持久的 100 GB 磁盘，休眠后仍在；约 1 秒的 copy-on-write checkpoint，覆盖磁盘但**不含进程**；空闲时分 warm 和 cold 两态 | "sleeps when idle"，对其 URL 发请求即可唤醒；空闲计时器看 HTTP、stdout、TCP 和任务；connectors 让 provider 凭证不进 Sprite | CPU-小时 + 内存 GB-小时 + 热/冷存储 GB-小时 | 未说明 |
| **Northflank Sandboxes** | Cloud Hypervisor microVM；GPU sandbox 用 gVisor（[产品页](https://northflank.com/product/sandboxes)） | 默认临时；Volumes（含多读写挂载）、S3 FUSE 挂载、pause/resume | GPU（L4 到 B200）、在自己云账号里的 BYOC | vCPU-小时 + GB-小时，按秒计费 | 未说明（Enterprise SLA 99.99%） |
| **Runloop Devboxes** | "virtual machine technology"；未说明 hypervisor（[overview](https://docs.runloop.ai/docs/devboxes/overview.md)） | suspend/resume **只保留磁盘**："not in-memory state"（[lifecycle](https://docs.runloop.ai/docs/devboxes/lifecycle)）；磁盘 snapshot 可 fork 出新 Devbox；Blueprints | Agent Gateways（LLM key 不进 sandbox）、MCP Hub、Playwright 浏览器、Ubuntu 桌面、network policies、tunnels（[features](https://docs.runloop.ai/docs/overview/runloop-features.md)） | CPU-秒 + GB-秒 + 存储 GB-秒；Pro $250/月（[pricing](https://www.runloop.ai/pricing)） | 未说明；SOC2 |
| **Morph Cloud** | VM（"Instances … the underlying VM objects"）（[mental model](https://cloud.morph.so/docs/concepts/mental-model)） | pause 保留内存："with all processes and memory state intact"；snapshots；branching = 做 snapshot 再起 N 个新实例（[pause-resume](https://cloud.morph.so/docs/documentation/instances/pause-resume)、[branch](https://cloud.morph.so/docs/documentation/instances/branch)） | 远程桌面、devboxes、S3 兼容的 Volumes | MCU = 1 vCPU-h + 4 GB RAM-h + 16 GB 磁盘-h，或 5 TB snapshot-小时；plans 页上没有每 MCU 的价格（[plans](https://cloud.morph.so/docs/documentation/setup/plans)） | 未说明 |
| **Blaxel Sandboxes** | "lightweight, sandboxed virtual machines"，EROFS + tmpfs overlay（[overview](https://docs.blaxel.ai/Sandboxes/Overview)） | standby 保留内存和进程，"in under 25 milliseconds" 恢复，空闲几秒后进入；archive 只保留文件系统；Volumes；Agent Drive | **每个 sandbox 一个 MCP server**（[MCP](https://docs.blaxel.ai/Sandboxes/MCP.md)）、preview URL、终端 | 内存 GB-秒（CPU 随内存捆绑）；standby 计算费为 $0；snapshot 存储 $0.20/GB-月（[pricing](https://blaxel.ai/pricing)） | 未说明；部分功能（in-place API upgrade、Agent Drive）为 beta |
| **Together Code Sandbox**（CodeSandbox SDK） | "CodeSandbox's proprietary implementation of Firecracker"（[发布文章，2025-05-20](https://www.together.ai/blog/code-sandbox)） | 基于内存 snapshot 的 hibernation，P95 恢复 511 ms；终止时可做 snapshot（[docs](https://docs.together.ai/docs/together-code-sandbox)） | 远程镜像构建器，2–64 vCPU | vCPU-小时 + GiB-小时（2025 年发布时价格） | **需开通**："your organization must have them enabled"；旧的 CodeSandbox SDK 是"legacy"；2025 年的价格可能已过时 |
| **Google Agent Platform sandboxes**（原 Vertex AI Agent Engine Code Execution） | Google 托管 tenant project 里的 "a gVisor-isolated container"（[sandbox overview](https://docs.cloud.google.com/gemini-enterprise-agent-platform/scale/sandbox)） | code execution 保留 "execution state (memory) for up to 14 days"；custom containers 为 7 天；snapshots 包含 "memory state"，可以分支（[code exec](https://docs.cloud.google.com/gemini-enterprise-agent-platform/scale/sandbox/code-execution-overview)、[snapshots](https://docs.cloud.google.com/gemini-enterprise-agent-platform/scale/sandbox/manage-snapshots)） | code execution（库固定、"no network access"、I/O 100 MB）、computer use、BYOC 自定义容器、VPC-SC、CMEK；架构以开源形式发布（Substrate） | 我读过的官方页面上没找到 | 未说明。产品已改名，旧的 `cloud.google.com/vertex-ai/...` URL 会重定向 |
| **AWS Bedrock AgentCore Code Interpreter** | "runs in a containerized environment"（[docs](https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/code-interpreter-tool.html)）；AgentCore Runtime 本身用 microVM（[pricing](https://aws.amazon.com/bedrock/agentcore/pricing/)） | 状态限于 session；默认超时 15 分钟，最长 8 小时（[sessions](https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/code-interpreter-resource-session-management.html)） | Python/JS/TS、shell 和 AWS CLI、S3 文件最大 5 GB、网络模式 Sandbox 或 Public、CloudTrail | vCPU-小时 + GB-小时，按秒，最少 1 秒；**I/O 等待和空闲时 CPU 免费** | 我读过的页面未说明；Runtime 的托管 session 存储为 "public preview" |
| **Azure Container Apps dynamic sessions** | "Hyper-V isolation"（[overview](https://learn.microsoft.com/en-us/azure/container-apps/sessions)） | 临时；cooldown 期后销毁 | 预热的 session pool；code interpreter、custom container 和 shell 三种 pool；**平台托管的 MCP server（preview）**（[MCP 教程](https://learn.microsoft.com/en-us/azure/container-apps/sessions-tutorial-shell-mcp)） | code interpreter：按已分配 session 计费，以 1 小时为增量。custom container：Dedicated plan 的 E16 节点（[billing](https://learn.microsoft.com/en-us/azure/container-apps/billing)） | overview 页没有 preview 标记；MCP server 为 **preview**（API `2025-02-02-preview`） |
| **OpenAI containers**（Code Interpreter 与 hosted shell） | "a fully sandboxed virtual machine"（[code interpreter](https://developers.openai.com/api/docs/guides/tools-code-interpreter)） | 空闲 20 分钟后过期，"can't be reactivated" | 1/4/16/64 GB 档位；hosted shell **默认不能出网**，有组织级 allowlist 和 `domain_secrets` 占位符（[shell](https://developers.openai.com/api/docs/guides/tools-shell)） | 按 container session（20 分钟）和内存档位计价，按分钟计费，最少 5 分钟（[pricing](https://developers.openai.com/api/docs/pricing)） | 未说明；文档已从 platform.openai.com 迁到 developers.openai.com |
| **kubernetes-sigs/agent-sandbox**（OSS） | 交给 RuntimeClass，"like gVisor or Kata Containers"（[README](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/README.md)） | 重启后保留的持久存储；pause/resume；deep hibernation 只是 "desired" | `Sandbox`、`SandboxTemplate`、`SandboxClaim`、`SandboxWarmPool` CRD；router；Python 和 Go SDK | —（自托管） | v1.0.12（2026-11-20）；API `agents.x-k8s.io/v1beta1` |
| **Agent Substrate**（OSS） | gVisor 或 Cloud Hypervisor；"Plain Linux containers are not supported"（[README](https://github.com/agent-substrate/substrate)） | 空闲时对完整状态（RAM + 文件系统）做 snapshot，收到请求时恢复；恢复时间 < 500 ms | 默认拒绝的网络、注入凭证的 egress gateway、每个 actor 一个 SPIFFE 身份、10–30 倍超卖 | —（自托管） | v1.0.1（2026-11-20） |
| **microsandbox**（OSS） | microVM（"Hardware-level isolation with tiny virtual machines"）；致谢中列了 libkrun（[repo](https://github.com/microsandbox/microsandbox)） | 运行状态的 snapshot 与恢复 | local-first，可在 Linux、macOS、Windows 上运行，有 TS/Rust/Python/Go/Ruby SDK，**有 MCP server** | —（自托管） | "still beta software"；v0.7.16（2026-11-18） |

**【推断】各家一句话定位：**
- E2B、Daytona：通用的 agent 电脑。
- Modal：带 sandbox 和 GPU 的计算平台。
- Vercel、Cloudflare：挂在开发者平台上的 sandbox。
- Runloop：编程 agent 的 devbox 加 benchmark。
- Morph：用于搜索和 RL 的可分支 VM。
- Blaxel：常驻、空闲时便宜（standby）的 agent。
- Sprites：持久的"agent 专用电脑"。
- Northflank：面向企业的 microVM 加 BYOC。
- 云厂商：各自 agent 平台里的 code interpreter 工具。
- OSS：在 Kubernetes 或本地自己搭。

---

## 3. 基本功能与差异化功能

✓ = 有文档说明。◐ = 部分支持、beta，或只在某些 sandbox 类型上有。– = 我读过的页面没提到（不代表没有）。前四列来自 [agent-sandbox-usage.zh.md](agent-sandbox-usage.zh.md)。

| 功能 | E2B | Daytona | Modal | Vercel | Cloudflare | Sprites | Northflank | Runloop | Morph | Blaxel | Google | AWS CI | Azure | OpenAI | 计数 ✓/◐ |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| Exec（+ 流式） | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | 14 |
| 文件 API | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | – | ✓ | – | ✓ | ✓ | ✓ | ✓ | ✓ | 12 |
| 后台进程 | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | – | ✓ | – | ✓ | – | ◐ | – | – | 10 |
| 端口 / preview URL | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | – | ✓ | ✓ | ✓ | ✓ | – | ◐ | – | 11 |
| 有状态的 code interpreter | ✓ | ✓ | – | – | ✓ | – | – | – | – | – | ✓ | ✓ | ✓ | ✓ | 7 |
| 文件系统持久化（snapshot 或保留磁盘） | ✓ | ✓ | ✓ | ✓ | ◐ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | – | – | – | 11 |
| 保留内存的 Hibernate（≈ 我们的 Hibernated） | ✓ | ◐ | ◐ | – | – | – | – | – | ✓ | ✓ | ✓ | – | – | – | 6 |
| 带内存的 Fork | ✓ | ◐ | – | – | – | – | – | – | ✓ | ◐ | ✓ | – | – | – | 5 |
| Volumes / bucket 挂载 | ◐ | ✓ | ✓ | ◐ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | – | ◐ | – | – | 11 |
| 凭证不进 sandbox | ✓ | ✓ | ◐ | ✓ | ✓ | ✓ | – | ✓ | – | – | – | – | – | ✓ | 8 |
| Egress allowlist | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | – | ✓ | – | ✓ | ◐ | ◐ | ◐ | ✓ | 12 |
| GPU | – | ✓ | ✓ | – | – | – | ✓ | – | – | – | – | – | – | – | 3 |
| 桌面 / computer use | ✓ | ✓ | – | – | – | – | – | ✓ | ✓ | – | ✓ | – | – | – | 5 |
| Windows / macOS 客户机 | – | ✓ | – | – | – | – | – | – | – | – | – | – | – | – | 1 |
| BYOC / 部署到自己的 VPC | ✓ | ✓ | – | – | – | – | ✓ | ✓ | – | – | – | – | – | – | 4 |
| Python + TS SDK | ✓ | ✓ | ✓ | ✓ | ◐ (TS) | ✓ | – | ✓ | ◐ (Py) | ✓ | ◐ | – | – | ✓ | 10 |
| 厂商自带 MCP server | ◐ | ✓ | – | ✓ | – | – | – | ◐ | – | ✓ | – | – | ◐ | – | 6 |

每格的来源见 §2 和前一篇。有几格需要说明：
- Google 的 egress 控制是 VPC-SC 加 template 上的 `internet_access` 设置（[custom containers](https://docs.cloud.google.com/gemini-enterprise-agent-platform/scale/sandbox/custom-containers)）。
- AWS 只有 "Sandbox 或 Public" 两种网络模式。Azure 的控制项是 `sessionNetworkConfiguration`（[MCP 教程](https://learn.microsoft.com/en-us/azure/container-apps/sessions-tutorial-shell-mcp)）。
- E2B 的 MCP 是在 sandbox *内部*运行 MCP server 的 gateway（[mcp-gateway](https://docs.e2b.dev/mcp-gateway.md)）。它用来控制 sandbox 的 `e2b-dev/mcp-server` **已归档**（GitHub API，2026-11-21 查询）。
- Runloop 的 MCP Hub 是通往其他 MCP server、并隐藏凭证的 gateway（[features](https://docs.runloop.ai/docs/overview/runloop-features.md)）。
- 自托管的 OSS 不计入。

计数列把 ✓ 和 ◐ 一起算。

**【推断】分组：**
- **基本功能（14 家中 ≥ 10）**：exec、文件、后台进程、端口、文件系统持久化、volumes 或挂载、egress allowlist、Python + TS SDK。
- **托管 sandbox 产品里通常会有（6–9）**：有状态的 code interpreter（7）、secrets proxy（8）、保留内存的 Hibernate（6）。MCP server 也有 6 家，但其中一半只是部分支持，算作正在兴起。
- **差异化功能（≤ 5）**：带内存的 Fork、桌面、BYOC、GPU、Windows/macOS。

我们的 spec 覆盖了所有基本功能，只差一项：**保留 Sandbox 身份的文件系统持久化**。我们有 Capture，但 Capture 会生成一个新 Template，换来一个新的 Sandbox ID。Vercel（"persistent by default"）、Sprites、Runloop suspend、Daytona v0 的 stop/start 和 Blaxel archive，保留的都是同一个 sandbox 和它的磁盘。

---

## 4. 定价对比

所有价格都是各页面上的标价；换算成每小时的数字是我自己算的。

| 产品 | CPU | 内存 | 存储 / snapshot | 其他计量 | 空闲 / hibernated / paused |
|---|---|---|---|---|---|
| E2B | $0.000014/vCPU-秒 ≈ $0.0504/vCPU-小时，**按分配** | $0.0000045/GiB-秒 ≈ $0.0162/GiB-小时 | 磁盘 "included … at no extra cost"（[pricing](https://e2b.dev/pricing)） | 套餐费（Pro $150/月）、并发加购 | "Compute (vCPU and RAM) is not billed for paused or killed sandboxes"（[FAQ](https://docs.e2b.dev/faq/calculate-sandbox-price.md)）；paused 的存储是否收费未说明 |
| Daytona | $0.0504/vCPU-小时 | $0.0162/GiB-小时 | $0.000108/GiB-小时，前 5 GiB 免费（[daytona.io pricing](https://www.daytona.io/pricing)）；v2 文档说临时磁盘免费、Volumes 收费 | GPU 按小时；Windows/macOS 按 core | v2：stopped 或 paused 的 sandbox 不收计算费，但 "its volumes stay billed"（[billing](https://www.daytona.com/docs/billing.md)） |
| Modal | $0.00003942/core-秒（1 core = 2 vCPU）≈ $0.071/vCPU-小时 | $0.00000667/GiB-秒 ≈ $0.024/GiB-小时 | Volumes $0.09/GiB-月 | Egress $0.04/GiB；按 "max(request, usage)" 计费；最少 0.125 core / 128 MiB（[pricing](https://modal.com/pricing)） | 没有 pause 状态 |
| Vercel | **Active CPU** $0.128/小时（不含 I/O 等待） | **按分配** $0.0212/GB-小时，最小增量 1 分钟 | Snapshots $0.08/GB-月；Drives $0.05/GB-月，另计读写 | 每百万次创建 $0.60；出站流量和暴露端口的流量收费，下载免费（[pricing](https://vercel.com/docs/sandbox/pricing)） | stopped = 文件系统 snapshot，按 snapshot 存储计费 |
| Cloudflare | $0.000020/vCPU-秒 ≈ $0.072/vCPU-小时，**只算 active** | $0.0000025/GiB-秒 ≈ $0.009/GiB-小时，按分配 | 磁盘 $0.00000007/GB-秒，按分配；beta 期间 snapshots 免费 | Egress $0.025/GB（北美/欧洲）；10 ms 粒度；Workers Paid $5/月（[pricing](https://developers.cloudflare.com/containers/pricing/)） | "Charges stop after the container instance goes to sleep" |
| Fly.io Sprites | $0.0385/CPU-小时 | $0.021875/GB-小时 | 热存储 $0.000683/GB-小时（醒着时）；冷存储 $0.000027/GB-小时 | — | "Warm and cold Sprites are not billed for compute, though storage still accrues"（[产品页](https://fly.io/sprites)） |
| Northflank | $0.01667/vCPU-小时 | $0.00833/GB-小时 | 临时磁盘免费 | BYOC：每个托管 vCPU-小时 $0.02083（[产品页](https://northflank.com/product/sandboxes)） | 未说明 |
| Runloop | $0.108/CPU-小时（$0.00003/秒） | $0.0252/GB-小时 | Devbox $0.00034/GB-小时；snapshot 和 blueprint $0.000072/GB-小时 | Blueprint 构建 $0.252/小时；Pro $250/月；"No minimums"（[pricing](https://www.runloop.ai/pricing)） | suspended：不收 CPU 和内存费，存储继续收费。注意：页面上自带的算例与它的单价对不上（约 $2.53 对比它写的每天 $2.73） |
| Morph | MCU 打包（1 vCPU-h + 4 GB-h + 16 GB 磁盘-h） | （含在 MCU 里） | 5 TB snapshot-小时 = 1 MCU | 官方 plans 页上没有 MCU 单价 | paused："only be charged for storage, not compute"（[pause-resume](https://cloud.morph.so/docs/documentation/instances/pause-resume)） |
| Blaxel | 不单独计费（随内存捆绑） | $0.0000115/GB-秒 ≈ $0.041/GB-小时 | Snapshots $0.20/GB-月；镜像 $0.045/GB-月；Volumes $0.12/GB-月 | Egress 已包含（[pricing](https://blaxel.ai/pricing)） | standby："$0 compute"；需付 snapshot 存储费 |
| Together | $0.0446/vCPU-小时 | $0.0149/GiB-小时 | 未说明 | — | 未说明。这是 **2025 年发布时的价格**，现在只提供定制方案（[blog](https://www.together.ai/blog/code-sandbox)、[docs](https://docs.together.ai/docs/together-code-sandbox)） |
| AWS AgentCore CI | $0.0895/vCPU-小时 | $0.00945/GB-小时，按**峰值**内存 | — | 按秒，最少 1 秒，最少 128 MB；传输按 EC2 价格（[pricing](https://aws.amazon.com/bedrock/agentcore/pricing/)） | "I/O wait and idle time is free, if no other background process is running"（只限 CPU） |
| Azure dynamic sessions | code interpreter：按已分配 session 计费，以 1 小时为增量 | — | — | custom container：Dedicated plan 的 E16 节点（[billing](https://learn.microsoft.com/en-us/azure/container-apps/billing)） | cooldown 结束即销毁 |
| OpenAI containers | 打包在内 | 1 GB 每 20 分钟 $0.03，64 GB 为 $1.92（≈ $0.09/GB-小时） | — | 按分钟计费，最少 5 分钟（[pricing](https://developers.openai.com/api/docs/pricing)） | 空闲 20 分钟后过期 |
| Google Agent Platform | 我读过的官方页面上没找到 | | | | |

**和我们的 Usage 定义对比**（Running：vCPU-秒 + GB-秒；Frozen：GB-秒；Hibernated：存储；Volume：存储）：

1. **状态模型与市场一致【推断】。** 凡是有 hibernate 或 suspend 状态的厂商，都对这个状态收存储费、不收计算费：E2B、Daytona、Morph、Runloop、Blaxel、Sprites、Cloudflare。没有一家对 hibernated 的 sandbox 收内存费。我们 Frozen = GB-秒 的思路，等同于 Vercel 和 Cloudflare 的"空闲时仍按分配内存收费"，也等同于 AgentCore Runtime 的 "memory billed during idle time until … reclaimed"（[pricing](https://aws.amazon.com/bedrock/agentcore/pricing/)）。只是没有人把它作为一个独立状态来卖。
2. **按分配 vs 按实际 CPU。** 我们的 Running 计量是分配的 vCPU-秒，与 E2B、Daytona、Northflank、Runloop 一致。Vercel、Cloudflare、AWS 只收 active CPU，因为在 agent 工作负载里 "time spent waiting for … AI model calls" 占大头（[Vercel](https://vercel.com/docs/sandbox/pricing)）。**【推断】** 在分配的 vCPU-秒之外，*额外*记录一个实际 CPU 时间的原始计量（cgroup `cpu.stat` 的 `usage_usec`，在 Docker 上读取成本很低）。代价很小，而对比表就能显示 agent-inside 的 Sandbox 实际上有多闲。
3. **几家厂商收费、我们却没有的计量：**
   - egress 字节数：Modal、Vercel、Cloudflare、AWS；
   - Capture 出的 Template 或 snapshot 占用的存储：Vercel、Blaxel、Runloop，Cloudflare beta 结束后也会收；
   - 创建次数：Vercel。

   **【推断】** 在 Usage 里加上 egress 字节数（Egress Proxy 本来就看得到每个字节）和 Template 存储；创建次数和最小计费增量不必加。定价仍是非目标，这里只是让原始 Usage 覆盖市场上实际计量的东西。

---

## 5. 接口：agent 实际怎么用 sandbox

**SDK。** Python + TypeScript 是底线：E2B、Daytona v2、Vercel、Runloop、Blaxel、Together（[E2B llms.txt](https://docs.e2b.dev/llms.txt)、[Daytona llms.txt](https://www.daytona.com/docs/llms.txt)、[Runloop](https://docs.runloop.ai/docs/devboxes/overview.md)、[Blaxel](https://docs.blaxel.ai/Sandboxes/Overview)、[Together](https://docs.together.ai/docs/together-code-sandbox)）。提供 Go 的有 Modal（[sandboxes](https://modal.com/docs/guide/sandboxes)）、Sprites（[产品页](https://fly.io/sprites)）、kubernetes-sigs/agent-sandbox（[README](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/README.md)）和 microsandbox（[repo](https://github.com/microsandbox/microsandbox)）。Cloudflare 只有 TypeScript，因为它的 SDK 跑在 Worker 里（[docs](https://developers.cloudflare.com/sandbox/)）。

**框架集成（主要渠道）。**
- **OpenAI Agents SDK "Sandbox Agents"** 内置了 Blaxel、Cloudflare、Databricks、Daytona、E2B、Modal、Vercel、Docker 和本地 Unix 的 client（[clients](https://openai.github.io/openai-agents-python/sandbox/clients/)）。SDK 警告本地 client "adds no OS-level confinement"（[sandbox agents](https://openai.github.io/openai-agents-python/sandbox_agents/)）。
- **LangChain / Deep Agents** 列了 30 个 sandbox 集成，包括 Daytona、Modal、AWS AgentCore、Vercel、E2B、Runloop 和 Kubernetes（[integrations](https://docs.langchain.com/oss/python/integrations/sandboxes)）。
- **Vercel AI SDK 7** 定义了一个 sandbox 接口，"a standard surface (`run`, `spawn`, file I/O)"。`@ai-sdk/sandbox-vercel` 和 E2B 实验性的 `@e2b/ai-sdk-sandbox` 实现了它（[E2B 页面](https://docs.e2b.dev/agents/vercel-ai-sdk.md)）。我没找到 ai-sdk.dev 上介绍这个接口的官方页面，这条只依据 E2B 的页面。
- **Claude Agent SDK。** Anthropic 的 hosting 指南有 Docker、Modal 和 Kubernetes 的 cookbook 代码。SDK "needs outbound HTTPS to `api.anthropic.com`"，指南建议把流量 "through an egress proxy that enforces domain allowlists, injects credentials, and logs requests"（[hosting](https://code.claude.com/docs/en/agent-sdk/hosting)）。这几乎逐字就是我们 ADR 0005/0006 的设计。
- **Claude Managed Agents 的 self-hosted sandboxes**（public beta，2026-05-19）列出 Cloudflare、Daytona、Modal、Vercel 为合作方（[公告](https://claude.com/resources/articles/claude-managed-agents-updates)）。

**sandbox 厂商提供的 MCP server。** 有三种模式。

| 模式 | 谁 | 形态 |
|---|---|---|
| A. 控制面 MCP：agent 用它创建和操作 sandbox | Daytona（`daytona-v0 mcp init/start`，本地运行，适配 Claude、Cursor、Windsurf）（[docs](https://www.daytona.io/docs/en/mcp.md)）；Vercel MCP（工具从 REST 端点一一生成，如 `run_session_command`、`write_session_files`、`update_session_network_policy`）（[tools](https://vercel.com/docs/agent-resources/vercel-mcp/tools/sandboxes.md)）；Azure（2 个工具 `launchShell` 和 `runShellCommandInRemoteEnvironment`，HTTP 上的 JSON-RPC，API key 放在 header 里，**preview**）（[教程](https://learn.microsoft.com/en-us/azure/container-apps/sessions-tutorial-shell-mcp)）；microsandbox（[repo](https://github.com/microsandbox/microsandbox)） | 现有 API 上的一层薄封装 |
| B. 由 sandbox 内的 agent 提供的每 sandbox 一个 MCP 端点 | Blaxel（`https://<sandbox>/mcp`，streamable HTTP，bearer token，提供进程、文件和代码搜索工具）（[MCP](https://docs.blaxel.ai/Sandboxes/MCP.md)） | Guest Agent 直接讲 MCP |
| C. 托管在 sandbox *内部*或*背后*的 MCP server | E2B MCP gateway（来自 Docker catalog 的 200+ server，`getMcpUrl()` + token）（[gateway](https://docs.e2b.dev/mcp-gateway.md)）；Runloop MCP Hub（[features](https://docs.runloop.ai/docs/overview/runloop-features.md)） | 一个工具目录，而不是 sandbox 控制 |

**【推断】我们要不要做一个？** 要：做模式 A。一个单独的小程序，只调用公共 REST API，提供少量工具：create、exec、process 启动/读输出、写文件、读文件、暴露端口、delete。理由：
1. Python SDK 是非目标，MCP server 是不做 SDK 也能让真实 agent（Claude Code、Cursor、Copilot）用上平台的最便宜办法。
2. Vercel 的 MCP 工具是从 REST 机械生成的，Azure 只有两个工具，说明它不需要做大。
3. 它顺便成为第三个 demo，"通过 MCP 的 agent outside"；ticket 04/05/06/11 做完后几乎不需要额外成本。
4. 它只用公共 API，和 `bench/` 一样是黑盒客户端，不影响 ADR 0005。

模式 B（由 Guest Agent 提供每 sandbox 的端点）以后可以考虑，但它会在不可信区域的紧邻处增加协议面，不适合放在 phase 1。

---

## 6. 路线图评审（本节全部是【推断】，依据 §2–§5）

### 6.1 计划做对了的地方

- **默认拒绝 egress 其实并不另类。** 前一篇发现四家开发型 sandbox 厂商都默认放开。范围扩大后可以看到，下面几家都默认拒绝：
  - Cloudflare Containers（`enableInternet: false`）；
  - OpenAI hosted shell（"don't have outbound network access by default"）；
  - Google code execution（"no network access"）；
  - Substrate（"Default-deny networking"）。

  保持现状，连同 Allowlist Preset 和 DNS resolver 一起。
- **Secret 以占位符形式存在、由 Egress Proxy 替换（ticket 23）**，在托管产品里已经普及：E2B、Daytona、Modal、Vercel、Cloudflare 的 outbound handler、Sprites connectors、Runloop Agent Gateways、OpenAI `domain_secrets`、Substrate 的 egress gateway。Anthropic 自己的 hosting 指南也这样建议。放在 phase 1 是对的。
- **Capture（ticket 22）** 对应业界主要的持久化方式（文件系统 → 镜像），而且三种 Runtime 都能做。
- **分开测冷启动和 snapshot 恢复（phase 3）** 是必要的。公开数字都是 snapshot 恢复：E2B 约 80 ms；Blaxel 从 standby "< 25 ms"；Together 从 hibernation 恢复 P95 511 ms，而冷启动是 2.7 s；Substrate 恢复 "< 500 ms"。

### 6.2 缺失：几乎每家都有的东西

1. **保留 Sandbox 身份、保留文件系统的 stop/resume，并能自动恢复。** 14 家中有 11 家有文件系统持久化，而且大多数是在*同一个* sandbox 上：
   - Vercel 在下一次调用时自动恢复；
   - Sprites 休眠后 "wakes with everything where it was"；
   - Runloop suspend 只保留磁盘；
   - Daytona v0 的 stop/start；
   - Blaxel archive；
   - E2B 的 `pause({mode:'filesystem'})`。

   我们的模型里有 Frozen（占着内存）、Hibernated（phase 3，只在 Firecracker 上）和 Capture（新 Template，因此是新 ID、新端口、新 token）。在 Docker 上，`docker stop` + `docker start` 几乎不费力就给出这个状态。它的 Usage 只算存储；"空闲 → stop，请求 → start"的策略可以复用 ticket 07 的空闲检测和 ticket 11 的端口代理。**建议**：为此在 phase 1 加一个 ticket。前提是先在 GLOSSARY 里定下名字：现在的 *Avoid* 列表因为含义模糊而排除了 "stopped" 和 "suspended"，所以要给这个状态起一个独立的名字，并写明"保留文件系统，进程重启"。有了 Firecracker 之后，phase 3 的 Hibernated 就是同一状态"带内存"的升级版。
2. **一个薄的 MCP server**（见 §5）。14 家中有 6 家提供了某种 MCP；而我们不出 Python SDK，所以它是接入真实 agent 客户端最便宜的路。
3. **运行时可更新的 Egress Policy。** E2B 的 `updateNetwork`、Vercel 的 "switchable at runtime"、Daytona v0 的运行时规则都允许这样做（见[前一篇](agent-sandbox-usage.zh.md)）。agent 往往只在初始化步骤（比如 `pip install`）需要网络，之后就想关掉。这是对 ticket 12 的一个小补充。
4. **Process 的 stdin / PTY。** E2B、Daytona、Cloudflare（GA 前加入了 PTY）、Blaxel（终端）和 Sprites（`sprite console`）都提供。`claude -p` 用不到，但这是人和很多 harness 默认期待的能力。它是 ticket 06 的一个小扩展，可以是可选项。
5. **egress 字节数和 Template 存储的 Usage 计量**（见 §4.3）。

### 6.3 范围过大或权重过高

1. **Frozen（ticket 08）成了 Usage（ticket 09）的依赖。** 没有厂商把原地冻结作为产品状态来提供（前一篇 §5）。不过它仍是理解 cgroup freezer 的一个好而便宜的练习，ticket 可以保留。但要**把 08 从 09 的 "blocked by" 里去掉**：Usage 可以先只支持 Running，Frozen 和新的 stopped 状态以后再作为新行加进去。
2. **Volumes（ticket 15），至少是现在这种形式。** Volume 常见，但往往不成熟：E2B 的处于 private beta，Vercel Drives 处于 public beta。成熟的那些，市场惯例是*多挂载*，如 Northflank、Daytona、Modal。单挂载的 Volume 会给 Quota 和 Usage 各加一个维度，带来的价值却不如 stopped 状态。建议把 15 挪到 phase 1 末尾、排在 stopped 状态的 ticket 之后，做的时候再考虑只读多挂载（spec §10 已经列了这一项）。
3. **phase 3 负担过重。** 它同时包含 Firecracker、OCI 转 rootfs、vsock、Hibernate、Fork、自动 hibernate 与自动恢复、冷启动与恢复的测量，以及 SWE-bench。建议拆成 **3a**：Firecracker Runtime，跑同样的测试套件，加上冷启动与恢复的对比；**3b**：Hibernate、Fork、自动 hibernate 和 SWE-bench。Fork 是只有约 5 家才有的差异化功能，适合放到靠后的子阶段。

### 6.4 gVisor 作为 phase 2 值不值得，还是直接上 Firecracker？

证据：
- gVisor 是生产环境里的隔离边界：Modal（两种 runtime 之一，也是 GPU 唯一可用的那种）、Google Agent Platform sandboxes（"gVisor-isolated container"）、Northflank（GPU）和 Substrate。
- kubernetes-sigs/agent-sandbox 把它列为首选的 RuntimeClass。
- Firecracker 在主打速度的创业公司里占主导：E2B、Vercel、Daytona v2、Cloudflare、Together。
- gVisor 还支持 `runsc checkpoint` / `restore`，可以保存进程和内存状态（[gVisor docs](https://gvisor.dev/docs/user_guide/checkpoint_restore/)）。该页面有一个限制："Docker does not support restoration into new containers."

**建议：保留 gVisor 作为 phase 2，并把它做成一个小阶段，加一个 stretch 目标。**
- 对比表是这个项目的核心问题，而 gVisor（用户态内核）恰好是 namespace 容器和 microVM 之间的中间点。跳过它，等于丢掉最有教学价值的那个数据点。
- 在 Docker 上它只是换一个 OCI runtime（`--runtime=runsc`），phase 1 的大部分东西原样沿用。它成本低，也不需要嵌套虚拟化，普通的 Linux VM 就能跑。
- stretch 目标：用 `runsc checkpoint` 并恢复到同一个容器，做出**第一个基于 gVisor 的 Hibernated**。这与 Google sandboxes 提供 "memory state" snapshot 的方式一致，也让 phase 3 有一个基线，可以和 Firecracker 的 snapshot 对比。

隔离层面的细节取舍由另一位 agent 负责，这里只谈产品层面。

### 6.5 修订后的阶段规划建议

| 阶段 | 范围（**加粗**为改动） | 在哪里跑 |
|---|---|---|
| 1 | Docker Runtime；现有全部功能；**另加：保留文件系统的 stopped 状态，空闲自动 stop、请求自动恢复；薄 MCP server；运行时可更新的 Egress Policy；egress 字节数和 Template 存储的 Usage 计量**；两个 demo agent；测试框架；`docs/learn/01-docker.md` | Mac |
| 2 | gVisor Runtime；重跑测试框架；**stretch：用 `runsc checkpoint`/`restore` 原地恢复实现 Hibernated**；`docs/learn/02-gvisor.md` | Linux VM 或 GCP |
| 3a | Firecracker Runtime：rootfs 转换、vsock、同样的测试套件，**冷启动与 snapshot 恢复分开测** | GCP n2（嵌套虚拟化） |
| 3b | **Firecracker 上的 Hibernated 和 Fork，超时自动 hibernate、有流量自动恢复**（即 stopped 状态升级为保留内存）、SWE-bench 子集、`docs/learn/03-firecracker.md` | 同上 |
| 4 | GPU，不变（14 家里只有 3 家提供，放在最后是对的）。注意 Modal 和 Northflank 把 GPU 放在 gVisor 上，所以 phase 2 的 Runtime 就是容器级 GPU 的路径。 | GCP g2；裸金属 |

### 6.6 phase 1 ticket 优先级调整

| 调整 | Ticket | 理由（章节） |
|---|---|---|
| **新增，排在 07 和 11 之后**：保留文件系统的 stopped 状态 + 空闲自动 stop + 请求自动恢复（`docker stop`/`start`）；先写 GLOSSARY 条目 | 25 | §6.2.1 |
| **新增，排在 04、05、06、11 之后；放进 M1 或紧随其后**：基于公共 API 的薄 MCP server（stdio + streamable HTTP；6–8 个工具） | 26 | §5、§6.2.2 |
| 把 08 从 09 的阻塞项中去掉 | 09 | §6.3.1 |
| 加上 egress 字节数和 Template 存储计量 | 09 | §4.3 |
| 加上 Egress Policy 的运行时更新 | 12 | §6.2.3 |
| 可选：Process 的 stdin/PTY | 06 | §6.2.4 |
| 挪到 phase 1 末尾；评估只读多挂载 | 15 | §6.3.2 |
| 降低优先级（作为学习 ticket 保留） | 08 | §6.3.1 |
| 不变，调研结果确认其必要 | 12、14、22、23 | §6.1 |

**按重要性排序：** (1) 带自动恢复的 stopped 状态；(2) MCP server；(3) phase 2 保持精简、加 C/R stretch 目标，并拆分 phase 3；(4) 让 Usage 与 Frozen 解耦，推迟 Volumes；(5) 运行时可更新的 Egress Policy；(6) 新的 Usage 计量；(7) PTY。

**厂商有、但建议不做的：**
- 平台上构建镜像（E2B templates、Runloop Blueprints、Together 的构建器）；
- phase 4 之前的 GPU；
- Windows 和 macOS 客户机；
- BYOC。

这些是产品功能，对项目要回答的问题没有帮助。

---

## 7. 来源（均于 2026-11-21 访问）

> 来源列表与英文版一致，条目名称保留英文原文。

**Prior research (E2B, Daytona, Modal, Vercel):** [agent-sandbox-usage.md](agent-sandbox-usage.md) and its source list.

**Pricing pages for the four above**
- E2B https://e2b.dev/pricing ; https://docs.e2b.dev/faq/calculate-sandbox-price.md ; MCP gateway https://docs.e2b.dev/mcp-gateway.md ; AI SDK https://docs.e2b.dev/agents/vercel-ai-sdk.md ; index https://docs.e2b.dev/llms.txt ; archived repo https://github.com/e2b-dev/mcp-server
- Daytona https://www.daytona.io/pricing ; https://www.daytona.com/docs/billing.md ; https://www.daytona.com/docs/llms.txt ; MCP https://www.daytona.io/docs/en/mcp.md
- Modal https://modal.com/pricing ; https://modal.com/docs/guide/sandboxes
- Vercel https://vercel.com/docs/sandbox/pricing ; MCP tools https://vercel.com/docs/agent-resources/vercel-mcp/tools/sandboxes.md

**Cloudflare:** https://developers.cloudflare.com/sandbox/ ; https://developers.cloudflare.com/sandbox/concepts/ ; https://developers.cloudflare.com/sandbox/concepts/lifetime/ ; https://developers.cloudflare.com/sandbox/concepts/security/ ; https://developers.cloudflare.com/containers/pricing/ ; https://developers.cloudflare.com/changelog/post/2026-04-13-containers-sandbox-ga/ ; https://developers.cloudflare.com/changelog/post/2026-09-30-sandbox-sdk-1-0/

**Fly.io Sprites:** https://fly.io/sprites (redirected from sprites.dev) ; https://docs.fly.io/sprites

**Northflank:** https://northflank.com/product/sandboxes

**Runloop:** https://docs.runloop.ai/ ; https://docs.runloop.ai/docs/overview/runloop-features.md ; https://docs.runloop.ai/docs/devboxes/overview.md ; https://docs.runloop.ai/docs/devboxes/lifecycle ; https://docs.runloop.ai/llms.txt ; https://www.runloop.ai/pricing

**Morph Cloud:** https://cloud.morph.so/docs/documentation/overview ; https://cloud.morph.so/docs/concepts/mental-model ; https://cloud.morph.so/docs/documentation/instances/branch ; https://cloud.morph.so/docs/documentation/instances/pause-resume ; https://cloud.morph.so/docs/documentation/setup/plans

**Blaxel:** https://docs.blaxel.ai/Sandboxes/Overview ; https://docs.blaxel.ai/Sandboxes/MCP.md ; https://blaxel.ai/pricing

**Together / CodeSandbox:** https://docs.together.ai/docs/together-code-sandbox ; https://www.together.ai/blog/code-sandbox (2025-05-20). https://codesandbox.io/docs/sdk/pricing returned 403 and was not used.

**Google:** https://docs.cloud.google.com/gemini-enterprise-agent-platform/scale/sandbox ; …/sandbox/code-execution-overview ; …/sandbox/manage-snapshots ; …/sandbox/custom-containers (the old https://cloud.google.com/vertex-ai/generative-ai/docs/agent-engine/code-execution/overview redirects to a landing page); Substrate https://github.com/agent-substrate/substrate

**AWS:** https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/code-interpreter-tool.html ; https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/code-interpreter-resource-session-management.html ; https://aws.amazon.com/bedrock/agentcore/pricing/

**Azure:** https://learn.microsoft.com/en-us/azure/container-apps/sessions ; https://learn.microsoft.com/en-us/azure/container-apps/billing ; https://learn.microsoft.com/en-us/azure/container-apps/sessions-tutorial-shell-mcp

**OpenAI:** https://developers.openai.com/api/docs/guides/tools-code-interpreter ; https://developers.openai.com/api/docs/guides/tools-shell ; https://developers.openai.com/api/docs/pricing ; Agents SDK https://openai.github.io/openai-agents-python/sandbox_agents/ ; https://openai.github.io/openai-agents-python/sandbox/clients/

**Anthropic:** https://code.claude.com/docs/en/agent-sdk/hosting (redirected from platform.claude.com) ; https://claude.com/resources/articles/claude-managed-agents-updates (2026-05-19)

**LangChain:** https://docs.langchain.com/oss/python/integrations/sandboxes

**OSS:** https://github.com/kubernetes-sigs/agent-sandbox (v1.0.12, 2026-11-20) ; https://github.com/microsandbox/microsandbox (v0.7.16, 2026-11-18) ; https://github.com/agent-substrate/substrate (v1.0.1, 2026-11-20). Release data from the GitHub API.

**gVisor:** https://gvisor.dev/docs/user_guide/checkpoint_restore/
