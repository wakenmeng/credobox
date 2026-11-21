# 23 · Secrets

> English | [中文](23-secrets.zh.md)

**Blocked by**: 12

## What
Per ADR 0006:
- `POST /v1/secrets` with a name, value and **required** destination hosts; `GET` never returns the value
- At Sandbox creation, map environment variables to Secrets; inside the Sandbox the variable holds a placeholder
- Placeholders and proxy variables are delivered in Init, never in the container config (ADR 0008)
- The Egress Proxy terminates TLS for a Secret's bound hosts using a per-Sandbox CA, and swaps the placeholder for the real value in request headers only
- The CA reaches the Sandbox through a read-only mount and `SSL_CERT_FILE`-style variables, delivered in Init; it is never written into the system trust store
- Fail closed: a placeholder that can't be resolved blocks the request
- No response scrubbing in v1

## Acceptance
- [ ] `printenv` inside the Sandbox shows only the placeholder
- [ ] A request to a bound host arrives with the real value (checked by a local test server)
- [ ] Sending the placeholder to any other host never leaks the real value
- [ ] A Secret without destination hosts can't be created
- [ ] One Sandbox's CA is not trusted by another Sandbox, and the system trust store is unchanged

## Learn
How TLS interception works, what trusting a CA really grants, and why this makes the Egress Proxy the most sensitive component on the Host.
