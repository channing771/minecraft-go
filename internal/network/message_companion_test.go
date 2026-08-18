package network

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"math"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
)

func TestCompanionMessageIDsAreAppendOnly(t *testing.T) {
	assertClientRegistry(t, []struct {
		state  State
		packet ClientPacket
		id     uint32
	}{
		{StatePlay, PlayerInput{}, 0},
		{StatePlay, PlaceBlock{}, 2},
		{StatePlay, RequestChunkResync{}, 3},
		{StatePlay, KeepAliveReply{}, 4},
		{StatePlay, SelectHotbar{}, 5},
		{StatePlay, MoveInventoryStack{}, 6},
		{StatePlay, CraftRecipe{}, 7},
		{StatePlay, OpenContainer{}, 8},
		{StatePlay, MoveContainerStack{}, 9},
		{StatePlay, CloseContainer{}, 10},
		{StatePlay, DropSelectedItem{}, 11},
		{StatePlay, ChatCommand{}, 12},
	})
	assertServerRegistry(t, []struct {
		state  State
		packet ServerPacket
		id     uint32
	}{
		{StatePlay, ChunkSnapshot{}, 0},
		{StatePlay, BlockChanges{}, 1},
		{StatePlay, ForgetChunks{}, 2},
		{StatePlay, PlayerState{}, 3},
		{StatePlay, CommandRejected{}, 4},
		{StatePlay, KeepAlive{}, 5},
		{StatePlay, Disconnect{}, 6},
		{StatePlay, RemotePlayerSpawn{}, 7},
		{StatePlay, RemotePlayerDespawn{}, 8},
		{StatePlay, RemotePlayerStates{}, 9},
		{StatePlay, InventoryState{}, 10},
		{StatePlay, ItemDropUpserts{}, 11},
		{StatePlay, ItemDropRemoves{}, 12},
		{StatePlay, FurnaceState{}, 13},
		{StatePlay, ContainerClosed{}, 14},
		{StatePlay, ChestState{}, 15},
		{StatePlay, ChatEvent{}, 16},
		{StatePlay, CompanionSpawn{}, 17},
		{StatePlay, CompanionStates{}, 18},
		{StatePlay, CompanionDespawn{}, 19},
	})
	if _, ok := clientPacketForID(StatePlay, 1); ok {
		t.Fatal("Play client packet ID 1 必须保持未分配")
	}
	if _, ok := clientPacketForID(StatePlay, 13); ok {
		t.Fatal("未知 client packet ID 13 被接受")
	}
	if _, ok := serverPacketForID(StatePlay, 20); ok {
		t.Fatal("未知 server packet ID 20 被接受")
	}
}

func TestChatEventTaskEnumsAreFrozen(t *testing.T) {
	// v17 在既有 kind 1..2 之后追加任务生命周期 kind 3..7，v18 追加停止 kind 8，
	// v19 追加伙伴台词 kind 9；未知 kind 10 仍非法。
	kinds := []struct {
		name string
		got  ChatEventKind
		want uint8
	}{
		{"accepted", ChatEventAccepted, 1},
		{"rejected", ChatEventRejected, 2},
		{"task started", ChatEventTaskStarted, 3},
		{"task progress", ChatEventTaskProgress, 4},
		{"task completed", ChatEventTaskCompleted, 5},
		{"task failed", ChatEventTaskFailed, 6},
		{"task timed out", ChatEventTaskTimedOut, 7},
		{"task stopped", ChatEventTaskStopped, 8},
		{"companion speech", ChatEventCompanionSpeech, 9},
	}
	for _, kind := range kinds {
		if uint8(kind.got) != kind.want {
			t.Fatalf("%s kind = %d, want %d", kind.name, kind.got, kind.want)
		}
	}

	// QueueFull 取 4：值 3 预留，v18 追加 NotFollowing=5，拒绝原因保留 0..15 的编号空间。
	reasons := []struct {
		name string
		got  ChatRejectReason
		want uint8
	}{
		{"none", ChatRejectNone, 0},
		{"invalid format", ChatRejectInvalidFormat, 1},
		{"unknown companion", ChatRejectUnknownCompanion, 2},
		{"queue full", ChatRejectQueueFull, 4},
		{"not following", ChatRejectNotFollowing, 5},
	}
	for _, reason := range reasons {
		if uint8(reason.got) != reason.want {
			t.Fatalf("%s reject reason = %d, want %d", reason.name, reason.got, reason.want)
		}
	}

	// TaskFailReason 与拒绝原因共用 reason 槽位，从 16 起错开区间；v18 追加 InventoryFull=20。
	failReasons := []struct {
		name string
		got  TaskFailReason
		want uint8
	}{
		{"planner unavailable", TaskFailPlannerUnavailable, 16},
		{"invalid plan", TaskFailInvalidPlan, 17},
		{"path unreachable", TaskFailPathUnreachable, 18},
		{"world changed", TaskFailWorldChanged, 19},
		{"inventory full", TaskFailInventoryFull, 20},
	}
	for _, reason := range failReasons {
		if uint8(reason.got) != reason.want {
			t.Fatalf("%s task fail reason = %d, want %d", reason.name, reason.got, reason.want)
		}
	}
}

func TestChatEventTaskLifecycleCombinationsValidateAndRoundTrip(t *testing.T) {
	// 任务事件必须携带完整伙伴身份与原始指令；TaskFailed 额外要求固定失败原因枚举，
	// QueueFull 与 NotFollowing 拒绝事件携带与 Accepted 相同的身份与指令。
	valid := []ChatEvent{
		taskChatEvent(2, ChatEventTaskStarted, ChatRejectNone),
		taskChatEvent(3, ChatEventTaskProgress, ChatRejectNone),
		taskChatEvent(4, ChatEventTaskCompleted, ChatRejectNone),
		taskChatEvent(5, ChatEventTaskTimedOut, ChatRejectNone),
		taskChatEvent(6, ChatEventTaskFailed, ChatRejectReason(TaskFailPlannerUnavailable)),
		taskChatEvent(7, ChatEventTaskFailed, ChatRejectReason(TaskFailInvalidPlan)),
		taskChatEvent(8, ChatEventTaskFailed, ChatRejectReason(TaskFailPathUnreachable)),
		taskChatEvent(9, ChatEventTaskFailed, ChatRejectReason(TaskFailWorldChanged)),
		taskChatEvent(10, ChatEventTaskFailed, ChatRejectReason(TaskFailInventoryFull)),
		taskChatEvent(11, ChatEventRejected, ChatRejectQueueFull),
		taskChatEvent(12, ChatEventRejected, ChatRejectNotFollowing),
		taskChatEvent(13, ChatEventTaskStopped, ChatRejectNone),
	}
	for _, event := range valid {
		if err := event.Validate(); err != nil {
			t.Fatalf("合法任务事件 %d/%d 被拒绝: %v", event.Kind, event.RejectReason, err)
		}
		packetID, payload, err := encodeServerControlPayload(StatePlay, event)
		if err != nil || packetID != 16 {
			t.Fatalf("任务事件编码 = (%d,%d,%v)", packetID, len(payload), err)
		}
		decoded, err := decodeServerControlPayload(StatePlay, packetID, payload)
		if err != nil || !reflect.DeepEqual(decoded, event) {
			t.Fatalf("任务事件往返 = %#v, %v，想要 %#v", decoded, err, event)
		}
	}
}

