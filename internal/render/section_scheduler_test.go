//go:build darwin

package render_test

import (
	"reflect"
	"testing"

	"github.com/channing771/mornlea/internal/assets"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/mesh"
	"github.com/channing771/mornlea/internal/render"
)

// recordingSink 记录每次上传的两条字节流,供分流断言使用。
type recordingSink struct {
	uploads []sinkUpload
	drops   int
}

type sinkUpload struct {
	pos    core.SectionPos
	opaque []byte
	water  []byte
}

func (s *recordingSink) UploadSection(x, y, z int32, opaque, water []byte) {
	s.uploads = append(s.uploads, sinkUpload{
		pos:    core.SectionPos{X: x, Y: y, Z: z},
		opaque: append([]byte(nil), opaque...),
		water:  append([]byte(nil), water...),
	})
}

func (s *recordingSink) DropSection(int32, int32, int32) { s.drops++ }

// waterQuad 构造一条带角高度的水面 quad(材质层为 LayerWater)。
func waterQuad(x, y, z uint8) mesh.Quad {
	return mesh.Quad{
		X: x, Y: y, Z: z, W: 1, H: 1,
		Face:    mesh.FacePosY,
		Mat:     assets.LayerWater,
		Light:   0xF0,
		Corners: [4]uint8{14, 14, 14, 14},
	}
}

// stoneQuad 构造一条普通的不透明 quad。
func stoneQuad(x, y, z uint8) mesh.Quad {
	return mesh.Quad{
		X: x, Y: y, Z: z, W: 2, H: 3,
		Face: mesh.FacePosX,
		Mat:  assets.LayerStone,
	}
}

// unpackStream 把上传字节流还原成 quad 序列,顺带校验每条实例恰好 8 字节。
func unpackStream(t *testing.T, name string, stream []byte) []mesh.Quad {
	t.Helper()
	if len(stream)%8 != 0 {
		t.Fatalf("%s 流长度 %d 不是 8 的倍数", name, len(stream))
	}
	out := make([]mesh.Quad, 0, len(stream)/8)
	for offset := 0; offset < len(stream); offset += 8 {
		var value uint64
		for i := 0; i < 8; i++ {
			value |= uint64(stream[offset+i]) << (8 * i)
		}
		out = append(out, mesh.UnpackQuad(value))
	}
	return out
}

// TestFlushUploadsPartitionsWaterQuadsByMaterial 锁定「上传路径按 material 分流」:
// 同一区段里的水面 quad 只出现在 water 流、其余 quad 只出现在 opaque 流,两条流
// 合起来不丢也不重,且每条实例仍是 8 字节。
//
// 承重点:水面必须离开不透明 terrain pass。若分流失效,水会重新混进单次
// indirect draw,而 terrain.wgsl 把 bit 12..19 读成 w-1/h-1——那 8 bit 现在装的
// 是角高度 7..15,水面会被画成 8×8 到 16×16 的巨型石板。
func TestFlushUploadsPartitionsWaterQuadsByMaterial(t *testing.T) {
	sink := &recordingSink{}
	scheduler := render.NewSectionScheduler(sink, 1<<20)
	pos := core.SectionPos{X: 1, Y: 2, Z: 3}
	quads := []mesh.Quad{
		stoneQuad(0, 0, 0),
		waterQuad(1, 5, 2),
		stoneQuad(2, 0, 0),
		waterQuad(3, 5, 4),
		waterQuad(5, 5, 6),
	}
	scheduler.QueueSection(pos, quads)
	scheduler.BeginFrame()
	scheduler.FlushUploads(core.ChunkPos{X: 1, Z: 3})

	if len(sink.uploads) != 1 {
		t.Fatalf("上传次数 = %d,想要 1", len(sink.uploads))
	}
	upload := sink.uploads[0]
	if upload.pos != pos {
		t.Fatalf("上传位置 = %+v,想要 %+v", upload.pos, pos)
	}
	opaque := unpackStream(t, "opaque", upload.opaque)
	water := unpackStream(t, "water", upload.water)
	if len(opaque)+len(water) != len(quads) {
		t.Fatalf("两条流合计 %d 条 quad,想要 %d", len(opaque)+len(water), len(quads))
	}
	for i, q := range water {
		if q.Mat != assets.LayerWater {
			t.Fatalf("water 流第 %d 条的材质层 = %d,想要 LayerWater", i, q.Mat)
		}
	}
	for i, q := range opaque {
		if q.Mat == assets.LayerWater {
			t.Fatalf("opaque 流第 %d 条是水面 quad,分流失效", i)
		}
	}
	// 逐条比对内容:分流只是重新分组,不得改动任何一条 quad。
	wantOpaque := []mesh.Quad{stoneQuad(0, 0, 0), stoneQuad(2, 0, 0)}
	wantWater := []mesh.Quad{waterQuad(1, 5, 2), waterQuad(3, 5, 4), waterQuad(5, 5, 6)}
	for i, want := range wantOpaque {
		if i >= len(opaque) || opaque[i] != want {
			t.Fatalf("opaque 流第 %d 条 = %+v,想要 %+v", i, opaque, want)
		}
	}
	for i, want := range wantWater {
		if i >= len(water) || water[i] != want {
			t.Fatalf("water 流第 %d 条 = %+v,想要 %+v", i, water, want)
		}
	}
}

