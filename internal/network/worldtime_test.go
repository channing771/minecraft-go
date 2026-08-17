package network

import (
	"encoding/hex"
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

func TestProtocolVersionIsNineteen(t *testing.T) {
	if ProtocolVersion != 19 {
		t.Fatalf("协议版本=%d，想要 19", ProtocolVersion)
	}
}

func TestProtocolV19RejectsPriorVersionsBeforePlay(t *testing.T) {
	// v18 是上一版本，必须和 v17 及更早版本一样在 Handshake 阶段稳定拒绝。
	for version := uint32(1); version < ProtocolVersion; version++ {
		stream := &staticClientHelloStream{version: version}
		if _, err := BeginServerLogin(t.Context(), stream); err == nil {
			t.Fatalf("v%d ClientHello 被接受", version)
		}
		reject, ok := stream.sent.(HandshakeReject)
		if !ok || reject.ServerProtocolVersion != ProtocolVersion ||
			reject.Code != HandshakeVersionMismatch {
			t.Fatalf("v%d 拒绝结果 = %#v，想要 v%d HandshakeReject", version, stream.sent, ProtocolVersion)
		}
	}
}

func TestProtocolV19HandshakeAcceptsCurrentVersion(t *testing.T) {
	// 当前版本 v19 的 ClientHello 必须通过握手：服务端读取后以同版本 ServerHello
	// 回应，而不是版本不匹配拒绝。
	client, server := NewMemoryStreamPair(4)
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	if err := client.Send(t.Context(), StateHandshake, ClientHello{ProtocolVersion: ProtocolVersion}); err != nil {
		t.Fatal(err)
	}
	hello, err := server.Recv(t.Context(), StateHandshake)
	if err != nil || hello != (ClientHello{ProtocolVersion: ProtocolVersion}) {
		t.Fatalf("v19 ClientHello = (%#v,%v)", hello, err)
	}
	if err := server.Send(t.Context(), StateHandshake, ServerHello{ProtocolVersion: ProtocolVersion}); err != nil {
		t.Fatal(err)
	}
	greeting, err := client.Recv(t.Context(), StateHandshake)
	if err != nil || greeting != (ServerHello{ProtocolVersion: ProtocolVersion}) {
		t.Fatalf("v19 ServerHello = (%#v,%v)", greeting, err)
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
	healthOffset := len(payload) - 8 - 1
	corrupted := append([]byte(nil), payload...)
	corrupted[healthOffset] = core.MaxHealth + 1
	if packet, err := decodeServerControlPayload(StatePlay, id, corrupted); err == nil {
		t.Fatalf("越界生命值 wire 载荷被解码接受: %#v", packet)
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
