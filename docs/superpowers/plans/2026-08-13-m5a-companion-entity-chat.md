# M5A 具名伙伴实体与聊天寻址 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在不接入外部 AI 模型、不执行世界动作的前提下，交付最多 4 个服务端权威具名伙伴、确定性聊天寻址、Memory/TCP 同构协议、持久身体状态，以及客户端可见实体和聊天 UI 的完整 M5A 闭环。

**Architecture:** 新增轻量 `internal/companion` 领域包，服务端继续通过现有 `sim.Engine`、权威 tick、区块兴趣、发布和持久化边界驱动伙伴；客户端复用远端玩家插值、Avatar/NameTag renderer 和 Hotbar HUD，不建立第二套模拟或渲染管线。M5A 只处理静态 idle 伙伴和 `@名称 指令` 的确定性寻址，任务 FIFO、路径移动、Planner、HTTP 模型调用、persona、工具执行与通用 `actorState` 抽取均不进入本计划。

**Tech Stack:** Go 1.26、现有 Memory/TCP transport、WebGPU 经 `internal/gfx`、GLFW 字符输入、OpenSpec 1.7、现有存档原子替换与 CRC32C、Go 标准库；不新增第三方依赖。

## Global Constraints

- 执行前使用 `superpowers:using-git-worktrees`，从包含本计划提交的 `main` 创建隔离分支 `codex/m5a-companion-entity-chat`；不得在根工作树实现。
- 实现前创建并校验 active OpenSpec change `m5a-companion-entity-chat`；若代码需要偏离 change，先修订 proposal/spec/design/tasks，再继续。
- 配置最多 4 个 active 伙伴，聚合存档最多 64 条 active+inactive body；达到 64 条时拒绝新增 ID，绝不删除旧记录。
- 伙伴不占现有最多 8 名玩家的容量；每个 active 伙伴固定贡献脚下区块为中心的 3×3 区块兴趣。
- 网络协议升级到 v16；保留所有 v15 message ID，新增 Client ID 12，Server ID 16..19；v15 登录必须被明确拒绝。
- `companions.ai` 使用 companion schema v1，只存 ID、维度、位置、yaw、pitch、36 格背包和聚合 revision；名称来自配置，不写存档。M5B 新增任务/FIFO 时必须升 schema v2。
- 配置 schema 仍为 v1；M5A 只识别 `ai.companions[].id/name`，未来 `endpoint/model/apiKeyEnv/timeout/persona` 字段不在本任务内。
- `benchmark scenario` 升到 v16：固定场景仍不注入伙伴或聊天，但 Avatar/NameTag 与 Hotbar HUD 的固定上传布局、offset 和每帧写入字节数改变，不能伪装成 v15。只增加唯一显式 `15:16` 迁移；既有 M2 v15、M5 v14 基线字节保持不变，本任务只生成 v16 记录，不提升基线。
- 性能数值只记录，不作为门禁；报告完整性、真实 overflow、数据丢失、协议/存档损坏和 I/O 错误仍必须失败。
- 所有自动测试均使用无窗口或离屏路径；不得启动或聚焦交互式游戏窗口。新视觉图必须在人工确认后才能写入 golden。
- Go 注释、GoDoc、文档和测试说明使用中文；标识符、wire magic、协议字段与约定俗成的技术术语保留英文。
- 每项任务执行 RED→GREEN→mutation（适用时）→受影响包 race→OpenSpec strict→独立只读 review→修复 findings→精确提交；ignored 报告写到 `.superpowers/sdd/2026-08-13-m5a-companion-entity-chat/`。
- 保留用户已有 `midscene_run/log/*.log` 改动和未跟踪 `mcgo`；不得暂存、覆盖或删除。

---

## 文件结构与职责

### 新增文件

- `internal/companion/identity.go`：独立 Companion ID、配置 Definition、持久 Body、固定容量与验证。
- `internal/network/message_companion.go`：五种新 wire message 与逐字段 Valid。
- `internal/storage/companion_types.go`、`companion_codec.go`：CompanionStore、MCAI v1、CRC32C、64 条上限。
- `internal/sim/companion.go`：最小静态 companionState、出生恢复、排序快照。
- `internal/server/companion_persistence.go`：单聚合存档 worker；`companion_publication.go`：可见集；`companion_chat.go`：tick 边界寻址。
- `internal/client/companions.go`、`chat.go`：伙伴插值镜像和固定 32 条 ChatEvent 环。
- `internal/render/hud/chat.go`、`cmd/mornlea/chat.go`：固定容量 HUD 布局和 1 KiB UTF-8 输入状态。
- `cmd/mornlea/testdata/golden/ai-companion.png`：人工确认后加入的唯一新视觉基线。
- 上述每个生产文件均配同包 `*_test.go`；codec 额外配 fuzz/golden，视觉额外配 capture test。

### 主要修改文件

- `internal/config/config.go`、`internal/server/config.go`、`cmd/mornlea/app_startup.go`、`cmd/mornlea-server/main.go`：AI 配置和启动注入。
- `internal/network/login.go`、`codec.go`、`codec_server.go`：协议 v16 与新消息 ID。
- `internal/storage/types.go`、`memory.go`、`disk.go`：WorldStore 组合 CompanionStore 和 `companions.ai`；备份复用现有根文件复制逻辑，不改生产备份实现。
- `internal/sim/engine.go`、`engine_step.go`、`engine_subscription.go`、`spawn.go`、`command.go`：伙伴注册、出生、兴趣与 TickResult。
- `internal/server/host.go`、`server.go`、`session.go`、`session_ingress.go`、`publication.go`、`shutdown.go`：启动合并、持久化、聊天、发布和容量。
- `internal/client/remote_players.go`、`remote_interpolation.go`、`receiver.go`：抽取未导出 remoteActor 并路由新消息。
- `internal/render/avatar.go`、`name_tag.go`、`hud/renderer.go`、`hud/layout.go`：一套 renderer/HUD 固定扩容。
- `internal/client/window.go`、`cmd/mornlea/app_messages.go`、`app_frame.go`、`app_render.go`、`app_lifecycle.go`、`interactive.go`：字符输入、镜像推进、渲染与动作抑制。
- `cmd/mornlea/capture.go`、`capture_scene.go`、既有 capture tests 和 `README.md`：末尾追加 `ai-companion`，旧场景改按 Name 查找。
- `internal/archcheck/dependency_test.go`：只登记需要的单向依赖。

---

### Task 1: 建立 M5A OpenSpec 契约

**Files:**
- Create: `openspec/changes/m5a-companion-entity-chat/.openspec.yaml`
- Create: `openspec/changes/m5a-companion-entity-chat/proposal.md`
- Create: `openspec/changes/m5a-companion-entity-chat/design.md`
- Create: `openspec/changes/m5a-companion-entity-chat/tasks.md`
- Create: `openspec/changes/m5a-companion-entity-chat/specs/companion-identity-configuration/spec.md`
- Create: `openspec/changes/m5a-companion-entity-chat/specs/authoritative-companion-entities/spec.md`
- Create: `openspec/changes/m5a-companion-entity-chat/specs/companion-chat-protocol/spec.md`
- Create: `openspec/changes/m5a-companion-entity-chat/specs/companion-persistence/spec.md`
- Create: `openspec/changes/m5a-companion-entity-chat/specs/companion-client-presentation/spec.md`
- Create: `openspec/changes/m5a-companion-entity-chat/specs/visual-verification/spec.md`
- Create: `openspec/changes/m5a-companion-entity-chat/specs/bounded-benchmark-workload/spec.md`
- Create: `openspec/changes/m5a-companion-entity-chat/specs/hardware-performance-baselines/spec.md`

**Interfaces:**
- Consumes: `docs/superpowers/specs/2026-08-13-ai-native-companions-design.md`
- Produces: active change `m5a-companion-entity-chat`，其 tasks 对应本计划 Task 2..14 和累计门禁/review；主规格同步与 archive 是外层收尾动作，不写成 active task，避免循环依赖。

- [ ] **Step 1: 初始化隔离 worktree 的本地依赖**

~~~bash
make rust
~~~

Expected: 使用仓库现有 pinned toolchain 构建 worktree-local native dylib；不得下载或改用其他 Go/Rust toolchain。失败则停止，不在缺 dylib 的 clean worktree 运行后续 Go 命令。

- [ ] **Step 2: 创建 change 并读取 schema 指令**

~~~bash
openspec new change m5a-companion-entity-chat
openspec status --change m5a-companion-entity-chat --json
openspec instructions proposal --change m5a-companion-entity-chat --json
openspec instructions specs --change m5a-companion-entity-chat --json
openspec instructions design --change m5a-companion-entity-chat --json
openspec instructions tasks --change m5a-companion-entity-chat --json
~~~

Expected: change 可见，初始 artifacts 未完成。

- [ ] **Step 3: 写 proposal 与八份 delta spec**

每条 Requirement 必须含 Given/When/Then，并锁定：

~~~text
配置 0..4、名称 canonical/无 Unicode 空白/区分大小写唯一；
独立 16-byte CompanionID；
服务端静态 idle、3×3 兴趣、玩家容量 8 不变；
@名称 指令只做确定性寻址，不执行；
协议 v16 和固定 ID；
companions.ai v1、64 条、inactive 保留、启用 AI 时损坏/future 拒绝启动；
客户端只读镜像、统一渲染、断线清空；
ai-companion 追加在 oak-grove 后成为末场景，且只新增一张 golden；
Avatar/NameTag/Hotbar HUD 上传布局改变固定 workload，因此 scenario v16；只允许 15:16；
v6..v15 历史报告保持可读，性能数值仍只记录，M2 v15/M5 v14 基线不改。
~~~

- [ ] **Step 4: 写 design 与 tasks**

design 明确 wire、MCAI header、`NewHost` error 边界、retry、renderer/HUD 容量、scenario v16 与唯一 `15:16` 迁移；visual delta 必须重写主规格内所有旧“末尾/终场景”子句（包括 Requirement 与全部 Scenario），保持旧顺序并只让 `ai-companion` 成为末场景。tasks 把测试放在对应实现前，且不把 archive 自身写成 active task。

- [ ] **Step 5: 严格校验和 placeholder scan**

~~~bash
openspec validate m5a-companion-entity-chat --strict --no-interactive
openspec validate --all --strict --no-interactive
rg -n 'TBD|TODO|implement later|fill in details|适当处理|类似 Task' openspec/changes/m5a-companion-entity-chat
~~~

Expected: 两次 strict PASS，scan 无输出。

- [ ] **Step 6: 独立评审并提交**

修复全部 findings 后：

~~~bash
git add openspec/changes/m5a-companion-entity-chat
git diff --cached --check
git commit -m "docs: 规划 M5A 具名伙伴实体与聊天寻址"
~~~

Expected: 提交只含 active change。

---

### Task 2: Companion 身份、配置与启动参数

**Files:**
- Create: `internal/companion/identity.go`
- Create: `internal/companion/identity_test.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/config/migration_test.go`
- Modify: `internal/server/config.go`
- Modify: `internal/server/config_test.go`
- Modify: `internal/archcheck/dependency_test.go`
- Modify: `cmd/mornlea/app.go`
- Modify: `cmd/mornlea/main.go`
- Modify: `cmd/mornlea/app_startup.go`
- Modify: `cmd/mornlea/debug_panel_test.go`
- Modify: `cmd/mornlea/run_test.go`
- Modify: `cmd/mornlea-server/main.go`
- Modify: `cmd/mornlea-server/main_test.go`
- Modify: `openspec/changes/m5a-companion-entity-chat/tasks.md`

