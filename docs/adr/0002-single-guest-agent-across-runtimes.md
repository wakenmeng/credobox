# One Guest Agent shared by every Runtime

> English | [中文](0002-single-guest-agent-across-runtimes.zh.md)

Every Sandbox runs the same Guest Agent, written in Go and built as a static binary. It provides command execution, streaming output, file reads and writes, the code interpreter, and port forwarding. A Runtime is only responsible for starting the Sandbox and connecting a channel to the Guest Agent: a unix socket on Docker, vsock on Firecracker. So the Docker Runtime does not use `docker exec` either. Each feature is written once, the three Runtimes behave identically, and the performance comparison stays fair.

**Amendment (2026-11-21, after research and ticket 00).** The research suggested that a unix socket shared between macOS and the container works on Docker Desktop 4.86+ with VirtioFS. Measured on 4.87.0, it doesn't, in either direction (ticket 00, E1). So in phase 1 the channel is **one long-lived `docker exec -i` stream per Sandbox**, opened by the Host Agent and running `guestagent bridge`, which relays the stream to the Guest Agent's root-only (0600) unix socket in the container's own filesystem. It measured 0.25 ms per round trip, and nothing listens on any address a Sandbox can reach, which keeps ADR 0005. It is still the same Guest Agent and the same gRPC service; only the transport differs, and on Firecracker it becomes vsock. `docker exec` only carries the channel; it never runs user commands. On a Linux host (phase 2's VM) a bind-mounted unix socket also works; under gVisor it needs runsc's `--host-uds=create`, never `all` or `open`.

## Considered Options

- Each Runtime uses its native mechanism (`docker exec` and `docker cp` on Docker, something separate on Firecracker): Docker would run sooner, but every feature would need three implementations, and the comparison numbers would mix in implementation differences.
