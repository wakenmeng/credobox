# 23 · Secret

> [English](23-secrets.md) | 中文

**Blocked by**: 12

## 做什么
按 ADR 0006：
- `POST /v1/secrets`，带名称、值和**必填**的目标域名；`GET` 永远不返回值
- 创建 Sandbox 时把环境变量映射到 Secret；在 Sandbox 里，这个环境变量的值是占位符
- 对于 Secret 绑定的域名，Egress Proxy 用每个 Sandbox 各自的 CA 终止 TLS，只在请求 header 里把占位符换成真实值
- CA 通过只读挂载和 `SSL_CERT_FILE` 之类的环境变量进入 Sandbox，在 Init 中下发；永远不写进系统信任库
- 换不了就拦截：占位符解析失败时直接拦截请求
- 第一版不改写响应

## 验收
- [ ] 在 Sandbox 里 `printenv`，只能看到占位符
- [ ] 发往绑定域名的请求，到达时带的是真实值（用一个本地测试服务器检查）
- [ ] 把占位符发给任何其他域名，都不会泄露真实值
- [ ] 不带目标域名的 Secret 创建不了
- [ ] 一个 Sandbox 的 CA 不会被另一个 Sandbox 信任，系统信任库没有被改动

## 学什么
TLS 拦截是怎么工作的；信任一个 CA 到底赋予了什么权力；为什么这让 Egress Proxy 成了 Host 上最敏感的组件。
