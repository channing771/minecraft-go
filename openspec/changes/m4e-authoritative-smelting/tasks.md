## 1. 稳定资源、固定配方与矿石生成

- [ ] 1.1 在 `internal/core` 先写失败测试，覆盖稳定方块 ID `CoalOreID=7`、`IronOreID=8`、`FurnaceID=9`、`IronBlockID=10` 与物品 ID `ItemCoal=5`、`ItemRawIron=6`、`ItemIronIngot=7`、`ItemFurnace=8`、`ItemIronBlock=9` 不漂移，`RegisteredItem` 接受全部新物品，煤炭/粗铁/铁锭不可放置，矿石与熔炉/铁块的掉落映射，以及 8 石头→1 熔炉、9 铁锭→1 铁块的最低索引扣料与产物无容量原子失败。
- [ ] 1.2 在 `internal/core` 最小实现：现有 enum 末尾追加 ID，新增单个 `RegisteredItem` 判断并让 `ItemStack.Valid` 与 `Hotbar.Add` 改用它，`BlockDrop`、`ItemPlacement`、`Recipe` 只追加确切 case；不新增注册表、接口或配置。
- [ ] 1.3 在 `internal/worldgen` 先写失败测试，覆盖同种子同坐标确定性、不同种子样本差异、煤仅 `Y<96`、铁仅 `Y<48`、只替换石头、铁优先、负坐标、`BaseBlockAt` 与整区块逐点一致以及 golden 漂移。
- [ ] 1.4 在 `generator.go` 只增加三维整数哈希与一处 `generatedBlockAt` 包装，`BaseBlockAt` 与 `GenerateChunk` 都调用它；先跑非 golden 测试，再用现有 `-update` 机制重写 golden 并复跑，不手工猜哈希。
- [ ] 1.5 在 `internal/assets` 为四个新方块追加程序化材质层，在 `internal/render` 让 `hotbarItemColor` 与 `itemDropColor` 覆盖全部注册物品（含不可放置物品）；补材质索引、颜色非零与 drop 渲染测试，不新增 shader、pipeline 或二进制资源。
- [ ] 1.6 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/core ./internal/worldgen ./internal/assets ./internal/render -race -count=1 && go test ./internal/archcheck -count=1'`，确认 `gofmt -l internal/core internal/worldgen internal/assets internal/render` 与 `git diff --check` 无输出；只暂存本组与任务勾选，提交 `feat: 定义 M4E 资源与矿石生成`，然后自动进入第 2 组。

## 2. 区块固定熔炉槽与原子批量掉落

- [ ] 2.1 在 `internal/world` 先写失败测试，覆盖 32 槽最低索引分配、第 33 个失败、generation 初次为 1/复用递增/耗尽不复用、活动槽方块索引唯一且对应熔炉方块、停用槽只保留 generation、`Clone` 与 `PayloadBytes` 包含熔炉状态、旧引用在复用后失效；`Chunk.Hash` 继续只表示方块。
- [ ] 2.2 在 `internal/world` 先写失败测试覆盖批量掉落：四堆可完整放入时成功、任一堆放不下时全部掉落物字节不变、同物品同位置先合并、空 stack 忽略、不可放置但已注册的煤炭/粗铁/铁锭可掉落。
- [ ] 2.3 在 `internal/core` 增加 `FurnaceRef`、`FurnacesPerChunk=32` 与统一 UI 槽常量；在 `internal/world` 实现 `FurnaceSlot`、`Chunk.Furnace`/`SetFurnace`/`FurnaceAt`/`PrepareFurnace`/`DeactivateFurnace`，只用固定数组两次扫描，不建立 map 或索引缓存。
- [ ] 2.4 实现在掉落物数组副本上预演的 `PrepareDropBatch`/`CommitDropBatch`（固定 `[4]core.ItemStack`），任何余量都返回失败且不修改区块；保留现有单物品 `PrepareDrop`/`CommitDrop`，其合法性改用 `RegisteredItem`。
- [ ] 2.5 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/world -race -count=1 && go test ./internal/archcheck -count=1'`，确认 `gofmt -l internal/core internal/world` 与 `git diff --check` 无输出；提交 `feat: 增加区块固定熔炉状态`，然后自动进入第 3 组。

## 3. 区块存档 schema v4

