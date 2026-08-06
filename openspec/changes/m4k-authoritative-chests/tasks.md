## 1. 稳定资源与箱子配方

- [x] 1.1 在 `internal/core` 先写失败测试，覆盖稳定 `ChestID` 与 `ItemChest` 追加在既有编号末尾且不漂移、`RegisteredItem` 接受箱子物品、箱子可放置并掉落自身，以及 8 石头合成 1 箱子的新 recipe ID、最低索引扣料与产物无容量原子失败。
- [x] 1.2 在 `internal/core` 最小实现：现有 enum 末尾追加 ID，`BlockDrop`、`ItemPlacement`、`Recipe` 只追加确切 case；不新增注册表、接口或配置。
- [x] 1.3 在 `internal/assets` 追加箱子的程序化材质层，在 `internal/render` 让 `hotbarItemColor` 与 `itemDropColor` 覆盖箱子物品；补材质索引唯一、颜色非零与 drop 渲染测试，不新增 shader、pipeline 或二进制资源。
- [x] 1.4 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/core ./internal/assets ./internal/render -race -count=1 && go test ./internal/archcheck -count=1'`，确认 `gofmt -l internal/core internal/assets internal/render` 与 `git diff --check` 无输出；只暂存本组与任务勾选，提交 `feat: 定义箱子资源与配方`，然后自动进入第 2 组。

## 2. 区块箱子槽与负载实测

- [x] 2.1 在 `internal/core` 增加 `ChestsPerChunk = 16`、`ChestSlots = 27` 与统一栏位常量；在 `internal/world` 先写失败测试，覆盖 16 槽最低索引分配、第 17 个失败、generation 初次为 1/复用递增/耗尽不复用、活动槽方块索引唯一且对应箱子方块、停用槽只保留 generation、27 格全空初始化、`Clone` 与 `PayloadBytes` 包含箱子状态、`Chunk.Hash` 仍只表示方块。
- [x] 2.2 在 `internal/world` 实现 `ChestSlot` 与 `Chunk.Chest`/`SetChest`/`ChestAt`/`PrepareChest`/`CommitChest`/`DeactivateChest`，只用固定数组两次扫描，不建立 map 或索引缓存；同步更新 `internal/sim` 与 `internal/server` 中基于 `PayloadBytes` 的固定预算常量。
- [x] 2.3 把 `PrepareDropBatch` 从固定 `[4]core.ItemStack` 泛化为接受调用方自有固定数组的切片，上限为 `1 + core.ChestSlots`，预演仍在掉落物数组副本上完成且失败不修改区块；补最坏 28 堆成功与任一堆放不下时字节不变的测试。
- [x] 2.4 **负载门禁**：执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/world ./internal/sim ./internal/server -race -count=1'`，随后只在现有 `--benchmark` 无窗口路径生成一次临时 Memory 报告，与既有 M5 基线的绝对门禁比较 RSS 与 p99。若任一绝对门禁被突破，停止实现、把 `ChestsPerChunk` 降到 8 并同步更新 proposal、design 与本文件后重测；不得放宽门禁或覆盖基线。
- [x] 2.5 记录实测 RSS 数值与结论到 `docs/notes/perf-baseline.md`，提交 `feat: 增加区块固定箱子状态`，然后自动进入第 3 组。

## 3. 区块存档 schema v6

- [ ] 3.1 在 `internal/storage` 先写失败测试，覆盖 v6 的 27 格物品与耐久 roundtrip、v5 方块/掉落物/熔炉无损迁移且 `NeedsRewrite=true`、v1–v4 链式迁移、v6 fixture 不迁移、未来版本拒绝。
- [ ] 3.2 先写损坏与故障失败测试：活动槽 generation=0、标志非 0/1、重复方块索引、索引越界、对应方块不是箱子、未知物品、数量越界、停用槽残留字段、截断与尾随字节，以及 DiskStore 部分写入失败后旧记录仍可加载。
- [ ] 3.3 把 `currentChunkSchema` 升为 6，逻辑负载按 sections → 掉落物 → 熔炉 → 16 箱子槽追加定长编码，migration registry 只增加 `5: 空箱子集合`；encode 与 decode 共用同一套校验，先重建 sections 再验证活动槽与箱子方块对应关系。
- [ ] 3.4 冻结 v5 fixture 为迁移输入并新增 v6 golden；把两者都加入 `FuzzDecodeChunkPayload` 语料。
- [ ] 3.5 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage ./internal/server -race -count=1 && go test ./internal/storage -run "^$" -fuzz FuzzDecodeChunkPayload -fuzztime=10s && go test ./internal/storage -run "^$" -bench "Chunk(Encode|Decode)" -benchmem -count=3 && go test ./internal/archcheck -count=1'`，确认无 panic、旧 fixture 无漂移、`gofmt` 与 `git diff --check` 通过；提交 `feat: 升级箱子区块存档 schema v6`，然后自动进入第 4 组。

