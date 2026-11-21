# 07 · Idle timeout and maximum lifetime

> English | [中文](07-idle-timeout-and-lifetime.zh.md)

**Blocked by**: 06, 25

## What
- Activity: API calls, running Execs, running Processes, active streams, and open connections on Exposed Ports (wired in once 11 is done)
- The Host Agent reports `last_activity_at` and the number of active operations; a Control Plane evaluator owns the deadlines
- By default an idle Sandbox becomes **Stopped** after 5 idle minutes (ticket 25), not Terminated; adjustable, but not beyond the Quota's maximum lifetime
- The maximum lifetime terminates a Sandbox whatever its activity

## Acceptance
- [ ] A command running for 10 minutes is not treated as idle meanwhile
- [ ] A Process running for 30 minutes with no API calls is not treated as idle
- [ ] Once every Process has exited and nothing else is active, the Sandbox becomes Stopped after the idle timeout
- [ ] A Process that never exits is still stopped by the maximum lifetime
- [ ] Event reasons distinguish "idle" (→ Stopped) from "max lifetime exceeded" (→ Terminated)
- [ ] Tested with an injectable fake clock, not sleeps
