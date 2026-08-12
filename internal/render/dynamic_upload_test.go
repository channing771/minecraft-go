package render

import (
	"encoding/binary"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/gfx"
)

// Hand-derived avatar layout: camera [0,64), 256-byte aligned instances
// [256,3616), then five u32 indirect arguments [3616,3636).
// Mutation killed: separate camera/instance/indirect writes, wrong binding
// ranges, a stale instance count, or a wrong indirect draw offset all fail.
func TestAvatarRendererBatchesOneDynamicUploadPerNonEmptyRender(t *testing.T) {
	dev := &dynamicUploadTestDevice{}
	renderer := NewAvatarRenderer(dev, gfx.FormatRGBA8Unorm, gfx.FormatDepth32Float)
	defer renderer.Release()

	upload := dev.bufferByLabel(t, "avatar dynamic upload")
	if got, want := upload.desc.Size, uint64(3636); got != want {
		t.Fatalf("dynamic upload size=%d want=%d", got, want)
	}
	if got, want := upload.desc.Usage,
		gfx.BufferUsageUniform|gfx.BufferUsageStorage|gfx.BufferUsageIndirect|gfx.BufferUsageCopyDst; got != want {
		t.Fatalf("dynamic upload usage=%v want=%v", got, want)
	}
	if got, want := len(dev.buffers), 3; got != want {
		t.Fatalf("constructor buffers=%d want=%d (dynamic/vertices/indices)", got, want)
	}
	if got, want := len(dev.bindDescs), 1; got != want {
		t.Fatalf("bind groups=%d want=%d", got, want)
	}
	bind := dev.bindDescs[0]
	assertBufferBindingRange(t, bind, 0, upload, 0, 80)
	assertBufferBindingRange(t, bind, 1, upload, 256, 3360)

	dev.resetWrites()
	encoder := &dynamicUploadTestEncoder{}
	renderer.Render(encoder, avatarTestView{}, avatarTestView{}, Camera{}, nil)
	if got := dev.writeCount(); got != 0 {
		t.Fatalf("empty render writes=%d want=0", got)
	}
	if got := len(encoder.passes); got != 0 {
		t.Fatalf("empty render passes=%d want=0", got)
	}

	renderer.Render(encoder, avatarTestView{}, avatarTestView{}, Camera{ViewProj: mgl32.Ident4()}, makeTestAvatars(8))
	if got := dev.writeCount(); got != 1 {
		t.Fatalf("non-empty render dynamic writes=%d want=1", got)
	}
	if got := len(upload.writes); got != 1 {
		t.Fatalf("upload buffer writes=%d want=1", got)
	}
	write := upload.writes[0]
	if write.offset != 0 || len(write.data) != 3636 {
		t.Fatalf("upload write offset/bytes=%d/%d want=0/3636", write.offset, len(write.data))
	}
	for _, offset := range []int{0, 20, 40, 60} {
		if got := float32At(write.data, offset); got != 1 {
			t.Fatalf("camera identity diagonal at %d=%f want=1", offset, got)
		}
	}
	args := write.data[3616:3636]
	wantArgs := [...]uint32{36, 42, 0, 0, 0}
	for index, want := range wantArgs {
		if got := binary.LittleEndian.Uint32(args[index*4:]); got != want {
			t.Fatalf("indirect arg %d=%d want=%d", index, got, want)
		}
	}
	if got, want := len(encoder.passes), 1; got != want {
		t.Fatalf("passes=%d want=%d", got, want)
	}
	pass := encoder.passes[0]
	if pass.indirect != upload || pass.indirectOffset != 3616 || pass.indirectDraws != 1 {
		t.Fatalf("indirect draw buffer/offset/count=%p/%d/%d want=%p/3616/1",
			pass.indirect, pass.indirectOffset, pass.indirectDraws, upload)
	}
}

