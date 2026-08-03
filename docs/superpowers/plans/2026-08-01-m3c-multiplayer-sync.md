# M3C 多人同步与远端玩家呈现实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让最多 8 个可信局域网玩家同时进入同一权威世界，按区块兴趣同步远端玩家，以 100 ms 缓冲插值和 Unicode 昵称方块人完成呈现，并保持多身份存档、断线和关服行为有界可重试。

**Architecture:** `sim.Engine` 继续只拥有以 `SessionID` 索引的权威玩家状态；`server.Server` 在每个 tick 内按 session 复用区块 subscription 计算玩家兴趣差分，`server.Host` 只负责身份接纳、双索引和持久化。客户端用纯值 `RemotePlayers` roster 验证 spawn/despawn/state 生命周期并输出插值 presentation，独立 `AvatarRenderer` 与 `NameTagRenderer` 在 terrain pass 后消费这些值。

**Tech Stack:** Go 1.26.0（用户现有 GVM）、现有手写二进制 protocol/TCP/Memory transport、`github.com/go-gl/mathgl`、WebGPU `internal/gfx` 抽象、WGSL、`golang.org/x/image/font/opentype` v0.44.0、内嵌 Noto Sans CJK SC、race/fuzz/真实 TCP/无窗口 headless GPU 测试。

## Global Constraints

- 所有本地 Go 命令必须通过 `zsh -ic 'gvm use go1.26.0 >/dev/null && ...'` 使用用户现有 GVM；不得安装另一套 Go。
- 所有新增或修改的文档使用中文；代码标识符、线上 packet 名称和现有英文技术术语保持技术原义。
- `network.ProtocolVersion` 固定提升为 `2`；不解码、不协商、不兼容 v1，v1 `ClientHello` 必须稳定收到服务器版本 2 的 `HandshakeVersionMismatch`。
- 现有 packet ID 不重排：Play serverbound 保持 `0..4`，Play clientbound 保持 `0..6`，仅追加 Spawn=`7`、Despawn=`8`、States=`9`；`LoginAlreadyOnline=7`。
- `RemotePlayerStates` count 固定 `1..7`，按 PlayerID 原始 16 bytes 严格升序；最大逻辑 payload 精确为 `296 bytes`，门禁 `<512 bytes`。
- 客户端永远不接收 `SessionID`；昵称不唯一，所有远端实体和存档索引只使用 UUIDv4 `PlayerID`。
- `MaxPlayers` 只接纳 `1..8`，默认 8；pre-login 仍为 16；同 ID 先返回 `AlreadyOnline`，不同 ID 满员才返回 `ServerFull`。
- 玩家 cache 固定 16，player save workers 固定 2，jobs capacity 固定 16，completions capacity 固定 2；任何输入都不能扩容这些结构。
- cache miss 必须在 Store.LoadPlayer 前原子插入 pending placeholder；所有 Store I/O 必须在 persistence mutex 与 world tick goroutine 之外。
- 单身份最多一个 SavePlayer 在途；失败 revision/value 原样按 `20,40,80,160,320,640,1200 tick` 重试；单次 Flush 不立即重投同一失败 revision。
- 玩家可见必须同时满足双方 Ready、非自己、同维度、目标脚底 chunk 在观察者 wanted set 且该 chunk 已成功发送 snapshot。
- tick publication 相对顺序固定为 Despawn→Forget→snapshot/delta→Spawn→一个 States→本地 PlayerState/rejection；heartbeat 可以穿插但不能改变这些消息彼此顺序。
- 任一 outbox 满、非法 packet、heartbeat timeout 或客户端协议错误只清理对应 session；其他 session 和 world tick 必须继续。
- 远端插值每玩家最多 4 帧，目标延迟 2 tick/100 ms，不预测、不外推；Spawn/Reset/维度变化/相邻位移严格大于 8 格时吸附并重新积累至少 3 帧。
- 方块人最多 7 个，约 `0.6×1.8` 格，无皮肤、肢体动画、碰撞、PvP 或阴影；颜色由 PlayerID 稳定派生。
- 名称 atlas 固定 `1024×1024 R8`、`32×32` cell、1024 slots，slot 0 为 tofu；不驱逐、不扩容，超过 1023 个已分配 glyph 后稳定降级为 tofu。
- 字体固定为上游提交 `f8d157532fbfaeda587e826d4cd5b21a49186f7c` 的 `NotoSansCJKsc-Regular.otf`：16,437,364 bytes，SHA-256 `2c76254f6fc379fddfce0a7e84fb5385bb135d3e399294f6eeb6680d0365b74b`。
- OFL 文件固定为同提交 `Sans/LICENSE`：4,301 bytes，SHA-256 `6a73f9541c2de74158c0e7cf6b0a58ef774f5a780bf191f2d7ec9cc53efe2bf2`。
- `network` 不 import `server/client/sim/storage/render`；`sim` 不 import `network/storage`；`server` 不 import `client/render/gfx`；`render` 不 import `network`；`cmd/mcgod` 不进入 GLFW/WebGPU/font 依赖闭包。
- 自动测试和性能验证不得创建或聚焦前台窗口；仅用户明确人工验收时才允许启动两个 `mcgo` 窗口。
- 每个任务都执行 red→green→refactor、focused `-race -count=1`、独立规格与代码质量评审，并恰好创建一个隔离提交后才进入下一任务。
- 既有门禁不放宽：≥100 FPS、frame p99 `<12 ms`、RSS `<2 GiB`、server tick p99 `<10 ms`、tick max `<50 ms`、physics `0 allocs/op`；同机相对 M3B accepted baseline 回退超过 20% 判红。

## Non-Goals

- 本阶段不增加聊天、皮肤、肢体动画、远端玩家碰撞、PvP、阴影、第三人称或本地玩家模型。
- 本阶段不增加账户认证、密码、公钥挑战、TLS、自动重连、断线续传、服务发现、NAT 穿透或公网部署承诺；PlayerID 仍只是可信局域网内的离线 UUIDv4 身份。
- 本阶段不改世界生成、区块 schema、物理规则或存档选择 UI，不提前实现 M4 生物/实体系统。

## 文件与职责映射

| 路径 | M3C 完成后的职责 |
|---|---|
| `internal/network/packet.go` | protocol v2、稳定登录错误码和封闭 packet 集合 |
| `internal/network/message.go` | 三类远端玩家消息、state 值和严格语义校验 |
| `internal/network/registry.go` | v2 state+direction packet ID 双向注册表 |
| `internal/network/codec.go` | Spawn/Despawn/States 的显式小端 codec 与 1..7 分配门禁 |
| `internal/server/session.go` | session identity 元数据、远端可见 map 和安全 attach/detach |
| `internal/server/player_publication.go` | 玩家兴趣候选、despawn 阶段、spawn/state 阶段与确定性排序 |
| `internal/server/publication.go` | 区块与玩家 publication 的固定线上阶段编排 |
| `internal/server/config.go` | `MaxPlayers` 1..8/default 8 与现有服务器配置 |
| `internal/server/host.go` | pending+Play 身份索引、session 索引、登录和多 session 生命周期 |
| `internal/server/player_persistence.go` | 16-entry cache、身份 lifecycle、快照合并和 Store load |
| `internal/server/player_save_scheduler.go` | 两 worker、两个有界队列、autosave/retry/completion |
| `internal/server/player_flush.go` | 多身份 deterministic Flush 与失败保留 |
| `internal/client/remote_players.go` | 远端 roster 协议状态机与排序 presentation |
| `internal/client/remote_interpolation.go` | 4-slot ring、100 ms target tick、角度插值和吸附 |
| `internal/gfx/gfx.go` / `wgpu.go` | R8Unorm、区域纹理上传和显式 alpha blend |
| `internal/render/font_atlas.go` | 内嵌字体、单 glyph worker、1024-slot atlas 和布局值 |
| `internal/render/avatar.go` | 固定 7 人/42 body-part 方块人 instance 与 depth-write pass |
| `internal/render/name_tag.go` | 昵称 billboard、背景/glyph instance 与 alpha/depth-read pass |
| `cmd/mcgo/app.go` | roster drain/advance、三条 render pass、资源关闭和客户端错误隔离 |
| `internal/server/multiplayer_tcp_integration_test.go` | 2-client 与 8-client 真实 TCP 纵向证明 |
| `internal/server/multiplayer_memory_integration_test.go` | 8-session 手动 2000 tick、transcript/hash parity |
| `cmd/mcgo/benchmark.go` / `internal/client/perf.go` | scenario v6 多人指标、7-avatar/name-tag 固定场景 |
| `docs/notes/perf-baseline.*` | v6 accepted baseline、复现命令和 20% 比较规则 |

## 规格覆盖矩阵

| 设计要求 | 实施任务 |
|---|---|
| protocol v2、IDs、布局、296 bytes、v1 拒绝 | Task 1 |
| Session identity 边界与 sim 隔离 | Task 2 |
| 兴趣集合、generation、publication 顺序、慢客户端隔离 | Task 3 |
| 16-entry 原子 cache、重连和身份 lifecycle | Task 4 |
| 两 worker、队列、retry、失败隔离 | Task 5 |
| MaxPlayers、双索引、同 ID/同名策略、mcgod 参数 | Task 6 |
| disconnect、Flush、关服重试和顺序 | Task 7 |
| 客户端 roster 协议错误与排序 | Task 8 |
| 4 帧/100 ms 插值、yaw wrap、reset/hold | Task 9 |
| R8 区域上传、alpha blend、headless gfx | Task 10 |
| 字体、OFL、glyph worker、atlas/tofu/churn | Task 11 |
| 方块人 | Task 12 |
| Unicode name tags | Task 13 |
| mcgo drain/render/close 集成 | Task 14 |
| 2-client TCP 相互可见、移动、方块、断线 | Task 15 |
| 8-client soak、Memory parity、全员重启恢复 | Task 16 |
| scenario v6、性能/依赖/全量出口门禁 | Task 17 |

---

## Task 1：冻结 protocol v2 与远端玩家 wire

**Files:**
- Modify: `internal/network/packet.go`
- Modify: `internal/network/message.go`
- Modify: `internal/network/registry.go`
- Modify: `internal/network/codec.go`
- Modify: `internal/network/packet_test.go`
- Modify: `internal/network/message_test.go`
- Modify: `internal/network/registry_test.go`
- Modify: `internal/network/codec_test.go`
- Modify: `internal/network/codec_fuzz_test.go`
- Modify: `internal/network/login_test.go`
- Modify: `internal/network/transport_consistency_test.go`
- Modify: `internal/network/benchmark_test.go`
- Modify: `cmd/mcgod/main.go`
- Modify: `cmd/mcgod/main_test.go`

**Interfaces:**
- Consumes: 现有 `core.PlayerID`、`core.NormalizeDisplayName`、`byteEncoder`/`byteDecoder`、`ServerPacket`/`ServerMessage`。
- Produces:

```go
const ProtocolVersion uint32 = 2
const LoginAlreadyOnline LoginRejectCode = 7

type RemotePlayerSpawn struct {
	PlayerID core.PlayerID
	DisplayName string
	ServerTick uint64
	Dimension core.DimensionID
	Position mgl32.Vec3
	Yaw, Pitch float32
}
type RemotePlayerDespawn struct { PlayerID core.PlayerID }
type RemotePlayerStates struct {
	ServerTick uint64
	Players []RemotePlayerState
}
type RemotePlayerState struct {
	PlayerID core.PlayerID
	Dimension core.DimensionID
	Position mgl32.Vec3
	Yaw, Pitch float32
	Reset bool
}
```

- [ ] **Step 1：写 v2 类型、注册表、golden 和拒绝路径的失败测试**

`internal/network/codec_test.go` 固定以下三条精确 wire：

```go
func TestProtocolV2RemotePlayerGolden(t *testing.T) {
	id := mustCodecPlayerID(t)
	tests := []struct {
		packet ServerPacket
		wantID uint32
		wantHex string
	}{
		{RemotePlayerSpawn{PlayerID: id, DisplayName: "陈", ServerTick: 1,
			Dimension: core.Overworld, Position: mgl32.Vec3{1, 2, 3}, Yaw: 4, Pitch: -5}, 7,
			"00112233445546778899aabbccddeeff03e999880100000000000000000000000000803f0000004000004040000080400000a0c0"},
		{RemotePlayerDespawn{PlayerID: id}, 8,
			"00112233445546778899aabbccddeeff"},
		{RemotePlayerStates{ServerTick: 2, Players: []RemotePlayerState{{PlayerID: id,
			Dimension: core.Overworld, Position: mgl32.Vec3{1, 2, 3}, Yaw: 4, Pitch: -5, Reset: true}}}, 9,
			"02000000000000000100112233445546778899aabbccddeeff000000000000803f0000004000004040000080400000a0c001"},
	}
	for _, test := range tests {
		packetID, payload, err := encodeServerControlPayload(StatePlay, test.packet)
		if err != nil || packetID != test.wantID || hex.EncodeToString(payload) != test.wantHex {
			t.Fatalf("%T id=%d payload=%x err=%v", test.packet, packetID, payload, err)
		}
		decoded, err := decodeServerControlPayload(StatePlay, packetID, payload)
		if err != nil || !sameServerPacket(decoded, test.packet) {
			t.Fatalf("round=%#v err=%v", decoded, err)
		}
	}
}
```

