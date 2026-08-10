package render

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/assets"
	"minecraft-go/internal/core"
	"minecraft-go/internal/gfx"
	"minecraft-go/internal/mesh"
)

func TestBuildBlockOutlinePartsMakesTwelveExpandedEdges(t *testing.T) {
	position := core.BlockPos{X: 4, Y: 5, Z: 6}
	parts := buildBlockOutlineParts(nil, position)
	if len(parts) != 12 {
		t.Fatalf("轮廓实例数 = %d，想要 12", len(parts))
	}

	var longAxes [3]int
	wantColor := [4]float32{1, 1, 1, 0.86}
	for index, part := range parts {
		bounds := transformedUnitCubeBounds(part.transform)
		size := bounds.max.Sub(bounds.min)
		longAxis := -1
		for axis := range 3 {
			switch {
			case outlineFloatNear(size[axis], 1.006):
				if longAxis != -1 {
					t.Fatalf("实例 %d 有多个长轴: %v", index, size)
				}
				longAxis = axis
			case !outlineFloatNear(size[axis], 0.018):
				t.Fatalf("实例 %d 尺寸 = %v，想要一轴 1.006、两轴 0.018", index, size)
			}
		}
		if longAxis == -1 {
			t.Fatalf("实例 %d 没有 1.006 长轴: %v", index, size)
		}
		longAxes[longAxis]++
		if part.color != wantColor {
			t.Fatalf("实例 %d 颜色 = %v，想要 %v", index, part.color, wantColor)
		}
	}
	if longAxes != [3]int{4, 4, 4} {
		t.Fatalf("X/Y/Z 长边数 = %v，想要 [4 4 4]", longAxes)
	}

	bounds := avatarPartsBounds(parts)
	assertVec3Near(t, bounds.min, mgl32.Vec3{3.997, 4.997, 5.997})
	assertVec3Near(t, bounds.max, mgl32.Vec3{5.003, 6.003, 7.003})
}

func TestBlockOutlineRendererUsesFixedTransparentDepthPass(t *testing.T) {
	dev := &avatarTestDevice{}
	renderer := NewBlockOutlineRenderer(dev, gfx.FormatRGBA8Unorm, gfx.FormatDepth32Float)
	defer renderer.Release()

	upload := dev.bufferByLabel(t, "block outline dynamic upload")
	if upload.desc.Size != 1236 {
		t.Fatalf("dynamic upload 大小 = %d，想要 1236", upload.desc.Size)
	}
	wantUsage := gfx.BufferUsageUniform | gfx.BufferUsageStorage |
		gfx.BufferUsageIndirect | gfx.BufferUsageCopyDst
	if upload.desc.Usage != wantUsage {
		t.Fatalf("dynamic upload usage = %v，想要 %v", upload.desc.Usage, wantUsage)
	}
	if len(dev.buffers) != 3 {
		t.Fatalf("构造 buffer 数 = %d，想要 3", len(dev.buffers))
	}
	assertBufferBindingRange(t, dev.bindDesc, 0, upload, 0, 80)
	assertBufferBindingRange(t, dev.bindDesc, 1, upload, 256, 960)
	if desc := dev.pipelineDesc; desc.DepthFormat != gfx.FormatDepth32Float ||
		desc.DepthWrite || !desc.DepthCompareLessEqual || desc.Blend != gfx.BlendAlpha {
		t.Fatalf("pipeline = %+v，想要 Depth32Float、LessEqual 只读深度和 alpha blend", desc)
	}

	encoder := &blockOutlineTestEncoder{}
	renderer.Render(encoder, avatarTestView{}, avatarTestView{}, Camera{}, BlockOutline{})
	if len(encoder.passes) != 0 || len(upload.lastWrite) != 0 {
		t.Fatalf("隐藏轮廓产生 pass/write = %d/%d", len(encoder.passes), len(upload.lastWrite))
	}

	target := avatarTestView{}
	depth := avatarTestView{}
	renderer.Render(encoder, target, depth, Camera{ViewProj: mgl32.Ident4()}, BlockOutline{
		Visible: true, Position: core.BlockPos{X: 1, Y: 2, Z: 3},
	})
	if len(encoder.passes) != 1 {
		t.Fatalf("可见轮廓 pass 数 = %d，想要 1", len(encoder.passes))
	}
	pass := encoder.passes[0]
	if pass.desc.Label != "block outline pass" || pass.desc.LoadClear ||
		pass.desc.ColorView != target || pass.desc.DepthView != depth {
		t.Fatalf("pass descriptor = %+v，想要加载既有颜色和深度", pass.desc)
	}
	if !pass.setIndex || pass.indirect != upload || pass.indirectOffset != 1216 ||
		pass.indirectDraws != 1 || !pass.ended {
		t.Fatalf("indexed indirect = index:%v buffer:%p offset:%d draws:%d ended:%v",
			pass.setIndex, pass.indirect, pass.indirectOffset, pass.indirectDraws, pass.ended)
	}
	if len(upload.lastWrite) != 1236 {
		t.Fatalf("单次 dynamic upload = %d bytes，想要 1236", len(upload.lastWrite))
	}
	wantArgs := [...]uint32{36, 12, 0, 0, 0}
	for index, want := range wantArgs {
		if got := binary.LittleEndian.Uint32(upload.lastWrite[1216+index*4:]); got != want {
			t.Fatalf("indirect 参数 %d = %d，想要 %d", index, got, want)
		}
	}
}

