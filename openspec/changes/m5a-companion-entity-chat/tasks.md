## 1. Companion 身份、配置与启动参数（计划 Task 2）

- [x] 1.1 在 `internal/companion`、`internal/config`、`internal/server`、`cmd/mornlea` 与 `cmd/mornlea-server` 先写 ID/名称/0..4 配置、未知字段告警、注入范围和 debug panel 保留 AI 的失败测试；运行 `go test ./internal/companion ./internal/config ./internal/server ./cmd/mornlea ./cmd/mornlea-server -run 'Test(ValidateDefinitions|CompanionID|CompanionBody|ConfigAI|ServerConfigCompanions|Run.*AI)' -race -count=1` 确认 RED。
- [x] 1.2 最小实现 `CompanionID`、Definition/Body、config v1 `ai.companions[].id/name`、普通本地/专服 clone 注入和真实 archcheck 依赖；远程、benchmark、capture 保持不注入，且不加入后续 AI 字段。
- [x] 1.3 运行 `go test ./internal/companion ./internal/config ./internal/server ./cmd/mornlea ./cmd/mornlea-server -run 'Test(ValidateDefinitions|CompanionID|CompanionBody|ConfigAI|ServerConfigCompanions|Run.*AI)' -race -count=1`、`go test ./internal/archcheck -count=1`、`go test ./internal/companion ./internal/config ./internal/server ./cmd/mornlea ./cmd/mornlea-server -race -count=1`、`go vet ./internal/companion ./internal/config ./internal/server ./cmd/mornlea ./cmd/mornlea-server`、`test -z "$(gofmt -l internal/companion internal/config internal/server cmd/mornlea cmd/mornlea-server)"`、`openspec validate m5a-companion-entity-chat --strict --no-interactive`；临时删除重复名称检查，确认第一条命令 RED 后恢复并重跑至 PASS。

## 2. 协议 v16 与伙伴/聊天消息（计划 Task 3）

- [x] 2.1 在 `internal/network` 及客户端/专服协议测试中先锁定 v16/v15、旧 ID、Client 12、Server 16..19、最大 wire 长度、边界、排序与原子 decode，加入 companion codec fuzz/benchmark；`furnace_test.go` 与 `container_test.go` 只机械移除现已分配的 Client ID 12/Server ID 16 未分配断言，其余旧 ID 冻结语义不变；运行 `go test ./internal/network ./cmd/mornlea ./cmd/mornlea-server -run 'Test(Companion|Chat|Protocol|Handshake|ServerProtocol)' -race -count=1` 确认 RED。
- [x] 2.2 最小实现五种消息、sealed registry 与共用消息/`Validate` 边界；TCP codec 实现 wire 编解码，Memory typed packet 传递继续执行同一验证且不绕过；所有 decoder 在分配和返回前完成长度、UTF-8、枚举、ID、有限数值、`1..4` count 和严格排序校验。
- [x] 2.3 运行 `go test ./internal/network ./cmd/mornlea ./cmd/mornlea-server -run 'Test(Companion|Chat|Protocol|Handshake|Memory|TCP|ServerProtocol)' -race -count=1`、`go test ./internal/network -run '^$' -fuzz FuzzCompanionMessageCodec -fuzztime=5s`、`go test ./internal/network -run '^$' -bench 'Benchmark(Companion|ChatCommand)' -benchmem -count=5`、`go test ./internal/archcheck -count=1`、`go test ./internal/network ./cmd/mornlea ./cmd/mornlea-server -race -count=1`、`go vet ./internal/network ./cmd/mornlea ./cmd/mornlea-server`、`test -z "$(gofmt -l internal/network cmd/mornlea cmd/mornlea-server)"`、`openspec validate m5a-companion-entity-chat --strict --no-interactive`；临时把 States 上限改为 5，确认第一条命令 RED 后恢复并重跑至 PASS。

## 3. Companion schema v1 编解码（计划 Task 4）

