//go:build darwin

package render

import (
	"embed"
	"fmt"
	"image"
	"image/draw"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"minecraft-go/internal/gfx"
)

const (
	glyphAtlasSize       = 1024
	glyphCellSize        = 32
	glyphSlotCount       = 1024
	glyphRequestCapacity = 1024
	glyphResultCapacity  = 32
	glyphUploadBytes     = glyphCellSize * glyphCellSize
)

//go:embed assets/NotoSansCJKsc-Regular.otf assets/OFL.txt assets/NotoSansCJKsc-Regular.provenance.json
var embeddedGlyphAssets embed.FS

// Glyph describes one cell in the fixed-size glyph atlas.
type Glyph struct {
	Slot                        uint16
	U0, V0, U1, V1              float32
	Advance, BearingX, BearingY float32
	Width, Height               float32
}

type glyphResult struct {
	char    rune
	glyph   Glyph
	pixels  []byte
	missing bool
	err     error
}

type glyphFaceFactory func() (renderFace, workerFace font.Face, err error)

type glyphRasterizer interface {
	Rasterize(font.Face, rune) (Glyph, []byte, bool, error)
}

// GlyphAtlas owns a bounded 1024x1024 R8 texture split into 32x32 cells.
type GlyphAtlas struct {
	texture       gfx.Texture
	view          gfx.TextureView
	pendingUpload *glyphResult
	renderFace    font.Face

	mu          sync.Mutex
	requests    chan rune
	results     chan glyphResult
	stop        chan struct{}
	releaseDone chan struct{}
	requested   map[rune]struct{}
	glyphs      map[rune]Glyph
	tofu        Glyph
	nextSlot    uint16
	exhausted   bool
	released    bool
	worker      sync.WaitGroup
}

// NewGlyphAtlas creates the atlas from the embedded, pinned Noto font.
func NewGlyphAtlas(dev gfx.Device) (*GlyphAtlas, error) {
	return newGlyphAtlasWith(dev, embeddedGlyphFaceFactory, opentypeGlyphRasterizer{})
}

func newGlyphAtlasWith(dev gfx.Device, factory glyphFaceFactory, rasterizer glyphRasterizer) (*GlyphAtlas, error) {
	renderFace, workerFace, err := factory()
	if err != nil {
		return nil, fmt.Errorf("render: create glyph faces: %w", err)
	}
	if renderFace == nil || workerFace == nil {
		return nil, fmt.Errorf("render: glyph face factory returned nil face")
	}

	texture := dev.CreateTexture(gfx.TextureDesc{
		Label:     "glyph-atlas",
		Width:     glyphAtlasSize,
		Height:    glyphAtlasSize,
		Layers:    1,
		MipLevels: 1,
		Format:    gfx.FormatR8Unorm,
		Dimension: gfx.TextureDimension2D,
		Usage:     gfx.TextureUsageBinding | gfx.TextureUsageCopyDst,
	})
	view := texture.View(gfx.TextureViewDesc{Dimension: gfx.TextureViewDimension2D})
	tofu, pixels := tofuGlyph()
	texture.WriteRegion(0, 0, 0, 0, glyphCellSize, glyphCellSize, pixels)

	atlas := &GlyphAtlas{
		texture:     texture,
		view:        view,
		renderFace:  renderFace,
		requests:    make(chan rune, glyphRequestCapacity),
		results:     make(chan glyphResult, glyphResultCapacity),
		stop:        make(chan struct{}),
		releaseDone: make(chan struct{}),
		requested:   make(map[rune]struct{}, glyphSlotCount),
		glyphs:      make(map[rune]Glyph, glyphSlotCount),
		tofu:        tofu,
		nextSlot:    1,
	}
	atlas.worker.Add(1)
	go atlas.runWorker(workerFace, rasterizer)
	return atlas, nil
}

