# 04 · 流式 Exec 和执行参数

> [English](04-exec-streaming-and-options.md) | 中文

**Blocked by**: 01 · **里程碑**: M1

## 做什么
- 请求带 `Accept: text/event-stream` 时，以 SSE 流式返回 stdout 和 stderr，最后一条事件带退出码
- 执行参数：用户（默认 `user`，可指定 root）、工作目录（默认 `/home/user`）、环境变量、超时（默认 60 秒，最多 1 小时）
- 创建 Sandbox 时可以设一组默认环境变量

## 验收
- [x] 命令运行中就能持续收到输出，不是跑完才一次性返回
- [x] 流式 Exec 没有输出上限；非流式的一次性返回保留任务 01 的每路 1 MiB 上限和 `*_truncated` 标记
- [x] 超时后进程被杀，返回结果明确标出"超时"
- [x] 客户端中途断开，Sandbox 里的命令会被结束，不会留下孤儿进程
- [x] `whoami` 默认输出 `user`
- [x] 每条 Exec 都在自己的进程组里；超时或客户端断开时，整个进程组都被杀掉
- [x] 客户端断开时的行为写进 API 文档

## 学什么
Linux 的用户和 UID；进程组，以及为什么要按进程组整组杀掉。

## Status（2026-11-26）

在分支 `ticket-04-exec-streaming` 上完成。运行 `make test`（不需要 Docker）和 `make test-docker`（真实 Docker，`e2e/`）。

| 验收项 | 测试 |
|---|---|
| 命令运行中就能收到输出 | `e2e` `TestStreamedExecOutputArrivesWhileTheCommandRuns`；`internal/guestagent` `TestExecOutputArrivesWhileTheCommandRuns` |
| 流式 Exec 没有上限；缓冲结果每路保留 1 MiB 和 `*_truncated` | `e2e` `TestStreamedExecHasNoOutputCap`（3 MiB）、`TestLargeExecOutputIsTruncatedNotAnError`；`internal/guestagent` `TestStreamedExecHasNoOutputCap`、`TestBufferedExecOutputIsCappedAndMarkedTruncated` |
| 超时后进程被杀，结果标出超时 | `e2e` `TestExecTimeoutKillsTheProcessGroupAndIsMarked`（缓冲）、`TestStreamedExecTimeoutIsMarkedInTheLastEvent`；`internal/guestagent` `TestExecTimeoutKillsTheWholeProcessGroupAndSaysSo` |
| 客户端断开后命令被结束，没有孤儿进程 | `e2e` `TestClientDisconnectEndsTheExecWithNoOrphans`；`internal/guestagent` `TestCancelledExecKillsTheWholeProcessGroup` |
| `whoami` 默认输出 `user` | `e2e` `TestExecRunsAsUserByDefault`（alpine 本来没有 `user`；同时检查 `/home/user` 是 `user:750`）、`TestExecAsRootWhenRequested`；`internal/guestagent` `TestAddUserAccount…`（uid 的选择） |
| 每条 Exec 在自己的进程组里，超时或断开时整组被杀 | `e2e` `TestEveryExecRunsInItsOwnProcessGroup`，加上上面的超时和断开测试：它们都会启动后台子进程，并检查没有一个活下来 |
| 断开行为写进 API 文档 | [docs/api.zh.md](../../../docs/api.zh.md) 的“进程组、超时和断开” |

另外测试了：工作目录和环境变量参数，以及 镜像 Env < Sandbox `env` < Exec `env` 的叠加顺序（`TestExecWorkingDirectoryAndEnvironment`、`TestExecEnvironmentLayersImageThenSandboxDefaultsThenExec`）；参数不合法返回 `400`，什么都不运行（`TestBadExecOptionsAreRejected`）；Sandbox 的默认 `env` 能到达 Exec，但不进容器配置（`TestSandboxDefaultEnvironmentReachesExecsButNotTheContainerConfig`）；一个多字节字符不会被拆到两个 SSE 事件里（`TestStreamedMultibyteTextSurvivesChunking`）。

实现过程中做的决定：
- **由平台创建 `user`**（[ADR 0009](../../../docs/adr/0009-platform-creates-the-user-account.zh.md)，已和用户确认）。第一次 Init 时、启动 entrypoint 之前，如果镜像的 `/etc/passwd` 和 `/etc/group` 里没有 `user`，Guest Agent 就追加进去（取从 1000 起第一个空闲 uid），并创建 `/home/user`（0750）。entrypoint 仍然以镜像的 USER 运行；Exec 以 `user` 运行，除非 `user` 字段指定了别的账号。
- **默认工作目录是该账号的 home**：`user` 是 `/home/user`（和 spec 一致），root 是 `/root`，home 不存在时是 `/`。子进程先切换账号再进入目录（Go 的 fork 顺序），所以目录是按该账号自己的权限解析的，而不是按 root。
- **只有一个流式 RPC。** Guest Agent 的 `Exec` 是 server-streaming：`Started`、若干输出块、最后一个 `Exited`。缓冲响应就是 Host data proxy 把同一个流收集起来；它要求 Guest Agent 每路最多发 1 MiB（`max_output_bytes`），所以丢弃仍发生在 Sandbox 里面，无穷无尽的输出不会穿过通道。
- **超时由 Guest Agent 执行**，用 SIGKILL 杀掉整个组（退出码 137，`timed_out: true`）。Host data proxy 自己的截止时间是超时再加 10 秒，只作兜底。如果有离开了进程组的进程还拿着管道，Guest Agent 在杀完 1 秒后停止读取。
- **shell 已退出并且输出已关闭，Exec 才结束**（和 `ssh` 一样）。一个开着 stdout 的后台子进程会让 Exec 一直等到超时；输出被重定向的后台子进程在 Exec 结束后继续运行。只有超时或断开时才整组杀掉。
- **SSE 事件**：`start`（`pid`）、`stdout` / `stderr`（`text`）、`exit`（`exit_code`、`timed_out`）、`error`。输出以 JSON 里的 UTF-8 文本发送；proxy 会把块末尾不完整的多字节字符留到下一块。命令启动前发现的错误照常返回 JSON `400`。
- **Sandbox 的默认 `env`** 存在 Control Plane 的记录里（`default_env`，以后 re-Init 时可以再发一次），通过 CreateSandbox 发给 Host Agent，再通过 Init 发给 Guest Agent，作用于 entrypoint 和每条 Exec。变量名在 Control Plane 和 Host data proxy 里都会检查（`internal/envvars`）。
- 进程内的 Guest Agent 测试把 `user` 映射到运行测试的当前用户，因为切换到其他 uid 需要 root；已经是目标 uid 和 gid 的进程不做切换。setuid 本身由 `e2e` 覆盖。

留给后续任务：
- 主动离开进程组的进程（`setsid`、double-fork 的 daemon）能躲过整组杀。按 cgroup 杀才能抓到它；Process（任务 06）或加固（任务 16）可以再看。
- 流式客户端读得很慢时会产生背压：Guest Agent 阻塞在发送上时，只能靠 Host 的兜底截止时间（超时 + 10 秒）发现超时。
- 任务 16 会去掉 capabilities：Guest Agent 需要 CHOWN（给 `/home/user`）、SETUID 和 SETGID，spec 里的保留列表已经包含它们。
