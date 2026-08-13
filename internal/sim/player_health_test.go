package sim

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
)

// TestRegisterPlayerDefaultsMissingHealthToFull 覆盖"新玩家以满血开始"场景：
// 缺失（零值）生命值必须被当作"缺失"处理，落地为满血。
func TestRegisterPlayerDefaultsMissingHealthToFull(t *testing.T) {
	engine := NewEngine(0, 0)
	id := SessionID(70)
	engine.RegisterSession(id, core.Overworld, core.ChunkPos{})
	if got := engine.sessions[id].player.health; got != core.MaxHealth {
		t.Fatalf("新玩家 health = %d，想要 %d", got, core.MaxHealth)
	}
}

// TestPlayerRestoreCarriesExplicitHealthThroughSnapshot 覆盖生命值跨重启保真：
// 存档携带的非满血生命值必须原样出现在权威快照中。
func TestPlayerRestoreCarriesExplicitHealthThroughSnapshot(t *testing.T) {
	engine := NewEngine(0, 0)
	id := SessionID(71)
	current := PlayerLocation{
		Dimension: core.Overworld,
		Position:  mgl32.Vec3{2.5, 1, 0.5},
	}
	safe := PlayerLocation{
		Dimension: core.Overworld,
		Position:  mgl32.Vec3{16.5, 1, 0.5},
	}
	engine.RegisterPlayer(id, PlayerRestore{
		Current:        &current,
		Safe:           &safe,
		Health:         7,
		SpawnDimension: core.Overworld,
	})
	makeRestoreWorldReady(t, engine, current, safe)

	update := onlyPlayerUpdate(t, engine.Step(), id)
	if !update.Ready {
		t.Fatalf("restore 未激活: %+v", update)
	}
	snapshot, ok := engine.PlayerSnapshot(id)
	if !ok || snapshot.Health != 7 {
		t.Fatalf("PlayerSnapshot health=%d ok=%v，想要 7", snapshot.Health, ok)
	}
}

// TestPlayerRestoreMissingHealthDefaultsToFullThroughSnapshot 覆盖缺失值兜底：
// restore 未携带生命值时，快照必须回落到满血而不是零血。
func TestPlayerRestoreMissingHealthDefaultsToFullThroughSnapshot(t *testing.T) {
	engine := NewEngine(0, 0)
	id := SessionID(72)
	current := PlayerLocation{
		Dimension: core.Overworld,
		Position:  mgl32.Vec3{2.5, 1, 0.5},
	}
	safe := PlayerLocation{
		Dimension: core.Overworld,
		Position:  mgl32.Vec3{16.5, 1, 0.5},
	}
	engine.RegisterPlayer(id, PlayerRestore{
		Current:        &current,
		Safe:           &safe,
		SpawnDimension: core.Overworld,
	})
	makeRestoreWorldReady(t, engine, current, safe)

	onlyPlayerUpdate(t, engine.Step(), id)
	snapshot, ok := engine.PlayerSnapshot(id)
	if !ok || snapshot.Health != core.MaxHealth {
		t.Fatalf("PlayerSnapshot health=%d ok=%v，想要 %d", snapshot.Health, ok, core.MaxHealth)
	}
}
