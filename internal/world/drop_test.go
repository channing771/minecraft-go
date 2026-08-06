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
	generation := chunk.CommitDrop(slot, core.ItemStack{Item: core.ItemStone, Count: 1}, index, 10)
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

func TestChunkCommitDropPreservesToolDurability(t *testing.T) {
	chunk := dropTestChunk(t)
	index := dropTestIndex(t, core.BlockPos{X: 16, Y: 3, Z: -32})
	worn := core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: 73}
	slot, ok := chunk.PrepareDrop(worn.Item, index)
	if !ok {
		t.Fatal("磨损工具没有可用掉落物槽")
	}
	chunk.CommitDrop(slot, worn, index, 10)
	if got := chunk.Drop(slot).Stack; got != worn {
		t.Fatalf("掉落物堆 = %+v，想要保留来源工具 %+v", got, worn)
	}
}

func TestChunkCommitDropRejectsInvalidStacks(t *testing.T) {
	index := dropTestIndex(t, core.BlockPos{X: 16, Y: 3, Z: -32})
	for _, stack := range []core.ItemStack{
		{},
		{Item: core.ItemStone, Count: 2},
		{Item: core.ItemStonePickaxe, Count: 1},
		{Item: core.ItemStone, Count: 1, Durability: 1},
		{Item: core.ItemID(4242), Count: 1},
	} {
		chunk := dropTestChunk(t)
		before := chunk.DropsHash()
		if generation := chunk.CommitDrop(0, stack, index, 10); generation != 0 || chunk.DropsHash() != before {
			t.Fatalf("非法堆 %+v 被提交: generation=%d drop=%+v", stack, generation, chunk.Drop(0))
		}
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
	if generation := chunk.CommitDrop(slot, core.ItemStack{Item: core.ItemDirt, Count: 1}, index, 10); generation != 7 {
		t.Fatalf("合并 generation = %d，想要保持 7", generation)
	}
	got := chunk.Drop(2)
	if got.Stack.Count != core.MaxStackCount || got.Generation != 7 {
		t.Fatalf("合并后槽 2 = %+v，想要 64 个泥土且 ID 不变", got)
	}
	// 合并把拾取禁止窗口延长到较长的来源延迟；旧值为 0 时取新来源的 10。
	// 完整的短/等/长来源矩阵见 TestChunkCommitDropMergeUsesLongerPickupDelay。
	if got.PickupDelayTicks != 10 {
		t.Fatalf("合并后的拾取延迟 = %+v，想要 10", got)
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

	if got := chunk.CommitDrop(0, core.ItemStack{Item: core.ItemStone, Count: 1}, index, 10); got != 1 {
		t.Fatalf("首次 generation = %d，想要 1", got)
	}
	chunk.ClearDrop(0)
	if drop := chunk.Drop(0); drop.Active || drop.Generation != 1 {
		t.Fatalf("清空后槽 0 = %+v，想要保留 generation 1 且非活动", drop)
	}
	if got := chunk.CommitDrop(0, core.ItemStack{Item: core.ItemDirt, Count: 1}, index, 10); got != 2 {
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
	chunk.CommitDrop(0, core.ItemStack{Item: core.ItemStone, Count: 1}, index, 10)

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
	chunk.CommitDrop(0, core.ItemStack{Item: core.ItemStone, Count: 1}, dropTestIndex(t, core.BlockPos{X: 16, Y: 3, Z: -32}), 10)
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

func TestChunkCommitDropMergeUsesLongerPickupDelay(t *testing.T) {
	for _, test := range []struct {
		name     string
		old      uint8
		incoming uint8
		want     uint8
	}{
		{name: "来源更短", old: 40, incoming: 10, want: 40},
		{name: "来源相等", old: 10, incoming: 10, want: 10},
		{name: "来源更长", old: 5, incoming: 40, want: 40},
	} {
		t.Run(test.name, func(t *testing.T) {
			chunk := dropTestChunk(t)
			index := dropTestIndex(t, core.BlockPos{X: 16, Y: 3, Z: -32})
			chunk.SetDrop(2, world.DropSlot{
				Generation: 7, Active: true,
				Stack:      core.ItemStack{Item: core.ItemDirt, Count: 63},
				BlockIndex: index, AgeTicks: 99, PickupDelayTicks: test.old,
			})
			slot, ok := chunk.PrepareDrop(core.ItemDirt, index)
			if !ok || slot != 2 {
				t.Fatalf("预检 = (%d,%v)，想要 (2,true)", slot, ok)
			}
			generation := chunk.CommitDrop(slot, core.ItemStack{Item: core.ItemDirt, Count: 1}, index, test.incoming)
			got := chunk.Drop(slot)
			// 合并不得重置 ID、generation、年龄或物品，只延长拾取禁止窗口。
			if generation != 7 || got.Generation != 7 || got.AgeTicks != 99 ||
				got.Stack.Count != 64 || got.PickupDelayTicks != test.want {
				t.Fatalf("合并结果 = %+v generation=%d", got, generation)
			}
		})
	}
}

func TestChunkPrepareDropBatchMergeUsesLongerPickupDelay(t *testing.T) {
	chunk := dropTestChunk(t)
	index := dropTestIndex(t, core.BlockPos{X: 16, Y: 3, Z: -32})
	chunk.SetDrop(0, world.DropSlot{
		Generation: 3, Active: true,
		Stack:      core.ItemStack{Item: core.ItemCoal, Count: 1},
		BlockIndex: index, AgeTicks: 17, PickupDelayTicks: 2,
	})
	var stacks [4]core.ItemStack
	stacks[0] = core.ItemStack{Item: core.ItemCoal, Count: 1}
	next, ok := chunk.PrepareDropBatch(stacks, index, 10)
	if !ok {
		t.Fatal("batch 预检被拒绝")
	}
	got := next[0]
	if got.Generation != 3 || got.AgeTicks != 17 || got.Stack.Count != 2 ||
		got.PickupDelayTicks != 10 {
		t.Fatalf("batch 合并 = %+v", got)
	}
}

func TestChunkPrepareDropBatchPreservesToolDurability(t *testing.T) {
	chunk := dropTestChunk(t)
	index := dropTestIndex(t, core.BlockPos{X: 16, Y: 3, Z: -32})
	worn := core.ItemStack{Item: core.ItemIronPickaxe, Count: 1, Durability: 149}
	stacks := [4]core.ItemStack{worn}
	next, ok := chunk.PrepareDropBatch(stacks, index, 10)
	if !ok || next[0].Stack != worn {
		t.Fatalf("batch 掉落物 = %+v, %v，想要保留来源工具 %+v", next[0].Stack, ok, worn)
	}
}

func TestChunkPrepareDropBatchRejectsInvalidStacks(t *testing.T) {
	index := dropTestIndex(t, core.BlockPos{X: 16, Y: 3, Z: -32})
	for _, stack := range []core.ItemStack{
		{Item: core.ItemNone, Count: 1},
		{Item: core.ItemStone, Count: 1, Durability: 1},
		{Item: core.ItemStonePickaxe, Count: 1},
	} {
		chunk := dropTestChunk(t)
		stacks := [4]core.ItemStack{stack}
		if next, ok := chunk.PrepareDropBatch(stacks, index, 10); ok || next != [core.DropsPerChunk]world.DropSlot{} {
			t.Fatalf("非法 batch 堆 %+v 被接受: ok=%v next=%+v", stack, ok, next)
		}
	}
}

func TestChunkPrepareDropRespectsPerItemStackLimit(t *testing.T) {
	chunk := dropTestChunk(t)
	index := dropTestIndex(t, core.BlockPos{X: 16, Y: 3, Z: -32})
	// 镐的单格上限是 1，已有一把时不得再合并进同一槽。
	chunk.SetDrop(0, world.DropSlot{
		Generation: 5, Active: true,
		Stack:      core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: 73},
		BlockIndex: index,
	})

	slot, ok := chunk.PrepareDrop(core.ItemStonePickaxe, index)
	if !ok {
		t.Fatal("第二把镐应当占用新槽而不是被拒绝")
	}
	if slot == 0 {
		t.Fatal("第二把镐被合并进了已满的槽，违反单格上限 1")
	}

	chunk.CommitDrop(slot, core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: 41}, index, 10)
	if got := chunk.Drop(0).Stack.Count; got != 1 {
		t.Fatalf("原槽数量 = %d，想要保持 1", got)
	}
	if got := chunk.Drop(slot).Stack.Count; got != 1 {
		t.Fatalf("新槽数量 = %d，想要 1", got)
	}
}

func TestChunkPrepareDropStillMergesStackableItems(t *testing.T) {
	chunk := dropTestChunk(t)
	index := dropTestIndex(t, core.BlockPos{X: 16, Y: 3, Z: -32})
	// 可堆叠物品的合并行为不得被本次修复改变。
	chunk.SetDrop(0, world.DropSlot{
		Generation: 5, Active: true,
		Stack:      core.ItemStack{Item: core.ItemDirt, Count: 63},
		BlockIndex: index,
	})

	slot, ok := chunk.PrepareDrop(core.ItemDirt, index)
	if !ok || slot != 0 {
		t.Fatalf("可堆叠物品预检 = (%d,%v)，想要 (0,true)", slot, ok)
	}
	chunk.CommitDrop(slot, core.ItemStack{Item: core.ItemDirt, Count: 1}, index, 10)
	if got := chunk.Drop(0).Stack.Count; got != core.MaxStackCount {
		t.Fatalf("合并后数量 = %d，想要 64", got)
	}
}
