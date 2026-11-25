# 04 · 流式 Exec 和执行参数

> [English](04-exec-streaming-and-options.md) | 中文

**Blocked by**: 01 · **里程碑**: M1

## 做什么
- 请求带 `Accept: text/event-stream` 时，以 SSE 流式返回 stdout 和 stderr，最后一条事件带退出码
- 执行参数：用户（默认 `user`，可指定 root）、工作目录（默认 `/home/user`）、环境变量、超时（默认 60 秒，最多 1 小时）
- 创建 Sandbox 时可以设一组默认环境变量

## 验收
- [ ] 命令运行中就能持续收到输出，不是跑完才一次性返回
- [ ] 流式 Exec 没有输出上限；非流式的一次性返回保留任务 01 的每路 1 MiB 上限和 `*_truncated` 标记
- [ ] 超时后进程被杀，返回结果明确标出"超时"
- [ ] 客户端中途断开，Sandbox 里的命令会被结束，不会留下孤儿进程
- [ ] `whoami` 默认输出 `user`
- [ ] 每条 Exec 都在自己的进程组里；超时或客户端断开时，整个进程组都被杀掉
- [ ] 客户端断开时的行为写进 API 文档

## 学什么
Linux 的用户和 UID；进程组，以及为什么要按进程组整组杀掉。
