# Agent Sandbox: Spec

> English | [中文](spec.zh.md)

> This is a living document. When our understanding changes, update it and keep [GLOSSARY.md](../../GLOSSARY.md) and [docs/adr/](../../docs/adr/) in sync. The GLOSSARY is the authority on terms. The research behind the decisions is in [docs/research/](../../docs/research/), summarised in [research-review.md](research-review.md).

## 1. Problem

AI agents need a disposable, isolated computer to run the untrusted code they write. The goal is not utilization and throughput but **isolation, memory and cold-start time**. This project builds such a platform and answers one question: **how do Docker, gVisor and Firecracker differ in start speed, resource overhead and isolation strength, and why?**

This is a learning project. Learning isolation is a formal deliverable, as important as the code.

## 2. Goals and non-goals

**Goals**
- Manage the Sandbox lifecycle through a REST API, and run commands, execute code, read and write files, and expose ports inside a Sandbox.
- Support both ways the industry uses sandboxes: **the agent outside**, using the Sandbox as a tool, and **the agent inside**, running a whole agent (such as a coding agent CLI) in the Sandbox.
- Multiple Tenants with Quotas and Usage, modelled as a multi-tenant service from day one.
- Swappable Runtimes. All of them run the same test suites and produce a comparison table.
- Real agents complete a coding task: one from outside the Sandbox, one from inside, and one through a thin MCP server.

**Non-goals**
- Building images on the platform (Templates are registered by image reference or produced by Capture)
- An admin HTTP API (Control Plane command-line subcommands instead)
- Pricing, invoices, payments
- A Python SDK (the REST API and the MCP server are the interfaces)
- GPU in the first version: only 3 of 14 surveyed vendors offer it, and hard GPU isolation needs bare metal. The API keeps the field and returns "unsupported" when it is requested
- A weakened, filesystem-only Fork (that need is met by Capture and Stopped)
- Volumes written by several Sandboxes at once

## 3. Architecture

```
                        control path                          
Tenant's agent ──REST──▶ Control Plane ──gRPC (unix socket in phase 1)──▶ Host Agent
      │                  (SQLite, source of truth)                          │
      │ data path: Exec, files, Processes, code interpreter                 ├─ Runtime: Docker / gVisor / Firecracker
      └──REST/SSE + Access Token──▶ Host data proxy ─────────────────────── ├─ gRPC over unix socket / vsock ──▶ Guest Agent (PID 1 in every Sandbox)
                                    (also serves Exposed Ports)              

On each Host, and the only things a Sandbox may connect to (ADR 0005):
   Egress Proxy (enforces the Egress Policy, injects Secrets) · DNS resolver (answers only allowlisted names)
   On Docker Desktop both run as containers inside the Docker VM; each Sandbox sits alone on an --internal, isolated network.
```

- Split the Control Plane and the Host Agent: [ADR 0001](../../docs/adr/0001-control-plane-host-agent-split.md)
- One Guest Agent shared by every Runtime (phase 1 needs Docker Desktop ≥ 4.86 with VirtioFS; fallback transport `docker exec -i --stdio`): [ADR 0002](../../docs/adr/0002-single-guest-agent-across-runtimes.md)
- The platform injects the Guest Agent: [ADR 0003](../../docs/adr/0003-platform-injects-guest-agent.md)
- Connections across the Sandbox boundary are only opened outside-in; the Egress Proxy and DNS resolver are the one exception; the Control Plane ↔ Host Agent direction is decided separately: [ADR 0005](../../docs/adr/0005-connections-only-outside-in.md)
- Secrets are placeholders swapped in by the Egress Proxy: [ADR 0006](../../docs/adr/0006-secrets-as-placeholders.md)
- Data-path traffic goes straight to the Host: [ADR 0007](../../docs/adr/0007-data-path-to-host.md)
- Guest Agent lifecycle (PID 1, Init on every start and restore, non-dumpable): [ADR 0008](../../docs/adr/0008-guest-agent-lifecycle.md)
- Code layout: one Go module. Programs: `cmd/controlplane`, `cmd/hostagent`, `cmd/guestagent`, `cmd/egressproxy`, `cmd/dnsresolver`, `cmd/mcpserver`. The test harness lives in `bench/` and the demo agents in `demo/` (Go, using the Anthropic Go SDK, model Claude Sonnet 5.5).

