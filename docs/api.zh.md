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
| `env` | 可选 | Sandbox 里每个进程的默认环境变量：镜像的 entrypoint 和每条 Exec 都有。它们在 Init 中下发，从不写进容器配置（ADR 0008）。 |

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
 "stdout_truncated": false, "stderr_truncated": false}
```

stdout 和 stderr 各保留前 1 MiB。其余部分照样读出来然后丢弃，命令仍会跑完，`*_truncated` 表示有内容被截掉。输出更多时请用流式。

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

- **超时。** `timeout_seconds` 到了，整个进程组被 SIGKILL 杀掉。结果仍是正常响应：`"timed_out": true`，`exit_code` 通常是 137（128 + SIGKILL）。被杀之前的输出都保留。
- **客户端断开。** Exec 结束前客户端关闭连接（无论缓冲还是流式），Host data proxy 就会取消这条 Exec，Guest Agent 立即杀掉它的整个进程组。不会有任何东西继续运行，输出也没了：Exec 不能续接。必须在连接断开后继续运行的任务，请用 Process。
- **正常结束。** shell 已经退出，**并且** stdout 和 stderr 都已关闭，Exec 才结束。一个后台子进程如果还开着它们（`server &`），Exec 会一直等到它退出，或者超时把整个组杀掉。输出被重定向的后台子进程（`server >/tmp/log 2>&1 &`）不会拖住 Exec，Exec 结束后它继续运行。
- **局限。** 主动离开进程组的进程（`setsid`，或者 double-fork 进新 session 的 daemon）不会被组杀打到。
