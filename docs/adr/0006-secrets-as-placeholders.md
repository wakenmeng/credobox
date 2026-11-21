# Secrets are placeholders swapped in by the Egress Proxy

> English | [中文](0006-secrets-as-placeholders.zh.md)

When an agent runs inside a Sandbox it needs credentials such as an LLM API key, but any code in the Sandbox could read them. So a Sandbox only ever sees a placeholder (for example `ANTHROPIC_API_KEY=sbx_secret_…`). The Egress Proxy terminates TLS for the destinations a Secret is bound to, and replaces the placeholder with the real value in the request headers. Every major vendor has converged on this approach (see [the research note](../research/agent-sandbox-usage.md)), and placeholders let existing agent CLIs work unmodified. Where vendors differ, we chose the stricter option:

- **Destination hosts are required.** A Secret with no bound hosts cannot be created. Daytona v0 allowed omitting them and had to warn about it.
- **Fail closed.** If a placeholder can't be resolved, the request is blocked, not sent without the header (E2B fails open).
- **Headers only**, not the request body.
- **No response scrubbing in v1.** If an allowed host echoes the credential back, the Sandbox sees it; this is documented as a known limitation.
- **One CA per Sandbox**, trusted only by that Sandbox (see the amendment below for how), so a leaked CA key affects only that Sandbox.

**Amendment (2026-11-21, after research): how the CA gets in.** The per-Sandbox CA is not written into the system trust store, because `docker commit` would bake it into every captured Template. It is provided through a read-only mount, with `SSL_CERT_FILE`-style environment variables pointing at it, and delivered in Init ([ADR 0008](0008-guest-agent-lifecycle.md)). Ticket 00 (E6) showed that `docker commit` merges an explicit config with the container's own instead of replacing it, so Capture can't strip anything after the fact. Instead, placeholders, proxy variables and the CA path are never part of the container config: Init delivers them to the processes the Guest Agent starts (ADR 0008), and a captured Template's record keeps the source Template's original Entrypoint, Cmd, Env, User and WorkingDir.

## Considered Options

- Plain environment variables (what most vendors' Claude Code examples still do): simplest, but generated code can read the key and send it anywhere the Egress Policy allows.
- Header injection by per-host rule with nothing inside the Sandbox (E2B's `Secret.fill`): nothing to leak at all, but CLIs that refuse to start without the variable set need extra work.
