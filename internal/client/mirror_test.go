package client_test

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"minecraft-go/internal/client"
	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/world"
)

func TestMirrorRevisionGapRequestsOneResyncUntilSnapshot(t *testing.T) {
	mirror := client.NewMirror()
	chunkPos := core.ChunkPos{X: -2, Z: 4}
	if _, err := mirror.Apply(snapshotFromChunk(
		t, core.Overworld, world.NewChunk(chunkPos), 3,
	)); err != nil {
		t.Fatalf("导入初始快照: %v", err)
	}

	position := core.BlockPos{X: chunkPos.X << core.SectionShift, Y: 0, Z: chunkPos.Z << core.SectionShift}
	contiguous := blockChanges(core.Overworld, chunkPos, 3, position, core.StoneID)
	if update, err := mirror.Apply(contiguous); err != nil || update.Resync != nil {
		t.Fatalf("连续增量 update=%+v err=%v", update, err)
	}
	if block, loaded := mirror.BlockAt(core.Overworld, position); !loaded || block != core.StoneID {
		t.Fatalf("连续增量后 BlockAt = %d, %v", block, loaded)
	}

	duplicate, err := mirror.Apply(contiguous)
	if err != nil || duplicate.Resync != nil || len(duplicate.Dirty) != 0 {
		t.Fatalf("重复增量未忽略: update=%+v err=%v", duplicate, err)
	}

	gap := blockChanges(core.Overworld, chunkPos, 5, position, core.DirtID)
	update, err := mirror.Apply(gap)
	if err != nil {
		t.Fatalf("处理 revision gap: %v", err)
	}
	wantResync := &network.RequestChunkResync{
		Dimension:    core.Overworld,
		Chunk:        chunkPos,
		HaveRevision: 4,
	}
	if !reflect.DeepEqual(update.Resync, wantResync) {
		t.Fatalf("Resync = %+v，想要 %+v", update.Resync, wantResync)
	}
	stored, ok := mirror.Chunk(core.Overworld, chunkPos)
	if !ok || !stored.Desynced || stored.Revision != 4 {
		t.Fatalf("gap 后 chunk = %+v, ok=%v", stored, ok)
	}

	again, err := mirror.Apply(gap)
	if err != nil || again.Resync != nil {
		t.Fatalf("重复 gap 又请求 resync: update=%+v err=%v", again, err)
	}

	replacement := world.NewChunk(chunkPos)
	replacement.SetBlock(0, position.Y, 0, core.GrassID)
	if _, err := mirror.Apply(snapshotFromChunk(t, core.Overworld, replacement, 7)); err != nil {
		t.Fatalf("导入 resync 快照: %v", err)
	}
	stored, ok = mirror.Chunk(core.Overworld, chunkPos)
	if !ok || stored.Desynced || stored.Revision != 7 {
		t.Fatalf("resync 后 chunk = %+v, ok=%v", stored, ok)
	}
	if block, loaded := mirror.BlockAt(core.Overworld, position); !loaded || block != core.GrassID {
		t.Fatalf("resync 后 BlockAt = %d, %v", block, loaded)
	}
}

func TestMirrorMissingChunkDeltaRequestsOneResync(t *testing.T) {
	mirror := client.NewMirror()
	chunk := core.ChunkPos{X: 8, Z: -3}
	position := core.BlockPos{X: chunk.X << core.SectionShift, Y: core.MinY, Z: chunk.Z << core.SectionShift}
	delta := blockChanges(core.DimensionID(2), chunk, 1, position, core.StoneID)

	first, err := mirror.Apply(delta)
	if err != nil {
		t.Fatalf("处理缺失区块增量: %v", err)
	}
	want := &network.RequestChunkResync{Dimension: 2, Chunk: chunk, HaveRevision: 0}
	if !reflect.DeepEqual(first.Resync, want) {
		t.Fatalf("Resync = %+v，想要 %+v", first.Resync, want)
	}
	second, err := mirror.Apply(delta)
	if err != nil || second.Resync != nil {
		t.Fatalf("缺失区块重复请求: update=%+v err=%v", second, err)
	}
}

func TestMirrorBoundaryChangeDirtiesEightLoadedSections(t *testing.T) {
	mirror := client.NewMirror()
	for z := int32(0); z <= 1; z++ {
		for x := int32(0); x <= 1; x++ {
			pos := core.ChunkPos{X: x, Z: z}
			if _, err := mirror.Apply(snapshotFromChunk(
				t, core.Overworld, world.NewChunk(pos), 1,
			)); err != nil {
				t.Fatalf("导入 %+v: %v", pos, err)
			}
		}
	}
	position := core.BlockPos{X: 15, Y: core.MinY + 15, Z: 15}
	update, err := mirror.Apply(blockChanges(
		core.Overworld, core.ChunkPos{}, 1, position, core.StoneID,
	))
	if err != nil {
		t.Fatalf("应用边界增量: %v", err)
	}
	want := make([]core.SectionKey, 0, 8)
	for z := int32(0); z <= 1; z++ {
		for y := int32(0); y <= 1; y++ {
			for x := int32(0); x <= 1; x++ {
				want = append(want, core.SectionKey{
					Dimension: core.Overworld,
					Pos:       core.SectionPos{X: x, Y: y, Z: z},
				})
			}
		}
	}
	assertSectionKeys(t, update.Dirty, want)
}

