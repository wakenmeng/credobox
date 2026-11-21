# 16 · Docker 加固和内部鉴权

> [English](16-docker-hardening-and-internal-auth.md) | 中文

**Blocked by**: 01 · **里程碑**: M1

## 做什么
- Docker 加固：普通用户；先去掉全部 capabilities，再加回 SETUID、SETGID、CHOWN、DAC_OVERRIDE、FOWNER、KILL（Guest Agent 切换到 `user` 需要它们）；`no-new-privileges`；限制进程数；限制 CPU 和内存；默认 seccomp；不挂 Docker socket；不用特权模式。保留一个"裸配置"开关，只给隔离测试做对照
- Host Agent 的 gRPC 只监听 unix socket（ADR 0005）；Control Plane 用共享 token
- Init 下发一个每次启动都不同的新 token；Guest Agent 只把它放在内存里，并把自己设为 non-dumpable（ADR 0008）
- 磁盘软限制：Host Agent 定期检查每个 Sandbox 可写层的大小，超出就以 `disk_limit` 为原因终止它

## 验收
- [ ] Sandbox 里的普通用户和用 root 执行的命令都读不到 Guest Agent 的 token，直接调用 Guest Agent 会失败
- [ ] 在 Sandbox 里，通过 `host.docker.internal` 连不到 Host Agent
- [ ] Sandbox 里没有任何环境变量或文件包含这个 token
- [ ] 写入超过磁盘限制时，Sandbox 以 `disk_limit` 为原因结束
- [ ] 容器内 Guest Agent 的 socket 只有 root 能访问（0600），普通用户连不上

## 学什么
Linux capabilities、seccomp、`no-new-privileges` 和 non-dumpable 标志分别挡住什么。这是隔离笔记的核心素材。
