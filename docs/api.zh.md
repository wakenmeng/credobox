# 公共 API

> [English](api.md) | 中文

这里记录已经实现的部分。完整的能力清单见 [spec §4](../.scratch/sandbox/spec.zh.md)；本页逐个字段说明已有的接口。控制通路是 Control Plane；数据通路是 Sandbox 所在的 Host，地址是创建时返回的 `data_endpoint`，用该 Sandbox 的 Access Token 鉴权（ADR 0007）。

## 控制通路

### `POST /v1/sandboxes`

创建一个 Sandbox，阻塞到它变成 Running 或 Terminated 为止。

```json
{"template": "alpine:3.20", "env": {"PROJECT": "demo"}}
```

| 字段 | | |
|---|---|---|
| `template` | 必填 | Template 的 OCI 镜像引用。 |
| `env` | 可选 | Sandbox 里每个进程的默认环境变量：镜像的 entrypoint 和每条 Exec 都有。它们在 Init 中下发，从不写进容器配置（ADR 0008）。**不要把秘密放在这里**：Sandbox 存活期间，这些值以明文存在 Control Plane 的数据库里（Sandbox 变成 Terminated 时清掉），而且 Sandbox 里的任何进程都能读到。凭据应该用 Secret（任务 23），它们从不进入 Sandbox。 |

`201` 返回 Sandbox，带 `data_endpoint` 和 `access_token`；token 只在这里出现一次。创建失败或超时返回 `502` 或 `504`，Sandbox 变成 Terminated 并带 `termination_reason`。请求体或变量名不合法返回 `400`。

### `GET /v1/sandboxes/{id}`、`DELETE /v1/sandboxes/{id}`

读取一个 Sandbox；销毁它（变成 Terminated，原因是 `deleted`）。

## 数据通路

每个调用都带 `Authorization: Bearer <access_token>`。token 缺失或不对返回 `401`，Sandbox 不存在返回 `404`。

### `POST {data_endpoint}/v1/sandboxes/{id}/exec`

用 `/bin/sh -c` 运行一条前台命令。

```json
{"command": "pip install -r requirements.txt", "user": "root", "cwd": "/srv/app",
 "env": {"PIP_NO_CACHE_DIR": "1"}, "timeout_seconds": 300}
```

| 字段 | 默认值 | |
|---|---|---|
| `command` | 必填 | 交给 `/bin/sh -c`。 |
| `user` | `user` | 以哪个账号运行：`user`、`root`，或镜像 `/etc/passwd` 里的任何名字或 uid。每个 Sandbox 都有 `user`；镜像里没有的话，平台在启动时加上（ADR 0009）。 |
| `cwd` | 该账号的 home | `user` 是 `/home/user`，root 是 `/root`，home 不存在时是 `/`。进入目录时用的是该账号的身份和权限。 |
| `env` | 无 | 叠加在镜像的 Env 和 Sandbox 的 `env` 之上；重名时以 Exec 的值为准。 |
| `timeout_seconds` | 60 | 1 到 3600。更长的任务用 Process。 |

选项不合法（超时越界、用户不存在、目录不存在、变量名不合法）返回 `400`，什么都不会运行。

#### 缓冲响应（默认）

命令结束后返回 `200`：

```json
{"stdout": "...", "stderr": "...", "exit_code": 0, "timed_out": false,
 "stdout_truncated": false, "stderr_truncated": false,
 "stdout_omitted_bytes": 0, "stderr_omitted_bytes": 0}
```

stdout 和 stderr 各自最多保留 1 MiB。不超过时，全部内容都在 `stdout`（`stderr`）里。超过时，**前 256 KiB** 放在 `stdout`，**最后 768 KiB** 放在 `stdout_tail`（`stderr_tail`），中间部分丢弃：`stdout_truncated` 为 `true`，`stdout_omitted_bytes` 表示两段之间少了多少字节。结尾分得多，是因为错误信息和汇总通常在输出的最后。两段都在 UTF-8 字符边界上切开。被丢弃的部分照样会读出来，所以命令仍会跑完。要拿到全部输出，请用流式。

```json
{"stdout": "Collecting ...", "stdout_tail": "... 3 failed, 120 passed",
 "stdout_truncated": true, "stdout_omitted_bytes": 7340032, "exit_code": 1, ...}
```

#### 流式响应（`Accept: text/event-stream`）

返回 `200`，内容是 Server-Sent Events，每个事件一发生就发出，输出没有上限：

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

| 事件 | 数据 |
|---|---|
| `start` | `pid`：shell 的进程号，也就是它的进程组号。命令启动后发出。 |
| `stdout`、`stderr` | `text`：该流的下一段，UTF-8 文本。一个多字节字符不会被拆到两个事件里。不是合法 UTF-8 的字节变成 U+FFFD。 |
| `exit` | `exit_code` 和 `timed_out`。命令结束时总是最后一个事件。 |
| `error` | `error`：Guest Agent 中途出错；不会再有 `exit`。 |

命令启动前就发现的错误（上面那些 `400`）照常以 JSON 错误返回，而不是流。

#### 进程组、超时和断开

每条 Exec 的 shell 都是**自己进程组**的组长，它启动的所有进程都留在这个组里。（进程组是 `kill(-pgid)` 能一次打到的单位。只杀 shell 的话，它的子进程，比如用 `&` 启动的 server，会继续运行，而且没人管。）

- **超时。** `timeout_seconds` 到了，整个进程组被 SIGKILL 杀掉。结果仍是正常响应，带 `"timed_out": true`，被杀之前的输出都保留。**以 `timed_out` 为准，不要看 `exit_code`**：`exit_code` 是 shell 自己的退出码。如果是这次 kill 结束了 shell，它是 137（128 + SIGKILL）；但如果 shell 早已退出，只是一个开着 stdout 或 stderr 的后台子进程拖住了 Exec（见下面的“正常结束”），它可以是任何值，常常是 0。比如不重定向输出的 `server &`，会在等满超时后返回 `"timed_out": true, "exit_code": 0`。
- **客户端断开。** Exec 结束前客户端关闭连接（无论缓冲还是流式），Host data proxy 就会取消这条 Exec，Guest Agent 立即杀掉它的整个进程组。不会有任何东西继续运行，输出也没了：Exec 不能续接。必须在连接断开后继续运行的任务，请用 Process。
- **正常结束。** shell 已经退出，**并且** stdout 和 stderr 都已关闭，Exec 才结束。一个后台子进程如果还开着它们（`server &`），Exec 会一直等到它退出，或者超时把整个组杀掉。输出被重定向的后台子进程（`server >/tmp/log 2>&1 &`）不会拖住 Exec，Exec 结束后它继续运行。
- **局限。** 主动离开进程组的进程（`setsid`，或者 double-fork 进新 session 的 daemon）不会被组杀打到。
