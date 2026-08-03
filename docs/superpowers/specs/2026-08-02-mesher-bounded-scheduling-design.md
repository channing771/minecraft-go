# Mesher 有界确定性调度设计

## 背景

M3C scenario v6 的正式 Memory 验收在 `flying p99 >= 12 ms` 处停止。后续经批准的系统化诊断确认：飞行阶段 dirty 区段从 0 增长到 91,183，job 队列长期接近 1,024 容量，六个 worker 持续繁忙；与此同时，`Mesher.Schedule` 每帧扫描全部 dirty map，并在应用 `maxJobs` 之前排序完整候选切片。15 秒主线程采样中，`renderFrame` 内 81.7% 的样本位于该扫描/排序路径，最终稳定复现约 21.7–21.9 ms flying p99。

这不是远端玩家渲染回归，也不是 GPU 等待；不得通过修改 12 ms 门禁、benchmark 速度/场景或 v5 基线来规避。

## 目标

把 `Mesher.Schedule` 的主线程候选准备从依赖全部 dirty 数量的无界工作，改为依赖本次可实际投递数量的有界工作，同时保持现有网格 generation、过期结果、panic 重试、关闭行为和全局确定性区段顺序。

## 非目标

- 不改变 benchmark 场景、相机速度、阶段时长、分辨率或性能阈值。
- 不改变 worker 数量、job/result channel 容量或 renderer 上传预算。
- 不改变 `Mesher` 的导出方法签名。
- 不优化 `MeshSection`、`cloneNeighborhood`、GPU 上传或远端玩家渲染。
- 不把本次诊断或验证产物写入正式基线。

## 设计选择

采用索引最小堆维护“当前可以尝试投递”的 dirty 区段。

候选键仍按现有顺序比较：

1. `Dimension`
2. `Pos.X`
3. `Pos.Z`
4. `Pos.Y`

最小堆同时维护 `SectionKey -> heap index` 映射，因此：

- 重复 `MarkDirty` 不会重复插入同一键；generation 仍只存于权威 `dirty` map。
- `ForgetChunk` 可以在 `O(log n)` 内精确移除 ready 键，不留下以后可能错误复活的重复项。
- 每次弹出保持与当前完整排序相同的全局确定性次序。
- heap 内存与逻辑 ready 键数量成正比，不通过无限累积的 stale entry 换取速度。

FIFO 队列虽然更简单，但会把全局坐标顺序改成消息到达顺序；完整 map 扫描加 top-k 选择仍是 `O(all dirty)`。两者均不采用。

## 状态模型

`dirty` map 继续是“是否仍需某一 generation 的网格”的唯一权威状态。新增 ready heap 只表示该键当前既不在 `queued`，也不在 `inFlight`，可以被 `Schedule` 尝试。

同一键在任一时刻最多属于以下调度状态之一：

- ready：存在于索引最小堆；
- queued：job 已进入 channel、尚未被 worker 领取；
- in-flight：worker 已领取并正在处理；
- awaiting-drain：结果已发送，worker cleanup 与 `Drain` 可能短暂交错；
- absent：不 dirty，或已被遗忘。

`enqueueReadyLocked` 是进入 ready 状态的唯一入口。它只在 mesher 未关闭、键仍 dirty、且不在 queued/in-flight 时向 heap 添加键；heap 自身保证幂等。

## 数据流

### MarkDirty

`markDirtyLocked` 保持现有 generation 自增和零值跳过规则。写入新 generation 后调用 `enqueueReadyLocked`。若键仍 queued 或 in-flight，本次不进入 heap；对应旧 job 完成清理时再根据 generation 差异重新进入 ready。

### Schedule

`Schedule(mirror, maxJobs)` 先读取 job channel 当前空位，并计算：

```text
workLimit = min(maxJobs, cap(jobs) - len(jobs))
```

若 `workLimit <= 0`，立即返回，不遍历 dirty、不分配候选切片、不克隆邻域。

随后最多弹出 `workLimit` 个 ready 键。每个键按以下顺序处理：

1. 在锁内弹出最小键并读取当前 generation；已经不 dirty 或状态不再可投递则丢弃本次尝试。
2. 在锁外克隆邻域，避免长时间持有 mesher mutex。
3. 若中心区块已不存在，则删除该 dirty 项；以后重新收到 snapshot 时会由 `MarkDirty` 建立新 generation。
4. 再次加锁校验 mesher 未关闭、generation 未变化、且键未 queued/in-flight。
5. 校验失败时，把仍可投递的当前 generation 幂等放回 ready heap。
6. 校验成功后写入 queued，并通过现有非阻塞 channel send 投递。
7. 若 send 因并发竞争发现 channel 已满，撤销 queued、重新进入 ready，然后立即返回。

因此单次调用不会再扫描或排序全部 dirty 集合，候选准备上界为 `O(workLimit log ready)`。

