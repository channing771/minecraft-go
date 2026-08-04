## Context

动机见 `proposal.md`。当前 `measureGPUCompletion` 在 256 次循环中从标签准备前开始计时，经过命令编码、`Submit`、`Poll(true)` 和 `Release` 后才记录样本；这与既有文档声明的 `Submit + Poll(true)` 边界不一致。`LatencyRecorder` 使用 nearest-rank 分位数，因此 256 样本的 p99 是第三慢样本；保留的同机报告已证明相同探针代码会得到 2.618–4.909ms 的 p99，而 p50、p95 和主场景指标没有对应回退。

M4D 已在 `main` 上完成前四组并保留第五组未提交改动。新的 v8 基线如果直接在当前 `main` 上生成，就包含 M4D 实现，无法作为 M4D 前后比较起点。因此性能修复必须从 M4D 实现前的已提交基点 `0eace21` 隔离建立，再合入当前分支。

首轮 v8 正式链在提交 `4bda1bf309b4dfe3dbbc4d64c58772a5bbf6d48c` 上按约束各执行一次：Memory 自检通过，TCP 报告也生成成功，但跨 transport 门禁因 `remote_gpu_complete` p99 从 `1.338333ms` 升至 `2.549958ms` 而失败。两份报告的 GPU p50 几乎相同，TCP 的 FPS、tick、存档与多数多人指标相同或更好；历史 v7 还显示相同代码的 Memory p99 曾从 `2.618ms` 波动至 `4.909ms`。因此证据排除了 TCP 业务路径和 M4D 回退，指向 GPU 探针前的生命周期边界与相关尾部噪声。

## Goals / Non-Goals

**Goals:**

- 让 GPU 完成指标严格对应已声明的 CPU 提交到 GPU queue 清空时间。
- 用单次报告内 2048 个固定样本提高 p99 估计稳定性，保持全部阈值不变。
- 保留历史 v6/v7 报告读取能力，并用 scenario v8 阻止新旧语义混比。
- 建立不包含 M4D 实现的 M5 v8 基线，再用它验收当前 M4D。

**Non-Goals:**

- 不引入 GPU timestamp query、自动重试、多次运行择优或统计基线数据库。
- 不修改 still/flying workload、帧时长、分辨率、绝对门禁或 20% 回归阈值。
- 不改变交互客户端、游戏协议、存档、渲染输出或服务端运行时。
- 不保存 2048 个原始样本；当前摘要足以执行门禁，原始 trace 只在再次出现不可解释失败时增加。

## Decisions

### 1. 计时只包围 `Submit` 与阻塞 `Poll`

每次循环仍先完成昵称准备、command encoder 创建、角色/昵称 Render 和 `Finish`。命令准备完成后读取开始时间，依次调用 `Submit(command)` 与 `Poll(true)`，立即读取结束时间并记录差值，最后在计时区间外 `Release` command。

`multiplayerClientProbe` 增加一个默认指向 `time.Now` 的内部时钟函数。headless 测试使用现有 fake device 记录 `now → submit → poll → now → release` 顺序，同时验证 2048 次样本；生产循环不增加闭包、接口或额外分配。

否决 GPU timestamp query：它需要能力探测、query set、resolve/readback 和跨后端约定，远超本次修正 CPU 发起到 queue 完成指标的范围。否决只移动报告名称：不能修复错误计时边界。

### 2. 固定采集 2048 个样本并保持 nearest-rank p99

在共用报告类型所在的 `internal/client/perf.go` 新增唯一常量 `ScenarioV8GPUCompletionSamples = 2048`，由 producer recorder 容量、采样循环和两个报告完整性校验共同引用。2048 样本的 p99 约由第 21 慢样本决定，低于 1% 的孤立系统调度尖峰不会由三次事件支配；若慢样本持续达到 1%，p99 仍会如实退化。预计额外耗时约 2–4 秒，相对固定场景总时长可接受。

继续复用 `LatencyRecorder` 和现有 p50/p95/p99/max JSON，不新增窗口中位数、trimmed percentile 或重跑逻辑。否决三窗口 p99 中位数：它引入项目专用聚合规则并弱化单份报告的直观含义。否决只提高相对阈值或改用 p95：两者都会直接放宽门禁。

### 3. scenario v8 隔离新指标语义

`cmd/mcgo` 把 `scenarioVersion` 从 7 升为 8。报告 schema 字段不变；`remote_gpu_complete.samples` 从 256 变为 2048。`cmd/perfcheck` 对 v6/v7 继续要求至少 256 个该指标样本，对 v8 要求至少 2048 个，并继续拒绝不同 scenario 的普通相对比较。既有显式 `6:7` workload migration 不扩展到 `7:8`，因为 v8 会通过独立正式链建立新基线。

否决继续标记 v7：相同字段的计时边界和样本估计已经变化，静默混比会制造错误结论。否决增加第二个 GPU 字段并永久保留旧字段：旧指标边界错误且没有继续维护的价值。

### 4. 在 M4D 前基点建立正式 M5 v8 基线

使用独立 worktree 和 `codex/stabilize-remote-gpu-completion-gate` 分支，从 `0eace21` 创建；把本 change 的已批准规划带入该分支后，按 TDD 实现并先提交生产代码。该提交不含 M4D 枚举、协议、存档或 UI 变更，因此其正式报告是有效的 M4D 前基线。

