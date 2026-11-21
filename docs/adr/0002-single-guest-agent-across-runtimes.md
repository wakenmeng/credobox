# One Guest Agent shared by every Runtime

> English | [中文](0002-single-guest-agent-across-runtimes.zh.md)

Every Sandbox runs the same Guest Agent, written in Go and built as a static binary. It provides command execution, streaming output, file reads and writes, the code interpreter, and port forwarding. A Runtime is only responsible for starting the Sandbox and connecting a channel to the Guest Agent: a unix socket on Docker, vsock on Firecracker. So the Docker Runtime does not use `docker exec` either. Each feature is written once, the three Runtimes behave identically, and the performance comparison stays fair.

**Amendment (2026-11-21, after research).** On Docker Desktop for Mac, a unix socket shared between macOS and the container works only on Docker Desktop 4.86 or later with VirtioFS, so that is the phase 1 floor. A fallback transport, `docker exec -i … --stdio`, sits behind the same channel interface; it is still the same Guest Agent. Under gVisor the socket needs runsc's `--host-uds=create`, never `all` or `open`. See [the Docker/gVisor note](../research/phase1-docker-gvisor-on-mac.md).

## Considered Options

- Each Runtime uses its native mechanism (`docker exec` and `docker cp` on Docker, something separate on Firecracker): Docker would run sooner, but every feature would need three implementations, and the comparison numbers would mix in implementation differences.
