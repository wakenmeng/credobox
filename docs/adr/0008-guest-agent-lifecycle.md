# Guest Agent lifecycle: PID 1, Init on every start and restore, non-dumpable

> English | [中文](0008-guest-agent-lifecycle.zh.md)

The Guest Agent starts as **PID 1** in every Sandbox: it reaps child processes, mounts the pseudo-filesystems on Firecracker, and then starts the image's own entrypoint. Before any user process runs, and again after every restart, restore and Fork, the Host Agent sends it an **Init** call carrying a fresh token, host-provided entropy, the correct clock, a new `boot_id`, environment variables and the CA. A Sandbox is not Running until Init completes. Because every incarnation gets a new identity, a Fork can no longer leave two Sandboxes holding the same token, RNG state or clock (the clone hazards in [the Firecracker note](../research/firecracker-microvm.md)). The Guest Agent also marks itself **non-dumpable**. With `CAP_SYS_PTRACE` dropped, neither the ordinary user nor a root Exec can read its memory, and the spec states that this is the threat the token protects against.

## Considered Options

- Install an init system (systemd) into each image at Template build, as E2B does: a familiar init, but it rewrites images, which breaks "official images work unmodified" (ADR 0003).
- Give the Guest Agent its identity once, at creation: simpler, but every snapshot restore and Fork would duplicate it.