func TestChatEventTaskLifecycleCombinationsAreRejectedAtomically(t *testing.T) {
	// 任一字段不合要求即整体拒绝：不存在"部分应用"的组合。
	invalid := []interface{ Validate() error }{
		// 非 TaskFailed 的任务 kind 携带任何 reason 都非法。
		mutateTaskEvent(taskChatEvent(1, ChatEventTaskStarted, ChatRejectNone), func(e *ChatEvent) { e.RejectReason = ChatRejectInvalidFormat }),
		mutateTaskEvent(taskChatEvent(1, ChatEventTaskProgress, ChatRejectNone), func(e *ChatEvent) { e.RejectReason = ChatRejectQueueFull }),
		mutateTaskEvent(taskChatEvent(1, ChatEventTaskCompleted, ChatRejectNone), func(e *ChatEvent) { e.RejectReason = ChatRejectReason(TaskFailInvalidPlan) }),
		mutateTaskEvent(taskChatEvent(1, ChatEventTaskTimedOut, ChatRejectNone), func(e *ChatEvent) { e.RejectReason = ChatRejectReason(TaskFailWorldChanged) }),
		// 任务事件缺少伙伴身份。
		mutateTaskEvent(taskChatEvent(1, ChatEventTaskStarted, ChatRejectNone), func(e *ChatEvent) { e.CompanionID = companion.ID{} }),
		mutateTaskEvent(taskChatEvent(1, ChatEventTaskFailed, ChatRejectReason(TaskFailPlannerUnavailable)), func(e *ChatEvent) { e.CompanionID = companion.ID{} }),
		mutateTaskEvent(taskChatEvent(1, ChatEventTaskProgress, ChatRejectNone), func(e *ChatEvent) { e.CompanionName = "" }),
		mutateTaskEvent(taskChatEvent(1, ChatEventTaskCompleted, ChatRejectNone), func(e *ChatEvent) { e.CompanionName = " A" }),
		// 任务事件缺少合法原始指令。
		mutateTaskEvent(taskChatEvent(1, ChatEventTaskStarted, ChatRejectNone), func(e *ChatEvent) { e.Command = "" }),
		mutateTaskEvent(taskChatEvent(1, ChatEventTaskTimedOut, ChatRejectNone), func(e *ChatEvent) { e.Command = " x" }),
		mutateTaskEvent(taskChatEvent(1, ChatEventTaskFailed, ChatRejectReason(TaskFailPathUnreachable)), func(e *ChatEvent) { e.Command = "x " }),
		// TaskFailed 原因必须落在 16..20 固定枚举内。
		mutateTaskEvent(taskChatEvent(1, ChatEventTaskFailed, ChatRejectNone), func(e *ChatEvent) {}),
		mutateTaskEvent(taskChatEvent(1, ChatEventTaskFailed, ChatRejectInvalidFormat), func(e *ChatEvent) {}),
		mutateTaskEvent(taskChatEvent(1, ChatEventTaskFailed, ChatRejectUnknownCompanion), func(e *ChatEvent) {}),
		mutateTaskEvent(taskChatEvent(1, ChatEventTaskFailed, ChatRejectQueueFull), func(e *ChatEvent) {}),
		ChatEvent{EventID: 1, PlayerID: core.PlayerID(testCompanionID(9)), PlayerName: "Chen",
			CompanionID: testCompanionID(1), CompanionName: "A", Kind: ChatEventTaskFailed,
			RejectReason: ChatRejectReason(15), Command: "x"},
		ChatEvent{EventID: 1, PlayerID: core.PlayerID(testCompanionID(9)), PlayerName: "Chen",
			CompanionID: testCompanionID(1), CompanionName: "A", Kind: ChatEventTaskFailed,
			RejectReason: ChatRejectReason(21), Command: "x"},
		// TaskStopped 是任务事件：reason 必须为 None，且携带完整伙伴身份与原始指令。
		mutateTaskEvent(taskChatEvent(1, ChatEventTaskStopped, ChatRejectNone), func(e *ChatEvent) { e.RejectReason = ChatRejectInvalidFormat }),
		mutateTaskEvent(taskChatEvent(1, ChatEventTaskStopped, ChatRejectNone), func(e *ChatEvent) { e.RejectReason = ChatRejectQueueFull }),
		mutateTaskEvent(taskChatEvent(1, ChatEventTaskStopped, ChatRejectNone), func(e *ChatEvent) { e.RejectReason = ChatRejectNotFollowing }),
		mutateTaskEvent(taskChatEvent(1, ChatEventTaskStopped, ChatRejectNone), func(e *ChatEvent) { e.RejectReason = ChatRejectReason(TaskFailInventoryFull) }),
		mutateTaskEvent(taskChatEvent(1, ChatEventTaskStopped, ChatRejectNone), func(e *ChatEvent) { e.CompanionID = companion.ID{} }),
		mutateTaskEvent(taskChatEvent(1, ChatEventTaskStopped, ChatRejectNone), func(e *ChatEvent) { e.CompanionName = "" }),
		mutateTaskEvent(taskChatEvent(1, ChatEventTaskStopped, ChatRejectNone), func(e *ChatEvent) { e.Command = "" }),
		mutateTaskEvent(taskChatEvent(1, ChatEventTaskStopped, ChatRejectNone), func(e *ChatEvent) { e.Command = " x" }),
		// QueueFull 与 NotFollowing 拒绝都必须携带完整伙伴身份与合法指令。
		ChatEvent{EventID: 1, PlayerID: core.PlayerID(testCompanionID(9)), PlayerName: "Chen",
			Kind: ChatEventRejected, RejectReason: ChatRejectQueueFull},
		mutateTaskEvent(taskChatEvent(1, ChatEventRejected, ChatRejectQueueFull), func(e *ChatEvent) { e.CompanionID = companion.ID{} }),
		mutateTaskEvent(taskChatEvent(1, ChatEventRejected, ChatRejectQueueFull), func(e *ChatEvent) { e.CompanionName = "" }),
		mutateTaskEvent(taskChatEvent(1, ChatEventRejected, ChatRejectQueueFull), func(e *ChatEvent) { e.Command = "" }),
		mutateTaskEvent(taskChatEvent(1, ChatEventRejected, ChatRejectQueueFull), func(e *ChatEvent) { e.Command = " x" }),
		ChatEvent{EventID: 1, PlayerID: core.PlayerID(testCompanionID(9)), PlayerName: "Chen",
			Kind: ChatEventRejected, RejectReason: ChatRejectNotFollowing},
		mutateTaskEvent(taskChatEvent(1, ChatEventRejected, ChatRejectNotFollowing), func(e *ChatEvent) { e.CompanionID = companion.ID{} }),
		mutateTaskEvent(taskChatEvent(1, ChatEventRejected, ChatRejectNotFollowing), func(e *ChatEvent) { e.CompanionName = "" }),
		mutateTaskEvent(taskChatEvent(1, ChatEventRejected, ChatRejectNotFollowing), func(e *ChatEvent) { e.Command = "" }),
		mutateTaskEvent(taskChatEvent(1, ChatEventRejected, ChatRejectNotFollowing), func(e *ChatEvent) { e.Command = " x" }),
		// 其余 RejectReason 值（含预留 3）在 Rejected 上仍非法。
		ChatEvent{EventID: 1, PlayerID: core.PlayerID(testCompanionID(9)), PlayerName: "Chen",
			Kind: ChatEventRejected, RejectReason: ChatRejectReason(3)},
		ChatEvent{EventID: 1, PlayerID: core.PlayerID(testCompanionID(9)), PlayerName: "Chen",
			Kind: ChatEventRejected, RejectReason: ChatRejectReason(TaskFailInvalidPlan)},
		// 任务失败原因不得出现在非 TaskFailed kind 上（含 v18 新增的 TaskStopped）。
		mutateTaskEvent(validAcceptedChatEvent(), func(e *ChatEvent) { e.RejectReason = ChatRejectReason(TaskFailInvalidPlan) }),
		mutateTaskEvent(taskChatEvent(1, ChatEventTaskStopped, ChatRejectNone), func(e *ChatEvent) { e.RejectReason = ChatRejectReason(TaskFailWorldChanged) }),
		mutateTaskEvent(taskChatEvent(1, ChatEventRejected, ChatRejectInvalidFormat), func(e *ChatEvent) {
			e.RejectReason = ChatRejectReason(TaskFailWorldChanged)
			e.CompanionID = testCompanionID(1)
			e.Command = "x"
		}),
		// 未知 kind 保持非法。
		ChatEvent{EventID: 1, PlayerID: core.PlayerID(testCompanionID(9)), PlayerName: "Chen",
			CompanionID: testCompanionID(1), CompanionName: "A", Kind: ChatEventKind(10),
			RejectReason: ChatRejectNone, Command: "x"},
	}
	for _, message := range invalid {
		if err := message.Validate(); err == nil {
			t.Fatalf("非法任务组合被接受: %+v", message)
		}
		// 编码同样拒绝：非法组合不得进入 wire。
		if event, ok := message.(ChatEvent); ok {
			if _, _, err := encodeServerControlPayload(StatePlay, event); err == nil {
				t.Fatalf("非法任务组合被编码: %+v", event)
			}
		}
	}
}

