package network

import (
	"errors"

	"github.com/channing771/mornlea/internal/core"
)

const (
	remotePlayerStateWireBytes   = 41
	remotePlayerStatesMaxPayload = 8 + 1 + 7*remotePlayerStateWireBytes
)

const (
	dropIDWireBytes   = 4 + 4 + 4 + 1 + 4
	itemDropWireBytes = dropIDWireBytes + 4 + 2 + 1 + 2
)

// encodeItemStack 是所有携带物品堆的消息共用的 5 字节固定编码。
func encodeItemStack(e *byteEncoder, stack core.ItemStack) {
	e.u16(uint16(stack.Item))
	e.u8(stack.Count)
	e.u16(stack.Durability)
}

// decodeItemStack 读取一格固定编码；沿用已有错误则原样返回。
func decodeItemStack(d *byteDecoder, err error) (core.ItemStack, error) {
	if err != nil {
		return core.ItemStack{}, err
	}
	item, err := d.u16()
	if err != nil {
		return core.ItemStack{}, err
	}
	count, err := d.u8()
	if err != nil {
		return core.ItemStack{}, err
	}
	durability, err := d.u16()
	if err != nil {
		return core.ItemStack{}, err
	}
	return core.ItemStack{
		Item: core.ItemID(item), Count: count, Durability: durability,
	}, nil
}

// containerRefWireBytes 是容器引用（熔炉与箱子共用）的固定编码长度：
// 在原熔炉引用 17 字节的基础上追加 1 字节 Kind。
const containerRefWireBytes = 4 + 4 + 4 + 1 + 1 + 4

// encodeContainerRef 是熔炉与箱子共用的唯一容器引用编码 helper，
// 禁止为箱子单独维护一份重复实现。
func encodeContainerRef(e *byteEncoder, ref core.ContainerRef) {
	e.i32(int32(ref.Dimension))
	e.i32(ref.Chunk.X)
	e.i32(ref.Chunk.Z)
	e.u8(uint8(ref.Kind))
	e.u8(ref.Slot)
	e.u32(ref.Generation)
}

// decodeContainerRef 是 encodeContainerRef 对应的唯一解码 helper。
func decodeContainerRef(d *byteDecoder) (core.ContainerRef, error) {
	var ref core.ContainerRef
	dimension, err := d.i32()
	if err != nil {
		return core.ContainerRef{}, err
	}
	ref.Dimension = core.DimensionID(dimension)
	if ref.Chunk.X, err = d.i32(); err != nil {
		return core.ContainerRef{}, err
	}
	if ref.Chunk.Z, err = d.i32(); err != nil {
		return core.ContainerRef{}, err
	}
	kind, err := d.u8()
	if err != nil {
		return core.ContainerRef{}, err
	}
	ref.Kind = core.ContainerKind(kind)
	if ref.Slot, err = d.u8(); err != nil {
		return core.ContainerRef{}, err
	}
	if ref.Generation, err = d.u32(); err != nil {
		return core.ContainerRef{}, err
	}
	return ref, nil
}

func encodeDropID(e *byteEncoder, id core.DropID) {
	e.i32(int32(id.Dimension))
	e.i32(id.Chunk.X)
	e.i32(id.Chunk.Z)
	e.u8(id.Slot)
	e.u32(id.Generation)
}

func decodeDropID(d *byteDecoder) (core.DropID, error) {
	var id core.DropID
	dimension, err := d.i32()
	if err != nil {
		return core.DropID{}, err
	}
	id.Dimension = core.DimensionID(dimension)
	if id.Chunk.X, err = d.i32(); err != nil {
		return core.DropID{}, err
	}
	if id.Chunk.Z, err = d.i32(); err != nil {
		return core.DropID{}, err
	}
	if id.Slot, err = d.u8(); err != nil {
		return core.DropID{}, err
	}
	if id.Generation, err = d.u32(); err != nil {
		return core.DropID{}, err
	}
	return id, nil
}

// decodeItemDropBatchCount 在分配任何切片前校验计数与剩余字节。
func decodeItemDropBatchCount(d *byteDecoder, itemBytes int) (uint32, error) {
	count, err := d.uvarint()
	if err != nil {
		return 0, err
	}
	if count < 1 || count > MaxItemDropBatch {
		return 0, errors.New("network: item drop batch count is outside 1..32")
	}
	if len(d.data)-d.offset < int(count)*itemBytes {
		return 0, errCountShortInput
	}
	return count, nil
}

