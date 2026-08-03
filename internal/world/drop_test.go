package world_test

import (
	"math"
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

func dropTestChunk(t *testing.T) *world.Chunk {
	t.Helper()
	return world.NewChunk(core.ChunkPos{X: 1, Z: -2})
}

func dropTestIndex(t *testing.T, position core.BlockPos) uint32 {
	t.Helper()
	index, ok := world.ChunkBlockIndex(position)
	if !ok {
		t.Fatalf("方块位置 %+v 没有区块索引", position)
	}
	return index
}

func TestNewChunkStartsWithoutDrops(t *testing.T) {
	chunk := dropTestChunk(t)
	for slot := range core.DropsPerChunk {
		if got := chunk.Drop(slot); got != (world.DropSlot{}) {
			t.Fatalf("槽 %d = %+v，想要零值", slot, got)
		}
	}
}

func TestChunkCommitDropUsesLowestEmptySlot(t *testing.T) {
	chunk := dropTestChunk(t)
	index := dropTestIndex(t, core.BlockPos{X: 16, Y: 3, Z: -32})

	slot, ok := chunk.PrepareDrop(core.ItemStone, index)
	if !ok || slot != 0 {
		t.Fatalf("首个预检 = (%d, %v)，想要槽 0", slot, ok)
	}
	generation := chunk.CommitDrop(slot, core.ItemStone, index, 10)
	if generation != 1 {
		t.Fatalf("首次 generation = %d，想要 1", generation)
	}
	want := world.DropSlot{
		Generation: 1, Active: true,
		Stack:      core.ItemStack{Item: core.ItemStone, Count: 1},
		BlockIndex: index, PickupDelayTicks: 10,
	}
	if got := chunk.Drop(0); got != want {
		t.Fatalf("槽 0 = %+v，想要 %+v", got, want)
	}

	// 不同位置不合并，使用下一个空槽。
	other := dropTestIndex(t, core.BlockPos{X: 17, Y: 3, Z: -32})
	slot, ok = chunk.PrepareDrop(core.ItemStone, other)
	if !ok || slot != 1 {
		t.Fatalf("不同位置预检 = (%d, %v)，想要槽 1", slot, ok)
	}
}

func TestChunkPrepareDropMergesSamePositionAndItem(t *testing.T) {
	chunk := dropTestChunk(t)
	index := dropTestIndex(t, core.BlockPos{X: 16, Y: 3, Z: -32})
	chunk.SetDrop(2, world.DropSlot{
		Generation: 7, Active: true,
		Stack:      core.ItemStack{Item: core.ItemDirt, Count: 63},
		BlockIndex: index,
	})

	slot, ok := chunk.PrepareDrop(core.ItemDirt, index)
	if !ok || slot != 2 {
		t.Fatalf("合并预检 = (%d, %v)，想要槽 2", slot, ok)
	}
	if generation := chunk.CommitDrop(slot, core.ItemDirt, index, 10); generation != 7 {
		t.Fatalf("合并 generation = %d，想要保持 7", generation)
	}
	got := chunk.Drop(2)
	if got.Stack.Count != core.MaxStackCount || got.Generation != 7 {
		t.Fatalf("合并后槽 2 = %+v，想要 64 个泥土且 ID 不变", got)
	}
	// 合并不得改写已生效的拾取延迟。
	if got.PickupDelayTicks != 0 {
		t.Fatalf("合并改写了拾取延迟: %+v", got)
	}
}

func TestChunkPrepareDropSkipsFullAndMismatchedSlots(t *testing.T) {
	chunk := dropTestChunk(t)
	index := dropTestIndex(t, core.BlockPos{X: 16, Y: 3, Z: -32})
	chunk.SetDrop(0, world.DropSlot{
		Generation: 1, Active: true,
		Stack:      core.ItemStack{Item: core.ItemDirt, Count: core.MaxStackCount},
		BlockIndex: index,
	})
	chunk.SetDrop(1, world.DropSlot{
		Generation: 1, Active: true,
		Stack:      core.ItemStack{Item: core.ItemStone, Count: 1},
		BlockIndex: index,
	})

	slot, ok := chunk.PrepareDrop(core.ItemDirt, index)
	if !ok || slot != 2 {
		t.Fatalf("跳过满堆与异类堆的预检 = (%d, %v)，想要槽 2", slot, ok)
	}
}

func TestChunkPrepareDropRejectsWhenFull(t *testing.T) {
	chunk := dropTestChunk(t)
	index := dropTestIndex(t, core.BlockPos{X: 16, Y: 3, Z: -32})
	other := dropTestIndex(t, core.BlockPos{X: 17, Y: 3, Z: -32})
	for slot := range core.DropsPerChunk {
		chunk.SetDrop(slot, world.DropSlot{
			Generation: 1, Active: true,
			Stack:      core.ItemStack{Item: core.ItemStone, Count: core.MaxStackCount},
			BlockIndex: other,
		})
	}
	before := chunk.DropsHash()

	if slot, ok := chunk.PrepareDrop(core.ItemStone, index); ok {
		t.Fatalf("32 槽已满仍返回槽 %d", slot)
	}
	if chunk.DropsHash() != before {
		t.Fatal("预检失败修改了掉落物状态")
	}
}

func TestChunkDropGenerationIncrementsOnReuse(t *testing.T) {
	chunk := dropTestChunk(t)
	index := dropTestIndex(t, core.BlockPos{X: 16, Y: 3, Z: -32})

	if got := chunk.CommitDrop(0, core.ItemStone, index, 10); got != 1 {
		t.Fatalf("首次 generation = %d，想要 1", got)
	}
	chunk.ClearDrop(0)
	if drop := chunk.Drop(0); drop.Active || drop.Generation != 1 {
		t.Fatalf("清空后槽 0 = %+v，想要保留 generation 1 且非活动", drop)
	}
	if got := chunk.CommitDrop(0, core.ItemDirt, index, 10); got != 2 {
		t.Fatalf("复用 generation = %d，想要 2", got)
	}
}

func TestChunkPrepareDropSkipsExhaustedGeneration(t *testing.T) {
	chunk := dropTestChunk(t)
	index := dropTestIndex(t, core.BlockPos{X: 16, Y: 3, Z: -32})
	chunk.SetDrop(0, world.DropSlot{Generation: math.MaxUint32})

	slot, ok := chunk.PrepareDrop(core.ItemStone, index)
	if !ok || slot != 1 {
		t.Fatalf("耗尽 generation 的槽仍被复用: (%d, %v)", slot, ok)
	}
}

func TestChunkPrepareDropRejectsUnknownItem(t *testing.T) {
	chunk := dropTestChunk(t)
	index := dropTestIndex(t, core.BlockPos{X: 16, Y: 3, Z: -32})
	for _, item := range []core.ItemID{core.ItemNone, core.ItemID(4242)} {
		if slot, ok := chunk.PrepareDrop(item, index); ok {
			t.Fatalf("未注册物品 %d 得到槽 %d", item, slot)
		}
	}
}

func TestChunkCloneCopiesDropsIndependently(t *testing.T) {
	chunk := dropTestChunk(t)
	index := dropTestIndex(t, core.BlockPos{X: 16, Y: 3, Z: -32})
	chunk.CommitDrop(0, core.ItemStone, index, 10)

	clone := chunk.Clone()
	if clone.Drop(0) != chunk.Drop(0) {
		t.Fatalf("Clone 未复制掉落物: %+v", clone.Drop(0))
	}
	clone.ClearDrop(0)
	if !chunk.Drop(0).Active {
		t.Fatal("修改克隆影响了原区块掉落物")
	}
}

func TestChunkDropsHashCoversEverySlotField(t *testing.T) {
	index := dropTestIndex(t, core.BlockPos{X: 16, Y: 3, Z: -32})
	other := dropTestIndex(t, core.BlockPos{X: 17, Y: 3, Z: -32})
	base := world.DropSlot{
		Generation: 3, Active: true,
		Stack:      core.ItemStack{Item: core.ItemStone, Count: 5},
		BlockIndex: index, AgeTicks: 11, PickupDelayTicks: 7,
	}
	mutations := []struct {
		name  string
		apply func(*world.DropSlot)
	}{
		{"generation", func(slot *world.DropSlot) { slot.Generation = 4 }},
		{"active", func(slot *world.DropSlot) { slot.Active = false }},
		{"item", func(slot *world.DropSlot) { slot.Stack.Item = core.ItemDirt }},
		{"count", func(slot *world.DropSlot) { slot.Stack.Count = 6 }},
		{"blockIndex", func(slot *world.DropSlot) { slot.BlockIndex = other }},
		{"age", func(slot *world.DropSlot) { slot.AgeTicks = 12 }},
		{"pickupDelay", func(slot *world.DropSlot) { slot.PickupDelayTicks = 8 }},
	}
	chunk := dropTestChunk(t)
	chunk.SetDrop(1, base)
	baseline := chunk.DropsHash()
	for _, mutation := range mutations {
		mutated := base
		mutation.apply(&mutated)
		other := dropTestChunk(t)
		other.SetDrop(1, mutated)
		if other.DropsHash() == baseline {
			t.Fatalf("修改 %s 没有改变掉落物哈希", mutation.name)
		}
	}
}

func TestChunkDropsHashIsSlotPositional(t *testing.T) {
	index := dropTestIndex(t, core.BlockPos{X: 16, Y: 3, Z: -32})
	drop := world.DropSlot{
		Generation: 1, Active: true,
		Stack:      core.ItemStack{Item: core.ItemStone, Count: 1},
		BlockIndex: index,
	}
	first := dropTestChunk(t)
	first.SetDrop(0, drop)
	second := dropTestChunk(t)
	second.SetDrop(1, drop)
	if first.DropsHash() == second.DropsHash() {
		t.Fatal("不同槽位的相同掉落物产生了相同哈希")
	}
}

func TestChunkHashIgnoresDrops(t *testing.T) {
	chunk := dropTestChunk(t)
	before := chunk.Hash()
	chunk.CommitDrop(0, core.ItemStone, dropTestIndex(t, core.BlockPos{X: 16, Y: 3, Z: -32}), 10)
	if chunk.Hash() != before {
		t.Fatal("Chunk.Hash 必须保持只由逻辑方块决定")
	}
}

func TestChunkPayloadBytesCoversDropSlots(t *testing.T) {
	chunk := dropTestChunk(t)
	if got, want := chunk.PayloadBytes(), core.DropsPerChunk*world.DropSlotBytes; got <= want {
		t.Fatalf("PayloadBytes = %d，必须覆盖 %d 字节固定掉落物负载", got, want)
	}
}
