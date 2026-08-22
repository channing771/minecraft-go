package network

import (
	"encoding/hex"
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

func TestProtocolVersionIsTwentyFour(t *testing.T) {
	if ProtocolVersion != 24 {
		t.Fatalf("协议版本=%d，想要 24", ProtocolVersion)
	}
}

func TestProtocolV24RejectsPriorVersionsBeforePlay(t *testing.T) {
	// v23 是上一版本（rust-engine-lod-shell 交付的登录种子段），必须和 v22
	// 及更早版本一样在 Handshake 阶段稳定拒绝，并给出版本不匹配原因。
	// 循环上界是 `ProtocolVersion` 而不是某个字面量：升版时刚退役的那一版
	// 必须自动进入覆盖，否则这条用例只测得到远古版本。
	for version := uint32(1); version < ProtocolVersion; version++ {
		stream := &staticClientHelloStream{version: version}
		if _, err := BeginServerLogin(t.Context(), stream, 0); err == nil {
			t.Fatalf("v%d ClientHello 被接受", version)
		}
		reject, ok := stream.sent.(HandshakeReject)
		if !ok || reject.ServerProtocolVersion != ProtocolVersion ||
			reject.Code != HandshakeVersionMismatch {
			t.Fatalf("v%d 拒绝结果 = %#v，想要 v%d HandshakeReject", version, stream.sent, ProtocolVersion)
		}
	}
}

func TestHandshakeAcceptsCurrentVersion(t *testing.T) {
	// 当前版本的 ClientHello 必须通过握手：服务端读取后以同版本 ServerHello
	// 回应，而不是版本不匹配拒绝。（版本无关命名：测试始终跟随
	// `ProtocolVersion` 常量，协议升版时无需改名。）
	client, server := NewMemoryStreamPair(4)
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	if err := client.Send(t.Context(), StateHandshake, ClientHello{ProtocolVersion: ProtocolVersion}); err != nil {
		t.Fatal(err)
	}
	hello, err := server.Recv(t.Context(), StateHandshake)
	if err != nil || hello != (ClientHello{ProtocolVersion: ProtocolVersion}) {
		t.Fatalf("当前版本 ClientHello = (%#v,%v)", hello, err)
	}
	if err := server.Send(t.Context(), StateHandshake, ServerHello{ProtocolVersion: ProtocolVersion}); err != nil {
		t.Fatal(err)
	}
	greeting, err := client.Recv(t.Context(), StateHandshake)
	if err != nil || greeting != (ServerHello{ProtocolVersion: ProtocolVersion}) {
		t.Fatalf("当前版本 ServerHello = (%#v,%v)", greeting, err)
	}
}

func TestProtocolV9PlayerStateCarriesWorldTime(t *testing.T) {
	state := PlayerState{
		Dimension:      core.Overworld,
		ServerTick:     1,
		WorldTimeTicks: 0x0102030405060708,
	}
	id, payload, err := encodeServerControlPayload(StatePlay, state)
	if err != nil {
		t.Fatal(err)
	}
	if id != 3 {
		t.Fatalf("PlayerState packet ID = %d，想要 3", id)
	}

	// 绝对世界时间恰好追加在既有固定 payload 末尾。
	got := hex.EncodeToString(payload)
	const wantSuffix = "0807060504030201"
	if len(got) < len(wantSuffix) || got[len(got)-len(wantSuffix):] != wantSuffix {
		t.Fatalf("payload = %s，想要以 %s 结尾", got, wantSuffix)
	}

	round, err := decodeServerControlPayload(StatePlay, id, payload)
	if err != nil {
		t.Fatal(err)
	}
	if round != ServerPacket(state) {
		t.Fatalf("往返 = %#v，想要 %#v", round, state)
	}

	// payload 是固定长度：任何截断都必须被拒绝。
	for length := 0; length < len(payload); length++ {
		if _, err := decodeServerControlPayload(StatePlay, id, payload[:length]); err == nil {
			t.Fatalf("截断到 %d 字节被接受", length)
		}
	}
	if _, err := decodeServerControlPayload(StatePlay, id, append(payload, 0)); err == nil {
		t.Fatal("多出尾随字节被接受")
	}
}

func TestProtocolV9PlayerStateWorldTimeAcceptsFullRange(t *testing.T) {
	for _, ticks := range []uint64{0, 23999, 24000, 1 << 40, ^uint64(0)} {
		state := PlayerState{Dimension: core.Overworld, WorldTimeTicks: ticks}
		if err := state.Validate(); err != nil {
			t.Fatalf("时间 %d 被 Validate 拒绝：%v", ticks, err)
		}
		id, payload, err := encodeServerControlPayload(StatePlay, state)
		if err != nil {
			t.Fatal(err)
		}
		round, err := decodeServerControlPayload(StatePlay, id, payload)
		if err != nil {
			t.Fatal(err)
		}
		if round.(PlayerState).WorldTimeTicks != ticks {
			t.Fatalf("往返时间 = %d，想要 %d", round.(PlayerState).WorldTimeTicks, ticks)
		}
	}
}

func TestProtocolV14PlayerStateCarriesHealth(t *testing.T) {
	// 生命值恰好追加在既有采掘字段之后、世界时间之前的固定偏移。
	for _, health := range []uint8{0, 1, core.MaxHealth} {
		state := PlayerState{Dimension: core.Overworld, Health: health, WorldTimeTicks: 24000}
		if err := state.Validate(); err != nil {
			t.Fatalf("生命值 %d 被 Validate 拒绝：%v", health, err)
		}
		id, payload, err := encodeServerControlPayload(StatePlay, state)
		if err != nil {
			t.Fatal(err)
		}
		round, err := decodeServerControlPayload(StatePlay, id, payload)
		if err != nil {
			t.Fatal(err)
		}
		if round.(PlayerState).Health != health {
			t.Fatalf("往返生命值 = %d，想要 %d", round.(PlayerState).Health, health)
		}
	}
}

func TestProtocolV14PlayerStateRejectsOutOfRangeHealth(t *testing.T) {
	invalid := PlayerState{Dimension: core.Overworld, Health: core.MaxHealth + 1}
	if err := invalid.Validate(); err == nil {
		t.Fatal("越界生命值通过了 Validate")
	}
	if _, _, err := encodeServerControlPayload(StatePlay, invalid); err == nil {
		t.Fatal("越界生命值被编码接受")
	}

	// 构造一份合法 wire 载荷，再把生命值字节改写为越界值，验证解码器单独拒绝它，
	// 且不得输出部分 PlayerState。
	valid := PlayerState{Dimension: core.Overworld, Health: core.MaxHealth, WorldTimeTicks: 24000}
	id, payload, err := encodeServerControlPayload(StatePlay, valid)
	if err != nil {
		t.Fatal(err)
	}
	healthOffset := playerStateHealthOffset(len(payload))
	corrupted := append([]byte(nil), payload...)
	corrupted[healthOffset] = core.MaxHealth + 1
	if packet, err := decodeServerControlPayload(StatePlay, id, corrupted); err == nil {
		t.Fatalf("越界生命值 wire 载荷被解码接受: %#v", packet)
	}
	// 守卫排在真实断言之后：改写的必须确实是生命值字节，否则上面那条断言可能
	// 是被别的字段越界顶掉的（v21 就真实发生过一次）。
	//
	// 判据是"未改写的原始载荷在该偏移处正好是这份夹具编码进去的生命值"。这条
	// 断言的第一版写成了对比 corrupted 与 payload 的**相邻**字节、再解一次合法
	// 载荷的生命值：前者恒为 false（改写的是 healthOffset 那一字节，相邻字节
	// 当然没动），后者与偏移无关（只是重复了夹具编码了满血这件事）。两个子句
	// 都不看 healthOffset 指向哪里，因而恒真——把偏移改回错值照样 PASS，实测过。
	if payload[healthOffset] != core.MaxHealth {
		t.Fatalf("夹具无效：healthOffset 处是 %d，不是生命值 %d",
			payload[healthOffset], core.MaxHealth)
	}
}

// TestProtocolV21PlayerStateCarriesOxygen 覆盖 v21 追加的权威氧气：
// 它按 u16 小端紧跟在 Health 之后（v24 起其后还有 Hunger，再往后才是
// WorldTimeTicks），且往返保值。
func TestProtocolV21PlayerStateCarriesOxygen(t *testing.T) {
	for _, oxygen := range []uint16{0, 1, 0x0101, core.MaxOxygenTicks} {
		state := PlayerState{Dimension: core.Overworld, Oxygen: oxygen, WorldTimeTicks: 24000}
		if err := state.Validate(); err != nil {
			t.Fatalf("氧气 %d 被 Validate 拒绝：%v", oxygen, err)
		}
		id, payload, err := encodeServerControlPayload(StatePlay, state)
		if err != nil {
			t.Fatal(err)
		}
		round, err := decodeServerControlPayload(StatePlay, id, payload)
		if err != nil {
			t.Fatal(err)
		}
		if round.(PlayerState).Oxygen != oxygen {
			t.Fatalf("往返氧气 = %d，想要 %d", round.(PlayerState).Oxygen, oxygen)
		}
	}
}

// TestProtocolV21PlayerStateRejectsOutOfRangeOxygen 与生命值那条同形：
// 越界氧气必须在 Validate、编码与解码三处各自被拒。
func TestProtocolV21PlayerStateRejectsOutOfRangeOxygen(t *testing.T) {
	invalid := PlayerState{Dimension: core.Overworld, Oxygen: core.MaxOxygenTicks + 1}
	if err := invalid.Validate(); err == nil {
		t.Fatal("越界氧气通过了 Validate")
	}
	if _, _, err := encodeServerControlPayload(StatePlay, invalid); err == nil {
		t.Fatal("越界氧气被编码接受")
	}

	valid := PlayerState{Dimension: core.Overworld, Oxygen: core.MaxOxygenTicks, WorldTimeTicks: 24000}
	id, payload, err := encodeServerControlPayload(StatePlay, valid)
	if err != nil {
		t.Fatal(err)
	}
	oxygenOffset := playerStateOxygenOffset(len(payload))
	corrupted := append([]byte(nil), payload...)
	// 只动低字节即可越界：满值 300 = 0x012C，把低字节抬到 0xFF 得 0x01FF = 511。
	corrupted[oxygenOffset] = 0xFF
	if packet, err := decodeServerControlPayload(StatePlay, id, corrupted); err == nil {
		t.Fatalf("越界氧气 wire 载荷被解码接受: %#v", packet)
	}
	// 守卫排在真实断言之后：偏移必须真的指向氧气低字节。
	if payload[oxygenOffset] != byte(core.MaxOxygenTicks&0xFF) ||
		payload[oxygenOffset+1] != byte(core.MaxOxygenTicks>>8) {
		t.Fatal("夹具无效：oxygenOffset 没有指向氧气的两个字节")
	}
}

func TestProtocolV10DropSelectedItemRegistryIsFrozen(t *testing.T) {
	// 新 packet 只占用此前未分配的 ID 11；ID 1 继续保持未分配且不复用。
	if id, ok := clientPacketID(StatePlay, DropSelectedItem{}); !ok || id != 11 {
		t.Fatalf("DropSelectedItem packet ID = (%d,%v)，想要 (11,true)", id, ok)
	}
	packet, ok := clientPacketForID(StatePlay, 11)
	if !ok {
		t.Fatal("Play client packet ID 11 未注册")
	}
	if _, isDrop := packet.(DropSelectedItem); !isDrop {
		t.Fatalf("ID 11 解析为 %T，想要 DropSelectedItem", packet)
	}
	if _, ok := clientPacketForID(StatePlay, 1); ok {
		t.Fatal("Play client packet ID 1 必须保持未分配")
	}

	// 该消息在 Handshake 与 Login 阶段无效，只属于 Play。
	for _, state := range []State{StateHandshake, StateLogin} {
		if _, _, err := encodeClientPacketPayload(state, DropSelectedItem{}); err == nil {
			t.Fatalf("状态 %d 接受了 DropSelectedItem", state)
		}
	}
}

// PlayerState wire 载荷尾部各字段的字节宽度（v24 起）：
//
//	… | Health u8 | Oxygen u16 | Hunger u8 | WorldTimeTicks u64
//
// 下面三个 helper 由末尾向前**链式**求偏移，而不是各写一串 `len(payload)-8-2-1`
// 这样的裸算式。理由是血的教训：v21 追加 `Oxygen` 时，`playerStateHealthOffset`
// 的前身（一句写死的 len(payload)-8-2-1）静默改指到了氧气高字节，用例照样绿（把高字节写成 21 让氧气变成 5376，越界拒绝碰巧
// 仍然发生）；v24 追加 `Hunger` 时同一个坑会再来一次。链式表达让「尾部又多了
// 一个字段」只需要改最外层一处，其余偏移自动跟上。
const (
	playerStateWorldTimeBytes = 8
	playerStateHungerBytes    = 1
	playerStateOxygenBytes    = 2
	playerStateHealthBytes    = 1
)

// playerStateHungerOffset 返回饥饿值字节在 `PlayerState` 载荷中的下标。
func playerStateHungerOffset(payloadLen int) int {
	return payloadLen - playerStateWorldTimeBytes - playerStateHungerBytes
}

// playerStateOxygenOffset 返回氧气低字节在 `PlayerState` 载荷中的下标。
func playerStateOxygenOffset(payloadLen int) int {
	return playerStateHungerOffset(payloadLen) - playerStateOxygenBytes
}

// playerStateHealthOffset 返回生命值字节在 `PlayerState` 载荷中的下标。
func playerStateHealthOffset(payloadLen int) int {
	return playerStateOxygenOffset(payloadLen) - playerStateHealthBytes
}
