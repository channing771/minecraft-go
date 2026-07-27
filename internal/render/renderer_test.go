package render

import (
	"testing"

	"minecraft-go/internal/assets"
	"minecraft-go/internal/core"
	"minecraft-go/internal/gfx"
	"minecraft-go/internal/mesh"
)

func TestPendingUploadsEventuallyDrain(t *testing.T) {
	dev, err := gfx.NewHeadlessDevice()
	if err != nil {
		t.Skipf("本机无可用 GPU 适配器: %v", err)
	}
	defer dev.Release()

	// 每帧通常只够上传一个面；其中一个区段含 3 个面，会独占一帧。
	r := newRenderer(dev, assets.NewRegistry(), gfx.FormatRGBA8Unorm, 100, 8, 32)
	defer r.Release()

	quad := mesh.Quad{W: 1, H: 1, Face: mesh.FacePosY, AO: 0xFF, Light: 0xF0}
	for x := int32(0); x < 10; x++ {
		quads := []mesh.Quad{quad}
		if x == 5 {
			quads = []mesh.Quad{quad, quad, quad}
		}
		r.QueueSection(core.SectionPos{X: x, Y: 4}, quads)
	}

	for frame := 0; frame < 20 && r.PendingUploads() > 0; frame++ {
		r.BeginFrame()
		r.FlushUploads(core.ChunkPos{})
	}
	if got := r.PendingUploads(); got != 0 {
		t.Fatalf("20 帧后仍有 %d 个 pending 上传", got)
	}
	if got := len(r.sections); got != 10 {
		t.Fatalf("已上传区段数 = %d，想要 10", got)
	}
}

func TestQueueSectionOverwritesPendingAndDropOutside(t *testing.T) {
	dev, err := gfx.NewHeadlessDevice()
	if err != nil {
		t.Skipf("本机无可用 GPU 适配器: %v", err)
	}
	defer dev.Release()

	r := newRenderer(dev, assets.NewRegistry(), gfx.FormatRGBA8Unorm, 32, 1024, 8)
	defer r.Release()

	p := core.SectionPos{X: 1, Y: 4, Z: 1}
	q := mesh.Quad{W: 1, H: 1, Face: mesh.FacePosY, AO: 0xFF, Light: 0xF0}
	r.QueueSection(p, []mesh.Quad{q})
	r.QueueSection(p, []mesh.Quad{q, q})
	if got := len(r.pending[p]); got != 2 {
		t.Fatalf("同位置最新 pending 面数 = %d，想要 2", got)
	}

	r.BeginFrame()
	r.FlushUploads(core.ChunkPos{})
	if got := len(r.sections[p].packed); got != 2 {
		t.Fatalf("上传后的面数 = %d，想要 2", got)
	}

	r.DropOutside(core.ChunkPos{X: 20, Z: 20}, 1)
	if len(r.sections) != 0 || r.pool.Used() != 0 {
		t.Fatalf("DropOutside 后 sections=%d used=%d，想要全空", len(r.sections), r.pool.Used())
	}
}
