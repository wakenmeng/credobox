# 11 · Exposed Port

> [English](11-exposed-ports.md) | 中文

**Blocked by**: 06

## 做什么
- `POST .../{id}/ports/{port}` 返回访问地址（`{port}-{sandbox_id}.localhost`）和 token
- 请求经过 Host Agent 上的端口代理，转进 Sandbox
- 端口上的流量计入活动（接上 07）

## 验收
- [ ] 不带 token 返回 401，带对的 token 能访问 Sandbox 里的 HTTP 服务
- [ ] 支持 WebSocket
- [ ] 容器本身不映射任何宿主机端口
- [ ] token 用常量时间比较，转发前去掉鉴权 header
- [ ] 路由以生命周期 ID 校验，回收再用的地址不会连到新的 Sandbox
- [ ] 访问 Guest Agent 路径的请求一律拒绝
- [ ] 打开着的连接算作活动
