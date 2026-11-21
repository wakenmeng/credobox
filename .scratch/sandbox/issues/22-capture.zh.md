# 22 · Capture

> [English](22-capture.md) | 中文

**Blocked by**: 13

## 做什么
`POST .../{id}/capture` 把一个 Sandbox 当前的文件系统保存成一个新的私有 Template，归同一个 Tenant（在 Docker 上：`docker commit`，再把结果注册成 Template）。不保存内存和进程，所以它不是 Fork。调研过的每一家都有这个功能，它是业界最主要的持久化方式。

## 验收
- [ ] 在 Sandbox A 里装一个包，Capture 后用新 Template 创建 Sandbox B，包还在
- [ ] Guest Agent 二进制和 Sandbox 的 token 不会被固化进 Capture 出来的 Template（ADR 0003）
- [ ] Capture 出来的 Template 归该 Tenant 私有
- [ ] 新镜像的配置根据源 Template 的镜像配置显式重建：不含 Secret 占位符、代理环境变量或 Guest Agent 入口
- [ ] 用 Capture 出来的 Template 创建的 Sandbox，不信任原 Sandbox 的 CA
