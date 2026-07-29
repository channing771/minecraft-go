# M3B 二进制协议、TCP 直连与稳定玩家身份实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立严格版本化的端无关登录协议与 TCP 传输，交付可直连的 `mcgo` / 无渲染 `mcgod`，并按稳定玩家身份原子保存和恢复最小玩家状态。

**Architecture:** 用共享协议 driver 包裹内存或 TCP packet stream，只有完成 `Handshake → Login` 后才暴露 Play endpoint；长期存活的 `server.Host` 管理唯一在线槽、动态 `SessionID`、世界运行时和玩家存档。区块仍由 M3A `Server`/`Store` 管理，新增独立 `PlayerStore`、玩家保存协调器和本机 `profile`，所有网络、压缩和磁盘工作都留在 tick/渲染线程之外。

**Tech Stack:** Go 1.26.0（用户现有 GVM）、手写小端二进制 codec、规范 `uvarint`、`github.com/klauspost/compress/zstd` v1.19.1、TCP、CRC32C、JSON 本机档案、现有 `core`/`network`/`sim`/`server`/`storage`/`client`、race/fuzz/真实回环集成测试、无窗口性能门禁。

## Global Constraints

- 所有本地 Go 命令必须通过 `zsh -ic 'gvm use go1.26.0 >/dev/null && ...'` 使用用户现有 GVM；不得安装或下载另一套 Go。
- 所有新增或修改的文档使用中文；代码标识符、线上 magic 和既有英文术语保持技术原义。
- M3B 线上协议版本固定为 `1`；任何 wire 字节或语义变化必须提升版本，不能只改 golden。
- TCP 帧为 `[canonical uvarint frameLength][canonical uvarint packetID][payload]`；帧上限 2 MiB，小包 payload 上限 64 KiB。
- 只有 `ChunkSnapshot` 使用 zstd；压缩字节上限 1 MiB、声明/实际解压输出上限 2 MiB，decoder 最大内存也为 2 MiB。
- Handshake deadline 为 5 秒，Login deadline 为 10 秒；KeepAlive 每 5 秒发送，15 秒未收到匹配 token 即断开。
- pre-login 并发上限为 16，Play 在线槽为 1；第二个竞争登录必须得到 `ServerFull`。
- `PlayerID` 是客户端声明的 UUID v4 离线身份，不是认证或密钥；M3B 不增加 TLS、密码、公钥挑战或公网安全承诺。
- `SessionID` 只由服务端分配，非零、进程内单调递增且不复用；客户端永远不能声明它。
- 内存传输继续跳过二进制编解码，但与 TCP 共用状态机和消息验证；成功 Send 后消息及切片不可再修改。
- 玩家文件位于 `<world>/players/<canonical-player-id>.player`，上限 1 MiB，使用 v1 schema、CRC32C 和同目录原子替换。
- 玩家恢复严格按“当前位置 → 安全落点 → 确定性世界出生点”；候选区块 Ready 和 AABB 验证完成前保持 `Ready=false`。
- 断线玩家 dirty 未刷净时只允许同一 `PlayerID` 重连，其他 ID 得到 `StoreUnavailable`，确保离线 dirty 缓存最多一份。
- `network` 不 import `server`、`client`、`sim`、`storage` 或渲染包；`profile` 只允许 import `core`；`sim` 不 import `network` 或 `storage`。
- `cmd/mcgod` 不 import `client`、`render`、`gfx`、GLFW 或 WebGPU，并必须通过 `CGO_ENABLED=0 GOOS=linux go build ./cmd/mcgod`。
- 自动测试和性能验证不得启动或聚焦前台窗口；`mcgo` UI 只能在用户明确进行人工验收时启动。
- 每个任务都执行 red → green → refactor、focused `-race -count=1`、独立代码评审，并恰好创建一个隔离提交后才进入下一任务。
- 既有门禁不放宽：≥100 FPS、frame p99 `<12 ms`、RSS `<2 GiB`、server tick p99 `<10 ms`、tick max `<50 ms`、physics `0 allocs/op`；同机基准回退超过 20% 判红。

## 文件与职责映射

| 路径 | M3B 完成后的职责 |
|---|---|
| `internal/core/player_id.go` | UUID v4 `PlayerID`、规范文本解析/格式化、昵称规范化 |
| `internal/profile/profile.go` | 本机全局档案读取、严格 JSON 校验、首次创建和改名 |
| `internal/profile/atomic.go` | `0600` 档案的临时文件 + sync + rename + 目录 sync |
| `internal/network/packet.go` | 协议状态、方向、Handshake/Login/Play packet 集合和稳定错误码 |
| `internal/network/registry.go` | state+direction 局部 packet ID 注册表与完整性校验 |
| `internal/network/codec_primitives.go` | 有界小端基础类型、规范 uvarint、UTF-8 和有限 float codec |
| `internal/network/codec.go` | Handshake/Login 与小型 Play packet 显式编解码 |
| `internal/network/chunk_codec.go` | 规范区块逻辑 payload、zstd envelope 和 1/2 MiB 上限 |
| `internal/network/frame.go` | 2 MiB 有界 TCP framing、短读短写与规范长度前缀 |
| `internal/network/stream.go` | client/server packet stream 与 listener 抽象 |
| `internal/network/tcp.go` | TCP dial/listen、deadline、NoDelay、keepalive、幂等关闭 |
| `internal/network/memory.go` | 类型值 packet stream 与已附着 Play 测试端点 |
| `internal/network/login.go` | 共享 Handshake/Login driver、PendingLogin 和 Play endpoint 收窄 |
| `internal/storage/player_types.go` | `StoredPlayer`/`PlayerSave`/`PlayerStore`/`WorldStore` 契约 |
| `internal/storage/player_codec.go` | v1 玩家 envelope、CRC32C、有界显式 payload codec |
| `internal/storage/player_migration.go` | 连续玩家 schema 迁移注册表 |
| `internal/storage/memory.go` | 玩家深拷贝、revision 单调的内存实现 |
| `internal/storage/disk.go` | `players/` 下按 ID 原子加载/保存玩家文件 |
| `internal/sim/player.go` | 恢复候选、安全落点、动态注册/注销与保存快照 |
| `internal/server/session.go` | 动态 session、generation、reader/writer、心跳和退出结果 |
| `internal/server/publication.go` | 按显式 SessionID 发布 snapshot/delta/forget/rejection/player state |
| `internal/server/player_persistence.go` | 单玩家 cache、异步保存、retry、重连背压和 flush |
| `internal/server/host.go` | listener、pre-login 上限、唯一在线槽、登录、attach/detach、关服 |
| `internal/client/receiver.go` | 阻塞 endpoint reader 到有界 inbox，渲染线程预算化 drain |
| `cmd/mcgo/main.go` | `--connect`/`--name` 参数、互斥校验和远程错误退出 |
| `cmd/mcgo/app.go` | profile、本地 Host/远程 TCP 装配与统一 receiver 生命周期 |
| `cmd/mcgod/main.go` | 无图形专用服务端参数、监听、信号与安全关服 |
| `docs/notes/lan-server.md` | 中文局域网启动、连接、备份与无认证安全警告 |
| `internal/server/tcp_integration_test.go` | TCP + 磁盘断线重启、ServerFull、位置/区块一致性纵向证明 |
| `docs/notes/perf-baseline.*` | 场景 v5 与 M3B protocol/player-store 微基准基线 |

## 规格覆盖矩阵

| 设计文档要求 | 实施任务 |
|---|---|
| 身份、档案、昵称与信任边界（§3、§9） | Task 1、2、15 |
| 状态机、packet IDs、payload、错误码（§5–§6） | Task 2–5、8 |
| zstd 与恶意输入上限（§7、§15） | Task 3–6、9、17 |
| TCP、deadline、心跳、背压、关闭（§8、§16） | Task 6–8、12、14 |
| 玩家 schema、原子文件与迁移（§10） | Task 9–10 |
| 当前→安全→出生点恢复（§11） | Task 11、17 |
| dirty/revision/autosave/retry/flush（§12） | Task 10、13–14 |
| 动态 SessionID 与发布重构（§13） | Task 11–12 |
| `mcgo` / `mcgod` CLI 与生命周期（§14） | Task 15–16 |
| golden/fuzz/parity/真实重启（§17） | Task 1–10、17 |
| 性能、CI、依赖和出口条件（§18–§20） | Task 7、12、15–17 |

---

### Task 1：定义稳定 PlayerID 与本机全局档案

**Files:**
- Create: `internal/core/player_id.go`
- Create: `internal/core/player_id_test.go`
- Create: `internal/profile/profile.go`
- Create: `internal/profile/profile_test.go`
- Create: `internal/profile/profile_fuzz_test.go`
- Create: `internal/profile/atomic.go`
- Modify: `internal/archcheck/deps_test.go`

**Interfaces:**
- Consumes: `crypto/rand.Reader`, `os.UserConfigDir`, 标准库 JSON/UTF-8/filesystem
- Produces:

```go
package core

type PlayerID [16]byte

func ParsePlayerID(string) (PlayerID, error)
func (PlayerID) String() string
func (PlayerID) Valid() bool
func NormalizeDisplayName(string) (string, error)
```

```go
package profile

const CurrentVersion uint32 = 1

type Profile struct {
	Version     uint32
	PlayerID    core.PlayerID
	DisplayName string
}

type Options struct {
	Path          string
	RequestedName *string
	Random        io.Reader
}

func DefaultPath() (string, error)
func LoadOrCreate(Options) (Profile, error)
```

- [ ] **Step 1：写 PlayerID、昵称和 profile 的失败测试**

`internal/core/player_id_test.go` 至少固定以下行为：

```go
func TestPlayerIDCanonicalRoundTrip(t *testing.T) {
	want := "00112233-4455-4677-8899-aabbccddeeff"
	id, err := core.ParsePlayerID(want)
	if err != nil || !id.Valid() || id.String() != want {
		t.Fatalf("round trip = %q valid=%v err=%v", id.String(), id.Valid(), err)
	}
	for _, bad := range []string{
		"", "00112233445546778899aabbccddeeff",
		"00112233-4455-3677-8899-aabbccddeeff",
		"00112233-4455-4677-0899-aabbccddeeff",
		"00112233-4455-4677-8899-AABBCCDDEEFF",
	} {
		if _, err := core.ParsePlayerID(bad); err == nil {
			t.Fatalf("accepted non-canonical ID %q", bad)
		}
	}
}

func TestNormalizeDisplayName(t *testing.T) {
	got, err := core.NormalizeDisplayName("  陈 Chen  ")
	if err != nil || got != "陈 Chen" { t.Fatalf("got=%q err=%v", got, err) }
	for _, bad := range []string{"", "   ", "a\nb", strings.Repeat("界", 33)} {
		if _, err := core.NormalizeDisplayName(bad); err == nil {
			t.Fatalf("accepted name %q", bad)
		}
	}
}
```

`internal/profile/profile_test.go` 使用固定 16 字节 reader，断言：首次创建 v1 档案、权限
`0600`、目录权限不宽于 `0700`；第二次读取 ID 不变；显式改名不换 ID；损坏 JSON、重复字段、
未知字段、未来版本、非 v4 ID、rename 前注入失败都返回错误且不覆盖旧文件。核心测试：

```go
func TestLoadOrCreateKeepsIDWhenNameChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "minecraft-go", "profile.json")
	firstName := "Chen"
	first, err := LoadOrCreate(Options{
		Path: path, RequestedName: &firstName,
		Random: bytes.NewReader([]byte("0123456789abcdef")),
	})
	if err != nil { t.Fatal(err) }
	secondName := "Alex"
	second, err := LoadOrCreate(Options{Path: path, RequestedName: &secondName})
	if err != nil { t.Fatal(err) }
	if second.PlayerID != first.PlayerID || second.DisplayName != "Alex" {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
}
```

- [ ] **Step 2：运行测试并确认 RED**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/core ./internal/profile -race -count=1'
```

Expected: FAIL，`core.PlayerID` 和 `internal/profile` 尚不存在。

- [ ] **Step 3：实现规范 ID、昵称与原子 profile**

ID 只接受精确 36 字节小写文本、固定连字符、version nibble `4`、variant 高两位 `10`：

```go
func (id PlayerID) Valid() bool {
	return id != (PlayerID{}) && id[6]>>4 == 4 && id[8]&0xc0 == 0x80
}

func NormalizeDisplayName(name string) (string, error) {
	if !utf8.ValidString(name) { return "", errors.New("core: display name is not UTF-8") }
	name = strings.TrimSpace(name)
	count := utf8.RuneCountInString(name)
	if count < 1 || count > 32 || len(name) > 128 {
		return "", errors.New("core: display name length is outside 1..32")
	}
	for _, r := range name {
		if unicode.IsControl(r) { return "", errors.New("core: display name contains control character") }
	}
	return name, nil
}
```

首次创建时从 `Options.Random` 读取恰好 16 字节；nil 时用 `crypto/rand.Reader`，再设置：

```go
id[6] = id[6]&0x0f | 0x40
id[8] = id[8]&0x3f | 0x80
```

严格 JSON decoder 必须逐 token 检查重复/未知字段。`atomic.go` 使用同目录
`.profile.tmp-*`、full-write、`Chmod(0600)`、file Sync、Close、Rename、parent Sync；失败只删除
本次精确临时路径。`DefaultPath` 返回 `filepath.Join(userConfigDir, "minecraft-go", "profile.json")`。

- [ ] **Step 4：运行 focused race 与 fuzz smoke**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/core ./internal/profile ./internal/archcheck -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/profile -run "^$" -fuzz FuzzDecodeProfile -fuzztime=5s'
```

