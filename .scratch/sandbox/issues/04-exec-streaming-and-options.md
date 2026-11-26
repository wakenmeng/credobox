# 04 · Streaming Exec and Exec options

> English | [中文](04-exec-streaming-and-options.zh.md)

**Blocked by**: 01 · **Milestone**: M1

## What
- With `Accept: text/event-stream`, stream stdout and stderr as SSE; the final event carries the exit code
- Exec options: user (`user` by default, root can be requested), working directory (`/home/user` by default), environment variables, timeout (60 seconds by default, 1 hour at most)
- A set of default environment variables can be given when creating a Sandbox

## Acceptance
- [x] Output arrives continuously while the command runs, not all at once at the end
- [x] A streamed Exec has no output cap; the buffered (non-streaming) response keeps its 1 MiB per stream cap and the `*_truncated` flags from ticket 01
- [x] On timeout the process is killed and the result is clearly marked "timed out"
- [x] If the client disconnects midway, the command inside the Sandbox is ended, leaving no orphan processes
- [x] `whoami` prints `user` by default
- [x] Every Exec runs in its own process group; on timeout or disconnect the whole group is killed
- [x] The disconnect behaviour is written down in the API docs

## Learn
Linux users and UIDs; process groups, and why you kill the whole group.

## Status (2026-11-26)

Done on branch `ticket-04-exec-streaming`. Run `make test` (no Docker needed) and `make test-docker` (real Docker, `e2e/`).

| Acceptance | Test |
|---|---|
| Output arrives while the command runs | `e2e` `TestStreamedExecOutputArrivesWhileTheCommandRuns`; `internal/guestagent` `TestExecOutputArrivesWhileTheCommandRuns` |
| Streamed Exec has no cap; buffered keeps 1 MiB per stream and `*_truncated` | `e2e` `TestStreamedExecHasNoOutputCap` (3 MiB), `TestLargeBufferedOutputKeepsTheHeadAndTheTail`, `TestBufferedOutputWithinTheCapComesBackWhole`; `internal/guestagent` `TestStreamedExecHasNoOutputCap`, `TestBufferedExecKeepsTheHeadAndTheTailAndDropsTheMiddle`, `TestBufferedExecOutputBetweenHeadAndCapIsSplitWithNothingDropped`, `TestBufferedExecCutsHeadAndTailOnCharacterBoundaries` |
| Timeout kills the process and marks the result | `e2e` `TestExecTimeoutKillsTheProcessGroupAndIsMarked` (buffered), `TestStreamedExecTimeoutIsMarkedInTheLastEvent`; `internal/guestagent` `TestExecTimeoutKillsTheWholeProcessGroupAndSaysSo` |
| Client disconnect ends the command, no orphans | `e2e` `TestClientDisconnectEndsTheExecWithNoOrphans`; `internal/guestagent` `TestCancelledExecKillsTheWholeProcessGroup` |
| `whoami` prints `user` by default | `e2e` `TestExecRunsAsUserByDefault` (alpine has no `user`; also checks `/home/user` is `user:750`), `TestExecAsRootWhenRequested`; `internal/guestagent` `TestAddUserAccount…` (uid choice) |
| Each Exec in its own process group, whole group killed on timeout or disconnect | `e2e` `TestEveryExecRunsInItsOwnProcessGroup`, plus the timeout and disconnect tests above, which each start background children and check none survive |
| Disconnect behaviour in the API docs | [docs/api.md](../../../docs/api.md), "Process group, timeout and disconnect" |

Also tested: working directory and environment options, and the layering image Env < Sandbox `env` < Exec `env` (`TestExecWorkingDirectoryAndEnvironment`, `TestExecEnvironmentLayersImageThenSandboxDefaultsThenExec`); bad options are `400` and run nothing (`TestBadExecOptionsAreRejected`); the Sandbox's default `env` reaches Execs but not the container config (`TestSandboxDefaultEnvironmentReachesExecsButNotTheContainerConfig`); a multi-byte character is never split across SSE events (`TestStreamedMultibyteTextSurvivesChunking`).

