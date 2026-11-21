# 24 · demo agent（在 Sandbox 里面）

> [English](24-demo-agent-inside.md) | 中文

**Blocked by**: 07, 13, 23

## 做什么
业界的第二种用法：把一个现成的 coding agent CLI（比如 Claude Code）作为 Process 放进 Sandbox 里跑。它的 Template 预装好这个 CLI，Egress Policy 用 `llm-providers` 和 `package-registries` 两个预设，API key 来自 Secret。给它和任务 20 一样的任务，并把它的输出流式传回来。

## 验收
- [ ] agent 修好了失败的测试，而且它的 API key 在 Sandbox 里始终只是占位符
- [ ] agent 运行时间超过空闲超时，也不会被回收（任务 07）
- [ ] 和任务 20 对比：Sandbox 数量、耗时，以及两种用法各自产生多少 Usage
- [ ] 在每种 Runtime 上跑 10 次，记录带置信区间的成功率
- [ ] 把每次失败归类为环境问题、任务含糊或 agent 问题
