package client

import (
	"reflect"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/physics"
	"minecraft-go/internal/world"
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

func mirrorWithChunk(
	t *testing.T,
	dimension core.DimensionID,
	chunk *world.Chunk,
) *Mirror {
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
		Revision:  1,
		Sections:  sections,
	}
	if err := message.Validate(); err != nil {
		t.Fatalf("测试快照非法: %v", err)
	}
	mirror := NewMirror()
	if _, err := mirror.Apply(message); err != nil {
		t.Fatalf("导入测试区块: %v", err)
	}
	return mirror
}
