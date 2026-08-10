package core

import "testing"

func TestBlockDisplayNameCoversRegisteredBlocks(t *testing.T) {
	want := [...]string{
		"空气", "屏障", "石头", "泥土", "草方块", "基岩", "石砖",
		"煤矿石", "铁矿石", "熔炉", "铁块", "箱子", "发光块", "圆石",
		"平滑石", "沙子", "砾石", "橡木原木", "橡木木板", "树叶", "玻璃",
		"砖块", "白色羊毛", "红色瓦块", "黏土", "雪块", "苔藓圆石",
	}
	for id := AirID; id <= MossyCobblestoneID; id++ {
		got, ok := BlockDisplayName(id)
		if !ok || got != want[id] {
			t.Fatalf("BlockDisplayName(%d) = %q, %v，想要 %q, true", id, got, ok, want[id])
		}
	}
}

func TestBlockDisplayNameRejectsUnknownBlock(t *testing.T) {
	if got, ok := BlockDisplayName(MossyCobblestoneID + 1); ok || got != "" {
		t.Fatalf("BlockDisplayName(未知 ID) = %q, %v，想要空字符串, false", got, ok)
	}
}