Expected: PASS；fuzz 只允许返回合法 v1 Profile 或有界错误，不 panic、不改写文件。

- [ ] **Step 5：提交**

```bash
git add internal/core/player_id.go internal/core/player_id_test.go internal/profile internal/archcheck/deps_test.go
git commit -m "feat: 添加稳定玩家档案"
```

---

### Task 2：冻结协议状态、消息集合与 packet 注册表

**Files:**
- Create: `internal/network/packet.go`
- Create: `internal/network/packet_test.go`
- Create: `internal/network/registry.go`
- Create: `internal/network/registry_test.go`
- Modify: `internal/network/message.go`
- Modify: `internal/network/message_test.go`
- Modify: `internal/network/snapshot.go`
- Modify: `internal/network/snapshot_test.go`

**Interfaces:**
- Consumes: Task 1 `core.PlayerID`, `core.NormalizeDisplayName`；现有 Play 消息及 `Validate`
- Produces:

```go
const ProtocolVersion uint32 = 1

type State uint8
const (
	StateHandshake State = iota + 1
	StateLogin
	StatePlay
)

type ClientPacket interface{ clientPacket() }
type ServerPacket interface{ serverPacket() }

type ClientHello struct{ ProtocolVersion uint32 }
type ServerHello struct{ ProtocolVersion uint32 }
type HandshakeReject struct {
	ServerProtocolVersion uint32
	Code HandshakeRejectCode
	Message string
}
type LoginStart struct { PlayerID core.PlayerID; DisplayName string }
type LoginSuccess struct{ PlayerID core.PlayerID }
type LoginReject struct { Code LoginRejectCode; Message string }
type KeepAlive struct{ Token uint64 }
type KeepAliveReply struct{ Token uint64 }
type Disconnect struct { Code DisconnectCode; Message string }

func ValidateClientPacket(State, ClientPacket) error
func ValidateServerPacket(State, ServerPacket) error
```

- [ ] **Step 1：写失败的状态、错误码与注册表完整性测试**

```go
func TestProtocolV1PacketIDsAreFrozen(t *testing.T) {
	client := []struct{ state State; packet ClientPacket; id uint32 }{
		{StateHandshake, ClientHello{}, 0}, {StateLogin, LoginStart{}, 0},
		{StatePlay, PlayerInput{}, 0}, {StatePlay, BreakBlock{}, 1},
		{StatePlay, PlaceBlock{}, 2}, {StatePlay, RequestChunkResync{}, 3},
		{StatePlay, KeepAliveReply{}, 4},
	}
	server := []struct{ state State; packet ServerPacket; id uint32 }{
		{StateHandshake, ServerHello{}, 0}, {StateHandshake, HandshakeReject{}, 1},
		{StateLogin, LoginSuccess{}, 0}, {StateLogin, LoginReject{}, 1},
		{StatePlay, ChunkSnapshot{}, 0}, {StatePlay, BlockChanges{}, 1},
		{StatePlay, ForgetChunks{}, 2}, {StatePlay, PlayerState{}, 3},
		{StatePlay, CommandRejected{}, 4}, {StatePlay, KeepAlive{}, 5},
		{StatePlay, Disconnect{}, 6},
	}
	assertClientRegistry(t, client)
	assertServerRegistry(t, server)
}
```

另建表驱动测试，断言 `LoginStart` 拒绝零/非 v4 ID 和非法昵称；KeepAlive token 不能为 0；
Handshake/Login/Disconnect 错误码 0 或未知值非法；Play packet 放进 Handshake/Login 状态非法；
`BlockChanges` 为 1–4096；`ForgetChunks` 为 1–4096 且无重复 chunk；所有 float 必须有限。

- [ ] **Step 2：运行 network 测试并确认 RED**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "TestProtocolV1|TestValidate" -count=1'
```

Expected: FAIL，协议状态和新 packet 类型未定义。

- [ ] **Step 3：实现封闭 packet 集合和显式注册表**

Play 类型同时实现原 `ClientMessage`/`ServerMessage` 与新 packet marker；Handshake/Login 类型只实现
packet marker。错误码按 spec 固定为显式非零常量，例如：

```go
type LoginRejectCode uint8
const (
	LoginServerFull LoginRejectCode = iota + 1
	LoginInvalidIdentity
	LoginPlayerDataCorrupt
	LoginStoreUnavailable
	LoginProtocolViolation
	LoginInternalError
)
```

注册表使用显式 type switch，不用 reflect，不依赖 `iota` 自动推导 packet ID：

```go
func clientPacketID(state State, packet ClientPacket) (uint32, bool) {
	switch state {
	case StateHandshake:
		_, ok := packet.(ClientHello); return 0, ok
	case StateLogin:
		_, ok := packet.(LoginStart); return 0, ok
	case StatePlay:
		switch packet.(type) {
		case PlayerInput: return 0, true
		case BreakBlock: return 1, true
		case PlaceBlock: return 2, true
		case RequestChunkResync: return 3, true
		case KeepAliveReply: return 4, true
		}
	}
	return 0, false
}
```

server registry、ID→空 packet factory 和 validators 同样完整写出。Command rejection wire reason 显式映射现有
八种 `RejectReason` 到 1–8，禁止用字符串或枚举内存值直接上网。

- [ ] **Step 4：运行 network 全包 race 测试**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network ./internal/core -race -count=1'
```

Expected: PASS；注册表覆盖每个封闭 packet 恰好一次，非法 state/type 组合全部拒绝。

- [ ] **Step 5：提交**

```bash
git add internal/network/packet.go internal/network/packet_test.go internal/network/registry.go internal/network/registry_test.go internal/network/message.go internal/network/message_test.go internal/network/snapshot.go internal/network/snapshot_test.go
git commit -m "feat: 冻结 M3B 协议状态与消息表"
```

---

### Task 3：实现有界基础二进制编解码

**Files:**
- Create: `internal/network/codec_primitives.go`
- Create: `internal/network/codec_primitives_test.go`
- Create: `internal/network/codec_primitives_fuzz_test.go`

**Interfaces:**
- Consumes: Task 2 规定的小端类型、规范 uvarint、字符串和 float 上限
- Produces:

```go
type byteEncoder struct{ data []byte; err error }
type byteDecoder struct{ data []byte; offset int }

func (e *byteEncoder) uvarint(uint32)
func (e *byteEncoder) u8(uint8)
func (e *byteEncoder) i8(int8)
func (e *byteEncoder) u16(uint16)
func (e *byteEncoder) u32(uint32)
func (e *byteEncoder) i32(int32)
func (e *byteEncoder) u64(uint64)
func (e *byteEncoder) bool(bool)
func (e *byteEncoder) string(string, int)
func (e *byteEncoder) f32(float32)
func (d *byteDecoder) uvarint() (uint32, error)
func (d *byteDecoder) u8() (uint8, error)
func (d *byteDecoder) i8() (int8, error)
func (d *byteDecoder) u16() (uint16, error)
func (d *byteDecoder) u32() (uint32, error)
func (d *byteDecoder) i32() (int32, error)
func (d *byteDecoder) u64() (uint64, error)
func (d *byteDecoder) bool() (bool, error)
func (d *byteDecoder) string(maxBytes, maxRunes int) (string, error)
func (d *byteDecoder) f32() (float32, error)
func (d *byteDecoder) done() error
```

- [ ] **Step 1：写规范 uvarint、短输入和有限 float 的失败测试**

```go
func TestCanonicalUvarintBoundaries(t *testing.T) {
	tests := []struct{ value uint32; encoded []byte }{
		{0, []byte{0x00}}, {1, []byte{0x01}}, {127, []byte{0x7f}},
		{128, []byte{0x80, 0x01}}, {math.MaxUint32, []byte{0xff,0xff,0xff,0xff,0x0f}},
	}
	for _, tc := range tests {
		var e byteEncoder; e.uvarint(tc.value)
		if !bytes.Equal(e.data, tc.encoded) { t.Fatalf("%d => %x", tc.value, e.data) }
		d := byteDecoder{data: tc.encoded}
		got, err := d.uvarint()
		if err != nil || got != tc.value || d.done() != nil { t.Fatalf("got=%d err=%v", got, err) }
	}
	for _, bad := range [][]byte{{0x80}, {0x81,0x00}, {0xff,0xff,0xff,0xff,0x1f}} {
		d := byteDecoder{data: bad}
		if _, err := d.uvarint(); err == nil { t.Fatalf("accepted %x", bad) }
	}
}
```

同时测试 bool 只接受 0/1、所有 fixed-width 类型逐字节截断、字符串先检查长度再分配、非法 UTF-8、
尾随字节、NaN/±Inf；fuzz 任意字节序列只允许有界成功或错误。

- [ ] **Step 2：运行测试并确认 RED**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "TestCanonicalUvarint|TestPrimitive" -count=1'
```

Expected: FAIL，基础 codec 不存在。

- [ ] **Step 3：实现无反射、无隐式分配的 primitive codec**

decoder 在读取 uvarint 时保留原始字节数，再用规范长度比较：

```go
func canonicalUvarintLength(value uint32) int {
	switch {
	case value < 1<<7: return 1
	case value < 1<<14: return 2
	case value < 1<<21: return 3
	case value < 1<<28: return 4
	default: return 5
	}
}
```

所有 `take(n)` 先用 `n < 0 || n > len(data)-offset` 防整数溢出。`string` 先读长度，比较
`maxBytes` 与剩余字节，再验证 UTF-8/rune 数；`f32` 用 `math.Float32frombits` 后立即拒绝
`IsNaN/IsInf`。encoder 遇到非法值设置首个错误并停止追加。

- [ ] **Step 4：运行 race 与 fuzz smoke**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "^$" -fuzz FuzzPrimitiveDecoder -fuzztime=5s'
```

Expected: PASS；fuzz 不产生 panic、超界成功或超过输入/固定上限的分配。

- [ ] **Step 5：提交**

```bash
git add internal/network/codec_primitives.go internal/network/codec_primitives_test.go internal/network/codec_primitives_fuzz_test.go
git commit -m "feat: 添加有界协议基础编解码"
```

---

### Task 4：编码 Handshake、Login 与小型 Play packet

**Files:**
- Create: `internal/network/codec.go`
- Create: `internal/network/codec_test.go`
- Create: `internal/network/codec_fuzz_test.go`

**Interfaces:**
- Consumes: Task 2 packet registry/validators；Task 3 `byteEncoder`/`byteDecoder`
- Produces:

```go
func encodeClientPacketPayload(State, ClientPacket) (packetID uint32, payload []byte, err error)
func decodeClientPacketPayload(State, packetID uint32, payload []byte) (ClientPacket, error)
func encodeServerControlPayload(State, ServerPacket) (packetID uint32, payload []byte, err error)
func decodeServerControlPayload(State, packetID uint32, payload []byte) (ServerPacket, error)
```

`encode/decodeServerControlPayload` 处理除 `ChunkSnapshot` 外的全部 Server packet；Task 5 的
`Codec` 对 Play/S→C/ID 0 做唯一 snapshot 分派，不以临时 wire 表示代替。

- [ ] **Step 1：写固定字节 golden 与恶意 payload 失败测试**

```go
func TestProtocolV1SmallPacketGolden(t *testing.T) {
	tests := []struct{
		state State; packet ClientPacket; wantID uint32; wantHex string
	}{
		{StateHandshake, ClientHello{ProtocolVersion: 1}, 0, "01"},
		{StatePlay, PlayerInput{
			Sequence: 1, MoveX: -1, MoveZ: 1, Jump: true, Yaw: 1.5, Pitch: -0.5,
		}, 0, "0100000000000000ff01010000c03f000000bf"},
		{StatePlay, BreakBlock{Sequence: 2, Yaw: 0, Pitch: 0}, 1,
			"02000000000000000000000000000000"},
	}
	for _, tc := range tests {
		id, got, err := encodeClientPacketPayload(tc.state, tc.packet)
		if err != nil || id != tc.wantID || hex.EncodeToString(got) != tc.wantHex {
			t.Fatalf("%T id=%d payload=%x err=%v", tc.packet, id, got, err)
		}
		round, err := decodeClientPacketPayload(tc.state, id, got)
		if err != nil || !reflect.DeepEqual(round, tc.packet) { t.Fatalf("round=%+v err=%v", round, err) }
	}
}
```

另以表驱动冻结全部 Handshake/Login/Play 控制消息的字段顺序和错误码；覆盖 unknown ID、错误 state、
截断每个字段、尾随字节、`BlockChanges` 非连续 revision/乱序/跨 chunk、4097 changes、4097 forget、
非法 bool/float/block/dimension。server test 必须明确断言 Play/S→C/ID 0 留给 Task 5 snapshot，而非被
control decoder 接受。

- [ ] **Step 2：运行 focused codec 测试并确认 RED**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "TestProtocolV1SmallPacket|TestSmallPacket" -count=1'
```

Expected: FAIL，packet payload codec 不存在。

- [ ] **Step 3：逐类型实现显式编码与解码**

每个 switch case 严格按 spec §6.4 顺序写字段；decode 完成后必须同时执行 `decoder.done()` 和
`Validate*Packet`：

```go
case PlayerInput:
	var e byteEncoder
	e.u64(message.Sequence); e.i8(message.MoveX); e.i8(message.MoveZ)
	e.bool(message.Jump); e.f32(message.Yaw); e.f32(message.Pitch)
	return 0, e.data, e.err