func TestBlockOutlineRendererStableRenderDoesNotAllocate(t *testing.T) {
	renderer := &BlockOutlineRenderer{
		dynamic: &allocationRenderBuffer{},
		parts:   make([]avatarPart, 0, 12),
		upload:  make([]byte, 1236),
	}
	encoder := &allocationCommandEncoder{}
	outline := BlockOutline{Visible: true, Position: core.BlockPos{X: 1, Y: 2, Z: 3}}
	renderer.Render(encoder, nil, nil, Camera{}, outline)

	if allocations := testing.AllocsPerRun(100, func() {
		renderer.Render(encoder, nil, nil, Camera{}, outline)
	}); allocations != 0 {
		t.Fatalf("稳定轮廓 Render 分配 = %v，想要 0", allocations)
	}
	if len(renderer.parts) != 12 || cap(renderer.parts) != 12 || len(renderer.upload) != 1236 {
		t.Fatalf("固定存储 parts=%d/%d upload=%d，想要 12/12/1236",
			len(renderer.parts), cap(renderer.parts), len(renderer.upload))
	}
}

func TestBlockOutlineRendererReleaseIsIdempotent(t *testing.T) {
	dynamic := &avatarReleaseBuffer{}
	vertices := &avatarReleaseBuffer{}
	indices := &avatarReleaseBuffer{}
	pipeline := &avatarReleasePipeline{}
	bind := &avatarReleaseBindGroup{}
	renderer := &BlockOutlineRenderer{
		dynamic: dynamic, vertices: vertices, indices: indices,
		pipeline: pipeline, bind: bind,
	}

	renderer.Release()
	renderer.Release()
	for name, releases := range map[string]int{
		"dynamic": dynamic.releases, "vertices": vertices.releases,
		"indices": indices.releases, "pipeline": pipeline.releases,
		"bind": bind.releases,
	} {
		if releases != 1 {
			t.Errorf("%s release 次数 = %d，想要 1", name, releases)
		}
	}
}