## 4. Public API (draft)

Paths and fields may change during implementation; the set of capabilities is fixed by this table.

**Control path** (Control Plane, authenticated with the Tenant's API key)

| Capability | Endpoints |
|---|---|
| Sandbox | `POST /v1/sandboxes` (blocks until Running, or becomes Terminated on timeout; returns the Host's data endpoint and an Access Token), `GET /v1/sandboxes`, `GET /v1/sandboxes/{id}`, `DELETE /v1/sandboxes/{id}` |
| Lifecycle | `POST .../{id}/stop`, `POST .../{id}/start`, `POST .../{id}/freeze`, `POST .../{id}/unfreeze`; `.../hibernate`, `.../resume` and `.../fork` return "unsupported" until their phase |
| Capture | `POST .../{id}/capture` saves the filesystem as a new private Template |
| Egress | `PATCH .../{id}/egress` updates the Egress Policy of a running Sandbox |
| Events | `GET .../{id}/events`: lifecycle events and the termination reason |
| Exposed Port | `POST .../{id}/ports/{port}` returns an address and a token |
| Template | `POST /v1/templates` (register by image reference), `GET /v1/templates` |
| Volume | `POST /v1/volumes`, `GET`, `DELETE`; mounts are specified when creating a Sandbox |
| Secret | `POST /v1/secrets` (name, value, required destination hosts), `GET` (never returns the value), `DELETE` |
| Allowlist Preset | `GET /v1/allowlist-presets` |
| Usage | `GET /v1/usage?from=&to=&group_by=sandbox|volume|template|day`; raw intervals can also be queried |

**Data path** (the Host's data endpoint, authenticated with the Sandbox's Access Token; ADR 0007)

| Capability | Endpoints |
|---|---|
| Exec | `POST .../exec` returns stdout, stderr and the exit code synchronously; with `Accept: text/event-stream` it streams instead |
| Process | `POST .../processes`, `GET .../processes`, `GET .../processes/{pid}/output?stream=stdout|stderr&offset=`, `DELETE .../processes/{pid}` |
| Files | Upload, download, list a directory |
| Code interpreter | `POST .../contexts`, `POST .../contexts/{ctx}/run`; outputs text, errors and PNG |

A data-path request to a Stopped Sandbox starts it automatically.

**Create parameters**: Template, vCPUs, memory, disk, `gpu` (reserved), idle timeout, default environment variables, Egress Policy (blocked, allowlist or fully open; the allowlist may mix domains and Allowlist Presets), Secrets (environment variable name → Secret; the variable holds a placeholder), Volumes to mount.

**Exec parameters**: command, user (`user` by default, root can be requested), working directory (`/home/user` by default), environment variables, timeout (60 seconds by default, 1 hour at most). Agent runs longer than an hour should use a Process. If the client disconnects, the Exec's process group is killed.

**MCP server**: a separate binary that calls only the public API and exposes 6–8 tools (create, exec, read/write file, start/read Process, stop, delete).

## 5. Lifecycle rules

```
Starting ──Init──▶ Running ⇄ Frozen
                     │  ▲
               idle  ▼  │ start (explicit, or any data-path request)
                   Stopped ──7 days──▶ Terminated
Running ⇄ Hibernated (phase 3b)
Any state ──▶ Terminated (deleted / max lifetime exceeded / Frozen timeout / disk_limit / failed / reconciliation cleanup)
```

- **Init**: before any user process runs, and after every start, restore and Fork, the Host Agent gives the Sandbox a fresh identity (ADR 0008). A Sandbox is Starting until Init completes.
- **Idle**: API calls, running Execs, running Processes, active streams and open connections on Exposed Ports all count as activity. The Host Agent reports `last_activity_at` and the number of active operations; the Control Plane owns the deadlines. By default a Sandbox becomes **Stopped** after 5 idle minutes. The timeout can be changed, but not beyond the Quota's maximum lifetime, which still bounds a forgotten server.
- **Stopped**: keeps the filesystem and Sandbox ID, releases CPU and memory, and is kept for 7 days before becoming Terminated. Restarting it boots fresh and runs Init again. Auto-start on Exposed Port traffic is deferred, because it needs the Control Plane in the request path.
- **Frozen**: does not count toward idle time, but has its own time limit (1 hour by default), after which the Sandbox becomes Terminated. An Exec on a Frozen Sandbox returns an error; it must be unfrozen explicitly first. Exposed Port requests to a Frozen Sandbox fail fast.
- **Reconciliation**: the Control Plane writes Starting before it calls the Host. The Host Agent periodically reports the Sandboxes actually running, each labelled with a lifecycle ID. An unknown container gets a grace period before it is destroyed; anything in the database but missing from the machine is marked failed, and its Usage is closed. If the database read fails, that Host is skipped for the cycle.

## 6. Usage, Quota and scheduling

- The Control Plane derives Usage from lifecycle events. The Host Agent generates each event's ID, so a retried report is counted once.
  - Running counts vCPU-seconds and memory GB-seconds;
  - Frozen counts memory GB-seconds;
  - Stopped and Hibernated count storage;
  - every Sandbox counts egress bytes;
  - Volumes and captured Templates count storage;
  - Starting counts nothing.
- When a Host is lost, the affected intervals end at that Host's last successful report, recorded on the Control Plane's clock: [ADR 0004](../../docs/adr/0004-usage-ends-at-last-host-report.md). No surveyed vendor does this; both bill until they notice.
- The Quota has 6 dimensions: concurrent Sandboxes, total vCPUs, total memory, total Volume capacity, maximum Sandbox lifetime, creation rate. Going over is rejected outright, with no queueing.
- Scheduling: Hosts report their capacity when they register, and scheduling is first-fit. Creates in flight reserve capacity. A Host that cannot fit a request refuses it with a typed error, and the Control Plane tries the next Host. Memory is never overcommitted; the CPU overcommit ratio is configurable per Host and defaults to 1.0.
- SQLite: WAL mode, a single writer goroutine, high-frequency activity kept in memory, and no transaction held open across an RPC.

## 7. Security model

There are three trust zones: the Control Plane (trusted), the Host Agent (trusted), and the inside of a Sandbox (**untrusted**).

- Control Plane ↔ Host Agent: a unix socket and a shared token in phase 1; when Hosts sit behind NAT, a Host-opened tunnel with mTLS (ADR 0005).
- Host Agent ↔ Guest Agent: a fresh random token per incarnation, delivered in Init. The Guest Agent keeps it only in memory and is non-dumpable. With `CAP_SYS_PTRACE` dropped, neither the ordinary user nor a root Exec can read it. That is exactly what the token protects against (ADR 0008).
- Connections across the Sandbox boundary are only opened outside-in. The only exception is the Egress Proxy and the DNS resolver, which have no management API. On Docker Desktop they run as containers in the Docker VM; the Host Agent configures them over a connection it opens, or through a read-only mounted file.
- Network on Docker Desktop: each Sandbox is attached only to its own `--internal` network with gateway mode `isolated` (Docker Engine ≥ 28). The Sandbox has no default route, and nothing answers on the network gateway.
- Docker hardening: an ordinary user; all capabilities dropped, then SETUID, SETGID, CHOWN, DAC_OVERRIDE, FOWNER and KILL added back; `no-new-privileges`; a process limit; CPU and memory limits; the default seccomp profile; no Docker socket mounted; no privileged mode.
- Egress: blocked by default, as at Cloudflare, OpenAI's hosted shell and Google. Allowlists combine domains and Allowlist Presets (`llm-providers`, `package-registries`, `github`) and can be updated while the Sandbox runs. The Egress Proxy checks the Host header for HTTP and the SNI for HTTPS, and only HTTP and HTTPS are allowed. The metadata address `169.254.169.254`, the Host itself and private network addresses are always blocked.
- DNS: a Sandbox can only use the platform's DNS resolver, which answers only for allowlisted names; all other DNS traffic is blocked. This closes DNS tunnelling.
- Secrets: placeholders inside the Sandbox, swapped in by the Egress Proxy, with required destination hosts, fail-closed, headers only, and no response scrubbing in v1. The per-Sandbox CA is provided through a read-only mount and `SSL_CERT_FILE`-style variables, never written into the trust store (ADR 0006).
- Capture rebuilds the image config explicitly from the source Template, so placeholders, proxy variables, the CA and the Guest Agent entrypoint are never baked in.
- Disk: Docker Desktop's default image store silently ignores `--storage-opt size`. As a soft limit, the Host Agent polls each Sandbox's writable-layer size and terminates it with reason `disk_limit`. The isolation report records this honestly as a soft limit, not a hard one.

## 8. Verification

The test harness only uses the public API, as a black-box client. Which Runtime is tested is set by the Host Agent's configuration. The measurement protocol follows §9.1 of [the verification note](../../docs/research/verification-and-benchmarks.md).

- **Performance**:
  - Defined intervals: client time to first Exec, image prepare, scheduling, Runtime start, Init and Guest Agent ready, and from phase 3 kernel boot and snapshot restore (plus restore to first Exec) separately, because vendors' advertised numbers come from snapshot restore. Destroy is measured on its own.
  - Every number is labelled with its cache state. p99 needs at least 1,000 serial samples; also run burst and open-loop loads, and report bootstrap confidence intervals.
  - Record the full environment: machine, host kernel, Runtime and version, configuration, and whether it ran nested.
  - Density: the headline number is the drop in host available memory divided by the number of Sandboxes. Attribution uses PSS and cgroup `memory.stat`. Report physical and schedulable density separately, with KSM off by default. The Host Agent exports these as Prometheus metrics so the harness stays black-box.
- **Isolation**: each test is one attack, paired with the layer that blocks it. This suite doubles as the learning curriculum.
  - Attacks: fork bomb, memory bomb, filling the disk, reaching the Host Agent through `host.docker.internal`, reaching the metadata address, reaching other Sandboxes, calling the Guest Agent as if it were the platform, reading the Guest Agent's token as root, privilege escalation, malicious Templates (WORKDIR symlinked into `/proc/self/fd`, setuid binaries), an Exec working-directory symlink swap, `unshare` plus cgroup `release_agent`, raw sockets, kernel-identity and `/proc` information leaks, bypassing the egress allowlist, DNS tunnelling, using the Egress Proxy to reach the Host Agent, the metadata address or private addresses, reading a Secret's real value from inside the Sandbox, and finding a default route or a listening gateway.
  - Tools at pinned versions: CDK `evaluate`, deepce enumeration only, amicontained (labelled unmaintained). Exploit modes never run by default; reproducing real CVEs is an optional lab in a throwaway VM.
  - Docker runs two sets, "bare" and "hardened". Results separate "escaped to the VM" from "escaped to the Host". A host configuration check (SMT, KSM, swap, mitigations) is reported as configuration, not as an attack.
- **Compatibility** (x86_64 Linux only, never under emulation on the arm64 Mac):
  - Primary: **Terminal-Bench 2.0 through Harbor**, using a `BaseEnvironment` built on our public API. Run an oracle sweep on Docker as the baseline, diff each task on gVisor and Firecracker, and run a nop sweep in which every task must fail. Exec calls longer than an hour map to a Process.
  - Secondary: **SWE-bench Verified Mini (50 instances)**, gold patch and empty patch, through a thin runner, reported as a per-instance confusion matrix against the official Docker harness.
  - Run under the `package-registries` and `github` presets first, then fully open; hardened and bare Docker side by side.
- **Demo agents**: outside the Sandbox, inside the Sandbox (with a Secret for the API key), and through the MCP server. Each runs 10 times per Runtime, recording the success rate with a confidence interval, the time per tool call split into LLM, network and sandbox, and failures classified as environment, ambiguous or agent.
- **Observability**: the Control Plane and the Host Agent both expose Prometheus metrics, and logs are structured JSON.

Deliverables: the comparison table (Runtime × metric) and one `docs/learn/` note per phase, in both languages.

## 9. Phases

| Phase | Scope | Where it runs |
|---|---|---|
| 0 | `/prototype` the Mac experiments E1–E8 from [the Docker/gVisor note](../../docs/research/phase1-docker-gvisor-on-mac.md) §5, so phase 1 rests on measured facts | Mac |
| 1 | Docker Runtime and every feature above (including Stopped, Capture, Secrets, the MCP server and the demo agents), the performance and isolation suites, `docs/learn/01-docker.md` | Mac |
| 2 | gVisor Runtime, kept small; stretch goal: a first Hibernated via `runsc checkpoint`/`restore`. Terminal-Bench 2.0 through Harbor on x86_64. `docs/learn/02-gvisor.md` | A Lima, Colima or OrbStack Linux VM on the Mac; GCP for x86_64 compatibility runs |
| 3a | Firecracker Runtime with cold boot: OCI-to-ext4 conversion, guest kernel, vsock, per-Sandbox network namespace routed to the Egress Proxy, jailer, rerun all suites, SWE-bench Verified Mini | GCP n2 with nested virtualization (every number labelled nested) |
| 3b | Template snapshots and Hibernate/resume on the same Host, with cold boot and snapshot restore measured separately | Same |
| 3c | Fork | Same |
| 4a | Container-level GPU on Docker and gVisor (nvproxy), with T4 or L4 | GCP g2 or AWS g4dn (about $15 in total) |
| 4b | VFIO GPU passthrough with Cloud Hypervisor as a GPU-only fourth Runtime, plus the GPU passthrough design doc | Bare metal (Hetzner GEX45, or an AWS `.metal` instance) |

Phase 1 tickets are in [issues/](issues/README.md). Work them in dependency order, starting with the unblocked ones.

## 10. Open questions and inputs for later phases

Write down questions here as they come up; once they're settled, fold them back into the sections above.

**Open**
- **Read-only Volume mounts in several Sandboxes at once**, for RL and evaluation workloads that share a dataset.
- **Scrubbing Secrets from responses**: v1 doesn't (ADR 0006). Revisit once the agent-inside demo works.
- **Auto-start on Exposed Port traffic, auto-hibernate on timeout and auto-resume on traffic**: deferred, because they need the Control Plane in the request path.
- **The hosted-harness pattern** (Claude Managed Agents, OpenAI Agents API): out of scope for now.
- **stdin and PTY for Processes**: optional.

**Inputs for phase 3** (to decide then, with measurements; see [the Firecracker note](../../docs/research/firecracker-microvm.md))
- Write a thin client over Firecracker's REST API pinned to one version; `firecracker-go-sdk` was archived on 2026-11-10.
- Snapshots restore only onto an identical setup, so Hosts must report a compatibility key (CPU model, host kernel, Firecracker version, snapshot format), and Firecracker upgrades need a policy because they invalidate snapshots.
- Hibernated storage is roughly memory size plus the disk diff; meter both, and size Quotas for the host-side snapshot store.
- One network namespace per Sandbox with the same guest IP and MAC inside each, and nftables allowing only the Egress Proxy and the resolver.
- Use the jailer and Firecracker's production host checklist as the phase 3 isolation curriculum (E2B skips the jailer).
- Guest kernel 6.18 with `CONFIG_VMGENID`, virtio-rng and vsock, vsock loopback off.
- One optional bare-metal calibration run to measure the cost of nesting.
- Decide how Capture produces a Template on Firecracker (an OCI layer, or an internal ext4 Template).
- Ticket 06 gains "no Process exit event lost across Hibernate or Fork", since every snapshot resets vsock connections.

**Inputs for phase 4** (see [the GPU note](../../docs/research/gpu-sandboxing.md))
- Whole GPUs only, with no time-slicing or MPS across tenants; MIG later.
- Cloud Hypervisor as a GPU-only fourth Runtime; ADR 0003 must then cover the guest kernel and NVIDIA driver pair; the host report adds GPU model, UUID, IOMMU group and health.
- GPUs become a seventh Quota dimension; GPU-seconds are metered in Running and Frozen; Hibernate on a GPU Sandbox is unsupported unless CUDA state was checkpointed.
- GPU hosts need reserved hugepages and no balloon or virtio-mem, and requests go only to GPUs verified clean.
- The design doc covers the trust model per sharing mode, host preparation (IOMMU, ACS and IOMMU-group audit, vfio-pci binding, hugepages), the tenant handoff protocol (reset, quarantine, VRAM canary scan, mark free), a start-time budget per phase, why not Firecracker (with a "revisit when VFIO ships" trigger), and a snapshot strategy (GPU-less VM snapshot, GPU hot-plug, in-guest `cuda-checkpoint` restore).
- GPU attacks for the isolation suite: reading the previous Sandbox's VRAM, seeing other tenants through NVML, requesting extra driver capabilities, an `LD_PRELOAD` hook-regression image (the shape of CVE-2025-23266), and DMA escape on 4b. A patching SLA for the NVIDIA driver and Container Toolkit, in CDI mode.
