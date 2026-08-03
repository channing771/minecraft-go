## 1. 固定 36 格物品模型

- [x] 1.1 在 `internal/core` 先写失败测试，覆盖 27 格背包、36 格统一索引、完整状态校验、整堆拾取的四阶段优先级与余量，以及空目标移动、同类合并、异类交换和所有非法移动不改原值。
- [x] 1.2 最小实现 `Inventory`、`BackpackSlots`、`InventorySlots`、`Valid`、`AddStack` 和 `MoveStack`；复用 `Hotbar`/`ItemStack`，不新增容器接口、策略对象或可变集合。
- [x] 1.3 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/core -race -count=1 && go test ./internal/archcheck -count=1'`，并确认 `git diff --check` 通过。
- [x] 1.4 只暂存本组代码、测试和本文件勾选，不包含 `midscene_run/` 或其他改动；提交 `feat: 定义固定权威背包`。提交成功后自动进入第 2 组。

## 2. 玩家状态与存档 schema v3

- [x] 2.1 在 `internal/storage`、`internal/sim`、`internal/server` 先写失败测试，覆盖 v3 完整状态 roundtrip/golden、v2 保留快捷栏并迁移空背包、v1 链式迁移、非法背包/未来版本拒绝、故障写入保留旧记录和多身份隔离。
- [x] 2.2 将玩家 DTO、Save/StoredPlayer、sim player/snapshot 和持久化装配统一改为 `core.Inventory`；schema v3 在 v2 payload 后追加固定 81 字节背包，选择与放置继续只访问其中 Hotbar。
- [x] 2.3 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage ./internal/sim ./internal/server -race -count=1 && go test ./internal/archcheck -count=1'`，确认 migration/golden/故障测试与 `git diff --check` 通过。
- [x] 2.4 只暂存本组代码、测试和本文件勾选；提交 `feat: 持久化玩家背包`。提交成功后自动进入第 3 组。

## 3. 完整状态协议 v5

- [ ] 3.1 在 `internal/network`、`internal/client`、`internal/server` 先写失败的 packet/registry/codec/golden/fuzz seed 测试，覆盖固定 109 字节 `InventoryState`、10 字节 `MoveInventoryStack`、精确长度与索引校验、v4 登录拒绝、首次状态顺序和只向所属玩家发布。
- [ ] 3.2 将 `ProtocolVersion` 升为 5，以 packet ID 10 的 `InventoryState` 替换 `HotbarState`，追加移动请求；把客户端镜像替换为 `InventoryMirror`，服务端 dirty 发布完整状态，不保留双状态或降级 codec。
- [ ] 3.3 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network ./internal/client ./internal/server -race -count=1 && go test ./internal/archcheck -count=1'`，确认 Memory/TCP 共用校验、v5 golden 和 `git diff --check` 通过。
- [ ] 3.4 只暂存本组代码、测试和本文件勾选；提交 `feat: 升级背包协议 v5`。提交成功后自动进入第 4 组。

## 4. 权威移动与背包拾取

- [ ] 4.1 在 `internal/sim`、`internal/server` 先写失败测试，覆盖移动命令 sequence、空目标/部分合并/交换、同格/越界/空来源拒绝、一次 dirty 发布，以及掉落物先快捷栏后背包、部分拾取、全满保留和多人稳定竞争。
- [ ] 4.2 接入 `CommandMoveInventoryStack` 和 endpoint 翻译；在单写者 tick 原子替换完整 Inventory，掉落物一次调用 `AddStack` 并同步提交余量，复用现有 `RejectInvalidInput`/`RejectInvalidSlot`。
- [ ] 4.3 增加 Memory/TCP 纵向测试，证明相同移动与拾取序列得到相同物品状态、掉落物和拒绝结果；执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim ./internal/server ./internal/network -race -count=1 && go test ./internal/archcheck -count=1'`，并确认 `git diff --check` 通过。
- [ ] 4.4 只暂存本组代码、测试和本文件勾选；提交 `feat: 权威移动并拾取背包物品`。提交成功后自动进入第 5 组。

## 5. 固定容量背包布局与渲染

