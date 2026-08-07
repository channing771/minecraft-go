## 1. 生命值字段与玩家存档 schema v5

- [x] 1.1 在 `internal/core` 先写失败测试，覆盖 `MaxHealth = 20` 稳定不漂移，以及生命值有效性判断接受 `0..20`、拒绝 `21` 与更大值。
- [x] 1.2 在 `internal/core` 增加 `MaxHealth` 常量与 `ValidHealth(uint8) bool`，只做范围判断，不新增结构体、接口或注册表。
- [x] 1.3 在 `internal/sim` 的玩家状态增加 `Health uint8` 字段，并让 `PlayerRestore`/`PlayerSnapshot` 携带它；新玩家与缺失值一律初始化为 `core.MaxHealth`。先写失败测试覆盖新玩家满血、快照往返与 `PlayerHash` 覆盖生命值。
- [x] 1.4 在 `internal/storage` 先写失败测试，覆盖 v5 生命值 roundtrip、v4 存档迁移为满血且 `NeedsRewrite=true`、v1–v3 沿链迁移、v5 fixture 不迁移、未来版本拒绝、越界生命值整体拒绝。参照 `player_codec.go` 既有 v4 编解码与 `player_migration.go` 的迁移链写法。
- [x] 1.5 把 `currentPlayerSchema` 升为 5，在 v4 负载末尾追加 1 字节生命值，migration registry 只增加 `4: 满血`；冻结 `player-v4.bin` 为迁移输入（删除其自动重生成的 golden 测试、保留 .bin 供迁移测试读取），新增 `player-v5.bin` golden，并把两者加入 `FuzzDecodePlayer` 语料。参照 M4K 冻结 `chunk-v5.bin` 的做法。
- [x] 1.6 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/core ./internal/storage ./internal/sim -race -count=1 && go test ./internal/storage -run "^$" -fuzz FuzzDecodePlayer -fuzztime=10s && go test ./internal/archcheck -count=1'`，确认 `gofmt -l internal/core internal/storage internal/sim` 与 `git diff --check` 无输出；只暂存本组与任务勾选，提交 `feat: 增加权威生命值与玩家 schema v5`，然后自动进入第 2 组。

## 2. 摔落伤害

- [ ] 2.1 在 `internal/sim` 先写失败测试覆盖伤害曲线边界：3 格无伤、4 格扣 1、23 格从满血致死（伤害恰为 20）、跳跃无伤、落地后继续停留不重复扣血、传送与重生重置峰值。测试用现有 `readyFlatPlayer` 系列 helper 构造，直接设置玩家位置与速度来制造下落。
- [ ] 2.2 在 `internal/sim` 的玩家状态增加不持久化的瞬态字段记录离地后的峰值 Y；在既有物理推进之后、按"上一 tick 不在地面且这一 tick 在地面"的边沿结算一次伤害，公式为 `floor(峰值Y − 落地Y) − 3`，负值取 0。落地、传送、重生、维度 reset 都把峰值重置为当前 Y。不新增分配。
- [ ] 2.3 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim -race -count=1 && go test ./internal/archcheck -count=1'`，确认 `gofmt` 与 `git diff --check` 通过；提交 `feat: 实现权威摔落伤害`，然后自动进入第 3 组。

## 3. 自动回复

- [ ] 3.1 在 `internal/sim` 先写失败测试：受伤后第 99 tick 不回复、第 100 tick 起每 40 tick 回 1 点、回复中途再次受伤则计时清零且回复中断、满血时不计时不回复也不产生额外发布。
- [ ] 3.2 在 `internal/sim` 增加不持久化的受伤计时字段，按 `RegenDelayTicks = 100` 与 `RegenIntervalTicks = 40` 推进回复；任何伤害清零计时。与熔炼推进同形，每玩家每 tick 固定整数运算，不新增分配。
- [ ] 3.3 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim -race -count=1 && go test ./internal/archcheck -count=1'`，确认 `gofmt` 与 `git diff --check` 通过；提交 `feat: 实现生命值自动回复`，然后自动进入第 4 组。