同一轮测试还必须固定：`ProtocolVersion==2`、IDs 7/8/9 双向完整、`LoginAlreadyOnline==7`、States count 0/8 拒绝、重复/乱序 ID 拒绝、UUID 非 v4 拒绝、非规范昵称/非有限 float/非法 bool 拒绝、7 条状态 payload 为 296 bytes、v1 ClientHello 返回版本 2 mismatch。`chunk-snapshot-v1.bin` 是区块 schema fixture，不改名也不改字节。

- [ ] **Step 2：运行 focused tests 并确认 RED**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network ./cmd/mcgod -run "TestProtocolV2|TestRemotePlayer|TestLoginReject|TestRunOpensWorld" -count=1'
```

Expected: FAIL，首先报告 `undefined: RemotePlayerSpawn` 或 `ProtocolVersion = 1, want 2`。

- [ ] **Step 3：实现封闭消息、严格校验、ID 7/8/9 和显式 codec**

三种顶层消息同时实现 `serverPacket()` 与 `serverMessage()`。校验必须在分配状态 slice 前读取 count 并拒绝 `count<1 || count>7`；每个 state 固定 41 bytes，最大逻辑 payload 按 `8+1+7*41` 断言。排序直接比较 UUID 原始 bytes：

```go
func (states RemotePlayerStates) Validate() error {
	if len(states.Players) < 1 || len(states.Players) > 7 {
		return errors.New("network: remote player state count is outside 1..7")
	}
	for index, state := range states.Players {
		if err := state.validate(); err != nil {
			return fmt.Errorf("network: remote player state %d: %w", index, err)
		}
		if index > 0 && bytes.Compare(states.Players[index-1].PlayerID[:], state.PlayerID[:]) >= 0 {
			return errors.New("network: remote player states are not strictly sorted")
		}
	}
	return nil
}

func (spawn RemotePlayerSpawn) Validate() error {
	name, err := core.NormalizeDisplayName(spawn.DisplayName)
	if err != nil || name != spawn.DisplayName || !spawn.PlayerID.Valid() ||
		spawn.Dimension != core.Overworld || !finiteVec3(spawn.Position) ||
		!finite32(spawn.Yaw) || !finite32(spawn.Pitch) {
		return errors.New("network: invalid remote player spawn")
	}
	return nil
}
```

在 `serverPacketID`/`serverPacketForID` 追加 7/8/9；codec 按设计字段顺序逐字段写读，Spawn string 使用 `128 bytes/32 runes` 上限。`cmd/mcgod` 删除本地 `protocolVersion=1`，日志直接记录 `network.ProtocolVersion`。

- [ ] **Step 4：运行 network race、fuzz smoke 与 mcgod tests**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network ./cmd/mcgod -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "^$" -fuzz "^FuzzSmallPacketCodec$" -fuzztime=5s'
```

Expected: PASS；fuzz 对任意 state/ID/payload 只返回规范 roundtrip 或有界错误，不 panic、不接受非规范 bytes。

- [ ] **Step 5：提交**

```bash
git add internal/network cmd/mcgod/main.go cmd/mcgod/main_test.go
git commit -m "feat: 定义 M3C 多人协议 v2"
```

---

## Task 2：给 Server session 增加稳定身份元数据

**Files:**
- Modify: `internal/server/session.go`
- Modify: `internal/server/server.go`
- Modify: `internal/server/host.go`
- Create: `internal/server/session_identity_test.go`
- Modify: `internal/server/world_setup_test.go`
- Modify: `internal/server/world_setup_external_test.go`
- Modify: `internal/server/attached_external_test.go`
- Modify: `internal/server/heartbeat_test.go`
- Modify: `internal/server/player_test.go`
- Modify: `internal/server/session_registry_test.go`

**Interfaces:**
- Consumes: Task 1 `core.PlayerID`/规范昵称；现有 `AttachSession(SessionSpec)` 和 `sim.Engine.RegisterPlayer(SessionID, PlayerRestore)`。
- Produces:

```go
type SessionSpec struct {
	ID sim.SessionID
	Generation uint64
	PlayerID core.PlayerID
	DisplayName string
	Endpoint network.ServerEndpoint
	Restore sim.PlayerRestore
}
```

`session` 保存 `playerID`/`displayName`，`Server` 保存 `playerSessions map[core.PlayerID]sim.SessionID` 以防御 direct attach 重复身份；`sim` 的公开值不新增 PlayerID 或昵称。

- [ ] **Step 1：写身份验证、重复身份和 sim 隔离的失败测试**

```go
func TestSessionIdentityMetadataIsValidatedBeforeRegister(t *testing.T) {
	running := NewWorld(registryTestConfig(), playerTestGenerator{}, testStore())
	t.Cleanup(func() { shutdownServerForTest(t, running) })
	_, endpoint := network.NewMemoryPair(8)
	valid := registrySessionSpec(7, 1, endpoint)
	for _, mutate := range []func(*SessionSpec){
		func(spec *SessionSpec) { spec.PlayerID = core.PlayerID{} },
		func(spec *SessionSpec) { spec.DisplayName = "  Chen  " },
		func(spec *SessionSpec) { spec.DisplayName = "" },
	} {
		spec := valid
		mutate(&spec)
		if _, err := running.AttachSession(spec); !errors.Is(err, ErrInvalidSession) {
			t.Fatalf("AttachSession(%+v) error=%v", spec, err)
		}
		if _, ok := running.PlayerStateFor(spec.ID); ok {
			t.Fatal("invalid identity reached sim")
		}
	}
}
```

再固定两个不同 SessionID 不能 attach 相同 PlayerID、detach 后同 PlayerID 可用新 generation 重新 attach、合法 nickname 原样保存在 session、`sim.PlayerUpdate`/`PlayerSnapshot` 的类型没有身份字段。

- [ ] **Step 2：运行 session focused tests 并确认 RED**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TestSessionIdentity|TestSessionRegistryRejectsInvalidAndDuplicateSpecs" -count=1'
```

Expected: FAIL，`SessionSpec.PlayerID` 与 `SessionSpec.DisplayName` 尚未定义。

- [ ] **Step 3：验证并索引身份，集中修复测试 fixture**

`attachSessionLocked` 必须先做纯验证和 duplicate identity 检查，再调用 `engine.RegisterPlayer`：

```go
canonical, err := core.NormalizeDisplayName(spec.DisplayName)
if server.lifecycle != serverRunning || spec.ID == 0 || spec.Generation == 0 ||
	spec.Endpoint == nil || spec.ID == trustedObserverSessionID ||
	!spec.PlayerID.Valid() || err != nil || canonical != spec.DisplayName {
	return nil, ErrInvalidSession
}
if server.sessions[spec.ID] != nil || server.playerSessions[spec.PlayerID] != 0 {
	return nil, ErrSessionExists
}
```

成功 attach 同时登记两个索引；detach 仅在 map 仍指向本 SessionID 时删除 PlayerID 索引。新增测试 helper：

```go
func registrySessionSpec(id sim.SessionID, generation uint64, endpoint network.ServerEndpoint) SessionSpec {
	return SessionSpec{
		ID: id, Generation: generation,
		PlayerID: core.PlayerID{0, 0, 0, 0, 0, 0, 0x40, 0, 0x80, 0, 0, 0, 0, 0, 0, byte(id)},
		DisplayName: fmt.Sprintf("Player-%d", id), Endpoint: endpoint, Restore: testRestore(),
	}
}
```

Host attach 时把 `pending.Identity()` 的规范值填入 `SessionSpec`。Trusted observer 保持无 PlayerID 且不登记身份索引。

- [ ] **Step 4：运行 server 全包 race**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server ./internal/sim -race -count=1'
```

Expected: PASS；所有旧 direct-attach 测试改用合法唯一 identity，sim 包没有新增 network/identity 依赖。

- [ ] **Step 5：提交**

```bash
git add internal/server
git commit -m "feat: 为服务端会话绑定稳定身份"
```

---

## Task 3：实现按 session 的玩家兴趣 publication

**Files:**
- Create: `internal/server/player_publication.go`
- Create: `internal/server/player_publication_test.go`
- Modify: `internal/server/session.go`
- Modify: `internal/server/publication.go`
- Modify: `internal/server/publication_test.go`
- Modify: `internal/server/session_registry_test.go`
- Modify: `internal/server/player_test.go`

**Interfaces:**
- Consumes: Task 1 三类消息；Task 2 session identity；现有 `sim.TickResult.Players`、`Engine.SessionWantsChunk`、`publication.snapshotSent`。
- Produces:

```go
type visiblePlayer struct {
	Session sim.SessionID
	Generation uint64
}

type visibleCandidate struct {
	PlayerID core.PlayerID
	DisplayName string
	Session sim.SessionID
	Generation uint64
	Update sim.PlayerUpdate
}

func (s *Server) publishRemoteDespawns(*session, map[sim.SessionID]sim.PlayerUpdate) bool
func (s *Server) publishRemoteSpawnsAndStates(*session, uint64, map[sim.SessionID]sim.PlayerUpdate) bool
```

- [ ] **Step 1：写兴趣矩阵、generation 和顺序的失败测试**

`player_publication_test.go` 定义一个两 session 手动 Step harness，固定下列完整矩阵：双方 Ready 才可见；自己永不出现；异维度不可见；目标脚底 chunk 不 wanted 时，全局 join/leave 不产生 Spawn/Despawn；目标脚底 chunk wanted 但 `snapshotSent=false` 不 Spawn；snapshot 成功后同 tick Snapshot→Spawn；离开时 Despawn→Forget；同 PlayerID 新 generation 产生 Despawn→Spawn；新 Spawn 不进入当 tick States；稳定目标只进入一个按 PlayerID 排序的 batch。脚底 chunk 用 `floor(position.X/Z)/16`，另测 `x=-0.1` 落在 chunk -1，禁止截断到 0。

核心顺序断言使用显式类型序列：

```go
func assertRemoteOrder(t *testing.T, messages []network.ServerMessage, want []reflect.Type) {
	t.Helper()
	got := make([]reflect.Type, len(messages))
	for index, message := range messages { got[index] = reflect.TypeOf(message) }
	if !reflect.DeepEqual(got, want) { t.Fatalf("order=%v want=%v", got, want) }
}

func TestRemotePlayerPublicationOrder(t *testing.T) {
	h := newRemotePublicationHarness(t)
	h.makeBothReadyAndVisible()
	h.moveTargetAcrossUnsentChunkAndPublishSnapshot()
	assertRemoteOrder(t, h.observerMessages(), []reflect.Type{
		reflect.TypeOf(network.RemotePlayerDespawn{}),
		reflect.TypeOf(network.ForgetChunks{}),
		reflect.TypeOf(network.ChunkSnapshot{}),
		reflect.TypeOf(network.RemotePlayerSpawn{}),
		reflect.TypeOf(network.PlayerState{}),
	})
}
```

同文件还断言 8 sessions 下每观察者最多 7 states、payload 296 bytes、slow observer detach 后其余 7 个仍收到本地 PlayerState。

- [ ] **Step 2：运行 publication tests 并确认 RED**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TestRemotePlayer|TestSessionRegistrySlowSession|TestInitialSnapshot|TestForget" -count=1'
```

Expected: FAIL，当前 Server 完全不产生远端玩家消息，且现有顺序是 snapshot/delta 先于 forget。

- [ ] **Step 3：以两阶段差分重排 publication**

`publishSession` 的实现顺序必须显式写成以下骨架：

```go
func (server *Server) publishSession(current *session, result sim.TickResult, players map[sim.SessionID]sim.PlayerUpdate) {
	server.updateSessionView(current, players[current.id])
	server.queueReadyAndResync(current, result)
	if !server.publishRemoteDespawns(current, players) { return }
	if !server.publishForget(current, result.Forget[current.id]) { return }
	deltas := server.classifyDeltas(current, result.Changes)
	if !server.publishSnapshots(current) || !server.publishDeltas(current, deltas) { return }
	if !server.publishRemoteSpawnsAndStates(current, result.Tick, players) { return }
	server.publishLocalResult(current, result, players[current.id])
}
```

`publishRemoteDespawns` 在 `applyForget` 删除 publication 前，用本 tick wanted、Ready、dimension、target foot chunk 和旧 `snapshotSent` 判断离开；Despawn enqueue 成功后才 delete visible map。snapshot/delta 完成后重新计算 candidates，按 PlayerID：新/generation 变化者发 Spawn，旧稳定者进入唯一 States，Spawn 本 tick 不进 States；Spawn/States 也只有对应 enqueue 成功后才提交 visible/last-tick 状态。任何 enqueue 失败只 `closePublicationSessionLocked(current, errSessionOutboxFull)` 并让外层继续下一个 observer。

- [ ] **Step 4：运行 server race 与确定性高重复**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TestRemotePlayerPublicationOrder|TestRemotePlayerInterest" -count=50'
```

Expected: PASS；50 次运行消息排序一致，heartbeat 是否穿插不改变 publication 消息相对顺序。

- [ ] **Step 5：提交**

```bash
git add internal/server/player_publication.go internal/server/player_publication_test.go internal/server/publication.go internal/server/publication_test.go internal/server/session.go internal/server/session_registry_test.go internal/server/player_test.go
git commit -m "feat: 按区块兴趣发布远端玩家"
```

---