## 4. 容器引用收敛与协议 v12

- [ ] 4.1 在 `internal/core` 先写失败测试，锁定 `ContainerRef` 的字段与 `ContainerKind` 稳定值，且 `ContainerKind` 的零值必须表示熔炉，使既有存档与测试构造的引用不被误判为箱子；确认 `FurnaceRef` 是 `ContainerRef` 的类型别名且既有调用点不需要改动。
- [ ] 4.2 在 `internal/network` 先写失败的 packet/registry/codec/golden/fuzz 测试，覆盖 `OpenContainer`/`MoveContainerStack`/`CloseContainer`/`ContainerClosed` 保持原有 packet ID `8`/`9`/`10`/`14`、`FurnaceState` 保持 `13`、新增 `ChestState` 使用 `15`，`ChestState` 固定 153 字节，以及未知容器类型、越界统一索引、截断、尾随字节与 v11 登录拒绝。
- [ ] 4.3 把 `ProtocolVersion` 升为 12，按容器中性命名重命名既有消息类型并新增 `ChestState`；容器引用编解码只保留一份 helper，禁止可变长度集合。
- [ ] 4.4 在 `internal/client` 扩展只读镜像：容器镜像只接受服务端确认的完整状态与关闭通知，新引用替换旧界面，过期状态或过期关闭不影响当前界面，非法状态返回 error，`Reset` 清空，`State` 返回值副本。
- [ ] 4.5 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/core ./internal/network ./internal/client -race -count=1 && go test ./internal/network -run "^$" -fuzz FuzzSmallPacketCodec -fuzztime=10s && go test ./internal/network -run "^$" -bench SmallPacketCodec -benchmem -count=3 && go test ./internal/archcheck -count=1'`，确认 `gofmt` 与 `git diff --check` 通过；提交 `feat: 收敛容器协议并升级 v12`，然后自动进入第 5 组。

## 5. 共用查看生命周期与跨容器事务

- [ ] 5.1 在 `internal/sim` 先写失败测试，覆盖打开命令的 Ready、sequence、有限视角与服务端六格射线；命中箱子时建立箱子查看关系并结束原有熔炉查看关系；一人最多一个、多人可同看；超距、方块改变、generation 变化、区块卸载、维度 reset 时精确一次关闭；断线直接移除查看者。
- [ ] 5.2 先写跨容器事务失败测试：物品栏与箱子之间的空目标整堆、同类合并留余量、异类交换、箱子接受任何已注册物品与带耐久工具、物品栏满时取出失败、越界统一索引、过期 generation 或 sequence 均整条拒绝且两侧不变、多玩家同 tick 按 session/sequence 稳定串行。
- [ ] 5.3 把 `sessionState` 的熔炉查看字段替换为容器中性字段，并把 `moveFurnaceStack` 泛化为在玩家物品与一个容器视图的值副本上计算：熔炉保留三格物品约束，箱子只校验 `ItemStack.Valid`；成功才同时写回，箱子不引入新的拒绝理由。
- [ ] 5.4 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim -race -count=1 && go test ./internal/archcheck -count=1'`，确认既有熔炉测试全部保持通过、`gofmt` 与 `git diff --check` 无输出；提交 `feat: 共用容器查看与跨容器事务`，然后自动进入第 6 组。

## 6. 放置与原子破坏

- [ ] 6.1 在 `internal/sim` 先写失败测试：箱子物品放置同时启用最低槽并扣 1；第 17 个箱子原子拒绝且不扣物品、不改方块与 revision；破坏空箱子只掉本体；满 27 格箱子按本体→`36..62` 稳定预演；掉落容量不足时方块、箱子、掉落物与 revision 全不变；成功后停用槽保留 generation 且内容清零。
- [ ] 6.2 把箱子接在既有共享交互原子点：放置时先预留槽再写方块、启用槽并扣物品；破坏时先预演批量掉落再清方块、停用槽并提交；全部变化仍走既有 pending chunk change，不重复提升 revision。
- [ ] 6.3 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim ./internal/world -race -count=1 && go test ./internal/archcheck -count=1'`，确认 `gofmt` 与 `git diff --check` 通过；提交 `feat: 原子放置与破坏箱子`，然后自动进入第 7 组。

