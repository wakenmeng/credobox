# Phase 3: the Firecracker Runtime and microVM techniques

> English | [中文](firecracker-microvm.zh.md)

> Research date: 2026-11-21. Every link was accessed on that day unless noted otherwise.
> Primary sources only: Firecracker docs and source (github.com/firecracker-microvm), cloud provider docs, kernel docs and source, the E2B runtime source and docs, and vendor engineering writing. Anything marked **[Inference]** is my own conclusion, not what a source says.
> Versions this note is pinned to: **Firecracker v1.18.0** (released 2026-11-09, [releases](https://github.com/firecracker-microvm/firecracker/releases)); **E2B runtime** at commit `89e7715` (2026-11-21, [e2b-dev/runtime](https://github.com/e2b-dev/runtime)). Firecracker changes quickly (the snapshot format changed three times in 2025–2026), so treat every version-dependent statement below as valid only for these versions.
> Terms follow the [GLOSSARY](../../GLOSSARY.md). This note does not repeat the vendor comparison in [agent-sandbox-usage.md](agent-sandbox-usage.md).

---

## 1. Summary

- **Where to run it: GCP n2 with nested virtualization works, and since February 2026 AWS offers the same on ordinary (non-metal) Intel instances.** GCP supports nested KVM on Intel VMs, and among AMD VMs only on N4D. It warns of "a 10% or greater decrease in performance" for CPU-bound work ([GCP](https://docs.cloud.google.com/compute/docs/instances/nested-virtualization/overview)). AWS has supported nested virtualization on C8i/M8i/R8i since 2026-02-16 and added the 7th-gen Intel families on 2026-06-18 ([AWS user guide](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/amazon-ec2-nested-virtualization.md)). Firecracker's own test matrix lists only `.metal` hosts ([README](https://github.com/firecracker-microvm/firecracker/blob/main/README.md)), and I found no primary source that measures Firecracker boot or restore under nesting.
- **`firecracker-go-sdk` and `firecracker-containerd` were both archived on 2026-11-10** ("this project is no longer maintained", [go-sdk](https://github.com/firecracker-microvm/firecracker-go-sdk), [containerd](https://github.com/firecracker-microvm/firecracker-containerd)). E2B drives the Firecracker HTTP API directly with a client generated from the OpenAPI spec. **[Inference]** The Host Agent should do the same.
- **E2B's design in one sentence: "A sandbox is a resumed snapshot."** A Template is a pre-booted VM snapshot. Memory is served lazily through `userfaultfd`, and the rootfs is a read-only ext4 image with a per-sandbox CoW layer served over NBD. Create, resume and fork all take this one path. Firecracker runs without the jailer, in its own mount namespace, network namespace and cgroup ([ARCHITECTURE.md](https://github.com/e2b-dev/runtime/blob/main/docs/ARCHITECTURE.md), [process.go](https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/sandbox/fc/process.go)).
- **Snapshots only restore onto an identical setup.** That means the same CPU model and guest-visible CPU features (Intel to AMD is never supported), and in practice the same host kernel. The snapshot format also changed in v1.11, v1.12 and v1.15, each time requiring snapshots to be regenerated ([snapshot-support](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md), [versioning](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/versioning.md), [CHANGELOG](https://github.com/firecracker-microvm/firecracker/blob/main/CHANGELOG.md)). **[Inference]** Hibernated and Fork therefore become scheduling constraints, and a Firecracker upgrade must have a story for existing Hibernated Sandboxes.
- **Every clone of one snapshot needs a new identity.** Firecracker documents the hazards: RNG state (VMGenID helps from Linux 5.18, but "there is no generic solution" for userspace RNGs), the guest IP and TAP names, the wall clock, and vsock connections, which are reset. AWS Lambda SnapStart adds IDs and secrets cached in memory. E2B's answer is a host-pushed `/init` after every restore, carrying a fresh access token per fork and re-syncing the clock. **[Inference]** Our Guest Agent needs the same "re-identify after restore" step, and its per-Sandbox token becomes per-incarnation.
- **Device passthrough is not in any Firecracker release as of v1.18.0.** PCI transport arrived in v1.13 and PCI hotplug of virtio devices in v1.16. VFIO is being built on a `feature/vfio` branch, and its tracking issue [#5679](https://github.com/firecracker-microvm/firecracker/issues/5679) is still open. "GPU needs Cloud Hypervisor or QEMU" holds today, but is time-sensitive.
- **Published numbers:** InstanceStart to `/sbin/init` in ≤125 ms (1 vCPU, 128 MiB, tuned kernel, `.metal`), VMM memory overhead ≤5 MiB, and VMM startup ≤8 CPU ms ([SPECIFICATION](https://github.com/firecracker-microvm/firecracker/blob/main/SPECIFICATION.md)). Firecracker publishes **no** snapshot-restore latency figure: its docs say only that restore time depends on memory size, vCPU count and devices.
- **Verdict on phase 3 [Inference]:** the scope (rootfs conversion, vsock, Hibernate, Fork, auto-hibernate/resume and SWE-bench) is too large for one phase. Split it. First: cold boot, OCI conversion, vsock, networking, the jailer and SWE-bench. Second: Template snapshots and Hibernate on the same Host. Third: Fork. Defer: auto-hibernate/resume on traffic, UFFD and diff snapshots.

---

## 2. Findings by question

### 2.1 Where to run it

**GCP.**
- What is supported. Nested virtualization relies on Intel VT-x or AMD-V. The overview page lists exclusions rather than a full allowlist: E2, memory-optimized VMs except M4/M4N, Arm VMs, and "AMD-powered VMs except N4D". Only Linux KVM is supported as the L1 hypervisor; "Microsoft Hyper-V isn't supported" ([overview](https://docs.cloud.google.com/compute/docs/instances/nested-virtualization/overview)).
- Performance. Nested VMs "might experience a 10% or greater decrease in performance" for CPU-bound workloads, and I/O-bound workloads may lose more (same page).
- Enabling it. Use `gcloud compute instances create … --enable-nested-virtualization` (or `advancedMachineFeatures.enableNestedVirtualization`). The older license-key method needs a minimum CPU platform of Intel Haswell ([enabling](https://docs.cloud.google.com/compute/docs/instances/nested-virtualization/enabling)).
- Firecracker's own dev-machine guide uses GCE nested virtualization with `n1-standard-2` plus `--min-cpu-platform="Intel Haswell"`, or `n2-standard-2`, and says "`E2` and `N2D` instances will not work" ([dev-machine-setup](https://github.com/firecracker-microvm/firecracker/blob/main/docs/dev-machine-setup.md)).
- E2B's single-host Terraform for GCP defaults to `n4-standard-4` with `enable_nested_virtualization = true`. A comment there says GCE arm64 types have no nested virtualization, "metal included (the kernel reports the feature unavailable, 2026-09-14)" ([variables.tf](https://github.com/e2b-dev/runtime/blob/main/embed/terraform/gcp/variables.tf), [main.tf](https://github.com/e2b-dev/runtime/blob/main/embed/terraform/gcp/main.tf)).
- Not confirmed: whether GCP live migration during host maintenance is safe for an L1 VM with running L2 guests. The kernel docs say migrating an L1 with a running L2 "works as of Linux kernel 5.3 and QEMU 4.2.0 for Intel x86". On AMD, once an L2 has started, "the L1 guest should no longer be migrated or saved" ([kernel docs](https://docs.kernel.org/virt/kvm/x86/running-nested-guests.html)). **[Inference]** This is another reason to stay on Intel (n2) rather than N4D.

**AWS.**
- Since **2026-02-16**, "customers can create nested virtual machines by running KVM or Hyper-V on virtual EC2 instances", on C8i, M8i and R8i in all commercial regions ([What's New, Feb 2026](https://aws.amazon.com/about-aws/whats-new/2026/02/amazon-ec2-nested-virtualization-on-virtual)).
- On **2026-06-18** this was extended to C7i, R7i, M7i, I7i, the `-flex` and `d` variants, and X8i, plus GovCloud ([What's New, Jun 2026](https://aws.amazon.com/about-aws/whats-new/2026/06/nested-virtualization-intel-us-gov-cloud/)). The user guide's current list also includes R7iz and I7ie.
- Details from the user guide: it is enabled with `--cpu-options "NestedVirtualization=enabled"`, an existing instance must be stopped first, and there is "no additional cost". AWS recommends that performance-sensitive or latency-sensitive users "evaluate bare metal instances" ([user guide](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/amazon-ec2-nested-virtualization.md)).
- Only Intel families are listed; Graviton is absent.
- E2B's AWS Terraform says it "was validated on m8i.xlarge" ([aws/variables.tf](https://github.com/e2b-dev/runtime/blob/main/embed/terraform/aws/variables.tf)).
- Firecracker v1.18.0 also added "Nitro Sandboxes" (`PUT /enclave`) as an alternative to KVM on EC2 ([CHANGELOG](https://github.com/firecracker-microvm/firecracker/blob/main/CHANGELOG.md)). That backend has "no paused state", so no snapshots ([snapshot-support](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md)). **[Inference]** It is irrelevant to us.

**What Firecracker itself tests.**
- The README's matrix covers only `.metal` instances: m5n, m6i, m7i, m8i, m6a, m7a and Graviton 2–5. A footnote says the 8th-gen Intel instances are supported only "using a 6.1 or 6.18 host kernel".
- Nested virtualization is not mentioned ([README](https://github.com/firecracker-microvm/firecracker/blob/main/README.md)).
- Supported host kernels are 5.10, 6.1 and 6.18 ([kernel-policy](https://github.com/firecracker-microvm/firecracker/blob/main/docs/kernel-policy.md)). **[Inference]** A stock distro kernel on a cloud VM may not be one of these; check `uname -r` against the table and record it in the results.

**Cheap bare metal (Hetzner).**
- The Hetzner AX matrix, accessed 2026-11-21, prices excl. VAT and subject to change ([matrix-ax](https://www.hetzner.com/dedicated-rootserver/matrix-ax/)):
  - AX41: Ryzen 5 3600, 6 cores, 64 GB, 2×512 GB NVMe, €59/month, no setup fee, €0.0945/hour.
  - AX42: Ryzen 7 PRO 8700GE, 8 cores, 64 GB DDR5 ECC, from €79/month, €49 setup.
  - AX102: Ryzen 9 7950X3D, 16 cores, 128 GB, from €159/month, €129 setup.
  - The page does not tie each spec row to a model explicitly, so the mapping above follows page order.
- **[Inference]** A dedicated root server exposes `/dev/kvm` with no nesting, so it gives a clean "bare-metal" calibration point. The costs:
  - These are desktop-class CPUs with SMT on, and Firecracker recommends disabling SMT "in production scenarios that require tenant separation" ([prod-host-setup](https://github.com/firecracker-microvm/firecracker/blob/main/docs/prod-host-setup.md)).
  - The CPU models differ from any cloud host, so snapshots won't move between them.
  - Provisioning is manual.

**How nesting affects boot and restore numbers.**
- **No primary source measures this for Firecracker.** Firecracker's docs never discuss nested performance. GCP gives only the generic "10% or greater" for CPU-bound work, and the kernel's nested-guest doc only advises checking that `enable_shadow_vmcs` and `ept` are on ([kernel docs](https://docs.kernel.org/virt/kvm/x86/running-nested-guests.html)). The third-party blog numbers I found are secondary and are left out.
- One relevant mechanism is documented: after a snapshot restore, KVM has to rebuild guest page tables, and Firecracker says 2M huge pages help because they "cut the KVM exits needed to rebuild page tables after snapshot restore" ([hugepages](https://github.com/firecracker-microvm/firecracker/blob/main/docs/hugepages.md)).
- **[Inference]** Under nesting, every such exit and every page fault on lazily loaded memory goes through L0 before L1 KVM handles it. So lazy restore (file-backed or UFFD) is probably where nested virtualization hurts most, more than CPU-bound guest code. The phase-3 comparison table should record the host type (nested n2 or bare metal) next to every Firecracker number, and ideally include one bare-metal run as calibration.

### 2.2 Firecracker tooling

| Option | Status (2026-11-21) | Snapshots / vsock / current API | Fit for us |
|---|---|---|---|
| `firecracker-go-sdk` | **Archived 2026-11-10**: "As of November 10 2026, this project is no longer maintained … There are no alternative solutions". Last tagged release v1.0.0 (2022-09-07) ([repo](https://github.com/firecracker-microvm/firecracker-go-sdk)) | Its swagger client lags the API; the README itself says some Firecracker features "are not yet supported by the SDK" | **Don't use** |
| `firecracker-containerd` | **Archived 2026-11-10**; points users to "more modern, secure alternatives" and to Nitro Sandboxes ([repo](https://github.com/firecracker-microvm/firecracker-containerd)) | n/a | Don't use |
| Drive the HTTP API directly | Firecracker serves a REST API on a unix socket, described by an OpenAPI file. Endpoints include `/machine-config`, `/boot-source`, `/drives/{id}`, `/network-interfaces/{id}`, `/vsock`, `/entropy`, `/actions`, `/vm`, `/snapshot/create`, `/snapshot/load`, `/mmds` ([firecracker.yaml](https://github.com/firecracker-microvm/firecracker/blob/main/src/firecracker/swagger/firecracker.yaml)) | Everything, at whatever version you pin | **Recommended [Inference]**: about a dozen calls; teaches what Firecracker actually does |
| flintlock | Active: v0.17.0 released 2026-11-17. A gRPC/HTTP service that runs Firecracker or Cloud Hypervisor microVMs from OCI images and can "talk to a guest agent in the microVM over a vsock device" ([repo](https://github.com/liquidmetal-dev/flintlock)) | Its feature list does not mention snapshots | **[Inference]** Would replace the Runtime layer of our Host Agent. Too much hidden for a learning project, and no Hibernate/Fork |
| Kata Containers + Firecracker | Kata 4.4.0 (2026-11-17) pins Firecracker v1.17.0 and needs the containerd `devmapper` snapshotter on a thin-pool, because "a block device as the backing store for a VM is required" ([how-to](https://github.com/kata-containers/kata-containers/blob/main/docs/how-to/how-to-use-kata-containers-with-firecracker.md), [versions.yaml](https://github.com/kata-containers/kata-containers/blob/main/versions.yaml)) | **[Inference]** Kata runs its own in-guest agent and owns the VM lifecycle | Conflicts with ADR 0002 (one Guest Agent) and hides vsock and snapshots |

**What E2B does.**
- E2B keeps an OpenAPI file and a generated go-swagger client in `packages/shared/pkg/fc` ([firecracker.yml](https://github.com/e2b-dev/runtime/blob/main/packages/shared/pkg/fc/firecracker.yml)).
- It starts each VM as `unshare -m -- bash -c "… ip netns exec <ns> firecracker --api-sock … [--enable-pci]"` ([script_builder.go](https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/sandbox/fc/script_builder.go), [process.go](https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/sandbox/fc/process.go)).
- It builds its own Firecracker binaries from pinned commits ([fc-versions](https://github.com/e2b-dev/runtime/tree/main/firecracker/fc-versions)).

### 2.3 OCI image to rootfs, and the kernel

**What Firecracker documents.**
- The rootfs is a file-backed block device. Their recipe is to create a file with `dd`, format it with `mkfs.ext4`, and populate it from a container.
- "The kernel runs `/sbin/init`… expects it to never exit".
- The filesystem driver must be built into the kernel ([rootfs-and-kernel-setup](https://github.com/firecracker-microvm/firecracker/blob/main/docs/rootfs-and-kernel-setup.md)).
- The docs do not cover read-only roots or overlays.
- Block device backing files "must be pre-formatted with a filesystem the guest kernel supports" ([design](https://github.com/firecracker-microvm/firecracker/blob/main/docs/design.md)).

**E2B.**
- Template build pipeline:
  1. Pull the image with `go-containerregistry`.
  2. Mount its layers with overlayfs, copy the merged tree out, and `mkfs.ext4` a fresh image (with `inline_data`, `^64bit` and similar options).
  3. `resize2fs`/`e2fsck` as needed ([oci.go](https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/template/build/core/oci/oci.go), [ext4.go](https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/template/build/core/filesystem/ext4.go)).
  4. Inject `envd`, its systemd/OpenRC units, and a **static BusyBox used as init** ([rootfs.go](https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/template/build/core/rootfs/rootfs.go), [fc-busybox](https://github.com/e2b-dev/runtime/tree/main/firecracker/fc-busybox)).
  5. Boot a VM per build phase: "base → user → one layer per recipe step → resize disk → finalize → optimize". Each layer is a pause-diff.
  6. The optimize phase "records which memory pages a fresh resume touches", which later drives prefetching ([ARCHITECTURE](https://github.com/e2b-dev/runtime/blob/main/docs/ARCHITECTURE.md)).
- At runtime, "the template rootfs stays read-only; writes go to a per-sandbox COW cache exposed to Firecracker as an NBD block device served by an in-process userspace NBD server". On pause, the dirty blocks become a diff (same doc).

**Devicemapper thin-pool.**
- This is what Kata+Firecracker uses: each container image layer becomes a thin snapshot, presented to the VM as a block device ([Kata how-to](https://github.com/kata-containers/kata-containers/blob/main/docs/how-to/how-to-use-kata-containers-with-firecracker.md)).
- **[Inference]** Thin snapshots give block-level CoW in the kernel with no userspace NBD server. The cost is thin-pool administration, and the pool "must be initialized again after each reboot" in some setups (secondary source, not relied on).

**Kernel.**
- Firecracker publishes validated guest configs in `resources/guest_configs` for 5.10, 6.1 and 6.18. The docs say they are meant for building Amazon Linux microVM kernels. `vmlinux` is recommended on x86_64; `bzImage` works but is slower to boot ([kernel-policy](https://github.com/firecracker-microvm/firecracker/blob/main/docs/kernel-policy.md), [rootfs-and-kernel-setup](https://github.com/firecracker-microvm/firecracker/blob/main/docs/rootfs-and-kernel-setup.md)).
- Guest kernel support windows:
  - 6.1: from v1.9.0, minimum end of support **2026-09-02**, a date that has already passed.
  - 6.18: from v1.16.1, until at least 2028-06-01.
- E2B builds 6.1.x kernels from the `microvm-kernel-*` tags of `amazonlinux/linux` with its own configs ([fc-kernels](https://github.com/e2b-dev/runtime/tree/main/firecracker/fc-kernels)). Its x86_64 6.1.190 config sets `CONFIG_VMGENID=y`, `CONFIG_HW_RANDOM_VIRTIO=y` and `CONFIG_VIRTIO_VSOCKETS=y`, and leaves `CONFIG_VSOCKETS_LOOPBACK` unset ([config](https://github.com/e2b-dev/runtime/blob/main/firecracker/fc-kernels/configs/x86_64/6.1.190.config)).

**[Inference] Recommendations for us.**
- Build a 6.18 guest from Firecracker's `microvm-kernel-ci-x86_64-6.18.config` and check those four options.
- Cache one ext4 image per **image digest**, not per tag.
- Supply our own init. Most OCI images have no `/sbin/init`, so the Guest Agent, or a tiny init in front of it, must be PID 1 or be started by one.

### 2.4 The Guest Agent over vsock

All from [vsock.md](https://github.com/firecracker-microvm/firecracker/blob/main/docs/vsock.md) unless noted.

**Host-initiated connections.**
1. The Host Agent connects to the VM's `uds_path`.
2. It writes `CONNECT <port>\n`.
3. Firecracker answers `OK <assigned_hostside_port>\n`.
4. The socket then becomes a byte stream to the guest's listener on `<port>`. If nothing is listening in the guest, Firecracker closes the host connection.

**[Inference]** In Go, a gRPC `WithContextDialer` that dials the unix socket and performs this one-line handshake is enough.

**Guest-initiated connections.**
- The guest connects to CID 2 and port P. Firecracker forwards the connection to the AF_UNIX socket at `<uds_path>_P`.
- If that socket doesn't exist or nothing is listening, "a VIRTIO_VSOCK_OP_RST packet will be sent back to the guest".
- Ports in `[2^30, 2^31)` are reserved for the host side of host-initiated connections, and guest requests to them are always RST.

**Keeping the host from listening (ADR 0005).**
- Firecracker only forwards to `<uds_path>_P` if something on the host listens there. So the rule is simply: no host process ever creates `<uds_path>_*`.
- With the jailer, `uds_path` lives inside the per-VM chroot ([jailer](https://github.com/firecracker-microvm/firecracker/blob/main/docs/jailer.md)). **[Inference]** That makes stray listeners from other host processes unlikely.
- **[Inference] Isolation test:** from inside the guest, connect `AF_VSOCK` to CID 2 on a sweep of ports (including the Guest Agent's) and expect a reset every time. On the host, assert that no `v.sock_*` file exists.

**Hardening inside the guest (from kernel source, plus inference).**
- Linux refuses to bind a vsock port ≤ 1023 (`LAST_RESERVED_PORT`) without `CAP_NET_BIND_SERVICE` ([af_vsock.c](https://github.com/torvalds/linux/blob/master/net/vmw_vsock/af_vsock.c), [af_vsock.h](https://github.com/torvalds/linux/blob/master/include/net/af_vsock.h)). **[Inference]** If the Guest Agent listens on a port below 1024, the ordinary user cannot take over the port if the agent ever restarts.
- A process inside the guest reaches its own CID only through the loopback transport (`VMADDR_CID_LOCAL` = 1, [vm_sockets.h](https://github.com/torvalds/linux/blob/master/include/uapi/linux/vm_sockets.h)). **[Inference]** Building the guest kernel without `CONFIG_VSOCKETS_LOOPBACK`, as E2B does, means code inside the Sandbox cannot reach the Guest Agent's vsock listener at all. The per-Sandbox token then becomes defense in depth rather than the only barrier.

**Snapshots.**
- Firecracker sends `VIRTIO_VSOCK_EVENT_TRANSPORT_RESET` during snapshot creation. On resume the guest driver closes existing connections, while listening sockets survive ([snapshot-support](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md)).
- v1.16 added `vsock_override` to move `uds_path` on restore ([CHANGELOG](https://github.com/firecracker-microvm/firecracker/blob/main/CHANGELOG.md)).
- **[Inference]** ADR 0005's "reconnect and resume from a sequence number" is therefore exercised on every Hibernate, resume and Fork, not just on a Host Agent restart.

**E2B does not use vsock for its agent.** envd listens on TCP port 49983 and is reached "over the host network at the sandbox slot IP". Its control routes are hidden only because the edge proxy refuses them. The token is delivered by `/init`, and MMDS carries only the token's hash ([ARCHITECTURE](https://github.com/e2b-dev/runtime/blob/main/docs/ARCHITECTURE.md)). **[Inference]** Our vsock design is stricter, because the control channel is not reachable on the guest's network at all.

### 2.5 Snapshots, Hibernate and Fork

**Full vs diff snapshots** ([snapshot-support](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md)).
- A snapshot is a memory file, a VM state file, and the disks, which the user manages.
- Full snapshots "always create a complete, resume-able snapshot". Snapshots became GA in v1.13 ([CHANGELOG](https://github.com/firecracker-microvm/firecracker/blob/main/CHANGELOG.md)).
- Diff snapshots contain only dirtied pages and are "generally not resumable". They have to be merged with `snapshot-editor edit-memory rebase`, and they are still developer preview.
- Dirty tracking (`track_dirty_pages`) uses KVM's dirty log and costs CPU. It "negates most of the benefits of huge pages".
- The fallback without dirty tracking uses `mincore(2)`, which only works "if swap is disabled".

**Memory backends.**
- **File** (default): memory is `MAP_PRIVATE`, loaded per page fault. Writes go to anonymous CoW memory. The memory file must stay unchanged for the life of the restored VM.
- **Uffd**: Firecracker sends a userfaultfd and the memory layout to a user-written handler over a unix socket, and the handler resolves faults with `UFFDIO_COPY`. If the handler dies, "Firecracker will hang when a page fault occurs". Balloon deflation produces `UFFD_EVENT_REMOVE` events that the handler must honor ([page-fault doc](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/handling-page-faults-on-snapshot-resume.md)).
- Explicit 2M huge pages require Uffd ([hugepages](https://github.com/firecracker-microvm/firecracker/blob/main/docs/hugepages.md)).
- CodeSandbox has written about cloning running VMs and sharing memory between them through UFFD ([CodeSandbox blog](https://codesandbox.io/blog/cloning-microvms-using-userfaultfd)). The page returned 403 to my fetch, so its numbers are not quoted here.

**Cost.**
- Firecracker publishes no restore-time number. Creation and resume time "depend on memory size, vCPU count, and device count".
- Creation writes all memory synchronously. `sync_snapshot_files` fsyncs by default, which is durable but slower.
- cgroups v1 causes "high restore latency"; v2 is strongly recommended.
- Operators "must" provision disk and enforce quotas to avoid DoS (all [snapshot-support](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md)).
- **[Inference]** For us, Hibernate costs one write of the whole guest memory plus the rootfs diff, and a Hibernated Sandbox occupies roughly its memory size in storage. That is what the "Hibernated counts storage" Usage line will meter.

**Restore limits.**
- Restore requires configurations that are "identical" to the original software and hardware.
- The only cross-kernel exception is `.metal` m5n/m6i/m6a moving from host kernel 5.10 to 6.1, which "is not recommended for production".
- Snapshots "are not compatible across CPU architectures and even across CPU models of the same architecture". Intel to AMD is unsupported ([versioning](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/versioning.md)).
- Host resources such as TAP names, drive paths and the vsock UDS must sit at the same relative paths, unless overridden with `network_overrides`, `vsock_override` or `drive_overrides`.
- The snapshot format version is checked on load. v1.11, v1.12 and v1.15 each say snapshots "must be regenerated" ([CHANGELOG](https://github.com/firecracker-microvm/firecracker/blob/main/CHANGELOG.md)).

**Clone hazards and their fixes.**

| Hazard | What the primary source says | Fix |
|---|---|---|
| Identical RNG state | Firecracker implements VMGenID: on restore it "writes a new identifier and injects a notification". Linux ≥5.18 (ACPI) reseeds its CSPRNG from it. A race window remains between vCPU resume and the reseed, and for userspace RNGs "There is no generic solution under the current programming model" ([random-for-clones](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/random-for-clones.md)). The kernel side is `add_vmfork_randomness()` in [vmgenid.c](https://github.com/torvalds/linux/blob/master/drivers/virt/vmgenid.c). Lambda SnapStart reseeds the kernel RNG on restore, but libraries that cache random bytes must be reseeded through runtime hooks ([SnapStart uniqueness](https://docs.aws.amazon.com/lambda/latest/dg/snapstart-uniqueness.html)) | Guest kernel with `CONFIG_VMGENID`; attach virtio-rng (`/entropy`); delete `/var/lib/systemd/random-seed` before snapshotting; after restore, `RNDADDENTROPY` + `RNDRESEEDCRNG` (needs `CAP_SYS_ADMIN`); bind-mount a new `boot_id`. E2B attaches the entropy device ([fc/client.go](https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/sandbox/fc/client.go)) and enables VMGENID in its kernel |
| Identical TAP name, IP and MAC | Firecracker restores the original TAP names, and every clone has the same guest IP. Fix: one network namespace per clone with the same TAP name inside it, a veth to the host, and NAT (`MASQUERADE` out, `DNAT` from a unique "clone address" in). Flush the guest's neighbour tables after restore. The doc does not discuss MAC addresses and calls itself not "performant or secure", just a starting example ([network-for-clones](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/network-for-clones.md)) | E2B: every guest is `169.254.0.21` behind tap `169.254.0.22` in its own netns, the tap has a fixed MAC "to keep resumed guests' cached gateway ARP entries valid", and the host side gets a unique `10.11.x.y` through SNAT/DNAT ([slot.go](https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/sandbox/network/slot.go), [network.go](https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/sandbox/network/network.go)). **[Inference]** An identical guest MAC is harmless when each clone has its own L2 segment |
| TCP and vsock connection state | "Network and vsock packet loss can be expected after resume", connection state is not guaranteed, and vsock connections are reset ([snapshot-support](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md)) | Treat every restore as a reconnect (ADR 0005). **[Inference]** Document that a Sandbox's outbound TCP connections (to the Egress Proxy) drop on Hibernate and Fork |
| Identical clock | The guest wall clock continues from snapshot time and "should be updated on the guest side". `clock_realtime: true` needs host ≥5.16 and kvm-clock. v1.15 added a VMClock device whose `vm_generation_counter` changes on restore; poll() support is in Linux 7.0 ([snapshot-support](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md), [CHANGELOG](https://github.com/firecracker-microvm/firecracker/blob/main/CHANGELOG.md)) | E2B re-sends `/init` after an in-place checkpoint "to re-sync the guest clock" and ships chrony templates ([ARCHITECTURE](https://github.com/e2b-dev/runtime/blob/main/docs/ARCHITECTURE.md)) |
| Identical secrets and tokens in memory | "Resuming the same state more than once is considered insecure" ([snapshot-support](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md)). SnapStart: "you must generate unique content after initialization. This includes unique IDs, unique secrets, and entropy". AWS's paper *Restoring Uniqueness in MicroVM Snapshots* proposes `MADV_WIPEONSUSPEND` and SysGenID for this ([arXiv 2102.12892](https://arxiv.org/abs/2102.12892)) | E2B mints "for a secured sandbox, an envd access token scoped to the fork", pushed via `/init` after restore ([ARCHITECTURE](https://github.com/e2b-dev/runtime/blob/main/docs/ARCHITECTURE.md)). **Our case:** see §4, item 2 |

**How E2B implements Hibernate (its "pause"), resume and Fork** ([ARCHITECTURE](https://github.com/e2b-dev/runtime/blob/main/docs/ARCHITECTURE.md)).
- **Pause.** The orchestrator pauses the VM, snapshots it, diffs memory (using dirty-page tracking) and the rootfs (the CoW cache) against the template, caches the result locally, and uploads it to object storage asynchronously.
  - It first probes envd's health.
  - It freezes the workload cgroups so that no process is caught half-started.
  - Filesystem-only snapshots additionally require the filesystem to be quiesced.
- **Resume.** Resume is "the same path as creation", with placement preferring the origin node, whose local cache avoids object-storage reads.
- **Fork.** E2B checkpoints the original in place (pause plus resume), then starts each fork "as a new sandbox rather than a resume", spread across nodes. Each fork gets a new ID, a new execution ID and a new token.
- **Auto-pause and auto-resume.** An eviction sweep auto-pauses idle sandboxes. When traffic arrives for a paused sandbox, the edge `client-proxy` calls the API's `ResumeSandbox`, with "one flight shared by concurrent requests".
- **Scale of the work.** The implementation section on pause and resume alone runs about 260 lines. It covers rate limits, refusal paths, a degrade to filesystem-only, and envd live upgrade. **[Inference]** This is the most complex part of their system, which is a warning about phase-3 scope.

### 2.6 Networking

**Firecracker's guide** ([network-setup](https://github.com/firecracker-microvm/firecracker/blob/main/docs/network-setup.md)).
- Create one TAP per VM, owned by the Firecracker user, and give each VM its own /30.
- Use nftables masquerade plus a forward rule. `iptables-nft` is "no longer recommended".
- Configure the guest IP with ip commands or with the kernel `ip=` argument. Set `guest_mac` explicitly, or the guest picks a random one.
- For clones, use the namespaced NAT described in §2.5.

**What the host must do.**
- "Firecracker does not perform any network traffic filtering" ([design](https://github.com/firecracker-microvm/firecracker/blob/main/docs/design.md)).
- Production hosts should drop TAP-originated packets to `169.254.169.254` and use rate limiters or `tc` against flooding ([prod-host-setup](https://github.com/firecracker-microvm/firecracker/blob/main/docs/prod-host-setup.md)).
- virtio-net devices have built-in bandwidth and ops rate limiters ([README](https://github.com/firecracker-microvm/firecracker/blob/main/README.md)).

**E2B's per-sandbox networking** ([ARCHITECTURE](https://github.com/e2b-dev/runtime/blob/main/docs/ARCHITECTURE.md), [network.go](https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/sandbox/network/network.go)).
- Each sandbox gets a "slot": a network namespace containing the TAP, a veth pair to the host, a unique host-side IP, NAT, and a per-slot nftables egress firewall.
- That firewall includes "SNI/Host-inspecting TCP firewall for domain allow/deny lists".
- Host-side services such as their HTTP proxy, the NFS portmapper and the NFS proxy are reached through `iptables -t nat PREROUTING -i <veth> … -j REDIRECT --to-port …`.
- Slots are pooled and reused.

**[Inference] How egress fits ADR 0005/0006.**
- Inside each Sandbox's netns, nftables allows only TCP to the Egress Proxy's port, and UDP/TCP 53 to the platform resolver, and drops everything else.
- On the host, `PREROUTING -i <veth>` redirects TCP 80/443 to the Egress Proxy (transparent mode, so it can read SNI and Host as the spec requires) and port 53 to the resolver. The metadata address, the Host's own addresses and private ranges are dropped before routing.
- Because every guest has the same IP inside its own netns, a Fork needs no network changes inside the guest.

### 2.7 Disk and resource limits, and the jailer

**Disk.**
- The rootfs is a block device backed by a file (or an NBD or thin device). The guest sees exactly that size.
- **[Inference]** The guest cannot grow the device, which closes the "filling the disk" gap the spec records for Docker Desktop.
- What must be bounded instead is the **host-side** CoW and snapshot storage. Firecracker says to "provision enough disk and enforce quotas" ([snapshot-support](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md)).
- Other controls: the jailer's `fsize` and `no-file` rlimits, cgroup block-IO throttling, and the virtio-block rate limiter ([prod-host-setup](https://github.com/firecracker-microvm/firecracker/blob/main/docs/prod-host-setup.md)).

**CPU and memory.**
- `/machine-config` sets the vCPU count and memory size. Firecracker "sets no aggressive defaults", so cgroup CPU and memory controls on the Firecracker process are the real limits (same doc).
- The balloon is "best effort": hosts "should never rely solely on the statistics" it reports. Running with `vm.overcommit_memory=1` is not recommended ([ballooning](https://github.com/firecracker-microvm/firecracker/blob/main/docs/ballooning.md)).
- E2B confines the `fc_vcpu` threads with `cpu.max` in a threaded child cgroup, so the VMM's own threads stay outside the quota ([ARCHITECTURE](https://github.com/e2b-dev/runtime/blob/main/docs/ARCHITECTURE.md)).

**The jailer** ([jailer.md](https://github.com/firecracker-microvm/firecracker/blob/main/docs/jailer.md)), step by step:
1. Validate paths, close inherited file descriptors and clean the environment.
2. Create `<chroot_base>/<exec>/<id>/root` and copy the binary in, so it "will not share memory with any other Firecracker process".
3. Apply rlimits (`no-file` defaults to 2048).
4. Create a cgroup (v1 or v2) and apply the `--cgroup` values.
5. `unshare` a mount namespace, then `pivot_root` and `chroot`.
6. `mknod` only `/dev/kvm` and `/dev/net/tun` (plus `/dev/userfaultfd` on 6.1, per the [page-fault doc](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/handling-page-faults-on-snapshot-resume.md)).
7. Optionally `setns` into a given netns, daemonize, and start a new PID namespace.
8. Drop to the given uid/gid and exec Firecracker.

"The jailer treats all its inputs as trusted", and it runs as root.

**Do production setups use it?**
- Firecracker says production should use the jailer "or equally restrictive process constraints", with a unique uid/gid per instance ideally ([prod-host-setup](https://github.com/firecracker-microvm/firecracker/blob/main/docs/prod-host-setup.md)).
- Kata's install steps download `jailer` alongside `firecracker` ([Kata how-to](https://github.com/kata-containers/kata-containers/blob/main/docs/how-to/how-to-use-kata-containers-with-firecracker.md)).
- **E2B does not use it.** In the code I read, the orchestrator runs "as root" and starts Firecracker through `unshare -m` and `ip netns exec`, with only `Setsid` and a cgroup FD. There is no uid drop and no chroot ([process.go](https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/sandbox/fc/process.go)).
- **[Inference]** For this project the jailer is worth using: each of its steps maps onto one attack in the isolation curriculum.

### 2.8 Firecracker's own security model

**Threat model** ([design](https://github.com/firecracker-microvm/firecracker/blob/main/docs/design.md)).
- "All vCPU threads are considered to be running malicious code as soon as they have been started".
- The first isolation layer is KVM plus the Firecracker virtualization boundary.
- For defense in depth, "Firecracker should only run constrained at the process level", meaning seccomp, cgroups, namespaces and dropped privileges via the jailer.
- Networking is the host's job.

**Seccomp** ([seccomp](https://github.com/firecracker-microvm/firecracker/blob/main/docs/seccomp.md)).
- Filters are installed per thread before any guest code runs: on the VMM thread, the API thread and each vCPU thread. They allow "the bare minimum set of system calls and parameters".
- The filters are compiled from JSON in `resources/seccomp` and embedded in the binary.
- Since v1.11 they are compiled with `libseccomp` ([CHANGELOG](https://github.com/firecracker-microvm/firecracker/blob/main/CHANGELOG.md)).
- Debug builds have no filters. `--no-seccomp` is for prototyping only.

**What Firecracker assumes the host does** ([prod-host-setup](https://github.com/firecracker-microvm/firecracker/blob/main/docs/prod-host-setup.md)):
- runs it under the jailer or equivalent, with cgroup limits, and one Firecracker process per tenant;
- keeps host and guest kernels and microcode patched, because "Firecracker is not able to mitigate host's hardware vulnerabilities";
- disables SMT where tenants must be separated;
- disables KSM;
- disables swap (against data remanence);
- uses ECC/TRR memory (against rowhammer);
- disables the 8250 serial port in production, or bounds its output;
- bounds log storage, which the guest can influence;
- runs an overwatcher that SIGKILLs hung Firecracker processes;
- filters egress itself.

### 2.9 Devices: PCI and passthrough

- **v1.13**: optional PCI transport for virtio devices via `--enable-pci`.
- **v1.16**: developer-preview PCI hot-plug and hot-unplug of block, pmem and net devices.
- **Unreleased**: PCIe hotplug auto-discovery ([CHANGELOG](https://github.com/firecracker-microvm/firecracker/blob/main/CHANGELOG.md)).
- The guest kernel needs `CONFIG_VIRTIO_PCI` for this transport ([kernel-policy](https://github.com/firecracker-microvm/firecracker/blob/main/docs/kernel-policy.md)). E2B passes `--enable-pci` when it is configured ([script_builder.go](https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/sandbox/fc/script_builder.go)).
- **VFIO passthrough is not released.**
  - Tracking issue [#5679](https://github.com/firecracker-microvm/firecracker/issues/5679) ("VFIO device passthrough support") is open.
  - The "chapter" PRs merged in 2026-09 to 2026-11 target the `feature/vfio` branch, not `main`: #6183, #6219, #6241, #6252 and #6316. #6356 (hotplug) and #6398 (docs) are still open.
  - An earlier single PR, [#5870](https://github.com/firecracker-microvm/firecracker/pull/5870), was closed without merging. (State checked through the GitHub API on 2026-11-21.)
- GPU details are left to the GPU research note.

### 2.10 Published performance numbers

| Metric | Number | Conditions | Source |
|---|---|---|---|
| Boot (InstanceStart to `/sbin/init`) | ≤ 125 ms | m5d.metal (HT off) or m6g.metal, 1 vCPU, 128 MiB, Firecracker-tuned guest kernel, serial console off, minimal rootfs; tested on every PR | [SPECIFICATION](https://github.com/firecracker-microvm/firecracker/blob/main/SPECIFICATION.md) |
| VMM startup (to API socket) | ≤ 8 CPU ms; wall clock 6–60 ms, typically ~12 ms | Same hardware | same |
| VMM memory overhead | ≤ 5 MiB | 1 vCPU, 128 MiB guest, excluding MMDS; many vsock connections can exceed it | same |
| CPU / network / storage | >95% of bare metal; 14.5 Gbps at ≤80% of a core (25 Gbps at 100%); +0.06 ms latency; 1 GiB/s at ≤70% of a core | All marked "pending" (not yet tested in CI) | same |
| Boot, 2020 paper | Pre-configured Firecracker boots in ~100–125 ms and end-to-end in ~150–175 ms (read from Fig. 5); 50 VMs in parallel: p99 146 ms (pre-configured Firecracker) vs 158 ms (Cloud Hypervisor); a network interface adds ~20 ms | Firecracker v0.20.0, m5d.metal, guest kernel 4.14.94, 1 vCPU / 256 MB, minimal init, no network | [NSDI '20 paper](https://www.usenix.org/conference/nsdi20/presentation/agache) |
| Memory overhead, 2020 paper | ~3 MB (Firecracker), ~13 MB (Cloud Hypervisor), ~131 MB (QEMU) | Same setup, measured with `pmap` | same |
| Huge pages | Boot time improves "up to 50%" | 2M hugetlbfs, Firecracker boot tests | [hugepages](https://github.com/firecracker-microvm/firecracker/blob/main/docs/hugepages.md) |
| Snapshot restore | **No figure published by Firecracker** | "depend[s] on memory size, vCPU count, and device count" | [snapshot-support](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md) |
| Restore vs boot (qualitative) | Booting a DSQL query processor from scratch takes "a couple hundred milliseconds, probably"; a snapshot restore is "orders of magnitude faster" | AWS engineering blog, no measurement | [Brooker, 2025-09-18](https://brooker.co.za/blog/2025/09/18/firecracker) |

Vendor create-time claims (E2B's "~80ms", Daytona and others) are already covered in [agent-sandbox-usage.md](agent-sandbox-usage.md) §2.

---

## 3. Claim verification

| # | Claim (made earlier from memory) | Verdict | Source and notes |
|---|---|---|---|
| 1 | "Firecracker needs KVM; on GCP you can get it with nested virtualization on n2; on AWS you traditionally need `.metal` instances." | **Holds, but outdated for AWS** | GCP nested virtualization works on Intel VMs (n2 included; Firecracker's own guide names `n2-standard-2`) ([GCP](https://docs.cloud.google.com/compute/docs/instances/nested-virtualization/overview), [dev-machine-setup](https://github.com/firecracker-microvm/firecracker/blob/main/docs/dev-machine-setup.md)). AWS has supported nested virtualization on virtual C8i/M8i/R8i instances since 2026-02-16, and on 7th-gen Intel since 2026-06-18 ([AWS](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/amazon-ec2-nested-virtualization.md)). Firecracker's test matrix is still `.metal` only. Nitro Sandboxes (v1.18) are a non-KVM backend without snapshots |
| 2 | "Firecracker doesn't support GPU or device passthrough, so GPU needs Cloud Hypervisor or QEMU." | **Holds for released versions (≤ v1.18.0); time-sensitive** | VFIO lives on `feature/vfio` and tracking issue #5679 is open ([CHANGELOG](https://github.com/firecracker-microvm/firecracker/blob/main/CHANGELOG.md), [#5679](https://github.com/firecracker-microvm/firecracker/issues/5679)). PCI transport (v1.13) and virtio PCI hotplug (v1.16) are released. Re-check before phase 4 |
| 3 | "Snapshot restore can bring a VM up in well under a second; vendor cold-start numbers come from snapshot restore, not kernel boot." | **Second half holds; first half is plausible but has no primary figure** | E2B: "'Creating' a sandbox means restoring one, not booting a kernel" ([README](https://github.com/e2b-dev/runtime)). Firecracker publishes no restore latency ([snapshot-support](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md)), and Brooker says "orders of magnitude faster" than boot without a number. Note that a kernel cold boot is itself ≤125 ms ([SPECIFICATION](https://github.com/firecracker-microvm/firecracker/blob/main/SPECIFICATION.md)). **[Inference]** What a snapshot really saves is userland start-up (init, the Guest Agent, the app), so our benchmark should time "Guest Agent ready" for both paths |
| 4 | "After snapshot restore, network config and random-number state need care." | **Holds, but incomplete** | Also needed: the wall clock, vsock and TCP connections (reset), in-memory secrets and tokens, `boot_id`, and userspace RNG libraries, for which "there is no generic solution" ([random-for-clones](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/random-for-clones.md), [network-for-clones](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/network-for-clones.md), [snapshot-support](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md), [SnapStart](https://docs.aws.amazon.com/lambda/latest/dg/snapstart-uniqueness.html)) |
| 5 | "Converting OCI images to a rootfs is the way to keep one Template for all Runtimes (ADR 0003: the platform copies the Guest Agent into the rootfs at conversion time)." | **Holds, with caveats** | E2B does exactly this: pull OCI, flatten to ext4, inject envd, its units and a static BusyBox init ([oci.go](https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/template/build/core/oci/oci.go), [rootfs.go](https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/template/build/core/rootfs/rootfs.go)). Kata and flintlock also consume OCI images directly. **[Inference]** Caveats: (a) the platform must supply init, because OCI images rarely have one; (b) the image config (ENV, USER, WORKDIR) must be carried over by the Guest Agent, since no container runtime applies it; (c) cache by digest; (d) **Capture on Firecracker** produces ext4, not an OCI image, so keeping the Template "an OCI image reference" requires converting the ext4 diff back into an OCI layer, or relaxing the GLOSSARY; (e) a pre-booted Template snapshot is a Firecracker-only derived artifact, not a second source of truth |

---

## 4. Impact on our design

Everything in this section is **[Inference]**, ranked by importance.

1. **Re-scope and reorder phase 3** (spec §9). The current list is roughly E2B's whole data plane.
   - **3a — Cold-boot Firecracker Runtime:**
     - OCI to ext4 conversion with digest caching and injected init plus Guest Agent;
     - a 6.18 guest kernel;
     - vsock channel;
     - netns, tap and veth networking with the Egress Proxy redirect;
     - jailer, cgroup v2 limits;
     - rerun the perf and isolation suites;
     - **the SWE-bench subset**. It tests conversion fidelity, not snapshots, so it belongs here, and should start at 20–50 instances with ext4 cached per image digest.
   - **3b — Template snapshot plus Hibernate/resume on the same Host:** full snapshots, the File memory backend, and a fresh copy of the rootfs per Sandbox (or a dm thin snapshot). Measure "cold boot vs Template restore" separately (spec §8).
   - **3c — Fork:** in-place checkpoint, N restores, and the re-identity protocol (item 2).
   - **Defer or cut:**
     - auto-hibernate on idle and auto-resume on traffic (item 5);
     - UFFD lazy loading and prefetch (a stretch goal for 3b);
     - diff snapshots (developer preview);
     - cross-Host resume.
2. **A new ADR, "The Guest Agent is re-identified after every restore"**, which also updates spec §7 and ticket 16.
   - After any Template restore, resume or Fork, the Host Agent opens the vsock channel and sends a re-init before the Sandbox counts as Running. The re-init carries:
     - a **new per-incarnation token**;
     - 32–64 bytes of host entropy (`RNDADDENTROPY` + `RNDRESEEDCRNG`);
     - the wall-clock time;
     - a new `boot_id`;
     - a neighbour-table flush;
     - the env vars.
   - Until that step completes, the agent accepts only it, authenticated by the snapshot-time token. The token is known only to the Host Agent and is never reused after that step.
   - This is what E2B does with `/init`. It makes the "Fork duplicates the token" problem harmless:
     - vsock is per-VM, and without `VSOCKETS_LOOPBACK` no in-guest code can reach the agent;
     - a child that reads the parent's old token from memory (possible with a root Exec) can't reach the parent;
     - the old token stops being accepted after the re-init.
   - Add isolation tests:
     - "a Fork child's agent rejects the parent's token";
     - "two Fork children produce different `/dev/urandom` streams and different Python `random` and `uuid4` values". The last one is expected to *fail* for libraries seeded before the snapshot; document it as a known limitation, like ADR 0006's response scrubbing.
3. **Hibernate and Fork become scheduling constraints** (spec §6, GLOSSARY "Hibernated", ADR 0001).
   - A Hibernated Sandbox can resume only on a Host with the same CPU model, host kernel, Firecracker version and snapshot format. Hosts should report this "snapshot compatibility key" at registration, and the Control Plane should match on it.
   - Write a policy for Firecracker upgrades, which invalidate snapshots. Either drain Hibernated Sandboxes before an upgrade, or fall back to cold-booting from the saved disk, which is E2B's `memory: false` rescue and loses processes.
   - Spec §5 should state that a resume may lose in-flight TCP connections.
4. **Don't use `firecracker-go-sdk`** (spec §3, code layout). It was archived on 2026-11-10. The Host Agent's Firecracker Runtime should be a thin client over the unix-socket REST API, using the endpoint list in §2.2. Pin the Firecracker version, and generate models from the version's `firecracker.yaml` or hand-write the dozen request types.
5. **Auto-resume on traffic crosses component boundaries** (spec §5, ADR 0001, tickets 07 and 11).
   - Traffic for a Hibernated Sandbox arrives at a port proxy that has no running Sandbox behind it. The resume needs the Control Plane, because placement and Quota may now point to another Host. E2B does this in its edge proxy via the API.
   - So the per-Host port proxy design in spec §3 doesn't cover it. Either defer the feature (recommended), or add a central entry point that can call the Control Plane.
6. **The networking design per Sandbox** (spec §7, ticket 12 for later Runtimes, ticket 19):
   - one netns per Sandbox;
   - the same fixed guest IP and tap MAC inside every netns, so a Fork needs no in-guest reconfiguration;
   - a unique host-side veth IP;
   - in-netns nftables that allow only the Egress Proxy and the resolver;
   - host-side REDIRECT of 80/443 and 53;
   - a hard drop of `169.254.169.254`, Host addresses and private ranges.
   
   Add to the isolation tests:
   - "guest connects `AF_VSOCK` to CID 2 on any port and is reset";
   - "no `v.sock_*` listener exists on the Host".
7. **Use the jailer, plus Firecracker's host checklist, as the phase-3 isolation curriculum** (spec §8, `docs/learn/03-firecracker.md`).
   - Pair each attack with the layer that blocks it: KVM, then seccomp, then the jailer's chroot, uid and cgroup, then netns and nftables.
   - Note honestly that E2B runs Firecracker as root without the jailer.
   - The host checklist: cgroups v2; swap, KSM and the serial console off; SMT on or off recorded as a variable.
8. **Guest kernel: use 6.18, not 6.1** (spec §9). Firecracker's minimum support date for 6.1 guests (2026-09-02) has passed. Turn on `CONFIG_VMGENID`, `CONFIG_HW_RANDOM_VIRTIO` and `CONFIG_VIRTIO_VSOCKETS`, leave `CONFIG_VSOCKETS_LOOPBACK` off, and attach the `/entropy` device.
9. **Where to run** (spec §9). GCP n2 with nested virtualization is fine for learning. Keep it Intel, not N4D.
   - Report every Firecracker number with a "nested" label.
   - Add one optional bare-metal calibration run (Hetzner AX hourly, or an AWS `.metal`).
   - AWS c8i/m8i nested is now an equivalent alternative.
   - Check the host kernel against Firecracker's supported list (5.10, 6.1 or 6.18) and record it.
10. **Usage and Quota** (spec §6, ticket 09).
    - Hibernated storage is roughly the memory size plus the disk diff, so meter both.
    - An in-place checkpoint for Fork briefly pauses the parent. Decide whether that is a Frozen interval or stays Running.
    - Disk quotas must cover the **host-side** CoW and snapshot store, as Firecracker requires.
11. **Capture on Firecracker** (ticket 22, GLOSSARY "Template"). Decide before 3a whether Capture produces an OCI layer (ext4 diff to tar to a pushed image), which keeps "Template = OCI reference", or a platform-internal ext4 Template. Either way the acceptance criterion "the Guest Agent binary and token are not baked in" means deleting the injected files before export.
12. **The Process and event-stream ticket (06) gains a Firecracker acceptance test:** "after Hibernate/resume and after Fork, no Process exit event is lost". Every snapshot resets vsock connections, so the sequence-number resume path is mandatory.

---

## 5. Sources (all accessed 2026-11-21)

**Firecracker** (main branch unless a version is given; latest release v1.18.0)
- README https://github.com/firecracker-microvm/firecracker/blob/main/README.md ; Releases https://github.com/firecracker-microvm/firecracker/releases ; CHANGELOG https://github.com/firecracker-microvm/firecracker/blob/main/CHANGELOG.md ; SPECIFICATION https://github.com/firecracker-microvm/firecracker/blob/main/SPECIFICATION.md
- Design and threat model https://github.com/firecracker-microvm/firecracker/blob/main/docs/design.md ; Production host setup https://github.com/firecracker-microvm/firecracker/blob/main/docs/prod-host-setup.md ; Jailer https://github.com/firecracker-microvm/firecracker/blob/main/docs/jailer.md ; Seccomp https://github.com/firecracker-microvm/firecracker/blob/main/docs/seccomp.md
- Snapshot support https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md ; Versioning https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/versioning.md ; Page faults / UFFD https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/handling-page-faults-on-snapshot-resume.md ; Random for clones https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/random-for-clones.md ; Network for clones https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/network-for-clones.md
- vsock https://github.com/firecracker-microvm/firecracker/blob/main/docs/vsock.md ; Network setup https://github.com/firecracker-microvm/firecracker/blob/main/docs/network-setup.md ; Hugepages https://github.com/firecracker-microvm/firecracker/blob/main/docs/hugepages.md ; Ballooning https://github.com/firecracker-microvm/firecracker/blob/main/docs/ballooning.md
- Kernel policy https://github.com/firecracker-microvm/firecracker/blob/main/docs/kernel-policy.md ; Rootfs and kernel setup https://github.com/firecracker-microvm/firecracker/blob/main/docs/rootfs-and-kernel-setup.md ; Dev machine setup https://github.com/firecracker-microvm/firecracker/blob/main/docs/dev-machine-setup.md ; API spec https://github.com/firecracker-microvm/firecracker/blob/main/src/firecracker/swagger/firecracker.yaml
- VFIO tracking issue https://github.com/firecracker-microvm/firecracker/issues/5679 ; closed PR https://github.com/firecracker-microvm/firecracker/pull/5870 ; `feature/vfio` PRs #6183 #6219 #6241 #6252 #6316 (merged into the branch), #6356 #6398 (open), checked via the GitHub API
- firecracker-go-sdk (archived) https://github.com/firecracker-microvm/firecracker-go-sdk ; firecracker-containerd (archived) https://github.com/firecracker-microvm/firecracker-containerd
- Agache et al., "Firecracker: Lightweight Virtualization for Serverless Applications", NSDI '20 https://www.usenix.org/conference/nsdi20/presentation/agache

**Cloud providers and hosting**
- GCP nested virtualization overview https://docs.cloud.google.com/compute/docs/instances/nested-virtualization/overview ; enabling https://docs.cloud.google.com/compute/docs/instances/nested-virtualization/enabling
- AWS user guide https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/amazon-ec2-nested-virtualization.md ; What's New 2026-02-16 https://aws.amazon.com/about-aws/whats-new/2026/02/amazon-ec2-nested-virtualization-on-virtual ; What's New 2026-06-18 https://aws.amazon.com/about-aws/whats-new/2026/06/nested-virtualization-intel-us-gov-cloud/
- AWS Lambda SnapStart uniqueness https://docs.aws.amazon.com/lambda/latest/dg/snapstart-uniqueness.html
- Hetzner AX matrix https://www.hetzner.com/dedicated-rootserver/matrix-ax/ (prices change; excl. VAT)

**Kernel**
- Running nested guests https://docs.kernel.org/virt/kvm/x86/running-nested-guests.html
- af_vsock.c https://github.com/torvalds/linux/blob/master/net/vmw_vsock/af_vsock.c ; af_vsock.h https://github.com/torvalds/linux/blob/master/include/net/af_vsock.h ; vm_sockets.h https://github.com/torvalds/linux/blob/master/include/uapi/linux/vm_sockets.h ; vmgenid.c https://github.com/torvalds/linux/blob/master/drivers/virt/vmgenid.c

**E2B runtime** (commit `89e7715`, 2026-11-21)
- README https://github.com/e2b-dev/runtime ; ARCHITECTURE https://github.com/e2b-dev/runtime/blob/main/docs/ARCHITECTURE.md ; DEV-LOCAL https://github.com/e2b-dev/runtime/blob/main/DEV-LOCAL.md
- Firecracker process https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/sandbox/fc/process.go ; start script https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/sandbox/fc/script_builder.go ; API client https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/sandbox/fc/client.go ; OpenAPI copy https://github.com/e2b-dev/runtime/blob/main/packages/shared/pkg/fc/firecracker.yml
- OCI to ext4 https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/template/build/core/oci/oci.go ; ext4 https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/template/build/core/filesystem/ext4.go ; rootfs injection https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/template/build/core/rootfs/rootfs.go
- Network slot https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/sandbox/network/slot.go ; network setup https://github.com/e2b-dev/runtime/blob/main/packages/orchestrator/pkg/sandbox/network/network.go
- Kernels https://github.com/e2b-dev/runtime/tree/main/firecracker/fc-kernels ; kernel config https://github.com/e2b-dev/runtime/blob/main/firecracker/fc-kernels/configs/x86_64/6.1.190.config ; Firecracker builds https://github.com/e2b-dev/runtime/tree/main/firecracker/fc-versions ; BusyBox https://github.com/e2b-dev/runtime/tree/main/firecracker/fc-busybox
- Embed Terraform: GCP https://github.com/e2b-dev/runtime/blob/main/embed/terraform/gcp/variables.tf , https://github.com/e2b-dev/runtime/blob/main/embed/terraform/gcp/main.tf ; AWS https://github.com/e2b-dev/runtime/blob/main/embed/terraform/aws/variables.tf

**Other primary sources**
- flintlock https://github.com/liquidmetal-dev/flintlock
- Kata Containers with Firecracker https://github.com/kata-containers/kata-containers/blob/main/docs/how-to/how-to-use-kata-containers-with-firecracker.md ; versions.yaml https://github.com/kata-containers/kata-containers/blob/main/versions.yaml
- Brooker et al., "Restoring Uniqueness in MicroVM Snapshots", arXiv 2102.12892 https://arxiv.org/abs/2102.12892
- Marc Brooker, blog post 2025-09-18 https://brooker.co.za/blog/2025/09/18/firecracker
- CodeSandbox, "Cloning microVMs using userfaultfd" https://codesandbox.io/blog/cloning-microvms-using-userfaultfd (returned 403 to my fetch; cited for existence only)

**Outdated or version-dependent items to re-check:** Firecracker snapshot format and guest-kernel support dates; VFIO status; AWS and GCP nested-virtualization instance lists; Hetzner prices; E2B internals, which change daily behind feature flags.
