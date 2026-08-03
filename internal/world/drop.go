package world

import (
	"crypto/sha256"
	"encoding/binary"
	"math"

	"minecraft-go/internal/core"
)

// DropSlotBytes 是单个掉落物槽的固定线上/存档编码长度。
const DropSlotBytes = 4 + 1 + 2 + 1 + 4 + 4 + 1

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
	if _, ok := core.ItemPlacement(item); !ok {
		return 0, false
	}
	for slot := range c.drops {
		drop := c.drops[slot]
		if drop.Active && drop.Stack.Item == item && drop.BlockIndex == blockIndex &&
			drop.Stack.Count < core.MaxStackCount {
			return slot, true
		}
	}
	for slot := range c.drops {
		if !c.drops[slot].Active && c.drops[slot].Generation != math.MaxUint32 {
			return slot, true
		}
	}
	return 0, false
}

// CommitDrop 把一个物品写入 PrepareDrop 返回的槽并返回该堆的 generation。
// 合并到已有堆时保留原 generation 与拾取延迟，启用空槽时 generation 加一。
func (c *Chunk) CommitDrop(
	slot int,
	item core.ItemID,
	blockIndex uint32,
	pickupDelay uint8,
) uint32 {
	if slot < 0 || slot >= core.DropsPerChunk {
		return 0
	}
	drop := c.drops[slot]
	if drop.Active {
		drop.Stack.Count++
		c.drops[slot] = drop
		return drop.Generation
	}
	c.drops[slot] = DropSlot{
		Generation:       drop.Generation + 1,
		Active:           true,
		Stack:            core.ItemStack{Item: item, Count: 1},
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
		binary.LittleEndian.PutUint32(encoded[8:], drop.BlockIndex)
		binary.LittleEndian.PutUint32(encoded[12:], drop.AgeTicks)
		encoded[16] = drop.PickupDelayTicks
		_, _ = hash.Write(encoded[:])
	}
	var sum [sha256.Size]byte
	hash.Sum(sum[:0])
	return sum
}