- [ ] 3.1 在 `internal/storage` 与 `internal/server` 先写失败测试，覆盖 v4 三格物品/进度/燃烧/generation roundtrip、v3 方块与掉落物无损迁移且 `NeedsRewrite=true`、v1/v2 链式迁移、v4 fixture 不迁移、未来版本拒绝，以及玩家 schema 保持 3 且可持久化煤炭/粗铁/铁锭/熔炉/铁块。
- [ ] 3.2 先写损坏与故障失败测试：活动槽 generation=0、标志非 0/1、重复方块索引、索引越界、对应方块不是熔炉、错误物品或数量、progress>199、burn>1600、停用槽残留字段、截断与尾随字节，以及 DiskStore 部分写入失败后旧记录仍可加载。
- [ ] 3.3 把 `currentChunkSchema` 升为 4，逻辑负载按 sections → 掉落物 → 32 熔炉槽追加定长编码，migration registry 只增加 `3: 空熔炉集合`；encode 与 decode 共用同一套校验，先重建 sections 再验证活动槽与熔炉方块对应关系。
- [ ] 3.4 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage ./internal/server -race -count=1 && go test ./internal/storage -run "^$" -fuzz FuzzDecodeChunkPayload -fuzztime=10s && go test ./internal/storage -run "^$" -bench "Chunk(Encode|Decode)" -benchmem -count=3 && go test ./internal/archcheck -count=1'`，确认无 panic、旧 fixture 无漂移、`gofmt` 与 `git diff --check` 通过；提交 `feat: 升级熔炉区块存档 schema v4`，然后自动进入第 4 组。

## 4. 协议 v7 熔炉消息与客户端只读镜像

- [ ] 4.1 在 `internal/network` 先写失败的 packet/registry/codec/golden/fuzz 测试，锁定客户端 ID `OpenFurnace=8`、`MoveFurnaceStack=9`、`CloseFurnace=10` 与服务端 ID `FurnaceState=13`、`FurnaceClosed=14` 及各自固定 payload 长度，覆盖熔炉引用字段、有限视角、统一索引 `0..38`、合法 stack、进度与燃烧范围、截断、尾随、未知值和 v6 登录拒绝。
- [ ] 4.2 在 `internal/client` 先写失败测试：`FurnaceMirror` 只接受熔炉状态与关闭消息，新 generation 替换旧值，过期状态或过期关闭不影响当前界面，非法状态返回 error，`Reset` 清空，`State` 返回值副本且客户端点击不改镜像。
- [ ] 4.3 把 `ProtocolVersion` 升为 7，在既有 switch 末尾追加消息与 packet case，增加私有 `encodeFurnaceRef`/`decodeFurnaceRef` helper，禁止可变长度集合；`FurnaceMirror` 只持有一个状态值与 bool，不建立 map。
- [ ] 4.4 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network ./internal/client -race -count=1 && go test ./internal/network -run "^$" -fuzz FuzzSmallPacketCodec -fuzztime=10s && go test ./internal/network -run "^$" -bench SmallPacketCodec -benchmem -count=3 && go test ./internal/archcheck -count=1'`，确认 `gofmt` 与 `git diff --check` 通过；提交 `feat: 定义权威熔炉协议 v7`，然后自动进入第 5 组。

## 5. 有界熔炼 tick 与查看生命周期

- [ ] 5.1 在 `internal/sim` 先写失败测试覆盖状态机：有效输入加煤时设 1600 后同 tick 变 1599 且进度为 1；第 200 tick 产出 1 铁锭；一个煤恰好 8 锭；空输入、错误输入、输出满或异类时进度与燃烧都暂停；恢复后从原值继续；多人重叠半径只推进一次；半径 2 边界；区块卸载或无 Ready 玩家时暂停；同区块多炉只升一次 revision。
- [ ] 5.2 先写打开与失效失败测试：Ready、sequence、有限视角、服务端六格射线、目标必须是活动且 generation 匹配的熔炉；一人最多一个、多人可同看；超距、方块改变、generation 变化、区块卸载、维度 reset 时精确一次关闭；断线直接移除查看者。
- [ ] 5.3 把 `dropInterestKeys` 最小重命名为 `activeInterestKeys` 并让 `advanceDrops` 与 `advanceFurnaces` 复用同一结果与既有 seen/scratch 和稳定排序；熔炉只在值变化时 `SetFurnace` 加 `touchChunk`，不新增每 tick map。
- [ ] 5.4 实现查看生命周期：打开命令复用 `LookDirection`、`core.RaycastBlocks` 与 `interactionReach`，session 只保存一个 `core.FurnaceRef` 加 bool；每 tick 在全部命令与推进之后验证查看者，再按 session 顺序输出完整熔炉状态，不在 `server.session` 建第二份真相。
- [ ] 5.5 增加 `BenchmarkAdvanceFurnaces6400`（8 名 Ready 玩家、25 个重叠区块、每区块 32 槽）并用 `testing.AllocsPerRun` 锁定稳定态推进 0 分配；若 `TickResult` 输出导致必要分配，只测不含查看者的推进 helper，不得用无界缓存规避。
- [ ] 5.6 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim -race -count=1 && go test ./internal/sim -run "^$" -bench AdvanceFurnaces6400 -benchmem -count=3 && go test ./internal/archcheck -count=1'`，确认 `gofmt` 与 `git diff --check` 通过；提交 `feat: 实现有界权威熔炼`，然后自动进入第 6 组。

