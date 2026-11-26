# Public API

> English | [中文](api.zh.md)

What is built so far. [Spec §4](../.scratch/sandbox/spec.md) lists the full set of capabilities; this page documents the endpoints that exist, field by field. The control path is the Control Plane; the data path is the Sandbox's Host, reached at the `data_endpoint` that create returns, with the Sandbox's Access Token (ADR 0007).

## Control path

### `POST /v1/sandboxes`

Creates a Sandbox and blocks until it is Running, or Terminated.

```json
{"template": "alpine:3.20", "env": {"PROJECT": "demo"}}
```

| Field | | |
|---|---|---|
| `template` | required | The Template's OCI image reference. |
| `env` | optional | Default environment variables for every process in the Sandbox: the image's entrypoint and every Exec. They are delivered in Init, never written into the container config (ADR 0008). **Don't put secrets here**: the values are stored in plain text in the Control Plane's database for as long as the Sandbox lives (and cleared when it is Terminated), and any process in the Sandbox can read them. Credentials belong in Secrets (ticket 23), which never enter the Sandbox. |

`201` returns the Sandbox with `data_endpoint` and `access_token`; the token is shown only here. A failed or timed-out create returns `502` or `504` with the Sandbox Terminated and its `termination_reason`. A bad body or variable name is `400`.

### `GET /v1/sandboxes/{id}`, `DELETE /v1/sandboxes/{id}`

Read a Sandbox; destroy it (it becomes Terminated with reason `deleted`).

## Data path

Every call carries `Authorization: Bearer <access_token>`. A missing or wrong token is `401`, an unknown Sandbox `404`.

### `POST {data_endpoint}/v1/sandboxes/{id}/exec`

Runs a foreground command with `/bin/sh -c`.

```json
{"command": "pip install -r requirements.txt", "user": "root", "cwd": "/srv/app",
 "env": {"PIP_NO_CACHE_DIR": "1"}, "timeout_seconds": 300}
```

| Field | Default | |
|---|---|---|
| `command` | required | Passed to `/bin/sh -c`. |
| `user` | `user` | The account to run as: `user`, `root`, or any name or uid in the image's `/etc/passwd`. Every Sandbox has `user`; the platform adds it at start if the image has none (ADR 0009). |
| `cwd` | the account's home | `/home/user` for `user`, `/root` for root, `/` if the home doesn't exist. The directory is entered as the account, with its permissions. |
| `env` | none | Added on top of the image's Env and the Sandbox's `env`; on a clash the Exec's value wins. |
| `timeout_seconds` | 60 | 1 to 3600. For anything longer, use a Process. |

A bad option (timeout out of range, unknown user, missing directory, invalid variable name) is `400`, and nothing runs.

#### Buffered response (the default)

`200` once the command has finished:

```json
{"stdout": "...", "stderr": "...", "exit_code": 0, "timed_out": false,
 "stdout_truncated": false, "stderr_truncated": false,
 "stdout_omitted_bytes": 0, "stderr_omitted_bytes": 0}
```

Each of stdout and stderr keeps at most 1 MiB. Up to that, all of it is in `stdout` (`stderr`). Beyond it, the stream keeps its **first 256 KiB** in `stdout` and its **last 768 KiB** in `stdout_tail` (`stderr_tail`), and drops the middle: `stdout_truncated` is `true` and `stdout_omitted_bytes` says how many bytes are missing between the two. The tail gets the larger share because the end of an output is where errors and summaries usually are. Both pieces are cut on UTF-8 character boundaries. The dropped part is still read, so the command runs to completion. For all of the output, stream it.

```json
{"stdout": "Collecting ...", "stdout_tail": "... 3 failed, 120 passed",
 "stdout_truncated": true, "stdout_omitted_bytes": 7340032, "exit_code": 1, ...}
```

#### Streamed response (`Accept: text/event-stream`)

`200` with Server-Sent Events, each sent as soon as it happens, with no cap on output:

```
event: start
data: {"pid":42}

event: stdout
data: {"text":"Collecting requests\n"}

event: stderr
data: {"text":"WARNING: ...\n"}

event: exit
data: {"exit_code":0,"timed_out":false}
```

| Event | Data |
|---|---|
| `start` | `pid`: the shell's process ID, which is also its process group ID. Sent once the command has started. |
| `stdout`, `stderr` | `text`: the next piece of that stream, as UTF-8. A multi-byte character is never split across events. Bytes that are not valid UTF-8 become U+FFFD. |
| `exit` | `exit_code` and `timed_out`. Always the last event of a finished command. |
| `error` | `error`: the Guest Agent failed midway; there is no `exit`. |

Errors found before the command starts (the `400`s above) come back as an ordinary JSON error, not as a stream.

#### Process group, timeout and disconnect

Each Exec's shell leads **its own process group**, and everything it starts stays in that group. (A process group is the unit that `kill(-pgid)` reaches. Killing only the shell would leave its children, for example a server it started with `&`, running with nobody to answer for them.)

- **Timeout.** When `timeout_seconds` passes, the whole process group is killed with SIGKILL. The result is still a normal response with `"timed_out": true`, and the output produced before the kill is kept. **Trust `timed_out`, not `exit_code`**: `exit_code` is the shell's own, which is 137 (128 + SIGKILL) when the kill ended the shell, but can be anything, often 0, when the shell had already exited and only a background child holding stdout or stderr open kept the Exec going (see "Normal exit" below). For example, `server &` without redirecting its output returns `"timed_out": true, "exit_code": 0` after the full timeout.
- **Client disconnect.** If the client closes the connection before the Exec has finished, buffered or streamed, the Host data proxy cancels the Exec and the Guest Agent kills its whole process group at once. Nothing keeps running, and the output is gone: an Exec is not resumable. To run something that must survive a dropped connection, use a Process.
- **Normal exit.** The Exec ends when the shell has exited **and** stdout and stderr have closed. A background child that keeps them open (`server &`) keeps the Exec going until it exits or the timeout kills the group. A background child whose output is redirected (`server >/tmp/log 2>&1 &`) lets the Exec end at once and keeps running afterwards.
- **Limit.** A process that deliberately leaves the group (`setsid`, or a daemon that double-forks into a new session) is not reached by the group kill.
