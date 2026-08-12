package network

import (
	"errors"

	"minecraft-go/internal/core"
)

// OpenContainer 请求打开视线内的容器；服务端用权威射线验证距离与目标方块，
// 并自行判定命中的是熔炉还是箱子——客户端没有可信信息可以声明容器种类。
type OpenContainer struct {
	Sequence   uint64
	Yaw, Pitch float32
}

func (OpenContainer) clientMessage() {}
func (OpenContainer) clientPacket()  {}

func (command OpenContainer) Validate() error {
	if !finite32(command.Yaw) || !finite32(command.Pitch) {
		return errors.New("network: open container has non-finite rotation")
	}
	return nil
}

// MoveContainerStack 请求在容器的统一栏位之间整堆移动；统一栏位的上限与
// 额外限制由 Container.Kind 决定：熔炉是 0..38 且输出格只能作为来源，
// 箱子是 0..62 且没有额外限制。
type MoveContainerStack struct {
	Sequence  uint64
	Container core.ContainerRef
	From, To  uint8
}

func (MoveContainerStack) clientMessage() {}
func (MoveContainerStack) clientPacket()  {}

func (command MoveContainerStack) Validate() error {
	if err := validAnyContainerRef(command.Container); err != nil {
		return err
	}
	if command.From == command.To {
		return errors.New("network: container move source equals target")
	}
	switch command.Container.Kind {
	case core.ContainerKindFurnace:
		if command.From >= core.FurnaceViewSlots || command.To >= core.FurnaceViewSlots {
			return errors.New("network: furnace move slot is outside 0..38")
		}
		if command.To == core.FurnaceOutputSlot {
			return errors.New("network: furnace output slot cannot be a move target")
		}
	case core.ContainerKindChest:
		if command.From >= core.ChestViewSlots || command.To >= core.ChestViewSlots {
			return errors.New("network: chest move slot is outside 0..62")
		}
	}
	return nil
}

// CloseContainer 结束当前会话的容器查看关系。
type CloseContainer struct {
	Sequence uint64
}

func (CloseContainer) clientMessage() {}
func (CloseContainer) clientPacket()  {}

func (CloseContainer) Validate() error { return nil }

// FurnaceState 是服务端发给当前查看者的完整熔炉状态。
type FurnaceState struct {
	Furnace       core.FurnaceRef
	Input         core.ItemStack
	Fuel          core.ItemStack
	Output        core.ItemStack
	ProgressTicks uint8
	BurnTicks     uint16
}

func (FurnaceState) serverMessage() {}
func (FurnaceState) serverPacket()  {}

func (state FurnaceState) Validate() error {
	if err := validFurnaceRef(state.Furnace); err != nil {
		return err
	}
	if state.ProgressTicks >= core.FurnaceSmeltTicks || state.BurnTicks > core.FurnaceBurnTicks {
		return errors.New("network: furnace timers are outside their fixed ranges")
	}
	if !validFurnaceInput(state.Input) ||
		!state.Fuel.Valid() || (state.Fuel.Item != core.ItemNone && state.Fuel.Item != core.ItemCoal) ||
		!validFurnaceOutput(state.Output) {
		return errors.New("network: furnace slot holds an item it cannot contain")
	}
	return nil
}

// ChestState 是服务端发给当前查看者的完整箱子状态：容器引用加 27 个固定格子。
// 箱子格接受任何已注册物品，因此校验只需要 ItemStack.Valid。
type ChestState struct {
	Chest core.ContainerRef
	Items [core.ChestSlots]core.ItemStack
}

func (ChestState) serverMessage() {}
func (ChestState) serverPacket()  {}

func (state ChestState) Validate() error {
	if err := validChestRef(state.Chest); err != nil {
		return err
	}
	for _, stack := range state.Items {
		if !stack.Valid() {
			return errors.New("network: chest slot holds an invalid item stack")
		}
	}
	return nil
}

// ContainerClosed 通知客户端当前查看的容器已经失效，熔炉与箱子共用同一失效通知。
type ContainerClosed struct {
	Container core.ContainerRef
}

func (ContainerClosed) serverMessage() {}
func (ContainerClosed) serverPacket()  {}

func (closed ContainerClosed) Validate() error {
	return validAnyContainerRef(closed.Container)
}

// validFurnaceRef 检查熔炉引用的种类与固定字段范围。
func validFurnaceRef(ref core.FurnaceRef) error {
	if ref.Kind != core.ContainerKindFurnace {
		return errors.New("network: furnace ref kind is not furnace")
	}
	if ref.Dimension != core.Overworld {
		return errors.New("network: furnace dimension is not overworld")
	}
	if ref.Slot >= core.FurnacesPerChunk {
		return errors.New("network: furnace slot is outside 0..31")
	}
	if ref.Generation == 0 {
		return errors.New("network: furnace generation is zero")
	}
	return nil
}

// validChestRef 检查箱子引用的种类与固定字段范围。
func validChestRef(ref core.ContainerRef) error {
	if ref.Kind != core.ContainerKindChest {
		return errors.New("network: chest ref kind is not chest")
	}
	if ref.Dimension != core.Overworld {
		return errors.New("network: chest dimension is not overworld")
	}
	if ref.Slot >= core.ChestsPerChunk {
		return errors.New("network: chest slot is outside 0..15")
	}
	if ref.Generation == 0 {
		return errors.New("network: chest generation is zero")
	}
	return nil
}

// validAnyContainerRef 校验容器中性消息携带的引用：Kind 必须是已知的熔炉或箱子之一，
// 未知种类（包含既有协议之外伪造的枚举值）一律拒绝。
func validAnyContainerRef(ref core.ContainerRef) error {
	switch ref.Kind {
	case core.ContainerKindFurnace:
		return validFurnaceRef(ref)
	case core.ContainerKindChest:
		return validChestRef(ref)
	default:
		return errors.New("network: unknown container kind")
	}
}

// validFurnaceInput 报告输入格是否为空或装着已注册的熔炼输入。
func validFurnaceInput(stack core.ItemStack) bool {
	if !stack.Valid() {
		return false
	}
	if stack.Item == core.ItemNone {
		return true
	}
	_, ok := core.SmeltingOutput(stack.Item)
	return ok
}

// validFurnaceOutput 报告输出格是否为空或装着固定熔炼产物。
func validFurnaceOutput(stack core.ItemStack) bool {
	if !stack.Valid() {
		return false
	}
	switch stack.Item {
	case core.ItemNone, core.ItemIronIngot, core.ItemGlass, core.ItemBrick:
		return true
	default:
		return false
	}
}
