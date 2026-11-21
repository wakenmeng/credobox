# 拆分 Control Plane 和 Host Agent

> [English](0001-control-plane-host-agent-split.md) | 中文

从第一天起，系统就分成两个组件：Control Plane 负责 API、Tenant、配额、调度和计量；每台 Host 上运行一个 Host Agent，负责通过 Runtime 管理本机的 Sandbox，并上报本机资源（CPU、内存、GPU）。第一阶段两个组件都跑在一台 Mac 上，看起来像是过度设计。但多台 Host、GPU 库存和调度、控制面不绑定云厂商，都依赖这个拆分，事后再拆就是一次重构。

## Considered Options

- 单进程（API、调度、Runtime 放在一起）：第一阶段更简单，但调度和资源上报会和 Runtime 代码耦合在一起，扩展到多台 Host 时要大改。