func TestChatEventTaskDecoderRejectsInvalidKindReasonCombinations(t *testing.T) {
	// 从合法 TaskStarted wire 出发做单字节突变，解码必须整体拒绝。
	started := taskChatEvent(1, ChatEventTaskStarted, ChatRejectNone)
	_, eventWire, err := encodeServerControlPayload(StatePlay, started)
	if err != nil {
		t.Fatal(err)
	}
	kindOffset := chatEventKindOffset(started)
	for _, mutation := range []struct {
		name   string
		offset int
		value  byte
	}{
		{"unknown kind", kindOffset, 10},
		{"task kind with rejection reason", kindOffset + 1, byte(ChatRejectInvalidFormat)},
		{"task kind with queue full reason", kindOffset + 1, byte(ChatRejectQueueFull)},
		{"task kind with task fail reason", kindOffset + 1, byte(TaskFailPlannerUnavailable)},
	} {
		payload := append([]byte(nil), eventWire...)
		payload[mutation.offset] = mutation.value
		if packet, err := decodeServerControlPayload(StatePlay, 16, payload); err == nil || packet != nil {
			t.Fatalf("%s wire 解码为 %#v, %v", mutation.name, packet, err)
		}
	}

	// TaskStopped wire 的 reason 槽位只接受 None：携带拒绝原因或失败原因都必须整体拒绝，
	// 未知 kind 10 也保持非法。
	stopped := taskChatEvent(1, ChatEventTaskStopped, ChatRejectNone)
	_, stoppedWire, err := encodeServerControlPayload(StatePlay, stopped)
	if err != nil {
		t.Fatal(err)
	}
	for _, reason := range []byte{byte(ChatRejectNotFollowing), byte(ChatRejectQueueFull), byte(TaskFailInventoryFull), byte(21)} {
		payload := append([]byte(nil), stoppedWire...)
		payload[kindOffset+1] = reason
		if packet, err := decodeServerControlPayload(StatePlay, 16, payload); err == nil || packet != nil {
			t.Fatalf("TaskStopped reason %d wire 解码为 %#v, %v", reason, packet, err)
		}
	}
	unknownKind := append([]byte(nil), stoppedWire...)
	unknownKind[kindOffset] = 10
	if packet, err := decodeServerControlPayload(StatePlay, 16, unknownKind); err == nil || packet != nil {
		t.Fatalf("未知 kind 10 wire 解码为 %#v, %v", packet, err)
	}

	// TaskFailed 的 reason 槽位只接受 16..20；15/21 与拒绝原因 4 都必须被拒。
	failed := taskChatEvent(2, ChatEventTaskFailed, ChatRejectReason(TaskFailPlannerUnavailable))
	_, failedWire, err := encodeServerControlPayload(StatePlay, failed)
	if err != nil {
		t.Fatal(err)
	}
	for _, reason := range []byte{0, 4, 15, 21} {
		payload := append([]byte(nil), failedWire...)
		payload[kindOffset+1] = reason
		if packet, err := decodeServerControlPayload(StatePlay, 16, payload); err == nil || packet != nil {
			t.Fatalf("TaskFailed reason %d wire 解码为 %#v, %v", reason, packet, err)
		}
	}
	for _, reason := range []byte{16, 17, 18, 19, 20} {
		payload := append([]byte(nil), failedWire...)
		payload[kindOffset+1] = reason
		packet, err := decodeServerControlPayload(StatePlay, 16, payload)
		if err != nil {
			t.Fatalf("TaskFailed reason %d 合法 wire 被拒: %v", reason, err)
		}
		event, ok := packet.(ChatEvent)
		if !ok || event.RejectReason != ChatRejectReason(reason) {
			t.Fatalf("TaskFailed reason %d 往返 = %#v", reason, packet)
		}
	}

	// QueueFull 与 NotFollowing 拒绝缺少伙伴身份的 wire 必须被拒。
	queueFull := taskChatEvent(3, ChatEventRejected, ChatRejectQueueFull)
	_, queueFullWire, err := encodeServerControlPayload(StatePlay, queueFull)
	if err != nil {
		t.Fatal(err)
	}
	queueFullWire = append([]byte(nil), queueFullWire...)
	clear(queueFullWire[8+16+1+len(queueFull.PlayerName) : 8+16+1+len(queueFull.PlayerName)+16])
	if packet, err := decodeServerControlPayload(StatePlay, 16, queueFullWire); err == nil || packet != nil {
		t.Fatalf("缺少伙伴身份的 QueueFull wire 解码为 %#v, %v", packet, err)
	}
	notFollowing := taskChatEvent(4, ChatEventRejected, ChatRejectNotFollowing)
	_, notFollowingWire, err := encodeServerControlPayload(StatePlay, notFollowing)
	if err != nil {
		t.Fatal(err)
	}
	notFollowingWire = append([]byte(nil), notFollowingWire...)
	clear(notFollowingWire[8+16+1+len(notFollowing.PlayerName) : 8+16+1+len(notFollowing.PlayerName)+16])
	if packet, err := decodeServerControlPayload(StatePlay, 16, notFollowingWire); err == nil || packet != nil {
		t.Fatalf("缺少伙伴身份的 NotFollowing wire 解码为 %#v, %v", packet, err)
	}
}

