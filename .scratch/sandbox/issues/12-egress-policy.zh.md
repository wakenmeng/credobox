# 12 · Egress Policy、Egress Proxy 和 DNS

> [English](12-egress-policy.md) | 中文

**Blocked by**: 01

## 做什么
- 网络：每个 Sandbox 只接自己的一个 `--internal` 网络，网关模式为 `isolated`（需要 Docker Engine ≥ 28）
- Egress Proxy 和 DNS 解析器作为容器跑在 Docker 的虚拟机里，同时接各个 Sandbox 网络和一个普通网络；它们没有管理接口，Host Agent 通过自己发起的连接或者一个只读挂载的文件给它们下发配置（ADR 0005）
- 三种模式：禁止（默认）、白名单、全开。白名单可以混用域名和 Allowlist Preset（`llm-providers`、`package-registries`、`github`）；`GET /v1/allowlist-presets` 列出所有预设
- 策略在创建时设定，也可以在 Sandbox 运行中修改（`PATCH .../{id}/egress`）
- Egress Proxy 检查 HTTP 的 Host 头和 HTTPS 的 SNI；其他协议一律禁止。它按 Sandbox 上报出网字节数，供 Usage 使用
- DNS 解析器只解析白名单里的域名；其他所有 DNS 流量一律禁止
- 不管哪种模式，都永远禁止访问 `169.254.169.254`、Host 本机（包括 `host.docker.internal`）和内网地址

## 验收
- [ ] Sandbox 里没有默认路由，网络网关上也没有任何服务响应
- [ ] 默认模式下 `curl https://pypi.org` 失败
- [ ] 用 `package-registries` 预设时，`pip install` 能成功，访问 `example.com` 失败
- [ ] 修改运行中 Sandbox 的策略后，不用重启就能生效
- [ ] 全开模式下也连不上元数据地址和 Host 本机
- [ ] 直接连 IP、绕开代理的尝试会失败
- [ ] 解析白名单以外的域名会失败，直接发给 `8.8.8.8` 的 DNS 查询会被拦截
- [ ] 让代理 `CONNECT` 到 Host Agent、内网地址或元数据地址，都会失败

## 学什么
network namespace；Docker 的 internal 网络；为什么只能按域名做白名单；SNI 是什么；DNS 隧道是怎么把数据偷偷带出去的。
