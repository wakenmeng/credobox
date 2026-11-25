# 04 · Streaming Exec and Exec options

> English | [中文](04-exec-streaming-and-options.zh.md)

**Blocked by**: 01 · **Milestone**: M1

## What
- With `Accept: text/event-stream`, stream stdout and stderr as SSE; the final event carries the exit code
- Exec options: user (`user` by default, root can be requested), working directory (`/home/user` by default), environment variables, timeout (60 seconds by default, 1 hour at most)
- A set of default environment variables can be given when creating a Sandbox

## Acceptance
- [ ] Output arrives continuously while the command runs, not all at once at the end
- [ ] A streamed Exec has no output cap; the buffered (non-streaming) response keeps its 1 MiB per stream cap and the `*_truncated` flags from ticket 01
- [ ] On timeout the process is killed and the result is clearly marked "timed out"
- [ ] If the client disconnects midway, the command inside the Sandbox is ended, leaving no orphan processes
- [ ] `whoami` prints `user` by default
- [ ] Every Exec runs in its own process group; on timeout or disconnect the whole group is killed
- [ ] The disconnect behaviour is written down in the API docs

## Learn
Linux users and UIDs; process groups, and why you kill the whole group.
