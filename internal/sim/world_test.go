package sim_test

import (
	"errors"
	"testing"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/sim"
	"github.com/channing771/mornlea/internal/world"
)

func TestDimensionLifecycleReadyUnloadAndRetry(t *testing.T) {
	dimension := sim.NewDimension(core.Overworld)
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

	if dimension.RequestUnload(pos) {
		t.Fatal("未持久的生成区块被立即卸载")
	}
	assertChunkInfo(t, dimension, pos, sim.ChunkUnloading, 1, nil)
	if !dimension.CancelUnload(pos) {
		t.Fatal("未取消生成区块的卸载请求")
	}
	if _, ready := dimension.BlockAt(blockPos); !ready {
		t.Fatal("取消卸载后 BlockAt 未恢复 Ready")
	}

	cleanPos := core.ChunkPos{X: 4, Z: 4}
	if !dimension.BeginLoading(cleanPos) {
		t.Fatal("没有开始干净区块加载")
	}
	if err := dimension.ApplyLoaded(
		cleanPos,
		world.NewChunk(cleanPos),
		1,
		1,
		false,
		false,
	); err != nil {
		t.Fatal(err)
	}
	if !dimension.RequestUnload(cleanPos) {
		t.Fatal("干净区块未立即卸载")
	}
	if _, ok := dimension.Info(cleanPos); ok {
		t.Fatal("干净区块卸载后仍存在")
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
		return sim.NewDimension(core.Overworld)
	}
	pos := core.ChunkPos{}

	assertPanics(t, "ApplyGenerated from Absent", func() {
		_ = newDimension().ApplyGenerated(pos, world.NewChunk(pos))
	})
	assertPanics(t, "MarkFailed from Absent", func() {
		newDimension().MarkFailed(pos, errors.New("failed"))
	})
	if newDimension().RequestUnload(pos) {
		t.Fatal("Absent 区块报告已卸载")
	}

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

func TestDimensionSetBlockRequiresReadyAndWorldHeight(t *testing.T) {
	dimension := sim.NewDimension(core.Overworld)
	if _, _, err := dimension.SetBlock(
		core.BlockPos{Y: 0},
		core.StoneID,
	); !errors.Is(err, sim.ErrChunkNotReady) {
		t.Fatalf("未加载 SetBlock = %v", err)
	}
	if _, _, err := dimension.SetBlock(
		core.BlockPos{Y: core.MaxY},
		core.StoneID,
	); !errors.Is(err, sim.ErrBlockOutOfWorld) {
		t.Fatalf("越界 SetBlock = %v", err)
	}
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

func generateFlatChunk(pos core.ChunkPos) *world.Chunk {
	chunk := world.NewChunk(pos)
	for z := 0; z < core.SectionSize; z++ {
		for x := 0; x < core.SectionSize; x++ {
			for y := int32(core.MinY); y <= 0; y++ {
				worldPos := core.BlockPos{
					X: pos.X<<core.SectionShift + int32(x),
					Y: y,
					Z: pos.Z<<core.SectionShift + int32(z),
				}
				chunk.SetBlock(x, y, z, flatBaseBlock(worldPos))
			}
		}
	}
	chunk.Compact()
	return chunk
}

func flatBaseBlock(position core.BlockPos) core.BlockID {
	switch {
	case position.Y < core.MinY || position.Y >= core.MaxY:
		return core.AirID
	case position.Y == core.MinY:
		return core.BedrockID
	case position.Y < 0:
		return core.StoneID
	case position.Y == 0:
		return core.GrassID
	default:
		return core.AirID
	}
}
