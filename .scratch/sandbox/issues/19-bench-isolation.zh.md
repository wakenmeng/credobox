# 19 · 测试框架：隔离（也是课程表）

> [English](19-bench-isolation.md) | 中文

**Blocked by**: 11, 12, 16, 23

## 做什么
每个测试是一次攻击，并注明挡住它的是哪一层：
- 资源：fork 炸弹、内存炸弹、写满磁盘（Docker 上是软限制）
- 向外连接：通过 `host.docker.internal` 连 Host Agent、访问元数据地址、访问其他 Sandbox、找默认路由或正在监听的网关
- 平台本身：冒充平台调用 Guest Agent、以 root 身份读取 Guest Agent 的 token
- 逃逸：提权、恶意 Template（WORKDIR 软链接到 `/proc/self/fd`、setuid 程序）、替换 Exec 工作目录的软链接、`unshare` 加 cgroup `release_agent`、原始套接字、内核身份和 `/proc` 信息泄露
- 出网：绕过白名单、DNS 隧道、借 Egress Proxy 去连 Host Agent、元数据地址或内网地址、读出 Secret 的真实值

工具都锁定版本：CDK 只跑 `evaluate`，deepce 只做枚举，amicontained（标注为已停止维护）。默认绝不运行利用模式。Host 配置检查（SMT、KSM、swap、漏洞缓解）作为配置报告。Docker 要同时跑"裸配置"和"加固后"两组。

## 验收
- [ ] 输出一张表：攻击 × 配置 → 被挡住 / 没挡住 / 已知缺口，并写明每项是哪一层挡住的
- [ ] 结果分开记录"逃到了虚拟机"和"逃到了 Host"
- [ ] 攻击没被挡住时有警示