func TestChatEventCompanionSpeechCombinationsValidateAndRoundTrip(t *testing.T) {
	// v19 追加的 CompanionSpeech 是 ChatEvent 中唯一允许携带模型生成文本的 kind：
	// 必须携带合法 event ID、完整玩家与伙伴身份、1..256 bytes 合法台词与 None reason，
	// 且不得复述玩家指令。256-byte 单字节与多字节混合台词都是合法上界。
	multibyteMax := strings.Repeat("台", 85) + "a"
	if len(multibyteMax) != 256 {
		t.Fatalf("多字节台词夹具 = %d bytes，想要 256", len(multibyteMax))
	}
	valid := []ChatEvent{
		companionSpeechEvent(1, "开始干活。"),
		companionSpeechEvent(2, strings.Repeat("x", 256)),
		companionSpeechEvent(3, multibyteMax),
	}
	for _, event := range valid {
		if err := event.Validate(); err != nil {
			t.Fatalf("合法台词事件 %d 被拒绝: %v", event.EventID, err)
		}
		packetID, payload, err := encodeServerControlPayload(StatePlay, event)
		if err != nil || packetID != 16 {
			t.Fatalf("台词事件编码 = (%d,%d,%v)", packetID, len(payload), err)
		}
		decoded, err := decodeServerControlPayload(StatePlay, packetID, payload)
		if err != nil || !reflect.DeepEqual(decoded, event) {
			t.Fatalf("台词事件往返 = %#v, %v，想要 %#v", decoded, err, event)
		}
	}
}

func TestChatEventCompanionSpeechCombinationsAreRejectedAtomically(t *testing.T) {
	// 任一字段不满足 Speech 的组合要求即整体拒绝，且不得进入 wire。
	invalid := []interface{ Validate() error }{
		// 台词长度与文本纪律：空台词、257-byte、首尾空白（含全角空格与不换行空格）。
		companionSpeechEvent(1, ""),
		companionSpeechEvent(1, strings.Repeat("x", 257)),
		companionSpeechEvent(1, " 台词"),
		companionSpeechEvent(1, "台词 "),
		companionSpeechEvent(1, "\u3000台词"),
		companionSpeechEvent(1, "台词\u00a0"),
		// 台词不得包含 NUL 或任何 Unicode control。
		companionSpeechEvent(1, "台\x00词"),
		companionSpeechEvent(1, "台\n词"),
		// 台词必须是有效 UTF-8。
		companionSpeechEvent(1, string([]byte{0xff})),
		// reason 必须为 None：拒绝原因与任务失败原因都不允许出现在台词事件上。
		mutateTaskEvent(companionSpeechEvent(1, "台词"), func(e *ChatEvent) { e.RejectReason = ChatRejectInvalidFormat }),
		mutateTaskEvent(companionSpeechEvent(1, "台词"), func(e *ChatEvent) { e.RejectReason = ChatRejectQueueFull }),
		mutateTaskEvent(companionSpeechEvent(1, "台词"), func(e *ChatEvent) { e.RejectReason = ChatRejectNotFollowing }),
		mutateTaskEvent(companionSpeechEvent(1, "台词"), func(e *ChatEvent) { e.RejectReason = ChatRejectReason(TaskFailInventoryFull) }),
		// 台词事件不得携带玩家指令字段：它只表达台词，不复述触发指令。
		mutateTaskEvent(companionSpeechEvent(1, "台词"), func(e *ChatEvent) { e.Command = "x" }),
		// 伙伴身份必须完整。
		mutateTaskEvent(companionSpeechEvent(1, "台词"), func(e *ChatEvent) { e.CompanionID = companion.ID{} }),
		mutateTaskEvent(companionSpeechEvent(1, "台词"), func(e *ChatEvent) { e.CompanionName = "" }),
		mutateTaskEvent(companionSpeechEvent(1, "台词"), func(e *ChatEvent) { e.CompanionName = " A" }),
		// 台词字段只属于 Speech kind：事实与拒绝 kind 携带台词都必须整条拒绝。
		mutateTaskEvent(validAcceptedChatEvent(), func(e *ChatEvent) { e.Speech = "台词" }),
		mutateTaskEvent(taskChatEvent(1, ChatEventTaskStarted, ChatRejectNone), func(e *ChatEvent) { e.Speech = "台词" }),
		mutateTaskEvent(taskChatEvent(1, ChatEventTaskFailed, ChatRejectReason(TaskFailPlannerUnavailable)), func(e *ChatEvent) { e.Speech = "台词" }),
		mutateTaskEvent(taskChatEvent(1, ChatEventTaskStopped, ChatRejectNone), func(e *ChatEvent) { e.Speech = "台词" }),
		mutateTaskEvent(taskChatEvent(1, ChatEventRejected, ChatRejectInvalidFormat), func(e *ChatEvent) { e.Speech = "台词" }),
		mutateTaskEvent(taskChatEvent(1, ChatEventRejected, ChatRejectQueueFull), func(e *ChatEvent) { e.Speech = "台词" }),
	}
	for _, message := range invalid {
		if err := message.Validate(); err == nil {
			t.Fatalf("非法台词组合被接受: %+v", message)
		}
		if event, ok := message.(ChatEvent); ok {
			if _, _, err := encodeServerControlPayload(StatePlay, event); err == nil {
				t.Fatalf("非法台词组合被编码: %+v", event)
			}
		}
	}
}

func TestChatEventCompanionSpeechDecoderRejectsInvalidWire(t *testing.T) {
	// 从合法台词 wire 出发做定向突变：未知 kind、非 None reason 与台词文本纪律
	// 违例都必须在解码层整体拒绝，不得产生部分应用的事件。
	event := companionSpeechEvent(1, "台词")
	_, wire, err := encodeServerControlPayload(StatePlay, event)
	if err != nil {
		t.Fatal(err)
	}
	kindOffset := chatEventKindOffset(event)
	for _, mutation := range []struct {
		name   string
		mutate func([]byte)
	}{
		{"unknown kind", func(p []byte) { p[kindOffset] = 10 }},
		{"reject reason", func(p []byte) { p[kindOffset+1] = byte(ChatRejectQueueFull) }},
		{"fail reason", func(p []byte) { p[kindOffset+1] = byte(TaskFailInventoryFull) }},
		{"speech NUL", func(p []byte) { p[kindOffset+3] = 0 }},
		{"speech control", func(p []byte) { p[kindOffset+3] = '\n' }},
		{"leading space", func(p []byte) { p[kindOffset+3] = ' ' }},
		{"trailing space", func(p []byte) { p[kindOffset+8] = ' ' }},
	} {
		payload := append([]byte(nil), wire...)
		mutation.mutate(payload)
		if packet, err := decodeServerControlPayload(StatePlay, 16, payload); err == nil || packet != nil {
			t.Fatalf("%s wire 解码为 %#v, %v", mutation.name, packet, err)
		}
	}

	// 编码器无法产出非法台词，wire 层的长度边界需要手工构造：
	// 空台词与 257/300-byte 台词槽位都必须被解码器拒绝。
	for _, speech := range []string{"", strings.Repeat("x", 257), strings.Repeat("x", 300)} {
		var encoder byteEncoder
		encoder.u64(1)
		encoder.data = append(encoder.data, event.PlayerID[:]...)
		encoder.string(event.PlayerName, 128)
		encoder.data = append(encoder.data, event.CompanionID[:]...)
		encoder.string(event.CompanionName, 128)
		encoder.u8(uint8(ChatEventCompanionSpeech))
		encoder.u8(uint8(ChatRejectNone))
		encoder.string(speech, 1024)
		if packet, err := decodeServerControlPayload(StatePlay, 16, encoder.data); err == nil || packet != nil {
			t.Fatalf("%d-byte 台词 wire 解码为 %#v, %v", len(speech), packet, err)
		}
	}
}

