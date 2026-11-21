# 15 · Volumes

> English | [中文](15-volumes.zh.md)

**Blocked by**: 03, 09 · **Order**: end of phase 1 (most vendors allow multi-mount, and several are still in beta)

## What
- Create, list and delete Volumes; choose a mount directory (such as `/data`) when creating a Sandbox
- A Volume is mounted in at most one Sandbox at a time
- Add "total Volume capacity" to the Quota, and meter storage into Usage

## Acceptance
- [ ] Sandbox A writes a file and is destroyed; Sandbox B mounts the same Volume and reads it
- [ ] Mounting into two Sandboxes at once is rejected
