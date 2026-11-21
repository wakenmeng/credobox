# 26 · MCP server

> English | [中文](26-mcp-server.zh.md)

**Blocked by**: 04, 05

## What
`cmd/mcpserver`: a separate binary that calls only the public API and exposes 6–8 tools (create, exec, read file, write file, start and read a Process, stop, delete). Since there is no Python SDK, this is the cheapest way for existing agents to use the platform. It doubles as the third demo: point an MCP-capable agent at it and give it the same task as ticket 20.

## Acceptance
- [ ] An MCP-capable agent completes the ticket 20 task using only these tools
- [ ] The server holds no state of its own beyond the Tenant's API key and the Sandboxes' Access Tokens