**Interfaces:**
- Consumes: `core.ParsePlayerID(string) (core.PlayerID, error)`、`core.NormalizeDisplayName(string) (string, error)`
- Produces:

~~~go
const MaxActive = 4
const MaxStored = 64

type ID [16]byte
func ParseID(string) (ID, error)
func (ID) Valid() bool
func (ID) String() string
func (ID) MarshalText() ([]byte, error)
func (*ID) UnmarshalText([]byte) error

type Definition struct {
	ID   ID     `json:"id"`
	Name string `json:"name"`
}
type Body struct {
	ID ID
	Dimension core.DimensionID
	Position [3]float32
	Yaw, Pitch float32
	Inventory core.Inventory
}
func ValidateDefinitions([]Definition) error
func ValidateName(string) error
~~~

所有权边界直接使用标准库 `slices.Clone`，不新增只包一行的公共 clone helper。

- [ ] **Step 1: 写 ID/Definition RED**

~~~go
func TestValidateDefinitions(t *testing.T) {
	// 分别覆盖：四个有效、五个、重复 ID、重复名称、
	// A/a 同时有效、名称含空白、名称非 canonical、零 ID。
}
func TestCompanionIDJSONRoundTrip(t *testing.T) {}
func TestCompanionBodyHasNoNameTaskOrPersona(t *testing.T) {}
func TestConfigAICompanionUnknownFieldsWarnAndIgnore(t *testing.T) {}
~~~

- [ ] **Step 2: 运行 RED**

~~~bash
go test ./internal/companion ./internal/config ./internal/server -run 'Test(ValidateDefinitions|CompanionID|CompanionBody|ConfigAI|ServerConfigCompanions)' -race -count=1
~~~

Expected: FAIL，包和字段不存在。

- [ ] **Step 3: 最小实现**

`ID` 调用 `core.ParsePlayerID` 后显式转换，不复制 UUID parser。名称先 `NormalizeDisplayName`，要求输出与输入完全相同，再逐 rune 拒绝 `unicode.IsSpace`。`MarshalText` 输出 canonical UUID；`UnmarshalText` 必须经 `ParseID` 验证 UUIDv4，不接受任意 16 bytes。

~~~go
type AI struct {
	Companions []companion.Definition `json:"companions,omitempty"`
}
type Config struct {
	// 既有字段保持原样。
	AI *AI `json:"ai,omitempty"`
}
func (config Config) CompanionDefinitions() []companion.Definition
~~~

`applyAI` 只识别 `companions`；未知 AI 直属字段按既有规则警告并忽略。每个 companion 条目先以 `map[string]json.RawMessage` 解析，只识别 `id/name`，对 `persona/task` 等条目内未来字段同样按精确路径告警后忽略，不能依赖 `encoding/json` 静默吞字段。缺失、`null`、空列表均禁用。`cmd/mornlea/main.go` 在读取 `effective` 后只为普通本地游戏把 `effective.CompanionDefinitions()` clone 到 `applicationOptions.Companions`；远程、benchmark、capture 都保持空，避免固定场景注入伙伴。专用服务端同样 clone 到 `server.Config.Companions`。调试面板保存已有配置时必须先读原配置、只更新 tunables，再证明 AI definitions 完全不变；本任务仍不得提前把 AGENTS/CLAUDE/config 的“当前基线”改成 M5A。

- [ ] **Step 4: GREEN 与 mutation**

~~~bash
go test ./internal/companion ./internal/config ./internal/server ./cmd/mornlea ./cmd/mornlea-server -run 'Test(ValidateDefinitions|CompanionID|CompanionBody|ConfigAI|ServerConfigCompanions|Run.*AI)' -race -count=1
go test ./internal/archcheck -count=1
~~~

Expected: PASS。测试锁定普通本地注入、远程不注入、benchmark/capture 不注入，以及 debug panel 保存后 AI 完全不变。临时删除重复名称检查，原测试 RED；恢复后 PASS。

- [ ] **Step 5: 校验、评审、勾选并提交**

~~~bash
go test ./internal/companion ./internal/config ./internal/server ./cmd/mornlea ./cmd/mornlea-server -race -count=1
go vet ./internal/companion ./internal/config ./internal/server ./cmd/mornlea ./cmd/mornlea-server
test -z "$(gofmt -l internal/companion internal/config internal/server cmd/mornlea cmd/mornlea-server)"
openspec validate --all --strict --no-interactive
git diff --check
git add internal/companion internal/config internal/server/config.go internal/server/config_test.go internal/archcheck/dependency_test.go cmd/mornlea cmd/mornlea-server openspec/changes/m5a-companion-entity-chat/tasks.md
git commit -m "feat: 定义 M5A 伙伴身份与配置"
~~~

Expected: review clean，提交不含其他文件。

---

### Task 3: 协议 v16 与伙伴/聊天消息

**Files:**
- Create: `internal/network/message_companion.go`
- Create: `internal/network/message_companion_test.go`
- Create: `internal/network/message_companion_fuzz_test.go`
- Modify: `internal/network/login.go`
- Modify: `internal/network/packet.go`
- Modify: `internal/network/registry.go`
- Modify: `internal/network/codec.go`
- Modify: `internal/network/codec_client.go`
- Modify: `internal/network/codec_server.go`
- Modify: `internal/network/message_test.go`
- Modify: `internal/network/benchmark_test.go`
- Modify: `internal/network/codec_invalid_test.go`
- Modify: `internal/network/codec_golden_test.go`
- Modify: `internal/network/registry_test.go`
- Modify: `internal/network/packet_test.go`
- Modify: `internal/network/worldtime_test.go`
- Modify: `internal/network/drop_test.go`
- Modify: `internal/network/memory_test.go`
- Modify: `internal/network/login_tcp_regression_test.go`
- Modify: `internal/network/transport_consistency_test.go`
- Modify: `cmd/mornlea/app_protocol_test.go`
- Modify: `cmd/mornlea-server/main_test.go`
- Modify: `internal/archcheck/dependency_test.go`
- Modify: `openspec/changes/m5a-companion-entity-chat/tasks.md`

**Interfaces:**
- Consumes: `companion.ID`、`core.PlayerID`、`mgl32.Vec3`
- Produces:

~~~go
const ProtocolVersion uint32 = 16
type ChatCommand struct { Text string }
type ChatEventKind uint8
const ( ChatEventAccepted ChatEventKind = 1; ChatEventRejected ChatEventKind = 2 )
type ChatRejectReason uint8
const ( ChatRejectNone ChatRejectReason = iota; ChatRejectInvalidFormat; ChatRejectUnknownCompanion )
type ChatEvent struct {
	EventID uint64
	PlayerID core.PlayerID
	PlayerName string
	CompanionID companion.ID
	CompanionName string
	Kind ChatEventKind
	RejectReason ChatRejectReason
	Command string
}
type CompanionSpawn struct {
	ID companion.ID; Name string; Tick uint64; Dimension core.DimensionID
	Position mgl32.Vec3; Yaw, Pitch float32
}
type CompanionState struct {
	ID companion.ID; Dimension core.DimensionID; Position mgl32.Vec3
	Yaw, Pitch float32; Reset bool
}
type CompanionStates struct { Tick uint64; States []CompanionState }
type CompanionDespawn struct { ID companion.ID }
~~~

固定 IDs：Client ChatCommand=12；Server ChatEvent=16、Spawn=17、States=18、Despawn=19。

- [ ] **Step 1: 写 registry/边界/原子 decode RED**

~~~go
func TestCompanionMessageIDsAreAppendOnly(t *testing.T) {}
func TestProtocolMessageShapesImplementSealedInterfaces(t *testing.T) {}
func TestProtocolV16AcceptsAndV15Rejects(t *testing.T) {}
func TestChatCommandAccepts1024BytesAndRejects1025(t *testing.T) {}
func TestCompanionSpawnAndChatEventStringBoundaries(t *testing.T) {}
func TestCompanionMessagesHaveFixedMaximumWireLengths(t *testing.T) {}
func TestCompanionStatesRejectsFiveDuplicateOrUnsortedAtomically(t *testing.T) {}
func FuzzCompanionMessageCodec(f *testing.F) {}
func BenchmarkChatCommandCodec(b *testing.B) {}
func BenchmarkCompanionMessageCodec(b *testing.B) {}
~~~

固定最大编码长度：ChatCommand=1026、CompanionSpawn=178、CompanionStates=173、ChatEvent=1328 bytes。分别覆盖 PlayerName/CompanionName 的 32 rune/128 UTF-8 bytes、Command 的 1024 bytes 和每个上限+1；decoder 必须先检查 count/length 再分配。消息全集断言从 7 client/13 server 精确更新为 8/17。

- [ ] **Step 2: 运行 RED**

~~~bash
go test ./internal/network ./cmd/mornlea -run 'Test(Companion|Chat|Protocol|Handshake)' -race -count=1
~~~

Expected: FAIL，version 和消息不存在。

- [ ] **Step 3: 实现最小 codec**

ChatCommand 要求 1..1024 UTF-8 bytes、无 NUL/Unicode control、无首尾空白。所有 ChatEvent 都要求完整 player 身份；Accepted 还要求完整 companion 身份、Reason=None、Command 非空；InvalidFormat 的 companion ID/name/command 为空；UnknownCompanion 的 companion ID/command 为空，只保留合法目标名称。CompanionStates 为 1..4 且 ID 严格升序。所有 decoder 先写局部变量、完整 Valid 后再返回，不允许部分应用。

- [ ] **Step 4: GREEN、fuzz 与 mutation**

~~~bash
go test ./internal/network ./cmd/mornlea ./cmd/mornlea-server -run 'Test(Companion|Chat|Protocol|Handshake|Memory|TCP)' -race -count=1
go test ./internal/network -run '^$' -fuzz FuzzCompanionMessageCodec -fuzztime=5s
go test ./internal/network -run '^$' -bench 'Benchmark(Companion|ChatCommand)' -benchmem -count=5
go test ./internal/archcheck -count=1
~~~

Expected: PASS；benchmark 覆盖 ChatCommand 和四种 server 消息的 encode/decode，数值只记报告。临时把 States 上限改为 5，五条拒绝测试 RED；恢复后 PASS。

- [ ] **Step 5: 校验、评审、勾选并提交**

~~~bash
go test ./internal/network ./cmd/mornlea ./cmd/mornlea-server -race -count=1
go vet ./internal/network ./cmd/mornlea ./cmd/mornlea-server
test -z "$(gofmt -l internal/network cmd/mornlea cmd/mornlea-server)"
openspec validate --all --strict --no-interactive
git diff --check
git add internal/network internal/archcheck/dependency_test.go cmd/mornlea/app_protocol_test.go cmd/mornlea-server/main_test.go openspec/changes/m5a-companion-entity-chat/tasks.md
git commit -m "feat: 升级 v16 伙伴与聊天协议"
~~~

