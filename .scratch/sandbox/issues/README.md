# Phase 1 tickets

> English | [中文](README.zh.md)

Work them in dependency order, starting with the unblocked ones. Use a fresh session and `/tdd` for each ticket (`/prototype` for 00).

**M0 (measure the Mac first)**: 00
**M1 (get the demo agent running)**: 00 → 01 → 02, 04, 05, 16 → 20
**End of phase 1**: 15 (Volumes), then 21

| # | Ticket | Blocked by |
|---|---|---|
| 00 | [Prototype: Mac experiments E1–E8](00-prototype-mac-experiments.md) | — |
| 01 | [Walking skeleton](01-walking-skeleton.md) | 00 |
| 02 | [Tenants and API keys](02-tenants-and-api-keys.md) | 01 |
| 03 | [Quota and scheduling](03-quota.md) | 02 |
| 04 | [Streaming Exec and options](04-exec-streaming-and-options.md) | 01 |
| 05 | [Files](05-files.md) | 01 |
| 06 | [Processes and the event stream](06-processes-and-event-stream.md) | 04 |
| 07 | [Idle timeout](07-idle-timeout-and-lifetime.md) | 06, 25 |
| 08 | [Freeze and unfreeze](08-freeze-unfreeze.md) (low priority) | 01 |
| 09 | [Usage](09-usage.md) | 02, 10 |
| 10 | [Reconciliation](10-reconciliation.md) | 01 |
| 11 | [Exposed Ports](11-exposed-ports.md) | 06 |
| 12 | [Egress Policy, Egress Proxy and DNS](12-egress-policy.md) | 01 |
| 13 | [Templates](13-templates.md) | 02 |
| 14 | [Code interpreter](14-code-interpreter.md) | 13 |
| 15 | [Volumes](15-volumes.md) (end of phase 1) | 03, 09 |
| 16 | [Docker hardening and internal auth](16-docker-hardening-and-internal-auth.md) | 01 |
| 17 | [Observability](17-observability.md) | 01 |
| 18 | [Test harness: performance](18-bench-perf.md) | 02, 04, 17 |
| 19 | [Test harness: isolation](19-bench-isolation.md) | 11, 12, 16, 23 |
| 20 | [Demo agent (outside)](20-demo-agent.md) | 04, 05 |
| 21 | [Isolation note and comparison table](21-learn-note-docker.md) | 18, 19, 20, 24, 26 |
| 22 | [Capture](22-capture.md) | 13 |
| 23 | [Secrets](23-secrets.md) | 12 |
| 24 | [Demo agent (inside)](24-demo-agent-inside.md) | 07, 13, 23 |
| 25 | [Stopped: stop and start](25-stop-start.md) | 01 |
| 26 | [MCP server](26-mcp-server.md) | 04, 05 |

**Phase 2**

| # | Ticket | Blocked by |
|---|---|---|
| 27 | [Harbor environment for Terminal-Bench 2.0](27-harbor-environment.md) | 06, 12, 13 |
