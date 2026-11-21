# Research review: what the six reports change

> English | [中文](research-review.zh.md)

A working document. It merges the recommendations of the six research notes from 2026-11-21, removes duplicates, flags conflicts, and lists what we need to decide. Each decision, once made, is folded into the GLOSSARY, spec, ADRs and tickets, and marked **Decided** here.

Reports:
[landscape](../../docs/research/landscape-and-roadmap.md) ·
[Docker/gVisor on Mac](../../docs/research/phase1-docker-gvisor-on-mac.md) ·
[Firecracker](../../docs/research/firecracker-microvm.md) ·
[GPU](../../docs/research/gpu-sandboxing.md) ·
[control plane](../../docs/research/control-plane-and-guest-agent.md) ·
[verification](../../docs/research/verification-and-benchmarks.md) ·
(earlier) [how customers use sandboxes](../../docs/research/agent-sandbox-usage.md)

## 1. Corrections to earlier claims

| Earlier claim | What the sources say | Report |
|---|---|---|
| On AWS, KVM needs `.metal` instances | Nested KVM on ordinary Intel instances (C8i/M8i/R8i) since 2026-02-16, 7th-gen since 2026-06-18 | Firecracker |
| Use `firecracker-go-sdk` | Archived 2026-11-10, as is `firecracker-containerd`; E2B calls the REST API directly | Firecracker |
| Disk limits fail on Docker Desktop "because the VM uses ext4" | The conclusion likely holds, but the reason is the xfs+pquota requirement plus the containerd image store silently ignoring the option | Docker/gVisor |
| Default-deny egress is stricter than everyone | Cloudflare, OpenAI's hosted shell, Google code execution and Substrate also deny by default | landscape |
| Hetzner GEX44 ≈ €180/month | GEX44 is off the price list; GEX45 is €214/month plus €209 setup | GPU |
| Run a SWE-bench Lite subset through our sandbox | The SWE-bench harness has no pluggable backend; Terminal-Bench 2.0 on Harbor does | verification |
| Unix socket between Host Agent and Guest Agent "just works" | Works only on Docker Desktop ≥ 4.86 with VirtioFS; unknown on gRPC FUSE; gVisor needs `--host-uds=create` | Docker/gVisor |

## 2. Where reports agree (stronger evidence)

- **Split phase 3** into smaller steps and **defer auto-resume on traffic** (landscape and Firecracker).
- **An Init step pushed by the Host Agent before user code runs**, carrying the token, env and CA, and repeated after every restore (Firecracker and control plane).
- **Default-deny egress plus secrets as placeholders** is mainstream (landscape and the earlier report).

## 3. Apparent conflicts, resolved

- **Does Daytona count background processes as activity?** The control-plane report read v0.190.0 code (no); the earlier report read the v2 docs (yes, "running commands count as activity"). Both are right for their version; our decision to count running Processes stands.
- **Hibernate in phase 2.** Landscape suggests `runsc checkpoint` on gVisor as a stretch goal; verification says keep Hibernate unsupported on *Docker* because of a November 2026 runc/CRIU CVE. They are about different Runtimes, so they don't conflict.

## 4. Decisions to make (round 6)

| # | Topic | Affects |
|---|---|---|
| D1 | Data path: Exec and file traffic go to the Host directly, not through the Control Plane | new ADR 0007, spec §3–4 |
| D2 | Narrow ADR 0005 to the Sandbox boundary; Control Plane ↔ Host Agent over a unix socket in phase 1, Host-opened tunnel later | ADR 0005, ticket 16 |
| D3 | Guest Agent lifecycle: PID 1, an Init call on every start and restore, non-dumpable | new ADR 0008, ADR 0003, tickets 01/16/23 |
| D4 | Phase 1 network topology on Docker Desktop and a version floor | spec §3/§7, ADR 0002, tickets 01/12 |
| D5 | A new Stopped state (disk and identity kept), auto-stop on idle | GLOSSARY, spec §5–6, new ticket |
| D6 | Capture and Secret CA fixes | ADR 0006, tickets 22/23 |
| D7 | Verification overhaul | spec §8, tickets 18–21/24, new ticket |
| D8 | Roadmap and ticket reordering | spec §9, tickets |
| D9 | Implementation details adopted into tickets | tickets 06/07/09/10/11/16/17, spec §7 |
| D10 | Phase 3 and phase 4 specifics recorded for later, not decided now | spec §9–10 |

**Decided (2026-11-21): D1–D10 were all accepted as recommended.** Where each one landed:

- D1 → [ADR 0007](../../docs/adr/0007-data-path-to-host.md), spec §3–4, ticket 01
- D2 → [ADR 0005](../../docs/adr/0005-connections-only-outside-in.md) amendment, ticket 16
- D3 → [ADR 0008](../../docs/adr/0008-guest-agent-lifecycle.md), [ADR 0003](../../docs/adr/0003-platform-injects-guest-agent.md) amendment, GLOSSARY "Init", tickets 01/16/23
- D4 → [ADR 0002](../../docs/adr/0002-single-guest-agent-across-runtimes.md) amendment, spec §3/§7, tickets 00/12
- D5 → GLOSSARY "Stopped", spec §5–6, tickets 07/09 and new ticket 25
- D6 → [ADR 0006](../../docs/adr/0006-secrets-as-placeholders.md) amendment, tickets 22/23
- D7 → spec §8, tickets 17–20/24 and new ticket 27
- D8 → spec §9, tickets 08/15/21, new tickets 00 and 26
- D9 → spec §5–7, tickets 04/06/07/09/10/11/16
- D10 → spec §10

## 5. Before ticket 01: experiments on the Mac

The Docker/gVisor report's §5 lists experiments E1–E8 that docs cannot settle (unix socket over VirtioFS, reaching 127.0.0.1-only Mac services, whether `--storage-opt size` is silently ignored, and others). Running them first, as a `/prototype`, turns phase 1's foundations from "documented" into "measured".

## 6. Ticket 00 results (measured on this Mac)

Full results: `prototype/mac-experiments/RESULTS.md` on the branch `prototype/mac-experiments`.

| Research claim | Measured on Docker Desktop 4.87.0 | Decision |
|---|---|---|
| Unix sockets cross VirtioFS from 4.86 | **No**, in neither direction | **Decided:** the phase 1 channel is a long-lived `docker exec -i` stream (0.25 ms per round trip) → ADR 0002, spec §3/§7, ticket 01 |
| Committing with an explicit config strips Env and Entrypoint | **No**: Docker merges the config | **Decided:** platform environment variables never enter the container config; Init delivers them; Template records keep the original image config → ADRs 0006/0008, spec §7, tickets 01/13/22/23 |
| `--storage-opt size` is silently ignored | Rejected with an explicit error (classic overlay2 on ext4) | Spec §7 wording corrected; soft limit unchanged |
| `--internal` + `isolated` is needed | Confirmed, and `--internal` alone still exposes the VM's own listeners | No change |
| Freeze, minimal capability set, non-dumpable | Confirmed | No change |
