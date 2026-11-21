# 调研复盘：六份报告改变了什么

> [English](research-review.md) | 中文

一份工作文档。它合并了 2026-11-21 六份调研笔记的建议，去掉重复，标出冲突，列出需要我们决定的事。每项决定做出后，会写回 GLOSSARY、spec、ADR 和任务，并在这里标为 **已决定**。

报告：
[落地与路线图](../../docs/research/landscape-and-roadmap.zh.md) ·
[Mac 上的 Docker/gVisor](../../docs/research/phase1-docker-gvisor-on-mac.zh.md) ·
[Firecracker](../../docs/research/firecracker-microvm.zh.md) ·
[GPU](../../docs/research/gpu-sandboxing.zh.md) ·
[控制面](../../docs/research/control-plane-and-guest-agent.zh.md) ·
[验证](../../docs/research/verification-and-benchmarks.zh.md) ·
（更早的）[客户怎么用 sandbox](../../docs/research/agent-sandbox-usage.zh.md)

## 1. 早先说法的纠正

| 早先的说法 | 来源实际怎么说 | 报告 |
|---|---|---|
| AWS 上跑 KVM 要用 `.metal` 实例 | 从 2026-02-16 起，普通 Intel 实例（C8i/M8i/R8i）支持嵌套 KVM，2026-06-18 起扩展到第 7 代 | Firecracker |
| 用 `firecracker-go-sdk` | 2026-11-10 已归档，`firecracker-containerd` 也是；E2B 直接调 REST API | Firecracker |
| Docker Desktop 上磁盘限制不生效，"因为虚拟机用 ext4" | 结论大概率成立，但原因是它要求 xfs+pquota，加上 containerd 镜像存储会悄悄忽略这个选项 | Docker/gVisor |
| 默认禁止出网比所有人都严 | Cloudflare、OpenAI 托管 shell、Google 代码执行、Substrate 也默认禁止 | 落地 |
| Hetzner GEX44 约每月 €180 | GEX44 已下架；GEX45 每月 €214，另收 €209 开通费 | GPU |
| 用我们的 sandbox 跑 SWE-bench Lite 子集 | SWE-bench 的测试框架不支持换后端；Harbor 上的 Terminal-Bench 2.0 支持 | 验证 |
| Host Agent 和 Guest Agent 之间的 unix socket "直接能用" | 只在 Docker Desktop ≥ 4.86 且用 VirtioFS 时可用；gRPC FUSE 下未知；gVisor 需要 `--host-uds=create` | Docker/gVisor |

## 2. 多份报告意见一致的地方（证据更强）

- **把第三阶段拆小**，并且**推迟"收到流量自动恢复"**（落地报告和 Firecracker 报告）。
- **用户代码运行之前，由 Host Agent 推送一个 Init 步骤**，带上 token、环境变量和 CA，每次恢复后都重来一遍（Firecracker 报告和控制面报告）。
- **默认禁止出网，加上用占位符做 Secret**，是主流做法（落地报告和更早的那份报告）。

## 3. 看起来冲突、实际已解决的地方

- **Daytona 算不算后台进程为活动？** 控制面报告读的是 v0.190.0 的代码（不算）；更早那份读的是 v2 的文档（算，"running commands count as activity"）。两者对各自的版本都对；我们"正在运行的 Process 算活动"的决定不变。
- **第二阶段的 Hibernate。** 落地报告建议在 gVisor 上用 `runsc checkpoint` 作为选做目标；验证报告说因为 2026 年 11 月的一个 runc/CRIU 漏洞，*Docker* 上保持不支持 Hibernate。两条说的是不同的 Runtime，不冲突。

## 4. 待决定的事（第六轮）

| # | 主题 | 影响 |
|---|---|---|
| D1 | 数据通路：Exec 和文件流量直接到 Host，不经过 Control Plane | 新 ADR 0007，spec §3–4 |
| D2 | 把 ADR 0005 收窄到 Sandbox 边界；第一阶段 Control Plane ↔ Host Agent 走 unix socket，以后由 Host 主动建隧道 | ADR 0005，任务 16 |
| D3 | Guest Agent 的生命周期：PID 1，每次启动和恢复都有一次 Init 调用，non-dumpable | 新 ADR 0008，ADR 0003，任务 01/16/23 |
| D4 | 第一阶段在 Docker Desktop 上的网络结构和最低版本 | spec §3/§7，ADR 0002，任务 01/12 |
| D5 | 新增 Stopped 状态（保留磁盘和身份），空闲时自动进入 | GLOSSARY，spec §5–6，新任务 |
| D6 | Capture 和 Secret CA 的修正 | ADR 0006，任务 22/23 |
| D7 | 验证方案大改 | spec §8，任务 18–21/24，新任务 |
| D8 | 路线图和任务顺序调整 | spec §9，任务 |
| D9 | 把实现细节写进任务 | 任务 06/07/09/10/11/16/17，spec §7 |
| D10 | 第三、四阶段的细节先记下，暂不决定 | spec §9–10 |

**已决定（2026-11-21）：D1–D10 全部按推荐采纳。** 各项落到了哪里：

- D1 → [ADR 0007](../../docs/adr/0007-data-path-to-host.zh.md)、spec §3–4、任务 01
- D2 → [ADR 0005](../../docs/adr/0005-connections-only-outside-in.zh.md) 的修订、任务 16
- D3 → [ADR 0008](../../docs/adr/0008-guest-agent-lifecycle.zh.md)、[ADR 0003](../../docs/adr/0003-platform-injects-guest-agent.zh.md) 的修订、GLOSSARY 的 "Init"、任务 01/16/23
- D4 → [ADR 0002](../../docs/adr/0002-single-guest-agent-across-runtimes.zh.md) 的修订、spec §3/§7、任务 00/12
- D5 → GLOSSARY 的 "Stopped"、spec §5–6、任务 07/09 和新任务 25
- D6 → [ADR 0006](../../docs/adr/0006-secrets-as-placeholders.zh.md) 的修订、任务 22/23
- D7 → spec §8、任务 17–20/24 和新任务 27
- D8 → spec §9、任务 08/15/21、新任务 00 和 26
- D9 → spec §5–7、任务 04/06/07/09/10/11/16
- D10 → spec §10

## 5. 任务 01 之前：在 Mac 上做实验

Docker/gVisor 报告的 §5 列了文档定不下来的实验 E1–E8（unix socket 能否穿过 VirtioFS、能否连到 Mac 上只监听 127.0.0.1 的服务、`--storage-opt size` 是否被悄悄忽略等）。先用 `/prototype` 把它们跑一遍，第一阶段的地基就从"文档上说可以"变成"实测过"。
