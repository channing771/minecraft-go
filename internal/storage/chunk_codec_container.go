package storage

import (
	"encoding/binary"
	"errors"
	"fmt"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

// validateFurnaceSlots 检查全部熔炉槽的固定约束：
// 活动槽的方块索引唯一、位于区块内且对应位置必须是熔炉方块。
func validateFurnaceSlots(chunk *world.Chunk) error {
	seen := make(map[uint32]int, core.FurnacesPerChunk)
	for slot := range core.FurnacesPerChunk {
		furnace := chunk.Furnace(slot)
		if !furnace.Valid() {
			return fmt.Errorf("furnace slot %d is not a valid fixed slot", slot)
		}
		if !furnace.Active {
			continue
		}
		if furnace.BlockIndex >= core.SectionsPerChunk*core.BlocksPerSection {
			return fmt.Errorf("furnace slot %d block index %d is outside the chunk",
				slot, furnace.BlockIndex)
		}
		if other, duplicate := seen[furnace.BlockIndex]; duplicate {
			return fmt.Errorf("furnace slots %d and %d share block index %d",
				other, slot, furnace.BlockIndex)
		}
		seen[furnace.BlockIndex] = slot
		pos, ok := world.BlockPosFromChunkIndex(chunk.Pos, furnace.BlockIndex)
		if !ok {
			return fmt.Errorf("furnace slot %d block index %d has no position",
				slot, furnace.BlockIndex)
		}
		lx, _, lz := pos.Local()
		if chunk.BlockAt(lx, pos.Y, lz) != core.FurnaceID {
			return fmt.Errorf("furnace slot %d does not point at a furnace block", slot)
		}
	}
	return nil
}

func appendLogicalFurnaceSlot(dst []byte, furnace world.FurnaceSlot) []byte {
	dst = appendU32(dst, furnace.Generation)
	active := byte(0)
	if furnace.Active {
		active = 1
	}
	dst = append(dst, active)
	dst = appendU32(dst, furnace.BlockIndex)
	for _, stack := range [3]core.ItemStack{furnace.Input, furnace.Fuel, furnace.Output} {
		dst = binary.LittleEndian.AppendUint16(dst, uint16(stack.Item))
		dst = append(dst, stack.Count)
	}
	dst = append(dst, furnace.ProgressTicks)
	return binary.LittleEndian.AppendUint16(dst, furnace.BurnTicks)
}

func decodeLogicalFurnaceSlot(d *byteDecoder) (world.FurnaceSlot, error) {
	var furnace world.FurnaceSlot
	generation, err := d.u32()
	if err != nil {
		return world.FurnaceSlot{}, err
	}
	furnace.Generation = generation
	active, err := d.u8()
	if err != nil {
		return world.FurnaceSlot{}, err
	}
	if active > 1 {
		return world.FurnaceSlot{}, fmt.Errorf("furnace active flag %d is not 0 or 1", active)
	}
	furnace.Active = active == 1
	blockIndex, err := d.u32()
	if err != nil {
		return world.FurnaceSlot{}, err
	}
	furnace.BlockIndex = blockIndex
	stacks := [3]*core.ItemStack{&furnace.Input, &furnace.Fuel, &furnace.Output}
	for _, stack := range stacks {
		item, err := d.u16()
		if err != nil {
			return world.FurnaceSlot{}, err
		}
		count, err := d.u8()
		if err != nil {
			return world.FurnaceSlot{}, err
		}
		stack.Item = core.ItemID(item)
		stack.Count = count
	}
	progress, err := d.u8()
	if err != nil {
		return world.FurnaceSlot{}, err
	}
	furnace.ProgressTicks = progress
	burn, err := d.u16()
	if err != nil {
		return world.FurnaceSlot{}, err
	}
	furnace.BurnTicks = burn
	if !furnace.Valid() {
		return world.FurnaceSlot{}, errors.New("furnace slot is not a valid fixed slot")
	}
	return furnace, nil
}

// validateChestSlots 检查全部箱子槽的固定约束：活动槽的方块索引唯一、
// 位于区块内且对应位置必须是箱子方块。箱子的 27 个格子不限制物品类型，
// 逐格调用 core.ItemStack.Valid() 已在 ChestSlot.Valid() 中完成。
func validateChestSlots(chunk *world.Chunk) error {
	seen := make(map[uint32]int, core.ChestsPerChunk)
	for slot := range core.ChestsPerChunk {
		chest := chunk.Chest(slot)
		if !chest.Valid() {
			return fmt.Errorf("chest slot %d is not a valid fixed slot", slot)
		}
		if !chest.Active {
			continue
		}
		if chest.BlockIndex >= core.SectionsPerChunk*core.BlocksPerSection {
			return fmt.Errorf("chest slot %d block index %d is outside the chunk",
				slot, chest.BlockIndex)
		}
		if other, duplicate := seen[chest.BlockIndex]; duplicate {
			return fmt.Errorf("chest slots %d and %d share block index %d",
				other, slot, chest.BlockIndex)
		}
		seen[chest.BlockIndex] = slot
		pos, ok := world.BlockPosFromChunkIndex(chunk.Pos, chest.BlockIndex)
		if !ok {
			return fmt.Errorf("chest slot %d block index %d has no position",
				slot, chest.BlockIndex)
		}
		lx, _, lz := pos.Local()
		if chunk.BlockAt(lx, pos.Y, lz) != core.ChestID {
			return fmt.Errorf("chest slot %d does not point at a chest block", slot)
		}
	}
	return nil
}

func appendLogicalChestSlot(dst []byte, chest world.ChestSlot) []byte {
	dst = appendU32(dst, chest.Generation)
	active := byte(0)
	if chest.Active {
		active = 1
	}
	dst = append(dst, active)
	dst = appendU32(dst, chest.BlockIndex)
	for _, stack := range chest.Items {
		dst = binary.LittleEndian.AppendUint16(dst, uint16(stack.Item))
		dst = append(dst, stack.Count)
		dst = binary.LittleEndian.AppendUint16(dst, stack.Durability)
	}
	return dst
}

func decodeLogicalChestSlot(d *byteDecoder) (world.ChestSlot, error) {
	var chest world.ChestSlot
	generation, err := d.u32()
	if err != nil {
		return world.ChestSlot{}, err
	}
	chest.Generation = generation
	active, err := d.u8()
	if err != nil {
		return world.ChestSlot{}, err
	}
	if active > 1 {
		return world.ChestSlot{}, fmt.Errorf("chest active flag %d is not 0 or 1", active)
	}
	chest.Active = active == 1
	blockIndex, err := d.u32()
	if err != nil {
		return world.ChestSlot{}, err
	}
	chest.BlockIndex = blockIndex
	for index := range chest.Items {
		item, err := d.u16()
		if err != nil {
			return world.ChestSlot{}, err
		}
		count, err := d.u8()
		if err != nil {
			return world.ChestSlot{}, err
		}
		durability, err := d.u16()
		if err != nil {
			return world.ChestSlot{}, err
		}
		chest.Items[index] = core.ItemStack{Item: core.ItemID(item), Count: count, Durability: durability}
	}
	if !chest.Valid() {
		return world.ChestSlot{}, errors.New("chest slot is not a valid fixed slot")
	}
	return chest, nil
}

// validateDropSlot 检查活动槽的固定字段上限；非活动槽只保留 generation。
func validateDropSlot(drop world.DropSlot) error {
	if !drop.Active {
		return nil
	}
	if drop.Generation == 0 {
		return errors.New("active drop slot has zero generation")
	}
	if !drop.Stack.Valid() {
		return errors.New("drop stack is invalid")
	}
	if drop.BlockIndex >= core.SectionsPerChunk*core.BlocksPerSection {
		return fmt.Errorf("drop block index %d is outside the chunk", drop.BlockIndex)
	}
	return nil
}

func appendLogicalDropSlot(dst []byte, drop world.DropSlot) []byte {
	dst = appendU32(dst, drop.Generation)
	active := byte(0)
	if drop.Active {
		active = 1
	}
	dst = append(dst, active)
	dst = binary.LittleEndian.AppendUint16(dst, uint16(drop.Stack.Item))
	dst = append(dst, drop.Stack.Count)
	dst = binary.LittleEndian.AppendUint16(dst, drop.Stack.Durability)
	dst = appendU32(dst, drop.BlockIndex)
	dst = appendU32(dst, drop.AgeTicks)
	return append(dst, drop.PickupDelayTicks)
}

func decodeLogicalDropSlot(d *byteDecoder) (world.DropSlot, error) {
	var drop world.DropSlot
	var err error
	if drop.Generation, err = d.u32(); err != nil {
		return world.DropSlot{}, err
	}
	active, err := d.u8()
	if err != nil {
		return world.DropSlot{}, err
	}
	if active > 1 {
		return world.DropSlot{}, fmt.Errorf("invalid drop active flag %d", active)
	}
	drop.Active = active == 1
	item, err := d.u16()
	if err != nil {
		return world.DropSlot{}, err
	}
	drop.Stack.Item = core.ItemID(item)
	if drop.Stack.Count, err = d.u8(); err != nil {
		return world.DropSlot{}, err
	}
	if drop.Stack.Durability, err = d.u16(); err != nil {
		return world.DropSlot{}, err
	}
	if drop.BlockIndex, err = d.u32(); err != nil {
		return world.DropSlot{}, err
	}
	if drop.AgeTicks, err = d.u32(); err != nil {
		return world.DropSlot{}, err
	}
	if drop.PickupDelayTicks, err = d.u8(); err != nil {
		return world.DropSlot{}, err
	}
	if err := validateDropSlot(drop); err != nil {
		return world.DropSlot{}, err
	}
	return drop, nil
}

func decodeLegacyLogicalDropSlot(d *byteDecoder) (world.DropSlot, error) {
	var drop world.DropSlot
	var err error
	if drop.Generation, err = d.u32(); err != nil {
		return world.DropSlot{}, err
	}
	active, err := d.u8()
	if err != nil {
		return world.DropSlot{}, err
	}
	if active > 1 {
		return world.DropSlot{}, fmt.Errorf("invalid drop active flag %d", active)
	}
	drop.Active = active == 1
	item, err := d.u16()
	if err != nil {
		return world.DropSlot{}, err
	}
	drop.Stack.Item = core.ItemID(item)
	if drop.Stack.Count, err = d.u8(); err != nil {
		return world.DropSlot{}, err
	}
	if drop.BlockIndex, err = d.u32(); err != nil {
		return world.DropSlot{}, err
	}
	if drop.AgeTicks, err = d.u32(); err != nil {
		return world.DropSlot{}, err
	}
	if drop.PickupDelayTicks, err = d.u8(); err != nil {
		return world.DropSlot{}, err
	}
	if !drop.Active {
		return drop, nil
	}
	if drop.Generation == 0 {
		return world.DropSlot{}, errors.New("active drop slot has zero generation")
	}
	if drop.BlockIndex >= core.SectionsPerChunk*core.BlocksPerSection {
		return world.DropSlot{}, fmt.Errorf("drop block index %d is outside the chunk", drop.BlockIndex)
	}
	if _, ok := core.ItemMaxDurability(drop.Stack.Item); ok {
		if drop.Stack.Count < 1 || drop.Stack.Count > core.MaxStackCount {
			return world.DropSlot{}, errors.New("legacy tool drop stack is invalid")
		}
		return drop, nil
	}
	if !drop.Stack.Valid() {
		return world.DropSlot{}, errors.New("drop stack is invalid")
	}
	return drop, nil
}
