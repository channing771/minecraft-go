package world

import (
	"crypto/sha256"
	"encoding/binary"
	"math"

	"minecraft-go/internal/core"
)

// DropSlotBytes 是单个掉落物槽的固定线上/存档编码长度。
// schema v5 起物品堆包含 Item、Count 与 Durability。
const DropSlotBytes = 4 + 1 + 2 + 1 + 2 + 4 + 4 + 1

// DropSlot 是区块中的一个固定掉落物槽。
// 槽始终保留 Generation，Active 为 false 时其余字段无意义。
type DropSlot struct {
	Generation       uint32
	Active           bool
	Stack            core.ItemStack
	BlockIndex       uint32
	AgeTicks         uint32
	PickupDelayTicks uint8
}

// Drop 返回指定槽的当前值；槽位越界时返回零值。
func (c *Chunk) Drop(slot int) DropSlot {
	if slot < 0 || slot >= core.DropsPerChunk {
		return DropSlot{}
	}
	return c.drops[slot]
}

// SetDrop 直接写入一个槽，供存档恢复与权威 tick 更新使用。
func (c *Chunk) SetDrop(slot int, value DropSlot) {
	if slot < 0 || slot >= core.DropsPerChunk {
		return
	}
	c.drops[slot] = value
}

// ClearDrop 停用一个槽并保留其 generation，使旧 ID 不会被重复分配。
func (c *Chunk) ClearDrop(slot int) {
	if slot < 0 || slot >= core.DropsPerChunk {
		return
	}
	c.drops[slot] = DropSlot{Generation: c.drops[slot].Generation}
}

// PrepareDrop 预检可接收一个 item 的槽：先找同物品、同方块位置的最低未满堆，
// 否则用最低的可复用空槽。它不修改区块，因此调用方可以先预检再原子提交。
func (c *Chunk) PrepareDrop(item core.ItemID, blockIndex uint32) (int, bool) {
	return prepareDropSlot(c.drops, item, blockIndex)
}

// CommitDrop 把一个物品写入 PrepareDrop 返回的槽并返回该堆的 generation。
// 合并保留 ID、generation 和年龄，PickupDelayTicks 取 old 与 incoming 的较大值；
// 启用空槽时 generation 加一。
func (c *Chunk) CommitDrop(
	slot int,
	stack core.ItemStack,
	blockIndex uint32,
	pickupDelay uint8,
) uint32 {
	if slot < 0 || slot >= core.DropsPerChunk || stack.Count != 1 || !stack.Valid() {
		return 0
	}
	drop := c.drops[slot]
	if drop.Active {
		drop.Stack.Count++
		// 合并保留 ID、generation 与既有寿命，只把拾取禁止窗口延长到较长的来源延迟。
		drop.PickupDelayTicks = max(drop.PickupDelayTicks, pickupDelay)
		c.drops[slot] = drop
		return drop.Generation
	}
	c.drops[slot] = DropSlot{
		Generation:       drop.Generation + 1,
		Active:           true,
		Stack:            stack,
		BlockIndex:       blockIndex,
		PickupDelayTicks: pickupDelay,
	}
	return c.drops[slot].Generation
}

// DropsHash 返回只由固定槽顺序与槽字段决定的稳定 SHA-256。
func (c *Chunk) DropsHash() [sha256.Size]byte {
	hash := sha256.New()
	var encoded [DropSlotBytes]byte
	for slot := range c.drops {
		drop := c.drops[slot]
		binary.LittleEndian.PutUint32(encoded[0:], drop.Generation)
		encoded[4] = 0
		if drop.Active {
			encoded[4] = 1
		}
		binary.LittleEndian.PutUint16(encoded[5:], uint16(drop.Stack.Item))
		encoded[7] = drop.Stack.Count
		binary.LittleEndian.PutUint16(encoded[8:], drop.Stack.Durability)
		binary.LittleEndian.PutUint32(encoded[10:], drop.BlockIndex)
		binary.LittleEndian.PutUint32(encoded[14:], drop.AgeTicks)
		encoded[18] = drop.PickupDelayTicks
		_, _ = hash.Write(encoded[:])
	}
	var sum [sha256.Size]byte
	hash.Sum(sum[:0])
	return sum
}

// maxDropBatchStacks 是一次批量掉落预演可接受的固定编译期堆数上限。
// 它取两类调用方最坏情形的较大者：破坏容器是容器本体加其全部格子
// （箱子为 1+core.ChestSlots = 28），玩家死亡是 core.InventorySlots = 36 个物品栏格。
// 用 max 表达而不是写死数字，任一侧的格数变化都会自动被覆盖。
const maxDropBatchStacks = max(1+core.ChestSlots, core.InventorySlots)

// PrepareDropBatch 在掉落物数组副本上按顺序完整放入调用方提供的堆，
// 调用方必须传入自身固定数组的切片；堆数超过 maxDropBatchStacks
// 或任何一堆放不下都返回 false 且不修改区块，供破坏熔炉、箱子与死亡掉落等原子操作预演。
func (c *Chunk) PrepareDropBatch(
	stacks []core.ItemStack,
	blockIndex uint32,
	pickupDelay uint8,
) ([core.DropsPerChunk]DropSlot, bool) {
	if len(stacks) > maxDropBatchStacks {
		return c.drops, false
	}
	next := c.drops
	for _, stack := range stacks {
		if stack == (core.ItemStack{}) {
			continue
		}
		if !stack.Valid() {
			return c.drops, false
		}
		limit, _ := core.ItemStackLimit(stack.Item)
		remaining := stack.Count
		for remaining > 0 {
			slot, ok := prepareDropSlot(next, stack.Item, blockIndex)
			if !ok {
				return c.drops, false
			}
			drop := next[slot]
			if drop.Active {
				space := limit - drop.Stack.Count
				taken := min(space, remaining)
				drop.Stack.Count += taken
				// 与单件合并同一规则：保留既有寿命，延长到较长的来源延迟。
				drop.PickupDelayTicks = max(drop.PickupDelayTicks, pickupDelay)
				remaining -= taken
				next[slot] = drop
				continue
			}
			taken := min(limit, remaining)
			incoming := stack
			incoming.Count = taken
			next[slot] = DropSlot{
				Generation:       drop.Generation + 1,
				Active:           true,
				Stack:            incoming,
				BlockIndex:       blockIndex,
				PickupDelayTicks: pickupDelay,
			}
			remaining -= taken
		}
	}
	return next, true
}

// CommitDropBatch 原子写入 PrepareDropBatch 预演出的完整掉落物数组。
func (c *Chunk) CommitDropBatch(next [core.DropsPerChunk]DropSlot) {
	c.drops = next
}

// prepareDropSlot 是选槽的唯一实现：PrepareDrop 与 PrepareDropBatch 都复用它，
// 避免两处各自硬编码堆叠上限而彼此走样。
func prepareDropSlot(
	drops [core.DropsPerChunk]DropSlot,
	item core.ItemID,
	blockIndex uint32,
) (int, bool) {
	// 合并必须遵守该物品自己的单格上限：工具上限为 1，因此永不合并。
	limit, ok := core.ItemStackLimit(item)
	if !ok {
		return 0, false
	}
	for slot := range drops {
		drop := drops[slot]
		if drop.Active && drop.Stack.Item == item && drop.BlockIndex == blockIndex &&
			drop.Stack.Count < limit {
			return slot, true
		}
	}
	for slot := range drops {
		if !drops[slot].Active && drops[slot].Generation != math.MaxUint32 {
			return slot, true
		}
	}
	return 0, false
}
