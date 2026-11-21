# 由平台注入 Guest Agent，不写进 Template

> [English](0003-platform-injects-guest-agent.md) | 中文

创建 Sandbox 时，由平台把 Guest Agent 放进去：Docker 用只读挂载，Firecracker 在把镜像转成 rootfs 时拷进去。Template 里不需要安装它，所以任何现成的 OCI 镜像都能直接注册成 Template，包括 SWE-bench 的几百个官方评测镜像。代价是平台要自己处理注入，也不能假设镜像里有什么：代码解释器依赖 Jupyter kernel，只有预装了 ipykernel 的内置 `python` Template 支持，其他 Template 调用时会明确报错。

**修订（2026-11-21，调研后）。** 为了在 Firecracker 上也做到"官方镜像不用改"，注入的 Guest Agent 作为 PID 1 运行，再启动镜像自己的 entrypoint，并应用镜像的 ENV、USER 和 WORKDIR（[ADR 0008](0008-guest-agent-lifecycle.zh.md)）。E2B 的做法是在构建 Template 时往每个镜像里装 systemd；Daytona 把它的 daemon 作为 PID 1 注入，我们采用 Daytona 的做法。

## Considered Options

- 每个 Template 自己安装 Guest Agent：平台更简单，但现成镜像都要重新构建一遍才能用，兼容性测试就不再是"拿官方镜像直接跑"。
