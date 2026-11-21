# 08 · Freeze 和 Unfreeze

> [English](08-freeze-unfreeze.md) | 中文

**Blocked by**: 01 · **优先级**: 低（没有厂商把原地冻结当作产品卖，但它是一节成本很低的课）

## 做什么
- `freeze` 用 `docker pause` 实现（cgroup freezer）；`unfreeze` 解冻
- Frozen 状态有单独的最长时限（默认 1 小时），到期就 Terminated
- 对 Frozen 的 Sandbox 调用 Exec 会返回明确的错误
- `hibernate`、`resume` 和 `fork` 在 Docker 上返回"不支持"

## 验收
- [ ] 一个每秒加 1 的计数器，冻结期间不增长，解冻后接着增长
- [ ] 测出解冻耗时，并记下来
- [ ] 计数器按循环次数计数，所以冻结期间流逝的墙上时间不算数
- [ ] 冻结时间超过 gRPC keepalive 超时后，Host Agent 和 Guest Agent 之间的通道仍然可用，或能干净地恢复
- [ ] 发往 Frozen Sandbox 的 Exposed Port 请求会立即失败

## 学什么
cgroup freezer：进程为什么能被冻住，内存为什么还占着。顺便搞清楚为什么我们的 Frozen 不是 E2B 的 "pause"（那个是我们的 Hibernated）。