Expected: review 明确确认旧 ID 未变、长度先验和 Memory/TCP 同构。

---

### Task 4: Companion schema v1 编解码

**Files:**
- Create: `internal/storage/companion_types.go`
- Create: `internal/storage/companion_codec.go`
- Create: `internal/storage/companion_codec_test.go`
- Create: `internal/storage/companion_codec_fuzz_test.go`
- Create: `internal/storage/testdata/companions-v1.bin`
- Modify: `internal/archcheck/dependency_test.go`
- Modify: `openspec/changes/m5a-companion-entity-chat/tasks.md`

**Interfaces:**
- Consumes: `companion.Body`、既有 inventory/item codec 语义、CRC32C helper
- Produces:

~~~go
var ErrCompanionsNotFound = errors.New("storage: companions not found")
type StoredCompanions struct {
	Revision uint64
	Records []companion.Body
}
type CompanionSave struct {
	Revision uint64
	Records []companion.Body
}
type CompanionStore interface {
	LoadCompanions(context.Context) (StoredCompanions, error)
	SaveCompanions(context.Context, CompanionSave) error
}
const maxCompanionFileLength = 32 + companion.MaxStored*221 // 14176 bytes
~~~

MCAI v1 固定布局：

~~~text
0..3   "MCAI"
4..7   envelope uint32 = 1
8..11  schema uint32 = 1
12..19 aggregate revision uint64 > 0
20..23 count uint32 <= 64
24..27 payloadBytes uint32 == count*221
28..31 CRC32C(header[8:28] + payload)
32..   records，按 CompanionID 严格升序

record = ID16 + dimension4 + position12 + yaw4 + pitch4
       + selected1 + 36*(item uint16 + count uint8 + durability uint16) = 221 bytes
~~~

- [ ] **Step 1: 写 round-trip/golden/损坏 RED**

~~~go
func TestCompanionCodecV1RoundTripAndGolden(t *testing.T) {}
func TestCompanionCodecRejectsCRCTruncationFutureVersionAndOversizedRecords(t *testing.T) {}
func TestCompanionCodecRejectsDuplicateOrUnsortedIDs(t *testing.T) {}
func TestCompanionCodecDoesNotPersistNameTaskOrPersona(t *testing.T) {}
func TestCompanionCodecV1PreservesWornToolDurability(t *testing.T) {}
func FuzzDecodeCompanions(f *testing.F) {}
~~~

固定 fixture 含两个 ID 乱序输入、不同维度/位置/yaw/pitch/背包，编码结果必须 canonical 排序且不改原切片。

- [ ] **Step 2: 运行 RED**

~~~bash
go test ./internal/storage -run 'TestCompanionCodec' -race -count=1
~~~

Expected: FAIL，codec/types 不存在。

- [ ] **Step 3: 实现有界 codec**

解码在分配前拒绝 `count>64`，检查精确 payload 长度和 CRC 后再分配；拒绝 NaN/Inf、非法维度/pitch/inventory。编码 clone 后按 ID 排序，重复 ID 返回错误。future schema 返回 `ErrFutureVersion`，其他结构损坏包装 `ErrCorrupt`。

- [ ] **Step 4: 生成 golden、GREEN 与 mutation**

fixture 测试复用仓库既有 `-update-storage-fixtures` 开关写入 binary，随后普通测试必须 byte-equal；不得用文本 patch 或 shell 重定向写 binary。

~~~bash
go test ./internal/storage -run '^TestCompanionCodecV1RoundTripAndGolden$' -update-storage-fixtures
go test ./internal/storage -run 'TestCompanionCodec' -race -count=1
go test ./internal/storage -run '^$' -fuzz FuzzDecodeCompanions -fuzztime=5s
go test ./internal/archcheck -count=1
shasum -a 256 internal/storage/testdata/companions-v1.bin
~~~

Expected: PASS。临时移除预分配前 count guard，oversize test RED；恢复后 PASS。

- [ ] **Step 5: 校验、评审、勾选并提交**

~~~bash
go test ./internal/storage -race -count=1
go vet ./internal/storage
test -z "$(gofmt -l internal/storage)"
openspec validate --all --strict --no-interactive
git diff --check
git add internal/storage/companion_types.go internal/storage/companion_codec.go internal/storage/companion_codec_test.go internal/storage/companion_codec_fuzz_test.go internal/storage/testdata/companions-v1.bin internal/archcheck/dependency_test.go openspec/changes/m5a-companion-entity-chat/tasks.md
git commit -m "feat: 定义伙伴存档 schema v1"
~~~

Expected: review 核对 32-byte header、221-byte record、future/CRC/aliasing 后 clean。

---

### Task 5: Memory/Disk CompanionStore 与原子替换

**Files:**
- Create: `internal/storage/companion_store_test.go`
- Modify: `internal/storage/types.go`
- Modify: `internal/storage/memory.go`
- Modify: `internal/storage/disk.go`
- Modify: `internal/storage/backup_test.go`
- Modify: `openspec/changes/m5a-companion-entity-chat/tasks.md`

**Interfaces:**
- Consumes: Task 4 `CompanionStore`、`StoredCompanions`、`CompanionSave`，现有 `replaceFileAtomicallyWithPatternAndHooks`
- Produces:

~~~go
type WorldStore interface {
	Store
	PlayerStore
	CompanionStore
}
~~~

- [ ] **Step 1: 写共享 contract 与故障 RED**

~~~go
func TestCompanionStoreContract(t *testing.T) {
	// 对 MemoryStore/DiskStore 跑同一组 missing、round-trip/no-alias、
	// revision/idempotency/conflict、context、close 断言。
}
func TestDiskCompanionAtomicReplaceKeepsOldFileOnFailure(t *testing.T) {}
func TestDiskCompanionOversizedFileIsCorruptAndSaveDoesNotOverwriteIt(t *testing.T) {}
func TestWorldBackupIncludesCompanionFileButSkipsTemporaryFiles(t *testing.T) {}
~~~

- [ ] **Step 2: 运行 RED**

~~~bash
go test ./internal/storage -run 'Test(CompanionStore|DiskCompanion|WorldBackupIncludesCompanion)' -race -count=1
~~~

Expected: FAIL，store 尚未实现。

- [ ] **Step 3: 实现最小聚合存储**

`MemoryStore` 保存单份 encoded bytes/revision，读写 clone；沿用既有生命周期语义，Close 只要求幂等。`DiskStore` 固定根文件名 `companions.ai`，post-close API 返回 `os.ErrClosed`，并复用原子替换 helper：

~~~text
lower revision -> ErrRevisionConflict
same revision + identical canonical bytes -> nil
same revision + different bytes -> ErrRevisionConflict
higher revision -> temp write + file Sync + Rename + parent Sync
~~~

`readCompanionFile` 必须像 player 文件一样用 `io.LimitReader(file, maxCompanionFileLength+1)`，合并 Close 错误，物理文件超过 14,176 bytes 时包装 `ErrCorrupt`。Save 必须先有界读取/验证正式文件；正式文件损坏、future 或超大时原样报错且不覆写。现有备份已复制所有正规根文件并跳过 `.*.tmp-*`，这里只加测试证明 `companions.ai` 被包含、temp 被忽略，不改备份生产代码。不得新建 repository/factory。

- [ ] **Step 4: GREEN 与原子 mutation**

~~~bash
go test ./internal/storage -run 'Test(CompanionStore|DiskCompanion|WorldBackupIncludesCompanion)' -race -count=1
go test ./internal/storage -race -count=1
~~~

Expected: PASS。临时跳过正式文件验证并覆写损坏文件，保护测试 RED；恢复后 PASS。

- [ ] **Step 5: 校验、评审、勾选并提交**

~~~bash
go vet ./internal/storage
test -z "$(gofmt -l internal/storage)"
go test ./internal/archcheck -count=1
openspec validate --all --strict --no-interactive
git diff --check
git add internal/storage openspec/changes/m5a-companion-entity-chat/tasks.md
git commit -m "feat: 持久化伙伴聚合状态"
~~~

Expected: review 核对 fd close、parent sync、backup、no-alias 后 clean。

---

### Task 6: sim 静态伙伴、出生与 3×3 兴趣

**Files:**
- Create: `internal/sim/companion.go`
- Create: `internal/sim/companion_test.go`
- Modify: `internal/sim/engine.go`
- Modify: `internal/sim/engine_step.go`
- Modify: `internal/sim/engine_subscription.go`
- Modify: `internal/sim/spawn.go`
- Modify: `internal/sim/command.go`
- Modify: `internal/sim/bench_test.go`
- Modify: `internal/archcheck/dependency_test.go`
- Modify: `openspec/changes/m5a-companion-entity-chat/tasks.md`

**Interfaces:**
- Consumes: `companion.Definition`、`companion.Body`、`physics.State`、`spawnCandidates`、`spawnCandidateChunks`
- Produces:

~~~go
type CompanionRestore struct {
	ID companion.ID
	Body *companion.Body
	SpawnDimension core.DimensionID
	SpawnAnchor core.ChunkPos
}
type CompanionUpdate struct {
	ID companion.ID
	Dimension core.DimensionID
	State physics.State
	Yaw, Pitch float32
	Reset bool
}
func (engine *Engine) RegisterCompanion(CompanionRestore)
func (engine *Engine) CompanionBodies() []companion.Body
// TickResult 新增 Companions []CompanionUpdate。
~~~

- [ ] **Step 1: 写实体、出生、兴趣 RED**

~~~go
func TestCompanionsAreSeparateSortedIdleActors(t *testing.T) {}
func TestCompanionRestoreWaitsForAllCollisionChunksBeforeFallback(t *testing.T) {}
func TestCompanionRestoreChunkInitiallyMissingThenRestores(t *testing.T) {}
func TestCompanionSpawnRetriesAfterFailedChunkRevisionChanges(t *testing.T) {}
func TestCompanionSpawnSearchRetainsOnlyThreeByThreeCandidateChunks(t *testing.T) {}
func TestCompanionInterestIsThreeByThreeAndDoesNotConsumeSessions(t *testing.T) {}
func TestEightPlayersAndFourCompanionsRemainIndependentInEngine(t *testing.T) {}
~~~

乱序注册后多 tick 输出必须 ID 升序、位置不动；碰撞中的 restore 必须复用既有碰撞检查并回退出生搜索。

- [ ] **Step 2: 运行 RED**

~~~bash
go test ./internal/sim -run 'Test(Companion|EightPlayersAndFourCompanions)' -race -count=1
~~~

Expected: FAIL，Engine 无 companion map。

- [ ] **Step 3: 实现最小 companionState**

~~~go
type companionState struct {
	id companion.ID
	dimension core.DimensionID
	body physics.State
	yaw, pitch float32
	inventory core.Inventory
	active, reset bool
	restoreCandidates []restoreCandidate
	nextRestore int
	restoreWanted map[core.ChunkKey]struct{}
	spawnCandidates []spawnColumn
	spawnChunks []core.ChunkPos
	spawnWanted map[core.ChunkPos]struct{}
	spawnIndex int
	exhausted bool
	exhaustedRevisions []uint64
}
~~~

