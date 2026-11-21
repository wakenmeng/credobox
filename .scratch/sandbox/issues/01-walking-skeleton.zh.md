# 01 · 最小闭环：创建 → Exec → 销毁

> [English](01-walking-skeleton.md) | 中文

**Blocked by**: 00 · **里程碑**: M1

## 做什么
最细的一条端到端链路，但结构从一开始就是最终的样子：
- 控制通路：REST → Control Plane（SQLite）→ 走 unix socket 的 gRPC → Host Agent → Docker Runtime
- 通道：每个 Sandbox 一条长连接的 `docker exec -i` 流，运行 `guestagent bridge`，转接到容器内 Guest Agent 那个只有 root 能访问的 socket（ADR 0002；任务 00 实测）
- Guest Agent 根据 Template 记录启动镜像原始的 Entrypoint 和 Cmd；平台相关的环境变量通过 Init 下发，从不放进容器配置
- Guest Agent 以只读方式挂载进去，作为 PID 1 运行，再启动镜像的 entrypoint（ADR 0003、0008）
- Host Agent 先发 Init（暂时只带 token），Sandbox 才算 Running
- 数据通路：`POST /v1/sandboxes` 返回 Host 的数据入口和 Access Token；`exec` 发给 Host 数据代理，不经过 Control Plane（ADR 0007）
- `DELETE /v1/sandboxes/{id}` → Terminated
- 先只有一个写死的 Tenant，鉴权放到 02

## 验收
- [ ] 创建、执行 `echo hi`、销毁，全程走公共 API
- [ ] 在 Sandbox 里 Guest Agent 是 PID 1，镜像本身不包含它
- [ ] 不带正确 Access Token 的 Exec 会被 Host 数据代理拒绝
- [ ] 创建失败或超时，Sandbox 变成 Terminated，并带上原因
- [ ] 命令返回非 0 退出码时，结果能如实带回来
- [ ] 对 Sandbox 执行 `docker inspect`，配置里看不到任何平台相关的环境变量

## 学什么
Docker 容器到底是什么（namespace 加 cgroups，再加一层文件系统）；PID 1 要负责什么（回收子进程、处理信号）；gRPC 怎么跑在 unix socket 上。
