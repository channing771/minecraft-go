package fluid

import (
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

// TestEvalCell_SourceSurvives 覆盖 spec Scenario「源方块永不自然消失」：
// 源方块四周（含上方）都不是流体、下方是实心（不触发垂直传播），推进求值后
// 源方块自身不应出现在写入集合里。
func TestEvalCell_SourceSurvives(t *testing.T) {
	w := newMemWorld()
	pos := core.BlockPos{X: 0, Y: 10, Z: 0}
	w.SetBlock(pos, core.WaterSourceID)
	w.SetBlock(core.BlockPos{X: 0, Y: 9, Z: 0}, core.StoneID) // 下方实心，不下落

	writes := evalCell(pos, w)
	if id, ok := writes[pos]; ok {
		t.Fatalf("源方块不应被规则改写自身，got writes[pos]=%v", id)
	}
}

// TestEvalCell_FlowingDiesWithoutSupport 覆盖 spec Scenario
// 「流动方块失去支撑后消失」：上方非流体、水平邻居中不存在等级更小的流体。
func TestEvalCell_FlowingDiesWithoutSupport(t *testing.T) {
	w := newMemWorld()
	pos := core.BlockPos{X: 0, Y: 10, Z: 0}
	w.SetBlock(pos, core.WaterLevel3ID)
	// 上方空气，四个水平邻居均为空气（等级不小于自身，实际上根本不是流体）。

	writes := evalCell(pos, w)
	got, ok := writes[pos]
	if !ok || got != core.AirID {
		t.Fatalf("失去支撑的流动方块应变为空气，got writes[pos]=%v ok=%v", got, ok)
	}
}

// TestEvalCell_FlowingSurvivesWithWeakerNeighbor 验证流动格若水平邻居等级
// 更小则存活，且存活后仍按下方是否可替换继续参与传播（此处下方实心，
// 于是进入水平传播分支）。
func TestEvalCell_FlowingSurvivesWithWeakerNeighbor(t *testing.T) {
	w := newMemWorld()
	pos := core.BlockPos{X: 0, Y: 10, Z: 0}
	w.SetBlock(pos, core.WaterLevel3ID)
	w.SetBlock(core.BlockPos{X: 0, Y: 9, Z: 0}, core.StoneID)        // 下方实心
	w.SetBlock(core.BlockPos{X: 1, Y: 10, Z: 0}, core.WaterLevel2ID) // 更强的水平邻居

	writes := evalCell(pos, w)
	if _, removed := writes[pos]; removed {
		t.Fatalf("有更强水平邻居支撑时不应变为空气")
	}
}

// TestEvalCell_FlowingSurvivesUnderFluidAbove 验证「上方是流体」也构成支撑。
func TestEvalCell_FlowingSurvivesUnderFluidAbove(t *testing.T) {
	w := newMemWorld()
	pos := core.BlockPos{X: 0, Y: 10, Z: 0}
	w.SetBlock(pos, core.WaterLevel7ID)
	w.SetBlock(core.BlockPos{X: 0, Y: 11, Z: 0}, core.WaterLevel1ID)
	w.SetBlock(core.BlockPos{X: 0, Y: 9, Z: 0}, core.StoneID)

	writes := evalCell(pos, w)
	if _, removed := writes[pos]; removed {
		t.Fatalf("上方是流体时不应变为空气")
	}
}

// TestEvalCell_VerticalPriority 覆盖 spec Scenario「垂直优先」：下方可替换时
// 只向下写等级 1，且本次不产生任何水平写入。
func TestEvalCell_VerticalPriority(t *testing.T) {
	w := newMemWorld()
	pos := core.BlockPos{X: 0, Y: 10, Z: 0}
	w.SetBlock(pos, core.WaterSourceID)
	// 下方是空气（可替换），四个水平邻居也是空气。

	writes := evalCell(pos, w)
	below := core.BlockPos{X: 0, Y: 9, Z: 0}
	if got, ok := writes[below]; !ok || got != core.WaterLevel1ID {
		t.Fatalf("下方应写入 WaterLevel1ID，got=%v ok=%v", got, ok)
	}
	if len(writes) != 1 {
		t.Fatalf("垂直优先时本次不应产生任何水平写入，got writes=%v", writes)
	}
}

// TestEvalCell_VerticalPriority_ReplacesWeakerFlow 验证下方“可替换”不仅指
// 空气，也包含等级更大（更弱）的流动水。
func TestEvalCell_VerticalPriority_ReplacesWeakerFlow(t *testing.T) {
	w := newMemWorld()
	pos := core.BlockPos{X: 0, Y: 10, Z: 0}
	w.SetBlock(pos, core.WaterLevel2ID)
	below := core.BlockPos{X: 0, Y: 9, Z: 0}
	w.SetBlock(below, core.WaterLevel5ID) // 更弱，可被替换
	// 需要存活：给一个更强水平邻居撑住，避免这条测试同时受存活判定干扰。
	w.SetBlock(core.BlockPos{X: 1, Y: 10, Z: 0}, core.WaterLevel1ID)

	writes := evalCell(pos, w)
	if got, ok := writes[below]; !ok || got != core.WaterLevel1ID {
		t.Fatalf("下方更弱的流动水应被替换为 WaterLevel1ID，got=%v ok=%v", got, ok)
	}
}

// TestEvalCell_HorizontalSpreadFromSource 覆盖 spec Scenario「水平传播递减」：
// 源（等级读作 0）向可替换的水平邻居写入等级 1。
func TestEvalCell_HorizontalSpreadFromSource(t *testing.T) {
	w := newMemWorld()
	pos := core.BlockPos{X: 0, Y: 10, Z: 0}
	w.SetBlock(pos, core.WaterSourceID)
	w.SetBlock(core.BlockPos{X: 0, Y: 9, Z: 0}, core.StoneID) // 下方实心，进入水平分支

	writes := evalCell(pos, w)
	for _, n := range horizontalNeighbors(pos) {
		if got, ok := writes[n]; !ok || got != core.WaterLevel1ID {
			t.Fatalf("水平邻居 %v 应写入 WaterLevel1ID，got=%v ok=%v", n, got, ok)
		}
	}
	if len(writes) != 4 {
		t.Fatalf("四个水平邻居都应被写入，got writes=%v", writes)
	}
}

// TestEvalCell_HorizontalSpreadDecrements 验证水平传播时等级严格 +1。
func TestEvalCell_HorizontalSpreadDecrements(t *testing.T) {
	w := newMemWorld()
	pos := core.BlockPos{X: 0, Y: 10, Z: 0}
	w.SetBlock(pos, core.WaterLevel3ID)
	w.SetBlock(core.BlockPos{X: 0, Y: 9, Z: 0}, core.StoneID)
	// 存活支撑：源在另一水平方向。
	w.SetBlock(core.BlockPos{X: 0, Y: 10, Z: -1}, core.WaterSourceID)

	writes := evalCell(pos, w)
	target := core.BlockPos{X: 1, Y: 10, Z: 0}
	if got, ok := writes[target]; !ok || got != core.WaterLevel4ID {
		t.Fatalf("等级 3 的水平传播应产出等级 4，got=%v ok=%v", got, ok)
	}
}

// TestEvalCell_HorizontalSpreadStopsAtSeven 覆盖 spec Scenario
// 「水平传播上界」：等级 7 的格下方不可替换时不再向任何水平方向传播。
func TestEvalCell_HorizontalSpreadStopsAtSeven(t *testing.T) {
	w := newMemWorld()
	pos := core.BlockPos{X: 0, Y: 10, Z: 0}
	w.SetBlock(pos, core.WaterLevel7ID)
	w.SetBlock(core.BlockPos{X: 0, Y: 9, Z: 0}, core.StoneID)
	// 存活支撑：上方是流体。
	w.SetBlock(core.BlockPos{X: 0, Y: 11, Z: 0}, core.WaterLevel1ID)

	writes := evalCell(pos, w)
	if len(writes) != 0 {
		t.Fatalf("等级 7 不应再向任何方向传播，got writes=%v", writes)
	}
}

// TestEvalCell_SourceNotOverwritten 覆盖 spec Scenario「源不可被流动方块
// 替换」：目标格是源时不写入。
func TestEvalCell_SourceNotOverwritten(t *testing.T) {
	w := newMemWorld()
	pos := core.BlockPos{X: 0, Y: 10, Z: 0}
	w.SetBlock(pos, core.WaterSourceID)
	w.SetBlock(core.BlockPos{X: 0, Y: 9, Z: 0}, core.StoneID)
	target := core.BlockPos{X: 1, Y: 10, Z: 0}
	w.SetBlock(target, core.WaterSourceID)

	writes := evalCell(pos, w)
	if _, ok := writes[target]; ok {
		t.Fatalf("源方块不应被流动传播改写，got writes[target]=%v", writes[target])
	}
}

// TestEvalCell_SolidNotOverwritten 覆盖 spec Scenario「实心方块不可替换」。
func TestEvalCell_SolidNotOverwritten(t *testing.T) {
	w := newMemWorld()
	pos := core.BlockPos{X: 0, Y: 10, Z: 0}
	w.SetBlock(pos, core.WaterSourceID)
	w.SetBlock(core.BlockPos{X: 0, Y: 9, Z: 0}, core.StoneID)
	target := core.BlockPos{X: 1, Y: 10, Z: 0}
	w.SetBlock(target, core.StoneID)

	writes := evalCell(pos, w)
	if _, ok := writes[target]; ok {
		t.Fatalf("实心方块不应被流动传播改写，got writes[target]=%v", writes[target])
	}
}

// TestEvalCell_StaleQueueEntryNoop 验证：若队列项对应的格在真正求值时已经
// 不是流体（比如已经变成空气或被放置了实心方块），evalCell 不产生任何写入。
func TestEvalCell_StaleQueueEntryNoop(t *testing.T) {
	w := newMemWorld()
	pos := core.BlockPos{X: 0, Y: 10, Z: 0}
	// 不设置 pos，读回来是 AirID。

	writes := evalCell(pos, w)
	if len(writes) != 0 {
		t.Fatalf("陈旧待更新项不应产生写入，got writes=%v", writes)
	}
}
