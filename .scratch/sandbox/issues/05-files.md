# 05 · Files

> English | [中文](05-files.zh.md)

**Blocked by**: 01 · **Milestone**: M1

## What
Upload, download and list a directory, all through the Guest Agent, without `docker cp` (ADR 0002).

## Acceptance
- [ ] Binary files round-trip byte for byte
- [ ] Large files are streamed in chunks, never read wholly into memory
- [ ] Out-of-bounds paths (such as `../../`, or symlinks pointing elsewhere) have defined behaviour and are covered by tests