func TestMirrorForgetRemovesChunkAndDirtiesLoadedNeighbor(t *testing.T) {
	mirror := client.NewMirror()
	center := core.ChunkPos{}
	east := core.ChunkPos{X: 1}
	for _, pos := range []core.ChunkPos{center, east} {
		if _, err := mirror.Apply(snapshotFromChunk(
			t, core.Overworld, world.NewChunk(pos), 1,
		)); err != nil {
			t.Fatalf("导入 %+v: %v", pos, err)
		}
	}

	update, err := mirror.Apply(network.ForgetChunks{
		Dimension: core.Overworld,
		Chunks:    []core.ChunkPos{center},
	})
	if err != nil {
		t.Fatalf("forget: %v", err)
	}
	assertSectionKeys(t, update.Forgotten, chunkSectionKeys(core.Overworld, center))
	assertSectionKeys(t, update.Dirty, chunkSectionKeys(core.Overworld, east))
	if _, ok := mirror.Chunk(core.Overworld, center); ok {
		t.Fatal("forget 后中心区块仍存在")
	}
	if _, ok := mirror.Chunk(core.Overworld, east); !ok {
		t.Fatal("forget 误删东侧区块")
	}
}

func TestMirrorRejectsMalformedForgetWithoutMutation(t *testing.T) {
	mirror := client.NewMirror()
	pos := core.ChunkPos{}
	if _, err := mirror.Apply(snapshotFromChunk(
		t, core.Overworld, world.NewChunk(pos), 1,
	)); err != nil {
		t.Fatalf("导入快照: %v", err)
	}
	_, err := mirror.Apply(network.ForgetChunks{
		Dimension: core.Overworld,
		Chunks:    []core.ChunkPos{pos, pos},
	})
	if err == nil {
		t.Fatal("重复 forget 未被拒绝")
	}
	if _, ok := mirror.Chunk(core.Overworld, pos); !ok {
		t.Fatal("畸形 forget 改变了镜像")
	}
}

func TestMirrorSurfacesCommandRejectionAndRejectsUnsupportedData(t *testing.T) {
	mirror := client.NewMirror()
	rejected := network.CommandRejected{Sequence: 41, Reason: network.RejectOccupied}
	update, err := mirror.Apply(rejected)
	if err != nil || !reflect.DeepEqual(update.Rejected, &rejected) {
		t.Fatalf("CommandRejected update=%+v err=%v", update, err)
	}
	if _, err := mirror.Apply(network.CommandRejected{Reason: network.RejectReason("unknown")}); err == nil {
		t.Fatal("未知拒绝原因未报错")
	}
	if _, err := mirror.Apply(nil); err == nil {
		t.Fatal("nil server message 未报错")
	}
}

func TestMirrorDoesNotConsumePlayerState(t *testing.T) {
	_, err := client.NewMirror().Apply(network.PlayerState{Ready: false})
	if err == nil || !strings.Contains(err.Error(), "unsupported server message") {
		t.Fatalf("Mirror.Apply PlayerState err=%v", err)
	}
}

func blockChanges(
	dimension core.DimensionID,
	chunk core.ChunkPos,
	base uint64,
	position core.BlockPos,
	block core.BlockID,
) network.BlockChanges {
	return network.BlockChanges{
		Dimension:    dimension,
		Chunk:        chunk,
		BaseRevision: base,
		NewRevision:  base + 1,
		Changes: []network.BlockChange{{
			Position: position,
			Block:    block,
		}},
	}
}

func chunkSectionKeys(dimension core.DimensionID, chunk core.ChunkPos) []core.SectionKey {
	keys := make([]core.SectionKey, 0, core.SectionsPerChunk)
	for y := int32(0); y < core.SectionsPerChunk; y++ {
		keys = append(keys, core.SectionKey{
			Dimension: dimension,
			Pos:       core.SectionPos{X: chunk.X, Y: y, Z: chunk.Z},
		})
	}
	return keys
}

func assertSectionKeys(t *testing.T, got, want []core.SectionKey) {
	t.Helper()
	got = append([]core.SectionKey(nil), got...)
	want = append([]core.SectionKey(nil), want...)
	sortSectionKeys(got)
	sortSectionKeys(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("section keys = %+v，想要 %+v", got, want)
	}
}

func sortSectionKeys(keys []core.SectionKey) {
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Dimension != keys[j].Dimension {
			return keys[i].Dimension < keys[j].Dimension
		}
		if keys[i].Pos.X != keys[j].Pos.X {
			return keys[i].Pos.X < keys[j].Pos.X
		}
		if keys[i].Pos.Y != keys[j].Pos.Y {
			return keys[i].Pos.Y < keys[j].Pos.Y
		}
		return keys[i].Pos.Z < keys[j].Pos.Z
	})
}