func embeddedGlyphFaceFactory() (font.Face, font.Face, error) {
	data, err := embeddedGlyphAssets.ReadFile("assets/NotoSansCJKsc-Regular.otf")
	if err != nil {
		return nil, nil, err
	}
	parsed, err := opentype.Parse(data)
	if err != nil {
		return nil, nil, err
	}
	options := &opentype.FaceOptions{Size: 24, DPI: 72, Hinting: font.HintingFull}
	renderFace, err := opentype.NewFace(parsed, options)
	if err != nil {
		return nil, nil, err
	}
	workerFace, err := opentype.NewFace(parsed, options)
	if err != nil {
		return nil, nil, err
	}
	return renderFace, workerFace, nil
}

func tofuGlyph() (Glyph, []byte) {
	pixels := make([]byte, glyphUploadBytes)
	for y := 4; y < glyphCellSize-4; y++ {
		for x := 5; x < glyphCellSize-5; x++ {
			if x < 8 || x >= glyphCellSize-8 || y < 7 || y >= glyphCellSize-7 {
				pixels[y*glyphCellSize+x] = 0xff
			}
		}
	}
	return Glyph{
		Slot:     0,
		U0:       0,
		V0:       0,
		U1:       float32(glyphCellSize) / float32(glyphAtlasSize),
		V1:       float32(glyphCellSize) / float32(glyphAtlasSize),
		Advance:  22,
		BearingX: 5,
		BearingY: 25,
		Width:    22,
		Height:   24,
	}, pixels
}

// Request schedules unseen runes without ever blocking the caller.
func (atlas *GlyphAtlas) Request(text string) {
	atlas.mu.Lock()
	defer atlas.mu.Unlock()
	if atlas.released || atlas.exhausted {
		return
	}
	for _, char := range text {
		if _, ok := atlas.glyphs[char]; ok {
			continue
		}
		if _, ok := atlas.requested[char]; ok {
			continue
		}
		select {
		case atlas.requests <- char:
			atlas.requested[char] = struct{}{}
		default:
			// A rejected rune remains unregistered so a later frame can retry it.
		}
	}
}

func (atlas *GlyphAtlas) runWorker(face font.Face, rasterizer glyphRasterizer) {
	defer atlas.worker.Done()
	for {
		select {
		case <-atlas.stop:
			return
		default:
		}

		select {
		case <-atlas.stop:
			return
		case char, ok := <-atlas.requests:
			if !ok {
				return
			}
			glyph, pixels, missing, err := rasterizer.Rasterize(face, char)
			result := glyphResult{char: char, glyph: glyph, pixels: pixels, missing: missing, err: err}
			select {
			case atlas.results <- result:
			case <-atlas.stop:
				return
			}
		}
	}
}

// FlushUploads moves at most one worker result to render-thread state.
func (atlas *GlyphAtlas) FlushUploads(budget *UploadBudget) error {
	atlas.mu.Lock()
	defer atlas.mu.Unlock()
	if atlas.released {
		return nil
	}
	if atlas.pendingUpload == nil {
		select {
		case result := <-atlas.results:
			atlas.pendingUpload = &result
		default:
			return nil
		}
	}

	pending := atlas.pendingUpload
	if pending.err != nil {
		atlas.pendingUpload = nil
		return fmt.Errorf("render: rasterize glyph %q: %w", pending.char, pending.err)
	}
	if pending.missing || atlas.exhausted {
		atlas.glyphs[pending.char] = atlas.tofu
		atlas.pendingUpload = nil
		return nil
	}
	if budget == nil {
		return fmt.Errorf("render: glyph upload budget is nil")
	}
	if !budget.TryConsume(glyphUploadBytes) {
		return nil
	}

	slot := atlas.nextSlot
	x := uint32(slot%32) * glyphCellSize
	y := uint32(slot/32) * glyphCellSize
	atlas.texture.WriteRegion(0, 0, x, y, glyphCellSize, glyphCellSize, pending.pixels)
	pending.glyph.Slot = slot
	pending.glyph.U0 = float32(x) / float32(glyphAtlasSize)
	pending.glyph.V0 = float32(y) / float32(glyphAtlasSize)
	pending.glyph.U1 = float32(x+glyphCellSize) / float32(glyphAtlasSize)
	pending.glyph.V1 = float32(y+glyphCellSize) / float32(glyphAtlasSize)
	atlas.glyphs[pending.char] = pending.glyph
	atlas.pendingUpload = nil
	atlas.nextSlot++
	if atlas.nextSlot >= glyphSlotCount {
		atlas.exhausted = true
	}
	return nil
}

