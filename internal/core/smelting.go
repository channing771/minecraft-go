package core

// SmeltingOutput 返回固定熔炼输入对应的唯一产物；未知输入返回 false。
func SmeltingOutput(input ItemID) (ItemID, bool) {
	switch input {
	case ItemRawIron:
		return ItemIronIngot, true
	case ItemSand:
		return ItemGlass, true
	case ItemClay:
		return ItemBrick, true
	default:
		return ItemNone, false
	}
}