// TestFlushUploadsKeepsWaterOnlySectionsAlive 锁定「只有水面的区段仍然上传」:
// 水下的一个区段完全可能只产出水面 quad(地形在相邻区段),若分流实现把
// 「opaque 为空」误当成「区段为空」而转成 drop,整片水会消失且没有任何断言会响。
func TestFlushUploadsKeepsWaterOnlySectionsAlive(t *testing.T) {
	sink := &recordingSink{}
	scheduler := render.NewSectionScheduler(sink, 1<<20)
	pos := core.SectionPos{X: 0, Y: 4, Z: 0}
	scheduler.QueueSection(pos, []mesh.Quad{waterQuad(0, 0, 0), waterQuad(1, 0, 0)})
	scheduler.BeginFrame()
	scheduler.FlushUploads(core.ChunkPos{})

	if len(sink.uploads) != 1 {
		t.Fatalf("上传次数 = %d,想要 1", len(sink.uploads))
	}
	if sink.drops != 0 {
		t.Fatalf("drop 次数 = %d,只含水面的区段不得被丢弃", sink.drops)
	}
	if got := len(sink.uploads[0].opaque); got != 0 {
		t.Fatalf("opaque 流长度 = %d,想要 0", got)
	}
	if got := len(sink.uploads[0].water); got != 16 {
		t.Fatalf("water 流长度 = %d,想要 16(2 条 × 8 字节)", got)
	}
}

// TestFlushUploadsBudgetCountsBothStreams 锁定预算仍按两条流的总字节计费:
// 水面若不计费,一片大水体就能绕过每帧上传预算。
func TestFlushUploadsBudgetCountsBothStreams(t *testing.T) {
	sink := &recordingSink{}
	// 预算只够一条 quad(8 字节)。
	scheduler := render.NewSectionScheduler(sink, 8)
	scheduler.QueueSection(core.SectionPos{X: 0}, []mesh.Quad{waterQuad(0, 0, 0)})
	scheduler.QueueSection(core.SectionPos{X: 5}, []mesh.Quad{waterQuad(0, 0, 0)})
	scheduler.BeginFrame()
	scheduler.FlushUploads(core.ChunkPos{})

	if len(sink.uploads) != 1 {
		t.Fatalf("上传次数 = %d,想要 1(预算只够一条 quad)", len(sink.uploads))
	}
	if scheduler.PendingUploads() != 1 {
		t.Fatalf("待冲刷区段 = %d,想要 1", scheduler.PendingUploads())
	}
}

// countingSink 只记数,不复制字节——它自己绝不能分配,否则会污染
// AllocsPerRun 的读数。
type countingSink struct{ uploads int }

func (s *countingSink) UploadSection(int32, int32, int32, []byte, []byte) { s.uploads++ }
func (s *countingSink) DropSection(int32, int32, int32)                   {}

// allocsPerFlush 测量「排队一个区段 + 冲刷一帧」的稳态分配次数。
func allocsPerFlush(quads []mesh.Quad) float64 {
	scheduler := render.NewSectionScheduler(&countingSink{}, 1<<20)
	pos := core.SectionPos{}
	run := func() {
		scheduler.QueueSection(pos, quads)
		scheduler.BeginFrame()
		scheduler.FlushUploads(core.ChunkPos{})
	}
	// 预热:两条打包 scratch 都在首次冲刷时按最坏情况扩容到位。
	run()
	run()
	return testing.AllocsPerRun(200, run)
}