```

`BlockChanges` 和 `ForgetChunks` 在读 count 后先验证 `count <= 4096` 且最小剩余字节足够，再分配。
Command rejection 通过显式 reason↔wire 表，未知字符串 reason 编码失败。所有 decoder 返回带
state/direction/packet ID 上下文的错误，但不包含任意原始 payload。

- [ ] **Step 4：运行 network race 与 codec fuzz smoke**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "^$" -fuzz FuzzSmallPacketCodec -fuzztime=5s'
```

Expected: PASS；任意成功 decode 再 encode 得到规范唯一字节。

- [ ] **Step 5：提交**

```bash
git add internal/network/codec.go internal/network/codec_test.go internal/network/codec_fuzz_test.go
git commit -m "feat: 编码 M3B 控制与游戏消息"
```

---
### Task 5：实现区块快照逻辑 codec 与有界 zstd envelope

**Files:**
- Create: `internal/network/chunk_codec.go`
- Create: `internal/network/chunk_codec_test.go`
- Create: `internal/network/chunk_codec_fuzz_test.go`
- Create: `internal/network/testdata/chunk-snapshot-v1.bin`
- Modify: `internal/network/codec.go`

**Interfaces:**
- Consumes: Task 2 `ChunkSnapshot.Validate` 和 packet ID；Task 3 primitives；Task 4 control codec
- Produces:

```go
const (
	MaxCompressedSnapshot = 1 << 20
	MaxDecodedSnapshot    = 2 << 20
	MaxSmallPayload       = 64 << 10
)

type Codec struct {
	encoder *zstd.Encoder
	decoder *zstd.Decoder
}

func NewCodec() (*Codec, error)
func (c *Codec) Close() error
func (c *Codec) EncodeClient(State, ClientPacket) (uint32, []byte, error)
func (c *Codec) DecodeClient(State, uint32, []byte) (ClientPacket, error)
func (c *Codec) EncodeServer(State, ServerPacket) (uint32, []byte, error)
func (c *Codec) DecodeServer(State, uint32, []byte) (ServerPacket, error)

// chunk_codec_test.go：返回拥有独立 slices 的规范 24-section fixture。
func fixtureSnapshot(core.ChunkPos, revision uint64) ChunkSnapshot
```

- [ ] **Step 1：写快照 fixture、边界、所有权和 fuzz 失败测试**

```go
var updateProtocolFixtures = flag.Bool(
	"update-protocol-fixtures", false, "重写已提交的协议 fixture",
)

func TestChunkSnapshotV1Fixture(t *testing.T) {
	snapshot := fixtureSnapshot(core.ChunkPos{X: -3, Z: 7}, 19)
	codec, err := NewCodec(); if err != nil { t.Fatal(err) }; defer codec.Close()
	id, encoded, err := codec.EncodeServer(StatePlay, snapshot)
	if err != nil || id != 0 { t.Fatalf("id=%d err=%v", id, err) }
	path := filepath.Join("testdata", "chunk-snapshot-v1.bin")
	if *updateProtocolFixtures {
		if err := os.WriteFile(path, encoded, 0o644); err != nil { t.Fatal(err) }
	}
	want, err := os.ReadFile(path); if err != nil { t.Fatal(err) }
	if !bytes.Equal(encoded, want) { t.Fatal("v1 snapshot fixture drift; bump ProtocolVersion") }
	round, err := codec.DecodeServer(StatePlay, id, want)
	if err != nil || !reflect.DeepEqual(round, snapshot) { t.Fatalf("round=%+v err=%v", round, err) }
}
```

测试还必须逐项覆盖：24 个 section 的 Single/Indexed4/Indexed8/Direct；负 chunk 坐标；decoded 和
compressed 长度 `limit-1/limit/limit+1`；实际解压长度不等于声明；zstd checksum 损坏；尾随字节；
非法 palette slot/word count/high bits；Encode 后修改原 snapshot 不改变已编码字节；Decode 后修改返回
切片不影响第二次 Decode。

- [ ] **Step 2：运行 fixture 测试并确认 RED**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "TestChunkSnapshotV1|TestSnapshotBounds" -count=1'
```

Expected: FAIL，`Codec` 和 snapshot zstd 编解码尚不存在。

- [ ] **Step 3：实现规范逻辑 payload 与复用 zstd codec**

`encodeLogicalSnapshot` 严格写入 dim/chunk/revision/24 sections；每种 storage 只写 spec 规定字段。
创建 codec 时固定单并发和 checksum：

```go
encoder, err := zstd.NewWriter(nil,
	zstd.WithEncoderConcurrency(1),
	zstd.WithEncoderCRC(true),
)
decoder, err := zstd.NewReader(nil,
	zstd.WithDecoderConcurrency(1),
	zstd.WithDecoderMaxMemory(MaxDecodedSnapshot),
)
```

外层使用两个小端 `uint32` 长度。Decode 在调用 zstd 前检查 payload 总长、compressed ≤1 MiB、
decoded ≤2 MiB；`DecodeAll` 后检查实际长度完全相等，再执行 `ChunkSnapshot.Validate`。

`Codec.Encode/DecodeClient` 委托 Task 4；`Encode/DecodeServer` 仅在 `StatePlay && id/type == snapshot`
时走 zstd，其余委托 control codec。任何小包超过 64 KiB 都拒绝。

- [ ] **Step 4：生成一次 fixture 并立即关闭更新模式复验**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run TestChunkSnapshotV1Fixture -update-protocol-fixtures -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run TestChunkSnapshotV1Fixture -count=1'
```

Expected: 两次 PASS；第二次只读取已提交候选字节，任何漂移失败。

- [ ] **Step 5：运行 race 与 snapshot fuzz smoke**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "^$" -fuzz FuzzChunkSnapshotCodec -fuzztime=5s'
```

Expected: PASS；恶意 zstd 不导致 panic 或超过 2 MiB 的输出/内存预算。

- [ ] **Step 6：提交**

```bash
git add internal/network/chunk_codec.go internal/network/chunk_codec_test.go internal/network/chunk_codec_fuzz_test.go internal/network/testdata/chunk-snapshot-v1.bin internal/network/codec.go
git commit -m "feat: 压缩有界区块快照协议"
```

---

### Task 6：实现规范有界 TCP framing

**Files:**
- Create: `internal/network/frame.go`
- Create: `internal/network/frame_test.go`
- Create: `internal/network/frame_fuzz_test.go`

**Interfaces:**
- Consumes: Task 3 规范 uvarint 规则；Task 5 payload 上限
- Produces:

```go
const MaxFrameBytes = 2 << 20

func WriteFrame(io.Writer, packetID uint32, payload []byte) error
func ReadFrame(io.Reader) (packetID uint32, payload []byte, err error)

// frame_test.go：每次 Read 最多返回一个字节。
type oneByteReader struct { data []byte; offset int }
func (r *oneByteReader) Read([]byte) (int, error)
```

- [ ] **Step 1：写拆包、粘包、短读短写和恶意长度失败测试**

```go
func TestFrameSplitAndCoalescedReads(t *testing.T) {
	var wire bytes.Buffer
	if err := WriteFrame(&wire, 3, []byte{1,2,3}); err != nil { t.Fatal(err) }
	if err := WriteFrame(&wire, 4, []byte{5,6}); err != nil { t.Fatal(err) }
	r := &oneByteReader{data: wire.Bytes()}
	id, payload, err := ReadFrame(r)
	if err != nil || id != 3 || !bytes.Equal(payload, []byte{1,2,3}) { t.Fatal(id, payload, err) }
	id, payload, err = ReadFrame(r)
	if err != nil || id != 4 || !bytes.Equal(payload, []byte{5,6}) { t.Fatal(id, payload, err) }
}
```

另断言：`frameLength=0`、2 MiB+1、长度和实际 EOF 不符、packet ID 截断/溢出/非规范、frame 内只有
packet ID、payload 恰好达到上限、writer 每次只写 1 字节、writer 返回 `(0,nil)` 时得到
`io.ErrShortWrite`。fuzz 任意输入不得 panic。

- [ ] **Step 2：运行 frame 测试并确认 RED**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "TestFrame" -count=1'
```

Expected: FAIL，`ReadFrame`/`WriteFrame` 不存在。

- [ ] **Step 3：实现 full-write 和分配前长度验证**

`WriteFrame` 先编码 packet ID，检查 `len(id)+len(payload)` 使用溢出安全加法且在 1..2 MiB，再 full-write
长度、ID、payload。`ReadFrame` 从 reader 最多读取 5 字节规范 uvarint，验证长度后只分配精确 frame，
`io.ReadFull` 填充，再从 frame 内解析 packet ID：

```go
if frameLength == 0 || frameLength > MaxFrameBytes {
	return 0, nil, fmt.Errorf("network: frame length %d exceeds bounds", frameLength)
}
frame := make([]byte, int(frameLength))
if _, err := io.ReadFull(r, frame); err != nil { return 0, nil, err }
id, used, err := decodeCanonicalUvarintPrefix(frame)
if err != nil { return 0, nil, fmt.Errorf("network: packet ID: %w", err) }
return id, append([]byte(nil), frame[used:]...), nil
```

- [ ] **Step 4：运行 race 与 frame fuzz smoke**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "^$" -fuzz FuzzReadFrame -fuzztime=5s'
```

Expected: PASS；超界长度在大分配前失败。

- [ ] **Step 5：提交**

```bash
git add internal/network/frame.go internal/network/frame_test.go internal/network/frame_fuzz_test.go
git commit -m "feat: 添加规范有界 TCP 帧"
```

---

### Task 7：实现 TCP packet stream 与 listener

**Files:**
- Create: `internal/network/stream.go`
- Create: `internal/network/tcp.go`
- Create: `internal/network/tcp_test.go`
- Modify: `internal/archcheck/deps_test.go`

**Interfaces:**
- Consumes: Task 2 State/Packet；Task 5 `Codec`；Task 6 framing
- Produces:

```go
type ClientPacketStream interface {
	Send(context.Context, State, ClientPacket) error
	Recv(context.Context, State) (ServerPacket, error)
	Close() error
}

type ServerPacketStream interface {
	Send(context.Context, State, ServerPacket) error
	Recv(context.Context, State) (ClientPacket, error)
	Peer() string
	Close() error
}

type Listener interface {
	Accept(context.Context) (ServerPacketStream, error)
	Addr() string
	Close() error
}

func DialTCP(context.Context, string) (ClientPacketStream, error)
func ListenTCP(string) (Listener, error)
```

- [ ] **Step 1：写真实回环、deadline、取消和并发关闭失败测试**

```go
func TestTCPStreamRoundTripAndPeer(t *testing.T) {
	listener, err := ListenTCP("127.0.0.1:0"); if err != nil { t.Fatal(err) }
	defer listener.Close()
	serverResult := make(chan ServerPacketStream, 1)
	go func() { stream, _ := listener.Accept(context.Background()); serverResult <- stream }()
	client, err := DialTCP(context.Background(), listener.Addr()); if err != nil { t.Fatal(err) }
	defer client.Close()
	server := <-serverResult; defer server.Close()
	if err := client.Send(context.Background(), StateHandshake, ClientHello{ProtocolVersion: 1}); err != nil { t.Fatal(err) }
	packet, err := server.Recv(context.Background(), StateHandshake)
	if err != nil || packet != (ClientHello{ProtocolVersion: 1}) || server.Peer() == "" {
		t.Fatalf("packet=%+v peer=%q err=%v", packet, server.Peer(), err)
	}
}
```

还要覆盖：Dial cancellation、Accept cancellation 不永久关闭 listener、Recv deadline、Send deadline、peer close
唤醒阻塞操作、client/server/peer 三方并发 Close 100 次、TCP_NODELAY 和 keepalive 设置失败上浮、解码非法帧
关闭单连接但 listener 仍可接收下一连接。

- [ ] **Step 2：运行 TCP 测试并确认 RED**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "TestTCP" -count=1'
```

Expected: FAIL，stream/listener API 未实现。

- [ ] **Step 3：实现每方向单 owner 的 TCP stream**

`tcpStream` 持有 `net.Conn`、一个 `Codec`、read mutex、write mutex、`sync.Once`。Send/Recv 先用
`context.AfterFunc` 把对应 deadline 设为 `time.Now()` 以响应取消，再设置/清理显式 context deadline：

```go
stop := context.AfterFunc(ctx, func() { _ = conn.SetReadDeadline(time.Now()) })
defer stop()
if deadline, ok := ctx.Deadline(); ok { _ = conn.SetReadDeadline(deadline) }
defer conn.SetReadDeadline(time.Time{})
id, payload, err := ReadFrame(conn)
```

client/server stream 分别调用正确方向的 `Codec.Encode*`/`Decode*`。TCP dial 后断言底层为 `*net.TCPConn`，
调用 `SetNoDelay(true)`、`SetKeepAlive(true)`、`SetKeepAlivePeriod(30*time.Second)`。

listener 用 `*net.TCPListener.SetDeadline` 的短周期循环响应无 deadline context；超时后先检查 ctx，再继续，
不因为一次 Accept cancel 关闭共享 listener。只有 `network/tcp.go` import `net`，archcheck 新增语法/依赖门禁。

- [ ] **Step 4：运行高重复 race 生命周期测试**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "TestTCP|TestConcurrentTCP" -race -count=20'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
```

Expected: PASS；无 data race、goroutine leak 或 listener 被单坏连接关闭。

- [ ] **Step 5：提交**

```bash
git add internal/network/stream.go internal/network/tcp.go internal/network/tcp_test.go internal/archcheck/deps_test.go
git commit -m "feat: 添加 TCP 协议流与监听器"
```

---

### Task 8：共享 Handshake/Login driver 与内存 packet stream

**Files:**
- Create: `internal/network/login.go`
- Create: `internal/network/login_test.go`
- Modify: `internal/network/memory.go`
- Modify: `internal/network/memory_test.go`
- Modify: `internal/network/transport.go`
- Create: `internal/network/transport_consistency_test.go`

**Interfaces:**
- Consumes: Task 1 PlayerID；Task 2 packet/state；Task 7 stream API
- Produces:

```go
type Identity struct { PlayerID core.PlayerID; DisplayName string }

