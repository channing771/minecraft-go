package client

import (
	"reflect"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/physics"
	"github.com/channing771/mornlea/internal/world"
)

func TestMirrorCollisionSourceLoadedAir(t *testing.T) {
	mirror := mirrorWithChunk(t, core.Overworld, world.NewChunk(core.ChunkPos{}))
	source := MirrorCollisionSource{Mirror: mirror, Dimension: core.Overworld}

	got := source.CollisionBoxes(core.BlockPos{X: 2, Y: 10, Z: 3})
	want := physics.CollisionBoxSet{Loaded: true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("air collision boxes = %+v，想要 %+v", got, want)
	}
}

func TestMirrorCollisionSourceLoadedStone(t *testing.T) {
	chunk := world.NewChunk(core.ChunkPos{})
	position := core.BlockPos{X: 2, Y: 10, Z: 3}
	chunk.SetBlock(2, position.Y, 3, core.StoneID)
	mirror := mirrorWithChunk(t, core.Overworld, chunk)
	source := MirrorCollisionSource{Mirror: mirror, Dimension: core.Overworld}

	got := source.CollisionBoxes(position)
	want := physics.CollisionBoxSet{
		Loaded: true,
		Count:  1,
		Boxes: [8]core.AABB{{
			Max: mgl32.Vec3{1, 1, 1},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stone collision boxes = %+v，想要 %+v", got, want)
	}
}

func TestMirrorCollisionSourceMissingChunk(t *testing.T) {
	source := MirrorCollisionSource{
		Mirror:    NewMirror(),
		Dimension: core.Overworld,
	}

	got := source.CollisionBoxes(core.BlockPos{X: 32, Y: 10, Z: 0})
	if got != (physics.CollisionBoxSet{}) {
		t.Fatalf("missing collision boxes = %+v，想要 Loaded=false", got)
	}
}

func TestMirrorCollisionSourceClosesDesyncedChunkUntilReplacementSnapshot(t *testing.T) {
	position := core.BlockPos{X: 2, Y: 10, Z: 3}
	mirror := mirrorWithChunk(t, core.Overworld, world.NewChunk(core.ChunkPos{}))
	source := MirrorCollisionSource{Mirror: mirror, Dimension: core.Overworld}

	gap := network.BlockChanges{
		Dimension:    core.Overworld,
		Chunk:        core.ChunkPos{},
		BaseRevision: 2,
		NewRevision:  3,
		Changes: []network.BlockChange{{
			Position: position,
			Block:    core.StoneID,
		}},
	}
	update, err := mirror.Apply(gap)
	if err != nil || update.Resync == nil {
		t.Fatalf("revision gap update=%+v err=%v，想要 resync", update, err)
	}
	if got := source.CollisionBoxes(position); got.Loaded {
		t.Fatalf("desynced chunk collision boxes=%+v，想要 Loaded=false", got)
	}
	for _, outside := range []core.BlockPos{
		{X: position.X, Y: core.MinY - 1, Z: position.Z},
		{X: position.X, Y: core.MaxY, Z: position.Z},
	} {
		if got := source.CollisionBoxes(outside); !got.Loaded || got.Count != 0 {
			t.Fatalf("世界高度外 %+v collision boxes=%+v，想要 loaded air", outside, got)
		}
	}

	replacement := world.NewChunk(core.ChunkPos{})
	replacement.SetBlock(2, position.Y, 3, core.StoneID)
	if _, err := mirror.Apply(collisionSnapshotFromChunk(
		t, core.Overworld, replacement, 3,
	)); err != nil {
		t.Fatalf("导入 replacement snapshot: %v", err)
	}
	got := source.CollisionBoxes(position)
	if !got.Loaded || got.Count != 1 {
		t.Fatalf("replacement snapshot collision boxes=%+v，想要 loaded solid", got)
	}
}

func mirrorWithChunk(
	t *testing.T,
	dimension core.DimensionID,
	chunk *world.Chunk,
) *Mirror {
	t.Helper()
	message := collisionSnapshotFromChunk(t, dimension, chunk, 1)
	mirror := NewMirror()
	if _, err := mirror.Apply(message); err != nil {
		t.Fatalf("导入测试区块: %v", err)
	}
	return mirror
}

func collisionSnapshotFromChunk(
	t *testing.T,
	dimension core.DimensionID,
	chunk *world.Chunk,
	revision uint64,
) network.ChunkSnapshot {
	t.Helper()
	sections := make([]network.SectionData, core.SectionsPerChunk)
	for index := range sections {
		snapshot := chunk.Section(index).Blocks.Snapshot()
		sections[index] = network.SectionData{
			Y:       int32(index),
			Storage: network.SectionStorage(snapshot.Kind),
			Single:  snapshot.Single,
			Bits:    snapshot.Bits,
			Palette: append([]core.BlockID(nil), snapshot.Palette...),
			Packed:  append([]uint64(nil), snapshot.Packed...),
		}
	}
	message := network.ChunkSnapshot{
		Dimension: dimension,
		Chunk:     chunk.Pos,
		Revision:  revision,
		Sections:  sections,
	}
	if err := message.Validate(); err != nil {
		t.Fatalf("测试快照非法: %v", err)
	}
	return message
}
