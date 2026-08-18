package network

import (
	"reflect"
	"strings"
	"testing"

	"github.com/channing771/mornlea/internal/companion"
)

// TestChatCommandTextLimitLocksToCompanionPlanCommandBytes 锁定 network 侧聊天
// 指令文本上限与 companion.MaxPlanCommandBytes 同源（E7 同源化锁）：边界夹具全部
// 由 companion 常量构造，恰好等于上限的文本必须通过 Validate、按上限编码并往返，
// 超出一字节必须被整体拒绝。companion 常量或 network 校验/编码上限任何一侧漂移，
// 本测试都会变红——取代此前只靠注释约定的「裸字面量 + 注释」防漂移方式。
func TestChatCommandTextLimitLocksToCompanionPlanCommandBytes(t *testing.T) {
	atLimit := strings.Repeat("x", companion.MaxPlanCommandBytes)
	command := ChatCommand{Text: atLimit}
	if err := command.Validate(); err != nil {
		t.Fatalf("恰好 %d-byte 指令被拒绝: %v", companion.MaxPlanCommandBytes, err)
	}
	packetID, payload, err := encodeClientPacketPayload(StatePlay, command)
	if err != nil || packetID != 12 {
		t.Fatalf("上限指令编码 = (%d,%d,%v)", packetID, len(payload), err)
	}
	// wire 上限与文本上限同源推导：uvarint 长度前缀 2 bytes + 文本字节。
	if len(payload) != companion.MaxPlanCommandBytes+2 {
		t.Fatalf("上限指令 wire = %d bytes，想要 %d（MaxPlanCommandBytes+2）",
			len(payload), companion.MaxPlanCommandBytes+2)
	}
	decoded, err := decodeClientPacketPayload(StatePlay, packetID, payload)
	if err != nil || !reflect.DeepEqual(decoded, command) {
		t.Fatalf("上限指令往返 = %#v, %v", decoded, err)
	}

	overLimit := strings.Repeat("x", companion.MaxPlanCommandBytes+1)
	if err := (ChatCommand{Text: overLimit}).Validate(); err == nil {
		t.Fatalf("%d-byte 指令通过 Validate", len(overLimit))
	}
	if _, _, err := encodeClientPacketPayload(StatePlay, ChatCommand{Text: overLimit}); err == nil {
		t.Fatal("超限指令被编码")
	}
}

// TestChatEventTextLimitsLocksToCompanionConstants 锁定 ChatEvent 唯一文本槽位在
// 两种 kind 下的上限与 companion 常量同源（E7 同源化锁）：任务事实事件复述的
// 原始指令恰好 MaxPlanCommandBytes 字节合法，CompanionSpeech 台词恰好
// MaxDialogueLineBytes 字节合法，各超出一字节即整体拒绝且不得进入 wire。
// 台词上界与 Dialogue 表达平面（dialogue_types.go）共用同一常量，两侧不可能漂移。
func TestChatEventTextLimitsLocksToCompanionConstants(t *testing.T) {
	commandEvent := taskChatEvent(1, ChatEventTaskStarted, ChatRejectNone)
	commandEvent.Command = strings.Repeat("x", companion.MaxPlanCommandBytes)
	if err := commandEvent.Validate(); err != nil {
		t.Fatalf("恰好 %d-byte 指令事件被拒绝: %v", companion.MaxPlanCommandBytes, err)
	}
	packetID, payload, err := encodeServerControlPayload(StatePlay, commandEvent)
	if err != nil || packetID != 16 {
		t.Fatalf("上限指令事件编码 = (%d,%d,%v)", packetID, len(payload), err)
	}
	decoded, err := decodeServerControlPayload(StatePlay, packetID, payload)
	if err != nil || !reflect.DeepEqual(decoded, commandEvent) {
		t.Fatalf("上限指令事件往返 = %#v, %v", decoded, err)
	}
	overCommand := commandEvent
	overCommand.Command = strings.Repeat("x", companion.MaxPlanCommandBytes+1)
	if err := overCommand.Validate(); err == nil {
		t.Fatalf("%d-byte 指令事件通过 Validate", len(overCommand.Command))
	}
	if _, _, err := encodeServerControlPayload(StatePlay, overCommand); err == nil {
		t.Fatal("超限指令事件被编码")
	}

	speechEvent := companionSpeechEvent(2, strings.Repeat("x", companion.MaxDialogueLineBytes))
	if err := speechEvent.Validate(); err != nil {
		t.Fatalf("恰好 %d-byte 台词事件被拒绝: %v", companion.MaxDialogueLineBytes, err)
	}
	packetID, payload, err = encodeServerControlPayload(StatePlay, speechEvent)
	if err != nil || packetID != 16 {
		t.Fatalf("上限台词事件编码 = (%d,%d,%v)", packetID, len(payload), err)
	}
	decoded, err = decodeServerControlPayload(StatePlay, packetID, payload)
	if err != nil || !reflect.DeepEqual(decoded, speechEvent) {
		t.Fatalf("上限台词事件往返 = %#v, %v", decoded, err)
	}
	overSpeech := companionSpeechEvent(3, strings.Repeat("x", companion.MaxDialogueLineBytes+1))
	if err := overSpeech.Validate(); err == nil {
		t.Fatalf("%d-byte 台词事件通过 Validate", companion.MaxDialogueLineBytes+1)
	}
	if _, _, err := encodeServerControlPayload(StatePlay, overSpeech); err == nil {
		t.Fatal("超限台词事件被编码")
	}
}