## Task 4：把玩家持久化升级为 16 项、按 PlayerID 去重的缓存

**Files:**
- Modify: `internal/server/player_persistence.go`
- Modify: `internal/server/player_persistence_test.go`
- Create: `internal/server/player_persistence_cache_test.go`
- Modify: `internal/server/host.go`
- Modify: `internal/server/host_test.go`

**Interfaces:**

```go
const playerCacheCapacity = 16

type cachedPlayer struct {
	id                  core.PlayerID
	name, pendingName   string
	persisted           uint64
	snapshot            sim.PlayerSnapshot
	hasSnapshot         bool
	hasObservedSnapshot bool
	missing             bool
	missingConfirmed    bool
	dirty               bool
	active              bool
	inFlight            bool
	forcePending        bool
	retry               *playerSaveJob
	loadDone            chan struct{}
	loading             bool
}

type playerPersistence struct {
	store        storage.PlayerStore
	config       Config
	mu           sync.Mutex
	completionMu sync.Mutex
	cache        map[core.PlayerID]*cachedPlayer
	jobs         chan playerSaveJob
	completions  chan playerSaveCompletion
	ctx          context.Context
	cancel       context.CancelFunc
	done         chan struct{}
	closeOnce    sync.Once
}

func (p *playerPersistence) Prepare(context.Context, core.PlayerID, string, storage.Metadata) (sim.PlayerRestore, error)
func (p *playerPersistence) Activate(core.PlayerID, string) error
func (p *playerPersistence) Confirm(core.PlayerID)
func (p *playerPersistence) Abort(core.PlayerID)
func (p *playerPersistence) Deactivate(core.PlayerID)
func (p *playerPersistence) Observe(core.PlayerID, string, sim.PlayerSnapshot, uint64, bool) error
```

- [ ] **Step 1：写缓存容量、同 ID 合并与重连语义的失败测试**

`player_persistence_cache_test.go` 使用带 `LoadPlayer` 调用计数和受控阻塞 channel 的 Store，固定以下行为：同 PlayerID 的 8 个并发 `Prepare` 只触发一次 `LoadPlayer`；16 个不同 ID 的 LoadPlayer 可同时开始并各占一个 placeholder；clean 且非 active/pending 的项立即驱逐；16 项全为 active、pending、dirty、in-flight 或 retry 时，第 17 个 `Prepare` 返回 `ErrPlayerPersistenceBackpressure` 且不调用 Store；dirty 玩家断线后、save 完成前重连得到缓存中的新状态而不是旧磁盘状态；失败 `Prepare` 删除本次占位并唤醒同 ID 等待者；Store 返回 not-found 的新身份在 `Confirm` 前不 dirty、不保存候选昵称，Abort 后不留下 cache entry。

```go
func TestPlayerPersistenceCoalescesConcurrentLoad(t *testing.T) {
	store := newBlockingPlayerStore()
	persistence := newPlayerPersistence(store, playerPersistenceTestConfig())
	defer persistence.CloseWorker()
	var group sync.WaitGroup
	group.Add(8)
	for index := 0; index < 8; index++ {
		go func() {
			defer group.Done()
			_, err := persistence.Prepare(context.Background(), testPlayerID(1), "Player-1", testMetadata())
			if err != nil { t.Error(err); return }
		}()
	}
	store.WaitForLoadStarted(t, 1)
	store.UnblockLoad()
	group.Wait()
	persistence.Abort(testPlayerID(1))
	if got := store.LoadCount(testPlayerID(1)); got != 1 { t.Fatalf("loads=%d want=1", got) }
}
```

- [ ] **Step 2：运行缓存测试并确认 RED**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TestPlayerPersistence(Coalesces|Capacity|Evicts|Reconnects|FailedLoad)" -count=1'
```

Expected: FAIL，当前实现只有一个全局 `cachedPlayer`，不同 ID 会覆盖，同 ID 并发也没有 load placeholder。

- [ ] **Step 3：实现占位加载和安全淘汰**

移除串行所有身份的 `prepareMu`。`Prepare` 在锁内按 PlayerID 查表：已加载直接登记 pending nickname 并返回最新 restore；loading 项复制 `loadDone` 后解锁等待；miss 先删除所有 clean 且非 active/pending 的项，若仍满 16 就返回 backpressure，否则插入 loading placeholder，然后解锁执行 Store I/O。完成 I/O 后重新加锁写入结果、关闭 `loadDone`，失败时只在 map 仍指向同一 placeholder 时删除。不得在 `playerPersistence.mu` 内调用 Store；不同 ID 的 MemoryStore LoadPlayer 可并行开始，DiskStore 自身的内部锁不在本任务扩大。

`Activate` 把完成 attach 的项设为 active；`Confirm` 只确认登录并保持 `active=true`，不得像单玩家实现一样立即清 active。`Abort` 只释放未完成登录的 pending nickname；`Deactivate` 只用于已确认 session exit，在 Host 完成可用的 force Observe 后清 active，并按规则删除 clean entry。dirty/retry 状态继续驻留以支持保存或快速重连。所有删除都比较 map 中当前指针，防止旧 load/save completion 删除后继 generation。

- [ ] **Step 4：运行缓存回归和竞态检查**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TestPlayerPersistence|TestPlayerRestore" -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TestPlayerPersistence(Coalesces|Capacity|Evicts|Reconnects|FailedLoad)" -race -count=20'
```

Expected: PASS；20 次竞态运行只出现一次同 ID LoadPlayer，淘汰对象和返回错误稳定。

- [ ] **Step 5：提交**

```bash
git add internal/server/player_persistence.go internal/server/player_persistence_test.go internal/server/player_persistence_cache_test.go internal/server/host.go internal/server/host_test.go
git commit -m "feat: 缓存多玩家持久化状态"
```

---

## Task 5：实现两个固定 save worker、容量 16 的调度器与逐 ID 重试

**Files:**
- Create: `internal/server/player_save_scheduler.go`
- Create: `internal/server/player_save_scheduler_test.go`
- Modify: `internal/server/player_persistence.go`
- Modify: `internal/server/player_persistence_test.go`

**Interfaces:**

```go
const (
	playerSaveWorkerCount = 2
	playerSaveJobCapacity  = 16
	playerSaveDoneCapacity = 2
)

type playerSaveJob struct {
	Save     storage.PlayerSave
	Attempt  uint32
	NextTick uint64
}

type playerSaveCompletion struct {
	Job      playerSaveJob
	Revision uint64
	Err      error
}

type playerSaveScheduler struct {
	jobs        chan playerSaveJob
	completions chan playerSaveCompletion
}

func newPlayerSaveScheduler(storage.PlayerStore) *playerSaveScheduler
func (s *playerSaveScheduler) CloseJobs()
func (s *playerSaveScheduler) Wait()
```

- [ ] **Step 1：写 worker 数量、队列上限、完成顺序和重试的失败测试**

`player_save_scheduler_test.go` 用受控 Store 同时阻塞两个不同 PlayerID 的 SavePlayer，断言第三个 Save 尚未进入 Store；`cap(jobs)==16`、`cap(completions)==2`。让两个 completion 填满但暂不 Poll，证明 worker 只在有界 channel 上背压、无额外 goroutine 或 slice 增长；Poll 后队列回落。释放两个 Save 时即使 Store 以反序返回，persistence 收集当前批 completions 后也按 `Job.Save.PlayerID` 排序应用。再构造 ID 1 第一次失败、ID 2 成功的场景，断言失败只让 ID 1 保持 retry，按 20/40/80 tick、上限 1200 的 backoff 重投完全相同的 revision/value，ID 2 不被重复保存；失败等待期间 ID 1 的新 Observe 合并到下一 revision，不改写正在重试的 bytes。

```go
func TestPlayerSaveSchedulerUsesExactlyTwoWorkers(t *testing.T) {
	store := newConcurrentSaveStore()
	scheduler := newPlayerSaveScheduler(store)
	defer func() { scheduler.CloseJobs(); scheduler.Wait() }()
	for id := 1; id <= 3; id++ { scheduler.jobs <- testSaveJob(id, 1) }
	store.WaitForStarted(t, 2)
	if got := store.Started(); got != 2 { t.Fatalf("started=%d want=2", got) }
	store.UnblockAll()
	for index := 0; index < 3; index++ { <-scheduler.completions }
}
```

- [ ] **Step 2：运行 scheduler tests 并确认 RED**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TestPlayerSave(Scheduler|Retry|Completion)" -count=1'
```

Expected: FAIL，当前 jobs/completions 容量均为 1，只有一个 worker，并以 completion 到达顺序直接更新缓存。

- [ ] **Step 3：实现固定并发、每 ID 单飞与确定性 completion drain**

创建恰好两个 worker；worker 只执行 `Store.SavePlayer` 并把原 job、返回 revision 与 error 写入 completion channel。simulation owner 每 tick 先把当下可读 completions 全部 drain 到 slice，按 `Job.Save.PlayerID`、`Job.Save.Revision` 排序，再逐项应用；autosave 到点时也先收集 eligible entries 并按 PlayerID 排序 dispatch。每个 cache entry 最多一个 in-flight：新 Observe 只更新内存 snapshot/dirty；旧 revision 成功后若仍 dirty，下一次调度生成更高 revision 的最新快照；失败保存原 job 到 retry 并按 Attempt 计算 backoff，后续重投相同 PlayerSave bytes。队列满时不阻塞 simulation，而是保留 dirty/retry 到下一 tick。固定 player worker 数不得读取 world `Config.SaveWorkers`。

- [ ] **Step 4：运行持久化竞态和容量压力测试**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TestPlayerSave|TestPlayerPersistence" -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TestPlayerSaveSchedulerUsesExactlyTwoWorkers|TestPlayerSaveRetryIsPerPlayer" -count=50'
```

Expected: PASS；50 次运行均只有两个并发 Store.SavePlayer，失败 ID 的重试不改变其他 ID 的完成状态。

- [ ] **Step 5：提交**

```bash
git add internal/server/player_save_scheduler.go internal/server/player_save_scheduler_test.go internal/server/player_persistence.go internal/server/player_persistence_test.go
git commit -m "feat: 并发调度多玩家保存"
```

---

## Task 6：把 Host 准入改为最多 8 个 session 的双索引注册表

**Files:**
- Modify: `internal/server/config.go`
- Create: `internal/server/config_test.go`
- Modify: `internal/server/host.go`
- Modify: `internal/server/host_test.go`
- Create: `internal/server/login_tcp_test.go`
- Modify: `cmd/mcgod/main.go`
- Modify: `cmd/mcgod/main_test.go`

**Interfaces:**

```go
type Config struct {
	MaxPlayers int
}

type activeLogin struct {
	PlayerID   core.PlayerID
	Name       string
	Session    sim.SessionID
	Generation uint64
}

type Host struct {
	activeByPlayer  map[core.PlayerID]*activeLogin
	activeBySession map[sim.SessionID]*activeLogin
}

func (host *Host) reserveLogin(core.PlayerID) (*activeLogin, error)
func (host *Host) promoteLogin(*activeLogin, sim.SessionID, uint64) error
func (host *Host) releaseLogin(*activeLogin)
```

`Config.MaxPlayers==0` 解释为默认 8；显式值只接受 1..8。`DefaultConfig` 直接写入 8，`NewHost` 在把 config 保存到 `Host.config` 或传给 `NewWorld` 之前先对同一局部副本调用 `config.validate()`，避免 Host 留下未归一化的 0。`mcgod --max-players` 默认 8，并在构造 Server/Host 前验证。

- [ ] **Step 1：写多玩家、重复 ID、同名和容量的失败测试**

`host_test.go` 覆盖：8 个唯一 PlayerID 同时登录成功，session ID 从 1 严格单调递增且断线后不复用；第 9 个唯一 ID 得到现有 server-full reject；在线 PlayerID 的 newcomer 在 Store.LoadPlayer 前得到 code 7 `AlreadyOnline`，旧连接仍可收发；相同 DisplayName 的不同 ID 同时成功；关闭任意中间 session 后新 ID 可占空位；旧连接的延迟 cleanup 不能删除同 ID 的新 entry。另将一个连接分别变为 malformed、heartbeat timeout 和不 drain 的 slow client，断言只删除该 entry，其余 7 个仍收发并推进 world tick。

`login_tcp_test.go` 通过真实 TCP 并发发起 8 个 login，并验证 duplicate 与 full 的 wire reject code。`config_test.go` 用 `DefaultConfig` 和把 MaxPlayers 置回 0 后直接 `NewHost` 两条路径断言 Host 最终配置都是 8；显式负数/9 panic。`cmd/mcgod/main_test.go` 覆盖 flag 默认值、`--max-players=1`、0 和 9，CLI 的显式 0 必须拒绝。

