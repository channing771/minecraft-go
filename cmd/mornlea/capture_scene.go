package main

import (
	"fmt"
	"sort"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/physics"
	"github.com/channing771/mornlea/internal/render/hud"
	"github.com/channing771/mornlea/internal/world"
)

var captureAICompanionID = companion.ID{0: 0x42, 6: 0x40, 8: 0x80, 15: 0x14}

func prepareAICompanion(app *application) error {
	if err := prepareCaptureAirNeighborhood(app); err != nil {
		return err
	}
	changes := make([]network.BlockChange, 0, 11*13*2)
	for z := int32(0); z <= 12; z++ {
		for x := int32(0); x <= 10; x++ {
			changes = append(changes,
				network.BlockChange{Position: core.BlockPos{X: x, Y: -1, Z: z}, Block: core.StoneID},
				network.BlockChange{Position: core.BlockPos{X: x, Y: 0, Z: z}, Block: core.GrassID},
			)
		}
	}
	sort.Slice(changes, func(i, j int) bool {
		left, _ := world.ChunkBlockIndex(changes[i].Position)
		right, _ := world.ChunkBlockIndex(changes[j].Position)
		return left < right
	})
	return applyCaptureMirror(app, network.BlockChanges{
		Dimension: core.Overworld, Chunk: core.ChunkPos{},
		BaseRevision: 1, NewRevision: 2, Changes: changes,
	})
}

func applyAICompanionCaptureState(app *application) error {
	if app.remotePlayers == nil || app.companions == nil || app.chatEvents == nil || app.itemDrops == nil {
		return fmt.Errorf("ai-companion 需要完整客户端呈现镜像")
	}

	app.remotePlayers.Reset()
	app.companions.Reset()
	app.chatEvents.Reset()
	app.chatInput.Cancel()
	app.chatEventBuffer = [32]network.ChatEvent{}
	app.chatLines = [6]string{}
	app.chatLineCount = 0
	app.formattedChatEventID = 0
	app.inventory.Reset()
	app.inventoryOpen = false
	app.inventorySource = -1
	app.furnace.Reset()
	app.chest.Reset()
	app.miningOverlay = hud.MiningOverlay{}
	app.damageFeedback.Reset()
	app.damageStrength = 0
	app.itemDrops.Reset()
	app.remotePresentations = app.remotePresentations[:0]
	app.companionPresentations = app.companionPresentations[:0]
	app.remoteAvatars = app.remoteAvatars[:0]
	app.remoteNameTags = app.remoteNameTags[:0]
	app.itemDropInstances = app.itemDropInstances[:0]
	if app.panel != nil {
		app.panel.visible = false
	}

	app.worldTimeTicks = 6000
	app.camera = client.Camera{
		Pos: mgl32.Vec3{5.5, 3.2, 9.5}, Yaw: 0, Pitch: -0.05,
		FovY: mgl32.DegToRad(70), Aspect: float32(captureWidth) / captureHeight,
		Near: 0.1, Far: 2000,
	}
	app.center = cameraChunk(app.camera.Pos)
	app.blockTargetReset = false

	if err := app.companions.ApplySpawn(network.CompanionSpawn{
		ID: captureAICompanionID, Name: "阿木", Tick: 1, Dimension: core.Overworld,
		Position: mgl32.Vec3{5.5, 1, 4}, Yaw: 3.1415927,
	}); err != nil {
		return fmt.Errorf("装入固定伙伴: %w", err)
	}
	if err := app.chatEvents.Apply(network.ChatEvent{
		EventID: 1, PlayerID: core.PlayerID{0: 0x23, 6: 0x40, 8: 0x80, 15: 0x11},
		PlayerName: "旅人", CompanionID: captureAICompanionID, CompanionName: "阿木",
		Kind: network.ChatEventAccepted, Command: "挖石头",
	}); err != nil {
		return fmt.Errorf("装入固定聊天事件: %w", err)
	}
	app.chatInput.Open()
	for _, value := range "@阿木 挖石头" {
		app.chatInput.Append(value)
	}
	return nil
}

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

