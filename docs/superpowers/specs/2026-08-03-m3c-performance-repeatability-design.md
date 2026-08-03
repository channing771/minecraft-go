# M3C 性能验收可重复性设计

日期：2026-08-03
状态：设计方向与全部设计段已批准

## 1. 背景

M3C Task 17 在 commit `b58d8bd761f6b11d2bb3ae8f42903d6e81bc5676` 上完成了新的
Step 5 全量门禁。随后各执行一次正式 scenario v6 Memory 与 TCP 报告；两份报告均通过
全部绝对门禁，但同 scenario 的 Memory→TCP 比较失败：

- server tick p99：`0.169833 → 0.207542 ms`，增加 22.2%；
- world persistence max：`20.524583 → 38.687292 ms`，增加 88.5%；
- interest diff max：`0.071917 → 0.090125 ms`，增加 25.3%；
- avatar submit max：`1.830917 → 6.106083 ms`，增加 233.5%；
- player save jobs high-water：`4 → 6`，增加 50%。

正式比较失败后立即停止；没有重跑，没有修改阈值，也没有覆盖 accepted v5 baseline。

历史证据表明这些失败不构成稳定的 TCP 退化。在 commit `5958c85` 的旧正式 pair 中，上述
五项均未出现 TCP 回退；当前三个 max 失败的 p50/p95/p99 基本一致。当前 jobs high-water 的
TCP 值 6 也与其他五份 Memory 产物及同 commit 的非权威 diagnostic Memory 产物一致，只有
本次正式 Memory 抽样得到 4。

进一步代码追踪确认，`measureMultiplayerServerProbe` 无论报告的顶层 transport 是 memory 还是
tcp，内部始终启动独立 Host，并使用八对 `network.NewMemoryStreamPair`。它的 tick、interest、
outbound、queue high-water 与 peak RSS 因而不是 TCP 数据路径的测量。现有实现还使用独立的
50 ms ticker 发送输入和抽样 `Host.Stats`；该 ticker 与 Host 的 tick/player-poll ticker 没有
固定相位。tick p99 只有 200 个样本，而 player jobs channel 的瞬时深度会随两个保存 worker
的调度在 4 和 6 之间变化。

## 2. 目标与不变约束

本设计的目标是让一次性正式验收比较有明确语义，并减少独立 ticker 相位导致的采样漂移。

以下约束保持不变：

- 正式 8-session server probe 继续运行真实 `Host.Run`；
- 继续使用八对内存流和真实 Handshake/Login；
- 正式测量窗口仍为 10 秒、20 Hz、200 个完整 server ticks；
- report schema 和全部字段保持不变；
- 20% 相对退化比例保持不变；
- v6 全部现有绝对阈值保持不变；
- accepted v5 baseline 在完整的新验收链通过前保持逐字不变；
- 失败后停止，不自动重试，不挑选有利样本。

本设计细化并取代 `2026-08-02-m3c-scenario-upgrade-policy-design.md` 中“同 scenario 对全部
共有字段执行相对比较”的笼统规则。v5→v6 显式迁移规则、全部绝对门禁和失败即停规则不变。

## 3. 三种比较模式

`perfcheck` 根据两份报告已有的 `scenario_version` 和 `transport` 自动选择比较模式，不新增
CLI 参数。

scenario 小于 6 的同版本比较保留现有行为；本设计只细化 v6 的跨 transport parity 与同
transport 回归。

### 3.1 显式 v5→v6 迁移

行为保持不变：仅接受精确的 `--allow-scenario-upgrade 5:6`，验证两份报告 schema、硬件一致性
以及 current v6 的全部绝对门禁，不执行跨 scenario 相对比较。

### 3.2 v6 跨 transport parity

当 baseline/current 都是 v6 且 transport 不同时，先执行全部既有绝对门禁，再对 transport
相关、统计稳定的字段执行 20% 相对比较。

执行相对比较：

