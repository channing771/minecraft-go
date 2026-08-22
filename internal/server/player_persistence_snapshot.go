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
	// 三层饥饿状态只有走到这里才来自真实存档：上面那条提前返回是"缺失玩家 /
	// 尚未观察到权威快照"，那两种情况没有可恢复的饥饿状态，HasHunger 保持假，
	// 由 sim 的 RegisterPlayer 落到与新玩家相同的固定初值。
	restore.Hunger = player.snapshot.Hunger
	restore.SaturationMilli = player.snapshot.SaturationMilli
	restore.ExhaustionMilli = player.snapshot.ExhaustionMilli
	restore.HasHunger = true
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
		// 更旧的 schema 没有饥饿字段，storage 的迁移链已经在这一步之前把它们
		// 补成固定初值，这里因此无条件照抄。
		Hunger:          stored.Hunger,
		SaturationMilli: stored.SaturationMilli,
		ExhaustionMilli: stored.ExhaustionMilli,
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
		}, Inventory: starterMaterialInventory(),
			// 缺失玩家的首份快照可能先于 sim 的第一次 Observe 落盘（Confirm 会
			// 直接标脏），因此这里就要写初值而不是零值：零饥饿是合法取值，
			// 落盘后重登的新玩家会直接进入挨饿状态。
			Hunger:          core.MaxHunger,
			SaturationMilli: core.InitialSaturationMilli,
		},
		hasSnapshot: true,
		missing:     true,
	}
}

// starterMaterialItems 是一次性材料包的稳定材料清单，顺序即背包格位顺序。
var starterMaterialItems = [...]core.ItemID{
	core.ItemCobblestone, core.ItemSmoothStone, core.ItemSand, core.ItemGravel,
	core.ItemOakLog, core.ItemOakPlanks, core.ItemLeaves, core.ItemGlass,
	core.ItemBrick, core.ItemWhiteWool, core.ItemRoofTile, core.ItemClay,
	core.ItemSnowBlock, core.ItemMossyCobblestone,
}

// starterSeedSlot 是起步种子所在的背包格位：紧随材料清单最后一格。
// 写成 len(starterMaterialItems) 而不是字面量 14，材料清单增删时种子自动跟随。
const starterSeedSlot = len(starterMaterialItems)

func starterMaterialInventory() core.Inventory {
	var inventory core.Inventory
	for slot, item := range starterMaterialItems {
		inventory.Backpack[slot] = core.ItemStack{Item: item, Count: core.MaxStackCount}
	}
	// 草丛等自然种子来源尚不存在，这一格是玩家取得第一颗种子的唯一途径。
	// 它和材料一样只在 ErrPlayerNotFound 路径构造，既有玩家不会被补发。
	inventory.Backpack[starterSeedSlot] = core.ItemStack{
		Item: core.ItemWheatSeeds, Count: core.MaxStackCount,
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
		// 三层全部落盘：不写疲劳会让"重登清疲劳"变成无成本操作（design.md D7）。
		Hunger:          player.snapshot.Hunger,
		SaturationMilli: player.snapshot.SaturationMilli,
		ExhaustionMilli: player.snapshot.ExhaustionMilli,
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
		player.snapshot.Health != save.Health ||
		player.snapshot.Hunger != save.Hunger ||
		player.snapshot.SaturationMilli != save.SaturationMilli ||
		player.snapshot.ExhaustionMilli != save.ExhaustionMilli {
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
	// 三层饥饿状态参与变更检测：饥饿是唯一会在玩家原地不动时独自变化的状态，
	// 漏掉任何一个字段都会让"只有饥饿变了"的 tick 被判为无变化而永不落盘。
	if left.Current != right.Current || left.Yaw != right.Yaw ||
		left.Pitch != right.Pitch || left.Inventory != right.Inventory ||
		left.Health != right.Health || left.Hunger != right.Hunger ||
		left.SaturationMilli != right.SaturationMilli ||
		left.ExhaustionMilli != right.ExhaustionMilli {
		return false
	}
	if left.Safe == nil || right.Safe == nil {
		return left.Safe == nil && right.Safe == nil
	}
	return *left.Safe == *right.Safe
}
