package core

var blockDisplayNames = [...]string{
	"空气", "屏障", "石头", "泥土", "草方块", "基岩", "石砖",
	"煤矿石", "铁矿石", "熔炉", "铁块", "箱子", "发光块", "圆石",
	"平滑石", "沙子", "砾石", "橡木原木", "橡木木板", "树叶", "玻璃",
	"砖块", "白色羊毛", "红色瓦块", "黏土", "雪块", "苔藓圆石",
	// 8 个流体方块编号紧随苔藓圆石追加，显示名同步补齐，否则
	// RegisteredBlock 扩容后 BlockDisplayName 会索引越界 panic。
	"水源", "一级流水", "二级流水", "三级流水", "四级流水", "五级流水", "六级流水", "七级流水",
	// 10 个农业方块编号紧随流体追加，显示名同步补齐，否则 RegisteredBlock
	// 扩容后 BlockDisplayName 会索引越界 panic。
	"干耕地", "湿耕地",
	"小麦阶段0", "小麦阶段1", "小麦阶段2", "小麦阶段3",
	"小麦阶段4", "小麦阶段5", "小麦阶段6", "小麦阶段7",
}

// BlockDisplayName 返回已注册方块的中文显示名。
func BlockDisplayName(id BlockID) (string, bool) {
	if !RegisteredBlock(id) {
		return "", false
	}
	return blockDisplayNames[id], true
}
