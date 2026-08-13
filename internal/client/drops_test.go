package client_test

import (
	"testing"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
)

func dropID(chunkX int32, slot uint8, generation uint32) core.DropID {
	return core.DropID{
		Dimension:  core.Overworld,
		Chunk:      core.ChunkPos{X: chunkX},
		Slot:       slot,
		Generation: generation,
	}
}

func dropUpsert(id core.DropID, count uint8) network.ItemDrop {
	return network.ItemDrop{ID: id, BlockIndex: 7, Item: core.ItemStone, Count: count}
}

func TestItemDropsApplyAddsAndReplacesByID(t *testing.T) {
	mirror := client.NewItemDrops()
	id := dropID(0, 1, 1)
	if err := mirror.Apply(network.ItemDropUpserts{
		Drops: []network.ItemDrop{dropUpsert(id, 5)},
	}); err != nil {
		t.Fatalf("首次 upsert: %v", err)
	}
	if got := mirror.Presentations(); len(got) != 1 || got[0].Count != 5 {
		t.Fatalf("镜像 = %+v，想要一个数量 5 的条目", got)
	}

	if err := mirror.Apply(network.ItemDropUpserts{
		Drops: []network.ItemDrop{dropUpsert(id, 3)},
	}); err != nil {
		t.Fatalf("部分拾取 upsert: %v", err)
	}
	got := mirror.Presentations()
	if len(got) != 1 || got[0].ID != id || got[0].Count != 3 {
		t.Fatalf("镜像 = %+v，想要同一 ID 数量变为 3", got)
	}
}

func TestItemDropsKeepsToolDurability(t *testing.T) {
	mirror := client.NewItemDrops()
	id := dropID(0, 1, 1)
	full, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	if err := mirror.Apply(network.ItemDropUpserts{Drops: []network.ItemDrop{{
		ID: id, BlockIndex: 7, Item: core.ItemStonePickaxe, Count: 1, Durability: full,
	}}}); err != nil {
		t.Fatalf("应用工具掉落物: %v", err)
	}
	if got := mirror.Presentations(); len(got) != 1 || got[0].Durability != full {
		t.Fatalf("工具掉落物镜像 = %+v，想要耐久 %d", got, full)
	}
}

func TestItemDropsPresentationsAreSortedByID(t *testing.T) {
	mirror := client.NewItemDrops()
	if err := mirror.Apply(network.ItemDropUpserts{Drops: []network.ItemDrop{
		dropUpsert(dropID(-1, 0, 1), 1),
		dropUpsert(dropID(0, 2, 1), 1),
		dropUpsert(dropID(0, 2, 2), 1),
	}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got := mirror.Presentations()
	if len(got) != 3 {
		t.Fatalf("镜像条目 = %d，想要 3", len(got))
	}
	for index := 1; index < len(got); index++ {
		if got[index-1].ID.Compare(got[index].ID) >= 0 {
			t.Fatalf("呈现顺序不是严格递增: %+v", got)
		}
	}
}

func TestItemDropsRemoveRejectsUnknownIDWithoutMutating(t *testing.T) {
	mirror := client.NewItemDrops()
	known := dropID(0, 1, 1)
	if err := mirror.Apply(network.ItemDropUpserts{
		Drops: []network.ItemDrop{dropUpsert(known, 2)},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	err := mirror.Apply(network.ItemDropRemoves{IDs: []core.DropID{known, dropID(0, 2, 1)}})
	if err == nil {
		t.Fatal("未知 ID 的 remove 被接受")
	}
	if got := mirror.Presentations(); len(got) != 1 || got[0].ID != known {
		t.Fatalf("非法 remove 部分应用: %+v", got)
	}
}

func TestItemDropsRejectsInvalidBatchWithoutMutating(t *testing.T) {
	mirror := client.NewItemDrops()
	valid := dropID(0, 0, 1)
	if err := mirror.Apply(network.ItemDropUpserts{
		Drops: []network.ItemDrop{dropUpsert(valid, 1)},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	before := append([]client.ItemDropPresentation(nil), mirror.Presentations()...)

	invalid := []network.ItemDropUpserts{
		{Drops: []network.ItemDrop{dropUpsert(dropID(0, 1, 1), 1), dropUpsert(dropID(0, 1, 1), 2)}},
		{Drops: []network.ItemDrop{dropUpsert(dropID(0, 3, 1), 1), dropUpsert(dropID(0, 2, 1), 1)}},
		{Drops: []network.ItemDrop{dropUpsert(dropID(0, 4, 1), 0)}},
		{Drops: []network.ItemDrop{{ID: dropID(0, 5, 1), Item: core.ItemID(4242), Count: 1}}},
		{},
	}
	for index, batch := range invalid {
		if err := mirror.Apply(batch); err == nil {
			t.Fatalf("非法批次 %d 被接受", index)
		}
		got := mirror.Presentations()
		if len(got) != len(before) || got[0] != before[0] {
			t.Fatalf("非法批次 %d 修改了镜像: %+v", index, got)
		}
	}
}

func TestItemDropsRejectsBatchBeyondCapacity(t *testing.T) {
	mirror := client.NewItemDrops()
	slot, chunkX := 0, int32(0)
	for filled := 0; filled < client.MaxItemDrops; filled++ {
		if err := mirror.Apply(network.ItemDropUpserts{
			Drops: []network.ItemDrop{dropUpsert(dropID(chunkX, uint8(slot), 1), 1)},
		}); err != nil {
			t.Fatalf("填充第 %d 项: %v", filled, err)
		}
		slot++
		if slot == core.DropsPerChunk {
			slot, chunkX = 0, chunkX+1
		}
	}
	if got := len(mirror.Presentations()); got != client.MaxItemDrops {
		t.Fatalf("镜像 = %d 项，想要 %d", got, client.MaxItemDrops)
	}

	overflow := network.ItemDropUpserts{
		Drops: []network.ItemDrop{dropUpsert(dropID(chunkX+1, 0, 1), 1)},
	}
	if err := mirror.Apply(overflow); err == nil {
		t.Fatal("超过容量的批次被接受")
	}
	if got := len(mirror.Presentations()); got != client.MaxItemDrops {
		t.Fatalf("超限批次改变了镜像大小: %d", got)
	}
}

func TestItemDropsResetClearsMirror(t *testing.T) {
	mirror := client.NewItemDrops()
	if err := mirror.Apply(network.ItemDropUpserts{
		Drops: []network.ItemDrop{dropUpsert(dropID(0, 0, 1), 1)},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	mirror.Reset()
	if got := mirror.Presentations(); len(got) != 0 {
		t.Fatalf("reset 后镜像 = %+v，想要为空", got)
	}
}

func TestItemDropsRejectsUnsupportedMessage(t *testing.T) {
	mirror := client.NewItemDrops()
	if err := mirror.Apply(network.InventoryState{}); err == nil {
		t.Fatal("非掉落物消息被接受")
	}
}

func TestItemDropsPresentationsReuseBufferWithoutAllocating(t *testing.T) {
	mirror := client.NewItemDrops()
	for slot := range core.DropsPerChunk {
		if err := mirror.Apply(network.ItemDropUpserts{
			Drops: []network.ItemDrop{dropUpsert(dropID(0, uint8(slot), 1), 1)},
		}); err != nil {
			t.Fatalf("填充槽 %d: %v", slot, err)
		}
	}
	mirror.Presentations()

	if allocations := testing.AllocsPerRun(100, func() {
		mirror.Presentations()
	}); allocations != 0 {
		t.Fatalf("稳定容量下 Presentations 分配 = %v，想要 0", allocations)
	}
}
