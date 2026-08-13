//go:build darwin

package render

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/gfx"
)

var embeddedGlyphFontDataSink []byte

// 删除 Renderer 的固定 uniform 编码数组会让稳定帧重新产生堆分配。
func TestRendererRenderDoesNotAllocate(t *testing.T) {
	buffer := &allocationRenderBuffer{}
	renderer := &Renderer{
		camera: buffer, skyCamera: buffer,
		zeroArgs: buffer, indirect: buffer, index: buffer,
		cull: &culler{uniforms: buffer, sections: buffer},
		sections: map[core.SectionPos]sectionSlot{
			{Y: 4}: {packed: []uint64{1}},
		},
	}
	encoder := &allocationCommandEncoder{}
	camera := Camera{ViewProj: mgl32.Ident4(), ViewProjInv: mgl32.Ident4()}

	renderer.Render(encoder, nil, nil, camera)
	if got := renderer.LastFrameStats().CandidateFaces; got != 1 {
		t.Fatalf("warm candidate faces=%d want=1", got)
	}
	allocations := testing.AllocsPerRun(1000, func() {
		renderer.Render(encoder, nil, nil, camera)
	})
	if allocations != 0 {
		t.Fatalf("warmed terrain Render allocations=%v want=0", allocations)
	}
}

// Mutation killed: copying the avatar slice for sorting or rebuilding part
// storage makes the warmed CPU path allocate on every rendered frame.
func TestAvatarRenderCPUPathReusesSortingAndPartStorage(t *testing.T) {
	avatars := makeTestAvatars(maxRemoteAvatars)
	dynamic := &allocationRenderBuffer{}
	renderer := &AvatarRenderer{
		dynamic: dynamic,
		parts:   make([]avatarPart, 0, maxAvatarParts),
		upload:  make([]byte, avatarUploadBytes),
	}
	encoder := &allocationCommandEncoder{}

	renderer.Render(encoder, nil, nil, Camera{}, avatars)
	allocations := testing.AllocsPerRun(1000, func() {
		renderer.Render(encoder, nil, nil, Camera{}, avatars)
	})
	if allocations != 0 {
		t.Fatalf("warmed avatar Render allocations=%v want=0", allocations)
	}
	if got, want := len(renderer.parts), maxAvatarParts; got != want {
		t.Fatalf("parts=%d want=%d", got, want)
	}
	firstBounds := avatarPartsBounds(renderer.parts[:avatarPartsPerBody])
	if firstBounds.min[0] < 0.69 || firstBounds.max[0] > 1.31 {
		t.Fatalf("first sorted avatar bounds=%+v want PlayerID 1 near x=1", firstBounds)
	}
}

// Mutation killed: sort.Slice/copy or []rune truncation/layout makes each
// warmed Prepare allocate even though all renderer and fake storage is fixed.
func TestNameTagPrepareCPUPathReusesSortingAndUnicodeLayoutStorage(t *testing.T) {
	source := &allocationGlyphSource{}
	renderer := &NameTagRenderer{
		atlas: source,
		layout: nameTagLayout{
			glyphs:      make([]nameTagGlyph, 0, maxNameTagGlyphs),
			backgrounds: make([]nameTagBackground, 0, maxNameTags),
		},
		upload: make([]byte, nameTagUploadBytes),
	}
	tags := make([]NameTag, maxNameTags)
	for index := range tags {
		id := byte(maxNameTags - index)
		tags[index] = NameTag{PlayerID: testNameTagID(id), Text: "A"}
		tags[index].Anchor[0] = float32(id)
	}
	tags[len(tags)-1].Text = strings.Repeat("界", maxNameTagRunes+8)
	budget := NewUploadBudget(1024)

	if err := renderer.Prepare(tags, budget); err != nil {
		t.Fatalf("warm Prepare: %v", err)
	}
	allocations := testing.AllocsPerRun(1000, func() {
		source.requestCount = 0
		if err := renderer.Prepare(tags, budget); err != nil {
			panic(err)
		}
	})
	if allocations != 0 {
		t.Fatalf("warmed name-tag Prepare allocations=%v want=0", allocations)
	}
	if got, want := source.requestCount, maxNameTags; got != want {
		t.Fatalf("requests=%d want=%d", got, want)
	}
	if got := utf8.RuneCountInString(source.requests[0]); got != maxNameTagRunes {
		t.Fatalf("first sorted request runes=%d want=%d", got, maxNameTagRunes)
	}
	if source.requests[0] != strings.Repeat("界", maxNameTagRunes) {
		t.Fatalf("first sorted request=%q want 32 Unicode runes", source.requests[0])
	}
	if got, want := len(renderer.layout.backgrounds), maxNameTags; got != want {
		t.Fatalf("backgrounds=%d want=%d", got, want)
	}
	for index, background := range renderer.layout.backgrounds {
		if got, want := background.Anchor[0], float32(index+1); got != want {
			t.Fatalf("background %d anchor x=%v want=%v sorted by PlayerID", index, got, want)
		}
	}

	flushErr := errors.New("allocation test flush failure")
	source.requestCount = 0
	source.flushErr = flushErr
	if got := renderer.Prepare(tags, budget); got != flushErr {
		t.Fatalf("Prepare error=%v want exact %v", got, flushErr)
	}
}

