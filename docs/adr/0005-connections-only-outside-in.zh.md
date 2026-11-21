# 连接只能由外向内发起，事件走长连接推送

> [English](0005-connections-only-outside-in.md) | 中文

信任边界之间的连接只能由更可信的一方发起：Control Plane 连 Host Agent，Host Agent 连 Guest Agent。Sandbox 里的任何东西都不能主动连 Host Agent 或 Control Plane。Guest Agent 需要主动通知的事，比如命令执行完了、Process 退出了、端口打开了，都通过 Host Agent 建立的长连接流推送回去。Guest Agent 会把事件缓存起来，Host Agent 断线重连后可以从断点继续接收。这样 Host Agent 就没有任何一个监听端口能被不可信代码访问到，攻击面最小。

这条是策略，不是物理限制。Docker Desktop 的 `host.docker.internal` 可以从容器连到宿主机，Firecracker 的 vsock 也支持 guest 主动连接（只要宿主机在 `<uds_path>_<port>` 上监听）。所以必须靠"Host 端不监听"来保证，并用隔离测试来验证。

**唯一的有意例外：Egress Proxy 和平台的 DNS 解析器。** 出网流量总得有个去处，所以 Sandbox 必须能连上这两个组件。为了把这个例外压到最小，它们都是独立于 Host Agent 的进程，没有任何管理接口，除了转发规则允许的流量（或者解析白名单里的域名）什么也做不了。隔离测试会直接攻击它们：借代理去连 Host Agent、元数据地址或内网地址，以及通过 DNS 把数据偷偷发出去。

**修订（2026-11-21，调研后）：适用范围。** 这条规则只管 **Sandbox 边界**。Control Plane 和 Host Agent 都是可信的，"由更可信的一方发起连接"决定不了它们之间谁连谁。第一阶段 Control Plane 通过 unix socket 连 Host Agent，所以 Sandbox 里的任何东西（比如通过 `host.docker.internal`）都连不到它。等 Host 跑到 NAT 后面（第三、四阶段），改为 Host 主动连向 Control Plane 建一条隧道，在里面跑同样的 gRPC 服务。我们不改成 Host 轮询拉取任务，因为创建是阻塞的，轮询会让它变慢。见[控制面调研](../research/control-plane-and-guest-agent.zh.md)。

## Considered Options

- 让 Guest Agent 主动回调 Host Agent：通知更直接，但 Host Agent 必须开一个 Sandbox 能访问到的监听端口，所有不可信代码都能去攻击它。
- Sandbox 里的代码需要调用平台（比如创建子 Sandbox）时：和互联网上的任何客户端一样，带着 Tenant 的 API key 走公网 API，并且受 Egress Policy 约束，不走内部通道。
