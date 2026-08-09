package sim

import (
	"cmp"
	"math"
	"slices"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

// settleDeaths 结算本 tick 生命值归零的玩家。它必须在本 tick 发布权威状态之前运行，
// 外部才观察不到生命值为 0 的中间状态。死亡是罕见事件，不受每 tick 固定工作量契约约束；
// 没有玩家死亡时这里既不排序也不分配。
func (engine *Engine) settleDeaths(pending map[core.ChunkKey]*pendingChunkChanges) {
	var dying []SessionID
	for id, session := range engine.sessions {
		if session.player != nil && session.player.lifecycle == PlayerActive &&
			session.player.health == 0 {
			dying = append(dying, id)
		}
	}
	if len(dying) == 0 {
		return
	}
	slices.Sort(dying)
	for _, id := range dying {
		engine.settleDeath(engine.sessions[id], pending)
	}
}

// settleDeath 在同一 tick 内完成一名玩家的死亡结算：先把背包逐格掉进世界，
// 再回满生命值、清受伤计时，最后复用既有的位置跳变入口把玩家送回出生锚点。
func (engine *Engine) settleDeath(
	session *sessionState,
	pending map[core.ChunkKey]*pendingChunkChanges,
) {
	player := session.player
	engine.dropInventoryOnDeath(session, pending)
	player.health = core.MaxHealth
	player.resetRegenTimer()
	// beginReset 是既有的重生/传送路径：它把玩家置为待重生、位置移回出生锚点所在列、
	// 速度与输入归零，并把摔落峰值重置到新高度，因此死亡不需要另写一套位置赋值。
	player.beginReset()
	engine.subscriptionsDirty = true
}

// dropInventoryOnDeath 把玩家的 36 个物品栏格掉进世界。它按环形外扩依次尝试各个
// 已加载区块，逐格放置且放置成功后才清空该格，因此任何时刻每件物品要么在背包里、
// 要么在地上。扫完全部已加载区块仍放不下的格保留在背包里跟随重生：
// 死亡不被阻止，物品也不被销毁。
func (engine *Engine) dropInventoryOnDeath(
	session *sessionState,
	pending map[core.ChunkKey]*pendingChunkChanges,
) {
	player := session.player
	dimension := engine.dimensions[session.dimension]
	if dimension == nil {
		return
	}
	death := core.BlockPos{
		X: int32(math.Floor(float64(player.state.Position.X()))),
		Y: int32(math.Floor(float64(player.state.Position.Y()))),
		Z: int32(math.Floor(float64(player.state.Position.Z()))),
	}
	for _, key := range engine.deathDropChunks(session.dimension, death.Chunk()) {
		record := dimension.records[key.Pos]
		if record == nil || record.State != ChunkReady || record.Chunk == nil {
			continue
		}
		blockIndex, indexed := world.ChunkBlockIndex(clampBlockToChunk(death, key.Pos))
		if !indexed {
			continue
		}
		changed, remaining := placeDeathDrops(
			player, record.Chunk, blockIndex, engine.tunables.PlayerDropPickupDelayTicks,
		)
		if changed {
			// 每个被写入的区块各自登记 revision barrier；死亡掉落不做跨区块原子提交。
			engine.touchChunk(key, pending)
		}
		if remaining == 0 {
			return
		}
	}
}

// deathDropChunks 返回死亡掉落的候选区块：只包含已加载且 Ready 的区块，
// 按到死亡区块的环形半径 0、1、2… 逐圈排列，同圈内沿用 sortChunkKeys 的稳定顺序。
// 候选集合由已加载区块封顶，因此扫完即终止，既不会写未加载区块也不会触发同步加载。
func (engine *Engine) deathDropChunks(
	dimensionID core.DimensionID,
	death core.ChunkPos,
) []core.ChunkKey {
	dimension := engine.dimensions[dimensionID]
	keys := make([]core.ChunkKey, 0, len(dimension.records))
	for pos, record := range dimension.records {
		if record.State != ChunkReady || record.Chunk == nil {
			continue
		}
		keys = append(keys, core.ChunkKey{Dimension: dimensionID, Pos: pos})
	}
	sortChunkKeys(keys)
	slices.SortStableFunc(keys, func(left, right core.ChunkKey) int {
		return cmp.Compare(chunkRing(death, left.Pos), chunkRing(death, right.Pos))
	})
	return keys
}

// chunkRing 返回目标区块相对中心区块的圈号，即切比雪夫距离。
func chunkRing(center, pos core.ChunkPos) int64 {
	dx := int64(pos.X) - int64(center.X)
	dz := int64(pos.Z) - int64(center.Z)
	if dx < 0 {
		dx = -dx
	}
	if dz < 0 {
		dz = -dz
	}
	return max(dx, dz)
}

// clampBlockToChunk 把方块位置夹到目标区块的水平范围内，取该区块中离死亡点最近的一列。
// 溢出的掉落物因此落在朝向死亡点的一侧，且位置只由死亡点和区块坐标决定。
func clampBlockToChunk(block core.BlockPos, pos core.ChunkPos) core.BlockPos {
	minX := pos.X << core.SectionShift
	minZ := pos.Z << core.SectionShift
	block.X = min(max(block.X, minX), minX+core.SectionSize-1)
	block.Z = min(max(block.Z, minZ), minZ+core.SectionSize-1)
	return block
}

// placeDeathDrops 在一个区块上逐格放置玩家仍持有的物品堆。单格用批量路径预演，
// 成功才提交并清空该格，因此一格的拆分与合并保持原子。
// 返回该区块是否被写入，以及仍留在背包里的非空格数。
//
// 它改写 player.inventory 但不置 inventoryDirty：死亡结算的调用方 settleDeath
// 随后一定会调用 beginReset，由后者统一置脏。这条隐式依赖不要在别处复用。
//
// pickupDelayTicks 由调用方传入本 tick 的快照值，这个自由函数本身绝不读取
// ActiveTunables。
func placeDeathDrops(
	player *playerState,
	chunk *world.Chunk,
	blockIndex uint32,
	pickupDelayTicks uint8,
) (changed bool, remaining int) {
	for slot := uint8(0); slot < core.InventorySlots; slot++ {
		stack, _ := player.inventory.Slot(slot)
		if stack.Item == core.ItemNone {
			continue
		}
		batch := [1]core.ItemStack{stack}
		// ponytail: 逐格预演，最坏情况每个候选区块整份拷贝 [32]DropSlot 共 36 次。
		// 死亡是罕见事件，不在每 tick 固定工作量契约内，因此这个天花板可以接受。
		// 若将来它成为热点，升级路径是把整轮放置改成"一次拷贝 + 多格预演"。
		next, ok := chunk.PrepareDropBatch(
			batch[:], blockIndex, pickupDelayTicks,
		)
		if !ok {
			remaining++
			continue
		}
		chunk.CommitDropBatch(next)
		cleared, ok := player.inventory.SetSlot(slot, core.ItemStack{})
		if !ok {
			panic("sim: 清空物品栏格必须成功")
		}
		player.inventory = cleared
		changed = true
	}
	return changed, remaining
}
