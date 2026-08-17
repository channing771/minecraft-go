package client_test

import (
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
)

func TestCompanionsSpawnStatesInterpolateDespawnAndReset(t *testing.T) {
	var companions client.Companions
	spawn := companionSpawn(1, 10, "阿木", mgl32.Vec3{})
	if err := companions.ApplySpawn(spawn); err != nil {
		t.Fatal(err)
	}
	for tick := uint64(11); tick <= 13; tick++ {
		if err := companions.ApplyStates(network.CompanionStates{
			Tick: tick,
			States: []network.CompanionState{{
				ID: spawn.ID, Dimension: core.Overworld,
				Position: mgl32.Vec3{float32(tick - 10), 0, 0},
			}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	companions.Advance(25 * time.Millisecond)
	got := companions.AppendPresentations(nil)
	if len(got) != 1 || got[0].Position != (mgl32.Vec3{1.5, 0, 0}) {
		t.Fatalf("interpolated presentations = %+v", got)
	}
	if err := companions.ApplyDespawn(network.CompanionDespawn{ID: spawn.ID}); err != nil {
		t.Fatal(err)
	}
	if got := companions.AppendPresentations(nil); len(got) != 0 {
		t.Fatalf("despawn left presentations: %+v", got)
	}
	if err := companions.ApplySpawn(spawn); err != nil {
		t.Fatal(err)
	}
	companions.Reset()
	if got := companions.AppendPresentations(nil); len(got) != 0 {
		t.Fatalf("Reset left presentations: %+v", got)
	}
}

func TestCompanionsRejectDuplicateUnknownStaleAndFiveAtomically(t *testing.T) {
	var companions client.Companions
	first := companionSpawn(1, 10, "甲", mgl32.Vec3{1, 0, 0})
	second := companionSpawn(2, 10, "乙", mgl32.Vec3{2, 0, 0})
	for _, spawn := range []network.CompanionSpawn{first, second} {
		if err := companions.ApplySpawn(spawn); err != nil {
			t.Fatal(err)
		}
	}
	want := companions.AppendPresentations(nil)
	invalidSpawn := first
	invalidSpawn.ID = companion.ID{}
	if err := companions.ApplySpawn(invalidSpawn); !errors.Is(err, client.ErrCompanionProtocol) {
		t.Fatalf("invalid spawn error = %v", err)
	}
	if err := companions.ApplySpawn(first); !errors.Is(err, client.ErrCompanionProtocol) {
		t.Fatalf("duplicate spawn error = %v", err)
	}
	unknown := network.CompanionStates{Tick: 11, States: []network.CompanionState{
		{ID: first.ID, Dimension: core.Overworld, Position: mgl32.Vec3{10, 0, 0}},
		{ID: companionTestID(3), Dimension: core.Overworld, Position: mgl32.Vec3{30, 0, 0}},
	}}
	if err := companions.ApplyStates(unknown); !errors.Is(err, client.ErrCompanionProtocol) {
		t.Fatalf("unknown state error = %v", err)
	}
	stale := network.CompanionStates{Tick: 10, States: []network.CompanionState{{
		ID: first.ID, Dimension: core.Overworld, Position: mgl32.Vec3{11, 0, 0},
	}}}
	if err := companions.ApplyStates(stale); !errors.Is(err, client.ErrCompanionProtocol) {
		t.Fatalf("stale state error = %v", err)
	}
	five := network.CompanionStates{Tick: 11}
	for last := byte(1); last <= 5; last++ {
		five.States = append(five.States, network.CompanionState{
			ID: companionTestID(last), Dimension: core.Overworld,
		})
	}
	if err := companions.ApplyStates(five); !errors.Is(err, client.ErrCompanionProtocol) {
		t.Fatalf("five-state error = %v", err)
	}
	if err := companions.ApplyDespawn(network.CompanionDespawn{ID: companionTestID(3)}); !errors.Is(err, client.ErrCompanionProtocol) {
		t.Fatalf("unknown despawn error = %v", err)
	}
	if err := companions.ApplyDespawn(network.CompanionDespawn{}); !errors.Is(err, client.ErrCompanionProtocol) {
		t.Fatalf("invalid despawn error = %v", err)
	}
	if got := companions.AppendPresentations(nil); !reflect.DeepEqual(got, want) {
		t.Fatalf("rejected messages mutated companions: got %+v want %+v", got, want)
	}
}

func TestCompanionsRejectStateAtSpawnTickAtomically(t *testing.T) {
	var companions client.Companions
	spawn := companionSpawn(1, 7, "阿木", mgl32.Vec3{1, 0, 0})
	if err := companions.ApplySpawn(spawn); err != nil {
		t.Fatal(err)
	}
	want := companions.AppendPresentations(nil)
	err := companions.ApplyStates(network.CompanionStates{Tick: spawn.Tick, States: []network.CompanionState{{
		ID: spawn.ID, Dimension: core.Overworld, Position: mgl32.Vec3{9, 0, 0},
	}}})
	if !errors.Is(err, client.ErrCompanionProtocol) {
		t.Fatalf("same-tick state error = %v", err)
	}
	if got := companions.AppendPresentations(nil); !reflect.DeepEqual(got, want) {
		t.Fatalf("same-tick batch mutated companions: got %+v want %+v", got, want)
	}
}

func TestCompanionsPresentInIDOrder(t *testing.T) {
	var companions client.Companions
	for _, last := range []byte{4, 1, 3, 2} {
		if err := companions.ApplySpawn(companionSpawn(last, 1, string(rune('A'+last)), mgl32.Vec3{float32(last), 0, 0})); err != nil {
			t.Fatal(err)
		}
	}
	dst := make([]client.CompanionPresentation, 0, companion.MaxActive)
	dst = companions.AppendPresentations(dst)
	for index := range dst {
		if dst[index].ID != companionTestID(byte(index+1)) {
			t.Fatalf("presentation order = %+v", dst)
		}
	}
}

func TestChatEventsKeepLatestThirtyTwoInEventOrder(t *testing.T) {
	var events client.ChatEvents
	for eventID := uint64(1); eventID <= 40; eventID++ {
		if err := events.Apply(chatEvent(eventID)); err != nil {
			t.Fatal(err)
		}
	}
	dst := make([]network.ChatEvent, 0, 32)
	dst = events.Events(dst)
	if len(dst) != 32 || dst[0].EventID != 9 || dst[31].EventID != 40 {
		t.Fatalf("events = %+v", dst)
	}
}

func TestChatEventsRejectDuplicateOrStaleWithoutMutation(t *testing.T) {
	var events client.ChatEvents
	for _, eventID := range []uint64{5, 8} {
		if err := events.Apply(chatEvent(eventID)); err != nil {
			t.Fatal(err)
		}
	}
	want := events.Events(nil)
	for _, eventID := range []uint64{8, 7} {
		if err := events.Apply(chatEvent(eventID)); !errors.Is(err, client.ErrChatEventProtocol) {
			t.Fatalf("Apply event %d error = %v", eventID, err)
		}
	}
	if got := events.Events(nil); !reflect.DeepEqual(got, want) {
		t.Fatalf("stale event mutated ring: got %+v want %+v", got, want)
	}
}

// TestCompanionInterpolationBetweenBatchesStaysWithinAuthority 锁定移动伙伴在
// 连续状态批次之间的插值语义：预热完成后每个"批次 + 帧"呈现位置都精确落在
// 两个权威位置之间（滞后 2 tick、帧推进 25 ms 的同机制采样），批次流停止后
// 呈现位置收敛到最新权威位置之前一格并保持，绝不外推越过最新权威位置。
func TestCompanionInterpolationBetweenBatchesStaysWithinAuthority(t *testing.T) {
	var companions client.Companions
	spawn := companionSpawn(1, 10, "阿木", mgl32.Vec3{0, 0, 0})
	if err := companions.ApplySpawn(spawn); err != nil {
		t.Fatal(err)
	}
	// 预热到 4 个快照，与远端玩家一致的 warm-up（不足 3 个快照时贴最新值）。
	for tick := uint64(11); tick <= 13; tick++ {
		applyCompanionState(t, &companions, spawn.ID, tick, mgl32.Vec3{float32(tick - 10), 0, 0})
	}
	// 模拟帧循环：每个 tick 一批状态到达后推进一帧（app.frame 先 drain 后 Advance）。
	for tick := uint64(14); tick <= 20; tick++ {
		applyCompanionState(t, &companions, spawn.ID, tick, mgl32.Vec3{float32(tick - 10), 0, 0})
		companions.Advance(25 * time.Millisecond)
		got := onlyCompanionPresentation(t, &companions)
		// target = latest + 25ms*20tps - 2 = latest - 1.5，落在 T-2 与 T-1 两个权威位置之间。
		want := float32(tick) - 11.5
		if math.Abs(float64(got.Position.X()-want)) > 1e-5 {
			t.Fatalf("tick %d 插值位置 = %v，want %v", tick, got.Position.X(), want)
		}
		if got.Position.X() > float32(tick-10)+1e-5 {
			t.Fatalf("tick %d 呈现越过最新权威位置：%v > %d", tick, got.Position.X(), tick-10)
		}
	}
	// 批次流停止：无论帧推进多久（含远超一个 tick 的巨帧，elapsed 被钳制），
	// 呈现位置都停在最新权威位置之前一格，不外推、不回退。
	for range 4 {
		companions.Advance(25 * time.Millisecond)
	}
	assertCompanionPositionX(t, &companions, 9)
	companions.Advance(time.Hour)
	assertCompanionPositionX(t, &companions, 9)
}

// TestCompanionInterpolationSwitchesIdleMoveIdleWithoutJumpOrGhost 锁定静止与
// 移动相互切换的连续性：每次切换后的帧间位移不超过每 tick 权威位移，呈现
// 始终位于已见权威位置的包络内，无跳变、无残影、无倒退。
func TestCompanionInterpolationSwitchesIdleMoveIdleWithoutJumpOrGhost(t *testing.T) {
	var companions client.Companions
	spawn := companionSpawn(1, 10, "阿木", mgl32.Vec3{5, 0, 0})
	if err := companions.ApplySpawn(spawn); err != nil {
		t.Fatal(err)
	}
	// 静止四拍：位置恒为 5。
	for tick := uint64(11); tick <= 13; tick++ {
		applyCompanionState(t, &companions, spawn.ID, tick, mgl32.Vec3{5, 0, 0})
	}
	type frame struct {
		tick      uint64
		positionX float32
	}
	var frames []frame
	record := func(tick uint64) {
		t.Helper()
		companions.Advance(25 * time.Millisecond)
		frames = append(frames, frame{tick: tick, positionX: onlyCompanionPresentation(t, &companions).Position.X()})
	}
	// 静止期两帧。
	record(13)
	record(13)
	// 移动期：tick 14..18 每拍前进 1 格（低于 8 格 reset 阈值，走插值）。
	for tick := uint64(14); tick <= 18; tick++ {
		applyCompanionState(t, &companions, spawn.ID, tick, mgl32.Vec3{5 + float32(tick-13), 0, 0})
		record(tick)
	}
	// 回到静止：位置恒为 10。
	for tick := uint64(19); tick <= 20; tick++ {
		applyCompanionState(t, &companions, spawn.ID, tick, mgl32.Vec3{10, 0, 0})
		record(tick)
	}
	want := []float32{5, 5, 5, 5.5, 6.5, 7.5, 8.5, 9.5, 10}
	if len(frames) != len(want) {
		t.Fatalf("帧数 = %d，want %d（frames=%+v）", len(frames), len(want), frames)
	}
	previous := float32(5)
	for index, got := range frames {
		if math.Abs(float64(got.positionX-want[index])) > 1e-5 {
			t.Fatalf("帧 %d（tick %d）位置 = %v，want %v（全部帧=%+v）",
				index, got.tick, got.positionX, want[index], frames)
		}
		if got.positionX < previous-1e-5 {
			t.Fatalf("帧 %d（tick %d）位置 %v 倒退到 %v 之下", index, got.tick, got.positionX, previous)
		}
		if jump := math.Abs(float64(got.positionX - previous)); jump > 1+1e-5 {
			t.Fatalf("帧 %d（tick %d）跳变 %v 格（%v → %v），超过每 tick 权威位移",
				index, got.tick, jump, previous, got.positionX)
		}
		if got.positionX < 5-1e-5 || got.positionX > 10+1e-5 {
			t.Fatalf("帧 %d（tick %d）位置 %v 越出权威包络 [5,10]", index, got.tick, got.positionX)
		}
		previous = got.positionX
	}
}

// TestCompanionInterpolationInvalidBatchesKeepStateAtomically 锁定非法状态批次
// （未知 ID、过时 tick、超 4 项、ID 乱序、非法位姿）整批拒绝后插值状态不变：
// 呈现不变、lastTick 不前移，后续合法批次仍在原轨迹上继续插值。
func TestCompanionInterpolationInvalidBatchesKeepStateAtomically(t *testing.T) {
	var companions client.Companions
	spawn := companionSpawn(1, 10, "阿木", mgl32.Vec3{0, 0, 0})
	if err := companions.ApplySpawn(spawn); err != nil {
		t.Fatal(err)
	}
	for tick := uint64(11); tick <= 13; tick++ {
		applyCompanionState(t, &companions, spawn.ID, tick, mgl32.Vec3{float32(tick - 10), 0, 0})
	}
	want := companions.AppendPresentations(nil)
	invalid := []network.CompanionStates{
		// 混入未知伙伴 ID。
		{Tick: 14, States: []network.CompanionState{
			{ID: spawn.ID, Dimension: core.Overworld, Position: mgl32.Vec3{30, 0, 0}},
			{ID: companionTestID(3), Dimension: core.Overworld, Position: mgl32.Vec3{40, 0, 0}},
		}},
		// tick 不新于 lastTick（等于 13）。
		{Tick: 13, States: []network.CompanionState{
			{ID: spawn.ID, Dimension: core.Overworld, Position: mgl32.Vec3{13, 0, 0}},
		}},
		// 批次超过 4 项。
		{Tick: 14, States: []network.CompanionState{
			{ID: companionTestID(1), Dimension: core.Overworld},
			{ID: companionTestID(2), Dimension: core.Overworld},
			{ID: companionTestID(3), Dimension: core.Overworld},
			{ID: companionTestID(4), Dimension: core.Overworld},
			{ID: companionTestID(5), Dimension: core.Overworld},
		}},
		// ID 乱序（重复）。
		{Tick: 14, States: []network.CompanionState{
			{ID: spawn.ID, Dimension: core.Overworld, Position: mgl32.Vec3{14, 0, 0}},
			{ID: spawn.ID, Dimension: core.Overworld, Position: mgl32.Vec3{15, 0, 0}},
		}},
		// 非法位姿（NaN 位置）。
		{Tick: 14, States: []network.CompanionState{
			{ID: spawn.ID, Dimension: core.Overworld, Position: mgl32.Vec3{float32(math.NaN()), 0, 0}},
		}},
	}
	for index, batch := range invalid {
		if err := companions.ApplyStates(batch); !errors.Is(err, client.ErrCompanionProtocol) {
			t.Fatalf("非法批次 %d error = %v，want ErrCompanionProtocol", index, err)
		}
		if got := companions.AppendPresentations(nil); !reflect.DeepEqual(got, want) {
			t.Fatalf("非法批次 %d 改动呈现：got %+v want %+v", index, got, want)
		}
	}
	// lastTick 未被非法批次前移：tick 14 仍可接受，且插值落在原轨迹上
	//（ring 11..14 → 位置 1..4，target 12.5 → 2.5）。
	applyCompanionState(t, &companions, spawn.ID, 14, mgl32.Vec3{4, 0, 0})
	companions.Advance(25 * time.Millisecond)
	assertCompanionPositionX(t, &companions, 2.5)
}

// TestCompanionInterpolationResetClearsHistoryAndRestartsFromSpawn 锁定断线清理：
// 插值进行中的伙伴被 Reset 后镜像与插值历史一并清空，重新 Spawn 从新权威位置
// 冷启动（重新走 warm-up），不会残留任何断线前的快照。
func TestCompanionInterpolationResetClearsHistoryAndRestartsFromSpawn(t *testing.T) {
	var companions client.Companions
	spawn := companionSpawn(1, 10, "阿木", mgl32.Vec3{0, 0, 0})
	if err := companions.ApplySpawn(spawn); err != nil {
		t.Fatal(err)
	}
	for tick := uint64(11); tick <= 13; tick++ {
		applyCompanionState(t, &companions, spawn.ID, tick, mgl32.Vec3{float32(tick - 10), 0, 0})
	}
	companions.Advance(25 * time.Millisecond)
	assertCompanionPositionX(t, &companions, 1.5)

	companions.Reset()
	if got := companions.AppendPresentations(nil); len(got) != 0 {
		t.Fatalf("Reset 后残留呈现：%+v", got)
	}
	respawn := companionSpawn(1, 100, "阿木", mgl32.Vec3{50, 0, 0})
	if err := companions.ApplySpawn(respawn); err != nil {
		t.Fatal(err)
	}
	assertCompanionPositionX(t, &companions, 50)
	// 只有两个快照时贴最新值：若断线前的快照泄入 ring，计数会 ≥3 并产生旧轨迹插值。
	applyCompanionState(t, &companions, spawn.ID, 101, mgl32.Vec3{50.5, 0, 0})
	assertCompanionPositionX(t, &companions, 50.5)
	companions.Advance(25 * time.Millisecond)
	assertCompanionPositionX(t, &companions, 50.5)
	// 第三个快照后 target=102+0.5-2=100.5，采样 100→101 的中点 50.25；
	// 只有全新 ring（50, 50.5, 51）才会得到该值。
	applyCompanionState(t, &companions, spawn.ID, 102, mgl32.Vec3{51, 0, 0})
	companions.Advance(25 * time.Millisecond)
	assertCompanionPositionX(t, &companions, 50.25)
}

// TestChatEventsTaskLifecycleEnterRingAndEvictInOrder 锁定任务生命周期事件与
// 寻址事件走同一条 32 条事件环：严格递增 EventID 依序进入，溢出后保留最新 32 条。
func TestChatEventsTaskLifecycleEnterRingAndEvictInOrder(t *testing.T) {
	var events client.ChatEvents
	kinds := []network.ChatEventKind{
		network.ChatEventAccepted,
		network.ChatEventTaskStarted,
		network.ChatEventTaskProgress,
		network.ChatEventTaskCompleted,
		network.ChatEventTaskFailed,
		network.ChatEventTaskTimedOut,
	}
	for eventID := uint64(1); eventID <= 40; eventID++ {
		kind := kinds[(eventID-1)%uint64(len(kinds))]
		reason := network.ChatRejectNone
		if kind == network.ChatEventTaskFailed {
			reason = network.ChatRejectReason(network.TaskFailPlannerUnavailable +
				network.TaskFailReason((eventID-1)%4))
		}
		if err := events.Apply(companionTaskEvent(eventID, kind, reason)); err != nil {
			t.Fatalf("Apply 任务事件 %d：%v", eventID, err)
		}
	}
	dst := events.Events(nil)
	if len(dst) != 32 || dst[0].EventID != 9 || dst[31].EventID != 40 {
		t.Fatalf("事件环 = %d 条（首 %d 末 %d），want 32 条（首 9 末 40）",
			len(dst), dst[0].EventID, dst[31].EventID)
	}
	for index, event := range dst {
		wantID := uint64(9 + index)
		if event.EventID != wantID {
			t.Fatalf("环内顺序破坏：index %d EventID=%d want=%d", index, event.EventID, wantID)
		}
		if event.Kind != kinds[(wantID-1)%uint64(len(kinds))] {
			t.Fatalf("环内 index %d kind=%d 与入环序列不符", index, event.Kind)
		}
	}
}

func applyCompanionState(
	t *testing.T,
	companions *client.Companions,
	id companion.ID,
	tick uint64,
	position mgl32.Vec3,
) {
	t.Helper()
	if err := companions.ApplyStates(network.CompanionStates{
		Tick:   tick,
		States: []network.CompanionState{{ID: id, Dimension: core.Overworld, Position: position}},
	}); err != nil {
		t.Fatalf("ApplyStates tick %d：%v", tick, err)
	}
}

func onlyCompanionPresentation(t *testing.T, companions *client.Companions) client.CompanionPresentation {
	t.Helper()
	presentations := companions.AppendPresentations(nil)
	if len(presentations) != 1 {
		t.Fatalf("呈现数量 = %d，want 1", len(presentations))
	}
	return presentations[0]
}

func assertCompanionPositionX(t *testing.T, companions *client.Companions, want float32) {
	t.Helper()
	got := onlyCompanionPresentation(t, companions).Position.X()
	if math.Abs(float64(got-want)) > 1e-5 {
		t.Fatalf("呈现位置 X = %v，want %v", got, want)
	}
}

func companionTaskEvent(eventID uint64, kind network.ChatEventKind, reason network.ChatRejectReason) network.ChatEvent {
	event := chatEvent(eventID)
	event.Kind = kind
	event.RejectReason = reason
	return event
}

// TestChatEventsV18StoppedNotFollowingAndInventoryFullEnterRing 锁定协议 v18
// 三个新值经 ChatEvents.Apply（network.Validate 组合校验）按序进入事件环：
// TaskStopped 终态事件、NotFollowing 停止旁路拒绝与 TaskFailInventoryFull
// 失败原因都携带完整身份与原始指令，客户端事件环无需 per-kind 分支即可接
// 收并保持顺序；事实行的稳定中文格式由 cmd/mornlea 的 formatChatEvent 锁定
// （模型自由文本在任何 kind 上都不存在 wire 槽位）。
func TestChatEventsV18StoppedNotFollowingAndInventoryFullEnterRing(t *testing.T) {
	var events client.ChatEvents
	v18 := []network.ChatEvent{
		companionTaskEvent(1, network.ChatEventTaskStopped, network.ChatRejectNone),
		companionTaskEvent(2, network.ChatEventTaskFailed,
			network.ChatRejectReason(network.TaskFailInventoryFull)),
		companionTaskEvent(3, network.ChatEventRejected, network.ChatRejectNotFollowing),
	}
	for _, event := range v18 {
		if err := events.Apply(event); err != nil {
			t.Fatalf("Apply 事件 %d：%v", event.EventID, err)
		}
	}
	got := events.Events(nil)
	if len(got) != 3 ||
		got[0].Kind != network.ChatEventTaskStopped || got[0].RejectReason != network.ChatRejectNone ||
		got[1].Kind != network.ChatEventTaskFailed ||
		got[1].RejectReason != network.ChatRejectReason(network.TaskFailInventoryFull) ||
		got[2].Kind != network.ChatEventRejected || got[2].RejectReason != network.ChatRejectNotFollowing {
		t.Fatalf("v18 事件环=%+v，想要按序保留三个新值", got)
	}
	for index, event := range got {
		if event.EventID != uint64(index+1) {
			t.Fatalf("v18 事件[%d] ID=%d，想要按序递增", index, event.EventID)
		}
	}
}

func companionSpawn(last byte, tick uint64, name string, position mgl32.Vec3) network.CompanionSpawn {
	return network.CompanionSpawn{
		ID: companionTestID(last), Name: name, Tick: tick,
		Dimension: core.Overworld, Position: position,
	}
}

func companionTestID(last byte) companion.ID {
	return companion.ID{0: 0x12, 6: 0x40, 8: 0x80, 15: last}
}

func chatEvent(eventID uint64) network.ChatEvent {
	return network.ChatEvent{
		EventID: eventID, PlayerID: core.PlayerID{0: 0x12, 6: 0x40, 8: 0x80, 15: 1},
		PlayerName: "Chen", CompanionID: companionTestID(1), CompanionName: "阿木",
		Kind: network.ChatEventAccepted, Command: "挖石头",
	}
}