### Worker 完成、过期与 panic

worker 领取 job 时仍执行 queued -> in-flight 转换。

cleanup 删除匹配的 in-flight generation 后，仅在以下情况重新加入 ready：

- job panic，dirty generation 仍需重试；
- 处理期间发生 `MarkDirty`，当前 dirty generation 与 job generation 不同。

成功生成且 generation 未变化的 job 不在 cleanup 中重新入队；它由 `Drain` 接受后删除 dirty。若 `Drain` 发现邻域印章过期，它沿用现有 `markDirtyLocked` 生成新 generation：无论 `Drain` 和 worker cleanup 谁先获得锁，二者之一都会把新 generation 加入 ready，且 heap 幂等保证只保留一个键。

### ForgetChunk、失败投递与 Close

- `ForgetChunk` 同时删除 dirty、queued、测试故障状态和 ready heap 中的键；已在 channel/in-flight 的旧 job 仍由 generation/存在性校验丢弃。
- `removeQueued` 撤销匹配 queued generation 后，若该键仍可投递则重新加入 ready。
- `Close` 继续先设置 `isClosed` 并关闭通知 channel；`enqueueReadyLocked` 在关闭后拒绝新增候选，worker 退出和等待规则不变。

## 文件边界

- `internal/client/mesher_ready_queue.go`：只负责索引最小堆、键比较以及 ready 的增删弹出，不读取 Mirror、不创建 job。
- `internal/client/mesher.go`：保留 dirty/generation 权威状态、worker 生命周期和实际调度数据流。
- `internal/client/mesher_ready_queue_test.go`：直接验证真实 heap 的确定性顺序、幂等、精确删除和索引维护。
- `internal/client/mesher_backpressure_test.go`：验证满 job 队列时大 backlog 不触发候选扫描/分配，以及 generation/panic/forget 的调度行为。
- 现有 `internal/client/mesher_test.go`：继续作为导出行为的集成回归；不为了新实现重写既有断言。

## TDD 与验证

实施必须遵循 RED -> GREEN -> REFACTOR：

1. 先添加 ready heap 行为测试；旧代码因缺少该组件而失败。
2. 添加满 job 队列的大 backlog 回归；旧 `Schedule` 因完整候选切片分配而失败，新实现必须零候选分配并保持状态不变。
3. 添加确定性分批投递、重复重脏、panic 重试和 forget 不复活测试；复用真实 `Mesher`/`Mirror`，不 mock 被测调度器。
4. 运行既有 mesher 测试及 `-race`，证明原有过期印章、关闭和结果队列行为不变。
5. 增加 90k ready 键的调度微基准，记录 `ns/op`、`B/op`、`allocs/op`；不得用宽松时间断言制造易波动单测。
6. 运行 `go test ./internal/client -count=1`、`go test -race ./internal/client -count=1`、`go test ./... -count=1` 和 `git diff --check`。
7. 完成独立代码评审后，最多运行一次使用独立 `/tmp` 路径的诊断 Memory benchmark。该结果只验证 flying p99 与 backlog 行为，不作为正式 Task 2 报告或基线。

必须做两项 mutation check：

- 删除 job 队列满时的立即返回，满队列 backlog 回归必须 RED。
- 绕过 ready heap、恢复完整 dirty 候选扫描/排序，90k 调度基准和对应分配证据必须明显退化。

## 验收标准

- `Schedule` 不再创建或排序完整 dirty 候选切片。
- job 队列无空位时，`Schedule` 在不克隆邻域、不改变 dirty/queued/in-flight 状态的情况下返回。
- ready 键顺序精确保持 `Dimension/X/Z/Y` 排序。
- 既有 stale generation、邻域印章失效、panic 重试、ForgetChunk 和 Close 测试全部通过且 race-clean。
- 90k backlog 微基准不再随全部 dirty 数量执行线性扫描或大切片分配。
- 一次诊断 Memory benchmark 的 flying p99 必须 `< 12 ms`，否则停止并重新诊断；不得进入正式 Task 2 验收。
- v5 baseline JSON/Markdown 字节与 SHA-256 保持不变。

## 风险与控制

GitNexus 预分析结果：`Schedule` 和 `MarkDirty` 为 MEDIUM，各有 5 个直接测试调用者、1 个 Client 模块且无已索引执行流程；`Mesher`、`Drain`、`markDirtyLocked` 为 LOW。当前没有 HIGH 或 CRITICAL 风险。

主要风险是 queued/in-flight 与 `Drain` cleanup 的交错导致 dirty 键丢失或重复投递。控制方式是保留 dirty generation 为唯一事实来源、所有 ready 转换都在同一 mutex 下完成、heap 添加幂等，并通过 stale/panic/forget/race 测试与 mutation check 验证。