func TestCompanionMessageGolden(t *testing.T) {
	tests := []struct {
		name    string
		client  ClientPacket
		server  ServerPacket
		wantID  uint32
		wantHex string
	}{
		{"ChatCommand", ChatCommand{Text: "@A x"}, nil, 12, "0440412078"},
		{"ChatEvent", nil, validAcceptedChatEvent(), 16,
			"0100000000000000" + "10000000000040008000000000000009" + "044368656e" +
				"10000000000040008000000000000001" + "0141" + "0100" + "0178"},
		{"ChatEventTaskStarted", nil, taskChatEvent(2, ChatEventTaskStarted, ChatRejectNone), 16,
			"0200000000000000" + "10000000000040008000000000000009" + "044368656e" +
				"10000000000040008000000000000001" + "0141" + "0300" + "0178"},
		{"ChatEventTaskProgress", nil, taskChatEvent(3, ChatEventTaskProgress, ChatRejectNone), 16,
			"0300000000000000" + "10000000000040008000000000000009" + "044368656e" +
				"10000000000040008000000000000001" + "0141" + "0400" + "0178"},
		{"ChatEventTaskCompleted", nil, taskChatEvent(4, ChatEventTaskCompleted, ChatRejectNone), 16,
			"0400000000000000" + "10000000000040008000000000000009" + "044368656e" +
				"10000000000040008000000000000001" + "0141" + "0500" + "0178"},
		{"ChatEventTaskFailed", nil, taskChatEvent(5, ChatEventTaskFailed, ChatRejectReason(TaskFailPlannerUnavailable)), 16,
			"0500000000000000" + "10000000000040008000000000000009" + "044368656e" +
				"10000000000040008000000000000001" + "0141" + "0610" + "0178"},
		{"ChatEventTaskTimedOut", nil, taskChatEvent(6, ChatEventTaskTimedOut, ChatRejectNone), 16,
			"0600000000000000" + "10000000000040008000000000000009" + "044368656e" +
				"10000000000040008000000000000001" + "0141" + "0700" + "0178"},
		{"ChatEventQueueFull", nil, taskChatEvent(7, ChatEventRejected, ChatRejectQueueFull), 16,
			"0700000000000000" + "10000000000040008000000000000009" + "044368656e" +
				"10000000000040008000000000000001" + "0141" + "0204" + "0178"},
		{"ChatEventNotFollowing", nil, taskChatEvent(14, ChatEventRejected, ChatRejectNotFollowing), 16,
			"0e00000000000000" + "10000000000040008000000000000009" + "044368656e" +
				"10000000000040008000000000000001" + "0141" + "0205" + "0178"},
		{"ChatEventTaskStopped", nil, taskChatEvent(13, ChatEventTaskStopped, ChatRejectNone), 16,
			"0d00000000000000" + "10000000000040008000000000000009" + "044368656e" +
				"10000000000040008000000000000001" + "0141" + "0800" + "0178"},
		{"ChatEventTaskFailInventoryFull", nil, taskChatEvent(15, ChatEventTaskFailed, ChatRejectReason(TaskFailInventoryFull)), 16,
			"0f00000000000000" + "10000000000040008000000000000009" + "044368656e" +
				"10000000000040008000000000000001" + "0141" + "0614" + "0178"},
		// v19 台词事件：kind 9、reason 0，台词复用既有文本槽位（此处为 6-byte "台词"）。
		{"ChatEventCompanionSpeech", nil, companionSpeechEvent(16, "台词"), 16,
			"1000000000000000" + "10000000000040008000000000000009" + "044368656e" +
				"10000000000040008000000000000001" + "0141" + "0900" + "06e58fb0e8af8d"},
		{"CompanionSpawn", nil, CompanionSpawn{ID: testCompanionID(1), Name: "A", Tick: 1}, 17,
			"10000000000040008000000000000001" + "0141" + "0100000000000000" +
				"00000000" + "000000000000000000000000" + "0000000000000000"},
		{"CompanionStates", nil, CompanionStates{Tick: 2, States: []CompanionState{{ID: testCompanionID(1)}}}, 18,
			"0200000000000000" + "01" + "10000000000040008000000000000001" +
				"00000000" + "000000000000000000000000" + "0000000000000000" + "00"},
		{"CompanionDespawn", nil, CompanionDespawn{ID: testCompanionID(1)}, 19,
			"10000000000040008000000000000001"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var packetID uint32
			var payload []byte
			var err error
			if test.client != nil {
				packetID, payload, err = encodeClientPacketPayload(StatePlay, test.client)
			} else {
				packetID, payload, err = encodeServerControlPayload(StatePlay, test.server)
			}
			if err != nil || packetID != test.wantID || hex.EncodeToString(payload) != test.wantHex {
				t.Fatalf("id=%d payload=%x error=%v，想要 id=%d payload=%s",
					packetID, payload, err, test.wantID, test.wantHex)
			}
		})
	}
}

func TestChatCommandAccepts1024BytesAndRejects1025(t *testing.T) {
	for _, text := range []string{"@A x", strings.Repeat("x", 1024)} {
		packet := ChatCommand{Text: text}
		if err := packet.Validate(); err != nil {
			t.Fatalf("%d-byte ChatCommand 被拒绝: %v", len(text), err)
		}
		packetID, payload, err := encodeClientPacketPayload(StatePlay, packet)
		if err != nil || packetID != 12 {
			t.Fatalf("ChatCommand 编码 = (%d,%d,%v)", packetID, len(payload), err)
		}
	}
	for _, text := range []string{
		"", strings.Repeat("x", 1025), " x", "x ", "\u3000x", "x\u00a0",
		"x\x00y", "x\ny", string([]byte{0xff}),
	} {
		packet := ChatCommand{Text: text}
		if err := packet.Validate(); err == nil {
			t.Fatalf("非法 ChatCommand %q 通过 Validate", text)
		}
		if _, _, err := encodeClientPacketPayload(StatePlay, packet); err == nil {
			t.Fatalf("非法 ChatCommand %q 被编码", text)
		}
	}
	for _, payload := range [][]byte{
		{0},
		append([]byte{0x81, 0x08}, make([]byte, 1025)...),
		{1, 0xff},
		{3, 'x', 0, 'y'},
		{3, 'x', '\n', 'y'},
		{2, ' ', 'x'},
		{2, 'x', ' '},
		{4, 0xe3, 0x80, 0x80, 'x'},
		{3, 'x', 0xc2, 0xa0},
	} {
		if packet, err := decodeClientPacketPayload(StatePlay, 12, payload); err == nil || packet != nil {
			t.Fatalf("非法 ChatCommand wire 解码为 %#v, %v", packet, err)
		}
	}
}

