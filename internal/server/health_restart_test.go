package server

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/sim"
)

// healthRestartFallHeight 是重启测试用的落差：伤害 = floor(16) − 3 = 13，
// 从满血摔到 7。
const healthRestartFallHeight = float32(16)

// TestHealthSevenSurvivesDiskRestart 覆盖"生命值跨重启保真"：玩家摔到 7 点血、
// 正常断开使存档落盘、关服、用同一磁盘世界重开并重连，生命值必须仍是 7。
// 同一条测试顺带覆盖"存档里没有生命值的旧玩家按满血登录"。
//
// 时序前提：自动回复需要 RegenDelayTicks + RegenIntervalTicks = 140 个 tick
// （50ms/tick 约 7 秒）才恢复第一点，而"观察到 7 点血 → 断开 → 落盘"远快于此，
// 因此 7 是稳定的观察值。
func TestHealthSevenSurvivesDiskRestart(t *testing.T) {
	root := t.TempDir()
	identity := integrationIdentity(0x97, "Survivor")
	loc := sim.PlayerLocation{Dimension: core.Overworld, Position: mgl32.Vec3{0.5, 1.001, 0.5}}
	seedIntegrationPlayer(t, root, identity, sim.PlayerSnapshot{Current: loc})

	first := startDiskHost(t, root, "127.0.0.1:0", changedGenerator{})
	connected := dialIntegrationClient(t, first.Addr, identity)
	waitClientReadyFor(t, first, connected, identity.PlayerID)

	// 存档里没有生命值的旧玩家必须按满血登录。
	if state, _ := waitHealth(t, connected, func(state network.PlayerState) bool {
		return state.Ready
	}); state.Health != core.MaxHealth {
		t.Fatalf("无生命值存档的登录生命值 = %d，想要满血 %d",
			state.Health, core.MaxHealth)
	}

	first.Host.world.SetPlayerPositionForTest(
		first.SessionFor(t, identity.PlayerID),
		mgl32.Vec3{0.5, 1 + healthRestartFallHeight, 0.5},
	)
	waitHealth(t, connected, func(state network.PlayerState) bool {
		return state.Ready && state.Health == core.MaxHealth-13
	})

	// 正常刷新与关服：断开触发玩家存档落盘，关服等待写入完成。
	if err := connected.Close(); err != nil {
		t.Fatalf("关闭连接: %v", err)
	}
	first.WaitPlayerSaved(t, identity.PlayerID)
	first.Shutdown(t)

	// 用同一磁盘世界重开并重连：生命值必须原值恢复。
	second := startDiskHost(t, root, "127.0.0.1:0", changedGenerator{})
	reconnected := dialIntegrationClient(t, second.Addr, identity)
	waitClientReadyFor(t, second, reconnected, identity.PlayerID)
	state, _ := waitHealth(t, reconnected, func(state network.PlayerState) bool {
		return state.Ready
	})
	if state.Health != core.MaxHealth-13 {
		t.Fatalf("重启后生命值 = %d，想要原值 %d", state.Health, core.MaxHealth-13)
	}
	snapshot, ok := second.PlayerSnapshotFor(t, identity.PlayerID)
	if !ok || snapshot.Health != core.MaxHealth-13 {
		t.Fatalf("重启后权威快照生命值 = %+v (%t)，想要 %d",
			snapshot, ok, core.MaxHealth-13)
	}

	if err := reconnected.Close(); err != nil {
		t.Fatal(err)
	}
	second.Shutdown(t)
}