Decisions made while building it:
- **The platform creates `user`** ([ADR 0009](../../../docs/adr/0009-platform-creates-the-user-account.md), agreed with the user). At the first Init, before the entrypoint starts, the Guest Agent appends `user` to the image's `/etc/passwd` and `/etc/group` if it isn't there (first free uid from 1000), and creates `/home/user` (0750). The entrypoint still runs as the image's USER; Execs run as `user` unless `user` names another account.
- **The default working directory is the account's home**: `/home/user` for `user` (what the spec says), `/root` for root, `/` if the home doesn't exist. The child enters it after switching accounts (Go's fork order), so a directory is resolved with the account's own permissions, not root's.
- **One streaming RPC.** The Guest Agent's `Exec` is server-streaming: `Started`, output chunks, then one `Exited`. The buffered response is the Host data proxy collecting the same stream; it asks the Guest Agent for a capped stream (`output_head_bytes`, `output_tail_bytes`), so the drop still happens inside the Sandbox and an endless output never crosses the channel.
- **The buffered cap keeps the head and the tail** (changed after review, agreed with the user). Ticket 01 kept the first 1 MiB, but for an agent the end of an output (a compiler error, a test summary, the last traceback frame) matters most. Now a stream over 1 MiB keeps its first 256 KiB in `stdout` and its last 768 KiB in `stdout_tail`, with `stdout_omitted_bytes` for the gap; within 1 MiB everything is in `stdout`. The Guest Agent sends the head as it comes and holds the tail in a buffer of at most twice 768 KiB, sending it in `Exited`; both are cut on UTF-8 boundaries (`internal/utf8cut`, shared with the SSE path). Processes (ticket 06) keep the newest output too, marking the loss with `truncated_before`.
- **The Guest Agent enforces the timeout** and kills the group with SIGKILL (exit code 137, `timed_out: true`). The Host data proxy's own deadline is the timeout plus 10 s, only as a backstop. If something that left the group still holds the pipes, the Guest Agent stops reading 1 s after the kill.
- **An Exec ends when the shell has exited and its output has closed** (the same as `ssh`). So a timed-out Exec reports the shell's own exit code, which is 0 for `server &` with its output not redirected; `timed_out` is the field to trust (`TestExecHeldOpenByABackgroundChildTimesOutWithTheShellsExitCode`). A background child that keeps stdout open keeps the Exec open until the timeout; one with redirected output keeps running after the Exec. The group is killed only on timeout or disconnect.
- **SSE events**: `start` (`pid`), `stdout` / `stderr` (`text`), `exit` (`exit_code`, `timed_out`), `error`. Output is sent as UTF-8 text in JSON; the proxy holds back an incomplete multi-byte character at a chunk's end. Errors found before the command starts are ordinary JSON `400`s.
- **The Sandbox's default `env`** is stored in the Control Plane's record (`default_env`, so a later re-Init can resend it), sent to the Host Agent in CreateSandbox and to the Guest Agent in Init, and applied to the entrypoint and every Exec. It is plain text in the database, so `markTerminated` clears it (changed after review; `TestTerminatedSandboxNoLongerHoldsItsEnvironment` in `internal/controlplane`), and the API docs say credentials belong in Secrets (ticket 23). A record left Starting after a failed write keeps it until reconciliation (ticket 10). Variable names are checked in the Control Plane and the Host data proxy (`internal/envvars`).
- The in-process Guest Agent tests map `user` to whoever runs them, since switching to any other uid needs root; a process already running as the target uid and gid gets no switch. setuid itself is covered by `e2e`.

Left for later tickets:
- A process that leaves the group on purpose (`setsid`, a double-forking daemon) survives the group kill. Killing by cgroup would catch it; Processes (ticket 06) or hardening (ticket 16) can revisit it.
- A very slow streaming client applies backpressure: while the Guest Agent is blocked sending, it notices the timeout only through the Host's backstop deadline (timeout + 10 s).
- Ticket 16 drops capabilities: the Guest Agent needs CHOWN (for `/home/user`), SETUID and SETGID, which the spec's list already keeps.
