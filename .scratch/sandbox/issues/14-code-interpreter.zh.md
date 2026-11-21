# 14 · 代码解释器

> [English](14-code-interpreter.md) | 中文

**Blocked by**: 13

## 做什么
- 由 Guest Agent 转发给 Template 里的 Jupyter kernel
- 每个 Sandbox 有一个默认 Execution Context，也可以显式创建更多个
- 输出包括文本、错误和 PNG 图片

## 验收
- [ ] `x = 1` 之后再执行 `x + 1`，得到 2
- [ ] 两个 Execution Context 之间的变量互不影响
- [ ] 用 matplotlib 画图，能返回 PNG
- [ ] 在没有 ipykernel 的 Template 上调用，明确报错"不支持"