- load seconds 与 snapshot seconds；
- still/flying 的 p50、p95、p99、FPS 与 peak RSS；
- world persistence 的 p50、p95、p99；
- protocol encode/decode p99 与 bytes，保留既有 `0.01 ms` noise floor；
- player persistence 的 p50、p95、p99，保留既有 `0.01 ms` noise floor；
- remote state encode/decode、roster apply、interpolation、avatar submit、name-tag submit、
  remote GPU complete 的 p50、p95、p99。

不执行跨 transport 相对比较：

- 所有 latency/phase/persistence 的 raw `max_ms`；
- 独立内存 server probe 的 ticks；
- 独立内存 server probe 的 interest diff、server outbound bytes、三个 queue high-water 和
  multiplayer peak RSS。

这些字段仍完整写入 JSON，并继续接受 schema、样本数、单调性及既有绝对门禁。它们没有被
删除或改写，只是不再被错误解释为 TCP 相对 Memory 的传输回归。

### 3.3 v6 同 transport 回归

当 baseline/current 都是 v6 且 transport 相同时，先执行全部既有绝对门禁，再执行 20%
相对比较。

除跨 transport 列表外，还比较独立 server probe 的：

- tick p50、p95、p99；
- interest diff p50、p95、p99；
- server outbound bytes；
- multiplayer peak RSS。

所有 raw `max_ms` 和三个 queue high-water 均不执行相对比较。raw max 继续保留在报告；已有
绝对 max 门禁继续生效。queue high-water 继续接受既有 `512/16/2` 绝对上限和 cleanup 归零
检查。不会新增、删除或放宽任何绝对阈值。

## 4. Server probe 测量 epoch

`measureMultiplayerServerProbe` 保留真实 Host、真实内存 packet stream、真实 codec 计数与真实
异步保存 worker，只调整测量窗口的同步方式。

### 4.1 登录与 warm-up

1. 启动 Host 及八个客户端，完成真实 Handshake/Login。
2. 等待 `Host.Stats().ActivePlayers == 8`。
3. 在 recorder 未启用状态下等待 20 个完整 tick，约一秒。warm-up tick 和 interest 样本不
   进入报告。
4. warm-up 超时、客户端提前退出或 active player 数不为 8 时立即失败。

### 4.2 Tick 驱动的固定输入

`TickObserver` 的 benchmark wrapper 在 server Step 末尾发布非阻塞 tick 信号。observer 本身
不得等待 benchmark goroutine，也不得调用 `Host.Stats`，避免在 server `stepMu` 持锁期间形成
锁反转。

warm-up 完成后，基准控制器在下一个 tick 边界清空 recorder/high-water 状态并启用测量。
启用后先向八个客户端发送序号 1 的固定输入，供第一个 measured tick 消费。随后每收到一个
完成 tick 信号：

1. 记录该 tick 已由 observer 提交的 duration；
2. 调用 `Host.Stats` 取得 tick 后的瞬时有界状态；如果 server 仍在解锁，调用会自然等待
   `stepMu`，但 observer 不会阻塞；
3. 更新 outbox/jobs/done high-water；
4. 每 20 tick 采集一次 process RSS；
5. 若尚未完成第 200 个 tick，则向八个客户端各发送下一序号的一组固定 `PlayerInput`，供
   下一个 server tick 消费。

不再创建第二个独立 50 ms 输入/抽样 ticker，因此 Host tick、输入序号和 Stats 抽样之间不再
发生任意相位漂移。tick signal channel 必须能容纳完整 warm-up 与 measured epoch；observer
若检测到 signal overflow，只记录 overflow 状态而不阻塞，probe 随后必须失败。

### 4.3 精确窗口与完成条件

正式窗口精确接收 200 个完成 tick 信号，即 20 Hz 下的 10 秒。测量启用期间八个观察者每 tick
各贡献一个 interest 样本，因此最终必须是 1600 个 interest samples。

完成 200 tick 后立即关闭 recorder gate，再执行现有客户端 drain、Host Shutdown、连接关闭、
goroutine 等待和队列归零检查。以下任一情况都使探针失败：