- [ ] 5.1 在 `internal/render` 先写 headless 失败测试，覆盖关闭时 9 格 HUD、打开时 3×9 背包加 1×9 快捷栏、统一索引、来源高亮、边界/界外命中、最多两位数量和满 36 格固定 allocation/GPU 容量。
- [ ] 5.2 扩展现有 `HotbarRenderer` 与同一 pipeline/buffer，增加完整 Inventory 布局和共用几何的 `InventorySlotAt`；不新增第二个 renderer、贴图、每栏对象或二进制资源。
- [ ] 5.3 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/render -race -count=1 && go test ./internal/archcheck -count=1'`，确认布局/命中一致、稳定分配和 `git diff --check` 通过。
- [ ] 5.4 只暂存本组代码、测试和本文件勾选；提交 `feat: 绘制固定背包界面`。提交成功后自动进入第 6 组。

## 6. E 键输入与应用接线

- [ ] 6.1 在 `internal/client`、`cmd/mcgo` 先写 headless 失败测试，覆盖 `E` 上升沿、打开释放鼠标、两次有效点击只发一次请求、界外点击无效、未确认不预测、`E`/`Escape` 关闭清选并重新捕获、断线/维度 reset，以及打开期间抑制所有游戏操作。
- [ ] 6.2 增加 `KeyE` 映射和应用的 open/source 两个字段；用 `render.InventorySlotAt` 路由点击，打开时立即并持续发送中性移动输入，不发送视角、挖掘、放置或数字键选择，关闭后恢复原交互。
- [ ] 6.3 把 InventoryMirror 同时接入快捷栏 HUD 和背包 overlay；执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/client ./internal/render ./cmd/mcgo -race -count=1 && go test ./internal/archcheck -count=1'`，测试不得启动或聚焦游戏窗口，并确认 `git diff --check` 通过。
- [ ] 6.4 只暂存本组代码、测试和本文件勾选；提交 `feat: 接入背包输入交互`。提交成功后自动进入第 7 组。

## 7. 重启闭环、文档与最终门禁

- [ ] 7.1 在 `internal/server` 增加 DiskStore 重启纵向测试：v2 玩家迁移、掉落物跨快捷栏/背包拾取、跨区移动/交换、正常刷新和多身份乱序重连必须恢复相同完整物品状态，保存失败保持可重试。
- [ ] 7.2 在 `internal/core`、`internal/network`、`internal/storage`、`internal/sim`、`internal/client`、`internal/render` 增加 36 格 Add/Move、完整状态 codec、玩家 v3 codec、8 玩家拾取/发布、满界面微基准或 allocation 门禁；不得放宽既有阈值。
- [ ] 7.3 更新 `README.md` 与 `docs/notes/lan-server.md`：说明 `E` 背包操作、整堆规则、拾取优先级、协议 v5、玩家 schema v3/v2 迁移、备份/回退、可信 LAN 边界和未实现范围；文档使用中文。
- [ ] 7.4 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "^$" -fuzz FuzzSmallPacketCodec -fuzztime=10s && go test ./internal/storage -run "^$" -fuzz FuzzDecodePlayer -fuzztime=10s'`，确认无 panic、无无界分配和失败语料。
- [ ] 7.5 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/core ./internal/network ./internal/storage ./internal/sim ./internal/server ./internal/client ./internal/render -run "^$" -bench "Inventory|PlayerCodec|SmallPacketCodec|Hotbar" -benchmem -count=3'`，保存结果并只优化实测根因，不降低门禁。
- [ ] 7.6 仅在能保证不聚焦窗口时运行固定 offscreen benchmark 并用 `cmd/perfcheck` 对已接受基线做同场景 20% 比较；否则明确记录跳过，不得改用前台测试或覆盖基线。
- [ ] 7.7 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race && go vet ./... && go test ./internal/archcheck -count=1 && gofmt -l .'`、`openspec validate --all --strict --no-interactive` 和 `git diff --check`；`gofmt -l .` 必须无输出，且 `midscene_run/` 保持未暂存。
- [ ] 7.8 只暂存本组实现、测试、中文文档和本文件勾选；提交 `chore: 关闭 M4C 权威背包`。提交成功后停止实现，等待同步规格、归档和推送指令。