func TestCompanionSpawnAndChatEventStringBoundaries(t *testing.T) {
	id := testCompanionID(1)
	playerID := core.PlayerID(testCompanionID(2))
	maxName := strings.Repeat("𐐀", 32)
	maxCommand := strings.Repeat("x", 1024)
	valid := []interface{ Validate() error }{
		CompanionSpawn{ID: id, Name: maxName, Dimension: core.Overworld, Pitch: float32(math.Pi / 2)},
		ChatEvent{EventID: 1, PlayerID: playerID, PlayerName: maxName, CompanionID: id,
			CompanionName: maxName, Kind: ChatEventAccepted, RejectReason: ChatRejectNone, Command: maxCommand},
		ChatEvent{EventID: 2, PlayerID: playerID, PlayerName: "Chen", Kind: ChatEventRejected,
			RejectReason: ChatRejectInvalidFormat},
		ChatEvent{EventID: 3, PlayerID: playerID, PlayerName: "Chen", CompanionName: maxName,
			Kind: ChatEventRejected, RejectReason: ChatRejectUnknownCompanion},
		ChatEvent{EventID: 4, PlayerID: playerID, PlayerName: maxName, CompanionID: id,
			CompanionName: maxName, Kind: ChatEventTaskFailed,
			RejectReason: ChatRejectReason(TaskFailWorldChanged), Command: maxCommand},
		ChatEvent{EventID: 5, PlayerID: playerID, PlayerName: maxName, CompanionID: id,
			CompanionName: maxName, Kind: ChatEventRejected,
			RejectReason: ChatRejectQueueFull, Command: maxCommand},
	}
	for _, message := range valid {
		if err := message.Validate(); err != nil {
			t.Fatalf("合法 %T 被拒绝: %v", message, err)
		}
	}

	tooManyRunes := strings.Repeat("a", 33)
	// 单个合法 UTF-8 rune 最多四字节，因此超过 128 bytes 的合法名称必然也超过 32 rune。
	exact129Bytes := strings.Repeat("𐐀", 32) + "a"
	tooManyBytes := strings.Repeat("𐐀", 33)
	if len(exact129Bytes) != 129 || utf8.RuneCountInString(exact129Bytes) != 33 {
		t.Fatalf("精确边界夹具 = %d bytes/%d runes，想要 129/33",
			len(exact129Bytes), utf8.RuneCountInString(exact129Bytes))
	}
	invalid := []interface{ Validate() error }{
		CompanionSpawn{ID: id, Name: tooManyRunes, Dimension: core.Overworld},
		CompanionSpawn{ID: id, Name: exact129Bytes, Dimension: core.Overworld},
		CompanionSpawn{ID: id, Name: tooManyBytes, Dimension: core.Overworld},
		CompanionSpawn{ID: id, Name: " A", Dimension: core.Overworld},
		CompanionSpawn{ID: id, Name: "A\n", Dimension: core.Overworld},
		CompanionSpawn{ID: id, Name: "A", Dimension: 1},
		CompanionSpawn{ID: id, Name: "A", Dimension: core.Overworld, Pitch: float32(math.Pi/2) + 0.01},
		ChatEvent{EventID: 1, PlayerID: playerID, PlayerName: tooManyRunes, Kind: ChatEventRejected, RejectReason: ChatRejectInvalidFormat},
		ChatEvent{EventID: 1, PlayerID: playerID, PlayerName: exact129Bytes, Kind: ChatEventRejected, RejectReason: ChatRejectInvalidFormat},
		ChatEvent{EventID: 1, PlayerID: playerID, PlayerName: tooManyBytes, Kind: ChatEventRejected, RejectReason: ChatRejectInvalidFormat},
		ChatEvent{EventID: 1, PlayerID: playerID, PlayerName: "Chen", CompanionID: id,
			CompanionName: "A", Kind: ChatEventAccepted, RejectReason: ChatRejectNone},
		ChatEvent{EventID: 1, PlayerID: playerID, PlayerName: "Chen", CompanionID: id,
			CompanionName: "A", Kind: ChatEventRejected, RejectReason: ChatRejectInvalidFormat},
		ChatEvent{EventID: 1, PlayerID: playerID, PlayerName: "Chen", CompanionName: "A",
			Kind: ChatEventRejected, RejectReason: ChatRejectInvalidFormat},
		ChatEvent{EventID: 1, PlayerID: playerID, PlayerName: "Chen", CompanionName: "A", Command: "x",
			Kind: ChatEventRejected, RejectReason: ChatRejectUnknownCompanion},
		ChatEvent{EventID: 1, PlayerID: playerID, PlayerName: "Chen", CompanionName: "A",
			Kind: ChatEventAccepted, RejectReason: ChatRejectNone, Command: "x"},
		ChatEvent{EventID: 1, PlayerID: playerID, PlayerName: "Chen", CompanionID: id,
			CompanionName: "A", Kind: ChatEventAccepted, RejectReason: ChatRejectInvalidFormat, Command: "x"},
		ChatEvent{EventID: 1, PlayerID: playerID, PlayerName: "Chen", CompanionID: id,
			CompanionName: "A", Kind: ChatEventKind(10), RejectReason: ChatRejectNone, Command: "x"},
	}
	for _, message := range invalid {
		if err := message.Validate(); err == nil {
			t.Fatalf("非法 %T 被接受: %+v", message, message)
		}
	}

	var oversizedNameEvent byteEncoder
	oversizedNameEvent.u64(1)
	oversizedNameEvent.data = append(oversizedNameEvent.data, playerID[:]...)
	oversizedNameEvent.string(exact129Bytes, 129)
	oversizedNameEvent.data = append(oversizedNameEvent.data, make([]byte, len(id))...)
	oversizedNameEvent.string("", 128)
	oversizedNameEvent.u8(uint8(ChatEventRejected))
	oversizedNameEvent.u8(uint8(ChatRejectInvalidFormat))
	oversizedNameEvent.string("", 1024)
	if packet, err := decodeServerControlPayload(StatePlay, 16, oversizedNameEvent.data); err == nil || packet != nil {
		t.Fatalf("129-byte player name wire 解码为 %#v, %v", packet, err)
	}
}

func TestCompanionPitchUsesRadiansInValidateAndDecode(t *testing.T) {
	id := testCompanionID(1)
	halfPi := float32(math.Pi / 2)
	aboveHalfPi := math.Nextafter32(halfPi, float32(math.Inf(1)))
	belowNegativeHalfPi := math.Nextafter32(-halfPi, float32(math.Inf(-1)))

	for _, pitch := range []float32{-halfPi, halfPi} {
		if err := (CompanionSpawn{ID: id, Name: "A", Dimension: core.Overworld, Pitch: pitch}).Validate(); err != nil {
			t.Fatalf("合法 Spawn pitch %v 被拒绝: %v", pitch, err)
		}
		if err := (CompanionStates{States: []CompanionState{{
			ID: id, Dimension: core.Overworld, Pitch: pitch,
		}}}).Validate(); err != nil {
			t.Fatalf("合法 States pitch %v 被拒绝: %v", pitch, err)
		}
	}

	for _, message := range []interface{ Validate() error }{
		CompanionSpawn{ID: id, Name: "A", Dimension: core.Overworld, Pitch: aboveHalfPi},
		CompanionSpawn{ID: id, Name: "A", Dimension: core.Overworld, Pitch: belowNegativeHalfPi},
		CompanionStates{States: []CompanionState{{ID: id, Dimension: core.Overworld, Pitch: aboveHalfPi}}},
		CompanionStates{States: []CompanionState{{ID: id, Dimension: core.Overworld, Pitch: belowNegativeHalfPi}}},
	} {
		if err := message.Validate(); err == nil {
			t.Fatalf("非法 %T pitch 通过 Validate: %+v", message, message)
		}
	}

	_, spawnWire, err := encodeServerControlPayload(StatePlay, CompanionSpawn{
		ID: id, Name: "A", Dimension: core.Overworld, Pitch: halfPi,
	})
	if err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(spawnWire[len(spawnWire)-4:], math.Float32bits(aboveHalfPi))
	if packet, err := decodeServerControlPayload(StatePlay, 17, spawnWire); err == nil || packet != nil {
		t.Fatalf("非法 Spawn pitch raw wire 解码为 %#v, %v", packet, err)
	}

	statesWire := companionStatesWireFixture(id)
	binary.LittleEndian.PutUint32(statesWire[len(statesWire)-5:len(statesWire)-1], math.Float32bits(belowNegativeHalfPi))
	if packet, err := decodeServerControlPayload(StatePlay, 18, statesWire); err == nil || packet != nil {
		t.Fatalf("非法 States pitch raw wire 解码为 %#v, %v", packet, err)
	}
}

