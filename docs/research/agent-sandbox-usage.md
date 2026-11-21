# How customers actually use AI agent sandbox platforms: E2B and Daytona, compared with Modal and Vercel

> English | [中文](agent-sandbox-usage.zh.md)

> Research date: 2026-11-21. Every link below was accessed on that day unless noted otherwise.
> Primary sources only: official vendor docs, vendor GitHub repos and source code, vendor blogs, and vendor-published customer stories. Customer stories are vendor marketing; the numbers in them are self-reported by the vendor and have not been verified by a third party.
> Anything marked **[Inference]** is my own conclusion, not what the source says. Terminology follows the project [GLOSSARY](../../GLOSSARY.md); vendors' own names for things are kept in quotes or code formatting.

---

## 1. Summary

- **Both agent locations are mainstream, and the docs have shifted their weight toward "the agent runs inside the Sandbox".** E2B's quickstart and "Connect LLMs" page are still written as "agent outside, Sandbox as a tool" ([connect-llms](https://docs.e2b.dev/quickstart/connect-llms.md)). But its Agents directory lists about 50 integration pages, most of which install a complete agent such as Claude Code, Codex or OpenHands inside the Sandbox and run it there ([llms.txt](https://docs.e2b.dev/llms.txt), [coding-agents](https://docs.e2b.dev/use-cases/coding-agents.md)). Daytona's guides show the same proportion ([Daytona v2 llms.txt](https://www.daytona.com/docs/llms.txt)). A third pattern is emerging: a hosted harness (Claude Managed Agents, OpenAI Agents API, Cursor self-hosted workers) runs the agent loop on the vendor's side and treats the Sandbox as a "self-hosted environment" ([E2B](https://docs.e2b.dev/agents/claude-managed-agents.md), [Daytona](https://www.daytona.com/docs/guides/claude/claude-managed-agents.md), [Modal](https://modal.com/docs/examples/cursor.md)).
- **Customers mainly build five kinds of things:** code interpreters and data analysis (Perplexity, Groq, Lindy), a "virtual computer" for general-purpose agents (Manus, Genspark), vibe coding and app generation (Anything, Lovable, Orchids), background coding agents (Ramp, Capy, Mintlify), and RL, evals and benchmarks (Hugging Face Open R1, LMArena, Terminal-Bench, Snorkel, Martian).
- **Secret handling has converged on "the egress proxy injects credentials".** E2B, Daytona, Modal (beta) and Vercel all offer the same mechanism: the Sandbox holds only a placeholder, or nothing at all; the egress proxy terminates TLS and fills in the real header by destination domain as the request leaves the Sandbox. The cost is that the Sandbox must trust the platform's proxy CA. Yet each vendor's Claude Code example still puts `ANTHROPIC_API_KEY` straight into an environment variable ([E2B](https://docs.e2b.dev/agents/claude-code.md), [Daytona](https://www.daytona.com/docs/guides/claude/claude-code-run-tasks-stream-logs-sandbox.md)).
- **All four allow outbound traffic by default**, and all offer "block all / CIDR allowlist / domain allowlist (decided by SNI or Host header)". This project's spec denies egress by default, which is stricter than any of them.
- **What vendors call "pause" mostly corresponds to this project's Hibernated, not Paused.** E2B's pause writes memory and disk to storage, is not billed for compute, can be kept indefinitely, and resumes in about 1 second ([persistence](https://docs.e2b.dev/sandbox/persistence.md)). None of the four makes an in-place freeze that keeps memory occupied (our Paused) a main selling point.
- **The workhorse of persistence is the "filesystem-only snapshot", which everyone has.** Memory snapshots or Fork are a bonus: GA on E2B, VM-class Sandboxes only on Daytona v0, and alpha or restricted on Daytona v2 Linux Sandboxes and on Modal.
- **Isolation technology is converging on microVMs.** E2B and Vercel use Firecracker. Daytona v0 (daytona.io) defaults to containers; v2 (daytona.com) switched to Firecracker and Cloud Hypervisor. Modal offers two runtimes, gVisor and VM. The advertised cold-start numbers come from "restoring from a memory snapshot, not cold-booting a kernel" ([E2B runtime README](https://github.com/e2b-dev/runtime), [Daytona v2 architecture](https://www.daytona.com/docs/architecture.md)).
- **Verification of the 6 earlier claims:** 3 hold (Modal's gVisor and multi-cloud, Firecracker vsock supporting guest-initiated connections, Stripe replacing usage records with Billing Meters). 3 only partly hold because the platforms have changed: E2B self-hosting is no longer GCP-centric; Daytona's isolation depends on v0 vs v2; E2B's "150ms" comes from 2025 customer stories, while the current docs say "~80ms". See section 4.

---

## 2. Platform details

### 2.1 E2B

**Background.** The current docs live at docs.e2b.dev, SDK version v2.64.0 ([llms.txt](https://docs.e2b.dev/llms.txt)). The whole backend is open source as `e2b-dev/runtime`, Apache-2.0. The former `e2b-dev/infra` now redirects to that repo ([runtime README](https://github.com/e2b-dev/runtime)).

#### (1) Where the agent runs

- **Both are supported; the onboarding material is "outside".** The quickstart calls `Sandbox.create()` in the customer's own process, then `run_code()` ([quickstart](https://docs.e2b.dev/quickstart.md)). The "Connect LLMs" page says "The easiest way to connect an LLM to E2B is to use the tool use capabilities of the LLM", and all its examples have the customer's server call the LLM and then hand the code to the Sandbox to execute ([connect-llms](https://docs.e2b.dev/quickstart/connect-llms.md)).
- **"Inside" is the main thread of the Coding Agents use case.** The docs say "E2B sandboxes give each agent a full Linux environment … Pre-built templates mean you can go from zero to a running agent in a single API call". It ships pre-built Templates for Claude Code, Codex, Amp, Gemini CLI, OpenCode and others ([coding-agents](https://docs.e2b.dev/use-cases/coding-agents.md)). The Claude Code page's flow is: `Sandbox.create("claude", envs={ANTHROPIC_API_KEY})`, then `commands.run('claude --dangerously-skip-permissions -p "…"', timeout=0)`, and finally read back `git diff` or the `--output-format stream-json` output ([claude-code](https://docs.e2b.dev/agents/claude-code.md)).
- **Hosted harness pattern.** The Claude Managed Agents page says "Claude runs the agentic loop and reasoning process; E2B provides isolated sandbox infrastructure for the environment where tool calls execute" ([claude-managed-agents](https://docs.e2b.dev/agents/claude-managed-agents.md)). Claude Toolsets has the client-side tool runner drive an E2B desktop Sandbox for computer use and browser use ([claude-toolsets](https://docs.e2b.dev/agents/claude-toolsets.md)), which is essentially also "agent outside".
- **[Inference]** By page count, "inside" is now the majority; by onboarding path, "outside" is still the default.

#### (2) Use cases and customer stories

The customer index groups production use into five categories: general-purpose agents, vibe coding and app generation, enterprise automation, code actions and data analysis, and research and evals. The vendor self-reports "more than 100,000 teams have run a sandbox", with Sandbox starts passing 1 billion in June 2026 and 2 billion in October 2026 ([customers.md](https://e2b.dev/customers.md)). The table below is drawn from the vendor's customer stories:

| Customer | What they do | Agent location (per the story) | Source |
|---|---|---|---|
| Manus | General-purpose agent. A planner decomposes tasks; executors use a browser, terminal and filesystem; sessions can run for dozens of minutes; needs pause/resume (while waiting for user confirmation, needing credentials, or passing human verification); **self-hosts E2B on its own machines** | Agent loop on Manus's side; the Sandbox is a "virtual computer" | [case](https://e2b.dev/customers/how-manus-uses-e2b-to-provide-agents-with-virtual-computers.md) (2025-05-06) |
| Genspark | Super Agent: research, slide creation, data analysis; needs long workflows and persistent state | Same as above | [case](https://e2b.dev/customers/genspark.md) (2026-05-07) |
| Anything | Vibe coding, over 1 million users; one Template per project, with new versions generated continuously; uses snapshots to resume running dev servers | The Sandbox is a dev machine | [case](https://e2b.dev/customers/anything.md) (2026-07-20) |
| Rogo | Financial agents; **Claude Managed Agents as the harness, E2B as the execution layer**; 10,000 to 15,000 concurrent Sandboxes; uses domain allowlists | Hosted harness outside | [case](https://e2b.dev/customers/rogo.md) (2026-06-12) |
| StackAI | Enterprise no-code: Code Node, Computer Use/RPA, Browser Use | Outside | [case](https://e2b.dev/customers/stackai.md) (2026-06-08) |
| Groq | Compound: server-side tool execution (code execution plus search) | Outside | [case](https://e2b.dev/customers/groqs-compound-ai-models-are-powered-by-e2b.md) (2025-05-20) |
| Lindy | Code Action in workflows, running Python and JS | Outside | [case](https://e2b.dev/customers/lindy-powers-ai-workflows-with-e2b-code-action.md) (2025-05-19) |
| Hugging Face | Open R1's RL reward function: executes model-generated code in the Sandbox and scores it against test cases | Outside; the training loop calls the Sandbox | [case](https://e2b.dev/customers/how-hugging-face-is-using-e2b-to-replicate-deepseek-r1.md) (2025-03-31) |
| LMArena | WebDev Arena evals: runs web apps generated by two LLMs side by side | Outside | [case](https://e2b.dev/customers/how-lmarena-collaborated-with-e2b-to-build-llm-web-development-evals.md) (2025-02-03) |

> Staleness note: the Manus story says paid users' data "can save … for up to 14 days". The current docs say Paused is kept indefinitely (see subsection 4); the 14 days presumably reflects 2025. The old Perplexity story URL now 307-redirects to /customers and the original is unreachable, so this note does not cite it.

#### (3) Secrets and egress

- **Egress is fully open by default.** The docs: "Every sandbox has outbound access to the internet by default". Available controls: `allowInternetAccess=false`, equivalent to `denyOut: 0.0.0.0/0`; `allowOut` and `denyOut` by IP or CIDR; and a domain allowlist, which requires denying all traffic at the same time. Domain filtering "only works for HTTP traffic on port 80 (via Host header inspection) and TLS traffic on port 443 (via SNI inspection)" and does not support QUIC. Allow takes precedence over deny ([internet-access](https://docs.e2b.dev/network/internet-access.md)).
- **Details worth noting** (same page):
  - As soon as domain filtering is used, `8.8.8.8` is allowed automatically, and the allowance covers all traffic to it, not just DNS. It can be turned off with `dnsServers: []`.
  - A blocked TCP connection looks connected from inside the Sandbox, because the firewall has to accept it before it can decide whether the destination is allowed.
  - Each Sandbox can have at most 1,500 outbound TCP connections open at once.
  - The network policy can be changed while the Sandbox runs, via `updateNetwork`.
- **Secrets (egress proxy injection).** The docs: "Rather than passing a key as an environment variable or baking it into a template, where any code running in the sandbox can read it, you store the value once and reference it in a network rule. The egress proxy injects the real value into matching outbound HTTPS requests outside the sandbox" ([secrets](https://docs.e2b.dev/secrets.md)). Usage is to write `Secret.fill('name')` in `network.rules[host].transform.headers` ([inject](https://docs.e2b.dev/secrets/inject.md)). Key semantics:
  - Injects into HTTPS requests only, not plain HTTP.
  - Injects by destination host. The docs warn: "Every process in the sandbox that sends an HTTPS request to that host gets the secret, including a browser".
  - **Fails open** on resolution failure: the request still goes out, just without the corresponding header.
  - The proxy terminates TLS with E2B's CA, which is written into `/etc/ssl/certs/ca-certificates.crt`. The CA is generated per node, so it may change after a Sandbox resumes on a different node, and clients that pin the certificate loaded at startup will fail ([internet-access §TLS](https://docs.e2b.dev/network/internet-access.md)).
  - There is also workload identity, which mints a short-lived token at each request (same page).
  - Limits: 10 secrets per project on Hobby, 100 on paid plans; a single value can be up to 64 KiB ([limits](https://docs.e2b.dev/secrets/limits.md)).
- **But the Claude Code example still uses environment variables.** The docs: "The examples pass it into the sandbox, where Claude Code reads it". The docs also warn that an auto-approved agent "can still make outbound network requests — internet access is enabled by default", and recommend combining it with network rules ([claude-code](https://docs.e2b.dev/agents/claude-code.md)).
- **Control channel into the Sandbox.** Since SDK v2, secure access is on by default: calls to envd (E2B's Guest Agent) must carry `X-Access-Token`, which is returned when the Sandbox is created ([secured-access](https://docs.e2b.dev/sandbox/secured-access.md)).

#### (4) Persistence

| Mechanism | Semantics (per the docs) | Maps to this project's term |
|---|---|---|
| `pause()` / `connect()` | "both the sandbox's filesystem and memory state will be saved"; "A paused sandbox is kept **indefinitely**"; Paused is not billed for compute and does not count toward concurrency; pause takes about 4 seconds per GiB of RAM, resume about 1 second ([persistence](https://docs.e2b.dev/sandbox/persistence.md), [sandbox-lifetime](https://docs.e2b.dev/faq/sandbox-lifetime.md)) | **Hibernated** (not Paused) |
| `lifecycle.onTimeout: 'pause'` + `autoResume` | Automatically pauses on timeout; the next SDK call or an HTTP request to its URL wakes it (same sources) | Hibernated + wake on traffic |
| `pause({mode:'filesystem'})` | Saves only the disk; resume reboots ([filesystem-only-snapshots](https://docs.e2b.dev/sandbox/filesystem-only-snapshots.md)) | No equivalent |
| `createSnapshot()` | "including both its filesystem and memory state"; the original Sandbox pauses briefly and then keeps running; one snapshot can start many Sandboxes ([snapshots](https://docs.e2b.dev/sandbox/snapshots.md)) | Full-state checkpoint |
| `fork({count})` | Snapshots and starts copies in one call; the SDK allows at most 20 at a time, and a fork's default timeout is 15 seconds; the original Sandbox pauses briefly and its connections drop ([fork](https://docs.e2b.dev/sandbox/fork.md)). The runtime README says "up to a hundred per request", which disagrees with the SDK limit ([runtime](https://github.com/e2b-dev/runtime)) | **Fork** |
| Volumes | **Private beta**; lifecycle independent of the Sandbox; **can be mounted to multiple Sandboxes**; known issues: file locking can hang, metadata caching is disabled, permission bits are not enforced, mounts only at creation, no snapshots ([volumes](https://docs.e2b.dev/volumes.md), [beta limitations](https://docs.e2b.dev/faq/volumes-beta-limitations.md)) | Volume (but this project limits a Volume to one Sandbox at a time) |
| Template | The build snapshots the whole "running sandbox", so it can "load the sandbox in ~80ms any time later with all the processes already running" ([how-it-works](https://docs.e2b.dev/template/how-it-works.md)) | Template (this project only registers by image) |

#### (5) Lifecycle and API shape

- **Timeouts.** The SDK's default timeout is 5 minutes, and "the default is destructive", i.e. `onTimeout: 'kill'`. Continuous runtime is capped at 1 hour on Hobby and 24 hours on Pro; pausing and resuming resets the clock ([sandbox-lifetime](https://docs.e2b.dev/faq/sandbox-lifetime.md)). The REST API's `NewSandbox.timeout` defaults to **15 seconds** ([create-sandbox API](https://docs.e2b.dev/api-reference/sandboxes/create-sandbox.md)), unlike the SDK default. E2B uses a **wall-clock timeout**, not an idle timer. It can be extended with `setTimeout`.
- **Create returns** fields such as `sandboxID`, `templateID`, `envdVersion`, `envdAccessToken`, and `trafficAccessToken` (same source).
- **Exec and Process.** `commands.run()` uses `/bin/bash -l -c` by default ([commands](https://docs.e2b.dev/commands.md)); the SDK's default per-command timeout is 60 seconds ([claude-code](https://docs.e2b.dev/agents/claude-code.md)). With `background: true` it returns immediately and the command runs in the background; another process can reattach with `commands.connect(pid)`, and the docs list "kicking off a long task from a serverless function" as a common pattern ([background](https://docs.e2b.dev/commands/background.md)). There are also PTYs and streaming output. SDK streams send a keepalive every 50 seconds, and no single stream is guaranteed to last past 1 hour ([sandbox-lifetime](https://docs.e2b.dev/faq/sandbox-lifetime.md)).
- **Code interpreter.** Supports multiple code contexts, equivalent to **Execution Context** ([contexts](https://docs.e2b.dev/code-interpreting/contexts.md)).
- **Files.** Read/write, upload/download (with pre-signed URLs), and watch ([llms.txt](https://docs.e2b.dev/llms.txt)).
- **Ports.** Addresses look like `https://<port>-<sandboxId>.e2b.app`. **Public by default**; the docs say "Treat a public URL like a credential" ([public-url](https://docs.e2b.dev/network/public-url.md)). **Projects created after 2026-10-30 default to private**, requiring a traffic access token; older projects keep their previous setting ([restrict-public-access](https://docs.e2b.dev/network/restrict-public-access.md)).

#### (6) Isolation and infrastructure

- The docs: "Every sandbox is a Firecracker microVM, not a container. Each one boots its own kernel". The managed service runs on **Google Cloud**, plus an **AWS** US West region ([security-and-compliance](https://docs.e2b.dev/faq/security-and-compliance.md)). The table on the BYOC page describes E2B Cloud as "E2B's Google Cloud account" ([byoc](https://docs.e2b.dev/byoc.md)).
- The kernel is LTS 6.1 and is fixed when the Template is built ([how-it-works](https://docs.e2b.dev/template/how-it-works.md)).
- Architecturally, "A sandbox is a resumed snapshot": memory pages are loaded on demand via `userfaultfd`, and the rootfs is a CoW layer on a read-only image; each Sandbox has its own cgroup, network namespace, and nftables firewall. Fresh creation, resume and fork all take the same path ([runtime README](https://github.com/e2b-dev/runtime)).
- **Deployment options** ([byoc](https://docs.e2b.dev/byoc.md), [Embed README](https://github.com/e2b-dev/runtime/tree/main/embed)):
  - E2B Cloud: managed.
  - BYOC: Enterprise only, on AWS, GCP or Azure, operated by E2B.
  - E2B Embed (self-hosted): **single machine**, "a host with KVM, a VM on GCP, AWS, or Azure, or a node in your Kubernetes cluster", installed via Docker Compose or Terraform.
- **Advertised cold start.**
  - Current docs: "~80ms", meaning loading from a Template snapshot with processes already running ([how-it-works](https://docs.e2b.dev/template/how-it-works.md)).
  - Customer stories: "~150ms" (LMArena 2025-02, Manus 2025-05, Lindy 2025-05).
  - The homepage ([e2b.dev](https://e2b.dev/)) now only says "boots from a snapshot", and the customers page says "Boots from a snapshot. No cold start."; neither gives a number ([customers/research](https://e2b.dev/customers/research.md)).

---

### 2.2 Daytona

**Background (important).** Daytona currently runs two platforms side by side:

- **Daytona v0**: `app.daytona.io`, docs at daytona.io/docs, SDK 0.x; the docs call it "the current platform".
- **Daytona v2**: `api.daytona.com`, docs at daytona.com/docs, SDK 2.x; it is "the next version". It has its own API and resource model, and Sandboxes are not migrated automatically ([upgrade-to-v2](https://www.daytona.io/docs/en/upgrade-to-v2.md)).

The open-source repo `daytonaio/daytona` says "As of June 2026, Daytona's core development has moved to a private codebase"; its last release is v0.190.0 ([GitHub README](https://github.com/daytonaio/daytona)). Below, v0 and v2 are labelled separately.

#### (1) Where the agent runs

- **Both are supported; the guides are mostly "inside".** In the v2 guides list, "Run Claude Code / Devin CLI / Gemini CLI / Kimi CLI / Kiro CLI / OpenCode / OpenClaw / Letta Code in a Daytona Sandbox" make up the majority ([v2 llms.txt](https://www.daytona.com/docs/llms.txt)). The Claude Code example: `sandbox.process.exec("npm install -g @anthropic-ai/claude-code")`, then run `ANTHROPIC_API_KEY=… claude --dangerously-skip-permissions -p …` in a PTY ([guide](https://www.daytona.com/docs/guides/claude/claude-code-run-tasks-stream-logs-sandbox.md)).
- **"Outside" examples:** data analysis ("You prompt the LLM to generate code … The sandbox runs the AI-generated code and returns the results", [guide](https://www.daytona.com/docs/guides/claude/data-analysis-with-ai.md)), plus LangChain, Vercel AI SDK, DSPy RLM and others.
- **Hybrid:** "Project Manager Agent (local) and a Developer Agent (in-sandbox)" ([two-agent guide](https://www.daytona.com/docs/guides/claude/claude-agent-sdk-connect-service-sandbox.md)).
- **Hosted harness:** the "self-hosted environment" for Claude Managed Agents; the docs say "the agent loop, prompt caching, model calls … stay on Anthropic's side. The container that holds the agent's filesystem and shell runs on your infrastructure" ([guide](https://www.daytona.com/docs/guides/claude/claude-managed-agents.md)). There are also integrations for the OpenAI Agents API, Cursor self-hosted machines, and Devin Outposts ([v2 llms.txt](https://www.daytona.com/docs/llms.txt)).

#### (2) Use cases and customer stories

The v0 docs position it for "AI, agent and reinforcement learning workflows" ([llms-full](https://www.daytona.io/docs/llms-full.txt)). The v2 docs list these use cases: code execution for AI agents, coding agents, RL ("very large numbers of identical environments, run one episode in each"), untrusted user code, and development environments ([linux-sandboxes](https://www.daytona.com/docs/linux-sandboxes.md)). The table below is drawn from the vendor's customer stories:

| Customer | What they do | Source |
|---|---|---|
| Laude Institute (Terminal-Bench) | Agent benchmark; went from 4 to 6 Docker containers locally to thousands in parallel; uses the Declarative Image Builder to build environments from each task's Dockerfile; "37k sandboxes created in one week" | [case](https://www.daytona.io/customers/laude) |
| Snorkel | Agent simulation and eval datasets; "1,073,756 sandboxes provisioned"; values that the open-source code is auditable | [case](https://www.daytona.io/customers/snorkel) |
| Martian | ARES: online RL for coding agents (SWE-Bench Verified and others) | [case](https://www.daytona.io/customers/martian) |
| Stanford CS (DataComp) | Experiment environments for training agentic models; a single environment runs for up to 3 hours | [case](https://www.daytona.io/customers/stanford) |
| Mastra | Cloud features of an agent framework (Mastra Studio); needs stateful, long-running Sandboxes that can "pause, wait for human approval, and resume"; the story says "each sandbox is container-based … starts in sub-second time" | [case](https://www.daytona.io/customers/mastra) |
| Capy | AI IDE. A Captain agent decomposes tasks and multiple Build agents run in parallel, one Sandbox per agent; needs to pause for days and resume from a checkpoint | [case](https://www.daytona.io/customers/capy) |
| Mintlify | "Docs from GitHub": an agent analyzes a code repo and generates documentation | [case](https://www.daytona.io/customers/mintlify) |
| WRITER | Enterprise agent platform. Originally self-built on AWS Fargate; moved to Daytona after passing 20,000+ Sandboxes a day | [case](https://www.daytona.io/customers/writer) |
| Orchids | AI full-stack development / app generation | [case](https://www.daytona.io/customers/orchids) |

> The customer story pages carry no publication dates. Judging from phrases on the pages such as "Terminal Bench 2.0 on the horizon", most were probably written in 2025 and describe the v0 (container) platform.

#### (3) Secrets and egress

- **v0 egress is tiered by organization plan.** "Tier 1 & Tier 2: Network access is restricted and cannot be overridden at the sandbox level". "Tier 3 & Tier 4: Full internet access is available by default"; these two tiers can configure per Sandbox: `networkAllowList` (CIDR), `domainAllowList`, `networkBlockAll`, which are mutually exclusive; traffic can also be chained to the customer's own proxy via `outboundProxyUrl`. Rules can be changed at runtime ([v0 network-limits](https://www.daytona.io/docs/en/network-limits.md)).
- **v2 egress is open by default.** The docs: "Egress is open by default, and a network policy restricts it with a default-deny allow list of IPv4 CIDR ranges and domains". It applies only to Linux and macOS Sandboxes; Windows and GPU Sandboxes don't support it. Ingress is closed by default; ports must be exposed explicitly ([v2 isolation](https://www.daytona.com/docs/isolation.md), [v2 network-limits](https://www.daytona.com/docs/network-limits.md)).
- **Secrets (egress proxy replaces placeholders), in both v0 and v2.** At Sandbox creation an environment variable is mapped to a secret, and inside the Sandbox that variable holds a placeholder (`dtn_secret_…` in v0). When the proxy sees the placeholder in an HTTPS request and the destination host is in that secret's `hosts` allowlist, it swaps in the real value ([v0 secrets](https://www.daytona.io/docs/en/secrets.md)). Differences between v0 and v2:

  | | v0 | v2 |
  |---|---|---|
  | `hosts` allowlist | Optional; omitted means "unrestricted", and the docs explicitly warn about the risk | **Required** |
  | Replacement scope | Request headers only | Replaces in "the request line, every header, and the body" |
  | Responses | Scrubs the real value in responses back to the placeholder | "The proxy does not rewrite responses, so an approved host that echoes the credential returns the plaintext value to the sandbox" |

  v2 also states that the proxy "dials the destination itself and validates its certificate", to guard against DNS, SNI and Host spoofing ([v2 secrets](https://www.daytona.com/docs/secrets.md)).
- **Official examples go both ways.** The Fireworks guide uses a secret for BYOK and shows `printenv` revealing only the placeholder ([guide](https://www.daytona.com/docs/guides/fireworks/fireworks-coding-agent.md)). But the Claude Code guide still splices the plaintext key into the command line ([guide](https://www.daytona.com/docs/guides/claude/claude-code-run-tasks-stream-logs-sandbox.md)).

#### (4) Persistence

**v0** ([v0 sandboxes](https://www.daytona.io/docs/en/sandboxes.md)):

| Operation | Container (default) | Linux VM / Windows | Semantics |
|---|---|---|---|
| stop/start | ✓ | ✓ | "Stopping terminates the running container. The filesystem is preserved, but memory state is not" |
| archive | ✓ | ✗ | Moves a stopped Sandbox's filesystem to object storage, freeing disk quota |
| pause/resume | ✗ | ✓ | "filesystem and memory state preserved. State is offloaded to nearby storage"; **[Inference]** close to this project's Hibernated |
| fork | ✗ | ✓ | Copies filesystem and memory and records a fork tree; a parent Sandbox cannot be deleted while it has live fork children |
| snapshot from sandbox | Filesystem only | Filesystem, or filesystem plus memory | — |

Volumes (v0): "FUSE-based mounts … stored in an S3-compatible object store", "a single volume can be mounted to multiple sandboxes", with `subpath` recommended for isolating tenants ([v0 volumes](https://www.daytona.io/docs/en/volumes.md)).

**v2** ([v2 sandboxes](https://www.daytona.com/docs/sandboxes.md), [persistence](https://www.daytona.com/docs/persistence.md)): "Sandboxes are ephemeral by default". **Linux Sandboxes support no stop/start, pause or fork, and have no filesystem or memory persistence**; they can only be deleted. State is kept in three ways:

- image capture: saves a running Sandbox's filesystem as an image, which amounts to creating a new Template.
- Memory snapshot: "a copy of the memory and disk of a sandbox … A sandbox restored from it continues from that point instead of booting"; the restored result is a new Sandbox ([linux-sandboxes](https://www.daytona.com/docs/linux-sandboxes.md)).
- Volumes: disk volumes or bucket volumes, up to 8 per Sandbox attached at creation, with commit/pull semantics ([v2 volumes](https://www.daytona.com/docs/volumes.md)).

Only macOS Sandboxes support pause and fork.

#### (5) Lifecycle and API shape

- **v0** ([v0 sandboxes](https://www.daytona.io/docs/en/sandboxes.md)):
  - auto-stop defaults to stopping after 15 minutes of inactivity. **Only lifecycle operations, preview traffic, SSH and Toolbox API requests reset the timer**. Background scripts don't count as activity; the docs say "If you run a long-running task like LLM inference that takes more than 15 minutes … the sandbox may auto-stop mid-process".
  - auto-archive defaults to 7 days, and auto-delete defaults to never. VM-class Sandboxes with neither set default to auto-pause after 60 minutes. A wall-clock TTL can also be set.
  - Default resources: 1 vCPU, 1 GiB memory, 3 GiB disk.
- **v2** ([linux-sandboxes](https://www.daytona.com/docs/linux-sandboxes.md)): three timeouts, each of which **deletes** the Sandbox on expiry:
  - create timeout: defaults to 3600 seconds.
  - lifetime timeout: at most 86400 seconds; the default depends on project or platform configuration.
  - idle timeout: none by default unless the project sets one. The docs say "Requests, connections, and running commands count as activity"; **running commands count as activity**, unlike v0.
  - Default resources: 0.5 cores, 1 GiB memory ([v2 sandboxes](https://www.daytona.com/docs/sandboxes.md)).
- **Exec and Process.** There are one-off commands; sessions, i.e. persistent shells that "keep the working directory and environment variables across the commands", which can run long background processes; PTYs and log streaming; and code execution, either stateless or in a persistent interpreter context, equivalent to **Execution Context** ([v0 process](https://www.daytona.io/docs/en/process-code-execution.md), [v2 process](https://www.daytona.com/docs/process-execution.md)). Filesystem and Git operations have their own APIs.
- **Ports (preview URLs).** Private by default. A standard preview URL requires the `x-daytona-preview-token` header; that token is valid for the whole Sandbox and cannot be revoked. A signed preview URL puts the token in the URL, is bound to a single port, can expire, and can be revoked (v2 default 60 seconds, at most 7 days). A Sandbox can also be made public ([v0 preview](https://www.daytona.io/docs/en/preview.md), [v2 preview](https://www.daytona.com/docs/preview.md)).

#### (6) Isolation and infrastructure

- **v0: containers by default.** The docs: "Sandboxes run as **Linux containers** by default". There are also VM-class Sandboxes (Linux VM, Windows), GPU Sandboxes and macOS Sandboxes ([v0 sandboxes](https://www.daytona.io/docs/en/sandboxes.md)). The same doc also says each Sandbox has "a dedicated kernel", which doesn't match ordinary container semantics; the docs don't explain it.
  - **Source-code observation** (last open-source release v0.190.0; may not reflect the cloud production configuration): the runner creates containers through the Docker API (`apps/runner/pkg/docker`); the OCI runtime is configured by the `CONTAINER_RUNTIME` environment variable, which defaults to empty, i.e. Docker's default runtime; non-GPU Sandboxes are created with `Privileged: gpuIndex == nil`, with the comment "Non-GPU sandboxes still need privileged for their current workloads" ([container_configs.go@v0.190.0](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/pkg/docker/container_configs.go), [config.go@v0.190.0](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/cmd/runner/config/config.go)). Which runtime the cloud production environment uses (e.g. Sysbox or Kata) is not stated in the docs. **[Inference]** The docs' "dedicated kernel" claim doesn't hold under the open-source code's default configuration; it could only hold with a runtime the docs don't mention.
- **v2: microVMs by default.** The docs: "Linux sandboxes are Firecracker microVMs that run their own Linux kernel, and they are the default sandbox type". Windows and GPU Sandboxes use Cloud Hypervisor; macOS uses Apple's Virtualization framework ([v2 sandboxes](https://www.daytona.com/docs/sandboxes.md)). Runners pull jobs via NATS JetStream, and the docs say "Runners accept no inbound connections" ([architecture](https://www.daytona.com/docs/architecture.md)). BYOC supports running Sandboxes on the customer's own Kubernetes nodes, which must be bare metal or have nested virtualization ([byoc](https://www.daytona.com/docs/bring-your-own-compute.md)). The docs don't say which cloud the managed service uses; the CPU type list includes AWS Graviton ([v2 sandboxes](https://www.daytona.com/docs/sandboxes.md)).
- **Advertised cold start.**
  - The v0 intro page and README: "spinning up in under 90ms from code to execution"; the Container card says "<90ms" ([llms-full](https://www.daytona.io/docs/llms-full.txt), [v0 sandboxes](https://www.daytona.io/docs/en/sandboxes.md)).
  - The v2 quickstart still says "A sandbox spins up in under 90ms from code to execution" ([quickstart](https://www.daytona.com/docs/quickstart.md)).
  - But the v2 architecture page says "Expect most Linux sandboxes to be created in under a second, because their runner resumes a pre-built memory snapshot" ([architecture](https://www.daytona.com/docs/architecture.md)).
  - Neither states the measurement conditions.

---

### 2.3 Modal Sandboxes (for comparison, brief)

- **Agent location.** Both. "Outside": code interpreters, "Run a LangGraph agent's code in a secure GPU sandbox". "Inside": Cursor Cloud Agent workers run in the Sandbox ([llms.txt](https://modal.com/llms.txt), [cursor](https://modal.com/docs/examples/cursor.md)). In the Ramp story, "OpenCode runs as the coding agent" inside the Sandbox, alongside Postgres, Redis, a VS Code server, VNC and Chromium in the same environment ([Ramp story, 2026-02-19](https://modal.com/blog/how-ramp-built-a-full-context-background-coding-agent-on-modal)).
- **Customers.** The product page lists Linear (Coding Sessions), Ramp (Inspect), Cognition (RL infrastructure), and Abundant (RL environments) ([products/sandboxes](https://modal.com/products/sandboxes)).
- **Secrets and egress.**
  - Default egress: "can make outbound connections to any public IP address". Options: `block_network`, `outbound_cidr_allowlist`, `outbound_domain_allowlist` (beta, port 443 TLS only). The docs warn "DNS traffic is allowed without restriction except when all outbound traffic is blocked" ([sandbox-networking](https://modal.com/docs/guide/sandbox-networking.md)).
  - Secret injection (beta): "An Outbound Policy lets a Sandbox call external HTTPS APIs … without the API key ever being visible inside the Sandbox" ([secret-injection](https://modal.com/docs/guide/sandbox-secret-injection.md)).
  - Counter-example: the Cursor example puts `CURSOR_API_KEY` in the worker's environment, and the docs say outright "code running in a worker sandbox can read it" ([cursor](https://modal.com/docs/examples/cursor.md)).
- **Persistence** ([sandbox-snapshots](https://modal.com/docs/guide/sandbox-snapshots.md)):
  - Filesystem Snapshots and Directory Snapshots are both Images, stored as a diff against the base image, retained for 30 days by default.
  - Memory Snapshots are alpha, saving the "entire state, both in memory and on the filesystem"; they **expire after 7 days and cannot be extended**; taking one currently terminates the original Sandbox; they can only be restored on the same instance type.
  - Volumes can be shared by multiple Sandboxes and support subpaths ([sandbox-files](https://modal.com/docs/guide/sandbox-files.md)).
  - **[Inference]** Modal has no pause/resume; persistence is "snapshot → new Sandbox".
- **Lifecycle.** "default maximum lifetime of 5 minutes", up to 24 hours; `idle_timeout` is optional, and activity includes a running exec, writes to stdin, and open TCP connections on a tunnel. Beyond 24 hours, use snapshots to relay ([sandboxes](https://modal.com/docs/guide/sandboxes.md)). `sb.exec()` supports streaming stdout/stderr ([sandbox-spawn](https://modal.com/docs/guide/sandbox-spawn.md)). There is a file API ([sandbox-files](https://modal.com/docs/guide/sandbox-files.md)). Ports are exposed via Tunnels, by default on subdomains of `w.modal.host` ([sandbox-networking](https://modal.com/docs/guide/sandbox-networking.md)).
- **Isolation and infrastructure.**
  - Two runtimes: gVisor ("implements the Linux system call interface in userspace") and VM ("its own Linux kernel … KVM"); the docs say "leave unset to let Modal choose for you". GPU Sandboxes can only use gVisor ([sandboxes](https://modal.com/docs/guide/sandboxes.md), [sandbox-networking](https://modal.com/docs/guide/sandbox-networking.md)).
  - Multi-cloud: the docs say "Modal runs containers globally across multiple different clouds" ([region-selection](https://modal.com/docs/guide/region-selection.md)). Sandboxes have a `cloud` parameter, described as "Cloud provider for the sandbox" ([SDK](https://modal.com/docs/sdk/py/latest/Sandbox.md)).
  - Advertised cold start: "Modal Sandboxes start in under one second", "Sub-second cold-starts even on custom and large images" ([products/sandboxes](https://modal.com/products/sandboxes)).

### 2.4 Vercel Sandbox (for comparison, brief)

- **Agent location.** The docs say "Run coding agents", and the default image comes with coding agents pre-installed ([sandbox](https://vercel.com/docs/sandbox.md)). A KB article is dedicated to running the Claude Agent SDK in the Sandbox ([kb](https://vercel.com/kb/guide/using-vercel-sandbox-claude-agent-sdk)). Customers named in the GA blog: Roo Code uses snapshots so an agent can pick up a task "days later"; Blackbox AI's Agents HQ dispatches multiple agents in parallel ([GA blog, 2026-01-30](https://vercel.com/blog/vercel-sandbox-is-now-generally-available)).
- **Secrets and egress** ([firewall](https://vercel.com/docs/sandbox/concepts/firewall.md), [security](https://vercel.com/docs/sandbox/concepts/security.md), [credentials](https://vercel.com/docs/sandbox/concepts/credentials.md)):
  - Defaults to `allow-all`; there are also `deny-all` (blocks DNS too) and user-defined allowlists, all switchable at runtime.
  - Firewall rules can do credentials brokering, request proxying, or direct responses. Domains whose requests need rewriting get TLS-terminated with "a CA unique to your sandbox"; other domains aren't decrypted.
  - Platform-managed npm credentials; the docs say the Sandbox "can use the credential but can't read it".
- **Persistence** ([concepts](https://vercel.com/docs/sandbox/concepts.md), [duration KB](https://vercel.com/kb/guide/vercel-sandbox-duration-and-persistence)):
  - "persistent by default": when a session stops, the filesystem is snapshotted automatically, and the next SDK call automatically starts a new session that restores it. **Snapshots contain only the filesystem** and by default expire 30 days after last use.
  - Drives provide cross-Sandbox persistence, currently in private beta.
- **Lifecycle.** Each session defaults to a 5-minute timeout, up to 45 minutes on Hobby and 24 hours on Pro, extendable with `extendTimeout()`. Default 2 vCPUs, with 2 GB of memory per vCPU. Ports are exposed via public URLs ([pricing](https://vercel.com/docs/sandbox/pricing.md), [security](https://vercel.com/docs/sandbox/concepts/security.md)).
- **Isolation and infrastructure.** The docs: "each sandbox runs in its own Firecracker microVM with a dedicated kernel"; sudo is allowed, and Docker, VPNs and FUSE can run inside. Default region `iad1` ([concepts](https://vercel.com/docs/sandbox/concepts.md), [security](https://vercel.com/docs/sandbox/concepts/security.md)). It is built on Hive, an internal compute platform "powered by Firecracker" ([GA blog](https://vercel.com/blog/vercel-sandbox-is-now-generally-available)). The cloud vendor isn't named; the docs only mention being able to "reach your own AWS VPC over VPC peering". **[Inference]** It runs on AWS. Cold start is described only as "Sandboxes start in milliseconds", with no specific number ([sandbox](https://vercel.com/docs/sandbox.md)).

---

## 3. Comparison table

| Platform | Agent location (as presented in docs) | Isolation technology | Persistence | Secret handling | Default egress | Advertised cold start (verbatim) |
|---|---|---|---|---|---|---|
| **E2B** | Both; onboarding is "outside" (tool use); the Agents directory has ~50 pages, mostly "inside"; hosted harness also supported | Firecracker microVM (managed on GCP, plus AWS US West) | pause stores memory plus disk and is kept indefinitely (≈ Hibernated); memory snapshots; Fork; filesystem-only pause/snapshot; Volumes (private beta, multi-mount) | Egress proxy injects headers by host (`Secret.fill`); workload identity; examples still pass keys via `envs` | **Fully open** | Docs "~80ms" (loading from a Template snapshot); 2025 stories "~150ms" |
| **Daytona v0** (daytona.io, current platform) | Both; guides mostly "inside" | **Linux containers by default** (open-source runner: Docker, privileged for non-GPU, runtime configurable); VM classes also available | Containers: stop keeps disk, archive to object storage, filesystem-only snapshots; VMs: pause, fork, memory snapshots; Volumes (FUSE + S3, multi-mount) | Placeholder env var + proxy replacement (headers only, scrubs responses, `hosts` optional) | Tier 1/2 restricted and not overridable; Tier 3/4 fully open | "under 90ms from code to execution", container card "<90ms" |
| **Daytona v2** (daytona.com, next generation) | Same as above | **Firecracker** (Linux), Cloud Hypervisor (Windows, GPU), Apple VZ (macOS) | Linux ephemeral by default; image snapshots (filesystem); memory snapshot → new Sandbox; Volumes (disk/bucket); no pause/fork (except macOS) | Same as v0, but `hosts` required, replaces in headers and body, does **not** scrub responses | **Fully open** | quickstart "under 90ms"; architecture "most … in under a second" |
| **Modal** | Both; customer stories (Ramp, Linear, Cursor) are "inside" | **gVisor** or VM (KVM), platform picks by default; GPU only on gVisor; multi-cloud | Filesystem/directory snapshots (Image, 30 days); memory snapshots (alpha, 7 days); Volumes; no pause | Outbound Policy header injection (beta); some examples still use env vars | **Fully open** (DNS unrestricted) | "start in under one second" |
| **Vercel** | Leans "inside" (pre-installed coding agents, Claude Agent SDK) | Firecracker microVM | Persistent by default: filesystem snapshot automatically on stop, restored automatically next time; snapshots are filesystem only; Drives (beta) | Firewall credentials brokering (one CA per Sandbox); managed npm credentials | **Fully open** (`allow-all`) | "start in milliseconds" |
| **This project's spec** | demo agent outside, via the public API | Docker → gVisor → Firecracker (switchable) | Paused (in-place freeze), Hibernated, Fork (no filesystem-only version), Volume (one mount at a time) | Default environment variables passed at create; no credential injection | **Denied** | (to be measured) |

---

## 4. Verification of earlier claims

| # | Claim | Verdict | Evidence |
|---|---|---|---|
| 1a | E2B uses Firecracker | **Holds** | "Every sandbox is a Firecracker microVM, not a container" ([security-and-compliance](https://docs.e2b.dev/faq/security-and-compliance.md)); "One Firecracker microVM per sandbox" ([runtime README](https://github.com/e2b-dev/runtime)) |
| 1b | E2B's self-hosting infrastructure mainly targets GCP | **Partly holds, out of date** | The managed service runs in E2B's own Google Cloud account, plus AWS US West ([security-and-compliance](https://docs.e2b.dev/faq/security-and-compliance.md), [byoc](https://docs.e2b.dev/byoc.md)). Self-hosting is now called E2B Embed and is **single-machine**: it installs on any Linux host with KVM (Docker Compose), on a single VM on GCP, AWS or Azure via Terraform, or as a K8s node. BYOC supports AWS, GCP and Azure ([Embed README](https://github.com/e2b-dev/runtime/tree/main/embed), [byoc](https://docs.e2b.dev/byoc.md)). "Self-hosting mainly targets GCP" no longer holds; "the managed service is mainly on GCP" holds |
| 2a | Daytona's default isolation is containers, not microVMs | **Depends on platform version** | v0 (daytona.io, which the docs call the "current platform"): **holds**; the docs say "Sandboxes run as Linux containers by default" ([v0 sandboxes](https://www.daytona.io/docs/en/sandboxes.md)); the open-source runner source also uses Docker containers ([container_configs.go@v0.190.0](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/pkg/docker/container_configs.go)). v2 (daytona.com, the "next version"): **does not hold**; the docs say "Linux sandboxes are Firecracker microVMs … the default sandbox type" ([v2 sandboxes](https://www.daytona.com/docs/sandboxes.md)) |
| 2b | Daytona advertises about 90ms Sandbox creation | **Holds (as advertised)** | "spinning up in under 90ms from code to execution" ([v0 llms-full](https://www.daytona.io/docs/llms-full.txt), [v2 quickstart](https://www.daytona.com/docs/quickstart.md)), with no measurement conditions stated. The v2 architecture page says "under a second" ([architecture](https://www.daytona.com/docs/architecture.md)); the two pages disagree |
| 3 | E2B advertises about 150ms starts | **Partly holds (older wording)** | "~150ms" appears in E2B's own 2025 customer stories: LMArena (2025-02-03), Manus (2025-05-06), Lindy (2025-05-19, "approximately 150ms") ([LMArena](https://e2b.dev/customers/how-lmarena-collaborated-with-e2b-to-build-llm-web-development-evals.md), [Manus](https://e2b.dev/customers/how-manus-uses-e2b-to-provide-agents-with-virtual-computers.md), [Lindy](https://e2b.dev/customers/lindy-powers-ai-workflows-with-e2b-code-action.md)). The current docs say "~80ms" to load from a Template snapshot ([how-it-works](https://docs.e2b.dev/template/how-it-works.md)); the homepage no longer gives a number |
| 4a | Modal Sandboxes use gVisor | **Holds, with an addendum** | There are two runtimes, gVisor and VM; the docs say "leave unset to let Modal choose"; GPU can only use gVisor ([sandboxes](https://modal.com/docs/guide/sandboxes.md)). The security page says "primitives like gVisor or microVMs" ([security](https://modal.com/docs/guide/security.md)) |
| 4b | Modal runs on multiple clouds | **Holds** | "Modal runs containers globally across multiple different clouds" ([region-selection](https://modal.com/docs/guide/region-selection.md)). The docs don't list which clouds |
| 5 | Firecracker vsock supports guest-initiated connections (the host listens on `<uds_path>_<port>`) | **Holds** | "it tries to forward the connection to an AF_UNIX socket listening on the host, at `/path/to/v.sock_PORT`". Addendum: ports in the `[2^30, 2^31)` range are reserved for host-initiated connections, and guest connection requests to that range are always RST ([vsock.md](https://github.com/firecracker-microvm/firecracker/blob/main/docs/vsock.md)) |
| 6 | Stripe's legacy usage records have been superseded by Billing Meters | **Holds** | "Stripe is deprecating usage-records billing … billing meters, our only solution for usage-based billing"; "The legacy usage records APIs are removed in Stripe version 2025-03-31.basil"; continuing to use them requires pinning API version 2025-02-24.acacia or earlier ([migration-guide](https://docs.stripe.com/billing/subscriptions/usage-based-legacy/migration-guide.md)) |

---

## 5. Implications for this project's design (**this whole section is inference**)

Grouped as "aligned with the industry / different from the industry / missing from the spec". Each item names the relevant spec section.

**Aligned with the industry**

1. **Templates registered by image, Guest Agent injected by the platform, scheduling on the Host side.** This structure is isomorphic to E2B (API → orchestrator → envd) and Daytona (API → runner → daemon) ([E2B runtime](https://github.com/e2b-dev/runtime), [Daytona architecture](https://www.daytona.com/docs/architecture.md)). ADRs 0001, 0002 and 0003 need no changes.
2. **Exposed Port requires a token by default.** Consistent with Daytona, and with E2B's default for projects created after 2026-10-30. E2B changing its default is itself evidence that "public by default" proved to be a pitfall.
3. **5-minute default reclamation.** Same order of magnitude as the 5-minute default timeouts of E2B, Modal and Vercel.

**Different from the industry (trade-offs to make deliberately)**

4. **Egress denied by default (spec §7).** All four default to fully open. Keeping default-deny is a reasonable security choice, but it directly affects the "agent inside the Sandbox" pattern: the agent must reach the LLM API, and very likely pip, npm and GitHub too. Suggest offering reusable allowlist presets at the Template or Tenant level (e.g. "llm-providers", "package-registries"). Daytona v0's "essential services" is this kind of approach ([v0 network-limits](https://www.daytona.io/docs/en/network-limits.md)).
5. **Terminology mismatch: the industry's "pause" ≈ this project's Hibernated.** E2B's and Daytona VMs' pause write state to storage and release CPU, and E2B explicitly says paused Sandboxes aren't billed for compute and can be kept indefinitely. This project's Paused is an in-place freeze with memory still held, plus a 1-hour cap; the industry rarely treats that as a selling point. Suggestions: (a) state this mapping explicitly in the learning notes so readers don't treat the two as the same thing; (b) the phase-3 Hibernate is the capability that actually matches the industry, so consider making "auto-Hibernate on timeout, auto-resume on request" (E2B's `onTimeout: 'pause'` + `autoResume`) a goal.
6. **"No weakened filesystem-only Fork" (spec §2).** The definition of Fork can stay. But the industry's main persistence mechanism is precisely "filesystem snapshot → new Sandbox or new Template": E2B's filesystem mode, Daytona v2's image capture, Modal's Filesystem Snapshot, and Vercel's persistent Sandboxes all fall into this category. Suggest adding it to the GLOSSARY as a **separate concept** (e.g. "capture a Template from a Sandbox") rather than counting it as a Fork. It is much simpler to implement than memory-level Fork and works on all three Runtimes.
7. **A Volume mounts on only one Sandbox at a time (GLOSSARY).** E2B, Daytona and Modal Volumes all allow simultaneous mounting by multiple Sandboxes, with tenant isolation via subpaths. Single-mount semantics are simpler, but rule out the "shared read-only dataset" common in RL and evals. Consider adding "multiple read-only mounts" later.

**Missing from the spec**

8. **What supporting the "agent inside the Sandbox" pattern requires.**
   - (a) **Idle detection would kill it by mistake.** Spec §5 says "Process 本身不算" (a Process by itself doesn't count) as activity. If a customer runs Claude Code as a Process in the Sandbox and makes no API calls during a 30-minute run, the Sandbox gets reclaimed after 5 minutes. Daytona v0's docs specifically warn about the same problem, v2 changed to "running commands count as activity", and E2B simply uses a wall-clock timeout. Suggest giving Process a "keepalive" option, or adding a separate wall-clock mode.
   - (b) **Exec is capped at 1 hour (spec §4).** Agent tasks can run longer, so customers should be clearly steered to Process.
   - (c) **Output can be large.** Process output needs to be readable as a stream and resumable after a disconnect (the spec already has `output?offset=`, which is the right direction).

   The demo agent could add a "run the agent inside the Sandbox" variant as a second form of validation.
9. **Credential injection by the egress proxy (secrets proxy).** All four have it, and in the "agent inside" pattern it is the only way to avoid handing the LLM key to untrusted code. This project's egress proxy currently only inspects SNI and doesn't terminate TLS (spec §7), so it can't inject. If we do it:
   - (a) the proxy must terminate TLS for allowlisted domains that need injection, and install a CA unique to each Sandbox or each Host in the Sandbox's system trust store (both E2B and Vercel do this);
   - (b) credentials are bound to a "destination host allowlist", with `hosts` required (learning from Daytona v0 allowing it to be omitted);
   - (c) decide explicitly whether resolution failure fails open or closed, and whether to scrub responses. E2B, Daytona v0 and Daytona v2 each differ on these two points.

   This suits a phase-2 or phase-3 ADR. Note that the egress proxy is a Host-side component the Sandbox can actively connect to, a known exception to ADR 0005's "outside-in only"; the isolation tests should add a "Sandbox impersonates or bypasses the egress proxy" item.
10. **Pre-taking memory snapshots in the Template for cold start.** The advertised cold-start numbers of E2B ("A sandbox is a resumed snapshot") and Daytona v2 ("resumes a pre-built memory snapshot … instead of a cold boot") come from restoring snapshots, not booting a kernel. The phase-3 Firecracker comparison table should measure "cold kernel boot" and "restore from snapshot" as separate paths; otherwise our numbers can't be compared with the industry's.
11. **DNS is a common hole in egress allowlists.** With domain filtering, E2B automatically allows all traffic to 8.8.8.8; Modal's DNS is unrestricted ([E2B](https://docs.e2b.dev/network/internet-access.md), [Modal](https://modal.com/docs/guide/sandbox-networking.md)). Spec §7 only allows HTTP and HTTPS and says nothing about DNS. Suggest stating it in the spec and adding DNS tunneling to the "bypass the egress allowlist" isolation test.
12. **Hosted harness pattern.** The "self-hosted environment" of Claude Managed Agents and the OpenAI Agents API requires the platform to receive webhooks, or to actively route sessions to some long-lived Sandbox ([E2B](https://docs.e2b.dev/agents/claude-managed-agents.md)). This is outside the project's scope; noted here for future reference.

---

## 6. Sources (all accessed 2026-11-21)

**E2B**
- Docs home https://docs.e2b.dev/ ; index https://docs.e2b.dev/llms.txt
- Quickstart https://docs.e2b.dev/quickstart.md ; Connect LLMs https://docs.e2b.dev/quickstart/connect-llms.md
- Sandbox lifecycle https://docs.e2b.dev/sandbox.md ; Sandbox lifetime FAQ https://docs.e2b.dev/faq/sandbox-lifetime.md
- Persistence https://docs.e2b.dev/sandbox/persistence.md ; Snapshots https://docs.e2b.dev/sandbox/snapshots.md ; Fork https://docs.e2b.dev/sandbox/fork.md ; Filesystem-only snapshots https://docs.e2b.dev/sandbox/filesystem-only-snapshots.md
- Internet access https://docs.e2b.dev/network/internet-access.md ; Public URL https://docs.e2b.dev/network/public-url.md ; Restrict public access https://docs.e2b.dev/network/restrict-public-access.md ; Secured access https://docs.e2b.dev/sandbox/secured-access.md
- Secrets https://docs.e2b.dev/secrets.md ; Inject https://docs.e2b.dev/secrets/inject.md ; Limits https://docs.e2b.dev/secrets/limits.md
- Volumes https://docs.e2b.dev/volumes.md ; Volumes beta limitations https://docs.e2b.dev/faq/volumes-beta-limitations.md
- Commands https://docs.e2b.dev/commands.md ; Background commands https://docs.e2b.dev/commands/background.md ; Code contexts https://docs.e2b.dev/code-interpreting/contexts.md
- Template how it works https://docs.e2b.dev/template/how-it-works.md
- Coding agents https://docs.e2b.dev/use-cases/coding-agents.md ; Claude Code https://docs.e2b.dev/agents/claude-code.md ; Claude Managed Agents https://docs.e2b.dev/agents/claude-managed-agents.md ; Claude Toolsets https://docs.e2b.dev/agents/claude-toolsets.md
- Security & compliance https://docs.e2b.dev/faq/security-and-compliance.md ; BYOC https://docs.e2b.dev/byoc.md ; Create sandbox API https://docs.e2b.dev/api-reference/sandboxes/create-sandbox.md
- E2B Runtime repo https://github.com/e2b-dev/runtime (`e2b-dev/infra` redirects here); Embed https://github.com/e2b-dev/runtime/tree/main/embed
- Homepage https://e2b.dev/ ; customer index https://e2b.dev/customers.md , https://e2b.dev/customers/research.md
- Customer stories: Manus https://e2b.dev/customers/how-manus-uses-e2b-to-provide-agents-with-virtual-computers.md ; Genspark https://e2b.dev/customers/genspark.md ; Anything https://e2b.dev/customers/anything.md ; Rogo https://e2b.dev/customers/rogo.md ; StackAI https://e2b.dev/customers/stackai.md ; Groq https://e2b.dev/customers/groqs-compound-ai-models-are-powered-by-e2b.md ; Lindy https://e2b.dev/customers/lindy-powers-ai-workflows-with-e2b-code-action.md ; Hugging Face https://e2b.dev/customers/how-hugging-face-is-using-e2b-to-replicate-deepseek-r1.md ; LMArena https://e2b.dev/customers/how-lmarena-collaborated-with-e2b-to-build-llm-web-development-evals.md

**Daytona v0 (daytona.io)**
- Sandboxes https://www.daytona.io/docs/en/sandboxes.md ; full docs https://www.daytona.io/docs/llms-full.txt
- Network limits https://www.daytona.io/docs/en/network-limits.md ; Secrets https://www.daytona.io/docs/en/secrets.md ; Volumes https://www.daytona.io/docs/en/volumes.md ; Preview https://www.daytona.io/docs/en/preview.md ; Process & code execution https://www.daytona.io/docs/en/process-code-execution.md
- Upgrade to v2 https://www.daytona.io/docs/en/upgrade-to-v2.md
- GitHub https://github.com/daytonaio/daytona ; runner source https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/pkg/docker/container_configs.go , https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/cmd/runner/config/config.go
- Customer stories: https://www.daytona.io/customers/laude , /snorkel , /martian , /stanford , /mastra , /capy , /mintlify , /writer , /orchids (all prefixed with https://www.daytona.io/customers/)

**Daytona v2 (daytona.com)**
- Index https://www.daytona.com/docs/llms.txt ; Quickstart https://www.daytona.com/docs/quickstart.md
- Sandboxes https://www.daytona.com/docs/sandboxes.md ; Linux sandboxes https://www.daytona.com/docs/linux-sandboxes.md ; Isolation https://www.daytona.com/docs/isolation.md ; Architecture https://www.daytona.com/docs/architecture.md
- Persistence https://www.daytona.com/docs/persistence.md ; Volumes https://www.daytona.com/docs/volumes.md ; Network limits https://www.daytona.com/docs/network-limits.md ; Secrets https://www.daytona.com/docs/secrets.md ; Preview https://www.daytona.com/docs/preview.md ; Process execution https://www.daytona.com/docs/process-execution.md ; BYOC https://www.daytona.com/docs/bring-your-own-compute.md
- Guides: Claude Code https://www.daytona.com/docs/guides/claude/claude-code-run-tasks-stream-logs-sandbox.md ; Two-agent https://www.daytona.com/docs/guides/claude/claude-agent-sdk-connect-service-sandbox.md ; Claude Managed Agents https://www.daytona.com/docs/guides/claude/claude-managed-agents.md ; Data analysis https://www.daytona.com/docs/guides/claude/data-analysis-with-ai.md ; Fireworks BYOK https://www.daytona.com/docs/guides/fireworks/fireworks-coding-agent.md

**Modal**
- Index https://modal.com/llms.txt ; Sandboxes https://modal.com/docs/guide/sandboxes.md ; Running commands https://modal.com/docs/guide/sandbox-spawn.md ; Networking & security https://modal.com/docs/guide/sandbox-networking.md ; File access https://modal.com/docs/guide/sandbox-files.md ; Snapshots https://modal.com/docs/guide/sandbox-snapshots.md ; Secret injection https://modal.com/docs/guide/sandbox-secret-injection.md ; Security https://modal.com/docs/guide/security.md ; Region selection https://modal.com/docs/guide/region-selection.md ; SDK https://modal.com/docs/sdk/py/latest/Sandbox.md
- Cursor example https://modal.com/docs/examples/cursor.md ; product page https://modal.com/products/sandboxes ; Ramp story https://modal.com/blog/how-ramp-built-a-full-context-background-coding-agent-on-modal

**Vercel**
- Sandbox https://vercel.com/docs/sandbox.md ; Concepts https://vercel.com/docs/sandbox/concepts.md ; Security https://vercel.com/docs/sandbox/concepts/security.md ; Firewall https://vercel.com/docs/sandbox/concepts/firewall.md ; Credentials https://vercel.com/docs/sandbox/concepts/credentials.md ; Pricing & limits https://vercel.com/docs/sandbox/pricing.md
- KB: Duration & persistence https://vercel.com/kb/guide/vercel-sandbox-duration-and-persistence ; Claude Agent SDK https://vercel.com/kb/guide/using-vercel-sandbox-claude-agent-sdk
- GA blog https://vercel.com/blog/vercel-sandbox-is-now-generally-available

**Other**
- Firecracker vsock https://github.com/firecracker-microvm/firecracker/blob/main/docs/vsock.md
- Stripe migration guide https://docs.stripe.com/billing/subscriptions/usage-based-legacy/migration-guide.md

**Summary of staleness and uncertainty notes**
- E2B's "~150ms" and the Manus story's "kept for 14 days" are both 2025 statements that disagree with the current docs.
- E2B Volumes and Vercel Drives are private beta; Modal Memory Snapshots are alpha; Modal's domain allowlist and secret injection are beta.
- Daytona is mid-migration from v0 to v2, and the defaults differ greatly between the two sets of docs. Most customer stories are based on v0. The open-source repo stopped being maintained in June 2026, so source-code observations don't represent the current cloud.
- Secondary sources were used only to find leads during the search phase and are not the basis of any conclusion.
