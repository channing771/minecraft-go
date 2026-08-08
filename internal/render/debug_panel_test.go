package render

import (
	"strings"
	"testing"
	"unicode/utf8"

	"minecraft-go/internal/gfx"
)

// newTestPanelRenderer 复用 name_tag_test.go 里已有的假设备与假字形源
// （nameTagTestDevice、newFakeNameTagAtlas），不新造一套。
func newTestPanelRenderer(t *testing.T) (*DebugPanelRenderer, *nameTagTestDevice) {
	t.Helper()
	dev := &nameTagTestDevice{}
	renderer := NewDebugPanelRenderer(dev, gfx.FormatRGBA8Unorm, newFakeNameTagAtlas())
	t.Cleanup(renderer.Release)
	return renderer, dev
}

// newBenchPanelRenderer 是 newTestPanelRenderer 的 benchmark 版本。
func newBenchPanelRenderer(b *testing.B) (*DebugPanelRenderer, *nameTagTestDevice) {
	b.Helper()
	dev := &nameTagTestDevice{}
	renderer := NewDebugPanelRenderer(dev, gfx.FormatRGBA8Unorm, newFakeNameTagAtlas())
	b.Cleanup(renderer.Release)
	return renderer, dev
}

// samplePanelRows 是若干条代表性参数行：可编辑、选中、只读各一条。
func samplePanelRows() []PanelRow {
	return []PanelRow{
		{Label: "重力", Value: "32"},
		{Label: "跳跃力", Value: "8.4", Selected: true},
		{Label: "行走速度", Value: "4.3", ReadOnly: true},
	}
}

func TestDebugPanelInvisibleProducesNoInstances(t *testing.T) {
	renderer, _ := newTestPanelRenderer(t)
	if err := renderer.Prepare(false, PanelReadout{}, samplePanelRows(), 1280, 720, nil); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got := renderer.QuadCount(); got != 0 {
		t.Fatalf("关闭时不得产出实例，实际 %d 个矩形", got)
	}
	if got := renderer.GlyphCount(); got != 0 {
		t.Fatalf("关闭时不得产出字形，实际 %d 个", got)
	}
}

// 评审 Finding 1：原断言是 GlyphCount() > maxPanelGlyphs 才失败，但样本行
// 用的是 "参数"/"1.0" 这种 5 个字形的短文本——192 行不截断也只有
// 57(读数区)+192*5=1017 个字形，远够不到 3408 的上限，删掉行数截断整个测试
// 也会通过。改成标签与数值都占满 maxPanelRunesPerSide 的长文本，并断言
// 确切的字形数（读数区字形数 + 恰好 maxPanelRows 行的字形数），而不是一个
// 永远为真的上界，这样行数截断被删掉时数字会对不上。
func TestDebugPanelRespectsRowCap(t *testing.T) {
	rows := make([]PanelRow, maxPanelRows*3)
	for i := range rows {
		rows[i] = PanelRow{
			Label: strings.Repeat("参", maxPanelRunesPerSide),
			Value: strings.Repeat("9", maxPanelRunesPerSide),
		}
	}

	readoutOnly, _ := newTestPanelRenderer(t)
	if err := readoutOnly.Prepare(true, PanelReadout{}, nil, 1280, 720, nil); err != nil {
		t.Fatalf("Prepare(无参数行): %v", err)
	}
	readoutGlyphs := readoutOnly.GlyphCount()

	renderer, _ := newTestPanelRenderer(t)
	if err := renderer.Prepare(true, PanelReadout{}, rows, 1280, 720, nil); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	wantGlyphs := readoutGlyphs + maxPanelRows*maxPanelRunesPerSide*2
	if got := renderer.GlyphCount(); got != wantGlyphs {
		t.Fatalf("字形数=%d，想要 %d（%d 行超额输入必须被恰好截到 %d 行）",
			got, wantGlyphs, len(rows), maxPanelRows)
	}
	if got := renderer.QuadCount(); got != 1 {
		t.Fatalf("未选中任何行时矩形数=%d，想要 1（只有面板背景）", got)
	}
}

