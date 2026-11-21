# Split the Control Plane and the Host Agent

> English | [中文](0001-control-plane-host-agent-split.zh.md)

From day one the system has two components. The Control Plane owns the API, Tenants, Quotas, scheduling and Usage. A Host Agent runs on every Host, manages that Host's Sandboxes through a Runtime, and reports the Host's resources (CPU, memory, GPU). In phase 1 both components run on one Mac, which looks like over-engineering. But multiple Hosts, GPU inventory and scheduling, and a cloud-agnostic Control Plane all depend on this split, and splitting later would mean a rewrite.

## Considered Options

- A single process (API, scheduling and Runtime together): simpler in phase 1, but scheduling and resource reporting end up tangled with Runtime code, and growing to multiple Hosts would need a major rework.
