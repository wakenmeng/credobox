# 03 · Quota and scheduling

> English | [中文](03-quota.zh.md)

**Blocked by**: 02

## What
- Implement 5 Quota dimensions first: concurrent Sandboxes, total vCPUs, total memory, maximum Sandbox lifetime, creation rate (total Volume capacity is added in 15)
- Hosts report capacity when they register; scheduling is first-fit; memory is never overcommitted, and the CPU overcommit ratio is configurable per Host (1.0 by default)
- Requesting a GPU returns an explicit "unsupported"

## Acceptance
- [ ] Going over any dimension is rejected outright, and the error says which one
- [ ] Two creation requests arriving at the same time can't jointly exceed the Quota (concurrency test)
- [ ] Insufficient Host capacity returns "out of capacity", a different error from "Quota exceeded"