func TestCompanionMessagesHaveFixedMaximumWireLengths(t *testing.T) {
	id := testCompanionID(1)
	maxName := strings.Repeat("𐐀", 32)
	maxCommand := strings.Repeat("x", 1024)
	states := make([]CompanionState, 4)
	for index := range states {
		states[index] = CompanionState{ID: testCompanionID(byte(index + 1)), Dimension: core.Overworld}
	}
	tests := []struct {
		name string
		want int
		call func() ([]byte, error)
	}{
		{"ChatCommand", 1026, func() ([]byte, error) {
			_, payload, err := encodeClientPacketPayload(StatePlay, ChatCommand{Text: maxCommand})
			return payload, err
		}},
		{"CompanionSpawn", 178, func() ([]byte, error) {
			_, payload, err := encodeServerControlPayload(StatePlay, CompanionSpawn{ID: id, Name: maxName, Dimension: core.Overworld})
			return payload, err
		}},
		{"CompanionStates", 173, func() ([]byte, error) {
			_, payload, err := encodeServerControlPayload(StatePlay, CompanionStates{States: states})
			return payload, err
		}},
		{"ChatEvent", 1328, func() ([]byte, error) {
			_, payload, err := encodeServerControlPayload(StatePlay, ChatEvent{EventID: 1,
				PlayerID: core.PlayerID(testCompanionID(9)), PlayerName: maxName, CompanionID: id,
				CompanionName: maxName, Kind: ChatEventAccepted, RejectReason: ChatRejectNone, Command: maxCommand})
			return payload, err
		}},
		// 任务事件复用同一 wire 形状，固定上限仍是 1328 bytes。
		{"ChatEventTaskFailed", 1328, func() ([]byte, error) {
			_, payload, err := encodeServerControlPayload(StatePlay, ChatEvent{EventID: 1,
				PlayerID: core.PlayerID(testCompanionID(9)), PlayerName: maxName, CompanionID: id,
				CompanionName: maxName, Kind: ChatEventTaskFailed,
				RejectReason: ChatRejectReason(TaskFailPlannerUnavailable), Command: maxCommand})
			return payload, err
		}},
		// v18 新增的 TaskStopped 与 NotFollowing 同样复用既有 wire 形状，不改变上限。
		{"ChatEventTaskStopped", 1328, func() ([]byte, error) {
			_, payload, err := encodeServerControlPayload(StatePlay, ChatEvent{EventID: 1,
				PlayerID: core.PlayerID(testCompanionID(9)), PlayerName: maxName, CompanionID: id,
				CompanionName: maxName, Kind: ChatEventTaskStopped,
				RejectReason: ChatRejectNone, Command: maxCommand})
			return payload, err
		}},
		{"ChatEventNotFollowing", 1328, func() ([]byte, error) {
			_, payload, err := encodeServerControlPayload(StatePlay, ChatEvent{EventID: 1,
				PlayerID: core.PlayerID(testCompanionID(9)), PlayerName: maxName, CompanionID: id,
				CompanionName: maxName, Kind: ChatEventRejected,
				RejectReason: ChatRejectNotFollowing, Command: maxCommand})
			return payload, err
		}},
		// v19 的 CompanionSpeech 复用既有文本槽位：256-byte 台词 + 最长身份的事件
		// 只有 560 bytes，固定上限 1328 bytes 不变。
		{"ChatEventCompanionSpeech", 560, func() ([]byte, error) {
			_, payload, err := encodeServerControlPayload(StatePlay, ChatEvent{EventID: 1,
				PlayerID: core.PlayerID(testCompanionID(9)), PlayerName: maxName, CompanionID: id,
				CompanionName: maxName, Kind: ChatEventCompanionSpeech,
				RejectReason: ChatRejectNone, Speech: strings.Repeat("x", 256)})
			return payload, err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload, err := test.call()
			if err != nil || len(payload) != test.want {
				t.Fatalf("wire length=%d error=%v, 想要 %d", len(payload), err, test.want)
			}
		})
	}
	// 既有指令文本边界不回归：1022/1023/1024-byte 指令对应 1326/1327/1328-byte wire，
	// 1328 仍是 ChatEvent 的固定上限。
	for _, boundary := range []struct {
		commandBytes int
		wantWire     int
	}{
		{1022, 1326},
		{1023, 1327},
		{1024, 1328},
	} {
		_, payload, err := encodeServerControlPayload(StatePlay, ChatEvent{EventID: 1,
			PlayerID: core.PlayerID(testCompanionID(9)), PlayerName: maxName, CompanionID: id,
			CompanionName: maxName, Kind: ChatEventAccepted, RejectReason: ChatRejectNone,
			Command: strings.Repeat("x", boundary.commandBytes)})
		if err != nil || len(payload) != boundary.wantWire {
			t.Fatalf("%d-byte 指令事件 wire length=%d error=%v, 想要 %d",
				boundary.commandBytes, len(payload), err, boundary.wantWire)
		}
	}
	if packet, err := decodeClientPacketPayload(StatePlay, 12, make([]byte, 1027)); err == nil || packet != nil {
		t.Fatalf("超长 ChatCommand 解码为 %#v, %v", packet, err)
	}
	for id, length := range map[uint32]int{16: 1329, 17: 179, 18: 174, 19: 17} {
		if packet, err := decodeServerControlPayload(StatePlay, id, make([]byte, length)); err == nil || packet != nil {
			t.Fatalf("超长 server ID %d 解码为 %#v, %v", id, packet, err)
		}
	}
}

func TestCompanionStatesRejectsFiveDuplicateOrUnsortedAtomically(t *testing.T) {
	five := make([]CompanionState, 5)
	for index := range five {
		five[index] = CompanionState{ID: testCompanionID(byte(index + 1)), Dimension: core.Overworld}
	}
	if err := (CompanionStates{States: five}).Validate(); err == nil {
		t.Fatal("五项 states 通过 Validate")
	}
	if _, _, err := encodeServerControlPayload(StatePlay, CompanionStates{States: five}); err == nil {
		t.Fatal("五项 states 被编码")
	}
	for _, test := range []struct {
		name string
		ids  []companion.ID
	}{
		{"empty", nil},
		{"five", []companion.ID{testCompanionID(1), testCompanionID(2), testCompanionID(3), testCompanionID(4), testCompanionID(5)}},
		{"duplicate", []companion.ID{testCompanionID(1), testCompanionID(1)}},
		{"unsorted", []companion.ID{testCompanionID(2), testCompanionID(1)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			packet, err := decodeServerControlPayload(StatePlay, 18, companionStatesWireFixture(test.ids...))
			if err == nil || packet != nil {
				t.Fatalf("非法 states 解码为 %#v, %v", packet, err)
			}
		})
	}
	declaredHuge := append(make([]byte, 8), 0xff, 0xff, 0xff, 0xff, 0x0f)
	if packet, err := decodeServerControlPayload(StatePlay, 18, declaredHuge); err == nil || packet != nil {
		t.Fatalf("巨大 count 解码为 %#v, %v", packet, err)
	}
}

