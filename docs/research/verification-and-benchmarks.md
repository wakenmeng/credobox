# Verification: benchmarks, eval-harness integration and isolation testing

> English | [中文](verification-and-benchmarks.zh.md)

> Research date: 2026-11-21. Every link was accessed on that day unless noted otherwise.
> Primary sources only: papers, official docs, harness source code, CVE advisories and vendor security docs. Vendor blogs count as primary only for the vendor's own claims about its own product. Where I measured something myself (image sizes, task counts), I say so and give the method.
> Anything marked **[Inference]** is my own conclusion, not what the source says. Terms follow the [GLOSSARY](../../GLOSSARY.md). This note does not repeat [agent-sandbox-usage.md](agent-sandbox-usage.md), which already covers vendors' advertised cold-start numbers and customer stories.

---

## 1. Summary

- **No vendor-neutral, peer-reviewed benchmark compares sandbox providers today.** The only public, reproducible cross-provider benchmark is ComputeSDK's. It measures one thing, "TTI" (client-side `create()` through the first successful `node -v`, destroy excluded), in burst mode only, and ranks providers with a weighted median/p95/p99 score multiplied by the success rate ([tti.bench.ts](https://github.com/computesdk/benchmarks/blob/master/benchmarks/sandbox/tti.bench.ts), [scoring.ts](https://github.com/computesdk/benchmarks/blob/master/benchmarks/sandbox/scoring.ts)). The academic runtime comparisons (Firecracker NSDI'20, gVisor HotCloud'19, VEE'20, CLOSER'21) are 5–7 years old and used software versions that have since changed a lot.
- **"Start time" means a different interval to everyone.** Firecracker defines it as InstanceStart → guest `/sbin/init`, with a minimal kernel and no serial console ([SPECIFICATION.md](https://github.com/firecracker-microvm/firecracker/blob/main/SPECIFICATION.md)). E2B's "~80ms" is loading a snapshot with processes already running ([how-it-works](https://docs.e2b.dev/template/how-it-works.md)). Daytona doesn't state its conditions. Modal says Created → Scheduled → Started → Ready are separate events, and that benchmarks measure only the floor ([Modal blog, 2026-06-22](https://modal.com/blog/unpacking-sandbox-startup-latency)). Our harness needs its own written definitions, and §9 proposes a set.
- **Terminal-Bench 2.0 on Harbor is the best compatibility test for us, better than SWE-bench.** Harbor has a documented plug-in point for custom sandboxes: implement `BaseEnvironment` (start/stop/exec/upload/download) and pass `-e module:Class` ([custom-sandboxes](https://github.com/harbor-framework/harbor/blob/main/docs-mintlify/sandboxes/custom-sandboxes.mdx)). All 89 TB2 tasks are single-container, ship a prebuilt `docker_image`, and have an oracle solution, so an "oracle sweep" checks environment parity without calling an LLM (my count of the [terminal-bench-2](https://github.com/harbor-framework/terminal-bench-2) repo).
- **The SWE-bench harness has no pluggable backend.** It drives Docker through docker-py, and its only alternative is a built-in Modal path ([run_evaluation.py](https://github.com/SWE-bench/SWE-bench/blob/main/swebench/harness/run_evaluation.py)). It runs containers as `root` in `/testbed` with `cap_add=["SYS_ADMIN"]`, which conflicts with our hardened Docker defaults. The images are x86_64-first, with arm64 "experimental" ([README](https://github.com/SWE-bench/SWE-bench/blob/main/README.md)). A sample instance image is about 1.1–3 GB compressed (my measurement). **[Inference]** For us, SWE-bench is a good secondary check, run as gold-patch plus empty-patch sweeps on a fixed 50-instance subset.
- **Escape-probing tools are uneven in 2026.** `amicontained` is effectively unmaintained: last release 2019, last push 2020. `deepce` is maintained, but some of its exploit modes "overwrite runC which can be destructive" ([README](https://github.com/stealthcopter/deepce)). CDK is the most actively maintained (v1.5.6, 2026-02) and has an `evaluate` mode plus PoCs for CVE-2019-5736 and CVE-2022-0492 ([CDK](https://github.com/cdk-team/CDK)).
- **The runc CVEs make good teaching tests, and our design already removes one attack path.** In CVE-2024-21626, "attack 2" needs `runc exec --cwd` into an attacker-controlled path ([GHSA-xr7r-f8xq-vfvv](https://github.com/opencontainers/runc/security/advisories/GHSA-xr7r-f8xq-vfvv)). **[Inference]** Our Exec goes through the Guest Agent, not `runc exec`, so that path doesn't exist for us. The malicious-image variant ("attack 1") does still apply, because Tenants register private Templates by image reference. gVisor and Firecracker don't run runc inside the trust boundary at all.
- **Memory overhead depends on what you count.** RSS double-counts shared pages and PSS splits them ([kernel proc docs](https://docs.kernel.org/filesystems/proc.html)). Under gVisor, application memory shows up as shmem rather than anon ([gVisor resource model](https://gvisor.dev/docs/architecture_guide/resources/)). Firecracker recommends disabling KSM for security, and KSM is exactly the mechanism that would raise density ([prod-host-setup](https://github.com/firecracker-microvm/firecracker/blob/main/docs/prod-host-setup.md)). gVisor's own density benchmark avoids these accounting differences: it measures host available memory before and after starting N sandboxes and divides by N ([gVisor performance guide](https://gvisor.dev/docs/architecture_guide/performance/)).
- **Agent-level sandbox evaluation exists only in pieces.** Harbor's adapters use parity experiments: the same agent and model on two environments, mean ± SEM over repeated runs ([SWE-bench adapter README](https://github.com/harbor-framework/harbor/blob/main/adapters/swebench/README.md)). Terminal-Bench reports success with 95% CIs, along with timeouts and execution time ([arXiv:2601.11868](https://arxiv.org/abs/2601.11868)). Research systems treat sandbox provisioning as latency on the critical path ([SpecBox](https://arxiv.org/abs/2607.23933)). I found no published study comparing container vs gVisor vs microVM on agent task success.

---

## 2. Existing sandbox and runtime benchmarks (Q1)

### 2.1 ComputeSDK benchmarks: the only cross-provider one

- **What it is.** An MIT-licensed repo, last pushed 2026-11-21, that benchmarks sandbox, browser, storage and AI-gateway providers. Results are published as leaderboards at computesdk.com ([repo](https://github.com/computesdk/benchmarks)).
- **What "TTI" measures, from the source:** "TTI = sandbox create through the first command (`node -v`) succeeding, excluding destroy". The timer runs in the benchmark process: `createStart = performance.now()` before `compute.sandbox.create()`, and `ttiMs = createMs + (performance.now() - commandStart)` after `runCommand('node -v')` returns exit 0 ([tti.bench.ts](https://github.com/computesdk/benchmarks/blob/master/benchmarks/sandbox/tti.bench.ts)). Create has a 120 s timeout and the command a 30 s timeout.
- **Load shape.** Burst only: "--concurrency N opens all slots at once". The file says "Sequential and staggered shapes were retired (2026-08-11)". The scheduled workflow runs weekly, Friday 13:00 UTC, and its dispatch defaults are 100 iterations at concurrency 100 ([workflow](https://github.com/computesdk/benchmarks/blob/master/.github/workflows/sandbox-tti-benchmarks.yml)).
- **Scoring.** Each of median, p95 and p99 is scored as `100 × (1 − value/10 000 ms)`, weighted 0.60/0.25/0.15, and the result is multiplied by the success rate ([scoring.ts](https://github.com/computesdk/benchmarks/blob/master/benchmarks/sandbox/scoring.ts)).
- **Other suites in the same repo.** A "DAX" workload builds OpenCode inside a fresh sandbox and times prepare/download/clone/install/typecheck ([dax.bench.ts](https://github.com/computesdk/benchmarks/blob/master/benchmarks/sandbox/dax.bench.ts)). A reliability suite installs and runs the OpenCode agent CLI inside a sandbox ([sandbox-opencode.bench.ts](https://github.com/computesdk/benchmarks/blob/master/benchmarks/reliability/sandbox-opencode.bench.ts)). There are also snapshot, pause-resume and CRIU workflows under `.computesdk/workflows/stealth-*`.
- **Caveats.**
  - **[Inference]** ComputeSDK is a company selling a multi-provider SDK, and the repo includes its own Firecracker host provider (`ec2-firecrab`). The benchmark is open and reproducible, but it isn't independent.
  - **[Inference]** TTI is measured from a CI runner, so it includes the network RTT and region distance between runner and provider. It says nothing about kernel boot vs snapshot restore.
  - **[Inference]** With 100 samples, p99 is essentially the largest or second-largest value.

### 2.2 Academic runtime comparisons

| Study | Runtimes and versions | What it measures | Method notes it states | Status |
|---|---|---|---|---|
| Agache et al., "Firecracker", NSDI'20 ([paper](https://www.usenix.org/system/files/nsdi20-paper-agache.pdf), [data](https://github.com/firecracker-microvm/nsdi2020-data)) | Firecracker, Cloud Hypervisor, QEMU; guest kernel 4.14.94; host 4.15 | Boot time; memory overhead; IO | Boot = "time between when VMM process is forked and the guest kernel forks its init process", with a minimal init that writes to an IO port and the VMMs patched to `exit()` on that write. 500 serial samples; 1000 VMs at 50 concurrent. "End-to-end" (including API configuration) vs "pre-configured" are reported separately. Memory overhead = non-shared segments from `pmap`, excluding the VMM binary. | Old kernel and VMM versions; method still sound |
| Young et al., "The True Cost of Containing: A gVisor Case Study", HotCloud'19 ([paper](https://www.usenix.org/system/files/hotcloud19-paper-young.pdf)) | runc vs runsc (ptrace, KVM); Ubuntu 16.04, kernel 4.4, Docker 18.09 | Container lifecycle, syscalls, malloc, network, file open, Python import | Lifecycle is measured "long enough to achieve steady state since prior work shows container destruction has significant asynchronous overheads". Serial setup+teardown: runc 1.014 s, runsc-KVM 1.117 s. | **Outdated.** gVisor now recommends the Systrap platform ([performance guide](https://gvisor.dev/docs/architecture_guide/performance/)) and has directfs ([security model](https://gvisor.dev/docs/architecture_guide/security/)). |
| Anjali, Caraza-Harter, Swift, "Blending Containers and Virtual Machines: A Study of Firecracker and gVisor", VEE'20 ([paper](https://pages.cs.wisc.edu/~swift/papers/vee20-isolation.pdf)) | LXC, gVisor 20200127 (KVM mode), Firecracker v0.19.1; host 5.4 | Host-kernel code coverage plus CPU, network, memory and IO performance | Uses gVisor KVM mode and notes "the official performance guide of gVisor uses ptrace mode … so many of our performance results are different". IO tests flush caches inside the guest, on the host, or both; "a warm cache within the microVM results in nearly double the throughput as a warm cache in the host". | **Outdated** versions; the coverage method is still a useful idea |
| Li et al., "Comparative Performance Study of Lightweight Hypervisors Used in Container Environment", CLOSER'21 ([paper](https://www.scitepress.org/Papers/2021/104405/104405.pdf)) | Docker, gVisor (ptrace, KVM), Kata 1.11 (QEMU, Firecracker v0.21.1), QEMU/KVM | Startup, memory footprint, syscalls, network, IO, nginx, MySQL | Startup: `time` from launch "to the stage of the network stack being successfully initialized", 100 rounds. Memory = average private dirty pages over 100 containers, and it notes the shared `dockerd` (~40 MB) is not counted. Hyper-threading on. | **Outdated** (2020 software) |

### 2.3 Project-maintained suites (useful templates for our harness)

- **Firecracker CI performance tests.**
  - `test_boottime.py` reads a "Guest-boot-time" value written by a boot-timer device that the guest pokes from `init`. It records `time_to_ssh_up`, `build_time`, `resume_time` and `systemd-analyze` kernel/userspace splits, with 10 iterations per (vCPU, memory, huge pages, block/pmem) configuration ([test_boottime.py](https://github.com/firecracker-microvm/firecracker/blob/main/tests/integration_tests/performance/test_boottime.py)).
  - `test_snapshot.py` measures **restore latency** (30 iterations, memory 128 MiB to 12 GiB) separately from **post-restore latency**, i.e. the cost of memory pages faulted in after resume, with and without a UFFD handler ([test_snapshot.py](https://github.com/firecracker-microvm/firecracker/blob/main/tests/integration_tests/performance/test_snapshot.py)).
  - `test_memory_overhead.py` sums `psutil` memory maps of the Firecracker process minus the guest-memory regions, and reports both `size` and `rss`, plus `uss` ([test_memory_overhead.py](https://github.com/firecracker-microvm/firecracker/blob/main/tests/integration_tests/performance/test_memory_overhead.py)).
- **gVisor `perf.py`.**
  - `startup` times Docker container start until a request reaches the bound port.
  - `density` takes host available memory before and after launching many containers and divides by the count, "because some runtimes don't use individual container cgroups".
  - The guide defaults to ptrace, which it calls the platform with "the highest structural costs by far", and the hosts were Debian 9 on GCE n1-standard-4 ([performance guide](https://gvisor.dev/docs/architecture_guide/performance/)). **Outdated** host OS; no date or version is given.

### 2.4 Methodology pitfalls the sources call out

| Pitfall | Source | What to do (**[Inference]** unless noted) |
|---|---|---|
| End-to-end vs pre-configured boot differ by tens of ms | Firecracker NSDI'20 Fig. 5 | Report both: API-driven create vs Runtime-internal boot |
| Kernel config dominates boot: Ubuntu's stock kernel "takes an additional 900ms"; serial console logging costs up to 70 ms; a network interface adds ~20 ms | Firecracker NSDI'20 §5.1 | Record the guest kernel config and boot args |
| Host kernel 6.1 regressed `KVM_CREATE_VM` latency, depending on cgroup version and CPU | [prod-host-setup](https://github.com/firecracker-microvm/firecracker/blob/main/docs/prod-host-setup.md) | Record the host kernel, cgroup version and mitigations |
| Snapshot restore returns before memory is faulted in | Firecracker `test_snapshot.py` | Measure restore-to-first-Exec, not only the restore call |
| Destruction is asynchronous, so serial create/destroy loops interfere | HotCloud'19 §3.1 | Measure in steady state and time destroy separately |
| Cache location (guest vs host page cache) changes IO results ~2× | VEE'20 §7 | Declare the cache state; drop caches for "cold" runs |
| gVisor platform choice changes results a lot | VEE'20 §3; gVisor guide | Record the platform (systrap/KVM/ptrace) and directfs |
| Image size drives startup for containers and Kata | CLOSER'21 §3.1.1 | Separate the image-pull/unpack phase from Runtime start |
| Shared daemons (dockerd ~40 MB) are excluded from per-container memory | CLOSER'21 §3.1.2 | Report per-sandbox memory and fixed per-Host overhead separately |
| Burst vs sequential vs staggered load give different distributions | ComputeSDK retired two shapes; Firecracker reports serial vs concurrent | Run serial and burst; state which one a number comes from |
| Closed-loop load generators hide queueing delay ("coordinated omission") | [wrk2 README](https://github.com/giltene/wrk2) | For throughput, use an open-loop arrival schedule and measure from the intended send time |
| Infrastructure failures counted as task failures | [SWE-bench infra_failure.py](https://github.com/SWE-bench/SWE-bench/blob/main/swebench/harness/infra_failure.py) | Classify failures (environment / ambiguous / task) before scoring |

---

## 3. How vendors define "start time" (Q2)

| Who | Number | Interval and conditions, as stated | Source |
|---|---|---|---|
| Firecracker | ≤125 ms | "from receiving the Firecracker InstanceStart API call to the start of the Linux guest user-space `/sbin/init` process", 1 vCPU / 128 MiB, serial console disabled, "minimal kernel and root file system", on m5d.metal (SMT off) or m6g.metal | [SPECIFICATION.md](https://github.com/firecracker-microvm/firecracker/blob/main/SPECIFICATION.md) |
| Firecracker | 8 CPU ms (6–60 ms wall) | VMM process start "up to API socket availability" | same |
| Firecracker CI | (metrics) | `guest_boot_time` from the boot-timer device; `time_to_ssh_up`; snapshot `latency` from the `load_snapshot` metric; post-restore page-fault latency measured separately | [test_boottime.py](https://github.com/firecracker-microvm/firecracker/blob/main/tests/integration_tests/performance/test_boottime.py), [test_snapshot.py](https://github.com/firecracker-microvm/firecracker/blob/main/tests/integration_tests/performance/test_snapshot.py) |
| E2B | ~80 ms | "load the sandbox in ~80ms any time later … with all the processes already running". This is a template snapshot taken after the start command and ready check ran. The page doesn't say where the timer starts or stops. | [how-it-works](https://docs.e2b.dev/template/how-it-works.md) |
| Daytona | <90 ms; 71 ms "Sandbox Creation" | "from code to execution". The 2025-04-28 launch post's table (Creation 71 / Execution 67 / Cleanup 59 / Total 197 ms) says only "measured under typical agentic workloads". v2 docs: "created in under a second, because their runner resumes a pre-built memory snapshot". | [Daytona Cloud post](https://www.daytona.io/dotfiles/introducing-daytona-cloud-the-agent-native-infrastructure), [v2 architecture](https://www.daytona.com/docs/architecture.md) |
| Modal | "under one second" | Separates Created (no resources yet), Scheduled (assigned to a worker), Started ("container is live, the entrypoint process is running, and network tunnels and Volume mounts are active") and Ready (application init done). `Sandbox.create` is async. Recommends readiness probes and warm pools. | [Modal blog, 2026-06-22](https://modal.com/blog/unpacking-sandbox-startup-latency), [products/sandboxes](https://modal.com/products/sandboxes) |
| ComputeSDK | (measured) | Client-side `create()` → first successful `node -v`, destroy excluded | [tti.bench.ts](https://github.com/computesdk/benchmarks/blob/master/benchmarks/sandbox/tti.bench.ts) |

**[Inference]** None of the vendor numbers is comparable with another, or with Firecracker's spec. E2B's and Daytona v2's numbers are snapshot restores with processes already running. Firecracker's is a cold kernel boot with a tuned kernel, and it excludes VMM configuration. Modal's "Ready" contains application work that we don't control. Our proposed definitions are in §9.1.

---

## 4. SWE-bench integration (Q3)

### 4.1 How the harness runs an evaluation

- **Images.** Three layers: a base image, about 60 environment images, and one instance image per task. Cache levels trade disk for speed; `instance` caching needs about 2,000 GB, and the default `env` level needs about 100 GB. The docs recommend at least 120 GB free disk, 16 GB RAM and 8 CPUs ([docker_setup.md](https://github.com/SWE-bench/SWE-bench/blob/main/docs/guides/docker_setup.md)).
- **Container setup.** The harness creates one container per instance from `test_spec.image`. It runs as `user=CONTAINER_USER`, which is `root`, in workdir `/testbed`, with `command="tail -f /dev/null"` and `cap_add=["SYS_ADMIN"]`, because "Docker's default seccomp profile only permits CLONE_NEWUSER with CAP_SYS_ADMIN, which browser sandboxes need" ([run_evaluation.py](https://github.com/SWE-bench/SWE-bench/blob/main/swebench/harness/run_evaluation.py), [image_builder/constants](https://github.com/SWE-bench/SWE-bench/blob/main/swebench/image_builder/constants/__init__.py)).
- **Evaluation steps.** The harness copies the patch in and tries `git apply --verbose`, then `--3way`, then `--reject`, then `patch --fuzz=5`. It then runs `eval.sh` and grades by parsing the test log against the instance's `FAIL_TO_PASS` and `PASS_TO_PASS` lists. `skip_patch` is a built-in "negative test mode" ([run_evaluation.py](https://github.com/SWE-bench/SWE-bench/blob/main/swebench/harness/run_evaluation.py)).
- **Result caching and v5 CLI changes.** Results are cached by `run_id` + `instance_id` regardless of the patch content. The v5 CLI builds images from a separate `swe-bench-tasks` repo, and `--task-repo` is the documented path on ARM Macs ([README](https://github.com/SWE-bench/SWE-bench/blob/main/README.md)).

### 4.2 Architectures and image sizes

- **Architecture support.** The README recommends `x86_64` and says "Support for `arm64` machines is experimental" ([README](https://github.com/SWE-bench/SWE-bench/blob/main/README.md)). The image key is `sweb.eval.{x86_64|arm64}.{instance_id}` ([image_spec.py](https://github.com/SWE-bench/SWE-bench/blob/main/swebench/image_builder/image_spec.py)). Epoch AI's optimized registry built 2,290 of 2,294 x86_64 images. It provides 1,819 arm64 images "on a best-effort basis, and have not been tested" ([Epoch, 2025-07-10](https://epoch.ai/blog/swebench-docker)).
- **Sizes.** Epoch reports all 2,290 images at 684 GiB before optimization and 67 GiB after, and Verified (500) at 189 GiB → 30 GiB ([Epoch](https://epoch.ai/blog/swebench-docker)).
- **My measurement** (Docker Hub registry API, 2026-11-21). The official `swebench/sweb.eval.x86_64.*` instance images I sampled are amd64-only OCI indexes of 1.09–3.07 GB compressed:
  - django-11099: 1,152 MB
  - sympy-20590: 1,089 MB
  - astropy-12907: 1,134 MB
  - scikit-learn-13142: 1,481 MB
  - matplotlib-23314: 3,072 MB
  - sphinx-8595: 1,098 MB

  The `swebench` Docker Hub namespace lists 4,503 repositories, of which 281 match "arm64".

### 4.3 Can it target a custom sandbox backend?

- **The SWE-bench harness itself: no generic interface.** It calls docker-py directly. Its one alternative is `--modal true`, which "builds its own images remotely" and can't be combined with `--task-repo` ([run_evaluation.py](https://github.com/SWE-bench/SWE-bench/blob/main/swebench/harness/run_evaluation.py), [evaluation guide](https://github.com/SWE-bench/SWE-bench/blob/main/docs/guides/evaluation.md)).
- **SWE-ReX (agent side).**
  - `AbstractDeployment` (`start`, `stop`, `is_alive`, `runtime`) and `AbstractRuntime` (`create_session`, `run_in_session`, `execute`, `read_file`, `write_file`, `upload`) are the extension points. Docker, Modal, Fargate, Daytona and "remote" deployments exist ([SWE-ReX source](https://github.com/SWE-agent/SWE-ReX/tree/main/src/swerex)).
  - Remote deployments run a `swerex-remote` server inside the sandbox and talk to it over HTTP ([README](https://github.com/SWE-agent/SWE-ReX)). **[Inference]** For us, that means an Exposed Port, or an `AbstractRuntime` implemented directly on our Exec API.
- **mini-swe-agent (agent side).** An environment is a small class with `execute(action, cwd, timeout) -> dict`, implemented for local, Docker, Singularity, bubblewrap and SWE-ReX Docker/Modal ([environments](https://github.com/SWE-agent/mini-swe-agent/tree/main/src/minisweagent/environments)). **[Inference]** This maps one-to-one onto our synchronous Exec and is the cheapest agent integration.
- **Harbor SWE-bench adapter (both sides).**
  - It converts SWE-bench Verified into Harbor tasks. The task Dockerfile is `FROM {docker_image}` plus a `curl … uv … install` step, and `tests/test.sh` runs a `uv`-managed parser that imports `swebench==4.0.3` grading functions ([adapter](https://github.com/harbor-framework/harbor/tree/main/adapters/swebench), [test.sh template](https://github.com/harbor-framework/harbor/blob/main/adapters/swebench/src/swebench_adapter/task-template/tests/test.sh)).
  - **[Inference]** So it needs an image build and verifier-time network access to PyPI.

### 4.4 Smallest credible subset

- **Official subsets.** Lite has 300 test + 23 dev instances, per Hugging Face ([dataset](https://huggingface.co/datasets/SWE-bench/SWE-bench_Lite)). The repo docs disagree with themselves on this: `datasets.md` says 534 and `faq.md` says 300 ([datasets.md](https://github.com/SWE-bench/SWE-bench/blob/main/docs/guides/datasets.md), [faq.md](https://github.com/SWE-bench/SWE-bench/blob/main/docs/faq.md)). Verified has 500 ([dataset](https://huggingface.co/datasets/SWE-bench/SWE-bench_Verified)).
- **SWE-bench Verified Mini.** 50 instances, with "approximately the same distribution of performance, test pass rates and difficulty as the original dataset". It "requires 5GB instead of 130GB of storage" ([HF card](https://huggingface.co/datasets/MariusHobbhahn/swe-bench-verified-mini)). This is a community subset, so its selection claims are the author's own.
- **[Inference]** For a *sandbox* compatibility test we aren't measuring a model, so we don't need statistical power over model scores. We need per-instance agreement between Runtimes. Verified Mini (50) is the smallest subset that is fixed, published and Verified-based. Lite is less useful now, because Epoch's and Harbor's tooling target Verified.

### 4.5 How to check "matches the official Docker environment"

- **Harbor's parity method.**
  - Harbor validates adapters by running the same agent and model on both harnesses and comparing resolved rates. For the SWE-bench adapter: 3 reruns, mean ± SEM, 0.545 ± 0.007 vs 0.563 official, on 499 tasks ([adapter README](https://github.com/harbor-framework/harbor/blob/main/adapters/swebench/README.md)).
  - The same README lists tasks that fail "even with the Oracle agent", for example `astropy__astropy-8872` and `django__django-10097`.
  - It also reports an environment-induced failure: `scikit-learn__scikit-learn-14710` "Passes locally; times out on Daytona" because the verifier "is effectively single-core and CPU-bound".
- **[Inference] Proposed check for us:**
  1. Run the **gold patch** and the **empty patch** (the harness's `skip_patch` mode) for each instance on the official harness with plain Docker. This gives the baseline sets R₀ (resolved with gold) and N₀ (must fail with empty).
  2. Run the same two sweeps through our public API on each Runtime.
  3. Report the per-instance confusion matrix against the baseline, not just a percentage.
  4. Rerun every disagreeing instance 3 times and classify it as flaky (it also flips on Docker), runtime-induced, or resource-induced (timeouts, per Harbor's scikit-learn case).
  5. Report both resource settings: "same vCPU/memory as the official run" and our default spec.

---

## 5. Terminal-Bench and Harbor (Q4)

### 5.1 Task packaging

- **Task structure.** A Harbor task is a directory containing `instruction.md`, `task.toml`, `environment/` (a Dockerfile, docker-compose.yaml, or another spec), `solution/solve.sh` and `tests/test.sh`. The verifier must write `/logs/verifier/reward.txt` or `reward.json` ([task overview](https://github.com/harbor-framework/harbor/blob/main/docs-mintlify/tasks/overview.mdx), [verifier](https://github.com/harbor-framework/harbor/blob/main/docs-mintlify/tasks/verifier.mdx)).
- **Prebuilt images.** `[environment].docker_image` lets providers skip the build: "most providers prefer the prebuilt image over building" ([environment](https://github.com/harbor-framework/harbor/blob/main/docs-mintlify/tasks/environment.mdx)).
- **Terminal-Bench 2.0.** 89 tasks selected from 229 contributed, run with `harbor run -d terminal-bench@2.0`. The paper's experiments used Daytona "between 32 and 100 containers in parallel", and results carry 95% CIs ([arXiv:2601.11868](https://arxiv.org/abs/2601.11868)).
  - The paper allows agents internet access.
  - It names residual nondeterminism from "hardware differences (e.g., CPU architectures)" and "container runtime enforcement".
- **My count of `harbor-framework/terminal-bench-2`** (2026-11-21):
  - All 89 tasks have `docker_image` set, and all have `allow_internet = true`.
  - 0 use docker-compose.
  - CPUs: 84 tasks × 1, 3 × 2, 2 × 4.
  - Memory: 69 tasks × 2 GB, 17 × 4 GB, 3 × 8 GB.
  - GPUs: 0.
  - Agent timeouts range from 600 s to 12,000 s; 2 tasks exceed 3,600 s.
  - All 89 images are **amd64-only**. Compressed sizes: median 149 MB, p90 614 MB, max 8.8 GB, sum 49 GB counting shared layers once per image.
  - A successor, [terminal-bench-2-1](https://github.com/harbor-framework/terminal-bench-2-1) (90 tasks), exists.

### 5.2 Pluggable sandbox providers

- **Built-in environments.** Harbor ships about 40 environments, including `docker` (the default), `podman`, `daytona`, `e2b`, `modal`, `runloop`, `vercel` and `gke`. It also has a local `kata` environment, whose docstring notes that image builds run under runc and that network policies aren't supported ([pre-integrated sandboxes](https://github.com/harbor-framework/harbor/blob/main/docs-mintlify/sandboxes/pre-integrated-sandboxes.mdx), [kata.py](https://github.com/harbor-framework/harbor/blob/main/src/harbor/environments/kata.py)).
- **Custom environments.**
  - "Implement `BaseEnvironment` to add one" and run it with `-e my_sandbox:MySandbox`. The required methods are `start`, `stop`, `exec(command, cwd, env, timeout_sec, user)`, `upload_file/dir` and `download_file/dir`, plus an optional `preflight()` for credentials ([custom-sandboxes](https://github.com/harbor-framework/harbor/blob/main/docs-mintlify/sandboxes/custom-sandboxes.mdx)).
  - The docs say: "Declare only capabilities the provider enforces." The flags include `disable_internet`, `network_allowlist`, `network_allowlist_hostnames` and `docker_compose` ([capabilities.py](https://github.com/harbor-framework/harbor/blob/main/src/harbor/environments/capabilities.py)).
- **Oracle and nop agents.** `oracle` runs `solution/solve.sh`; `nop` does nothing ([agents](https://github.com/harbor-framework/harbor/tree/main/src/harbor/agents)). **[Inference]** An oracle sweep is a positive control and a nop sweep a negative control, neither needing an LLM.
- **Both agent locations.** Harbor exercises both: "installed" agents (Claude Code, Codex, …) run inside the sandbox, while Terminus-2 drives a tmux session through `exec` and installs tmux/asciinema itself ([agents/installed](https://github.com/harbor-framework/harbor/tree/main/src/harbor/agents/installed), [terminus_2](https://github.com/harbor-framework/harbor/tree/main/src/harbor/agents/terminus_2)).
- **Evidence of gVisor compatibility gaps.** Harbor's Modal environment notes: "gVisor Modal sandboxes lack netlink permissions for creating veth pairs", so Docker-in-sandbox needs host networking. Modal defaults CPU-only Compose tasks to its VM runtime ([modal.py](https://github.com/harbor-framework/harbor/blob/main/src/harbor/environments/modal.py)).

**[Inference] Fit with our platform.**
- **Matches.**
  - Single-container prebuilt images match ADR 0003 ("register any OCI image as-is").
  - Harbor's `exec` signature matches our Exec parameters (user, cwd, env, timeout).
  - Its network capability model maps onto our Egress Policy modes.
- **Mismatches.**
  1. **Agent timeouts over our 1-hour Exec cap.** Two tasks exceed it, so the adapter must start long agent runs as a Process.
  2. **Network.** Every TB2 task assumes internet access, so the compatibility run needs a "fully open" or broad allowlist policy. That is itself a useful test of the Egress Proxy under real workloads.
  3. **Architecture.** The images are amd64-only, so the compatibility test belongs on the x86_64 host (phase 3, GCP), as spec §8 already says.
  4. **Builds.** Tasks without a prebuilt image need the adapter to build client-side and push to a registry, since building is a non-goal.

---

## 6. Isolation testing tools, threat models and CVEs (Q5)

### 6.1 Escape-probing tools in 2026

| Tool | Maintenance (GitHub, 2026-11-21) | What it actually checks | Caveat |
|---|---|---|---|
| [amicontained](https://github.com/genuinetools/amicontained) | Last release v0.4.9, 2019-11; last push 2020-12 | Detected runtime, namespaces, user-ns mappings, AppArmor profile, capability sets, seccomp mode, and a list of blocked syscalls found by trying them | Runtime detection is a fixed list that includes `gvisor` but has no microVM entry ([bpfd proc.go](https://github.com/genuinetools/bpfd/blob/master/proc/proc.go)). **[Inference]** Inside Firecracker it should report `not-found`, which is the correct teaching result. |
| [deepce](https://github.com/stealthcopter/deepce) | Last push 2025-12; no releases | Enumeration: container ID, IP/DNS, Docker version, interesting mounts, passwords in files, env vars, other containers, port scans of the host, exposed docker.sock. Exploits: docker group, privileged mode, exposed socket. | "some exploits will overwrite runC which can be destructive". Use enumeration only. |
| [CDK](https://github.com/cdk-team/CDK) | v1.5.6, 2026-02; last push 2026-05 | `cdk evaluate`: capabilities, mounts, net namespace, sensitive env/files/processes, cloud metadata API, K8s API. Exploits include runc CVE-2019-5736, containerd-shim CVE-2020-15257, docker.sock, mount-cgroup, CVE-2022-0492 user-ns abuse, procfs, ptrace, devices.allow | It's an offensive toolkit. Run `evaluate` by default and exploits only in a throwaway VM. |
| [docker-bench-security](https://github.com/docker/docker-bench-security) | Last release 2023-12; push 2026-06 | Host and daemon CIS checks, run from the host | Host-side, not a sandbox-side probe |

### 6.2 Official threat models to map attacks onto

- **Docker.** Four areas: kernel namespaces and cgroups, the "attack surface of the Docker daemon", loopholes in the container configuration profile, and kernel hardening features. Two key lines: "only trusted users should be allowed to control your Docker daemon"; cgroups ensure "a single container cannot bring the system down by exhausting one of those resources". Capabilities use "an allowlist instead of a denylist approach" ([Docker security](https://docs.docker.com/engine/security/)).
- **gVisor.**
  - The primary goal is to "minimize the System API attack vector". "No system call is passed through directly to the host". The Sentry "is not permitted to open new files, create new sockets…", with exceptions when host networking or **directfs** is enabled. It relies on host cgroups for resource exhaustion, and "does not provide protection against hardware side channels" ([gVisor security model](https://gvisor.dev/docs/architecture_guide/security/)).
  - A worked example: gVisor wasn't affected by CVE-2020-14386 because it doesn't implement `PACKET_RX_RING`, and raw sockets are off unless `--net-raw` is passed ([gVisor blog, 2020-09-18](https://gvisor.dev/blog/2020/09/18/containing-a-real-vulnerability/)).
- **Firecracker.**
  - "all vCPU threads are considered to be running malicious code as soon as they have been started". "Firecracker does not perform any network traffic filtering", so egress must be filtered on the host ([design.md](https://github.com/firecracker-microvm/firecracker/blob/main/docs/design.md)).
  - Production hosts should use the jailer, cgroups and seccomp, disable swap, disable SMT, disable KSM ("side channel issues that rely on page deduplication") and use Rowhammer-mitigated memory ([prod-host-setup](https://github.com/firecracker-microvm/firecracker/blob/main/docs/prod-host-setup.md)).

### 6.3 Container-escape CVEs that make good teaching tests

| CVE | Mechanism | Docker (runc) | gVisor | Firecracker (our design) | Safe to reproduce? |
|---|---|---|---|---|---|
| [CVE-2019-5736](https://nvd.nist.gov/vuln/detail/CVE-2019-5736) | Root inside the container overwrites the host runc binary (via `/proc/self/exe`). Per NVD, the attacker needs either "a new container with an attacker-controlled image" or an existing container they can write to that is later entered with exec. | "runc through 1.0-rc6, as used in Docker before 18.09.2" | **[Inference]** Not applicable: no runc, and `/proc/self/exe` resolves inside the Sentry's VFS | **[Inference]** Not applicable: no runc on the host path | Only in a disposable VM with pinned old Docker; CDK's `runc-pwn` exists. **[Inference]** The malicious-image variant maps to our private Templates. |
| [CVE-2024-21626](https://github.com/opencontainers/runc/security/advisories/GHSA-xr7r-f8xq-vfvv) "Leaky Vessels" | Leaked fd to host `/sys/fs/cgroup`. Attack 1: an image's `WORKDIR` is a symlink to `/proc/self/fd/7`. Attack 2: `runc exec --cwd` into a swapped symlink. Attack 3a/3b: overwrite a host binary. | runc ≤1.1.11 vulnerable; 1.1.12 fixed | **[Inference]** Not applicable | **[Inference]** Not applicable | Yes in a throwaway VM with runc 1.1.11. Attack 1 maps to *our private Templates*. **[Inference]** Attack 2 doesn't map, because our Exec cwd is resolved by the Guest Agent inside the Sandbox. |
| [CVE-2025-31133](https://github.com/opencontainers/runc/security/advisories/GHSA-9493-h29p-rfm2), [-52565](https://github.com/opencontainers/runc/security/advisories/GHSA-qw9x-cqr3-wc7r), [-52881](https://github.com/opencontainers/runc/security/advisories/GHSA-cgrx-mc8f-2prm) (2025-11) | Mount races on maskedPaths `/dev/null` and `/dev/console`, and procfs write redirects | "all known versions" before the fix | **[Inference]** Not applicable | **[Inference]** Not applicable | Harder: needs races with shared mounts. Better as a reading exercise. |
| [CVE-2022-0492](https://nvd.nist.gov/vuln/detail/CVE-2022-0492) | NVD: the cgroups v1 `release_agent` feature can be abused "to escalate privileges and bypass the namespace isolation". The CDK exploit reaches it via `unshare` of user+cgroup namespaces. | SWE-bench's own source notes that Docker's default seccomp "only permits CLONE_NEWUSER with CAP_SYS_ADMIN". **[Inference]** So hardened Docker blocks the `unshare` path, a cgroup v2 host has no v1 `release_agent`, and the SWE-bench-style `cap_add SYS_ADMIN` re-opens it. | **[Inference]** It would hit the Sentry's own cgroupfs, not the host's | **[Inference]** Guest kernel only | Yes. A clean "bare vs hardened" demo; CDK `abuse-unpriv-userns` |
| [CVE-2020-14386](https://nvd.nist.gov/vuln/detail/CVE-2020-14386) | AF_PACKET ring overflow; needs `CAP_NET_RAW` (a Docker default) | Vulnerable on an unpatched kernel unless NET_RAW is dropped | Not vulnerable (source above) | Guest kernel only | Don't run kernel exploits. Test "can I open a raw socket" instead. |
| [runc CVE-2026-119101](https://github.com/opencontainers/runc/security/advisories/GHSA-g2c3-j8g3-7gpw) (2026-11-12) | A malicious image makes `runc restore` (CRIU) bind-mount a host path | Only relevant if Hibernate on Docker used CRIU | n/a | n/a | Reading only. **[Inference]** A reason to keep Docker Hibernate "unsupported". |

**[Inference] How the Runtimes change the outcome.** On Docker, every runc and kernel CVE lands on the host kernel or host filesystem. On gVisor, the attack has to break the Sentry first, and then a seccomp-filtered, unprivileged Go process (see the 2020 blog). On Firecracker, the attack has to break the guest kernel, and then KVM and the jailed VMM. For the curriculum, a suite of real exploits matters less than probing *which layer answers each attack*: `uname -r` and `/proc/version` show the host kernel, a gVisor version or a guest kernel; amicontained shows the blocked-syscall list; the cgroup and mount views differ.

---

## 7. Measuring density and memory overhead fairly (Q6)

- **Definitions.**
  - RSS is "the amount of the mapping that is currently resident in RAM". PSS divides each page "by the number of processes sharing it". `smaps_rollup` gives per-process sums more cheaply than `smaps` ([kernel proc docs](https://docs.kernel.org/filesystems/proc.html)).
  - cgroup v2 `memory.current` is "the total amount of memory currently being used by the cgroup and its descendants". `memory.stat` splits it into anon, file (page cache, tmpfs), kernel and shmem. "A memory area is charged to the cgroup which instantiated it" ([cgroup v2](https://docs.kernel.org/admin-guide/cgroup-v2.html)).
- **Per-Runtime traps.**
  - **gVisor.** "A single memfd backs all application memory", accounted as shmem, so `anon` stays low. Use `memory.current` or `runsc usage`. In-sandbox statistics "are approximations" ([gVisor resource model](https://gvisor.dev/docs/architecture_guide/resources/)).
  - **Firecracker.** Guest memory is an anonymous mapping inside the VMM process. Firecracker's own overhead test subtracts the guest regions and reports `rss`, `size` and `uss` ([test_memory_overhead.py](https://github.com/firecracker-microvm/firecracker/blob/main/tests/integration_tests/performance/test_memory_overhead.py)). Its spec target is "≤5 MiB" VMM overhead ([SPECIFICATION.md](https://github.com/firecracker-microvm/firecracker/blob/main/SPECIFICATION.md)). Touched guest pages stay resident until a balloon reclaims them. The balloon "requires cooperation from a driver in the guest" and supports free-page reporting/hinting ([ballooning.md](https://github.com/firecracker-microvm/firecracker/blob/main/docs/ballooning.md)).
  - **Containers.** Per-container figures exclude shared daemons. CLOSER'21 used private dirty pages and noted the excluded ~40 MB `dockerd`.
- **Page sharing.**
  - KSM merges only `MADV_MERGEABLE` anonymous areas, was "originally developed for use with KVM", and "may use a lot of processing power". Savings come from `pages_sharing` and `general_profit` ([KSM docs](https://docs.kernel.org/admin-guide/mm/ksm.html)).
  - Firecracker recommends disabling KSM for side-channel reasons ([prod-host-setup](https://github.com/firecracker-microvm/firecracker/blob/main/docs/prod-host-setup.md)).
  - **[Inference]** Report density with KSM off by default, and optionally a KSM-on column that is labelled as weakening isolation.
- **[Inference] A method that is fair across all three Runtimes:**
  1. **Host-marginal (headline number).** Following gVisor's `perf.py density`, record `MemAvailable` and `Committed_AS` from `/proc/meminfo`. Then start N = 10, 50, 100 idle Sandboxes, each from the same Template, each answering one health Exec. Wait for steady state, and report (ΔMemAvailable)/N. This includes shims, Sentry/Gofer, VMM, guest kernel and page cache, and needs no knowledge of the Runtime's internals.
  2. **Attribution (explanatory).** For each Sandbox, sum the PSS of all its host processes (container shim; runsc sandbox/gofer; firecracker) and read the cgroup's `memory.current` and `memory.stat`. Report anon/file/shmem/kernel separately. Report fixed per-Host daemons (dockerd, containerd, Host Agent, Egress Proxy) once, never divided across Sandboxes.
  3. **After-workload residue.** Run a fixed workload (e.g. allocate and free 256 MB, then `pip install` a package), go idle, and re-measure. This shows memory a microVM never gives back without a balloon or free-page reporting.
  4. **Two density numbers.** *Physical density* is the largest N that still passes an Exec SLO (e.g. p99 below 2× baseline) before OOM or swap. *Schedulable density* is host memory divided by requested memory, because spec §6 never overcommits memory. They answer different questions.
  5. **For snapshot-restored microVMs**, PSS is required, not RSS, because clones may share file-backed memory pages.

---

## 8. Agent-level evaluation of a sandbox (Q7)

- **Parity experiments (Harbor).** Run the same agent and model on two environments over k reruns and compare mean ± SEM. Name the outlier tasks the environment causes, for example the CPU-bound timeout on Daytona ([SWE-bench adapter README](https://github.com/harbor-framework/harbor/blob/main/adapters/swebench/README.md)). Harbor keeps an aggregate [parity_summary.csv](https://github.com/harbor-framework/harbor/blob/main/adapters/parity_summary.csv) across its adapters.
- **Terminal-Bench metrics.** Resolution rate with 95% CI, agent execution time, model calls and tokens, and timeout rate per model. Most trials finish "in under 20 minutes", and the extremes reach "up to 2 hours" ([arXiv:2601.11868](https://arxiv.org/abs/2601.11868) App. A).
- **SWE-bench infrastructure-failure classifier.** It separates "the environment broke" from "the patch was wrong" by matching log signatures: OOM, container unavailable, network unreachable, and others. It is advisory only and never changes the denominator ([infra_failure.py](https://github.com/SWE-bench/SWE-bench/blob/main/swebench/harness/infra_failure.py)).
- **ComputeSDK OpenCode reliability.** Install the agent CLI inside a sandbox, run a trivial prompt, and check the output; 3 iterations, staggered 60 s apart ([sandbox-opencode.bench.ts](https://github.com/computesdk/benchmarks/blob/master/benchmarks/reliability/sandbox-opencode.bench.ts)).
- **Research on latency budgets.**
  - SpecBox treats on-demand sandbox start as a "cold-start penalty" on the agent's critical path. It overlaps bootstrapping with model inference and reports P99 end-to-end latency cut "by up to 2.9×" against on-demand ([arXiv:2607.23933](https://arxiv.org/abs/2607.23933)).
  - AgentSysBench finds "non-LLM components dominating latency in 5 of 10 applications" and sandbox working-set memory peaking at 28 GB per session ([arXiv:2608.15127](https://arxiv.org/abs/2608.15127)).
  - DSec (DeepSeek) exposes container, microVM and full-VM backends behind one SDK, but publishes no head-to-head comparison ([arXiv:2609.22978](https://arxiv.org/abs/2609.22978)).
- **[Inference] Gap.** I found no published study of task success rate for container vs gVisor vs microVM. Our two demo agents could produce a small version: same task, same model, N runs per Runtime.

---

## 9. Proposed measurement protocol for our harness (**this whole section is inference**)

### 9.1 Definitions (each a pair of named timestamps)

All server-side timestamps come from one monotonic clock per process and are exposed through `GET .../events` (ticket 17). Client timestamps come from the harness.

| Metric | Start | End | Notes |
|---|---|---|---|
| **create latency (client)** | harness sends `POST /v1/sandboxes` | response received (Running) | Our create blocks until Running |
| **create-to-first-Exec (TTFE)** | same | client receives exit code of `true` | Comparable to ComputeSDK TTI, but run `true` instead of `node -v` |
| scheduling | Control Plane accepts | Host chosen and Host Agent acknowledges | |
| **image prepare** | Host Agent starts pull/unpack/rootfs conversion | done | New phase. Zero when the image is cached; the image digest and size are recorded |
| Runtime start | Host Agent calls the Runtime | Runtime reports started (container started / `InstanceStart` returned / snapshot load returned) | |
| **kernel boot** (Firecracker only) | `InstanceStart` | guest `/sbin/init` (boot-timer device or first serial/vsock byte) | Same definition as the Firecracker spec |
| **snapshot restore** (Firecracker, phase 3) | snapshot load request | load returned | Report together with restore-to-first-Exec, because of lazy page faults |
| **Guest Agent ready** | Runtime start ends | first authenticated health RPC answered | The GLOSSARY's Starting → Running boundary |
| Exec RTT | client sends Exec `true` on a Running Sandbox | client has the result | Warm Sandbox, serial |
| destroy | `DELETE` sent | Terminated *and* the Host Agent reports the cgroup/VMM gone | Async teardown, per HotCloud'19 |

### 9.2 Warm and cold handling

Every create number is labelled with one cache state. Never mix them, and never discard the first run.

- **C0 cold-image.** Image not on the Host; page cache dropped (`echo 3 > /proc/sys/vm/drop_caches`).
- **C1 warm-image.** Image cached; page cache dropped.
- **C2 warm-cache.** Image cached; same Template created in the last minute.
- **S (phase 3).** Restore from a Template snapshot, with a cold or warm snapshot file in page cache.
- **Pool-warm** (if ever added). Reported separately and never in the comparison table.

### 9.3 Load shapes and sample sizes

- **Serial.** n = 200 per (Runtime × cache state) for p50 and p95. For a p99 that means something, n ≥ 1,000, so that about 10 samples lie above it. Firecracker's paper used 500 serial samples; with 100 samples, p99 is about the second-largest value.
- **Burst.** Concurrency 10, 50 and 100, repeated 10 times, with the success rate reported.
- **Open-loop throughput.** A fixed arrival rate (λ = 1, 2, 5, 10 creates/s) for 60 s. Latency is measured from the scheduled send time, to avoid coordinated omission, and the λ at which p99 exceeds 2× serial p99 is reported.
- **Statistics.** Report the median with a bootstrap 95% CI, plus p95 and p99, min/max and the failure count. Comparing Runtimes uses medians with CIs, not means.

### 9.4 What to record with every run

- **Host.** CPU model, cores, SMT on/off, RAM, host kernel version, cgroup v1/v2, KSM on/off, THP mode, swap on/off, CPU governor, and whether the host is nested virtualization (the GCP n2 nested-virt case for Firecracker).
- **Runtime.** Docker/containerd/runc versions; runsc version, platform (systrap/KVM/ptrace), directfs and network mode; Firecracker version, jailer on/off, guest kernel version, config hash and boot args; seccomp profile hash; "bare" or "hardened".
- **Mac (phase 1).** Docker Desktop version, VM type (Apple Virtualization / QEMU), VM CPU/memory/disk, Rosetta on/off, and the image architecture. Never publish a timing taken under emulation.
- **Sandbox spec.** Template digest and compressed/uncompressed size, vCPU, memory, disk and Egress Policy.
- **Harness.** Harness git commit, where the client runs (same Host over loopback vs remote) and the measured RTT to the Control Plane.

### 9.5 Isolation-suite output

For each attack × configuration, report:
- the result: blocked / not blocked / known gap;
- the layer that answered, e.g. "seccomp EPERM on unshare", "Sentry: syscall not implemented", "guest kernel only", "Egress Proxy 403";
- the evidence: the exact command and its output.

---

## 10. Impact on our design (**every proposed change is inference**)

| # | Finding (section) | Affects | Proposed change |
|---|---|---|---|
| 1 | Harbor has a documented custom-sandbox interface; TB2 is 89 single-container, prebuilt-image tasks with oracle solutions (§5) | spec §8 "Compatibility"; new ticket | **Make Terminal-Bench 2.0 via Harbor the primary compatibility test, and keep SWE-bench as a secondary one.** Add a ticket: "Harbor `BaseEnvironment` for our public API". Acceptance: oracle sweep on Docker = baseline, then a per-task diff for gVisor and Firecracker; nop sweep = all fail. The **oracle/nop sweeps are the pass criterion**. A real-agent run on a 10-task subset is optional. |
| 2 | The SWE-bench harness can't target other backends; it needs root, `/testbed` and `SYS_ADMIN` (§4) | spec §8; ADR 0003's "run the official images directly" | Replace "a subset of SWE-bench Lite" with "SWE-bench Verified Mini (50), gold + empty patch". Use a thin runner that builds `TestSpec` with the `swebench` library, runs the official instance image as a Template, runs `eval_script` via Exec as root, and grades with `swebench.harness.grading` on the client. Report the per-instance confusion matrix against the official Docker harness and classify the differences (§4.5). |
| 3 | SWE-bench and TB2 images are amd64-only (§4.2, §5.1) | spec §9 phases | Keep the compatibility tests on x86_64 Linux (phase 2 on GCP and phase 3), never on the arm64 Mac. State in ticket 18 that no number taken under emulation is published. |
| 4 | Our hardened Docker drops all capabilities; SWE-bench adds `SYS_ADMIN` and runs as root (§4.1) | ticket 16; spec §7 | Expect compatibility differences under "hardened". The compatibility run reports both "hardened" and "bare" Docker, so the table shows what each hardening step costs in compatibility. That cost is part of the lesson. |
| 5 | TB2 needs internet access; the Harbor SWE-bench verifier uses `uv` + PyPI (§4.3, §5.1) | ticket 12 (Egress Policy); Allowlist Presets | Run the compatibility suites under `package-registries` + `github` presets first and fully open second. Record which tasks need which domains. This doubles as a real-workload test of the Egress Proxy. |
| 6 | Two TB2 tasks have agent timeouts over 3,600 s; Exec is capped at 1 h (§5.1) | spec §4; ticket 06 | The Harbor adapter maps `exec` with `timeout_sec > 3600` to a Process and polls its output. No API change. |
| 7 | "Start time" is defined differently by everyone (§3) | spec §8 "Performance"; tickets 17 and 18 | Adopt the definitions in §9.1. Add an **image prepare** phase to the three in ticket 17, add client-side TTFE, and require cache-state labels C0/C1/C2/S on every number. |
| 8 | Snapshot restore returns before pages are faulted in (Firecracker CI) (§2.3) | spec §8 phase 3; spec §9 phase 3 | Report "restore call" and "restore → first Exec" as two columns, next to "cold kernel boot". |
| 9 | Memory accounting differs per Runtime; Firecracker says disable KSM (§7) | spec §8 density/memory; ticket 18; ticket 17 | Headline = host-marginal ΔMemAvailable/N; attribution = PSS + cgroup `memory.stat`. Report physical vs schedulable density. Default to KSM off. Have the Host Agent export host `MemAvailable` and per-Sandbox PSS as Prometheus metrics, so the harness stays a black box with respect to the Runtime. |
| 10 | Load shape and percentiles drive results; coordinated omission (§2.4) | ticket 18 | Serial n ≥ 1,000 for p99, burst, and open-loop throughput, with bootstrap CIs (§9.3). |
| 11 | `amicontained` is unmaintained; deepce exploits can overwrite runc; CDK is maintained (§6.1) | ticket 19; spec §8 "Isolation" | Use **CDK `evaluate`** (pinned version) + **deepce enumeration only** + amicontained (pinned, for its syscall and capability listing, labelled "unmaintained since 2019"). Never run exploit modes in the default suite. |
| 12 | Leaky Vessels attack 1 = a malicious image's WORKDIR; private Templates come from Tenants (§6.3) | ticket 19; ticket 13 | Add **"malicious Template"** attacks: WORKDIR symlink to `/proc/self/fd/N`, a `/dev/null`-replacing image, a setuid binary in the image. On patched runc they are regression tests. Optionally, a "lab" mode in a throwaway VM pins runc 1.1.11 to show the real escape on Docker and its absence on gVisor and Firecracker. |
| 13 | Leaky Vessels attack 2 needs `runc exec --cwd`; our Exec cwd goes through the Guest Agent (§6.3) | ticket 19; ADR 0002/0003 | Add a test: replace `/home/user` with a symlink to `/proc/self/fd/7`, then Exec with the default cwd. Expected: no host access, because cwd is resolved inside the Sandbox. Note in the learning notes that "one entry point" removes this class of attack. |
| 14 | CVE-2022-0492 and CVE-2020-14386 depend on `unshare`/`SYS_ADMIN` and `NET_RAW` (§6.3) | ticket 19 | Add "unshare user+cgroup ns and write `release_agent`" (blocked by seccomp on hardened; show it under bare + SYS_ADMIN) and "open AF_PACKET raw socket" (hardened drops NET_RAW; gVisor refuses by default). Neither runs a kernel exploit. |
| 15 | gVisor and Firecracker threat models exclude hardware side channels; Firecracker asks for SMT and KSM off (§6.2) | ticket 19; spec §7 | Add a host-configuration check row (SMT, KSM, swap, kernel mitigations via `/sys/devices/system/cpu/vulnerabilities`) reported as "configuration", not "attack". Record the known gap honestly. |
| 16 | Information-disclosure surfaces differ by Runtime (§6.3) | ticket 19 | Add probes for `uname -r` and `/proc/version`, `/proc/kcore`, `/proc/timer_list`, `/proc/keys`, `dmesg`, `/sys/firmware` and CPU model leakage, with the expected answer per Runtime. |
| 17 | No published agent-level container vs microVM comparison; Harbor and TB use parity + CIs (§8) | tickets 20 and 24 | Run each demo agent N = 10 times per Runtime. Record success rate with a CI; per-tool-call time split into LLM, network and sandbox; Sandboxes, Execs and Usage; and failures classified as environment / ambiguous / agent (following SWE-bench's classifier). |
| 18 | CRIU restore has its own runc CVE (2026-11) (§6.3) | spec §4 (Docker Hibernate "unsupported") | Keep Hibernate unsupported on Docker, and cite this advisory in the learning note as one reason. |

**Answer to "replace or join?"** Join, but swap priority. TB2 + Harbor becomes the primary compatibility test because it: (a) plugs into a documented interface instead of a Docker-only harness; (b) needs no image builds; (c) has oracle and nop controls that need no LLM; (d) exercises both agent locations; (e) is what the industry's sandbox vendors already integrate with. SWE-bench Verified Mini stays as the secondary test because it covers Python test suites that are heavy on syscalls and the filesystem, it is what spec §8 promised, and its gold/empty-patch method is cheap.

**Proposed ticket 19 attack list** (current list plus additions **in bold**): fork bomb · memory bomb · fill disk (known gap on Docker Desktop) · Host Agent via `host.docker.internal` · metadata address · other Sandboxes · impersonate the platform to the Guest Agent · privilege escalation (**setuid binary from the Template; `no-new-privileges` check**) · escape probing (**CDK `evaluate`, deepce enumeration, amicontained; pinned versions**) · **malicious Template: WORKDIR → `/proc/self/fd/N`** · **Exec-cwd symlink swap** · **`unshare` + cgroup `release_agent`** · **raw socket / NET_RAW** · **kernel-identity and `/proc` info-leak probes** · bypass the egress allowlist · DNS tunnelling · Egress Proxy to Host Agent / metadata / private addresses · read a Secret's real value · **host configuration check (SMT/KSM/swap/mitigations)**.

---

## 11. Sources (all accessed 2026-11-21)

**Benchmarks and papers**
- ComputeSDK benchmarks repo: https://github.com/computesdk/benchmarks ; [tti.bench.ts](https://github.com/computesdk/benchmarks/blob/master/benchmarks/sandbox/tti.bench.ts) ; [scoring.ts](https://github.com/computesdk/benchmarks/blob/master/benchmarks/sandbox/scoring.ts) ; [TTI workflow](https://github.com/computesdk/benchmarks/blob/master/.github/workflows/sandbox-tti-benchmarks.yml) ; [dax.bench.ts](https://github.com/computesdk/benchmarks/blob/master/benchmarks/sandbox/dax.bench.ts) ; [sandbox-opencode.bench.ts](https://github.com/computesdk/benchmarks/blob/master/benchmarks/reliability/sandbox-opencode.bench.ts)
- Agache et al., Firecracker, NSDI 2020: https://www.usenix.org/system/files/nsdi20-paper-agache.pdf ; data: https://github.com/firecracker-microvm/nsdi2020-data
- Young et al., The True Cost of Containing, HotCloud 2019: https://www.usenix.org/system/files/hotcloud19-paper-young.pdf
- Anjali et al., Blending Containers and Virtual Machines, VEE 2020: https://pages.cs.wisc.edu/~swift/papers/vee20-isolation.pdf
- Li et al., Comparative Performance Study of Lightweight Hypervisors, CLOSER 2021: https://www.scitepress.org/Papers/2021/104405/104405.pdf
- Merrill et al., Terminal-Bench, arXiv:2601.11868 (ICLR 2026): https://arxiv.org/abs/2601.11868
- SpecBox, arXiv:2607.23933: https://arxiv.org/abs/2607.23933
- AgentSysBench, arXiv:2608.15127: https://arxiv.org/abs/2608.15127
- DSec, arXiv:2609.22978: https://arxiv.org/abs/2609.22978
- wrk2 (coordinated omission): https://github.com/giltene/wrk2

**Vendor and project definitions**
- Firecracker SPECIFICATION.md: https://github.com/firecracker-microvm/firecracker/blob/main/SPECIFICATION.md
- Firecracker performance tests: [test_boottime.py](https://github.com/firecracker-microvm/firecracker/blob/main/tests/integration_tests/performance/test_boottime.py), [test_snapshot.py](https://github.com/firecracker-microvm/firecracker/blob/main/tests/integration_tests/performance/test_snapshot.py), [test_memory_overhead.py](https://github.com/firecracker-microvm/firecracker/blob/main/tests/integration_tests/performance/test_memory_overhead.py)
- Firecracker docs: [design.md](https://github.com/firecracker-microvm/firecracker/blob/main/docs/design.md), [prod-host-setup.md](https://github.com/firecracker-microvm/firecracker/blob/main/docs/prod-host-setup.md), [ballooning.md](https://github.com/firecracker-microvm/firecracker/blob/main/docs/ballooning.md)
- gVisor: [performance guide](https://gvisor.dev/docs/architecture_guide/performance/), [security model](https://gvisor.dev/docs/architecture_guide/security/), [resource model](https://gvisor.dev/docs/architecture_guide/resources/), [Containing a Real Vulnerability (2020-09-18)](https://gvisor.dev/blog/2020/09/18/containing-a-real-vulnerability/)
- E2B template how-it-works: https://docs.e2b.dev/template/how-it-works.md
- Daytona: [Daytona Cloud launch post (2025-04-28)](https://www.daytona.io/dotfiles/introducing-daytona-cloud-the-agent-native-infrastructure), [v2 architecture](https://www.daytona.com/docs/architecture.md)
- Modal: [Unpacking sandbox startup latency (2026-06-22)](https://modal.com/blog/unpacking-sandbox-startup-latency), [products/sandboxes](https://modal.com/products/sandboxes)

**Eval harnesses**
- SWE-bench: [README](https://github.com/SWE-bench/SWE-bench/blob/main/README.md), [docker_setup.md](https://github.com/SWE-bench/SWE-bench/blob/main/docs/guides/docker_setup.md), [evaluation.md](https://github.com/SWE-bench/SWE-bench/blob/main/docs/guides/evaluation.md), [datasets.md](https://github.com/SWE-bench/SWE-bench/blob/main/docs/guides/datasets.md), [faq.md](https://github.com/SWE-bench/SWE-bench/blob/main/docs/faq.md), [run_evaluation.py](https://github.com/SWE-bench/SWE-bench/blob/main/swebench/harness/run_evaluation.py), [image_spec.py](https://github.com/SWE-bench/SWE-bench/blob/main/swebench/image_builder/image_spec.py), [image_builder constants](https://github.com/SWE-bench/SWE-bench/blob/main/swebench/image_builder/constants/__init__.py), [infra_failure.py](https://github.com/SWE-bench/SWE-bench/blob/main/swebench/harness/infra_failure.py)
- SWE-bench datasets: [Lite](https://huggingface.co/datasets/SWE-bench/SWE-bench_Lite), [Verified](https://huggingface.co/datasets/SWE-bench/SWE-bench_Verified), [Verified Mini](https://huggingface.co/datasets/MariusHobbhahn/swe-bench-verified-mini)
- Epoch AI, SWE-bench Docker images (2025-07-10): https://epoch.ai/blog/swebench-docker
- SWE-ReX: https://github.com/SWE-agent/SWE-ReX ; mini-swe-agent environments: https://github.com/SWE-agent/mini-swe-agent/tree/main/src/minisweagent/environments
- Harbor: [repo](https://github.com/harbor-framework/harbor), [custom-sandboxes](https://github.com/harbor-framework/harbor/blob/main/docs-mintlify/sandboxes/custom-sandboxes.mdx), [pre-integrated sandboxes](https://github.com/harbor-framework/harbor/blob/main/docs-mintlify/sandboxes/pre-integrated-sandboxes.mdx), [task overview](https://github.com/harbor-framework/harbor/blob/main/docs-mintlify/tasks/overview.mdx), [environment](https://github.com/harbor-framework/harbor/blob/main/docs-mintlify/tasks/environment.mdx), [verifier](https://github.com/harbor-framework/harbor/blob/main/docs-mintlify/tasks/verifier.mdx), [capabilities.py](https://github.com/harbor-framework/harbor/blob/main/src/harbor/environments/capabilities.py), [kata.py](https://github.com/harbor-framework/harbor/blob/main/src/harbor/environments/kata.py), [modal.py](https://github.com/harbor-framework/harbor/blob/main/src/harbor/environments/modal.py), [agents](https://github.com/harbor-framework/harbor/tree/main/src/harbor/agents), [SWE-bench adapter](https://github.com/harbor-framework/harbor/tree/main/adapters/swebench), [parity_summary.csv](https://github.com/harbor-framework/harbor/blob/main/adapters/parity_summary.csv)
- Terminal-Bench 2.0 tasks: https://github.com/harbor-framework/terminal-bench-2 ; 2.1: https://github.com/harbor-framework/terminal-bench-2-1

**Isolation tools, threat models, CVEs**
- amicontained: https://github.com/genuinetools/amicontained ; runtime list: https://github.com/genuinetools/bpfd/blob/master/proc/proc.go
- deepce: https://github.com/stealthcopter/deepce ; CDK: https://github.com/cdk-team/CDK ; docker-bench-security: https://github.com/docker/docker-bench-security
- Docker Engine security: https://docs.docker.com/engine/security/
- runc advisories: [CVE-2024-21626](https://github.com/opencontainers/runc/security/advisories/GHSA-xr7r-f8xq-vfvv), [CVE-2025-31133](https://github.com/opencontainers/runc/security/advisories/GHSA-9493-h29p-rfm2), [CVE-2025-52565](https://github.com/opencontainers/runc/security/advisories/GHSA-qw9x-cqr3-wc7r), [CVE-2025-52881](https://github.com/opencontainers/runc/security/advisories/GHSA-cgrx-mc8f-2prm), [CVE-2026-119101](https://github.com/opencontainers/runc/security/advisories/GHSA-g2c3-j8g3-7gpw)
- NVD: [CVE-2019-5736](https://nvd.nist.gov/vuln/detail/CVE-2019-5736), [CVE-2022-0492](https://nvd.nist.gov/vuln/detail/CVE-2022-0492), [CVE-2020-14386](https://nvd.nist.gov/vuln/detail/CVE-2020-14386)
- Linux kernel docs: [/proc (smaps, PSS)](https://docs.kernel.org/filesystems/proc.html), [cgroup v2](https://docs.kernel.org/admin-guide/cgroup-v2.html), [KSM](https://docs.kernel.org/admin-guide/mm/ksm.html)

**Own measurements** (2026-11-21, method stated in §4.2 and §5.1): Docker Hub registry v2 manifests and config blobs for all 89 TB2 `docker_image` values and 6 SWE-bench instance images; `task.toml` fields aggregated from the `terminal-bench-2` repo at `main`.
