package render

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// samplePanelRows 是若干条代表性参数行：可编辑、选中、只读各一条。
func samplePanelRows() []PanelRow {
	return []PanelRow{
		{Label: "重力", Value: "32"},
		{Label: "跳跃力", Value: "8.4", Selected: true},
		{Label: "行走速度", Value: "4.3", ReadOnly: true},
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
