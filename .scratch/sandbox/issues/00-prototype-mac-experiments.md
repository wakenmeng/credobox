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

## Status (2026-11-21)

E1–E7 ran on Docker Desktop 4.87.0; E8 (gVisor in Lima) is deferred to phase 2. The scripts, raw logs and results are on the branch `prototype/mac-experiments` (commit `603dbb0`), in `prototype/mac-experiments/RESULTS.md`.

- Contradicted: the unix socket does not cross the macOS/VM boundary (ADR 0002 amendment), and committing with an explicit config does not strip Env or Entrypoint (ADR 0006 amendment, ticket 22). Both were decided on 2026-11-21: the phase 1 channel is a long-lived `docker exec -i` stream (ADR 0002), and platform environment variables never enter the container config (ADRs 0006, 0008; tickets 01, 13, 22, 23).
- Corrected: spec §7 disk wording.
- Confirmed: isolated internal networks (and that `--internal` alone leaks the VM's listeners), the proxy and resolver design, freeze, the minimal capability set, non-dumpable.
