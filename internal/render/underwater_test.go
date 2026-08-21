package render

import "testing"

// TestUnderwaterViewSwitchesWithTheSubmersionFlag 覆盖 spec Scenario
// 「入水与出水切换视觉」：相机在流体格时叠水色并压低可见半径，离开后两者都复原。
//
// 对照落在两条规则明显分歧的地方——**Tint 的 alpha 是否为 0** 与 **半径是否被压低**，
// 而不是"返回了某个结构体"。出水那一侧必须与本变更之前逐位一致（alpha 0 让调用方
// 整段跳过水色 TLV，帧字节不变），这是"关闭态零影响"唯一可断言的形状。
func TestUnderwaterViewSwitchesWithTheSubmersionFlag(t *testing.T) {
	const base = 32
	dry := UnderwaterViewFor(false, base)
	if dry.Tint != ([4]float32{}) {
		t.Fatalf("出水时 Tint=%v，想要全零（不叠加）", dry.Tint)
	}
	if dry.VisibleRadius != base {
		t.Fatalf("出水时可见半径=%d，想要原样的 %d", dry.VisibleRadius, base)
	}

	wet := UnderwaterViewFor(true, base)
	if wet.Tint[3] <= 0 {
		t.Fatalf("入水时 Tint alpha=%v，想要大于 0（必须叠加水色）", wet.Tint[3])
	}
	if wet.Tint[3] > 1 {
		t.Fatalf("入水时 Tint alpha=%v 超出 0..1", wet.Tint[3])
	}
	if wet.VisibleRadius >= dry.VisibleRadius {
		t.Fatalf("入水时可见半径=%d，想要严格小于出水时的 %d（必须压低远处可见度）",
			wet.VisibleRadius, dry.VisibleRadius)
	}
	if wet.VisibleRadius < 1 {
		t.Fatalf("入水时可见半径=%d，想要至少 1（不能把世界整个关掉）", wet.VisibleRadius)
	}
	// 水色必须真的是"水色"而不是纯黑或纯白：蓝分量应当明显高于红分量，
	// 否则叠加出来的是灰雾，Scenario 说的"水色"就没有落点。
	if wet.Tint[2] <= wet.Tint[0] {
		t.Fatalf("Tint RGB=%v 不是水色：蓝分量必须高于红分量", wet.Tint[:3])
	}
}

// TestUnderwaterViewNeverRaisesTheVisibleRadius 覆盖低视距配置：
// 水下半径只压不抬，绝不因为入水反而让人看得更远。
func TestUnderwaterViewNeverRaisesTheVisibleRadius(t *testing.T) {
	for _, base := range []int{1, 2, 4, 8, 32, 64} {
		wet := UnderwaterViewFor(true, base)
		if wet.VisibleRadius > base {
			t.Fatalf("基础半径 %d：水下半径=%d，想要不超过基础值", base, wet.VisibleRadius)
		}
		if dry := UnderwaterViewFor(false, base); dry.VisibleRadius != base {
			t.Fatalf("基础半径 %d：出水半径=%d，想要原样返回", base, dry.VisibleRadius)
		}
	}
	// 守卫排在真实断言之后：扫描区间里必须至少有一个基础半径真的被压低，
	// 否则上面那个循环只是在验证 min(x, y) == x，改坏压制常量也不会变红。
	lowered := 0
	for _, base := range []int{1, 2, 4, 8, 32, 64} {
		if UnderwaterViewFor(true, base).VisibleRadius < base {
			lowered++
		}
	}
	if lowered == 0 {
		t.Fatal("夹具无效：扫描区间内没有任何基础半径被压低，压制常量零覆盖")
	}
}