- [x] 3.1 在 `internal/storage` 先写 MCAI v1 round-trip/golden、排序不改输入、CRC/future/truncation/oversize、非法浮点/背包和 worn durability 测试与 fuzz；运行 `go test ./internal/storage -run 'TestCompanionCodec' -race -count=1` 确认 RED。
- [x] 3.2 最小实现 32-byte header、221-byte record、14,176-byte 上限、64 条预分配前门禁、canonical 排序、CRC32C 及 corrupt/future error；v1 不保存名称或任务。
- [x] 3.3 仅运行 `go test ./internal/storage -run '^TestCompanionCodecV1RoundTripAndGolden$' -update-storage-fixtures` 生成 `companions-v1.bin`；再运行 `go test ./internal/storage -run 'TestCompanionCodec' -race -count=1`、`go test ./internal/storage -run '^$' -fuzz FuzzDecodeCompanions -fuzztime=5s`、`shasum -a 256 internal/storage/testdata/companions-v1.bin`、`go test ./internal/archcheck -count=1`、`go test ./internal/storage -race -count=1`、`go vet ./internal/storage`、`test -z "$(gofmt -l internal/storage)"`、`openspec validate m5a-companion-entity-chat --strict --no-interactive`；临时移除预分配前 count guard，确认 codec 测试 RED 后恢复并重跑至 PASS。

## 4. Memory/Disk CompanionStore 与原子替换（计划 Task 5）

- [x] 4.1 在 `internal/storage` 先写 Memory/Disk 共用 contract、revision/idempotency/conflict/no-alias/context/close、原子替换失败、正式文件损坏不覆写和 backup 包含 `companions.ai` 但跳过 temp 的测试；运行 `go test ./internal/storage -run 'Test(CompanionStore|DiskCompanion|WorldBackupIncludesCompanion)' -race -count=1` 确认 RED。
- [x] 4.2 将 `CompanionStore` 组合进 `WorldStore`；Memory 保存 canonical bytes，Disk 使用固定根文件、`max+1` 有界读取和既有 temp/Sync/Rename/parent-Sync helper，不新增 repository/factory 或改动备份生产逻辑。
- [x] 4.3 运行 `go test ./internal/storage -run 'Test(CompanionStore|DiskCompanion|WorldBackupIncludesCompanion)' -race -count=1`、`go test ./internal/storage -race -count=1`、`go test ./internal/archcheck -count=1`、`go vet ./internal/storage`、`test -z "$(gofmt -l internal/storage)"`、`openspec validate m5a-companion-entity-chat --strict --no-interactive`；临时跳过正式文件验证并覆写损坏文件，确认包含 `Test(CompanionStore|DiskCompanion|WorldBackupIncludesCompanion)` 的命令 RED 后恢复并重跑至 PASS。

## 5. sim 静态伙伴、出生与 3×3 兴趣（计划 Task 6）

- [x] 5.1 在 `internal/sim` 先写排序 idle、restore 碰撞就绪、revision retry、3×3 candidate/active interest、八玩家加四伙伴独立容量测试；运行 `go test ./internal/sim -run 'Test(Companion|EightPlayersAndFourCompanions)' -race -count=1` 确认 RED。
- [x] 5.2 实现最小独立 `companionState`、注册/身体快照、复用既有 restore/spawn 私有 helper 和 subscription union；不抽 `actorState`、不接 movement/mining/death/session。
- [x] 5.3 运行 `go test ./internal/sim -run 'Test(Companion|EightPlayersAndFourCompanions)' -race -count=1`、`go test ./internal/sim -run '^$' -bench 'Benchmark.*Step' -benchmem -count=5`、`go test ./internal/sim -race -count=1`、`go test ./internal/archcheck -count=1`、`go vet ./internal/sim`、`test -z "$(gofmt -l internal/sim)"`、`openspec validate m5a-companion-entity-chat --strict --no-interactive`；临时把兴趣半径改为 2，确认包含 `Test(Companion|EightPlayersAndFourCompanions)` 的命令 RED 后恢复并重跑至 PASS。

## 6. 单聚合伙伴持久化 worker（计划 Task 7）

- [x] 6.1 在 `internal/server` 先写单 in-flight、inactive 保留、失败 dirty/retry、Flush retry、mutex 外 Save、in-flight 期间更新、输入 no-alias 和 Flush latest 测试；运行 `go test ./internal/server -run 'TestCompanionPersistence' -race -count=1` 确认 RED。
- [x] 6.2 实现一个 capacity-1 jobs/completions worker、内部生命周期 context、frozen job/retry clone、Observe/Poll/Flush/Close；store I/O 不持 mutex、不阻塞 tick，不抽通用 worker。
- [x] 6.3 运行 `go test ./internal/server -run 'TestCompanionPersistence' -race -count=1`、`go test ./internal/server -race -count=1`、`go vet ./internal/server`、`test -z "$(gofmt -l internal/server)"`、`openspec validate m5a-companion-entity-chat --strict --no-interactive`；临时把 `SaveCompanions` 放进 mutex，确认包含 `TestCompanionPersistence` 的命令 RED 后恢复并重跑至 PASS。

