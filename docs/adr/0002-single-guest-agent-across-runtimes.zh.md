# 所有 Runtime 共用一个 Guest Agent

> [English](0002-single-guest-agent-across-runtimes.md) | 中文

每个 Sandbox 里都运行同一个用 Go 写的 Guest Agent（静态二进制）。执行命令、流式输出、读写文件、代码解释器、端口转发，都由它提供。Runtime 只负责启动 Sandbox，再接通一条到 Guest Agent 的通道：Docker 用 unix socket，Firecracker 用 vsock。所以 Docker Runtime 也不使用 `docker exec`。这样每个功能只写一遍，三种 Runtime 的行为完全一致，性能对比才公平。

**修订（2026-11-21，调研后）。** 在 Mac 的 Docker Desktop 上，macOS 和容器之间共享 unix socket，只在 Docker Desktop 4.86 及以上、并且使用 VirtioFS 时可用，所以这是第一阶段的最低版本。另备一个后备通道 `docker exec -i … --stdio`，放在同一个通道接口后面，用的仍然是同一个 Guest Agent。在 gVisor 下，socket 需要 runsc 的 `--host-uds=create`，绝不用 `all` 或 `open`。见 [Docker/gVisor 调研](../research/phase1-docker-gvisor-on-mac.zh.md)。

## Considered Options

- 每种 Runtime 用自己原生的机制（Docker 用 `docker exec` 和 `docker cp`，Firecracker 另写一套）：Docker 阶段更快能跑起来，但每加一个功能都要实现三遍，而且对比测出来的差异会混进实现上的差异。
