# The platform creates the `user` account at Sandbox start

> English | [中文](0009-platform-creates-the-user-account.zh.md)

Execs run as `user` by default (spec §4), and later the Docker hardening (ticket 16) needs an ordinary account too. But Linux only knows numbers: a process has a uid, and a name such as `user` is just a line in the image's `/etc/passwd` that maps it to that uid and a home directory. `whoami` is a lookup of the current uid in that file. Official images such as `alpine:3.20` and `python:3.12-slim` have no `user` line, so "run as `user`" means nothing in them.

So **the Guest Agent creates the account**. On the first Init, before it starts the image's entrypoint, it looks for `user` in the image's `/etc/passwd`. If the name is there, it is used as it is. If not, the Guest Agent appends `user` to `/etc/passwd` and `/etc/group`, with the first uid from 1000 up that no line uses (and the same number as gid if that is free too, otherwise the first free gid from 1000 up), home `/home/user` and shell `/bin/sh`. It then creates `/home/user`, owned by that uid, mode 0750. It writes the files itself rather than running `adduser` or `useradd`, because which of those exists (if either) depends on the distribution. Every image keeps working unmodified (ADR 0003), and `whoami` prints `user` in every Sandbox.

What runs as whom:
- The image's entrypoint still runs as the image's USER, as Docker would run it (ticket 01).
- An Exec runs as `user` unless the request names another account. `root`, or any name or uid in the image's `/etc/passwd`, may be requested; the platform imposes no boundary between `user` and root inside a Sandbox (the Sandbox is the boundary).
- An Exec's default working directory is the account's home: `/home/user` for the default account, `/root` for root, `/` if the home does not exist.

Because the lines are written into the container's writable layer, Capture (`docker commit`) keeps them. That is harmless: the account holds no secret, and when a Sandbox starts from the captured Template the Guest Agent finds `user` already there and changes nothing.

## Considered Options

- **Require each Template to provide `user`** (checked when ticket 13 registers it): the platform never touches the image's files, but ordinary official images could no longer be used as they are, which breaks ADR 0003.
- **Fall back to the image's USER when there is no `user`**: the least work, but on `alpine` an Exec would run as root, `whoami` would print `root`, and the default identity would differ from Template to Template, so an agent could rely on neither.
