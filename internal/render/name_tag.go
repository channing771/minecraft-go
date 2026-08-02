package render

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"math"
	"sort"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/gfx"
)

const (
	maxNameTags      = 7
	maxNameTagRunes  = 32
	maxNameTagGlyphs = maxNameTags * maxNameTagRunes

	nameTagInstanceBytes = 64
	nameTagCameraBytes   = 96
	nameTagPaddingX      = float32(4)
	nameTagPaddingY      = float32(2)
)

//go:embed shader/name_tag.wgsl
var nameTagShader string

type NameTag struct {
	PlayerID core.PlayerID
	Text     string
	Anchor   mgl32.Vec3
}

type BillboardCamera struct {
	ViewProj mgl32.Mat4
	Right    mgl32.Vec3
	Up       mgl32.Vec3
}

type GlyphSource interface {
	Request(string)
	FlushUploads(*UploadBudget) error
	Glyph(rune) Glyph
	Kern(rune, rune) float32
	TextureView() gfx.TextureView
}

type nameTagGlyph struct {
	Anchor              mgl32.Vec3
	CenterX             float32
	X, Y, Width, Height float32
	U0, V0, U1, V1      float32
	Color               [4]float32
}

type nameTagBackground struct {
	Anchor              mgl32.Vec3
	CenterX             float32
	X, Y, Width, Height float32
	Color               [4]float32
}

type nameTagLayout struct {
	glyphs      []nameTagGlyph
	backgrounds []nameTagBackground
}

type NameTagRenderer struct {
	atlas GlyphSource

	glyphInstances      gfx.Buffer
	backgroundInstances gfx.Buffer
	camera              gfx.Buffer
	backgroundPipeline  gfx.RenderPipeline
	glyphPipeline       gfx.RenderPipeline
	bind                gfx.BindGroup
	sampler             gfx.Sampler

	layout          nameTagLayout
	glyphBytes      []byte
	backgroundBytes []byte
	cameraBytes     []byte
}

func NewNameTagRenderer(
	dev gfx.Device,
	colorFormat, depthFormat gfx.TextureFormat,
	atlas GlyphSource,
) *NameTagRenderer {
	renderer := &NameTagRenderer{atlas: atlas}
	renderer.glyphInstances = dev.CreateBuffer(gfx.BufferDesc{
		Label: "name-tag glyph instances",
		Size:  uint64(maxNameTagGlyphs * nameTagInstanceBytes),
		Usage: gfx.BufferUsageStorage | gfx.BufferUsageCopyDst,
	})
	renderer.backgroundInstances = dev.CreateBuffer(gfx.BufferDesc{
		Label: "name-tag background instances",
		Size:  uint64(maxNameTags * nameTagInstanceBytes),
		Usage: gfx.BufferUsageStorage | gfx.BufferUsageCopyDst,
	})
	renderer.camera = dev.CreateBuffer(gfx.BufferDesc{
		Label: "name-tag camera",
		Size:  nameTagCameraBytes,
		Usage: gfx.BufferUsageUniform | gfx.BufferUsageCopyDst,
	})
	renderer.layout = nameTagLayout{
		glyphs:      make([]nameTagGlyph, 0, maxNameTagGlyphs),
		backgrounds: make([]nameTagBackground, 0, maxNameTags),
	}
	renderer.glyphBytes = make([]byte, maxNameTagGlyphs*nameTagInstanceBytes)
	renderer.backgroundBytes = make([]byte, maxNameTags*nameTagInstanceBytes)
	renderer.cameraBytes = make([]byte, nameTagCameraBytes)

	layout := gfx.BindGroupLayout{
		Label: "name-tag layout",
		Entries: []gfx.BindGroupLayoutEntry{
			{Binding: 0, Type: gfx.BindingUniformBuffer, VisibleIn: gfx.StageVertex},
			{Binding: 1, Type: gfx.BindingStorageBufferRO, VisibleIn: gfx.StageVertex},
			{Binding: 2, Type: gfx.BindingStorageBufferRO, VisibleIn: gfx.StageVertex},
			{Binding: 3, Type: gfx.BindingSampledTextureFloat, VisibleIn: gfx.StageFragment, ViewDimension: gfx.TextureViewDimension2D},
			{Binding: 4, Type: gfx.BindingSampler, VisibleIn: gfx.StageFragment},
		},
	}
	module := dev.CreateShaderModule(nameTagShader)
	renderer.backgroundPipeline = dev.CreateRenderPipeline(nameTagPipelineDesc(
		"name-tag background", module, colorFormat, depthFormat, layout, "background_vs", "background_fs",
	))
	renderer.glyphPipeline = dev.CreateRenderPipeline(nameTagPipelineDesc(
		"name-tag glyph", module, colorFormat, depthFormat, layout, "glyph_vs", "glyph_fs",
	))
	module.Release()
	renderer.sampler = dev.CreateSampler(gfx.SamplerDesc{
		Label: "name-tag glyph sampler", MagFilter: gfx.FilterLinear, MinFilter: gfx.FilterLinear,
		MipFilter: gfx.FilterNearest, Address: gfx.AddressClampToEdge,
	})
	renderer.bind = dev.CreateBindGroup(gfx.BindGroupDesc{
		Label:  "name-tag resources",
		Layout: layout,
		Entries: []gfx.BindGroupEntry{
			{Binding: 0, Buffer: renderer.camera},
			{Binding: 1, Buffer: renderer.glyphInstances},
			{Binding: 2, Buffer: renderer.backgroundInstances},
			{Binding: 3, Texture: atlas.TextureView()},
			{Binding: 4, Sampler: renderer.sampler},
		},
	})
	return renderer
}