不要抽 `actorState`，不要接 player movement/mining/death。只复用现有私有 `restoreCandidate`/spawn helper 和玩家的 revision-retry 模式：restore 所需区块未齐时保持 pending；全部 ready 后才验证碰撞并决定恢复或回退；acquire/generate 失败按 observed revision 隔离，revision 改变后允许重试。注册最多 4、重复 ID panic。新伙伴和无效 restore 使用固定 block radius 16，使候选区块最多 3×3；pending restore/spawn wanted 都并入 subscription union，active 伙伴只并入脚下 chunk 周围 dx/dz=-1..1，不伪造 SessionID。`subscriptionDistanceSquared` 对伙伴独占区块也按最近 restore/spawn anchor 或 active 伙伴位置计算，不能把它们一律降为无穷远。

- [ ] **Step 4: GREEN、mutation 与记录**

~~~bash
go test ./internal/sim -run 'Test(Companion|EightPlayersAndFourCompanions)' -race -count=1
go test ./internal/sim -run '^$' -bench 'Benchmark.*Step' -benchmem -count=5
~~~

Expected: tests PASS，benchmark 仅记录。临时把兴趣半径改为 2，3×3 测试 RED；恢复后 PASS。

- [ ] **Step 5: 校验、评审、勾选并提交**

~~~bash
go test ./internal/sim -race -count=1
go vet ./internal/sim
test -z "$(gofmt -l internal/sim)"
go test ./internal/archcheck -count=1
openspec validate --all --strict --no-interactive
git diff --check
git add internal/sim internal/archcheck/dependency_test.go openspec/changes/m5a-companion-entity-chat/tasks.md
git commit -m "feat: 加入服务端权威静态伙伴"
~~~

Expected: review 核对 tick 顺序、排序、固定工作上限和无 speculative abstraction。

---

### Task 7: 单聚合伙伴持久化 worker

**Files:**
- Create: `internal/server/companion_persistence.go`
- Create: `internal/server/companion_persistence_test.go`
- Modify: `openspec/changes/m5a-companion-entity-chat/tasks.md`

**Interfaces:**
- Consumes: `storage.CompanionStore`、`StoredCompanions`、`CompanionSave`、`companion.Body`
- Produces:

~~~go
func newCompanionPersistence(storage.CompanionStore, storage.StoredCompanions, Config) *companionPersistence
func (*companionPersistence) Observe([]companion.Body)
func (*companionPersistence) Poll(uint64) error
func (*companionPersistence) Flush(context.Context) error
func (*companionPersistence) Close()
~~~

- [ ] **Step 1: 写调度 RED**

~~~go
func TestCompanionPersistenceCoalescesToOneInflightAggregate(t *testing.T) {}
func TestCompanionPersistencePreservesInactiveRecords(t *testing.T) {}
func TestCompanionPersistenceSaveFailureRetainsDirtyAndRetriesAtTick(t *testing.T) {}
func TestCompanionPersistenceFlushFailureCanBeRetried(t *testing.T) {}
func TestCompanionPersistenceDoesNotHoldMutexDuringStoreSave(t *testing.T) {}
func TestCompanionPersistenceChangeDuringInflightRemainsDirty(t *testing.T) {}
func TestCompanionPersistenceRetryDoesNotAliasStoreSaveInput(t *testing.T) {}
func TestCompanionPersistenceFlushWaitsForInflightAndWritesLatestOnce(t *testing.T) {}
~~~

- [ ] **Step 2: 运行 RED**

~~~bash
go test ./internal/server -run 'TestCompanionPersistence' -race -count=1
~~~

Expected: FAIL，persistence 不存在。

- [ ] **Step 3: 实现固定容量 worker**

只建一个 goroutine、`jobs chan CompanionSave` capacity 1、`completions chan result` capacity 1。生命周期 context 必须由内部 `context.WithCancel(context.Background())` 创建，绝不继承 `NewHost`/signal context；`NewHost` 的 context 只用于 Task 8 的同步 Load。records 最多 64；提交前 clone+排序，job 和 retry 各持有独立 frozen clone。Observe 在 N revision in-flight 时发现新 body 必须保留 newer dirty，N 成功不能清掉它。Poll 返回 completion/save 错误供 Server 记录，并在 autosave/retry 到期且非 in-flight 时发 revision+1。

Flush 先等待并收割当前 in-flight：失败时原样返回并保留同一 frozen revision；成功但 latest 已变化时，只再提交并等待最新 snapshot 一次。所有 `Store.SaveCompanions` 在 mutex 外，恶意 store 修改入参不能污染 latest 或 retry。Close 只在 store 成功 Sync/Close 后 cancel/wait，重复安全。不得抽通用 worker/interface。

- [ ] **Step 4: GREEN 与锁 mutation**

~~~bash
go test ./internal/server -run 'TestCompanionPersistence' -race -count=1
~~~

Expected: PASS。临时将 `SaveCompanions` 放到 mutex 内，锁测试 RED；恢复后 PASS。

- [ ] **Step 5: 校验、评审、勾选并提交**

~~~bash
go test ./internal/server -race -count=1
go vet ./internal/server
test -z "$(gofmt -l internal/server)"
openspec validate --all --strict --no-interactive
git diff --check
git add internal/server/companion_persistence.go internal/server/companion_persistence_test.go openspec/changes/m5a-companion-entity-chat/tasks.md
git commit -m "feat: 调度伙伴状态持久化"
~~~

Expected: review 核对失败保留 dirty、单 in-flight、锁和生命周期。

---

### Task 8: Host 启动合并、恢复与 shutdown

**Files:**
- Create: `internal/server/companion_bootstrap_test.go`
- Modify: `internal/server/host.go`
- Modify: `internal/server/host_integration_test.go`
- Modify: `internal/server/host_lifecycle_test.go`
- Modify: `internal/server/host_shutdown_test.go`
- Modify: `internal/server/host_stats_test.go`
- Modify: `internal/server/host_test_helpers_test.go`
- Modify: `internal/server/config_test.go`
- Modify: `internal/server/block_light_integration_test.go`
- Modify: `internal/server/login_tcp_test.go`
- Modify: `internal/server/material_processing_integration_test.go`
- Modify: `internal/server/multiplayer_restart_test.go`
- Modify: `internal/server/multiplayer_tcp_capacity_test.go`
- Modify: `internal/server/multiplayer_tcp_gameplay_test.go`
- Modify: `internal/server/tcp_integration_helpers_test.go`
- Modify: `internal/server/tcp_restart_integration_test.go`
- Modify: `internal/server/transport_parity_integration_test.go`
- Modify: `internal/server/server.go`
- Modify: `internal/server/shutdown.go`
- Modify: `internal/server/shutdown_test.go`
- Modify: `cmd/mornlea/app_startup.go`
- Modify: `cmd/mornlea/app_dependencies.go`
- Modify: `cmd/mornlea/app_connection_test.go`
- Modify: `cmd/mornlea/multiplayer_benchmark_server.go`
- Modify: `cmd/mornlea-server/main.go`
- Modify: `cmd/mornlea-server/main_test.go`
- Modify: `cmd/mornlea-server/subprocess_test.go`
- Modify: `openspec/changes/m5a-companion-entity-chat/tasks.md`

**Interfaces:**
- Consumes: Tasks 5..7 store/sim/persistence APIs
- Produces:

~~~go
func NewHost(
	ctx context.Context,
	config Config,
	generator Generator,
	store storage.WorldStore,
) (*Host, error)
~~~

`NewWorld` 继续供 AI-disabled benchmark/test 使用；若 `Config.Companions` 非空则 panic 并提示使用 `NewHost`。未导出 `newWorld` 接受可空 `*companionPersistence` 并交给 `Server` 持有。`NewHost` 只用传入 ctx 同步加载/验证 companion 存档，在任何 worker 启动前完成 merge，再创建 persistence、注册 companion 并构造 world；Host 本身不轮询或 Flush companion。

- [ ] **Step 1: 写 bootstrap/merge/失败 RED**

~~~go
func TestNewHostSkipsCompanionStoreWhenAIDisabled(t *testing.T) {}
func TestNewHostRestoresConfiguredBodiesAndPreservesInactiveRecords(t *testing.T) {}
func TestNewHostAddsConfiguredIDWithoutDeletingInactiveRecords(t *testing.T) {}
func TestNewHostRejectsSixtyFifthDistinctStoredOrNewCompanion(t *testing.T) {}
func TestNewHostRejectsCorruptOrFutureCompanionStoreBeforeWorkersStart(t *testing.T) {}
func TestRemovingAllCompanionConfigDisablesAIAndLeavesFileUntouched(t *testing.T) {}
func TestCompanionShutdownFlushFailureIsRetryable(t *testing.T) {}
func TestCompanionShutdownPersistsBodyCreatedByFinalStepBeforeSync(t *testing.T) {}
func TestCompanionShutdownOrdersSaveBeforeStoreSyncAndClose(t *testing.T) {}
~~~

- [ ] **Step 2: 运行 RED**

~~~bash
go test ./internal/server ./cmd/mornlea ./cmd/mornlea-server -run 'Test(NewHost|RemovingAllCompanion|CompanionShutdown)' -race -count=1
~~~

Expected: FAIL，NewHost signature/merge 不满足契约。

- [ ] **Step 3: 实现启动合并**

规则固定：

~~~text
configured empty -> 不 Load、不 Save，AI disabled，原 companions.ai 不动；
configured nonempty -> Load；missing 视为空；corrupt/future 返回 error；
已存配置 ID -> 恢复 body，名称取配置；
新配置 ID -> 从 metadata spawn anchor 搜索，空背包；
未配置旧 ID -> 只保留 inactive record，不注册 sim；
stored 与 configured 新 ID 的去重并集 >64 -> 启动失败，不修改存档；
首次 active body 形成后 Observe，revision 由 persistence 推进。
~~~

`Server.step` 在每次 `engine.Step()` 后、仍持 `stepMu` 时只调用 `companions.Observe(engine.CompanionBodies())` 并非阻塞地 `Poll(tick)`；Store I/O 仍只在 worker 执行，不阻塞 tick。`Server.Shutdown` 的最后一次 drain+`engine.Step()` 后必须再次 Observe，然后在 `store.Sync`/`store.Close` 前 Flush。Flush 失败不得 Close persistence/store，第二次 Shutdown 可重试；成功关闭 store 后才 Close worker。事件测试锁定 `companion-save < store-sync < store-close`。AI disabled 时指针为 nil，不增加 ticker/goroutine。

- [ ] **Step 4: 机械更新 NewHost callers 并 GREEN**

所有 caller 传现有 context 并处理 error；`applicationDependencies.newHost` 与专服 `dependencies.newHost` 都改成第一个参数为 `context.Context` 并返回 `(host, error)`。`NewHost` 失败不关闭 caller-owned store。应用 caller 只关闭自己已打开的 store，专用服务端 caller 同时关闭自己已打开的 listener；不得 double-close。专服必须先把 `effective.CompanionDefinitions()` clone 到 `server.Config.Companions` 再构造 Host。

~~~bash
rg -n '\bNewHost\(' --glob '*.go'
go test ./internal/server ./cmd/mornlea ./cmd/mornlea-server -run 'Test(NewHost|RemovingAllCompanion|CompanionShutdown|Application.*Host)' -race -count=1
~~~

