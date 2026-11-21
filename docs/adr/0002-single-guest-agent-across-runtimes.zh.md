# 所有 Runtime 共用一个 Guest Agent

> [English](0002-single-guest-agent-across-runtimes.md) | 中文

每个 Sandbox 里都运行同一个用 Go 写的 Guest Agent（静态二进制）。执行命令、流式输出、读写文件、代码解释器、端口转发，都由它提供。Runtime 只负责启动 Sandbox，再接通一条到 Guest Agent 的通道：Docker 用 unix socket，Firecracker 用 vsock。所以 Docker Runtime 也不使用 `docker exec`。这样每个功能只写一遍，三种 Runtime 的行为完全一致，性能对比才公平。

**修订（2026-11-21，调研和任务 00 之后）。** 调研认为，在 Docker Desktop 4.86 以上并使用 VirtioFS 时，macOS 和容器之间可以共享 unix socket。在 4.87.0 上实测，两个方向都不行（任务 00，E1）。所以第一阶段的通道是**每个 Sandbox 一条长连接的 `docker exec -i` 流**：由 Host Agent 打开，运行 `guestagent bridge`，它把这条流转接到容器自己文件系统里的 Guest Agent unix socket 上，这个 socket 只有 root 能访问（0600）。实测每次往返 0.25 ms，而且 Sandbox 能访问到的地址上没有任何东西在监听，符合 ADR 0005。用的仍然是同一个 Guest Agent、同一个 gRPC 服务，只是传输方式不同；到了 Firecracker 就换成 vsock。`docker exec` 只用来承载通道，绝不用来执行用户命令。在 Linux 主机上（第二阶段的虚拟机），绑定挂载的 unix socket 也能用；在 gVisor 下需要 runsc 的 `--host-uds=create`，绝不用 `all` 或 `open`。

## Considered Options

- 每种 Runtime 用自己原生的机制（Docker 用 `docker exec` 和 `docker cp`，Firecracker 另写一套）：Docker 阶段更快能跑起来，但每加一个功能都要实现三遍，而且对比测出来的差异会混进实现上的差异。
