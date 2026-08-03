package sim

import (
	"sort"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

const (
	// DropInterestRadius 是掉落物 tick 与同步的固定区块半径。
	DropInterestRadius = 2
	// DropPickupDelayTicks 是新掉落物可被拾取前的活动 tick 数。
	DropPickupDelayTicks = 10
	// DropLifetimeTicks 是掉落物累计活动 tick 的寿命上限。
	DropLifetimeTicks = 6000
	// dropPickupRange 是玩家到方块中心的最大拾取距离。
	dropPickupRange = 1.25
)

// sessionDropWantedSnapshot 返回该会话的固定半径掉落物兴趣区块。
// 只有已进入 Play 的玩家参与掉落物 tick 与同步。
func (engine *Engine) sessionDropWantedSnapshot(
	session *sessionState,
) map[core.ChunkKey]struct{} {
	wanted := make(map[core.ChunkKey]struct{})
	if session.player == nil || session.player.lifecycle != PlayerActive ||
		engine.dimensions[session.dimension] == nil {
		return wanted
	}
	for dz := -DropInterestRadius; dz <= DropInterestRadius; dz++ {
		for dx := -DropInterestRadius; dx <= DropInterestRadius; dx++ {
			wanted[core.ChunkKey{
				Dimension: session.dimension,
				Pos: core.ChunkPos{
					X: session.center.X + int32(dx),
					Z: session.center.Z + int32(dz),
				},
			}] = struct{}{}
		}
	}
	return wanted
}

// advanceDrops 在单写者 tick 中推进兴趣范围内的掉落物寿命并处理拾取。
// 它按 ChunkKey、SessionID、槽位稳定扫描，最坏为玩家数 × 25 区块 × 32 槽。
func (engine *Engine) advanceDrops(pending map[core.ChunkKey]*pendingChunkChanges) {
	keys := engine.dropInterestKeys()
	if len(keys) == 0 {
		return
	}
	sessions := engine.sortedActiveSessions()
	for _, key := range keys {
		dimension := engine.dimensions[key.Dimension]
		if dimension == nil {
			continue
		}
		record, ok := dimension.records[key.Pos]
		if !ok || record.State != ChunkReady || record.Chunk == nil {
			continue
		}
		if engine.advanceChunkDrops(key, record.Chunk, sessions) {
			engine.touchChunk(key, pending)
		}
	}
}

// advanceChunkDrops 推进一个区块的全部槽，返回该区块是否发生变化。
func (engine *Engine) advanceChunkDrops(
	key core.ChunkKey,
	chunk *world.Chunk,
	sessions []SessionID,
) bool {
	// 只有可观察的变化（过期、拾取）推进区块 revision；
	// 年龄与拾取延迟是权威内部计数，不产生每 tick 的发布。
	changed := false
	for slot := range core.DropsPerChunk {
		drop := chunk.Drop(slot)
		if !drop.Active {
			continue
		}
		if drop.PickupDelayTicks > 0 {
			drop.PickupDelayTicks--
		}
		drop.AgeTicks++
		if drop.AgeTicks >= DropLifetimeTicks {
			chunk.ClearDrop(slot)
			changed = true
			continue
		}
		chunk.SetDrop(slot, drop)
		if drop.PickupDelayTicks > 0 {
			continue
		}
		if engine.pickUpDrop(key, chunk, slot, sessions) {
			changed = true
		}
	}
	return changed
}

// pickUpDrop 让范围内的玩家按稳定顺序尽量拾取该堆，返回堆是否发生变化。
func (engine *Engine) pickUpDrop(
	key core.ChunkKey,
	chunk *world.Chunk,
	slot int,
	sessions []SessionID,
) bool {
	center, ok := dropCenter(key.Pos, chunk.Drop(slot).BlockIndex)
	if !ok {
		return false
	}
	changed := false
	for _, id := range sessions {
		session := engine.sessions[id]
		if session == nil || session.player == nil || session.dimension != key.Dimension {
			continue
		}
		drop := chunk.Drop(slot)
		if !drop.Active || drop.Stack.Count == 0 {
			return true
		}
		if !withinPickupRange(session.player.state.Position, center) {
			continue
		}
		player := session.player
		hotbar := player.hotbar
		taken := uint8(0)
		for taken < drop.Stack.Count {
			next, added := hotbar.Add(drop.Stack.Item)
			if !added {
				break
			}
			hotbar = next
			taken++
		}
		if taken == 0 {
			continue
		}
		player.hotbar = hotbar
		player.hotbarDirty = true
		drop.Stack.Count -= taken
		if drop.Stack.Count == 0 {
			chunk.ClearDrop(slot)
			return true
		}
		chunk.SetDrop(slot, drop)
		changed = true
	}
	return changed
}

// dropCenter 返回掉落物所在方块的中心世界坐标。
func dropCenter(chunk core.ChunkPos, blockIndex uint32) (mgl32.Vec3, bool) {
	position, ok := world.BlockPosFromChunkIndex(chunk, blockIndex)
	if !ok {
		return mgl32.Vec3{}, false
	}
	return mgl32.Vec3{
		float32(position.X) + 0.5,
		float32(position.Y) + 0.5,
		float32(position.Z) + 0.5,
	}, true
}

func withinPickupRange(player, center mgl32.Vec3) bool {
	return center.Sub(player).Len() <= dropPickupRange
}

// dropInterestKeys 返回本 tick 需要推进的区块，按稳定顺序排列且不重复。
func (engine *Engine) dropInterestKeys() []core.ChunkKey {
	union := make(map[core.ChunkKey]struct{})
	for _, session := range engine.sessions {
		for key := range engine.sessionDropWantedSnapshot(session) {
			union[key] = struct{}{}
		}
	}
	keys := make([]core.ChunkKey, 0, len(union))
	for key := range union {
		keys = append(keys, key)
	}
	sortChunkKeys(keys)
	return keys
}

func (engine *Engine) sortedActiveSessions() []SessionID {
	sessions := make([]SessionID, 0, len(engine.sessions))
	for id, session := range engine.sessions {
		if session.player != nil && session.player.lifecycle == PlayerActive {
			sessions = append(sessions, id)
		}
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i] < sessions[j] })
	return sessions
}

// touchChunk 为只有掉落物变化的区块登记一个零方块 revision barrier。
func (engine *Engine) touchChunk(
	key core.ChunkKey,
	pending map[core.ChunkKey]*pendingChunkChanges,
) {
	if pending[key] != nil {
		return
	}
	record := engine.dimensions[key.Dimension].records[key.Pos]
	pending[key] = &pendingChunkChanges{
		baseRevision: record.Revision,
		changes:      make(map[uint32]BlockChange),
		dirty:        make(map[int]struct{}),
	}
}

// SetChunkDropForTest 直接写入一个已 Ready 区块的掉落物槽，仅供测试构造固定场景。
func (engine *Engine) SetChunkDropForTest(
	key core.ChunkKey,
	slot int,
	value world.DropSlot,
) {
	dimension := engine.dimensions[key.Dimension]
	if dimension == nil {
		return
	}
	if record, ok := dimension.records[key.Pos]; ok && record.Chunk != nil {
		record.Chunk.SetDrop(slot, value)
	}
}

// SetBlockForTest 直接写入一个已 Ready 区块的方块，仅供测试构造固定场景。
func (engine *Engine) SetBlockForTest(position core.BlockPos, block core.BlockID) {
	dimension := engine.dimensions[core.Overworld]
	if dimension == nil {
		return
	}
	_, _, _ = dimension.SetBlock(position, block)
}
