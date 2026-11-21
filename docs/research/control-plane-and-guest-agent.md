# Control Plane, Host Agent and Guest Agent: how open-source sandbox platforms structure them

> English | [中文](control-plane-and-guest-agent.zh.md)

> Research date: 2026-11-21. Every link was accessed on that day.
> Versions read:
> - **E2B Runtime** (formerly `e2b-dev/infra`; the Go module path is still `github.com/e2b-dev/infra`): `main` at commit [`89e7715`](https://github.com/e2b-dev/runtime/tree/89e771512f7787d46d3402f7c238ec2b4bd650e4) (2026-11-21).
> - **Daytona**: last open-source release, tag [`v0.190.0`](https://github.com/daytonaio/daytona/tree/v0.190.0) (commit `01c502b`, 2026-06-23), plus the Daytona v2 [architecture page](https://www.daytona.com/docs/architecture.md). v2 itself is closed source, so v2 claims rest on that page alone.
> - **kubernetes-sigs/agent-sandbox**: `main` at commit [`1b7d533`](https://github.com/kubernetes-sigs/agent-sandbox/tree/1b7d5336539b9acd44ec431a61f4e83700a0a392) (2026-11-21; latest tag v1.0.12).
> - **SWE-ReX**: `main` at commit [`5c995c3`](https://github.com/SWE-agent/SWE-ReX/tree/5c995c365dfb1fd5bc56fda688be5d8538f9931f) (latest tag v1.4.0).
>
> Primary sources only: source code and the vendors' own architecture docs. OpenHands and microsandbox were not studied in this pass (OpenHands moved its runtime out of the main repo and there was no time to follow it); they are left out rather than described from memory.
> Anything marked **[Inference]** is my conclusion, not what a source says. Terms follow the [GLOSSARY](../../GLOSSARY.md); vendors' own names are in code formatting. User-facing semantics (pricing, timeouts as documented, Secret UX) are in [agent-sandbox-usage.md](agent-sandbox-usage.md) and not repeated.

---

## 1. Summary

- **E2B pushes; Daytona has moved to pull.** E2B's API dials each orchestrator over gRPC and blocks on `Create`, which itself blocks on envd's `/init`. For remote (BYOC) clusters it still pushes, through a single edge endpoint per cluster. Daytona's last open-source runner already defaults to `API_VERSION=2`: it long-polls the API for jobs (Postgres rows, with Redis `LPUSH`/`BRPOP` as the wake-up) and reports results back. Daytona v2 says "Runners accept no inbound connections", and its create returns before the sandbox is ready. **[Inference]** For us the direction between Control Plane and Host Agent is not a trust-boundary question, because both are trusted. Keep push in phase 1, but move the Host Agent's listener onto a unix socket so no Sandbox can reach it, and add a Host-initiated reverse tunnel when the first Host sits behind NAT.
- **In both vendors, data-plane traffic bypasses the control-plane API.** SDK → edge proxy → node → envd/toolbox. In our spec every Exec goes through the Control Plane. **[Inference]** That is fine and simpler for a learning project, but it makes the Control Plane ↔ Host channel carry all streaming output, so design it for streams from day one.
- **envd is a Connect RPC + REST server with a small, good process API.** It offers `Start` (a server stream of `start`/`data`/`end`/`keepalive` events), `Connect` (reattach by PID or tag), `List`, `SendInput`/`StreamInput`, `SendSignal`, `Update` (PTY resize), a filesystem service with watchers, and a REST `/init` that the orchestrator pushes after boot (env vars, access token, CA bundle, CPU count). It is started by systemd, which E2B's template builder installs into every image. It is not PID 1.
- **Process model gaps in envd that we should not copy.** Output produced while no client is attached is **discarded**: there is no buffer or replay, only a 64-event channel. Exit codes are kept for 30 seconds. Signals go to the direct child only, not the process group. Daytona's daemon does better on both counts: it writes session command output to log files that can be re-read and followed, and it kills with `kill(-pgid)` plus a process-tree walk. Our `output?offset=` design is ahead of E2B here.
- **State.** E2B treats Redis as the source of truth for *running* sandboxes and Postgres for durable entities. A node sync every 20 s kills sandboxes that are on a node but not in Redis, after a **1-minute grace period**, and skips the whole node when a store read fails. Daytona keeps `state`/`desiredState`/`pending` in Postgres and reconciles every 10 s. agent-sandbox uses etcd through the Kubernetes API. **[Inference]** SQLite in one Control Plane is reasonable. The first thing to break is high-frequency writes (activity, heartbeats), which Daytona already buffers in Redis.
- **Timers live in the control plane, not on the host.** E2B's API runs an evictor that polls Redis for expired wall-clock `EndTime`s every 50 ms. The orchestrator stores the end time but does not act on it. Daytona's proxy reports activity to the API at most every 50 s, the API buffers it in a Redis sorted set, flushes it to Postgres every 10 s, and an auto-stop cron runs every 10 s. Neither vendor counts background processes as activity in its open-source code.
- **Metering: neither vendor does what ADR 0004 does.** E2B's API emits `InstanceStarted`/`InstanceStopped` to an external collector and clamps the stop time to `min(now, EndTime)`. Daytona opens and closes Postgres "usage periods" on observed state changes, using the API's clock at the moment it notices. A lost runner is therefore billed until the platform notices. ADR 0004 is stricter than both; it needs a per-Host `last_report_at`, recorded with the Control Plane's clock, to work.
- **Egress.** E2B gives each sandbox its own netns and nftables rules, and runs the SNI/Host-inspecting TCP firewall **inside the orchestrator process**. It exposes a plug-in `EgressProxy` interface, whose closed-source implementation resolves secret markers. The CA bundle reaches the guest via envd `/init`. Daytona v0 uses CIDR-only iptables chains per container. E2B also lets sandboxes open connections to host-side services (`hyperloop` `/me` and `/logs`, an NFS proxy), which is a wider exception than our ADR 0005 allows.

---

## 2. Findings by question

### Q1. Control Plane ↔ Host direction

**E2B: push, all the way.**
- The API calls `SandboxService.Create/Update/List/Delete/Pause/Checkpoint` on each orchestrator over gRPC on port 5008 ([ARCHITECTURE.md §Orchestrator](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L576-L593), [orchestrator.proto](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/orchestrator/orchestrator.proto#L373-L380)).
- Node discovery uses Kubernetes, DNS, a static list or Nomad ([ARCHITECTURE.md §Deployment topology](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L1505-L1510)).
- Creation is synchronous end to end. The doc says "The API blocks on the gRPC `Create`, which itself blocks on envd's `/init` — when the client gets a response, the sandbox is fully usable" ([§Sandbox creation](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L879-L909)).
- **Remote clusters are still push.** The API dials the cluster's *edge endpoint* with an edge token. The comment in the code says "A remote cluster's instances are all reached through the same edge endpoint, routed by the instance ID each call carries" ([remote_connections.go](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/clusters/remote_connections.go#L21-L30), [cluster.go](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/clusters/cluster.go#L142-L170)). So E2B solves "nodes in someone else's cloud" with one inbound ingress per cluster, not one per node.
- Reconnect semantics come from idempotency instead of durable jobs. There is an opt-in `Idempotency-Key` with Redis reservations, and a `Should-Retry` header that is `false` when the outcome is unknown, "where a retry could create a second sandbox" ([§Opt-in request idempotency, §Sandbox creation retry hints](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L948-L1014)).

**Daytona v0.190.0: both modes in code, pull is the default.**
- The runner config sets `API_VERSION` with `default:"2"` ([config.go#L64](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/cmd/runner/config/config.go#L64)). With version 2 the runner starts a health-check service (every 30 s by default) and a poller ([main.go#L245-L292](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/cmd/runner/main.go#L245-L292)).
- The API side: "Instead of making direct API calls to the runner, it creates jobs in the database that the v2 runner polls" ([runnerAdapter.v2.ts#L49](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/sandbox/runner-adapter/runnerAdapter.v2.ts#L49)). The job row is written to Postgres, then the API does `LPUSH` to a per-runner Redis list. `pollJobs` waits on `BRPOP` with a 30 s timeout and falls back to the database on Redis errors ([job.service.ts#L88-L230](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/sandbox/services/job.service.ts#L88-L230)).
- **Reconnect is a clean story with pull.** On startup the poller first re-fetches its own `IN_PROGRESS` jobs and re-executes them ([poller.go#L53-L66](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/pkg/runner/v2/poller/poller.go#L53-L66)). Results go back through `UpdateJobStatus` ([executor.go#L211](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/pkg/runner/v2/executor/executor.go#L211)).
- **The runner still listens even in v2 mode.** `api.NewApiServer` starts unconditionally ([main.go#L294](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/cmd/runner/main.go#L294)), and the proxy forwards toolbox and preview traffic to `runner.ProxyUrl/sandboxes/{id}/toolbox/...` ([get_sandbox_target.go#L99](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/proxy/pkg/proxy/get_sandbox_target.go#L99)). In v0, pull covered the control path only.

**Daytona v2 (closed; architecture page).**
- NATS JetStream carries jobs and streams events back. The runners "pull jobs only from its queue", which "lets a runner run behind a firewall", and "Runners accept no inbound connections" ([architecture](https://www.daytona.com/docs/architecture.md)).
- Create is asynchronous: "Expect a sandbox to reach its new state after a create or delete returns" (same page).
- The page does not say how proxy traffic reaches a runner that accepts no inbound connections; it only mentions a "runner overlay network". The mechanism is unknown.

**kubernetes-sigs/agent-sandbox.** There is no Host Agent of its own. The controller writes Pods through the Kubernetes API, and the kubelet *watches* the API server, so it is pull by construction ([README](https://github.com/kubernetes-sigs/agent-sandbox/blob/1b7d5336539b9acd44ec431a61f4e83700a0a392/README.md)).

**Trade-offs**

| | Push (E2B, our ADR 0005) | Pull / Host-initiated (Daytona v2, Kubernetes) |
|---|---|---|
| NAT, remote clouds | Needs a reachable endpoint per Host, or per cluster as with E2B's edge | Works behind NAT and firewalls; the Host only needs outbound access |
| Attack surface on the Host | A listening port; on Docker Desktop, `host.docker.internal` makes it reachable from containers unless it is bound carefully | No listener for control traffic (Daytona v0 still had one for data traffic) |
| Latency of a blocking create | One RPC; natural to block on it | Long poll or a stream adds a wake-up hop. Daytona v2 stopped blocking altogether |
| Reconnect and retry | Caller retries plus idempotency keys; the outcome after a broken connection can be unknown | Durable job rows; the runner re-claims `IN_PROGRESS` work after a restart, so delivery is at-least-once and handlers must be idempotent |
| Streaming Exec output through the channel | Natural with gRPC server streams | Awkward with job polling; needs a reverse tunnel or a separate data path |

**Compared with our design**
- ADR 0005 justifies push with "only the more trusted side opens connections". That rule is about the **Sandbox** boundary. The Control Plane and the Host Agent are both in trusted zones (spec §7), so the rule does not decide their direction. **[Inference]**
- Our create blocks (spec §4) and our Exec streams through the Control Plane (spec §3). Job polling in the style of Daytona v0 would fit both badly. **[Inference]**
- **Recommendation [Inference]: don't flip now.**
  - Phase 1 runs on one Mac. Serve the Host Agent's gRPC on a **unix socket**, not TCP. Then `host.docker.internal` cannot reach it, which closes one of the spec's own isolation attacks by construction, and the question of direction disappears.
  - From phase 3 onward, when a Host is a GCP VM or a Hetzner box behind NAT, add a **Host-initiated reverse tunnel**: the Host Agent dials the Control Plane, and the Control Plane speaks the same gRPC service back over that connection, for example with a yamux-style multiplexer. That keeps blocking create and streaming Exec unchanged and removes the Host's control listener.
  - Durable job rows are worth copying only if create becomes asynchronous.

### Q2. Guest Agent design

**envd (E2B)**
- **Protocol.** It listens on port 49983 and uses chi + **Connect RPC**, plus REST ([ARCHITECTURE.md §Envd](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L721-L777)). Connect serves the gRPC, gRPC-Web and Connect protocols over HTTP/1.1 or HTTP/2, which is why the SDKs can call it through an ordinary HTTPS edge.
- **Process service** ([process.proto](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/envd/spec/process/process.proto)):
  - `Start` returns a server stream of `ProcessEvent{start{pid} | data{stdout|stderr|pty} | end{exit_code, exited, status, error} | keepalive}`.
  - `Connect` reattaches to a running process by `ProcessSelector{pid | tag}`.
  - `List`, `SendInput`, `StreamInput` (client stream, "ensures ordering"), `CloseStdin`, `SendSignal` (SIGTERM/SIGKILL only), `Update` (PTY resize).
- **Filesystem service**: `Stat`, `ListDir`, `MakeDir`, `Move`, `Remove`, `WatchDir` (stream), plus polling watchers (`CreateWatcher`, `GetWatcherEvents`, `RemoveWatcher`) ([filesystem.proto](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/envd/spec/filesystem/filesystem.proto)).
- **REST routes**: `/files` upload and download, `/health`, `/metrics`, `/envs`. The control routes `/init`, `/upgrade` and the freeze/thaw hooks are marked `x-internal`, and the edge proxy refuses them with a 404 ([§Envd](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L732-L741), [ingress.go#L52-L55](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/shared/pkg/proxy/ingress.go#L52-L55)).
- **Auth.**
  - Requests carry an `X-Access-Token` header. The API mints the token, the orchestrator writes the token's **hash** into Firecracker MMDS, and it pushes the token itself through `/init` after boot. `/init` is exempt from auth because it is what delivers the token, "which is the main reason the proxy refuses it outright" ([§Envd](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L759-L761), [§Orchestrator](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L631-L633)).
  - The orchestrator reaches envd over the host network at the sandbox's slot IP, over TCP, not vsock (same section).
- **`/init` payload.** Env vars, the access token, metadata, the online CPU count, and the **CA bundle** to trust. envd writes the bundle into the guest trust store and warns that "Applications that cached their trust store must reload it" ([§Envd, Guest trust](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L742-L758)).
- **Injection and start.**
  - The template builder converts the Docker image to ext4 and injects files: hostname, DNS, `envd.service`, and a provisioning script. It then boots a Firecracker VM with **BusyBox init** to run the provisioning script, which "will install systemd, that is later used for proper VM boot" ([builder.go#L113-L124](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/orchestrator/pkg/template/build/builder.go#L113-L124)).
  - It has per-distro init profiles: systemd, OpenRC for Alpine, and NixOS ([distro/init.go](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/orchestrator/pkg/template/build/phases/base/distro/init.go#L11-L19)).
  - envd runs as a systemd unit with `User=root`, `Restart=always`, and `DefaultDependencies=no` so that it starts early ([envd.service.tpl](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/orchestrator/pkg/template/build/core/rootfs/files/envd.service.tpl)). **envd is not PID 1.**
  - envd can be live-upgraded in place with `syscall.Exec` under the same PID, carrying its process table and file descriptors across ([§Envd, Live upgrade](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L762-L770)).

**Daytona daemon / toolbox (v0.190.0)**
- **Injection.** The runner bind-mounts the daemon binary **read-only** at `/usr/local/bin/daytona`. It sets that as the container's `Entrypoint` and passes the image's original entrypoint (or cmd) as arguments ([container_configs.go#L143-L189](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/pkg/docker/container_configs.go#L143-L189)). The daemon therefore **runs as PID 1** and installs a SIGCHLD reaper: "The daemon runs as PID 1 inside containers, so orphaned processes … get reparented here" ([main.go#L225-L230](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/daemon/cmd/daemon/main.go#L225-L230)).
- **Protocol.** It speaks plain HTTP REST plus WebSocket on port 2280, served by gin. The API is broad: `/files/*`, `/process/execute`, `/process/session/*`, `/process/pty/*` (WebSocket), `/process/interpreter/*`, `/git/*`, `/lsp/*`, `/computeruse/*`, `/port`, and `/proxy` ([server.go#L166-L375](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/daemon/pkg/toolbox/server.go#L166-L375)).
- **Auth.** I found **no authentication middleware** on the toolbox router. The token received in `/init` is used only for telemetry ([controller.go#L33](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/daemon/pkg/toolbox/controller.go#L33), [telemetry.go#L46](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/daemon/pkg/toolbox/telemetry.go#L46)). Access control happens at the proxy and the runner. **[Inference]** Any process inside the container can drive the daemon, which runs as root.

**agent-sandbox and SWE-ReX: the two other injection choices**
- agent-sandbox has no platform agent. The template's image carries its own server. The example `python-runtime-sandbox` is a FastAPI app on port 8888 with `/execute`, `/upload` and `/download` ([main.py](https://github.com/kubernetes-sigs/agent-sandbox/blob/1b7d5336539b9acd44ec431a61f4e83700a0a392/examples/python-runtime-sandbox/main.py#L117-L230), [python-sandbox-template.yaml](https://github.com/kubernetes-sigs/agent-sandbox/blob/1b7d5336539b9acd44ec431a61f4e83700a0a392/clients/python/agentic-sandbox-client/python-sandbox-template.yaml#L15-L18)).
- SWE-ReX injects its agent at runtime.
  - The container command is `swerex-remote --auth-token <uuid>`. If that is missing, it falls back to `pip install pipx && pipx run swerex-remote`, which needs Python and network access inside the image ([docker.py#L117-L131](https://github.com/SWE-agent/SWE-ReX/blob/5c995c365dfb1fd5bc56fda688be5d8538f9931f/src/swerex/deployment/docker.py#L117-L131)).
  - It uses a fresh UUID per container, checked as an `X-API-Key` header ([server.py#L35-L72](https://github.com/SWE-agent/SWE-ReX/blob/5c995c365dfb1fd5bc56fda688be5d8538f9931f/src/swerex/server.py#L35-L72)).

**Compared with our design**
- **ADR 0003 (the platform injects the agent) is closest to Daytona v0 for Docker**, which uses a read-only bind mount plus an entrypoint override. It is *not* what E2B does for Firecracker: E2B rewrites the image, including installing systemd. **[Inference]** To keep "run official images as-is" on Firecracker, our Guest Agent should be the guest's **PID 1** there too: it mounts `/proc`, `/sys`, `/dev` and `/tmp`, reaps children, and then starts the image's entrypoint. Then the image needs no init system.
- **ADR 0002 (one agent, unix socket or vsock)** is consistent with the vendors. E2B uses TCP on a per-sandbox network and Daytona uses TCP on the Docker bridge. Our choice of unix socket or vsock avoids exposing the agent on a network at all, which is stricter. **[Inference]**
- **What to copy [Inference]:**
  1. A process API shaped like envd's: `Start` as a stream of `start`/`data`/`end`/`keepalive`; `Connect` by PID or caller-supplied tag; `SendSignal`; `StreamInput`; PTY resize.
  2. An `/init`-style call that the Host Agent **pushes** after start. It should carry the per-Sandbox token, default env vars, the per-Sandbox CA for ADR 0006, and the clock. That gives spec §7's "token kept in a root process's memory" a concrete delivery path.
  3. Keepalive events on long streams.
  4. Separate internal routes from user-facing ones, even if all of them travel over the unix socket.
- Consider Connect-Go instead of plain gRPC. **[Inference]** It serves gRPC to the Host Agent and stays curl-able for debugging and the learning notes.

### Q3. Process and Exec model

**E2B / envd**
- **One primitive.** Every command, foreground or background, is a `Start`. "Background" is an SDK behaviour: the SDK simply doesn't wait.
- **Survives a client disconnect.** The process context is `context.Background()`, with a comment saying "We do not want the command to be killed if the request context is cancelled". The only bound is the client's `Connect-Timeout-Ms` header ([start.go#L34-L44](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/envd/internal/services/process/start.go#L34-L44), [start.go#L229-L241](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/envd/internal/services/process/start.go#L229-L241)).
- **Reattach** with `Connect(pid|tag)` ([connect.go](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/envd/internal/services/process/connect.go#L19-L60)).
- **No output replay.**
  - Output is published to subscribers only `if outMultiplex.HasSubscribers()`; otherwise the bytes are dropped ([handler.go#L296-L407](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/envd/internal/services/process/handler/handler.go#L296-L407)).
  - The fan-out channel buffers 64 events ([handler.go#L32](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/envd/internal/services/process/handler/handler.go#L32)).
  - A late `Connect` sees only output produced after it subscribes.
- **Exit status** is kept for `terminatedRetentionTTL = 30 * time.Second`, so a client that reconnects within 30 s still learns the exit code ([service.go#L21-L30](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/envd/internal/services/process/service.go#L21-L30)).
- **Killing.**
  - `sysSignal` calls `p.cmd.Process.Signal(signal)`, which reaches the direct child only ([os_unix.go#L18-L21](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/envd/internal/services/process/handler/os_unix.go#L18-L21)). The child is a `/bin/sh -c '… exec cmd'` wrapper with no `Setpgid` ([spawn_unix.go#L20-L57](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/envd/internal/services/process/handler/spawn_unix.go#L20-L57)).
  - Processes are placed into per-type cgroups (user, PTY, system) with `CLONE_INTO_CGROUP` ([handler.go#L440-L449](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/envd/internal/services/process/handler/handler.go#L440-L449)). Those cgroups are used for pause freezing, not for kill.
- **PTY** uses `creack/pty` `StartWithSize` and supports resize ([pty_unix.go](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/envd/internal/services/process/handler/pty_unix.go)).

**Daytona daemon**
- **Three models**:
  - `/process/execute`: synchronous, with a timeout.
  - **Sessions**: a persistent `bash` per session. Each command gets a `cmdId` and runs `async` or sync.
  - **PTY sessions** over WebSocket ([server.go#L200-L241](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/daemon/pkg/toolbox/server.go#L200-L241)).
- **Output is persisted.** Each session command's output is written to `<configDir>/sessions/<sid>/<cmdId>/output.log`, with stdout and stderr distinguished by inline prefixes, and the exit code goes to a file. The log endpoints return the whole file or `follow=true` over WebSocket ([execute.go#L19-L80](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/daemon/pkg/session/execute.go#L19-L80), [log.go](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/daemon/pkg/toolbox/process/session/log.go#L23-L45)). Because the output is a file, a late reader sees everything.
- **Process groups.** Sessions and `/process/execute` set `Setpgid: true` ([create.go#L27](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/daemon/pkg/session/create.go#L27)). Termination sends `kill(-pid, SIGTERM)`, walks the process tree for descendants that escaped with `setsid`, waits a grace period, then sends `SIGKILL` ([delete.go#L44-L64](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/daemon/pkg/session/delete.go#L44-L64)).

**Compared with our design**
- Our split (Exec in the foreground with a timeout; Process in the background with `output?offset=`) is **more explicit than E2B's** single primitive. Our offset replay fixes E2B's biggest gap. Daytona's file-backed logs show that the replay can be simple. **[Inference]**
- **Proposed details [Inference]:**
  - **Output store.** Use one append-only log per Process on the Guest Agent. Use a tmpfs file, or memory if size-capped, for example 8 MiB per stream. Separate stdout and stderr offsets, or frame them in one stream.
  - **Truncation.** When a cap drops old bytes, report `truncated_before` so that `offset` stays meaningful.
  - **Exit status.** Keep it until the Process is deleted or the Sandbox ends, not for 30 seconds.
  - **Process groups.** Start every Exec and Process with `Setpgid`. Kill with `kill(-pgid, SIGTERM)`, wait a grace period, then `SIGKILL`. On Firecracker, put each Process in its own cgroup and use `cgroup.kill`, which leaves no escapees.
  - **Disconnects.** Decide explicitly what happens to an Exec when its client disconnects. E2B keeps it running; with a separate Process primitive, killing a foreground Exec on disconnect is cleaner and avoids invisible work. Either way, write it down in spec §4.
- Ticket 06 calls for a single Guest → Host event stream with sequence numbers. envd has no such stream: each process has its own stream, and the Host polls with `List`. **[Inference]** A sequenced event log is reasonable, but the per-Process output log makes most of it unnecessary. Exit events can be derived from the Process table on reconnect.

### Q4. State and reconciliation

- **E2B**
  - **Postgres** holds durable state: teams, templates, builds, snapshots, volumes, API keys.
  - **Redis** holds the "running-sandbox store (source of truth)" plus routing records and caches.
  - **ClickHouse** holds events and metrics ([§Data stores](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L843-L850)).
  - The routing record `sandbox:routing:{id}` is written by the **orchestrator** on `MarkRunning` and deleted on `MarkStopping`, guarded by `execution_id`/`lifecycle_id` so that a stale lifecycle cannot delete a newer one ([§Sandbox routing records](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L1043-L1068)).
- **E2B reconciliation**
  - `keepInSync` runs every `cacheSyncTime = 20 * time.Second` ([cache.go#L24](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/orchestrator/cache.go#L24)). Each node sync calls `ServiceInfo` and `List`, then `store.Reconcile`. The code comment reads "Redis is the source of truth — divergent sandboxes are orphans running on the node but not present in the store. Kill them." ([store.go#L390-L438](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/sandbox/store.go#L390-L438)).
  - Orphan candidates younger than `orphanGracePeriod = time.Minute` are skipped. On a store read error the sync skips "the whole node to avoid mass kills" ([redis/main.go#L38](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/shared/pkg/sandboxstore/storage/redis/main.go#L38), [#L274-L316](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/shared/pkg/sandboxstore/storage/redis/main.go#L274-L316)).
  - Sandboxes the node reports as ended are closed through `EndEndedSandbox`.
  - Node restarts are handled by "startup reclaim", which tears down leftover namespaces and scratch directories ([§Orchestrator](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L672-L679)).
- **Daytona v0**
  - Postgres holds `state`, `desiredState` and a `pending` flag per sandbox ([sandbox.entity.ts#L33-L105](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/sandbox/entities/sandbox.entity.ts#L33-L105)). Redis holds locks, the activity buffer and job wake-ups.
  - A `sync-states` cron runs every 10 s per runner, under a Redis lock, and drives pending sandboxes toward their desired state ([sandbox.manager.ts#L831-L888](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/sandbox/managers/sandbox.manager.ts#L831-L888)).
  - The runner's Docker monitor removes iptables chains of containers that no longer exist ([monitor.go#L250-L276](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/pkg/docker/monitor.go#L250-L276)).
  - I did not find a sweep that destroys containers unknown to the database.
- **Daytona v2**: Postgres for entities, NATS JetStream for jobs and events ([architecture](https://www.daytona.com/docs/architecture.md)).
- **agent-sandbox**: etcd through the Kubernetes API, with a standard controller reconcile loop ([sandbox_controller.go](https://github.com/kubernetes-sigs/agent-sandbox/blob/1b7d5336539b9acd44ec431a61f4e83700a0a392/controllers/sandbox_controller.go)).

**Compared with our design**
- SQLite as the source of truth in a single Control Plane is the same shape as E2B's "one store is the truth, the nodes are reconciled against it". It is reasonable for a learning project. **[Inference]**
- **What breaks first [Inference]:**
  1. **Write amplification from high-frequency updates.** Activity timestamps, per-Host reports every few seconds, and Usage events land on SQLite's single writer. Daytona buffers activity in Redis and flushes it every 10 s for the same reason. Keep activity in memory, persist only state transitions and periodic snapshots, use WAL mode, and route writes through one goroutine.
  2. **Holding a transaction across a Host Agent RPC** during a blocking create. Write the `Starting` row first, commit, call the Host, then commit the transition.
  3. **A single point of failure.** When the Control Plane is down, timers and reconciliation stop. E2B has the same property.
- **Reconciliation rules to copy into ticket 10 [Inference]:**
  - Give unknown Sandboxes a grace period (E2B uses 1 minute) so that a create in flight is not killed.
  - Skip a Host's reconcile when the database read fails.
  - Write the database intent (`Starting`) before calling the Host, so that "on the machine but not in the database" really means an orphan.

### Q5. Lifecycle timers

- **E2B: wall clock, enforced by the API.**
  - The API's evictor polls `store.ExpiredItems` every `pollInterval = 50 * time.Millisecond` and evicts each expired sandbox (kill, or auto-pause) with bounded concurrency ([evict.go#L24](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/orchestrator/evictor/evict.go#L24), [#L138-L195](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/orchestrator/evictor/evict.go#L138-L195)).
  - `KeepAliveFor` (the SDK's `setTimeout`) moves `EndTime` in Redis. The comment says "The store is the expiry clock" ([keep_alive.go#L22-L52](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/orchestrator/keep_alive.go#L22-L52)).
  - The orchestrator receives and stores the end time (`SetEndAt`). I found no node-side timer that kills on it ([sandboxes.go#L749-L752](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/orchestrator/pkg/server/sandboxes.go#L749-L752)). **[Inference]** If the API is down, sandboxes outlive their timeout.
  - I found no activity tracking in the open-source code.
- **Daytona v0: idle, observed at the proxy.**
  - For each proxied request, the proxy posts activity to the API and keeps re-posting every 50 s while the connection lives ([get_sandbox_target.go#L87-L95](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/proxy/pkg/proxy/get_sandbox_target.go#L87-L95), [#L311-L335](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/proxy/pkg/proxy/get_sandbox_target.go#L311-L335)).
  - The API throttles that per sandbox, `ZADD`s it into a Redis sorted set, and flushes the set to Postgres every 10 s ([sandbox-activity.service.ts#L25-L80](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/sandbox/services/sandbox-activity.service.ts#L25-L80)).
  - An `auto-stop-check` cron runs every 10 s and selects sandboxes whose `lastActivityAt < NOW() - autoStopInterval` ([sandbox.manager.ts#L97-L130](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/sandbox/managers/sandbox.manager.ts#L97-L130)).
  - Processes inside the sandbox are invisible to this path, which matches the v0 docs' warning that a long task "may auto-stop mid-process".
- **agent-sandbox**: an absolute `shutdownTime` that the controller enforces; `shutdownPolicy` is `Retain` or `Delete` ([sandbox_types.go#L395-L416](https://github.com/kubernetes-sigs/agent-sandbox/blob/1b7d5336539b9acd44ec431a61f4e83700a0a392/api/v1beta1/sandbox_types.go#L395-L416)).

**Compared with our design**
- Our idle rule counts running Processes and Exposed Port traffic (spec §5, ticket 07). That is something no vendor implements in open-source code. The activity signals are spread out: API calls and Execs are seen by the Control Plane, while Processes, streams and port traffic are seen by the Host Agent and the Guest Agent.
- **Proposal [Inference]:**
  - The Host Agent keeps, per Sandbox, `last_activity_at` and `active_count` (running Execs, Processes, open streams, port connections), and sends both in its periodic report.
  - The Control Plane owns the deadline, in one evaluator loop like E2B's evictor, using `max(cp_seen, host_reported)`. Test it with the fake clock that ticket 07 already requires.
  - Optional safety net: the Host Agent enforces the *maximum lifetime* locally when it has not reached the Control Plane for N minutes, so a Control Plane outage cannot run Sandboxes forever.

### Q6. Metering and Usage

- **E2B**
  - On insert and remove, the API calls `InstanceStarted`/`InstanceStopped` on an external `AnalyticsCollector` gRPC service, which is not in the repo ([analytics.go#L43-L96](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/orchestrator/analytics.go#L43-L96)).
  - The stop time is computed by the API as `sbxStopTime`, which clamps it to `EndTime` "to prevent stale record evicted late reporting incorrect data" ([analytics.go#L26-L41](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/orchestrator/analytics.go#L26-L41)).
  - Each run carries an `ExecutionId`.
  - Lifecycle events also go to ClickHouse `sandbox_events`, whose `id` column is `DEFAULT generateUUIDv4()`. The ID is generated at insert time, so it cannot deduplicate a retried event ([migration](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/clickhouse/migrations/20251017213616_sandbox_events_id.sql#L5)).
  - The orchestrator writes cgroup `sandbox_host_stats` samples ([§Data stores](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L849)).
- **Daytona v0**
  - It uses Postgres `sandbox_usage_period` rows. When it observes a `STATE_UPDATED` event, it closes the open period and opens a new one with `new Date()`, the API's clock. `STARTED` opens a CPU, memory and disk period; `STOPPING`/`PAUSING` opens a disk-only period; `ERROR`/`DESTROYED` closes the period ([usage.service.ts#L70-L156](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/usage/services/usage.service.ts#L70-L156)).
  - A per-sandbox Redis lock serializes these updates.
  - A cron closes and reopens periods older than 24 h ([#L158-L228](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/usage/services/usage.service.ts#L158-L228)).
  - When a runner is lost, the period ends when the API sets `ERROR`, that is, when it notices.
- **Daytona v2**: "runners report state changes as events and stream usage to billing" ([architecture](https://www.daytona.com/docs/architecture.md)). No detail is given.

**Compared with our design**
- ADR 0004 ends an interval at the Host's last successful report. It is **stricter than both vendors**: Daytona bills until it notices, and E2B until it notices or until `EndTime`. No vendor contradicts it, but none validates it either.
- **Implementation notes [Inference]:**
  - Record `last_report_at` per Host using the **Control Plane's receive clock**, to avoid Host clock skew.
  - Generate event IDs **at the source**, the Host Agent, per transition, so a retry reuses the ID. E2B's insert-time UUID is the counterexample.
  - Derive intervals only from events, plus the ADR 0004 cut on Host loss.
  - Daytona's 24 h roll-over is a cheap way to bound open intervals; we split by day at query time anyway (ticket 09).

### Q7. Scheduling and overcommit

- **E2B**
  - It uses **best-of-K**: sample K=3 ready nodes, score each as the max of CPU load and hugepage-pool load, and pick the lowest ([placement_best_of_K.go#L12-L32](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/orchestrator/placement/placement_best_of_K.go#L12-L32)).
  - The CPU score is `(requested + reserved + α·busy) / (R·cpus)`. The defaults are **R = 4**, a CPU overcommit ratio, and α = 0.5.
  - In-flight placements are counted locally (`PlacementMetrics.InProgress()`) so that concurrent creates don't pile onto one node.
  - The memory overcommit check `placement-memory-max-overcommit` defaults to **0, meaning disabled** ([flags.go#L715-L719](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/shared/pkg/featureflags/flags.go#L715-L719)). Memory density comes from lazy UFFD paging and hugepage-pool accounting.
  - A node that refuses with `ResourceExhausted` stays eligible, and placement backs off with jitter ([§Placement](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L206-L238)).
- **Daytona v0**
  - Runners report metrics: usage percentages and allocated CPU, memory and disk. The API computes a **TOPSIS** score over seven criteria ([runner.service.ts#L1095-L1160](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/sandbox/services/runner.service.ts#L1095-L1160)).
  - The default "critical" allocation is **500 %** for CPU, memory and disk, so allocation up to 5× is scored rather than refused ([configuration.ts#L194-L245](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/config/configuration.ts#L194-L245)).
  - Placement takes the top 10 runners above a score threshold and picks **one at random** ([runner.service.ts#L361](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/sandbox/services/runner.service.ts#L361), [#L803-L820](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/sandbox/services/runner.service.ts#L803-L820)).

**Compared with our design**
- First-fit with no memory overcommit and a CPU ratio of 1.0 (spec §6) is far more conservative than both vendors. That is right for clean benchmarks. **[Inference]**
- **Copy two things [Inference]:**
  1. Count **in-flight reservations** in the scheduler. Otherwise two concurrent creates can both fit into the last slot.
  2. Let the **Host Agent refuse** a create it cannot fit, with a typed error like `ResourceExhausted`. The Control Plane then tries the next Host instead of trusting a stale capacity report.

### Q8. Exposed Ports and ingress

- **E2B**
  - Wildcard DNS sends `*.domain` to **client-proxy**, a stateless edge. It parses `<port>-<sandboxId>.<domain>` and looks up the orchestrator-written Redis record `sandbox:routing:{id}`. It forwards to the node's orchestrator proxy on `:5007`, which dials `slotIP:port` ([§Client proxy](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L779-L789), [§Sandbox traffic](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L1016-L1041)).
  - On a miss, meaning a paused sandbox, it calls the API's `ResumeSandbox` and shares one "resume flight" between concurrent requests.
  - **Token check.** The header is `e2b-traffic-access-token`, compared with `subtle.ConstantTimeCompare`. The envd port is exempt from it because envd has its own auth. Internal envd paths are refused ([ingress.go#L14-L70](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/shared/pkg/proxy/ingress.go#L14-L70)).
  - Header-based routing is supported for IP hosts and localhost ([host.go](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/shared/pkg/proxy/host.go)).
- **Daytona v0**
  - The proxy parses `<port>-<sandboxId | signedToken>.<proxy domain>`, or `/toolbox/<id>/…` for the toolbox. It authenticates unless the sandbox is public; the toolbox, terminal and recording ports are always authenticated ([get_sandbox_target.go#L24-L80](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/proxy/pkg/proxy/get_sandbox_target.go#L24-L80)).
  - It resolves the runner's `ProxyUrl` from the API and caches it for 2 min, then forwards to the runner. Every proxied request also feeds idle tracking (Q5).
- **agent-sandbox**
  - Its **sandbox-router** routes on `X-Sandbox-ID`, `X-Sandbox-Namespace` and `X-Sandbox-Port` headers, not hostnames. It resolves through an informer cache or a per-sandbox Service DNS name ([sandbox-router/README.md](https://github.com/kubernetes-sigs/agent-sandbox/blob/1b7d5336539b9acd44ec431a61f4e83700a0a392/sandbox-router/README.md)).
  - It strips `Authorization` before forwarding, so that "any sandbox" cannot impersonate the caller.
  - It rejects loopback, link-local and similar addresses in `X-Sandbox-Pod-IP` as an SSRF defence.

**Compared with our design**
- Ticket 11 puts the port proxy on the Host. That is E2B's orchestrator-proxy layer without the edge in front, which is fine with one Host. **[Inference]**
- **Copy [Inference]:**
  - Compare tokens in constant time.
  - Strip the platform's auth header before forwarding (agent-sandbox).
  - Refuse Guest Agent paths through the port proxy.
  - Make the routing table a Control Plane → proxy push, keyed by Sandbox and lifecycle, so a stale entry cannot route to a reused IP (E2B's `execution_id`/`lifecycle_id` guard).
  - Count proxied connections as activity for the whole time they are open, as Daytona's 50 s re-post does.

### Q9. Egress Proxy placement

- **E2B**
  - Each sandbox has a network "slot": a netns, a veth and a tap, a unique host IP, NAT, and its own nftables egress firewall ([§Orchestrator, Networking](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L675-L679)).
  - Domain rules are enforced by `tcpfirewall`, which runs **inside the orchestrator process**. It is one proxy per host with separate ports for HTTP (Host header), TLS (SNI) and everything else (CIDR only) ([tcpfirewall/proxy.go#L26-L60](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/orchestrator/pkg/tcpfirewall/proxy.go#L26-L60)).
  - A pluggable `EgressProxy` interface (`OnSlotCreate`, `CABundle(envdVersion)`, `HeadFilterRules`) is the seam for the closed-source edition ([egressproxy.go](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/orchestrator/pkg/sandbox/network/egressproxy.go)). That edition "resolves a marker to a value at sandbox egress" ([§API, Secrets](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md#L243-L256)).
  - The CA bundle that egress interception needs is delivered into the guest by envd `/init` (Q2).
  - **Sandbox → host services.** Per-slot `REDIRECT` rules send sandbox traffic for a fixed in-sandbox IP to host-side servers. On port 80 that is `hyperloop`, with `/me` (identifies the caller by source address) and `/logs`. On ports 111 and 2049 it is an NFS proxy for volumes ([network.go#L339-L366](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/orchestrator/pkg/sandbox/network/network.go#L339-L366), [hyperloopserver/handlers/me.go](https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/orchestrator/pkg/hyperloopserver/handlers/me.go)).
- **Daytona v0**
  - Each container gets an iptables chain inserted in `DOCKER-USER` and matched by source IP. The chain is a **CIDR allowlist** ending in `DROP`, with no domain inspection ([netrules/set.go](https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/pkg/netrules/set.go#L8-L50)).
  - In v2 the runner enforces the egress policy and blocks traffic between sandboxes on the host. Secrets are "encrypted at rest with AES-256-GCM" in Postgres and travel "sealed to each runner's key" in NATS ([architecture](https://www.daytona.com/docs/architecture.md)).

**Compared with our design**
- A separate Egress Proxy process per Host (ADR 0005, ADR 0006) is **stricter than E2B**, which runs its proxy in-process with the orchestrator. If that proxy has a bug, the orchestrator process is exposed. **[Inference]**
- E2B's per-slot nftables "redirect to proxy port" is the mechanism to copy for Firecracker. For Docker on a Mac, the equivalent is an internal-only Docker network whose only route out is the proxy container. **[Inference]**
- **Copy E2B's CA delivery [Inference].** The Host Agent hands the per-Sandbox CA to the Guest Agent's init call, and the Guest Agent installs it **before** starting the image's entrypoint. That sidesteps E2B's "Applications that cached their trust store must reload it" caveat.
- **E2B's `hyperloop` and NFS redirects are the counterexample ADR 0005 warns about.** Host services are reachable from sandboxes, and the caller is identified by source IP. Keep our narrower exception. **[Inference]**

---

## 3. Component map

| Our GLOSSARY term | E2B | Daytona v0.190.0 | Daytona v2 (docs) | agent-sandbox | SWE-ReX |
|---|---|---|---|---|---|
| **Control Plane** | `packages/api` (REST, placement, evictor, node sync) + Postgres/Redis/ClickHouse | `apps/api` (NestJS) + Postgres + Redis | API + Postgres + NATS JetStream (+ optional Redis), per region | Controller + Kubernetes API server/etcd | The deployment class in the client process |
| **Host** | Sandbox node | Runner machine | Runner | Kubernetes node | Docker host or a cloud service (Modal, Fargate, Daytona) |
| **Host Agent** | `packages/orchestrator` (gRPC :5008, sandbox proxy :5007) | `apps/runner` (REST API + v2 poller/executor/health check) | Runner (pulls jobs) | kubelet + container runtime (not part of the project) | none |
| **Guest Agent** | `envd` (:49983, Connect RPC + REST; systemd unit; not PID 1) | daemon/toolbox (`/usr/local/bin/daytona`, :2280 REST/WebSocket; PID 1) | daemon (Toolbox API + SSH) | none; the image's own server (example: FastAPI on :8888) | `swerex-remote` (FastAPI :8000, `X-API-Key`) |
| **Egress Proxy** | `tcpfirewall` inside the orchestrator + per-slot nftables; `EgressProxy` plug-in (closed edition resolves secrets) | per-container iptables chain (CIDR only) | runner-enforced egress policy | NetworkPolicy (example `agent-sandbox-rl-network-policy`) | none |
| **Port proxy / Exposed Port** | `client-proxy` (edge) → orchestrator proxy; `e2b-traffic-access-token` | `apps/proxy` → runner `ProxyUrl`; preview token or signed URL | stateless proxy, host- and path-based | `sandbox-router` (header-based) | published container port |
| **Usage** | API → external AnalyticsCollector (start/stop); ClickHouse events and host stats | Postgres `sandbox_usage_period` | runners "stream usage to billing" | none | none |
| **Reconciliation** | API node sync every 20 s; orphans killed after 1 min grace | `sync-states` cron every 10 s on desired state | events from runners | controller reconcile | none |
| **Exec / Process** | one `Start` primitive; `Connect` reattaches; no replay | `/process/execute` + sessions with file-backed logs + PTY | Toolbox API | `/execute` (example) | `run_in_session`, `execute` |

---

## 4. Impact on our design (all proposals are **[Inference]**)

| # | Finding | Affects | Proposed change |
|---|---|---|---|
| 1 | ADR 0005's "more trusted side connects" rule doesn't decide CP ↔ Host (both trusted). Daytona moved to pull for NAT and firewalls; E2B stays push with a per-cluster edge | **ADR 0005**, spec §3, §7, ticket 01, ticket 16 | Amend ADR 0005 so that "outside-in" is scoped to the Sandbox boundary. In phase 1, serve the Host Agent on a **unix socket** (CP and Host share the Mac), so `host.docker.internal` cannot reach it. Record a future option: a Host-initiated reverse tunnel carrying the same gRPC service, for Hosts behind NAT. Don't adopt job polling while create is blocking |
| 2 | envd drops output with no subscriber and keeps exit codes for 30 s; Daytona persists logs | spec §4 (Process), ticket 06 | Per-Process append-only output log in the Guest Agent, capped, with `truncated_before`. Separate stdout and stderr offsets. Keep exit status until DELETE or Sandbox end |
| 3 | envd signals only the direct child; Daytona kills the process group and walks the tree | ticket 06, ticket 04 | `Setpgid` on every Exec and Process; kill `-pgid` with SIGTERM, then SIGKILL after a grace period; a cgroup per Process with `cgroup.kill` on Firecracker |
| 4 | E2B makes the image bootable by installing systemd; Daytona runs its agent as PID 1 | **ADR 0003**, ticket 01, phase 3 | State in ADR 0003 that the Guest Agent runs as **PID 1** (child reaper; on Firecracker also mounts the pseudo-filesystems) and starts the image's entrypoint as a child. Then no Template needs an init system |
| 5 | envd's `/init` is pushed by the host: token, env, CA bundle, CPU count | spec §7, ticket 16, ticket 23, ADR 0006 | Add an `Init` RPC from the Host Agent to the Guest Agent, called once before the entrypoint starts. It carries the per-Sandbox token, default env and the per-Sandbox CA. The Guest Agent installs the CA before any user process starts |
| 6 | E2B reconcile: 1-minute grace for orphans, skip a node on read errors, orchestrator-guarded lifecycle IDs | ticket 10, spec §5 | Write the `Starting` row before calling the Host. Ignore unknown containers younger than a grace period. Skip a Host's reconcile on a DB error. Tag containers with Sandbox ID **and** a lifecycle ID |
| 7 | Timers live in the control plane in both vendors; neither counts Processes | ticket 07, spec §5 | The Host Agent reports `last_activity_at` and `active_count` per Sandbox. The CP evaluator owns deadlines (fake clock). Optional: the Host enforces max lifetime when the CP is unreachable |
| 8 | No vendor implements ADR 0004; E2B's event IDs are generated at insert | **ADR 0004** (keep), ticket 09 | Record `last_report_at` per Host on the CP's clock. Event IDs are generated by the Host Agent per transition and reused on retry |
| 9 | E2B counts in-flight placements, and nodes refuse with `ResourceExhausted` | spec §6, ticket 03 | The scheduler reserves capacity for in-flight creates. The Host Agent does its own admission check and returns a typed refusal that the CP retries on the next Host |
| 10 | Port proxies: constant-time token compare, strip the auth header, lifecycle-guarded routes, refuse agent paths | ticket 11 | Adopt all four. Count open proxied connections as activity |
| 11 | E2B's domain filter runs in-process with the orchestrator; it also allows sandbox → host `hyperloop`/NFS | ADR 0005, ADR 0006, ticket 12 | Keep the Egress Proxy a separate process (no change). Add an isolation test that only the Egress Proxy and DNS ports are reachable from a Sandbox, including after Volumes land (E2B needed NFS) |
| 12 | Vendors keep data traffic (exec, files) off the control-plane API | spec §3 | Keep it through the CP in phase 1, but design CP ↔ Host for many concurrent server streams with backpressure. Note in the spec that a direct edge → Host path is the scale-out option |
| 13 | SQLite single writer vs high-frequency activity and report writes | spec §3 | WAL mode; one writer goroutine; activity kept in memory and persisted only on transitions or snapshots; no transaction held across an RPC |
| 14 | envd uses Connect RPC (gRPC-compatible, curl-able) | ADR 0002 | Optional: implement the Guest Agent with connect-go so the Host Agent speaks gRPC and humans can curl it |

**Findings that challenge an existing ADR**
- **ADR 0005.** Its rationale ("only the more trusted side opens connections") is applied to CP ↔ Host, where it does not hold. Daytona v2 deliberately chose the opposite direction for operational reasons. The ADR's core, which is Sandbox → Host, still stands. E2B is a counterexample to the strictness: it allows sandbox → host `hyperloop` and NFS connections.
- **ADR 0003.** It holds for Docker, where Daytona v0 does the same. On Firecracker, E2B does *not* inject at run time; it rewrites the image at template build, including installing systemd. Keeping ADR 0003 there means the Guest Agent must be PID 1 (row 4).
- **ADR 0004** is not contradicted, but neither vendor does it. Both bill until the platform notices.

---

## 5. Sources (all accessed 2026-11-21)

**E2B Runtime @ `89e771512f7787d46d3402f7c238ec2b4bd650e4`**
- README: https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/README.md
- Architecture: https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/docs/ARCHITECTURE.md
- Orchestrator gRPC: https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/orchestrator/orchestrator.proto
- envd process proto: https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/envd/spec/process/process.proto
- envd filesystem proto: https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/envd/spec/filesystem/filesystem.proto
- envd Start / Connect / service / handler / spawn / os / pty: https://github.com/e2b-dev/runtime/tree/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/envd/internal/services/process
- envd systemd unit: https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/orchestrator/pkg/template/build/core/rootfs/files/envd.service.tpl
- Template builder: https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/orchestrator/pkg/template/build/builder.go
- Init profiles: https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/orchestrator/pkg/template/build/phases/base/distro/init.go
- Evictor: https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/orchestrator/evictor/evict.go
- Keep-alive: https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/orchestrator/keep_alive.go
- Node sync loop: https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/orchestrator/cache.go
- Node sync: https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/orchestrator/nodemanager/sync.go
- Store reconcile: https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/sandbox/store.go
- Redis storage: https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/shared/pkg/sandboxstore/storage/redis/main.go
- Analytics: https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/orchestrator/analytics.go
- ClickHouse event ID migration: https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/clickhouse/migrations/20251017213616_sandbox_events_id.sql
- Placement: https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/orchestrator/placement/placement_best_of_K.go
- Feature flags: https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/shared/pkg/featureflags/flags.go
- Remote clusters: https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/clusters/remote_connections.go, https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/api/internal/clusters/cluster.go
- Ingress: https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/shared/pkg/proxy/ingress.go
- Host routing: https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/shared/pkg/proxy/host.go
- Slot networking: https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/orchestrator/pkg/sandbox/network/network.go
- EgressProxy interface: https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/orchestrator/pkg/sandbox/network/egressproxy.go
- TCP firewall: https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/orchestrator/pkg/tcpfirewall/proxy.go
- Hyperloop: https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/orchestrator/pkg/hyperloopserver/handlers/me.go
- Orchestrator sandboxes server: https://github.com/e2b-dev/runtime/blob/89e771512f7787d46d3402f7c238ec2b4bd650e4/packages/orchestrator/pkg/server/sandboxes.go

**Daytona @ `v0.190.0`**
- Runner main: https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/cmd/runner/main.go
- Runner config: https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/cmd/runner/config/config.go
- Poller: https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/pkg/runner/v2/poller/poller.go
- Executor: https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/pkg/runner/v2/executor/executor.go
- Runner adapter v2: https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/sandbox/runner-adapter/runnerAdapter.v2.ts
- Job service: https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/sandbox/services/job.service.ts
- Container config: https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/pkg/docker/container_configs.go
- Docker monitor: https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/pkg/docker/monitor.go
- Net rules: https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/pkg/netrules/set.go
- Daemon main: https://github.com/daytonaio/daytona/blob/v0.190.0/apps/daemon/cmd/daemon/main.go
- Toolbox server: https://github.com/daytonaio/daytona/blob/v0.190.0/apps/daemon/pkg/toolbox/server.go
- Toolbox controller: https://github.com/daytonaio/daytona/blob/v0.190.0/apps/daemon/pkg/toolbox/controller.go
- Toolbox telemetry: https://github.com/daytonaio/daytona/blob/v0.190.0/apps/daemon/pkg/toolbox/telemetry.go
- Session execute: https://github.com/daytonaio/daytona/blob/v0.190.0/apps/daemon/pkg/session/execute.go
- Session create: https://github.com/daytonaio/daytona/blob/v0.190.0/apps/daemon/pkg/session/create.go
- Session delete: https://github.com/daytonaio/daytona/blob/v0.190.0/apps/daemon/pkg/session/delete.go
- Session logs: https://github.com/daytonaio/daytona/blob/v0.190.0/apps/daemon/pkg/toolbox/process/session/log.go
- Proxy target: https://github.com/daytonaio/daytona/blob/v0.190.0/apps/proxy/pkg/proxy/get_sandbox_target.go
- Activity: https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/sandbox/services/sandbox-activity.service.ts
- Sandbox manager: https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/sandbox/managers/sandbox.manager.ts
- Sandbox entity: https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/sandbox/entities/sandbox.entity.ts
- Runner service: https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/sandbox/services/runner.service.ts
- Configuration: https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/config/configuration.ts
- Usage: https://github.com/daytonaio/daytona/blob/v0.190.0/apps/api/src/usage/services/usage.service.ts
- Daytona v2 architecture page (vendor doc; v2 is closed source): https://www.daytona.com/docs/architecture.md

**kubernetes-sigs/agent-sandbox @ `1b7d5336539b9acd44ec431a61f4e83700a0a392`**
- README: https://github.com/kubernetes-sigs/agent-sandbox/blob/1b7d5336539b9acd44ec431a61f4e83700a0a392/README.md
- Sandbox types: https://github.com/kubernetes-sigs/agent-sandbox/blob/1b7d5336539b9acd44ec431a61f4e83700a0a392/api/v1beta1/sandbox_types.go
- Controller: https://github.com/kubernetes-sigs/agent-sandbox/blob/1b7d5336539b9acd44ec431a61f4e83700a0a392/controllers/sandbox_controller.go
- Router: https://github.com/kubernetes-sigs/agent-sandbox/blob/1b7d5336539b9acd44ec431a61f4e83700a0a392/sandbox-router/README.md
- Example runtime: https://github.com/kubernetes-sigs/agent-sandbox/blob/1b7d5336539b9acd44ec431a61f4e83700a0a392/examples/python-runtime-sandbox/main.py
- Example template: https://github.com/kubernetes-sigs/agent-sandbox/blob/1b7d5336539b9acd44ec431a61f4e83700a0a392/clients/python/agentic-sandbox-client/python-sandbox-template.yaml

**SWE-ReX @ `5c995c365dfb1fd5bc56fda688be5d8538f9931f`**
- Docker deployment: https://github.com/SWE-agent/SWE-ReX/blob/5c995c365dfb1fd5bc56fda688be5d8538f9931f/src/swerex/deployment/docker.py
- Server: https://github.com/SWE-agent/SWE-ReX/blob/5c995c365dfb1fd5bc56fda688be5d8538f9931f/src/swerex/server.py
