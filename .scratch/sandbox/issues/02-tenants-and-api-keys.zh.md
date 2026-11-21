# 02 · Tenant、API key 和管理命令

> [English](02-tenants-and-api-keys.md) | 中文

**Blocked by**: 01 · **里程碑**: M1

## 做什么
- 在 `controlplane` 里加管理用的命令行子命令：创建 Tenant、签发和吊销 API key
- 公共 API 全部要求带 API key；每个 Sandbox 都属于唯一一个 Tenant

## 验收
- [ ] 不带 key、key 错误、key 已吊销，都返回 401
- [ ] Tenant A 看不到，也不能操作 Tenant B 的 Sandbox（返回 404，不是 403）
- [ ] 数据库里只存 key 的哈希，不存明文