## 6. 跨容器移动、放置与原子破坏

- [ ] 6.1 在 `internal/sim` 先写失败测试覆盖跨容器事务：物品栏→输入只接受粗铁、→燃料只接受煤炭、输出只能作为来源、空目标整堆、同类合并留余量、合法异类交换、非法交换双方不变、物品栏满时取出失败、过期 generation 或 sequence 不变、多玩家同 tick 按 session/sequence 稳定串行且所有查看者看到同一最终值。
- [ ] 6.2 先写放置与破坏失败测试：熔炉物品放置同时启用最低槽并扣 1；第 33 炉原子拒绝且不扣物品、不改方块与 revision；铁块放置不分配熔炉槽；破坏空炉掉本体；满内容炉按本体→输入→燃料→输出稳定预演；掉落容量不足时方块、熔炉、掉落物与 revision 全不变；成功后停用槽保留 generation。
- [ ] 6.3 实现纯 helper `moveFurnaceStack(inventory, furnace, from, to)`，在值副本上按现有 `Inventory.MoveStack` 语义计算并验证最终三格约束，成功才同时写回玩家物品与区块熔炉；只追加一个拒绝理由 `RejectFurnaceCapacity`（sim 稳定值 11、wire ID 12），其余复用 `RejectInvalidInput`/`RejectInvalidSlot`，不创建通用 `Container` 接口。
- [ ] 6.4 把熔炉特例接在共享交互原子点：放置时先 `PrepareFurnace` 再写方块、启用槽并扣物品；破坏时先 `PrepareDropBatch` 再清方块、停用槽并提交批量掉落；全部变化仍走既有 pending chunk change，炉内部变化用 `touchChunk` 不重复提升 revision。
- [ ] 6.5 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim ./internal/world ./internal/network -race -count=1 && go test ./internal/archcheck -count=1'`，确认 `gofmt` 与 `git diff --check` 通过；提交 `feat: 原子操作共享熔炉物品`，然后自动进入第 7 组。

## 7. 服务端翻译、私有发布与 Memory/TCP 闭环

- [ ] 7.1 在 `internal/server` 先写失败测试：三个客户端消息字段无损翻译为 sim 命令；熔炉状态只发给当前查看者；两名查看者收到相同完整状态；未打开界面者不收；关闭通知精确一次；完整物品状态仍只发本人；outbox 满时继续关闭慢 session 且不阻塞其他玩家。
- [ ] 7.2 先写 Memory/TCP 纵向失败测试，使用同一脚本：两玩家登录 → 获取资源 → 放炉 → 同时打开 → 交错移动煤炭与粗铁 → 推进 200 tick → 两端同见 1 铁锭 → 一人取出 → 另一人见输出为空 → 旧引用命令被拒绝；两种 transport 的最终区块、物品状态与拒绝序列必须相同。
- [ ] 7.3 先写 DiskStore 重启失败测试：在进度 137、燃烧 1463 时正常刷新、关闭并重开，确认三格与计时原值恢复且停服墙钟不补算，重新进入活动范围后下一 tick 变 138/1462；注入保存失败时旧完整记录可恢复，重试后才整体更新。
- [ ] 7.4 在现有 switch 与发布顺序中最小接线，`server.session` 不保存熔炉状态或查看者 map；执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -race -count=1 && go test ./internal/network ./internal/sim ./internal/storage -race -count=1 && go test ./internal/archcheck -count=1'`，确认 `gofmt` 与 `git diff --check` 通过；提交 `feat: 接通多人权威熔炉服务端`，然后自动进入第 8 组。

## 8. 熔炉界面接线

