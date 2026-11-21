# 20 · Demo agent (outside the Sandbox)

> English | [中文](20-demo-agent.zh.md)

**Blocked by**: 04, 05 · **Milestone**: M1

## What
A Go agent under `demo/` using the Anthropic Go SDK with tool use, model Claude Sonnet 5.5. The agent loop runs on your machine; the Sandbox is its tool. Tools: exec, read file, write file (add the code interpreter once 14 is done). The task: fix a failing test in a small repository.

## Acceptance
- [ ] The task is completed entirely through the public API, and the test ends up green
- [ ] Record how many Sandboxes the task created, how many Execs it ran, and how long it took
- [ ] Run 10 times per Runtime; record the success rate with a confidence interval
- [ ] Split the time of each tool call into LLM, network and sandbox
- [ ] Classify each failure as environment, ambiguous or agent
