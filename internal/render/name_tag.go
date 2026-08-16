package render

import (
	"encoding/binary"
	"fmt"
	"math"
	"slices"

	"github.com/go-gl/mathgl/mgl32"
)

const (
	maxNameTags      = 12
	maxNameTagRunes  = 32
	maxNameTagGlyphs = maxNameTags * maxNameTagRunes

	nameTagInstanceBytes = 64
	nameTagCameraBytes   = 96
	nameTagPaddingX      = float32(4)
	nameTagPaddingY      = float32(2)

	nameTagCameraOffset     = 0
	nameTagBackgroundOffset = 256
	nameTagBackgroundSize   = maxNameTags * nameTagInstanceBytes
	nameTagGlyphOffset      = (nameTagBackgroundOffset + nameTagBackgroundSize + 255) / 256 * 256
	nameTagGlyphSize        = maxNameTagGlyphs * nameTagInstanceBytes
	nameTagUploadBytes      = nameTagGlyphOffset + nameTagGlyphSize
)

type NameTag struct {
	Key    EntityKey
	Text   string
	Anchor mgl32.Vec3
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

	layout  nameTagLayout
	ordered []NameTag
	upload  []byte
}

func (renderer *NameTagRenderer) Prepare(tags []NameTag, budget *UploadBudget) error {
	if len(tags) > maxNameTags {
		return fmt.Errorf("render: name tag count %d exceeds %d", len(tags), maxNameTags)
	}
	renderer.ordered = orderedNameTagsInto(renderer.ordered[:0], tags)
	for index := range renderer.ordered {
		renderer.ordered[index].Text = truncateNameTagText(renderer.ordered[index].Text)
		renderer.atlas.Request(renderer.ordered[index].Text)
	}
	if err := renderer.atlas.FlushUploads(budget); err != nil {
		return err
	}

	renderer.layout = layoutOrderedNameTags(&renderer.layout, renderer.atlas, renderer.ordered)
	encodeNameTagBackgrounds(
		renderer.upload[nameTagBackgroundOffset:nameTagBackgroundOffset+nameTagBackgroundSize],
		renderer.layout.backgrounds,
	)
	encodeNameTagGlyphs(renderer.upload[nameTagGlyphOffset:nameTagUploadBytes], renderer.layout.glyphs)
	return nil
}

func layoutNameTags(dst *nameTagLayout, atlas GlyphSource, tags []NameTag) nameTagLayout {
	return layoutOrderedNameTags(dst, atlas, orderedNameTags(tags))
}

func layoutOrderedNameTags(dst *nameTagLayout, atlas GlyphSource, ordered []NameTag) nameTagLayout {
	if dst == nil {
		dst = &nameTagLayout{
			glyphs:      make([]nameTagGlyph, 0, maxNameTagGlyphs),
			backgrounds: make([]nameTagBackground, 0, maxNameTags),
		}
	}
	dst.glyphs = dst.glyphs[:0]
	dst.backgrounds = dst.backgrounds[:0]

	for _, tag := range ordered {
		text := truncateNameTagText(tag.Text)
		if text == "" {
			continue
		}

		start := len(dst.glyphs)
		penX := float32(0)
		minX, minY := float32(math.Inf(1)), float32(math.Inf(1))
		maxX, maxY := float32(math.Inf(-1)), float32(math.Inf(-1))
		var previous rune
		havePrevious := false
		for _, char := range text {
			if havePrevious {
				penX += atlas.Kern(previous, char)
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
			previous = char
			havePrevious = true
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
	return orderedNameTagsInto(nil, tags)
}

func orderedNameTagsInto(dst []NameTag, tags []NameTag) []NameTag {
	ordered := append(dst, tags...)
	slices.SortFunc(ordered, func(left, right NameTag) int {
		return compareEntityKeys(left.Key, right.Key)
	})
	return ordered
}

func truncateNameTagText(text string) string {
	runes := 0
	for index := range text {
		if runes == maxNameTagRunes {
			return text[:index]
		}
		runes++
	}
	return text
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
