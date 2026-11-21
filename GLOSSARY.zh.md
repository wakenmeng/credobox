# Agent Sandbox

> [English](GLOSSARY.md) | 中文

一个给 AI agent 用的代码执行平台：通过 API 创建一次性的隔离环境，在里面执行命令、运行代码、读写文件。agent 可以在外面把 Sandbox 当作工具来用，也可以自己跑在 Sandbox 里面。底层的隔离方式可以切换。

## 核心对象

**Tenant**:
平台的计费和配额主体，持有 API key。每个 Sandbox 都属于唯一一个 Tenant。
_Avoid_: User, account, customer, team

**Sandbox**:
属于某个 Tenant 的隔离执行环境，从某个 Template 创建，有自己的资源规格和生命周期。
_Avoid_: Container, VM, box, environment, instance

**Template**:
Sandbox 的初始环境定义，以一个 OCI 镜像地址注册，是所有 Runtime 共同的唯一来源。分两种：平台内置的公共 Template，和归某个 Tenant 私有的 Template。
_Avoid_: Image, environment, base, snapshot

**Runtime**:
把 Template 变成运行中 Sandbox 的隔离技术，比如 Docker、gVisor、Firecracker。
_Avoid_: Backend, provider, driver, engine

## 系统组成

**Control Plane**:
对外提供 API 的中心组件，负责 Tenant、配额、调度和计量，决定每个 Sandbox 放在哪台 Host 上。
_Avoid_: Server, API server, orchestrator, master

**Host**:
真正运行 Sandbox 的机器，向 Control Plane 上报自己有多少 CPU、内存和 GPU。
_Avoid_: Node, worker, machine, server

**Host Agent**:
运行在每台 Host 上的进程，负责通过 Runtime 管理本机上的 Sandbox。
_Avoid_: Node agent, daemon, worker, kubelet

**Guest Agent**:
运行在每个 Sandbox 内部的进程，是 Sandbox 对外的唯一操作入口：执行命令、读写文件、运行代码、转发端口。
_Avoid_: envd, in-sandbox daemon, sidecar

**Egress Proxy**:
每台 Host 上独立于 Host Agent 的进程，Sandbox 的所有出网流量都经过它。它负责执行 Egress Policy、注入 Secret。它和平台的 DNS 解析器一起，是 Host 侧唯一允许 Sandbox 主动连接的组件。
_Avoid_: Firewall, gateway, NAT, outbound proxy

## 生命周期

**Init**:
Host Agent 给一个正在启动、重新启动或刚恢复的 Sandbox 赋予身份的步骤：新的 token、随机数、时钟、环境变量和它的 CA。Init 完成后，Sandbox 才变成 Running。
_Avoid_: Bootstrap, provisioning, handshake

**Starting**:
Sandbox 正在创建，或者它的 Init 还没完成，不能执行命令，也不计 Usage。
_Avoid_: Creating, pending, booting

**Running**:
Sandbox 正常运行，占用 CPU 和内存。

**Frozen**:
Sandbox 的进程被冻结在原地，内存仍然占用，可以在几毫秒内解冻。在 Docker 上就是 `docker pause` 做的事；在 gVisor 上，冻结发生在 gVisor 自己的内核里。
_Avoid_: Paused, suspended

**Stopped**:
Sandbox 的进程已经结束、内存已经释放，但文件系统和 Sandbox ID 都保留。再次启动时，从这个文件系统重新开机。空闲的 Sandbox 会变成 Stopped，而不是 Terminated。
_Avoid_: Shut down, halted, archived, suspended

**Hibernated**:
Sandbox 的内存和进程状态被保存到磁盘，CPU 和内存都已释放，恢复时从磁盘加载。它和 Stopped 类似，只是内存和进程也保留了下来。E2B 和 Daytona 说的 "pause" 就是这个。
_Avoid_: Paused, snapshotted, suspended, archived

**Fork**:
把一个 Sandbox 的完整状态（内存、进程、文件系统）复制成一个或多个新 Sandbox。只复制文件系统的不算 Fork，那叫 Capture。
_Avoid_: Clone, copy, branch, commit

**Capture**:
把一个 Sandbox 当前的文件系统保存成一个新的私有 Template，不保存内存和进程。
_Avoid_: Snapshot, commit, image capture, checkpoint

**Terminated**:
Sandbox 已经销毁，不能再恢复。原因可能是主动销毁、超时、失败或对账时被清理。它会记下一个终止原因：`deleted`（通过 API 销毁）、`failed`（创建失败，或 Sandbox 自身出错）或 `start_timeout`（在创建超时之内没有进入 Running），外加一段给人看的说明。后续任务会加上各自的原因，比如 `disk_limit`。
_Avoid_: Deleted, dead

## 使用

**Access Token**:
只对某一个 Sandbox 有效的 token，在创建时返回，用来授权直接发给这个 Sandbox 所在 Host 的数据通路请求（Exec、文件、Process、代码解释器）。
_Avoid_: API key, session token

**Exec**:
在 Sandbox 里执行一条前台命令，等它结束或超时，结果可以同步一次性返回，也可以流式返回。
_Avoid_: Run, command, job

**Process**:
在 Sandbox 里通过后台方式启动的长期运行程序，有自己的 ID，可以列出、读取输出、结束，不受单条命令的超时限制。
_Avoid_: Background job, daemon, task

**Volume**:
归某个 Tenant 的持久文件系统，生命周期独立于 Sandbox，同一时间最多挂载在一个 Sandbox 上。
_Avoid_: Disk, storage, persistent filesystem, mount

**Execution Context**:
Sandbox 内代码解释器的一个独立 Python 状态空间，变量可以跨多次调用保留，不同 Execution Context 之间互不影响。
_Avoid_: Kernel, session, notebook

**Exposed Port**:
Sandbox 里的服务端口，通过平台代理以一个专属地址对外提供访问，默认需要 token。
_Avoid_: Port mapping, preview URL, tunnel

**Egress Policy**:
Sandbox 访问外部网络的规则，三选一：禁止、域名白名单、全开。
_Avoid_: Firewall, network policy, internet access

**Allowlist Preset**:
平台维护的一组有名字、可复用的域名，比如 `llm-providers`、`package-registries`、`github`，可以在 Egress Policy 里直接引用。
_Avoid_: Profile, group, essential services

**Secret**:
Tenant 存放在平台上的凭据，绑定了允许接收它的目标域名。在 Sandbox 里它只是一个占位符，请求出网时由 Egress Proxy 换成真实值。
_Avoid_: API key, environment variable, credential, token

## 计量与配额

**Quota**:
Tenant 能使用的资源上限：同时存在的 Sandbox 数、vCPU 总数、内存总量、Volume 总容量、单个 Sandbox 最长存活时间、创建速率。
_Avoid_: Limit, plan, allowance

**Usage**:
一段时间内消耗的资源量，只记资源、不含价格。Sandbox 按状态计算：Running 计 vCPU 秒和内存 GB 秒，Frozen 计内存 GB 秒，Stopped 和 Hibernated 计存储。每个 Sandbox 还计出网字节数；Volume 和 Capture 出来的 Template 按存储量计算。
_Avoid_: Billing, consumption, metering record, cost