## 7. Host 启动合并、恢复与 shutdown（计划 Task 8）

- [x] 7.1 在 `internal/server`、`cmd/mornlea` 与 `cmd/mornlea-server` 先写 AI-disabled 跳过 store、active/inactive merge、65th 拒绝、corrupt/future worker 前失败、清空配置不触碰文件、shutdown retry/final-step/order 测试；运行 `go test ./internal/server ./cmd/mornlea ./cmd/mornlea-server -run 'Test(NewHost|RemovingAllCompanion|CompanionShutdown)' -race -count=1` 确认 RED。
- [x] 7.2 实现 `NewHost(ctx, config, generator, store) (*Host,error)` 的同步 Load/merge 边界、caller-owned 资源清理、AI-disabled `NewWorld` 限制，以及 Server step Observe/Poll 和 save-before-Sync-before-Close；机械更新全部 `NewHost` caller。
- [x] 7.3 运行 `rg -n '\bNewHost\(' --glob '*.go'` 核对 caller，再运行 `go test ./internal/server ./cmd/mornlea ./cmd/mornlea-server -run 'Test(NewHost|RemovingAllCompanion|CompanionShutdown|Application.*Host)' -race -count=1`、`go test ./internal/server ./cmd/mornlea ./cmd/mornlea-server -race -count=1`、`go test ./internal/archcheck -count=1`、`go vet ./internal/server ./cmd/mornlea ./cmd/mornlea-server`、`test -z "$(gofmt -l internal/server cmd/mornlea cmd/mornlea-server)"`、`openspec validate m5a-companion-entity-chat --strict --no-interactive`；临时在 merge 中丢弃 inactive records，确认包含 `Test(NewHost|RemovingAllCompanion|CompanionShutdown|Application.*Host)` 的命令 RED 后恢复并重跑至 PASS。

## 8. 伙伴网络发布与独立容量（计划 Task 9）

- [x] 8.1 在 `internal/server` 先写 snapshot-before-spawn、排序 states/new-spawn 延迟一 tick、interest-exit despawn、8+4 容量和未知 definition 整批失败测试；运行 `go test ./internal/server -run 'Test(CompanionPublication|EightPlayersAndFourCompanions)' -race -count=1` 确认 RED。
- [x] 8.2 增加独立 `visibleCompanions`，复用脚下 chunk 小 helper，按 despawn/Forget/snapshot/spawn/states 固定顺序发布；整批 definition preflight 在任何 enqueue/visibility mutation 前完成，不建通用 entity publisher。
- [x] 8.3 运行 `go test ./internal/server -run 'Test(CompanionPublication|EightPlayersAndFourCompanions)' -race -count=1`、`go test ./internal/server -race -count=1`、`go vet ./internal/server`、`test -z "$(gofmt -l internal/server)"`、`openspec validate m5a-companion-entity-chat --strict --no-interactive`；临时允许 snapshot 前 Spawn，确认包含 `Test(CompanionPublication|EightPlayersAndFourCompanions)` 的命令 RED 后恢复并重跑至 PASS。

## 9. tick 边界聊天寻址与 Memory/TCP parity（计划 Task 10）