- [ ] **Step 2：运行 Host/login tests 并确认 RED**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server ./cmd/mcgod -run "Test(HostAllowsEightPlayers|HostRejectsNinthPlayer|HostRejectsDuplicatePlayerBeforeLoad|HostAllowsDuplicateDisplayName|LoginTCPMultiplayer|OptionsMaxPlayers)" -count=1'
```

Expected: FAIL，当前 Host 只有一个 `active *activeLogin`，第二个连接就被 server-full 拒绝。

- [ ] **Step 3：在一个临界区内完成 duplicate-first 准入**

`Config.validate` 把零值 MaxPlayers 归一为 8，并对非零值强制 1..8；`NewHost` 的第一条语句就是 `config.validate()`，之后才构造 Host/NewWorld。初始化双 map，在 Host mutex 内按以下固定顺序处理：先查 `activeByPlayer[playerID]` 并返回 `AlreadyOnline`；再比较 `len(activeByPlayer)` 与 MaxPlayers；成功则立即插入 reservation，之后才调用 persistence Prepare/Store.LoadPlayer。分配 session 后 `promoteLogin` 将同一 entry 加入 `activeBySession`。任何错误路径调用 `releaseLogin(entry)`；删除时必须满足 map 当前值与 entry 指针相等，并同时清理 session 索引。

Host 的 polling、shutdown snapshot 和统计都从 `activeBySession` 复制 slice，按 SessionID 排序后解锁处理；不得持 Host mutex 调用 endpoint、Store 或 Server 方法。

- [ ] **Step 4：连接 CLI flag 并运行竞态回归**

`cmd/mcgod` 将 `--max-players` 传入 server config；帮助文本明确范围 1..8、默认 8。运行：

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server ./cmd/mcgod -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TestHostRejectsDuplicatePlayerBeforeLoad|TestHostCleanupUsesEntryIdentity" -count=50'
```

Expected: PASS；重复 ID 的 Store.LoadPlayer 次数为 0，50 次 cleanup 竞态测试都保留当前 entry。

- [ ] **Step 5：提交**

```bash
git add internal/server/config.go internal/server/config_test.go internal/server/host.go internal/server/host_test.go internal/server/login_tcp_test.go cmd/mcgod/main.go cmd/mcgod/main_test.go
git commit -m "feat: 支持八玩家并发准入"
```

---

## Task 7：实现确定性 Flush 和多 session 严格关闭

**Files:**
- Create: `internal/server/player_flush.go`
- Create: `internal/server/player_flush_test.go`
- Modify: `internal/server/player_persistence.go`
- Modify: `internal/server/host.go`
- Create: `internal/server/host_shutdown_test.go`
- Modify: `internal/server/shutdown_test.go`

**Interfaces:**

```go
type playerSaveKey struct {
	playerID core.PlayerID
	revision uint64
}

func (p *playerPersistence) Flush(context.Context) error
func (host *Host) waitAcceptLoop(context.Context) error
func (host *Host) closePendingLogins()
func (host *Host) waitPendingLogins()
```

- [ ] **Step 1：写 Flush 一次尝试、稳定错误和关闭屏障的失败测试**

`player_flush_test.go` 构造三个 dirty ID：ID 1 失败、ID 2 成功、ID 3 失败；断言一次 `Flush` 每个 `(PlayerID, revision)` 最多尝试一次、不会因 ID 1 失败跳过 ID 2/3、返回错误按 PlayerID 排序且文本在 50 次运行一致。另测 worker 已有旧 revision in-flight 时，Flush 等 completion 后只尝试仍未保存的最新 revision。

`host_shutdown_test.go` 覆盖三类连接：尚未读完 login、已 reservation 正在 Load、已 promote 到 Play。调用 Shutdown 后必须先禁止新 reservation，再关闭 pending login streams、等待 pending goroutine 退出、detach 全部 active sessions、等待 session handler 收集最终快照并 force Observe，然后在 RunTicks/world/player workers 仍存活时 Flush。Flush 失败必须立即返回并保留 world、Store、RunTicks 和 player workers，使第二次 Shutdown 可重试；Flush 成功后才停止 world，world Store 成功 sync/close 后才关闭 jobs 并等待两个 player workers。最终成功后 Host 两个索引为空、Store 不再被调用、goroutine 回到基线容差 2。

```go
func TestPlayerFlushAttemptsEachRevisionOnceAndSortsErrors(t *testing.T) {
	persistence, store := newDirtyPersistence(t, 3)
	store.Fail(testPlayerID(1), errors.New("one"))
	store.Fail(testPlayerID(3), errors.New("three"))
	err := persistence.Flush(context.Background())
	if got, want := err.Error(), "save player 00000000-0000-4000-8000-000000000001 revision 1: one\nsave player 00000000-0000-4000-8000-000000000003 revision 1: three"; got != want {
		t.Fatalf("error=%q want=%q", got, want)
	}
	store.AssertAttempts(t, map[playerSaveKey]int{
		{playerID: testPlayerID(1), revision: 1}: 1,
		{playerID: testPlayerID(2), revision: 1}: 1,
		{playerID: testPlayerID(3), revision: 1}: 1,
	})
}
```

- [ ] **Step 2：运行 Flush/shutdown tests 并确认 RED**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "Test(PlayerFlush|HostShutdownMultiplayer|HostShutdownPendingLogin)" -count=1'
```

Expected: FAIL，当前 Flush 和 shutdown 只围绕一个 cached player/active login，且 pre-login tracking 会一直覆盖 Play 生命周期。

- [ ] **Step 3：拆分 pending-login 与 active-session 生命周期**

Host 分成三个 WaitGroup：`acceptWG` 只覆盖 listener accept-loop，`pendingWG` 只覆盖登录握手/Load/promotion，`sessionWG` 覆盖 promotion 后 session handler。Run 在 Host mutex 内检查 `!closing` 后先 `acceptWG.Add(1)` 再启动 loop；acquire 在同一 mutex 屏障内先检查 `!closing` 再登记 stream/`pendingWG.Add(1)`；promotion 也在该屏障内先 `sessionWG.Add(1)` 再从 pending 转移。Shutdown 先在 mutex 内设置 closing，之后不会再有任何 WaitGroup.Add；关闭 listener 并等待 acceptWG 后，才复制/关闭 pending streams并等待 pendingWG；再按 SessionID detach active sessions并等待 sessionWG，让每个 handler 收集 SessionExit、执行可用的 force Observe，最后无条件 `Deactivate(PlayerID)`。所有失败 defer 使用同一幂等 transition helper，禁止 Add 与 Wait 并发。

- [ ] **Step 4：实现单次 revision 尝试集合与严格 shutdown 顺序**

`Flush` 维护函数局部 `attempted map[playerSaveKey]struct{}`，循环 drain completions、按 PlayerID 调度尚未尝试的 dirty revision、等待 in-flight；单项失败记录后继续其他 ID，不在同次 Flush 重试同 key。若失败期间 entry revision 前进，新 key 可尝试一次。最终错误按 PlayerID、revision 排序，用 `errors.Join` 保留 `errors.Is`。

Shutdown 固定为：mark closing → close listener → wait accept-loop → close/wait pending logins → detach sorted active sessions → wait session handlers/force Observe/Deactivate → Flush players。若 Flush 失败，将稳定错误与 listener error Join 后返回，不能 cancel RunTicks、关闭 world Store 或关闭 player jobs。Flush 成功才 cancel RunTicks 并调用 world Shutdown；world 成功 sync/close 后再 `CloseJobs`、等待两个 player workers并 drain completions。第二次 Shutdown 通过现有 gate 串行进入并重试仍 dirty 的 revision。

- [ ] **Step 5：运行高重复、竞态与现有关闭回归**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TestPlayerFlush|TestHostShutdown" -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TestPlayerFlushAttemptsEachRevisionOnceAndSortsErrors|TestHostShutdownMultiplayer" -count=50'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -count=1'
```

Expected: PASS；50 次错误文本和 Store 尝试集合一致，完整 server suite 无 goroutine 泄漏。

- [ ] **Step 6：提交**

```bash
git add internal/server/player_flush.go internal/server/player_flush_test.go internal/server/player_persistence.go internal/server/host.go internal/server/host_shutdown_test.go internal/server/shutdown_test.go
git commit -m "feat: 确定性关闭多人服务器"
```

---

## Task 8：实现严格的远端玩家 roster 状态机

**Files:**
- Create: `internal/client/remote_players.go`
- Create: `internal/client/remote_players_test.go`

**Interfaces:**

```go
var ErrRemotePlayerProtocol = errors.New("remote player protocol error")

type RemotePresentation struct {
	PlayerID    core.PlayerID
	DisplayName string
	Dimension   core.DimensionID
	Position    mgl32.Vec3
	Yaw         float32
	Pitch       float32
}

type RemotePlayers struct {
	players map[core.PlayerID]*remotePlayer
}

func NewRemotePlayers() *RemotePlayers
func (players *RemotePlayers) Apply(network.ServerMessage) error
func (players *RemotePlayers) Presentations() []RemotePresentation
func (players *RemotePlayers) Reset()
```

- [ ] **Step 1：写 Spawn/Despawn/States 严格转换的失败测试**

`remote_players_test.go` 固定：Spawn 创建 roster 项；重复 Spawn 返回包裹 `ErrRemotePlayerProtocol` 的错误且不覆盖旧项；未知 PlayerID 的 Despawn/States、States tick 小于或等于该玩家最近 tick、非法 UUID/昵称/dimension/NaN/Inf 都是协议错误；一个 States batch 更新多个已知玩家；Despawn 清除状态；`Reset` 清空全部；`Presentations` 永远按 PlayerID 排序并返回副本。非远端消息传给 `Apply` 也返回协议错误，调用者必须先分流。

```go
func TestRemotePlayersRejectsUnknownState(t *testing.T) {
	players := NewRemotePlayers()
	err := players.Apply(network.RemotePlayerStates{
		ServerTick: 7,
		Players: []network.RemotePlayerState{{PlayerID: testPlayerID(2)}},
	})
	if !errors.Is(err, ErrRemotePlayerProtocol) { t.Fatalf("error=%v", err) }
	if got := players.Presentations(); len(got) != 0 { t.Fatalf("presentations=%v", got) }
}
```

- [ ] **Step 2：运行 roster tests 并确认 RED**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/client -run "TestRemotePlayers" -count=1'
```

Expected: FAIL，当前 client 没有远端 roster 类型。

- [ ] **Step 3：实现原子校验后提交状态**

每个 `Apply` 先完整校验消息，再修改 map；一个 batch 中任一未知 ID 或非递增 tick 都必须保持整个 batch 前的状态。Spawn 保存 display name、dimension 和首个 authoritative sample；States 只更新 motion sample，不允许隐式创建；Despawn 删除完整 player。错误使用 `%w: <message type>: <reason>`，原因包含 PlayerID 与 tick，不能 panic。

- [ ] **Step 4：运行 client 回归与竞态测试**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/client -run "TestRemotePlayers" -race -count=20'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/client -count=1'
```

Expected: PASS；20 次重复的 presentation 顺序一致，非法 batch 不产生半更新。

- [ ] **Step 5：提交**

```bash
git add internal/client/remote_players.go internal/client/remote_players_test.go
git commit -m "feat: 维护远端玩家 roster"
```

---

## Task 9：为远端玩家实现四快照、落后两 tick 的插值

**Files:**
- Modify: `internal/client/remote_players.go`
- Modify: `internal/client/remote_players_test.go`
- Create: `internal/client/remote_interpolation.go`
- Create: `internal/client/remote_interpolation_test.go`

**Interfaces:**

```go
const (
	remoteSnapshotCapacity = 4
	remoteInterpolationLag = 2.0
	remoteTickRate         = 20.0
)

type remoteSnapshot struct {
	tick      uint64
	dimension core.DimensionID
	position  mgl32.Vec3
	yaw       float32
	pitch     float32
}

func (players *RemotePlayers) Advance(elapsed time.Duration)
```

- [ ] **Step 1：写纯时间驱动插值和 reset 条件的失败测试**

测试不用 `time.Sleep`，而是显式调用 `Advance`。覆盖：ring 只保留最新 4 个快照；收到 tick N 后渲染目标为 `float64(N)+clamp(elapsed.Seconds()*20, 0, 1)-2`；目标夹在两个样本之间时位置/pitch 线性插值、yaw 以弧度走 `[-π,π]` 最短圆弧；目标早于最老样本保持最老，晚于最新样本保持最新且绝不外推；只有 1 或 2 个样本时保持最新；第 3 个样本才恢复插值；dimension 改变、`Reset=true`、两份相邻脚底位置距离严格大于 8、Despawn/Spawn 都清 ring 并重新 warm up；tick 不连续本身不 reset，仍只在权威端点间插值。

```go
func TestRemotePlayersInterpolatesTwoTicksBehind(t *testing.T) {
	players := seededRemotePlayers(t, testPlayerID(1), []remoteSnapshot{
		{tick: 10, position: mgl32.Vec3{0, 0, 0}, yaw: -0.2},
		{tick: 11, position: mgl32.Vec3{1, 0, 0}, yaw: -0.1},
		{tick: 12, position: mgl32.Vec3{2, 0, 0}, yaw: 0},
		{tick: 13, position: mgl32.Vec3{3, 0, 0}, yaw: 0.1},
	})
	players.Advance(25 * time.Millisecond)
	got := players.Presentations()[0]
	assertVec3Close(t, got.Position, mgl32.Vec3{1.5, 0, 0})
	assertAngleClose(t, got.Yaw, -0.05)
}
```

