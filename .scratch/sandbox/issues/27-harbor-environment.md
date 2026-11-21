# 27 · Harbor environment for Terminal-Bench 2.0 (phase 2)

> English | [中文](27-harbor-environment.zh.md)

**Blocked by**: 06, 12, 13 · **Phase**: 2, on x86_64 Linux

## What
A Harbor `BaseEnvironment` (passed with `-e module:Class`) built on our public API, so Terminal-Bench 2.0 can run on our Sandboxes. All 89 TB2 tasks are single-container with a prebuilt amd64 image and an oracle solution, so environment parity can be tested without an LLM. Exec calls longer than an hour map to a Process. See §4 of [the verification note](../../../docs/research/verification-and-benchmarks.md).

## Acceptance
- [ ] Oracle sweep on the Docker Runtime as the baseline, with per-task results recorded
- [ ] Nop sweep: every task fails
- [ ] The same sweeps on gVisor produce a per-task diff against the baseline
- [ ] Runs under the `package-registries` and `github` presets first, then fully open