const (
	HandshakeTimeout = 5 * time.Second
	LoginTimeout     = 10 * time.Second
)

type RemoteError struct { State State; Code uint8; Message string }
func (e *RemoteError) Error() string

type PendingLogin struct {
	stream   ServerPacketStream
	identity Identity
	decided  atomic.Bool
}
func BeginServerLogin(context.Context, ServerPacketStream) (*PendingLogin, error)
func (p *PendingLogin) Identity() Identity
func (p *PendingLogin) Accept(context.Context, func(ServerEndpoint) error) error
func (p *PendingLogin) Reject(context.Context, LoginRejectCode, string) error

func LoginClient(context.Context, ClientPacketStream, Identity) (ClientEndpoint, error)
func NewMemoryStreamPair(capacity int) (ClientPacketStream, ServerPacketStream)
func NewMemoryPair(capacity int) (ClientEndpoint, ServerEndpoint) // 仅已附着测试/benchmark
```

- [ ] **Step 1：写成功登录、拒绝、状态越界和端无关 transcript 失败测试**

```go
func TestMemoryLoginTransitionsToPlay(t *testing.T) {
	clientStream, serverStream := NewMemoryStreamPair(16)
	id := core.PlayerID{0,1,2,3,4,5,0x46,7,0x88,9,10,11,12,13,14,15}
	serverDone := make(chan error, 1)
	go func() {
		pending, err := BeginServerLogin(context.Background(), serverStream)
		if err != nil { serverDone <- err; return }
		if pending.Identity() != (Identity{PlayerID: id, DisplayName: "Chen"}) {
			serverDone <- fmt.Errorf("identity=%+v", pending.Identity()); return
		}
		var endpoint ServerEndpoint
		err = pending.Accept(context.Background(), func(attached ServerEndpoint) error {
			endpoint = attached
			return nil
		})
		if err == nil { err = endpoint.Send(context.Background(), PlayerState{Ready: false}) }
		serverDone <- err
	}()
	client, err := LoginClient(context.Background(), clientStream, Identity{PlayerID:id, DisplayName:"Chen"})
	if err != nil { t.Fatal(err) }
	if message, err := client.Recv(context.Background()); err != nil || message.(PlayerState).Ready {
		t.Fatalf("message=%+v err=%v", message, err)
	}
	if err := <-serverDone; err != nil { t.Fatal(err) }
}
```

测试版本不匹配必须在 Handshake 得到 `RemoteError`；ServerFull/InvalidIdentity 在 Login 得到稳定 code；
PendingLogin Accept/Reject 只能调用一次；LoginSuccess echo ID 不符时客户端关闭；任何提前 Play 包是
`ProtocolViolation`；内存和 TCP 上执行同一成功/拒绝 transcript 得到相同状态结果。

Play client endpoint 的 `Recv` 必须拦截 `KeepAlive`、原样发送 `KeepAliveReply` 并继续读取；收到
`Disconnect` 返回 `RemoteError` 而不把控制包交给 mirror。

- [ ] **Step 2：运行 login 测试并确认 RED**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "TestMemoryLogin|TestLogin|TestProtocolTranscript" -count=1'
```

Expected: FAIL，login driver 和 memory packet stream 尚不存在。

- [ ] **Step 3：把 memoryPair 提升为 packet stream 并保留已附着测试 helper**

共享 channel 改为 `chan ClientPacket` / `chan ServerPacket`，Send 时先执行对应 state validator。成功 Send
转移消息所有权，Close/背压/取消语义保持现有测试：

```go
func NewMemoryStreamPair(capacity int) (ClientPacketStream, ServerPacketStream) {
	if capacity < 1 { panic("network: memory transport capacity must be positive") }
	pair := newMemoryPacketPair(capacity)
	return &memoryClientStream{pair: pair}, &memoryServerStream{pair: pair, peer: "memory"}
}
```

`NewMemoryPair` 只把 stream 包装为已处于 Play 的 endpoint，暂时供现有 unit/benchmark harness 与尚未迁移的
`mcgo` 装配使用。Task 15 把生产 `mcgo` 改到完整登录后，再增加 archcheck 禁止生产代码调用该 helper。

- [ ] **Step 4：实现共享同步登录 driver 和收窄 Play endpoint**

客户端和服务端分别为 Handshake 创建 5 秒子 context、为 Login 创建 10 秒子 context；父 context 更早截止时
服从父截止。客户端严格执行 Hello send → Hello recv → LoginStart send → LoginSuccess/Reject recv。服务端
`BeginServerLogin` 严格执行逆序并只返回尚未决议的 PendingLogin。Accept 创建带 gate 的 Play endpoint，
先调用 attach callback；callback 成功后才发送 `LoginSuccess` 并打开 gate，因此 attach reader/writer 不可能把
Play 包排到 LoginSuccess 前：

```go
func (p *PendingLogin) Accept(ctx context.Context, attach func(ServerEndpoint) error) error {
	if attach == nil { return errors.New("network: nil login attach callback") }
	if !p.decide() { return errors.New("network: pending login already decided") }
	endpoint := newGatedServerPlayEndpoint(p.stream)
	if err := attach(endpoint); err != nil {
		endpoint.abort()
		_ = p.stream.Send(ctx, StateLogin, LoginReject{Code:LoginInternalError, Message:"服务端无法建立会话"})
		_ = p.stream.Close()
		return err
	}
	if err := p.stream.Send(ctx, StateLogin, LoginSuccess{PlayerID:p.identity.PlayerID}); err != nil {
		endpoint.abort(); _ = p.stream.Close(); return err
	}
	endpoint.commit()
	return nil
}
```

任何错误都幂等关闭 stream。成功后 driver 不再允许访问原始 Login state；上层只持有
`ClientEndpoint`/`ServerEndpoint`。

- [ ] **Step 5：运行内存/TCP 一致性与 race 测试**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "TestMemoryLogin|TestTCPLogin|TestProtocolTranscript" -race -count=20'
```

Expected: PASS；两种 stream 的状态结果一致，控制包不泄漏到 Play 消费者。

- [ ] **Step 6：提交**

```bash
git add internal/network/login.go internal/network/login_test.go internal/network/memory.go internal/network/memory_test.go internal/network/transport.go internal/network/transport_consistency_test.go
git commit -m "feat: 统一内存与 TCP 登录状态机"
```

---

### Task 9：定义玩家存档值、v1 codec 与迁移/fuzz 门禁

**Files:**
- Create: `internal/storage/player_types.go`
- Create: `internal/storage/player_codec.go`
- Create: `internal/storage/player_codec_test.go`
- Create: `internal/storage/player_codec_fuzz_test.go`
- Create: `internal/storage/player_migration.go`
- Create: `internal/storage/player_migration_test.go`
- Create: `internal/storage/testdata/player-v1.bin`
- Modify: `internal/storage/types.go`

**Interfaces:**
- Consumes: Task 1 `core.PlayerID`/昵称；M3A classified storage errors、CRC32C 与显式 codec 模式
- Produces:

```go
var ErrPlayerNotFound = errors.New("storage: player not found")

type PlayerLocation struct {
	Dimension core.DimensionID
	Position  [3]float32
}

type StoredPlayer struct {
	PlayerID    core.PlayerID
	Revision    uint64
	DisplayName string
	Current     PlayerLocation
	Yaw, Pitch  float32
	Safe        *PlayerLocation
	NeedsRewrite bool
}

type PlayerSave struct {
	PlayerID    core.PlayerID
	Revision    uint64
	DisplayName string
	Current     PlayerLocation
	Yaw, Pitch  float32
	Safe        *PlayerLocation
}

type PlayerStore interface {
	LoadPlayer(context.Context, core.PlayerID) (StoredPlayer, error)
	SavePlayer(context.Context, PlayerSave) (uint64, error)
}

type WorldStore interface { Store; PlayerStore }

func encodePlayer(PlayerSave) ([]byte, error)
func decodePlayer(core.PlayerID, []byte) (StoredPlayer, error)

