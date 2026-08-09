package sim

import (
	"math"
	"sort"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

const (
	// DropInterestRadius 是掉落物 tick 与同步的固定区块半径。
	DropInterestRadius = core.DropInterestRadius

	// 以下是可调参数的编译期默认值。唯一读取入口是 Tunables 快照，
	// 不得再以导出常量暴露——见 internal/archcheck 的 TestTunableConstantsAreNotExported。

	// defaultDropPickupDelayTicks 是采掘与方块破坏产生的掉落物可被拾取前的活动 tick 数。
	defaultDropPickupDelayTicks = 10
	// defaultPlayerDropPickupDelayTicks 是玩家主动丢弃的掉落物可被拾取前的活动 tick 数。
	// 它比方块破坏更长，避免刚丢出的物品被自己立刻拾回。
	defaultPlayerDropPickupDelayTicks = 40
	// defaultDropLifetimeTicks 是掉落物累计活动 tick 的寿命上限。
	defaultDropLifetimeTicks = 6000
	// defaultDropPickupRange 是玩家到方块中心的最大拾取距离。
	defaultDropPickupRange = 1.25
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
	keys := engine.activeInterestKeys()
	if len(keys) == 0 {
		return
	}
	sessions := engine.sortedActiveSessions()
	lifetimeTicks := engine.tunables.DropLifetimeTicks
	pickupRange := engine.tunables.DropPickupRange
	for _, key := range keys {
		dimension := engine.dimensions[key.Dimension]
		if dimension == nil {
			continue
		}
		record, ok := dimension.records[key.Pos]
		if !ok || record.State != ChunkReady || record.Chunk == nil {
			continue
		}
		if engine.advanceChunkDrops(key, record.Chunk, sessions, lifetimeTicks, pickupRange) {
			engine.touchChunk(key, pending)
		}
	}
}

// advanceChunkDrops 推进一个区块的全部槽，返回该区块是否发生变化。
// lifetimeTicks、pickupRange 是调用方在本 tick 入口取的快照值，本函数全程只用它们，
// 不再重新读取生效参数。
func (engine *Engine) advanceChunkDrops(
	key core.ChunkKey,
	chunk *world.Chunk,
	sessions []SessionID,
	lifetimeTicks uint32,
	pickupRange float32,
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
		if drop.AgeTicks >= lifetimeTicks {
			chunk.ClearDrop(slot)
			changed = true
			continue
		}
		chunk.SetDrop(slot, drop)
		if drop.PickupDelayTicks > 0 {
			continue
		}
		if engine.pickUpDrop(key, chunk, slot, sessions, pickupRange) {
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
	pickupRange float32,
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
		if !withinPickupRange(session.player.state.Position, center, pickupRange) {
			continue
		}
		player := session.player
		// 一次整堆装入：先快捷栏后背包，余量留在地面。
		next, remainder := player.inventory.AddStack(drop.Stack)
		taken := drop.Stack.Count - remainder.Count
		if taken == 0 {
			continue
		}
		player.inventory = next
		player.inventoryDirty = true
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

// withinPickupRange 报告玩家是否在拾取距离内。pickupRange 由调用方传入本 tick 的
// 快照值，这个自由函数本身绝不读取 ActiveTunables。
func withinPickupRange(player, center mgl32.Vec3, pickupRange float32) bool {
	return center.Sub(player).Len() <= pickupRange
}

// activeInterestKeys 返回本 tick 需要推进的区块，按稳定顺序排列且不重复。
// 掉落物与熔炉共用同一集合，并复用引擎 scratch，因此稳定玩家数下不产生每 tick 分配。
func (engine *Engine) activeInterestKeys() []core.ChunkKey {
	if engine.dropKeySeen == nil {
		engine.dropKeySeen = make(map[core.ChunkKey]struct{}, core.MaxSessionDrops)
	}
	clear(engine.dropKeySeen)
	keys := engine.dropKeyScratch[:0]
	for _, session := range engine.sessions {
		if session.player == nil || session.player.lifecycle != PlayerActive ||
			engine.dimensions[session.dimension] == nil {
			continue
		}
		for dx := -DropInterestRadius; dx <= DropInterestRadius; dx++ {
			for dz := -DropInterestRadius; dz <= DropInterestRadius; dz++ {
				key := core.ChunkKey{
					Dimension: session.dimension,
					Pos: core.ChunkPos{
						X: session.center.X + int32(dx),
						Z: session.center.Z + int32(dz),
					},
				}
				if _, seen := engine.dropKeySeen[key]; seen {
					continue
				}
				engine.dropKeySeen[key] = struct{}{}
				keys = append(keys, key)
			}
		}
	}
	sortChunkKeys(keys)
	engine.dropKeyScratch = keys
	return keys
}

func (engine *Engine) sortedActiveSessions() []SessionID {
	sessions := engine.dropSessionScratch[:0]
	for id, session := range engine.sessions {
		if session.player != nil && session.player.lifecycle == PlayerActive {
			sessions = append(sessions, id)
		}
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i] < sessions[j] })
	engine.dropSessionScratch = sessions
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

// DropSnapshot 是一个权威掉落物堆在本 tick 的完整可见值。
type DropSnapshot struct {
	ID         core.DropID
	BlockIndex uint32
	Item       core.ItemID
	Count      uint8
	Durability uint16
}

// MaxSessionDrops 是单个会话镜像的固定上限：25 个兴趣区块 × 每区块 32 槽。
const MaxSessionDrops = core.MaxSessionDrops

// AppendSessionDrops 按稳定 ID 顺序把该会话兴趣范围内的当前掉落物追加到 dst。
// 调用方可复用 dst 底层数组；结果最多 MaxSessionDrops 项。
func (engine *Engine) AppendSessionDrops(id SessionID, dst []DropSnapshot) []DropSnapshot {
	session := engine.sessions[id]
	if session == nil || session.player == nil ||
		session.player.lifecycle != PlayerActive {
		return dst
	}
	dimension := engine.dimensions[session.dimension]
	if dimension == nil {
		return dst
	}
	// 按 ChunkKey 的排序顺序直接遍历固定半径，避免每次调用分配集合。
	for dx := -DropInterestRadius; dx <= DropInterestRadius; dx++ {
		for dz := -DropInterestRadius; dz <= DropInterestRadius; dz++ {
			key := core.ChunkKey{
				Dimension: session.dimension,
				Pos: core.ChunkPos{
					X: session.center.X + int32(dx),
					Z: session.center.Z + int32(dz),
				},
			}
			record, ok := dimension.records[key.Pos]
			if !ok || record.State != ChunkReady || record.Chunk == nil {
				continue
			}
			dst = appendChunkDrops(dst, key, record.Chunk)
		}
	}
	return dst
}

func appendChunkDrops(
	dst []DropSnapshot,
	key core.ChunkKey,
	chunk *world.Chunk,
) []DropSnapshot {
	for slot := range core.DropsPerChunk {
		drop := chunk.Drop(slot)
		if !drop.Active {
			continue
		}
		dst = append(dst, DropSnapshot{
			ID: core.DropID{
				Dimension:  key.Dimension,
				Chunk:      key.Pos,
				Slot:       uint8(slot),
				Generation: drop.Generation,
			},
			BlockIndex: drop.BlockIndex,
			Item:       drop.Stack.Item,
			Count:      drop.Stack.Count,
			Durability: drop.Stack.Durability,
		})
	}
	return dst
}

// dropSelectedItem 在单写者 tick 内把权威选中快捷栏中的一个物品原地转移为掉落物。
//
// 全部预检在任何写入之前完成：任一失败都不改变背包、掉落物、区块 revision
// 或 persistence 状态。位置取玩家脚底方块，栏位取权威选中格，两者都不由客户端提供。
func (engine *Engine) dropSelectedItem(
	session *sessionState,
	pending map[core.ChunkKey]*pendingChunkChanges,
) (RejectReason, bool) {
	if session.player == nil || session.player.lifecycle != PlayerActive {
		return RejectPlayerNotReady, true
	}
	player := session.player
	selected := player.inventory.Hotbar.Selected
	nextHotbar, ok := player.inventory.Hotbar.Consume(selected)
	if !ok {
		return RejectInvalidSlot, true
	}
	stack := player.inventory.Hotbar.Slots[selected]
	position := core.BlockPos{
		X: int32(math.Floor(float64(player.state.Position.X()))),
		Y: int32(math.Floor(float64(player.state.Position.Y()))),
		Z: int32(math.Floor(float64(player.state.Position.Z()))),
	}
	key := core.ChunkKey{Dimension: session.dimension, Pos: position.Chunk()}
	dimension := engine.dimensions[key.Dimension]
	if dimension == nil {
		return RejectChunkNotReady, true
	}
	record := dimension.records[key.Pos]
	if record == nil || record.State != ChunkReady || record.Chunk == nil {
		return RejectChunkNotReady, true
	}
	blockIndex, ok := world.ChunkBlockIndex(position)
	if !ok {
		return RejectChunkNotReady, true
	}
	dropSlot, ok := record.Chunk.PrepareDrop(stack.Item, blockIndex)
	if !ok {
		return RejectDropCapacity, true
	}
	dropped := stack
	dropped.Count = 1
	record.Chunk.CommitDrop(dropSlot, dropped, blockIndex, engine.tunables.PlayerDropPickupDelayTicks)
	player.inventory.Hotbar = nextHotbar
	player.inventoryDirty = true
	engine.touchChunk(key, pending)
	return 0, false
}
