package network

import (
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

// taskCombinationSeed 手工构造任意 kind/reason 组合的 ChatEvent wire 种子；
// 非法组合无法经编码器产生，fuzz 需要从 wire 层直接探索它们。
func taskCombinationSeed(kind ChatEventKind, reason byte) []byte {
	playerID := core.PlayerID(testCompanionID(9))
	companionID := testCompanionID(1)
	var encoder byteEncoder
	encoder.u64(1)
	encoder.data = append(encoder.data, playerID[:]...)
	encoder.string("Chen", 128)
	encoder.data = append(encoder.data, companionID[:]...)
	encoder.string("A", 128)
	encoder.u8(uint8(kind))
	encoder.u8(reason)
	encoder.string("x", 1024)
	return encoder.data
}

func FuzzCompanionMessageCodec(f *testing.F) {
	clientID, clientPayload, err := encodeClientPacketPayload(StatePlay, ChatCommand{Text: "@A x"})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(uint8(0), clientID, clientPayload)
	for _, packet := range []ServerPacket{
		validAcceptedChatEvent(),
		taskChatEvent(2, ChatEventTaskStarted, ChatRejectNone),
		taskChatEvent(3, ChatEventTaskProgress, ChatRejectNone),
		taskChatEvent(4, ChatEventTaskCompleted, ChatRejectNone),
		taskChatEvent(5, ChatEventTaskFailed, ChatRejectReason(TaskFailPlannerUnavailable)),
		taskChatEvent(6, ChatEventTaskTimedOut, ChatRejectNone),
		taskChatEvent(7, ChatEventRejected, ChatRejectQueueFull),
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
	// 非法 kind/reason 组合种子：任务 kind 带拒绝原因、TaskFailed 带越界原因、未知 kind。
	f.Add(uint8(1), uint32(16), taskCombinationSeed(ChatEventTaskStarted, byte(ChatRejectInvalidFormat)))
	f.Add(uint8(1), uint32(16), taskCombinationSeed(ChatEventTaskFailed, 15))
	f.Add(uint8(1), uint32(16), taskCombinationSeed(ChatEventTaskFailed, 20))
	f.Add(uint8(1), uint32(16), taskCombinationSeed(ChatEventKind(8), 0))

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
