# Guest Agent 的生命周期：PID 1、每次启动和恢复都做 Init、non-dumpable

> [English](0008-guest-agent-lifecycle.md) | 中文

每个 Sandbox 里，Guest Agent 都以 **PID 1** 启动：它回收子进程，在 Firecracker 上挂载伪文件系统，然后启动镜像自己的 entrypoint。在任何用户进程运行之前，以及每次重新启动、恢复和 Fork 之后，Host Agent 都会给它发一次 **Init** 调用，带上新的 token、宿主机提供的随机数、正确的时钟、新的 `boot_id`、环境变量和 CA。Init 完成前，Sandbox 不算 Running。因为每次都换上新的身份，Fork 就不会再让两个 Sandbox 拿着同一个 token、同样的随机数状态或同一个时钟（见 [Firecracker 调研](../research/firecracker-microvm.zh.md)里的克隆隐患）。Guest Agent 还会把自己设为 **non-dumpable**。在去掉 `CAP_SYS_PTRACE` 的前提下，普通用户和用 root 执行的命令都读不到它的内存。spec 里写明：这就是 token 所防范的威胁。

**修订（2026-11-21，任务 00 之后）。** 容器配置里只有镜像自己的配置，加上作为入口的 Guest Agent。所有平台相关的环境变量（Secret 占位符、代理环境变量、CA 路径）都在 Init 中下发，由 Guest Agent 加到它启动的进程上。镜像原始的 Entrypoint、Cmd、Env、User 和 WorkingDir 从 Template 记录里读取，而不是从容器配置里读取。这样它们就不会出现在 `docker commit` 捕获的任何内容里（任务 00，E6）。

**修订（2026-11-21，任务 01 审查之后）：第一次 Init 是 trust on first use（首次信任）。** Guest Agent 收到第一次 Init 时不要求 token，直接采用这次 Init 带来的 token。之后的每次 Init 都必须带上当前的 token。这样做安全，只靠两个前提：（1）第一次 Init 走的通道只有 Host Agent 能连上（在 Docker 里是 0700 tmpfs 上的一个 0600 socket，通过 `docker exec --user 0:0` 进入）；（2）第一次 Init 之前没有任何用户进程在运行，因为 Guest Agent 要等 Init 之后才启动镜像的 entrypoint。每换一种 Runtime，都要重新确认这两点。到了 Firecracker，通道换成 vsock：在 Init 之前，用户代码不能连上 vsock 的 guest 端；快照恢复和 Fork 都是*重新* Init，必须带当前的 token，永远不是第一次 Init。

## Considered Options

- 像 E2B 那样，在构建 Template 时往每个镜像里装一个 init 系统（systemd）：init 是大家熟悉的，但要改写镜像，违背"官方镜像不用改"（ADR 0003）。
- 只在创建时给 Guest Agent 一次身份：更简单，但每次快照恢复和 Fork 都会把它原样复制。