func prepareMaterialsShowcase(app *application) error {
	if err := prepareCaptureAirNeighborhood(app); err != nil {
		return err
	}
	blocks := make(map[core.ChunkPos]map[core.BlockPos]core.BlockID)
	setBlock := func(position core.BlockPos, block core.BlockID) {
		chunk := position.Chunk()
		if blocks[chunk] == nil {
			blocks[chunk] = make(map[core.BlockPos]core.BlockID)
		}
		blocks[chunk][position] = block
	}
	materials := [...]core.BlockID{
		core.CobblestoneID, core.SmoothStoneID, core.SandID, core.GravelID,
		core.OakLogID, core.OakPlanksID, core.LeavesID, core.GlassID,
		core.BrickID, core.WhiteWoolID, core.RoofTileID, core.ClayID,
		core.SnowBlockID, core.MossyCobblestoneID,
	}
	columnStarts := [...]int32{-10, -7, -4, -1, 2, 5, 8}
	for index, block := range materials {
		startY := int32(1)
		if index >= len(columnStarts) {
			startY = 4
		}
		startX := columnStarts[index%len(columnStarts)]
		for y := startY; y <= startY+1; y++ {
			for x := startX; x <= startX+1; x++ {
				setBlock(core.BlockPos{X: x, Y: y, Z: -8}, block)
			}
		}
	}
	for y := int32(4); y <= 5; y++ {
		for x := int32(-10); x <= -9; x++ {
			setBlock(core.BlockPos{X: x, Y: y, Z: -9}, core.BrickID)
		}
	}
	for x := int32(-4); x <= 3; x++ {
		setBlock(core.BlockPos{X: x, Y: 0, Z: -1}, core.GrassID)
	}
	for z := int32(-2); z <= 0; z++ {
		for x := int32(0); x <= 3; x++ {
			setBlock(core.BlockPos{X: x, Y: 4, Z: z}, core.StoneID)
		}
	}
	for y := int32(1); y <= 3; y++ {
		setBlock(core.BlockPos{X: 7, Y: y, Z: -1}, core.OakLogID)
	}

	for z := int32(-1); z <= 1; z++ {
		for x := int32(-1); x <= 1; x++ {
			chunk := core.ChunkPos{X: x, Z: z}
			changes := make([]network.BlockChange, 0, len(blocks[chunk]))
			for position, block := range blocks[chunk] {
				changes = append(changes, network.BlockChange{Position: position, Block: block})
			}
			sort.Slice(changes, func(i, j int) bool {
				left, _ := world.ChunkBlockIndex(changes[i].Position)
				right, _ := world.ChunkBlockIndex(changes[j].Position)
				return left < right
			})
			if err := applyCaptureMirror(app, network.BlockChanges{
				Dimension: core.Overworld, Chunk: chunk,
				BaseRevision: 1, NewRevision: 2, Changes: changes,
			}); err != nil {
				return fmt.Errorf("装入材料展示变化 (%d,%d): %w", x, z, err)
			}
		}
	}
	return nil
}

func prepareTargetBlockFeedback(app *application) error {
	if err := prepareCaptureAirNeighborhood(app); err != nil {
		return err
	}
	return applyCaptureMirror(app, network.BlockChanges{
		Dimension:    core.Overworld,
		Chunk:        core.ChunkPos{Z: -1},
		BaseRevision: 1,
		NewRevision:  2,
		Changes: []network.BlockChange{{
			Position: core.BlockPos{X: 0, Y: 3, Z: -3},
			Block:    core.BrickID,
		}},
	})
}

func applyCaptureMirror(app *application, message network.ServerMessage) error {
	update, err := app.mirror.Apply(message)
	if err != nil {
		return err
	}
	app.mesher.MarkDirty(update.Dirty...)
	return nil
}

// captureWaterBasinChunkRadius 是水景夹具覆盖的区块半径（以原点区块为中心）。
// 夹具在 x 上跨到 ±9、z 上跨到 −16，正好落在 3×3 的区块窗口里，与其余固定
// 场景（tunnel/room/showcase）用同一个窗口，便于共用空气快照与遍历。
const captureWaterBasinChunkRadius = 1

