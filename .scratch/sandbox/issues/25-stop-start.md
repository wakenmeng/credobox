# 25 · Stopped: stop and start

> English | [中文](25-stop-start.zh.md)

**Blocked by**: 01

## What
The state that about 11 of 14 surveyed vendors have: processes end and memory is released, but the filesystem and Sandbox ID are kept.
- `POST .../{id}/stop` and `POST .../{id}/start` (on Docker: `docker stop` / `docker start`)
- Starting again boots fresh from the kept filesystem and runs Init again with a fresh token (ADR 0008)
- A data-path request to a Stopped Sandbox starts it automatically
- A Stopped Sandbox is kept for 7 days, then becomes Terminated
- Usage counts storage only while Stopped

## Acceptance
- [ ] Write a file, stop, start: the file is there and the Sandbox ID is unchanged
- [ ] The token after start differs from the token before stop
- [ ] An Exec on a Stopped Sandbox starts it and then runs
- [ ] After 7 days (fake clock), a Stopped Sandbox becomes Terminated
