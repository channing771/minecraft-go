package network

import (
	"strings"
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
		taskChatEvent(8, ChatEventTaskStopped, ChatRejectNone),
		taskChatEvent(9, ChatEventTaskFailed, ChatRejectReason(TaskFailInventoryFull)),
		taskChatEvent(10, ChatEventRejected, ChatRejectNotFollowing),
		companionSpeechEvent(11, strings.Repeat("x", 256)),
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
	// 非法 kind/reason 组合种子：任务 kind 带拒绝原因、TaskStopped 带停止拒绝原因、
	// TaskFailed 带越界原因（21）、台词 kind 带拒绝原因、未知 kind 10；
	// kind 9 的合法台词 wire 也作为种子探索 v19 新路径。
	f.Add(uint8(1), uint32(16), taskCombinationSeed(ChatEventTaskStarted, byte(ChatRejectInvalidFormat)))
	f.Add(uint8(1), uint32(16), taskCombinationSeed(ChatEventTaskStopped, byte(ChatRejectNotFollowing)))
	f.Add(uint8(1), uint32(16), taskCombinationSeed(ChatEventTaskFailed, 15))
	f.Add(uint8(1), uint32(16), taskCombinationSeed(ChatEventTaskFailed, 21))
	f.Add(uint8(1), uint32(16), taskCombinationSeed(ChatEventCompanionSpeech, byte(ChatRejectQueueFull)))
	f.Add(uint8(1), uint32(16), taskCombinationSeed(ChatEventKind(9), 0))
	f.Add(uint8(1), uint32(16), taskCombinationSeed(ChatEventKind(10), 0))
	// 无效 UTF-8 文本槽位种子（F-4 定界突变）：台词与指令槽位的裸 0xFF 0xFE
	// 与截断多字节序列——解码必须在 string 原语层拒绝，fuzz 从这些定界点
	// 向外探索同类字节。wire 由 chatEventWireWithRawTextSlot 手工拼出。
	f.Add(uint8(1), uint32(16), chatEventWireWithRawTextSlot(ChatEventCompanionSpeech, []byte{0xFF, 0xFE}))
	f.Add(uint8(1), uint32(16), chatEventWireWithRawTextSlot(ChatEventCompanionSpeech, []byte{0xE5, 0x8F}))
	f.Add(uint8(1), uint32(16), chatEventWireWithRawTextSlot(ChatEventAccepted, []byte{0xFF, 0xFE}))

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
