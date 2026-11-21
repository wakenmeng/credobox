# 02 · Tenants, API keys and admin commands

> English | [中文](02-tenants-and-api-keys.zh.md)

**Blocked by**: 01 · **Milestone**: M1

## What
- Add admin command-line subcommands to `controlplane`: create Tenants, issue and revoke API keys
- Every public API call requires an API key; every Sandbox belongs to exactly one Tenant

## Acceptance
- [ ] A missing, wrong or revoked key returns 401
- [ ] Tenant A can't see or touch Tenant B's Sandboxes (404, not 403)
- [ ] The database stores only key hashes, never plaintext keys