记录精确 HEAD、电源/硬件/OS/Go、M2 基线哈希、无遗留 benchmark 进程和两个全新路径后，再取得针对该 HEAD 与路径的执行确认。Memory 与 TCP 各运行恰好一次：Memory 先自检完整性/绝对门禁，TCP 再相对 Memory 比较。任一步失败立即停止，不重跑；全部通过后才把 Memory 原始字节写入 M5 当前基线并更新中文 provenance。

基线提交合入当前 `main` 后，M4D 使用同一 v8 生产代码和新 M5 基线各生成一次全新的 Memory/TCP 当前报告并执行 20% 比较。这样基线不含 M4D，而当前报告包含 M4D；本次失败的 v7 JSON 只保留为诊断证据，不提升或改写。

否决直接在当前 `main` 建立基线：那会把 M4D 纳入比较起点。否决从旧 v7 JSON推导 v8：计时语义和样本数不同，无法换算。

### 5. 提交与验证保持两段可回退

第一段提交 OpenSpec；第二段在隔离分支提交 v8 代码与测试。正式链通过后，第三段只提交 M5 v8 JSON/Markdown 与任务勾选。合入当前 `main` 后再更新 M4D 5.5 的场景版本和执行结果，不混入性能修复 change 的基线提交。

单元验证覆盖计时事件顺序、2048 样本、v6/v7 兼容、v8 下限、场景不匹配和阈值不变；最终运行 `go test ./... -race -count=1`、`go vet ./...`、架构检查、`gofmt -l .`、OpenSpec strict 和 `git diff --check`。所有 benchmark 保持 headless。

### 6. GPU 探针使用显式 trusted observer 收尾屏障

首轮实现调用 `app.closeClientSession(nil)` 后立即进入 GPU 探针。该调用只关闭客户端 receiver；内置服务端继续运行，trusted observer 的 server endpoint 没有 reader，只能在 writer 的后续发送失败后通过新 goroutine 异步卸载，并且卸载还要取得 `stepMu`。Memory 共享关闭状态会更快暴露失败，TCP 对端关闭传播则依赖内核 I/O 时序；两者都不能作为稳定的测量屏障。

在 `internal/server.Server` 增加幂等的 `CloseTrustedObserver`。它取得 `stepMu`，若 observer 存在则直接调用既有 `detachTrustedObserverLocked`；该路径同步从 registry/engine 移除 observer、取消 session 并关闭 server endpoint，observer 已不存在时直接返回。benchmark 在 GPU 探针前先调用该方法，再关闭客户端 receiver；因此 Memory/TCP 都由同一个服务端所有权边界完成收尾，不依赖 writer 失败、休眠或超时轮询。

单元测试锁定同步卸载、endpoint 关闭和重复调用安全；headless benchmark 测试锁定 observer 收尾发生在首个 GPU 时钟读取前。保留 2048 样本、`Submit + Poll(true)` 计时范围、20% 阈值和所有绝对门禁，不保存原始样本。

v8 尚未合入 `main`，也没有可接受的 v8 基线，因此该修复仍属于 scenario v8 的建立过程，无需再升 v9。首轮 v8 报告及 SHA-256 只作为失败证据；修复提交后必须记录新的精确 HEAD、全新 Memory/TCP 路径并重新取得一次性执行授权。

## Risks / Trade-offs

- [2048 样本仍包含 OS、Go runtime 和 Metal 驱动调度] → 指标本来就是 CPU 发起到 queue 完成，不声称纯 GPU timestamp；扩大样本只减少少数孤立事件对 p99 的支配，不隐藏持续退化。
- [额外样本增加正式链时间] → 只增加数秒离线测量，不进入交互热路径；不再增加窗口或重复运行。
- [从旧基点分支会产生一次非线性合入] → 基线必须排除 M4D；分支只修改性能代码、基线和本 change，合入前严格检查与 M4D 工作区的重叠文件。
- [正式 v8 链仍可能失败] → 保留输出和证据后停止，不修改阈值、样本数或基线；形成新假设后另行更新本 change。
- [显式卸载与异步 writer 失败竞态] → `CloseTrustedObserver` 在 `stepMu` 下幂等操作当前 generation；延迟到达的旧 detach 通过既有 generation 检查成为 no-op。
- [当前 M5 文件内容从 v7 更新为 v8] → 中文 provenance 明确记录被替代场景和提交；Git 历史保留 v7 精确字节，M2 文件完全不动。

## Migration Plan

1. 提交并严格校验本 change 的规划；保留 M4D 未提交第五组改动和 `midscene_run/`。
2. 从 `0eace21` 创建隔离分支，带入规划，按 TDD 实现 v8 并提交；确认没有 M4D 生产代码。
3. 保留首轮 v8 Memory/TCP 报告、哈希和失败输出，不重跑、不提升；提交同步 observer 收尾屏障及测试。
4. 重新完成全量自动验证，记录新的精确 HEAD 和全新临时路径，取得执行确认后运行一条新的 Memory/TCP 各一次正式链。
5. 仅在全部通过后更新 M5 当前基线与中文 provenance，验证 M2 哈希未变并提交。
6. 把性能分支合入当前 `main`，用 M5 v8 基线执行一次 M4D Memory/TCP 当前报告；通过后恢复 M4D 第五组提交与最终收尾。
7. 回退时恢复 v7 M5 基线提交并撤销 v8 生产代码；不同 scenario 报告始终拒绝相对比较，不需要数据迁移。
