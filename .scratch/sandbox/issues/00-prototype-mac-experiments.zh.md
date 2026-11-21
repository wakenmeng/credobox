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
