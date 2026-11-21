# 17 · Observability

> English | [中文](17-observability.zh.md)

**Blocked by**: 01

## What
- The Control Plane and the Host Agent both expose Prometheus metrics: Sandboxes per state, creation time (split into scheduling, Runtime start and Guest Agent ready), Exec latency, failure counts, reconciliation cleanups
- Structured JSON logs
- `GET .../{id}/events` returns lifecycle events and the termination reason

## Acceptance
- [ ] After creating a Sandbox, all three phase timings are visible in the metrics
- [ ] Metrics follow the interval definitions of ticket 18, including a separate image-prepare phase
- [ ] The Host Agent exports density metrics (host available memory, per-Sandbox PSS and `memory.stat`)