// Glyph returns tofu for runes that are missing, pending, or beyond capacity.
func (atlas *GlyphAtlas) Glyph(char rune) Glyph {
	atlas.mu.Lock()
	defer atlas.mu.Unlock()
	if glyph, ok := atlas.glyphs[char]; ok {
		return glyph
	}
	return atlas.tofu
}

// Kern returns the render-thread face's kerning adjustment in pixels.
func (atlas *GlyphAtlas) Kern(left, right rune) float32 {
	atlas.mu.Lock()
	defer atlas.mu.Unlock()
	if atlas.released || atlas.renderFace == nil {
		return 0
	}
	return float32(atlas.renderFace.Kern(left, right)) / 64
}

// TextureView returns the atlas-owned borrowed view. Callers must not release it.
func (atlas *GlyphAtlas) TextureView() gfx.TextureView {
	atlas.mu.Lock()
	defer atlas.mu.Unlock()
	if atlas.released {
		return nil
	}
	return atlas.view
}

// Release stops the worker, then releases the view and texture exactly once.
func (atlas *GlyphAtlas) Release() {
	atlas.mu.Lock()
	if atlas.released {
		done := atlas.releaseDone
		atlas.mu.Unlock()
		<-done
		return
	}
	atlas.released = true
	close(atlas.requests)
	close(atlas.stop)
	atlas.mu.Unlock()

	atlas.worker.Wait()

	atlas.mu.Lock()
	atlas.renderFace = nil
	atlas.pendingUpload = nil
	view, texture := atlas.view, atlas.texture
	atlas.view, atlas.texture = nil, nil
	atlas.mu.Unlock()
	if view != nil {
		view.Release()
	}
	if texture != nil {
		texture.Release()
	}
	close(atlas.releaseDone)
}

type opentypeGlyphRasterizer struct{}

func (opentypeGlyphRasterizer) Rasterize(face font.Face, char rune) (Glyph, []byte, bool, error) {
	bounds, advance, ok := face.GlyphBounds(char)
	if !ok {
		return Glyph{}, nil, true, nil
	}
	const padding = 2
	dot := fixed.Point26_6{
		X: fixed.I(padding) - bounds.Min.X,
		Y: fixed.I(padding) - bounds.Min.Y,
	}
	drawRect, mask, maskPoint, _, ok := face.Glyph(dot, char)
	if !ok {
		return Glyph{}, nil, true, nil
	}
	cell := image.NewAlpha(image.Rect(0, 0, glyphCellSize, glyphCellSize))
	clipped := drawRect.Intersect(cell.Bounds())
	if !clipped.Empty() {
		maskPoint = maskPoint.Add(clipped.Min.Sub(drawRect.Min))
		draw.DrawMask(cell, clipped, image.White, image.Point{}, mask, maskPoint, draw.Src)
	}
	return Glyph{
		Advance:  float32(advance) / 64,
		BearingX: float32(bounds.Min.X) / 64,
		BearingY: -float32(bounds.Min.Y) / 64,
		Width:    float32(bounds.Max.X-bounds.Min.X) / 64,
		Height:   float32(bounds.Max.Y-bounds.Min.Y) / 64,
	}, cell.Pix, false, nil
}
