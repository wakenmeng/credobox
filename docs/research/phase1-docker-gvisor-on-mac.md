# Phases 1–2 on a Mac: the Docker Runtime on Docker Desktop, and gVisor

> English | [中文](phase1-docker-gvisor-on-mac.zh.md)

> Research date: 2026-11-21. Every link was accessed on that day. The Docker Desktop facts are for **Docker Desktop 4.100.0 (released 2026-11-19)** and the versions before it, as listed in the [release notes](https://docs.docker.com/desktop/release-notes/). Docker Desktop ships often and has changed exactly the behaviours this note depends on (unix sockets over VirtioFS changed in 4.86.0, in August 2026), so check `docker version` and the release notes before relying on anything here.
> Primary sources only: Docker docs and release notes, the Docker blog, moby/runc/gVisor source on GitHub, gVisor docs, and Linux kernel docs. Secondary sources are labelled **(secondary)**. Anything marked **[Inference]** is my own conclusion, not something a source says. Terms follow the [GLOSSARY](../../GLOSSARY.md).
> I ran no Docker commands. Questions that only an experiment can settle are listed in §5, with the commands to run.

---

## 1. Summary

- **The unix-socket channel of ADR 0002 should work on current Docker Desktop, but only recently, and only with VirtioFS.** Docker Desktop 4.86.0 (2026-08-10) says "AF_UNIX sockets shared over VirtioFS now work in both directions (host to guest and guest to host)" ([release notes](https://docs.docker.com/desktop/release-notes/)). The older osxfs docs said socket files did not cross the hypervisor at all ([osxfs doc, archived](https://github.com/docker/docker.github.io/blob/a17a06c6a15028011250900c49a1b59620cf1c65/docker-for-mac/osxfs.md)). Pin a minimum Docker Desktop version and test it (§5 E1). Under gVisor the same channel needs `runsc --host-uds=create`, because the default is `none` ([runsc flags source](https://github.com/google/gvisor/blob/master/runsc/config/flags.go)).
- **On the default network a Sandbox can reach the Mac and everything the Mac can reach.** Outbound traffic is re-originated by the `com.docker.backend` process on the Mac ([networking](https://docs.docker.com/desktop/features/networking/)), and `host.docker.internal` "Resolves to the internal IP address of your host" ([how-tos](https://docs.docker.com/desktop/features/networking/networking-how-tos/)). No document says whether services bound only to `127.0.0.1` are reachable, so §5 E2 tests it. The fix is the same either way: never put a Sandbox on a network that has a default route.
- **Minimal enforceable egress design without macOS iptables:** attach each Sandbox only to an `--internal` bridge network created with gateway mode `isolated` (Engine ≥ 28.0). Run the Egress Proxy and the DNS resolver as **containers inside the Docker VM**, each attached to that internal network and to a normal egress network. An internal network has "no default route" ([network create](https://docs.docker.com/reference/cli/docker/network/create/)), so the proxy is not optional: anything that skips it fails. Since Engine 26.0, the embedded DNS does not forward to external servers for internal-only containers ([26.0 notes](https://docs.docker.com/engine/release-notes/26.0/)), and since 26.1.3 it can use a resolver *on* the internal network ([26.1 notes](https://docs.docker.com/engine/release-notes/26.1/)). `--dns <resolver IP>` therefore gives "platform resolver only".
- **There is no disk limit on Docker Desktop by default.** The claim's conclusion holds, but its stated reason doesn't. moby's overlay2 driver refuses `--storage-opt size` unless the backing filesystem is xfs mounted with `pquota` ([overlay.go](https://github.com/moby/moby/blob/master/daemon/graphdriver/overlay2/overlay.go)). Docker Desktop also defaults to the containerd image store ([settings](https://docs.docker.com/desktop/settings-and-maintenance/settings/)), and in that code path moby passes `StorageOpt` along but prepares the snapshot without any size ([image_snapshot.go](https://github.com/moby/moby/blob/master/daemon/containerd/image_snapshot.go)).
- **Freeze:** with runc, `docker pause` writes `cgroup.freeze` (the cgroup v2 freezer) ([opencontainers/cgroups fs2/freezer.go](https://github.com/opencontainers/cgroups/blob/main/fs2/freezer.go)). **With gVisor it does not.** `runsc pause` calls the Sentry's own `kernel.Pause()` ([runsc boot/controller.go](https://github.com/google/gvisor/blob/master/runsc/boot/controller.go)), so the GLOSSARY's "This is what `docker pause` does" is Runtime-specific.
- **Capture with `docker commit` works on the Mac, but out of the box it leaks Sandbox state into the Template.** moby uses the *container's* config (Env, Entrypoint, User, …) as the new image's config ([daemon/commit.go](https://github.com/moby/moby/blob/master/daemon/commit.go)). That bakes in Secret placeholders, proxy variables and the injected entrypoint. **[Inference]** Any per-Sandbox CA written into the trust store would be baked in too, which breaks ADR 0006's "one CA per Sandbox". Volumes are excluded ([commit](https://docs.docker.com/reference/cli/docker/container/commit/)). Under gVisor, the default rootfs overlay keeps changes out of the host upper layer, so `docker commit` would miss them unless `--overlay2=none` is used.
- **Hardening:** `--cap-drop=ALL`, `no-new-privileges` and `--pids-limit` are ordinary engine features. **[Inference]** They should work on Docker Desktop. **[Inference]** `--cap-drop=ALL` will break a root Guest Agent that switches to `user` and manages files, so add back a minimal set. userns-remap and the containerd image store are mutually exclusive ([containerd store](https://docs.docker.com/engine/storage/containerd/)). Enhanced Container Isolation needs Docker Business ([settings](https://docs.docker.com/desktop/settings-and-maintenance/settings/)).
- **gVisor in phase 2:** gVisor supports x86_64 and ARM64 on Linux 5.6+ ([install](https://gvisor.dev/docs/user_guide/install/)). Its default platform, systrap, "is a better choice when running inside a VM" ([platforms](https://gvisor.dev/docs/architecture_guide/platforms/)), so a Lima, Colima or OrbStack Linux VM on Apple Silicon is viable without nested virtualization. Docker Desktop has no documented way to add `runsc`. Of the agent-relevant gaps, Docker-in-gVisor needs special flags ([docker-in-gvisor](https://gvisor.dev/docs/tutorials/docker-in-gvisor/)), and `perf_event_open` and `userfaultfd` are unimplemented on arm64 ([arm64 syscalls](https://gvisor.dev/docs/user_guide/compatibility/linux/arm64/)).

---

## 2. Findings by question

### Q1. The Host Agent ↔ Guest Agent channel (ADR 0002)

**What the sources say**

- The legacy osxfs docs (2016–2017 era) said: "Socket files and named pipes only transmit between containers and between macOS processes"; "no transmission across the hypervisor is supported, yet" ([osxfs.md, archived](https://github.com/docker/docker.github.io/blob/a17a06c6a15028011250900c49a1b59620cf1c65/docker-for-mac/osxfs.md)). **Outdated.**
- Docker Desktop 4.86.0 (2026-08-10), under "For all platforms": "AF_UNIX sockets shared over VirtioFS now work in both directions (host to guest and guest to host)." ([release notes](https://docs.docker.com/desktop/release-notes/))
- Docker Desktop 4.93.0 (2026-09-28) fixed "a security vulnerability that allowed VM-side code to request Unix socket port forwards, which could expose arbitrary host paths to manipulation" ([release notes](https://docs.docker.com/desktop/release-notes/)). No CVE is listed.
- The Mac file-sharing implementations are **VirtioFS** (the default) and **gRPC FUSE**. Docker VMM supports only VirtioFS ([settings](https://docs.docker.com/desktop/settings-and-maintenance/settings/)). The Mac VMMs are Docker VMM (Apple silicon only), the Apple Virtualization framework, and the deprecated HyperKit ([VMM](https://docs.docker.com/desktop/features/vmm/)).
- Docker Desktop already forwards specific host sockets into containers, for example the SSH agent at `/run/host-services/ssh-auth.sock` ([how-tos](https://docs.docker.com/desktop/features/networking/networking-how-tos/)).
- Docker Desktop "can't route traffic to Linux containers": the Mac cannot reach container IPs ([how-tos](https://docs.docker.com/desktop/features/networking/networking-how-tos/)).
- Under gVisor, `--host-uds` "controls permission to access host Unix-domain sockets. Values: none|open|create|all, default: none". `create` "allows UDS from the host to be created, e.g. bind(2)", and `open` allows `connect(2)` ([flags.go](https://github.com/google/gvisor/blob/master/runsc/config/flags.go), [config.go](https://github.com/google/gvisor/blob/master/runsc/config/config.go); master at commit `ecba2b3`, 2026-11-20).
- Before 4.86, users reported failures with socket bind mounts under VirtioFS (secondary: [Colima #997](https://github.com/abiosoft/colima/issues/997), [Docker forum](https://forums.docker.com/t/unix-socket-on-bind-mount/142653)).

**Options, and how each fits ADR 0005**

| Option | Works on Docker Desktop for Mac? | ADR 0005 fit | Notes |
|---|---|---|---|
| **A. Guest Agent listens on a unix socket in a bind-mounted per-Sandbox directory; the Host Agent on macOS connects** (current plan) | Docker Desktop ≥ 4.86 with VirtioFS, per the release note; gRPC FUSE unknown (§5 E1) | **Good.** The Host Agent dials and listens on nothing. The socket is the Guest Agent's | **[Inference]** The directory must be writable only by root in the Sandbox: an ordinary user who could replace the socket would receive the Host Agent's connection and the per-Sandbox token. A root Exec (the spec allows `user: root`) can always do this, so the token proves nothing about the Guest Agent's identity. That is acceptable, because the token only authenticates the platform to the Guest Agent, not the other way round. The directory must never contain a socket created by the host (see the "host to guest" direction), or code in the Sandbox could connect to it. Needs `--host-uds=create` (not `all`) under gVisor |
| **B. `docker exec -i <id> /opt/ga/guestagent --stdio`, with gRPC carried over the exec stream** | **[Inference]** Yes. It uses only the Docker API, which Docker Desktop already proxies from the Mac to the VM over unix sockets ([networking blog, 2022](https://www.docker.com/blog/how-docker-desktop-networking-works-under-the-hood/)) | **Good.** Outside-in, with no socket file and no network endpoint at all | **[Inference]** This still honours ADR 0002, because all features stay in the single Guest Agent and exec is only a byte pipe. Use a non-TTY exec: TTY line discipline corrupts binary, and non-TTY stdout is multiplexed with 8-byte frame headers that must be demultiplexed. Costs one exec per (re)connection. Works unchanged under runsc. **The most portable fallback** |
| **C. Host Agent inside the VM (as a container with the Docker socket, or a process via a privileged helper), using ordinary unix sockets on a VM-side path or a Docker volume** | **[Inference]** Yes, but it is unsupported plumbing: the VM is managed by Docker Desktop | Good, if the Host Agent's own listener (for the Control Plane) is unreachable from Sandboxes. A Host Agent container on a normal bridge or on `--network host` listening on `0.0.0.0` *would* be reachable through the bridge gateway (Q2) | Gives up the "Host Agent is a Go program on macOS" property. Needed anyway if the Host Agent must reach container IPs (option D) |
| **D. Guest Agent listens on TCP on a dedicated "control" Docker network** | Only if the Host Agent is also in the VM: the Mac cannot route to container IPs | **Weak.** Every Sandbox on that network can reach every other Sandbox's Guest Agent unless ICC is disabled, and disabling ICC also blocks the Host Agent container. That forces one control network per Sandbox | Not recommended |
| **E. Publish the Guest Agent port to the Mac (`-p 127.0.0.1:N:port`)** | Yes | **Bad.** Conflicts with ticket 11's "The container itself maps no host ports". **[Inference]** If Mac loopback is reachable through `host.docker.internal` (Q2), other Sandboxes on NAT networks could reach it | Not recommended |

**[Inference] Recommendation:** keep option A as the primary channel, gated on a minimum Docker Desktop version and a passing E1. Put option B behind the same `dial()` interface in the Docker Runtime as the fallback, and as the default for any Docker host where socket sharing is unproven. Connect the Control Plane and the Host Agent over a unix socket on macOS rather than TCP, so that even a mis-networked Sandbox has no TCP target to aim at.

### Q2. `host.docker.internal` and host reachability

**What the sources say**

- `host.docker.internal` "Resolves to the internal IP address of your host". `gateway.docker.internal` "Resolves to the gateway IP of the Docker VM". The official example reaches `python -m http.server 8000` on the Mac with `curl http://host.docker.internal:8000` ([how-tos](https://docs.docker.com/desktop/features/networking/networking-how-tos/)). `http.server` binds to all interfaces by default, so the example proves reachability only for `0.0.0.0`-bound services.
- "All outbound container network traffic originates from the `com.docker.backend` process". "On the host, Docker Desktop's backend process receives the traffic and creates standard TCP/IP connections" ([networking](https://docs.docker.com/desktop/features/networking/)). The 2022 Docker blog describes the same design: the host-side user-space stack "calls `connect()` itself from the host" ([blog, Jan 2022](https://www.docker.com/blog/how-docker-desktop-networking-works-under-the-hood/)). The user-space stack moved from vpnkit to gVisor's netstack in 4.19 (secondary/vendor blog: [Docker Desktop 4.19](https://www.docker.com/blog/docker-desktop-4-19/)).
- The default Docker Desktop subnet is `192.168.65.0/24` ([settings](https://docs.docker.com/desktop/settings-and-maintenance/settings/)).
- "Enable host networking" (4.34+) "Allows containers started with `--net=host` to use `localhost` to connect to TCP and UDP services on the host" ([settings](https://docs.docker.com/desktop/settings-and-maintenance/settings/), [host driver](https://docs.docker.com/engine/network/drivers/host/)).
- On an internal network, "an address is assigned to the bridge … containers in the network can access host services listening on that bridge address". Gateway mode `isolated` (internal networks only) means "no address is assigned to the bridge" ([port publishing](https://docs.docker.com/engine/network/port-publishing/)). Both `isolated` and `gw-priority` arrived in Docker Engine 28.0.0 ([Engine 28 notes](https://docs.docker.com/engine/release-notes/28/)).
- Host firewalls can filter on `com.docker.backend` ([networking](https://docs.docker.com/desktop/features/networking/)).

**What the sources do not say:** whether a service bound **only to 127.0.0.1 on the Mac** is reachable through `host.docker.internal`. Forum threads disagree (secondary: [forum](https://forums.docker.com/t/connection-refused-on-host-docker-internal/136925)). **[Inference]** Because connections are re-originated by a Mac process, the mapping to Mac loopback is a design choice of Docker Desktop and could go either way. Treat it as reachable until §5 E2 shows otherwise.

**[Inference] What a container on a default/NAT bridge can reach:**
1. Mac services on `0.0.0.0` (documented), possibly Mac services on `127.0.0.1` (unknown);
2. anything the Mac can reach: the LAN, VPN-only corporate hosts, the internet;
3. inside the VM, the bridge gateway address and anything listening on `0.0.0.0` in the VM's root network namespace (a `--network host` container, Docker Desktop's own VM services);
4. other containers on the same bridge (ICC defaults to `true`; [bridge driver](https://docs.docker.com/engine/network/drivers/bridge/)).

**How to block it [Inference]:** blocking by name does nothing (the IP still routes), and there is no Docker Desktop setting that turns off container-to-host access. Blocking has to be structural:
- an `--internal` network gives no default route, so there is no path to `192.168.65.x`, the Mac or the LAN;
- gateway mode `isolated` removes the VM-side bridge address;
- never use `--network host`;
- use one internal network per Sandbox, or `enable_icc=false`, to separate Sandboxes from each other.

The Egress Proxy, which *does* have a route, must refuse `host.docker.internal`, `192.168.65.0/24`, the Docker subnets, link-local and RFC 1918 addresses **after** resolving the name itself, to defeat DNS rebinding.

### Q3. Egress enforcement without macOS iptables

**What the sources say**

- `--internal`: "Containers on an internal network may communicate between each other, but not with any other network, as no default route is configured and firewall rules are set up to drop all traffic to or from other networks" ([network create](https://docs.docker.com/reference/cli/docker/network/create/)).
- A container can be on several networks (repeat `--network`), and `gw-priority` picks which one provides the default gateway ([run](https://docs.docker.com/reference/cli/docker/container/run/), [networking](https://docs.docker.com/engine/network/)).
- Containers on user-defined networks use the embedded DNS server at `127.0.0.11`, which "forwards external DNS lookups to the DNS servers configured on the host", overridable with `--dns` ([networking](https://docs.docker.com/engine/network/)).
- CVE-2024-29018 fix (Engine 26.0): "Do not forward requests to external DNS servers for a container that is only connected to an 'internal' network" ([26.0 notes](https://docs.docker.com/engine/release-notes/26.0/)). Engine 26.1.3: "Fix a regression that prevented the use of DNS servers within a `--internal` network" ([26.1 notes](https://docs.docker.com/engine/release-notes/26.1/)).
- `HTTP_PROXY`-style variables are only a convention that clients read. Docker Desktop's own container proxy setting applies to running containers only in "air-gapped container" mode, which needs Docker Business and is configured organisation-wide in `admin-settings.json` (`containersProxy`, `transparentPorts`) ([air-gapped containers](https://docs.docker.com/enterprise/security/hardened-desktop/air-gapped-containers/), [networking](https://docs.docker.com/desktop/features/networking/)).

**Options compared [Inference]**

| Approach | Enforced? | Verdict |
|---|---|---|
| `HTTP(S)_PROXY` env only | No: any program can open a socket directly | Convenience only; keep it *on top of* enforcement |
| `--internal` network + dual-homed proxy container | **Yes.** No route exists except to peers on the internal network | **Minimal enforceable design** |
| Transparent redirect (iptables `REDIRECT`/TPROXY) inside the Docker VM via a privileged helper | Yes, while the rules last | Unsupported on Docker Desktop: the VM is managed and its rules may be rewritten by Docker on network changes or restarts. A good learning exercise on the phase 2 Linux VM, not for phase 1 |
| Make the proxy the Sandbox's default gateway | Needs `NET_ADMIN` in the Sandbox, or netns surgery from the VM | Not worth it |
| Docker Desktop air-gapped containers | Yes, globally | Business-only and not per-Sandbox; doesn't fit Egress Policy modes |

**Minimal enforceable design [Inference]**

```
docker network create --internal \
  -o com.docker.network.bridge.gateway_mode_ipv4=isolated \
  -o com.docker.network.bridge.enable_icc=true  sbx-<id>          # one per Sandbox
docker network create sbx-egress                                   # ordinary NAT bridge
# Egress Proxy + DNS resolver: containers, dual-homed, no management API
docker network connect sbx-<id> egress-proxy                       # also on sbx-egress
docker run --network sbx-<id> --dns <resolver-ip-on-sbx-<id>> \
  -e HTTPS_PROXY=http://<proxy-ip>:3128 -e HTTP_PROXY=... -e NO_PROXY= ...  <template>
```

- **Fully open mode is still proxied.** Fully open becomes an "allow all public addresses" rule on the proxy, so the always-blocked addresses (metadata, Host, private) stay enforced in every mode, as spec §7 requires.
- **DNS.** The Sandbox's `/etc/resolv.conf` points at `127.0.0.11`, and the embedded server forwards non-container names only to the `--dns` resolver, which is on the internal network. A query sent straight to `8.8.8.8:53` has no route. Answering only allowlisted names (and never recursing for the rest) closes DNS tunnelling through the resolver.
- **Residual leaks.** The embedded DNS still answers container names on the same network (the proxy's, the resolver's), and with one network per Sandbox that is all it can reveal.
- **The Egress Proxy cannot be a macOS process.** The Sandbox has no route to the Mac, so the proxy and resolver must be containers in the VM. **Who configures the proxy**, given that it has "no management API" (ADR 0005)? The Host Agent should push configuration over a channel *it* opens: option A or B from Q1 applied to the proxy container, or a read-only bind-mounted config file.
- **Version floor:** Docker Engine ≥ 28.0 for `isolated`, and ≥ 26.1.3 for the DNS behaviour. Check the Engine version bundled with Docker Desktop (`docker version`).

### Q4. Disk limits

**What the sources say**

- `--storage-opt size` "is only available for the `btrfs`, `overlay2`, `windowsfilter`, and `zfs` storage drivers", and for overlay2 only when "the backing filesystem is `xfs` and mounted with the `pquota` mount option" ([run](https://docs.docker.com/reference/cli/docker/container/run/)).
- In moby's source, overlay2 returns `"--storage-opt is supported only for overlay over xfs with 'pquota' mount option"` when project quota isn't available ([overlay.go](https://github.com/moby/moby/blob/master/daemon/graphdriver/overlay2/overlay.go)).
- Docker Desktop's "Use containerd for pulling and storing images" is **enabled by default** ([settings](https://docs.docker.com/desktop/settings-and-maintenance/settings/)). The engine then uses the containerd `overlayfs` snapshotter, not the overlay2 graph driver ([containerd store](https://docs.docker.com/engine/storage/containerd/)).
- In moby's containerd image-store path, `CreateRWLayerOpts{StorageOpt: …}` is passed into `createLayer`, but the snapshot is created with `sn.Prepare(ctx, layerName, parentSnapshot)` and the option isn't used ([image_snapshot.go](https://github.com/moby/moby/blob/master/daemon/containerd/image_snapshot.go); last changed 2026-02-21). **[Inference]** On default Docker Desktop, `--storage-opt size=` is accepted and silently not enforced. This is not "because ext4". §5 E4 confirms it.
- No Docker document I found says what filesystem the Docker Desktop VM disk uses. **Can't confirm "ext4".** Docker Desktop has only a VM-wide "Disk usage limit" ([settings](https://docs.docker.com/desktop/settings-and-maintenance/settings/)).
- For gVisor, Docker's `SizeRw` does not see writes under the default rootfs overlay. The fix in that thread was `--overlay2=none` ([gVisor #10256](https://github.com/google/gvisor/issues/10256), closed "not planned" 2024-11-27).

**Alternatives [Inference]**

1. **Soft limit (phase 1, any Runtime with a host-visible upper layer):** the Host Agent polls the size of each container's writable layer (`docker ps --size` / `SizeRw`) and terminates the Sandbox past the limit, with reason `disk_limit`. This catches the attack after the fact. Docker Desktop's VM-wide disk limit protects the Mac.
2. **Bounded writable paths:** `--read-only` rootfs plus `--tmpfs /home/user:size=…` and `/tmp:size=…`. The limit is hard, but tmpfs pages count against the memory limit (cgroup v2 memory controller), `docker commit` doesn't capture tmpfs, and many images expect a writable `/usr` or `/var` for package installs. This conflicts with Capture.
3. **A loop-mounted filesystem image as the Sandbox's home directory** (a `local` volume with `type`/`device`/`o=loop`). The limit is hard for that path only. Needs loop support in the VM kernel (§5 E4).
4. **Real fix (phase 2 VM):** a Lima/Colima VM with `/var/lib/docker` on xfs mounted `pquota`, **the classic overlay2 driver**, and the containerd image store disabled. Then `--storage-opt size=` is enforced by the documented mechanism.

### Q5. Freeze (`docker pause`)

**What the sources say**

- "On Linux, this uses the freezer cgroup … the process is unaware, and unable to capture, that it is being suspended" ([pause](https://docs.docker.com/reference/cli/docker/container/pause/)). That page links to the cgroup **v1** freezer doc, which explains why SIGSTOP is observable and the freezer isn't ([freezer-subsystem.txt](https://www.kernel.org/doc/Documentation/cgroup-v1/freezer-subsystem.txt)).
- On cgroup v2, runc's cgroup library writes `"1"` to `cgroup.freeze` and then checks the state it reads back ([opencontainers/cgroups fs2/freezer.go](https://github.com/opencontainers/cgroups/blob/main/fs2/freezer.go)).
- Kernel: writing 1 to `cgroup.freeze` stops all processes in the cgroup and its descendants "and will not run until the cgroup will be explicitly unfrozen". "Processes in the frozen cgroup can be killed by a fatal signal". `cgroup.events` reports `frozen 1` ([cgroup-v2](https://docs.kernel.org/admin-guide/cgroup-v2.html)).
- **gVisor differs.** `runsc pause` → `Sandbox.Pause` → the `ContMgrPause` RPC → `cm.l.k.Pause()`, which "pauses all tasks, blocking until they are stopped", inside the Sentry ([container.go](https://github.com/google/gvisor/blob/master/runsc/container/container.go), [sandbox.go](https://github.com/google/gvisor/blob/master/runsc/sandbox/sandbox.go), [controller.go](https://github.com/google/gvisor/blob/master/runsc/boot/controller.go)).
- None of these sources says anything explicit about memory or timers during a freeze.

**[Inference] Things that will surprise us:**
- **Memory.** Frozen pages stay allocated and charged to the container's memory cgroup, so Usage is right to bill Frozen as memory GB-seconds. They are *not* pinned, though: under memory pressure the kernel can still reclaim or swap them, and Docker Desktop's VM has a 1 GB swap by default ([settings](https://docs.docker.com/desktop/settings-and-maintenance/settings/)). Unfreeze is then fast but not always "milliseconds" (§5 E5).
- **Timers and clocks keep running.** Wall and monotonic time advance during the freeze, and expired timers fire right after thaw. A loop that does `i++; sleep 1` pauses its count. A counter computed from elapsed time jumps. Go's `time.Ticker` drops missed ticks. Ticket 08's acceptance test must use the first kind.
- **Network connections.** With runc, the VM kernel's TCP stack is not frozen, so it keeps ACKing and buffering until the receive window fills. Peers only notice through application-level timeouts. **gRPC keepalive between the Host Agent and the Guest Agent will fail**, because the Guest Agent can't answer HTTP/2 PINGs, so the Host Agent must either suspend keepalive for Frozen Sandboxes or expect to reconnect and resume (ADR 0005 already requires resumable streams). Requests through Exposed Ports will hang, so the port proxy should reject quickly for Frozen Sandboxes. Upstream connections held by the Egress Proxy may hit idle timeouts. Under gVisor, netstack runs in the Sentry, and whether it keeps ACKing during `kernel.Pause()` is unknown (§5 E5).

### Q6. Capture (`docker commit`)

**What the sources say**

- "Commits do not include any data contained in mounted volumes". By default "the container being committed and its processes will be paused while the image is committed" (`--pause`/`--no-pause`). `--change` accepts `CMD|ENTRYPOINT|ENV|EXPOSE|LABEL|ONBUILD|USER|VOLUME|WORKDIR` ([commit](https://docs.docker.com/reference/cli/docker/container/commit/)). The page says nothing explicit about bind mounts.
- The writable layer holds "All writes to the container that add new or modify existing data". Volumes and bind mounts are separate from it ([storage drivers](https://docs.docker.com/engine/storage/drivers/)).
- moby `CreateImageFromContainer`: it pauses unless `NoPause`; `if c.Config == nil { c.Config = container.Config }`; then `merge(newConfig, container.Config)` ([daemon/commit.go](https://github.com/moby/moby/blob/master/daemon/commit.go), last changed 2026-09-25). In other words, **the container's runtime config becomes the image config.** Commit is implemented for both the classic and the containerd image stores (`daemon/images/image_commit.go`, `daemon/containerd/image_commit.go`).
- Under gVisor, the rootfs overlay is on by default (`--overlay2=root:self`). "All modifications are made to the overlay, keeping the underlying filesystem unmodified … disable it with `--overlay2=none`" ([filesystem](https://gvisor.dev/docs/user_guide/filesystem/), [config.go defaultOverlay2](https://github.com/google/gvisor/blob/master/runsc/config/config.go)). The self-backed filestore lives in the host upper layer and is hidden by a whiteout ([rootfs overlay blog](https://gvisor.dev/blog/2023/05/08/rootfs-overlay/)). gVisor's own alternative is `runsc tar rootfs-upper`, which writes the upper layer to a tar file ([rootfs snapshot](https://gvisor.dev/docs/user_guide/rootfs_snapshot/)).

**[Inference] Consequences for ADR 0003 and ticket 22:**

- **Guest Agent binary:** a read-only bind mount is not in the writable layer, so the binary is not captured. Docker does create the **mount-point directory** (or an empty file for a file mount) in the rootfs, and that empty path may show up in the Template. That is harmless, but strip it if you want an exact match (§5 E6).
- **Config leakage, the real problem:** Env (Secret placeholders, `HTTP(S)_PROXY`, `SSL_CERT_FILE`, anything the platform set), the injected Entrypoint/Cmd (for example `/opt/ga/guestagent`), `User`, `WorkingDir` and labels are all copied into the Template. A Sandbox created from the captured Template would then try to start a Guest Agent path that no longer exists, carry stale placeholders, and inherit stale settings. **Fix:** call the commit API with an explicit `Config` built from the *source Template's* image config, plus whatever the user changed in the image if we ever support that, and never the container's config.
- **Per-Sandbox CA (ADR 0006):** a CA installed by writing into `/etc/ssl/certs` or running `update-ca-certificates` lands in the writable layer and would be captured. Every Sandbox made from that Template would then trust another Sandbox's CA. **Fix:** inject the CA bundle as a read-only bind mount and point tools at it (`SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE`, `NODE_EXTRA_CA_CERTS`, `GIT_SSL_CAINFO`), or bind-mount over the system bundle file. Either way, the original file stays in the lower layer.
- **Token:** if the per-Sandbox token reaches the Guest Agent through an env var or a rootfs file, it gets captured. Deliver it through the bind-mounted directory (read, then unlinked) or as the first message on the Host Agent's connection.
- **Volumes and tmpfs** are not captured. Document that Capture excludes Volume contents.
- **Running processes** are frozen for the length of the commit. **Under gVisor**, Capture needs `--overlay2=none` (slower filesystem) or a gVisor-specific Capture built on `runsc tar rootfs-upper`, imported as an image layer.

### Q7. Hardening defaults

**What the sources say**

- **Default seccomp** "is an allowlist that denies access to system calls by default" and "disables around 44 system calls out of 300+". Blocked calls include `mount`, `unshare` and `clone` namespace creation ("with the exception of `unshare --user`" / "except `CLONE_NEWUSER`"), `keyctl`/`add_key`/`request_key`, `bpf`, `userfaultfd`, `perf_event_open`, `kexec_load`, and `io_uring_*` ([seccomp](https://docs.docker.com/engine/security/seccomp/)).
- **Default capabilities (14 kept):** AUDIT_WRITE, CHOWN, DAC_OVERRIDE, FOWNER, FSETID, KILL, MKNOD, NET_BIND_SERVICE, NET_RAW, SETFCAP, SETGID, SETPCAP, SETUID, SYS_CHROOT. Everything else, including SYS_ADMIN, SYS_PTRACE, NET_ADMIN and BPF, is not granted ([containers/run](https://docs.docker.com/engine/containers/run/)).
- **`no-new-privileges`:** "Disable container processes from gaining new privileges" ([run](https://docs.docker.com/reference/cli/docker/container/run/)). In the kernel, setuid/setgid bits and file capabilities stop working across `execve`, and the flag is inherited and cannot be unset ([no_new_privs](https://docs.kernel.org/userspace-api/no_new_privs.html)).
- **`--pids-limit`** maps to `pids.max`. Once at the limit, `fork()`/`clone()` "will return -EAGAIN" ([cgroup-v2](https://docs.kernel.org/admin-guide/cgroup-v2.html)).
- **User namespaces:** userns-remap is set in `daemon.json` and is incompatible with `--pid=host`, `--network=host` and `--privileged` (without `--userns=host`) ([userns-remap](https://docs.docker.com/engine/security/userns-remap/)). "The containerd image store is not available when using user namespace remapping" ([containerd store](https://docs.docker.com/engine/storage/containerd/)). Docker Desktop lets you edit `daemon.json` in the Docker Engine tab ([settings](https://docs.docker.com/desktop/settings-and-maintenance/settings/)). No doc says userns-remap is supported on Docker Desktop.
- **Enhanced Container Isolation** needs sign-in and Docker Business ([settings](https://docs.docker.com/desktop/settings-and-maintenance/settings/)). Docker implements it with Sysbox. With it on, "all containers leverage Linux user namespaces", the Docker socket mount is blocked, control-path syscalls are vetted, and read-only bind mounts can't be remounted read-write ([ECI](https://docs.docker.com/enterprise/security/hardened-desktop/enhanced-container-isolation/)). It doesn't work with Docker Desktop host networking ([host driver](https://docs.docker.com/engine/network/drivers/host/)), and extension containers run without it ([ECI limitations](https://docs.docker.com/enterprise/security/hardened-desktop/enhanced-container-isolation/limitations/)).

**[Inference] What each one buys this project, and the catches:**

| Control | Blocks | Catch for us |
|---|---|---|
| Default seccomp | Kernel attack surface: new mount/user-ns tricks via `unshare`, keyrings (not namespaced), `bpf`, `io_uring`, `perf_event_open` | It also blocks `userfaultfd`/`perf`, which few agents need. Unprivileged `unshare --user` is still allowed, so whether the VM kernel allows unprivileged user namespaces matters (§5 E7) |
| `--cap-drop=ALL` | `NET_RAW` (packet crafting / ARP spoofing on the bridge), `MKNOD`, `DAC_OVERRIDE`, `SETUID`, … | **It breaks a root Guest Agent.** Switching to `user` for Exec needs SETUID/SETGID, writing files as other users needs CHOWN/DAC_OVERRIDE/FOWNER, and killing user Processes needs KILL. Use `--cap-drop=ALL --cap-add=SETUID,SETGID,CHOWN,DAC_OVERRIDE,FOWNER,KILL` |
| `no-new-privileges` | Escalation through setuid binaries (`sudo`, `su`) and file capabilities | Templates that rely on `sudo` break, which is the intent. A root Exec goes through the Guest Agent, not `sudo` |
| `--pids-limit` | Fork bombs | Pick a limit that leaves room for build tools (for example 512–4096) |
| Non-dumpable Guest Agent (`prctl(PR_SET_DUMPABLE, 0)`) | Reading the token through `/proc/<pid>/mem` by a same-uid (root) process without `CAP_SYS_PTRACE` | **A root Exec can otherwise read the Guest Agent's memory.** The spec allows `user: root` |
| userns-remap | Container root isn't host (VM) root | On Docker Desktop it would need the classic image store. It is a VM-wide daemon setting, not per Sandbox. Worth one experiment, not a default |
| ECI | Same as userns, plus Docker socket and VM config protection | Paid tier, not reproducible later on Linux. Skip it |

**Context:** on Docker Desktop, the Linux VM is itself a boundary between a container and macOS. A container escape lands in the VM, not on the Mac, so the isolation tests should record "escape → VM" as distinct from "escape → Host".

### Q8. gVisor in phase 2

**What the sources say**

- **Platforms and architectures:** "gVisor supports x86_64 and ARM64, and requires Linux 5.6+" ([install](https://gvisor.dev/docs/user_guide/install/), [FAQ](https://gvisor.dev/docs/user_guide/FAQ/)).
- **Platform choice:** systrap is the default (since mid-2023; the runsc flag default is `"systrap"`, per [flags.go](https://github.com/google/gvisor/blob/master/runsc/config/flags.go)). systrap "is a better choice when running inside a VM, or on a machine without virtualization support". KVM gives "the best performance" on bare metal, while with nested virtualization "the systrap platform will often provide better performance". ptrace "is no longer supported and is expected to eventually be removed" ([platforms](https://gvisor.dev/docs/architecture_guide/platforms/)).
- **Docker Desktop:** the gVisor docs don't mention macOS or Docker Desktop ([install](https://gvisor.dev/docs/user_guide/install/), [Docker quick start](https://gvisor.dev/docs/user_guide/quick_start/docker/)). Docker's alternative-runtimes doc names `io.containerd.runsc.v1`, says nothing about Docker Desktop ([alternative runtimes](https://docs.docker.com/engine/daemon/alternative-runtimes/)), and the shim must be on the daemon's PATH *inside the VM*. A community write-up copies runsc into the Docker Desktop VM (secondary: [dev.to](https://dev.to/rimelek/using-gvisors-container-runtime-in-docker-desktop-374m)).
- **OrbStack** release notes: v2.2.0 (2026-06-04) "Improved gVisor (runsc) compatibility"; v2.2.2 (2026-08-02) "Fixed file ownership in gVisor (runsc) containers" ([OrbStack release notes](https://docs.orbstack.dev/release-notes)). OrbStack's Docker engine config is editable through `orb config docker` ([OrbStack Docker](https://docs.orbstack.dev/docker/)).
- **Lima nested virtualization:** needs Apple M3+ and macOS 15 (secondary only: [DeepWiki on Lima](https://deepwiki.com/lima-vm/lima/16.2-macos-specific-features), [CNCF blog on Lima 2.3](https://cncf.io/blog/2026/11/08/lima-v2-3-native-on-windows-vmnet-on-linux-and-menu-tui)). I couldn't load Apple's `isNestedVirtualizationSupported` page.
- **Security model:** gVisor's goal is to minimise the "System API" attack surface. "No system call is passed through directly to the host". The Sentry's own host syscalls are explicitly enumerated, and it cannot open files or create sockets (except with host networking or directfs). File access goes through the Gofer. Out of scope: hardware side channels ("does not provide protection against hardware side channels"), resource exhaustion (left to host cgroups), and "A sandbox is not a substitute for a secure architecture" ([security](https://gvisor.dev/docs/architecture_guide/security/)).
- **Networking:** by default `--network=sandbox`, so netstack runs in the Sentry and writes link-layer packets to the device in the netns Docker set up ([networking](https://gvisor.dev/docs/user_guide/networking/)). `--net-raw` defaults to false, so raw sockets are disabled ([flags.go](https://github.com/google/gvisor/blob/master/runsc/config/flags.go)).
- **Compatibility gaps for agents:**
  - only a subset of the syscall ABI is implemented;
  - `io_uring` is off by default;
  - in-sandbox cgroup limits are not enforced;
  - `iptables` is partial;
  - KVM inside the sandbox is unsupported ([compatibility](https://gvisor.dev/docs/user_guide/compatibility/));
  - on arm64, `perf_event_open` and `userfaultfd` are unimplemented, `ptrace` is partial, `bpf` is CGROUP_DEVICE-only, `unshare` lacks time/cgroup namespaces, and `add_key`/`request_key` are unimplemented ([arm64 table](https://gvisor.dev/docs/user_guide/compatibility/linux/arm64/)).
- **Docker in gVisor** needs runsc `--net-raw` (plus `--allow-packet-socket-write` for Docker 28+), and for Docker 29 either a `tmpfs` at `/var/lib/docker` or `--feature containerd-snapshotter=false`. The inner `dockerd` must run with `--iptables=false --ip6tables=false`, so published ports don't work and `--network=host` inside is the workaround ([docker-in-gvisor](https://gvisor.dev/docs/tutorials/docker-in-gvisor/)).
- **Checkpoint/restore** exists (`docker checkpoint create`), and restore must go into the same container ([checkpoint/restore](https://gvisor.dev/docs/user_guide/checkpoint_restore/)). This could become a gVisor Hibernated state later; it's out of phase 2 scope.

**[Inference] Phase 2 recommendation:** run phase 2 in a Lima (or Colima) VM, or an OrbStack Linux machine, with a stock Docker Engine, installing runsc from gVisor's apt repository. Don't use Docker Desktop. Use the arm64 build and the default systrap platform. This also gives a VM we control, where xfs+pquota (Q4) and iptables experiments (Q3) are possible. Register two runtimes:
- `runsc` with `--host-uds=create` (the Q1 channel);
- `runsc-capture` with `--overlay2=none`, or implement gVisor Capture with `runsc tar rootfs-upper`.

The egress design from Q3 should carry over unchanged, because netstack still exits through the Docker-managed veth (§5 E8 verifies it). Docker-in-Sandbox under gVisor needs flags that weaken it (`--net-raw`), so it should be an explicit per-Template opt-in, if offered at all.

---

## 3. Claim verification

| # | Claim | Verdict | Evidence |
|---|---|---|---|
| 1 | "`--storage-opt size` doesn't take effect on Docker Desktop for Mac because the VM uses ext4." | **Conclusion likely holds; the reason is wrong or unconfirmed.** | The size option needs overlay2 on xfs+pquota ([run](https://docs.docker.com/reference/cli/docker/container/run/), [overlay.go](https://github.com/moby/moby/blob/master/daemon/graphdriver/overlay2/overlay.go)). Docker Desktop defaults to the containerd image store ([settings](https://docs.docker.com/desktop/settings-and-maintenance/settings/)), whose moby code path doesn't apply `StorageOpt` ([image_snapshot.go](https://github.com/moby/moby/blob/master/daemon/containerd/image_snapshot.go)). No doc says the VM uses ext4. Verify with §5 E4 |
| 2 | "Inside a container on Docker Desktop, `host.docker.internal` resolves to the Mac host, so a Host Agent listening on a TCP port could be reached from a Sandbox." | **Holds** for a port bound to all interfaces. **Can't confirm** for `127.0.0.1`-only | [how-tos](https://docs.docker.com/desktop/features/networking/networking-how-tos/), [networking](https://docs.docker.com/desktop/features/networking/). Verify with §5 E2 |
| 3 | "`docker pause` freezes processes with the cgroup freezer; memory stays allocated." | **Holds for runc** (cgroup v2 `cgroup.freeze`). "Memory stays allocated" is consistent with the kernel docs but not stated there; it can still be swapped. **Doesn't hold for gVisor**, which uses Sentry `kernel.Pause()` | [pause](https://docs.docker.com/reference/cli/docker/container/pause/), [fs2/freezer.go](https://github.com/opencontainers/cgroups/blob/main/fs2/freezer.go), [cgroup-v2](https://docs.kernel.org/admin-guide/cgroup-v2.html), [runsc controller.go](https://github.com/google/gvisor/blob/master/runsc/boot/controller.go) |
| 4 | "Capture can be implemented with `docker commit` on the Mac in phase 1." | **Holds, with required changes.** Pass an explicit config (otherwise the container's Env and Entrypoint are baked in), keep the CA and token out of the rootfs, and note that Volumes are excluded. Breaks under gVisor's default overlay | [commit](https://docs.docker.com/reference/cli/docker/container/commit/), [daemon/commit.go](https://github.com/moby/moby/blob/master/daemon/commit.go), [gVisor filesystem](https://gvisor.dev/docs/user_guide/filesystem/) |
| 5 | "gVisor can run in a Linux VM on the Mac (OrbStack or Lima)." | **Holds in principle.** ARM64 is supported and systrap needs no virtualization. OrbStack's release notes reference runsc containers. Not documented by gVisor for macOS, and not on Docker Desktop | [install](https://gvisor.dev/docs/user_guide/install/), [platforms](https://gvisor.dev/docs/architecture_guide/platforms/), [OrbStack notes](https://docs.orbstack.dev/release-notes). Verify with §5 E8 |

---

## 4. Impact on our design (all proposed changes are **[Inference]**)

| # | Finding | Affects | Proposed change |
|---|---|---|---|
| 1 | NAT networks give Sandboxes a route to the Mac, the LAN and the VM. `--internal` + `isolated` removes it | Spec §3 and §7; ADR 0005; tickets 12, 16, 19 | Spec §7: "Every Sandbox is attached only to its own `--internal` bridge network (gateway mode `isolated`); the Egress Proxy and DNS resolver are the only other members." Ticket 12: add acceptance "`ip route` in a Sandbox shows no default route" and "the bridge gateway address answers nothing". ADR 0005: the "it has to be enforced by the host not listening" paragraph should also say "and by giving Sandboxes no route" |
| 2 | On Docker Desktop, the Egress Proxy and resolver must be containers in the VM | Spec §3 diagram; GLOSSARY "Egress Proxy" ("The process on each Host"); ADR 0005 | Spec §3: "On Docker Desktop, the Egress Proxy and DNS resolver run as dual-homed containers inside the Docker VM". Add a sentence on how the Host Agent configures them: over a connection the Host Agent opens, or through a read-only bind-mounted config, never through a listening management API |
| 3 | The unix socket over VirtioFS works only since Docker Desktop 4.86; gVisor needs `--host-uds=create` | ADR 0002; ticket 01; spec §9 | ADR 0002 "Consequences": minimum Docker Desktop 4.86 with VirtioFS. Name `docker exec -i … --stdio` as the fallback transport behind the same interface (it's still one Guest Agent). Phase 2: runsc with `--host-uds=create`, never `all`/`open`. Ticket 01: add acceptance "Host Agent startup refuses Docker Desktop < 4.86 / gRPC FUSE unless the exec transport is configured" |
| 4 | `docker commit` copies the container's config, and anything written into the rootfs (CA, token) is captured | Ticket 22; ADRs 0003 and 0006; ticket 23 | Ticket 22: commit with an explicit `Config` derived from the source Template's image config. Add acceptance "captured Template's `Config.Env`/`Entrypoint` equal the source Template's" and "the per-Sandbox CA is not trusted in a Sandbox created from the captured Template". Ticket 23 / ADR 0006: install the per-Sandbox CA via a read-only bind mount plus `SSL_CERT_FILE`-style variables, not by modifying the trust store in the rootfs |
| 5 | `--cap-drop=ALL` conflicts with a root Guest Agent, and a root Exec can read the token | Ticket 16; spec §7 | Ticket 16: "drop all capabilities, then add back SETUID, SETGID, CHOWN, DAC_OVERRIDE, FOWNER, KILL", and "the Guest Agent sets itself non-dumpable". Spec §7: state that the token protects against the ordinary user, not against a root Exec |
| 6 | Disk limits: storage-opt is silently ignored on default Docker Desktop | Spec §7 "Known gap"; tickets 19, 03 | Reword the gap: "the default containerd image store ignores `--storage-opt size`". Add a soft limit (Host Agent polls `SizeRw` and terminates with reason `disk_limit`) so the "fill the disk" test has an outcome. Phase 2 VM: overlay2 on xfs+pquota for a hard limit. Note that `SizeRw` is blind under gVisor's default overlay |
| 7 | Freeze semantics: keepalive breaks, clocks advance, gVisor pause isn't the cgroup freezer | Ticket 08; GLOSSARY "Frozen"; ticket 11 | GLOSSARY: "On Docker (runc) this is `docker pause`, i.e. the cgroup freezer; gVisor pauses inside its own kernel." Ticket 08: define the counter as a per-iteration loop counter, add "the Host Agent's channel survives or resumes after a freeze longer than the keepalive timeout", and "an Exposed Port request to a Frozen Sandbox fails fast". Also record unfreeze time under memory pressure |
| 8 | Control Plane ↔ Host Agent over TCP on the Mac may be reachable from a mis-networked Sandbox | Spec §7; ticket 16 | Phase 1: Control Plane ↔ Host Agent over a unix socket on macOS. The isolation test "reach the Host Agent via `host.docker.internal`" is then a regression test for both the network and the listener |
| 9 | gVisor's platform, location and Capture | Spec §9 phase 2; future `docs/learn/02-gvisor.md` | Spec §9: "Phase 2 runs in a Lima/Colima VM or an OrbStack Linux machine (arm64, systrap), not Docker Desktop." Capture on gVisor: `--overlay2=none` runtime or `runsc tar rootfs-upper`. Docker-in-Sandbox is out of scope unless a Template explicitly opts in |
| 10 | Docker Desktop's VM is an extra boundary | Tickets 19, 21 | The isolation table should distinguish "escaped to VM" from "escaped to Host", so the Docker column isn't overstated on the Mac and understated on Linux |

---

## 5. Experiments to run on the Mac

Run them on the exact Docker Desktop version you'll build against (`docker version`), and repeat after upgrades. The commands assume `brew install socat` on the Mac.

**E1. Unix sockets across the bind mount (Q1).** Repeat with Settings → General → file sharing set to VirtioFS, then to gRPC FUSE.
```sh
mkdir -p ~/sbx-e1 && chmod 700 ~/sbx-e1
# Direction we need: the Guest Agent (container) listens, the Mac connects
docker run -d --name e1 -v ~/sbx-e1:/run/ga alpine sh -c \
  'apk add -q socat && socat UNIX-LISTEN:/run/ga/ga.sock,fork,mode=600 SYSTEM:"echo pong-from-container"'
sleep 5; ls -l ~/sbx-e1; socat - UNIX-CONNECT:$HOME/sbx-e1/ga.sock      # expect: pong-from-container
# Direction that must NOT exist in production: the Mac listens, the container connects
socat UNIX-LISTEN:$HOME/sbx-e1/host.sock,fork SYSTEM:'echo pong-from-mac' &
docker exec e1 socat - UNIX-CONNECT:/run/ga/host.sock                  # records what a Sandbox could reach
# gRPC-level latency: build cmd/guestagent and cmd/hostagent, time 1000 unary Execs of `true` over the socket
docker rm -f e1; kill %1
```

**E2. Host reachability (Q2).**
```sh
python3 -m http.server 18001 --bind 127.0.0.1 &   # loopback-only
python3 -m http.server 18002 --bind 0.0.0.0 &     # all interfaces
probe='getent hosts host.docker.internal gateway.docker.internal; ip route;
  for p in 18001 18002; do wget -q -T3 -O- http://host.docker.internal:$p >/dev/null && echo "$p REACHABLE" || echo "$p blocked"; done;
  wget -q -T3 -O- http://1.1.1.1 >/dev/null && echo INTERNET || echo no-internet'
docker run --rm alpine sh -c "$probe"                                            # default bridge
docker network create e2-nat && docker run --rm --network e2-nat alpine sh -c "$probe"
docker network create --internal e2-int && docker run --rm --network e2-int alpine sh -c "$probe"
docker network create --internal -o com.docker.network.bridge.gateway_mode_ipv4=isolated e2-iso \
  && docker run --rm --network e2-iso alpine sh -c "$probe; ip addr"
# What listens inside the VM (diagnostic only; privileged)
docker run --rm --privileged --pid=host alpine nsenter -t 1 -m -n -- sh -c 'netstat -lntu 2>/dev/null || ss -lntu'
# Scan the gateway from an internal (non-isolated) network
docker run --rm --network e2-int alpine sh -c 'apk add -q nmap; nmap -Pn -p- $(ip route | awk "/src/{print \$1}" | sed "s#0/.*#1#")'
kill %1 %2
```

**E3. Egress design (Q3).**
```sh
docker network create --internal -o com.docker.network.bridge.gateway_mode_ipv4=isolated e3-sbx
docker network create e3-egress
docker run -d --name e3-proxy --network e3-egress ubuntu/squid && docker network connect e3-sbx e3-proxy
docker run -d --name e3-dns --network e3-egress alpine sh -c 'apk add -q dnsmasq && dnsmasq -k --no-resolv --server=1.1.1.1 --log-queries' \
  && docker network connect e3-sbx e3-dns
DNS_IP=$(docker inspect -f '{{(index .NetworkSettings.Networks "e3-sbx").IPAddress}}' e3-dns)
docker run --rm --network e3-sbx --dns $DNS_IP alpine sh -c '
  cat /etc/resolv.conf; nslookup example.com; nslookup example.com 8.8.8.8; ip route;
  wget -q -T3 -O- http://93.184.215.14 >/dev/null && echo DIRECT-IP-LEAK || echo direct-ip-blocked;
  https_proxy=http://e3-proxy:3128 wget -q -T5 -O- https://example.com >/dev/null && echo via-proxy-ok'
docker logs e3-dns | tail   # confirms the embedded DNS forwarded to the platform resolver
```

**E4. Disk limits (Q4).**
```sh
docker info --format 'driver={{.Driver}} status={{json .DriverStatus}}'
docker run --rm --storage-opt size=1G alpine sh -c 'df -h /; dd if=/dev/zero of=/big bs=1M count=2048 && echo WROTE-2G-LIMIT-IGNORED'
docker run --rm --privileged --pid=host alpine nsenter -t 1 -m -- sh -c 'mount | grep -E "/var/lib/(docker|containerd)"; df -T /var/lib/docker /var/lib/containerd 2>/dev/null'
# Loop-image alternative
docker run --rm --privileged --pid=host alpine nsenter -t 1 -m -- sh -c 'ls /dev/loop* | head -3; grep -w loop /proc/modules /proc/devices'
```

**E5. Freeze (Q5).**
```sh
docker run -d --name e5 alpine sh -c 'i=0; while true; do i=$((i+1)); echo "$i $(date +%s)"; sleep 1; done'
sleep 5; CID=$(docker inspect -f '{{.Id}}' e5); docker pause e5
docker run --rm --privileged --pid=host alpine nsenter -t 1 -m -- sh -c "find /sys/fs/cgroup -path '*$CID*' -name cgroup.events -exec cat {} \;"
docker stats --no-stream e5; sleep 20; time docker unpause e5; sleep 3; docker logs e5 | tail -6   # counter resumes, timestamps jump
# TCP during freeze
docker network create e5-net; docker run -d --name e5-srv --network e5-net alpine nc -lk -p 9000
docker run -d --name e5-cli --network e5-net alpine sh -c 'while true; do date; sleep 1; done | nc e5-srv 9000'
docker pause e5-srv; sleep 60; docker unpause e5-srv; docker ps -a --filter name=e5-cli   # still connected?
# Memory pressure: freeze a container holding 2 GB, run a memory hog, then time the unpause
```
Repeat the TCP part with `--runtime=runsc` in the phase 2 VM.

**E6. Capture (Q6).**
```sh
mkdir -p ~/sbx-e6 && touch ~/sbx-e6/guestagent      # stand-in for the injected binary
docker run -d --name e6 -e ANTHROPIC_API_KEY=sbx_secret_x -e HTTPS_PROXY=http://p:3128 \
  -v ~/sbx-e6:/opt/ga:ro --tmpfs /tmp --entrypoint /bin/sh alpine -c 'sleep 1d'
docker exec e6 sh -c 'echo hi > /root/f; echo t > /tmp/t; cp /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/extra.pem'
docker commit e6 e6:cap
docker inspect -f 'Env={{json .Config.Env}} EP={{json .Config.Entrypoint}} Cmd={{json .Config.Cmd}}' e6:cap   # expect leakage
docker run --rm --entrypoint sh e6:cap -c 'ls -la /opt/ga /tmp; cat /root/f; ls /etc/ssl/certs/extra.pem'
docker image history e6:cap
# Explicit config via the API (what the Host Agent should do)
curl --unix-socket ~/.docker/run/docker.sock -X POST \
  "http://localhost/commit?container=e6&repo=e6&tag=clean" -H 'Content-Type: application/json' \
  -d "$(docker image inspect -f '{{json .Config}}' alpine)"
docker inspect -f '{{json .Config.Env}}' e6:clean
```

**E7. Hardening (Q7).**
```sh
docker info --format 'cgroup={{.CgroupVersion}} kernel={{.KernelVersion}} sec={{json .SecurityOptions}} runtimes={{json .Runtimes}}'
docker run --rm --cap-drop=ALL --security-opt no-new-privileges --pids-limit 64 --user 1000:1000 alpine \
  sh -c 'grep -E "CapEff|CapBnd|NoNewPrivs|Seccomp" /proc/self/status; for i in $(seq 100); do sleep 30 & done; echo spawned'
docker run --rm alpine sh -c 'cat /proc/sys/user/max_user_namespaces; apk add -q util-linux; unshare -U -r id'   # unprivileged userns?
docker run --rm --cap-drop=ALL --cap-add=SETUID,SETGID,CHOWN,DAC_OVERRIDE,FOWNER,KILL alpine \
  sh -c 'adduser -D u; su u -c id'   # minimal set lets root drop to user
# userns-remap: Settings → General: turn OFF the containerd image store; Docker Engine tab: add "userns-remap": "default"; then
docker info --format '{{json .SecurityOptions}}'; docker run --rm alpine sh -c 'cat /proc/self/uid_map'
```

**E8. gVisor in a Linux VM (Q8).**
```sh
limactl start --name=gv template://docker          # or: orb create ubuntu gv
limactl shell gv -- sh -c 'uname -m; ls -l /dev/kvm 2>&1'
# Inside the VM, install runsc from gVisor's apt repo per https://gvisor.dev/docs/user_guide/install/, then:
sudo runsc install                                                   # default runtime "runsc"
sudo runsc install --runtime runsc-uds -- --host-uds=create
sudo runsc install --runtime runsc-capture -- --overlay2=none
sudo systemctl restart docker
docker run --rm --runtime=runsc alpine dmesg | head -3                  # gVisor banner
docker run --rm --runtime=runsc -v /tmp/gv:/run/ga alpine sh -c 'apk add -q socat; timeout 3 socat UNIX-LISTEN:/run/ga/s - ; echo rc=$?'  # expect failure
docker run -d --runtime=runsc-uds --name gvu -v /tmp/gv:/run/ga alpine sh -c 'apk add -q socat; socat UNIX-LISTEN:/run/ga/s,fork SYSTEM:"echo pong"'
sleep 5; sudo socat - UNIX-CONNECT:/tmp/gv/s
for rt in runsc runsc-capture; do docker run -d --runtime=$rt --name c-$rt alpine sleep 1d;
  docker exec c-$rt sh -c 'echo hi > /root/f'; docker commit c-$rt cap:$rt; docker run --rm cap:$rt cat /root/f; done   # runsc: expect missing
docker run -d --runtime=runsc --name gp alpine sh -c 'i=0; while true; do i=$((i+1)); echo $i; sleep 1; done'; docker pause gp; sleep 10; docker unpause gp; docker logs gp | tail -3
# Re-run E2/E3 probes with --runtime=runsc to confirm the network design carries over
```

---

## 6. Sources (all accessed 2026-11-21)

**Docker Desktop**
- Release notes (4.100.0 = 2026-11-19; 4.86.0 AF_UNIX over VirtioFS; 4.93.0 socket-forward fix) https://docs.docker.com/desktop/release-notes/
- Networking https://docs.docker.com/desktop/features/networking/ (also `.md`) ; How-tos https://docs.docker.com/desktop/features/networking/networking-how-tos/
- Settings https://docs.docker.com/desktop/settings-and-maintenance/settings/ ; VMM https://docs.docker.com/desktop/features/vmm/
- Enhanced Container Isolation https://docs.docker.com/enterprise/security/hardened-desktop/enhanced-container-isolation/ ; limitations https://docs.docker.com/enterprise/security/hardened-desktop/enhanced-container-isolation/limitations/
- Air-gapped containers https://docs.docker.com/enterprise/security/hardened-desktop/air-gapped-containers/
- Docker blog, "How Docker Desktop Networking Works Under the Hood" (D. Scott, 2022-01-25) https://www.docker.com/blog/how-docker-desktop-networking-works-under-the-hood/ ; Docker Desktop 4.19 blog https://www.docker.com/blog/docker-desktop-4-19/
- Legacy osxfs doc (archived, outdated) https://github.com/docker/docker.github.io/blob/a17a06c6a15028011250900c49a1b59620cf1c65/docker-for-mac/osxfs.md

**Docker Engine docs**
- `docker run` https://docs.docker.com/reference/cli/docker/container/run/ ; `docker commit` https://docs.docker.com/reference/cli/docker/container/commit/ ; `docker pause` https://docs.docker.com/reference/cli/docker/container/pause/
- `docker network create` https://docs.docker.com/reference/cli/docker/network/create/ ; Networking https://docs.docker.com/engine/network/ ; Bridge driver https://docs.docker.com/engine/network/drivers/bridge/ ; Port publishing https://docs.docker.com/engine/network/port-publishing/ ; Host driver https://docs.docker.com/engine/network/drivers/host/
- Seccomp https://docs.docker.com/engine/security/seccomp/ ; Running containers (capabilities) https://docs.docker.com/engine/containers/run/ ; Security https://docs.docker.com/engine/security/ ; userns-remap https://docs.docker.com/engine/security/userns-remap/
- Storage drivers https://docs.docker.com/engine/storage/drivers/ ; overlayfs driver https://docs.docker.com/engine/storage/drivers/overlayfs-driver/ ; containerd image store https://docs.docker.com/engine/storage/containerd/ ; Alternative runtimes https://docs.docker.com/engine/daemon/alternative-runtimes/
- Engine release notes 26.0 https://docs.docker.com/engine/release-notes/26.0/ ; 26.1 https://docs.docker.com/engine/release-notes/26.1/ ; 28 https://docs.docker.com/engine/release-notes/28/

**Source code (default branches, read 2026-11-21)**
- moby `daemon/graphdriver/overlay2/overlay.go` https://github.com/moby/moby/blob/master/daemon/graphdriver/overlay2/overlay.go
- moby `daemon/containerd/image_snapshot.go` (last commit be32017, 2026-02-21) https://github.com/moby/moby/blob/master/daemon/containerd/image_snapshot.go
- moby `daemon/commit.go` (last commit c7572b3, 2026-09-25) https://github.com/moby/moby/blob/master/daemon/commit.go
- opencontainers/cgroups `fs2/freezer.go` https://github.com/opencontainers/cgroups/blob/main/fs2/freezer.go
- gVisor `runsc/config/flags.go` and `config.go` (last commit ecba2b3, 2026-11-20) https://github.com/google/gvisor/blob/master/runsc/config/flags.go , https://github.com/google/gvisor/blob/master/runsc/config/config.go
- gVisor `runsc/container/container.go`, `runsc/sandbox/sandbox.go`, `runsc/boot/controller.go` https://github.com/google/gvisor/tree/master/runsc
- gVisor issue #10256 (SizeRw under overlay) https://github.com/google/gvisor/issues/10256

**gVisor docs**
- Install https://gvisor.dev/docs/user_guide/install/ ; Docker quick start https://gvisor.dev/docs/user_guide/quick_start/docker/ ; FAQ https://gvisor.dev/docs/user_guide/FAQ/
- Platforms https://gvisor.dev/docs/architecture_guide/platforms/ ; Security model https://gvisor.dev/docs/architecture_guide/security/
- Compatibility https://gvisor.dev/docs/user_guide/compatibility/ ; arm64 syscalls https://gvisor.dev/docs/user_guide/compatibility/linux/arm64/
- Docker in gVisor https://gvisor.dev/docs/tutorials/docker-in-gvisor/ ; Networking https://gvisor.dev/docs/user_guide/networking/ ; Filesystem https://gvisor.dev/docs/user_guide/filesystem/
- Rootfs overlay blog (2023-05-08) https://gvisor.dev/blog/2023/05/08/rootfs-overlay/ ; Rootfs tar snapshots https://gvisor.dev/docs/user_guide/rootfs_snapshot/ ; Filesystem snapshots https://gvisor.dev/docs/user_guide/fs_snapshot/ ; Checkpoint/restore https://gvisor.dev/docs/user_guide/checkpoint_restore/

**Linux kernel**
- cgroup v2 https://docs.kernel.org/admin-guide/cgroup-v2.html ; cgroup v1 freezer https://www.kernel.org/doc/Documentation/cgroup-v1/freezer-subsystem.txt ; no_new_privs https://docs.kernel.org/userspace-api/no_new_privs.html

**Other VM tools**
- OrbStack release notes https://docs.orbstack.dev/release-notes ; OrbStack Docker https://docs.orbstack.dev/docker/
- Lima vz https://lima-vm.io/docs/config/vmtype/vz/

**Secondary (labelled where used)**
- Colima #997 https://github.com/abiosoft/colima/issues/997 ; Docker forum (socket on bind mount) https://forums.docker.com/t/unix-socket-on-bind-mount/142653 ; Docker forum (host.docker.internal) https://forums.docker.com/t/connection-refused-on-host-docker-internal/136925
- dev.to, runsc in Docker Desktop https://dev.to/rimelek/using-gvisors-container-runtime-in-docker-desktop-374m
- DeepWiki on Lima macOS features https://deepwiki.com/lima-vm/lima/16.2-macos-specific-features ; CNCF blog on Lima 2.3 https://cncf.io/blog/2026/11/08/lima-v2-3-native-on-windows-vmnet-on-linux-and-menu-tui