## 4. 协议 v13 与客户端镜像

- [ ] 4.1 在 `internal/network` 先写失败的 packet/codec/golden/fuzz 测试：`PlayerState` 增加 `Health uint8` 后 payload 固定增加 1 字节、生命值 `21` 及以上被拒绝、截断与尾随字节拒绝、v12 登录拒绝、既有容器与采掘消息的 packet ID 与布局不变。
- [ ] 4.2 把 `ProtocolVersion` 升为 13，在 `PlayerState` 消息与 codec 中追加生命值字段与范围校验；更新受影响的 golden hex 与长度断言，**只做机械跟随，不得放宽任何既有断言**。
- [ ] 4.3 在 `internal/client` 让只读镜像承载生命值：只接受服务端确认值，`Reset` 清空，不做任何预测。先写失败测试覆盖应用、重置与非法值拒绝。
- [ ] 4.4 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network ./internal/client -race -count=1 && go test ./internal/network -run "^$" -fuzz FuzzSmallPacketCodec -fuzztime=10s && go test ./internal/network -run "^$" -bench SmallPacketCodec -benchmem -count=3 && go test ./internal/archcheck -count=1'`，确认 `gofmt` 与 `git diff --check` 通过；提交 `feat: 升级协议 v13 携带生命值`，然后自动进入第 5 组。

## 5. 死亡结算与掉落

- [ ] 5.1 在 `internal/world` 先写失败测试，覆盖 `PrepareDropBatch` 接受 36 个互不相同的堆而不因上限被拒绝，以及超过新上限时整体拒绝且掉落物字节不变。
- [ ] 5.2 把 `PrepareDropBatch` 的堆数上限从 `1 + core.ChestSlots` 改为能同时覆盖容器破坏与 36 格死亡掉落的固定编译期常量，仍在函数入口一次性判定、超限立即返回失败且不修改区块。既有熔炉与箱子调用点行为不变。
- [ ] 5.3 在 `internal/sim` 先写失败测试覆盖死亡结算：背包被清空且物品出现在世界中、玩家回到出生锚点且满血、速度归零、带耐久的镐无损掉落、外部观察不到生命值为 0 的中间状态、同一初始状态两次结算掉落分布完全相同。
- [ ] 5.4 先写失败测试覆盖环形外扩：死亡区块有空位时全部落在死亡区块且同类合并；死亡区块满时溢出到邻近已加载区块且对应背包格被清空；未加载区块不被写入也不触发加载；扫完全部已加载区块仍装不下时该格保留在背包且不阻止死亡、不销毁物品。
- [ ] 5.5 在 `internal/sim` 实现死亡结算：生命值降到 0 时于同一 tick 内逐格放置（放成功才清空该格）、传回出生锚点、回满血、速度归零、重置摔落峰值与受伤计时。环形外扩按半径 0、1、2… 逐圈，同圈用既有 `sortChunkKeys` 的稳定顺序，只写 `ChunkReady` 且 `Chunk != nil` 的区块，扫完已加载集合即终止。**不引入跨区块原子提交**：每个被写入的区块各自 `touchChunk`。
- [ ] 5.6 **benchmark 风险实测**：执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport memory --perf-output /tmp/m4l-fall.json'`，确认八人探针玩家在整个测量窗口内不发生摔落伤害（探针玩家生命值始终为 20）。若发生，调整 `cmd/mcgo/multiplayer_benchmark.go` 的探针输入脚本使其不摔落，**不得放宽门禁、不得覆盖基线**；只有在无法避免时才停止并向用户报告需要升级 scenario 重建基线。
- [ ] 5.7 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim ./internal/world -race -count=1 && go test ./internal/archcheck -count=1'`，把 5.6 的实测结论记入 `docs/notes/perf-baseline.md`（标明为诊断性测量、非新基线）；提交 `feat: 实现死亡结算与背包掉落`，然后自动进入第 6 组。