// storage player tests 共用：返回固定合法 v4 ID 和完整 v1 save。
func fixturePlayerID() core.PlayerID
func fixturePlayerSave(core.PlayerID, revision uint64) PlayerSave
```

- [ ] **Step 1：写 v1 精确 round-trip、fixture、迁移连续性和腐坏测试**

```go
func TestPlayerCodecRoundTrip(t *testing.T) {
	id := fixturePlayerID()
	safe := &PlayerLocation{Dimension: core.Overworld, Position: [3]float32{1.5, 65, -2.5}}
	want := PlayerSave{
		PlayerID:id, Revision:7, DisplayName:"Chen",
		Current:PlayerLocation{Dimension:core.Overworld, Position:[3]float32{2.5,70,-3.5}},
		Yaw:1.25, Pitch:-0.5, Safe:safe,
	}
	encoded, err := encodePlayer(want); if err != nil { t.Fatal(err) }
	got, err := decodePlayer(id, encoded)
	if err != nil || got.PlayerID != want.PlayerID || got.Revision != want.Revision ||
		got.DisplayName != want.DisplayName || got.Current != want.Current ||
		got.Yaw != want.Yaw || got.Pitch != want.Pitch || *got.Safe != *want.Safe {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}
```

`TestPlayerV1Fixture` 复用现有 package-level `updateStorageFixtures` 显式更新 flag，路径固定为
`testdata/player-v1.bin`。测试修改 magic/envelope/schema/ID/revision/payload length/CRC/每个 float/安全标志/
尾随字节；断言 future schema 为 `ErrFutureVersion`，CRC/结构为 `ErrCorrupt`，decoded payload 1 MiB+1
在分配前拒绝。迁移注册表逐整数检查 oldest..current-1；M3B v1 空迁移链合法。

- [ ] **Step 2：运行 player codec 测试并确认 RED**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -run "TestPlayerCodec|TestPlayerV1|TestPlayerMigration" -count=1'
```

Expected: FAIL，玩家值、codec 和 fixture 不存在。

- [ ] **Step 3：实现 `MCPL` envelope、CRC32C 与 v1 DTO**

固定 envelope：`magic[4] | envelopeVersion u32 | schema u32 | playerID[16] | revision u64 |
payloadLength u32 | crc32c u32 | payload`。CRC 覆盖规范
`schema|playerID|revision|payloadLength|payload`，使用 Castagnoli。

v1 payload 顺序：昵称 string、current dim+3×f32、yaw f32、pitch f32、hasSafe bool，若 true 再写
safe dim+3×f32。验证 ID v4、revision>0、昵称、Overworld、position/yaw/pitch 有限、pitch 在
`[-π/2, π/2]`、无尾随字节。Decode 先 DTO，再按连续 migration 链转换，迁移后深拷贝 Safe。

- [ ] **Step 4：生成 fixture、关闭更新模式复验并运行 fuzz**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -run TestPlayerV1Fixture -update-storage-fixtures -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -run "TestPlayerV1|TestPlayerMigration" -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -run "^$" -fuzz FuzzDecodePlayer -fuzztime=5s'
```

Expected: PASS；fixture 稳定，fuzz 不 panic、不越过 1 MiB 上限、不接受错误 ID/revision。

- [ ] **Step 5：提交**

```bash
git add internal/storage/player_types.go internal/storage/player_codec.go internal/storage/player_codec_test.go internal/storage/player_codec_fuzz_test.go internal/storage/player_migration.go internal/storage/player_migration_test.go internal/storage/testdata/player-v1.bin internal/storage/types.go
git commit -m "feat: 定义版本化玩家存档格式"
```

---

### Task 10：实现 MemoryStore 与 DiskStore 的玩家语义

**Files:**
- Modify: `internal/storage/memory.go`
- Modify: `internal/storage/memory_test.go`
- Modify: `internal/storage/disk.go`
- Modify: `internal/storage/disk_test.go`
- Modify: `internal/storage/world_files.go`
- Modify: `internal/storage/world_files_test.go`
- Modify: `internal/storage/metadata.go`
- Create: `internal/storage/player_store_test.go`

**Interfaces:**
- Consumes: Task 9 `PlayerStore`/codec；M3A world lock 与 atomic replacement
- Produces: `var _ WorldStore = (*MemoryStore)(nil)`、`var _ WorldStore = (*DiskStore)(nil)`；
  `<world>/players/<uuid>.player` 原子实现

- [ ] **Step 1：写共享 store contract、所有权和 revision 失败测试**

```go
func testPlayerStoreContract(t *testing.T, open func(*testing.T) PlayerStore) {
	t.Helper(); store := open(t); id := fixturePlayerID()
	if _, err := store.LoadPlayer(context.Background(), id); !errors.Is(err, ErrPlayerNotFound) {
		t.Fatalf("missing err=%v", err)
	}
	save := fixturePlayerSave(id, 1)
	if revision, err := store.SavePlayer(context.Background(), save); err != nil || revision != 1 {
		t.Fatalf("save revision=%d err=%v", revision, err)
	}
	loaded, err := store.LoadPlayer(context.Background(), id); if err != nil { t.Fatal(err) }
	loaded.Safe.Position[0] = 999
	again, _ := store.LoadPlayer(context.Background(), id)
	if again.Safe.Position[0] == 999 { t.Fatal("loaded player aliases store") }
	if _, err := store.SavePlayer(context.Background(), fixturePlayerSave(id, 0)); err == nil {
		t.Fatal("accepted zero revision")
	}
}
```

共享 contract 对 Memory/Disk 都运行，并断言：equal revision/same bytes 幂等；equal/different 和 lower revision
返回 `ErrRevisionConflict`；higher revision 成功；context canceled 不变更；Save 输入 Safe 后续修改不影响 store；
路径始终是 canonical ID；另一个 ID 不碰撞。

Disk fault injection 覆盖 temp write/Sync/Close/Rename/directory Sync；rename 前失败保留旧完整文件，rename 后
directory Sync 失败返回错误但下次 Load 只得到完整旧或完整新。坏/未来玩家文件不会被 Save 入口静默覆盖。

- [ ] **Step 2：运行 store contract 并确认 RED**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -run "TestMemoryPlayer|TestDiskPlayer|TestPlayerStore" -count=1'
```

Expected: FAIL，现有 Store 没有玩家方法或 players 目录。

- [ ] **Step 3：实现深拷贝 MemoryStore 玩家 map**

在同一 mutex 下维护 `map[core.PlayerID]memoryPlayer`。Save 先 `validatePlayerSave`、编码得到 canonical bytes，
用 bytes/hash 比较 equal revision，验证整个操作后才 clone/apply：

```go
type memoryPlayer struct { revision uint64; encoded []byte }

func (store *MemoryStore) LoadPlayer(ctx context.Context, id core.PlayerID) (StoredPlayer, error) {
	if err := ctx.Err(); err != nil { return StoredPlayer{}, err }
	store.mu.Lock(); defer store.mu.Unlock()
	stored, ok := store.players[id]
	if !ok { return StoredPlayer{}, fmt.Errorf("%w: %s", ErrPlayerNotFound, id) }
	return decodePlayer(id, append([]byte(nil), stored.encoded...))
}
```

- [ ] **Step 4：实现 world-lock 保护下的 DiskStore 玩家文件**

`openWorldFiles` 在持锁后创建/验证 `players` 目录。`DiskStore.LoadPlayer` 在 store mutex 下只读取精确
`playerPath(id)`，not-exist 映射 `ErrPlayerNotFound`，其余交给 codec。

将 metadata 的原子 helper 泛化为：

```go
func replaceFileAtomically(path, pattern string, data []byte, mode fs.FileMode) error
```

metadata 传 `.world.meta.tmp-*`，玩家传 `.<uuid>.player.tmp-*`。`SavePlayer` 先读旧文件验证 revision，
再对新 bytes 做同目录 `0600` 原子替换。任何路径都不接受用户字符串拼接。

- [ ] **Step 5：运行 storage race、重开与 fault injection**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -run "TestDiskPlayerAtomic|TestPlayerStoreContract" -race -count=20'
```

Expected: PASS；关闭重开后 revision/数据一致，注入中断没有半文件或临时文件泄漏。

- [ ] **Step 6：提交**

```bash
git add internal/storage/memory.go internal/storage/memory_test.go internal/storage/disk.go internal/storage/disk_test.go internal/storage/world_files.go internal/storage/world_files_test.go internal/storage/metadata.go internal/storage/player_store_test.go
git commit -m "feat: 原子保存稳定玩家状态"
```

---

### Task 11：在 sim 中恢复玩家、维护安全落点并动态注销

**Files:**
- Modify: `internal/sim/player.go`
- Modify: `internal/sim/spawn.go`
- Modify: `internal/sim/engine.go`
- Modify: `internal/sim/command.go`
- Create: `internal/sim/player_restore_test.go`
- Create: `internal/sim/player_lifecycle_test.go`
- Modify: `internal/sim/player_test.go`
- Modify: `internal/sim/spawn_test.go`

**Interfaces:**
- Consumes: Task 9 storage 值的语义，但不得 import storage；现有 physics/AABB/chunk Ready
- Produces:

```go
type PlayerLocation struct {
	Dimension core.DimensionID
	Position  mgl32.Vec3
}

type PlayerRestore struct {
	Current        *PlayerLocation
	Safe           *PlayerLocation
	Yaw, Pitch     float32
	SpawnDimension core.DimensionID
	SpawnAnchor    core.ChunkPos
}

type PlayerSnapshot struct {
	Current PlayerLocation
	Yaw, Pitch float32
	Safe *PlayerLocation
}

func (engine *Engine) RegisterPlayer(SessionID, PlayerRestore)
func (engine *Engine) UnregisterSession(SessionID) (PlayerSnapshot, bool)
func (engine *Engine) PlayerSnapshot(SessionID) (PlayerSnapshot, bool)

// player_restore_test.go：同步提交测试 chunks，直到候选查询所需 chunks 全部 Ready。
func makeRestoreWorldReady(*testing.T, *Engine, PlayerLocation, PlayerLocation)
func onlyPlayerUpdate(*testing.T, TickResult, SessionID) PlayerUpdate
```

原 `RegisterSession(id, dimension, anchor)` 保留为测试/旧调用的薄包装，只构造无 Current/Safe 的
`PlayerRestore`；生产 Host 只调用 `RegisterPlayer`。

- [ ] **Step 1：写三级恢复、Ready 边界、安全落点和注销失败测试**

```go
func TestPlayerRestoreFallsBackCurrentSafeThenSpawn(t *testing.T) {
	engine := NewEngine(1); id := SessionID(7)
	current := PlayerLocation{Dimension:core.Overworld, Position:mgl32.Vec3{0.5,1,0.5}}
	safe := PlayerLocation{Dimension:core.Overworld, Position:mgl32.Vec3{16.5,2,0.5}}
	engine.RegisterPlayer(id, PlayerRestore{
		Current:&current, Safe:&safe, Yaw:1.25, Pitch:-0.25,
		SpawnDimension:core.Overworld, SpawnAnchor:core.ChunkPos{},
	})
	// current 区块放置碰撞方块，safe 区块提供完整支撑；在两者 Ready 前都不能 Active。
	makeRestoreWorldReady(t, engine, current, safe)
	update := onlyPlayerUpdate(t, engine.Step(), id)
	if !update.Ready || update.State.Position != safe.Position || !update.Reset {
		t.Fatalf("update=%+v", update)
	}
}
```

精确测试矩阵：current 有效优先；current 空中有效可恢复；current 被堵转 safe；safe 无支撑转 spawn；
候选所需相邻 chunk 未 Ready 时停在该候选，不越过；恢复时速度零；yaw/pitch 保留；OnGround 由物理重算；
Active 且完整接地后更新 Safe；空中/未知 chunk/部分支撑不更新 Safe；Active 玩家 Unregister 返回最后
snapshot，Pending 玩家返回 `HasSnapshot=false` 以保留 Host 已加载的原记录；两者都删除 session、丢弃后续旧命令
并触发世界订阅收缩；重复注销返回 false 不 panic。

- [ ] **Step 2：运行 lifecycle 测试并确认 RED**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim -run "TestPlayerRestore|TestSafeLocation|TestUnregister" -count=1'
```

Expected: FAIL，新 restore/snapshot/unregister API 不存在。

- [ ] **Step 3：实现 exact restore candidate 状态机**

`playerState` 增加 current/safe exact candidates、candidate index 和 `safe *PlayerLocation`。pending 推进顺序：

```go
for player.nextRestore < len(player.restoreCandidates) {
	candidate := player.restoreCandidates[player.nextRestore]
	engine.retainRestoreChunks(session, candidate)
	valid, ready := engine.validateRestoreCandidate(candidate)
	if !ready { return }
	if valid { player.activate(candidate.Location, player.yaw, player.pitch); return }
	player.nextRestore++
}
engine.advanceSpawnSearch(id, session)
```

current 只要求完整 AABB 空闲；safe 额外要求脚下完整实心支撑。候选区块集合由玩家 AABB 与脚下查询的
block span 确定，排序后加入 `spawnWanted`，不能只保留中心 chunk。

- [ ] **Step 4：实现安全落点与动态注销**

active physics 后调用 `updateSafeLocation`：仅 `OnGround`、AABB free+Ready、脚下 support complete+Ready 时
复制位置。注销在 tick 单写边界调用：

```go
func (engine *Engine) UnregisterSession(id SessionID) (PlayerSnapshot, bool) {
	session := engine.sessions[id]
	if session == nil || session.player == nil { return PlayerSnapshot{}, false }
	snapshot := session.player.snapshot(session.dimension)
	delete(engine.sessions, id)
	engine.subscriptionsDirty = true
	return snapshot, true
}
```

`takeInbox` 后处理命令时未知 session 不再通过 `session(id)` 隐式创建生产 player session；只有
`CommandTrustedObserverCenter` 可使用预先登记的 observer session。旧 session 命令静默丢弃并有计数测试。

- [ ] **Step 5：运行 sim 全量 race 与确定性 replay**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim ./internal/physics -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim -run "TestPlayerRestore|TestSafeLocation|TestUnregister|TestPlayerReplay" -race -count=20'
```

Expected: PASS；同一 restore/区块/输入序列产生相同玩家与世界 hash。

- [ ] **Step 6：提交**

```bash
git add internal/sim/player.go internal/sim/spawn.go internal/sim/engine.go internal/sim/command.go internal/sim/player_restore_test.go internal/sim/player_lifecycle_test.go internal/sim/player_test.go internal/sim/spawn_test.go
git commit -m "feat: 恢复并安全注销权威玩家"
```

---

### Task 12：把 Server 重构为动态 session registry

**Files:**
- Modify: `internal/server/server.go`
- Modify: `internal/server/config.go`
- Modify: `internal/server/session.go`
- Modify: `internal/server/publication.go`
- Modify: `internal/server/shutdown.go`
- Modify: `internal/server/generator.go`
- Create: `internal/server/session_registry_test.go`
- Create: `internal/server/heartbeat_test.go`
- Create: `internal/server/attached_test.go`
- Create: `internal/server/attached_external_test.go`
- Modify: `internal/server/acquire_test.go`
- Modify: `internal/server/generator_test.go`
- Modify: `internal/server/integration_test.go`
- Modify: `internal/server/persistence_test.go`
- Modify: `internal/server/persistence_integration_test.go`
- Modify: `internal/server/session_test.go`
- Modify: `internal/server/publication_test.go`
- Modify: `internal/server/player_test.go`
- Modify: `internal/server/player_integration_test.go`
- Modify: `internal/server/server_external_test.go`
- Modify: `internal/server/shutdown_test.go`
- Modify: `internal/server/subscription_test.go`
- Modify: `internal/server/tick_test.go`
- Modify: `internal/archcheck/deps_test.go`

**Interfaces:**
- Consumes: Task 8 Play endpoints/KeepAlive；Task 11 dynamic sim lifecycle；现有 server tick/persistence
- Produces:

```go
type SessionSpec struct {
	ID         sim.SessionID
	Generation uint64
	Endpoint   network.ServerEndpoint
	Restore    sim.PlayerRestore
}

type SessionExit struct {
	ID         sim.SessionID
	Generation uint64
	Snapshot   sim.PlayerSnapshot
	HasSnapshot bool
	Err        error
}

var (
	ErrInvalidSession = errors.New("server: invalid session")
	ErrSessionExists  = errors.New("server: session already exists")
)

type incomingCommand struct {
	Session    sim.SessionID
	Generation uint64
	Command    sim.Command
}

func NewWorld(Config, Generator, storage.Store) *Server
func (s *Server) RunTicks(context.Context) error
func (s *Server) AttachSession(SessionSpec) (<-chan SessionExit, error)
func (s *Server) DetachSession(sim.SessionID, generation uint64, cause error) bool
func (s *Server) PlayerStateFor(sim.SessionID) (sim.PlayerUpdate, bool)
func (s *Server) PlayerSnapshotFor(sim.SessionID) (sim.PlayerSnapshot, bool)

// session_registry_test.go 的确定性输入。
func registryTestConfig() Config
func testRestore() sim.PlayerRestore
func testStore() storage.Store
```

- [ ] **Step 1：写动态 ID、generation、按 session 发布和 heartbeat 失败测试**

```go
func TestServerRejectsStaleSessionGeneration(t *testing.T) {
	running := NewWorld(registryTestConfig(), playerTestGenerator{}, testStore())
	client1, server1 := network.NewMemoryPair(32)
	exit1, err := running.AttachSession(SessionSpec{
		ID:7, Generation:1, Endpoint:server1, Restore:testRestore(),
	})
	if err != nil { t.Fatal(err) }
	if !running.DetachSession(7, 1, network.ErrClosed) { t.Fatal("detach failed") }
	<-exit1
	client2, server2 := network.NewMemoryPair(32)
	defer client1.Close(); defer client2.Close()
	if _, err := running.AttachSession(SessionSpec{ID:8, Generation:2, Endpoint:server2, Restore:testRestore()}); err != nil { t.Fatal(err) }
	running.enqueueIncoming(incomingCommand{Session:7, Generation:1, Command:sim.Command{Session:7, Sequence:99, Kind:sim.CommandPlayerInput}})
	running.StepForTest()
	if _, ok := running.PlayerStateFor(7); ok { t.Fatal("stale session recreated") }
}
```

另测试：任意非 1 ID 能完整登录/发布；重复 ID 或 generation 0 拒绝；旧 reader 缓冲命令不进入新 session；
snapshot/delta/forget/rejection/player state 都只去目标 ID；publication 顺序按 SessionID 稳定；慢 session 关闭
不影响 world；5 秒发 token 1、匹配回复清除、错误/重复 token 断开、15 秒 timeout；关服按 ID 排序关闭并只发
一次 exit；trusted observer 使用保留的非玩家 ID 且不恢复 `localSessionID`。

- [ ] **Step 2：运行 registry/heartbeat 测试并确认 RED**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TestServerRejectsStale|TestSessionRegistry|TestHeartbeat" -count=1'
```

Expected: FAIL，Server 仍绑定唯一 endpoint 与 `localSessionID`。

- [ ] **Step 3：拆出无 endpoint 的长期世界构造器并附着 session**

`Server` 改为 `sessions map[sim.SessionID]*session`，incoming 带 generation。`NewWorld` 只启动世界/生成/保存
worker，不注册玩家或创建 session。`RunTicks` 只运行 20 TPS 循环，context 结束时不调用 Shutdown；Host 用它保持
Store 打开直到玩家 flush。原 `Run` 作为测试兼容包装，在 RunTicks 返回后执行既有安全 Shutdown。
Attach/Detach 都在 `stepMu` 下执行，与 Step 串行：

```go
func (server *Server) AttachSession(spec SessionSpec) (<-chan SessionExit, error) {
	server.stepMu.Lock(); defer server.stepMu.Unlock()
	if server.lifecycle != serverRunning || spec.ID == 0 || spec.Generation == 0 || spec.Endpoint == nil {
		return nil, ErrInvalidSession
	}
	if server.sessions[spec.ID] != nil { return nil, ErrSessionExists }
	server.engine.RegisterPlayer(spec.ID, spec.Restore)
	s := newSession(server.ctx, spec, server.config, &server.workers)
	server.sessions[spec.ID] = s
	go server.endpointReader(s)
	return s.exit, nil
}
```

现有 `New`/`NewMemory`/`NewEmbedded` 暂时作为测试、benchmark 和尚未迁移 `mcgo` 的已附着兼容包装，内部调用
`NewWorld` 后附着局部 ID 1；删除 `localSessionID` 常量，兼容 ID 只存在于包装函数局部。Task 12 同时新增
`attached_test.go`（package server）和 `attached_external_test.go`（package server_test），逐步把 server tests 改为
显式 `NewWorld` + `AttachSession`。Task 15 在 `mcgo` 迁移完成后删除这些生产包装并启用最终 archcheck。

- [ ] **Step 4：逐 session 改写输入、publication 与取消逻辑**

`translateClientMessage(id,message)` 总是覆盖权威 Session；`drainIncoming` 只接受 map 中 generation 相等项。
`publish` 按排序 ID 遍历，`classifyDeltas(session, batches)`、`publishSnapshots(session)`、forget/resync/rejection
都显式接收 session。取消 pending chunk 只能在 `engine.WantsChunk(key)==false` 时发生，不能因单 session forget
误取消 union 仍需要的工作。

unknown network message 或 invalid sim rejection 只关闭对应 session。`PlayerStateFor` 取指定 ID；旧
`PlayerState()` 只随兼容包装保留到 Task 15，server tests 改用显式 ID。

- [ ] **Step 5：实现可注入 clock 的 session heartbeat 与一次性 exit**

Config 默认：

```go
HeartbeatInterval: 5 * time.Second,
HeartbeatTimeout:  15 * time.Second,
```

`newSession` 额外接收 package-private `heartbeatClock`（生产为真实 timer，测试为手动 clock），测试不等待真实
5/15 秒。session reader 消费 `KeepAliveReply`，不翻译成 sim 命令；heartbeat loop 每次只允许一个 outstanding token，token
从 1 单调递增。reader/writer/heartbeat 任一失败调用一次 `DetachSession`，后者关闭 endpoint、注销 sim、构造
snapshot、关闭只读 exit channel，generation 防止迟到路径重复退出。

- [ ] **Step 6：改写关服为关闭 registry 全部 session**

`Shutdown` 在冻结世界时按 SessionID 排序 detach，等待所有 runtime worker，再执行既有 chunk flush/sync/close。
trusted observer 不进入玩家 session registry；benchmark 用 `AttachTrustedObserver(endpoint)` 的独立 sink 字段发布
区块消息且不启动玩家/心跳，`SetTrustedObserverCenter` 只驱动该 sink。关服单独关闭它。

- [ ] **Step 7：运行 server 全量 race 与重复生命周期测试**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server ./internal/sim -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TestSessionRegistry|TestHeartbeat|TestServerRejectsStale|TestShutdown" -race -count=20'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
```

Expected: PASS；无固定生产 session、无 stale input、无双 exit 或 goroutine 泄漏。

- [ ] **Step 8：提交**

```bash
git add internal/server internal/archcheck/deps_test.go
git commit -m "refactor: 服务端改用动态会话注册表"
```

---

### Task 13：实现单玩家缓存、异步保存、retry 与重连背压

**Files:**
- Create: `internal/server/player_persistence.go`
- Create: `internal/server/player_persistence_test.go`
- Modify: `internal/server/config.go`
- Modify: `internal/server/shutdown_test.go`

**Interfaces:**
- Consumes: Task 9 `PlayerStore` values；Task 11 `sim.PlayerRestore`/`PlayerSnapshot`；M3A retry 参数
- Produces:

```go
var ErrPlayerPersistenceBackpressure = errors.New("server: player persistence backpressure")

type playerPersistence struct {
	store       storage.PlayerStore
	config      Config
	mu          sync.Mutex
	cache       *cachedPlayer
	jobs        chan playerSaveJob
	completions chan playerSaveCompletion
	ctx         context.Context
	cancel      context.CancelFunc
	done        chan struct{}
}

type cachedPlayer struct {
	id core.PlayerID; name, pendingName string; persisted uint64
	snapshot sim.PlayerSnapshot; hasSnapshot bool
	missing, dirty, active, inFlight bool
	retry *playerSaveJob
}
type playerSaveJob struct { Save storage.PlayerSave; Attempt uint32; NextTick uint64 }
type playerSaveCompletion struct { Job playerSaveJob; Revision uint64; Err error }

func newPlayerPersistence(storage.PlayerStore, Config) *playerPersistence
func (p *playerPersistence) Prepare(context.Context, core.PlayerID, string, storage.Metadata) (sim.PlayerRestore, error)
func (p *playerPersistence) Activate(core.PlayerID, string) error
func (p *playerPersistence) Confirm(core.PlayerID)
func (p *playerPersistence) Abort(core.PlayerID)
func (p *playerPersistence) Observe(core.PlayerID, string, sim.PlayerSnapshot, uint64, bool) error
func (p *playerPersistence) Poll(uint64) error
func (p *playerPersistence) Flush(context.Context) error
func (p *playerPersistence) CloseWorker()

// player_persistence_test.go 的可控 store 与固定值。
type controllablePlayerStore struct {
	mu sync.Mutex
	loaded map[core.PlayerID]storage.StoredPlayer
	saveStarted chan storage.PlayerSave
	saveResults chan error
}
func newControllablePlayerStore() *controllablePlayerStore
func (s *controllablePlayerStore) complete(error)
func playerPersistenceTestConfig() Config
func playerID(byte) core.PlayerID
func testMetadata() storage.Metadata
func testPlayerSnapshot(float32) sim.PlayerSnapshot
```

`Observe(..., tick, force)` 中 `force=true` 用于断线/关服，立即调度；false 只更新最新快照，到
`AutosaveTicks=6000` 时由 Poll 调度。

- [ ] **Step 1：写首次登录、缓存优先、单在途、retry/backpressure/flush 失败测试**

```go
func TestDirtyDisconnectedPlayerBlocksOnlyDifferentIdentity(t *testing.T) {
	store := newControllablePlayerStore()
	p := newPlayerPersistence(store, playerPersistenceTestConfig())
	defer p.CloseWorker()
	idA, idB := playerID(1), playerID(2)
	if _, err := p.Prepare(context.Background(), idA, "A", testMetadata()); err != nil { t.Fatal(err) }
	p.Observe(idA, "A", testPlayerSnapshot(10), 20, true)
	store.complete(errors.New("disk full"))
	if err := p.Poll(21); err == nil { t.Fatal("save failure not surfaced") }
	if _, err := p.Prepare(context.Background(), idB, "B", testMetadata());
		!errors.Is(err, ErrPlayerPersistenceBackpressure) { t.Fatalf("different ID err=%v", err) }
	restored, err := p.Prepare(context.Background(), idA, "A", testMetadata())
	if err != nil || restored.Current.Position[0] != 10 { t.Fatalf("restore=%+v err=%v", restored, err) }
}
```

矩阵还包括：not found 生成 spawn-only restore，但只有 Confirm 后才标 dirty；loaded v1 转 sim 值；Prepare/Activate 后
Abort 不持久化未成功登录的昵称，Confirm 后昵称变化 dirty 但 ID/revision不变；每个 player 最多一个在途；
在途期间连续 Observe 只保留最新；成功只清除不晚于提交快照的 dirty；
失败在 tick 20/40/80…1200 retry；同 revision retry 相同 bytes；Flush 忽略 backoff 立即尝试并在 ctx 截止时
保留可重试状态；成功且离线后允许切换到另一 ID；CloseWorker 无 goroutine 泄漏。

- [ ] **Step 2：运行 player persistence 测试并确认 RED**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TestPlayerPersistence|TestDirtyDisconnected" -count=1'
```

Expected: FAIL，player persistence coordinator 不存在。

- [ ] **Step 3：实现唯一 cache 与 storage↔sim 纯转换**

`cachedPlayer` 保存 ID、已提交 name、pending name、persisted revision、最新 snapshot、dirty、active、inFlight、retry。
Prepare 只加载/保留 pending name，不把登录尝试当成成功；Activate 只把 staged cache 标为 active，使 attach callback
可在 LoginSuccess 前完成且不产生持久化变更；LoginSuccess 发送成功后的 Confirm（无失败路径）才提交 pending name，
并让 missing/name change 变 dirty；Abort 清除 pending/active，保留登录前 cache 状态。Prepare 主体：

```go
if p.cache != nil {
	if p.cache.id == id { p.cache.pendingName = name; return p.cache.restore(metadata), nil }
	if p.cache.dirty || p.cache.inFlight { return sim.PlayerRestore{}, ErrPlayerPersistenceBackpressure }
	p.cache = nil
}
stored, err := p.store.LoadPlayer(ctx, id)
switch {
case errors.Is(err, storage.ErrPlayerNotFound): p.cache = newMissingPlayer(id, name, metadata)
case err != nil: return sim.PlayerRestore{}, err
default: p.cache = cachedFromStored(stored, name, metadata)
}
return p.cache.restore(metadata), nil
```

转换逐字段复制 `[3]float32`↔`mgl32.Vec3` 和 Safe 指针，player persistence 不共享可变指针。

- [ ] **Step 4：实现一个 worker、revision 与确定性 retry**

调度时在 `persisted+1` 分配 revision、复制不可变 `PlayerSave`，写入 capacity 1 jobs。completion 带原 job；
成功更新 persisted，若最新 snapshot 与 job 不同仍 dirty；失败保留同 revision/job，在
`min(RetryBaseTicks<<attempt, RetryMaxTicks)` 后重试。Poll 先 drain completion 再决定调度，所有状态在 mutex
下，调用 Store 时不持锁。

Flush 循环 dispatch 最新/retry、等待 completion、检查 ctx，直到 `!dirty && !inFlight`；保存失败立即返回
根因但保留 job，后续 Flush 可重试。`CloseWorker` 只在成功 Flush 或明确测试 cleanup 后取消 worker。

- [ ] **Step 5：运行高重复 race、失败重试和 cleanup 测试**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TestPlayerPersistence|TestDirtyDisconnected|TestPlayerFlush" -race -count=20'
```

Expected: PASS；最多一个 SavePlayer 调用在途，不丢最新 snapshot，不无限缓存不同 ID。

- [ ] **Step 6：提交**

```bash
git add internal/server/player_persistence.go internal/server/player_persistence_test.go internal/server/config.go internal/server/shutdown_test.go
git commit -m "feat: 异步保存并重试玩家状态"
```

---

### Task 14：实现 Host 登录接纳、在线槽与完整生命周期

**Files:**
- Create: `internal/server/host.go`
- Create: `internal/server/host_test.go`
- Create: `internal/server/host_integration_test.go`
- Modify: `internal/server/shutdown.go`
- Modify: `internal/server/server.go`

**Interfaces:**
- Consumes: Task 8 PendingLogin；Task 10 `WorldStore`；Task 12 dynamic Server；Task 13 player persistence
- Produces:

```go
type Host struct {
	config Config
	world *Server
	players *playerPersistence
	preLogin chan struct{}
	mu sync.Mutex
	active *activeLogin
	preLoginStreams map[uint64]network.ServerPacketStream
	nextPreLogin uint64
	nextSession sim.SessionID
	nextGeneration uint64
	listener network.Listener
	runtimeCancel context.CancelFunc
	runtimeDone chan error
	workers sync.WaitGroup
	shutdownGate chan struct{}
	closing bool
}

type activeLogin struct {
	PlayerID core.PlayerID
	Name string
	Session sim.SessionID
	Generation uint64
}

func NewHost(Config, Generator, storage.WorldStore) *Host
func (h *Host) Run(context.Context, network.Listener) error // listener 可 nil（内置模式）
func (h *Host) AcceptStream(context.Context, network.ServerPacketStream) error
func (h *Host) Shutdown(context.Context) error

// host_test.go 的 memory 登录句柄；测试与 Host 同属 package server，可读 h.world 的复制值查询。
type testLogin struct { Client network.ClientEndpoint; Done <-chan error; Identity network.Identity }
func newTestHost(*testing.T) *Host
func startMemoryLogin(*testing.T, *Host, network.Identity) testLogin
func playerIdentity(byte) network.Identity
func waitReady(*testing.T, *Host, testLogin)
```

- [ ] **Step 1：写并发登录、ServerFull、断线继续运行和关服顺序失败测试**

```go
func TestHostAllowsExactlyOneConcurrentLogin(t *testing.T) {
	host := newTestHost(t)
	ctx, cancel := context.WithCancel(context.Background()); defer cancel()
	go host.Run(ctx, nil)
	first := startMemoryLogin(t, host, playerIdentity(1))
	waitReady(t, host, first)
	secondStream, secondServer := network.NewMemoryStreamPair(8)
	go host.AcceptStream(context.Background(), secondServer)
	_, err := network.LoginClient(context.Background(), secondStream, playerIdentity(2))
	var remote *network.RemoteError
	if !errors.As(err, &remote) || remote.State != network.StateLogin ||
		network.LoginRejectCode(remote.Code) != network.LoginServerFull {
		t.Fatalf("second login err=%v", err)
	}
}
```

另测试：16 个 pre-login 占满后第 17 个立即关闭；握手 5 秒、登录 10 秒超时；两个 LoginStart 竞争只有
一个占槽；PlayerStore load 在占槽后且只执行一次；Attach callback 完成前客户端收不到 LoginSuccess；断线
注销、Observe(force)、释放槽；专用 Host 断线后 tick 继续；保存失败时同 ID 可重连、不同 ID 得
StoreUnavailable；session ID 从 1 单调递增不复用，溢出拒绝；listener 坏连接不停止 Accept；Shutdown 顺序
listener→detach→player Flush→world Shutdown→worker exit；失败 Flush 后第二次 Shutdown 可成功。

- [ ] **Step 2：运行 Host 测试并确认 RED**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TestHost" -count=1'
```

Expected: FAIL，Host API 不存在。

- [ ] **Step 3：实现 pre-login semaphore 与唯一槽原子状态**

Host 持有 capacity 16 semaphore、mutex 保护的 `active *activeLogin`、next SessionID/generation、listener 和
lifecycle。Accept loop 只做 Accept + 非阻塞获取 semaphore + goroutine；超过上限关闭新 stream。

`AcceptStream` 流程固定：BeginServerLogin → reserve slot → player Prepare → allocate ID/generation →
PendingLogin.Accept(Attach callback) → wait SessionExit。任一失败按是否已发 LoginSuccess 选择 LoginReject 或
Play Disconnect，defer 精确释放 semaphore/slot。

Prepare 错误映射固定：`ErrCorrupt`/`ErrFutureVersion`→`LoginPlayerDataCorrupt`，
`ErrPlayerPersistenceBackpressure`/其他 I/O→`LoginStoreUnavailable`；非法 identity 已由 login driver 在任何 Store
读取前拒绝。Attach 的 gated endpoint 保证 callback 完成前没有 Play 包越过 LoginSuccess。

```go
err = pending.Accept(loginCtx, func(endpoint network.ServerEndpoint) error {
	exit, attachErr = h.world.AttachSession(SessionSpec{
		ID: sessionID, Generation:generation, Endpoint:endpoint, Restore:restore,
	})
	if attachErr != nil { return attachErr }
	if activateErr := h.players.Activate(identity.PlayerID, identity.DisplayName); activateErr != nil {
		h.world.DetachSession(sessionID, generation, activateErr)
		return activateErr
	}
	return nil
})
if err != nil {
	if exit != nil { h.world.DetachSession(sessionID, generation, err); <-exit }
	h.players.Abort(identity.PlayerID)
	return err
}
h.players.Confirm(identity.PlayerID)
```

- [ ] **Step 4：实现 exit→玩家保存与 autosave 观察**

AcceptStream 等待 exit；若有 snapshot，调用 `players.Observe(id,name,snapshot,tick,true)`，再释放在线槽。Host
运行一个 50ms 管理 ticker：读取 `world.TickCount()` 和当前 `PlayerSnapshotFor`，调用 Observe(false) 与
Poll(tick)；管理 ticker 不推进 sim，也不要求 tick 精确同步，6000 阈值用权威 TickCount 判定。

listener nil 时 Run 仍用内部 context 启动 `world.RunTicks` 与管理 ticker，供内置 memory stream；listener 非 nil
时并发 accept。Poll 返回保存错误时记录并保留 retry，不停止世界。Run 收到外部 context 后进入 Host Shutdown；
它必须像 M3A 一样从 `context.Background()` 创建 `Config.ShutdownTimeout` 的新 owner context，不能把已取消的
Run context 传给 flush。正常 context cancellation 在完整关服成功后返回 nil。

- [ ] **Step 5：实现 retryable Host Shutdown**

Shutdown 用 gate 串行：标 closing、关闭 listener、拒绝新 login、关闭 `preLoginStreams` 中每条流并等待对应
goroutine、Detach active 并等待 exit、Observe(force)、`players.Flush(ctx)`；只有 player flush 成功后才取消
RunTicks 并调用 `world.Shutdown(ctx)` 让同一个 WorldStore Sync/Close；最后关闭 player worker。player flush 失败时
RunTicks/world/store 保持打开，第二次 Shutdown 重试。

- [ ] **Step 6：运行 Host 高并发 race 与泄漏测试**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TestHost" -race -count=20'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server ./internal/network ./internal/storage -race -count=1'
```

Expected: PASS；竞争登录最多一个成功，所有失败/取消路径在共享 deadline 内退出。

- [ ] **Step 7：提交**

```bash
git add internal/server/host.go internal/server/host_test.go internal/server/host_integration_test.go internal/server/shutdown.go internal/server/server.go
git commit -m "feat: 接纳并管理单玩家联机会话"
```

---

### Task 15：让 mcgo 使用 profile、本地 Host 或远程 TCP

**Files:**
- Create: `internal/client/receiver.go`
- Create: `internal/client/receiver_test.go`
- Modify: `cmd/mcgo/main.go`
- Modify: `cmd/mcgo/main_test.go`
- Modify: `cmd/mcgo/app.go`
- Modify: `cmd/mcgo/app_test.go`
- Modify: `cmd/mcgo/storage_test.go`
- Modify: `internal/archcheck/deps_test.go`
- Modify: `internal/server/generator.go`
- Modify: `internal/server/server.go`

**Interfaces:**
- Consumes: Task 1 profile；Task 8 client login；Task 14 Host；现有 mirror/predictor/render
- Produces:

```go
package client

type Receiver struct {
	endpoint network.ClientEndpoint
	inbox chan network.ServerMessage
	done chan struct{}
	closeOnce sync.Once
	mu sync.Mutex
	err error
}
func NewReceiver(network.ClientEndpoint, int) *Receiver
func (r *Receiver) TryRecv() (network.ServerMessage, bool)
func (r *Receiver) Err() error
func (r *Receiver) Close() error
```

```go
type applicationOptions struct {
	Seed      int64
	Benchmark bool
	WorldPath string
	Connect   string
	Identity  *network.Identity // benchmark 为 nil
}

type mainOptions struct {
	Application   applicationOptions
	PerfOutput    string
	RequestedName *string
}

func openApplicationStore(context.Context, applicationOptions) (storage.WorldStore, error)
func newApplication(applicationOptions) (*application, error)
```

- [ ] **Step 1：写参数互斥、profile bypass、receiver 和连接装配失败测试**

```go
func TestParseMainOptionsRejectsRemoteLocalConflicts(t *testing.T) {
	for _, args := range [][]string{
		{"--connect","127.0.0.1:25565","--world","worlds/demo"},
		{"--connect","127.0.0.1:25565","--benchmark","--perf-output","x.json"},
		{"--benchmark","--perf-output","x.json","--name","Chen"},
	} {
		if _, err := parseMainOptions(args); err == nil { t.Fatalf("accepted %v", args) }
	}
}
```

测试必须区分“显式 `--world`”与默认值；普通单机/远程恰好调用一次 profile loader，benchmark 永不调用；
`--name` 传入 RequestedName 并持久化；remote 不打开本地 Store；local 不 DialTCP；连接/登录在创建窗口前失败；
Receiver 在 endpoint 阻塞 Recv 时不阻塞 TryRecv，容量满视为协议消费者过慢并关闭；Close 唤醒 reader；远程
Disconnect 使 `runInteractive` 返回非零错误；所有 app tests 使用 fake window/device，不能前台开窗。

- [ ] **Step 2：运行 mcgo/client tests 并确认 RED**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/client ./cmd/mcgo -run "TestReceiver|TestParseMainOptions|TestApplicationConnection" -count=1'
```

Expected: FAIL，新 flags、Receiver 和 Host 装配不存在。

- [ ] **Step 3：实现统一阻塞 reader→有界 inbox**

Receiver goroutine 唯一调用 endpoint.Recv；业务消息写 capacity 队列，满队列设置明确错误并关闭 endpoint。
TryRecv 纯非阻塞，render/main 每帧最多取既有 budget：

```go
func (r *Receiver) TryRecv() (network.ServerMessage, bool) {
	select { case message := <-r.inbox: return message, true; default: return nil, false }
}
```

Close 先 endpoint.Close，再等 done；Err 在 mutex 下返回一次根因。Memory 和 TCP app 都必须使用 Receiver，不能保留
“已取消 context 轮询 Recv”的旧路径。

- [ ] **Step 4：解析 flags 并在 benchmark 外加载 profile**

新增 `--connect`、`--name`。用 `FlagSet.Visit` 记录是否显式传 `--world`/`--name`；只在非 benchmark 调用：

```go
path, err := profile.DefaultPath()
identity, err := profile.LoadOrCreate(profile.Options{
	Path:path, RequestedName:explicitName,
})
```

转换为 `network.Identity` 放入 options。benchmark 保持固定 seed、MemoryStore、trusted observer，不读写用户档案。
同一步新增 archcheck：`network.NewMemoryPair` 只能出现在 `_test.go` 和显式 benchmark 装配；普通 `mcgo` 必须走
NewMemoryStreamPair + LoginClient。

- [ ] **Step 5：在创建图形资源前完成 local/remote 连接**

remote：`DialTCP` → `LoginClient`；local：OpenDisk WorldStore → NewHost → Run(nil) goroutine →
NewMemoryStreamPair → Host.AcceptStream goroutine → LoginClient。任一失败按所有权逆序关闭，不创建 window。

application 持有 Receiver、可选 local Host cancel/done、或 benchmark Server。`Close` 顺序为 receiver/endpoint → local
Host cancel并等待安全关服 → GPU/window；remote 没有本地 Store。窗口标题更新为 `minecraft-go — M3B TCP world`。
删除 Task 12 暂留的 `New`/`NewMemory`/`NewEmbedded`/`NewEmbeddedMemory` 与无参数 `PlayerState()` 兼容包装；全部
生产和测试调用已改用 `NewWorld`、显式 Attach 或 Host。archcheck 同时禁止这些旧标识重新进入生产代码。

- [ ] **Step 6：让交互循环上浮断线错误**

`runInteractive` 返回 `error`；每帧 drain Receiver 后检查 `receiver.Err()`，非正常 close 时退出。保持 predictor/mirror
处理不变；协议非法关闭远程 endpoint，但不得尝试取消不存在的本地 server。`runWithDependencies` join app.Close 错误。

- [ ] **Step 7：运行无窗口 app race 回归**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/client ./cmd/mcgo -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo -run "TestApplicationConnection|TestRunInteractive|TestClose" -race -count=20'
```

Expected: PASS；测试进程无 GLFW window，远程失败不碰本地世界，单机 Close 刷净 Host。

- [ ] **Step 8：提交**

```bash
git add internal/client/receiver.go internal/client/receiver_test.go cmd/mcgo/main.go cmd/mcgo/main_test.go cmd/mcgo/app.go cmd/mcgo/app_test.go cmd/mcgo/storage_test.go internal/archcheck/deps_test.go internal/server/generator.go internal/server/server.go
git commit -m "feat: 客户端支持本地身份与 TCP 直连"
```

---

### Task 16：交付无渲染 mcgod 专用服务端

**Files:**
- Create: `cmd/mcgod/main.go`
- Create: `cmd/mcgod/main_test.go`
- Create: `cmd/mcgod/subprocess_test.go`
- Create: `docs/notes/lan-server.md`
- Modify: `internal/archcheck/deps_test.go`
- Modify: `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: Task 7 `ListenTCP`；Task 10 DiskStore；Task 14 Host；现有 world metadata/generator
- Produces:

```go
type options struct { Listen, World string; Seed int64 }
func parseOptions([]string) (options, error)
func run(context.Context, []string, dependencies) error
```

- [ ] **Step 1：写参数、已有 seed 权威、信号关服和无图形依赖失败测试**

```go
func TestDefaultOptions(t *testing.T) {
	got, err := parseOptions(nil)
	if err != nil || got.Listen != ":25565" || got.World != "worlds/default" || got.Seed != 42 {
		t.Fatalf("options=%+v err=%v", got, err)
	}
}
```

测试未知位置参数/非法地址/空 world；依赖注入断言 OpenDisk 先于 Listen，已有 metadata seed 覆盖 `--seed`；Run 在
context cancel 后调用 Host.Shutdown；player/chunk flush 失败返回根因；启动日志含 resolved listen、规范 world、协议 1；
subprocess 收到 SIGTERM 后正常退出并释放 world.lock；另一个进程可重新打开；保存失败 helper 非零退出。

- [ ] **Step 2：运行 mcgod tests/build 并确认 RED**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgod -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && CGO_ENABLED=0 GOOS=linux go build ./cmd/mcgod'
```

Expected: FAIL，`cmd/mcgod` 不存在。

- [ ] **Step 3：实现参数、装配和信号 context**

`main` 使用 `signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)`；run：

```go
store, err := storage.OpenDisk(ctx, options.World, storage.OpenOptions{Create: storage.Metadata{
	FormatVersion:1, Seed:options.Seed, SpawnDimension:core.Overworld,
}})
if err != nil { return fmt.Errorf("打开世界: %w", err) }
listener, err := network.ListenTCP(options.Listen)
if err != nil { _ = store.Close(); return fmt.Errorf("监听 %q: %w", options.Listen, err) }
config := server.DefaultConfig(store.Metadata().Seed)
host := server.NewHost(config, worldgen.New(store.Metadata().Seed), store)
return host.Run(ctx, listener)
```

任何 early return 都关闭已取得资源；Host.Run 负责正常/取消后的安全关服。日志使用 `slog`，不输出玩家原始 payload。
`main` 把“signal context canceled 且 Host 完整关服成功”视为正常 exit 0；真实监听、玩家 flush、世界 Shutdown
错误仍以非零退出，不能被 context cancellation 掩盖。

- [ ] **Step 4：增加架构与 Linux 无 CGO CI 门禁**

archcheck 扫描 `cmd/mcgod` imports，明确禁止 client/render/gfx/glfw/webgpu。CI 新增：

```yaml
- name: 构建无图形专用服务端
  run: CGO_ENABLED=0 GOOS=linux go build ./cmd/mcgod
