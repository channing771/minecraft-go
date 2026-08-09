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

// 评审 Finding：原断言是 hasDimmedGlyph()（布尔），而顶部 7 行读数
// 恒为 ReadOnly:true，因此哪怕 appendPanelRow 完全无视 row.ReadOnly、
// 把参数行全画成亮色，这个布尔也照样为真。改成与"只含读数区"的基线做差，
// 并同时钉住两个方向：可编辑行不得贡献暗色字形，只读行必须贡献恰好
// 标签+数值的字形数。
func TestDebugPanelReadOnlyRowUsesDimColor(t *testing.T) {
	const label, value = "重力", "32"
	rowGlyphs := utf8.RuneCountInString(label) + utf8.RuneCountInString(value)

	readoutOnly, _ := newTestPanelRenderer(t)
	if err := readoutOnly.Prepare(true, PanelReadout{}, nil, 1280, 720, nil); err != nil {
		t.Fatalf("Prepare(无参数行): %v", err)
	}
	baseline := readoutOnly.dimmedGlyphCount()

	editable, _ := newTestPanelRenderer(t)
	if err := editable.Prepare(true, PanelReadout{},
		[]PanelRow{{Label: label, Value: value}}, 1280, 720, nil); err != nil {
		t.Fatalf("Prepare(可编辑行): %v", err)
	}
	if got := editable.dimmedGlyphCount(); got != baseline {
		t.Fatalf("可编辑行的暗色字形数=%d，想要 %d（只读读数区的基线）；"+
			"可编辑参数必须以亮色绘制", got, baseline)
	}

	readOnly, _ := newTestPanelRenderer(t)
	if err := readOnly.Prepare(true, PanelReadout{},
		[]PanelRow{{Label: label, Value: value, ReadOnly: true}}, 1280, 720, nil); err != nil {
		t.Fatalf("Prepare(只读行): %v", err)
	}
	if got := readOnly.dimmedGlyphCount(); got != baseline+rowGlyphs {
		t.Fatalf("只读行的暗色字形数=%d，想要 %d（基线 %d + 该行 %d 个字形）；"+
			"只读行必须以暗色绘制，以便一眼区分不可编辑的参数",
			got, baseline+rowGlyphs, baseline, rowGlyphs)
	}
}

// 评审 Finding：原版比较的是"1 行且未选中"与"2 行且其中 1 行选中"，把行数
// 与选中态搅在了一起——一个给**每一行**都追加高亮矩形的错误实现会得到 2 与 3，
// 照样满足 after > before。改成两次都用同一条行，只翻转 Selected，并断言差值
// 恰好为 1（容量常量 maxPanelQuads = 1 + maxPanelRows 就是按"每行至多一个选中
// 高亮"预算的）。这样"每行都高亮"给出 2 与 2，"完全不高亮"给出 1 与 1，两种
// 退化都会被判失败。
//
// 两次 Prepare 用独立的 renderer：newFakeNameTagAtlas 的 FlushUploads 按设计
// 只允许调用一次，同一个 renderer 上连续两次 Prepare 会触发它的保护。
func TestDebugPanelSelectedRowHasHighlight(t *testing.T) {
	row := PanelRow{Label: "重力", Value: "32"}

	unselected, _ := newTestPanelRenderer(t)
	if err := unselected.Prepare(true, PanelReadout{}, []PanelRow{row}, 1280, 720, nil); err != nil {
		t.Fatalf("Prepare(未选中): %v", err)
	}
	before := unselected.QuadCount()

	row.Selected = true
	selected, _ := newTestPanelRenderer(t)
	if err := selected.Prepare(true, PanelReadout{}, []PanelRow{row}, 1280, 720, nil); err != nil {
		t.Fatalf("Prepare(已选中): %v", err)
	}
	if got := selected.QuadCount(); got != before+1 {
		t.Fatalf("同一条行翻转 Selected 后矩形数=%d，想要 %d（未选中 %d + 恰好一个高亮）",
			got, before+1, before)
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
