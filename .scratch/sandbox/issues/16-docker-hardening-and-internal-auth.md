# 16 · Docker hardening and internal auth

> English | [中文](16-docker-hardening-and-internal-auth.zh.md)

**Blocked by**: 01 · **Milestone**: M1

## What
- Docker hardening: an ordinary user; all capabilities dropped, then SETUID, SETGID, CHOWN, DAC_OVERRIDE, FOWNER and KILL added back (the Guest Agent needs them to switch to `user`); `no-new-privileges`; a process limit; CPU and memory limits; the default seccomp profile; no Docker socket mounted; no privileged mode. Keep a "bare" switch used only as the isolation tests' control group
- The Host Agent's gRPC listens only on a unix socket (ADR 0005); the Control Plane uses a shared token
- Init delivers a fresh per-incarnation token; the Guest Agent keeps it only in memory and marks itself non-dumpable (ADR 0008)
- Disk soft limit: the Host Agent polls each Sandbox's writable-layer size and terminates it with reason `disk_limit` when it goes over

## Acceptance
- [ ] Neither the ordinary user nor a root Exec inside a Sandbox can read the Guest Agent's token, and calling the Guest Agent directly fails
- [ ] Inside a Sandbox, nothing on `host.docker.internal` reaches the Host Agent
- [ ] The token is in no environment variable and no file in the Sandbox
- [ ] Writing past the disk limit ends the Sandbox with reason `disk_limit`
- [ ] The Guest Agent's socket inside the container is root-only (0600), and the ordinary user can't connect to it

## Learn
What Linux capabilities, seccomp, `no-new-privileges` and the non-dumpable flag each block. This is the core material for the isolation note.
