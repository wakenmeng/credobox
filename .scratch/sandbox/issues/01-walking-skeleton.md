# 01 · Walking skeleton: create → Exec → destroy

> English | [中文](01-walking-skeleton.zh.md)

**Blocked by**: 00 · **Milestone**: M1

## What
The thinnest end-to-end path, with the final structure in place:
- Control path: REST → Control Plane (SQLite) → gRPC over a unix socket → Host Agent → Docker Runtime
- Channel: one long-lived `docker exec -i` stream per Sandbox running `guestagent bridge`, relayed to the Guest Agent's root-only socket inside the container (ADR 0002; measured in ticket 00)
- The Guest Agent starts the image's original Entrypoint and Cmd from the Template record; platform environment variables arrive in Init, never in the container config
- The Guest Agent is mounted read-only and runs as PID 1, then starts the image's entrypoint (ADR 0003, 0008)
- The Host Agent sends Init (token only, for now) before the Sandbox counts as Running
- Data path: `POST /v1/sandboxes` returns the Host's data endpoint and an Access Token; `exec` goes to the Host data proxy, not the Control Plane (ADR 0007)
- `DELETE /v1/sandboxes/{id}` → Terminated
- A single hard-coded Tenant for now; authentication comes in 02

## Acceptance
- [x] Create, run `echo hi`, destroy, entirely through the public API
- [x] The Guest Agent is PID 1 inside the Sandbox, and the image itself does not contain it
- [x] An Exec without the right Access Token is rejected by the Host data proxy
- [x] When creation fails or times out, the Sandbox becomes Terminated with a reason
- [x] A non-zero exit code comes back faithfully in the result
- [x] `docker inspect` of a Sandbox shows no platform environment variables in its config

## Learn
What a Docker container really is (namespaces plus cgroups, plus a filesystem layer); what PID 1 has to do (reaping, signals); how gRPC runs over a unix socket.

## Status (2026-11-21)

Done on branch `ticket-01-walking-skeleton`. Run `make test` (no Docker needed) and `make test-docker` (sets `SANDBOX_DOCKER_TESTS=1` and runs `e2e/` against real Docker).

| Acceptance | Test |
|---|---|
| Create, `echo hi`, destroy through the public API | `e2e` `TestCreateExecDestroy` |
| Guest Agent is PID 1, not in the image | `TestGuestAgentIsPID1AndNotInTheImage`, `TestGuestAgentIsMountedReadOnly` |
| Wrong Access Token rejected by the Host data proxy | `TestExecWithoutTheRightAccessTokenIsRejected` (none, wrong, another Sandbox's) |
| Failed or timed-out creation → Terminated with a reason | `TestFailedCreateEndsTerminatedWithAReason` (`failed`), `TestCreateThatTimesOutEndsTerminatedWithAReason` (`start_timeout`, no container left) |
| Non-zero exit code comes back faithfully | `TestNonZeroExitCodeComesBackFaithfully` |
| No platform env in `docker inspect` | `TestContainerConfigHasNoPlatformEnvironment` (container Env equals the image's exactly) |

Also tested: the image's Entrypoint and Cmd run after Init, as children of PID 1, and PID 1 reaps orphans (`e2e`). The Guest Agent's gRPC service is tested on its own (`internal/guestagent`): Exec before Init is refused, and Exec without the current token is refused. The token from a previous Init is refused, and a re-Init must carry the current token, so code in the Sandbox can't plant its own.

Decisions made while building it:
- **Network: `--network none` for now.** The `docker exec -i` channel needs no network. **Ticket 12 replaces it** with the Sandbox's own `--internal` network with gateway mode `isolated`.
- The Docker Runtime drives the `docker` CLI (no Go SDK), so every step is a command you can run by hand.
- The Control Plane mints the Access Token, returns it once, and stores only its SHA-256 hash. The Host Agent holds the hash in memory only; after a Host Agent restart it is gone until reconciliation (ticket 10).
- Termination reasons are `deleted`, `failed` and `start_timeout`, each with a detail string (GLOSSARY, spec §5).
- Until Templates exist (ticket 13), the Template record is the image config, read with `docker image inspect` at create time. The image's Entrypoint and Cmd become the Guest Agent's arguments (`guestagent init -- …`); Env and WorkingDir come from the image's own config.
- The Guest Agent's socket is `/run/gpubox/guestagent.sock` (0600) on a mode-0700 tmpfs, which `docker commit` never captures.

Left for later tickets:
- The image's USER is not applied yet: the entrypoint and Execs run as root (Execs as `user` is ticket 04, the hardened user set is ticket 16).
- Exec runs `/bin/sh -c`. A background child that keeps stdout open keeps the Exec open, and a cancelled Exec kills only its shell. Process groups come with ticket 04.
- A unix socket path is limited to 103 bytes on macOS, so keep `-socket` short (the default under `$TMPDIR` is).