- measured ticks 不等于 200；
- interest samples 不等于 1600；
- outbound bytes 为零；
- queue high-water 超过既有上限；
- RSS 为零或超过既有上限；
- 任一登录、发送、drain、Shutdown 或 cleanup 操作失败；
- cleanup 后 `Host.Stats` 非零。

外层保留有界 timeout 作为故障保护，但 timeout 不是成功窗口的计数依据。

## 5. 其他 recorder

客户端 multiplayer probe 已在主场景 10 秒 warm-up 之后创建，因此 encode/decode、roster、
interpolation 和 render submit 不需要额外预热阶段。`app.ticks` 与 `app.saves` 已在 still 前
reset；该顺序保持不变。GPU completion 仍在主 still/flying 采样之外独立运行 256 次。

本设计不增加重复渲染、不改变 still/flying 时长、不改变相机路径、不改变 mesher 预算，也不
改变 transport 实现。

## 6. 错误处理与产物规则

- 比较器必须先验证 schema、provenance 和绝对门禁，再选择相对比较字段；
- 任一失败返回非零并列出具体指标；
- 未知 transport 组合继续拒绝；
- 正式性能命令使用包含 commit 的全新、碰撞安全路径；
- 旧正式失败 JSON/log 只作为诊断证据，不得提升为 baseline；
- 新 Memory→TCP parity 通过前不得备份或替换 accepted v5 baseline；
- Step 7 current 只在 Step 6 完整通过并接受 v6 baseline 后运行。

## 7. TDD 与变异证明

实现前先写失败测试，固定以下契约：

1. warm-up tick/interest 样本不进入 measured summary；
2. measured window 必须精确得到 200 ticks 和 1600 interest samples；
3. tick 信号驱动输入，删除该同步关系后测试转红；
4. v6 Memory→TCP 中，仅改变 raw max、queue high-water 或独立 server probe 字段不会产生相对
   失败，但违反既有绝对门禁仍失败；
5. v6 Memory→TCP 的 transport 相关 p99 从 1.000 增至 1.201 必须失败，增至 1.200 通过；
6. v6 Memory→Memory 的 server tick/interest p99 从 1.000 增至 1.201 必须失败；
7. v5→v6 显式迁移、缺省跨版本拒绝、硬件拒绝和 schema 拒绝行为保持不变；
8. 删除任一比较 profile 分支或绕过 recorder gate 时，至少一个新增测试转红。

真实 10 秒 probe 集成测试继续验证真实登录、精确样本、outbound、queue/RSS 上限及 cleanup。
测试不得创建前台窗口。

## 8. 实施与验收顺序

1. 对每个将修改的现有 symbol 执行 GitNexus upstream impact；HIGH/CRITICAL 必须先报告并暂停。
2. 按 TDD 完成比较 profile 与测量 epoch，运行 focused、full 和 race 测试。
3. 使用独立子代理完成规格评审与代码质量评审；阻塞问题修复后重新验证。
4. 从 Task 17 Step 5 重新执行格式、全量 test、race、fuzz、vet、archcheck、Linux CGO0 build、
   physics 零分配及状态检查。
5. Step 5 全绿后，使用新路径各执行一次正式 Memory、迁移检查、TCP 和 Memory→TCP parity。
6. Step 6 全绿后才接受 Memory v6 baseline，再各执行一次 Step 7 current 与同 transport 回归。
7. 提交前运行 `gitnexus_detect_changes`，完成最终规格/代码评审和 baseline provenance 检查。

任何正式门禁失败都回到系统化诊断；不得自动重跑、放宽阈值或覆盖 baseline。

## 9. 范围

预计允许修改：

- `cmd/perfcheck/main.go`
- `cmd/perfcheck/main_test.go`
- `cmd/mcgo/multiplayer_benchmark.go`
- `cmd/mcgo/benchmark_v6_test.go`
- 必要时为 recorder gate 增加的最小测试辅助代码
- M3C plan、brief/report、progress 与最终性能基线文档

不修改 report schema、业务 transport、Host/session/player persistence 生产语义、renderer、mesher、
绝对阈值、20% 比例或 accepted baseline，直到正式接受流程允许更新 baseline。