// Mutation killed: switching the font back to embed.FS.ReadFile returns a new
// allocation/backing array for every call instead of the embedded bytes.
func TestEmbeddedGlyphFontDataIsZeroCopy(t *testing.T) {
	first := embeddedGlyphFontData()
	second := embeddedGlyphFontData()
	if len(first) == 0 || len(second) == 0 {
		t.Fatal("embedded font data is empty")
	}
	if &first[0] != &second[0] {
		t.Fatal("consecutive embedded font data calls use different backing arrays")
	}
	if got := string(first[:4]); got != "OTTO" {
		t.Fatalf("font magic=%q want OTTO", got)
	}
	allocations := testing.AllocsPerRun(1000, func() {
		embeddedGlyphFontDataSink = embeddedGlyphFontData()
	})
	if allocations != 0 {
		t.Fatalf("embedded font data allocations=%v want=0", allocations)
	}
}

// Mutation killed: removing metadata validation from the production face
// factory lets an empty embedded license/provenance reach font parsing.
func TestEmbeddedGlyphFaceFactoryRequiresProductionMetadata(t *testing.T) {
	const (
		licenseMarker    = "SIL OPEN FONT LICENSE Version 1.1 - 26 February 2007"
		provenanceMarker = "\"repository\": \"notofonts/noto-cjk\""
	)
	if !strings.Contains(embeddedGlyphLicense, licenseMarker) {
		t.Fatalf("embedded license missing marker %q", licenseMarker)
	}
	if !strings.Contains(embeddedGlyphProvenance, provenanceMarker) {
		t.Fatalf("embedded provenance missing marker %q", provenanceMarker)
	}

	savedLicense, savedProvenance := embeddedGlyphLicense, embeddedGlyphProvenance
	defer func() {
		embeddedGlyphLicense = savedLicense
		embeddedGlyphProvenance = savedProvenance
	}()
	tests := []struct {
		name  string
		clear func()
		want  string
	}{
		{
			name:  "license",
			clear: func() { embeddedGlyphLicense = "" },
			want:  "render: embedded glyph license metadata is missing or invalid",
		},
		{
			name:  "provenance",
			clear: func() { embeddedGlyphProvenance = "" },
			want:  "render: embedded glyph provenance metadata is missing or invalid",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			embeddedGlyphLicense = savedLicense
			embeddedGlyphProvenance = savedProvenance
			test.clear()
			if err := validateEmbeddedGlyphMetadata(); err == nil || err.Error() != test.want {
				t.Fatalf("validation error=%v want=%q", err, test.want)
			}
			_, _, err := embeddedGlyphFaceFactory()
			if err == nil || err.Error() != test.want {
				t.Fatalf("factory error=%v want=%q", err, test.want)
			}
		})
	}
}

type allocationRenderBuffer struct{}

func (*allocationRenderBuffer) Size() uint64         { return avatarUploadBytes }
func (*allocationRenderBuffer) Write(uint64, []byte) {}
func (*allocationRenderBuffer) ReadBack() []byte     { return nil }
func (*allocationRenderBuffer) Release()             {}

type allocationCommandEncoder struct {
	pass    allocationRenderPass
	compute allocationComputePass
}

func (encoder *allocationCommandEncoder) BeginRenderPass(gfx.RenderPassDesc) gfx.RenderPass {
	return &encoder.pass
}
func (encoder *allocationCommandEncoder) BeginComputePass(string) gfx.ComputePass {
	return &encoder.compute
}
func (*allocationCommandEncoder) CopyBufferToBuffer(gfx.Buffer, uint64, gfx.Buffer, uint64, uint64) {
}
func (*allocationCommandEncoder) Finish() gfx.CommandBuffer { return nil }

type allocationRenderPass struct{}

func (*allocationRenderPass) SetPipeline(gfx.RenderPipeline)             {}
func (*allocationRenderPass) SetBindGroup(uint32, gfx.BindGroup)         {}
func (*allocationRenderPass) SetVertexBuffer(uint32, gfx.Buffer, uint64) {}
func (*allocationRenderPass) SetIndexBuffer(gfx.Buffer, uint64)          {}
func (*allocationRenderPass) DrawIndexedIndirect(gfx.Buffer, uint64)     {}
func (*allocationRenderPass) Draw(uint32, uint32)                        {}
func (*allocationRenderPass) End()                                       {}

type allocationComputePass struct{}

func (*allocationComputePass) SetPipeline(gfx.ComputePipeline)    {}
func (*allocationComputePass) SetBindGroup(uint32, gfx.BindGroup) {}
func (*allocationComputePass) Dispatch(uint32, uint32, uint32)    {}
func (*allocationComputePass) End()                               {}

type allocationGlyphSource struct {
	requests     [maxNameTags]string
	requestCount int
	flushErr     error
}

func (source *allocationGlyphSource) Request(text string) {
	source.requests[source.requestCount] = text
	source.requestCount++
}

func (source *allocationGlyphSource) FlushUploads(*UploadBudget) error { return source.flushErr }
func (*allocationGlyphSource) Glyph(rune) Glyph {
	return Glyph{Advance: 8, BearingX: 1, BearingY: 10, Width: 7, Height: 12}
}
func (*allocationGlyphSource) Kern(rune, rune) float32      { return 0.25 }
func (*allocationGlyphSource) TextureView() gfx.TextureView { return nil }
