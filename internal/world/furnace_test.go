package world_test

import (
	"math"
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

// furnaceChunkIndex 返回区块内某个局部坐标的方块索引。
func furnaceChunkIndex(t *testing.T, pos core.ChunkPos, lx int32, y int32, lz int32) uint32 {
	t.Helper()
	index, ok := world.ChunkBlockIndex(core.BlockPos{
		X: pos.X<<core.SectionShift + lx, Y: y, Z: pos.Z<<core.SectionShift + lz,
	})
	if !ok {
		t.Fatalf("局部坐标 (%d,%d,%d) 没有区块索引", lx, y, lz)
	}
	return index
}

func TestPrepareFurnaceUsesLowestReusableSlot(t *testing.T) {
	chunk := world.NewChunk(core.ChunkPos{})
	index := furnaceChunkIndex(t, chunk.Pos, 1, 2, 3)

	slot, ok := chunk.PrepareFurnace(index)
	if !ok || slot != 0 {
		t.Fatalf("首个熔炉槽 = %d, %v，想要 0", slot, ok)
	}
	generation := chunk.CommitFurnace(slot, index)
	if generation != 1 {
		t.Fatalf("首次 generation = %d，想要 1", generation)
	}
	furnace := chunk.Furnace(slot)
	if !furnace.Active || furnace.BlockIndex != index || furnace.Generation != 1 {
		t.Fatalf("启用后槽 = %+v", furnace)
	}
	if furnace.Input != (core.ItemStack{}) || furnace.Fuel != (core.ItemStack{}) ||
		furnace.Output != (core.ItemStack{}) ||
		furnace.ProgressTicks != 0 || furnace.BurnTicks != 0 {
		t.Fatalf("新熔炉不是空状态: %+v", furnace)
	}

	// 同一位置已有活动熔炉时必须拒绝。
	if _, ok := chunk.PrepareFurnace(index); ok {
		t.Fatal("同一方块位置分配了第二个熔炉")
	}
	// 下一个不同位置使用次低索引。
	next := furnaceChunkIndex(t, chunk.Pos, 4, 2, 5)
	if slot, ok := chunk.PrepareFurnace(next); !ok || slot != 1 {
		t.Fatalf("第二个熔炉槽 = %d, %v，想要 1", slot, ok)
	}
}

func TestPrepareFurnaceRejectsThirtyThird(t *testing.T) {
	chunk := world.NewChunk(core.ChunkPos{})
	for slot := range core.FurnacesPerChunk {
		index := furnaceChunkIndex(t, chunk.Pos, int32(slot%16), 2, int32(slot/16))
		got, ok := chunk.PrepareFurnace(index)
		if !ok || got != slot {
			t.Fatalf("第 %d 个熔炉槽 = %d, %v", slot, got, ok)
		}
		chunk.CommitFurnace(got, index)
	}
	if _, ok := chunk.PrepareFurnace(furnaceChunkIndex(t, chunk.Pos, 15, 9, 15)); ok {
		t.Fatal("第 33 个熔炉未被拒绝")
	}
}

func TestFurnaceGenerationGuardsReuse(t *testing.T) {
	chunk := world.NewChunk(core.ChunkPos{})
	index := furnaceChunkIndex(t, chunk.Pos, 1, 2, 3)
	slot, _ := chunk.PrepareFurnace(index)
	chunk.CommitFurnace(slot, index)

	chunk.DeactivateFurnace(slot)
	stopped := chunk.Furnace(slot)
	if stopped.Active || stopped.Generation != 1 {
		t.Fatalf("停用后槽 = %+v，想要保留 generation 1", stopped)
	}
	if stopped.BlockIndex != 0 || stopped.Input != (core.ItemStack{}) ||
		stopped.ProgressTicks != 0 || stopped.BurnTicks != 0 {
		t.Fatalf("停用槽残留字段: %+v", stopped)
	}

	// 复用同一槽必须递增 generation，使旧引用失效。
	again, ok := chunk.PrepareFurnace(index)
	if !ok || again != slot {
		t.Fatalf("复用槽 = %d, %v", again, ok)
	}
	if got := chunk.CommitFurnace(again, index); got != 2 {
		t.Fatalf("复用后 generation = %d，想要 2", got)
	}

	// generation 耗尽的槽不再复用。
	chunk.SetFurnace(5, world.FurnaceSlot{Generation: math.MaxUint32})
	exhausted := furnaceChunkIndex(t, chunk.Pos, 7, 2, 7)
	if got, ok := chunk.PrepareFurnace(exhausted); ok && got == 5 {
		t.Fatal("generation 耗尽的槽被复用")
	}
}

func TestFurnaceAtFindsActiveSlotByBlockIndex(t *testing.T) {
	chunk := world.NewChunk(core.ChunkPos{})
	index := furnaceChunkIndex(t, chunk.Pos, 2, 3, 4)
	slot, _ := chunk.PrepareFurnace(index)
	chunk.CommitFurnace(slot, index)

	got, ok := chunk.FurnaceAt(index)
	if !ok || got != slot {
		t.Fatalf("FurnaceAt = %d, %v，想要 %d", got, ok, slot)
	}
	if _, ok := chunk.FurnaceAt(furnaceChunkIndex(t, chunk.Pos, 9, 9, 9)); ok {
		t.Fatal("空位置报告存在熔炉")
	}
	chunk.DeactivateFurnace(slot)
	if _, ok := chunk.FurnaceAt(index); ok {
		t.Fatal("停用后仍能按位置找到熔炉")
	}
}

func TestChunkCloneAndPayloadIncludeFurnaces(t *testing.T) {
	chunk := world.NewChunk(core.ChunkPos{})
	index := furnaceChunkIndex(t, chunk.Pos, 1, 2, 3)
	slot, _ := chunk.PrepareFurnace(index)
	chunk.CommitFurnace(slot, index)
	chunk.SetFurnace(slot, world.FurnaceSlot{
		Generation: 1, Active: true, BlockIndex: index,
		Input:         core.ItemStack{Item: core.ItemRawIron, Count: 3},
		Fuel:          core.ItemStack{Item: core.ItemCoal, Count: 2},
		Output:        core.ItemStack{Item: core.ItemIronIngot, Count: 1},
		ProgressTicks: 137, BurnTicks: 1463,
	})

	clone := chunk.Clone()
	if clone.Furnace(slot) != chunk.Furnace(slot) {
		t.Fatal("Clone 未复制熔炉状态")
	}
	clone.SetFurnace(slot, world.FurnaceSlot{})
	if chunk.Furnace(slot).ProgressTicks != 137 {
		t.Fatal("Clone 与原区块共享熔炉数组")
	}

	empty := world.NewChunk(core.ChunkPos{})
	if chunk.PayloadBytes() != empty.PayloadBytes() {
		t.Fatal("熔炉负载不是固定长度")
	}
	if empty.PayloadBytes() <= core.FurnacesPerChunk*world.FurnaceSlotBytes {
		t.Fatal("PayloadBytes 未计入固定熔炉槽")
	}
}

func TestChunkHashStillCoversOnlyBlocks(t *testing.T) {
	chunk := world.NewChunk(core.ChunkPos{})
	before := chunk.Hash()
	index := furnaceChunkIndex(t, chunk.Pos, 1, 2, 3)
	slot, _ := chunk.PrepareFurnace(index)
	chunk.CommitFurnace(slot, index)
	if chunk.Hash() != before {
		t.Fatal("熔炉状态影响了只表示方块的 Hash")
	}
}

func TestPrepareDropBatchCommitsAllOrNothing(t *testing.T) {
	chunk := world.NewChunk(core.ChunkPos{})
	index := furnaceChunkIndex(t, chunk.Pos, 1, 2, 3)
	stacks := [4]core.ItemStack{
		{Item: core.ItemFurnace, Count: 1},
		{Item: core.ItemRawIron, Count: 3},
		{Item: core.ItemCoal, Count: 2},
		{Item: core.ItemIronIngot, Count: 1},
	}

	next, ok := chunk.PrepareDropBatch(stacks, index, 10)
	if !ok {
		t.Fatal("空区块无法容纳四堆掉落物")
	}
	if chunk.Drop(0).Active {
		t.Fatal("PrepareDropBatch 修改了区块")
	}
	chunk.CommitDropBatch(next)

	seen := map[core.ItemID]uint8{}
	for slot := range core.DropsPerChunk {
		drop := chunk.Drop(slot)
		if drop.Active {
			seen[drop.Stack.Item] += drop.Stack.Count
		}
	}
	for _, stack := range stacks {
		if seen[stack.Item] != stack.Count {
			t.Fatalf("物品 %d 掉落 %d，想要 %d", stack.Item, seen[stack.Item], stack.Count)
		}
	}
}

func TestPrepareDropBatchFailsWithoutMutating(t *testing.T) {
	chunk := world.NewChunk(core.ChunkPos{})
	elsewhere := furnaceChunkIndex(t, chunk.Pos, 9, 5, 9)
	for slot := range core.DropsPerChunk {
		chunk.SetDrop(slot, world.DropSlot{
			Generation: 1, Active: true,
			Stack:      core.ItemStack{Item: core.ItemStone, Count: core.MaxStackCount},
			BlockIndex: elsewhere,
		})
	}
	before := chunk.DropsHash()

	index := furnaceChunkIndex(t, chunk.Pos, 1, 2, 3)
	stacks := [4]core.ItemStack{{Item: core.ItemFurnace, Count: 1}}
	if _, ok := chunk.PrepareDropBatch(stacks, index, 10); ok {
		t.Fatal("满掉落物槽仍接受批量掉落")
	}
	if chunk.DropsHash() != before {
		t.Fatal("失败的批量预演修改了掉落物")
	}
}

func TestPrepareDropBatchMergesAndIgnoresEmptyStacks(t *testing.T) {
	chunk := world.NewChunk(core.ChunkPos{})
	index := furnaceChunkIndex(t, chunk.Pos, 1, 2, 3)
	chunk.SetDrop(0, world.DropSlot{
		Generation: 4, Active: true,
		Stack:      core.ItemStack{Item: core.ItemCoal, Count: 60},
		BlockIndex: index,
	})

	stacks := [4]core.ItemStack{
		{Item: core.ItemCoal, Count: 4},
		{},
		{Item: core.ItemRawIron, Count: 1},
		{},
	}
	next, ok := chunk.PrepareDropBatch(stacks, index, 0)
	if !ok {
		t.Fatal("可合并的批量掉落被拒绝")
	}
	chunk.CommitDropBatch(next)
	if got := chunk.Drop(0); got.Stack.Count != 64 || got.Generation != 4 {
		t.Fatalf("同物品同位置未合并: %+v", got)
	}
	if got := chunk.Drop(1); !got.Active || got.Stack.Item != core.ItemRawIron {
		t.Fatalf("第二堆未落到次低空槽: %+v", got)
	}
	for slot := 2; slot < core.DropsPerChunk; slot++ {
		if chunk.Drop(slot).Active {
			t.Fatalf("空 stack 占用了槽 %d", slot)
		}
	}
}
