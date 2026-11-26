# gpubox

> English | [中文](README.zh.md)

A sandbox platform for AI agents, written in Go. Through an API, an agent creates a disposable, isolated environment and uses it to run commands. The isolation technology underneath can be swapped, so Docker, gVisor and Firecracker can be compared on start time, overhead and isolation strength.

- **Phase 1** (now): Docker Desktop on a Mac.
- **Phase 2**: Firecracker on GCP.
- **Then**: GPUs.

## How it fits together

```
client ──REST :8080──▶ Control Plane (SQLite) ──gRPC, unix socket──▶ Host Agent ──docker──▶ container
   │                                                                     ▲                  └ Guest Agent (PID 1)
   └──────────── Exec, straight to the Host data proxy :8081 ────────────┘
```

- **Control Plane**: the public API and the records (create, get, delete).
- **Host Agent**: one per Host. It runs Sandboxes through a Runtime, and serves the data proxy that Exec goes to (ADR 0007).
- **Guest Agent**: PID 1 inside every Sandbox, mounted in read-only. It is reached through a long-lived `docker exec` channel.

## Quick start

You need Go 1.26, Docker Desktop, and `jq` for the examples. Start each service in its own terminal.

```bash
make build
```

```bash
./bin/hostagent
```

```bash
./bin/controlplane
```

Then create a Sandbox and run a command in it:

```bash
R=$(curl -s -X POST localhost:8080/v1/sandboxes -d '{"template":"alpine:3.20"}'); ID=$(echo "$R" | jq -r .id); TOK=$(echo "$R" | jq -r .access_token)
```

```bash
curl -s -X POST localhost:8081/v1/sandboxes/$ID/exec -H "Authorization: Bearer $TOK" -d '{"command":"echo hi; whoami"}' | jq .
```

```bash
curl -s -X DELETE localhost:8080/v1/sandboxes/$ID | jq .
```

Every field, and streaming with `Accept: text/event-stream`, is in [docs/api.md](docs/api.md).

## Tests

```bash
make test
```

```bash
make test-docker
```

`make test` needs no Docker. `make test-docker` runs `e2e/` against real Docker.

## Layout

| Path | What |
|---|---|
| `cmd/` | The three binaries: `controlplane`, `hostagent`, `guestagent` |
| `internal/` | Their code, and `runtime/docker` |
| `proto/`, `gen/` | gRPC definitions and generated code (`make proto`, needs `buf`) |
| `e2e/` | End-to-end tests against Docker |
| `docs/adr/` | Architecture decisions |
| `docs/research/` | The research behind them |
| `.scratch/sandbox/` | Spec and tickets ([README](.scratch/sandbox/issues/README.md)) |
| `GLOSSARY.md` | The domain terms; the authority on naming |

Every doc comes in English (`X.md`, canonical) and Chinese (`X.zh.md`).

## Status

Phase 1, milestone M1. Done:

- **00**: Mac experiments.
- **01**: create → Exec → destroy.
- **04**: streaming Exec, Exec options and process groups.

Next: 16 (hardening), 05 (files), 02 (tenants and API keys).