- [x] 9.1 在 `internal/server` 先写精确 parser、名称 32 rune/128 bytes 边界、Invalid/Unknown 单播、Accepted 广播顺序、stale generation 和 Memory/TCP parity 测试；运行 `go test ./internal/server -run 'Test(ChatCommand|MalformedOrUnknownCompanion|CompanionAddress|AcceptedCompanionChat|StaleSessionChat|CompanionChatMemoryTCPParity)' -race -count=1` 确认 RED。
- [x] 9.2 实现 bounded `incomingChats`、tick 前按 channel 顺序 drain、精确大小写名称查找和进程内 EventID；只产生事实 delivery，不构造 `sim.Command`、FIFO、任务或身体/world mutation。
- [x] 9.3 运行 `go test ./internal/server -run 'Test(ChatCommand|MalformedOrUnknownCompanion|CompanionAddress|AcceptedCompanionChat|StaleSessionChat|CompanionChatMemoryTCPParity)' -race -count=1`、`go test ./internal/server -run '^$' -bench BenchmarkChatRoutingFourCompanions -benchmem -count=5`、`go test ./internal/server -race -count=1`、`go vet ./internal/server`、`test -z "$(gofmt -l internal/server)"`、`openspec validate m5a-companion-entity-chat --strict --no-interactive`；临时改为 prefix matching，确认包含 `Test(ChatCommand|MalformedOrUnknownCompanion|CompanionAddress|AcceptedCompanionChat|StaleSessionChat|CompanionChatMemoryTCPParity)` 的命令 RED 后恢复并重跑至 PASS。

## 10. 客户端伙伴插值与 ChatEvent 环（计划 Task 11）

- [x] 10.1 在 `internal/client` 与 `cmd/mornlea` 先写 Spawn/States/Despawn/Reset、未知/过时/五项批次原子拒绝、ID 排序、32 条事件环、stale EventID、消息路由、断线清理及两条真实循环恰好一次 Advance 测试；运行 `go test ./internal/client ./cmd/mornlea -run 'Test(Companion|ChatEvents|ApplicationRoutesCompanion|ApplicationAdvancesCompanions)' -race -count=1` 确认 RED。
- [x] 10.2 只抽未导出 `remoteActor`，实现最多四项伙伴镜像和固定 `[32]` ChatEvent 环；receiver 协议错误关闭 endpoint，断线 Reset，frame/interactive 同帧恰好推进一次，并只登记真实 `client -> companion` archcheck 边。
- [x] 10.3 运行 `go test ./internal/client ./cmd/mornlea -run 'Test(Companion|ChatEvents|ApplicationRoutesCompanion|ApplicationAdvancesCompanions)' -race -count=1`、`go test ./internal/client -run 'TestCompanionPresentationHotPathAllocations' -count=1`、`go test ./internal/client ./cmd/mornlea -race -count=1`、`go test ./internal/archcheck -count=1`、`go vet ./internal/client ./cmd/mornlea`、`test -z "$(gofmt -l internal/client cmd/mornlea)"`、`openspec validate m5a-companion-entity-chat --strict --no-interactive`；临时逐项 Apply 后再验证 batch，确认包含 `Test(Companion|ChatEvents|ApplicationRoutesCompanion|ApplicationAdvancesCompanions)` 的命令 RED 后恢复并重跑至 PASS。

## 11. 统一 Avatar/NameTag 与 scenario v16（计划 Task 12）

- [x] 11.1 在 `internal/render`、`cmd/mornlea` 与 `cmd/perfcheck` 先写 EntityKey domain、旧玩家配色、Avatar 11/12 overflow、NameTag 12/13 overflow、单 pass、0 alloc、v16 固定上传布局、v6..v15 历史可读、唯一 `15:16` 和性能 record-only 测试；运行 `go test ./internal/render ./cmd/mornlea ./cmd/perfcheck -run 'Test(EntityKey|AvatarPlayerPalette|AvatarRendererAcceptsEleven|NameTagRendererAcceptsTwelve|ApplicationRendersSeven|ElevenActor|BenchmarkScenarioV16|Perfcheck)' -race -count=1` 确认 RED。
- [x] 11.2 只扩现有 Avatar/NameTag buffer 与输入上限，overflow 在 GPU/atlas 副作用前返回；app 合并玩家/伙伴后按 EntityKey 排序，保留玩家颜色，并把 benchmark/perfcheck 升到 scenario v16 和唯一 `15:16`。
- [x] 11.3 锁定 Avatar 66 parts/5280/5536/5556 与 NameTag 12/768/1024/24576/25600；运行 `go test ./internal/render ./cmd/mornlea ./cmd/perfcheck -run 'Test(EntityKey|AvatarPlayerPalette|AvatarRendererAcceptsEleven|NameTagRendererAcceptsTwelve|ApplicationRendersSeven|ElevenActor|BenchmarkScenarioV16|Perfcheck)' -race -count=1`、`go test ./internal/render -run '^$' -bench 'Benchmark(Avatar|NameTag|Multiplayer)' -benchmem -count=5`、`go test ./internal/render ./cmd/mornlea ./cmd/perfcheck -race -count=1`、`go vet ./internal/render ./cmd/mornlea ./cmd/perfcheck`、`test -z "$(gofmt -l internal/render cmd/mornlea cmd/perfcheck)"`、`openspec validate m5a-companion-entity-chat --strict --no-interactive`；分别临时移除 Avatar preflight、改变玩家颜色、把 scenario 改回 15、允许 `14:16`，确认包含 `Test(EntityKey|AvatarPlayerPalette|AvatarRendererAcceptsEleven|NameTagRendererAcceptsTwelve|ApplicationRendersSeven|ElevenActor|BenchmarkScenarioV16|Perfcheck)` 的命令逐项 RED 后恢复并重跑至 PASS。

