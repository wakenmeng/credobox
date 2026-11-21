# 07 · 空闲超时和最长存活时间

> [English](07-idle-timeout-and-lifetime.md) | 中文

**Blocked by**: 06, 25

## 做什么
- 活动包括：API 调用、正在执行的 Exec、正在运行的 Process、正在进行的流式输出、Exposed Port 上打开的连接（等 11 做完再接上）
- Host Agent 上报 `last_activity_at` 和正在进行的操作数；由 Control Plane 里的判定器掌管截止时间
- 默认空闲 5 分钟后变成 **Stopped**（任务 25），而不是 Terminated；可以调整，但不能超过 Quota 里的最长存活时间
- 不管有没有活动，超过最长存活时间都会终止 Sandbox

## 验收
- [ ] 一条命令跑 10 分钟，期间不会被当成空闲
- [ ] 一个 Process 跑 30 分钟、期间没有任何 API 调用，不会被当成空闲
- [ ] 所有 Process 都退出、也没有其他活动后，过了空闲超时，Sandbox 变成 Stopped
- [ ] 一个永远不退出的 Process，仍然会被最长存活时间结束
- [ ] 事件原因能区分"空闲"（→ Stopped）和"超过最长存活时间"（→ Terminated）
- [ ] 用可注入的假时钟来测，不用 sleep
