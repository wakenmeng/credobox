# 00 · 原型：Mac 实验 E1–E8

> [English](00-prototype-mac-experiments.md) | 中文

**Blocked by**: 无 · **里程碑**: M0

## 做什么
用 `/prototype` 在这台 Mac 上跑完 [Docker/gVisor 调研](../../../docs/research/phase1-docker-gvisor-on-mac.zh.md) §5 里的实验 E1–E8。这些是文档定不下来的问题，比如：unix socket 能不能双向穿过 VirtioFS；Sandbox 能不能连到 Mac 上只监听 127.0.0.1 的服务；`--storage-opt size` 是不是被悄悄忽略；`--internal` 加 `isolated` 的网络是否真的没有出口路由。一次性的代码放在 `prototype/mac-experiments` 分支上。

## 验收
- [ ] 每个实验都有记录在案的结果（命令、输出、Docker Desktop 和 Engine 的版本）
- [ ] 凡是和 spec 或 ADR 矛盾的结果，都写回这些文档，中英两版
- [ ] 任务 01 根据结果写明用哪种通道

## 学什么
Docker Desktop 的 Linux 虚拟机、VirtioFS 和它的网络栈是怎么配合的。

## 状态（2026-11-21）

E1–E7 已在 Docker Desktop 4.87.0 上跑完；E8（Lima 里的 gVisor）推迟到第二阶段。脚本、原始日志和结果在 `prototype/mac-experiments` 分支（提交 `603dbb0`）的 `prototype/mac-experiments/RESULTS.zh.md`。

- 被推翻的：unix socket 跨不过 macOS 和虚拟机之间的边界（ADR 0002 的修订）；带显式配置 commit 去不掉 Env 和 Entrypoint（ADR 0006 的修订、任务 22）。这两项已于 2026-11-21 决定：第一阶段的通道是一条长连接的 `docker exec -i` 流（ADR 0002）；平台相关的环境变量从不进入容器配置（ADR 0006、0008；任务 01、13、22、23）。
- 已更正的：spec §7 关于磁盘的说法。
- 得到确认的：隔离模式的 internal 网络（以及只用 `--internal` 会暴露虚拟机自己的监听端口）、代理和解析器的方案、冻结、最小的 capabilities 组合、non-dumpable。
