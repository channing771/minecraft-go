## 1. 工具物品、单格上限与固定配方

- [ ] 1.1 在 `internal/core` 和 `internal/storage` 先写失败测试，覆盖石镐/铁镐稳定 ID、单格上限 1、普通物品上限 64、添加/移动/合成容量、两条固定配方、玩家 schema v3 往返与未知工具 ID 的旧读取拒绝。
- [ ] 1.2 在 `internal/core/item.go`、`inventory.go`、`recipe.go` 追加工具和配方并让全部栏位算法复用统一上限查询；保持单输入配方、玩家 schema v3 和区块 schema v4 不变。
- [ ] 1.3 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/core ./internal/storage -race -count=1'`、`go test ./internal/archcheck -count=1`、`gofmt -l .` 与 `git diff --check`，通过后提交 `feat: 定义采掘工具与固定配方`。

## 2. 协议 v8 固定采掘字段

- [ ] 2.1 在 `internal/network` 先写失败的 registry、codec golden、验证与登录测试：`PlayerInput` 携带持续采掘布尔值，`PlayerState` 携带规范权威进度，协议为 v8，v7 登录拒绝，Play client packet ID `1` 未分配且其他 ID 不变。
- [ ] 2.2 扩展 `internal/network/message.go`、`codec.go`、`packet.go` 和 `registry.go`，并在 `internal/sim`、`internal/server`、`internal/client` 只加入编译所需的固定字段与零状态映射；删除即时 `BreakBlock` codec/registry 入口，不新增独立采掘消息。
- [ ] 2.3 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network ./internal/client ./internal/server -race -count=1 && go test ./internal/network -run "^$" -fuzz FuzzSmallPacketCodec -fuzztime=10s && go test ./internal/network -run "^$" -bench SmallPacketCodec -benchmem -count=3'`，再运行 archcheck、gofmt 与 diff check，通过后提交 `feat: 定义权威采掘协议 v8`。

## 3. 持续输入与即时破坏退役

- [ ] 3.1 在 `internal/client`、`cmd/mcgo` 和 `internal/server` 先写失败测试，覆盖主键按住而非上升沿、容器打开和 predictor neutral 发送 `Mining=false`、固定步输入携带视角与采掘状态、服务端 ingress 保存最后有效意图，以及旧 packet ID `1` 触发协议违规。
- [ ] 3.2 修改 `internal/client/input.go`、`predictor.go`、`cmd/mcgo/main.go`、`app.go` 与 `internal/server/session.go`，让持续输入端到端到达 simulation owner；删除客户端 `breakBlock` 发送路径和服务端 `BreakBlock` 翻译，不改变右键放置/打开熔炉语义。
- [ ] 3.3 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/client ./internal/server ./cmd/mcgo -race -count=1'`，再运行 archcheck、gofmt 与 diff check，通过后提交 `feat: 接通持续采掘输入`。

## 4. 权威采掘进度与固定等级

- [ ] 4.1 在新的 `internal/sim/mining_test.go` 先写表驱动失败测试，覆盖全部方块/手持组合、首 tick、持续递增、松键、目标/方块/工具变化、超距、未就绪、基岩、reset、八名玩家独立状态与权威发布字段。
- [ ] 4.2 新增 `internal/sim/mining.go` 的固定规则与 `playerMiningState`，在移动、掉落物、熔炉、跨容器移动和放置之后按排序 session 推进每人一次六格射线；无效目标正常清零且不逐 tick 拒绝。
- [ ] 4.3 增加 `AllocsPerRun` 或等价微基准，锁定八名持续采掘的零堆分配与每 tick 最多八次射线；不得加入 map、slice 增长、goroutine 或配置层。
- [ ] 4.4 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim -race -count=1 && go test ./internal/sim -run "^$" -bench "Mining" -benchmem -count=3'`，再运行 archcheck、gofmt 与 diff check，通过后提交 `feat: 推进权威计时采掘`。

## 5. 原子破坏、掉落与熔炉内容保全

- [ ] 5.1 迁移 `internal/sim` 现有交互/掉落/熔炉测试为采掘完成语义，并先增加失败场景：错误工具无方块掉落、裸手石头启动、容量不足不破坏、错误工具熔炉只掉内容、空熔炉无掉落、同 tick 两人只提交一次。
- [ ] 5.2 从 `internal/sim/engine.go` 的即时破坏分支抽出唯一按已验证目标完成的原子 helper；harvestable 普通方块预演一个掉落，错误工具普通方块不预演，熔炉按工具等级预演本体与内容或仅内容。
- [ ] 5.3 删除 `CommandBreakBlock` 及只服务即时破坏的代码和测试夹具，保留放置射线与拒绝映射；完成成功或容量失败后清零，容量失败只用最后输入 sequence 拒绝一次。
- [ ] 5.4 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim ./internal/world ./internal/server -race -count=1'`，再运行 archcheck、gofmt 与 diff check，通过后提交 `feat: 原子完成工具采掘`。

## 6. Memory/TCP 发布与生命周期闭环

