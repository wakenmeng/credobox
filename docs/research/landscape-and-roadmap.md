# Agent sandbox market landscape and a check of our roadmap

> English | [中文](landscape-and-roadmap.zh.md)

> Research date: 2026-11-21. Every link was accessed on that day unless noted otherwise.
> Primary sources only: official product pages, docs, pricing pages, changelogs and official repos. Anything that isn't a primary source is labelled as such.
> **[Inference]** marks my own conclusions, not what a source says. Terms follow the [GLOSSARY](../../GLOSSARY.md); vendors' own names are kept in quotes or code formatting.
> This builds on [agent-sandbox-usage.md](agent-sandbox-usage.md), which covers E2B, Daytona, Modal and Vercel in depth. Those four appear here only in the comparison tables, and their claims cite that document's sources.

---

## 1. Summary

- **There are about 15 serious offerings, in three groups.** (a) Sandbox-first startups: E2B, Daytona, Modal, Vercel, Runloop, Morph, Blaxel, Together/CodeSandbox, Fly.io Sprites and Northflank. (b) Hyperscaler "code interpreter" tools: AWS AgentCore Code Interpreter, Azure Container Apps dynamic sessions, Google's Agent Platform sandboxes and OpenAI's containers. (c) Self-hostable open source: kubernetes-sigs/agent-sandbox, Agent Substrate and microsandbox. Cloudflare's Sandbox SDK sits between (a) and (b). All the startups and Cloudflare charge per second or per hour for CPU and memory. The hyperscaler tools are more often capped, preinstalled and short-lived.
- **Isolation has converged on VMs, with gVisor as a solid second.** Firecracker is used by E2B, Vercel, Daytona v2, Cloudflare Containers and Together. Cloud Hypervisor is used by Northflank, Daytona v2 (Windows and GPU) and Substrate. gVisor is used by Modal, Google's Agent Platform sandboxes, Northflank (GPU), Substrate and kubernetes-sigs/agent-sandbox. Hyper-V is used by Azure. Nobody in this list sells plain containers as the boundary for untrusted code.
- **Table stakes**, which essentially every product has (§3): exec with streaming, file read and write, background processes, ports or preview URLs, starting from an OCI image, filesystem-level persistence (a snapshot, or a disk kept across stop and start), some egress control, a TTL or idle timeout, per-second metering, and SDKs in Python and TypeScript. **Close to universal among the startups:** a secrets proxy that keeps credentials out of the sandbox.
- **Less common (about 6 of 14):** Hibernate that keeps memory, with sub-second resume (E2B, Blaxel, Morph, Together, Google, Substrate). **Differentiators that few have:** Fork with memory (E2B, Morph, Daytona VMs, Google snapshots), GPU (Modal, Daytona, Northflank), Windows and macOS (Daytona), BYOC or deploy-to-VPC (E2B, Daytona, Northflank, Runloop) and a desktop for computer use (E2B, Daytona, Runloop, Morph, Google).
- **Pricing splits two ways.** Some charge for *allocated* CPU (E2B, Daytona, Northflank, Runloop, Together, Modal's max(request, usage)). Others charge only for *active* CPU and bill memory as provisioned (Vercel, Cloudflare, AWS). Nobody bills compute for a hibernated or suspended sandbox; all of them bill its storage. Our Usage definition matches this, but it has no meters for egress bytes or for the storage of captured Templates, which several vendors do bill.
- **Agents reach sandboxes through framework adapters more than through MCP.** The OpenAI Agents SDK has 8 sandbox clients, LangChain Deep Agents lists 30 sandbox integrations, AI SDK 7 defines a sandbox interface, and Claude Managed Agents' self-hosted sandboxes name Cloudflare, Daytona, Modal and Vercel as partners. MCP servers are a minority but are growing: Daytona, Vercel, Blaxel, Azure (preview) and microsandbox have one, while E2B archived its own.
- **Roadmap verdict [Inference].** The phase plan is sound for a learning project, with one table stake missing and one feature over-weighted. **Missing:** a "stop but keep the disk" state with auto-resume, which almost everyone has and which `docker stop`/`start` gives almost for free. **Over-weighted:** Frozen. No vendor sells it, yet ticket 09 (Usage) is blocked on it. **Keep gVisor as phase 2:** it is a mainstream production choice and has checkpoint/restore, so it can deliver a first Hibernate earlier than phase 3. **Add a thin MCP server**, which is the cheapest way to let real agents use the platform given that a Python SDK is a non-goal.

---

## 2. Landscape table

"Status" records what the vendor's own pages say. "Not stated" means the pages I read are silent on it.

| Product | Isolation | Persistence | Key features | Pricing unit | Status |
|---|---|---|---|---|---|
| **E2B** | Firecracker microVM | pause = memory + disk, kept indefinitely; filesystem-only pause; snapshots; Fork; Volumes (private beta) | Code interpreter contexts, desktop sandboxes, MCP gateway, secrets proxy, BYOC and self-hosted Embed | vCPU-second + GiB-second (allocated); disk free | GA (see [prior doc](agent-sandbox-usage.md#21-e2b)) |
| **Daytona** v0 / v2 | v0: containers by default, VMs optional. v2: Firecracker (Linux), Cloud Hypervisor (Windows, GPU), Apple VZ (macOS) | v0: stop/start keeps disk, archive, VM pause and Fork. v2: image capture, memory snapshot to a new sandbox, Volumes | Computer use, Windows/macOS, GPU, MCP server (v0 CLI), BYOC | vCPU-hour + GiB-hour, billed per second | v0 "current", v2 "next" (see [prior doc](agent-sandbox-usage.md#22-daytona)) |
| **Modal** | gVisor or VM | Filesystem/directory snapshots; memory snapshots (alpha); Volumes | GPU (gVisor only), secret injection (beta), tunnels | core-second (1 core = 2 vCPU) + GiB-second, max(request, usage) | GA; some features alpha or beta |
| **Vercel Sandbox** | Firecracker microVM | Persistent by default: filesystem snapshot on stop, restored on resume; Drives (public beta) | Credentials brokering in the firewall, Vercel MCP tools | Active CPU-hour + provisioned GB-hour + creations + egress | GA 2026-01-30 |
| **Cloudflare Sandbox SDK / Containers** | Containers: "Firecracker microVM with its own kernel and network" ([concepts](https://developers.cloudflare.com/sandbox/concepts/)); Dynamic Workers: V8 isolates | The disk is lost when the instance stops; `snapshotContainer()` saves the writable root filesystem only, with 30-day expiry (public beta); R2/S3 bucket mounts ([lifetime](https://developers.cloudflare.com/sandbox/concepts/lifetime/)) | Persistent code interpreters (Python, JS, TS), PTY terminals, preview URLs, outbound handlers that inject credentials, **no Internet by default** ([security](https://developers.cloudflare.com/sandbox/concepts/security/)) | Active vCPU-second + provisioned GiB-second + disk GB-second, metered in 10 ms steps; nothing charged while asleep ([pricing](https://developers.cloudflare.com/containers/pricing/)) | GA 2026-04-13 ([changelog](https://developers.cloudflare.com/changelog/post/2026-04-13-containers-sandbox-ga/)); SDK 1.0 released 2026-09-30, without the word "GA"; snapshots in public beta ([changelog](https://developers.cloudflare.com/changelog/post/2026-09-30-sandbox-sdk-1-0/)) |
| **Fly.io Sprites** | "hardware-isolated VM" / "dedicated microVM"; hypervisor not named ([product](https://fly.io/sprites), [docs](https://docs.fly.io/sprites)) | Persistent 100 GB disk that survives sleep; copy-on-write checkpoints in about 1 s that cover the disk but **not processes**; warm and cold idle states | "Sleeps when idle", wakes on a request to its URL; idle timer counts HTTP, stdout, TCP and tasks; connectors keep provider credentials out of the Sprite | CPU-hour + GB-hour of memory + hot and cold storage GB-hour | Not stated |
| **Northflank Sandboxes** | Cloud Hypervisor microVM; gVisor for GPU sandboxes ([product](https://northflank.com/product/sandboxes)) | Ephemeral by default; Volumes (including multi-read-write), S3 FUSE mounts, pause/resume | GPU (L4 to B200), BYOC in your own cloud account | vCPU-hour + GB-hour, billed per second | Not stated (Enterprise SLA 99.99%) |
| **Runloop Devboxes** | "virtual machine technology"; hypervisor not named ([overview](https://docs.runloop.ai/docs/devboxes/overview.md)) | Suspend/resume keeps **disk only**: "not in-memory state" ([lifecycle](https://docs.runloop.ai/docs/devboxes/lifecycle)); disk snapshots that can fork new Devboxes; Blueprints | Agent Gateways (LLM keys stay out), MCP Hub, Playwright browser, Ubuntu desktop, network policies, tunnels ([features](https://docs.runloop.ai/docs/overview/runloop-features.md)) | CPU-second + GB-second + storage GB-second; Pro $250/mo ([pricing](https://www.runloop.ai/pricing)) | Not stated; SOC2 |
| **Morph Cloud** | VMs ("Instances … the underlying VM objects") ([mental model](https://cloud.morph.so/docs/concepts/mental-model)) | Pause keeps memory: "with all processes and memory state intact"; snapshots; branching = snapshot plus N new instances ([pause-resume](https://cloud.morph.so/docs/documentation/instances/pause-resume), [branch](https://cloud.morph.so/docs/documentation/instances/branch)) | Remote desktop, devboxes, S3-compatible Volumes | MCU = 1 vCPU-h + 4 GB RAM-h + 16 GB disk-h, or 5 TB snapshot-hours; the per-MCU price isn't on the plans page ([plans](https://cloud.morph.so/docs/documentation/setup/plans)) | Not stated |
| **Blaxel Sandboxes** | "lightweight, sandboxed virtual machines" with an EROFS + tmpfs overlay ([overview](https://docs.blaxel.ai/Sandboxes/Overview)) | Standby keeps memory and processes, resumes "in under 25 milliseconds", and starts after a few seconds idle; archive keeps the filesystem only; Volumes; Agent Drive | **MCP server per sandbox** ([MCP](https://docs.blaxel.ai/Sandboxes/MCP.md)), preview URLs, terminal | GB RAM-second (CPU bundled with memory); standby $0 compute; snapshot storage $0.20/GB-mo ([pricing](https://blaxel.ai/pricing)) | Not stated; some features (in-place API upgrade, Agent Drive) in beta |
| **Together Code Sandbox** (CodeSandbox SDK) | "CodeSandbox's proprietary implementation of Firecracker" ([launch post, 2025-05-20](https://www.together.ai/blog/code-sandbox)) | Hibernation from memory snapshots, resume in 511 ms at P95; snapshot on termination ([docs](https://docs.together.ai/docs/together-code-sandbox)) | Remote image builder, 2–64 vCPU | vCPU-hour + GiB-hour (2025 launch rates) | **Gated**: "your organization must have them enabled"; the old CodeSandbox SDK is "legacy"; the 2025 price may be out of date |
| **Google Agent Platform sandboxes** (formerly Vertex AI Agent Engine Code Execution) | "a gVisor-isolated container" in a Google-managed tenant project ([sandbox overview](https://docs.cloud.google.com/gemini-enterprise-agent-platform/scale/sandbox)) | Code execution keeps "execution state (memory) for up to 14 days"; custom containers 7 days; snapshots include "memory state" and can branch ([code exec](https://docs.cloud.google.com/gemini-enterprise-agent-platform/scale/sandbox/code-execution-overview), [snapshots](https://docs.cloud.google.com/gemini-enterprise-agent-platform/scale/sandbox/manage-snapshots)) | Code execution (fixed libraries, "no network access", 100 MB I/O), computer use, BYOC custom containers, VPC-SC, CMEK; architecture published as open source (Substrate) | Not found on the official pages I read | Not stated. The product was renamed and the old `cloud.google.com/vertex-ai/...` URL now redirects |
| **AWS Bedrock AgentCore Code Interpreter** | "runs in a containerized environment" ([docs](https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/code-interpreter-tool.html)); AgentCore Runtime itself uses microVMs ([pricing](https://aws.amazon.com/bedrock/agentcore/pricing/)) | Session-scoped state; default timeout 15 min, up to 8 h ([sessions](https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/code-interpreter-resource-session-management.html)) | Python/JS/TS, shell and AWS CLI, S3 up to 5 GB, network mode Sandbox or Public, CloudTrail | vCPU-hour + GB-hour, per second, 1 s minimum; **CPU free during I/O wait and idle** | Not stated on the pages I read; managed session storage for Runtime is "public preview" |
| **Azure Container Apps dynamic sessions** | "Hyper-V isolation" ([overview](https://learn.microsoft.com/en-us/azure/container-apps/sessions)) | Ephemeral; destroyed after a cooldown period | Pre-warmed session pools; code interpreter, custom container and shell pools; **platform-managed MCP server (preview)** ([MCP tutorial](https://learn.microsoft.com/en-us/azure/container-apps/sessions-tutorial-shell-mcp)) | Code interpreter: per allocated session, billed in one-hour increments. Custom container: Dedicated-plan E16 nodes ([billing](https://learn.microsoft.com/en-us/azure/container-apps/billing)) | No preview banner on the overview page; MCP server in **preview** (API `2025-02-02-preview`) |
| **OpenAI containers** (Code Interpreter and hosted shell) | "a fully sandboxed virtual machine" ([code interpreter](https://developers.openai.com/api/docs/guides/tools-code-interpreter)) | Expires after 20 min idle; "can't be reactivated" | 1/4/16/64 GB tiers; hosted shell has **no outbound network by default**, an org-level allowlist, and `domain_secrets` placeholders ([shell](https://developers.openai.com/api/docs/guides/tools-shell)) | Per container session (20 min) by memory tier, billed by the minute with a 5-minute minimum ([pricing](https://developers.openai.com/api/docs/pricing)) | Not stated; docs moved from platform.openai.com to developers.openai.com |
| **kubernetes-sigs/agent-sandbox** (OSS) | Delegates to a RuntimeClass, "like gVisor or Kata Containers" ([README](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/README.md)) | Persistent storage that survives restarts; pause/resume; deep hibernation is only "desired" | `Sandbox`, `SandboxTemplate`, `SandboxClaim` and `SandboxWarmPool` CRDs; router; Python and Go SDKs | — (self-hosted) | v1.0.12 (2026-11-20); API `agents.x-k8s.io/v1beta1` |
| **Agent Substrate** (OSS) | gVisor or Cloud Hypervisor; "Plain Linux containers are not supported" ([README](https://github.com/agent-substrate/substrate)) | Full state (RAM + filesystem) snapshotted when idle and restored on request; resume in under 500 ms | Default-deny networking, an egress gateway that injects credentials, SPIFFE identity per actor, 10–30x oversubscription | — (self-hosted) | v1.0.1 (2026-11-20) |
| **microsandbox** (OSS) | microVMs ("Hardware-level isolation with tiny virtual machines"); libkrun is credited in the acknowledgements ([repo](https://github.com/microsandbox/microsandbox)) | Snapshot and restore of running state | Local-first, runs on Linux, macOS and Windows, SDKs in TS/Rust/Python/Go/Ruby, **MCP server** | — (self-hosted) | "still beta software"; v0.7.16 (2026-11-18) |

**[Inference] Positioning in one line each:**
- E2B and Daytona: general agent computers.
- Modal: a compute platform with sandboxes plus GPUs.
- Vercel and Cloudflare: sandboxes attached to a developer platform.
- Runloop: coding-agent devboxes plus benchmarks.
- Morph: branching VMs for search and RL.
- Blaxel: always-on agents that cost little while idle (standby).
- Sprites: persistent "computers for agents".
- Northflank: microVMs plus BYOC for enterprises.
- Hyperscalers: a code-interpreter tool inside their own agent platforms.
- OSS: build-it-yourself on Kubernetes or locally.

---

## 3. Table stakes vs differentiators

✓ = documented. ◐ = partial, beta, or only on some sandbox types. – = not documented in the pages I read (which doesn't prove it's absent). The first four columns come from [agent-sandbox-usage.md](agent-sandbox-usage.md).

| Feature | E2B | Daytona | Modal | Vercel | Cloudflare | Sprites | Northflank | Runloop | Morph | Blaxel | Google | AWS CI | Azure | OpenAI | Count ✓/◐ |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| Exec (+ streaming) | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | 14 |
| Files API | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | – | ✓ | – | ✓ | ✓ | ✓ | ✓ | ✓ | 12 |
| Background processes | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | – | ✓ | – | ✓ | – | ◐ | – | – | 10 |
| Ports / preview URL | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | – | ✓ | ✓ | ✓ | ✓ | – | ◐ | – | 11 |
| Stateful code interpreter | ✓ | ✓ | – | – | ✓ | – | – | – | – | – | ✓ | ✓ | ✓ | ✓ | 7 |
| Filesystem persistence (snapshot or kept disk) | ✓ | ✓ | ✓ | ✓ | ◐ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | – | – | – | 11 |
| Hibernate with memory (≈ our Hibernated) | ✓ | ◐ | ◐ | – | – | – | – | – | ✓ | ✓ | ✓ | – | – | – | 6 |
| Fork with memory | ✓ | ◐ | – | – | – | – | – | – | ✓ | ◐ | ✓ | – | – | – | 5 |
| Volumes / bucket mounts | ◐ | ✓ | ✓ | ◐ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | – | ◐ | – | – | 11 |
| Secrets kept out of the sandbox | ✓ | ✓ | ◐ | ✓ | ✓ | ✓ | – | ✓ | – | – | – | – | – | ✓ | 8 |
| Egress allowlist | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | – | ✓ | – | ✓ | ◐ | ◐ | ◐ | ✓ | 12 |
| GPU | – | ✓ | ✓ | – | – | – | ✓ | – | – | – | – | – | – | – | 3 |
| Desktop / computer use | ✓ | ✓ | – | – | – | – | – | ✓ | ✓ | – | ✓ | – | – | – | 5 |
| Windows / macOS guests | – | ✓ | – | – | – | – | – | – | – | – | – | – | – | – | 1 |
| BYOC / deploy to your VPC | ✓ | ✓ | – | – | – | – | ✓ | ✓ | – | – | – | – | – | – | 4 |
| Python + TS SDKs | ✓ | ✓ | ✓ | ✓ | ◐ (TS) | ✓ | – | ✓ | ◐ (Py) | ✓ | ◐ | – | – | ✓ | 10 |
| Vendor MCP server | ◐ | ✓ | – | ✓ | – | – | – | ◐ | – | ✓ | – | – | ◐ | – | 6 |

Sources for each cell are in §2 and in the prior doc. Some cells need a note:
- Google's egress control is VPC-SC plus the `internet_access` setting on templates ([custom containers](https://docs.cloud.google.com/gemini-enterprise-agent-platform/scale/sandbox/custom-containers)).
- AWS offers only "Sandbox or Public" network modes. Azure's control is `sessionNetworkConfiguration` ([MCP tutorial](https://learn.microsoft.com/en-us/azure/container-apps/sessions-tutorial-shell-mcp)).
- E2B's MCP offering is a gateway that runs MCP servers *inside* the sandbox ([mcp-gateway](https://docs.e2b.dev/mcp-gateway.md)). Its sandbox-control server `e2b-dev/mcp-server` is **archived** (GitHub API, checked 2026-11-21).
- Runloop's MCP Hub is a credential-hiding gateway to other MCP servers ([features](https://docs.runloop.ai/docs/overview/runloop-features.md)).
- Self-hosted OSS is left out of the counts.

The count column counts ✓ and ◐ together.

**[Inference] Grouping:**
- **Table stakes (≥ 10 of 14):** exec, files, background processes, ports, filesystem persistence, volumes or mounts, egress allowlist, and Python + TS SDKs.
- **Expected in managed sandbox products (6–9):** a stateful code interpreter (7), a secrets proxy (8), and Hibernate with memory (6). An MCP server also reaches 6, but half of those are partial, so it counts as emerging.
- **Differentiators (≤ 5):** Fork with memory, desktop, BYOC, GPU, and Windows/macOS.

Our spec covers every table stake except one: **filesystem persistence that keeps the Sandbox's identity**. We do have Capture, but Capture creates a new Template and hands back a new Sandbox ID. Vercel ("persistent by default"), Sprites, Runloop suspend, Daytona v0 stop/start and Blaxel archive all keep the same sandbox and its disk.

---

## 4. Pricing comparison

All rates are list prices quoted from each page. The normalisation to per-hour figures is my arithmetic.

| Product | CPU | Memory | Storage / snapshot | Other meters | Idle / hibernated / paused |
|---|---|---|---|---|---|
| E2B | $0.000014/vCPU-s ≈ $0.0504/vCPU-h, **allocated** | $0.0000045/GiB-s ≈ $0.0162/GiB-h | Disk "included … at no extra cost" ([pricing](https://e2b.dev/pricing)) | Plan fee (Pro $150/mo), concurrency add-ons | "Compute (vCPU and RAM) is not billed for paused or killed sandboxes" ([FAQ](https://docs.e2b.dev/faq/calculate-sandbox-price.md)); storage for paused sandboxes not stated |
| Daytona | $0.0504/vCPU-h | $0.0162/GiB-h | $0.000108/GiB-h, first 5 GiB free ([daytona.io pricing](https://www.daytona.io/pricing)); v2 docs: ephemeral disk free, Volumes billed | GPU by the hour, Windows/macOS by the core | v2: stopped or paused sandboxes aren't billed for compute, but "its volumes stay billed" ([billing](https://www.daytona.com/docs/billing.md)) |
| Modal | $0.00003942/core-s (1 core = 2 vCPU) ≈ $0.071/vCPU-h | $0.00000667/GiB-s ≈ $0.024/GiB-h | Volumes $0.09/GiB-mo | Egress $0.04/GiB; billed on "max(request, usage)"; minimum 0.125 core / 128 MiB ([pricing](https://modal.com/pricing)) | No pause state |
| Vercel | $0.128/h of **Active CPU** (I/O wait excluded) | $0.0212/GB-h **provisioned**, 1-minute minimum increments | Snapshots $0.08/GB-mo; Drives $0.05/GB-mo + reads and writes | $0.60 per 1M creations; outbound and exposed-port data billed, downloads free ([pricing](https://vercel.com/docs/sandbox/pricing)) | Stopped = filesystem snapshot, billed as snapshot storage |
| Cloudflare | $0.000020/vCPU-s ≈ $0.072/vCPU-h, **active only** | $0.0000025/GiB-s ≈ $0.009/GiB-h, provisioned | Disk $0.00000007/GB-s, provisioned; snapshots free during beta | Egress $0.025/GB (NA/EU); 10 ms granularity; $5/mo Workers Paid ([pricing](https://developers.cloudflare.com/containers/pricing/)) | "Charges stop after the container instance goes to sleep" |
| Fly.io Sprites | $0.0385/CPU-h | $0.021875/GB-h | Hot $0.000683/GB-h while awake; cold $0.000027/GB-h | — | "Warm and cold Sprites are not billed for compute, though storage still accrues" ([product](https://fly.io/sprites)) |
| Northflank | $0.01667/vCPU-h | $0.00833/GB-h | Ephemeral disk free | BYOC: $0.02083 per managed vCPU-h ([product](https://northflank.com/product/sandboxes)) | Not stated |
| Runloop | $0.108/CPU-h ($0.00003/s) | $0.0252/GB-h | Devbox $0.00034/GB-h; snapshot and blueprint $0.000072/GB-h | Blueprint builds $0.252/h; Pro $250/mo; "No minimums" ([pricing](https://www.runloop.ai/pricing)) | Suspended: no CPU or memory charge, storage continues. Note: the page's own worked example doesn't match its rates (≈$2.53 vs the stated $2.73 per day) |
| Morph | MCU bundle (1 vCPU-h + 4 GB-h + 16 GB disk-h) | (in MCU) | 5 TB snapshot-hours = 1 MCU | MCU price not on the official plans page | Paused: "only be charged for storage, not compute" ([pause-resume](https://cloud.morph.so/docs/documentation/instances/pause-resume)) |
| Blaxel | Not billed separately (bundled with memory) | $0.0000115/GB-s ≈ $0.041/GB-h | Snapshots $0.20/GB-mo; images $0.045/GB-mo; Volumes $0.12/GB-mo | Egress included ([pricing](https://blaxel.ai/pricing)) | Standby: "$0 compute"; you pay for snapshot storage |
| Together | $0.0446/vCPU-h | $0.0149/GiB-h | Not stated | — | Not stated. These are **2025 launch rates**; the product is now on custom plans ([blog](https://www.together.ai/blog/code-sandbox), [docs](https://docs.together.ai/docs/together-code-sandbox)) |
| AWS AgentCore CI | $0.0895/vCPU-h | $0.00945/GB-h at **peak** memory | — | Per second, 1 s minimum, 128 MB minimum; EC2 transfer rates ([pricing](https://aws.amazon.com/bedrock/agentcore/pricing/)) | "I/O wait and idle time is free, if no other background process is running" (CPU only) |
| Azure dynamic sessions | Code interpreter: per allocated session, in 1-hour increments | — | — | Custom container: Dedicated plan, E16 nodes ([billing](https://learn.microsoft.com/en-us/azure/container-apps/billing)) | Ends at cooldown |
| OpenAI containers | Bundled | $0.03 per 20 min at 1 GB, up to $1.92 at 64 GB (≈ $0.09/GB-h) | — | By the minute, 5-minute minimum ([pricing](https://developers.openai.com/api/docs/pricing)) | Expires after 20 min idle |
| Google Agent Platform | Not found on the official pages I read | | | | |

**Compared with our Usage definition** (Running: vCPU-s + GB-s; Frozen: GB-s; Hibernated: storage; Volume: storage):

1. **The state model matches the market [Inference].** Every vendor that has a hibernate or suspend state charges storage and no compute for it: E2B, Daytona, Morph, Runloop, Blaxel, Sprites, Cloudflare. None charges memory for a hibernated sandbox. Our Frozen = GB-s is the same idea as "provisioned memory is billed while idle" at Vercel and Cloudflare, and as "memory billed during idle time until … reclaimed" at AgentCore Runtime ([pricing](https://aws.amazon.com/bedrock/agentcore/pricing/)). Nobody sells it as a separate state.
2. **Allocated vs active CPU.** Our Running meter is allocated vCPU-s, which matches E2B, Daytona, Northflank and Runloop. Vercel, Cloudflare and AWS bill active CPU, because "time spent waiting for … AI model calls" dominates agent workloads ([Vercel](https://vercel.com/docs/sandbox/pricing)). **[Inference]** Record an *additional* raw meter for actual CPU time (cgroup `cpu.stat` `usage_usec`, which is cheap to read on Docker) next to allocated vCPU-s. It costs little, and the comparison table can then show how idle agent-inside Sandboxes really are.
3. **Meters we lack that several vendors charge:**
   - egress bytes: Modal, Vercel, Cloudflare, AWS;
   - storage held by captured Templates or snapshots: Vercel, Blaxel, Runloop, Cloudflare after the beta;
   - creation count: Vercel.

   **[Inference]** Add egress bytes (the Egress Proxy already sees every byte) and Template storage to Usage. Leave out creation count and minimum increments. Pricing remains a non-goal; the point is only that the raw Usage covers what the market meters.

---

## 5. Interfaces: how agents actually reach sandboxes

**SDKs.** Python + TypeScript is the baseline: E2B, Daytona v2, Vercel, Runloop, Blaxel and Together ([E2B llms.txt](https://docs.e2b.dev/llms.txt), [Daytona llms.txt](https://www.daytona.com/docs/llms.txt), [Runloop](https://docs.runloop.ai/docs/devboxes/overview.md), [Blaxel](https://docs.blaxel.ai/Sandboxes/Overview), [Together](https://docs.together.ai/docs/together-code-sandbox)). Go is offered by Modal ([sandboxes](https://modal.com/docs/guide/sandboxes)), Sprites ([product](https://fly.io/sprites)), kubernetes-sigs/agent-sandbox ([README](https://github.com/kubernetes-sigs/agent-sandbox/blob/main/README.md)) and microsandbox ([repo](https://github.com/microsandbox/microsandbox)). Cloudflare is TypeScript-only, because the SDK runs inside a Worker ([docs](https://developers.cloudflare.com/sandbox/)).

**Framework integrations (the main channel).**
- **OpenAI Agents SDK "Sandbox Agents"** has built-in clients for Blaxel, Cloudflare, Databricks, Daytona, E2B, Modal, Vercel, Docker and local Unix ([clients](https://openai.github.io/openai-agents-python/sandbox/clients/)). The SDK warns that the local client "adds no OS-level confinement" ([sandbox agents](https://openai.github.io/openai-agents-python/sandbox_agents/)).
- **LangChain / Deep Agents** lists 30 sandbox integrations, including Daytona, Modal, AWS AgentCore, Vercel, E2B, Runloop and Kubernetes ([integrations](https://docs.langchain.com/oss/python/integrations/sandboxes)).
- **Vercel AI SDK 7** defines a sandbox interface, "a standard surface (`run`, `spawn`, file I/O)". `@ai-sdk/sandbox-vercel` and E2B's experimental `@e2b/ai-sdk-sandbox` implement it ([E2B page](https://docs.e2b.dev/agents/vercel-ai-sdk.md)). I found no official ai-sdk.dev page for this interface; that claim rests on E2B's page.
- **Claude Agent SDK.** Anthropic's hosting guide has cookbook code for Docker, Modal and Kubernetes. The SDK "needs outbound HTTPS to `api.anthropic.com`", and the guide recommends routing traffic "through an egress proxy that enforces domain allowlists, injects credentials, and logs requests" ([hosting](https://code.claude.com/docs/en/agent-sdk/hosting)). That is our ADR 0005/0006 design almost word for word.
- **Claude Managed Agents self-hosted sandboxes** (public beta, 2026-05-19) name Cloudflare, Daytona, Modal and Vercel as partners ([announcement](https://claude.com/resources/articles/claude-managed-agents-updates)).

**MCP servers offered by sandbox vendors.** There are three patterns.

| Pattern | Who | Shape |
|---|---|---|
| A. Control-plane MCP: an agent creates and drives sandboxes | Daytona (`daytona-v0 mcp init/start`, local, for Claude, Cursor and Windsurf) ([docs](https://www.daytona.io/docs/en/mcp.md)); Vercel MCP (tools generated one-to-one from REST endpoints, e.g. `run_session_command`, `write_session_files`, `update_session_network_policy`) ([tools](https://vercel.com/docs/agent-resources/vercel-mcp/tools/sandboxes.md)); Azure (2 tools, `launchShell` and `runShellCommandInRemoteEnvironment`, JSON-RPC over HTTP, API-key header, **preview**) ([tutorial](https://learn.microsoft.com/en-us/azure/container-apps/sessions-tutorial-shell-mcp)); microsandbox ([repo](https://github.com/microsandbox/microsandbox)) | A thin wrapper over the existing API |
| B. Per-sandbox MCP endpoint served by the in-sandbox agent | Blaxel (`https://<sandbox>/mcp`, streamable HTTP, bearer token, tools for processes, files and code search) ([MCP](https://docs.blaxel.ai/Sandboxes/MCP.md)) | The Guest Agent speaks MCP |
| C. MCP servers hosted *inside* or *behind* the sandbox | E2B MCP gateway (200+ servers from the Docker catalog, `getMcpUrl()` + token) ([gateway](https://docs.e2b.dev/mcp-gateway.md)); Runloop MCP Hub ([features](https://docs.runloop.ai/docs/overview/runloop-features.md)) | A tool catalogue, not sandbox control |

**[Inference] Should we ship one?** Yes: pattern A, as a small separate binary that calls only the public REST API, with a handful of tools: create, exec, process start/output, write file, read file, expose port, delete. Reasons:
1. A Python SDK is a non-goal, and an MCP server is the cheapest way for real agents (Claude Code, Cursor, Copilot) to use the platform without one.
2. Vercel's MCP tools are generated mechanically from REST, and Azure's has two tools, so it doesn't need to be big.
3. It doubles as a third demo, "agent outside via MCP", that costs almost nothing once tickets 04/05/06/11 exist.
4. Because it uses only the public API, it is a black-box client like `bench/`, and ADR 0005 is unaffected.

Pattern B, a per-sandbox endpoint from the Guest Agent, is a reasonable later option, but it puts protocol surface in the untrusted zone's neighbour; so it shouldn't be phase 1.

---

## 6. Roadmap critique (everything in this section is **[Inference]**, grounded in §2–§5)

### 6.1 What the plan gets right

- **Default-deny egress isn't an outlier after all.** The prior doc found the four dev-sandbox vendors open by default. The wider survey shows Cloudflare Containers (`enableInternet: false`), OpenAI hosted shell ("don't have outbound network access by default"), Google code execution ("no network access") and Substrate ("Default-deny networking") all deny by default. Keep it, together with Allowlist Presets and the DNS resolver.
- **Secrets as placeholders swapped in by the Egress Proxy (ticket 23)** is now universal among the managed products: E2B, Daytona, Modal, Vercel, Cloudflare outbound handlers, Sprites connectors, Runloop Agent Gateways, OpenAI `domain_secrets` and Substrate's egress gateway. Anthropic's own hosting guide recommends the same thing. It's correctly in phase 1.
- **Capture (ticket 22)** matches the industry's main persistence mechanism (filesystem → image) and works on every Runtime.
- **Measuring cold boot and snapshot restore separately (phase 3)** is necessary. Published numbers are snapshot restores: E2B ~80 ms, Blaxel "< 25 ms" from standby, Together 511 ms P95 from hibernation versus 2.7 s cold, Substrate "< 500 ms" resume.

### 6.2 Missing: things nearly every provider has

1. **A filesystem-preserving stop/resume that keeps the Sandbox's identity, with auto-resume.** Eleven of fourteen products have filesystem persistence, and most attach it to the *same* sandbox:
   - Vercel restores automatically on the next call;
   - Sprites sleeps and "wakes with everything where it was";
   - Runloop suspend keeps only the disk;
   - Daytona v0 stop/start;
   - Blaxel archive;
   - E2B `pause({mode:'filesystem'})`.

   Our model has Frozen (memory held), Hibernated (phase 3, Firecracker only) and Capture (a new Template, so a new ID, new ports and new tokens). On Docker, `docker stop` + `docker start` gives exactly this state for nearly no work. Usage would be storage only, and an "idle → stop, request → start" policy reuses the idle-detection logic of ticket 07 and the port proxy of ticket 11. **Recommendation:** add a phase-1 ticket for this. It needs a GLOSSARY decision first: the current *Avoid* lists rule out "stopped" and "suspended" because those words are ambiguous, so give the state a name of its own and state "filesystem kept, processes restart". Once Firecracker exists, phase-3 Hibernated becomes the "with memory" upgrade of the same state.
2. **A thin MCP server** (see §5). Six of fourteen vendors have some MCP offering, and since we ship no Python SDK it is our cheapest path to real agent clients.
3. **Runtime-updatable Egress Policy.** E2B `updateNetwork`, Vercel "switchable at runtime" and Daytona v0 runtime rules (see [prior doc](agent-sandbox-usage.md#3-comparison-table)) all allow it. Agents often need the network only for a setup step, such as `pip install`, and then want it closed. This is a small addition to ticket 12.
4. **stdin / PTY for Processes.** E2B, Daytona, Cloudflare (PTY added before GA), Blaxel (terminal) and Sprites (`sprite console`) offer it. It isn't needed for `claude -p`, but it is what humans and many harnesses expect. It's a small extension of ticket 06 and can be optional.
5. **Usage meters for egress bytes and Template storage** (see §4.3).

### 6.3 Over-scoped or over-weighted

1. **Frozen (ticket 08) as a dependency of Usage (ticket 09).** No vendor offers an in-place freeze as a product state (prior doc §5). It is still a good, cheap learning exercise about the cgroup freezer, so keep the ticket. But **remove 08 from 09's "blocked by"**: Usage can ship with Running only, and Frozen and the new stopped state can be added as further rows.
2. **Volumes (ticket 15), at least in their current form.** Volumes are common but often immature: E2B's are in private beta and Vercel Drives are in public beta. Where they are mature, the market norm is *multi-mount*, as at Northflank, Daytona and Modal. Single-mount Volumes add Quota and Usage dimensions while delivering less than the stopped state would. Move 15 to the end of phase 1, after the stopped-state ticket, and consider read-only multi-mount when it is built (spec §10 already lists this).
3. **Phase 3 is overloaded.** It covers Firecracker, OCI-to-rootfs conversion, vsock, Hibernate, Fork, auto-hibernate and auto-resume, cold-vs-restore measurement and SWE-bench all at once. Split it into **3a**: the Firecracker Runtime with the same test suites plus cold boot vs restore; and **3b**: Hibernate, Fork, auto-hibernate and SWE-bench. Fork is a differentiator held by about five products, so it suits a later sub-phase.

### 6.4 Is gVisor as phase 2 worth it, or go straight to Firecracker?

Evidence: gVisor is a production boundary at Modal (one of its two runtimes, and the only one for GPU), at Google's Agent Platform sandboxes ("gVisor-isolated container"), at Northflank (GPU) and at Substrate. kubernetes-sigs/agent-sandbox names it as a primary RuntimeClass. Firecracker dominates among the startups that market speed: E2B, Vercel, Daytona v2, Cloudflare and Together. gVisor also supports `runsc checkpoint` / `restore`, which saves process and memory state ([gVisor docs](https://gvisor.dev/docs/user_guide/checkpoint_restore/)). One caveat from that page: "Docker does not support restoration into new containers."

**Recommendation: keep gVisor as phase 2, and make it a small phase with one stretch goal.**
- The comparison table is the project's main question, and gVisor is the middle point (a userspace kernel) between a namespace container and a microVM. Skipping it would remove the most instructive data point.
- With Docker it is a different OCI runtime (`--runtime=runsc`), so most of phase 1 carries over unchanged. Its cost is low, and it runs in a plain Linux VM without nested virtualization.
- Stretch goal: use `runsc checkpoint` and restore into the same container to deliver a **first Hibernated on gVisor**. That matches how Google's sandboxes offer "memory state" snapshots, and it gives phase 3 a baseline to compare Firecracker snapshots against.

The isolation-specific trade-offs are another agent's topic; this is only the product-level view.

### 6.5 Proposed revised phase plan

| Phase | Scope (changes in **bold**) | Where |
|---|---|---|
| 1 | Docker Runtime; all current features; **plus a filesystem-preserving stopped state with auto-stop on idle and auto-resume on request; a thin MCP server; a runtime-updatable Egress Policy; egress-bytes and Template-storage Usage meters**; both demo agents; the test harness; `docs/learn/01-docker.md` | Mac |
| 2 | gVisor Runtime; rerun the harness; **stretch: Hibernated via `runsc checkpoint`/`restore`, restored in place**; `docs/learn/02-gvisor.md` | Linux VM or GCP |
| 3a | Firecracker Runtime: rootfs conversion, vsock, same test suites, **cold boot vs snapshot restore measured separately** | GCP n2 (nested virtualization) |
| 3b | **Hibernated and Fork on Firecracker, auto-hibernate on timeout with auto-resume on traffic** (the stopped state upgraded to keep memory), SWE-bench subset, `docs/learn/03-firecracker.md` | Same |
| 4 | GPU, unchanged (only 3 of 14 vendors offer it, so it is correctly last). Note that Modal and Northflank put GPU on gVisor, so phase 2's Runtime is the container-level GPU path. | GCP g2; bare metal |

### 6.6 Phase 1 ticket priority changes

| Change | Ticket | Why (section) |
|---|---|---|
| **New, after 07 and 11:** stopped state that keeps the filesystem + auto-stop on idle + auto-resume on request (`docker stop`/`start`); GLOSSARY entry first | 25 | §6.2.1 |
| **New, after 04, 05, 06 and 11; put it in M1 or right after:** thin MCP server over the public API (stdio + streamable HTTP; 6–8 tools) | 26 | §5, §6.2.2 |
| Remove 08 from 09's blockers | 09 | §6.3.1 |
| Add egress bytes and Template storage meters | 09 | §4.3 |
| Add a runtime update of the Egress Policy | 12 | §6.2.3 |
| Optional: stdin/PTY for Processes | 06 | §6.2.4 |
| Push to the end of phase 1; evaluate read-only multi-mount | 15 | §6.3.2 |
| Lower priority (keep it as a learning ticket) | 08 | §6.3.1 |
| Unchanged and confirmed by the survey | 12, 14, 22, 23 | §6.1 |

**Ranked by importance:** (1) the stopped state with auto-resume; (2) the MCP server; (3) keep phase 2 small, give it a C/R stretch goal, and split phase 3; (4) decouple Usage from Frozen and defer Volumes; (5) the runtime-updatable Egress Policy; (6) the new Usage meters; (7) PTY.

**Things to leave out, even though vendors have them:**
- Building images on the platform (E2B templates, Runloop Blueprints, Together's builder);
- GPU before phase 4;
- Windows and macOS guests;
- BYOC.

These are product features that don't serve the project's question.

---

## 7. Sources (all accessed 2026-11-21)

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