func nameTagPipelineDesc(
	label string,
	module gfx.ShaderModule,
	colorFormat, depthFormat gfx.TextureFormat,
	layout gfx.BindGroupLayout,
	vertexEntry, fragmentEntry string,
) gfx.RenderPipelineDesc {
	return gfx.RenderPipelineDesc{
		Label: label, Shader: module,
		VertexEntry: vertexEntry, FragmentEntry: fragmentEntry,
		BindGroups:  []gfx.BindGroupLayout{layout},
		ColorFormat: colorFormat, DepthFormat: depthFormat,
		DepthWrite: false, Blend: gfx.BlendAlpha,
	}
}

func (renderer *NameTagRenderer) Prepare(tags []NameTag, budget *UploadBudget) error {
	ordered := orderedNameTags(tags)
	for index := range ordered {
		ordered[index].Text = truncateNameTagText(ordered[index].Text)
		renderer.atlas.Request(ordered[index].Text)
	}
	if err := renderer.atlas.FlushUploads(budget); err != nil {
		return err
	}

	renderer.layout = layoutNameTags(&renderer.layout, renderer.atlas, ordered)
	glyphBytes := encodeNameTagGlyphs(renderer.glyphBytes, renderer.layout.glyphs)
	backgroundBytes := encodeNameTagBackgrounds(renderer.backgroundBytes, renderer.layout.backgrounds)
	if len(glyphBytes) != 0 {
		renderer.glyphInstances.Write(0, glyphBytes)
	}
	if len(backgroundBytes) != 0 {
		renderer.backgroundInstances.Write(0, backgroundBytes)
	}
	return nil
}

func (renderer *NameTagRenderer) Render(
	encoder gfx.CommandEncoder,
	target, depth gfx.TextureView,
	camera BillboardCamera,
) {
	if len(renderer.layout.backgrounds) == 0 && len(renderer.layout.glyphs) == 0 {
		return
	}
	renderer.camera.Write(0, encodeBillboardCamera(renderer.cameraBytes, camera))
	pass := encoder.BeginRenderPass(nameTagPassDesc(target, depth))
	pass.SetBindGroup(0, renderer.bind)
	if len(renderer.layout.backgrounds) != 0 {
		pass.SetPipeline(renderer.backgroundPipeline)
		pass.Draw(6, uint32(len(renderer.layout.backgrounds)))
	}
	if len(renderer.layout.glyphs) != 0 {
		pass.SetPipeline(renderer.glyphPipeline)
		pass.Draw(6, uint32(len(renderer.layout.glyphs)))
	}
	pass.End()
}

func nameTagPassDesc(target, depth gfx.TextureView) gfx.RenderPassDesc {
	return gfx.RenderPassDesc{
		Label: "name-tag pass", ColorView: target, DepthView: depth, LoadClear: false,
	}
}

