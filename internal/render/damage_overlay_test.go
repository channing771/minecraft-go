//go:build darwin

package render

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"

	"minecraft-go/internal/gfx"
)

type damageOverlayTestEncoder struct {
	descs []gfx.RenderPassDesc
	pass  skyTestPass
}

func (encoder *damageOverlayTestEncoder) BeginRenderPass(desc gfx.RenderPassDesc) gfx.RenderPass {
	encoder.descs = append(encoder.descs, desc)
	return &encoder.pass
}
func (*damageOverlayTestEncoder) BeginComputePass(string) gfx.ComputePass {
	panic("unexpected compute pass")
}
func (*damageOverlayTestEncoder) CopyBufferToBuffer(gfx.Buffer, uint64, gfx.Buffer, uint64, uint64) {
	panic("unexpected buffer copy")
}
func (*damageOverlayTestEncoder) Finish() gfx.CommandBuffer { panic("unexpected finish") }

func TestDamageOverlayUsesFixedResourcesAndOneTriangle(t *testing.T) {
	device := &skyTestDevice{}
	renderer := NewDamageOverlayRenderer(device, gfx.FormatRGBA8Unorm)
	uniform := device.buffer(t, "damage overlay uniform")
	pipeline := device.renderPipeline(t, "damage overlay")
	bind := device.bindGroup(t, "damage overlay resources")

	if uniform.desc.Size != 16 || uniform.desc.Usage != gfx.BufferUsageUniform|gfx.BufferUsageCopyDst {
		t.Fatalf("uniform=%+v，想要固定 16 字节 Uniform|CopyDst", uniform.desc)
	}
	if pipeline.desc.Blend != gfx.BlendAlpha || pipeline.desc.DepthFormat != gfx.FormatUndefined || pipeline.desc.DepthWrite {
		t.Fatalf("pipeline=%+v，想要无 depth 的 alpha blend", pipeline.desc)
	}
	if len(bind.desc.Entries) != 1 || bind.desc.Entries[0].Buffer != uniform || bind.desc.Entries[0].Size != 16 {
		t.Fatalf("bind entries=%+v，想要一个完整 uniform 绑定", bind.desc.Entries)
	}

	renderer.Render(nil, nil, 0)
	renderer.Render(nil, nil, -1)
	renderer.Render(nil, nil, float32(math.NaN()))
	if len(uniform.writes) != 0 {
		t.Fatalf("非活动强度产生 %d 次 uniform 写入", len(uniform.writes))
	}

	encoder := &damageOverlayTestEncoder{}
	target := &skyTestView{}
	renderer.Render(encoder, target, 2)
	if len(encoder.descs) != 1 || encoder.descs[0].Label != "damage overlay pass" || encoder.descs[0].LoadClear || encoder.descs[0].ColorView != target {
		t.Fatalf("pass descriptors=%+v，想要保留目标的单 pass", encoder.descs)
	}
	if got := encoder.pass.commands; len(got) != 2 || got[0] != "pipeline:damage overlay" || got[1] != "draw:damage overlay:3:1" {
		t.Fatalf("commands=%v，想要一次三顶点 draw", got)
	}
	if len(uniform.writes) != 1 || len(uniform.writes[0]) != 16 {
		t.Fatalf("uniform writes=%d bytes=%d，想要 1/16", len(uniform.writes), writeBytes(uniform.writes))
	}
	if got := math.Float32frombits(binary.LittleEndian.Uint32(uniform.writes[0][:4])); got != 1 {
		t.Fatalf("钳制后 strength=%v，想要 1", got)
	}
	if !bytes.Equal(uniform.writes[0][4:], make([]byte, 12)) {
		t.Fatalf("uniform padding=%v，想要全零", uniform.writes[0][4:])
	}

	renderer.Release()
	renderer.Release()
	if uniform.releases != 1 || pipeline.releases != 1 || bind.releases != 1 {
		t.Fatalf("release counts=%d/%d/%d，想要各 1", uniform.releases, pipeline.releases, bind.releases)
	}
}

const damageOverlayHeadlessSize = 64

func TestDamageOverlayHeadlessPixels(t *testing.T) {
	device, err := gfx.NewHeadlessDevice()
	if err != nil {
		if skyHeadlessAdapterUnavailable(err) {
			t.Skipf("本机无可用 GPU adapter: %v", err)
		}
		t.Fatalf("创建 headless GPU device: %v", err)
	}
	defer device.Release()
	renderer := NewDamageOverlayRenderer(device, gfx.FormatRGBA8Unorm)
	defer renderer.Release()

	base := renderDamageOverlayHeadless(t, device, renderer, 0)
	got := renderDamageOverlayHeadless(t, device, renderer, 1)
	baseCenter := damageOverlayPixel(base, 32, 32)
	gotCenter := damageOverlayPixel(got, 32, 32)
	if gotCenter != baseCenter {
		t.Fatalf("中心像素=%v，想要保持底图 %v", gotCenter, baseCenter)
	}
	baseEdge := damageOverlayPixel(base, 0, 32)
	gotEdge := damageOverlayPixel(got, 0, 32)
	if int(gotEdge[0])-int(baseEdge[0]) < 35 || gotEdge[0] <= gotEdge[1]+35 {
		t.Fatalf("边缘像素 base=%v got=%v，想要明显红色增量", baseEdge, gotEdge)
	}
}

func renderDamageOverlayHeadless(
	t *testing.T,
	device gfx.Device,
	renderer *DamageOverlayRenderer,
	strength float32,
) []byte {
	t.Helper()
	color := device.CreateTexture(gfx.TextureDesc{
		Label: "damage overlay headless color",
		Width: damageOverlayHeadlessSize, Height: damageOverlayHeadlessSize,
		Format: gfx.FormatRGBA8Unorm,
		Usage:  gfx.TextureUsageRenderTarget | gfx.TextureUsageCopySrc,
	})
	defer color.Release()
	view := color.View(gfx.TextureViewDesc{Dimension: gfx.TextureViewDimension2D})
	defer view.Release()
	encoder := device.CreateCommandEncoder()
	clearPass := encoder.BeginRenderPass(gfx.RenderPassDesc{
		Label: "damage overlay test clear", ColorView: view,
		ClearColor: [4]float32{0.1, 0.1, 0.1, 1}, LoadClear: true,
	})
	clearPass.End()
	renderer.Render(encoder, view, strength)
	commands := encoder.Finish()
	device.Submit(commands)
	commands.Release()
	device.Poll(true)
	return color.ReadLayer(0, 0)
}

func damageOverlayPixel(pixels []byte, x, y int) [4]byte {
	offset := (y*damageOverlayHeadlessSize + x) * 4
	return [4]byte{pixels[offset], pixels[offset+1], pixels[offset+2], pixels[offset+3]}
}

func BenchmarkDamageOverlayHidden(b *testing.B) {
	device := &skyTestDevice{}
	renderer := NewDamageOverlayRenderer(device, gfx.FormatRGBA8Unorm)
	b.Cleanup(renderer.Release)
	b.ReportAllocs()
	for b.Loop() {
		renderer.Render(nil, nil, 0)
	}
}
