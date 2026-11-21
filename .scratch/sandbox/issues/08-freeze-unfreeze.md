# 08 · Freeze and unfreeze

> English | [中文](08-freeze-unfreeze.zh.md)

**Blocked by**: 01 · **Priority**: low (no vendor sells an in-place freeze, but it is a cheap lesson)

## What
- `freeze` uses `docker pause` (the cgroup freezer); `unfreeze` resumes
- The Frozen state has its own time limit (1 hour by default), after which the Sandbox becomes Terminated
- An Exec on a Frozen Sandbox returns an explicit error
- `hibernate`, `resume` and `fork` return "unsupported" on Docker

## Acceptance
- [ ] A counter that increments every second doesn't advance while frozen, and continues after unfreeze
- [ ] Unfreeze time is measured and recorded
- [ ] The counter is a per-iteration loop counter, so wall-clock time passing while frozen doesn't count
- [ ] The Host Agent ↔ Guest Agent channel survives, or resumes cleanly after, a freeze longer than the gRPC keepalive timeout
- [ ] Exposed Port requests to a Frozen Sandbox fail fast

## Learn
The cgroup freezer: how processes can be frozen, and why their memory stays allocated. Also why our Frozen is not E2B's "pause" (that one is our Hibernated).
