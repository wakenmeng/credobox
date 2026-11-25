# Guest Agent lifecycle: PID 1, Init on every start and restore, non-dumpable

> English | [中文](0008-guest-agent-lifecycle.zh.md)

The Guest Agent starts as **PID 1** in every Sandbox: it reaps child processes, mounts the pseudo-filesystems on Firecracker, and then starts the image's own entrypoint. Before any user process runs, and again after every restart, restore and Fork, the Host Agent sends it an **Init** call carrying a fresh token, host-provided entropy, the correct clock, a new `boot_id`, environment variables and the CA. A Sandbox is not Running until Init completes. Because every incarnation gets a new identity, a Fork can no longer leave two Sandboxes holding the same token, RNG state or clock (the clone hazards in [the Firecracker note](../research/firecracker-microvm.md)). The Guest Agent also marks itself **non-dumpable**. With `CAP_SYS_PTRACE` dropped, neither the ordinary user nor a root Exec can read its memory, and the spec states that this is the threat the token protects against.

**Amendment (2026-11-21, after ticket 00).** The container config holds only the image's own config plus the Guest Agent as entrypoint. Every platform environment variable (Secret placeholders, proxy variables, the CA path) arrives in Init, and the Guest Agent adds it to the processes it starts. The image's original Entrypoint, Cmd, Env, User and WorkingDir come from the Template record, not from the container config. This keeps them out of anything `docker commit` captures (ticket 00, E6).

**Amendment (2026-11-21, after ticket 01 review): the first Init is trust on first use.** The Guest Agent accepts its first Init without a token and adopts the token that Init carries. Every later Init must carry the current token. This is safe only while two things hold: (1) the channel the first Init arrives on can be reached only by the Host Agent (in Docker, a 0600 socket on a 0700 tmpfs, reached through `docker exec --user 0:0`), and (2) no user process runs before the first Init, because the Guest Agent starts the image's entrypoint only after it. Each new Runtime must re-check both. On Firecracker the channel becomes vsock: the guest side of vsock must not be connectable by user code before Init, and a snapshot restore or Fork is a *re*-Init, which needs the current token, never a first Init.

## Considered Options

- Install an init system (systemd) into each image at Template build, as E2B does: a familiar init, but it rewrites images, which breaks "official images work unmodified" (ADR 0003).
- Give the Guest Agent its identity once, at creation: simpler, but every snapshot restore and Fork would duplicate it.
