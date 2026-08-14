package render

import (
	"encoding/binary"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/gfx"
)

// Mutation killed: iterating over UTF-8 bytes, omitting A/V kerning, or emitting
// fewer/more than one glyph instance per rune changes these literal results.
func TestNameTagLayoutUsesUnicodeRunesAndKerning(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	atlas.kerns[[2]rune{'A', 'V'}] = -2
	layout := layoutNameTags(nil, atlas, []NameTag{{Key: testEntityKey(testNameTagID(1)), Text: "AV 中文"}})
	if got, want := len(layout.glyphs), 5; got != want {
		t.Fatalf("glyphs=%d want=%d", got, want)
	}
	if got, want := layout.glyphs[1].X, float32(8); got != want {
		t.Fatalf("second x=%f want=%f", got, want)
	}

	long := strings.Repeat("中", 33)
	layout = layoutNameTags(nil, atlas, []NameTag{{Key: testEntityKey(testNameTagID(1)), Text: long}})
	if got, want := len(layout.glyphs), 32; got != want {
		t.Fatalf("Unicode-truncated glyphs=%d want=%d", got, want)
	}
}

func TestNameTagRendererAcceptsTwelveAndRejectsThirteenBeforeAtlasMutation(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	dev := &nameTagTestDevice{}
	renderer := NewNameTagRenderer(dev, gfx.FormatRGBA8Unorm, gfx.FormatDepth32Float, atlas)
	defer renderer.Release()
	tags := makeEntityNameTags(12, strings.Repeat("A", 32))
	if err := renderer.Prepare(tags, NewUploadBudget(1<<20)); err != nil {
		t.Fatalf("12 个 NameTag Prepare: %v", err)
	}
	if got, want := dev.bufferByLabel(t, "name-tag dynamic upload").desc.Size, uint64(25600); got != want {
		t.Fatalf("dynamic upload size=%d，想要 %d", got, want)
	}
	if len(renderer.layout.backgrounds) != 12 || len(renderer.layout.glyphs) != 384 {
		t.Fatalf("12 个 NameTag layout=%d/%d", len(renderer.layout.backgrounds), len(renderer.layout.glyphs))
	}
	if nameTagBackgroundSize != 768 || nameTagGlyphOffset != 1024 ||
		nameTagGlyphSize != 24576 || nameTagUploadBytes != 25600 {
		t.Fatalf("NameTag 布局=%d/%d/%d/%d，想要 768/1024/24576/25600",
			nameTagBackgroundSize, nameTagGlyphOffset, nameTagGlyphSize, nameTagUploadBytes)
	}
	encoder := &nameTagTestEncoder{}
	renderer.Render(encoder, &nameTagTestView{}, &nameTagTestView{}, BillboardCamera{})
	upload := dev.bufferByLabel(t, "name-tag dynamic upload")
	if got, want := len(upload.lastWrite), 25600; got != want {
		t.Fatalf("12 个 32-rune NameTag upload=%d，想要 %d", got, want)
	}
	if got, want := len(encoder.passes), 1; got != want {
		t.Fatalf("12 个 NameTag pass=%d，想要 %d", got, want)
	}
	wantOrdered := append([]NameTag(nil), renderer.ordered...)
	wantLayout := nameTagLayout{
		glyphs:      append([]nameTagGlyph(nil), renderer.layout.glyphs...),
		backgrounds: append([]nameTagBackground(nil), renderer.layout.backgrounds...),
	}
	wantUpload := append([]byte(nil), renderer.upload...)
	wantRequested := make(map[rune]struct{}, len(atlas.requested))
	for char := range atlas.requested {
		wantRequested[char] = struct{}{}
	}
	wantFlushes := atlas.flushes
	upload.lastWrite = nil
	encoder.passes = nil
	if err := renderer.Prepare(makeEntityNameTags(13, "Z"), NewUploadBudget(1<<20)); err == nil {
		t.Fatal("13 个 NameTag 未被拒绝")
	}
	if len(upload.lastWrite) != 0 || len(encoder.passes) != 0 {
		t.Fatalf("overflow 后 GPU write/pass=%d/%d，想要 0/0", len(upload.lastWrite), len(encoder.passes))
	}
	if atlas.flushes != wantFlushes || !reflect.DeepEqual(atlas.requested, wantRequested) {
		t.Fatalf("overflow 改变 atlas：flushes=%d/%d requested=%v/%v",
			atlas.flushes, wantFlushes, atlas.requested, wantRequested)
	}
	if !reflect.DeepEqual(renderer.ordered, wantOrdered) ||
		!reflect.DeepEqual(renderer.layout, wantLayout) ||
		!reflect.DeepEqual(renderer.upload, wantUpload) {
		t.Fatal("overflow 改变了 NameTagRenderer 上一帧状态")
	}
}