// prepareWaterBasin 装入水景夹具：一座石质水池，池底有可辨识的水下地形，
// 水体顶层沿 **+Z** 方向（即由池深处向岸边）从源方块递减到 7 级流动水，
// 形成一段连续的斜水面：z 越大等级越弱、`h_raw = 14 - level` 越低。
//
// 三件事同时被这一份夹具覆盖，与视觉门禁要求的三点对应：
//   - **水面斜坡**：顶层 y=4 沿 z 增大依次是 WaterSourceID → WaterLevel1..7ID
//     （见下方 `slope[z+10]`：z=−10 取 1 级、z=−4 取 7 级），角高度按
//     `h_raw = 14 - level` 插值，渲染出沿 +Z 连续下降的斜面而不是台阶。
//   - **水下视角**：水体足够深且四周留有余量，相机放进去就是眼睛浸没态。
//   - **水面之下的地形**：池底铺了沙丘、砾石带与一处露出水面的圆石堆，
//     三种材质在水下的可辨识度是「水下变暗但不立刻归零」的直接证据。
//
// 夹具全部走客户端只读镜像装入，不依赖任何流体模拟推进，因此与机器速度无关。
func prepareWaterBasin(app *application) error {
	if err := prepareCaptureAirNeighborhood(app); err != nil {
		return err
	}
	blocks := make(map[core.ChunkPos]map[core.BlockPos]core.BlockID)
	setBlock := func(position core.BlockPos, block core.BlockID) {
		chunk := position.Chunk()
		if blocks[chunk] == nil {
			blocks[chunk] = make(map[core.BlockPos]core.BlockID)
		}
		blocks[chunk][position] = block
	}
	// 池底与围墙。围墙比水面高两格，挡住背面漏光，让水下的暗度只由水深决定。
	for z := int32(-16); z <= 2; z++ {
		for x := int32(-9); x <= 9; x++ {
			setBlock(core.BlockPos{X: x, Y: 0, Z: z}, core.StoneID)
		}
		for y := int32(1); y <= 6; y++ {
			setBlock(core.BlockPos{X: -9, Y: y, Z: z}, core.StoneID)
			setBlock(core.BlockPos{X: 9, Y: y, Z: z}, core.StoneID)
		}
	}
	for y := int32(1); y <= 6; y++ {
		for x := int32(-9); x <= 9; x++ {
			setBlock(core.BlockPos{X: x, Y: y, Z: -16}, core.StoneID)
		}
	}
	// 水下地形：三块材质不同、高度不同的水下地形，用来看"水面之下的地形可见"。
	for z := int32(-14); z <= -11; z++ {
		for x := int32(-6); x <= -3; x++ {
			setBlock(core.BlockPos{X: x, Y: 1, Z: z}, core.SandID)
			setBlock(core.BlockPos{X: x, Y: 2, Z: z}, core.SandID)
		}
	}
	for z := int32(-13); z <= -6; z++ {
		for x := int32(1); x <= 6; x++ {
			setBlock(core.BlockPos{X: x, Y: 1, Z: z}, core.GravelID)
		}
	}
	// 圆石堆顶到 y=5，露出水面，给出"同一材质水上水下各是什么样"的对照。
	for z := int32(-8); z <= -7; z++ {
		for x := int32(-1); x <= 0; x++ {
			for y := int32(1); y <= 5; y++ {
				setBlock(core.BlockPos{X: x, Y: y, Z: z}, core.CobblestoneID)
			}
		}
	}
	// 岸：干地一侧铺草，让岸线在画面里可辨。
	for z := int32(-3); z <= 2; z++ {
		for x := int32(-8); x <= 8; x++ {
			setBlock(core.BlockPos{X: x, Y: 0, Z: z}, core.GrassID)
		}
	}
	// 水体。y=1..3 是满格水源；y=4 是顶层，沿 +Z（z 增大）递减出斜面。
	// 顺序在固体之后写，但只写尚未被固体占据的格——池底地形优先。
	slope := [...]core.BlockID{
		core.WaterLevel1ID, core.WaterLevel2ID, core.WaterLevel3ID, core.WaterLevel4ID,
		core.WaterLevel5ID, core.WaterLevel6ID, core.WaterLevel7ID,
	}
	for z := int32(-15); z <= -4; z++ {
		for x := int32(-8); x <= 8; x++ {
			for y := int32(1); y <= 4; y++ {
				position := core.BlockPos{X: x, Y: y, Z: z}
				if _, occupied := blocks[position.Chunk()][position]; occupied {
					continue
				}
				block := core.WaterSourceID
				if y == 4 && z >= -10 {
					block = slope[z+10]
				}
				setBlock(position, block)
			}
		}
	}
	return applyCaptureBlocks(app, blocks, captureWaterBasinChunkRadius, "水池")
}

// applyCaptureBlocks 把按区块分组的方块表逐区块装入客户端镜像。
// 每个区块内按 world.ChunkBlockIndex 排序后再发，保证同一份夹具在任何机器上
// 都产生逐字节相同的 BlockChanges——map 的遍历序不能进线上字节。
func applyCaptureBlocks(
	app *application,
	blocks map[core.ChunkPos]map[core.BlockPos]core.BlockID,
	chunkRadius int32,
	label string,
) error {
	for z := -chunkRadius; z <= chunkRadius; z++ {
		for x := -chunkRadius; x <= chunkRadius; x++ {
			chunk := core.ChunkPos{X: x, Z: z}
			changes := make([]network.BlockChange, 0, len(blocks[chunk]))
			for position, block := range blocks[chunk] {
				changes = append(changes, network.BlockChange{Position: position, Block: block})
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
				return fmt.Errorf("装入%s变化 (%d,%d): %w", label, x, z, err)
			}
		}
	}
	return nil
}

