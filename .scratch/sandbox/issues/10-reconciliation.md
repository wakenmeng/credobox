# 10 · Reconciliation

> English | [中文](10-reconciliation.zh.md)

**Blocked by**: 01

## What
The Host Agent periodically reports the Sandboxes actually running on its Host, and the Control Plane compares that with the database: anything on the machine but not in the database is destroyed; anything in the database but missing from the machine is marked failed.

## Acceptance
- [ ] A Sandbox removed by hand with `docker rm -f` is marked failed within one reconciliation cycle
- [ ] A container started by hand with the platform's label is cleaned up
- [ ] After a Host Agent restart, it takes back the Sandboxes still running instead of killing them by mistake
- [ ] The Control Plane writes Starting before calling the Host, and every container carries a lifecycle ID label
- [ ] A record left in Starting (for example the Host started the Sandbox but writing Running failed) is settled: Terminated with reason `failed` if the Host has no such Sandbox
- [ ] An unknown container gets a grace period before it is destroyed
- [ ] If the database read fails, the Host is skipped for that cycle instead of having its Sandboxes destroyed