## 12. 聊天输入与同一 HUD pass（计划 Task 13）

- [x] 12.1 在 `internal/client/window`、`internal/render/hud` 与 `cmd/mornlea` 先写中文/rune Backspace、1024/1025 bytes、sticky overflow、closed drain、Enter/Esc/优先级、动作抑制、cursor baseline、断线清理、稳定格式、未确认 inventory 仍显示 chat、六行容量、空 chat 无额外 pass/alloc 和 v16 HUD offset 测试；运行 `go test ./internal/client ./internal/render/hud ./cmd/mornlea -run 'Test(Chat|EmptyChat|TextInput|WindowMaps|ApplicationRendersChat)' -race -count=1` 确认 RED。
- [x] 12.2 实现固定字符队列和 `chatInput`、最近六行缓存、同一 HUD pass 的 ChatOverlay；聊天布局移出 inventory validity 早退，固定扩容到 236 quads/700 glyphs、glyph offset 11776、总容量 45376，空聊天写入 11776 bytes。
- [x] 12.3 运行 `go test ./internal/client ./internal/render/hud ./cmd/mornlea -run 'Test(Chat|EmptyChat|TextInput|WindowMaps|ApplicationRendersChat)' -race -count=1`、`go test ./internal/render/hud -run 'TestChatOverlayHotPathAllocations' -count=1`、`go test ./internal/client ./internal/render/... ./cmd/mornlea -race -count=1`、`go vet ./internal/client ./internal/render/... ./cmd/mornlea`、`test -z "$(gofmt -l internal/render internal/client/window.go internal/client/window_test.go cmd/mornlea)"`、`openspec validate m5a-companion-entity-chat --strict --no-interactive`；临时按 byte 截断 Backspace，确认包含 `Test(Chat|EmptyChat|TextInput|WindowMaps|ApplicationRendersChat)` 的命令 RED 后恢复并重跑至 PASS。

## 13. 无窗口 ai-companion 视觉场景（计划 Task 14）

- [x] 13.1 在 `cmd/mornlea` 先写 `ai-companion` 唯一末场景、旧场景按 Name 查找、固定 fixture 和污染后完整 reset 测试；运行 `go test ./cmd/mornlea -run 'TestCapture(AICompanion|OakGrove|TargetBlock)' -race -count=1` 确认 RED。
- [x] 13.2 在 `oak-grove` 后追加固定 `ai-companion`，更新 README 场景清单但不写 golden；运行 `VISUAL_OUT=/private/tmp/mornlea-m5a-ai-companion-candidate make visual-check`，要求仅因缺少 `ai-companion.png` 非零且全部旧场景通过既有双阈值。
- [x] 13.3 使用 `view_image` 检查 `/private/tmp/mornlea-m5a-ai-companion-candidate/ai-companion.png` 并运行 `shasum -a 256 /private/tmp/mornlea-m5a-ai-companion-candidate/ai-companion.png`；向用户报告绝对路径和 SHA-256，只有收到明确人工确认后才运行 `VISUAL_OUT=/private/tmp/mornlea-m5a-ai-companion-update make visual-update` 与 `test "$(git status --short --untracked-files=all -- cmd/mornlea/testdata/golden)" = "?? cmd/mornlea/testdata/golden/ai-companion.png"`。
- [x] 13.4 运行 `VISUAL_OUT=/private/tmp/mornlea-m5a-ai-companion-check-1 make visual-check`、`VISUAL_OUT=/private/tmp/mornlea-m5a-ai-companion-check-2 make visual-check`、`go test ./cmd/mornlea -race -count=1`、`go vet ./cmd/mornlea`、`test -z "$(gofmt -l cmd/mornlea)"`、`openspec validate m5a-companion-entity-chat --strict --no-interactive`；确认旧 golden 零修改后完成该任务。

