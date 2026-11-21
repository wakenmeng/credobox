# 10 · 对账

> [English](10-reconciliation.md) | 中文

**Blocked by**: 01

## 做什么
Host Agent 定期上报本机实际在跑的 Sandbox，Control Plane 拿它和数据库对比：机器上有、数据库里没有的，销毁；数据库里有、机器上没有的，标记为失败。

## 验收
- [ ] 手动 `docker rm -f` 删掉一个 Sandbox，它会在一个对账周期内被标记为失败
- [ ] 手动起一个带平台标签的容器，它会被清理掉
- [ ] Host Agent 重启后，能重新接管还在跑的 Sandbox，不会误杀
- [ ] Control Plane 先写入 Starting 再调用 Host，每个容器都带有生命周期 ID 标签
- [ ] 陌生容器先给一段宽限期，再销毁
- [ ] 数据库读取失败时，这一轮跳过这台 Host，而不是把它的 Sandbox 都销毁