func layoutNameTags(dst *nameTagLayout, atlas GlyphSource, tags []NameTag) nameTagLayout {
	if dst == nil {
		dst = &nameTagLayout{
			glyphs:      make([]nameTagGlyph, 0, maxNameTagGlyphs),
			backgrounds: make([]nameTagBackground, 0, maxNameTags),
		}
	}
	dst.glyphs = dst.glyphs[:0]
	dst.backgrounds = dst.backgrounds[:0]

	for _, tag := range orderedNameTags(tags) {
		runes := []rune(truncateNameTagText(tag.Text))
		if len(runes) == 0 {
			continue
		}

		start := len(dst.glyphs)
		penX := float32(0)
		minX, minY := float32(math.Inf(1)), float32(math.Inf(1))
		maxX, maxY := float32(math.Inf(-1)), float32(math.Inf(-1))
		for index, char := range runes {
			if index != 0 {
				penX += atlas.Kern(runes[index-1], char)
			}
			glyph := atlas.Glyph(char)
			x := penX + glyph.BearingX
			y := -glyph.BearingY
			dst.glyphs = append(dst.glyphs, nameTagGlyph{
				Anchor: tag.Anchor,
				X:      x, Y: y, Width: glyph.Width, Height: glyph.Height,
				U0: glyph.U0, V0: glyph.V0, U1: glyph.U1, V1: glyph.V1,
				Color: [4]float32{1, 1, 1, 1},
			})
			minX, minY = min(minX, x), min(minY, y)
			maxX, maxY = max(maxX, x+glyph.Width), max(maxY, y+glyph.Height)
			penX += glyph.Advance
		}
		minX = min(minX, 0)
		maxX = max(maxX, penX)
		centerX := (minX + maxX) * 0.5
		for index := start; index < len(dst.glyphs); index++ {
			dst.glyphs[index].CenterX = centerX
		}
		dst.backgrounds = append(dst.backgrounds, nameTagBackground{
			Anchor: tag.Anchor, CenterX: centerX,
			X: minX - nameTagPaddingX, Y: minY - nameTagPaddingY,
			Width: maxX - minX + 2*nameTagPaddingX, Height: maxY - minY + 2*nameTagPaddingY,
			Color: [4]float32{0.02, 0.02, 0.02, 0.58},
		})
	}
	return *dst
}

func orderedNameTags(tags []NameTag) []NameTag {
	ordered := append([]NameTag(nil), tags...)
	sort.Slice(ordered, func(left, right int) bool {
		return bytes.Compare(ordered[left].PlayerID[:], ordered[right].PlayerID[:]) < 0
	})
	if len(ordered) > maxNameTags {
		ordered = ordered[:maxNameTags]
	}
	return ordered
}

func truncateNameTagText(text string) string {
	runes := []rune(text)
	if len(runes) > maxNameTagRunes {
		runes = runes[:maxNameTagRunes]
	}
	return string(runes)
}

func encodeNameTagGlyphs(dst []byte, glyphs []nameTagGlyph) []byte {
	out := dst[:len(glyphs)*nameTagInstanceBytes]
	for index, glyph := range glyphs {
		writeNameTagInstance(out[index*nameTagInstanceBytes:], glyph.Anchor, glyph.CenterX,
			glyph.X, glyph.Y, glyph.Width, glyph.Height,
			[4]float32{glyph.U0, glyph.V0, glyph.U1, glyph.V1}, glyph.Color)
	}
	return out
}

func encodeNameTagBackgrounds(dst []byte, backgrounds []nameTagBackground) []byte {
	out := dst[:len(backgrounds)*nameTagInstanceBytes]
	for index, background := range backgrounds {
		writeNameTagInstance(out[index*nameTagInstanceBytes:], background.Anchor, background.CenterX,
			background.X, background.Y, background.Width, background.Height,
			[4]float32{}, background.Color)
	}
	return out
}

func writeNameTagInstance(
	dst []byte,
	anchor mgl32.Vec3,
	centerX float32,
	x, y, width, height float32,
	uv, color [4]float32,
) {
	values := [16]float32{
		anchor[0], anchor[1], anchor[2], centerX,
		x, y, width, height,
		uv[0], uv[1], uv[2], uv[3],
		color[0], color[1], color[2], color[3],
	}
	for index, value := range values {
		binary.LittleEndian.PutUint32(dst[index*4:], math.Float32bits(value))
	}
}

func encodeBillboardCamera(out []byte, camera BillboardCamera) []byte {
	out = out[:nameTagCameraBytes]
	for index, value := range camera.ViewProj {
		binary.LittleEndian.PutUint32(out[index*4:], math.Float32bits(value))
	}
	for index, value := range camera.Right {
		binary.LittleEndian.PutUint32(out[64+index*4:], math.Float32bits(value))
	}
	for index, value := range camera.Up {
		binary.LittleEndian.PutUint32(out[80+index*4:], math.Float32bits(value))
	}
	return out
}

// Release releases only handles owned by the renderer. The atlas and its view
// are borrowed from the application and deliberately remain untouched.
func (renderer *NameTagRenderer) Release() {
	if renderer.bind != nil {
		renderer.bind.Release()
		renderer.bind = nil
	}
	if renderer.glyphPipeline != nil {
		renderer.glyphPipeline.Release()
		renderer.glyphPipeline = nil
	}
	if renderer.backgroundPipeline != nil {
		renderer.backgroundPipeline.Release()
		renderer.backgroundPipeline = nil
	}
	if renderer.sampler != nil {
		renderer.sampler.Release()
		renderer.sampler = nil
	}
	for _, buffer := range []*gfx.Buffer{
		&renderer.camera, &renderer.backgroundInstances, &renderer.glyphInstances,
	} {
		if *buffer != nil {
			(*buffer).Release()
			*buffer = nil
		}
	}
}
