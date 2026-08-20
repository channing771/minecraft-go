package sim

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/physics"
	"github.com/channing771/mornlea/internal/world"
)

// TestSubmersionFlagsParityAuthorityVersusMirror 覆盖 spec Scenario
// 「权威与预测一致」：同一份方块数据、同一玩家位置下，服务端 Dimension 与
// 客户端 Mirror 算出的两个浸没标志逐位相同。
//
// 本用例刻意跨包（package sim 的测试引用 internal/client）：只有把两个
// FluidSource 实现放进同一个进程、喂同一份方块数据，才谈得上「逐位相同」。
// 生产依赖方向未变，internal/archcheck 扫的是生产 import，不受影响。
//
// 需要说明这条断言承重在哪：判定规则本身两侧共用 physics.SubmersionFlags，
// 规则写错会一起错、差值恒等——所以这条守的不是规则，而是两个
// IsFluidAt 适配器（权威 BlockAt vs 镜像区块查表，以及各自的
// 未就绪/失同步/越界分支）不会给出不同的方块视图。
func TestSubmersionFlagsParityAuthorityVersusMirror(t *testing.T) {
	chunk := world.NewChunk(core.ChunkPos{})
	for z := range core.SectionSize {
		for x := range core.SectionSize {
			chunk.SetBlock(x, 0, z, core.StoneID)
		}
	}
	// 一个水池：方块 y=1..4 是水，等级各不相同，确保 IsFluid 覆盖整段编号区间。
	for y := int32(1); y <= 4; y++ {
		chunk.SetBlock(4, y, 4, core.WaterSourceID)
		chunk.SetBlock(5, y, 4, core.WaterLevel7ID)
		chunk.SetBlock(4, y, 5, core.BlockID(int(core.WaterSourceID)+int(y)))
	}
	chunk.Compact()

	dimension := NewDimension(core.Overworld)
	dimension.records[core.ChunkPos{}] = &ChunkRecord{State: ChunkReady, Chunk: chunk}
	authority := dimensionCollisionSource{dimension: dimension}

	mirror := client.NewMirror()
	if _, err := mirror.Apply(parityChunkSnapshot(t, chunk)); err != nil {
		t.Fatalf("导入镜像区块: %v", err)
	}
	predicted := client.MirrorCollisionSource{Mirror: mirror, Dimension: core.Overworld}

	// 位置语料横跨水池内外、水面上下、区块外与世界高度边界。
	positions := make([]mgl32.Vec3, 0, 256)
	for _, x := range []float32{4.5, 5.0, 5.5, 6.5, 20.5} {
		for _, z := range []float32{4.5, 5.0, 5.5, 6.5} {
			for _, y := range []float32{
				0.5, 1, 1.5, 2, 3, 3.5, 4, 4.5, 5, 5.5, 6,
				float32(core.MinY), float32(core.MaxY) - 1,
			} {
				positions = append(positions, mgl32.Vec3{x, y, z})
			}
		}
	}
	// 区块外（镜像与权威都没有这块数据）也必须一致地判为「无水」。
	positions = append(positions, mgl32.Vec3{200.5, 2, 200.5})

	sawFluid, sawDry := false, false
	for _, position := range positions {
		authorityBody, authorityEye := physics.SubmersionFlags(position, authority)
		predictedBody, predictedEye := physics.SubmersionFlags(position, predicted)
		if authorityBody != predictedBody || authorityEye != predictedEye {
			t.Fatalf(
				"位置 %v：权威 body/eye=%v/%v，预测 body/eye=%v/%v",
				position, authorityBody, authorityEye, predictedBody, predictedEye,
			)
		}
		if authorityBody {
			sawFluid = true
		} else {
			sawDry = true
		}
	}
	// 夹具前提守卫排在真实断言之后：语料必须同时覆盖到「有水」与「无水」，
	// 否则两侧恒返回同一个常量也会全绿。
	if !sawFluid || !sawDry {
		t.Fatalf("夹具无效：sawFluid=%v sawDry=%v，语料未同时覆盖两种结果", sawFluid, sawDry)
	}
}

func parityChunkSnapshot(t *testing.T, chunk *world.Chunk) network.ChunkSnapshot {
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
		Dimension: core.Overworld,
		Chunk:     chunk.Pos,
		Revision:  1,
		Sections:  sections,
	}
	if err := message.Validate(); err != nil {
		t.Fatalf("测试快照非法: %v", err)
	}
	return message
}
