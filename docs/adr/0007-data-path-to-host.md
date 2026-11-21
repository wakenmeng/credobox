# Data-path traffic goes straight to the Host, not through the Control Plane

> English | [中文](0007-data-path-to-host.zh.md)

The API is split into a control path and a data path. Control operations (create, delete, stop, freeze, Quota, Usage, Templates, Secrets) go to the Control Plane. Data operations (Exec, files, Processes, the code interpreter) go directly to a proxy on the Sandbox's Host, which forwards them to the Guest Agent. When a Sandbox is created, the Control Plane returns that Host's data endpoint and an Access Token valid only for this Sandbox. E2B and Daytona both work this way ([control-plane note](../research/control-plane-and-guest-agent.md)): streaming output never makes an extra hop, and the Control Plane does not become a bottleneck. In phase 1 everything runs on one Mac, so the difference is small, but the structure is right from the start.

## Considered Options

- Route Exec through the Control Plane: one endpoint for clients and simpler auth, but every stream crosses the Control Plane, and activity tracking and scaling get harder.
