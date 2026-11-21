# GPU sandboxing (phase 4): isolation, passthrough and start time

> English | [中文](gpu-sandboxing.zh.md)

> Research date: 2026-11-21. Every link below was accessed on that day unless noted otherwise. Prices are on-demand list prices read on 2026-11-21 and change often.
> Primary sources only: NVIDIA docs and blogs, Linux kernel docs and source, VMM docs, source and issue trackers (Cloud Hypervisor, QEMU, Firecracker), gVisor docs, cloud provider pricing and quota pages, vendor engineering blogs, and CVE records. A few points have only a secondary source; they are labelled **(secondary)**.
> Anything marked **[Inference]** is my own conclusion, not what the source says. Terms follow the project [GLOSSARY](../../GLOSSARY.md). This note does not repeat [agent-sandbox-usage.md](agent-sandbox-usage.md), which already covers Modal's two runtimes and Daytona v2's choice of hypervisor at a high level.

---

## 1. Summary

- **Container-level GPU shares the host's NVIDIA kernel driver with every tenant.** The NVIDIA Container Toolkit (or a CDI spec) only injects device nodes, driver libraries and hooks into the container. gVisor's `nvproxy` puts a userspace kernel in front of the host, but it forwards an allowlisted set of `ioctl`s to the same host driver. gVisor's own docs say it is "much less effective" against bugs in the NVIDIA driver itself ([gVisor GPU](https://gvisor.dev/docs/user_guide/gpu/)). The toolkit's hooks have had two container escapes: CVE-2024-0132 and CVE-2025-23266 ([NVD API](https://services.nvd.nist.gov/rest/json/cves/2.0?cveId=CVE-2025-23266)).
- **Hard isolation means a whole GPU given to a VM through VFIO.** You need an IOMMU, a viable IOMMU group with every device in it bound to `vfio-pci` ([kernel VFIO](https://docs.kernel.org/driver-api/vfio.html)), and a VMM that supports it. Cloud Hypervisor and QEMU support it. **Firecracker still has no released VFIO support.** PCI landed in v1.13, but VFIO lives on a `feature/vfio` branch, is tested only with NVMe, and rejects snapshots ([#5679](https://github.com/firecracker-microvm/firecracker/issues/5679), [#6055](https://github.com/firecracker-microvm/firecracker/pull/6055)).
- **You can't do VFIO passthrough inside a GPU cloud VM.** To assign a device to a nested VM, the L1 needs an IOMMU, and the kernel's no-IOMMU mode explicitly cannot do VM device assignment ([VFIO Kconfig](https://raw.githubusercontent.com/torvalds/linux/master/drivers/vfio/Kconfig)). AWS supports nested virtualization only on C/M/R/I/X families, none with a GPU ([AWS nested](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/amazon-ec2-nested-virtualization.html)). GCP doesn't document a vIOMMU. You need bare metal.
- **"A GPU behind an API call in seconds" mostly comes down to five costs**, in rough order:
  1. getting a healthy GPU host, which takes minutes and which Modal solves by keeping a buffer of idle GPUs;
  2. model weights, which are throughput-bound at a few GB/s;
  3. engine setup such as CUDA graphs and `torch.compile`;
  4. pinning VM memory for VFIO, which grows linearly with guest RAM (253 s for a 1.2 TB guest);
  5. BAR mapping of large-BAR GPUs, about 1.5 s per GPU, or 1–3 extra minutes for 4×H100 attached at boot.

  Sources: [Modal](https://modal.com/blog/truly-serverless-gpus), [Canonical](https://cpaelzer.github.io/blogs/007-vfio-slow-startup), [LKML](https://lkml.iu.edu/hypermail/linux/kernel/2411.3/03532.html). The industry answer is to checkpoint and restore CUDA state with `cuda-checkpoint` and the CRIU CUDA plugin. Modal reports 4–10× faster starts this way.
- **Handing a GPU from one tenant to the next is a real problem, and NVIDIA only documents a memory scrub for Confidential Computing mode.** In CC mode the firmware "scrubs the GPU state and memory" on reset ([NVIDIA CC blog](https://developer.nvidia.com/blog/confidential-computing-on-h100-gpus-for-secure-and-trustworthy-ai/)). `vfio-pci` resets the function (FLR, falling back to a bus reset) when the device is opened and released ([vfio_pci_core.c](https://raw.githubusercontent.com/torvalds/linux/master/drivers/vfio/pci/vfio_pci_core.c)). But no NVIDIA page I found says a reset clears VRAM outside CC mode. LeftoverLocals (CVE-2023-4969) affected Apple, AMD, Qualcomm and Imagination, but NVIDIA said it was not affected ([Trail of Bits](https://blog.trailofbits.com/2024/01/16/leftoverlocals-listening-to-llm-responses-through-leaked-gpu-local-memory/)).
- **Of the sharing modes, only whole-GPU and MIG give hardware isolation.** Time-slicing has "no memory or fault-isolation" ([GPU Operator](https://docs.nvidia.com/datacenter/cloud-native/gpu-operator/latest/gpu-sharing.html)). MPS clients run as one UID with "limited" error containment ([MPS](https://docs.nvidia.com/deploy/mps/when-to-use-mps.html)). MIG needs Ampere or later datacenter parts. The L4, T4 and A10 that learners can afford don't support MIG ([MIG GPUs](https://docs.nvidia.com/datacenter/tesla/mig-user-guide/supported-gpus.html)).
- **Vendors split into two camps.**
  - gVisor plus nvproxy on shared hosts: Modal (GPU Sandboxes are gVisor-only), GKE Sandbox, Beam, and Northflank when nested virtualization is unavailable.
  - Cloud Hypervisor plus whole-GPU passthrough on dedicated hosts: Daytona v2, and Fly.io, which spent months on NVIDIA drivers, then scaled GPUs back and reportedly ends them on 2026-07-31 **(secondary)**.
- **A realistic phase 4 for a learner** has two parts:
  - 4a: Docker and gVisor with GPUs on a GCP `g2-standard-4` ($0.7068/h on demand, $0.4230/h spot, us-central1) or an AWS `g4dn.xlarge` ($0.526/h, us-east-1).
  - 4b: VFIO with Cloud Hypervisor, not Firecracker, on bare metal: Hetzner GEX45 (€214/month plus €209 setup, or €0.3429/h; the GEX44 is no longer on the price list), AWS `g5g.metal` ($2.744/h, Arm with 2×T4G), or `g4dn.metal` ($7.824/h, 8×T4).

  AWS GPU quota starts at 0 vCPUs, so you must request an increase before launching any of the AWS options ([AWS quotas](https://docs.aws.amazon.com/ec2/latest/instancetypes/ec2-instance-quotas.html)).

---

## 2. Findings by question

### 2.1 Container-level GPU: NVIDIA Container Toolkit, CDI and gVisor `nvproxy`

**How the toolkit exposes a GPU.**

- The NVIDIA Container Runtime "is a thin wrapper around the native runC". It adds the NVIDIA hook as a `prestart` hook, and from v1.12.0 it also edits the OCI spec "to inject specific devices and mounts" ([architecture](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/arch-overview.html)).
- The install guide's prerequisite is to "Install the NVIDIA GPU driver for your Linux distribution" on the machine that runs the toolkit ([install guide](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html)).
- CDI is "an open specification for container runtimes". Since toolkit v1.18.0 a systemd unit `nvidia-cdi-refresh` regenerates `/var/run/cdi/nvidia.yaml` automatically. Devices are named like `nvidia.com/gpu=0`, and MIG devices like `nvidia.com/gpu=1:0`. The spec carries hooks such as `create-symlinks`, `update-ldcache` and `enable-cuda-compat` ([CDI](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/cdi-support.html)).
- **[Inference]** Every GPU container on a Host talks to one shared NVIDIA kernel module, and through it to one shared Linux kernel. Only the userspace libraries are per-container. That is the same trust model as Docker for CPU, plus a large closed-source driver attack surface.

**The toolkit itself has been the escape path.**

- CVE-2024-0132: a TOCTOU in toolkit 1.16.1 and earlier in its default configuration, through which "a specifically crafted container image may gain access to the host file system". NVD adds: "This does not impact use cases where CDI is used" ([NVD API](https://services.nvd.nist.gov/rest/json/cves/2.0?cveId=CVE-2024-0132)).
- CVE-2025-23266: a flaw in "some hooks used to initialize the container" that lets "an attacker … execute arbitrary code with elevated permissions" ([NVD API](https://services.nvd.nist.gov/rest/json/cves/2.0?cveId=CVE-2025-23266)).
  - Wiz, who found it, explains the mechanism. The `createContainer` hook runs privileged on the host, inherits `LD_PRELOAD` from the image, and runs "with its working directory set to the container's root filesystem".
  - Wiz concludes that "containers are not a strong security barrier and should not be relied upon as the sole means of isolation" ([Wiz, 2025-07-17](https://www.wiz.io/blog/nvidia-ai-vulnerability-cve-2025-23266-nvidiascape)).
  - The fixed versions given (toolkit 1.17.8, GPU Operator 25.3.1) come from news coverage **(secondary)**. Confirm them in NVIDIA's bulletin.

**gVisor `nvproxy`** ([gVisor GPU guide](https://gvisor.dev/docs/user_guide/gpu/)):

- **What it is.** A passthrough driver in the gVisor Sentry that "forwards `ioctl(2)` calls made to NVIDIA devices" to the host driver. It "does not emulate NVIDIA kernel-mode driver (KMD) logic". Because NVIDIA's ioctl ABI isn't stable, nvproxy keeps per-driver-version ABI tables.
- **GPUs.** Officially supported: T4, A100, A10G, L4 and H100. Other GPUs of the same microarchitectures "will likely work".
- **Drivers.** An allowlist that tracks GKE's driver versions. Unsupported drivers need `--nvproxy-allow-unsupported-driver`; unknown drivers always fail. `runsc nvproxy list-supported-drivers` prints the list.
- **Surface.** By default only the `compute` and `utility` capabilities are allowed. It exposes `/dev/nvidiactl`, `/dev/nvidia-uvm` and `/dev/nvidia#`, but not `/dev/nvidia-caps/*`, so MIG doesn't work. Only a curated ioctl set is allowed. On the KVM platform, `cudaMallocManaged()` is flaky.
- **How to use it.** Pass `--nvproxy`, then use `docker run --runtime=runsc --gpus=all`, or CDI with containerd ≥ 1.7. `nvidia-container-toolkit` is still required on the host.
- **The security argument, in gVisor's own words.**
  - The seccomp filters allow only allowlisted ioctls, so past Linux kernel CVEs "remain mitigated even when 'nvproxy' is used".
  - But "gVisor is much less effective at mitigating vulnerabilities within the NVIDIA GPU drivers themselves, _because_ gVisor passes through calls".
  - "There is no validation of things like DMA buffers."
  - "it is imperative that users update NVIDIA drivers in a timely manner."
- **GKE Sandbox.** GKE runs GPU workloads under gVisor:
  - GA on H100, A100, L4 and T4; preview on RTX PRO 6000, GB200, B200 and H200.
  - "Only CUDA workloads are supported".
  - "Don't use GPU time-sharing in sandboxed Pods, because the GPU isn't fully isolated between Pods".
  - "gVisor doesn't mitigate all NVIDIA driver vulnerabilities, but retains protection against Linux kernel vulnerabilities" ([GKE Sandbox](https://docs.cloud.google.com/kubernetes-engine/docs/concepts/sandbox-pods)).
- **Production users, as stated by the vendors themselves:**
  - **Modal.** "GPU Sandboxes are only supported with `runtime="gvisor"`" ([Modal sandboxes](https://modal.com/docs/guide/sandboxes.md)). Modal's 2026-05-12 engineering post says user containers run under `runsc`, and that Modal's "`nvproxy` sub-component" talks to the kernel-mode driver and that Modal contributes to it ([Modal, truly serverless GPUs](https://modal.com/blog/truly-serverless-gpus)).
  - **gVisor's users page.** Lists Beam ("gVisor isolation, GPU acceleration") and Northflank, which runs GPU workloads in gVisor "when nested virtualization is unavailable" ([gVisor users](https://gvisor.dev/users/)).
- **[Inference]** With gVisor, a CDI or legacy-mode NVIDIA hook still runs on the host *before* the sandbox exists. gVisor therefore does not protect against toolkit-hook bugs like CVE-2025-23266. Patching the toolkit and preferring CDI matter equally under Docker and gVisor.

### 2.2 VM-level GPU passthrough (VFIO)

**What VFIO needs** ([kernel VFIO](https://docs.kernel.org/driver-api/vfio.html)):

- **An IOMMU, and the IOMMU group as the unit of ownership.** "A group is a set of devices which is isolatable from all other devices in the system", and "Groups are therefore the unit of ownership used by VFIO".
- **ACS.** A bridge without ACS can let traffic be redirected "without reaching the IOMMU", which forces the devices behind it into one group.
- **A viable group.** Every device in the group must be bound to a VFIO driver (or left unbound) before the group is usable.
- **Binding.** Unbind the GPU from `nvidia`/`nouveau`, `modprobe vfio-pci`, then write the vendor and device ID to `new_id`. Do the same for the GPU's audio function, which is usually in the same group.
- **Without an IOMMU there is no device assignment.** The no-IOMMU mode taints the kernel, and "Device assignment to virtual machines is also not possible with this mode since there is no IOMMU to provide DMA translation" ([VFIO Kconfig](https://raw.githubusercontent.com/torvalds/linux/master/drivers/vfio/Kconfig)).
- **Memory pinning.** A VMM must pin, and map into the IOMMU, all guest memory before the guest starts:
  - Firecracker's maintainers: passthrough "implies pinning physical memory which would remove our memory oversubscription capabilities" ([#849](https://github.com/firecracker-microvm/firecracker/issues/849)), and microVMs would have to "start with their full memory mapped" ([#1179](https://github.com/firecracker-microvm/firecracker/issues/1179)).
  - Firecracker's VFIO PR is incompatible with virtio-balloon and virtio-mem ([#6055](https://github.com/firecracker-microvm/firecracker/pull/6055)).
- **BARs.** Datacenter GPUs expose very large 64-bit BARs; Alex Williamson mentions "large 128GB BARs" on DGX H100 ([LKML](https://lkml.iu.edu/hypermail/linux/kernel/2411.3/03532.html)). Cloud Hypervisor documents that many devices' 32-bit MMIO BARs can exhaust a PCI segment's aperture. It provides `mmio32_aperture_weight` and `x_exclude_mmap_bars` to manage this ([CH VFIO](https://github.com/cloud-hypervisor/cloud-hypervisor/blob/main/docs/vfio.md)).

**Which VMMs support it (as of 2026-11-21):**

| VMM | GPU passthrough | Evidence |
|---|---|---|
| **QEMU** | Yes, long-standing. Supports the legacy VFIO container or the iommufd backend: `-device vfio-pci,host=0000:02:00.0[,iommufd=…]` | [QEMU iommufd](https://www.qemu.org/docs/master/devel/vfio-iommufd.html) |
| **Cloud Hypervisor** | Yes. `--device path=/sys/bus/pci/devices/…`, optionally with `--platform iommufd=on`. Documents NVIDIA GPUDirect P2P cliques (Turing, Ampere, Hopper, Lovelace). Supports VFIO **hot-plug** with `ch-remote add-device`, which requires ACPI. Snapshot and live migration work only with migration-capable *variant* drivers; "Generic `vfio_pci` is not migratable" | [CH VFIO](https://github.com/cloud-hypervisor/cloud-hypervisor/blob/main/docs/vfio.md), [CH hotplug](https://github.com/cloud-hypervisor/cloud-hypervisor/blob/main/docs/hotplug.md) |
| **Firecracker** | **No released support.** v1.13.0: "Added PCI support in Firecracker. PCI support is optional." That is virtio over PCI, not passthrough. The VFIO tracking issue #5679 has been open since 2026-02-11 and says vGPU and MIG GPUs "will be supported (and we test it internally)". PR #5870 was closed in favour of #6055, which was retargeted to `feature/vfio`, closed unmerged on 2026-10-21, NVMe-tested only, "Pre-boot only", and rejects snapshots. No release up to v1.18.0 (Nov 9) mentions VFIO. A maintainer said in 2025: "We do not have date yet for official GPU support in Firecracker" | [CHANGELOG](https://github.com/firecracker-microvm/firecracker/blob/main/CHANGELOG.md), [releases](https://github.com/firecracker-microvm/firecracker/releases), [#5679](https://github.com/firecracker-microvm/firecracker/issues/5679), [#5870](https://github.com/firecracker-microvm/firecracker/pull/5870), [#6055](https://github.com/firecracker-microvm/firecracker/pull/6055), [#1179](https://github.com/firecracker-microvm/firecracker/issues/1179) |

**Nested: why a cloud GPU VM can't pass its GPU again.**

- The kernel rule above means an L1 without an IOMMU can't assign a device to an L2.
- AWS's nested virtualization (2026) is limited to M7i/M8i, C7i/C8i, R7i/R8i, X8i and I7i/I7ie families. No G or P family is on the list, and AWS recommends bare metal for performance-sensitive cases ([AWS nested](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/amazon-ec2-nested-virtualization.html)).
- GCP's nested virtualization page lists only KVM as the L1 hypervisor and excludes E2, ARM, most AMD and most memory-optimized VMs. It says nothing about GPUs, IOMMU or VFIO in the L2 ([GCP nested](https://docs.cloud.google.com/compute/docs/instances/nested-virtualization/overview)).
- **[Inference]** The accurate form of the claim is this: nested passthrough would need the cloud to expose a virtual IOMMU to the L1 (QEMU can do this for its own guests), and neither GCP nor AWS documents one. So in practice you need bare metal, or a `.metal` instance, where you control the real IOMMU.
- **[Inference]** GPU sharing still works *inside* a cloud GPU VM, because it doesn't need nested virtualization: Docker plus the toolkit, and gVisor plus nvproxy. That is exactly what GKE Sandbox and Modal sell.

### 2.3 "A GPU behind an API call in seconds": what dominates start time

| Phase | What the sources say | Mitigation that vendors or engineers describe |
|---|---|---|
| **Getting a GPU host** | Spinning up and health-checking a new instance takes "minutes to tens of minutes" ([Modal](https://modal.com/blog/truly-serverless-gpus)) | Modal keeps a buffer of idle, healthy GPUs shared across apps, sized by a linear program. It runs brief health checks at boot and `dcgmi diag` weekly (same source) |
| **VFIO binding** | No timing found in primary sources. **[Inference]** It's a sysfs write that is cheap compared with everything below; do it once when the Host starts, not per Sandbox | Keep GPUs bound to `vfio-pci` permanently on a passthrough Host |
| **VMM memory pinning** | Pinning and IOMMU-mapping guest memory is linear in guest RAM. For a 1.2 TB guest, preparation took 253 s with VFIO versus 6 s without; 1 GiB hugepages cut it to 59 s; disabling THP made it worse (476 s). On a 128 GiB guest it ranged from seconds to about 10 minutes depending on fragmentation ([Canonical engineer's blog, 2024-04-28](https://cpaelzer.github.io/blogs/007-vfio-slow-startup)) | 1 GiB hugepages, pre-allocation, correct NUMA placement (same source) |
| **BAR mapping at guest PCI enumeration** | 4×H100 passthrough added "1-3 extra minutes" when attached at boot, versus "under 20 seconds cumulatively" when hot-plugged after boot. About 1.5 s per GPU per re-initialization. Hugepages didn't help. Williamson: "The BAR space is walked, faulted, and mapped" ([LKML, 2024-11-26](https://lkml.iu.edu/hypermail/linux/kernel/2411.3/03532.html)) | Hot-plug after boot. Newer kernels map pfnmap regions with large pages (same thread) |
| **Guest driver load and init** | Without persistence, the GPU is "initialized and deinitialized" per job, causing "long load times for each CUDA job, on the order of seconds" ([NVIDIA driver persistence](https://docs.nvidia.com/deploy/driver-persistence/background.html)). ECC scrubbing runs "during driver initialization" ([nvidia-smi](https://docs.nvidia.com/deploy/nvidia-smi/index.html)) | `nvidia-persistenced`, which "sleeps with the device files open" ([persistence daemon](https://docs.nvidia.com/deploy/driver-persistence/persistence-daemon.html)) |
| **CUDA context and module load** | "Lazy loading reduces program initialization time by waiting to load CUDA modules until they are needed". It is the default since CUDA 12.2 on Linux and 12.3 everywhere ([CUDA guide](https://docs.nvidia.com/cuda/cuda-programming-guide/04-special-topics/lazy-loading.html)) | Lazy loading; GPU memory snapshots (below) |
| **Engine setup** (CUDA graphs, `torch.compile`) | "between tens of seconds and minutes"; CUDA graphs have no native serialization ([Modal](https://modal.com/blog/truly-serverless-gpus)) | GPU memory snapshots skip it entirely (ViT with `torch.compile`: 8.5 s → 2.25 s) ([Modal GPU snapshots](https://modal.com/blog/gpu-mem-snapshots)) |
| **Model weights** | A few GB/s, so seconds to hundreds of seconds. Snapshots don't help because the load is throughput-bound ([Modal](https://modal.com/blog/truly-serverless-gpus)) | Lazy content-addressed filesystem with tiered caches (page cache, local SSD, AZ cache, CDN, blob). An in-AZ or RDMA weight server is "working on it" (same source) |

**GPU memory snapshotting, with the current state of each tool:**

- **`cuda-checkpoint`** ([GitHub](https://github.com/NVIDIA/cuda-checkpoint), [NVIDIA blog 2024-07-02](https://developer.nvidia.com/blog/checkpointing-cuda-applications-with-criu/)):
  - **How it works.** It locks the CUDA APIs, waits for submitted work, copies device memory to host memory, and releases the GPU. "A suspended CUDA process no longer directly refers to any GPU hardware at the OS level", so CRIU can then dump it.
  - **Driver support.** Starts at driver 550. 570 added CRIU 4.0+ integration, 580 added GPU migration and "container partial passthrough", 595 added Arm, and 610 added `cuIpcGetMemHandle` IPC.
  - **Still unsupported.** UVM, and IPC memory from `cuMemExportToShareableHandle()`.
- **CRIU CUDA plugin** ([CRIU plugins/cuda](https://github.com/checkpoint-restore/criu/tree/criu-dev/plugins/cuda)):
  - **Backends.** A driver-API backend for CUDA ≥ 13 and a `cuda-checkpoint` CLI backend for r565/r570.
  - **Moving to other GPUs.** It can remap GPUs on restore by UUID or ordinal, but the targets "must still be compatible and have enough memory".
  - **Not supported.** NVIDIA UVM managed memory, MIG and MPS.
  - **A security warning.** Untrusted workloads not rooted at PID 1 of a PID namespace may escape tracing through `CLONE_UNTRACED`, so filtering that flag with seccomp is recommended.
- **Modal** ([GPU snapshots, 2025-07-30](https://modal.com/blog/gpu-mem-snapshots), [truly serverless, 2026-05-12](https://modal.com/blog/truly-serverless-gpus)):
  - **Mechanism.** The CUDA checkpoint API (570/575 branches), "fully integrated with our existing gVisor checkpoint/restore system". Snapshots can restore on a different host with compatible GPU hardware.
  - **Speedups.** "typical speedups are between 4-10x". vLLM with Qwen3-0.6B went from 95.7 s to 13.8 s mean.
  - **Limits.** Single-GPU only; NCCL programs can deadlock. CPU snapshots are sensitive to host CPU features; for example `g6.12xlarge` lacks `pclmulqdq`.
- **Daytona v2** documents that a GPU Sandbox checkpoint "saves its processes, memory, and GPU memory" and "resumes in a new GPU sandbox", but it doesn't say how ([Daytona sandboxes](https://www.daytona.com/docs/sandboxes.md)). The same page marks pause/resume, fork and memory persistence as unsupported for GPU Sandboxes. **[Inference]** Given that generic `vfio-pci` devices can't be snapshotted by Cloud Hypervisor, this is most likely an in-guest CUDA checkpoint followed by a CPU-side snapshot, not a VMM snapshot of the passthrough device.
- **Cloud Hypervisor and VFIO snapshots.**
  - On-demand (lazy) restore: "VFIO devices are not supported".
  - Hot-plugging a VFIO device *after* restore works but forces every guest page to be write-faulted, so page-cache sharing is lost ([CH snapshot_restore](https://github.com/cloud-hypervisor/cloud-hypervisor/blob/main/docs/snapshot_restore.md)).
  - **[Inference]** This suggests a pattern for fast GPU VM starts: restore a pre-booted, GPU-less VM snapshot, then hot-plug the already-reset GPU, then restore the CUDA state with `cuda-checkpoint` inside the guest. It is unverified, and it is the most interesting thing to measure in phase 4b.

### 2.4 Multi-tenant safety between tenants

- **What reset does.**
  - `vfio-pci` calls `pci_try_reset_function()` when a device is opened. On release it restores saved state and resets the function again. If the device "needs_reset (such as by lack of FLR support)" and every device on the bus is unused, it falls back to `pci_reset_bus()`, the secondary bus reset ([vfio_pci_core.c](https://raw.githubusercontent.com/torvalds/linux/master/drivers/vfio/pci/vfio_pci_core.c)).
  - `nvidia-smi -r` "can be used to clear GPU HW and SW state", requires root, and requires that no application is using the device. That doc says nothing about clearing framebuffer memory ([nvidia-smi](https://docs.nvidia.com/deploy/nvidia-smi/index.html)).
- **Scrubbing is documented for CC mode.** NVIDIA's H100 CC post (2023-08-03) says that when CC mode is enabled and at tenant shutdown, the host issues a reset and "GPU firmware scrubs the GPU state and memory" ([NVIDIA CC blog](https://developer.nvidia.com/blog/confidential-computing-on-h100-gpus-for-secure-and-trustworthy-ai/)). An ACM Queue article by NVIDIA engineers reportedly says a PF-FLR "will scrub memory … before the GPU is handed to the next tenant" (from search snippet; the page returned 403) **(unverified)**.
- **[Inference]** Outside CC mode I found no NVIDIA statement that FLR or SBR clears VRAM. A platform must therefore *test* for leftover data rather than assume it is cleared: write a canary to VRAM in Sandbox A, destroy it, hand the GPU to Sandbox B and scan VRAM. That test belongs in the isolation suite.
- **LeftoverLocals (CVE-2023-4969).**
  - GPU local memory isn't cleared between kernels, so a co-resident process can read another's data, including LLM output. Confirmed on Apple, AMD, Qualcomm and Imagination.
  - "NVIDIA: confirmed that their devices are not currently impacted".
  - The researchers suggest avoiding multi-tenant GPU environments, and note that partitioned cloud GPUs (vGPU, MxGPU) may not be affected ([Trail of Bits](https://blog.trailofbits.com/2024/01/16/leftoverlocals-listening-to-llm-responses-through-leaked-gpu-local-memory/), [NVD](https://services.nvd.nist.gov/rest/json/cves/2.0?cveId=CVE-2023-4969)).
  - **[Inference]** The lesson is about the class of bug: any time two tenants share a GPU concurrently (time-slicing, MPS, nvproxy on one GPU), hardware state not cleared between them becomes a channel between tenants.
- **MIG as a boundary.**
  - NVIDIA says each instance has "separate and isolated paths through the entire memory system". L2 and DRAM bandwidth are guaranteed, MIG provides "fault isolation for different clients such as VMs, containers or processes", and it supports passthrough and vGPU to VMs ([MIG intro](https://docs.nvidia.com/datacenter/tesla/mig-user-guide/introduction.html)).
  - Supported parts: A100, A30, H100, H200, H20, B200, B300, GB200 (up to 7 instances) and RTX PRO Blackwell (2–4 instances) ([MIG GPUs](https://docs.nvidia.com/datacenter/tesla/mig-user-guide/supported-gpus.html)).
  - Caveats: nvproxy doesn't expose `/dev/nvidia-caps` ([gVisor](https://gvisor.dev/docs/user_guide/gpu/)). The CRIU CUDA plugin doesn't support MIG ([CRIU](https://github.com/checkpoint-restore/criu/tree/criu-dev/plugins/cuda)). Fly.io: "MIG gives you a UUID to talk to the host driver, not a PCI device", so it was unusable for their micro-VMs ([Fly.io](https://fly.io/blog/wrong-about-gpu/)).
- **Confidential computing.**
  - H100 CC modes are CC-Off, CC-On and CC-DevTools. CC-On disables performance counters to prevent side channels. CPU↔GPU traffic goes through encrypted bounce buffers at "roughly 4 GBytes/sec" ([NVIDIA CC blog](https://developer.nvidia.com/blog/confidential-computing-on-h100-gpus-for-secure-and-trustworthy-ai/)).
  - The R595 release notes list Hopper (H100, H200, …) and Blackwell (HGX B200, RTX PRO 6000 Server Edition). Modes: "Single GPU Passthrough (SPT CC)", Hopper multi-GPU with "Protected PCIe (PPCIe)", and "Blackwell Multiple GPU Passthrough (MPT CC)". GB200 isn't mentioned ([R595 TRD1](https://docs.nvidia.com/confidential-computing/release-notes/r595-trd1.md)).
  - **[Inference]** CC is always a passthrough mode into a TDX or SEV-SNP confidential VM. It is out of reach for a learner (it needs a CC-capable CPU, an H100 or B200, and attestation) but worth one section in the design doc, as the "tenant doesn't trust the host" tier.
- **Firecracker's two original objections (2019)** are still the clearest statement of the problem: memory pinning removes oversubscription, and "We can only run 1 customer workload securely per physical GPU", with switching "long enough … to make it impractical" ([#849](https://github.com/firecracker-microvm/firecracker/issues/849)).

### 2.5 Sharing vs dedicating

| Mode | What is shared | Hard isolation between Tenants? | Source |
|---|---|---|---|
| Whole GPU via VFIO into a VM | Nothing on the GPU. PCIe topology and the IOMMU group are host concerns | **Yes**, by the IOMMU, plus a reset between tenants (scrubbing only documented for CC) | [kernel VFIO](https://docs.kernel.org/driver-api/vfio.html), [NVIDIA CC](https://developer.nvidia.com/blog/confidential-computing-on-h100-gpus-for-secure-and-trustworthy-ai/) |
| Whole GPU to one container (Docker or gVisor) | Host kernel and NVIDIA KMD | GPU memory isn't shared concurrently. The driver and kernel are shared; gVisor narrows the kernel surface but not the driver surface | [gVisor](https://gvisor.dev/docs/user_guide/gpu/), [GKE](https://docs.cloud.google.com/kubernetes-engine/docs/concepts/sandbox-pods) |
| MIG | One physical GPU split into slices with separate memory paths | **Hardware partition** with fault isolation, per NVIDIA. Ampere and later datacenter parts only | [MIG intro](https://docs.nvidia.com/datacenter/tesla/mig-user-guide/introduction.html) |
| vGPU (time-sliced or MIG-backed) | A mediated GPU behind NVIDIA's host vGPU manager | Depends on the backing. It needs NVIDIA vGPU software and a license server. AWS `g6f` sells 1/8 to 1/2 of an L4 as "Fractional vGPU VMs" with GRID drivers | [vGPU docs](https://docs.nvidia.com/vgpu/latest/grid-vgpu-user-guide/index.html), [AWS G6](https://aws.amazon.com/ec2/instance-types/g6/) |
| Time-slicing | Everything | **No**: "no memory or fault-isolation between replicas" | [GPU Operator](https://docs.nvidia.com/datacenter/cloud-native/gpu-operator/latest/gpu-sharing.html) |
| MPS | Everything; one MPS server per user | **No**. Separate address spaces, but "limited" error containment, and clients must share the server's UID; `-multiuser-server` is "dropping isolation between users" | [MPS](https://docs.nvidia.com/deploy/mps/when-to-use-mps.html) |

**[Inference]** For an untrusted multi-tenant agent sandbox, the defensible options are whole GPU per Sandbox (container or VM) and MIG slices per Sandbox. Time-slicing and MPS should only ever be used *within* one Tenant.

### 2.6 Who does what (vendor-stated)

| Vendor | GPU isolation | Source |
|---|---|---|
| **Modal** | GPU Sandboxes are gVisor-only. User containers run under `runsc`; Modal contributes to `nvproxy`. GPU memory snapshots use the CUDA checkpoint API inside the gVisor checkpoint. Up to 8 GPUs per container, "always attached to the same physical machine" | [sandboxes](https://modal.com/docs/guide/sandboxes.md), [truly serverless](https://modal.com/blog/truly-serverless-gpus), [gpu](https://modal.com/docs/guide/gpu.md) |
| **Daytona v2** | GPU Sandboxes run on "Cloud Hypervisor under KVM". "The GPUs of a running GPU sandbox are its own, and no other sandbox uses them at the same time". GPUs offered: H100, H200, B200, B300, RTX Pro 6000, RTX 4090, RTX 5090, L40S, and AMD MI355X. Checkpoints include GPU memory; no pause, fork or spot | [sandboxes](https://www.daytona.com/docs/sandboxes.md), [gpu-sandboxes](https://www.daytona.com/docs/gpu-sandboxes.md) |
| **Fly.io** | Firecracker normally, but Cloud Hypervisor for GPU Machines, "a very similar Rust codebase that supports PCI passthrough". GPU and non-GPU workloads ran on separate hardware. Two external security assessments (Atredis, Tetrel). Months of failed attempts to get NVIDIA's host drivers to map virtualized GPUs into Cloud Hypervisor ("we hex-edited the closed-source drivers to trick them into thinking our hypervisor was QEMU"). MIG and vGPU were unusable for them. Product scaled back (post of 2025-02-14). A user forum post says GPUs end on 2026-07-31 **(secondary)** | [Fly.io blog](https://fly.io/blog/wrong-about-gpu/), [community post](https://community.fly.io/t/gpu-migration-fly-io-gpus-will-be-deprecated-as-of-july-31-2026/27110) |
| **GKE Sandbox** | gVisor and nvproxy, with whole GPUs; time-sharing discouraged | [GKE](https://docs.cloud.google.com/kubernetes-engine/docs/concepts/sandbox-pods) |
| **Beam, Northflank** | gVisor with GPUs; Northflank uses it "when nested virtualization is unavailable" | [gVisor users](https://gvisor.dev/users/) |
| **RunPod** | The Pods page says "Runpod runs Docker for you" and distinguishes Secure Cloud (T3/T4 data centres) from Community Cloud (peer providers). It makes **no isolation statement** | [RunPod pods](https://docs.runpod.io/pods/overview) |
| **AWS (as a substrate)** | Sells fractional L4s (`g6f`) as vGPU VMs, so sharing below a whole GPU happens inside the hypervisor | [AWS G6](https://aws.amazon.com/ec2/instance-types/g6/) |

**[Inference]** The split follows the CPU story in [agent-sandbox-usage.md](agent-sandbox-usage.md): vendors who started on gVisor (Modal) kept it for GPUs, and vendors who started on microVMs (Daytona v2, Fly) had to switch VMMs to Cloud Hypervisor for GPUs, because Firecracker can't do passthrough.

### 2.7 Where a learner can experiment, and what it costs

All prices were read on 2026-11-21 and exclude tax.

| Option | What you get | Price | Good for | Source |
|---|---|---|---|---|
| GCP `g2-standard-4` | 1× L4, 4 vCPU, 16 GiB, us-central1 | **$0.706832/h** on demand; **$0.423016/h** spot | Docker and gVisor GPU (phase 4a). No passthrough (no vIOMMU documented) | [GCP accelerator pricing](https://cloud.google.com/products/compute/pricing/accelerator-optimized) |
| AWS `g4dn.xlarge` | 1× T4, 4 vCPU, 16 GiB, us-east-1 | **$0.526/h** | Phase 4a; T4 is officially supported by nvproxy | [AWS price JSON (us-east-1, published 2026-11-21)](https://b0.p.awsstatic.com/pricing/2.0/meteredUnitMaps/ec2/USD/current/ec2-ondemand-without-sec-sel/US%20East%20(N.%20Virginia)/Linux/index.json), [G4 page](https://aws.amazon.com/ec2/instance-types/g4/) |
| AWS `g6.xlarge` | 1× L4, 4 vCPU, 16 GiB | **$0.8048/h** | Phase 4a | same JSON, [G6 page](https://aws.amazon.com/ec2/instance-types/g6/) |
| AWS `g6f.large` | 1/8 L4 (3 GB) vGPU | **$0.202/h** | Seeing vGPU from the guest side only | same |
| AWS `g4dn.metal` | **bare metal**, 8× T4, 96 vCPU, 384 GiB | **$7.824/h** | VFIO and Cloud Hypervisor, multi-GPU scheduling | same JSON |
| AWS `g5g.metal` | **bare metal**, 2× T4G, Graviton2 (Arm), 64 vCPU, 128 GiB | **$2.744/h** | Cheapest AWS bare metal with a GPU; Arm adds friction (driver builds, Cloud Hypervisor aarch64) **[Inference]** | same JSON, [G5g page](https://aws.amazon.com/ec2/instance-types/g5g/) |
| Hetzner GEX45 | dedicated, RTX PRO 4000 Blackwell SFF 24 GB, i5-13500, 64 GB | **€214/month + €209 setup**, or **€0.3429/h** | Phase 4b at a flat cost; you own the kernel command line and the IOMMU | [Hetzner GPU matrix](https://www.hetzner.com/dedicated-rootserver/matrix-gpu/), [press release 2026-09-01](https://www.hetzner.com/pressroom/hetzner-expands-its-gpu-portfolio-with-the-gex45/) |
| Hetzner GEX63 / GEX131 | RTX PRO 6000 Blackwell Max-Q 96 GB | €999/month / from €1,499/month, plus the same amount again as setup | Larger models; the RTX PRO 6000 supports MIG (up to 4) | same, [MIG GPUs](https://docs.nvidia.com/datacenter/tesla/mig-user-guide/supported-gpus.html) |

- **GEX44.** It no longer appears on Hetzner's GPU price matrix (only GEX45, 63, 131 and 430). Hetzner's docs still list it among server lines ([Hetzner docs](https://docs.hetzner.com/robot/dedicated-server/server-lines/gpu-server/)).
- **[Inference]** I found no Hetzner page about IOMMU or VT-d. Forum reports **(secondary)** describe empty `/sys/kernel/iommu_groups` on some Hetzner boxes. Before committing to a month, check on the first day: `intel_iommu=on`, see whether the GPU's IOMMU group contains only the GPU and its audio function, and bind it to `vfio-pci`. A consumer-class board (i5-13500) may group devices coarsely.
- **[Inference]** gVisor nvproxy doesn't officially list Blackwell RTX PRO parts, although GKE Sandbox has RTX PRO 6000 in preview. Choose a T4 or L4 for phase 4a and keep Hetzner for 4b, where the guest runs a normal NVIDIA driver.
- **Quotas.**
  - **AWS:** the default for "Running On-Demand G and VT instances" and "All G and VT Spot Instance Requests" is **0** (measured in vCPUs); P instances are also 0. `g4dn.metal` needs a 96-vCPU quota and `g5g.metal` 64 ([AWS quotas](https://docs.aws.amazon.com/ec2/latest/instancetypes/ec2-instance-quotas.html)).
  - **GCP:** "New accounts and projects have a global GPU quota that applies to all regions". You must request per-model, per-region quota *and* the global `GPUs (all regions)` quota, and "If you're using a Free Trial Cloud Billing account, then you can't request a change to your quota" ([GCP quotas](https://docs.cloud.google.com/compute/resource-usage)). The page doesn't state the default number.

---

## 3. Claim verification

| # | Claim (made earlier from memory) | Verdict | Source |
|---|---|---|---|
| 1 | "Firecracker doesn't support GPUs; GPU passthrough needs Cloud Hypervisor or QEMU with VFIO." | **Holds, but out of date in one detail.** Firecracker has had optional PCI since v1.13 and active VFIO work (#5679, `feature/vfio` branch, NVMe-tested). Nothing has shipped in a release, and GPUs have neither been tested nor been given a date | [CHANGELOG](https://github.com/firecracker-microvm/firecracker/blob/main/CHANGELOG.md), [#5679](https://github.com/firecracker-microvm/firecracker/issues/5679), [#6055](https://github.com/firecracker-microvm/firecracker/pull/6055), [#1179](https://github.com/firecracker-microvm/firecracker/issues/1179), [CH VFIO](https://github.com/cloud-hypervisor/cloud-hypervisor/blob/main/docs/vfio.md), [QEMU](https://www.qemu.org/docs/master/devel/vfio-iommufd.html) |
| 2 | "Cloud GPU VMs already have the GPU passed through, so you can't pass it again into a nested VM; you need bare metal where you control the IOMMU." | **Holds in practice; the reason is more precise.** Nested assignment needs an IOMMU in the L1, and no-IOMMU mode can't assign to VMs. AWS doesn't offer nested virtualization on any GPU family. GCP documents no vIOMMU. It isn't impossible in principle (a vIOMMU would allow it) | [VFIO Kconfig](https://raw.githubusercontent.com/torvalds/linux/master/drivers/vfio/Kconfig), [AWS nested](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/amazon-ec2-nested-virtualization.html), [GCP nested](https://docs.cloud.google.com/compute/docs/instances/nested-virtualization/overview) |
| 3 | "gVisor supports NVIDIA GPUs via nvproxy, and Modal uses it." | **Holds.** Officially T4, A100, A10G, L4 and H100, with allowlisted drivers. Modal: GPU Sandboxes are gVisor-only, and Modal's 2026 post names its use of and contributions to `nvproxy` | [gVisor GPU](https://gvisor.dev/docs/user_guide/gpu/), [Modal sandboxes](https://modal.com/docs/guide/sandboxes.md), [Modal blog](https://modal.com/blog/truly-serverless-gpus) |
| 4 | "Before a GPU goes to a new tenant, its memory must be cleared and the GPU reset; this is a security problem." | **Holds as a requirement, but not documented as automatic.** `vfio-pci` resets (FLR or SBR) on open and release. NVIDIA documents a firmware memory scrub only for CC mode. Firecracker's maintainers named tenant switching as a blocker | [vfio_pci_core.c](https://raw.githubusercontent.com/torvalds/linux/master/drivers/vfio/pci/vfio_pci_core.c), [NVIDIA CC](https://developer.nvidia.com/blog/confidential-computing-on-h100-gpus-for-secure-and-trustworthy-ai/), [#849](https://github.com/firecracker-microvm/firecracker/issues/849) |
| 5a | GCP `g2-standard-4` ≈ $0.7/h | **Holds:** $0.706832/h on demand (us-central1); spot $0.423016/h | [GCP pricing](https://cloud.google.com/products/compute/pricing/accelerator-optimized) |
| 5b | AWS `g4dn.xlarge` ≈ $0.53/h | **Holds:** $0.526/h (us-east-1) | [AWS price JSON](https://b0.p.awsstatic.com/pricing/2.0/meteredUnitMaps/ec2/USD/current/ec2-ondemand-without-sec-sel/US%20East%20(N.%20Virginia)/Linux/index.json) |
| 5c | AWS `g4dn.metal` ≈ $8/h (8× T4) | **Holds:** $7.824/h, 8× T4, 96 vCPU. A cheaper bare-metal option exists: `g5g.metal` at $2.744/h (2× T4G, Arm) | same, [G4](https://aws.amazon.com/ec2/instance-types/g4/), [G5g](https://aws.amazon.com/ec2/instance-types/g5g/) |
| 5d | Hetzner GEX44 ≈ €180/month | **Doesn't hold any more.** The GEX44 isn't on the current price matrix. Its successor, the GEX45, is €214/month plus €209 setup (excl. VAT), or €0.3429/h | [Hetzner matrix](https://www.hetzner.com/dedicated-rootserver/matrix-gpu/), [press release](https://www.hetzner.com/pressroom/hetzner-expands-its-gpu-portfolio-with-the-gex45/) |
| 6 | "New GCP and AWS accounts have a GPU quota of 0 by default and must request an increase." | **AWS holds** (G/VT and P quotas default to 0 vCPUs). **GCP can't be confirmed numerically.** The docs say new projects have a global GPU quota but don't state its value, and Free Trial accounts can't request changes | [AWS quotas](https://docs.aws.amazon.com/ec2/latest/instancetypes/ec2-instance-quotas.html), [GCP quotas](https://docs.cloud.google.com/compute/resource-usage) |

---

## 4. Impact on our design (**everything in this section is [Inference]**)

Ranked by importance.

1. **Spec §9, phase 4: split it and change where it runs.**
   - **4a, container-level GPU on Docker and gVisor:**
     - Hardware: one GCP `g2-standard-4` (spot, about $0.42/h) or AWS `g4dn.xlarge` ($0.526/h). The GPU must be a T4 or L4 so nvproxy is on its supported list.
     - Scope: the `gpu` field becomes real for these two Runtimes, the Host reports GPU inventory, and the harness reruns with GPU workloads.
     - Cost: about 20 GPU-hours, under $15.
   - **4b, VFIO passthrough with a new Cloud Hypervisor Runtime:**
     - Hardware: a Hetzner GEX45 for a month (€423 for the first month including setup), or AWS `g5g.metal` / `g4dn.metal` by the hour after a quota request.
     - Firecracker stays GPU-less. The phase table's "GCP g2" applies only to 4a.
     - Add a fourth "Where it runs" note: no passthrough on cloud GPU VMs (see §2.2).
2. **The GPU passthrough design doc** (the spec's phase 4 deliverable) should contain at least:
   - (a) The trust model per mode, using the table in §2.5. State that container-level GPU shares the NVIDIA KMD across Tenants, and that gVisor does not cover driver bugs or toolkit hooks.
   - (b) Host preparation: IOMMU on, ACS and IOMMU-group audit, permanent `vfio-pci` binding, hugepages reserved for GPU Sandboxes.
   - (c) The tenant handoff protocol:
     - release the GPU, let `vfio-pci` reset it (record whether FLR or SBR was used), quarantine it;
     - **verify**, by scanning for a canary, that no data survives;
     - only then mark it free in the Control Plane.

     Note that NVIDIA documents scrubbing only for CC mode.
   - (d) A start-time budget per phase: scheduling, VFIO, memory pin, guest boot and BAR mapping, driver init, CUDA init, weights.
   - (e) Why not Firecracker, citing #849, #1179 and #5679, with a "revisit when VFIO ships in a release" trigger.
   - (f) Snapshot strategy: no VMM snapshot of a passthrough GPU. Instead use a GPU-less VM snapshot, then GPU hot-plug, then an in-guest `cuda-checkpoint` restore.
   - (g) Out of scope: MIG, vGPU, CC, multi-GPU P2P; each gets one paragraph on why.
3. **Spec §8, verification: add GPU isolation attacks and GPU start-time phases.**
   - **Attacks:**
     - read VRAM left over by the previous Sandbox on the same GPU;
     - list or inspect other Tenants' GPU processes with `nvidia-smi` or NVML;
     - use driver capabilities beyond `compute,utility` (`NVIDIA_DRIVER_CAPABILITIES=all`);
     - a toolkit-hook regression test, an image that sets `LD_PRELOAD` (the shape of CVE-2025-23266), run on both Docker and gVisor;
     - on 4b, DMA outside the guest (blocked by the IOMMU).
   - **Performance:** split "Runtime start" into VFIO and memory pin, guest boot, `nvidia` driver ready, first CUDA context, and weights loaded. Measure with and without persistence mode, hot-plug versus boot-time attach, and 4K versus 1 GiB hugepages.
4. **GLOSSARY, Quota and Usage (spec §6 and GLOSSARY "Quota" / "Usage").**
   - Add **GPUs** as a seventh Quota dimension (total GPUs, optionally per model).
   - Add GPU-seconds to Usage in Running, **and in Frozen**, because a frozen Sandbox still holds its GPU and the GPU can't go to anyone else.
   - Hibernated releases the GPU only if CUDA state was checkpointed. Otherwise a GPU Sandbox's hibernate should return "unsupported", as Daytona v2 does for pause.
5. **ADR candidate, "GPU Sandboxes use whole GPUs only; no time-slicing or MPS across Tenants".** It follows from §2.5 and GKE's own warning. MIG can be allowed later as a second option, but nvproxy and the CRIU CUDA plugin don't support MIG today.
6. **ADR candidate, "Cloud Hypervisor is the fourth Runtime, and only for GPUs".** It keeps ADR 0002, because Cloud Hypervisor has vsock and the same Guest Agent applies. ADR 0003 has to cover guest NVIDIA driver injection: the driver must match the guest kernel, so the platform, not the Template, should own the guest kernel and driver pair for this Runtime. It also changes ADR 0001's capacity report: a Host reports GPUs by model, UUID and IOMMU group, plus a health state.
7. **Spec §6, scheduling: memory overcommit is already off, which matches VFIO.** VFIO pins all guest memory, so a GPU Host must also reserve hugepages and refuse balloon or virtio-mem for GPU Sandboxes. First-fit should place a GPU request only on Hosts with a free, *verified-clean* GPU.
8. **Spec §7, security model: add a driver-patching rule and a CDI rule.**
   - NVIDIA drivers and the Container Toolkit get a patching SLA, as the gVisor docs demand.
   - Use CDI mode: CVE-2024-0132 didn't affect CDI.
   - Pin the toolkit at or above the version that fixed CVE-2025-23266.
9. **Spec §2, non-goals: keep "GPU in the first version" as a non-goal.** But say *why* in one line (Firecracker can't, and container-level GPU isn't hard isolation), so that phase 4 reads as a deliberate design and not as an afterthought.

---

## 5. Sources (all accessed 2026-11-21)

**NVIDIA**
- Container Toolkit: architecture https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/arch-overview.html ; CDI https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/cdi-support.html ; install guide https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html
- GPU Operator sharing https://docs.nvidia.com/datacenter/cloud-native/gpu-operator/latest/gpu-sharing.html
- MIG https://docs.nvidia.com/datacenter/tesla/mig-user-guide/introduction.html ; supported GPUs https://docs.nvidia.com/datacenter/tesla/mig-user-guide/supported-gpus.html
- MPS https://docs.nvidia.com/deploy/mps/when-to-use-mps.html ; vGPU user guide https://docs.nvidia.com/vgpu/latest/grid-vgpu-user-guide/index.html
- nvidia-smi https://docs.nvidia.com/deploy/nvidia-smi/index.html ; driver persistence https://docs.nvidia.com/deploy/driver-persistence/background.html , https://docs.nvidia.com/deploy/driver-persistence/persistence-daemon.html
- CUDA lazy loading https://docs.nvidia.com/cuda/cuda-programming-guide/04-special-topics/lazy-loading.html
- cuda-checkpoint https://github.com/NVIDIA/cuda-checkpoint ; blog https://developer.nvidia.com/blog/checkpointing-cuda-applications-with-criu/
- Confidential Computing blog https://developer.nvidia.com/blog/confidential-computing-on-h100-gpus-for-secure-and-trustworthy-ai/ ; R595 release notes https://docs.nvidia.com/confidential-computing/release-notes/r595-trd1.md

**Kernel and VMMs**
- VFIO https://docs.kernel.org/driver-api/vfio.html ; Kconfig https://raw.githubusercontent.com/torvalds/linux/master/drivers/vfio/Kconfig ; vfio_pci_core.c https://raw.githubusercontent.com/torvalds/linux/master/drivers/vfio/pci/vfio_pci_core.c
- Cloud Hypervisor VFIO https://github.com/cloud-hypervisor/cloud-hypervisor/blob/main/docs/vfio.md ; hotplug https://github.com/cloud-hypervisor/cloud-hypervisor/blob/main/docs/hotplug.md ; snapshot/restore https://github.com/cloud-hypervisor/cloud-hypervisor/blob/main/docs/snapshot_restore.md
- QEMU iommufd https://www.qemu.org/docs/master/devel/vfio-iommufd.html
- Firecracker CHANGELOG https://github.com/firecracker-microvm/firecracker/blob/main/CHANGELOG.md ; releases https://github.com/firecracker-microvm/firecracker/releases ; issues #849 https://github.com/firecracker-microvm/firecracker/issues/849 , #1179 https://github.com/firecracker-microvm/firecracker/issues/1179 , #5679 https://github.com/firecracker-microvm/firecracker/issues/5679 ; PRs #5870 https://github.com/firecracker-microvm/firecracker/pull/5870 , #6055 https://github.com/firecracker-microvm/firecracker/pull/6055
- CRIU CUDA plugin https://github.com/checkpoint-restore/criu/tree/criu-dev/plugins/cuda
- VFIO start-time measurements: Canonical engineer's blog https://cpaelzer.github.io/blogs/007-vfio-slow-startup ; LKML thread https://lkml.iu.edu/hypermail/linux/kernel/2411.3/03532.html

**gVisor and GKE**
- GPU guide https://gvisor.dev/docs/user_guide/gpu/ ; users https://gvisor.dev/users/ ; GKE Sandbox https://docs.cloud.google.com/kubernetes-engine/docs/concepts/sandbox-pods

**Security**
- NVD API: CVE-2024-0132 https://services.nvd.nist.gov/rest/json/cves/2.0?cveId=CVE-2024-0132 ; CVE-2025-23266 https://services.nvd.nist.gov/rest/json/cves/2.0?cveId=CVE-2025-23266 ; CVE-2023-4969 https://services.nvd.nist.gov/rest/json/cves/2.0?cveId=CVE-2023-4969
- Wiz NVIDIAScape https://www.wiz.io/blog/nvidia-ai-vulnerability-cve-2025-23266-nvidiascape ; Trail of Bits LeftoverLocals https://blog.trailofbits.com/2024/01/16/leftoverlocals-listening-to-llm-responses-through-leaked-gpu-local-memory/

**Vendors**
- Modal: sandboxes https://modal.com/docs/guide/sandboxes.md ; GPU https://modal.com/docs/guide/gpu.md ; security https://modal.com/docs/guide/security.md ; GPU memory snapshots https://modal.com/blog/gpu-mem-snapshots ; truly serverless GPUs https://modal.com/blog/truly-serverless-gpus
- Daytona v2: https://www.daytona.com/docs/sandboxes.md ; https://www.daytona.com/docs/gpu-sandboxes.md
- Fly.io: https://fly.io/blog/wrong-about-gpu/ ; community post (secondary) https://community.fly.io/t/gpu-migration-fly-io-gpus-will-be-deprecated-as-of-july-31-2026/27110
- RunPod: https://docs.runpod.io/pods/overview

**Clouds, prices and quotas**
- GCP accelerator-optimized pricing https://cloud.google.com/products/compute/pricing/accelerator-optimized ; GCP quotas https://docs.cloud.google.com/compute/resource-usage ; GCP nested virtualization https://docs.cloud.google.com/compute/docs/instances/nested-virtualization/overview
- AWS on-demand price JSON (us-east-1, Linux; manifest publication date 2026-11-21) https://b0.p.awsstatic.com/pricing/2.0/meteredUnitMaps/ec2/USD/current/ec2-ondemand-without-sec-sel/US%20East%20(N.%20Virginia)/Linux/index.json ; instance pages https://aws.amazon.com/ec2/instance-types/g4/ , https://aws.amazon.com/ec2/instance-types/g5g/ , https://aws.amazon.com/ec2/instance-types/g6/ ; quotas https://docs.aws.amazon.com/ec2/latest/instancetypes/ec2-instance-quotas.html ; nested virtualization https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/amazon-ec2-nested-virtualization.html
- Hetzner GPU matrix https://www.hetzner.com/dedicated-rootserver/matrix-gpu/ ; GEX45 press release https://www.hetzner.com/pressroom/hetzner-expands-its-gpu-portfolio-with-the-gex45/ ; server lines https://docs.hetzner.com/robot/dedicated-server/server-lines/gpu-server/

**Staleness and uncertainty notes**
- Firecracker's VFIO work is moving fast (`feature/vfio`, #5679). Re-check before phase 4b.
- The fixed versions for CVE-2025-23266 come from news coverage, and the ACM Queue PF-FLR quote comes from a search snippet; neither was read in its primary form.
- The Fly.io GPU end date comes only from a user forum post.
- The default GCP GPU quota value isn't stated in GCP's docs.
- RunPod publishes no isolation details.
