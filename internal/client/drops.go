package client

import (
	"errors"
	"fmt"
	"slices"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
)

// MaxItemDrops 是客户端掉落物镜像的固定容量，与服务端单会话上限一致。
const MaxItemDrops = core.MaxSessionDrops

// ItemDropPresentation 是一个掉落物的只读呈现值。
type ItemDropPresentation struct {
	ID         core.DropID
	BlockIndex uint32
	Item       core.ItemID
	Count      uint8
	Durability uint16
}

// ItemDrops 是权威掉落物的固定容量只读镜像。
// 它由客户端主线程独占；批次先整体验证再应用，失败不部分修改。
type ItemDrops struct {
	drops         map[core.DropID]ItemDropPresentation
	presentations []ItemDropPresentation
}

func NewItemDrops() *ItemDrops {
	return &ItemDrops{
		drops:         make(map[core.DropID]ItemDropPresentation, MaxItemDrops),
		presentations: make([]ItemDropPresentation, 0, MaxItemDrops),
	}
}

// Apply 应用一条掉落物差分消息；未知消息类型返回错误。
func (mirror *ItemDrops) Apply(message network.ServerMessage) error {
	if mirror == nil {
		return errors.New("client: nil item drop mirror")
	}
	switch message := message.(type) {
	case network.ItemDropUpserts:
		return mirror.applyUpserts(message)
	case network.ItemDropRemoves:
		return mirror.applyRemoves(message)
	default:
		return fmt.Errorf("client: unsupported item drop message %T", message)
	}
}

func (mirror *ItemDrops) applyUpserts(upserts network.ItemDropUpserts) error {
	if err := upserts.Validate(); err != nil {
		return err
	}
	// 先预检容量，保证超限批次不部分应用。
	added := 0
	for _, drop := range upserts.Drops {
		if _, known := mirror.drops[drop.ID]; !known {
			added++
		}
	}
	if len(mirror.drops)+added > MaxItemDrops {
		return fmt.Errorf("client: item drop mirror exceeds %d entries", MaxItemDrops)
	}
	for _, drop := range upserts.Drops {
		mirror.drops[drop.ID] = ItemDropPresentation{
			ID: drop.ID, BlockIndex: drop.BlockIndex, Item: drop.Item, Count: drop.Count,
			Durability: drop.Durability,
		}
	}
	return nil
}

func (mirror *ItemDrops) applyRemoves(removes network.ItemDropRemoves) error {
	if err := removes.Validate(); err != nil {
		return err
	}
	for _, id := range removes.IDs {
		if _, known := mirror.drops[id]; !known {
			return fmt.Errorf("client: unknown item drop %+v", id)
		}
	}
	for _, id := range removes.IDs {
		delete(mirror.drops, id)
	}
	return nil
}

// Presentations 返回按稳定 ID 顺序排列的当前掉落物，复用内部缓冲。
func (mirror *ItemDrops) Presentations() []ItemDropPresentation {
	if mirror == nil {
		return nil
	}
	mirror.presentations = mirror.presentations[:0]
	for _, drop := range mirror.drops {
		mirror.presentations = append(mirror.presentations, drop)
	}
	slices.SortFunc(mirror.presentations, func(left, right ItemDropPresentation) int {
		return left.ID.Compare(right.ID)
	})
	return mirror.presentations
}

// Reset 清空上一会话或上一维度的镜像。
func (mirror *ItemDrops) Reset() {
	if mirror == nil {
		return
	}
	clear(mirror.drops)
	mirror.presentations = mirror.presentations[:0]
}
