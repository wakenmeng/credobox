# 01 · Walking skeleton: create → Exec → destroy

> English | [中文](01-walking-skeleton.zh.md)

**Blocked by**: 00 · **Milestone**: M1

## What
The thinnest end-to-end path, with the final structure in place:
- Control path: REST → Control Plane (SQLite) → gRPC over a unix socket → Host Agent → Docker Runtime
- The Guest Agent is mounted read-only and runs as PID 1, then starts the image's entrypoint (ADR 0003, 0008)
- The Host Agent sends Init (token only, for now) before the Sandbox counts as Running
- Data path: `POST /v1/sandboxes` returns the Host's data endpoint and an Access Token; `exec` goes to the Host data proxy, not the Control Plane (ADR 0007)
- `DELETE /v1/sandboxes/{id}` → Terminated
- A single hard-coded Tenant for now; authentication comes in 02

## Acceptance
- [ ] Create, run `echo hi`, destroy, entirely through the public API
- [ ] The Guest Agent is PID 1 inside the Sandbox, and the image itself does not contain it
- [ ] An Exec without the right Access Token is rejected by the Host data proxy
- [ ] When creation fails or times out, the Sandbox becomes Terminated with a reason
- [ ] A non-zero exit code comes back faithfully in the result

## Learn
What a Docker container really is (namespaces plus cgroups, plus a filesystem layer); what PID 1 has to do (reaping, signals); how gRPC runs over a unix socket.
