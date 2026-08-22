package network

import "testing"

func FuzzSmallPacketCodec(f *testing.F) {
	f.Add(uint8(StateHandshake), uint32(0), []byte{2})
	f.Add(uint8(StateHandshake), uint32(0), []byte{1})
	f.Add(uint8(StateHandshake), uint32(0), []byte{3})
	f.Add(uint8(StateLogin), uint32(0), []byte{0})
	// v23 LoginSuccess：16 字节 UUIDv4 + little-endian uint64 世界种子。
	f.Add(uint8(StateLogin), uint32(0), []byte{
		0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x46, 0x77,
		0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff,
		0x88, 0x77, 0x66, 0x55, 0x44, 0x33, 0x22, 0x11,
	})
	f.Add(uint8(StatePlay), uint32(0), []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	f.Add(uint8(StatePlay), uint32(5), []byte{1, 0, 0, 0, 0, 0, 0, 0})
	// PlayerState 的种子由编码器现算，尾部字段（v21 起含 Oxygen、v24 起含 Hunger）
	// 一变就自动跟上，不会像手写字节那样悄悄退化成"截断的旧版载荷"。饥饿值取
	// 非零非满的中间值：满值样本进不了"越界饥饿必须被拒"的邻域。
	if id, payload, err := encodeServerControlPayload(StatePlay, PlayerState{
		Dimension: 0, Health: 15, Oxygen: 0x0101, Hunger: 12, WorldTimeTicks: 24000,
	}); err == nil {
		f.Add(uint8(StatePlay), id, payload)
	}
	// PlayerInput 的种子同理现算：v24 的进食位必须进入语料，且取 Mining=false、
	// Eating=true 这组能分辨"两个布尔字节写反"的组合。
	if id, payload, err := encodeClientPacketPayload(StatePlay, PlayerInput{
		Sequence: 0x0102030405060708, Yaw: 1.5, Pitch: -0.5, Eating: true,
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
