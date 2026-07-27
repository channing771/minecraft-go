package sim_test

import (
	"errors"
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/world"
)

func TestDimensionLifecycleReadyUnloadAndRetry(t *testing.T) {
	dimension := sim.NewDimension(core.Overworld, func(core.BlockPos) core.BlockID {
		return core.AirID
	})
	pos := core.ChunkPos{X: -2, Z: 3}

	if !dimension.BeginGeneration(pos) {
		t.Fatal("Absent → Generating 没有发生")
	}
	assertChunkInfo(t, dimension, pos, sim.ChunkGenerating, 0, nil)
	if dimension.BeginGeneration(pos) {
		t.Fatal("重复 BeginGeneration 不应重复排队")
	}

	generated := world.NewChunk(pos)
	generated.SetBlock(1, 0, 2, core.StoneID)
	if err := dimension.ApplyGenerated(pos, generated); err != nil {
		t.Fatal(err)
	}
	assertChunkInfo(t, dimension, pos, sim.ChunkReady, 1, nil)

	generated.SetBlock(1, 0, 2, core.DirtID)
	blockPos := core.BlockPos{
		X: pos.X<<core.SectionShift + 1,
		Y: 0,
		Z: pos.Z<<core.SectionShift + 2,
	}
	if got, ready := dimension.BlockAt(blockPos); !ready || got != core.DirtID {
		t.Fatalf("ApplyGenerated 没有接管 chunk: got (%d,%v)", got, ready)
	}

	clone, revision, ok := dimension.CloneReadyChunk(pos)
	if !ok || revision != 1 {
		t.Fatalf("CloneReadyChunk = (%v,%d,%v)", clone, revision, ok)
	}
	clone.SetBlock(1, 0, 2, core.GrassID)
	if got, _ := dimension.BlockAt(blockPos); got != core.DirtID {
		t.Fatal("修改读取副本影响了权威区块")
	}

	if err := dimension.Unload(pos); err != nil {
		t.Fatal(err)
	}
	if _, ok := dimension.Info(pos); ok {
		t.Fatal("Unload 后区块仍存在")
	}
	if _, ready := dimension.BlockAt(blockPos); ready {
		t.Fatal("Unload 后 BlockAt 仍报告 Ready")
	}

	failedPos := core.ChunkPos{X: 8, Z: -5}
	if !dimension.BeginGeneration(failedPos) {
		t.Fatal("没有开始失败用例的生成")
	}
	wantErr := errors.New("generator failed")
	dimension.MarkFailed(failedPos, wantErr)
	assertChunkInfo(t, dimension, failedPos, sim.ChunkFailed, 0, wantErr)
	if !dimension.BeginGeneration(failedPos) {
		t.Fatal("Failed → Generating 没有发生")
	}
	assertChunkInfo(t, dimension, failedPos, sim.ChunkGenerating, 0, nil)
}

func TestDimensionLifecycleRejectsInvalidTransitions(t *testing.T) {
	newDimension := func() *sim.Dimension {
		return sim.NewDimension(core.Overworld, func(core.BlockPos) core.BlockID {
			return core.AirID
		})
	}
	pos := core.ChunkPos{}

	assertPanics(t, "ApplyGenerated from Absent", func() {
		_ = newDimension().ApplyGenerated(pos, world.NewChunk(pos))
	})
	assertPanics(t, "MarkFailed from Absent", func() {
		newDimension().MarkFailed(pos, errors.New("failed"))
	})
	assertPanics(t, "Unload from Absent", func() {
		_ = newDimension().Unload(pos)
	})

	dimension := newDimension()
	dimension.BeginGeneration(pos)
	if err := dimension.ApplyGenerated(
		pos,
		world.NewChunk(core.ChunkPos{X: 1}),
	); err == nil {
		t.Fatal("错误坐标的生成结果被接受")
	}
	assertChunkInfo(t, dimension, pos, sim.ChunkGenerating, 0, nil)
	if err := dimension.ApplyGenerated(pos, nil); err == nil {
		t.Fatal("nil 生成结果被接受")
	}
	assertChunkInfo(t, dimension, pos, sim.ChunkGenerating, 0, nil)
}

func assertChunkInfo(
	t *testing.T,
	dimension *sim.Dimension,
	pos core.ChunkPos,
	state sim.ChunkState,
	revision uint64,
	wantErr error,
) {
	t.Helper()
	info, ok := dimension.Info(pos)
	if !ok {
		t.Fatalf("区块 %+v 不存在", pos)
	}
	if info.State != state || info.Revision != revision || !errors.Is(info.Err, wantErr) {
		t.Fatalf(
			"Info = {State:%v Revision:%d Err:%v}，想要 {%v %d %v}",
			info.State,
			info.Revision,
			info.Err,
			state,
			revision,
			wantErr,
		)
	}
}

func assertPanics(t *testing.T, name string, run func()) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("想要非法状态转换 panic")
			}
		}()
		run()
	})
}
