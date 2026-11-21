# 24 · Demo agent (inside the Sandbox)

> English | [中文](24-demo-agent-inside.zh.md)

**Blocked by**: 07, 13, 23

## What
The second way the industry uses sandboxes: run an existing coding agent CLI (such as Claude Code) inside a Sandbox as a Process. Its Template has the CLI preinstalled, its Egress Policy uses the `llm-providers` and `package-registries` presets, and its API key comes from a Secret. Give it the same task as ticket 20 and stream its output back.

## Acceptance
- [ ] The agent fixes the failing test, and its API key is only ever a placeholder inside the Sandbox
- [ ] The agent runs longer than the idle timeout without being reclaimed (ticket 07)
- [ ] Compare with ticket 20: Sandbox count, duration, and what each pattern costs in Usage
- [ ] Run 10 times per Runtime; record the success rate with a confidence interval
- [ ] Classify each failure as environment, ambiguous or agent
