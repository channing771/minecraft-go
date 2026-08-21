package main

import (
	"fmt"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/config"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/world"
	"github.com/channing771/mornlea/internal/worldgen"
)

const captureOakGroveSeed int64 = 42

// prepareOakGrove 把固定 3×3 生成区块经既有网络快照和 mirror 路径装入。
func prepareOakGrove(app *application) error {
	// 注水必须与抓帧进程自己的世界一致：抓帧路径把 FluidEnabled 钉成编译期
	// 默认值（见 main.go 的 resolveConfig 与 options.Application.FluidEnabled），
	// 而本夹具的种子与抓帧世界的默认种子同为 42，覆盖的又只是区块 (-1..1)。
	// 若这里按另一个门控值生成，这 9 个区块就会与周围的海不接缝——注水关时是
	// 一片海里的干涸方坑，注水开时是干世界里的一块水洼，都会在 golden 里留下
	// 一条纯属人为的区块边界断层。
	//
	// 因此这里**读**默认值而不是写字面量：写死 true 只在"默认值恰好是 true"
	// 时与抓帧路径一致，默认值一旦翻回去就静默错位，而 golden 只会显示成
	// 一条难以归因的地形断层。
	generator := worldgen.New(captureOakGroveSeed, config.Defaults().FluidEnabled)
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