// 手工推导的 name-tag 布局：camera [0,96)，background 位于下一个
// 256-byte 边界 [256,768)，glyph 位于 [768,17152)。小而固定的
// background 区域放在前面，使每帧单次写入保持紧凑。
// 杀死变异：在 Prepare 中上传、保留三个 GPU buffer/write、忽略绑定范围，
// 或上传空布局都会产生可观察失败。
func TestNameTagRendererDefersAndBatchesOneDynamicUploadPerRender(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	dev := &dynamicUploadTestDevice{}
	renderer := NewNameTagRenderer(dev, gfx.FormatRGBA8Unorm, gfx.FormatDepth32Float, atlas)
	defer renderer.Release()

	upload := dev.bufferByLabel(t, "name-tag dynamic upload")
	if got, want := upload.desc.Size, uint64(17152); got != want {
		t.Fatalf("dynamic upload size=%d want=%d", got, want)
	}
	if got, want := upload.desc.Usage,
		gfx.BufferUsageUniform|gfx.BufferUsageStorage|gfx.BufferUsageCopyDst; got != want {
		t.Fatalf("dynamic upload usage=%v want=%v", got, want)
	}
	if got, want := len(dev.buffers), 1; got != want {
		t.Fatalf("constructor buffers=%d want=1 combined upload", got)
	}
	if got, want := len(dev.bindDescs), 1; got != want {
		t.Fatalf("bind groups=%d want=%d", got, want)
	}
	bind := dev.bindDescs[0]
	assertBufferBindingRange(t, bind, 0, upload, 0, 96)
	assertBufferBindingRange(t, bind, 1, upload, 768, 16384)
	assertBufferBindingRange(t, bind, 2, upload, 256, 512)

	if err := renderer.Prepare([]NameTag{{
		PlayerID: testNameTagID(1), Text: "AV", Anchor: mgl32.Vec3{3, 4, 5},
	}}, NewUploadBudget(1024)); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got := dev.writeCount(); got != 0 {
		t.Fatalf("Prepare GPU writes=%d want=0", got)
	}

	encoder := &dynamicUploadTestEncoder{}
	renderer.Render(encoder, &nameTagTestView{}, &nameTagTestView{}, BillboardCamera{
		ViewProj: mgl32.Ident4(), Right: mgl32.Vec3{0.25, 0.5, 0.75}, Up: mgl32.Vec3{-0.5, 0.125, 1},
	})
	if got := dev.writeCount(); got != 1 {
		t.Fatalf("Render dynamic writes=%d want=1", got)
	}
	if got := len(upload.writes); got != 1 {
		t.Fatalf("upload buffer writes=%d want=1", got)
	}
	write := upload.writes[0]
	if write.offset != 0 || len(write.data) != 896 {
		t.Fatalf("upload write offset/bytes=%d/%d want=0/896", write.offset, len(write.data))
	}
	if got := [3]float32{float32At(write.data, 64), float32At(write.data, 68), float32At(write.data, 72)}; got != [3]float32{0.25, 0.5, 0.75} {
		t.Fatalf("camera right=%v want=[0.25 0.5 0.75]", got)
	}
	if got := [3]float32{float32At(write.data, 768), float32At(write.data, 772), float32At(write.data, 776)}; got != [3]float32{3, 4, 5} {
		t.Fatalf("first glyph anchor=%v want=[3 4 5]", got)
	}
	if got := [3]float32{float32At(write.data, 256), float32At(write.data, 260), float32At(write.data, 264)}; got != [3]float32{3, 4, 5} {
		t.Fatalf("background anchor=%v want=[3 4 5]", got)
	}
	if got, want := len(encoder.passes), 1; got != want {
		t.Fatalf("passes=%d want=%d", got, want)
	}
}

func TestNameTagRendererEmptyLayoutHasNoDynamicUpload(t *testing.T) {
	dev := &dynamicUploadTestDevice{}
	renderer := NewNameTagRenderer(dev, gfx.FormatRGBA8Unorm, gfx.FormatDepth32Float, newFakeNameTagAtlas())
	defer renderer.Release()
	if err := renderer.Prepare([]NameTag{{PlayerID: testNameTagID(1)}}, NewUploadBudget(1024)); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	encoder := &dynamicUploadTestEncoder{}
	renderer.Render(encoder, &nameTagTestView{}, &nameTagTestView{}, BillboardCamera{})
	if got := dev.writeCount(); got != 0 {
		t.Fatalf("empty layout writes=%d want=0", got)
	}
	if got := len(encoder.passes); got != 0 {
		t.Fatalf("empty layout passes=%d want=0", got)
	}
}

func TestNameTagRendererReleasesOneCombinedUploadIdempotently(t *testing.T) {
	dev := &dynamicUploadTestDevice{}
	renderer := NewNameTagRenderer(dev, gfx.FormatRGBA8Unorm, gfx.FormatDepth32Float, newFakeNameTagAtlas())
	if got, want := len(dev.buffers), 1; got != want {
		t.Fatalf("owned buffers=%d want=1", got)
	}
	upload := dev.bufferByLabel(t, "name-tag dynamic upload")
	renderer.Release()
	renderer.Release()
	if got := upload.releases; got != 1 {
		t.Fatalf("combined upload releases=%d want=1", got)
	}
}

func assertBufferBindingRange(
	t *testing.T,
	desc gfx.BindGroupDesc,
	binding uint32,
	buffer gfx.Buffer,
	offset, size uint64,
) {
	t.Helper()
	for _, entry := range desc.Entries {
		if entry.Binding != binding {
			continue
		}
		if entry.Buffer != buffer || entry.Offset != offset || entry.Size != size {
			t.Fatalf("binding %d buffer/range=%p/(%d,%d) want=%p/(%d,%d)",
				binding, entry.Buffer, entry.Offset, entry.Size, buffer, offset, size)
		}
		return
	}
	t.Fatalf("binding %d missing", binding)
}

type dynamicUploadTestDevice struct {
	buffers   []*dynamicUploadTestBuffer
	bindDescs []gfx.BindGroupDesc
}

func (device *dynamicUploadTestDevice) CreateBuffer(desc gfx.BufferDesc) gfx.Buffer {
	buffer := &dynamicUploadTestBuffer{desc: desc}
	device.buffers = append(device.buffers, buffer)
	return buffer
}