func TestCompanionDecoderRejectsInvalidIDsEnumsNumbersAndDimensions(t *testing.T) {
	spawn := CompanionSpawn{ID: testCompanionID(1), Name: "A", Tick: 1, Dimension: core.Overworld,
		Position: mgl32.Vec3{1, 2, 3}, Yaw: 4, Pitch: 0.5}
	_, spawnWire, err := encodeServerControlPayload(StatePlay, spawn)
	if err != nil {
		t.Fatal(err)
	}
	mutations := [][]byte{
		append([]byte(nil), spawnWire...),
		append([]byte(nil), spawnWire...),
		append([]byte(nil), spawnWire...),
		append([]byte(nil), spawnWire...),
	}
	clear(mutations[0][:16])
	binary.LittleEndian.PutUint32(mutations[1][26:30], 1)
	binary.LittleEndian.PutUint32(mutations[2][len(spawnWire)-4:], math.Float32bits(91))
	binary.LittleEndian.PutUint32(mutations[3][len(spawnWire)-8:len(spawnWire)-4], 0x7fc00000)
	for index, payload := range mutations {
		if packet, err := decodeServerControlPayload(StatePlay, 17, payload); err == nil || packet != nil {
			t.Fatalf("spawn mutation %d 解码为 %#v, %v", index, packet, err)
		}
	}

	accepted := validAcceptedChatEvent()
	_, eventWire, err := encodeServerControlPayload(StatePlay, accepted)
	if err != nil {
		t.Fatal(err)
	}
	kindOffset := chatEventKindOffset(accepted)
	for _, mutation := range []func([]byte){
		func(payload []byte) { payload[kindOffset] = 10 },
		func(payload []byte) { payload[kindOffset+1] = byte(ChatRejectInvalidFormat) },
		func(payload []byte) { clear(payload[8 : 8+16]) },
	} {
		payload := append([]byte(nil), eventWire...)
		mutation(payload)
		if packet, err := decodeServerControlPayload(StatePlay, 16, payload); err == nil || packet != nil {
			t.Fatalf("ChatEvent mutation 解码为 %#v, %v", packet, err)
		}
	}

	stateWire := companionStatesWireFixture(testCompanionID(1))
	stateOffset := 9
	for _, mutation := range []func([]byte){
		func(payload []byte) { clear(payload[stateOffset : stateOffset+16]) },
		func(payload []byte) { binary.LittleEndian.PutUint32(payload[stateOffset+16:stateOffset+20], 1) },
		func(payload []byte) {
			binary.LittleEndian.PutUint32(payload[len(payload)-5:len(payload)-1], math.Float32bits(-91))
		},
		func(payload []byte) { payload[len(payload)-1] = 2 },
	} {
		payload := append([]byte(nil), stateWire...)
		mutation(payload)
		if packet, err := decodeServerControlPayload(StatePlay, 18, payload); err == nil || packet != nil {
			t.Fatalf("CompanionStates mutation 解码为 %#v, %v", packet, err)
		}
	}
	if packet, err := decodeServerControlPayload(StatePlay, 19, make([]byte, 16)); err == nil || packet != nil {
		t.Fatalf("零 CompanionDespawn ID 解码为 %#v, %v", packet, err)
	}
}

func TestCompanionMessagesMatchMemoryAndTCP(t *testing.T) {
	clientMessage := ChatCommand{Text: "@A x"}
	serverMessages := []ServerPacket{
		validAcceptedChatEvent(),
		CompanionSpawn{ID: testCompanionID(1), Name: "A", Tick: 1, Dimension: core.Overworld},
		CompanionStates{Tick: 2, States: []CompanionState{{ID: testCompanionID(1), Dimension: core.Overworld}}},
		CompanionDespawn{ID: testCompanionID(1)},
	}
	for _, open := range transportOpeners {
		t.Run(open.name, func(t *testing.T) {
			client, server := open.open(t)
			t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
			if err := client.Send(context.Background(), StatePlay, clientMessage); err != nil {
				t.Fatal(err)
			}
			gotClient, err := server.Recv(context.Background(), StatePlay)
			if err != nil || !reflect.DeepEqual(gotClient, clientMessage) {
				t.Fatalf("client message = (%#v,%v)", gotClient, err)
			}
			for _, message := range serverMessages {
				if err := server.Send(context.Background(), StatePlay, message); err != nil {
					t.Fatal(err)
				}
				got, err := client.Recv(context.Background(), StatePlay)
				if err != nil || !reflect.DeepEqual(got, message) {
					t.Fatalf("server message = (%#v,%v), 想要 %#v", got, err, message)
				}
			}
		})
	}
}

func TestCompanionStatesFiveRejectedByMemoryAndTCP(t *testing.T) {
	five := make([]CompanionState, 5)
	for index := range five {
		five[index] = CompanionState{ID: testCompanionID(byte(index + 1)), Dimension: core.Overworld}
	}
	for _, open := range transportOpeners {
		t.Run(open.name, func(t *testing.T) {
			client, server := open.open(t)
			t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
			if err := server.Send(context.Background(), StatePlay, CompanionStates{States: five}); err == nil {
				t.Fatal("五项 CompanionStates 被 transport 接受")
			}
		})
	}
}

func validAcceptedChatEvent() ChatEvent {
	return ChatEvent{
		EventID: 1, PlayerID: core.PlayerID(testCompanionID(9)), PlayerName: "Chen",
		CompanionID: testCompanionID(1), CompanionName: "A",
		Kind: ChatEventAccepted, RejectReason: ChatRejectNone, Command: "x",
	}
}

// chatEventKindOffset 返回 ChatEvent wire 载荷中 kind 字节的偏移。头部布局与
// wire 编码一致：8 bytes 前缀（event ID）+ 16 玩家 ID + 1 名称长度 + 玩家名 +
// 16 伙伴 ID + 1 名称长度 + 伙伴名，随后即是 1 byte kind（再后 1 byte 是
// reason 槽位）。wire 突变测试据此定位 kind 与 reason 字节。
func chatEventKindOffset(event ChatEvent) int {
	return 8 + 16 + 1 + len(event.PlayerName) + 16 + 1 + len(event.CompanionName)
}

// taskChatEvent 构造一条携带完整伙伴身份与原始指令的任务生命周期事件；
// reason 槽位同时承载拒绝原因与 TaskFailReason。
func taskChatEvent(id uint64, kind ChatEventKind, reason ChatRejectReason) ChatEvent {
	return ChatEvent{
		EventID: id, PlayerID: core.PlayerID(testCompanionID(9)), PlayerName: "Chen",
		CompanionID: testCompanionID(1), CompanionName: "A",
		Kind: kind, RejectReason: reason, Command: "x",
	}
}

// companionSpeechEvent 构造一条 v19 伙伴台词事件：携带完整玩家与伙伴身份、
// 合法台词与 None reason，且不复述玩家指令。
func companionSpeechEvent(id uint64, speech string) ChatEvent {
	return ChatEvent{
		EventID: id, PlayerID: core.PlayerID(testCompanionID(9)), PlayerName: "Chen",
		CompanionID: testCompanionID(1), CompanionName: "A",
		Kind: ChatEventCompanionSpeech, RejectReason: ChatRejectNone, Speech: speech,
	}
}

// mutateTaskEvent 返回应用单字段突变后的任务事件副本，用于原子拒绝用例。
func mutateTaskEvent(event ChatEvent, mutate func(*ChatEvent)) ChatEvent {
	mutate(&event)
	return event
}

func testCompanionID(last byte) companion.ID {
	return companion.ID{0: 0x10, 6: 0x40, 8: 0x80, 15: last}
}

func companionStatesWireFixture(ids ...companion.ID) []byte {
	var encoder byteEncoder
	encoder.u64(1)
	encoder.uvarint(uint32(len(ids)))
	for _, id := range ids {
		encoder.data = append(encoder.data, id[:]...)
		encoder.i32(int32(core.Overworld))
		for range 3 {
			encoder.f32(0)
		}
		encoder.f32(0)
		encoder.f32(0)
		encoder.bool(false)
	}
	return encoder.data
}
