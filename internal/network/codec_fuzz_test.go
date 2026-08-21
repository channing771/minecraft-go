package network

import "testing"

func FuzzSmallPacketCodec(f *testing.F) {
	f.Add(uint8(StateHandshake), uint32(0), []byte{2})
	f.Add(uint8(StateHandshake), uint32(0), []byte{1})
	f.Add(uint8(StateHandshake), uint32(0), []byte{3})
	f.Add(uint8(StateLogin), uint32(0), []byte{0})
	f.Add(uint8(StatePlay), uint32(0), []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	f.Add(uint8(StatePlay), uint32(5), []byte{1, 0, 0, 0, 0, 0, 0, 0})
	// PlayerState 的种子由编码器现算，尾部字段（v21 起含 Oxygen）一变就自动跟上，
	// 不会像手写字节那样悄悄退化成"截断的旧版载荷"。
	if id, payload, err := encodeServerControlPayload(StatePlay, PlayerState{
		Dimension: 0, Health: 15, Oxygen: 0x0101, WorldTimeTicks: 24000,
	}); err == nil {
		f.Add(uint8(StatePlay), id, payload)
	}
	// TillSoil 的种子同样由编码器现算：v22 的唯一 wire 变化必须进入语料，
	// 且形状随字段变动自动跟上。
	if id, payload, err := encodeClientPacketPayload(StatePlay, TillSoil{
		Sequence: 0x0102030405060708, Yaw: 1.5, Pitch: -0.5,
	}); err == nil {
		f.Add(uint8(StatePlay), id, payload)
	}
	f.Fuzz(func(t *testing.T, rawState uint8, packetID uint32, payload []byte) {
		state := State(rawState)
		if packet, err := decodeClientPacketPayload(state, packetID, payload); err == nil {
			isStateMachineRejection := false
			switch packet.(type) {
			case ClientHello:
				isStateMachineRejection = state == StateHandshake
			case LoginStart:
				isStateMachineRejection = state == StateLogin
			}
			if !isStateMachineRejection {
				gotID, gotPayload, encodeErr := encodeClientPacketPayload(state, packet)
				if encodeErr != nil || gotID != packetID || string(gotPayload) != string(payload) {
					t.Fatalf("client canonical round trip id=%d payload=%x; got id=%d payload=%x err=%v", packetID, payload, gotID, gotPayload, encodeErr)
				}
			}
		}
		if packet, err := decodeServerControlPayload(state, packetID, payload); err == nil {
			gotID, gotPayload, encodeErr := encodeServerControlPayload(state, packet)
			if encodeErr != nil || gotID != packetID || string(gotPayload) != string(payload) {
				t.Fatalf("server canonical round trip id=%d payload=%x; got id=%d payload=%x err=%v", packetID, payload, gotID, gotPayload, encodeErr)
			}
		}
	})
}
