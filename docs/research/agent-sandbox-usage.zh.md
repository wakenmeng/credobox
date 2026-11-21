# AI agent 沙箱平台的客户实际怎么用：E2B、Daytona，及 Modal、Vercel 对比

> [English](agent-sandbox-usage.md) | 中文

> 调研日期：2026-11-21。下文所有链接均在这一天访问，除非另有说明。
> 只用一手来源：厂商官方文档、厂商 GitHub 仓库及源码、厂商博客、厂商发布的客户案例。客户案例是厂商的营销材料，里面的数字由厂商自己报告，第三方未核实。
> 文中 **【推断】** 标出的是我自己的推论，不是来源原文。术语以本项目 [GLOSSARY](../../GLOSSARY.zh.md) 为准，厂商自己的叫法放在引号或代码格式里。

---

## 1. 结论摘要

- **两种 agent 位置都是主流，文档的重心已经移到 "agent 在 Sandbox 里"。** E2B 的 quickstart 和 "Connect LLMs" 页仍然是 "agent 在外、Sandbox 当工具" 的写法（[connect-llms](https://docs.e2b.dev/quickstart/connect-llms.md)）。但它的 Agents 目录列了约 50 个集成页，大多数是把 Claude Code、Codex、OpenHands 这类完整 agent 装进 Sandbox 里跑（[llms.txt](https://docs.e2b.dev/llms.txt)、[coding-agents](https://docs.e2b.dev/use-cases/coding-agents.md)）。Daytona 的 guides 也是这个比例（[Daytona v2 llms.txt](https://www.daytona.com/docs/llms.txt)）。第三种模式正在出现：托管 harness（Claude Managed Agents、OpenAI Agents API、Cursor 自托管 worker）在厂商那边跑 agent 循环，把 Sandbox 当成 "自托管执行环境"（[E2B](https://docs.e2b.dev/agents/claude-managed-agents.md)、[Daytona](https://www.daytona.com/docs/guides/claude/claude-managed-agents.md)、[Modal](https://modal.com/docs/examples/cursor.md)）。
- **客户主要在做五类事情：** 代码解释器和数据分析（Perplexity、Groq、Lindy）、通用 agent 的 "虚拟电脑"（Manus、Genspark）、vibe coding 和生成 app（Anything、Lovable、Orchids）、后台编程 agent（Ramp、Capy、Mintlify）、RL、评测和基准（Hugging Face Open R1、LMArena、Terminal-Bench、Snorkel、Martian）。
- **密钥管理已经收敛到 "出网代理注入凭据"。** E2B、Daytona、Modal（beta）、Vercel 都提供同一种机制：Sandbox 里只有占位符，或者什么都没有；出网代理做 TLS 终止，在请求离开 Sandbox 时按目标域名把真实 header 填进去。代价是 Sandbox 必须信任平台的代理 CA。但各家的 Claude Code 示例仍然把 `ANTHROPIC_API_KEY` 直接放进环境变量（[E2B](https://docs.e2b.dev/agents/claude-code.md)、[Daytona](https://www.daytona.com/docs/guides/claude/claude-code-run-tasks-stream-logs-sandbox.md)）。
- **四家默认都允许出网**，都提供 "全禁 / CIDR 白名单 / 域名白名单（靠 SNI 或 Host 头判断）"。本项目 spec 默认禁止出网，比它们都严。
- **厂商说的 "pause" 大多相当于本项目的 Hibernated，不是 Paused。** E2B 的 pause 把内存和磁盘写进存储，不计算力费，可以无限期保留，恢复大约 1 秒（[persistence](https://docs.e2b.dev/sandbox/persistence.md)）。原地冻结、内存一直占着的 Paused，四家都没有作为主要卖点。
- **持久化的主力是 "只存文件系统的快照"，各家都有。** 内存快照或 Fork 是加分项：E2B 已经 GA，Daytona v0 只在 VM 类 Sandbox 上提供，Daytona v2 的 Linux Sandbox 和 Modal 都是 alpha 或受限状态。
- **隔离技术正在向 microVM 收敛。** E2B 和 Vercel 用 Firecracker。Daytona v0（daytona.io）默认用容器，v2（daytona.com）改成 Firecracker 和 Cloud Hypervisor。Modal 提供 gVisor 和 VM 两种 runtime。冷启动的宣传值靠的是 "从内存快照恢复，不冷启动内核"（[E2B runtime README](https://github.com/e2b-dev/runtime)、[Daytona v2 architecture](https://www.daytona.com/docs/architecture.md)）。
- **早先 6 条说法的核查结果：** 3 条成立（Modal 的 gVisor 和多云、Firecracker vsock 支持 guest 发起连接、Stripe 用 Billing Meters 取代 usage records）。3 条只部分成立，因为平台已经变了：E2B 自托管已经不是以 GCP 为主；Daytona 的隔离方式取决于 v0 还是 v2；E2B 的 "150ms" 来自 2025 年的客户案例，现行文档写的是 "~80ms"。详见第 4 节。

---

## 2. 各平台详情

### 2.1 E2B

**背景。** 现行文档在 docs.e2b.dev，SDK 版本 v2.64.0（[llms.txt](https://docs.e2b.dev/llms.txt)）。后端整体开源为 `e2b-dev/runtime`，Apache-2.0。原来的 `e2b-dev/infra` 现在重定向到这个仓库（[runtime README](https://github.com/e2b-dev/runtime)）。

#### (1) Agent 在哪里跑

- **两种都支持，入门材料是 "在外面"。** Quickstart 在客户自己的进程里 `Sandbox.create()`，然后 `run_code()`（[quickstart](https://docs.e2b.dev/quickstart.md)）。"Connect LLMs" 页的原话是 "The easiest way to connect an LLM to E2B is to use the tool use capabilities of the LLM"，示例都是客户的服务端调 LLM，再把代码交给 Sandbox 执行（[connect-llms](https://docs.e2b.dev/quickstart/connect-llms.md)）。
- **"在里面" 是 Coding Agents 用例的主线。** 文档写 "E2B sandboxes give each agent a full Linux environment … Pre-built templates mean you can go from zero to a running agent in a single API call"。它为 Claude Code、Codex、Amp、Gemini CLI、OpenCode 等提供预置 Template（[coding-agents](https://docs.e2b.dev/use-cases/coding-agents.md)）。Claude Code 页的流程是：`Sandbox.create("claude", envs={ANTHROPIC_API_KEY})`，接着 `commands.run('claude --dangerously-skip-permissions -p "…"', timeout=0)`，最后读回 `git diff` 或 `--output-format stream-json` 的输出（[claude-code](https://docs.e2b.dev/agents/claude-code.md)）。
- **托管 harness 模式。** Claude Managed Agents 那页写 "Claude runs the agentic loop and reasoning process; E2B provides isolated sandbox infrastructure for the environment where tool calls execute"（[claude-managed-agents](https://docs.e2b.dev/agents/claude-managed-agents.md)）。Claude Toolsets 由客户端的 tool runner 驱动 E2B 桌面 Sandbox 做 computer use 和 browser use（[claude-toolsets](https://docs.e2b.dev/agents/claude-toolsets.md)），本质上也是 "agent 在外"。
- **【推断】** 按页面数量算，"在里面" 已经占多数；按入门路径算，"在外面" 仍是默认。

#### (2) 用例与客户案例

客户索引页把生产用法归为五类：通用 agent、vibe coding 和生成 app、企业自动化、代码动作和数据分析、研究和评测。厂商自报 "more than 100,000 teams have run a sandbox"，Sandbox 启动次数在 2026 年 6 月超过 10 亿、10 月超过 20 亿（[customers.md](https://e2b.dev/customers.md)）。下表摘自厂商的客户案例：

| 客户 | 做什么 | agent 位置（据案例） | 来源 |
|---|---|---|---|
| Manus | 通用 agent。planner 拆任务，executor 用浏览器、终端、文件系统；会话可以跑几十分钟；需要 pause/resume（等用户确认、要凭据、过人机验证时）；**在自有机器上自托管 E2B** | agent 循环在 Manus 侧，Sandbox 是 "虚拟电脑" | [case](https://e2b.dev/customers/how-manus-uses-e2b-to-provide-agents-with-virtual-computers.md)（2025-05-06） |
| Genspark | Super Agent：研究、做幻灯片、数据分析；需要长流程和持久状态 | 同上 | [case](https://e2b.dev/customers/genspark.md)（2026-05-07） |
| Anything | vibe coding，超过 100 万用户；每个项目一个 Template，并持续生成新版本；用快照恢复正在跑的 dev server | Sandbox 是开发机 | [case](https://e2b.dev/customers/anything.md)（2026-07-20） |
| Rogo | 金融 agent；**Claude Managed Agents 当 harness，E2B 当执行层**；10,000 到 15,000 个 Sandbox 并发；用域名白名单 | 托管 harness 在外 | [case](https://e2b.dev/customers/rogo.md)（2026-06-12） |
| StackAI | 企业 no-code：Code Node、Computer Use/RPA、Browser Use | 在外 | [case](https://e2b.dev/customers/stackai.md)（2026-06-08） |
| Groq | Compound：服务端工具执行（代码执行加搜索） | 在外 | [case](https://e2b.dev/customers/groqs-compound-ai-models-are-powered-by-e2b.md)（2025-05-20） |
| Lindy | 工作流里的 Code Action，跑 Python 和 JS | 在外 | [case](https://e2b.dev/customers/lindy-powers-ai-workflows-with-e2b-code-action.md)（2025-05-19） |
| Hugging Face | Open R1 的 RL 奖励函数：在 Sandbox 里执行模型生成的代码，按测试用例打分 | 在外，训练循环调 Sandbox | [case](https://e2b.dev/customers/how-hugging-face-is-using-e2b-to-replicate-deepseek-r1.md)（2025-03-31） |
| LMArena | WebDev Arena 评测：并排运行两个 LLM 生成的 web app | 在外 | [case](https://e2b.dev/customers/how-lmarena-collaborated-with-e2b-to-build-llm-web-development-evals.md)（2025-02-03） |

> 过时提示：Manus 案例里写付费用户的数据 "can save … for up to 14 days"。现行文档写的是 Paused 无限期保留（见第 4 小节），14 天应是 2025 年的情况。Perplexity 的旧案例 URL 现在 307 跳转到 /customers，原文无法访问，因此本文不引用。

#### (3) 密钥与出网

- **默认出网全开。** 原文是 "Every sandbox has outbound access to the internet by default"。可选的控制有：`allowInternetAccess=false`，等价于 `denyOut: 0.0.0.0/0`；按 IP 或 CIDR 的 `allowOut` 和 `denyOut`；域名白名单，前提是同时 deny 全部流量。域名过滤 "only works for HTTP traffic on port 80 (via Host header inspection) and TLS traffic on port 443 (via SNI inspection)"，不支持 QUIC。allow 优先于 deny（[internet-access](https://docs.e2b.dev/network/internet-access.md)）。
- **几个值得注意的细节**（同一页）：
  - 只要用了域名过滤，就会自动放行 `8.8.8.8`，而且放行的是发往它的全部流量，不只是 DNS。可以用 `dnsServers: []` 关掉。
  - 被拦的 TCP 连接在 Sandbox 里看起来是连上了，因为防火墙要先 accept 才能判断目标是否允许。
  - 每个 Sandbox 最多同时开 1,500 条出站 TCP 连接。
  - 网络策略可以在 Sandbox 运行中用 `updateNetwork` 修改。
- **Secrets（出网代理注入）。** 原文："Rather than passing a key as an environment variable or baking it into a template, where any code running in the sandbox can read it, you store the value once and reference it in a network rule. The egress proxy injects the real value into matching outbound HTTPS requests outside the sandbox"（[secrets](https://docs.e2b.dev/secrets.md)）。用法是在 `network.rules[host].transform.headers` 里写 `Secret.fill('name')`（[inject](https://docs.e2b.dev/secrets/inject.md)）。语义要点：
  - 只注入 HTTPS 请求，不注入明文 HTTP。
  - 按目标主机注入。文档警告："Every process in the sandbox that sends an HTTPS request to that host gets the secret, including a browser"。
  - 解析失败时 **fail open**：请求照样发出，只是去掉对应的 header。
  - 代理用 E2B 的 CA 做 TLS 终止，这个 CA 会写进 `/etc/ssl/certs/ca-certificates.crt`。CA 是每个节点自己生成的，Sandbox 换节点恢复后可能变化，把证书固定在启动时加载的客户端会失败（[internet-access §TLS](https://docs.e2b.dev/network/internet-access.md)）。
  - 另有 workload identity，在每次请求时临时签发短期 token（同页）。
  - 限额：Hobby 每个项目 10 个 secret，付费 100 个；单个值最大 64 KiB（[limits](https://docs.e2b.dev/secrets/limits.md)）。
- **但 Claude Code 示例仍用环境变量。** 原文："The examples pass it into the sandbox, where Claude Code reads it"。文档同时提醒：auto-approve 的 agent "can still make outbound network requests — internet access is enabled by default"，建议配合网络规则使用（[claude-code](https://docs.e2b.dev/agents/claude-code.md)）。
- **访问 Sandbox 的控制通道。** 从 SDK v2 起默认开启 secure access：调用 envd（E2B 的 Guest Agent）时必须带 `X-Access-Token`，这个 token 在创建 Sandbox 时返回（[secured-access](https://docs.e2b.dev/sandbox/secured-access.md)）。

#### (4) 持久化

| 机制 | 语义（据文档） | 对应本项目术语 |
|---|---|---|
| `pause()` / `connect()` | "both the sandbox's filesystem and memory state will be saved"；"A paused sandbox is kept **indefinitely**"；Paused 不计算力费，也不占并发额度；pause 约 4 秒/GiB RAM，resume 约 1 秒（[persistence](https://docs.e2b.dev/sandbox/persistence.md)、[sandbox-lifetime](https://docs.e2b.dev/faq/sandbox-lifetime.md)） | **Hibernated**（不是 Paused） |
| `lifecycle.onTimeout: 'pause'` + `autoResume` | 超时后自动 pause；下一次 SDK 调用或发往其 URL 的 HTTP 请求会唤醒它（同上） | Hibernated + 按流量唤醒 |
| `pause({mode:'filesystem'})` | 只保存磁盘，resume 时重启（[filesystem-only-snapshots](https://docs.e2b.dev/sandbox/filesystem-only-snapshots.md)） | 无对应 |
| `createSnapshot()` | "including both its filesystem and memory state"；原 Sandbox 短暂暂停后继续跑；一个快照可以启动多个 Sandbox（[snapshots](https://docs.e2b.dev/sandbox/snapshots.md)） | 完整状态的检查点 |
| `fork({count})` | 一次调用完成快照和启动副本；SDK 一次最多 20 个，fork 默认 timeout 15 秒；原 Sandbox 会短暂暂停，连接会断（[fork](https://docs.e2b.dev/sandbox/fork.md)）。runtime README 写 "up to a hundred per request"，与 SDK 上限不一致（[runtime](https://github.com/e2b-dev/runtime)） | **Fork** |
| Volumes | **private beta**；生命周期独立于 Sandbox，**可以挂到多个 Sandbox**；已知问题：文件锁可能卡住、元数据缓存关闭、权限位不生效、只能在创建时挂载、不支持快照（[volumes](https://docs.e2b.dev/volumes.md)、[beta 限制](https://docs.e2b.dev/faq/volumes-beta-limitations.md)） | Volume（但本项目限制同一时间只挂一个 Sandbox） |
| Template | 构建时会把 "running sandbox" 整个打成快照，"load the sandbox in ~80ms any time later with all the processes already running"（[how-it-works](https://docs.e2b.dev/template/how-it-works.md)） | Template（本项目只按镜像注册） |

#### (5) 生命周期与 API 形态

- **超时。** SDK 默认 timeout 是 5 分钟，"the default is destructive"，即 `onTimeout: 'kill'`。连续运行上限 Hobby 1 小时、Pro 24 小时，pause 再 resume 会重新计时（[sandbox-lifetime](https://docs.e2b.dev/faq/sandbox-lifetime.md)）。REST API 的 `NewSandbox.timeout` 默认值是 **15 秒**（[create-sandbox API](https://docs.e2b.dev/api-reference/sandboxes/create-sandbox.md)），与 SDK 的默认值不同。E2B 用的是 **wall-clock timeout**，不是空闲计时。可以用 `setTimeout` 延长。
- **create 返回** `sandboxID`、`templateID`、`envdVersion`、`envdAccessToken`、`trafficAccessToken` 等字段（同上）。
- **Exec 和 Process。** `commands.run()` 默认用 `/bin/bash -l -c`（[commands](https://docs.e2b.dev/commands.md)）；SDK 对单条命令的默认超时是 60 秒（[claude-code](https://docs.e2b.dev/agents/claude-code.md)）。加 `background: true` 立即返回，命令在后台跑，可以从另一个进程用 `commands.connect(pid)` 重新挂上；文档把 "从 serverless 函数发起长任务" 列为常见模式（[background](https://docs.e2b.dev/commands/background.md)）。此外有 PTY 和流式输出。SDK 的流每 50 秒发一次 keepalive，单个流不保证能活过 1 小时（[sandbox-lifetime](https://docs.e2b.dev/faq/sandbox-lifetime.md)）。
- **代码解释器。** 有多个 code context，相当于 **Execution Context**（[contexts](https://docs.e2b.dev/code-interpreting/contexts.md)）。
- **文件。** 读写、上传下载（支持预签名 URL）、watch（[llms.txt](https://docs.e2b.dev/llms.txt)）。
- **端口。** 地址形如 `https://<port>-<sandboxId>.e2b.app`。**默认公开**，原文是 "Treat a public URL like a credential"（[public-url](https://docs.e2b.dev/network/public-url.md)）。**2026-10-30 之后新建的项目默认私有**，需要 traffic access token；老项目保持原来的设置（[restrict-public-access](https://docs.e2b.dev/network/restrict-public-access.md)）。

#### (6) 隔离与基础设施

- 原文："Every sandbox is a Firecracker microVM, not a container. Each one boots its own kernel"。托管版跑在 **Google Cloud** 上，另有 **AWS** 的 US West 区（[security-and-compliance](https://docs.e2b.dev/faq/security-and-compliance.md)）。BYOC 那页的表里，E2B Cloud 写作 "E2B's Google Cloud account"（[byoc](https://docs.e2b.dev/byoc.md)）。
- 内核是 LTS 6.1，在构建 Template 时就固定下来（[how-it-works](https://docs.e2b.dev/template/how-it-works.md)）。
- 架构上，"A sandbox is a resumed snapshot"：内存页通过 `userfaultfd` 按需加载，rootfs 是只读镜像上的 CoW 层；每个 Sandbox 有自己的 cgroup、网络命名空间和 nftables 防火墙。新建、resume、fork 走同一条路径（[runtime README](https://github.com/e2b-dev/runtime)）。
- **部署形态**（[byoc](https://docs.e2b.dev/byoc.md)、[Embed README](https://github.com/e2b-dev/runtime/tree/main/embed)）：
  - E2B Cloud：托管。
  - BYOC：仅企业版，支持 AWS、GCP、Azure，由 E2B 来运维。
  - E2B Embed（自托管）：**单机**，"a host with KVM, a VM on GCP, AWS, or Azure, or a node in your Kubernetes cluster"，有 Docker Compose 和 Terraform 两种装法。
- **冷启动宣传值。**
  - 现行文档："~80ms"，指从 Template 快照加载，进程已在运行（[how-it-works](https://docs.e2b.dev/template/how-it-works.md)）。
  - 客户案例："~150ms"（LMArena 2025-02、Manus 2025-05、Lindy 2025-05）。
  - 首页（[e2b.dev](https://e2b.dev/)）现在只说 "boots from a snapshot"，客户页说 "Boots from a snapshot. No cold start."，都没有给数字（[customers/research](https://e2b.dev/customers/research.md)）。

---

### 2.2 Daytona

**背景（重要）。** Daytona 现在同时存在两个平台：

- **Daytona v0**：`app.daytona.io`，文档在 daytona.io/docs，SDK 0.x，文档称之为 "the current platform"。
- **Daytona v2**：`api.daytona.com`，文档在 daytona.com/docs，SDK 2.x，是 "the next version"。它有独立的 API 和资源模型，Sandbox 不会自动迁过去（[upgrade-to-v2](https://www.daytona.io/docs/en/upgrade-to-v2.md)）。

开源仓库 `daytonaio/daytona` 写着 "As of June 2026, Daytona's core development has moved to a private codebase"，最后一个版本是 v0.190.0（[GitHub README](https://github.com/daytonaio/daytona)）。下文分别标注 v0 和 v2。

#### (1) Agent 在哪里跑

- **两种都支持；guides 以 "在里面" 为多。** v2 的 guides 列表里，"Run Claude Code / Devin CLI / Gemini CLI / Kimi CLI / Kiro CLI / OpenCode / OpenClaw / Letta Code in a Daytona Sandbox" 占了大部分（[v2 llms.txt](https://www.daytona.com/docs/llms.txt)）。Claude Code 示例：`sandbox.process.exec("npm install -g @anthropic-ai/claude-code")`，然后在 PTY 里执行 `ANTHROPIC_API_KEY=… claude --dangerously-skip-permissions -p …`（[guide](https://www.daytona.com/docs/guides/claude/claude-code-run-tasks-stream-logs-sandbox.md)）。
- **"在外面" 的示例：** 数据分析（"You prompt the LLM to generate code … The sandbox runs the AI-generated code and returns the results"，[guide](https://www.daytona.com/docs/guides/claude/data-analysis-with-ai.md)），以及 LangChain、Vercel AI SDK、DSPy RLM 等。
- **混合：** "Project Manager Agent (local) and a Developer Agent (in-sandbox)"（[two-agent guide](https://www.daytona.com/docs/guides/claude/claude-agent-sdk-connect-service-sandbox.md)）。
- **托管 harness：** Claude Managed Agents 的 "self-hosted environment"，原文 "the agent loop, prompt caching, model calls … stay on Anthropic's side. The container that holds the agent's filesystem and shell runs on your infrastructure"（[guide](https://www.daytona.com/docs/guides/claude/claude-managed-agents.md)）。此外还有 OpenAI Agents API、Cursor self-hosted machines、Devin Outposts 的集成（[v2 llms.txt](https://www.daytona.com/docs/llms.txt)）。

#### (2) 用例与客户案例

v0 文档的定位是 "AI, agent and reinforcement learning workflows"（[llms-full](https://www.daytona.io/docs/llms-full.txt)）。v2 文档列出的用例有：给 AI agent 执行代码、coding agent、RL（"very large numbers of identical environments, run one episode in each"）、不可信的用户代码、开发环境（[linux-sandboxes](https://www.daytona.com/docs/linux-sandboxes.md)）。下表摘自厂商的客户案例：

| 客户 | 做什么 | 来源 |
|---|---|---|
| Laude Institute（Terminal-Bench） | agent 基准；从本地只能跑 4 到 6 个 Docker 容器，扩到上千个并行；用 Declarative Image Builder 按任务的 Dockerfile 构建环境；"37k sandboxes created in one week" | [case](https://www.daytona.io/customers/laude) |
| Snorkel | agent 仿真和评测数据集；"1,073,756 sandboxes provisioned"；看重开源代码可审计 | [case](https://www.daytona.io/customers/snorkel) |
| Martian | ARES：面向 coding agent 的在线 RL（SWE-Bench Verified 等） | [case](https://www.daytona.io/customers/martian) |
| Stanford CS（DataComp） | 训练 agentic 模型的实验环境，单个环境最多跑 3 小时 | [case](https://www.daytona.io/customers/stanford) |
| Mastra | agent 框架的云功能（Mastra Studio）；要求有状态、长时间运行、能 "pause, wait for human approval, and resume"；案例里写 "each sandbox is container-based … starts in sub-second time" | [case](https://www.daytona.io/customers/mastra) |
| Capy | AI IDE。Captain agent 拆任务，多个 Build agent 并行，每个 agent 一个 Sandbox；需要暂停几天后从检查点恢复 | [case](https://www.daytona.io/customers/capy) |
| Mintlify | "Docs from GitHub"：agent 分析代码仓库并生成文档 | [case](https://www.daytona.io/customers/mintlify) |
| WRITER | 企业 agent 平台。原先在 AWS Fargate 上自建，日均超过 2 万个 Sandbox 后迁到 Daytona | [case](https://www.daytona.io/customers/writer) |
| Orchids | AI 全栈开发 / 生成 app | [case](https://www.daytona.io/customers/orchids) |

> 客户案例页没有写发布日期。从页面里 "Terminal Bench 2.0 on the horizon" 一类的说法看，大部分应写于 2025 年，描述的是 v0（容器）平台。

#### (3) 密钥与出网

- **v0 出网按组织套餐分级。** "Tier 1 & Tier 2: Network access is restricted and cannot be overridden at the sandbox level"。"Tier 3 & Tier 4: Full internet access is available by default"，这两档可以按 Sandbox 设置：`networkAllowList`（CIDR）、`domainAllowList`、`networkBlockAll`，三者互斥；也可以用 `outboundProxyUrl` 把流量串到客户自己的代理。规则可以在运行中修改（[v0 network-limits](https://www.daytona.io/docs/en/network-limits.md)）。
- **v2 出网默认开。** 原文："Egress is open by default, and a network policy restricts it with a default-deny allow list of IPv4 CIDR ranges and domains"。只对 Linux 和 macOS Sandbox 生效，Windows 和 GPU Sandbox 不支持。入站默认关闭，需要显式 expose 端口（[v2 isolation](https://www.daytona.com/docs/isolation.md)、[v2 network-limits](https://www.daytona.com/docs/network-limits.md)）。
- **Secrets（出网代理替换占位符），v0 和 v2 都有。** 创建 Sandbox 时把某个环境变量映射到一个 secret，Sandbox 里这个变量的值是占位符（v0 是 `dtn_secret_…`）。代理在 HTTPS 请求里看到占位符，并且目标主机在该 secret 的 `hosts` 白名单内时，换成真实值（[v0 secrets](https://www.daytona.io/docs/en/secrets.md)）。v0 和 v2 的差别：

  | | v0 | v2 |
  |---|---|---|
  | `hosts` 白名单 | 可省略，省略则 "unrestricted"，文档明确警告这样做的风险 | **必填** |
  | 替换范围 | 只替换请求 header | 替换 "the request line, every header, and the body" |
  | 响应 | 会把响应里的真实值 scrub 回占位符 | "The proxy does not rewrite responses, so an approved host that echoes the credential returns the plaintext value to the sandbox" |

  v2 还写明代理 "dials the destination itself and validates its certificate"，用来防 DNS、SNI、Host 欺骗（[v2 secrets](https://www.daytona.com/docs/secrets.md)）。
- **官方示例两种都有。** Fireworks 那篇 guide 用 secret 做 BYOK，并演示 `printenv` 只能看到占位符（[guide](https://www.daytona.com/docs/guides/fireworks/fireworks-coding-agent.md)）。但 Claude Code 那篇仍然把明文 key 拼进命令行（[guide](https://www.daytona.com/docs/guides/claude/claude-code-run-tasks-stream-logs-sandbox.md)）。

#### (4) 持久化

**v0**（[v0 sandboxes](https://www.daytona.io/docs/en/sandboxes.md)）：

| 操作 | Container（默认） | Linux VM / Windows | 语义 |
|---|---|---|---|
| stop/start | ✓ | ✓ | "Stopping terminates the running container. The filesystem is preserved, but memory state is not" |
| archive | ✓ | ✗ | 把停止状态 Sandbox 的文件系统移到对象存储，释放磁盘配额 |
| pause/resume | ✗ | ✓ | "filesystem and memory state preserved. State is offloaded to nearby storage"，【推断】接近本项目的 Hibernated |
| fork | ✗ | ✓ | 复制文件系统和内存，记录 fork 树；父 Sandbox 有活着的子 fork 时不能删除 |
| snapshot from sandbox | 只存文件系统 | 文件系统，或文件系统加内存 | — |

Volumes（v0）："FUSE-based mounts … stored in an S3-compatible object store"，"a single volume can be mounted to multiple sandboxes"，推荐按 `subpath` 隔离不同租户（[v0 volumes](https://www.daytona.io/docs/en/volumes.md)）。

**v2**（[v2 sandboxes](https://www.daytona.com/docs/sandboxes.md)、[persistence](https://www.daytona.com/docs/persistence.md)）："Sandboxes are ephemeral by default"。**Linux Sandbox 不支持 stop/start、pause、fork，也没有文件系统和内存持久化**，只能删除。状态靠三种方式保留：

- image capture：把正在运行的 Sandbox 的文件系统存成镜像，相当于新建一个 Template。
- 内存快照："a copy of the memory and disk of a sandbox … A sandbox restored from it continues from that point instead of booting"，恢复后是一个新 Sandbox（[linux-sandboxes](https://www.daytona.com/docs/linux-sandboxes.md)）。
- Volumes：disk volume 或 bucket volume，每个 Sandbox 创建时最多挂 8 个，有 commit/pull 语义（[v2 volumes](https://www.daytona.com/docs/volumes.md)）。

只有 macOS Sandbox 支持 pause 和 fork。

#### (5) 生命周期与 API 形态

- **v0**（[v0 sandboxes](https://www.daytona.io/docs/en/sandboxes.md)）：
  - auto-stop 默认 15 分钟无活动就停止。**只有生命周期操作、preview 流量、SSH、Toolbox API 请求会重置计时器**。后台脚本不算活动，原文："If you run a long-running task like LLM inference that takes more than 15 minutes … the sandbox may auto-stop mid-process"。
  - auto-archive 默认 7 天，auto-delete 默认不删。VM 类 Sandbox 如果都没设置，默认 60 分钟后 auto-pause。还可以设 wall-clock TTL。
  - 默认资源 1 vCPU、1 GiB 内存、3 GiB 磁盘。
- **v2**（[linux-sandboxes](https://www.daytona.com/docs/linux-sandboxes.md)）：三种 timeout，到期都会**删除** Sandbox：
  - create timeout：默认 3600 秒。
  - lifetime timeout：最多 86400 秒，默认值看项目或平台的配置。
  - idle timeout：默认没有，除非项目设置了。原文："Requests, connections, and running commands count as activity"，**正在运行的命令算活动**，和 v0 不同。
  - 默认资源 0.5 核、1 GiB 内存（[v2 sandboxes](https://www.daytona.com/docs/sandboxes.md)）。
- **Exec 和 Process。** 有一次性命令；有 session，即持久 shell，"keep the working directory and environment variables across the commands"，可以跑后台长进程；有 PTY 和日志流；有代码执行，可以无状态，也可以在持久 interpreter context 里跑，相当于 **Execution Context**（[v0 process](https://www.daytona.io/docs/en/process-code-execution.md)、[v2 process](https://www.daytona.com/docs/process-execution.md)）。文件系统和 Git 操作有单独的 API。
- **端口（preview URL）。** 默认私有。标准 preview URL 要带 `x-daytona-preview-token` header，这个 token 对整个 Sandbox 有效，而且不能撤销。signed preview URL 把 token 放进 URL，只绑定一个端口，可以设过期时间，可以撤销（v2 默认 60 秒，最长 7 天）。也可以把 Sandbox 设为 public（[v0 preview](https://www.daytona.io/docs/en/preview.md)、[v2 preview](https://www.daytona.com/docs/preview.md)）。

#### (6) 隔离与基础设施

- **v0：默认是容器。** 原文："Sandboxes run as **Linux containers** by default"。另有 VM 类 Sandbox（Linux VM、Windows）、GPU Sandbox、macOS Sandbox（[v0 sandboxes](https://www.daytona.io/docs/en/sandboxes.md)）。同一份文档又说每个 Sandbox 有 "a dedicated kernel"，这和普通容器的语义对不上，文档没有解释。
  - **源码观察**（开源仓库最后版本 v0.190.0，不一定反映云上生产配置）：runner 用 Docker API 创建容器（`apps/runner/pkg/docker`）；OCI runtime 由环境变量 `CONTAINER_RUNTIME` 配置，默认为空，也就是 Docker 的默认 runtime；非 GPU Sandbox 以 `Privileged: gpuIndex == nil` 创建，注释原文是 "Non-GPU sandboxes still need privileged for their current workloads"（[container_configs.go@v0.190.0](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/pkg/docker/container_configs.go)、[config.go@v0.190.0](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/cmd/runner/config/config.go)）。云上生产环境用的是哪种 runtime（例如 Sysbox 或 Kata），文档没有说。**【推断】** 文档里 "dedicated kernel" 的说法，在开源代码的默认配置下不成立；只能靠一个文档里没写的 runtime 才能成立。
- **v2：默认是 microVM。** 原文："Linux sandboxes are Firecracker microVMs that run their own Linux kernel, and they are the default sandbox type"。Windows 和 GPU Sandbox 用 Cloud Hypervisor，macOS 用 Apple Virtualization framework（[v2 sandboxes](https://www.daytona.com/docs/sandboxes.md)）。runner 通过 NATS JetStream 拉取任务，原文 "Runners accept no inbound connections"（[architecture](https://www.daytona.com/docs/architecture.md)）。BYOC 支持把 Sandbox 跑在客户自己的 Kubernetes 节点上，节点要求是裸金属，或者开了嵌套虚拟化（[byoc](https://www.daytona.com/docs/bring-your-own-compute.md)）。托管版用哪家云，文档没有说明；CPU 类型列表里有 AWS Graviton（[v2 sandboxes](https://www.daytona.com/docs/sandboxes.md)）。
- **冷启动宣传值。**
  - v0 的介绍页和 README："spinning up in under 90ms from code to execution"，Container 卡片上写 "<90ms"（[llms-full](https://www.daytona.io/docs/llms-full.txt)、[v0 sandboxes](https://www.daytona.io/docs/en/sandboxes.md)）。
  - v2 的 quickstart 仍然写 "A sandbox spins up in under 90ms from code to execution"（[quickstart](https://www.daytona.com/docs/quickstart.md)）。
  - 但 v2 的 architecture 页写 "Expect most Linux sandboxes to be created in under a second, because their runner resumes a pre-built memory snapshot"（[architecture](https://www.daytona.com/docs/architecture.md)）。
  - 两处都没有说明测量条件。

---

### 2.3 Modal Sandboxes（对比，简述）

- **Agent 位置。** 两种都有。"在外面"：代码解释器、"Run a LangGraph agent's code in a secure GPU sandbox"。"在里面"：Cursor Cloud Agent worker 跑在 Sandbox 里（[llms.txt](https://modal.com/llms.txt)、[cursor](https://modal.com/docs/examples/cursor.md)）。在 Ramp 的案例里，"OpenCode runs as the coding agent"，在 Sandbox 内运行，同一环境里还有 Postgres、Redis、VS Code server、VNC 和 Chromium（[Ramp 案例，2026-02-19](https://modal.com/blog/how-ramp-built-a-full-context-background-coding-agent-on-modal)）。
- **客户。** 产品页列出 Linear（Coding Sessions）、Ramp（Inspect）、Cognition（RL 基础设施）、Abundant（RL 环境）（[products/sandboxes](https://modal.com/products/sandboxes)）。
- **密钥与出网。**
  - 默认出网："can make outbound connections to any public IP address"。可选 `block_network`、`outbound_cidr_allowlist`、`outbound_domain_allowlist`（beta，只管 443 端口的 TLS）。文档警告 "DNS traffic is allowed without restriction except when all outbound traffic is blocked"（[sandbox-networking](https://modal.com/docs/guide/sandbox-networking.md)）。
  - Secret 注入（beta）："An Outbound Policy lets a Sandbox call external HTTPS APIs … without the API key ever being visible inside the Sandbox"（[secret-injection](https://modal.com/docs/guide/sandbox-secret-injection.md)）。
  - 反例：Cursor 示例把 `CURSOR_API_KEY` 放进 worker 的环境变量，文档明说 "code running in a worker sandbox can read it"（[cursor](https://modal.com/docs/examples/cursor.md)）。
- **持久化**（[sandbox-snapshots](https://modal.com/docs/guide/sandbox-snapshots.md)）：
  - Filesystem Snapshot 和 Directory Snapshot 都是 Image，按与基础镜像的 diff 存储，默认保留 30 天。
  - Memory Snapshot 是 alpha，保存 "entire state, both in memory and on the filesystem"，**7 天后过期，不能延长**；目前拍快照会让原 Sandbox 终止；只能在同一种实例类型上恢复。
  - Volumes 可以多个 Sandbox 共享，支持 subpath（[sandbox-files](https://modal.com/docs/guide/sandbox-files.md)）。
  - **【推断】** Modal 没有 pause/resume，持久化靠 "快照 → 新 Sandbox"。
- **生命周期。** "default maximum lifetime of 5 minutes"，最长 24 小时；`idle_timeout` 可选，活动包括正在运行的 exec、写 stdin、tunnel 上有打开的 TCP 连接。超过 24 小时要用快照接力（[sandboxes](https://modal.com/docs/guide/sandboxes.md)）。`sb.exec()` 支持流式 stdout/stderr（[sandbox-spawn](https://modal.com/docs/guide/sandbox-spawn.md)）。有文件 API（[sandbox-files](https://modal.com/docs/guide/sandbox-files.md)）。端口通过 Tunnels 暴露，默认是 `w.modal.host` 的子域名（[sandbox-networking](https://modal.com/docs/guide/sandbox-networking.md)）。
- **隔离与基础设施。**
  - 两种 runtime：gVisor（"implements the Linux system call interface in userspace"）和 VM（"its own Linux kernel … KVM"），原文 "leave unset to let Modal choose for you"。GPU Sandbox 只能用 gVisor（[sandboxes](https://modal.com/docs/guide/sandboxes.md)、[sandbox-networking](https://modal.com/docs/guide/sandbox-networking.md)）。
  - 多云：原文 "Modal runs containers globally across multiple different clouds"（[region-selection](https://modal.com/docs/guide/region-selection.md)）。Sandbox 有 `cloud` 参数，说明是 "Cloud provider for the sandbox"（[SDK](https://modal.com/docs/sdk/py/latest/Sandbox.md)）。
  - 冷启动宣传值："Modal Sandboxes start in under one second"，"Sub-second cold-starts even on custom and large images"（[products/sandboxes](https://modal.com/products/sandboxes)）。

### 2.4 Vercel Sandbox（对比，简述）

- **Agent 位置。** 文档写 "Run coding agents"，默认镜像里预装了 coding agent（[sandbox](https://vercel.com/docs/sandbox.md)）。KB 有一篇专门讲在 Sandbox 里跑 Claude Agent SDK（[kb](https://vercel.com/kb/guide/using-vercel-sandbox-claude-agent-sdk)）。GA 博客举的客户：Roo Code 用快照让 agent "days later" 继续之前的任务；Blackbox AI 的 Agents HQ 并行派发多个 agent（[GA 博客，2026-01-30](https://vercel.com/blog/vercel-sandbox-is-now-generally-available)）。
- **密钥与出网**（[firewall](https://vercel.com/docs/sandbox/concepts/firewall.md)、[security](https://vercel.com/docs/sandbox/concepts/security.md)、[credentials](https://vercel.com/docs/sandbox/concepts/credentials.md)）：
  - 默认 `allow-all`，另有 `deny-all`（连 DNS 也禁）和用户自定义白名单，都可以在运行中切换。
  - 防火墙规则可以做 credentials brokering、请求代理或直接返回响应。需要改写请求的域名，用 "a CA unique to your sandbox" 做 TLS 终止，其他域名不解密。
  - 平台托管的 npm 凭据，原文 "can use the credential but can't read it"。
- **持久化**（[concepts](https://vercel.com/docs/sandbox/concepts.md)、[duration KB](https://vercel.com/kb/guide/vercel-sandbox-duration-and-persistence)）：
  - "persistent by default"：session 停止时自动给文件系统拍快照，下一次 SDK 调用会自动开一个新 session 恢复。**快照只含文件系统**，默认在最后一次使用 30 天后过期。
  - Drives 用于跨 Sandbox 持久化，目前 private beta。
- **生命周期。** 每个 session 默认 timeout 5 分钟，Hobby 最长 45 分钟、Pro 最长 24 小时，可以 `extendTimeout()`。默认 2 vCPU，每个 vCPU 配 2 GB 内存。端口通过公网 URL 暴露（[pricing](https://vercel.com/docs/sandbox/pricing.md)、[security](https://vercel.com/docs/sandbox/concepts/security.md)）。
- **隔离与基础设施。** 原文："each sandbox runs in its own Firecracker microVM with a dedicated kernel"，允许 sudo，可以在里面跑 Docker、VPN、FUSE。默认区域 `iad1`（[concepts](https://vercel.com/docs/sandbox/concepts.md)、[security](https://vercel.com/docs/sandbox/concepts/security.md)）。底层是内部计算平台 Hive，"powered by Firecracker"（[GA 博客](https://vercel.com/blog/vercel-sandbox-is-now-generally-available)）。云厂商没有明说，只提到可以 "reach your own AWS VPC over VPC peering"。**【推断】** 跑在 AWS 上。冷启动只说了 "Sandboxes start in milliseconds"，没有具体数字（[sandbox](https://vercel.com/docs/sandbox.md)）。

---

## 3. 对比表

| 平台 | agent 位置（文档呈现） | 隔离技术 | 持久化方式 | 密钥处理 | 默认出网 | 冷启动宣传值（原文） |
|---|---|---|---|---|---|---|
| **E2B** | 两种都有；入门是 "在外面"（tool use）；Agents 目录约 50 页，多为 "在里面"；也支持托管 harness | Firecracker microVM（托管在 GCP，另有 AWS US West） | pause 存内存加磁盘并无限期保留（相当于 Hibernated）；内存快照；Fork；只存文件系统的 pause/快照；Volumes（private beta，可多挂） | 出网代理按主机注入 header（`Secret.fill`）；workload identity；示例仍用 `envs` 传 key | **全开** | 文档 "~80ms"（从 Template 快照加载）；2025 年案例 "~150ms" |
| **Daytona v0**（daytona.io，当前平台） | 两种都有；guides 多为 "在里面" | **默认 Linux 容器**（开源 runner：Docker，非 GPU 为 privileged，runtime 可配）；另有 VM 类 | 容器：stop 保留磁盘、archive 到对象存储、只存文件系统的快照；VM：pause、fork、内存快照；Volumes（FUSE + S3，可多挂） | 占位符环境变量 + 代理替换（只替换 header，scrub 响应，`hosts` 可省略） | Tier 1/2 受限且不能覆盖；Tier 3/4 全开 | "under 90ms from code to execution"，容器卡片 "<90ms" |
| **Daytona v2**（daytona.com，下一代） | 同上 | **Firecracker**（Linux）、Cloud Hypervisor（Windows、GPU）、Apple VZ（macOS） | Linux 默认临时；镜像快照（文件系统）；内存快照 → 新 Sandbox；Volumes（disk/bucket）；不支持 pause/fork（macOS 除外） | 同 v0，但 `hosts` 必填，替换 header 和 body，**不** scrub 响应 | **全开** | quickstart "under 90ms"；architecture "most … in under a second" |
| **Modal** | 两种都有；客户案例（Ramp、Linear、Cursor）为 "在里面" | **gVisor** 或 VM（KVM），默认由平台选；GPU 只能用 gVisor；多云 | 文件系统/目录快照（Image，30 天）；内存快照（alpha，7 天）；Volumes；没有 pause | Outbound Policy 注入 header（beta）；示例仍有放环境变量的 | **全开**（DNS 不受限） | "start in under one second" |
| **Vercel** | 侧重 "在里面"（预装 coding agent，Claude Agent SDK） | Firecracker microVM | 默认持久：stop 时自动做文件系统快照，下次自动恢复；快照只含文件系统；Drives（beta） | 防火墙 credentials brokering（每个 Sandbox 一个 CA）；托管 npm 凭据 | **全开**（`allow-all`） | "start in milliseconds" |
| **本项目 spec** | demo agent 在外面，走公共 API | Docker → gVisor → Firecracker（可切换） | Paused（原地冻结）、Hibernated、Fork（不做只复制文件系统的版本）、Volume（同一时间只挂一个） | 创建时传默认环境变量；没有凭据注入 | **禁止** | （待测） |

---

## 4. 早先说法的核查

| # | 说法 | 结论 | 依据 |
|---|---|---|---|
| 1a | E2B 用 Firecracker | **成立** | "Every sandbox is a Firecracker microVM, not a container"（[security-and-compliance](https://docs.e2b.dev/faq/security-and-compliance.md)）；"One Firecracker microVM per sandbox"（[runtime README](https://github.com/e2b-dev/runtime)） |
| 1b | E2B 的自托管基础设施主要面向 GCP | **部分成立，已过时** | 托管版跑在 E2B 自己的 Google Cloud 账号里，另有 AWS US West（[security-and-compliance](https://docs.e2b.dev/faq/security-and-compliance.md)、[byoc](https://docs.e2b.dev/byoc.md)）。自托管现在叫 E2B Embed，是**单机**形态，可以装在任何有 KVM 的 Linux 主机上（Docker Compose），也可以用 Terraform 装在 GCP、AWS、Azure 的单台 VM 上，或作为 K8s 节点。BYOC 支持 AWS、GCP、Azure（[Embed README](https://github.com/e2b-dev/runtime/tree/main/embed)、[byoc](https://docs.e2b.dev/byoc.md)）。"自托管主要面向 GCP" 已经不成立；"托管版以 GCP 为主" 成立 |
| 2a | Daytona 默认用容器隔离，不是 microVM | **取决于平台版本** | v0（daytona.io，文档称 "current platform"）：**成立**，原文 "Sandboxes run as Linux containers by default"（[v0 sandboxes](https://www.daytona.io/docs/en/sandboxes.md)）；开源 runner 源码也是 Docker 容器（[container_configs.go@v0.190.0](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/pkg/docker/container_configs.go)）。v2（daytona.com，"next version"）：**不成立**，原文 "Linux sandboxes are Firecracker microVMs … the default sandbox type"（[v2 sandboxes](https://www.daytona.com/docs/sandboxes.md)） |
| 2b | Daytona 宣传约 90ms 创建 Sandbox | **成立（宣传值）** | "spinning up in under 90ms from code to execution"（[v0 llms-full](https://www.daytona.io/docs/llms-full.txt)、[v2 quickstart](https://www.daytona.com/docs/quickstart.md)），没有说明测量条件。v2 的 architecture 页写的是 "under a second"（[architecture](https://www.daytona.com/docs/architecture.md)），两处不一致 |
| 3 | E2B 宣传约 150ms 启动 | **部分成立（旧口径）** | "~150ms" 出现在 E2B 自己 2025 年的客户案例里：LMArena（2025-02-03）、Manus（2025-05-06）、Lindy（2025-05-19，"approximately 150ms"）（[LMArena](https://e2b.dev/customers/how-lmarena-collaborated-with-e2b-to-build-llm-web-development-evals.md)、[Manus](https://e2b.dev/customers/how-manus-uses-e2b-to-provide-agents-with-virtual-computers.md)、[Lindy](https://e2b.dev/customers/lindy-powers-ai-workflows-with-e2b-code-action.md)）。现行文档写的是从 Template 快照加载 "~80ms"（[how-it-works](https://docs.e2b.dev/template/how-it-works.md)）；首页现在不给数字 |
| 4a | Modal Sandbox 用 gVisor | **成立，需补充** | 有 gVisor 和 VM 两种 runtime，原文 "leave unset to let Modal choose"；GPU 只能用 gVisor（[sandboxes](https://modal.com/docs/guide/sandboxes.md)）。安全页写 "primitives like gVisor or microVMs"（[security](https://modal.com/docs/guide/security.md)） |
| 4b | Modal 跑在多家云上 | **成立** | "Modal runs containers globally across multiple different clouds"（[region-selection](https://modal.com/docs/guide/region-selection.md)）。文档没有列出具体是哪几家云 |
| 5 | Firecracker vsock 支持 guest 发起连接（host 在 `<uds_path>_<port>` 上监听） | **成立** | "it tries to forward the connection to an AF_UNIX socket listening on the host, at `/path/to/v.sock_PORT`"。补充：`[2^30, 2^31)` 范围内的端口保留给 host 发起的连接，guest 向这个范围发起连接一律被 RST（[vsock.md](https://github.com/firecracker-microvm/firecracker/blob/main/docs/vsock.md)） |
| 6 | Stripe 的旧版 usage records 已被 Billing Meters 取代 | **成立** | "Stripe is deprecating usage-records billing … billing meters, our only solution for usage-based billing"；"The legacy usage records APIs are removed in Stripe version 2025-03-31.basil"，要继续用必须锁定 API 版本 2025-02-24.acacia 或更早（[migration-guide](https://docs.stripe.com/billing/subscriptions/usage-based-legacy/migration-guide.md)） |

---

## 5. 对本项目设计的启示（**本节全部为推断**）

按 "与业界一致 / 与业界不同 / spec 缺失" 分类。每条注明对应的 spec 章节。

**与业界一致**

1. **按镜像注册 Template、Guest Agent 由平台注入、Host 侧做调度。** 这个结构和 E2B（API → orchestrator → envd）、Daytona（API → runner → daemon）同构（[E2B runtime](https://github.com/e2b-dev/runtime)、[Daytona architecture](https://www.daytona.com/docs/architecture.md)）。ADR 0001、0002、0003 不需要改。
2. **Exposed Port 默认要求 token。** 和 Daytona 一致，也和 E2B 2026-10-30 之后新建项目的默认值一致。E2B 改默认值这件事本身说明 "默认公开" 被证明是个坑。
3. **默认 5 分钟回收。** 和 E2B、Modal、Vercel 的 5 分钟默认 timeout 是同一个量级。

**与业界不同（需要有意识地取舍）**

4. **默认禁止出网（spec §7）。** 四家默认都是全开。保持默认禁止是合理的安全选择，但会直接影响 "agent 在 Sandbox 里" 模式：agent 必须能访问 LLM API，很可能还要访问 pip、npm、GitHub。建议在 Template 或 Tenant 层面提供可复用的白名单预设（例如 "llm-providers"、"package-registries"）。Daytona v0 的 "essential services" 就是这类做法（[v0 network-limits](https://www.daytona.io/docs/en/network-limits.md)）。
5. **术语错位：业界的 "pause" ≈ 本项目的 Hibernated。** E2B、Daytona VM 的 pause 都把状态写进存储并释放 CPU，E2B 还明确说 pause 期间不收算力费、可以无限期保留。本项目的 Paused 是原地冻结、内存继续占着，而且有 1 小时上限，业界很少把这个当卖点。建议：(a) 学习文档里明确写出这个映射，避免读者把两者当成同一件事；(b) 第三阶段的 Hibernate 才是对标业界的关键能力，可以考虑把 "超时自动 Hibernate、有请求时自动恢复"（E2B 的 `onTimeout: 'pause'` + `autoResume`）列为目标。
6. **"不做只复制文件系统的弱化版 Fork"（spec §2）。** Fork 的定义可以保留。但业界的主力持久化手段恰恰是 "文件系统快照 → 新 Sandbox 或新 Template"：E2B 的 filesystem 模式、Daytona v2 的 image capture、Modal 的 Filesystem Snapshot、Vercel 的持久 Sandbox 都是这一类。建议把它作为**另一个概念**补进 GLOSSARY（例如 "从 Sandbox 捕获 Template"），而不是算作 Fork。实现起来比内存级 Fork 简单得多，三种 Runtime 都能做。
7. **Volume 同一时间只挂一个 Sandbox（GLOSSARY）。** E2B、Daytona、Modal 的 Volume 都允许多个 Sandbox 同时挂，并靠 subpath 做租户隔离。单挂载的语义更简单，但 RL 和评测场景常见的 "共享只读数据集" 就做不了。可以考虑之后加 "多个只读挂载"。

**spec 缺失**

8. **支持 "agent 在 Sandbox 里" 模式需要补的几样东西。**
   - (a) **空闲判定会误杀。** spec §5 规定 "Process 本身不算" 活动。如果客户在 Sandbox 里用 Process 跑 Claude Code，跑 30 分钟期间不调 API，Sandbox 会在 5 分钟后被回收。Daytona v0 文档专门警告了同一个问题，v2 改成 "running commands count as activity"；E2B 干脆用 wall-clock timeout。建议给 Process 加一个 "keepalive" 选项，或者另设 wall-clock 模式。
   - (b) **Exec 最长 1 小时（spec §4）。** agent 任务可能更长，要明确引导客户用 Process。
   - (c) **输出可能很大。** 要能流式读 Process 输出，并且断线后能接着读（spec 已有 `output?offset=`，方向对）。

   demo agent 可以加一个 "在 Sandbox 里跑 agent" 的变体，作为第二种验证方式。
9. **出网代理注入凭据（secrets proxy）。** 四家都有，而且是 "agent 在里面" 模式下唯一不把 LLM key 交给不可信代码的办法。本项目的出网代理现在只看 SNI，不终止 TLS（spec §7），所以做不了注入。如果要做：
   - (a) 代理要对白名单里需要注入的域名做 TLS 终止，并在 Sandbox 的系统信任库里安装每个 Sandbox 或每台 Host 独有的 CA（E2B 和 Vercel 都是这么做的）；
   - (b) 凭据按 "目标主机白名单" 绑定，`hosts` 必填（吸取 Daytona v0 允许省略的教训）；
   - (c) 明确解析失败时 fail open 还是 fail closed，以及要不要 scrub 响应。在这两点上 E2B、Daytona v0、Daytona v2 各不相同。

   这适合作为第二或第三阶段的 ADR。注意出网代理是 Sandbox 能主动连接的 Host 侧组件，属于 ADR 0005 "只由外向内" 的已知例外，隔离测试里应该加 "Sandbox 冒充或绕过出网代理" 这一项。
10. **在 Template 里预先拍内存快照来做冷启动。** E2B（"A sandbox is a resumed snapshot"）和 Daytona v2（"resumes a pre-built memory snapshot … instead of a cold boot"）的冷启动宣传值都来自恢复快照，不是启动内核。第三阶段的 Firecracker 对比表应当分开测 "冷启动内核" 和 "从快照恢复" 两种路径，否则和业界数字没法比。
11. **DNS 是出网白名单的常见漏洞。** E2B 用域名过滤时自动放行发往 8.8.8.8 的全部流量；Modal 的 DNS 不受限制（[E2B](https://docs.e2b.dev/network/internet-access.md)、[Modal](https://modal.com/docs/guide/sandbox-networking.md)）。spec §7 只放行 HTTP 和 HTTPS，DNS 怎么处理没有写。建议在 spec 里写明，并在隔离测试的 "绕过出网白名单" 一项里加上 DNS 隧道。
12. **托管 harness 模式。** Claude Managed Agents 和 OpenAI Agents API 的 "self-hosted environment" 需要平台能接收 webhook，或者主动把会话路由到某个长期存在的 Sandbox（[E2B](https://docs.e2b.dev/agents/claude-managed-agents.md)）。这不在本项目范围内，记一笔，留作以后参考。

---

## 6. 来源列表（均于 2026-11-21 访问）

**E2B**
- 文档首页 https://docs.e2b.dev/ ；索引 https://docs.e2b.dev/llms.txt
- Quickstart https://docs.e2b.dev/quickstart.md ；Connect LLMs https://docs.e2b.dev/quickstart/connect-llms.md
- Sandbox lifecycle https://docs.e2b.dev/sandbox.md ；Sandbox lifetime FAQ https://docs.e2b.dev/faq/sandbox-lifetime.md
- Persistence https://docs.e2b.dev/sandbox/persistence.md ；Snapshots https://docs.e2b.dev/sandbox/snapshots.md ；Fork https://docs.e2b.dev/sandbox/fork.md ；Filesystem-only snapshots https://docs.e2b.dev/sandbox/filesystem-only-snapshots.md
- Internet access https://docs.e2b.dev/network/internet-access.md ；Public URL https://docs.e2b.dev/network/public-url.md ；Restrict public access https://docs.e2b.dev/network/restrict-public-access.md ；Secured access https://docs.e2b.dev/sandbox/secured-access.md
- Secrets https://docs.e2b.dev/secrets.md ；Inject https://docs.e2b.dev/secrets/inject.md ；Limits https://docs.e2b.dev/secrets/limits.md
- Volumes https://docs.e2b.dev/volumes.md ；Volumes beta limitations https://docs.e2b.dev/faq/volumes-beta-limitations.md
- Commands https://docs.e2b.dev/commands.md ；Background commands https://docs.e2b.dev/commands/background.md ；Code contexts https://docs.e2b.dev/code-interpreting/contexts.md
- Template how it works https://docs.e2b.dev/template/how-it-works.md
- Coding agents https://docs.e2b.dev/use-cases/coding-agents.md ；Claude Code https://docs.e2b.dev/agents/claude-code.md ；Claude Managed Agents https://docs.e2b.dev/agents/claude-managed-agents.md ；Claude Toolsets https://docs.e2b.dev/agents/claude-toolsets.md
- Security & compliance https://docs.e2b.dev/faq/security-and-compliance.md ；BYOC https://docs.e2b.dev/byoc.md ；Create sandbox API https://docs.e2b.dev/api-reference/sandboxes/create-sandbox.md
- E2B Runtime 仓库 https://github.com/e2b-dev/runtime （`e2b-dev/infra` 重定向到这里）；Embed https://github.com/e2b-dev/runtime/tree/main/embed
- 首页 https://e2b.dev/ ；客户索引 https://e2b.dev/customers.md 、https://e2b.dev/customers/research.md
- 客户案例：Manus https://e2b.dev/customers/how-manus-uses-e2b-to-provide-agents-with-virtual-computers.md ；Genspark https://e2b.dev/customers/genspark.md ；Anything https://e2b.dev/customers/anything.md ；Rogo https://e2b.dev/customers/rogo.md ；StackAI https://e2b.dev/customers/stackai.md ；Groq https://e2b.dev/customers/groqs-compound-ai-models-are-powered-by-e2b.md ；Lindy https://e2b.dev/customers/lindy-powers-ai-workflows-with-e2b-code-action.md ；Hugging Face https://e2b.dev/customers/how-hugging-face-is-using-e2b-to-replicate-deepseek-r1.md ；LMArena https://e2b.dev/customers/how-lmarena-collaborated-with-e2b-to-build-llm-web-development-evals.md

**Daytona v0（daytona.io）**
- Sandboxes https://www.daytona.io/docs/en/sandboxes.md ；完整文档 https://www.daytona.io/docs/llms-full.txt
- Network limits https://www.daytona.io/docs/en/network-limits.md ；Secrets https://www.daytona.io/docs/en/secrets.md ；Volumes https://www.daytona.io/docs/en/volumes.md ；Preview https://www.daytona.io/docs/en/preview.md ；Process & code execution https://www.daytona.io/docs/en/process-code-execution.md
- Upgrade to v2 https://www.daytona.io/docs/en/upgrade-to-v2.md
- GitHub https://github.com/daytonaio/daytona ；runner 源码 https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/pkg/docker/container_configs.go 、https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/cmd/runner/config/config.go
- 客户案例：https://www.daytona.io/customers/laude 、/snorkel 、/martian 、/stanford 、/mastra 、/capy 、/mintlify 、/writer 、/orchids （均以 https://www.daytona.io/customers/ 为前缀）

**Daytona v2（daytona.com）**
- 索引 https://www.daytona.com/docs/llms.txt ；Quickstart https://www.daytona.com/docs/quickstart.md
- Sandboxes https://www.daytona.com/docs/sandboxes.md ；Linux sandboxes https://www.daytona.com/docs/linux-sandboxes.md ；Isolation https://www.daytona.com/docs/isolation.md ；Architecture https://www.daytona.com/docs/architecture.md
- Persistence https://www.daytona.com/docs/persistence.md ；Volumes https://www.daytona.com/docs/volumes.md ；Network limits https://www.daytona.com/docs/network-limits.md ；Secrets https://www.daytona.com/docs/secrets.md ；Preview https://www.daytona.com/docs/preview.md ；Process execution https://www.daytona.com/docs/process-execution.md ；BYOC https://www.daytona.com/docs/bring-your-own-compute.md
- Guides：Claude Code https://www.daytona.com/docs/guides/claude/claude-code-run-tasks-stream-logs-sandbox.md ；Two-agent https://www.daytona.com/docs/guides/claude/claude-agent-sdk-connect-service-sandbox.md ；Claude Managed Agents https://www.daytona.com/docs/guides/claude/claude-managed-agents.md ；Data analysis https://www.daytona.com/docs/guides/claude/data-analysis-with-ai.md ；Fireworks BYOK https://www.daytona.com/docs/guides/fireworks/fireworks-coding-agent.md

**Modal**
- 索引 https://modal.com/llms.txt ；Sandboxes https://modal.com/docs/guide/sandboxes.md ；Running commands https://modal.com/docs/guide/sandbox-spawn.md ；Networking & security https://modal.com/docs/guide/sandbox-networking.md ；File access https://modal.com/docs/guide/sandbox-files.md ；Snapshots https://modal.com/docs/guide/sandbox-snapshots.md ；Secret injection https://modal.com/docs/guide/sandbox-secret-injection.md ；Security https://modal.com/docs/guide/security.md ；Region selection https://modal.com/docs/guide/region-selection.md ；SDK https://modal.com/docs/sdk/py/latest/Sandbox.md
- Cursor 示例 https://modal.com/docs/examples/cursor.md ；产品页 https://modal.com/products/sandboxes ；Ramp 案例 https://modal.com/blog/how-ramp-built-a-full-context-background-coding-agent-on-modal

**Vercel**
- Sandbox https://vercel.com/docs/sandbox.md ；Concepts https://vercel.com/docs/sandbox/concepts.md ；Security https://vercel.com/docs/sandbox/concepts/security.md ；Firewall https://vercel.com/docs/sandbox/concepts/firewall.md ；Credentials https://vercel.com/docs/sandbox/concepts/credentials.md ；Pricing & limits https://vercel.com/docs/sandbox/pricing.md
- KB：Duration & persistence https://vercel.com/kb/guide/vercel-sandbox-duration-and-persistence ；Claude Agent SDK https://vercel.com/kb/guide/using-vercel-sandbox-claude-agent-sdk
- GA 博客 https://vercel.com/blog/vercel-sandbox-is-now-generally-available

**其他**
- Firecracker vsock https://github.com/firecracker-microvm/firecracker/blob/main/docs/vsock.md
- Stripe 迁移指南 https://docs.stripe.com/billing/subscriptions/usage-based-legacy/migration-guide.md

**过时或不确定提示汇总**
- E2B 的 "~150ms" 和 Manus 案例里 "保留 14 天" 都是 2025 年的说法，和现行文档不一致。
- E2B Volumes、Vercel Drives 是 private beta；Modal Memory Snapshot 是 alpha；Modal 的域名白名单和 secret 注入是 beta。
- Daytona 正处在 v0 → v2 迁移期，两套文档的默认值差别很大。客户案例大多基于 v0。开源仓库 2026 年 6 月起停止维护，源码观察不代表云上现状。
- 二手资料只在搜索阶段用来找线索，没有作为任何结论的依据。
