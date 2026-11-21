# 00 · Prototype: Mac experiments E1–E8

> English | [中文](00-prototype-mac-experiments.zh.md)

**Blocked by**: none · **Milestone**: M0

## What
Use `/prototype` to run experiments E1–E8 from §5 of [the Docker/gVisor note](../../../docs/research/phase1-docker-gvisor-on-mac.md) on this Mac. These are the questions docs can't settle, such as whether a unix socket crosses VirtioFS in both directions, whether a Sandbox can reach a Mac service bound only to 127.0.0.1, whether `--storage-opt size` is silently ignored, and whether an `--internal` + `isolated` network really leaves no route out. Keep the throwaway code on a `prototype/mac-experiments` branch.

## Acceptance
- [ ] Each experiment has a recorded result (command, output, Docker Desktop and Engine versions)
- [ ] Any result that contradicts the spec or an ADR is written back into them, in both languages
- [ ] Ticket 01 states which channel transport it uses, based on the results

## Learn
How Docker Desktop's Linux VM, VirtioFS and its network stack fit together.