## 6. 服务端接线与 Memory/TCP 闭环

- [ ] 6.1 在 `internal/server` 先写失败测试：权威玩家状态携带生命值只发给本人；死亡产生的掉落物差分正常发布给相关订阅者；慢会话继续走既有有界 outbox 与断开策略。
- [ ] 6.2 先写 Memory/TCP 纵向失败测试，使用同一脚本：玩家从高处摔落受伤 → 等待回复 → 再次致死摔落 → 背包掉在世界里 → 玩家回到出生点满血 → 另一名玩家能拾取这些掉落物。两种 transport 的最终区块、玩家状态与掉落物分布必须相同。参照 M4K 的 `runChestSharedByTwoPlayersScript` 写法，把断言写在共享脚本体内使两条链路自动共享。
- [ ] 6.3 先写 DiskStore 重启失败测试：玩家在生命值为 7 时正常刷新、关闭、重开，确认生命值原值恢复；v4 存档迁移后为满血。
- [ ] 6.4 在现有 switch 与发布顺序中最小接线；执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -race -count=1 && go test ./internal/network ./internal/sim ./internal/storage -race -count=1 && go test ./internal/archcheck -count=1'`，确认 `gofmt` 与 `git diff --check` 通过；提交 `feat: 接通生命值服务端闭环`，然后自动进入第 7 组。

## 7. HUD 接线

- [ ] 7.1 在 `internal/render` 先写 headless 失败测试，覆盖生命值 0/1/20 三种取值的绘制、按最坏布局重算后的固定 quad/glyph 容量、buffer 区间不重叠，且既有背包、熔炉、箱子与配方行布局保持不变。
- [ ] 7.2 在 `cmd/mcgo` 先写失败测试：HUD 显示最后确认的生命值；收到权威状态前不显示预测值；断线与玩家状态 reset 后清空。测试只用 fake window/gfx，不调用交互式 `run()`。
- [ ] 7.3 扩展既有 `HotbarRenderer` 的固定容量与布局绘制生命值，复用同一 pipeline 与缓冲；生命值是 render-local 值，由 app 从已确认镜像转换，`internal/render` 不得导入 `internal/network`。
- [ ] 7.4 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/render ./internal/client ./cmd/mcgo -race -count=1 && go test ./internal/archcheck -count=1'`，确认无窗口出现、`gofmt` 与 `git diff --check` 通过；提交 `feat: 接入生命值 HUD`，然后自动进入第 8 组。

## 8. 文档与最终门禁

- [ ] 8.1 更新 `README.md` 与 `docs/notes/lan-server.md`，说明生命值满值与显示、摔落安全高度与伤害曲线、自动回复的延迟与速率、死亡掉落与重生规则、协议 v13、玩家 schema v5 与 v1–v4 迁移、区块 schema v6 不变、备份与回退要求，以及未实现范围（战斗、怪物、食物、其他伤害源、床与重生点）；文档使用中文并与实现一致。
- [ ] 8.2 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && gofmt -l . && go test ./... -race -count=1 && go vet ./... && go test ./internal/archcheck -count=1 && CGO_ENABLED=0 GOOS=linux go build ./cmd/mcgod'`；`gofmt -l .` 必须无输出，且不得启动或聚焦游戏窗口。
- [ ] 8.3 执行 `openspec validate --all --strict --no-interactive` 与 `git diff --check`，核对 proposal、三份 delta specs、design 与实现一致；确认协议 v13、玩家 schema v5、区块 schema v6、scenario v12 与两份既有性能基线文件均未被放宽或静默覆盖。**特别核对 `authoritative-inventory` 的 REMOVED+ADDED 块逐字保留了主规格该 Requirement 下的全部 6 个既有场景**（M4K 曾在此漏带两条）。
- [ ] 8.4 只暂存 M4L 实现、测试、中文文档和本文件勾选，排除 `midscene_run/`；提交 `chore: 关闭 M4L 权威生命值`，停止实现并等待主规格同步、归档与推送指令。
