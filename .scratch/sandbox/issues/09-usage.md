# 09 · Usage

> English | [中文](09-usage.zh.md)

**Blocked by**: 02, 10

## What
- Derive Usage intervals from lifecycle events: Running counts vCPU-seconds and memory GB-seconds, Frozen counts memory GB-seconds, Stopped counts storage, Starting counts nothing
- Also meter each Sandbox's egress bytes (reported by the Egress Proxy) and the storage held by captured Templates
- The Host Agent generates each event's ID, so a retried report is counted once
- The Control Plane records each Host's `last_report_at` on its own clock; when a Host is lost, the interval ends there (ADR 0004)
- `GET /v1/usage`: totals by Sandbox, Volume, Template or day; raw intervals can also be queried

## Acceptance
- [ ] Replaying the same event 3 times leaves Usage unchanged
- [ ] With a simulated Host power loss, the interval ends at the last report
- [ ] An interval spanning midnight is split correctly in the per-day totals
- [ ] Stopping a Sandbox switches its Usage from compute to storage at the right moment