Expected: PASS。临时在 merge 中丢弃 inactive map，preservation test RED；恢复后 PASS。

- [ ] **Step 5: 校验、评审、勾选并提交**

~~~bash
go test ./internal/server ./cmd/mornlea ./cmd/mornlea-server -race -count=1
go vet ./internal/server ./cmd/mornlea ./cmd/mornlea-server
test -z "$(gofmt -l internal/server cmd/mornlea cmd/mornlea-server)"
go test ./internal/archcheck -count=1
openspec validate --all --strict --no-interactive
git diff --check
git add internal/server cmd/mornlea cmd/mornlea-server openspec/changes/m5a-companion-entity-chat/tasks.md
git commit -m "feat: 恢复并保存配置伙伴"
~~~

Expected: review 核对 constructor 清理、AI-disabled rollback、64 条和 shutdown retry。

---

### Task 9: 伙伴网络发布与独立容量

**Files:**
- Create: `internal/server/companion_publication.go`
- Create: `internal/server/companion_publication_test.go`
- Modify: `internal/server/session.go`
- Modify: `internal/server/publication.go`
- Modify: `internal/server/player_publication.go`
- Modify: `internal/server/integration_test.go`
- Modify: `internal/server/multiplayer_memory_integration_test.go`
- Modify: `internal/server/multiplayer_tcp_capacity_test.go`
- Modify: `openspec/changes/m5a-companion-entity-chat/tasks.md`

**Interfaces:**
- Consumes: `sim.TickResult.Companions`、Task 3 wire messages、现有 `SessionWantsChunk` 和 chunk snapshot publication
- Produces:

~~~go
// session 新增：
visibleCompanions map[companion.ID]struct{}

func (server *Server) companionPublicationCandidates(
	current *session,
	updates []sim.CompanionUpdate,
	definitions map[companion.ID]companion.Definition,
) (map[companion.ID]companionVisibleCandidate, bool)
func (server *Server) publishCompanionDespawns(
	current *session,
	candidates map[companion.ID]companionVisibleCandidate,
) bool
func (server *Server) publishCompanionSpawnsAndStates(
	current *session,
	tick uint64,
	candidates map[companion.ID]companionVisibleCandidate,
) bool
~~~

- [ ] **Step 1: 写 snapshot 前置、排序、despawn 和容量 RED**

~~~go
func TestCompanionPublicationWaitsForFootChunkSnapshot(t *testing.T) {}
func TestCompanionPublicationStatesAreSortedAndNewSpawnsSkipCurrentTick(t *testing.T) {}
func TestCompanionPublicationDespawnsOnInterestExit(t *testing.T) {}
func TestEightPlayersAndFourCompanionsUseIndependentServerCapacity(t *testing.T) {}
func TestCompanionPublicationRejectsUnknownDefinitionWithoutPartialVisibility(t *testing.T) {}
~~~

- [ ] **Step 2: 运行 RED**

~~~bash
go test ./internal/server -run 'Test(CompanionPublication|EightPlayersAndFourCompanions)' -race -count=1
~~~

Expected: FAIL，session 无伙伴可见集。

- [ ] **Step 3: 复用玩家 publication 范式**

只抽一个按 `dimension+position` 计算脚下 chunk 的小 helper，让 player/companion 共用；不得建立通用 entity publisher。顺序固定，保持现有“实体 despawn 在 Forget 前、spawn/state 在 snapshot 后”的纪律：

~~~text
RemotePlayerDespawn
CompanionDespawn（离开兴趣）
ForgetChunks
本 tick chunk snapshots / deltas
CompanionSpawn（首次可见且脚下 snapshot 已发送）
一个 CompanionStates（只含 tick 开始时已经可见的实体，ID 严格升序；新 Spawn 从下一 tick 才进入）
其余 player/drop/local state 原顺序
~~~

`visibleCompanions` 独立于 `visiblePlayers`；相同 16 bytes 的 PlayerID/CompanionID 不冲突。`publishSession` 必须先调用 `companionPublicationCandidates`，通过 `server.engine.SessionWantsChunk` 与 snapshotSent 计算候选，并在任何 enqueue/visible mutation 前验证整批 update 都有 definition；未知项立即沿用 `closePublicationSessionLocked`，不能部分可见。随后在 Forget 前调用 despawns、snapshot 后调用 spawns/states；不要为此建立通用 entity publisher。

- [ ] **Step 4: GREEN 与顺序 mutation**

~~~bash
go test ./internal/server -run 'Test(CompanionPublication|EightPlayersAndFourCompanions)' -race -count=1
~~~

Expected: PASS。临时允许 snapshot 前 Spawn，前置测试 RED；恢复后 PASS。

- [ ] **Step 5: 校验、评审、勾选并提交**

~~~bash
go test ./internal/server -race -count=1
go vet ./internal/server
test -z "$(gofmt -l internal/server)"
openspec validate --all --strict --no-interactive
git diff --check
git add internal/server openspec/changes/m5a-companion-entity-chat/tasks.md
git commit -m "feat: 向客户端发布伙伴实体"
~~~

Expected: review 核对 snapshot ordering、一个 batch、独立容量和 outbox 上限。

---

### Task 10: tick 边界聊天寻址与 Memory/TCP parity

**Files:**
- Create: `internal/server/companion_chat.go`
- Create: `internal/server/companion_chat_test.go`
- Modify: `internal/server/server.go`
- Modify: `internal/server/session_ingress.go`
- Modify: `internal/server/publication.go`
- Modify: `internal/server/shutdown.go`
- Modify: `internal/server/multiplayer_memory_integration_test.go`
- Modify: `internal/server/transport_parity_integration_test.go`
- Modify: `openspec/changes/m5a-companion-entity-chat/tasks.md`

**Interfaces:**
- Consumes: `network.ChatCommand`、`network.ChatEvent`、配置 definition map
- Produces:

~~~go
type incomingChat struct {
	sessionID sim.SessionID
	generation uint64
	command network.ChatCommand
}
type chatDelivery struct {
	event network.ChatEvent
	recipient sim.SessionID // 零表示广播到所有 active sessions。
}
func parseCompanionAddress(string) (name string, command string, reason network.ChatRejectReason)
~~~

parser 对目标调用 Task 2 的 `companion.ValidateName`：32 rune/128 UTF-8 bytes 内、canonical、无 control/空白才可能是 UnknownCompanion；33 rune、129 bytes 或其他非法目标统一生成 InvalidFormat，companion ID/name/command 全空。

- [ ] **Step 1: 写 parser、队列和 parity RED**

~~~go
func TestChatCommandAddressesExactConfiguredCompanionAtTickBoundary(t *testing.T) {}
func TestMalformedOrUnknownCompanionChatRejectsOnlySender(t *testing.T) {}
func TestCompanionAddressNameBoundaryIsThirtyTwoRunesAnd128Bytes(t *testing.T) {}
func TestAcceptedCompanionChatBroadcastsInChannelOrder(t *testing.T) {}
func TestStaleSessionChatGenerationIsDropped(t *testing.T) {}
func TestCompanionChatMemoryTCPParity(t *testing.T) {}
~~~

输入表至少覆盖 `@阿木 挖石头`、缺少 `@`、缺少命令、未知名称、`阿木`/`阿木甲` 精确匹配、多个分隔空白；event command 只保存 trim 后指令。

- [ ] **Step 2: 运行 RED**

~~~bash
go test ./internal/server -run 'Test(ChatCommand|MalformedOrUnknownCompanion|CompanionAddress|AcceptedCompanionChat|StaleSessionChat|CompanionChatMemoryTCPParity)' -race -count=1
~~~

Expected: FAIL，ChatCommand 被现有 translate 拒绝。

- [ ] **Step 3: 实现独立 bounded ingress**

`endpointReader` 在 `translateClientMessage` 前识别 ChatCommand，写入与现有 input capacity 相同的 `incomingChats`；`Server.step` 在 `engine.Step` 前按 channel 顺序 drain、检查 generation、精确名称查找、递增 process-local `EventID`。Accepted 广播在线 active sessions；Invalid/Unknown 只回发送者。不得构造 `sim.Command`，不得修改 companion/body/task。

- [ ] **Step 4: GREEN、parity 与 mutation**

~~~bash
go test ./internal/server -run 'Test(ChatCommand|MalformedOrUnknownCompanion|CompanionAddress|AcceptedCompanionChat|StaleSessionChat|CompanionChatMemoryTCPParity)' -race -count=1
~~~

Expected: PASS。临时将精确名称改为 prefix matching，`阿木`/`阿木甲` 测试 RED；恢复后 PASS。

- [ ] **Step 5: 性能记录、评审、勾选并提交**

新增 `BenchmarkChatRoutingFourCompanions`，预热后每轮处理一个合法命令，使用固定 4 定义且不启 transport：

~~~bash
go test ./internal/server -run '^$' -bench BenchmarkChatRoutingFourCompanions -benchmem -count=5
go test ./internal/server -race -count=1
go vet ./internal/server
test -z "$(gofmt -l internal/server)"
openspec validate --all --strict --no-interactive
git diff --check
git add internal/server openspec/changes/m5a-companion-entity-chat/tasks.md
git commit -m "feat: 在权威 tick 寻址具名伙伴"
~~~

Expected: benchmark 数值仅写报告；review 核对 bounded queue、generation、broadcast/reject 和零 sim mutation。

---

### Task 11: 客户端伙伴插值与 ChatEvent 环

**Files:**
- Create: `internal/client/companions.go`
- Create: `internal/client/companions_test.go`
- Create: `internal/client/chat.go`
- Create: `internal/client/chat_test.go`
- Modify: `internal/client/remote_players.go`
- Modify: `internal/client/remote_players_test.go`
- Modify: `internal/client/remote_interpolation.go`
- Modify: `internal/client/remote_interpolation_test.go`
- Modify: `internal/client/receiver.go`
- Modify: `internal/client/receiver_test.go`
- Modify: `internal/client/presentation_allocation_test.go`
- Modify: `cmd/mornlea/app.go`
- Modify: `cmd/mornlea/app_startup.go`
- Modify: `cmd/mornlea/app_messages.go`
- Modify: `cmd/mornlea/app_frame.go`
- Modify: `cmd/mornlea/interactive.go`
- Modify: `cmd/mornlea/app_lifecycle.go`
- Modify: `cmd/mornlea/app_connection_test.go`
- Modify: `internal/archcheck/dependency_test.go`
- Modify: `openspec/changes/m5a-companion-entity-chat/tasks.md`

**Interfaces:**
- Consumes: Task 3 server messages、现有 interpolation `remoteSnapshot` / `snapshotRing`
- Produces:

~~~go
type CompanionPresentation struct {
	ID companion.ID
	Name string
	Dimension core.DimensionID
	Position mgl32.Vec3
	Yaw, Pitch float32
}
type Companions struct { /* max 4 */ }
func (c *Companions) ApplySpawn(network.CompanionSpawn) error
func (c *Companions) ApplyStates(network.CompanionStates) error
func (c *Companions) ApplyDespawn(network.CompanionDespawn) error
func (c *Companions) Advance(time.Duration)
func (c *Companions) AppendPresentations([]CompanionPresentation) []CompanionPresentation
func (c *Companions) Reset()

