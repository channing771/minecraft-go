package hud

import (
	"reflect"
	"strings"
	"testing"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/render"
)

func TestChatOverlayShowsSixEventsAndInputWithinFixedCapacity(t *testing.T) {
	line := strings.Repeat("界", 33)
	lines := []string{line, line, line, line, line, line, line}
	renderer := &HotbarRenderer{
		atlas: newFakeNameTagAtlas(),
		layout: hotbarLayout{
			quads:  make([]hotbarInstance, 0, maxHotbarQuads),
			glyphs: make([]hotbarInstance, 0, maxHotbarGlyphs),
		},
		upload: make([]byte, hotbarUploadBytes),
	}
	if err := renderer.Prepare(
		core.Inventory{}, false, false, -1, nil, nil, MiningOverlay{}, HealthOverlay{}, OxygenOverlay{},
		ChatOverlay{Open: true, Input: line, Lines: lines},
		1280, 720, render.NewUploadBudget(1<<20),
	); err != nil {
		t.Fatal(err)
	}
	if len(renderer.layout.quads) != 2 || len(renderer.layout.glyphs) != 448 {
		t.Fatalf("chat layout quads=%d glyphs=%d", len(renderer.layout.quads), len(renderer.layout.glyphs))
	}
	if len(renderer.layout.quads) > maxHotbarQuads || len(renderer.layout.glyphs) > maxHotbarGlyphs {
		t.Fatalf("chat layout exceeds fixed capacity")
	}
}

func TestChatOverlayHotPathAllocations(t *testing.T) {
	source := &chatCountingGlyphSource{}
	renderer := &HotbarRenderer{
		atlas: source,
		layout: hotbarLayout{
			quads:  make([]hotbarInstance, 0, maxHotbarQuads),
			glyphs: make([]hotbarInstance, 0, maxHotbarGlyphs),
		},
		upload: make([]byte, hotbarUploadBytes),
	}
	overlay := ChatOverlay{
		Open:  true,
		Input: "@阿木 挖石头",
		Lines: []string{"Chen → 阿木：挖石头", "系统：未找到伙伴 阿树"},
	}
	budget := render.NewUploadBudget(1024)
	prepare := func() {
		if err := renderer.Prepare(
			core.Inventory{}, false, false, -1, nil, nil, MiningOverlay{}, HealthOverlay{}, OxygenOverlay{},
			overlay, 1280, 720, budget,
		); err != nil {
			panic(err)
		}
	}
	prepare()
	if allocations := testing.AllocsPerRun(1000, prepare); allocations != 0 {
		t.Fatalf("chat overlay allocations=%v", allocations)
	}
	source.flushes = 0
	prepare()
	if source.flushes != 1 {
		t.Fatalf("chat overlay atlas flushes=%d want=1", source.flushes)
	}
}

func TestChatOverlayRequestsOnlyVisibleRunes(t *testing.T) {
	source := &recordingChatGlyphSource{}
	linePrefix := strings.Repeat("甲", maxChatRunes-1)
	inputPrefix := strings.Repeat("丁", maxChatRunes-1)
	overlay := ChatOverlay{
		Open:  true,
		Input: inputPrefix + "戊己",
		Lines: []string{linePrefix + "乙丙"},
	}
	if !requestChatText(source, overlay) {
		t.Fatal("visible chat text was not requested")
	}
	if want := []string{linePrefix, inputPrefix, "…"}; !reflect.DeepEqual(source.requests, want) {
		t.Fatalf("atlas requests=%q want=%q", source.requests, want)
	}
}

func TestChatOverlayGlyphInkDoesNotOverlapOrEscapePanels(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	atlas.tofu = render.Glyph{Advance: 10, BearingY: 25, Width: 8, Height: 24}
	layout := hotbarLayout{
		quads:  make([]hotbarInstance, 0, maxChatQuads),
		glyphs: make([]hotbarInstance, 0, 14),
	}
	appendChatOverlay(&layout, atlas, ChatOverlay{
		Open: true, Input: "输",
		Lines: []string{"甲", "乙", "丙", "丁", "戊", "己"},
	}, 1280, 720)
	if len(layout.quads) != 2 || len(layout.glyphs) != 14 {
		t.Fatalf("chat geometry quads/glyphs=%d/%d want=2/14", len(layout.quads), len(layout.glyphs))
	}
	assertChatGlyphPairInsidePanel(t, layout.glyphs[0:2], layout.quads[0])
	for line := range maxChatLines {
		start := 2 + line*2
		pair := layout.glyphs[start : start+2]
		assertChatGlyphPairInsidePanel(t, pair, layout.quads[1])
		if line == 0 {
			continue
		}
		previousShadow := layout.glyphs[start-2]
		currentForeground := pair[1]
		if previousShadow.Y+previousShadow.Height > currentForeground.Y {
			t.Fatalf("history lines %d/%d overlap: previousBottom=%v currentTop=%v",
				line-1, line, previousShadow.Y+previousShadow.Height, currentForeground.Y)
		}
	}
	if historyBottom, inputTop := layout.quads[1].Y+layout.quads[1].Height, layout.quads[0].Y; historyBottom > inputTop {
		t.Fatalf("history/input panels overlap: historyBottom=%v inputTop=%v", historyBottom, inputTop)
	}
}

func assertChatGlyphPairInsidePanel(t *testing.T, pair []hotbarInstance, panel hotbarInstance) {
	t.Helper()
	if len(pair) != 2 {
		t.Fatalf("glyph pair len=%d want=2", len(pair))
	}
	foreground, shadow := pair[1], pair[0]
	if foreground.Y < panel.Y || shadow.Y+shadow.Height > panel.Y+panel.Height {
		t.Fatalf("glyph ink outside panel: foregroundTop=%v shadowBottom=%v panel=[%v,%v]",
			foreground.Y, shadow.Y+shadow.Height, panel.Y, panel.Y+panel.Height)
	}
}

type chatCountingGlyphSource struct {
	allocationGlyphSource
	flushes int
}

func (source *chatCountingGlyphSource) FlushUploads(*render.UploadBudget) error {
	source.flushes++
	return nil
}

type recordingChatGlyphSource struct {
	allocationGlyphSource
	requests []string
}

func (source *recordingChatGlyphSource) Request(text string) {
	source.requests = append(source.requests, text)
}
