# Host 失联时，Usage 截止到最后一次成功上报

> [English](0004-usage-ends-at-last-host-report.md) | 中文

Usage 只由 Control Plane 根据生命周期事件推算，每条事件都有唯一 ID，重复上报只记一次。如果 Host 失联，Sandbox 没有结束事件，那么它当时所在的那段 Usage 截止到这台 Host 最后一次成功上报的时刻，而不是对账发现它消失的时刻。这样可能少算几秒，但 Tenant 永远不用为平台的故障付费。

## Considered Options

- 截止到对账发现消失的时刻：实现更简单，但会把 Host 失联到被发现之间的时间也算给 Tenant。