## 14. 累计工程与性能门禁

- [ ] 14.1 在冻结 HEAD 运行 `make rust`、`go test ./internal/companion ./internal/config ./internal/network ./internal/storage ./internal/sim ./internal/server ./internal/client ./internal/render/... ./cmd/mornlea ./cmd/mornlea-server ./cmd/perfcheck -race -count=1`、`go test ./internal/archcheck -count=1`。
- [ ] 14.2 运行 `go test ./internal/network -run '^$' -fuzz FuzzCompanionMessageCodec -fuzztime=10s`、`go test ./internal/storage -run '^$' -fuzz FuzzDecodeCompanions -fuzztime=10s`、`go test ./internal/network ./internal/sim ./internal/server ./internal/render ./cmd/mornlea -run '^$' -bench 'Benchmark(Companion|ChatCommand|.*Step|ChatRoutingFourCompanions|Avatar|NameTag|Multiplayer)' -benchmem -count=5`、`VISUAL_OUT=/private/tmp/mornlea-m5a-final-visual make visual-check`；确认 benchmark 固定输入仍为七名玩家、零伙伴且 scenario 为 v16，性能数值只写记录。
- [ ] 14.3 运行 `m5a_perf_dir="$(mktemp -d /private/tmp/mornlea-m5a-v16.XXXXXX)"`，再依次运行 `TERM=xterm-256color zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/mornlea --benchmark --benchmark-transport memory --perf-output '$m5a_perf_dir/memory-v16.json'"`、`zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/perfcheck --baseline '$m5a_perf_dir/memory-v16.json' --current '$m5a_perf_dir/memory-v16.json'"`、`TERM=xterm-256color zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/mornlea --benchmark --benchmark-transport tcp --perf-output '$m5a_perf_dir/tcp-v16.json'"`、`zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/perfcheck --baseline '$m5a_perf_dir/tcp-v16.json' --current '$m5a_perf_dir/tcp-v16.json'"`、`zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/perfcheck --baseline '$m5a_perf_dir/memory-v16.json' --current '$m5a_perf_dir/tcp-v16.json'"`、`shasum -a 256 "$m5a_perf_dir/memory-v16.json" "$m5a_perf_dir/tcp-v16.json"`；不得改写 M2 v15/M5 v14 基线 JSON。
- [ ] 14.4 运行 `go test ./... -race -count=1`、`go vet ./...`、`test -z "$(gofmt -l .)"`、`git diff --check`、`openspec validate m5a-companion-entity-chat --strict --no-interactive`、`openspec validate --all --strict --no-interactive`，保证报告结构、身份、真实 overflow、数据丢失和 I/O 门禁未放宽。

## 15. 累计独立 review

- [ ] 15.1 对 Task 1 前一提交到当前冻结 HEAD 做独立 review，逐项核对 OpenSpec/实现一致、无 M5B–D、v16 旧 ID、schema v1/inactive/64、8+4、tick/snapshot/chat parity、原子客户端镜像、统一 renderer、Unicode UI、唯一新 golden、scenario v16/唯一 `15:16`/旧基线不变。
- [ ] 15.2 修复全部 Critical/Important/Minor finding。仅修改规划文档时运行 `openspec validate m5a-companion-entity-chat --strict --no-interactive`、`openspec validate --all --strict --no-interactive`、`rg -n 'T[B]D|T[O]DO|implement[ ]later|fill[ ]in[ ]details|适当[处]理|类似[ ]Task' openspec/changes/m5a-companion-entity-chat` 与 `git diff --check`。修改代码、producer 或 workload 时丢弃旧 v16 临时报告，并逐条执行 14.1、14.2、14.3、14.4 中列出的反引号命令。
- [ ] 15.3 确认本 active change 的 Task 2..14、累计门禁与累计 review 全部完成，并运行 `openspec status --change m5a-companion-entity-chat --json`、`openspec validate m5a-companion-entity-chat --strict --no-interactive`、`openspec validate --all --strict --no-interactive`；主规格同步和 archive 留给 active change 外层收尾，不在本清单中自引用。
