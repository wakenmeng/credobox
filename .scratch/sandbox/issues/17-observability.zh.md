# 17 · 可观测性

> [English](17-observability.md) | 中文

**Blocked by**: 01

## 做什么
- Control Plane 和 Host Agent 都暴露 Prometheus 指标：各状态的 Sandbox 数、创建耗时（按调度、Runtime 启动、Guest Agent 就绪三个阶段拆开）、Exec 延迟、失败次数、对账清理次数
- 日志用结构化 JSON
- `GET .../{id}/events` 查询生命周期事件和终止原因

## 验收
- [ ] 创建一个 Sandbox 后，三个阶段的耗时都能在指标里看到
- [ ] 指标按照任务 18 的时间段定义，包括单独的镜像准备阶段
- [ ] Host Agent 导出密度相关指标（宿主机可用内存、每个 Sandbox 的 PSS 和 `memory.stat`）
