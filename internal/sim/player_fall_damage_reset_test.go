package sim

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
)

// TestBeginResetResetsFallPeak 覆盖"维度 reset 重置峰值"场景：玩家下落途中触发
// beginReset（例如摔出世界下界）时，此前遗留的摔落峰值必须被重置为 reset 后的
// 新高度，不能带着旧峰值进入下一次重生。
func TestBeginResetResetsFallPeak(t *testing.T) {
	engine := NewEngine(0, 0, 0)
	id := SessionID(90)
	engine.RegisterSession(id, core.Overworld, core.ChunkPos{})
	player := engine.sessions[id].player
	player.peakY = 12345 // 模拟此前一次深度下落遗留的峰值

	player.beginReset()

	if player.peakY != player.state.Position.Y() {
		t.Fatalf(
			"beginReset 后 peakY=%v，想要等于新位置 Y=%v",
			player.peakY, player.state.Position.Y(),
		)
	}
}

// TestActivateResetsFallPeak 覆盖"传送/重生重置峰值"场景：玩家从 PendingSpawn
// 激活（无论来自 restore 候选还是新出生点）时，必须把遗留的摔落峰值重置为
// 激活后的落点高度，而不是沿用激活前的旧峰值。
func TestActivateResetsFallPeak(t *testing.T) {
	engine := NewEngine(0, 0, 0)
	id := SessionID(91)
	engine.RegisterSession(id, core.Overworld, core.ChunkPos{})
	session := engine.sessions[id]
	player := session.player
	player.peakY = 12345 // 模拟此前一次深度下落遗留的峰值

	location := PlayerLocation{
		Dimension: core.Overworld,
		Position:  mgl32.Vec3{4.5, 30, 4.5},
	}
	player.activate(session, location, false)

	if player.peakY != location.Position.Y() {
		t.Fatalf(
			"activate 后 peakY=%v，想要等于落点 Y=%v",
			player.peakY, location.Position.Y(),
		)
	}
}
