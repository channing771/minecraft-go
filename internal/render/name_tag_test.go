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

type nameTagTestView struct{ releases int }

func (view *nameTagTestView) Release() { view.releases++ }

func float32At(data []byte, offset int) float32 {
	return math.Float32frombits(binary.LittleEndian.Uint32(data[offset : offset+4]))
}
