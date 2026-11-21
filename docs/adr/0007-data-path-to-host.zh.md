# 数据通路的流量直接到 Host，不经过 Control Plane

> [English](0007-data-path-to-host.md) | 中文

API 分成控制通路和数据通路。控制类操作（创建、删除、停止、冻结、Quota、Usage、Template、Secret）发给 Control Plane。数据类操作（Exec、文件、Process、代码解释器）直接发给 Sandbox 所在 Host 上的代理，再由它转给 Guest Agent。创建 Sandbox 时，Control Plane 返回这台 Host 的数据入口和一个只对该 Sandbox 有效的 Access Token。E2B 和 Daytona 都是这样做的（[控制面调研](../research/control-plane-and-guest-agent.zh.md)）：流式输出不用多绕一圈，Control Plane 也不会成为瓶颈。第一阶段所有组件都在一台 Mac 上，差别不大，但结构从一开始就是对的。

## Considered Options

- 让 Exec 经过 Control Plane：客户端只用对接一个入口，鉴权也更简单；但每条流都要穿过 Control Plane，活动跟踪和扩展都更难。
