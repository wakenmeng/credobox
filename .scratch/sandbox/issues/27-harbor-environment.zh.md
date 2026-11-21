# 27 · 给 Terminal-Bench 2.0 用的 Harbor 环境（第二阶段）

> [English](27-harbor-environment.md) | 中文

**Blocked by**: 06, 12, 13 · **阶段**: 2，在 x86_64 Linux 上

## 做什么
一个基于我们公共 API 的 Harbor `BaseEnvironment`（用 `-e module:Class` 传入），让 Terminal-Bench 2.0 能在我们的 Sandbox 上跑。TB2 的 89 道题都是单容器的，自带预构建的 amd64 镜像和 oracle 解法，所以不用 LLM 就能测试环境是否一致。超过一小时的 Exec 改用 Process。见[验证调研](../../../docs/research/verification-and-benchmarks.zh.md) §4。

## 验收
- [ ] 在 Docker Runtime 上跑一遍 oracle 作为基线，记录每道题的结果
- [ ] 跑一遍 nop：每道题都失败
- [ ] 在 gVisor 上跑同样两遍，得出和基线逐题比对的差异
- [ ] 先在 `package-registries` 和 `github` 预设下跑，再在全开模式下跑