- [ ] **Step 2：运行 interpolation tests 并确认 RED**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/client -run "TestRemotePlayers(Interpolates|Holds|Resets|Warms|BoundsSnapshots)" -count=1'
```

Expected: FAIL，当前 presentation 只有最新 authoritative state，不保存 snapshot ring 或 elapsed。

- [ ] **Step 3：实现每玩家 ring 与无外推采样器**

`Apply(States)` 先完成 Task 8 的原子验证，再把样本 append 到每玩家固定 `[4]remoteSnapshot` ring。`Advance` 对每个玩家把自最新 authoritative message 后的 elapsed 限制在 `[0, 50ms]`；新的 States 把 elapsed 归零。Reset、dimension 变化或相邻位置距离 `>8` 时以新样本重建 ring；tick gap 不触发 reset。采样时只在至少 3 个样本中找包围目标的 pair，否则保持规则指定的端点。yaw 先归一化到 `[-π,π]`，差值也归一化到 `[-π,π]` 后插值。

- [ ] **Step 4：运行数值边界和完整 client suite**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/client -run "TestRemotePlayers" -count=50'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/client -race -count=1'
```

Expected: PASS；50 次纯时间测试没有墙钟抖动，所有结果不含 NaN/Inf。

- [ ] **Step 5：提交**

```bash
git add internal/client/remote_players.go internal/client/remote_players_test.go internal/client/remote_interpolation.go internal/client/remote_interpolation_test.go
git commit -m "feat: 插值远端玩家运动"
```

---

## Task 10：扩展 gfx 的 R8 子区域上传和 alpha blend

**Files:**
- Modify: `internal/gfx/gfx.go`
- Modify: `internal/gfx/wgpu.go`
- Create: `internal/gfx/wgpu_test.go`
- Create: `internal/gfx/texture_region_test.go`

**Interfaces:**

```go
// Append after FormatR32Uint so existing TextureFormat numeric values do not move.
const FormatR8Unorm TextureFormat = 7

type Texture interface {
	View(TextureViewDesc) TextureView
	WriteLayer(layer, mip uint32, pixels []byte)
	WriteRegion(layer, mip, x, y, width, height uint32, pixels []byte)
	Release()
}

type BlendMode uint8

const (
	BlendReplace BlendMode = iota
	BlendAlpha
)

type RenderPipelineDesc struct {
	Blend BlendMode
}
```

- [ ] **Step 1：写格式、region 边界和 pipeline 映射的失败测试**

`texture_region_test.go` 创建 1024×1024 单层 R8 texture，上传一个 `(x=32,y=64,width=32,height=32)` 的 1024-byte region 并要求成功；测试越界、零尺寸、错误 byte 数、非法 layer/mip 都按 gfx 现有“程序员错误”约定 panic，且 panic 文本稳定。RGBA8 的 2×3 region 必须恰好接收 24 bytes。`WriteLayer` 回归测试证明它等价于整 mip 的 `WriteRegion`。

`wgpu_test.go` 对纯 descriptor helper 断言：R8 映射 `wgpu.TextureFormatR8Unorm`；`BlendAlpha` 映射 source-alpha/one-minus-source-alpha 的颜色和 alpha component；默认 `BlendReplace` 保持现有 replace 配置。

- [ ] **Step 2：运行 gfx tests 并确认 RED**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/gfx -run "Test(TextureWriteRegion|TextureWriteLayerDelegates|TextureFormatR8|BlendMode)" -count=1'
```

Expected: FAIL，当前 Texture 只能整层写入，缺少 R8 和可选 blend。

- [ ] **Step 3：实现溢出安全校验与 WebGPU 映射**

`wgpuTexture` 保存创建时的 layers/mipLevels。用 mip 后尺寸校验 `x+width`、`y+height` 时先拒绝零尺寸，再比较 `x > mipWidth-width`，避免 uint32 overflow；用 `uint64(width)*uint64(height)*bytesPerPixel` 校验 payload 后再转换。R8 每 texel 1 byte，RGBA8 每 texel 4 bytes。`WriteLayer(layer,mip,pixels)` 读取 mip 尺寸并调用 `WriteRegion(layer,mip,0,0,width,height,pixels)`，不复制 payload。

WebGPU `Queue.WriteTexture` 的 origin 使用 region x/y/layer，`BytesPerRow=width*bytesPerPixel`、`RowsPerImage=height`。`BlendAlpha` 的 color component 使用 `SrcAlpha`/`OneMinusSrcAlpha`，alpha component 使用 `One`/`OneMinusSrcAlpha`，operation 都是 add；avatar/terrain 继续显式 `BlendReplace`。

- [ ] **Step 4：运行无窗口 gfx 回归和竞态测试**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/gfx -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/gfx -race -count=1'
```

Expected: PASS；测试只创建 headless adapter/device，不创建 surface 或窗口。

- [ ] **Step 5：提交**

```bash
git add internal/gfx/gfx.go internal/gfx/wgpu.go internal/gfx/wgpu_test.go internal/gfx/texture_region_test.go
git commit -m "feat: 支持字形纹理上传与透明混合"
```

---

## Task 11：嵌入固定来源的 Noto 字体并实现有界字形图集

**Files:**
- Create: `internal/render/assets/NotoSansCJKsc-Regular.otf`
- Create: `internal/render/assets/OFL.txt`
- Create: `internal/render/assets/NotoSansCJKsc-Regular.provenance.json`
- Create: `internal/render/font_atlas.go`
- Create: `internal/render/font_atlas_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Pinned assets:**

- Upstream repository: `notofonts/noto-cjk`
- Commit: `f8d157532fbfaeda587e826d4cd5b21a49186f7c`
- Font path: `Sans/OTF/SimplifiedChinese/NotoSansCJKsc-Regular.otf`
- Font SHA-256: `2c76254f6fc379fddfce0a7e84fb5385bb135d3e399294f6eeb6680d0365b74b`
- License path: `Sans/LICENSE`
- License SHA-256: `6a73f9541c2de74158c0e7cf6b0a58ef774f5a780bf191f2d7ec9cc53efe2bf2`
- Rasterizer: `golang.org/x/image/font/opentype v0.44.0`

**Interfaces:**

```go
const (
	glyphAtlasSize       = 1024
	glyphCellSize        = 32
	glyphSlotCount       = 1024
	glyphRequestCapacity = 1024
	glyphResultCapacity  = 32
)

type Glyph struct {
	Slot                         uint16
	U0, V0, U1, V1               float32
	Advance, BearingX, BearingY  float32
	Width, Height                float32
}

type GlyphAtlas struct {
	texture       gfx.Texture
	view          gfx.TextureView
	pendingUpload *glyphResult
	renderFace    font.Face
}

func NewGlyphAtlas(gfx.Device) (*GlyphAtlas, error)
func (atlas *GlyphAtlas) Request(text string)
func (atlas *GlyphAtlas) FlushUploads(*UploadBudget) error
func (atlas *GlyphAtlas) Glyph(rune) Glyph
func (atlas *GlyphAtlas) Kern(left, right rune) float32
func (atlas *GlyphAtlas) TextureView() gfx.TextureView
func (atlas *GlyphAtlas) Release()
```

- [ ] **Step 1：下载固定 commit 的字体与 OFL 并验证哈希**

只在本任务执行时联网获取一次，运行时和构建时均不联网：

```bash
mkdir -p internal/render/assets
curl -fL 'https://raw.githubusercontent.com/notofonts/noto-cjk/f8d157532fbfaeda587e826d4cd5b21a49186f7c/Sans/OTF/SimplifiedChinese/NotoSansCJKsc-Regular.otf' -o internal/render/assets/NotoSansCJKsc-Regular.otf
curl -fL 'https://raw.githubusercontent.com/notofonts/noto-cjk/f8d157532fbfaeda587e826d4cd5b21a49186f7c/Sans/LICENSE' -o internal/render/assets/OFL.txt
shasum -a 256 internal/render/assets/NotoSansCJKsc-Regular.otf internal/render/assets/OFL.txt
```

Expected: 输出分别严格等于上列两个 SHA-256。`provenance.json` 写入 repository、commit、两个 upstream path、两个 byte size、两个 hash 和许可证标识 `OFL-1.1`；`go:embed` 同时嵌入字体、OFL 和 provenance。

- [ ] **Step 2：写 tofu、中文、队列和不淘汰策略的失败测试**

`font_atlas_test.go` 使用可注入的同步 fake rasterizer/face factory 验证：slot 0 在构造时就是 tofu；同 rune 多次 Request 只入队一次；构造严格创建两个不同 face，worker 只调用 worker face，`Kern` 只调用 render face；单 worker 按首次请求顺序分配 slot 1..1023；请求/结果 channel 容量固定 1024/32；`FlushUploads` 每个 glyph 只消费 1024-byte UploadBudget 并调用一个 32×32 R8 `WriteRegion`；预算不足时结果留到下一帧；填满 1023 个 lifetime slots 后，第 1024 个新 rune 和以后所有新 rune 都永久返回 tofu，不淘汰、不扩图；人为填满 result queue 后调用 `Release` 仍能取消阻塞 send、等待 worker 退出；`Release` 可重复调用。

另用真实嵌入字体先 `Request("中AV")` 并在受控预算下 drain results，再断言 `Glyph('中')`、`Glyph('A')` 的 advance/尺寸与对应 R8 upload 非零，缺字 rune 返回 slot 0，`Kern('A','V')` 可调用且结果有限。

- [ ] **Step 3：运行 atlas tests 并确认 RED**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/render -run "TestGlyphAtlas" -count=1'
```

Expected: FAIL，当前没有字体资产、R8 atlas 或 glyph worker。

- [ ] **Step 4：实现单 worker、render-thread 分配和局部上传**

引入 `golang.org/x/image/font/opentype v0.44.0`。构造时只解析一次嵌入字体，再从同一个解析结果创建两个独立 face：`renderFace` 只供 render thread 的 `Kern` 使用，`workerFace` 只交给唯一 raster worker；禁止跨 goroutine 共享同一个 `font.Face`。随后创建 1024×1024 R8 texture，并同步生成 slot 0 tofu。`Request` 按 rune 遍历 UTF-8，锁内去重后非阻塞写入 request queue；队列满时不登记该 rune，下一帧 Request 可重试，render thread 始终不阻塞。唯一 worker 只用 `workerFace` 做字体测量/32×32 alpha rasterization，按 FIFO `select` 写有界 result queue 或响应 stop channel；result 满时 worker 阻塞而不扩容，Release 关闭 stop 后可退出。slot 分配、glyph map 修改和 `WriteRegion` 全在 `FlushUploads` 的 render thread 完成。达到 1024 slots 后设置 exhausted，所有未见 rune 直接映射 tofu。

`TextureView` 返回 atlas 自己持有的 borrowed view，调用方不得 Release。`FlushUploads` 任一时刻最多从 result channel 移入一个结果到 `pendingUpload`：若 pending 是 raster error，原样包装返回且不消费预算；若 `UploadBudget.TryConsume(32*32)` 失败，保留同一 `pendingUpload` 到下一帧；预算成功才分配 slot，计算 `x=(slot%32)*32`、`y=(slot/32)*32`，调用不会分配的 `WriteRegion`、发布 glyph map并清空 pending，然后才可取下一个结果。字体缺字不是 worker error，而是稳定映射 tofu。`Release` 在 mutex 内标记 released 并关闭 request/stop，确保并发 Request 不向已关闭 channel 写；等待 worker 退出并让 worker-local face 不再可达，将 `renderFace` 置空，再依次释放 view/texture，第二次调用无操作；不得假设 `font.Face` 提供 `Close`。

- [ ] **Step 5：验证资产、竞态、无网络构建和许可证**

```bash
shasum -a 256 internal/render/assets/NotoSansCJKsc-Regular.otf internal/render/assets/OFL.txt
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/render -run "TestGlyphAtlas" -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && GOPROXY=off go test ./internal/render -run "TestGlyphAtlasEmbeddedFont" -count=1'
rg -n 'OFL-1.1|f8d157532fbfaeda587e826d4cd5b21a49186f7c|2c76254f6fc379fddfce0a7e84fb5385bb135d3e399294f6eeb6680d0365b74b' internal/render/assets
```

Expected: hash 与 pinned 值一致；race 和离线测试通过；provenance/OFL 可由 `rg` 找到。

- [ ] **Step 6：提交**

```bash
git add go.mod go.sum internal/render/assets/NotoSansCJKsc-Regular.otf internal/render/assets/OFL.txt internal/render/assets/NotoSansCJKsc-Regular.provenance.json internal/render/font_atlas.go internal/render/font_atlas_test.go
git commit -m "feat: 嵌入有界中文字体图集"
```

---

## Task 12：实现最多 7 个稳定配色的方块人渲染器

**Files:**
- Create: `internal/render/avatar.go`
- Create: `internal/render/avatar_test.go`
- Create: `internal/render/shader/avatar.wgsl`

**Interfaces:**

```go
const (
	maxRemoteAvatars   = 7
	avatarPartsPerBody = 6
	maxAvatarParts     = maxRemoteAvatars * avatarPartsPerBody
)

type Avatar struct {
	PlayerID core.PlayerID
	Position mgl32.Vec3
	Yaw      float32
	Pitch    float32
}

type AvatarRenderer struct {
	instances gfx.Buffer
}

func NewAvatarRenderer(gfx.Device, gfx.TextureFormat, gfx.TextureFormat) *AvatarRenderer
func (renderer *AvatarRenderer) Render(gfx.CommandEncoder, gfx.TextureView, gfx.TextureView, Camera, []Avatar)
func (renderer *AvatarRenderer) Release()
func AvatarColor(core.PlayerID) [4]float32
```

