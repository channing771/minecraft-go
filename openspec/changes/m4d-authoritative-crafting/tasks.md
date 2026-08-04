## 1. 固定石砖枚举与纯合成

- [x] 1.1 在 `internal/core` 先写失败测试，覆盖稳定 `StoneBrickID`、`ItemStoneBrick`、recipe ID `1`、4 石头到 4 石砖、未知配方、跨栏位最低索引扣料、扣料后释放输出格、原料不足、产物无容量、非法 Inventory 不改原值和零分配最坏路径。
- [x] 1.2 在 `internal/core` 最小实现一个固定 recipe switch 与 `Inventory.Craft` 值操作；复用 `Slot`、`setSlot` 和 `AddStack`，不新增注册器、接口、配置或第二套可合成判断。
- [x] 1.3 在 `internal/assets`、`internal/render` 增加程序化石砖材质和现有物品色块映射，补充方块/物品放置、挖掘掉落和材质测试；不新增二进制资源或第二个 renderer。
- [x] 1.4 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/core ./internal/assets ./internal/render -race -count=1 && go test ./internal/archcheck -count=1'`，确认 `gofmt -l internal/core internal/assets internal/render` 与 `git diff --check` 无输出；只暂存本组与任务勾选，提交 `feat: 定义固定石砖配方`，然后自动进入第 2 组。

## 2. 区块存档 schema v3

- [x] 2.1 在 `internal/storage` 与 `internal/server` 先写失败测试，覆盖 v2 方块/掉落物无损迁移、v1 链式迁移、石砖方块与掉落物 v3 roundtrip/golden、未来版本拒绝、故障写入保留旧记录，以及正常 active bank 迁移结果传播为 `NeedsRewrite` 并由服务端保存确认。
- [x] 2.2 将区块当前 schema 升为 3，新增无数据变化的 `2→3` migration；把 `migrateChunk` 的 migrated 结果经 `decodedPayload` 和正常 `region.Load` 传播到 `StoredChunk.NeedsRewrite`，复用现有 acquire/dirty/原子保存路径，不扫描未加载区域。
- [x] 2.3 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage ./internal/server -race -count=1 && go test ./internal/archcheck -count=1'`，确认 migration/golden/故障测试、`gofmt -l internal/storage internal/server` 和 `git diff --check` 通过；提交 `feat: 升级区块存档 schema v3`，然后自动进入第 3 组。

## 3. 权威合成协议 v6 纵向闭环

- [ ] 3.1 在 `internal/network` 先写失败的 packet/registry/codec/golden/fuzz seed 测试，覆盖固定 9 字节 `CraftRecipe`、recipe ID 校验、截断/尾随拒绝、稳定 packet ID、协议 v6 登录和 v5 登录拒绝。
- [ ] 3.2 在 `internal/sim`、`internal/server` 先写失败测试，覆盖 Ready/sequence、成功一次 dirty、未知配方/原料不足/无容量原子拒绝、过期命令不重复执行、只向所属玩家确认，以及 8 玩家稳定隔离。
- [ ] 3.3 将 `ProtocolVersion` 升为 6，追加 `CraftRecipe` 请求、`CommandCraftRecipe` 和 endpoint 翻译；在权威 tick 调用 `Inventory.Craft`，失败复用 `RejectInvalidInput`，成功只走一次现有完整 `InventoryState` 私有发布，不新增结果包或 dirty 位。
- [ ] 3.4 增加 Memory/TCP 纵向测试，证明相同初始 Inventory 和合成序列得到相同最终状态、拒绝和持久化结果；执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network ./internal/sim ./internal/server -race -count=1 && go test ./internal/archcheck -count=1'`，确认 `gofmt` 与 `git diff --check` 通过；提交 `feat: 接入权威合成协议 v6`，然后自动进入第 4 组。

## 4. 固定合成入口与应用接线

- [ ] 4.1 在 `internal/render` 与 `cmd/mcgo` 先写 headless 失败测试，覆盖背包打开时固定配方行、enabled/disabled 状态、配方命中边界、有效点击只发一次、不可用不发送、发送后不改镜像、清除来源选择，以及关闭/断线/reset 行为不回退。
- [ ] 4.2 扩展现有 `HotbarRenderer` 的固定 CPU/GPU 容量和共用几何，绘制 `4 石头 → 4 石砖` 与一次合成按钮；增加一个纯配方命中函数，不新增列表、第二套 pipeline 或每帧对象。
- [ ] 4.3 在 `cmd/mcgo` 的背包点击路径用最后确认 Inventory 调用同一 `Craft` 函数判断可用性，成功命中时发送一次 recipe ID `1` 并清除来源选择；保持输入抑制、中性移动、鼠标捕获和服务端 tick 行为不变。
- [ ] 4.4 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/render ./internal/client ./cmd/mcgo -race -count=1 && go test ./internal/archcheck -count=1'`；测试不得启动或聚焦游戏窗口，确认满容量 allocation、buffer 边界、`gofmt` 和 `git diff --check` 通过；提交 `feat: 接入石砖合成界面`，然后自动进入第 5 组。

## 5. 重启闭环、兼容文档与专项门禁

- [ ] 5.1 在 `internal/server` 增加 DiskStore 重启纵向测试：从 v2 区块迁移、采集 4 石头、合成石砖、放置、挖掘、拾取、正常刷新和多身份乱序重连后，完整物品状态、石砖区块与掉落物必须一致；保存失败保持可重试且不部分提交。
- [ ] 5.2 更新 `README.md` 与 `docs/notes/lan-server.md`，说明固定石砖配方、背包操作、协议 v6、玩家 schema v3、区块 schema v3/v1-v2 迁移、备份/回退和未实现范围；文档使用中文并删除“尚无背包界面”的过时描述。
- [ ] 5.3 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "^$" -fuzz FuzzSmallPacketCodec -fuzztime=10s && go test ./internal/storage -run "^$" -fuzz FuzzDecodeChunkPayload -fuzztime=10s'`，确认无 panic、无无界分配和失败语料。
- [ ] 5.4 增加并执行固定工作量微基准：`zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/core ./internal/network ./internal/storage ./internal/sim -run "^$" -bench "Craft|ChunkCodec|SmallPacketCodec" -benchmem -count=3'`；只优化实测根因，不降低既有门禁。
- [ ] 5.5 只在现有 `--benchmark` 路径确认无窗口时生成全新 M5 scenario v7 Memory/TCP 临时报告，并显式使用 `docs/notes/perf-baseline-m5.json` 运行 `cmd/perfcheck --max-regression 0.20`；任一步失败停止，不重跑、不打开前台窗口、不覆盖基线。
- [ ] 5.6 只暂存本组测试、中文文档和任务勾选，确认 `midscene_run/` 仍未暂存；提交 `test: 验证石砖合成重启闭环`，然后自动进入第 6 组。

## 6. 最终门禁与阶段收尾

- [ ] 6.1 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race -count=1 && go vet ./... && go test ./internal/archcheck -count=1 && gofmt -l .'`；`gofmt -l .` 必须无输出，且不得启动或聚焦游戏窗口。
- [ ] 6.2 执行 `openspec validate --all --strict --no-interactive`、`git diff --check`，核对 proposal、4 份 delta specs、design 与实现一致，确认协议 v6、区块 schema v3 和性能基线均未被放宽或静默覆盖。
- [ ] 6.3 只暂存 M4D 实现、测试、中文文档和本文件勾选，排除 `midscene_run/`；提交 `chore: 关闭 M4D 权威合成`，停止实现并等待主规格同步、归档与推送指令。
