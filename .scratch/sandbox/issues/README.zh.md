# 第一阶段任务

> [English](README.md) | 中文

按依赖关系，从没有阻塞的任务开始做。每个任务开一个新会话，用 `/tdd`（任务 00 用 `/prototype`）。

**M0（先在 Mac 上实测）**: 00
**M1（让 demo agent 跑起来）**: 00 → 01 → 02、04、05、16 → 20
**第一阶段末尾**: 15（Volume），然后 21

| # | 任务 | Blocked by |
|---|---|---|
| 00 | [原型：Mac 实验 E1–E8](00-prototype-mac-experiments.zh.md) | — |
| 01 | [最小闭环](01-walking-skeleton.zh.md) | 00 |
| 02 | [Tenant 和 API key](02-tenants-and-api-keys.zh.md) | 01 |
| 03 | [Quota 和调度](03-quota.zh.md) | 02 |
| 04 | [流式 Exec 和执行参数](04-exec-streaming-and-options.zh.md) | 01 |
| 05 | [读写文件](05-files.zh.md) | 01 |
| 06 | [Process 和事件流](06-processes-and-event-stream.zh.md) | 04 |
| 07 | [空闲超时](07-idle-timeout-and-lifetime.zh.md) | 06, 25 |
| 08 | [Freeze 和 Unfreeze](08-freeze-unfreeze.zh.md)（低优先级） | 01 |
| 09 | [Usage](09-usage.zh.md) | 02, 10 |
| 10 | [对账](10-reconciliation.zh.md) | 01 |
| 11 | [Exposed Port](11-exposed-ports.zh.md) | 06 |
| 12 | [Egress Policy、Egress Proxy 和 DNS](12-egress-policy.zh.md) | 01 |
| 13 | [Template](13-templates.zh.md) | 02 |
| 14 | [代码解释器](14-code-interpreter.zh.md) | 13 |
| 15 | [Volume](15-volumes.zh.md)（第一阶段末尾） | 03, 09 |
| 16 | [Docker 加固和内部鉴权](16-docker-hardening-and-internal-auth.zh.md) | 01 |
| 17 | [可观测性](17-observability.zh.md) | 01 |
| 18 | [测试框架：性能](18-bench-perf.zh.md) | 02, 04, 17 |
| 19 | [测试框架：隔离](19-bench-isolation.zh.md) | 11, 12, 16, 23 |
| 20 | [demo agent（在外面）](20-demo-agent.zh.md) | 04, 05 |
| 21 | [隔离笔记和对比表](21-learn-note-docker.zh.md) | 18, 19, 20, 24, 26 |
| 22 | [Capture](22-capture.zh.md) | 13 |
| 23 | [Secret](23-secrets.zh.md) | 12 |
| 24 | [demo agent（在里面）](24-demo-agent-inside.zh.md) | 07, 13, 23 |
| 25 | [Stopped：停止和启动](25-stop-start.zh.md) | 01 |
| 26 | [MCP server](26-mcp-server.zh.md) | 04, 05 |

**第二阶段**

| # | 任务 | Blocked by |
|---|---|---|
| 27 | [给 Terminal-Bench 2.0 用的 Harbor 环境](27-harbor-environment.zh.md) | 06, 12, 13 |