- [ ] 8.1 在 `internal/render` 先写 headless 失败测试，覆盖 36 格物品加 3 格熔炉布局、来源 `0..38` 高亮、三种 stack 与数量、进度与燃烧条在 0 与边界值、固定 quad/glyph 容量、命中边界、满布局 allocation 与 buffer 边界，且原有背包合成行保持不变。
- [ ] 8.2 在 `internal/client` 与 `cmd/mcgo` 先写失败测试：本地镜像射线命中熔炉时只发一次打开请求、非熔炉仍发放置请求；收到权威状态后才显示；两次点击只发一次跨容器移动且不改镜像；`E`/`Escape` 立即清界面并发关闭；收到关闭通知、断线或玩家状态 reset 时清界面与来源；打开时抑制移动、视角、挖掘、放置与快捷栏选择；测试只用 fake window/gfx，不调用交互式 `run()`。
- [ ] 8.3 按最坏布局重新计算 `maxHotbarQuads`/`maxHotbarGlyphs`，`layoutInventory` 在 overlay 非 nil 时画三格与两条进度并省略合成行；新增与绘制共用几何的纯命中函数 `render.FurnaceSlotAt`；`FurnaceOverlay` 是 render-local 值，由 app 从已确认镜像转换，renderer 不导入 `network`。
- [ ] 8.4 在现有右键与点击路径分流：用 `core.RaycastBlocks` 加 `client.Mirror.BlockAt` 做本地命中只决定发送打开还是放置，服务端仍重新射线；统一来源索引 `0..38`；显式关闭可本地关界面，但任何物品变化仍等待服务端确认。
- [ ] 8.5 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/render ./internal/client ./cmd/mcgo -race -count=1 && go test ./internal/archcheck -count=1'`，确认无窗口出现、`gofmt` 与 `git diff --check` 通过；提交 `feat: 接入权威熔炉界面`，然后自动进入第 9 组。

## 9. scenario v9 与兼容文档

- [ ] 9.1 在 `cmd/mcgo` 与 `cmd/perfcheck` 先写失败测试：benchmark 报告标记 v9；默认 v8→v9 比较被拒绝；错误的迁移参数被拒绝；显式 `8:9` 执行完整性与绝对门禁并跳过相对回归；v9 缺字段被拒绝；v9 同场景 Memory/TCP 仍执行跨 transport 比较。
- [ ] 9.2 把 benchmark 场景版本升为 9 并让 `cmd/perfcheck` 只接受显式 `8:9` 升级，阈值、稳定指标与 M2 基线路径保持不变。
- [ ] 9.3 更新 `README.md`、`docs/notes/lan-server.md` 与 `docs/notes/perf-baseline.md`，说明矿石分布、熔炉操作与统一 `0..38` 界面、协议 v7、玩家 schema v3、区块 schema v4 与 v1-v3 迁移、备份与回退要求、scenario v9 升级以及未实现范围；文档使用中文。
- [ ] 9.4 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo ./cmd/perfcheck ./internal/client -race -count=1'`，确认 `gofmt` 与 `git diff --check` 通过；提交 `feat: 升级 benchmark scenario v9`，然后自动进入第 10 组。

## 10. 一次性 M5 scenario v9 性能基线

- [ ] 10.1 只在现有 `--benchmark` 无窗口路径上，用冻结的候选提交与两个全新临时路径各生成一次 Memory 与 TCP 的 scenario v9 正式报告；不得重跑失败步骤、不得打开前台窗口。
- [ ] 10.2 先用 Memory 报告通过完整性与绝对门禁，再用同硬件同场景的 TCP 报告相对该 Memory 报告通过跨 transport 比较；任一步失败立即停止本组，保留失败报告仅作诊断证据，不放宽阈值、不覆盖既有基线。
- [ ] 10.3 只有两步都通过时，才把 Memory 报告的精确字节写入 M5 基线文件，并在 `docs/notes/perf-baseline.md` 记录硬件、提交、命令、报告哈希与被替代的 v8 场景；提交 `chore: 建立 M5 scenario v9 基线`，然后自动进入第 11 组。

## 11. 最终门禁与阶段收尾

- [ ] 11.1 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race -count=1 && go vet ./... && go test ./internal/archcheck -count=1 && gofmt -l .'`；`gofmt -l .` 必须无输出，且不得启动或聚焦游戏窗口。
- [ ] 11.2 执行 `openspec validate --all --strict --no-interactive` 与 `git diff --check`，核对 proposal、四份 delta specs、design 与实现一致，确认协议 v7、区块 schema v4、玩家 schema v3 与 scenario v9 均未被放宽或静默覆盖。
- [ ] 11.3 只暂存 M4E 实现、测试、中文文档和本文件勾选，排除 `midscene_run/`；提交 `chore: 关闭 M4E 权威熔炼`，停止实现并等待主规格同步、归档与推送指令。
