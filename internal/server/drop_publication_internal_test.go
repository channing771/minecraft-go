package server

import (
	"testing"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/sim"
)

func TestDropPublicationKeepsDurabilityAndDetectsDurabilityOnlyChange(t *testing.T) {
	id := core.DropID{Dimension: core.Overworld, Slot: 1, Generation: 1}
	current := sim.DropSnapshot{
		ID: id, BlockIndex: 7, Item: core.ItemStonePickaxe, Count: 1, Durability: 72,
	}
	published := map[core.DropID]sim.DropSnapshot{
		id: {ID: id, BlockIndex: 7, Item: core.ItemStonePickaxe, Count: 1, Durability: 73},
	}

	upserts := appendChangedDropUpserts(nil, []sim.DropSnapshot{current}, published)
	if len(upserts) != 1 || upserts[0].Durability != current.Durability {
		t.Fatalf("耐久-only 差分 = %+v，想要耐久 %d 的 upsert", upserts, current.Durability)
	}
	if got := dropSnapshotFromItemDrop(upserts[0]); got != current {
		t.Fatalf("发布镜像 = %+v，想要 %+v", got, current)
	}
}
