## Context

动机见 `proposal.md`。`server.publish` 在每个会话外层用 `time.Now`/`time.Since` 包住完整 `publishSession`，其中包括可见玩家集合构造、区块与掉落物发布、完整背包状态、切片和 map 分配以及 outbox 入队。报告沿用历史字段名 `interest_diff`，但它不是一个隔离的纯兴趣差分微基准。

`measureMultiplayerServerProbe` 在主 benchmark 的载入、still、flying、传输收尾和 GPU 阶段之后运行，并在同一进程中创建八对 Memory stream；没有独立进程或堆/GC 状态归一化。相同提交的 Memory/TCP 顶层报告因而会执行相同的内部 server probe，却已出现 `interest_diff.p99_ms` 从 `0.028333ms` 到 `0.051875ms` 的 83% 波动，并在后续 pair 中反向变化。M4D 没有修改三个 publication 文件，定向 `BenchmarkEightPlayerInterest` 的当前中位数也未相对 M4D 前基线变慢。

## Goals / Non-Goals

**Goals:**

- 让同硬件回归门禁只使用有稳定语义和重复证据的相对指标。
- 保留历史报告兼容性、完整性证据和服务端整体性能覆盖。
- 在不重新采样 Memory、不升级场景或重建基线的前提下恢复 M4D 一次性正式链。

**Non-Goals:**

- 不重构服务端发布路径或把 `interest_diff` 拆成新的微观计时点。
- 不隔离整个 benchmark 到子进程，不控制 Go GC 或 OS 调度。
- 不新增或放宽绝对阈值，不修改 20% 比例、producer、JSON schema 或 accepted baseline。
- 不把 `BenchmarkEightPlayerInterest` 变成依赖单机纳秒值的硬编码测试。

## Decisions

### 1. 从同 transport 相对 profile 移除完整 `interest_diff` 摘要

`cmd/perfcheck` 继续在 scenario v6 及后续报告中校验 `interest_diff` 的样本数、正值和分位数单调性，但构造同 transport 相对延迟列表时不再加入该字段。只移除 p99 仍会让同提交曾波动 28.5% 的 p95 继续制造相同假阳性；只保留 p50 则仍把错误边界包装成稳定契约。因此整个摘要都不参与相对比较。

服务端整体退化仍由同 transport 的 tick p50/p95/p99、server outbound bytes 和 peak RSS 相对比较，以及 outbox/player queue、tick p99/max 和 RSS 绝对上限覆盖。定向 `BenchmarkEightPlayerInterest` 保留用于提交前诊断耗时与分配变化，但不引入新的机器相关阈值。

否决方案：重构或隔离 probe 会改变工作负载和计时语义，需要 scenario v9 与新基线；当前证据只要求停止错误解释，新增复杂测量系统不符合最小修复。

### 2. 保持 scenario v8、report schema 和基线逐字不变

producer 仍采集 200 tick、1600 个单会话发布时间样本并写入相同 JSON。比较器 profile 不属于 workload 或 schema，因此不提升 scenario。既有 v6/v7/v8 报告继续可读，M5 baseline 不修改；只有 `interest_diff` 单独越过相对阈值时的比较结果发生有意变化。

字段名继续保留以避免 schema 迁移，但中文规格与设计明确其真实语义。以后若要测量纯兴趣计算，必须新增独立字段和场景版本，不能静默复用 `interest_diff`。

### 3. 复判不可变 Memory 报告，再继续未执行的 TCP 步骤

修复后的比较器先读取 `/tmp/mcgo-m4d-v8-6d275a81688e-memory.json`。执行前后都校验既有 SHA-256 `a878195ac2e37bf5d1fe19cc67b833fcd7371073586d768f7cdf6834b2e84d95`，不运行 Memory benchmark。这样修正的是判定规则，不是通过重采样挑选结果。

Memory 若通过全部剩余门禁，才从精确提交 `6d275a81688e8b53263ae17ecc7754b02c9ba601` 的隔离 worktree 生成一次 `/tmp/mcgo-m4d-v8-6d275a81688e-tcp.json`。生成前确认路径不存在且 benchmark 使用 headless device；完成后核对 commit、scenario、transport、硬件、framebuffer、GPU 样本和哈希，再用修复后的比较器对 M5 baseline 判定。失败立即停止且不重跑。

否决在比较器修复后的新 HEAD 重跑 Memory/TCP pair：它会丢弃已有有效采样并违反失败后不挑选有利报告的纪律；比较器源码不影响原始生产提交的游戏工作负载。

### 4. 以 profile 变异测试锁定最小差异

先在 `cmd/perfcheck/main_test.go` 构造同 transport 完整报告，使 `interest_diff` 的 p50、p95、p99 分别超过 20%，预期新契约不产生 failure；测试在旧实现上必须转红。同时保留或新增反例，证明样本不足、非正、非单调、server tick 20.1% 退化、跨硬件和绝对门禁仍失败。生产修改只删除相对 profile 中的 `interest_diff` 条目，不改通用比较函数。

GitNexus 已从仓库移除且当前没有可调用工具。编辑前使用精确 symbol/caller 搜索和 `git diff` 作为 fallback，报告 `appendV6MultiplayerRegressions` 只由性能比较路径调用、影响 scenario v6 及后续同 transport 报告；提交前用同样方法核对实际改动范围。

## Risks / Trade-offs

- [发布路径的局部性能回退不再由 `interest_diff` 相对分位数直接判红] → 保留 server tick、outbound、RSS、队列门禁和定向微基准；出现第二个需要稳定局部计时的真实案例时再设计独立字段与 scenario v9。
- [历史字段名仍可能误导] → 保持 schema 兼容，同时在主规格、design 和测试名称中明确“单会话完整发布时间”。
- [旧报告由新比较器得到不同结论] → 只改变已证实不稳定字段的相对解释；报告原始字节、全部绝对门禁和其他相对指标保持不变。
- [TCP 单次运行仍可能暴露其他真实或测量问题] → 继续执行失败即停，不重跑、不改阈值、不覆盖 baseline。

## Migration Plan

1. 提交并严格校验本 change 的 OpenSpec 产物，不修改 M4D 生产代码或现有报告。
2. 按 TDD 修改 `cmd/perfcheck` 的同 transport profile，运行定向与全量验证并提交代码。
3. 用修复后的比较器复判既有 Memory 报告，确认哈希不变；任何剩余失败立即停止。
4. Memory 通过后，从 `6d275a8` 隔离 worktree 生成唯一一次 headless TCP 报告并判定。
5. 两步均通过后，把精确命令、哈希和结果写入 M4D tasks，再完成本 change 和 M4D 后续收尾。

回退只需恢复比较器 profile 与对应规格提交；不涉及报告、基线、协议或存档迁移。