type ChatEvents struct { /* ring 32 */ }
func (c *ChatEvents) Apply(network.ChatEvent) error
func (c *ChatEvents) Events([]network.ChatEvent) []network.ChatEvent
func (c *ChatEvents) Reset()
~~~

- [ ] **Step 1: 写原子镜像 RED**

~~~go
func TestCompanionsSpawnStatesInterpolateDespawnAndReset(t *testing.T) {}
func TestCompanionsRejectDuplicateUnknownStaleAndFiveAtomically(t *testing.T) {}
func TestCompanionsRejectStateAtSpawnTickAtomically(t *testing.T) {}
func TestCompanionsPresentInIDOrder(t *testing.T) {}
func TestChatEventsKeepLatestThirtyTwoInEventOrder(t *testing.T) {}
func TestChatEventsRejectDuplicateOrStaleWithoutMutation(t *testing.T) {}
func TestApplicationRoutesCompanionAndChatMessagesAndResetsOnDisconnect(t *testing.T) {}
func TestApplicationAdvancesCompanionsExactlyOnceInFrameAndInteractiveLoops(t *testing.T) {}
~~~

- [ ] **Step 2: 运行 RED**

~~~bash
go test ./internal/client ./cmd/mornlea -run 'Test(Companion|ChatEvents|ApplicationRoutesCompanion|ApplicationAdvancesCompanions)' -race -count=1
~~~

Expected: FAIL，镜像不存在。

- [ ] **Step 3: 抽最小 remoteActor 并实现镜像**

把 `remotePlayer` 中仅与运动插值有关的字段/方法抽成未导出 `remoteActor`，玩家和伙伴组合它；不得新建公共 actor 包。每个 CompanionStates 先验证全部 IDs/存在/tick，再推入任何 snapshot。ChatEvents 使用固定 `[32]network.ChatEvent` + start/count，无 heap queue；EventID 只要求严格递增，不要求连续。

`drainServerMessages` 只路由到对应镜像；任何协议错误关闭 endpoint。`closeClientSession` 同时 Reset companions/chat，避免重连残影。`frame` 和 `runInteractive` 两条真实帧路径都必须与 RemotePlayers 同帧、恰好一次调用 Companions.Advance；不要把推进隐藏进渲染转换函数。

本任务首次引入 `client -> companion`，同提交只登记这一条真实 archcheck 边，不预授权 render 或未来 AI 层依赖。

- [ ] **Step 4: GREEN、allocation 与 mutation**

~~~bash
go test ./internal/client ./cmd/mornlea -run 'Test(Companion|ChatEvents|ApplicationRoutesCompanion|ApplicationAdvancesCompanions)' -race -count=1
go test ./internal/client -run 'TestCompanionPresentationHotPathAllocations' -count=1
go test ./internal/archcheck -count=1
~~~

Expected: PASS，预热后 Advance+AppendPresentations 为 0 alloc。临时逐项 Apply 再校验 batch，原子拒绝测试 RED；恢复后 PASS。

- [ ] **Step 5: 校验、评审、勾选并提交**

~~~bash
go test ./internal/client ./cmd/mornlea -race -count=1
go vet ./internal/client ./cmd/mornlea
test -z "$(gofmt -l internal/client cmd/mornlea)"
openspec validate --all --strict --no-interactive
git diff --check
git add internal/client internal/archcheck/dependency_test.go cmd/mornlea openspec/changes/m5a-companion-entity-chat/tasks.md
git commit -m "feat: 镜像伙伴与聊天事件"
~~~

Expected: review 核对 batch atomicity、ring bound、单次 Advance、disconnect reset。

---

### Task 12: 复用 Avatar/NameTag renderer，并升级 benchmark scenario v16

**Files:**
- Modify: `internal/render/avatar.go`
- Modify: `internal/render/avatar_test.go`
- Modify: `internal/render/name_tag.go`
- Modify: `internal/render/name_tag_test.go`
- Modify: `internal/render/dynamic_upload_test.go`
- Modify: `internal/render/hot_path_allocation_test.go`
- Modify: `internal/render/multiplayer_bench_test.go`
- Modify: `cmd/mornlea/app.go`
- Modify: `cmd/mornlea/app_render.go`
- Modify: `cmd/mornlea/app_frame.go`
- Modify: `cmd/mornlea/app_render_test.go`
- Modify: `cmd/mornlea/app_protocol_test.go`
- Modify: `cmd/mornlea/app_connection_test.go`
- Modify: `cmd/mornlea/presentation_conversion_test.go`
- Modify: `cmd/mornlea/multiplayer_benchmark.go`
- Modify: `cmd/mornlea/benchmark.go`
- Modify: `cmd/mornlea/benchmark_scenario_test.go`
- Modify: `cmd/mornlea/benchmark_v5_test.go`
- Modify: `cmd/perfcheck/main.go`
- Modify: `cmd/perfcheck/compare.go`
- Modify: `cmd/perfcheck/helpers_test.go`
- Modify: `cmd/perfcheck/migration_test.go`
- Modify: `cmd/perfcheck/cli_test.go`
- Modify: `cmd/perfcheck/transport_test.go`
- Modify: `openspec/changes/m5a-companion-entity-chat/tasks.md`

**Interfaces:**
- Consumes: `client.CompanionPresentation`、现有 remote player presentations、scenario v15 报告
- Produces:

~~~go
type EntityKind uint8
const (
	EntityPlayer EntityKind = 1
	EntityCompanion EntityKind = 2
	EntityTarget EntityKind = 3
)
type EntityKey struct {
	Kind EntityKind
	ID [16]byte
}
func (renderer *AvatarRenderer) Render(
	encoder gfx.CommandEncoder,
	target, depth gfx.TextureView,
	camera Camera,
	avatars []Avatar,
) error
~~~

`Avatar` 和 `NameTag` 使用 EntityKey 排序，CompanionID 不转换成 PlayerID。`AvatarColor(core.PlayerID)` 保留旧 16-byte FNV 语义；只为 Companion 增加 domain tag 的内部颜色函数，确保全部旧 player 向量和旧 golden 不变。

- [ ] **Step 1: 写容量/键/单 pass RED**

~~~go
func TestEntityKeySeparatesEqualPlayerAndCompanionBytes(t *testing.T) {}
func TestAvatarPlayerPaletteVectorsRemainUnchanged(t *testing.T) {}
func TestAvatarRendererAcceptsElevenAndRejectsTwelveBeforeGPUWrite(t *testing.T) {}
func TestNameTagRendererAcceptsTwelveAndRejectsThirteenBeforeAtlasMutation(t *testing.T) {}
func TestApplicationRendersSevenPlayersAndFourCompanionsInOneAvatarAndNameTagPass(t *testing.T) {}
func TestElevenActorRenderHotPathAllocations(t *testing.T) {}
func TestBenchmarkScenarioV16AccountsForCompanionRendererUploadLayout(t *testing.T) {}
func TestPerfcheckOnlyAuthorizesScenarioV15ToV16(t *testing.T) {}
func TestPerfcheckV16PerformanceRegressionIsRecordOnly(t *testing.T) {}
func TestPerfcheckHistoricalScenariosRemainSameVersionReadable(t *testing.T) {}
~~~

锁定 avatar parts=66、instance bytes=5280、indirect offset=5536、upload bytes=5556；name tags=12、background bytes=768、glyph offset=`align(256+768,256)=1024`、glyph bytes=24576、upload bytes=25600。Task 13 还会固定 Hotbar HUD 新容量、offset 和空聊天帧实际上传长度；scenario v16 测试在 Task 13 才以最终三套布局闭合。overflow 测试必须证明没有 dynamic Write、render pass、atlas Request/Flush、ordered/layout mutation。

- [ ] **Step 2: 运行 RED**

~~~bash
go test ./internal/render ./cmd/mornlea ./cmd/perfcheck -run 'Test(EntityKey|AvatarPlayerPalette|AvatarRendererAcceptsEleven|NameTagRendererAcceptsTwelve|ApplicationRendersSeven|ElevenActor|BenchmarkScenarioV16|Perfcheck)' -race -count=1
~~~

Expected: FAIL，容量仍为 7/8、overflow 会静默截断、scenario 仍为 15。

- [ ] **Step 3: 最小扩容和失败返回**

只扩现有固定 buffer/layout 和输入上限；继续一套 shader、一套 GPU resource、各一个 pass。Avatar 在排序或 GPU write 前检查 `len(avatars)>11` 并返回 error；NameTag 在 atlas Request/Flush、ordered/layout mutation 前检查 `len(tags)>12`。NameTag glyph offset 由通用 256-byte align 公式得出。app 先追加 7 remote，再追加最多 4 companion，最终按 EntityKey 稳定排序；方块目标 name tag 使用 EntityTarget。app frame 与固定 benchmark 都必须处理 Render/Prepare error，不能吞掉。

- [ ] **Step 4: 实现 scenario v16 和唯一迁移**

`scenarioVersion=16`；分辨率、still/flying 时长、运动、采样、指标、绝对阈值、报告结构及固定 7-player 输入都不变，唯一 workload 变化集合是 M5A 已锁定的 Avatar/NameTag/Hotbar HUD 固定上传布局。`perfcheck` 当前只接受显式 `15:16`；v6..v15 报告仍可同版本读取/比较，历史 `14:15` 保留在旧报告和归档证据中，但不再作为当前迁移授权。跨 transport 仍要求同 scenario、同 commit、同硬件；任何性能数值只输出记录，结构缺失、真实 overflow、数据丢失和 I/O 错误仍失败。不得新增 `14:16` 或其他跳版。

- [ ] **Step 5: GREEN、mutation、allocation 与 benchmark**

~~~bash
go test ./internal/render ./cmd/mornlea ./cmd/perfcheck -run 'Test(EntityKey|AvatarPlayerPalette|AvatarRendererAcceptsEleven|NameTagRendererAcceptsTwelve|ApplicationRendersSeven|ElevenActor|BenchmarkScenarioV16|Perfcheck)' -race -count=1
go test ./internal/render -run '^$' -bench 'Benchmark(Avatar|NameTag|Multiplayer)' -benchmem -count=5
~~~

Expected: tests PASS，热路径预热后 0 alloc，benchmark 仅记录。分别临时去掉 Avatar preflight、给 player 颜色加入 kind、把 scenario 改回 15、允许 `14:16`；对应测试必须各自 RED，恢复后 PASS。

- [ ] **Step 6: 校验、评审、勾选并提交**

~~~bash
go test ./internal/render ./cmd/mornlea ./cmd/perfcheck -race -count=1
go vet ./internal/render ./cmd/mornlea ./cmd/perfcheck
test -z "$(gofmt -l internal/render cmd/mornlea cmd/perfcheck)"
openspec validate --all --strict --no-interactive
git diff --check
git add internal/render cmd/mornlea cmd/perfcheck openspec/changes/m5a-companion-entity-chat/tasks.md
git commit -m "feat: 扩容伙伴渲染并升级性能场景"
~~~

Expected: review 确认没有第二套 renderer/shader/resource、旧 player 配色不变、overflow 原子失败、scenario v16 和唯一 `15:16` 与 OpenSpec 一致。

---

### Task 13: 聊天输入与 HUD

