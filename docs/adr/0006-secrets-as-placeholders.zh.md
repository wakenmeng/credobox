# Secret 是占位符，由 Egress Proxy 换成真实值

> [English](0006-secrets-as-placeholders.md) | 中文

agent 跑在 Sandbox 里时需要凭据，比如 LLM 的 API key，但 Sandbox 里的任何代码都可能读到它。所以 Sandbox 里只放一个占位符（比如 `ANTHROPIC_API_KEY=sbx_secret_…`）。对于 Secret 绑定的目标域名，Egress Proxy 会终止 TLS，在请求 header 里把占位符换成真实值。各大厂商都已经收敛到这种做法（见[调研笔记](../research/agent-sandbox-usage.zh.md)），而且用占位符，现成的 agent CLI 不用改就能用。在各家做法不同的地方，我们选了更严格的一边：

- **必须绑定目标域名。** 没绑定域名的 Secret 不能创建。Daytona v0 允许省略，只好在文档里专门警告。
- **换不了就拦截（fail closed）。** 占位符解析失败时直接拦截请求，而不是去掉 header 照样发出（E2B 是 fail open）。
- **只替换 header**，不替换请求 body。
- **第一版不改写响应。** 如果某个允许的域名把凭据原样返回，Sandbox 会看到它。这一点作为已知限制写进文档。
- **每个 Sandbox 一个 CA**，只被这一个 Sandbox 信任（具体做法见下面的修订），CA 私钥泄露只影响这一个 Sandbox。

**修订（2026-11-21，调研后）：CA 怎么放进去。** 每个 Sandbox 的 CA 不写进系统信任库，因为 `docker commit` 会把它固化进每一个 Capture 出来的 Template。改为通过只读挂载提供，再用 `SSL_CERT_FILE` 之类的环境变量指向它，并在 Init 中下发（[ADR 0008](0008-guest-agent-lifecycle.zh.md)）。任务 00（E6）证明，`docker commit` 会把显式传入的配置和容器自己的配置合并，而不是替换掉它，所以 Capture 没法事后去掉任何东西。改为：占位符、代理环境变量和 CA 路径从一开始就不进入容器配置，而是由 Init 下发给 Guest Agent 启动的进程（ADR 0008）；Capture 出来的 Template，其记录里保存源 Template 原始的 Entrypoint、Cmd、Env、User 和 WorkingDir。

## Considered Options

- 直接用环境变量（大多数厂商的 Claude Code 示例仍然这样做）：最简单，但生成的代码能读到 key，并发送到 Egress Policy 允许的任何地方。
- Sandbox 里什么都不放，按域名规则注入 header（E2B 的 `Secret.fill`）：完全没有可泄露的东西，但有些 CLI 没设这个环境变量就不启动，需要额外处理。
