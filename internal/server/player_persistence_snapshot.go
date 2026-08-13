package server

import (
	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/sim"
	"github.com/channing771/mornlea/internal/storage"
)

func (player *cachedPlayer) restore(metadata storage.Metadata) sim.PlayerRestore {
	restore := sim.PlayerRestore{
		SpawnDimension: metadata.SpawnDimension,
		SpawnAnchor:    metadata.SpawnAnchor,
	}
	if !player.hasSnapshot || player.missing && !player.hasObservedSnapshot {
		restore.Inventory = player.snapshot.Inventory
		return restore
	}
	current := player.snapshot.Current
	restore.Current = &current
	if player.snapshot.Safe != nil {
		safe := *player.snapshot.Safe
		restore.Safe = &safe
	}
	restore.Yaw = player.snapshot.Yaw
	restore.Pitch = player.snapshot.Pitch
	restore.Inventory = player.snapshot.Inventory
	restore.Health = player.snapshot.Health
	return restore
}

func cachedPlayerFromStored(stored storage.StoredPlayer, pendingName string) *cachedPlayer {
	snapshot := sim.PlayerSnapshot{
		Current: sim.PlayerLocation{
			Dimension: stored.Current.Dimension,
			Position:  mgl32.Vec3(stored.Current.Position),
		},
		Yaw:       stored.Yaw,
		Pitch:     stored.Pitch,
		Inventory: stored.Inventory,
		Health:    stored.Health,
	}
	if stored.Safe != nil {
		snapshot.Safe = &sim.PlayerLocation{
			Dimension: stored.Safe.Dimension,
			Position:  mgl32.Vec3(stored.Safe.Position),
		}
	}
	return &cachedPlayer{
		id:                  stored.PlayerID,
		name:                stored.DisplayName,
		pendingName:         pendingName,
		persisted:           stored.Revision,
		snapshot:            snapshot,
		hasSnapshot:         true,
		hasObservedSnapshot: true,
		dirty:               stored.NeedsRewrite,
	}
}

func newMissingCachedPlayer(
	id core.PlayerID,
	name string,
	metadata storage.Metadata,
) *cachedPlayer {
	anchor := metadata.SpawnAnchor
	return &cachedPlayer{
		id:          id,
		pendingName: name,
		snapshot: sim.PlayerSnapshot{Current: sim.PlayerLocation{
			Dimension: metadata.SpawnDimension,
			Position: mgl32.Vec3{
				float32(anchor.X)*core.SectionSize + 0.5,
				core.MaxY + 1,
				float32(anchor.Z)*core.SectionSize + 0.5,
			},
		}, Inventory: starterMaterialInventory()},
		hasSnapshot: true,
		missing:     true,
	}
}

func starterMaterialInventory() core.Inventory {
	items := [...]core.ItemID{
		core.ItemCobblestone, core.ItemSmoothStone, core.ItemSand, core.ItemGravel,
		core.ItemOakLog, core.ItemOakPlanks, core.ItemLeaves, core.ItemGlass,
		core.ItemBrick, core.ItemWhiteWool, core.ItemRoofTile, core.ItemClay,
		core.ItemSnowBlock, core.ItemMossyCobblestone,
	}
	var inventory core.Inventory
	for slot, item := range items {
		inventory.Backpack[slot] = core.ItemStack{Item: item, Count: core.MaxStackCount}
	}
	if !inventory.Valid() {
		panic("server: invalid starter material inventory")
	}
	return inventory
}

func (player *cachedPlayer) save(revision uint64) storage.PlayerSave {
	save := storage.PlayerSave{
		PlayerID:    player.id,
		Revision:    revision,
		DisplayName: player.name,
		Current: storage.PlayerLocation{
			Dimension: player.snapshot.Current.Dimension,
			Position:  [3]float32(player.snapshot.Current.Position),
		},
		Yaw:       player.snapshot.Yaw,
		Pitch:     player.snapshot.Pitch,
		Inventory: player.snapshot.Inventory,
		Health:    player.snapshot.Health,
	}
	if player.snapshot.Safe != nil {
		save.Safe = &storage.PlayerLocation{
			Dimension: player.snapshot.Safe.Dimension,
			Position:  [3]float32(player.snapshot.Safe.Position),
		}
	}
	return save
}

func (player *cachedPlayer) matchesSave(save storage.PlayerSave) bool {
	if !player.hasSnapshot || player.id != save.PlayerID || player.name != save.DisplayName ||
		player.snapshot.Current.Dimension != save.Current.Dimension ||
		[3]float32(player.snapshot.Current.Position) != save.Current.Position ||
		player.snapshot.Yaw != save.Yaw || player.snapshot.Pitch != save.Pitch ||
		player.snapshot.Inventory != save.Inventory ||
		player.snapshot.Health != save.Health {
		return false
	}
	if player.snapshot.Safe == nil || save.Safe == nil {
		return player.snapshot.Safe == nil && save.Safe == nil
	}
	return player.snapshot.Safe.Dimension == save.Safe.Dimension &&
		[3]float32(player.snapshot.Safe.Position) == save.Safe.Position
}

func clonePlayerSave(save storage.PlayerSave) storage.PlayerSave {
	clone := save
	if save.Safe != nil {
		safe := *save.Safe
		clone.Safe = &safe
	}
	return clone
}

func clonePlayerSnapshot(snapshot sim.PlayerSnapshot) sim.PlayerSnapshot {
	clone := snapshot
	if snapshot.Safe != nil {
		safe := *snapshot.Safe
		clone.Safe = &safe
	}
	return clone
}

func playerSnapshotsEqual(left, right sim.PlayerSnapshot) bool {
	if left.Current != right.Current || left.Yaw != right.Yaw ||
		left.Pitch != right.Pitch || left.Inventory != right.Inventory ||
		left.Health != right.Health {
		return false
	}
	if left.Safe == nil || right.Safe == nil {
		return left.Safe == nil && right.Safe == nil
	}
	return *left.Safe == *right.Safe
}