- [ ] **Step 1：写人体几何、容量与颜色稳定性的失败测试**

`avatar_test.go` 对纯 `buildAvatarParts` 断言一个角色精确产生头、躯干、双臂、双腿 6 个局部轴对齐 cuboid transform；总包围盒约 `0.6×1.8` 格，脚底落在输入 Position，头跟随 Pitch、身体跟随 Yaw；输入 8 人只接受按 PlayerID 排序的前 7 人并生成 42 parts。相同 PlayerID 跨进程固定得到同一颜色，不同测试 ID 落入预定义固定调色板，所有通道在 `[0.2,0.9]` 以避免全黑/全白。

```go
func TestBuildAvatarPartsIsBounded(t *testing.T) {
	avatars := makeTestAvatars(8)
	parts := buildAvatarParts(nil, avatars)
	if got, want := len(parts), 42; got != want { t.Fatalf("parts=%d want=%d", got, want) }
	assertAvatarBounds(t, parts[:6], mgl32.Vec3{0.6, 1.8, 0.6})
}
```

- [ ] **Step 2：运行 avatar tests 并确认 RED**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/render -run "Test(Avatar|BuildAvatar)" -count=1'
```

Expected: FAIL，当前 render 只有 terrain renderer。

- [ ] **Step 3：实现固定 instance buffer 和独立 depth-write pass**

构造时一次性分配 42 个 part 的 storage instance buffer、cube vertex/index buffer、camera uniform、bind group 和 pipeline；运行时不得按玩家数扩 GPU buffer。shader 用 `instance_index` 从只读 storage buffer 取得 transform/color，因此不扩展 gfx VertexFormat。PlayerID 的 16 bytes 用固定 FNV-1a hash 选择代码内不可变的 16 色调色板；头部提高亮度，四肢降低亮度，保持身份主色。

`Render` 先按 PlayerID 排序并截断 7 人，写入紧凑 instances，随后创建 `LoadClear=false` 的 `avatar pass`。pipeline 使用 `BlendReplace`、depth compare less、`DepthWrite=true`；无 avatars 时不创建 pass。WGSL 从 unit cube 和每 part transform 构造位置/法线，不采样纹理。

- [ ] **Step 4：运行 headless draw 与资源释放测试**

使用 gfx headless device、64×64 color/depth target 编码 terrain clear 后的 avatar pass，提交并捕获 validation error；断言 `Release` 对每个 GPU handle 恰好一次，重复 Release 不 panic。

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/render -run "Test(Avatar|BuildAvatar)" -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/render -count=1'
```

Expected: PASS；测试不创建窗口或 surface。

- [ ] **Step 5：提交**

```bash
git add internal/render/avatar.go internal/render/avatar_test.go internal/render/shader/avatar.wgsl
git commit -m "feat: 渲染远端方块人"
```

---

## Task 13：实现 Unicode 昵称 billboard 与透明背景 pass

**Files:**
- Create: `internal/render/name_tag.go`
- Create: `internal/render/name_tag_test.go`
- Create: `internal/render/shader/name_tag.wgsl`

**Interfaces:**

```go
const (
	maxNameTags      = 7
	maxNameTagRunes  = 32
	maxNameTagGlyphs = maxNameTags * maxNameTagRunes
)

type NameTag struct {
	PlayerID core.PlayerID
	Text     string
	Anchor   mgl32.Vec3
}

type BillboardCamera struct {
	ViewProj mgl32.Mat4
	Right    mgl32.Vec3
	Up       mgl32.Vec3
}

type NameTagRenderer struct {
	atlas GlyphSource
}

type GlyphSource interface {
	Request(string)
	FlushUploads(*UploadBudget) error
	Glyph(rune) Glyph
	Kern(rune, rune) float32
	TextureView() gfx.TextureView
}

func NewNameTagRenderer(gfx.Device, gfx.TextureFormat, gfx.TextureFormat, GlyphSource) *NameTagRenderer
func (renderer *NameTagRenderer) Prepare([]NameTag, *UploadBudget) error
func (renderer *NameTagRenderer) Render(gfx.CommandEncoder, gfx.TextureView, gfx.TextureView, BillboardCamera)
func (renderer *NameTagRenderer) Release()
```

- [ ] **Step 1：写 Unicode 布局、kerning、上限和背景的失败测试**

`name_tag_test.go` 用 fake atlas 测试按 rune 而不是 byte 截断 32 个字符；`"AV 中文"` 生成 5 个 glyph instances，A/V 间应用 atlas kerning；缺字使用 tofu advance；每个非空标签精确生成一个半透明背景 quad，左右 padding 各 4 px、上下各 2 px；7 人最多 224 glyph + 7 background，输入 8 人按 PlayerID 丢弃最后一个；空昵称不生成任何 instance。布局输出必须与输入顺序无关。

```go
func TestNameTagLayoutUsesUnicodeRunesAndKerning(t *testing.T) {
	atlas := newFakeGlyphAtlas()
	atlas.SetKern('A', 'V', -2)
	layout := layoutNameTags(nil, atlas, []NameTag{{PlayerID: testPlayerID(1), Text: "AV 中文"}})
	if got, want := len(layout.glyphs), 5; got != want { t.Fatalf("glyphs=%d want=%d", got, want) }
	if got := layout.glyphs[1].X; got != atlas.Advance('A')-2 { t.Fatalf("second x=%f", got) }
}
```

- [ ] **Step 2：运行 name-tag tests 并确认 RED**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/render -run "TestNameTag" -count=1'
```

Expected: FAIL，当前没有 billboard layout 或透明 glyph pass。

- [ ] **Step 3：实现 Prepare、固定 buffers 和 camera-facing transform**

`Prepare` 按 PlayerID 排序并截断 7 人，对每个截断到 32 runes 的昵称调用 atlas.Request，再调用 `atlas.FlushUploads(budget)`，最后用当下可用 Glyph/tofu 构造固定容量 CPU slices 并写两个预分配 instance buffers。不得在 Render 内等待 glyph worker。

`Render` 使用 `BillboardCamera.Right/Up` 将以头顶 `Anchor` 为原点的像素布局变为世界空间 quad；背景先画、glyph 后画。两个 pipeline 都为 `BlendAlpha`、depth compare less、`DepthWrite=false`；pass 使用 `LoadClear=false` 保留 terrain/avatar 的 color/depth。shader 采样 R8 atlas，把通道乘进文字 alpha；背景不采样 atlas。

- [ ] **Step 4：运行 headless blend/depth 和资源生命周期测试**

用 64×64 headless target 顺序编码 clear、遮挡 cube、name-tag pass并提交，捕获任何 WebGPU validation error。纯 descriptor helper 断言背景/glyph pipeline 都是 `BlendAlpha`、depth compare less、`DepthWrite=false`，pass descriptor 为 `LoadClear=false`；布局测试证明背景先于 glyph。`NameTagRenderer.Release` 释放自身 handles，但 atlas 由 application 单独拥有，不能双重释放。

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/render -run "TestNameTag" -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/render -count=1'
```

Expected: PASS；中文/ASCII 布局、透明混合、深度读取和 Release 均在无窗口测试覆盖。

- [ ] **Step 5：提交**

```bash
git add internal/render/name_tag.go internal/render/name_tag_test.go internal/render/shader/name_tag.wgsl
git commit -m "feat: 渲染 Unicode 玩家昵称"
```

---

## Task 14：把 roster、插值和三条 render pass 接入 mcgo

**Files:**
- Modify: `internal/render/renderer.go`
- Modify: `cmd/mcgo/app.go`
- Modify: `cmd/mcgo/app_test.go`
- Modify: `cmd/mcgo/main.go`
- Modify: `cmd/mcgo/main_test.go`
- Modify: `cmd/mcgo/benchmark.go`

**Interfaces:**

```go
func (renderer *Renderer) UploadBudget() *UploadBudget

type application struct {
	remotePlayers *client.RemotePlayers
	avatarRenderer *render.AvatarRenderer
	nameTagRenderer *render.NameTagRenderer
	glyphAtlas *render.GlyphAtlas
}

func (app *application) frame(drainMax int, elapsed time.Duration) (bool, error)
func (app *application) renderFrame(workMax int) (bool, error)
func (app *application) closeClientSession(error)
```

- [ ] **Step 1：写消息分流、错误隔离、渲染顺序和 elapsed 的失败测试**

`app_test.go` 覆盖：三类 RemotePlayer 消息只进入 roster、不传给 Mirror；duplicate Spawn 等 roster 协议错误关闭 Receiver/`clientEndpoint`，不得调用内嵌 `serverCancel`，world/其他客户端继续；每帧 drain 后恰好调用一次 `RemotePlayers.Advance(elapsed)`；presentation 转换保留 PlayerID/位置/yaw/pitch/昵称且按 PlayerID；command encoder 的 pass 顺序严格 terrain→avatar→name-tag；glyph worker 注入错误由 frame 原样包装返回而不是 panic/吞掉；connection close 和 application Close 都调用 roster.Reset；close 顺序为 name-tag→atlas→avatar→terrain→depth/color/device，所有资源各一次。

```go
func TestRemoteProtocolErrorClosesOnlyClientEndpoint(t *testing.T) {
	app, endpoint, cancelCount := newRemoteProtocolApplication(t)
	spawn := network.RemotePlayerSpawn{
		PlayerID: testPlayerID(1), DisplayName: "Remote-1", ServerTick: 1,
		Dimension: core.Overworld, Position: mgl32.Vec3{0, 64, 0},
	}
	app.enqueue(spawn)
	app.enqueue(spawn)
	app.drainServerMessages(1)
	if got := len(app.remotePlayers.Presentations()); got != 1 { t.Fatalf("roster=%d", got) }
	if endpoint.CloseCount() != 0 { t.Fatalf("first valid spawn closed endpoint") }
	app.drainServerMessages(1)
	if endpoint.CloseCount() != 1 { t.Fatalf("close count=%d", endpoint.CloseCount()) }
	if got := cancelCount(); got != 0 { t.Fatalf("server cancel count=%d", got) }
}
```

- [ ] **Step 2：运行 app integration tests 并确认 RED**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo -run "Test(Remote|ApplicationRenderPassOrder|ApplicationCloseReleasesRemoteRenderers|FrameAdvancesRemotePlayers)" -count=1'
```

Expected: FAIL，application 尚无 roster/renderer，协议错误会取消整个内嵌服务器，`frame` 也没有 elapsed。

- [ ] **Step 3：接入构造、drain 和共享上传预算**

application 初始化时按 atlas→avatar→name-tag 创建资源；构造中途失败按逆序释放。`drainServerMessages` 用 type switch 先处理 `PlayerState`，再把 Spawn/Despawn/States 交给 roster，其余消息才交给 Mirror。任一路径的服务端协议错误都只记录日志并通过幂等 `closeClientSession` 关闭 Receiver/当前 endpoint、Reset roster；内嵌 server 由连接关闭触发该 session 的正常 detach，不调用 `serverCancel`。application Close 也在释放 render 资源前 Reset roster。

terrain `BeginFrame` 仍是共享 4 MiB budget 的唯一 reset；terrain FlushUploads 后 `NameTagRenderer.Prepare(tags, renderer.UploadBudget())` 消耗余量。`UploadBudget()` 只返回现有指针，不创建第二套预算。

- [ ] **Step 4：按 terrain→avatar→name-tag 编码并推进插值**

`frame(drainMax, elapsed)` 固定执行 drain→remote Advance→render 并返回 `(rendered,error)`。交互循环和 benchmark 都立即向上传播 NameTag Prepare/glyph worker error；交互循环把已经计算且限制到 100 ms 的 `dt` 传入 Advance，benchmark 使用其固定 frame duration，不读墙钟。将 presentation 映射为脚底 avatar position 与头顶 `position+[0,2.05,0]` name-tag anchor；Billboard right=`{cos(camera.Yaw),0,-sin(camera.Yaw)}`，up=`right.Cross(camera.Forward()).Normalize()`。

同一 encoder 中先调用现有 terrain `Render`（clear），再 avatar `Render`（load+depth write），最后 name-tag `Render`（load+alpha+depth read）。零远端玩家时后两者不建 pass。

- [ ] **Step 5：运行 app、client、render 竞态回归**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo ./internal/client ./internal/render -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo -run "TestRemoteProtocolErrorClosesOnlyClientEndpoint|TestApplicationRenderPassOrder" -count=50'
```

Expected: PASS；50 次均只关闭错误 endpoint，pass 顺序和资源释放稳定，测试不显示窗口。

- [ ] **Step 6：提交**

```bash
git add internal/render/renderer.go cmd/mcgo/app.go cmd/mcgo/app_test.go cmd/mcgo/main.go cmd/mcgo/main_test.go cmd/mcgo/benchmark.go
git commit -m "feat: 集成多人客户端呈现"
```

---

## Task 15：用两个真实 TCP 客户端闭合多人纵向场景

**Files:**
- Create: `internal/server/multiplayer_tcp_integration_test.go`

**Test harness:**

```go
type multiplayerTCPClient struct {
	identity  network.Identity
	endpoint  network.ClientEndpoint
	receiver  *client.Receiver
	mirror    *client.Mirror
	remotes   *client.RemotePlayers
	local     network.PlayerState
	transcript []multiplayerEvent
}

func connectMultiplayerTCPClient(context.Context, string, network.Identity) (*multiplayerTCPClient, error)
func (client *multiplayerTCPClient) drainUntil(context.Context, func() bool) error
```

