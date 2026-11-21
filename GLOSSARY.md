# Agent Sandbox

> English | [中文](GLOSSARY.zh.md)

A code-execution platform for AI agents. Through an API, agents create disposable isolated environments and use them to run commands, execute code, and read and write files. The agent can run outside and use the Sandbox as a tool, or run inside the Sandbox itself. The isolation technology underneath can be swapped.

## Core objects

**Tenant**:
The principal that is billed and given a Quota. It holds the API keys, and every Sandbox belongs to exactly one Tenant.
_Avoid_: User, account, customer, team

**Sandbox**:
An isolated execution environment owned by a Tenant. It is created from a Template and has its own resource spec and lifecycle.
_Avoid_: Container, VM, box, environment, instance

**Template**:
The starting environment for a Sandbox, registered as an OCI image reference and shared as the single source of truth by every Runtime. A Template is either a built-in public Template or a private Template owned by one Tenant.
_Avoid_: Image, environment, base, snapshot

**Runtime**:
The isolation technology that turns a Template into a running Sandbox, such as Docker, gVisor or Firecracker.
_Avoid_: Backend, provider, driver, engine

## System components

**Control Plane**:
The central component behind the public API. It owns Tenants, Quotas, scheduling and Usage, and decides which Host each Sandbox runs on.
_Avoid_: Server, API server, orchestrator, master

**Host**:
A machine that actually runs Sandboxes. It reports its CPU, memory and GPU capacity to the Control Plane.
_Avoid_: Node, worker, machine, server

**Host Agent**:
The process on each Host that manages that Host's Sandboxes through a Runtime.
_Avoid_: Node agent, daemon, worker, kubelet

**Guest Agent**:
The process inside each Sandbox and its only operational entry point. It runs commands, reads and writes files, runs code, and forwards ports.
_Avoid_: envd, in-sandbox daemon, sidecar

**Egress Proxy**:
The process on each Host, separate from the Host Agent, that all of a Sandbox's outbound traffic passes through. It enforces the Egress Policy and injects Secrets. Together with the platform's DNS resolver, it is the only Host-side component a Sandbox can connect to.
_Avoid_: Firewall, gateway, NAT, outbound proxy

## Lifecycle

**Init**:
The step in which the Host Agent gives a starting, restarted or restored Sandbox its identity: a fresh token, entropy, the clock, environment variables and its CA. A Sandbox becomes Running only after Init completes.
_Avoid_: Bootstrap, provisioning, handshake

**Starting**:
The Sandbox is being created, or its Init has not completed yet. It cannot run commands and accrues no Usage.
_Avoid_: Creating, pending, booting

**Running**:
The Sandbox is running normally and holds CPU and memory.

**Frozen**:
The Sandbox's processes are frozen in place. Memory stays allocated, and the Sandbox can be unfrozen within milliseconds. On Docker this is what `docker pause` does; on gVisor the freeze happens inside gVisor's own kernel.
_Avoid_: Paused, suspended

**Stopped**:
The Sandbox's processes have ended and its memory is released, but its filesystem and Sandbox ID are kept. Starting it again boots fresh from that filesystem. An idle Sandbox becomes Stopped rather than Terminated.
_Avoid_: Shut down, halted, archived, suspended

**Hibernated**:
The Sandbox's memory and process state have been saved to disk, and its CPU and memory released. Resuming loads that state back from disk. It is like Stopped, except that memory and processes are kept too. This is what E2B and Daytona call "pause".
_Avoid_: Paused, snapshotted, suspended, archived

**Fork**:
A copy of a Sandbox's full state (memory, processes and filesystem) into one or more new Sandboxes. A copy of the filesystem alone is not a Fork; that is a Capture.
_Avoid_: Clone, copy, branch, commit

**Capture**:
Saving a Sandbox's current filesystem as a new private Template. Memory and processes are not saved.
_Avoid_: Snapshot, commit, image capture, checkpoint

**Terminated**:
The Sandbox has been destroyed and cannot be resumed. It may have been deleted, timed out, failed, or been cleaned up by reconciliation. It records a termination reason: `deleted` (an API call destroyed it), `failed` (creation or the Sandbox itself failed) or `start_timeout` (it was not Running within the create timeout), plus a human-readable detail. Later tickets add their own reasons, such as `disk_limit`.
_Avoid_: Deleted, dead

## Working in a Sandbox

**Access Token**:
A token scoped to one Sandbox, returned when it is created, that authorizes the data-path calls (Exec, files, Processes, code interpreter) sent directly to that Sandbox's Host.
_Avoid_: API key, session token

**Exec**:
A foreground command run in a Sandbox until it exits or times out. Its result comes back either all at once or as a stream.
_Avoid_: Run, command, job

**Process**:
A long-running program started in the background inside a Sandbox. It has its own ID, can be listed, read and killed, and is not bound by the per-command timeout.
_Avoid_: Background job, daemon, task

**Volume**:
A persistent filesystem owned by a Tenant. Its lifecycle is independent of any Sandbox, and it is mounted in at most one Sandbox at a time.
_Avoid_: Disk, storage, persistent filesystem, mount

**Execution Context**:
One independent Python state in a Sandbox's code interpreter. Variables persist across calls, and Execution Contexts do not affect each other.
_Avoid_: Kernel, session, notebook

**Exposed Port**:
A service port inside a Sandbox that is reachable from outside at its own address through the platform proxy. By default it requires a token.
_Avoid_: Port mapping, preview URL, tunnel

**Egress Policy**:
The rule for a Sandbox's outbound network access. It is one of three modes: blocked, domain allowlist, or fully open.
_Avoid_: Firewall, network policy, internet access

**Allowlist Preset**:
A named, reusable set of domains maintained by the platform, such as `llm-providers`, `package-registries` or `github`, that an Egress Policy can reference.
_Avoid_: Profile, group, essential services

**Secret**:
A credential a Tenant stores on the platform, bound to the destination hosts allowed to receive it. Inside a Sandbox it appears only as a placeholder, and the Egress Proxy swaps in the real value on the way out.
_Avoid_: API key, environment variable, credential, token

## Metering and quotas

**Quota**:
The resource limits a Tenant can use: concurrent Sandboxes, total vCPUs, total memory, total Volume capacity, maximum Sandbox lifetime, and creation rate.
_Avoid_: Limit, plan, allowance

**Usage**:
The resources consumed over a period of time, without any price attached. A Sandbox is metered by its state: Running counts vCPU-seconds and memory GB-seconds, Frozen counts memory GB-seconds, and Stopped and Hibernated count storage. Every Sandbox also counts egress bytes, and Volumes and captured Templates count storage.
_Avoid_: Billing, consumption, metering record, cost
