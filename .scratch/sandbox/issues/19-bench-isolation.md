# 19 · Test harness: isolation (also the curriculum)

> English | [中文](19-bench-isolation.zh.md)

**Blocked by**: 11, 12, 16, 23

## What
Each test is one attack, annotated with the layer that blocks it:
- Resources: fork bomb, memory bomb, filling the disk (soft limit on Docker)
- Reaching out: the Host Agent through `host.docker.internal`, the metadata address, other Sandboxes, a default route or a listening gateway
- The platform itself: calling the Guest Agent as if it were the platform, reading the Guest Agent's token as root
- Escapes: privilege escalation, malicious Templates (WORKDIR symlinked into `/proc/self/fd`, setuid binaries), an Exec working-directory symlink swap, `unshare` plus cgroup `release_agent`, raw sockets, kernel-identity and `/proc` information leaks
- Egress: bypassing the allowlist, DNS tunnelling, using the Egress Proxy to reach the Host Agent, the metadata address or private addresses, reading a Secret's real value

Tools at pinned versions: CDK `evaluate`, deepce enumeration only, amicontained (labelled unmaintained). Exploit modes never run by default. A host configuration check (SMT, KSM, swap, mitigations) is reported as configuration. Docker runs both a "bare" and a "hardened" set.

## Acceptance
- [ ] Prints a table: attack × configuration → blocked / not blocked / known gap, naming the layer that blocked each one
- [ ] Results separate "escaped to the VM" from "escaped to the Host"
- [ ] An unblocked attack is flagged