- [ ] 6.1 在 `internal/server` 先写失败的 Memory/TCP 纵向测试，覆盖按住到完成、权威进度逐 tick 发布、工具切换重置、错误工具无掉落、两玩家竞争、断线/reset 清零、v7 登录拒绝和 Memory/TCP 最终哈希一致。
- [ ] 6.2 让 `internal/server/publication.go` 与相关 session 代码把 sim 固定采掘更新映射到所属玩家的 `PlayerState`，不广播给其他玩家、不增加独立 outbox 消息，并保持慢客户端只关闭自身。
- [ ] 6.3 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server ./internal/network ./internal/client -race -count=1'`，再运行 archcheck、gofmt 与 diff check，通过后提交 `feat: 发布多人权威采掘状态`。

## 7. 五行配方与权威 HUD

- [ ] 7.1 在 `internal/render` 与 `cmd/mcgo` 先写失败测试，覆盖五条固定配方的绘制/命中/可合成状态、两把工具色块、固定最坏缓冲大小、绿色可掉落条、橙色无掉落条、非活动不绘制和 reset 清零。
- [ ] 7.2 修改 `internal/render/hotbar.go` 与 `cmd/mcgo/app.go`，只在现有 HUD pipeline 增加背景/填充两个 quad，并把固定配方数组扩为五项；不新增 shader、纹理、glyph、pipeline 或动态容量。
- [ ] 7.3 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/render ./cmd/mcgo ./internal/client -race -count=1'`，确认自动测试未创建或聚焦窗口，再运行 archcheck、gofmt 与 diff check，通过后提交 `feat: 显示采掘进度与工具配方`。

## 8. scenario v10、兼容文档与比较器

- [ ] 8.1 在 `cmd/mcgo` 与 `cmd/perfcheck` 先写失败测试：报告标记 v10、v9/v10 默认拒绝、仅 `9:10` 可显式迁移、迁移只执行完整性/绝对门禁、v10 同场景与跨 transport 仍执行既有稳定门禁、历史 v6-v9 保持可读。
- [ ] 8.2 把 benchmark producer 和比较器升级到 scenario v10，不改变 2560x1440、still/flying、2048 GPU 样本、RSS、tick、20% 相对阈值和其他绝对门禁；M2 基线文件内容与路径保持不变。
- [ ] 8.3 更新 `README.md`、`docs/notes/lan-server.md` 与 `docs/notes/perf-baseline.md`，使用中文说明按住采掘、两级工具、五条配方、协议 v8、玩家 schema v3、区块 schema v4、备份/回退、scenario v10 与未实现范围。
- [ ] 8.4 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo ./cmd/perfcheck ./internal/network -race -count=1'`，再运行 OpenSpec strict、gofmt 与 diff check，通过后提交 `feat: 升级 benchmark scenario v10`。

## 9. 候选版本完整门禁

- [ ] 9.1 对本 change 的 proposal、八份 delta specs、design、tasks 与实现逐项映射，修正任何不一致；确认没有耐久、木材、多原料、裂纹或共享进度的越界实现。
- [ ] 9.2 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race'`、`go vet ./...`、`go test ./internal/archcheck -count=1`、`gofmt -l .`、`git diff --check` 与 `openspec validate --all --strict --no-interactive`，任何失败只修根因且不得绕过 Hook 或放宽门禁。
- [ ] 9.3 运行协议 fuzz/benchmark、采掘微基准及相关存档 golden/fuzz；核对无前台游戏窗口、无遗留 benchmark 进程、tracked 工作树只含 M4F 预期文件。
- [ ] 9.4 勾选已完成任务并提交冻结候选 `chore: 关闭 M4F 权威计时采掘`；提交后不再修改 producer、场景、阈值或采掘热路径，除非新建修复提交并重新完成本组门禁。

## 10. 一次性 M5 scenario v10 基线

- [ ] 10.1 在冻结候选提交上记录精确 HEAD、M2/M5 既有基线哈希、硬件/系统/Go 身份、供电与负载，确认两个全新临时输出路径不存在且无遗留进程；向用户报告边界并取得 Memory/TCP 各一次、失败即停且不得重跑的明确授权。
- [ ] 10.2 仅通过现有无窗口 benchmark 路径生成一次 M5 Memory v10 报告；用既有 v9 M5 基线和显式 `9:10` 执行完整性/绝对门禁，失败立即停止且不得生成 TCP 或覆盖基线。
- [ ] 10.3 Memory 通过后生成一次同 HEAD 的 M5 TCP v10 报告，并执行 TCP 自校验及 Memory→TCP 同场景比较；失败立即停止且不得重跑或覆盖基线。
- [ ] 10.4 两步都通过后，把 Memory 报告精确字节写入 `docs/notes/perf-baseline-m5.json`，更新两份性能记录的 HEAD、命令、哈希、环境和被替代 v9 身份；校验 M2 文件哈希未变后提交 `chore: 建立 M5 scenario v10 基线`。

## 11. 最终同步与归档

- [ ] 11.1 重新运行全仓 race、vet、archcheck、gofmt、diff check、OpenSpec strict 与适用性能比较，确认全部任务已勾选且 tracked 工作树干净。
- [ ] 11.2 把八份 delta specs 同步到主规格，核对新建 `authoritative-mining` purpose、被移除的 v7 requirement、协议 v8、schema v3/v4、scenario v10 和 M5/M2 基线边界均准确。
- [ ] 11.3 归档 `m4f-authoritative-mining-tools` change，再次运行 `openspec validate --all --strict --no-interactive` 与 `git diff --check`，提交 `chore: 归档 M4F 权威计时采掘`。
