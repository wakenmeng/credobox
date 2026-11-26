# 由平台在 Sandbox 启动时创建 `user` 账号

> [English](0009-platform-creates-the-user-account.md) | 中文

Exec 默认以 `user` 身份运行（spec §4），之后的 Docker 加固（任务 16）也需要一个普通账号。但 Linux 只认数字：进程有一个 uid，`user` 这样的名字只是镜像 `/etc/passwd` 里的一行，把名字映射到 uid 和 home 目录。`whoami` 就是拿当前 uid 去这个文件里查。`alpine:3.20`、`python:3.12-slim` 这类官方镜像里没有 `user` 这一行，所以在它们里面“以 `user` 运行”没有意义。

因此 **由 Guest Agent 创建这个账号**。第一次 Init 时，在启动镜像的 entrypoint 之前，它先去镜像的 `/etc/passwd` 里找 `user`。如果已经有这个名字，就原样使用。如果没有，Guest Agent 就往 `/etc/passwd` 和 `/etc/group` 里追加 `user`：uid 取从 1000 起第一个没被任何一行用掉的数字（gid 如果这个数字也空闲就取同一个，否则取从 1000 起第一个空闲的 gid），home 是 `/home/user`，shell 是 `/bin/sh`。然后创建 `/home/user`，属主是这个 uid，权限 0750。它自己写这些文件，而不是调用 `adduser` 或 `useradd`，因为镜像里有哪一个（甚至一个都没有）取决于发行版。这样每个镜像都能原样使用（ADR 0003），每个 Sandbox 里 `whoami` 都打印 `user`。

谁以什么身份运行：
- 镜像的 entrypoint 仍然以镜像的 USER 运行，和 Docker 的做法一样（任务 01）。
- Exec 以 `user` 运行，除非请求里指定了别的账号。可以指定 `root`，或者镜像 `/etc/passwd` 里的任何名字或 uid；在 Sandbox 内部，平台不在 `user` 和 root 之间设边界（边界是 Sandbox 本身）。
- Exec 默认的工作目录是该账号的 home：默认账号是 `/home/user`，root 是 `/root`，home 不存在时是 `/`。

因为这几行写在容器的可写层里，Capture（`docker commit`）会把它们保留下来。这没有坏处：账号里没有任何秘密，而从捕获出来的 Template 启动 Sandbox 时，Guest Agent 发现 `user` 已经存在，什么都不改。

## Considered Options

- **要求每个 Template 自带 `user`**（任务 13 注册时检查）：平台从不动镜像里的文件，但普通的官方镜像就不能原样使用了，违背 ADR 0003。
- **没有 `user` 时回退到镜像的 USER**：最省事，但在 `alpine` 上 Exec 会以 root 运行，`whoami` 打印 `root`，而且默认身份随 Template 而变，agent 两头都没法依赖。
