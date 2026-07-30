package network

import "testing"

func FuzzSmallPacketCodec(f *testing.F) {
	f.Add(uint8(StateHandshake), uint32(0), []byte{1})
	f.Add(uint8(StateLogin), uint32(0), []byte{0})
	f.Add(uint8(StatePlay), uint32(0), []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	f.Add(uint8(StatePlay), uint32(5), []byte{1, 0, 0, 0, 0, 0, 0, 0})
	f.Fuzz(func(t *testing.T, rawState uint8, packetID uint32, payload []byte) {
		state := State(rawState)
		if packet, err := decodeClientPacketPayload(state, packetID, payload); err == nil {
			gotID, gotPayload, encodeErr := encodeClientPacketPayload(state, packet)
			if encodeErr != nil || gotID != packetID || string(gotPayload) != string(payload) {
				t.Fatalf("client canonical round trip id=%d payload=%x; got id=%d payload=%x err=%v", packetID, payload, gotID, gotPayload, encodeErr)
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
