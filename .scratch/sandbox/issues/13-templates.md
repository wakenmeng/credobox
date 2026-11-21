# 13 · Templates

> English | [中文](13-templates.zh.md)

**Blocked by**: 02

## What
- `POST /v1/templates` registers by image reference; Templates are either built-in public ones or private ones owned by a Tenant
- A built-in `python` Template with ipykernel preinstalled
- At registration, the Template record stores the image's Entrypoint, Cmd, Env, User and WorkingDir; the Guest Agent starts the entrypoint from this record (ADR 0008)

## Acceptance
- [ ] An ordinary official image (such as `python:3.12-slim`) works as a Template unmodified (ADR 0003)
- [ ] Tenant A can't use Tenant B's private Templates
- [ ] An image whose entrypoint is a long-running server starts it as usual inside a Sandbox
