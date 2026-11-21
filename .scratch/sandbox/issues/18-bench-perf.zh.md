# 18 · 测试框架：性能

> [English](18-bench-perf.md) | 中文

**Blocked by**: 02, 04, 17

## 做什么
`bench/` 下的黑盒客户端，只走公共 API，遵循[验证调研](../../../docs/research/verification-and-benchmarks.zh.md) §9.1 的测量规范：
- 时间段：客户端到第一次 Exec、镜像准备、调度、Runtime 启动、Init 和 Guest Agent 就绪、销毁
- 每个数字都标明缓存状态；测 p99 至少 1000 个串行样本；另跑突发负载和开环负载；给出 bootstrap 置信区间
- 密度：宿主机可用内存的减少量 ÷ Sandbox 数量，用 PSS 和 cgroup 的 `memory.stat` 做归因；物理密度和可调度密度分开报告；关闭 KSM
- Exec 往返延迟和并发创建吞吐

## 验收
- [ ] 一条命令跑完整套性能测试，输出 Markdown 表格
- [ ] 结果里记下运行环境（机器、Host 内核、Runtime 及版本、配置、是否嵌套），保证可以复现
- [ ] 每个报告的数字都写明它对应的时间段定义和缓存状态
