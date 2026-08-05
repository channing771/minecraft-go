package network

import (
	"encoding/hex"
	"testing"

	"minecraft-go/internal/core"
)

func TestProtocolVersionIsNine(t *testing.T) {
	if ProtocolVersion != 9 {
		t.Fatalf("协议版本 = %d，想要 9", ProtocolVersion)
	}
}

func TestProtocolV9RejectsVersionEightBeforePlay(t *testing.T) {
	// v8 是上一版本，必须和更早版本一样在 Handshake 阶段稳定拒绝。
	for _, version := range []uint32{1, 2, 3, 4, 5, 6, 7, 8} {
		stream := &staticClientHelloStream{version: version}
		if _, err := BeginServerLogin(t.Context(), stream); err == nil {
			t.Fatalf("v%d ClientHello 被接受", version)
		}
		reject, ok := stream.sent.(HandshakeReject)
		if !ok || reject.ServerProtocolVersion != ProtocolVersion ||
			reject.Code != HandshakeVersionMismatch {
			t.Fatalf("v%d 拒绝结果 = %#v，想要 v9 HandshakeReject", version, stream.sent)
		}
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
