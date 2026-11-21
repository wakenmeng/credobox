# 06 · Processes and the event stream

> English | [中文](06-processes-and-event-stream.zh.md)

**Blocked by**: 04

## What
- Start a Process in the background and return its ID immediately; list Processes; send signals
- Output goes to a capped, append-only log per Process, with separate stdout and stderr offsets and a `truncated_before` marker when the cap drops old output; output produced while no client is attached is never lost
- The exit status is kept until the Process is deleted or the Sandbox ends (E2B keeps it only 30 seconds)
- Every Process runs in its own process group; ending it sends SIGTERM, then SIGKILL, to the whole group
- Per ADR 0005: the Host Agent opens a long-lived stream to the Guest Agent, and the Guest Agent pushes events over it; events carry sequence numbers so a reconnect can resume from where it left off

## Acceptance
- [ ] Start `python -m http.server`; the call returns immediately and the server keeps running
- [ ] Output written while no client was reading can still be read later from offset 0
- [ ] Killing a Process that spawned children leaves no orphans
- [ ] The exit code is still readable an hour after the Process exited
- [ ] No Process exit event is lost when the Host Agent restarts
- [ ] When the Sandbox is Frozen, its Processes freeze with it
