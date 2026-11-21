# 13 · Template

> [English](13-templates.md) | 中文

**Blocked by**: 02

## 做什么
- `POST /v1/templates` 按镜像地址注册；Template 分平台内置的公共 Template 和归某个 Tenant 私有的 Template
- 内置一个 `python` Template，预装好 ipykernel

## 验收
- [ ] 一个普通的官方镜像（比如 `python:3.12-slim`）不用改就能当 Template 用（ADR 0003）
- [ ] Tenant A 用不了 Tenant B 的私有 Template
