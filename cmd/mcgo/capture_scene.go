package main

import (
	"fmt"
	"sort"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/world"
)

func prepareSkylightTunnel(app *application) error {
	if err := prepareCaptureAirNeighborhood(app); err != nil {
		return err
	}

	stones := make(map[core.ChunkPos]map[core.BlockPos]struct{})
	addStone := func(position core.BlockPos) {
		chunk := position.Chunk()
		if stones[chunk] == nil {
			stones[chunk] = make(map[core.BlockPos]struct{})
		}
		stones[chunk][position] = struct{}{}
	}
	// 内部宽 5、高 4、长 20；入口 z=4 露天，z=-16 的后墙阻止背面漏光。
	for z := int32(-15); z <= 4; z++ {
		for x := int32(-3); x <= 3; x++ {
			addStone(core.BlockPos{X: x, Y: 0, Z: z})
			if z < 4 {
				addStone(core.BlockPos{X: x, Y: 5, Z: z})
			}
		}
		for y := int32(1); y <= 4; y++ {
			addStone(core.BlockPos{X: -3, Y: y, Z: z})
			addStone(core.BlockPos{X: 3, Y: y, Z: z})
		}
	}
	for y := int32(0); y <= 5; y++ {
		for x := int32(-3); x <= 3; x++ {
			addStone(core.BlockPos{X: x, Y: y, Z: -16})
		}
	}
	for z := int32(-1); z <= 1; z++ {
		for x := int32(-1); x <= 1; x++ {
			chunk := core.ChunkPos{X: x, Z: z}
			changes := make([]network.BlockChange, 0, len(stones[chunk]))
			for position := range stones[chunk] {
				changes = append(changes, network.BlockChange{
					Position: position,
					Block:    core.StoneID,
				})
			}
			sort.Slice(changes, func(i, j int) bool {
				left, _ := world.ChunkBlockIndex(changes[i].Position)
				right, _ := world.ChunkBlockIndex(changes[j].Position)
				return left < right
			})
			if err := applyCaptureMirror(app, network.BlockChanges{
				Dimension:    core.Overworld,
				Chunk:        chunk,
				BaseRevision: 1,
				NewRevision:  2,
				Changes:      changes,
			}); err != nil {
				return fmt.Errorf("装入通道变化 (%d,%d): %w", x, z, err)
			}
		}
	}
	return nil
}

func prepareCaptureAirNeighborhood(app *application) error {
	for z := int32(-1); z <= 1; z++ {
		for x := int32(-1); x <= 1; x++ {
			sections := make([]network.SectionData, core.SectionsPerChunk)
			for y := range sections {
				sections[y] = network.SectionData{
					Y: int32(y), Storage: network.SectionSingle, Single: core.AirID,
				}
			}
			if err := applyCaptureMirror(app, network.ChunkSnapshot{
				Dimension: core.Overworld,
				Chunk:     core.ChunkPos{X: x, Z: z},
				Revision:  1,
				Sections:  sections,
			}); err != nil {
				return fmt.Errorf("装入空气快照 (%d,%d): %w", x, z, err)
			}
		}
	}
	return nil
}

func prepareBlockLightRoom(app *application) error {
	if err := prepareCaptureAirNeighborhood(app); err != nil {
		return err
	}
	return applyCaptureBlockLightRoomChanges(app)
}

func applyCaptureBlockLightRoomChanges(app *application) error {
	blocks := make(map[core.ChunkPos]map[core.BlockPos]core.BlockID)
	setBlock := func(position core.BlockPos, block core.BlockID) {
		chunk := position.Chunk()
		if blocks[chunk] == nil {
			blocks[chunk] = make(map[core.BlockPos]core.BlockID)
		}
		blocks[chunk][position] = block
	}
	for z := int32(-10); z <= 2; z++ {
		for x := int32(-6); x <= 6; x++ {
			setBlock(core.BlockPos{X: x, Y: 0, Z: z}, core.StoneID)
			setBlock(core.BlockPos{X: x, Y: 6, Z: z}, core.StoneID)
		}
		for y := int32(1); y <= 5; y++ {
			setBlock(core.BlockPos{X: -6, Y: y, Z: z}, core.StoneID)
			setBlock(core.BlockPos{X: 6, Y: y, Z: z}, core.StoneID)
		}
	}
	for y := int32(1); y <= 5; y++ {
		for x := int32(-6); x <= 6; x++ {
			setBlock(core.BlockPos{X: x, Y: y, Z: -10}, core.StoneID)
			setBlock(core.BlockPos{X: x, Y: y, Z: 2}, core.StoneID)
		}
	}
	setBlock(core.BlockPos{X: 0, Y: 3, Z: -4}, core.LightBlockID)

	for z := int32(-1); z <= 1; z++ {
		for x := int32(-1); x <= 1; x++ {
			chunk := core.ChunkPos{X: x, Z: z}
			changes := make([]network.BlockChange, 0, len(blocks[chunk]))
			for position, block := range blocks[chunk] {
				changes = append(changes, network.BlockChange{
					Position: position,
					Block:    block,
				})
			}
			sort.Slice(changes, func(i, j int) bool {
				left, _ := world.ChunkBlockIndex(changes[i].Position)
				right, _ := world.ChunkBlockIndex(changes[j].Position)
				return left < right
			})
			if err := applyCaptureMirror(app, network.BlockChanges{
				Dimension:    core.Overworld,
				Chunk:        chunk,
				BaseRevision: 1,
				NewRevision:  2,
				Changes:      changes,
			}); err != nil {
				return fmt.Errorf("装入发光房间变化 (%d,%d): %w", x, z, err)
			}
		}
	}
	return nil
}

func applyCaptureMirror(app *application, message network.ServerMessage) error {
	update, err := app.mirror.Apply(message)
	if err != nil {
		return err
	}
	app.mesher.MarkDirty(update.Dirty...)
	return nil
}
