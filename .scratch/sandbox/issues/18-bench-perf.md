# 18 · Test harness: performance

> English | [中文](18-bench-perf.zh.md)

**Blocked by**: 02, 04, 17

## What
A black-box client under `bench/` that only uses the public API, following the measurement protocol in §9.1 of [the verification note](../../../docs/research/verification-and-benchmarks.md):
- Intervals: client time to first Exec, image prepare, scheduling, Runtime start, Init and Guest Agent ready, destroy
- Every number labelled with its cache state; at least 1,000 serial samples for p99; burst and open-loop runs; bootstrap confidence intervals
- Density: drop in host available memory divided by the number of Sandboxes, with PSS and cgroup `memory.stat` for attribution; physical and schedulable density reported separately; KSM off
- Exec round-trip latency and concurrent creation throughput

## Acceptance
- [ ] One command runs the whole performance suite and prints a Markdown table
- [ ] The results record the environment (machine, host kernel, Runtime and version, configuration, nested or not) so they can be reproduced
- [ ] Every reported number names its interval definition and cache state