// resetCapturePresentation 把共用同一个 application 的呈现状态清回空。
//
// 水景两个场景排在 ai-companion 之后，而那个场景会留下伙伴、聊天事件、打开的
// 聊天输入与格式化缓存——不显式清掉就会静默出现在水面上。清理项与
// applyAICompanionCaptureState 自己开头的那段一一对应：谁留下的谁负责被清掉，
// 但清理的落点必须在**后一个**场景，因为场景表没有 teardown 钩子。
func resetCapturePresentation(app *application) error {
	if app.remotePlayers == nil || app.companions == nil ||
		app.chatEvents == nil || app.itemDrops == nil {
		return fmt.Errorf("水景场景需要完整客户端呈现镜像")
	}
	app.remotePlayers.Reset()
	app.companions.Reset()
	app.chatEvents.Reset()
	app.chatInput.Cancel()
	app.chatEventBuffer = [32]network.ChatEvent{}
	app.chatLines = [6]string{}
	app.chatLineCount = 0
	app.formattedChatEventID = 0
	app.itemDrops.Reset()
	app.remotePresentations = app.remotePresentations[:0]
	app.companionPresentations = app.companionPresentations[:0]
	app.remoteAvatars = app.remoteAvatars[:0]
	app.remoteNameTags = app.remoteNameTags[:0]
	app.itemDropInstances = app.itemDropInstances[:0]
	app.miningOverlay = hud.MiningOverlay{}
	app.damageFeedback.Reset()
	app.damageStrength = 0
	app.furnace.Reset()
	app.chest.Reset()
	app.inventoryOpen = false
	app.inventorySource = -1
	app.blockTargetReset = false
	if app.panel != nil {
		app.panel.visible = false
	}
	return app.inventory.Apply(network.InventoryState{Inventory: core.Inventory{}})
}

// captureUnderwaterEyePosition 是 water-underwater 场景注入的权威玩家位置
// （脚底）。选点要求：脚与眼所在格都是水，且四周离最近的非流体格有若干格余量。
//
// 余量是必需的：ApplyPlayerState 会重放尚未确认的历史输入，而历史长度取决于
// 服务端在抓帧期间确认到第几条，也就是取决于机器速度。位置选在水体正中，
// 重放几步造成的微小位移就不可能把浸没标志翻过去，画面因此仍然确定。
var captureUnderwaterEyePosition = mgl32.Vec3{4.5, 1.2, -4.5}

// captureUnderwaterOxygen 是 water-underwater 场景注入的权威氧气值。
// 取满值（core.MaxOxygenTicks = 300）的一半上下，氧气条因此以半满出现——
// 满值时 HUD 按设计不画氧气条，端点画错了看不出来。
const captureUnderwaterOxygen = 160

// applyWaterUnderwaterCaptureState 把相机与权威玩家状态一起放进水里。
//
// 水下视觉读的是 Predictor.EyeInFluid()，即权威/预测共用的那一个浸没标志，
// 不是相机坐标——规格要求视觉与溺水判定 MUST NOT 存在第二套判定。所以这里
// 必须注入一条权威 PlayerState 让预测器按镜像重算标志，只摆相机是不够的。
//
// 注入走非 Reset 分支：Reset 会命中 Predictor.Begin，而 Begin 手上没有方块视图，
// 按其文档把标志置为 false，画面就会变成"人在水里但视觉没入水"。
func applyWaterUnderwaterCaptureState(app *application) error {
	if err := resetCapturePresentation(app); err != nil {
		return err
	}
	app.worldTimeTicks = 6000
	// ServerTick 取一个远大于抓帧期间真实 tick 的常量：ApplyPlayerState 有
	// 单调校验，小于等于已收到的 tick 会被静默忽略，而真实 tick 取决于加载
	// 花了多久，不能拿来做常量。
	if _, err := app.predictor.ApplyPlayerState(network.PlayerState{
		ServerTick:     1 << 20,
		Dimension:      core.Overworld,
		Position:       captureUnderwaterEyePosition,
		Yaw:            0,
		Pitch:          -0.05,
		Ready:          true,
		Health:         core.MaxHealth,
		Oxygen:         captureUnderwaterOxygen,
		WorldTimeTicks: 6000,
	}, client.MirrorCollisionSource{
		Mirror:    app.mirror,
		Dimension: core.Overworld,
	}); err != nil {
		return fmt.Errorf("注入水下权威玩家状态: %w", err)
	}
	if !app.predictor.EyeInFluid() {
		return fmt.Errorf(
			"water-underwater 的相机没有落在水里：位置 %v 处 EyeInFluid=false",
			captureUnderwaterEyePosition)
	}
	app.camera.Pos = captureUnderwaterEyePosition.Add(
		mgl32.Vec3{0, physics.ActiveTunables().EyeHeight, 0})
	app.camera.Yaw = 0
	app.camera.Pitch = -0.05
	app.center = cameraChunk(app.camera.Pos)
	return nil
}