**Files:**
- Create: `internal/render/hud/chat.go`
- Create: `internal/render/hud/chat_test.go`
- Create: `cmd/mornlea/chat.go`
- Create: `cmd/mornlea/chat_test.go`
- Modify: `internal/render/hud/renderer.go`
- Modify: `internal/render/hud/layout.go`
- Modify: `internal/render/hud/renderer_test.go`
- Modify: `internal/render/hud/layout_test.go`
- Modify: `internal/client/window.go`
- Create: `internal/client/window_test.go`
- Modify: `cmd/mornlea/app.go`
- Modify: `cmd/mornlea/app_input.go`
- Modify: `cmd/mornlea/interactive.go`
- Modify: `cmd/mornlea/app_lifecycle.go`
- Modify: `cmd/mornlea/interactive_test.go`
- Modify: `cmd/mornlea/app_frame.go`
- Modify: `cmd/mornlea/app_render_test.go`
- Modify: `cmd/mornlea/app_input_test.go`
- Modify: `cmd/mornlea/app_test_helpers_test.go`
- Modify: `openspec/changes/m5a-companion-entity-chat/tasks.md`

**Interfaces:**
- Consumes: Task 11 ChatEvents、`network.ChatCommand`、现有 Hotbar glyph atlas/layout
- Produces:

~~~go
package hud
type ChatOverlay struct {
	Open bool
	Input string
	Lines []string // 调用方提供最近最多 6 条。
}

// cmd/mornlea
const maxChatCommandBytes = 1024
type chatInput struct {
	open bool
	runes [1024]rune
	count int
	bytes int
	overflow bool
}
func (c *chatInput) Open()
func (c *chatInput) Cancel()
func (c *chatInput) Append(rune)
func (c *chatInput) Backspace()
func (c *chatInput) Submit() (network.ChatCommand, bool)
~~~

`applicationWindow` 新增 `DrainTextInput([]rune) ([]rune, bool)`；GLFW char callback 写固定 `[1024]rune` 队列，满时设置 sticky overflow，不能静默发送截断前缀。`KeyBackspace` 追加在既有 iota 末尾并映射 `glfw.KeyBackspace`，不改旧 key 数值。

- [ ] **Step 1: 写 UTF-8、快捷键、布局 RED**

~~~go
func TestChatInputAcceptsChineseAndBackspaceRemovesOneRune(t *testing.T) {}
func TestChatInputCapsUTF8At1024Bytes(t *testing.T) {}
func TestChatPaste1024ASCIIIsAcceptedAnd1025IsNotPartiallySent(t *testing.T) {}
func TestChatOverflowRemainsInvalidAfterBackspaceAndNeverSendsTruncatedPrefix(t *testing.T) {}
func TestTextInputWhileChatClosedIsDrainedAndNeverLeaksIntoNextChat(t *testing.T) {}
func TestWindowMapsBackspaceAndReportsTextOverflow(t *testing.T) {}
func TestChatSubmitTrimsOuterWhitespaceBeforeValidation(t *testing.T) {}
func TestChatOpenSuppressesMovementMiningPlacementInventoryAndHotbar(t *testing.T) {}
func TestChatEnterSendsAndEscapeCancels(t *testing.T) {}
func TestChatCloseRecapturesCursorAndResetsMouseBaseline(t *testing.T) {}
func TestChatInputAndFormattedLinesResetOnDisconnect(t *testing.T) {}
func TestChatEnterDefersToOpenInventoryOrVisibleDebugPanel(t *testing.T) {}
func TestChatEventFormattingIsStableForAcceptedInvalidAndUnknown(t *testing.T) {}
func TestApplicationRendersChatBeforeInventoryConfirmation(t *testing.T) {}
func TestChatOverlayShowsSixEventsAndInputWithinFixedCapacity(t *testing.T) {}
func TestEmptyChatDoesNotAddHUDPassOrAllocation(t *testing.T) {}
func TestChatHUDCapacityAndOffsetsAreIncludedInScenarioV16(t *testing.T) {}
~~~

- [ ] **Step 2: 运行 RED**

~~~bash
go test ./internal/client ./internal/render/hud ./cmd/mornlea -run 'Test(Chat|EmptyChat|TextInput|WindowMaps|ApplicationRendersChat)' -race -count=1
~~~

Expected: FAIL，input/layout 不存在。

- [ ] **Step 3: 实现固定输入与同一 HUD pass**

关闭 chat 时每帧仍 Drain 并丢弃字符/overflow，防止 gameplay 键入残留；Enter 边沿按以下优先级处理：inventory/container 已开则不打开 chat；可见 debug panel 继续消费 Enter 重置当前行；其余情况打开 chat。chat 打开后不再把 Enter 交给 panel，cursor 释放、发送中性移动并抑制 movement/mining/placement/inventory/hotbar。Enter 先 `strings.TrimSpace`，仅在有效非空且未 overflow 时发送；Escape 清空关闭，Backspace 只删除一个 rune，绝不能清除 sticky overflow，因为已经丢失的字符不可恢复。overflow/control/超过 1024 bytes 的输入使本次编辑保持无效，直到 Cancel 或重新 Open；1025-rune 粘贴即使随后 Backspace 也必须整条不发送，不能发送截断前缀。Enter 成功发送或 Escape 取消后重新捕获 cursor，并立即用当前 `CursorPos` 重置 mouse baseline，避免下一帧跳视角。`closeClientSession` 还要 Cancel 未发送输入并清空格式化行缓存，重连不得残留聊天 UI。

HUD 只显示最近 6 条，每行最多 32 rune，超出在第 32 位用 `…`；另显示一条输入。固定增加最多 2 quads 和 448 glyph instances（7*32*阴影2）。用现有 256-byte 对齐公式重新锁定 `maxHotbarQuads`、`maxHotbarGlyphs`、`hotbarGlyphOffset`、`hotbarUploadBytes`，并把最终容量/offset 与空聊天 benchmark 帧实际上传长度写进 scenario v16 测试和 OpenSpec；这是 Task 12 已声明 workload 变化的一部分。所有 text 在一次 atlas Flush 前 Request。把 `app_frame.go` 的 HUD Prepare/Render 调用移出 `inventoryConfirmed` 门控，并把确认状态显式传入 renderer：未确认时不得画伪造的空背包，但聊天仍可见；renderer 内聊天布局也必须位于 inventory validity 早退之外。空 chat 沿用同一个既有 HUD pass，不新增 pass，但固定 offset/上传长度按 v16 记录。

cmd 层把事实事件格式化为稳定中文行，renderer 不依赖 network：Accepted=`玩家名 → 伙伴名：指令`；InvalidFormat=`系统：格式应为 @伙伴名 指令`；UnknownCompanion=`系统：未找到伙伴 目标名`。只格式化最近 6 条；仅在最新 EventID 变化时更新 app-owned 缓存，事件未变的稳态帧复用 `[]string` 和已有字符串。

- [ ] **Step 4: GREEN、mutation 与 allocation**

~~~bash
go test ./internal/client ./internal/render/hud ./cmd/mornlea -run 'Test(Chat|EmptyChat|TextInput|WindowMaps|ApplicationRendersChat)' -race -count=1
go test ./internal/render/hud -run 'TestChatOverlayHotPathAllocations' -count=1
~~~

Expected: PASS。临时用 byte 截断 Backspace，中文测试 RED；恢复 rune 语义后 PASS。

- [ ] **Step 5: 校验、评审、勾选并提交**

~~~bash
go test ./internal/client ./internal/render/... ./cmd/mornlea -race -count=1
go vet ./internal/client ./internal/render/... ./cmd/mornlea
test -z "$(gofmt -l internal/render internal/client/window.go internal/client/window_test.go cmd/mornlea)"
openspec validate --all --strict --no-interactive
git diff --check
git add internal/render/hud internal/client/window.go internal/client/window_test.go cmd/mornlea openspec/changes/m5a-companion-entity-chat/tasks.md
git commit -m "feat: 加入伙伴聊天输入与 HUD"
~~~

Expected: review 核对 1 KiB、Unicode、输入优先级、固定容量和空状态零额外 pass。

---

### Task 14: 无窗口 ai-companion 视觉场景

**Files:**
- Create: `cmd/mornlea/capture_ai_companion_test.go`
- Create after approval: `cmd/mornlea/testdata/golden/ai-companion.png`
- Modify: `cmd/mornlea/capture.go`
- Modify: `cmd/mornlea/capture_scene.go`
- Modify: `cmd/mornlea/capture_oak_grove_test.go`
- Modify: `cmd/mornlea/capture_scene_test.go`
- Modify: `cmd/mornlea/capture_image_test.go`
- Modify: `README.md`
- Modify: `openspec/changes/m5a-companion-entity-chat/tasks.md`

**Interfaces:**
- Consumes: 完整客户端镜像、renderer 和 HUD
- Produces: 保持全部旧顺序、追加在 `oak-grove` 后的末尾 `ai-companion` fixture 与一张批准 golden；visual delta 重写主规格所有旧“末尾/终场景”子句（包括现有 Requirement 与全部 Scenario），最终只让 `ai-companion` 成为末场景

- [ ] **Step 1: 写场景顺序与 fixture RED**

~~~go
func TestCaptureAICompanionIsLastAndDeterministic(t *testing.T) {}
func TestCaptureOakGroveFindsSceneByName(t *testing.T) {}
func TestCaptureTargetBlockFeedbackFindsSceneByName(t *testing.T) {}
func TestCaptureAICompanionClearsPriorClientState(t *testing.T) {}
~~~

fixture 固定世界时间、相机、伙伴维度/位置/yaw/pitch、中文名牌“阿木”、一条 accepted ChatEvent 和打开的 `@阿木 挖石头` 输入；独特 rune 总数不超过 32。进入场景前重置 remote/companion/chat/inventory/panel/furnace/chest、mining overlay、damage feedback/strength、item drops 和其他会参与该帧呈现的共享状态。`TestCaptureAICompanionClearsPriorClientState` 先逐项污染这些字段，再证明 fixture 不依赖前一个 `oak-grove` 或列表顺序。

- [ ] **Step 2: 运行 RED**

~~~bash
go test ./cmd/mornlea -run 'TestCapture(AICompanion|OakGrove|TargetBlock)' -race -count=1
~~~

Expected: FAIL，场景不存在/旧测试仍按索引。

- [ ] **Step 3: 实现场景但不写 golden**

在 `oak-grove` 后 append `ai-companion` 并使其成为新末场景；旧场景测试改按 Name 查找，不继续使用 last/倒数位置。更新 README 场景清单，并在 active visual delta 中明确覆盖主规格每一处旧“末尾/终场景”描述。先只生成候选：

~~~bash
VISUAL_OUT=/private/tmp/mornlea-m5a-ai-companion-candidate make visual-check
~~~

Expected: 非零只因为 `ai-companion.png` 缺失；所有旧场景均按既有最大通道差/差异像素比例双阈值 PASS。任何旧场景超过阈值、artifact、GPU/IO 错误都立即停止，不 update；不得另加逐字节相等门禁。

- [ ] **Step 4: 人工视觉确认点**

用 `view_image` 打开 `/private/tmp/mornlea-m5a-ai-companion-candidate/ai-companion.png`，核对伙伴模型、中文名牌、聊天历史、输入框、无遮挡/裁切/tofu。向用户给出绝对路径和 SHA-256，等待明确确认；未确认不得继续。

