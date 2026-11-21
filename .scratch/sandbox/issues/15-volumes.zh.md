# 15 · Volume

> [English](15-volumes.md) | 中文

**Blocked by**: 03, 09 · **顺序**: 第一阶段末尾（多数厂商允许多挂载，而且好几家还在 beta）

## 做什么
- 创建、列出、删除 Volume；创建 Sandbox 时指定挂载到哪个目录（比如 `/data`）
- 同一时间只能挂在一个 Sandbox 上
- 在 Quota 里加上"Volume 总容量"，按存储量计入 Usage

## 验收
- [ ] Sandbox A 写入文件后销毁，Sandbox B 挂上同一个 Volume 能读到
- [ ] 尝试同时挂到两个 Sandbox 上会被拒绝
