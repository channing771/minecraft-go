package network

import (
	"errors"
	"fmt"

	"minecraft-go/internal/core"
)

// MaxItemDropBatch 是单条掉落物消息可携带的固定上限。
const MaxItemDropBatch = 32

// ItemDrop 是一个权威掉落物堆的完整线上值。
type ItemDrop struct {
	ID         core.DropID
	BlockIndex uint32
	Item       core.ItemID
	Count      uint8
	Durability uint16
}

func (drop ItemDrop) validate() error {
	if !drop.ID.Valid() {
		return errors.New("network: invalid item drop ID")
	}
	if drop.BlockIndex >= maxChunkBlockIndex {
		return errors.New("network: item drop block index is outside the chunk")
	}
	stack := core.ItemStack{
		Item: drop.Item, Count: drop.Count, Durability: drop.Durability,
	}
	if !stack.Valid() {
		return errors.New("network: invalid item drop stack")
	}
	return nil
}

// ItemDropUpserts 按稳定 ID 顺序新增或整体替换掉落物。
type ItemDropUpserts struct {
	ServerTick uint64
	Drops      []ItemDrop
}

func (ItemDropUpserts) serverMessage() {}
func (ItemDropUpserts) serverPacket()  {}

func (upserts ItemDropUpserts) Validate() error {
	if err := validItemDropBatchLength(len(upserts.Drops)); err != nil {
		return err
	}
	for index, drop := range upserts.Drops {
		if err := drop.validate(); err != nil {
			return fmt.Errorf("network: item drop %d: %w", index, err)
		}
		if index > 0 && upserts.Drops[index-1].ID.Compare(drop.ID) >= 0 {
			return errors.New("network: item drop upserts are not strictly sorted")
		}
	}
	return nil
}

// ItemDropRemoves 按稳定 ID 顺序移除掉落物。
type ItemDropRemoves struct {
	ServerTick uint64
	IDs        []core.DropID
}

func (ItemDropRemoves) serverMessage() {}
func (ItemDropRemoves) serverPacket()  {}

func (removes ItemDropRemoves) Validate() error {
	if err := validItemDropBatchLength(len(removes.IDs)); err != nil {
		return err
	}
	for index, id := range removes.IDs {
		if !id.Valid() {
			return fmt.Errorf("network: item drop remove %d: invalid ID", index)
		}
		if index > 0 && removes.IDs[index-1].Compare(id) >= 0 {
			return errors.New("network: item drop removes are not strictly sorted")
		}
	}
	return nil
}

func validItemDropBatchLength(count int) error {
	if count < 1 || count > MaxItemDropBatch {
		return errors.New("network: item drop batch count is outside 1..32")
	}
	return nil
}
