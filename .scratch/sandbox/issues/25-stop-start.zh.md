# 25 · Stopped：停止和启动

> [English](25-stop-start.md) | 中文

**Blocked by**: 01

## 做什么
调研的 14 家里大约有 11 家都有这个状态：进程结束、内存释放，但文件系统和 Sandbox ID 保留。
- `POST .../{id}/stop` 和 `POST .../{id}/start`（在 Docker 上就是 `docker stop` / `docker start`）
- 再次启动时，从保留的文件系统重新开机，并用新的 token 再做一次 Init（ADR 0008）
- 对 Stopped 的 Sandbox 发数据通路请求，会自动启动它
- Stopped 的 Sandbox 保留 7 天，之后变成 Terminated
- Stopped 期间，Usage 只计存储

## 验收
- [ ] 写一个文件，停止，再启动：文件还在，Sandbox ID 不变
- [ ] 启动后的 token 和停止前的不一样
- [ ] 对 Stopped 的 Sandbox 发 Exec，会先启动它再执行
- [ ] 7 天后（用假时钟），Stopped 的 Sandbox 变成 Terminated
