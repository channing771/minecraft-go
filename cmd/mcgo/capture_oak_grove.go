package main

import (
	"fmt"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/world"
	"minecraft-go/internal/worldgen"
)

const captureOakGroveSeed int64 = 42

// prepareOakGrove 把固定 3×3 生成区块经既有网络快照和 mirror 路径装入。
func prepareOakGrove(app *application) error {
	generator := worldgen.New(captureOakGroveSeed)
	for z := int32(-1); z <= 1; z++ {
		for x := int32(-1); x <= 1; x++ {
			chunk := generator.GenerateChunk(core.ChunkPos{X: x, Z: z})
			if err := applyCaptureMirror(app, captureOakGroveSnapshot(chunk)); err != nil {
				return fmt.Errorf("装入橡树林区块 (%d,%d): %w", x, z, err)
			}
		}
	}
	return nil
}

// captureOakGroveSnapshot 将生成区块转换成与服务端相同的可验证快照。
func captureOakGroveSnapshot(chunk *world.Chunk) network.ChunkSnapshot {
	sections := make([]network.SectionData, core.SectionsPerChunk)
	for index := range sections {
		snapshot := chunk.Section(index).Blocks.Snapshot()
		sections[index] = network.SectionData{
			Y:       int32(index),
			Storage: network.SectionStorage(snapshot.Kind),
			Single:  snapshot.Single,
			Bits:    snapshot.Bits,
			Palette: append([]core.BlockID(nil), snapshot.Palette...),
			Packed:  append([]uint64(nil), snapshot.Packed...),
		}
	}
	return network.ChunkSnapshot{
		Dimension: core.Overworld,
		Chunk:     chunk.Pos,
		Revision:  1,
		Sections:  sections,
	}
}

func applyOakGroveCaptureState(app *application) error {
	app.worldTimeTicks = 6000
	app.camera.Pos = mgl32.Vec3{-3.5, 75.5, 12.5}
	app.camera.Yaw = 0
	app.camera.Pitch = -0.38
	app.inventoryOpen = false
	app.inventorySource = -1
	if app.remotePlayers == nil {
		return fmt.Errorf("oak-grove 需要远端玩家追踪器，当前为 nil")
	}
	app.remotePlayers.Reset()
	app.furnace.Reset()
	app.chest.Reset()
	if app.panel != nil {
		app.panel.visible = false
	}
	return app.inventory.Apply(network.InventoryState{Inventory: core.Inventory{}})
}
