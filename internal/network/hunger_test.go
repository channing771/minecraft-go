package network

import (
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

// TestProtocolV24PlayerStateCarriesHunger 覆盖 v24 追加的权威饥饿值：
// 它按 u8 落在 `Oxygen` 之后、`WorldTimeTicks` 之前，且全值域往返保值。
//
// 样本刻意含 12 这个既非 0 也非 `core.MaxHunger` 的中间值：0 与「编码器漏写
// 该字段、解码器读出零值」不可分辨，满值又与「未初始化即吃饱」的实现巧合重合。
func TestProtocolV24PlayerStateCarriesHunger(t *testing.T) {
	for _, hunger := range []uint8{0, 1, 12, core.MaxHunger} {
		state := PlayerState{Dimension: core.Overworld, Hunger: hunger, WorldTimeTicks: 24000}
		if err := state.Validate(); err != nil {
			t.Fatalf("饥饿值 %d 被 Validate 拒绝：%v", hunger, err)
		}
		id, payload, err := encodeServerControlPayload(StatePlay, state)
		if err != nil {
			t.Fatal(err)
		}
		round, err := decodeServerControlPayload(StatePlay, id, payload)
		if err != nil {
			t.Fatal(err)
		}
		if got := round.(PlayerState).Hunger; got != hunger {
			t.Fatalf("往返饥饿值 = %d，想要 %d", got, hunger)
		}
	}
}

// TestProtocolV24PlayerStateRejectsOutOfRangeHunger 与生命值、氧气那两条同形：
// 越界饥饿值必须在 `Validate`、编码与解码三处各自被拒。
func TestProtocolV24PlayerStateRejectsOutOfRangeHunger(t *testing.T) {
	invalid := PlayerState{Dimension: core.Overworld, Hunger: core.MaxHunger + 1}
	if err := invalid.Validate(); err == nil {
		t.Fatal("越界饥饿值通过了 Validate")
	}
	if _, _, err := encodeServerControlPayload(StatePlay, invalid); err == nil {
		t.Fatal("越界饥饿值被编码接受")
	}

	valid := PlayerState{Dimension: core.Overworld, Hunger: core.MaxHunger, WorldTimeTicks: 24000}
	id, payload, err := encodeServerControlPayload(StatePlay, valid)
	if err != nil {
		t.Fatal(err)
	}
	offset := playerStateHungerOffset(len(payload))
	corrupted := append([]byte(nil), payload...)
	corrupted[offset] = core.MaxHunger + 1
	if packet, err := decodeServerControlPayload(StatePlay, id, corrupted); err == nil {
		t.Fatalf("越界饥饿值 wire 载荷被解码接受: %#v", packet)
	}
	// 守卫排在真实断言之后：偏移必须真的指向饥饿值那一字节，否则上面那条拒绝
	// 可能是被相邻字段（氧气高字节 / 世界时间低字节）越界顶掉的。
	if payload[offset] != core.MaxHunger {
		t.Fatalf("夹具无效：hungerOffset 处是 %d，不是饥饿值 %d", payload[offset], core.MaxHunger)
	}
}

// TestProtocolV24PlayerInputCarriesEating 覆盖 v24 追加的进食输入位：它与
// `Mining` 同形（一个 bool 字节），追加在 `Mining` 之后且不重排既有布尔。
//
// 四种 (Mining, Eating) 组合全跑：只测同真同假的话，把两个字节写反的实现照样绿。
func TestProtocolV24PlayerInputCarriesEating(t *testing.T) {
	for _, tc := range []struct{ mining, eating bool }{
		{false, false}, {true, false}, {false, true}, {true, true},
	} {
		input := PlayerInput{Sequence: 7, Mining: tc.mining, Eating: tc.eating}
		id, payload, err := encodeClientPacketPayload(StatePlay, input)
		if err != nil {
			t.Fatal(err)
		}
		round, err := decodeClientPacketPayload(StatePlay, id, payload)
		if err != nil {
			t.Fatal(err)
		}
		got := round.(PlayerInput)
		if got.Mining != tc.mining || got.Eating != tc.eating {
			t.Fatalf("往返 (Mining,Eating) = (%v,%v)，想要 (%v,%v)",
				got.Mining, got.Eating, tc.mining, tc.eating)
		}
		// 进食位是载荷最末一字节，采掘位紧邻其前：位置性断言，防止两者互换。
		if payload[len(payload)-1] != boolWireByte(tc.eating) ||
			payload[len(payload)-2] != boolWireByte(tc.mining) {
			t.Fatalf("载荷尾部 = %x，想要 Mining=%v 后跟 Eating=%v",
				payload[len(payload)-2:], tc.mining, tc.eating)
		}
	}
}

func boolWireByte(value bool) byte {
	if value {
		return 1
	}
	return 0
}
