package network

import (
	"errors"

	"minecraft-go/internal/core"
)

// PlayerInput 仅承载玩家输入值；语义验证由 sim 和 client 完成。
type PlayerInput struct {
	Sequence uint64
	MoveX    int8
	MoveZ    int8
	Jump     bool
	Yaw      float32
	Pitch    float32
	Mining   bool
}

func (PlayerInput) clientMessage() {}
func (PlayerInput) clientPacket()  {}

func (input PlayerInput) Validate() error {
	if !finite32(input.Yaw) || !finite32(input.Pitch) {
		return errors.New("network: player input has non-finite rotation")
	}
	return nil
}

type PlaceBlock struct {
	Sequence   uint64
	Yaw, Pitch float32
	Slot       uint8
}

func (PlaceBlock) clientMessage() {}
func (PlaceBlock) clientPacket()  {}

func (command PlaceBlock) Validate() error {
	if !finite32(command.Yaw) || !finite32(command.Pitch) {
		return errors.New("network: place block has non-finite rotation")
	}
	if command.Slot >= core.HotbarSlots {
		return errors.New("network: place block hotbar slot is outside 0..8")
	}
	return nil
}

// SelectHotbar 请求把权威选中栏位切换到 Slot。
type SelectHotbar struct {
	Sequence uint64
	Slot     uint8
}

func (SelectHotbar) clientMessage() {}
func (SelectHotbar) clientPacket()  {}

func (command SelectHotbar) Validate() error {
	if command.Slot >= core.HotbarSlots {
		return errors.New("network: hotbar selection slot is outside 0..8")
	}
	return nil
}

// CraftRecipe 按稳定配方 ID 请求一次合成；原料与产物完全由服务端决定。
type CraftRecipe struct {
	Sequence uint64
	Recipe   core.RecipeID
}

func (CraftRecipe) clientMessage() {}
func (CraftRecipe) clientPacket()  {}

func (command CraftRecipe) Validate() error {
	if _, ok := core.Recipe(command.Recipe); !ok {
		return errors.New("network: unknown crafting recipe")
	}
	return nil
}

type RequestChunkResync struct {
	Sequence     uint64
	Dimension    core.DimensionID
	Chunk        core.ChunkPos
	HaveRevision uint64
}

func (RequestChunkResync) clientMessage() {}
func (RequestChunkResync) clientPacket()  {}

func (request RequestChunkResync) Validate() error {
	if request.Dimension != core.Overworld {
		return errors.New("network: chunk resync dimension is not overworld")
	}
	return nil
}

// DropSelectedItem 请求把权威选中快捷栏中的一个物品原地转移为掉落物。
// 客户端不携带栏位或位置：权威选中格与脚底坐标都由服务端决定。
type DropSelectedItem struct {
	Sequence uint64
}

func (DropSelectedItem) clientMessage() {}
func (DropSelectedItem) clientPacket()  {}

func (DropSelectedItem) Validate() error { return nil }