// 非法 WGSL、binding、depth 或 pass 顺序会在 Submit/Poll 时触发校验错误。
func TestBlockOutlineRendererHeadlessClearOccluderAndDraw(t *testing.T) {
	dev, err := gfx.NewHeadlessDevice()
	if err != nil {
		t.Skipf("本机无可用 GPU 适配器: %v", err)
	}
	defer dev.Release()

	color := dev.CreateTexture(gfx.TextureDesc{
		Label: "block outline test color", Width: 64, Height: 64,
		Format: gfx.FormatRGBA8Unorm, Usage: gfx.TextureUsageRenderTarget,
	})
	defer color.Release()
	colorView := color.View(gfx.TextureViewDesc{})
	defer colorView.Release()
	depth := dev.CreateTexture(gfx.TextureDesc{
		Label: "block outline test depth", Width: 64, Height: 64,
		Format: gfx.FormatDepth32Float, Usage: gfx.TextureUsageRenderTarget,
	})
	defer depth.Release()
	depthView := depth.View(gfx.TextureViewDesc{Aspect: gfx.AspectDepthOnly})
	defer depthView.Release()

	registry := assets.NewRegistry()
	occluder := New(dev, registry, gfx.FormatRGBA8Unorm)
	defer occluder.Release()
	quads := make([]mesh.Quad, 0, 6)
	for face := mesh.FaceNegX; face <= mesh.FacePosZ; face++ {
		quads = append(quads, mesh.Quad{
			W: 1, H: 1, Face: face, Mat: registry.Material(core.StoneID, face),
			AO: 0xff, Light: 0xff,
		})
	}
	occluder.QueueSection(core.SectionPos{Y: 4}, quads)
	occluder.BeginFrame()
	occluder.FlushUploads(core.ChunkPos{})
	renderer := NewBlockOutlineRenderer(dev, gfx.FormatRGBA8Unorm, gfx.FormatDepth32Float)
	defer renderer.Release()

	cameraPosition := mgl32.Vec3{2, 2, 4}
	viewProj := core.Perspective(mgl32.DegToRad(60), 1, 0.1, 100).Mul4(
		mgl32.LookAtV(cameraPosition, mgl32.Vec3{0.5, 0.5, 0.5}, mgl32.Vec3{0, 1, 0}),
	)
	camera := Camera{
		ViewProj: viewProj, ViewProjInv: viewProj.Inv(), Pos: cameraPosition, Daylight: 1,
	}
	encoder := dev.CreateCommandEncoder()
	occluder.Render(encoder, colorView, depthView, camera)
	if got := occluder.LastFrameStats().CandidateFaces; got != 6 {
		t.Fatalf("遮挡石块候选面 = %d，想要 6", got)
	}
	renderer.Render(encoder, colorView, depthView, camera, BlockOutline{
		Visible: true,
	})
	commands := encoder.Finish()
	dev.Submit(commands)
	commands.Release()
	dev.Poll(true)
}

func outlineFloatNear(got, want float32) bool {
	return math.Abs(float64(got-want)) <= 1e-5
}

type blockOutlineTestEncoder struct {
	passes []*blockOutlineTestPass
}

func (encoder *blockOutlineTestEncoder) BeginRenderPass(desc gfx.RenderPassDesc) gfx.RenderPass {
	pass := &blockOutlineTestPass{desc: desc}
	encoder.passes = append(encoder.passes, pass)
	return pass
}
func (*blockOutlineTestEncoder) BeginComputePass(string) gfx.ComputePass {
	panic("轮廓不应创建 compute pass")
}
func (*blockOutlineTestEncoder) CopyBufferToBuffer(gfx.Buffer, uint64, gfx.Buffer, uint64, uint64) {
	panic("轮廓不应复制 buffer")
}
func (*blockOutlineTestEncoder) Finish() gfx.CommandBuffer {
	panic("描述符测试不应结束 encoder")
}

type blockOutlineTestPass struct {
	desc           gfx.RenderPassDesc
	indirect       gfx.Buffer
	indirectOffset uint64
	indirectDraws  int
	setIndex       bool
	ended          bool
}

func (*blockOutlineTestPass) SetPipeline(gfx.RenderPipeline)             {}
func (*blockOutlineTestPass) SetBindGroup(uint32, gfx.BindGroup)         {}
func (*blockOutlineTestPass) SetVertexBuffer(uint32, gfx.Buffer, uint64) {}
func (pass *blockOutlineTestPass) SetIndexBuffer(gfx.Buffer, uint64)     { pass.setIndex = true }
func (pass *blockOutlineTestPass) DrawIndexedIndirect(buffer gfx.Buffer, offset uint64) {
	pass.indirect = buffer
	pass.indirectOffset = offset
	pass.indirectDraws++
}
func (*blockOutlineTestPass) Draw(uint32, uint32) { panic("轮廓应使用 indexed indirect draw") }
func (pass *blockOutlineTestPass) End()           { pass.ended = true }