- [ ] **Step 1：写两客户端 Spawn/States/block/Despawn 的失败纵向测试**

在 `127.0.0.1:0` 启动真实 Host/TCP listener 和 Memory world/player store，连接固定不同 UUID 的 A=`阿明`、B=`Builder`。测试按以下顺序断言业务事件，不依赖 heartbeat 穿插：

1. A/B 都收到 Ready local PlayerState 和初始 chunk snapshot；
2. 双方各收到对方恰好一次 Spawn，且 Spawn 必须晚于包含目标脚底的 snapshot；
3. A 连续发送 8 个 PlayerInput，B 收到 A 的 States tick 严格递增且最终位置改变；
4. A 在双方可见 chunk 内 BreakBlock，A/B Mirror 最终 block ID、revision 和 chunk hash 相同；
5. 关闭 B 后，A 收到 B 的一个 Despawn，此后不再收到 B 的 States；
6. A 仍可发送输入并收到新的 local PlayerState，证明 B 断线没有终止 world。

每个等待使用共享 20 秒 context deadline 和明确 predicate；禁止裸 `time.Sleep`，超时时输出两个 transcript、roster 和最近 local state。

- [ ] **Step 2：运行测试并确认 RED**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TestMultiplayerTCPClientsSeeMoveEditAndDespawn" -count=1 -v'
```

Expected: FAIL；在 Tasks 1–14 完成前，第二客户端无法完整经历远端 roster 与状态链路。

- [ ] **Step 3：实现只存在于测试文件的 headless client driver**

driver 复用公开 `network.DialTCP`/`LoginClient`、`client.Receiver`、`Mirror`、`RemotePlayers`，按消息类型分流并记录去除 KeepAlive 的业务 transcript。它不 import `cmd/mcgo`、不创建 gfx device/window；发送序列、PlayerID、输入 yaw/pitch 和目标 block 都是固定常量。cleanup 先关两个 client，再以独立 shutdown context 停 Host，并断言无未消费 error。

- [ ] **Step 4：运行竞态和高重复 TCP 证明**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TestMultiplayerTCPClientsSeeMoveEditAndDespawn" -race -count=1 -v'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TestMultiplayerTCPClientsSeeMoveEditAndDespawn" -count=10'
```

Expected: PASS；10 次均满足相同业务相对顺序，测试进程不打开窗口或监听非 loopback 地址。

- [ ] **Step 5：提交**

```bash
git add internal/server/multiplayer_tcp_integration_test.go
git commit -m "test: 闭合双客户端 TCP 同步"
```

---

## Task 16：证明 8-client 有界压力、Memory/TCP parity 与全员重启恢复

**Files:**
- Modify: `internal/server/multiplayer_tcp_integration_test.go`
- Create: `internal/server/multiplayer_memory_integration_test.go`
- Create: `internal/server/multiplayer_restart_test.go`

**Deterministic script:**

```go
type multiplayerScriptStep struct {
	Tick   uint64
	Player int
	Input  *network.PlayerInput
	Break  *network.BreakBlock
	Place  *network.PlaceBlock
}

func fixedEightPlayerScript(ticks uint64) []multiplayerScriptStep
func canonicalMultiplayerTranscript([]multiplayerEvent) [32]byte
```

- [ ] **Step 1：写 8 个 TCP client 的固定 10 秒 soak 失败测试**

使用 8 个固定 PlayerID/中英文昵称并发登录 loopback Host，全部 Ready/互相各见 7 人后开始整整 10 秒固定脚本：每个 client 每 50 ms 发送一个轮转方向 PlayerInput，每 20 tick 由一个 client 在共同 wanted chunk 内交替放置/破坏方块；所有 client 持续 drain 到 Mirror/RemotePlayers。断言：无协议错误；每人 roster 始终不超过 7；States batch 1..7；至少收到 150 个递增 remote ticks；最终 8 个 Mirror 的目标 chunk revision/hash 一致。

测试内以同包只读 helper 每 tick 采样 session outbox、player jobs、completions 长度：high-water 不得超过各自 capacity，脚本停止后 5 秒内全部回落到 0。记录连接稳定期 goroutine 数，10 秒后不得超过稳定值+4；关闭全部连接并 shutdown 后不得超过登录前基线+4。

- [ ] **Step 2：运行 TCP soak 并确认 RED**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TestEightTCPClientsSoakIsBounded" -count=1 -v -timeout=45s'
```

Expected: FAIL；旧单玩家 Host、publication 和 persistence 无法承载该场景。

- [ ] **Step 3：写并通过 8-session Memory 手动 2000 tick 确定性测试**

构造 8 个容量 4096 的 Memory endpoint，以固定身份 `AttachSession`。本测试的 2000-tick 输入按每 10 tick 反向的对称小步移动，断言每人的脚底始终留在初始 chunk，所有方块命令也只触及该 chunk；因此整个测量期 wanted union 精确等于初始 wanted union。先把该 union 的每个 key 写入 MemoryStore，并用只存在于测试文件的 `trackedMemoryStore` 包装 `LoadChunk`，以 atomic counter 暴露当前 chunk load in-flight 数；Server 固定单 chunk worker。

正式记录前保持玩家不动并推进/drain，直到初始 union 的每个 key 都由 `Server.ChunkInfo` 报告 `sim.ChunkReady`、8 人全部 Ready、各 session 的初始 snapshot 已发送，同时 `pending/jobs/acquired/generated/incoming/queued` 为空且 tracked store 的 in-flight counter 为 0；再额外 Step 一次确认这些条件仍成立并丢弃全部 warm-up transcript。由于脚本被测试断言限制在同一初始 foot chunk，测量期不会改变 wanted set 或安排新的异步 chunk load/generate。

测量阶段只用 `Server.StepForTest()` 推进恰好 2000 tick，并复用 Step 4 的无墙钟 ingress barrier：每 tick 先断言 incoming 为空，按固定 PlayerID/sequence 发送本 tick 命令，用带 deadline 的 context 加 `runtime.Gosched()` 等到 `len(running.incoming)==expectedCommands`，再恰好 Step 一次并 drain 每个 client 到 local `PlayerState.ServerTick == result.Tick`；禁止发送后立即 Step 或使用 `time.Sleep`。barrier 超时输出 tick、expected/actual、待处理 sequence 和全部 transcript。相同 seed/script 连跑两次，比较规范化业务 transcript SHA-256、8 个 `PlayerSnapshot` hash、所有 touched chunk 权威 hash 和 8 个 Mirror hash，必须逐字节相同。heartbeat token 和真实时间字段从 transcript 排除，业务 tick/revision/PlayerID/message type/value 全部保留。

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TestEightMemorySessionsAreDeterministicFor2000Ticks" -count=1'
```

Expected after harness implementation: PASS；测试不依赖墙钟推进 world。

- [ ] **Step 4：比较同一 200-tick 脚本的 Memory/TCP 业务结果**

为 parity 运行器提供两种 packet-stream factory：`network.NewMemoryStreamPair` 与 loopback `network.ListenTCP`/`DialTCP`。两者都用 `LoginClient` 对 `BeginServerLogin`，并在 `PendingLogin.Accept` callback 内把得到的 Play `ServerEndpoint` 交给同一手动 Server harness；完成 8 个 LoginSuccess 后执行相同 200 tick 子脚本。

每个 tick 使用同一条无墙钟 barrier：先断言 `running.incoming` 为空；按固定 PlayerID/sequence 顺序发送该 tick 的全部命令；在尚未并发调用 `StepForTest` 的前提下，用带 deadline 的 context 加 `runtime.Gosched()` 等到 `len(running.incoming)==expectedCommands`；随后恰好调用一次 `StepForTest()`；最后 drain 每个 client，直到其 local `PlayerState.ServerTick == result.Tick`，并再次断言 incoming 为空后才进入下一 tick。禁止 `time.Sleep`。barrier 超时时输出 transport、tick、expected/actual count、仍待处理的 sequence 列表和 8 份 transcript。两种 transport 都完成 200 个 barrier 后，比较规范 transcript、8 个 player hash、touched chunk hash 与 Mirror hash；不得只比较 packet 数量。

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TestEightPlayerMemoryTCPParity" -count=10'
```

Expected: PASS；10 次两种 transport 的业务 hash 相同。

- [ ] **Step 5：写 DiskStore 全员关服重启恢复测试**

在 `t.TempDir()` 启动 DiskStore/Host，8 个身份登录后各移动到不同但已加载的安全位置并确认名字；等待相应 snapshot revision 被观察后关闭 clients，调用 Host Shutdown。用同一路径、seed 和 8 个身份重建进程内 Server/Host，乱序并发重连，逐 PlayerID 比较 current position、safe location、DisplayName、revision；再比较 edited chunk hash。session ID 必须是新运行时值，不进入比较或存档。

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TestEightPlayersSurviveDiskRestart" -count=1 -v'
```

Expected: PASS；8 个身份独立恢复，重启后无重复/串档。

- [ ] **Step 6：运行完整竞态纵向门禁**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "Test(EightTCPClientsSoakIsBounded|EightMemorySessionsAreDeterministicFor2000Ticks|EightPlayerMemoryTCPParity|EightPlayersSurviveDiskRestart)" -race -count=1 -v -timeout=120s'
```

Expected: PASS；race 无报告，队列/goroutine 回落，所有测试只用 loopback/headless。

- [ ] **Step 7：提交**

```bash
git add internal/server/multiplayer_tcp_integration_test.go internal/server/multiplayer_memory_integration_test.go internal/server/multiplayer_restart_test.go
git commit -m "test: 验证八玩家有界同步与恢复"
```

---

## Task 17：升级 scenario v6、性能指标、文档与全量出口门禁

> **2026-08-03 归档声明：** 本 Task 17 中原有 Step 6/Step 7 的固定 `/tmp/mcgo-m3c-{memory,tcp,current}.json` 命令、失败后从 Step 5 重跑的描述及宽松 sample 检查均只保留为历史证据，不再可执行。唯一有效的执行合同是 `2026-08-03-m3c-performance-repeatability.md` 的 Task 6–8：使用 commit 派生的 collision-safe 路径、只执行一次正式链，并要求精确 `200 measured ticks/1600 interest samples`；发生失败必须停下，不得按本归档段落重跑。

**Files:**
- Modify: `internal/client/perf.go`
- Modify: `internal/client/perf_test.go`
- Modify: `internal/network/benchmark_test.go`
- Modify: `internal/server/config.go`
- Modify: `internal/server/host.go`
- Create: `internal/server/host_stats_test.go`
- Create: `internal/server/multiplayer_bench_test.go`
- Create: `internal/render/multiplayer_bench_test.go`
- Modify: `cmd/mcgo/benchmark.go`
- Create: `cmd/mcgo/multiplayer_benchmark.go`
- Create: `cmd/mcgo/benchmark_v6_test.go`
- Modify: `cmd/perfcheck/main.go`
- Modify: `cmd/perfcheck/main_test.go`
- Modify: `internal/archcheck/deps_test.go`
- Modify: `.github/workflows/ci.yml`
- Modify: `Makefile`
- Modify: `README.md`
- Modify: `docs/notes/lan-server.md`
- Modify: `docs/notes/perf-baseline.json`
- Modify: `docs/notes/perf-baseline.md`

**Stable report schema:**

```go
type LatencySummary struct {
	Samples int     `json:"samples"`
	P50MS   float64 `json:"p50_ms"`
	P95MS   float64 `json:"p95_ms"`
	P99MS   float64 `json:"p99_ms"`
	MaxMS   float64 `json:"max_ms"`
}

type MultiplayerSummary struct {
	RemoteStateEncode  LatencySummary `json:"remote_state_encode"`
	RemoteStateDecode  LatencySummary `json:"remote_state_decode"`
	InterestDiff       LatencySummary `json:"interest_diff"`
	RosterApply        LatencySummary `json:"roster_apply"`
	Interpolation      LatencySummary `json:"interpolation"`
	AvatarSubmit       LatencySummary `json:"avatar_submit"`
	NameTagSubmit      LatencySummary `json:"name_tag_submit"`
	RemoteGPUComplete  LatencySummary `json:"remote_gpu_complete"`
	ServerOutboundBytes uint64         `json:"server_outbound_bytes"`
	OutboxHighWater     int            `json:"outbox_high_water"`
	PlayerJobsHighWater int            `json:"player_jobs_high_water"`
	PlayerDoneHighWater int            `json:"player_done_high_water"`
	PeakRSSBytes        uint64         `json:"peak_rss_bytes"`
}

type PerfReport struct {
	Multiplayer MultiplayerSummary `json:"multiplayer"`
}

type HostStats struct {
	ActivePlayers           int
	MaxSessionOutboxDepth   int
	PlayerSaveJobDepth      int
	PlayerSaveDoneDepth     int
}

