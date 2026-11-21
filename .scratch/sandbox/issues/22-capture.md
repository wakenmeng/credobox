# 22 · Capture

> English | [中文](22-capture.zh.md)

**Blocked by**: 13

## What
`POST .../{id}/capture` saves a Sandbox's current filesystem as a new private Template owned by the same Tenant (on Docker: `docker commit`, then register the result). Memory and processes are not saved, so this is not a Fork. Every vendor we surveyed has this; it is the industry's main persistence mechanism.

## Acceptance
- [ ] Install a package in Sandbox A, Capture it, create Sandbox B from the new Template: the package is there
- [ ] The Guest Agent binary and the Sandbox's token are not baked into the captured Template (ADR 0003)
- [ ] The captured Template is private to the Tenant
- [ ] The new image's config is rebuilt explicitly from the source Template's image config: no Secret placeholders, proxy variables or Guest Agent entrypoint
- [ ] A Sandbox created from the captured Template does not trust the original Sandbox's CA
