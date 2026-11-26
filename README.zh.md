# gpubox

> [English](README.md) | 中文

一个学习项目：给 AI agent 用的 sandbox 平台，用 Go 写。agent 通过 API 创建一个用完即弃的隔离环境，在里面运行命令。底下的隔离技术可以替换。这个项目想回答的问题是：Docker、gVisor 和 Firecracker 在启动时间、资源开销和隔离强度上有什么不同，为什么。

- **第一阶段**（现在）：Mac 上的 Docker Desktop。
- **第二阶段**：GCP 上的 Firecracker。
- **之后**：GPU。

## 各部分怎么连起来

```
客户端 ──REST :8080──▶ Control Plane (SQLite) ──gRPC，unix socket──▶ Host Agent ──docker──▶ 容器
   │                                                                     ▲                  └ Guest Agent（PID 1）
   └───────────── Exec，直接发给 Host 数据代理 :8081 ─────────────────────┘
```

- **Control Plane**：公共 API，以及所有记录（创建、查询、删除）。
- **Host Agent**：每台 Host 一个。它通过 Runtime 运行 Sandbox，并提供 Exec 用的数据代理（ADR 0007）。
- **Guest Agent**：每个 Sandbox 里的 PID 1，以只读方式挂载进去，通过一条长期的 `docker exec` 通道访问。

## 快速上手

需要 Go 1.26、Docker Desktop，示例还要用到 `jq`。每个服务在单独的终端里启动。

```bash
make build
```

```bash
./bin/hostagent
```

```bash
./bin/controlplane
```

然后创建一个 Sandbox，在里面运行命令：

```bash
R=$(curl -s -X POST localhost:8080/v1/sandboxes -d '{"template":"alpine:3.20"}'); ID=$(echo "$R" | jq -r .id); TOK=$(echo "$R" | jq -r .access_token)
```

```bash
curl -s -X POST localhost:8081/v1/sandboxes/$ID/exec -H "Authorization: Bearer $TOK" -d '{"command":"echo hi; whoami"}' | jq .
```

```bash
curl -s -X DELETE localhost:8080/v1/sandboxes/$ID | jq .
```

所有字段，以及用 `Accept: text/event-stream` 拿流式输出，见 [docs/api.zh.md](docs/api.zh.md)。

## 测试

```bash
make test
```

```bash
make test-docker
```

`make test` 不需要 Docker。`make test-docker` 对真实的 Docker 运行 `e2e/`。

## 目录

| 路径 | 内容 |
|---|---|
| `cmd/` | 三个程序：`controlplane`、`hostagent`、`guestagent` |
| `internal/` | 它们的代码，以及 `runtime/docker` |
| `proto/`、`gen/` | gRPC 定义和生成的代码（`make proto`，需要 `buf`） |
| `e2e/` | 对 Docker 的端到端测试 |
| `docs/adr/` | 架构决策 |
| `docs/research/` | 这些决策背后的调研 |
| `.scratch/sandbox/` | spec 和任务（[README](.scratch/sandbox/issues/README.zh.md)） |
| `GLOSSARY.md` | 领域术语，命名以它为准 |

每份文档都有英文（`X.md`，以它为准）和中文（`X.zh.md`）两份。

## 进度

第一阶段，里程碑 M1。已完成：

- **00**：Mac 上的实验。
- **01**：创建 → Exec → 销毁。
- **04**：流式 Exec、Exec 选项和进程组。

下一步：16（加固）、05（文件）、02（Tenant 和 API key）。
