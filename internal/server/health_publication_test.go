package server

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/sim"
)

// TestPlayerHealthPublishesOnlyToOwner 覆盖"生命值只随本人的权威玩家状态下发"：
// 每个会话收到的 PlayerState 必须携带自己的生命值，且任何一条发给它的消息
// 都不得暴露另一名玩家的生命值（远端玩家消息本身不带该字段）。
func TestPlayerHealthPublishesOnlyToOwner(t *testing.T) {
	h := newRemotePublicationHarness(t, 1, 2)
	h.markSnapshotSent(1, core.ChunkPos{})
	h.markSnapshotSent(2, core.ChunkPos{})
	first := h.playerUpdate(1, true, core.Overworld, mgl32.Vec3{0.5, 2, 0.5})
	first.Health = 7
	second := h.playerUpdate(2, true, core.Overworld, mgl32.Vec3{0.5, 2, 0.5})
	second.Health = 11

	h.publish(sim.TickResult{Tick: 5, Players: []sim.PlayerUpdate{first, second}})

	assertOwnHealthOnly(t, h.drain(1), 7, 11)
	assertOwnHealthOnly(t, h.drain(2), 11, 7)
}

// TestPlayerHealthSlowSessionKeepsBoundedOutboxPolicy 覆盖"慢会话仍走既有有界
// outbox 与断开策略"：outbox 塞满的会话被断开，其余会话照常收到各自的生命值。
func TestPlayerHealthSlowSessionKeepsBoundedOutboxPolicy(t *testing.T) {
	ids := []sim.SessionID{1, 2, 3}
	h := newRemotePublicationHarness(t, ids...)
	for _, id := range ids {
		h.markSnapshotSent(id, core.ChunkPos{})
	}
	h.running.sessions[1].outbox = make(chan network.ServerMessage, 1)
	players := h.readyPlayers(ids...)
	for index := range players {
		players[index].Health = uint8(index) + 3
	}

	h.publish(sim.TickResult{Tick: 6, Players: players})

	if h.running.sessions[1] != nil {
		t.Fatal("满 outbox 的慢会话未被断开")
	}
	for index, id := range ids[1:] {
		want := uint8(index) + 4
		if h.running.sessions[id] == nil {
			t.Fatalf("慢会话断开时误伤了会话 %d", id)
		}
		assertOwnHealthOnly(t, h.drain(id), want, 3)
	}
}

// assertOwnHealthOnly 断言一批发给某个会话的消息里，恰好有一条 PlayerState 且
// 携带 want 生命值，同时不得出现携带 other 生命值的玩家状态。
func assertOwnHealthOnly(
	t *testing.T,
	messages []network.ServerMessage,
	want, other uint8,
) {
	t.Helper()
	states := 0
	for _, message := range messages {
		state, ok := message.(network.PlayerState)
		if !ok {
			continue
		}
		states++
		if state.Health == other {
			t.Fatalf("PlayerState 泄漏了他人生命值 %d", other)
		}
		if state.Health != want {
			t.Fatalf("PlayerState.Health = %d，想要 %d", state.Health, want)
		}
	}
	if states != 1 {
		t.Fatalf("PlayerState 条数 = %d，想要 1（消息 %#v）", states, messages)
	}
}

// TestPlayerPersistenceDirtyDetectionIncludesHealth 覆盖"只有生命值变化也必须被
// 持久化"：玩家原地站着回血时位置与物品都不变，快照比较与存档比较都必须把生命值
// 算进去，否则这次变化会被当成无变化而丢失。
func TestPlayerPersistenceDirtyDetectionIncludesHealth(t *testing.T) {
	full := sim.PlayerSnapshot{Health: core.MaxHealth}
	hurt := full
	hurt.Health = 7
	if playerSnapshotsEqual(full, hurt) {
		t.Fatal("快照比较忽略了生命值")
	}

	player := &cachedPlayer{hasSnapshot: true, snapshot: full}
	save := player.save(1)
	if save.Health != core.MaxHealth {
		t.Fatalf("存档生命值 = %d，想要 %d", save.Health, core.MaxHealth)
	}
	if !player.matchesSave(save) {
		t.Fatal("同值存档必须匹配缓存快照")
	}
	save.Health = 7
	if player.matchesSave(save) {
		t.Fatal("存档比较忽略了生命值")
	}
}