func makeEntityNameTags(count int, text string) []NameTag {
	tags := make([]NameTag, count)
	for index := range tags {
		last := byte(count - index)
		tags[index] = NameTag{
			Key:    EntityKey{Kind: EntityPlayer, ID: [16]byte(testNameTagID(last))},
			Text:   text,
			Anchor: mgl32.Vec3{float32(last), 2, 3},
		}
	}
	return tags
}

// Mutation killed: using a zero/default advance for a missing rune places the
// following A at x=0 or x=10 instead of the tofu glyph's hand-set 17 pixels.
func TestNameTagLayoutUsesTofuAdvanceForMissingRune(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	atlas.tofu.Advance = 17
	layout := layoutNameTags(nil, atlas, []NameTag{{
		Key: testEntityKey(testNameTagID(1)), Text: "\u0378A",
	}})
	if got, want := layout.glyphs[1].X, float32(17); got != want {
		t.Fatalf("glyph after tofu x=%f want=%f", got, want)
	}
}

// Mutation killed: omitting a background, making it opaque, drawing it after
// glyphs, or changing 4px horizontal / 2px vertical padding fails this check.
func TestNameTagLayoutAddsOnePaddedTransparentBackground(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	layout := layoutNameTags(nil, atlas, []NameTag{{Key: testEntityKey(testNameTagID(1)), Text: "AV"}})
	if got, want := len(layout.backgrounds), 1; got != want {
		t.Fatalf("backgrounds=%d want=%d", got, want)
	}
	background := layout.backgrounds[0]
	if got, want := [4]float32{background.X, background.Y, background.Width, background.Height}, ([4]float32{-4, -12, 28, 16}); got != want {
		t.Fatalf("background rect=%v want=%v", got, want)
	}
	if background.Color[3] <= 0 || background.Color[3] >= 1 {
		t.Fatalf("background alpha=%f; want strictly translucent", background.Color[3])
	}
}

