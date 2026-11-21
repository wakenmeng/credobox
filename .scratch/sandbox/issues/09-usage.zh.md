# 09 · Usage

> [English](09-usage.md) | 中文

**Blocked by**: 02, 10

## 做什么
- 由生命周期事件推算 Usage 时间段：Running 计 vCPU 秒和内存 GB 秒，Frozen 计内存 GB 秒，Stopped 计存储，Starting 不计
- 另外计量每个 Sandbox 的出网字节数（由 Egress Proxy 上报），以及 Capture 出来的 Template 占用的存储
- 事件 ID 由 Host Agent 生成，所以重试的上报只记一次
- Control Plane 按自己的时钟记录每台 Host 的 `last_report_at`；Host 失联时，时间段就截止到这里（ADR 0004）
- `GET /v1/usage`：按 Sandbox、Volume、Template 或日期汇总，也能查原始时间段

## 验收
- [ ] 同一条事件重放 3 次，Usage 不变
- [ ] 模拟 Host 断电，时间段截止到最后一次上报的时刻
- [ ] 跨天的时间段在按日期汇总时被正确切开
- [ ] 停止一个 Sandbox 时，它的 Usage 在正确的时刻从计算切换为存储
