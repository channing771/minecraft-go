package core

import "testing"

func TestBlockDisplayNameCoversRegisteredBlocks(t *testing.T) {
	want := [...]string{
		"空气", "屏障", "石头", "泥土", "草方块", "基岩", "石砖",
		"煤矿石", "铁矿石", "熔炉", "铁块", "箱子", "发光块", "圆石",
		"平滑石", "沙子", "砾石", "橡木原木", "橡木木板", "树叶", "玻璃",
		"砖块", "白色羊毛", "红色瓦块", "黏土", "雪块", "苔藓圆石",
		"水源", "一级流水", "二级流水", "三级流水", "四级流水", "五级流水", "六级流水", "七级流水",
		"干耕地", "湿耕地",
		"小麦阶段0", "小麦阶段1", "小麦阶段2", "小麦阶段3",
		"小麦阶段4", "小麦阶段5", "小麦阶段6", "小麦阶段7",
	}
	// want 必须与注册表等长：循环上界用 BlockIDMax 表达「全部已注册方块」，
	// 若只推进上界而忘了补显示名，下面的索引会越界 panic 而不是静默漏测；
	// 这条长度断言把诊断提前到"少了几条名字"。
	if len(want) != int(BlockIDMax) {
		t.Fatalf("显示名夹具有 %d 条，已注册方块有 %d 个：新增方块必须同步补显示名",
			len(want), int(BlockIDMax))
	}
	for id := AirID; id < BlockIDMax; id++ {
		got, ok := BlockDisplayName(id)
		if !ok || got != want[id] {
			t.Fatalf("BlockDisplayName(%d) = %q, %v，想要 %q, true", id, got, ok, want[id])
		}
	}
}

func TestBlockDisplayNameRejectsUnknownBlock(t *testing.T) {
	// 未注册编号一律用 BlockIDMax 表达：具体编号（历史上写过
	// MossyCobblestoneID+1、WaterLevel7ID+1）会在追加新方块时静默变成已注册，
	// 让本用例失去意义。
	if got, ok := BlockDisplayName(BlockIDMax); ok || got != "" {
		t.Fatalf("BlockDisplayName(未知 ID) = %q, %v，想要空字符串, false", got, ok)
	}
}
