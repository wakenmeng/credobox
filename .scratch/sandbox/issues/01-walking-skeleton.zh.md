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
- [x] 创建、执行 `echo hi`、销毁，全程走公共 API
- [x] 在 Sandbox 里 Guest Agent 是 PID 1，镜像本身不包含它
- [x] 不带正确 Access Token 的 Exec 会被 Host 数据代理拒绝
- [x] 创建失败或超时，Sandbox 变成 Terminated，并带上原因
- [x] 命令返回非 0 退出码时，结果能如实带回来
- [x] 对 Sandbox 执行 `docker inspect`，配置里看不到任何平台相关的环境变量

## 学什么
Docker 容器到底是什么（namespace 加 cgroups，再加一层文件系统）；PID 1 要负责什么（回收子进程、处理信号）；gRPC 怎么跑在 unix socket 上。

## 状态（2026-11-21）

已在分支 `ticket-01-walking-skeleton` 上完成。运行 `make test`（不需要 Docker）和 `make test-docker`（设置 `SANDBOX_DOCKER_TESTS=1`，对真实的 Docker 运行 `e2e/`）。

| 验收项 | 测试 |
|---|---|
| 全程走公共 API：创建、`echo hi`、销毁 | `e2e` `TestCreateExecDestroy` |
| Guest Agent 是 PID 1，镜像里没有它 | `TestGuestAgentIsPID1AndNotInTheImage`、`TestGuestAgentIsMountedReadOnly` |
| Access Token 不对时被 Host 数据代理拒绝 | `TestExecWithoutTheRightAccessTokenIsRejected`（不带、错的、别的 Sandbox 的） |
| 创建失败或超时 → Terminated 并带原因 | `TestFailedCreateEndsTerminatedWithAReason`（`failed`）、`TestCreateThatTimesOutEndsTerminatedWithAReason`（`start_timeout`，不留下容器） |
| 非 0 退出码如实带回 | `TestNonZeroExitCodeComesBackFaithfully` |
| `docker inspect` 里没有平台环境变量 | `TestContainerConfigHasNoPlatformEnvironment`（容器的 Env 和镜像的完全一样） |

另外还测了：镜像的 Entrypoint 和 Cmd 在 Init 之后启动，是 PID 1 的子进程；PID 1 会回收孤儿进程（`e2e`）。Guest Agent 的 gRPC 服务单独测试（`internal/guestagent`）：Init 之前的 Exec 被拒绝，不带当前 token 的 Exec 被拒绝。上一次 Init 的 token 会被拒绝，重新 Init 必须带上当前的 token，这样 Sandbox 里的代码没法换上自己的 token。

实现时做的决定：
- **网络：暂时用 `--network none`。** `docker exec -i` 通道不需要网络。**任务 12 会把它换成** Sandbox 自己的、网关模式为 `isolated` 的 `--internal` 网络。
- Docker Runtime 调用 `docker` 命令行（不用 Go SDK），每一步都是你可以手动运行的命令。
- Access Token 由 Control Plane 生成，只返回一次，只保存它的 SHA-256 哈希。Host Agent 只在内存里保存这个哈希；Host Agent 重启后就没了，要等对账（任务 10）。
- 终止原因是 `deleted`、`failed` 和 `start_timeout`，各自带一段说明（GLOSSARY、spec §5）。
- 在有 Template 之前（任务 13），Template 记录就是镜像配置，创建时用 `docker image inspect` 读取。镜像的 Entrypoint 和 Cmd 变成 Guest Agent 的参数（`guestagent init -- …`）；Env 和 WorkingDir 来自镜像自己的配置。
- Guest Agent 的 socket 是 `/run/gpubox/guestagent.sock`（0600），放在权限 0700 的 tmpfs 上，`docker commit` 永远不会把它带进去。

留给后续任务：
- 镜像的 USER 还没有生效：entrypoint 和 Exec 都以 root 运行（Exec 默认用 `user` 是任务 04，加固后的用户设置是任务 16）。
- Exec 用 `/bin/sh -c` 运行。后台子进程如果一直占着 stdout，Exec 就不会结束；Exec 被取消时只杀掉它的 shell。进程组放在任务 04。
- macOS 上 unix socket 路径最多 103 字节，所以 `-socket` 要短（默认放在 `$TMPDIR` 下就够短）。