// 杀死变异：排序错误、遗漏第十二个名牌或依赖输入顺序都会改变这些结果。
func TestNameTagLayoutSortsTwelveTags(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	tags := make([]NameTag, 12)
	for index := range tags {
		id := byte(12 - index)
		tags[index] = NameTag{
			Key:    testEntityKey(testNameTagID(id)),
			Text:   strings.Repeat("中", 32),
			Anchor: mgl32.Vec3{float32(id), 2, 3},
		}
	}
	layout := layoutNameTags(nil, atlas, tags)
	if got, want := len(layout.glyphs), 384; got != want {
		t.Fatalf("glyphs=%d want=%d", got, want)
	}
	if got, want := len(layout.backgrounds), 12; got != want {
		t.Fatalf("backgrounds=%d want=%d", got, want)
	}
	if got, want := layout.glyphs[0].Anchor[0], float32(1); got != want {
		t.Fatalf("first selected anchor x=%f want=%f", got, want)
	}
	if got, want := layout.glyphs[len(layout.glyphs)-1].Anchor[0], float32(12); got != want {
		t.Fatalf("last selected anchor x=%f want=%f", got, want)
	}

	forward := layoutNameTags(nil, atlas, append([]NameTag(nil), tags...))
	reversed := append([]NameTag(nil), tags...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	backward := layoutNameTags(nil, atlas, reversed)
	if !reflect.DeepEqual(forward, backward) {
		t.Fatal("layout changed when input order changed")
	}
}

// Mutation killed: allocating even one glyph/background for empty text makes
// the observable layout non-empty.
func TestNameTagLayoutSkipsEmptyText(t *testing.T) {
	layout := layoutNameTags(nil, newFakeNameTagAtlas(), []NameTag{{
		Key: testEntityKey(testNameTagID(1)), Text: "",
	}})
	if len(layout.glyphs) != 0 || len(layout.backgrounds) != 0 {
		t.Fatalf("empty text layout=%+v; want no instances", layout)
	}
}

// Mutation killed: flushing before every selected text has been requested,
// flushing more than once, or laying out before Flush leaves R at tofu x=17.
func TestNameTagPrepareRequestsAllThenFlushesOnceBeforeLayout(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	atlas.strictFlushRunes = map[rune]struct{}{'Q': {}, 'R': {}}
	atlas.flushGlyphs = map[rune]Glyph{
		'Q': fakeNameTagGlyph(19),
		'R': fakeNameTagGlyph(11),
	}
	dev := &nameTagTestDevice{}
	renderer := NewNameTagRenderer(dev, gfx.FormatRGBA8Unorm, gfx.FormatDepth32Float, atlas)
	defer renderer.Release()

	if err := renderer.Prepare([]NameTag{{Key: testEntityKey(testNameTagID(1)), Text: "QR"}}, NewUploadBudget(1024)); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got, want := renderer.layout.glyphs[1].X, float32(19); got != want {
		t.Fatalf("post-flush second glyph x=%f want=%f", got, want)
	}
	if got := len(dev.bufferByLabel(t, "name-tag dynamic upload").lastWrite); got != 0 {
		t.Fatalf("Prepare GPU upload bytes=%d want=0", got)
	}
	if got := float32At(renderer.upload, nameTagGlyphOffset+nameTagInstanceBytes+16); got != 19 {
		t.Fatalf("second encoded glyph x=%f want=19", got)
	}
	if got := float32At(renderer.upload, 256+60); got <= 0 || got >= 1 {
		t.Fatalf("encoded background alpha=%f want strictly translucent", got)
	}
}

// Mutation killed: wrapping the atlas upload error changes its identity, while
// continuing after the error publishes a new layout or overwrites GPU buffers.
func TestNameTagPreparePropagatesFlushError(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	dev := &nameTagTestDevice{}
	renderer := NewNameTagRenderer(dev, gfx.FormatRGBA8Unorm, gfx.FormatDepth32Float, atlas)
	defer renderer.Release()
	wantAnchor := mgl32.Vec3{1, 2, 3}
	if err := renderer.Prepare([]NameTag{{
		Key: testEntityKey(testNameTagID(1)), Text: "A", Anchor: wantAnchor,
	}}, NewUploadBudget(1024)); err != nil {
		t.Fatalf("initial Prepare: %v", err)
	}
	if got := len(renderer.layout.glyphs); got != 1 || renderer.layout.glyphs[0].Anchor != wantAnchor {
		t.Fatalf("initial layout glyphs=%d anchor=%v want=1/%v", got, renderer.layout.glyphs[0].Anchor, wantAnchor)
	}
	wantLayout := nameTagLayout{
		glyphs:      append([]nameTagGlyph(nil), renderer.layout.glyphs...),
		backgrounds: append([]nameTagBackground(nil), renderer.layout.backgrounds...),
	}
	uploadBuffer := dev.bufferByLabel(t, "name-tag dynamic upload")
	wantUpload := append([]byte(nil), renderer.upload...)
	wantGPUWrite := append([]byte(nil), uploadBuffer.lastWrite...)
	if got := float32At(wantUpload, nameTagGlyphOffset); got != wantAnchor[0] {
		t.Fatalf("initial encoded glyph anchor x=%f want=%f", got, wantAnchor[0])
	}
	if len(wantGPUWrite) != 0 {
		t.Fatalf("initial Prepare GPU upload bytes=%d want=0", len(wantGPUWrite))
	}

	flushErr := errors.New("upload failed")
	atlas.flushErr = flushErr
	got := renderer.Prepare([]NameTag{{
		Key: testEntityKey(testNameTagID(2)), Text: "VV", Anchor: mgl32.Vec3{9, 8, 7},
	}}, NewUploadBudget(1024))
	if got != flushErr {
		t.Errorf("Prepare error=%v want exact error %v", got, flushErr)
	}
	if !reflect.DeepEqual(renderer.layout, wantLayout) {
		t.Errorf("layout changed after flush error: got=%+v want=%+v", renderer.layout, wantLayout)
	}
	if !reflect.DeepEqual(renderer.upload, wantUpload) {
		t.Errorf("CPU upload changed after flush error")
	}
	if !reflect.DeepEqual(uploadBuffer.lastWrite, wantGPUWrite) {
		t.Errorf("GPU upload changed after flush error")
	}
}

// Mutation killed: dynamic buffer creation, replace blending, depth writes,
// clearing attachments, glyph-before-background, or ignoring Right/Up changes
// the captured descriptors, draw order, buffer count, or camera bytes.
func TestNameTagRendererUsesFixedTransparentDepthPass(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	dev := &nameTagTestDevice{}
	renderer := NewNameTagRenderer(dev, gfx.FormatRGBA8Unorm, gfx.FormatDepth32Float, atlas)
	defer renderer.Release()

	upload := dev.bufferByLabel(t, "name-tag dynamic upload")
	if got, want := upload.desc.Size, uint64(25600); got != want {
		t.Fatalf("dynamic upload size=%d want=%d", got, want)
	}
	if got, want := upload.desc.Usage, gfx.BufferUsageUniform|gfx.BufferUsageStorage|gfx.BufferUsageCopyDst; got != want {
		t.Fatalf("dynamic upload usage=%v want=%v", got, want)
	}
	if got, want := len(dev.buffers), 1; got != want {
		t.Fatalf("constructor buffers=%d want=%d", got, want)
	}
	if got, want := len(dev.pipelineDescs), 2; got != want {
		t.Fatalf("pipeline descriptors=%d want=%d", got, want)
	}
	for _, desc := range dev.pipelineDescs {
		if desc.Blend != gfx.BlendAlpha || desc.DepthWrite || desc.DepthFormat != gfx.FormatDepth32Float {
			t.Fatalf("pipeline %q=%+v; want alpha blend, depth less/read-only", desc.Label, desc)
		}
	}

	if err := renderer.Prepare([]NameTag{{Key: testEntityKey(testNameTagID(1)), Text: "A"}}, NewUploadBudget(1024)); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(upload.lastWrite) != 0 {
		t.Fatalf("Prepare wrote %d GPU bytes want=0", len(upload.lastWrite))
	}
	encoder := &nameTagTestEncoder{}
	camera := BillboardCamera{
		ViewProj: mgl32.Ident4(),
		Right:    mgl32.Vec3{0.25, 0.5, 0.75},
		Up:       mgl32.Vec3{-0.5, 0.125, 1},
	}
	renderer.Render(encoder, &nameTagTestView{}, &nameTagTestView{}, camera)
	if got, want := len(dev.buffers), 1; got != want {
		t.Fatalf("Render created buffers: got=%d want=%d", got, want)
	}
	if got, want := len(encoder.passes), 1; got != want {
		t.Fatalf("passes=%d want=%d", got, want)
	}
	pass := encoder.passes[0]
	if pass.desc.Label != "name-tag pass" || pass.desc.LoadClear {
		t.Fatalf("pass descriptor=%+v; want load existing color/depth", pass.desc)
	}
	if got, want := pass.pipelineLabels, []string{"name-tag background", "name-tag glyph"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pipeline order=%v want=%v", got, want)
	}
	if got, want := pass.drawInstances, []uint32{1, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("draw instance counts=%v want=%v", got, want)
	}
	if !pass.ended {
		t.Fatal("name-tag pass was not ended")
	}

	cameraBytes := upload.lastWrite[:96]
	gotRight := [3]float32{float32At(cameraBytes, 64), float32At(cameraBytes, 68), float32At(cameraBytes, 72)}
	gotUp := [3]float32{float32At(cameraBytes, 80), float32At(cameraBytes, 84), float32At(cameraBytes, 88)}
	if gotRight != [3]float32{0.25, 0.5, 0.75} || gotUp != [3]float32{-0.5, 0.125, 1} {
		t.Fatalf("camera Right/Up=%v/%v", gotRight, gotUp)
	}
}

// Mutation killed: creating a pass for an empty prepared layout emits one
// observable pass instead of none.
func TestNameTagRendererSkipsEmptyPreparedLayout(t *testing.T) {
	renderer := NewNameTagRenderer(&nameTagTestDevice{}, gfx.FormatRGBA8Unorm, gfx.FormatDepth32Float, newFakeNameTagAtlas())
	defer renderer.Release()
	if err := renderer.Prepare([]NameTag{{Key: testEntityKey(testNameTagID(1))}}, NewUploadBudget(1024)); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	encoder := &nameTagTestEncoder{}
	renderer.Render(encoder, &nameTagTestView{}, &nameTagTestView{}, BillboardCamera{})
	if len(encoder.passes) != 0 {
		t.Fatalf("empty layout passes=%d want=0", len(encoder.passes))
	}
}

// Mutation killed: releasing the borrowed atlas/view, leaking an owned handle,
// or failing idempotency changes at least one exact release count.
func TestNameTagRendererReleaseOwnsOnlyItsHandles(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	dev := &nameTagTestDevice{}
	renderer := NewNameTagRenderer(dev, gfx.FormatRGBA8Unorm, gfx.FormatDepth32Float, atlas)
	renderer.Release()
	renderer.Release()

	for _, buffer := range dev.buffers {
		if buffer.releases != 1 {
			t.Errorf("buffer %q releases=%d want=1", buffer.desc.Label, buffer.releases)
		}
	}
	for _, pipeline := range dev.pipelines {
		if pipeline.releases != 1 {
			t.Errorf("pipeline %q releases=%d want=1", pipeline.label, pipeline.releases)
		}
	}
	if dev.bind.releases != 1 || dev.sampler.releases != 1 {
		t.Errorf("bind/sampler releases=%d/%d want=1/1", dev.bind.releases, dev.sampler.releases)
	}
	if atlas.view.releases != 0 || atlas.releases != 0 {
		t.Fatalf("borrowed atlas/view releases=%d/%d want=0/0", atlas.releases, atlas.view.releases)
	}
}

// Mutation killed: invalid WGSL bindings, buffer layouts, attachment formats,
// or pass sequencing triggers a WebGPU validation panic during submit/poll.
func TestNameTagRendererHeadlessClearOccluderAndDraw(t *testing.T) {
	dev, err := gfx.NewHeadlessDevice()
	if err != nil {
		t.Skipf("本机无可用 GPU 适配器: %v", err)
	}
	defer dev.Release()

	color := dev.CreateTexture(gfx.TextureDesc{
		Label: "name-tag test color", Width: 64, Height: 64,
		Format: gfx.FormatRGBA8Unorm, Usage: gfx.TextureUsageRenderTarget,
	})
	defer color.Release()
	colorView := color.View(gfx.TextureViewDesc{})
	defer colorView.Release()
	depth := dev.CreateTexture(gfx.TextureDesc{
		Label: "name-tag test depth", Width: 64, Height: 64,
		Format: gfx.FormatDepth32Float, Usage: gfx.TextureUsageRenderTarget,
	})
	defer depth.Release()
	depthView := depth.View(gfx.TextureViewDesc{Aspect: gfx.AspectDepthOnly})
	defer depthView.Release()

	atlas, err := NewGlyphAtlas(dev)
	if err != nil {
		t.Fatalf("NewGlyphAtlas: %v", err)
	}
	defer atlas.Release()
	avatar := NewAvatarRenderer(dev, gfx.FormatRGBA8Unorm, gfx.FormatDepth32Float)
	defer avatar.Release()
	renderer := NewNameTagRenderer(dev, gfx.FormatRGBA8Unorm, gfx.FormatDepth32Float, atlas)
	defer renderer.Release()
	if err := renderer.Prepare([]NameTag{{
		Key: testEntityKey(testNameTagID(1)), Text: "A中", Anchor: mgl32.Vec3{0, 0, 0.5},
	}}, NewUploadBudget(1024)); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	encoder := dev.CreateCommandEncoder()
	clear := encoder.BeginRenderPass(gfx.RenderPassDesc{
		Label: "terrain clear", ColorView: colorView, DepthView: depthView, LoadClear: true,
	})
	clear.End()
	if err := avatar.Render(encoder, colorView, depthView, Camera{ViewProj: mgl32.Ident4()}, []Avatar{{
		Key: testEntityKey(testNameTagID(2)), Position: mgl32.Vec3{0, -0.9, 0},
	}}); err != nil {
		t.Fatal(err)
	}
	renderer.Render(encoder, colorView, depthView, BillboardCamera{
		ViewProj: mgl32.Ident4(), Right: mgl32.Vec3{1, 0, 0}, Up: mgl32.Vec3{0, 1, 0},
	})
	commands := encoder.Finish()
	dev.Submit(commands)
	commands.Release()
	dev.Poll(true)
}

func testNameTagID(last byte) core.PlayerID {
	return core.PlayerID{0, 1, 2, 3, 4, 5, 0x46, 7, 0x88, 9, 10, 11, 12, 13, 14, last}
}

func fakeNameTagGlyph(advance float32) Glyph {
	return Glyph{U0: 0.1, V0: 0.2, U1: 0.3, V1: 0.4, Advance: advance, BearingY: 10, Width: 8, Height: 12}
}

type fakeNameTagAtlas struct {
	glyphs           map[rune]Glyph
	kerns            map[[2]rune]float32
	tofu             Glyph
	requested        map[rune]struct{}
	strictFlushRunes map[rune]struct{}
	flushGlyphs      map[rune]Glyph
	flushErr         error
	flushes          int
	view             *nameTagTestView
	releases         int
}

func newFakeNameTagAtlas() *fakeNameTagAtlas {
	glyphs := make(map[rune]Glyph)
	for _, char := range []rune{'A', 'V', ' ', '中', '文'} {
		glyphs[char] = fakeNameTagGlyph(10)
	}
	return &fakeNameTagAtlas{
		glyphs: glyphs, kerns: make(map[[2]rune]float32),
		tofu: fakeNameTagGlyph(13), requested: make(map[rune]struct{}), view: &nameTagTestView{},
	}
}

func (atlas *fakeNameTagAtlas) Request(text string) {
	for _, char := range text {
		atlas.requested[char] = struct{}{}
	}
}

func (atlas *fakeNameTagAtlas) FlushUploads(*UploadBudget) error {
	atlas.flushes++
	if atlas.flushErr != nil {
		return atlas.flushErr
	}
	if atlas.flushes != 1 {
		return errors.New("FlushUploads called more than once")
	}
	for char := range atlas.strictFlushRunes {
		if _, ok := atlas.requested[char]; !ok {
			return errors.New("FlushUploads called before all text was requested")
		}
	}
	for char, glyph := range atlas.flushGlyphs {
		atlas.glyphs[char] = glyph
	}
	return nil
}

func (atlas *fakeNameTagAtlas) Glyph(char rune) Glyph {
	if glyph, ok := atlas.glyphs[char]; ok {
		return glyph
	}
	return atlas.tofu
}

func (atlas *fakeNameTagAtlas) Kern(left, right rune) float32 {
	return atlas.kerns[[2]rune{left, right}]
}

func (atlas *fakeNameTagAtlas) TextureView() gfx.TextureView { return atlas.view }

type nameTagTestDevice struct {
	buffers       []*nameTagTestBuffer
	pipelineDescs []gfx.RenderPipelineDesc
	pipelines     []*nameTagTestPipeline
	textures      []*nameTagTestTexture
	bind          *nameTagTestBindGroup
	sampler       *nameTagTestSampler
}

func (d *nameTagTestDevice) CreateBuffer(desc gfx.BufferDesc) gfx.Buffer {
	buffer := &nameTagTestBuffer{desc: desc}
	d.buffers = append(d.buffers, buffer)
	return buffer
}
func (*nameTagTestDevice) CreateShaderModule(string) gfx.ShaderModule { return &nameTagTestShader{} }
func (d *nameTagTestDevice) CreateRenderPipeline(desc gfx.RenderPipelineDesc) gfx.RenderPipeline {
	pipeline := &nameTagTestPipeline{label: desc.Label}
	d.pipelineDescs = append(d.pipelineDescs, desc)
	d.pipelines = append(d.pipelines, pipeline)
	return pipeline
}
func (*nameTagTestDevice) CreateComputePipeline(gfx.ComputePipelineDesc) gfx.ComputePipeline {
	panic("unexpected compute pipeline")
}
func (d *nameTagTestDevice) CreateBindGroup(gfx.BindGroupDesc) gfx.BindGroup {
	d.bind = &nameTagTestBindGroup{}
	return d.bind
}
func (d *nameTagTestDevice) CreateTexture(desc gfx.TextureDesc) gfx.Texture {
	texture := &nameTagTestTexture{desc: desc, view: &nameTagTestView{}}
	d.textures = append(d.textures, texture)
	return texture
}
func (d *nameTagTestDevice) CreateSampler(gfx.SamplerDesc) gfx.Sampler {
	d.sampler = &nameTagTestSampler{}
	return d.sampler
}
func (*nameTagTestDevice) CreateCommandEncoder() gfx.CommandEncoder { panic("unexpected encoder") }
func (*nameTagTestDevice) Submit(...gfx.CommandBuffer)              {}
func (*nameTagTestDevice) Poll(bool)                                {}
func (*nameTagTestDevice) Release()                                 {}

func (d *nameTagTestDevice) bufferByLabel(t *testing.T, label string) *nameTagTestBuffer {
	t.Helper()
	for _, buffer := range d.buffers {
		if buffer.desc.Label == label {
			return buffer
		}
	}
	t.Fatalf("buffer %q was not created", label)
	return nil
}

type nameTagTestBuffer struct {
	desc      gfx.BufferDesc
	lastWrite []byte
	writes    int
	releases  int
}

func (b *nameTagTestBuffer) Size() uint64 { return b.desc.Size }
func (b *nameTagTestBuffer) Write(_ uint64, data []byte) {
	b.lastWrite = append(b.lastWrite[:0], data...)
	b.writes++
}
func (*nameTagTestBuffer) ReadBack() []byte { panic("unexpected readback") }
func (b *nameTagTestBuffer) Release()       { b.releases++ }

type nameTagTestShader struct{}

func (*nameTagTestShader) Release() {}

type nameTagTestPipeline struct {
	label    string
	releases int
}

func (pipeline *nameTagTestPipeline) Release() { pipeline.releases++ }

type nameTagTestBindGroup struct{ releases int }

func (group *nameTagTestBindGroup) Release() { group.releases++ }

type nameTagTestSampler struct{ releases int }

func (sampler *nameTagTestSampler) Release() { sampler.releases++ }

type nameTagTestTexture struct {
	desc     gfx.TextureDesc
	view     *nameTagTestView
	pixels   []byte
	releases int
}

func (texture *nameTagTestTexture) View(gfx.TextureViewDesc) gfx.TextureView { return texture.view }
func (texture *nameTagTestTexture) WriteLayer(_ uint32, _ uint32, pixels []byte) {
	texture.pixels = append(texture.pixels[:0], pixels...)
}
func (*nameTagTestTexture) WriteRegion(uint32, uint32, uint32, uint32, uint32, uint32, []byte) {
	panic("unexpected texture region")
}
func (*nameTagTestTexture) ReadLayer(uint32, uint32) []byte { panic("unexpected texture read") }
func (texture *nameTagTestTexture) Release()                { texture.releases++ }

type nameTagTestEncoder struct{ passes []*nameTagTestPass }

func (encoder *nameTagTestEncoder) BeginRenderPass(desc gfx.RenderPassDesc) gfx.RenderPass {
	pass := &nameTagTestPass{desc: desc}
	encoder.passes = append(encoder.passes, pass)
	return pass
}
func (*nameTagTestEncoder) BeginComputePass(string) gfx.ComputePass { panic("unexpected compute pass") }
func (*nameTagTestEncoder) CopyBufferToBuffer(gfx.Buffer, uint64, gfx.Buffer, uint64, uint64) {
	panic("unexpected buffer copy")
}
func (*nameTagTestEncoder) Finish() gfx.CommandBuffer { panic("unexpected finish") }

type nameTagTestPass struct {
	desc           gfx.RenderPassDesc
	pipelineLabels []string
	drawInstances  []uint32
	ended          bool
}

func (pass *nameTagTestPass) SetPipeline(pipeline gfx.RenderPipeline) {
	pass.pipelineLabels = append(pass.pipelineLabels, pipeline.(*nameTagTestPipeline).label)
}
func (*nameTagTestPass) SetBindGroup(uint32, gfx.BindGroup)         {}
func (*nameTagTestPass) SetVertexBuffer(uint32, gfx.Buffer, uint64) {}
func (*nameTagTestPass) SetIndexBuffer(gfx.Buffer, uint64)          { panic("unexpected index buffer") }
func (*nameTagTestPass) DrawIndexedIndirect(gfx.Buffer, uint64)     { panic("unexpected indirect draw") }
func (pass *nameTagTestPass) Draw(vertices, instances uint32) {
	if vertices != 6 {
		panic("name-tag quad did not use six vertices")
	}
	pass.drawInstances = append(pass.drawInstances, instances)
}
func (pass *nameTagTestPass) End() { pass.ended = true }

type nameTagTestView struct{ releases int }

func (view *nameTagTestView) Release() { view.releases++ }

func float32At(data []byte, offset int) float32 {
	return math.Float32frombits(binary.LittleEndian.Uint32(data[offset : offset+4]))
}
