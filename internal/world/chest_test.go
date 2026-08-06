package world_test

import (
	"math"
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

func TestPrepareChestUsesLowestReusableSlot(t *testing.T) {
	chunk := world.NewChunk(core.ChunkPos{})
	index := furnaceChunkIndex(t, chunk.Pos, 1, 2, 3)

	slot, ok := chunk.PrepareChest(index)
	if !ok || slot != 0 {
		t.Fatalf("首个箱子槽 = %d, %v，想要 0", slot, ok)
	}
	generation := chunk.CommitChest(slot, index)
	if generation != 1 {
		t.Fatalf("首次 generation = %d，想要 1", generation)
	}
	chest := chunk.Chest(slot)
	if !chest.Active || chest.BlockIndex != index || chest.Generation != 1 {
		t.Fatalf("启用后槽 = %+v", chest)
	}
	if chest.Items != ([core.ChestSlots]core.ItemStack{}) {
		t.Fatalf("新箱子的 27 格不是全空: %+v", chest.Items)
	}

	// 同一位置已有活动箱子时必须拒绝。
	if _, ok := chunk.PrepareChest(index); ok {
		t.Fatal("同一方块位置分配了第二个箱子")
	}
	// 下一个不同位置使用次低索引。
	next := furnaceChunkIndex(t, chunk.Pos, 4, 2, 5)
	if slot, ok := chunk.PrepareChest(next); !ok || slot != 1 {
		t.Fatalf("第二个箱子槽 = %d, %v，想要 1", slot, ok)
	}
}

func TestPrepareChestRejectsSeventeenth(t *testing.T) {
	chunk := world.NewChunk(core.ChunkPos{})
	for slot := range core.ChestsPerChunk {
		index := furnaceChunkIndex(t, chunk.Pos, int32(slot%16), 2, int32(slot/16))
		got, ok := chunk.PrepareChest(index)
		if !ok || got != slot {
			t.Fatalf("第 %d 个箱子槽 = %d, %v", slot, got, ok)
		}
		chunk.CommitChest(got, index)
	}
	if _, ok := chunk.PrepareChest(furnaceChunkIndex(t, chunk.Pos, 15, 9, 15)); ok {
		t.Fatal("第 17 个箱子未被拒绝")
	}
}

func TestChestGenerationGuardsReuse(t *testing.T) {
	chunk := world.NewChunk(core.ChunkPos{})
	index := furnaceChunkIndex(t, chunk.Pos, 1, 2, 3)
	slot, _ := chunk.PrepareChest(index)
	chunk.CommitChest(slot, index)
	chunk.SetChest(slot, world.ChestSlot{
		Generation: 1, Active: true, BlockIndex: index,
		Items: [core.ChestSlots]core.ItemStack{
			{Item: core.ItemStone, Count: 12},
		},
	})

	chunk.DeactivateChest(slot)
	stopped := chunk.Chest(slot)
	if stopped.Active || stopped.Generation != 1 {
		t.Fatalf("停用后槽 = %+v，想要保留 generation 1", stopped)
	}
	if stopped.BlockIndex != 0 || stopped.Items != ([core.ChestSlots]core.ItemStack{}) {
		t.Fatalf("停用槽残留字段: %+v", stopped)
	}

	// 复用同一槽必须递增 generation，使旧引用失效。
	again, ok := chunk.PrepareChest(index)
	if !ok || again != slot {
		t.Fatalf("复用槽 = %d, %v", again, ok)
	}
	if got := chunk.CommitChest(again, index); got != 2 {
		t.Fatalf("复用后 generation = %d，想要 2", got)
	}

	// generation 耗尽的槽不再复用。
	chunk.SetChest(5, world.ChestSlot{Generation: math.MaxUint32})
	exhausted := furnaceChunkIndex(t, chunk.Pos, 7, 2, 7)
	if got, ok := chunk.PrepareChest(exhausted); ok && got == 5 {
		t.Fatal("generation 耗尽的槽被复用")
	}
}

func TestChestAtFindsActiveSlotByBlockIndex(t *testing.T) {
	chunk := world.NewChunk(core.ChunkPos{})
	index := furnaceChunkIndex(t, chunk.Pos, 2, 3, 4)
	slot, _ := chunk.PrepareChest(index)
	chunk.CommitChest(slot, index)

	got, ok := chunk.ChestAt(index)
	if !ok || got != slot {
		t.Fatalf("ChestAt = %d, %v，想要 %d", got, ok, slot)
	}
	if _, ok := chunk.ChestAt(furnaceChunkIndex(t, chunk.Pos, 9, 9, 9)); ok {
		t.Fatal("空位置报告存在箱子")
	}
	chunk.DeactivateChest(slot)
	if _, ok := chunk.ChestAt(index); ok {
		t.Fatal("停用后仍能按位置找到箱子")
	}
}

// TestChestSlotBlockIndexMatchesChestBlock 覆盖活动箱子槽的方块索引与
// 实际写入的箱子方块一一对应：调用方（sim 层）把方块写为箱子、把槽提交在同一
// 索引上后，两条真相在该索引上完全一致。
func TestChestSlotBlockIndexMatchesChestBlock(t *testing.T) {
	chunk := world.NewChunk(core.ChunkPos{})
	index := furnaceChunkIndex(t, chunk.Pos, 6, 4, 6)
	chunk.SetBlock(6, 4, 6, core.ChestID)

	slot, ok := chunk.PrepareChest(index)
	if !ok {
		t.Fatal("箱子方块位置未能分配箱子槽")
	}
	chunk.CommitChest(slot, index)

	if chunk.BlockAt(6, 4, 6) != core.ChestID {
		t.Fatal("箱子槽对应位置的方块不是箱子")
	}
	got, ok := chunk.ChestAt(index)
	if !ok || got != slot {
		t.Fatalf("ChestAt = %d, %v，想要 %d", got, ok, slot)
	}
}

func TestChunkCloneAndPayloadIncludeChests(t *testing.T) {
	chunk := world.NewChunk(core.ChunkPos{})
	index := furnaceChunkIndex(t, chunk.Pos, 1, 2, 3)
	slot, _ := chunk.PrepareChest(index)
	chunk.CommitChest(slot, index)
	items := [core.ChestSlots]core.ItemStack{}
	items[0] = core.ItemStack{Item: core.ItemStone, Count: 8}
	items[26] = core.ItemStack{Item: core.ItemIronPickaxe, Count: 1, Durability: 42}
	chunk.SetChest(slot, world.ChestSlot{
		Generation: 1, Active: true, BlockIndex: index, Items: items,
	})

	clone := chunk.Clone()
	if clone.Chest(slot) != chunk.Chest(slot) {
		t.Fatal("Clone 未复制箱子状态")
	}
	clone.SetChest(slot, world.ChestSlot{})
	if chunk.Chest(slot).Items[0].Count != 8 {
		t.Fatal("Clone 与原区块共享箱子数组")
	}

	empty := world.NewChunk(core.ChunkPos{})
	if chunk.PayloadBytes() != empty.PayloadBytes() {
		t.Fatal("箱子负载不是固定长度")
	}
	if empty.PayloadBytes() <= core.FurnacesPerChunk*world.FurnaceSlotBytes+
		core.ChestsPerChunk*world.ChestSlotBytes {
		t.Fatal("PayloadBytes 未计入固定箱子槽")
	}
}

func TestChunkHashStillCoversOnlyBlocksWithChest(t *testing.T) {
	chunk := world.NewChunk(core.ChunkPos{})
	before := chunk.Hash()
	index := furnaceChunkIndex(t, chunk.Pos, 1, 2, 3)
	slot, _ := chunk.PrepareChest(index)
	chunk.CommitChest(slot, index)
	if chunk.Hash() != before {
		t.Fatal("箱子状态影响了只表示方块的 Hash")
	}
}

// TestPrepareDropBatchAcceptsWorstCaseChestBatch 覆盖箱子破坏的最坏情形：
// 本体加 27 个满格，共 28 堆，且逐堆都已在上限 64，彼此不能合并进同一槽，
// 必须占满 28 个独立掉落物槽，仍在 32 个掉落物槽容量内。
func TestPrepareDropBatchAcceptsWorstCaseChestBatch(t *testing.T) {
	chunk := world.NewChunk(core.ChunkPos{})
	index := furnaceChunkIndex(t, chunk.Pos, 1, 2, 3)
	stacks := make([]core.ItemStack, 1+core.ChestSlots)
	for i := range stacks {
		stacks[i] = core.ItemStack{Item: core.ItemStone, Count: core.MaxStackCount}
	}

	next, ok := chunk.PrepareDropBatch(stacks, index, 10)
	if !ok {
		t.Fatal("最坏情形的 28 堆掉落被拒绝")
	}
	active := 0
	for slot := range core.DropsPerChunk {
		if next[slot].Active {
			active++
			if next[slot].Stack.Count != core.MaxStackCount {
				t.Fatalf("槽 %d 未满: %+v", slot, next[slot])
			}
		}
	}
	if active != len(stacks) {
		t.Fatalf("活动掉落槽数 = %d，想要 %d", active, len(stacks))
	}
}

// TestPrepareDropBatchWorstCaseFailureLeavesBytesUnchanged 覆盖任一堆放不下时
// 区块字节完全不变：预先占用若干槽使可用槽不足 28 个，批量预演必须整体失败
// 且不得修改任何已提交的掉落物。
func TestPrepareDropBatchWorstCaseFailureLeavesBytesUnchanged(t *testing.T) {
	chunk := world.NewChunk(core.ChunkPos{})
	elsewhere := furnaceChunkIndex(t, chunk.Pos, 9, 5, 9)
	// 占用 5 个槽，只剩 27 个可复用槽，不足以容纳 28 堆互不合并的物品。
	for slot := 0; slot < 5; slot++ {
		chunk.SetDrop(slot, world.DropSlot{
			Generation: 1, Active: true,
			Stack:      core.ItemStack{Item: core.ItemCoal, Count: core.MaxStackCount},
			BlockIndex: elsewhere,
		})
	}
	before := chunk.DropsHash()

	index := furnaceChunkIndex(t, chunk.Pos, 1, 2, 3)
	stacks := make([]core.ItemStack, 1+core.ChestSlots)
	for i := range stacks {
		stacks[i] = core.ItemStack{Item: core.ItemStone, Count: core.MaxStackCount}
	}
	if _, ok := chunk.PrepareDropBatch(stacks, index, 10); ok {
		t.Fatal("容量不足的最坏批量仍被接受")
	}
	if chunk.DropsHash() != before {
		t.Fatal("失败的最坏批量预演修改了掉落物")
	}
}

// TestPrepareDropBatchRejectsOversizedSlice 覆盖超过 1+core.ChestSlots 个堆时
// 必须原子拒绝而不是 panic 或修改区块。
func TestPrepareDropBatchRejectsOversizedSlice(t *testing.T) {
	chunk := world.NewChunk(core.ChunkPos{})
	before := chunk.DropsHash()
	index := furnaceChunkIndex(t, chunk.Pos, 1, 2, 3)
	stacks := make([]core.ItemStack, 1+core.ChestSlots+1)
	for i := range stacks {
		stacks[i] = core.ItemStack{Item: core.ItemStone, Count: 1}
	}
	if _, ok := chunk.PrepareDropBatch(stacks, index, 10); ok {
		t.Fatal("超过上限的堆数量仍被接受")
	}
	if chunk.DropsHash() != before {
		t.Fatal("超过上限的批量预演修改了掉落物")
	}
}