```

不改变既有 macOS race/benchmark jobs，不为本地执行安装 Go。

- [ ] **Step 5：编写中文局域网运行与安全说明**

`docs/notes/lan-server.md` 给出 `mcgod --listen :25565 --world ...`、
`mcgo --connect 192.168.x.x:25565 --name ...`、仅监听 localhost 的写法、正常 SIGINT/SIGTERM 关服和备份前先
关服的步骤；醒目标明 M3B 无认证/加密，只能用于可信局域网，不建议路由器端口映射或公网暴露。

- [ ] **Step 6：运行 subprocess、race、Linux build**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgod ./internal/archcheck -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgod -run "TestMCGodProcess" -count=10'
zsh -ic 'gvm use go1.26.0 >/dev/null && CGO_ENABLED=0 GOOS=linux go build ./cmd/mcgod'
```

Expected: PASS；无窗口依赖，SIGTERM 后世界锁可重获，失败保存非零退出。

- [ ] **Step 7：提交**

```bash
git add cmd/mcgod docs/notes/lan-server.md internal/archcheck/deps_test.go .github/workflows/ci.yml
git commit -m "feat: 交付无渲染 TCP 专用服务端"
```

---

### Task 17：证明 TCP 重启一致性并建立 M3B 最终门禁

**Files:**
- Create: `internal/server/tcp_integration_test.go`
- Create: `internal/network/benchmark_test.go`
- Create: `internal/storage/player_bench_test.go`
- Modify: `cmd/mcgo/benchmark.go`
- Modify: `cmd/mcgo/main.go`
- Modify: `cmd/mcgo/main_test.go`
- Rename: `cmd/mcgo/benchmark_v4_test.go` → `cmd/mcgo/benchmark_v5_test.go`
- Modify: `cmd/perfcheck/main.go`
- Modify: `cmd/perfcheck/main_test.go`
- Modify: `docs/notes/perf-baseline.json`
- Modify: `docs/notes/perf-baseline.md`
- Modify: `.github/workflows/ci.yml`
- Modify: `internal/archcheck/deps_test.go`

