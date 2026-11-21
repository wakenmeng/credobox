# 06 · Process 和事件流

> [English](06-processes-and-event-stream.md) | 中文

**Blocked by**: 04

## 做什么
- 后台启动一个 Process，立即返回 ID；可以列出 Process，可以发送信号
- 输出写进每个 Process 各自的追加日志，日志有大小上限；stdout 和 stderr 分开计偏移量；超出上限丢掉旧输出时，用 `truncated_before` 标出来；没有客户端在读的时候产生的输出也不会丢
- 退出码一直保留到 Process 被删除或 Sandbox 结束（E2B 只保留 30 秒）
- 每个 Process 都在自己的进程组里；结束时先给整个进程组发 SIGTERM，再发 SIGKILL
- 按 ADR 0005：Host Agent 主动建立一条到 Guest Agent 的长连接，Guest Agent 通过它推送事件；事件有序号，断线重连后可以从断点继续接收

## 验收
- [ ] 起一个 `python -m http.server`，调用立即返回，服务一直在跑
- [ ] 没有客户端在读时写出的输出，之后仍能从偏移量 0 开始读到
- [ ] 结束一个启动了子进程的 Process，不会留下孤儿进程
- [ ] Process 退出一小时后，仍然能读到它的退出码
- [ ] Host Agent 重启后，不会丢 Process 退出事件
- [ ] Sandbox 被 Frozen 时，Process 也一起冻结
