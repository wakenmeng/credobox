# 20 · demo agent（在 Sandbox 外面）

> [English](20-demo-agent.md) | 中文

**Blocked by**: 04, 05 · **里程碑**: M1

## 做什么
`demo/` 下用 Go 写一个 agent，调用 Anthropic Go SDK，用 tool use，模型用 Claude Sonnet 5.5。agent 循环跑在你自己的机器上，Sandbox 是它的工具。工具包括 exec、读文件、写文件（14 完成后再加代码解释器）。任务是在一个小仓库里修复一个失败的测试。

## 验收
- [ ] 从头到尾只通过公共 API 完成任务，最后测试变绿
- [ ] 记下这次任务一共创建了几次 Sandbox、调用了几次 Exec、花了多长时间
- [ ] 在每种 Runtime 上跑 10 次，记录带置信区间的成功率
- [ ] 把每次工具调用的耗时拆成 LLM、网络、sandbox 三部分
- [ ] 把每次失败归类为环境问题、任务含糊或 agent 问题
