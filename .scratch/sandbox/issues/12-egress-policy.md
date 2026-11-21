# 12 · Egress Policy, Egress Proxy and DNS

> English | [中文](12-egress-policy.zh.md)

**Blocked by**: 01

## What
- Network: each Sandbox is attached only to its own `--internal` network with gateway mode `isolated` (Docker Engine ≥ 28)
- The Egress Proxy and the DNS resolver run as containers inside the Docker VM, attached to the Sandbox networks and to one normal network; they have no management API, and the Host Agent configures them over a connection it opens or through a read-only mounted file (ADR 0005)
- Three modes: blocked (default), allowlist, fully open. Allowlists mix domains and Allowlist Presets (`llm-providers`, `package-registries`, `github`); `GET /v1/allowlist-presets` lists them
- The policy is set at creation and can be updated while the Sandbox runs (`PATCH .../{id}/egress`)
- The Egress Proxy checks the Host header for HTTP and the SNI for HTTPS; every other protocol is blocked. It reports egress bytes per Sandbox for Usage
- The DNS resolver answers only allowlisted names; all other DNS traffic is blocked
- In every mode, `169.254.169.254`, the Host itself (including `host.docker.internal`) and private network addresses are always blocked

## Acceptance
- [ ] Inside a Sandbox there is no default route, and nothing answers on the network gateway
- [ ] In the default mode, `curl https://pypi.org` fails
- [ ] With the `package-registries` preset, `pip install` succeeds and `example.com` fails
- [ ] Updating the policy on a running Sandbox takes effect without restarting it
- [ ] Even when fully open, the metadata address and the Host stay unreachable
- [ ] Connecting directly to an IP to bypass the proxy fails
- [ ] Resolving a non-allowlisted name fails, and a DNS query sent straight to `8.8.8.8` is blocked
- [ ] Asking the proxy to `CONNECT` to the Host Agent, a private address or the metadata address fails

## Learn
Network namespaces; Docker's internal networks; why an allowlist can only work by domain; what SNI is; how DNS tunnelling smuggles data out.
