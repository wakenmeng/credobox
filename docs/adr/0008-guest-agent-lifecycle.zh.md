# Guest Agent 的生命周期：PID 1、每次启动和恢复都做 Init、non-dumpable

> [English](0008-guest-agent-lifecycle.md) | 中文

每个 Sandbox 里，Guest Agent 都以 **PID 1** 启动：它回收子进程，在 Firecracker 上挂载伪文件系统，然后启动镜像自己的 entrypoint。在任何用户进程运行之前，以及每次重新启动、恢复和 Fork 之后，Host Agent 都会给它发一次 **Init** 调用，带上新的 token、宿主机提供的随机数、正确的时钟、新的 `boot_id`、环境变量和 CA。Init 完成前，Sandbox 不算 Running。因为每次都换上新的身份，Fork 就不会再让两个 Sandbox 拿着同一个 token、同样的随机数状态或同一个时钟（见 [Firecracker 调研](../research/firecracker-microvm.zh.md)里的克隆隐患）。Guest Agent 还会把自己设为 **non-dumpable**。在去掉 `CAP_SYS_PTRACE` 的前提下，普通用户和用 root 执行的命令都读不到它的内存。spec 里写明：这就是 token 所防范的威胁。

## Considered Options

- 像 E2B 那样，在构建 Template 时往每个镜像里装一个 init 系统（systemd）：init 是大家熟悉的，但要改写镜像，违背"官方镜像不用改"（ADR 0003）。
- 只在创建时给 Guest Agent 一次身份：更简单，但每次快照恢复和 Fork 都会把它原样复制。
