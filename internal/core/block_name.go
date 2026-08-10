package core

var blockDisplayNames = [...]string{
	"空气", "屏障", "石头", "泥土", "草方块", "基岩", "石砖",
	"煤矿石", "铁矿石", "熔炉", "铁块", "箱子", "发光块", "圆石",
	"平滑石", "沙子", "砾石", "橡木原木", "橡木木板", "树叶", "玻璃",
	"砖块", "白色羊毛", "红色瓦块", "黏土", "雪块", "苔藓圆石",
}

// BlockDisplayName 返回已注册方块的中文显示名。
func BlockDisplayName(id BlockID) (string, bool) {
	if !RegisteredBlock(id) {
		return "", false
	}
	return blockDisplayNames[id], true
}