**Interfaces:**
- Consumes: Tasks 1–16 完整 M3B 纵向切片；M3A scenario v4/perfcheck
- Produces: scenario v5，真实 TCP+Disk restart proof，Memory/TCP 业务 transcript/hash parity，M3B protocol/player-store 基线

```go
// tcp_integration_test.go；全部等待都基于 channel/state，不使用固定 sleep。
type integrationHost struct { Host *Host; Addr string; Done <-chan error }
type integrationClient struct { Endpoint network.ClientEndpoint; Mirror *client.Mirror }
type flatGenerator struct{}
type changedGenerator struct{}
func (flatGenerator) GenerateChunk(core.ChunkPos) *world.Chunk
func (changedGenerator) GenerateChunk(core.ChunkPos) *world.Chunk
func integrationPlayerID() core.PlayerID
func startDiskHost(*testing.T, string, string, Generator) integrationHost
func dialIntegrationClient(*testing.T, string, network.Identity) integrationClient
func (c integrationClient) Close() error
func waitClientReady(*testing.T, integrationHost, integrationClient)
func movePlayerAndPlaceBlock(*testing.T, integrationHost, integrationClient, core.BlockPos)
func (h integrationHost) PlayerSnapshot(*testing.T, core.PlayerID) sim.PlayerSnapshot
func (h integrationHost) ChunkHash(*testing.T, core.ChunkPos) ([32]byte, uint64)
func (h integrationHost) WaitPlayerSaved(*testing.T)
func (h integrationHost) Shutdown(*testing.T)
func assertPlayerRestored(*testing.T, integrationHost, core.PlayerID, sim.PlayerSnapshot)
func assertChunkHash(*testing.T, integrationHost, core.ChunkPos, [32]byte, uint64)
func assertMirrorMatchesAuthority(*testing.T, integrationHost, integrationClient)
```