- [ ] **Step 5: 确认后写唯一 golden 并双重校验**

~~~bash
VISUAL_OUT=/private/tmp/mornlea-m5a-ai-companion-update make visual-update
test "$(git status --short --untracked-files=all -- cmd/mornlea/testdata/golden)" = \
  "?? cmd/mornlea/testdata/golden/ai-companion.png"
VISUAL_OUT=/private/tmp/mornlea-m5a-ai-companion-check-1 make visual-check
VISUAL_OUT=/private/tmp/mornlea-m5a-ai-companion-check-2 make visual-check
~~~

Expected: status 精确只列 `?? cmd/mornlea/testdata/golden/ai-companion.png`，没有旧 golden 修改；两次 visual-check 都按既有最大通道差/差异像素比例双阈值全场景 PASS。不得追加逐字节相等门禁。

- [ ] **Step 6: 评审、勾选并提交**

独立视觉/代码 review 检查新图和所有场景顺序后：

~~~bash
go test ./cmd/mornlea -race -count=1
go vet ./cmd/mornlea
test -z "$(gofmt -l cmd/mornlea)"
openspec validate --all --strict --no-interactive
git diff --check
git add cmd/mornlea/capture.go cmd/mornlea/capture_scene.go cmd/mornlea/capture_ai_companion_test.go cmd/mornlea/capture_oak_grove_test.go cmd/mornlea/capture_scene_test.go cmd/mornlea/capture_image_test.go README.md cmd/mornlea/testdata/golden/ai-companion.png openspec/changes/m5a-companion-entity-chat/tasks.md
git commit -m "test: 固化 AI 伙伴无窗口场景"
~~~

Expected: 提交只有 capture 文本、单张新 golden 和 checkbox。

---

### Task 15: 累计门禁、主规格同步与归档

**Files:**
- Modify: `openspec/changes/m5a-companion-entity-chat/tasks.md`
- Modify: `AGENTS.md`
- Modify: `CLAUDE.md`
- Modify: `openspec/config.yaml`
- Modify: `README.md`
- Modify: `docs/notes/lan-server.md`
- Modify: `docs/notes/perf-baseline.md`
- Modify: `docs/notes/perf-baseline-m5.md`
- Modify: 新增/修改能力对应的 `openspec/specs/*/spec.md`
- Move: `openspec/changes/m5a-companion-entity-chat` → `openspec/changes/archive/2026-08-13-m5a-companion-entity-chat`
- Create ignored: `.superpowers/sdd/2026-08-13-m5a-companion-entity-chat/final-report.md`
- Create ignored: `.superpowers/sdd/2026-08-13-m5a-companion-entity-chat/final-review.md`

**Interfaces:**
- Consumes: Tasks 1..14 全部提交与报告
- Produces: 归档 change、同步主规格、clean feature branch；不自动 push/PR

- [ ] **Step 1: 冻结 HEAD/范围并跑受影响门禁**

~~~bash
make rust
go test ./internal/companion ./internal/config ./internal/network ./internal/storage ./internal/sim ./internal/server ./internal/client ./internal/render/... ./cmd/mornlea ./cmd/mornlea-server ./cmd/perfcheck -race -count=1
go test ./internal/archcheck -count=1
~~~

Expected: 全部 PASS；`make rust` 使用现有 toolchain，不下载。若 cargo 不可用，记录为环境阻塞并停止，不伪报。

- [ ] **Step 2: 跑 fuzz、golden、微基准与视觉记录**

~~~bash
go test ./internal/network -run '^$' -fuzz FuzzCompanionMessageCodec -fuzztime=10s
go test ./internal/storage -run '^$' -fuzz FuzzDecodeCompanions -fuzztime=10s
go test ./internal/network ./internal/sim ./internal/server ./internal/render ./cmd/mornlea -run '^$' -bench '^Benchmark(CompanionMessageCodec|ChatCommandCodec|EngineStep.*|ChatRoutingFourCompanions|RemoteAvatarNameTag|RemotePresentationConversion)$' -benchmem -count=5
VISUAL_OUT=/private/tmp/mornlea-m5a-final-visual make visual-check
~~~

Expected: fuzz/visual PASS；微基准数值完整写报告但不作阈值判断。确认固定 benchmark 输入仍为 7 players、0 companions，scenario 已诚实标记 v16。

- [ ] **Step 3: 生成完整 scenario v16 Memory/TCP 记录**

记录硬件、供电、load average 和遗留进程作为 provenance，但任何宿主静稳状态都不得成为 producer 前置门禁。使用全新目录、不覆盖基线；报告参数有效时允许重新运行，不绑定一次性路径或授权：

~~~bash
m5a_perf_dir="$(mktemp -d /private/tmp/mornlea-m5a-v16.XXXXXX)"
TERM=xterm-256color zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/mornlea --benchmark --benchmark-transport memory --perf-output '$m5a_perf_dir/memory-v16.json'"
zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/perfcheck --baseline '$m5a_perf_dir/memory-v16.json' --current '$m5a_perf_dir/memory-v16.json'"
TERM=xterm-256color zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/mornlea --benchmark --benchmark-transport tcp --perf-output '$m5a_perf_dir/tcp-v16.json'"
zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/perfcheck --baseline '$m5a_perf_dir/tcp-v16.json' --current '$m5a_perf_dir/tcp-v16.json'"
zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/perfcheck --baseline '$m5a_perf_dir/memory-v16.json' --current '$m5a_perf_dir/tcp-v16.json'"
shasum -a 256 "$m5a_perf_dir/memory-v16.json" "$m5a_perf_dir/tcp-v16.json"
~~~

Expected: 两个 producer 都写出完整 scenario v16、同一 HEAD/硬件的报告；两个自比较和一次显式跨 transport 比较都 PASS。性能数值及宿主静稳状态只写 final report，不作阈值判断；真实 overflow、数据丢失、报告缺字段、身份不一致或 I/O 错误仍失败。不得改写 `docs/notes/perf-baseline*.json`，不得用 M5 v14 做 `14:16` 跳版。

把两份 v16 报告的 HEAD、transport、硬件、scenario、SHA-256 和 provenance 摘要追加到 `docs/notes/perf-baseline-m5.md`，并在 `docs/notes/perf-baseline.md` 顶部当前状态中链接该记录；它们是可重复生成的 record-only 证据，不提升任何基线。两份 JSON 继续只保存在临时目录，现有 M2 v15/M5 v14 基线 JSON 必须逐字节不变。

- [ ] **Step 4: 跑全仓 fresh 门禁**

~~~bash
go test ./... -race -count=1
go vet ./...
test -z "$(gofmt -l .)"
git diff --check
openspec validate --all --strict --no-interactive
~~~

Expected: 全部 PASS，无前台窗口、无 data loss/overflow。

- [ ] **Step 5: fresh 累计独立评审**

使用 `superpowers:requesting-code-review`，评审范围为 Task 1 前一提交至当前 HEAD。必须逐项核对：

~~~text
OpenSpec 行为与实现一致；
M5A 无 planner/HTTP/FIFO/path/action/persona/actorState；
协议 v16 旧 ID 不变、v15 拒绝；
schema v1 无 task/name、64 条和 inactive 保留；
8 players + 4 companions；
tick/order/snapshot/chat parity；
客户端原子镜像、统一 renderer、Unicode UI；
只新增 ai-companion golden；
scenario v16 仅因固定上传布局变化，唯一 15:16 迁移；性能只记录且旧基线未改。
~~~

Expected: Critical/Important/Minor 全部关闭；修复 wave 必须重新跑相称门禁并单独提交。只要 review 修复触及代码、producer 或 scenario workload，就丢弃此前 v16 临时报告，在新的冻结 HEAD 重新执行 Steps 2–4、重算 SHA-256 并更新 tracked provenance；不得把旧 HEAD 报告带入归档。

- [ ] **Step 6: 勾完 active tasks、同步主规格并归档**

先确认 active tasks 中 Task 2..14、累计门禁和累计 review 100% 完成；archive 自身不在 active tasks 内。此时才把 AGENTS/CLAUDE/config 的“当前基线”同步到 M5A、协议 v16、companion schema v1、benchmark scenario v16，并保持 AGENTS 与 CLAUDE byte-equal。再使用 `openspec-sync-specs` 智能合并八份 delta，逐 Requirement 核对主规格；然后使用 `openspec-archive-change`：

~~~bash
openspec status --change m5a-companion-entity-chat --json
openspec validate m5a-companion-entity-chat --strict --no-interactive
openspec validate --all --strict --no-interactive
cmp -s AGENTS.md CLAUDE.md
~~~

归档前同时把 README 与 `docs/notes/lan-server.md` 的当前里程碑/线上协议/scenario 更新为 M5A/v16；性能说明必须写清当前唯一迁移是 `15:16`、v16 只记录且 M2 v15/M5 v14 基线未提升。不得改写历史段落或两份基线 JSON。

归档后：

~~~bash
openspec list --json
openspec validate --all --strict --no-interactive
test -f openspec/changes/archive/2026-08-13-m5a-companion-entity-chat/.openspec.yaml
git diff --check
~~~

Expected: active list 不含该 change，archive tasks 全勾，主规格与 delta 行为等价。

- [ ] **Step 7: 提交归档并最终核验**

~~~bash
git add AGENTS.md CLAUDE.md openspec/config.yaml README.md docs/notes/lan-server.md docs/notes/perf-baseline.md docs/notes/perf-baseline-m5.md openspec/specs
git add -A -- openspec/changes/m5a-companion-entity-chat openspec/changes/archive/2026-08-13-m5a-companion-entity-chat
git diff --cached --name-status
git diff --cached --check
git commit -m "docs: 归档 M5A 伙伴实体与聊天寻址"
git status --short --branch
git log --oneline --decorate -15
~~~

Expected: feature worktree clean；根工作树原有三份日志和 `mcgo` 未变化。写 final report 的 HEAD、所有命令 exit、benchmark 记录、视觉 SHA、review verdict 与归档提交；随后用 `superpowers:finishing-a-development-branch` 让用户选择合并、push/PR 或保留分支。

---

## 自审映射

- 身份/配置/容量：Task 2。
- v16 wire、Memory/TCP message parity：Task 3。
- schema v1、64 条、CRC/future/atomic disk：Tasks 4..5。
- 权威静态实体、出生、3×3、8+4：Task 6。
- runtime retry、inactive 保留、startup/shutdown：Tasks 7..8。
- snapshot/spawn/state/despawn：Task 9。
- `@名称 指令`、tick 顺序、broadcast/reject：Task 10。
- 客户端镜像/插值/reset：Task 11。
- 单 renderer 呈现与 EntityKey：Task 12。
- UTF-8 输入/HUD/动作抑制：Task 13。
- 无窗口人工视觉确认与唯一 golden：Task 14。
- 全仓门禁、累计 review、主规格同步/归档：Task 15。

明确跳过：M5B 的 FIFO/go_to、M5C 的 mine/place/follow、M5D Planner/HTTP/persona/summary、任何认证/加密、通用 actor 抽象、第二套 renderer/shader。相应里程碑开始且有真实调用者时再增加。