func (host *Host) Stats() HostStats
```

**2026-08-03 已批准的可重复性策略（取代下文早期“所有共有指标”措辞）：**

- v6 Memory→TCP：比较 transport 相关稳定 p50/p95/p99、FPS、RSS、load/snapshot、protocol 与 persistence；raw max、queue high-water 和独立内存 server probe 不做跨 transport 相对比较。
- v6 同 transport：额外比较 server tick/interest p50/p95/p99、outbound 与 multiplayer RSS；raw max 和 queue high-water 仍只执行既有绝对门禁。
- server probe：8 登录完成后 warm-up 20 ticks，再由 TickObserver 信号驱动 200 measured ticks/1600 interest samples，不再使用第二个 50 ms ticker。

被拒绝的正式证据只保留用于诊断，不得提升为 baseline：

```text
a86285d45a00e85f2bb0eb0ae960b3d4efd04beeecb31c917a852f6537ffbe01  /tmp/mcgo-m3c-memory-b58d8bd.json
12886882b273dd2e0712e78dc2d5f6fb0587c0aacca215c86162f581b0308771  /tmp/mcgo-m3c-tcp-b58d8bd.json
875e4533728f3c4bbcaed153bb1af821f4970e066ea36b3ae7be0d8ba69aeef4  /tmp/mcgo-m3c-step6-compare-b58d8bd.log
428e9b61bd8a8bf782fdc4e8d54f488d544bee4b7da948873638fe40ba60a191  docs/notes/perf-baseline.json
ac4dfffd78fc3a31d56b3cf728651e805535b60d302d2c2f55273d23f79ecfdb  docs/notes/perf-baseline.md
```

最后两项仍是逐字冻结的 accepted v5 baseline；新 Step 6 完整通过前不得覆盖。

- [ ] **Step 1：写 v6 JSON、阈值和跨版本拒绝的失败测试并确认 RED**

`perf_test.go` 用手工 JSON 断言 scenario 6 的所有 latency samples/percentile、outbound/high-water/RSS 字段不可缺失。`benchmark_v6_test.go` 断言固定场景包含本地玩家、7 个 Spawn、每 tick 一个 count=7 States batch、7 个 Unicode 标签，且 `scenarioVersion==6`。`perfcheck` 测试固定：默认 scenario 不同拒绝比较；仅显式 `--allow-scenario-upgrade 5:6` 可执行 v5→v6 迁移验证，该模式只验证两份报告的版本字段完整性、硬件一致性和当前 v6 绝对门禁，不执行跨场景相对比较；反向/跳版本/硬件不同仍拒绝。v6 跨 transport 与同 transport 分别使用上方批准的稳定指标画像，严格大于 20% 失败、恰好 20% 通过；低样本/缺字段失败；server tick p99 `>=10ms` 失败而不是旧的 15 ms。

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/client ./internal/server ./cmd/mcgo ./cmd/perfcheck -run "Test(PerfReportV6|ScenarioV6|PerfcheckV6|PerfcheckV5SameScenario|PerformanceThresholds|InterestObserver|HostStats|BenchmarkServerEpoch|BenchmarkServerMeasuredWindow)" -count=1'
```

Expected: FAIL，报告仍是 v5 且 tick p99 门禁为 15 ms。

- [ ] **Step 2：实现无分配 latency recorder 与完整 v6 场景**

在 `internal/client/perf.go` 增加固定容量环形 `LatencyRecorder`，热路径 `Add(time.Duration)` 不分配，Summary 才复制/排序。benchmark 启动时向 roster 注入 7 个固定 PlayerID/Unicode Spawn，位置固定在相机附近且都进入 frustum；每个模拟 tick 生成一个按 ID 排序的 7-state batch，轨迹由 tick/player index 的 sin/cos 固定函数决定。每个 frame 调用 `rendered, err := app.frame(4096, fixedBenchmarkFrameDuration)` 并检查 err，不以实际 render 时长推进插值。

分别围绕 Codec Encode/Decode、roster Apply、Advance、Avatar Render submit、NameTag Prepare+Render 记录 CPU duration。`RemoteGPUComplete` 在正式 still/flying 采样之外运行 256 次独立 headless remote-only command buffer，以 `Submit`+`Device.Poll(true)` 记录完成时间，避免 Poll 扭曲主场景 FPS；报告文档标注它是 CPU 发起至 GPU queue 完成的贡献量，不声称硬件 timestamp query。

- [ ] **Step 3：实现 8-session server 性能探针与有界指标**

`Config` 新增仅在非 nil 时调用的 `InterestObserver func(time.Duration)`，在每观察者兴趣差分外层计时，默认 nil 路径不读墙钟。`Host.Stats` 在短临界区内汇总 active 数、所有 session 中最大的单 outbox 当前深度和 player jobs/completions 当前深度，不暴露 map/channel，也不重编码消息。

`cmd/mcgo/multiplayer_benchmark.go` 启动一个 nil-listener Host.Run，并通过 8 对 `network.NewMemoryStreamPair`/`Host.AcceptStream` 完成真实 Handshake/Login；server stream wrapper 在实际 Send 前用 `Codec.EncodeServer(state, packet)` 计算 canonical packet ID+payload+frame-prefix 逻辑字节数。8 个 headless client 全部登录后先 warm-up 20 个完整 tick，再由 `TickObserver` 完成信号驱动输入、`Host.Stats` high-water 与 process RSS 采样；不再使用第二个 50 ms ticker。measured window 必须精确得到 200 ticks/1600 interest samples。`internal/server/multiplayer_bench_test.go` 另用 `StepForTest` 提供 `BenchmarkEightPlayerInterest`。探针还要求 `ServerOutboundBytes>0`、outbox 不超过配置、jobs≤16、done≤2、peak RSS 非零且低于 2GiB；cleanup 等队列归零并 Shutdown。最终 `PerfReport.Ticks` 使用这份 8-client tick summary，而不是主画面单连接 server 的 tick recorder。

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network ./internal/server ./internal/render -run "^$" -bench "(RemotePlayerStateCodec|EightPlayerInterest|RemoteAvatarNameTag)" -benchmem -count=3'
```

Expected: benchmark 均可执行；固定容量热循环报告稳定 alloc 数，任何新增 allocation 都在评审中解释。

- [ ] **Step 4：锁定绝对门禁、依赖闭包、CI 和用户文档**

`validatePerformanceReport`/`perfcheck` 固定：still/flying FPS≥100、frame p99 `<12ms`、各 phase 与 multiplayer peak RSS `<2GiB`、8-client server tick p99 `<10ms`、max `<50ms`、所有 queue high-water 不超过硬上限。保留 physics `0 B/op, 0 allocs/op`。v6 相同 scenario 按 transport 是否相同选择上方批准的稳定指标画像；raw max 与 queue high-water 只执行既有绝对门禁。显式 `5:6` 只执行版本字段完整性、硬件一致性和当前 v6 绝对门禁。失败运行绝不覆盖 accepted baseline。

archcheck 明确检查 `cmd/mcgod` 依赖闭包没有 client/render/gfx/GLFW/WebGPU/x/image/font。README 把 M3 标成完成并链接 LAN 文档；`lan-server.md` 写 `--max-players 1..8`、同 ID/同名语义、可信局域网无认证/加密警告、两个客户端人工验收命令，以及断线/关服存档行为。窗口标题更新为 `minecraft-go — M3C multiplayer world`，但自动验证不启动窗口。

- [ ] **Step 5：运行格式、单测、race、fuzz、vet、archcheck 和无窗口构建**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && gofmt -w $(git ls-files -co --exclude-standard "*.go")'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network ./internal/server ./internal/client ./internal/render ./cmd/mcgo -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "^$" -fuzz "^FuzzSmallPacketCodec$" -fuzztime=10s'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "^$" -fuzz "^FuzzReadFrame$" -fuzztime=10s'
zsh -ic 'gvm use go1.26.0 >/dev/null && go vet ./...'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && CGO_ENABLED=0 GOOS=linux go build -o /tmp/mcgod-linux ./cmd/mcgod'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/physics -run "^$" -bench "^BenchmarkStepPlayer" -benchmem' | tee /tmp/mcgo-m3c-physics.txt
test "$(rg -c 'BenchmarkStepPlayer.*0 B/op +0 allocs/op' /tmp/mcgo-m3c-physics.txt)" -eq 3
test -z "$(gofmt -l $(git ls-files -co --exclude-standard "*.go"))"
git diff --check
```

Expected: 全部 exit 0；fuzz 无 crash/超大分配；Linux mcgod 不拉入 Darwin 客户端；physics 输出 0 B/op、0 allocs/op；gofmt/diff-check 为空。

- [ ] **Step 6：运行并接受同机 scenario v6 Memory/TCP 性能报告**

先保留旧 v5 baseline，运行新的 Memory 报告并立即执行显式迁移验证：

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --perf-output /tmp/mcgo-m3c-memory.json'
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck --baseline docs/notes/perf-baseline.json --current /tmp/mcgo-m3c-memory.json --max-regression 0.20 --allow-scenario-upgrade 5:6'
```

Expected: Memory 报告为 scenario 6，v5/v6 报告字段完整、硬件相同，且当前 v6 全部绝对门禁通过；该显式迁移不执行跨场景相对比较。迁移验证通过后才运行新的 TCP 报告和同 scenario 比较：

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport tcp --perf-output /tmp/mcgo-m3c-tcp.json'
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck --baseline /tmp/mcgo-m3c-memory.json --current /tmp/mcgo-m3c-tcp.json --max-regression 0.20'
```

Expected: 两个报告都是 scenario 6、硬件相同、绝对门禁通过，TCP 相对 Memory 的 transport 相关稳定指标回退不超过 20%；raw max、queue high-water 与独立内存 server probe 不参与跨 transport 相对比较。迁移验证与 Memory→TCP 比较都通过后，才用 Memory 报告的精确 JSON（保留尾换行）更新 `docs/notes/perf-baseline.json`；`perf-baseline.md` 记录 commit/hardware、全部比较命令、两个报告 SHA-256、全部多人指标和三组微基准原始结果，不手工杜撰数值。

- [ ] **Step 7：对 accepted v6 baseline 做第二次同机回归**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --perf-output /tmp/mcgo-m3c-current.json'
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck --baseline docs/notes/perf-baseline.json --current /tmp/mcgo-m3c-current.json --max-regression 0.20'
shasum -a 256 docs/notes/perf-baseline.json /tmp/mcgo-m3c-memory.json /tmp/mcgo-m3c-tcp.json /tmp/mcgo-m3c-current.json
```

Expected: perfcheck 输出通过，所有绝对门禁仍绿，accepted baseline 只来自 Step 6 的通过运行。若门禁失败，保留旧 baseline、修复原因并从 Step 5 重跑，不能放宽阈值。

- [ ] **Step 8：执行规格/代码双评审与最终状态检查**

逐条核对本计划规格覆盖矩阵和设计 §19 的全部退出条件；检查每个 Task 有 RED/GREEN 证据与单独 commit；运行：

```bash
git log --oneline -17
git status --short
rg -n 'ProtocolVersion = 2|scenarioVersion = 6|MaxPlayers|RemotePlayerSpawn|RemotePlayerDespawn|RemotePlayerStates' internal cmd
test -z "$(zsh -ic 'gvm use go1.26.0 >/dev/null && go list -deps ./cmd/mcgod' | rg 'internal/(client|render|gfx)|glfw|webgpu|x/image/font')"
```

Expected: 17 个任务各一个提交；状态只包含本任务待提交的 gate/docs 文件；关键标识存在；mcgod 禁止依赖查询无输出。

- [ ] **Step 9：提交**

```bash
git add internal/client/perf.go internal/client/perf_test.go internal/network/benchmark_test.go internal/server/config.go internal/server/host.go internal/server/host_stats_test.go internal/server/multiplayer_bench_test.go internal/render/multiplayer_bench_test.go cmd/mcgo/benchmark.go cmd/mcgo/multiplayer_benchmark.go cmd/mcgo/benchmark_v6_test.go cmd/perfcheck/main.go cmd/perfcheck/main_test.go internal/archcheck/deps_test.go .github/workflows/ci.yml Makefile README.md docs/notes/lan-server.md docs/notes/perf-baseline.json docs/notes/perf-baseline.md
git commit -m "chore: 关闭 M3C 多人同步里程碑"
```

---

## M3C 完成检查表

- [ ] 8 个不同 PlayerID 同时在线，第 9 个 ServerFull；重复 ID newcomer AlreadyOnline，旧会话保持。
- [ ] v2 golden/fuzz/v1 拒绝、兴趣矩阵和 publication 相对顺序全部通过。
- [ ] 客户端 roster 严格验证，4-slot 插值不外推，Reset/维度/大位移按规范重新 warm up。
- [ ] 方块人和 Unicode 昵称在固定 GPU/atlas 容量内渲染，glyph churn 稳定 tofu，字体/OFL/provenance 可复核。
- [ ] 16-entry cache、2 workers、16/2 queues 和单 ID 在途限制都有压力证明。
- [ ] 两客户端 TCP、8-client 10 秒 soak、2000-tick Memory、Memory/TCP parity 和 8 人 Disk restart 全绿。
- [ ] 错误 session 隔离，Flush/Shutdown 错误稳定且不跳过其他身份，队列和 goroutine 回落。
- [ ] scenario v6 绝对门禁与同机 20% 回归通过，accepted baseline 来自通过运行。
- [ ] test/race/fuzz/vet/gofmt/archcheck/Linux mcgod/physics alloc 全部通过，自动验证没有前台窗口。
- [ ] README/LAN/性能文档与实现一致，M3 可以关闭并进入 M4。