// TestWaterPartitionAddsNoPerFrameAllocations 锁定 voxel-visual-presentation
// MODIFIED 的「预热后 MUST 不产生每帧动态资源创建或堆分配」在 Go 上传侧的部分:
// 含水与不含水的同规模区段,稳态分配次数必须**完全相同**。
//
// 这条断言选的是「相等」而不是「小于某个常数」:常数阈值会随无关改动漂移,
// 而相等直接钉住「分流本身不额外分配」。若实现每帧新建水面缓冲(而不是复用
// 跨帧 scratch),含水那一侧的读数会立刻高出来。
func TestWaterPartitionAddsNoPerFrameAllocations(t *testing.T) {
	const count = 64
	opaqueOnly := make([]mesh.Quad, 0, count)
	mixed := make([]mesh.Quad, 0, count)
	for i := 0; i < count; i++ {
		opaqueOnly = append(opaqueOnly, stoneQuad(uint8(i%16), 0, 0))
		if i%2 == 0 {
			mixed = append(mixed, waterQuad(uint8(i%16), 5, 0))
		} else {
			mixed = append(mixed, stoneQuad(uint8(i%16), 0, 0))
		}
	}
	withoutWater := allocsPerFlush(opaqueOnly)
	withWater := allocsPerFlush(mixed)
	if withWater != withoutWater {
		t.Fatalf("含水区段每帧分配 %.1f 次,不含水 %.1f 次:分流不得引入每帧分配",
			withWater, withoutWater)
	}
}

// TestWaterQuadInstanceStaysEightBytes 锁定「水面 quad 实例 MUST 保持 8 字节」。
//
// 带四个角高度的水面 quad 打包后仍是一个 u64,上传流长度恰好是 quad 数 × 8,
// 且解包后角高度逐个还原、W/H 回到 1。角高度借的是 W/H 与 bit 55..62 的空闲位,
// 任何「加一个字段就好了」的改法都会在这里变红。
func TestWaterQuadInstanceStaysEightBytes(t *testing.T) {
	sink := &recordingSink{}
	scheduler := render.NewSectionScheduler(sink, 1<<20)
	quads := []mesh.Quad{
		{X: 1, Y: 2, Z: 3, W: 1, H: 1, Face: mesh.FacePosY, Mat: assets.LayerWater,
			AO: 0x5A, Light: 0xA5, Corners: [4]uint8{15, 14, 13, 7}},
		{X: 4, Y: 5, Z: 6, W: 1, H: 1, Face: mesh.FacePosX, Mat: assets.LayerWater,
			Corners: [4]uint8{0, 15, 15, 0}},
	}
	scheduler.QueueSection(core.SectionPos{}, quads)
	scheduler.BeginFrame()
	scheduler.FlushUploads(core.ChunkPos{})

	if len(sink.uploads) != 1 {
		t.Fatalf("上传次数 = %d,想要 1", len(sink.uploads))
	}
	stream := sink.uploads[0].water
	if len(stream) != len(quads)*8 {
		t.Fatalf("water 流 %d 字节,想要 %d(每条实例 8 字节)", len(stream), len(quads)*8)
	}
	for i, got := range unpackStream(t, "water", stream) {
		if got != quads[i] {
			t.Fatalf("第 %d 条往返后 = %+v,想要 %+v", i, got, quads[i])
		}
	}
}

// TestSectionSinkExposesExactlyOneExtraStream 锁定「只允许恰好一个额外的
// 半透明阶段」在上传契约上的投影:SectionSink 只暴露 opaque 与 water 两条流。
//
// 再加一个透明 pass 必然需要第三条上传流(每个 pass 都要有自己的实例来源),
// 于是这里会变红,改动者被迫先去修订 voxel-visual-presentation 的边界。
func TestSectionSinkExposesExactlyOneExtraStream(t *testing.T) {
	method, ok := reflect.TypeOf((*render.SectionSink)(nil)).Elem().MethodByName("UploadSection")
	if !ok {
		t.Fatal("SectionSink 没有 UploadSection 方法")
	}
	streams := 0
	for i := 0; i < method.Type.NumIn(); i++ {
		if method.Type.In(i) == reflect.TypeOf([]byte(nil)) {
			streams++
		}
	}
	if streams != 2 {
		t.Fatalf("UploadSection 有 %d 条字节流,想要 2(不透明 + 唯一的水面阶段)", streams)
	}
}