## 7. 服务端接线与 Memory/TCP 闭环

- [ ] 7.1 在 `internal/server` 先写失败测试：四个容器消息字段无损翻译为 sim 命令；箱子状态只发给当前查看者；两名查看者收到相同完整状态；未打开界面者不收；关闭通知精确一次；完整物品状态仍只发本人；outbox 满时继续关闭慢 session 且不阻塞其他玩家。
- [ ] 7.2 先写 Memory/TCP 纵向失败测试，使用同一脚本：两玩家登录 → 放置箱子 → 同时打开 → 交错存取 → 两端同见最终内容 → 一人破坏箱子 → 另一人收到关闭通知 → 旧引用命令被拒绝；两种 transport 的最终区块、物品状态与拒绝序列必须相同。
- [ ] 7.3 先写 DiskStore 重启失败测试：存入含耐久工具的箱子后正常刷新、关闭并重开，确认 27 格物品、数量与耐久原值恢复；注入保存失败时旧完整记录可恢复，重试后才整体更新。
- [ ] 7.4 在现有 switch 与发布顺序中最小接线，`server.session` 不保存容器状态或查看者 map；执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -race -count=1 && go test ./internal/network ./internal/sim ./internal/storage -race -count=1 && go test ./internal/archcheck -count=1'`，确认 `gofmt` 与 `git diff --check` 通过；提交 `feat: 接通多人权威箱子服务端`，然后自动进入第 8 组。

## 8. 箱子界面接线

- [ ] 8.1 在 `internal/render` 先写 headless 失败测试，覆盖 36 格物品加 27 格箱子的布局、来源 `0..62` 高亮、格内物品与数量、固定 quad/glyph 容量、命中边界、满布局 allocation 与 buffer 边界，且原有背包配方行与熔炉视图保持不变。
- [ ] 8.2 在 `internal/client` 与 `cmd/mcgo` 先写失败测试：本地镜像射线命中箱子时只发一次打开请求、非容器仍发放置请求；收到权威状态后才显示；两次点击只发一次跨容器移动且不改镜像；`E`/`Escape` 立即清界面并发关闭；收到关闭通知、断线或玩家状态 reset 时清界面与来源；打开时抑制移动、视角、采掘、放置与快捷栏选择并发送非采掘状态；测试只用 fake window/gfx，不调用交互式 `run()`。
- [ ] 8.3 按最坏布局重新计算固定 quad/glyph 容量，让同一 `layoutInventory` 在箱子叠加值非 nil 时画 27 格而不是配方行；新增与绘制共用几何的纯命中函数覆盖 `0..62`；箱子叠加值是 render-local 值，由 app 从已确认镜像转换，renderer 不导入 `network`。
- [ ] 8.4 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/render ./internal/client ./cmd/mcgo -race -count=1 && go test ./internal/archcheck -count=1'`，确认无窗口出现、`gofmt` 与 `git diff --check` 通过；提交 `feat: 接入权威箱子界面`，然后自动进入第 9 组。

## 9. 文档与最终门禁

- [ ] 9.1 更新 `README.md` 与 `docs/notes/lan-server.md`，说明箱子合成与放置、统一 `0..62` 界面、同时只能查看一个容器、协议 v12、玩家 schema v4 不变、区块 schema v6 与 v1–v5 迁移、备份与回退要求以及未实现范围；文档使用中文并保持与实现一致。
- [ ] 9.2 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && gofmt -l . && go test ./... -race -count=1 && go vet ./... && go test ./internal/archcheck -count=1 && CGO_ENABLED=0 GOOS=linux go build ./cmd/mcgod'`；`gofmt -l .` 必须无输出，且不得启动或聚焦游戏窗口。
- [ ] 9.3 执行 `openspec validate --all --strict --no-interactive` 与 `git diff --check`，核对 proposal、三份 delta specs、design 与实现一致，确认协议 v12、区块 schema v6、玩家 schema v4、scenario v12 与两份既有基线均未被放宽或静默覆盖。
- [ ] 9.4 只暂存 M4K 实现、测试、中文文档和本文件勾选，排除 `midscene_run/`；提交 `chore: 关闭 M4K 权威箱子`，停止实现并等待主规格同步、归档与推送指令。
