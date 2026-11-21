# Connections are only opened outside-in; events flow over long-lived streams

> English | [中文](0005-connections-only-outside-in.zh.md)

Across a trust boundary, only the more trusted side opens connections: the Control Plane connects to the Host Agent, and the Host Agent connects to the Guest Agent. Nothing inside a Sandbox can open a connection to the Host Agent or the Control Plane. Anything the Guest Agent needs to report on its own initiative (a command finished, a Process exited, a port opened) is pushed back over a long-lived stream that the Host Agent opened. The Guest Agent buffers events, so the Host Agent can reconnect and resume from where it left off. This way the Host Agent has no listening port that untrusted code can reach, which keeps the attack surface minimal.

This is a policy, not a physical limit. Docker Desktop's `host.docker.internal` lets a container reach the host, and Firecracker's vsock also supports guest-initiated connections (whenever the host listens on `<uds_path>_<port>`). So it has to be enforced by the host not listening, and verified by the isolation tests.

**The one deliberate exception: the Egress Proxy and the platform DNS resolver.** Outbound traffic has to go somewhere, so Sandboxes must be able to connect to these two. To keep the exception narrow, both run as processes separate from the Host Agent, expose no management API, and can do nothing except forward traffic their rules allow (or answer DNS for allowlisted names). The isolation tests attack them directly: using the proxy to reach the Host Agent, the metadata address or private addresses, and tunnelling data out through DNS.

**Amendment (2026-11-21, after research): scope.** This rule governs the **Sandbox boundary** only. The Control Plane and the Host Agent are both trusted, so "the more trusted side opens the connection" does not decide which of them dials the other. In phase 1 the Control Plane reaches the Host Agent over a unix socket, so nothing in a Sandbox (for example via `host.docker.internal`) can reach it. When Hosts sit behind NAT (phases 3–4), the Host opens a tunnel to the Control Plane and the same gRPC service runs inside it. We are not switching to Hosts polling for jobs, because creation blocks and polling would slow it down. See [the control-plane note](../research/control-plane-and-guest-agent.md).

## Considered Options

- Let the Guest Agent call back to the Host Agent: notifications are more direct, but the Host Agent must expose a listening port that Sandboxes can reach, open to attack from all untrusted code.
- Code inside a Sandbox that needs the platform (for example, to create a child Sandbox): it calls the public API with the Tenant's API key, like any client on the internet, subject to the Egress Policy, and never uses an internal channel.
