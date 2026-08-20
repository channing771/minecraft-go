//go:build darwin

package render_test

import (
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
	pos          core.SectionPos
	opaque       []byte
	water        []byte
	uploadNumber int
}

func (s *recordingSink) UploadSection(x, y, z int32, opaque, water []byte) {
	s.uploads = append(s.uploads, sinkUpload{
		pos:          core.SectionPos{X: x, Y: y, Z: z},
		opaque:       append([]byte(nil), opaque...),
		water:        append([]byte(nil), water...),
		uploadNumber: len(s.uploads),
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
