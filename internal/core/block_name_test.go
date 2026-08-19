package core

import "testing"

func TestBlockDisplayNameCoversRegisteredBlocks(t *testing.T) {
	want := [...]string{
		"空气", "屏障", "石头", "泥土", "草方块", "基岩", "石砖",
		"煤矿石", "铁矿石", "熔炉", "铁块", "箱子", "发光块", "圆石",
		"平滑石", "沙子", "砾石", "橡木原木", "橡木木板", "树叶", "玻璃",
		"砖块", "白色羊毛", "红色瓦块", "黏土", "雪块", "苔藓圆石",
		"水源", "一级流水", "二级流水", "三级流水", "四级流水", "五级流水", "六级流水", "七级流水",
	}
	for id := AirID; id <= WaterLevel7ID; id++ {
		got, ok := BlockDisplayName(id)
		if !ok || got != want[id] {
			t.Fatalf("BlockDisplayName(%d) = %q, %v，想要 %q, true", id, got, ok, want[id])
		}
	}
}

func TestBlockDisplayNameRejectsUnknownBlock(t *testing.T) {
	// MossyCobblestoneID+1 现在是 WaterSourceID（已注册），真正越界的未知
	// 方块编号是 WaterLevel7ID+1。
	if got, ok := BlockDisplayName(WaterLevel7ID + 1); ok || got != "" {
		t.Fatalf("BlockDisplayName(未知 ID) = %q, %v，想要空字符串, false", got, ok)
	}
}