func (*dynamicUploadTestDevice) CreateShaderModule(string) gfx.ShaderModule {
	return &dynamicUploadTestShader{}
}

func (*dynamicUploadTestDevice) CreateRenderPipeline(desc gfx.RenderPipelineDesc) gfx.RenderPipeline {
	return &dynamicUploadTestPipeline{label: desc.Label}
}

func (*dynamicUploadTestDevice) CreateComputePipeline(gfx.ComputePipelineDesc) gfx.ComputePipeline {
	panic("unexpected compute pipeline")
}

func (device *dynamicUploadTestDevice) CreateBindGroup(desc gfx.BindGroupDesc) gfx.BindGroup {
	device.bindDescs = append(device.bindDescs, desc)
	return &dynamicUploadTestBindGroup{}
}

func (*dynamicUploadTestDevice) CreateTexture(gfx.TextureDesc) gfx.Texture {
	panic("unexpected texture")
}

func (*dynamicUploadTestDevice) CreateSampler(gfx.SamplerDesc) gfx.Sampler {
	return &dynamicUploadTestSampler{}
}

func (*dynamicUploadTestDevice) CreateCommandEncoder() gfx.CommandEncoder {
	panic("unexpected encoder")
}

func (*dynamicUploadTestDevice) Submit(...gfx.CommandBuffer) {}
func (*dynamicUploadTestDevice) Poll(bool)                   {}
func (*dynamicUploadTestDevice) Release()                    {}

func (device *dynamicUploadTestDevice) bufferByLabel(t *testing.T, label string) *dynamicUploadTestBuffer {
	t.Helper()
	for _, buffer := range device.buffers {
		if buffer.desc.Label == label {
			return buffer
		}
	}
	t.Fatalf("buffer %q was not created", label)
	return nil
}

func (device *dynamicUploadTestDevice) resetWrites() {
	for _, buffer := range device.buffers {
		buffer.writes = nil
	}
}

func (device *dynamicUploadTestDevice) writeCount() int {
	count := 0
	for _, buffer := range device.buffers {
		count += len(buffer.writes)
	}
	return count
}

type dynamicUploadTestWrite struct {
	offset uint64
	data   []byte
}

type dynamicUploadTestBuffer struct {
	desc     gfx.BufferDesc
	writes   []dynamicUploadTestWrite
	releases int
}

func (buffer *dynamicUploadTestBuffer) Size() uint64 { return buffer.desc.Size }

func (buffer *dynamicUploadTestBuffer) Write(offset uint64, data []byte) {
	buffer.writes = append(buffer.writes, dynamicUploadTestWrite{
		offset: offset,
		data:   append([]byte(nil), data...),
	})
}

func (*dynamicUploadTestBuffer) ReadBack() []byte { panic("unexpected readback") }
func (buffer *dynamicUploadTestBuffer) Release()  { buffer.releases++ }

type dynamicUploadTestShader struct{}

func (*dynamicUploadTestShader) Release() {}

type dynamicUploadTestPipeline struct{ label string }

func (*dynamicUploadTestPipeline) Release() {}

type dynamicUploadTestBindGroup struct{}

func (*dynamicUploadTestBindGroup) Release() {}

type dynamicUploadTestSampler struct{}

func (*dynamicUploadTestSampler) Release() {}

type dynamicUploadTestEncoder struct{ passes []*dynamicUploadTestPass }

func (encoder *dynamicUploadTestEncoder) BeginRenderPass(desc gfx.RenderPassDesc) gfx.RenderPass {
	pass := &dynamicUploadTestPass{desc: desc}
	encoder.passes = append(encoder.passes, pass)
	return pass
}

func (*dynamicUploadTestEncoder) BeginComputePass(string) gfx.ComputePass {
	panic("unexpected compute pass")
}

func (*dynamicUploadTestEncoder) CopyBufferToBuffer(gfx.Buffer, uint64, gfx.Buffer, uint64, uint64) {
	panic("unexpected buffer copy")
}

func (*dynamicUploadTestEncoder) Finish() gfx.CommandBuffer { panic("unexpected finish") }

type dynamicUploadTestPass struct {
	desc           gfx.RenderPassDesc
	indirect       gfx.Buffer
	indirectOffset uint64
	indirectDraws  int
}

func (*dynamicUploadTestPass) SetPipeline(gfx.RenderPipeline)             {}
func (*dynamicUploadTestPass) SetBindGroup(uint32, gfx.BindGroup)         {}
func (*dynamicUploadTestPass) SetVertexBuffer(uint32, gfx.Buffer, uint64) {}
func (*dynamicUploadTestPass) SetIndexBuffer(gfx.Buffer, uint64)          {}

func (pass *dynamicUploadTestPass) DrawIndexedIndirect(buffer gfx.Buffer, offset uint64) {
	pass.indirect = buffer
	pass.indirectOffset = offset
	pass.indirectDraws++
}

func (*dynamicUploadTestPass) Draw(uint32, uint32) {}
func (*dynamicUploadTestPass) End()                {}