- [ ] **Step 1：写真实 TCP + 磁盘重启纵向失败测试**

```go
func TestTCPPlayerAndWorldSurviveDisconnectAndRestart(t *testing.T) {
	root := t.TempDir(); id := integrationPlayerID()
	first := startDiskHost(t, root, "127.0.0.1:0", flatGenerator{})
	client := dialIntegrationClient(t, first.Addr, network.Identity{PlayerID:id, DisplayName:"Chen"})
	waitClientReady(t, first, client)
	movePlayerAndPlaceBlock(t, first, client, core.BlockPos{X:0,Y:1,Z:-5})
	wantPlayer := first.PlayerSnapshot(t, id)
	wantHash, wantRevision := first.ChunkHash(t, core.ChunkPos{})
	client.Close(); first.WaitPlayerSaved(t); first.Shutdown(t)

	second := startDiskHost(t, root, "127.0.0.1:0", changedGenerator{})
	reconnected := dialIntegrationClient(t, second.Addr, network.Identity{PlayerID:id, DisplayName:"Chen2"})
	waitClientReady(t, second, reconnected)
	assertPlayerRestored(t, second, id, wantPlayer)
	assertChunkHash(t, second, core.ChunkPos{}, wantHash, wantRevision)
	assertMirrorMatchesAuthority(t, second, reconnected)
	reconnected.Close(); second.Shutdown(t)
}
```

同文件还要证明：第二并发客户端 `ServerFull`；错误协议版本；玩家文件 CRC 损坏只拒绝该玩家且 Host 继续；
当前位置被新方块堵塞回退 safe；safe 也失效回退确定性 spawn；保存注入失败后同 ID 从内存恢复最新、不同 ID
StoreUnavailable；重试成功后不同 ID 可登录；坏/慢 TCP client 不停止 listener；所有 runtime/session/save goroutine
在共享 deadline 内退出。

- [ ] **Step 2：写 Memory/TCP 同脚本 parity 失败测试**

脚本固定 identity、tick 同步点、输入 sequence、移动、place/break/resync、断开 tick。过滤 Handshake/Login/
KeepAlive/Disconnect、地址、压缩长度和临时 SessionID 后，比较：有序业务消息、服务端 PlayerHash、区块
hash/revision、Mirror hash。测试必须先在旧实现失败，因为真实 TCP/Host transcript 尚未被最终 harness 覆盖。

- [ ] **Step 3：运行纵向测试并确认 RED**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TestTCPPlayerAndWorld|TestMemoryTCPParity" -count=1 -v'
```

Expected: FAIL 于缺失 integration helper、未同步的 tick/save 观察点或 parity 差异；不得通过 sleep 猜测 Ready/保存。

- [ ] **Step 4：补齐只读测试同步探针并让纵向测试 GREEN**

补齐只读、复制值的测试探针：Host 当前 session ID/player save status、最近应用 tick；生产测试通过消息/状态 channel
等待。禁止导出可变 engine/store 指针，禁止 wall-clock 长 sleep。

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TestTCPPlayerAndWorld|TestMemoryTCPParity" -race -count=1 -v'
```

Expected: PASS；所有等待由确定性事件完成，重启前已确认玩家和区块落盘。

- [ ] **Step 5：建立 protocol/player-store benchmark 与 scenario v5**

新增 benchmark：所有小包 encode/decode 的 ns/B/allocs；最坏合法 snapshot zstd encode/decode 和压缩比；TCP loopback
连续 PlayerInput/ChunkSnapshot 吞吐；玩家 codec/MemoryStore/DiskStore save/load。scenario v5 在现有 offscreen benchmark
中新增可选真实 TCP 路径，但仍无窗口，并报告：

```json
{
  "scenario_version": 5,
  "transport": "memory|tcp",
  "protocol": {"encode_p99_ms": 0, "decode_p99_ms": 0, "bytes": 0},
  "player_persistence": {"snapshots": 0, "p50_ms": 0, "p95_ms": 0, "p99_ms": 0, "max_ms": 0}
}
```

perfcheck 保留 M3A 绝对阈值和 20% 比较，并对新增字段执行相同同机回退规则；失败报告绝不覆盖 accepted baseline。

- [ ] **Step 6：运行 focused integration、race 和全部 benchmark**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server ./internal/network ./internal/storage -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TestTCPPlayerAndWorld|TestMemoryTCPParity" -race -count=10'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -run "^$" -bench=. -benchtime=1x -count=1'
```

Expected: PASS；真实 socket/disk 重启一致，全部 benchmark 可执行且不启动窗口。

- [ ] **Step 7：在资源稳定时生成并双跑正式无窗口性能报告**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --perf-output /tmp/mcgo-m3b-memory.json'
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck --baseline docs/notes/perf-baseline.json --current /tmp/mcgo-m3b-memory.json'
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport tcp --perf-output /tmp/mcgo-m3b-tcp.json'
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck --baseline /tmp/mcgo-m3b-memory.json --current /tmp/mcgo-m3b-tcp.json'
```

Expected: 两次 perfcheck PASS；frame/tick/persistence 绝对门禁通过，TCP 相对 Memory 与 accepted baseline 的适用
字段不回退超过 20%。若主机压力使报告不可判定，保留原 baseline/阈值并记录失败证据，不能自动豁免或覆盖。

- [ ] **Step 8：更新中文基线说明并执行最终全量门禁**

`docs/notes/perf-baseline.md` 记录硬件、commit、命令、Memory/TCP 报告 hash、协议/player benchmark、阈值和任何明确
限制。然后执行：

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go vet ./...'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck ./internal/storage ./internal/network ./internal/physics -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -run "^$" -bench=. -benchtime=1x -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && CGO_ENABLED=0 GOOS=linux go build ./cmd/mcgod'
zsh -ic 'gvm use go1.26.0 >/dev/null && test -z "$(gofmt -l .)"'
git diff --check
```

Expected: 全部 exit 0；race 无报告、gofmt 无输出、Linux build 成功、工作树仅含 Task 17 预期文件。

- [ ] **Step 9：独立最终评审并提交**

评审逐条核对 spec §20 的 19 项出口条件、每个历史 RED/GREEN 证据、无前台进程和 baseline 未被失败运行覆盖。
修复发现后重新执行 Step 6–8，再提交：

```bash
git add internal/server/tcp_integration_test.go internal/network/benchmark_test.go internal/storage/player_bench_test.go cmd/mcgo/benchmark.go cmd/mcgo/main.go cmd/mcgo/main_test.go cmd/mcgo/benchmark_v4_test.go cmd/mcgo/benchmark_v5_test.go cmd/perfcheck/main.go cmd/perfcheck/main_test.go docs/notes/perf-baseline.json docs/notes/perf-baseline.md .github/workflows/ci.yml internal/archcheck/deps_test.go
git commit -m "chore: M3B TCP 重启与性能门禁"
```

---

## M3B 完成检查表

- [ ] Tasks 1–17 每项都有独立 RED、GREEN、focused race、评审和提交。
- [ ] 生产代码不再依赖固定 `localSessionID` 或唯一 constructor endpoint。
- [ ] 所有 packet ID、payload golden、player/snapshot fixture 与协议版本 1 对齐。
- [ ] Memory 与 TCP 共享登录状态机、消息验证和业务 transcript 语义。
- [ ] 恶意 frame、payload、zstd、profile、player file fuzz 不 panic、不无界分配。
- [ ] `mcgo` 默认单机、`--connect` 远程和 benchmark 三条装配路径互不混淆。
- [ ] `mcgod` 无图形依赖、Linux/无 CGO 可构建、信号关服可重试刷盘。
- [ ] 同一时间最多一个 Play 玩家，ServerFull/StoreUnavailable 边界稳定。
- [ ] 玩家 ID/昵称/当前位置/安全落点/revision 跨断线与重启保持正确。
- [ ] 真实 TCP + Disk 纵向测试比较玩家、区块、revision 和镜像 hash。
- [ ] session/listener/receiver/worker/store 无泄漏、无 data race。
- [ ] 既有性能阈值未放宽，M3B Memory/TCP 新基线只来自通过的无窗口运行。
- [ ] 全量 test/race/vet/gofmt/arch/benchmark/Linux build/diff-check 通过。