// 评审 Finding 2：原断言同样是永远为真的上界——一行 400+100 rune 的输入
// 就算完全不截断也只有 57+500=557 个字形，够不到 3408。改成断言确切数量
// （读数区字形数 + 恰好 maxPanelRunesPerSide*2），truncatePanelText 被
// 破坏（比如改成按字节截或者完全不截）时这个数字会对不上。
// 另见 TestTruncatePanelTextTruncatesByRuneNotByte：直接钉住“按 rune 不按
// 字节”这条性质，不依赖整条 Prepare 流水线。
func TestDebugPanelTruncatesLongText(t *testing.T) {
	readoutOnly, _ := newTestPanelRenderer(t)
	if err := readoutOnly.Prepare(true, PanelReadout{}, nil, 1280, 720, nil); err != nil {
		t.Fatalf("Prepare(无参数行): %v", err)
	}
	readoutGlyphs := readoutOnly.GlyphCount()

	renderer, _ := newTestPanelRenderer(t)
	rows := []PanelRow{{Label: strings.Repeat("超长标签", 100), Value: strings.Repeat("9", 100)}}
	if err := renderer.Prepare(true, PanelReadout{}, rows, 1280, 720, nil); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	wantGlyphs := readoutGlyphs + maxPanelRunesPerSide*2
	if got := renderer.GlyphCount(); got != wantGlyphs {
		t.Fatalf("超长文本必须截断到标签+数值各 %d 个字形，字形数=%d，想要 %d",
			maxPanelRunesPerSide, got, wantGlyphs)
	}
}

// 直接钉住 truncatePanelText 的核心性质：按 rune 计数截断，不是按字节。
// 中文标签是多字节 UTF-8，按字节截断会切出非法字符串；这条测试不经过
// Prepare/GlyphCount 这层间接断言，直接检查截断函数本身。
func TestTruncatePanelTextTruncatesByRuneNotByte(t *testing.T) {
	long := strings.Repeat("超长标签", 100) // 400 个多字节 rune
	got := truncatePanelText(long)
	if !utf8.ValidString(got) {
		t.Fatalf("截断结果不是合法 UTF-8: %q", got)
	}
	if n := utf8.RuneCountInString(got); n != maxPanelRunesPerSide {
		t.Fatalf("rune 数=%d，想要 %d（必须按 rune 截断，不能按字节截断切坏多字节字符）",
			n, maxPanelRunesPerSide)
	}
}

func TestDebugPanelReadOnlyRowUsesDimColor(t *testing.T) {
	renderer, _ := newTestPanelRenderer(t)
	rows := []PanelRow{{Label: "重力", Value: "32", ReadOnly: true}}
	if err := renderer.Prepare(true, PanelReadout{}, rows, 1280, 720, nil); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !renderer.hasDimmedGlyph() {
		t.Fatal("只读行必须以暗色绘制，以便一眼区分不可编辑的参数")
	}
}

// Mutation killed: 选中行如果不追加高亮矩形，选中前后的矩形数会相等。
// 断言用 before（不含高亮）而不是 brief 草稿里的 before+1：容量常量
// maxPanelQuads = 1 + maxPanelRows 明确按“每行至多一个选中高亮”预算，
// 因此正确实现下选中一行只应新增恰好 1 个矩形，用 before+1 判定会让
// 正确实现也被判失败。
//
// before/after 分别用独立的 renderer 准备：newFakeNameTagAtlas 的
// FlushUploads 按设计只允许调用一次（同名 name-tag 测试里 Prepare 也只
// 调一次），同一个 renderer 上连续两次 Prepare 会触发它的“调用超过一次”
// 保护，因此这里不复用 renderer 状态，只比较两次独立布局的矩形数。
func TestDebugPanelSelectedRowHasHighlight(t *testing.T) {
	rows := []PanelRow{{Label: "重力", Value: "32"}, {Label: "跳跃", Value: "8.4", Selected: true}}

	unselected, _ := newTestPanelRenderer(t)
	if err := unselected.Prepare(true, PanelReadout{}, rows[:1], 1280, 720, nil); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	before := unselected.QuadCount()

	selected, _ := newTestPanelRenderer(t)
	if err := selected.Prepare(true, PanelReadout{}, rows, 1280, 720, nil); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if selected.QuadCount() <= before {
		t.Fatal("选中行必须额外产出高亮矩形")
	}
}

// Mutation killed: 面板关闭是热路径的零开销出口；请求字形、布局或写入
// upload buffer 中的任何一步都会让这里产生堆分配。
func BenchmarkDebugPanelHidden(b *testing.B) {
	renderer, _ := newBenchPanelRenderer(b)
	rows := samplePanelRows()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := renderer.Prepare(false, PanelReadout{}, rows, 1280, 720, nil); err != nil {
			b.Fatal(err)
		}
	}
}