func decodeItemDropUpserts(d *byteDecoder) (ServerPacket, error) {
	var result ItemDropUpserts
	serverTick, err := d.u64()
	if err != nil {
		return nil, err
	}
	result.ServerTick = serverTick
	count, err := decodeItemDropBatchCount(d, itemDropWireBytes)
	if err != nil {
		return nil, err
	}
	result.Drops = make([]ItemDrop, count)
	for index := range result.Drops {
		drop := &result.Drops[index]
		if drop.ID, err = decodeDropID(d); err != nil {
			return nil, err
		}
		if drop.BlockIndex, err = d.u32(); err != nil {
			return nil, err
		}
		stack, err := decodeItemStack(d, nil)
		if err != nil {
			return nil, err
		}
		drop.Item = stack.Item
		drop.Count = stack.Count
		drop.Durability = stack.Durability
	}
	return result, nil
}

func decodeItemDropRemoves(d *byteDecoder) (ServerPacket, error) {
	var result ItemDropRemoves
	serverTick, err := d.u64()
	if err != nil {
		return nil, err
	}
	result.ServerTick = serverTick
	count, err := decodeItemDropBatchCount(d, dropIDWireBytes)
	if err != nil {
		return nil, err
	}
	result.IDs = make([]core.DropID, count)
	for index := range result.IDs {
		if result.IDs[index], err = decodeDropID(d); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func decodeRemotePlayerStates(d *byteDecoder) (ServerPacket, error) {
	var result RemotePlayerStates
	serverTick, err := d.u64()
	result.ServerTick = serverTick
	var count uint32
	if err == nil {
		count, err = d.uvarint()
	}
	if err == nil && (count < 1 || count > 7) {
		err = errors.New("network: remote player state count is outside 1..7")
	}
	if err == nil && len(d.data)-d.offset < int(count)*remotePlayerStateWireBytes {
		err = errors.New("network: remote player states exceed remaining payload")
	}
	if err == nil {
		result.Players = make([]RemotePlayerState, int(count))
		for index := range result.Players {
			player := &result.Players[index]
			var id []byte
			id, err = d.take(len(player.PlayerID))
			if err == nil {
				copy(player.PlayerID[:], id)
			}
			var dimension int32
			if err == nil {
				dimension, err = d.i32()
				player.Dimension = core.DimensionID(dimension)
			}
			for component := range player.Position {
				if err == nil {
					player.Position[component], err = d.f32()
				}
			}
			if err == nil {
				player.Yaw, err = d.f32()
			}
			if err == nil {
				player.Pitch, err = d.f32()
			}
			if err == nil {
				player.Reset, err = d.bool()
			}
			if err != nil {
				break
			}
		}
	}
	return result, err
}

func decodeBlockChanges(d *byteDecoder) (ServerPacket, error) {
	var result BlockChanges
	var dimension int32
	var err error
	dimension, err = d.i32()
	result.Dimension = core.DimensionID(dimension)
	if err == nil {
		result.Chunk.X, err = d.i32()
	}
	if err == nil {
		result.Chunk.Z, err = d.i32()
	}
	if err == nil {
		result.BaseRevision, err = d.u64()
	}
	if err == nil {
		result.NewRevision, err = d.u64()
	}
	var count uint32
	if err == nil {
		count, err = d.uvarint()
	}
	// v4 允许零条方块变化作为纯掉落物变化的 revision barrier。
	if err == nil && count > 4096 {
		err = errInvalidCount
	}
	if err == nil && len(d.data)-d.offset < int(count)*14 {
		err = errCountShortInput
	}
	if err == nil {
		result.Changes = make([]BlockChange, int(count))
		for index := range result.Changes {
			if result.Changes[index].Position.X, err = d.i32(); err != nil {
				break
			}
			if result.Changes[index].Position.Y, err = d.i32(); err != nil {
				break
			}
			if result.Changes[index].Position.Z, err = d.i32(); err != nil {
				break
			}
			var block uint16
			if block, err = d.u16(); err != nil {
				break
			}
			result.Changes[index].Block = core.BlockID(block)
		}
	}
	return result, err
}

func decodeForgetChunks(d *byteDecoder) (ServerPacket, error) {
	var result ForgetChunks
	dimension, err := d.i32()
	result.Dimension = core.DimensionID(dimension)
	var count uint32
	if err == nil {
		count, err = d.uvarint()
	}
	if err == nil && (count < 1 || count > 4096) {
		err = errInvalidCount
	}
	if err == nil && len(d.data)-d.offset < int(count)*8 {
		err = errCountShortInput
	}
	if err == nil {
		result.Chunks = make([]core.ChunkPos, int(count))
		for index := range result.Chunks {
			if result.Chunks[index].X, err = d.i32(); err != nil {
				break
			}
			if result.Chunks[index].Z, err = d.i32(); err != nil {
				break
			}
		}
	}
	return result, err
}
