# 26 · MCP server

> [English](26-mcp-server.md) | 中文

**Blocked by**: 04, 05

## 做什么
`cmd/mcpserver`：一个独立程序，只调用公共 API，提供 6 到 8 个工具（创建、exec、读文件、写文件、启动和读取 Process、停止、删除）。因为我们不提供 Python SDK，这是让现有 agent 用上平台最省事的办法。它同时也是第三个 demo：让一个支持 MCP 的 agent 连上它，做和任务 20 一样的任务。

## 验收
- [ ] 一个支持 MCP 的 agent 只用这些工具就完成了任务 20 的任务
- [ ] 除了 Tenant 的 API key 和各个 Sandbox 的 Access Token，server 自己不保存任何状态
