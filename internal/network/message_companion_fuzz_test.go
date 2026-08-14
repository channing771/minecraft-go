package network

import "testing"

func FuzzCompanionMessageCodec(f *testing.F) {
	clientID, clientPayload, err := encodeClientPacketPayload(StatePlay, ChatCommand{Text: "@A x"})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(uint8(0), clientID, clientPayload)
	for _, packet := range []ServerPacket{
		validAcceptedChatEvent(),
		CompanionSpawn{ID: testCompanionID(1), Name: "A"},
		CompanionStates{States: []CompanionState{{ID: testCompanionID(1)}}},
		CompanionDespawn{ID: testCompanionID(1)},
	} {
		packetID, payload, encodeErr := encodeServerControlPayload(StatePlay, packet)
		if encodeErr != nil {
			f.Fatal(encodeErr)
		}
		f.Add(uint8(1), packetID, payload)
	}
	f.Add(uint8(0), uint32(12), []byte{0xff, 0xff, 0xff, 0xff, 0x0f})
	f.Add(uint8(1), uint32(18), append(make([]byte, 8), 0xff, 0xff, 0xff, 0xff, 0x0f))

	f.Fuzz(func(t *testing.T, direction uint8, packetID uint32, payload []byte) {
		if direction&1 == 0 {
			packet, decodeErr := decodeClientPacketPayload(StatePlay, packetID, payload)
			if decodeErr != nil {
				return
			}
			gotID, gotPayload, encodeErr := encodeClientPacketPayload(StatePlay, packet)
			if encodeErr != nil || gotID != packetID || string(gotPayload) != string(payload) {
				t.Fatalf("client canonical round trip id=%d payload=%x; got id=%d payload=%x err=%v",
					packetID, payload, gotID, gotPayload, encodeErr)
			}
			return
		}
		packet, decodeErr := decodeServerControlPayload(StatePlay, packetID, payload)
		if decodeErr != nil {
			return
		}
		gotID, gotPayload, encodeErr := encodeServerControlPayload(StatePlay, packet)
		if encodeErr != nil || gotID != packetID || string(gotPayload) != string(payload) {
			t.Fatalf("server canonical round trip id=%d payload=%x; got id=%d payload=%x err=%v",
				packetID, payload, gotID, gotPayload, encodeErr)
		}
	})
}
