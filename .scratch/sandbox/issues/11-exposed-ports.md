# 11 · Exposed Ports

> English | [中文](11-exposed-ports.zh.md)

**Blocked by**: 06

## What
- `POST .../{id}/ports/{port}` returns an address (`{port}-{sandbox_id}.localhost`) and a token
- Requests go through the port proxy on the Host Agent and into the Sandbox
- Traffic on the port counts as activity (wire into 07)

## Acceptance
- [ ] Without a token the request gets 401; with the right token it reaches the HTTP service in the Sandbox
- [ ] WebSocket works
- [ ] The container itself maps no host ports
- [ ] Tokens are compared in constant time, and the auth header is stripped before forwarding
- [ ] Routes are guarded by the lifecycle ID, so a recycled address never reaches a new Sandbox
- [ ] Requests to Guest Agent paths are refused
- [ ] Open connections count as activity
