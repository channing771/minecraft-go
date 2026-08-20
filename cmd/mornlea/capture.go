package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/physics"
)

// captureWidth/captureHeight 是视觉场景的固定分辨率。
// 刻意远小于 benchmark 的 2560×1440：golden 图要长期入库并反复更新，
// 全尺寸会让仓库历史迅速膨胀，而 360p 足以暴露本设施要抓的问题类别。
const (
	captureWidth  = 640
	captureHeight = 360
)

// captureDrainMax 是抓帧期间每帧处理的服务端消息上限，取值与 benchmark 一致。
const captureDrainMax = benchmarkMessageDrainMax

// captureGlyphSettleFrames 是 Apply 之后、真正回读之前额外渲染的帧数，
// 用来让字形图集的异步光栅化收敛。GlyphAtlas.Request 只把符文入队，
// 光栅化在后台 worker 完成，FlushUploads 每帧最多把一个结果搬上 GPU；
// 一个场景在 Apply 里第一次用到的文本（比如新出现的远端玩家昵称）如果
// 立刻回读，会读到 tofu 占位符而不是真正的字形。这里只重复渲染、不再
// drain——不会让服务端消息覆盖 Apply 设的常量——把收敛让给 worker。
// ponytail: 32 帧是 4 倍余量（每帧搬一个字形，8 个字形需要 8 帧）。
// 若后续昵称更长或多个名牌同时出现（参考 maxNameTagGlyphs），可能不够。
// 升级路径：轮询 GlyphAtlas 直到收敛，需要给它加一个导出的自省方法。
const captureGlyphSettleFrames = 32

var captureSettleTimeout = 5 * time.Minute

// captureScene 是一个视觉场景。三要素缺一不可：确定性的世界状态由固定种子、
// waitUntilLoaded 与可选 Prepare 保证，固定的相机位姿与其余呈现状态由 Apply
// 设置，抓帧时机由 WarmupFrames 和收敛判据固定。任何一项随环境变化，产出的图
// 就不可比对。
//
// 潜在陷阱：app.go 把 a.serverTick 传给 itemDropRenderer.Render 作掉落物动画相位，
// 而 a.serverTick 的取值依赖 waitUntilLoaded 花了多少个权威 tick 才收敛，
// 这本身取决于机器速度——不是本文件描述的三个常量之一。当前所有场景都不含
// 掉落物，因此无害；但第一个引入掉落物的场景会让基线的动画相位依赖机器速度，
// 到时需要额外想办法钉死或忽略这一相位。
type captureScene struct {
	Name string
	// WarmupFrames 是 Apply 之前空跑的帧数，用来让上传预算与网格化收敛。
	WarmupFrames int
	// Prepare 在权威消息完成最后一次 drain 后装入固定镜像夹具。
	Prepare func(*application) error
	// Apply 在最后一帧渲染前执行，是场景对呈现状态的全部干预。
	// 它跑在 drainServerMessages 之后，因此设置的值不会被当帧的服务端消息覆盖。
	Apply func(*application) error
	// PinVolatile 可选，在字形收敛帧之后、最后一帧渲染之前执行，用来钉住那些
	// 随机器速度变化、因而不属于场景三要素的量。
	//
	// 存在的理由：Apply 跑在收敛帧之前，而收敛帧会推进帧间隔与权威 tick，
	// 在 Apply 里设的值到最后一帧已经被覆盖。目前只有调试面板需要它——它的
	// 读数区直接显示帧时与 tick，这两者在同一台机器上重复抓帧也会变。
	PinVolatile func(*application) error
}

func captureSettled(stats client.MesherStats, pending int) bool {
	return stats.DirtySections == 0 && stats.QueuedJobs == 0 &&
		stats.InFlightJobs == 0 && stats.ReadyResults == 0 && pending == 0
}

// captureScenes 是表驱动的场景清单，新增场景即新增一行。
//
// 全部场景共用同一个 application，按本列表的顺序依次执行——两个场景之间不会
// 重置任何呈现状态。因此每个 Apply 都必须显式设定自己渲染依赖的全部字段，
// 不能依赖"没设置就是零值"，否则该场景的画面会悄悄继承前一个场景留下的状态，
// 而这份继承关系不会出现在任何一个场景自己的代码里——重排本列表、删掉某个
// 场景、或在两者之间插入新场景，都会静默改变后续场景的期望像素。新增场景应
// 追加在列表末尾；若确实需要调整顺序或插入位置，须用 --update-golden 重新
// 生成所有受影响场景的基线，并逐张人眼确认。
var captureScenes = []captureScene{
	{
		Name:         "terrain-noon",
		WarmupFrames: 8,
		Apply: func(app *application) error {
			// 6000 tick 是正午，日光与太阳高度都取到最大值，
			// 是昼夜管线上最容易看出偏差的相位。
			app.worldTimeTicks = 6000
			// 登录首条权威 PlayerState 必然触发 ResetView，把 Yaw/Pitch 覆盖成
			// 服务端下发的出生朝向——那不是本场景声明的常量。这里显式钉死，
			// 避免相机姿态随出生朝向漂移；Pitch 取小幅度下俯以避免画面被天空占满。
			app.camera.Yaw = 0
			app.camera.Pitch = -0.25
			return nil
		},
	},
	// ponytail: 生命值只覆盖满血 20。部分心形需要注入 PlayerState，
	// 而 Predictor.ApplyPlayerState 带 ServerTick 单调校验与位置和解，
	// 从抓帧钩子注入会牵动相机。要覆盖需要先给 Predictor 加一个
	// 只改生命值的测试入口，或让抓帧场景能脚本化地驱动服务端造成伤害。
	{
		Name:         "hud-hotbar-health",
		WarmupFrames: 8,
		Apply: func(app *application) error {
			app.worldTimeTicks = 6000
			// 与 terrain-noon 一样显式钉死相机姿态：登录首条权威 PlayerState 的
			// ResetView 会把 Yaw/Pitch 覆盖成出生朝向，不显式设置就不是常量。
			app.camera.Yaw = 0
			app.camera.Pitch = -0.25
			// 走 InventoryMirror.Apply 而不是直接改内部字段：它会执行
			// Inventory.Valid() 校验，因此这份构造数据同时也是一条格式自检。
			inventory := core.Inventory{}
			inventory.Hotbar.Selected = 2
			inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 64}
			inventory.Hotbar.Slots[1] = core.ItemStack{Item: core.ItemStoneBrick, Count: 7}
			// 耐久 40/131 让磨损条画在偏左位置——满耐久和空耐久都是端点，
			// 端点画错了不容易看出来。
			inventory.Hotbar.Slots[2] = core.ItemStack{
				Item: core.ItemStonePickaxe, Count: 1, Durability: 40,
			}
			inventory.Hotbar.Slots[3] = core.ItemStack{
				Item: core.ItemIronPickaxe, Count: 1, Durability: 250,
			}
			inventory.Backpack[0] = core.ItemStack{Item: core.ItemCoal, Count: 12}
			return app.inventory.Apply(network.InventoryState{Inventory: inventory})
		},
	},
	{
		Name:         "avatar-nametag",
		WarmupFrames: 8,
		Apply: func(app *application) error {
			app.worldTimeTicks = 6000
			app.camera.Yaw = 0
			app.camera.Pitch = -0.25
			// 本场景不关心物品栏，但前一个场景（hud-hotbar-health）会把石镐、
			// 铁镐等物品状态留在 app.inventory 里——这些场景共用同一个
			// application，不显式清空就会被悄悄继承。这里显式设成空物品栏，
			// 让本场景的画面只由自己的 Apply 决定，不依赖场景表的执行顺序。
			if err := app.inventory.Apply(network.InventoryState{Inventory: core.Inventory{}}); err != nil {
				return fmt.Errorf("重置物品栏: %w", err)
			}
			if app.remotePlayers == nil {
				return fmt.Errorf("avatar-nametag 需要远端玩家追踪器，当前为 nil")
			}
			// 昵称刻意混用 ASCII 与非 ASCII：字形 atlas 的分支在这两类上不同，
			// 只用 ASCII 会漏掉整条宽字符路径。
			spawn := network.RemotePlayerSpawn{
				// PlayerID{1} 不是合法 UUIDv4（第 6 字节高 4 位须为 4，第 8 字节
				// 高 2 位须为 10），applySpawn 会拒绝；这里改用与仓库测试同款的
				// 合法 UUIDv4 形状占位符。
				PlayerID:    core.PlayerID{6: 0x40, 8: 0x80, 15: 1},
				DisplayName: "测试Player",
				ServerTick:  1,
				Position:    app.camera.Pos.Add(mgl32.Vec3{0, 0, -6}),
			}
			return app.remotePlayers.Apply(spawn)
		},
	},
	{
		Name:         "inventory-crafting",
		WarmupFrames: 8,
		Apply: func(app *application) error {
			app.worldTimeTicks = 6000
			app.camera.Yaw = 0
			app.camera.Pitch = -0.25
			app.remotePlayers.Reset()
			app.furnace.Reset()
			app.chest.Reset()
			app.inventoryOpen = true
			app.inventorySource = 12

			inventory := core.Inventory{}
			inventory.Hotbar.Selected = 1
			inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 64}
			inventory.Hotbar.Slots[1] = core.ItemStack{Item: core.ItemGrass, Count: 32}
			inventory.Hotbar.Slots[2] = core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: 40}
			inventory.Hotbar.Slots[3] = core.ItemStack{Item: core.ItemFurnace, Count: 2}
			inventory.Hotbar.Slots[4] = core.ItemStack{Item: core.ItemChest, Count: 1}
			inventory.Backpack[0] = core.ItemStack{Item: core.ItemDirt, Count: 48}
			inventory.Backpack[1] = core.ItemStack{Item: core.ItemStoneBrick, Count: 16}
			inventory.Backpack[2] = core.ItemStack{Item: core.ItemCoal, Count: 12}
			inventory.Backpack[3] = core.ItemStack{Item: core.ItemRawIron, Count: 8}
			inventory.Backpack[4] = core.ItemStack{Item: core.ItemIronIngot, Count: 9}
			inventory.Backpack[5] = core.ItemStack{Item: core.ItemOakLog, Count: 1}
			inventory.Backpack[6] = core.ItemStack{Item: core.ItemGlass, Count: 4}
			inventory.Backpack[9] = core.ItemStack{Item: core.ItemIronBlock, Count: 1}
			return app.inventory.Apply(network.InventoryState{Inventory: inventory})
		},
	},
	{
		// 调试面板的视觉布局（行距、标签列宽、段头分组、只读行暗色）此前没有
		// 任何自动化覆盖，只能靠人眼。字形 UV 缺陷正是在这里被发现的——面板是
		// 全项目唯一大量绘制拉丁文本的界面，窄字符丢失在它身上最明显。
		Name:         "debug-panel",
		WarmupFrames: 8,
		Apply: func(app *application) error {
			app.worldTimeTicks = 6000
			app.camera.Yaw = 0
			app.camera.Pitch = -0.25
			// 与其余场景一样显式清空上一个场景留下的呈现状态：本列表共用同一个
			// application，不显式设置就会静默继承 inventory-crafting 的背包与容器。
			app.remotePlayers.Reset()
			app.furnace.Reset()
			app.chest.Reset()
			app.inventoryOpen = false
			if err := app.inventory.Apply(
				network.InventoryState{Inventory: core.Inventory{}},
			); err != nil {
				return fmt.Errorf("重置物品栏: %w", err)
			}
			if app.panel == nil {
				return fmt.Errorf("debug-panel 需要面板状态，当前为 nil")
			}
			app.panel.visible = true
			return nil
		},
		PinVolatile: func(app *application) error {
			// 面板读数区直接显示帧时与权威 tick，两者都随机器速度变化：
			// 同机重复抓帧实测 tick 在 412..416 之间、帧时在 3.3..4.3ms 之间，
			// 足以让基线比对超出阈值。
			//
			// panelLastFrameAt 清零后，下一帧的帧时按 panelFrameInput 的定义
			// 保持 0，显示为固定的 "0.00 ms"；serverTick 钉成常量。
			app.panelLastFrameAt = time.Time{}
			app.serverTick = capturePinnedServerTick
			return nil
		},
	},
	{
		Name:         "skylight-tunnel",
		WarmupFrames: 8,
		Prepare:      prepareSkylightTunnel,
		Apply: func(app *application) error {
			app.worldTimeTicks = 6000
			app.camera.Pos = mgl32.Vec3{0.5, 2.8, 8.5}
			app.camera.Yaw = 0
			app.camera.Pitch = -0.04
			app.inventoryOpen = false
			if app.panel != nil {
				app.panel.visible = false
			}
			if err := app.inventory.Apply(network.InventoryState{Inventory: core.Inventory{}}); err != nil {
				return fmt.Errorf("重置物品栏: %w", err)
			}
			if app.remotePlayers == nil {
				return fmt.Errorf("skylight-tunnel 需要远端玩家追踪器，当前为 nil")
			}
			// 用快照逐一走合法 despawn，空列表自然成功，也不会遗漏其他玩家。
			for _, player := range app.remotePlayers.Presentations() {
				if err := app.remotePlayers.Apply(network.RemotePlayerDespawn{
					PlayerID: player.PlayerID,
				}); err != nil {
					return fmt.Errorf("清除远端玩家 %s: %w", player.PlayerID, err)
				}
			}
			return nil
		},
	},
	{
		Name:         "block-light-room",
		WarmupFrames: 8,
		Prepare:      prepareBlockLightRoom,
		Apply: func(app *application) error {
			app.worldTimeTicks = 18000
			app.camera.Pos = mgl32.Vec3{0.5, 2.8, 0.5}
			app.camera.Yaw = 0
			app.camera.Pitch = 0
			app.inventoryOpen = false
			if app.panel != nil {
				app.panel.visible = false
			}
			if err := app.inventory.Apply(network.InventoryState{Inventory: core.Inventory{}}); err != nil {
				return fmt.Errorf("重置物品栏: %w", err)
			}
			app.remotePlayers.Reset()
			app.furnace.Reset()
			app.chest.Reset()
			return nil
		},
	},
	{
		Name:         "materials-showcase",
		WarmupFrames: 8,
		Prepare:      prepareMaterialsShowcase,
		Apply: func(app *application) error {
			app.worldTimeTicks = 6000
			app.camera.Pos = mgl32.Vec3{0.5, 5.8, 13.5}
			app.camera.Yaw = 0
			app.camera.Pitch = -0.12
			app.inventoryOpen = false
			app.remotePlayers.Reset()
			app.furnace.Reset()
			app.chest.Reset()
			if app.panel != nil {
				app.panel.visible = false
			}
			return app.inventory.Apply(network.InventoryState{Inventory: core.Inventory{}})
		},
	},
	{
		Name:         "target-block-feedback",
		WarmupFrames: 8,
		Prepare:      prepareTargetBlockFeedback,
		Apply: func(app *application) error {
			app.worldTimeTicks = 6000
			app.camera.Pos = mgl32.Vec3{0.5, 3.5, 2.5}
			app.camera.Yaw, app.camera.Pitch = 0, 0
			app.inventoryOpen = false
			app.inventorySource = -1
			app.remotePlayers.Reset()
			app.furnace.Reset()
			app.chest.Reset()
			if app.panel != nil {
				app.panel.visible = false
			}
			return app.inventory.Apply(network.InventoryState{Inventory: core.Inventory{}})
		},
	},
	{
		Name:         "oak-grove",
		WarmupFrames: 8,
		Prepare:      prepareOakGrove,
		Apply:        applyOakGroveCaptureState,
	},
	{
		Name:         "ai-companion",
		WarmupFrames: 8,
		Prepare:      prepareAICompanion,
		Apply:        applyAICompanionCaptureState,
	},
	{
		// 水景一：水面之上俯瞰。覆盖「水面斜坡」与「水面之下的地形」——
		// 顶层水沿 −Z 由源方块递减到 7 级，角高度插值成连续斜面；
		// 池底的沙丘、砾石带与露出水面的圆石堆透过水面可见。
		Name:         "water-surface-slope",
		WarmupFrames: 8,
		Prepare:      prepareWaterBasin,
		Apply: func(app *application) error {
			if err := resetCapturePresentation(app); err != nil {
				return err
			}
			app.worldTimeTicks = 6000
			// 3/4 角俯视：斜水面的梯度沿 −Z，从侧后方看过去，水面高度差
			// 在画面里是一条明显倾斜的水线，正视（yaw=0）反而只能看到它退远。
			app.camera.Pos = mgl32.Vec3{7.5, 6.4, 4.5}
			app.camera.Yaw = 0.6
			app.camera.Pitch = -0.3
			app.center = cameraChunk(app.camera.Pos)
			return nil
		},
	},
	{
		// 水景二：水下视角。覆盖「水下视角」——相机与权威位置一起放进水体，
		// 眼睛浸没标志因此为真，画面同时呈现水色叠加、被压低的可见半径、
		// 天空光穿水衰减，以及未满的氧气条。
		//
		// Prepare 重新装一遍夹具而不是继承上一个场景：镜像的 ChunkSnapshot 会把
		// 区块 revision 复位，因此重装是幂等的，本场景的画面不依赖场景表的顺序。
		//
		// **本场景 MUST 排在场景表最后**，由 TestWaterUnderwaterCaptureSceneIsLast
		// 兜底。它注入的权威 PlayerState 带一个远大于真实值的 ServerTick，此后
		// 一切真实 PlayerState 都会被预测器的单调校验静默忽略，浸没标志因此永久
		// 停在"在水里"；排在它后面的任何场景都会带着水色叠加与被压低的可见半径
		// 出图。这不是假设——把它插在 ai-companion 之前实测过一次，ai-companion
		// 的画面 98.75% 的像素随之改变。
		Name:         "water-underwater",
		WarmupFrames: 8,
		Prepare:      prepareWaterBasin,
		Apply:        applyWaterUnderwaterCaptureState,
	},
}

// capturePinnedServerTick 是 debug-panel 场景钉死的权威 tick 值。
// 取一个与真实加载时长无关的常量即可，数值本身没有语义。
const capturePinnedServerTick = 400

// runCapture 依次跑完全部视觉场景。updateGolden 为真时把抓到的图写进 golden 基线；
// 为假时与已有基线比对，超阈值的场景把实拍图与差异图写进 dir 并返回错误。
func runCapture(app *application, dir string, updateGolden bool) error {
	if width, height := app.framebufferSize(); width != captureWidth || height != captureHeight {
		return fmt.Errorf("capture framebuffer=%dx%d，要求精确 %dx%d",
			width, height, captureWidth, captureHeight)
	}
	if app.window != nil {
		return fmt.Errorf("capture 需要无头 offscreen 渲染器,当前为窗口模式")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建抓帧输出目录 %s: %w", dir, err)
	}
	// 复用 benchmark 的加载等待：同样的视距、同样的收敛判据。
	// 抓帧不另设视距，否则图里所见与真实客户端所见就会分歧，golden 随之失去意义。
	if _, err := waitUntilLoaded(app, 5*time.Minute); err != nil {
		return fmt.Errorf("固定场景加载: %w", err)
	}
	// 用 errors.Join 累积每个场景的错误并跑完全部场景，而不是遇错即停：
	// 着色器或格式类改动通常会让多个场景同时变红，只看到第一个红的场景
	// 会漏掉其余场景的信息，也漏跑它们各自的图像产出。
	var errs []error
	for _, scene := range captureScenes {
		if err := captureOne(app, dir, scene, updateGolden); err != nil {
			errs = append(errs, fmt.Errorf("场景 %s: %w", scene.Name, err))
		}
	}
	return errors.Join(errs...)
}

func captureOne(app *application, dir string, scene captureScene, updateGolden bool) error {
	for i := 0; i < scene.WarmupFrames; i++ {
		if _, err := app.frame(captureDrainMax, captureDrainMax, physics.FixedDelta); err != nil {
			return fmt.Errorf("预热第 %d 帧: %w", i, err)
		}
	}
	// 最后一帧手工拆开 frame()：先收消息，再装入夹具并覆盖呈现状态，最后渲染。
	// 顺序不能变；从 Prepare 开始不再 drain，固定夹具不会被权威消息覆盖。
	app.drainServerMessages(captureDrainMax)
	if scene.Prepare != nil {
		if err := scene.Prepare(app); err != nil {
			return fmt.Errorf("准备场景夹具: %w", err)
		}
	}
	if err := scene.Apply(app); err != nil {
		return fmt.Errorf("应用场景状态: %w", err)
	}
	settleDeadline := time.Now().Add(captureSettleTimeout)
	for i := 0; ; i++ {
		if _, err := app.renderFrame(captureDrainMax); err != nil {
			return fmt.Errorf("场景收敛第 %d 帧: %w", i, err)
		}
		stats, pending := app.mesher.Stats(), app.scheduler.PendingUploads()
		if i+1 >= captureGlyphSettleFrames && captureSettled(stats, pending) {
			break
		}
		if time.Now().After(settleDeadline) {
			return fmt.Errorf("场景 %s 在 %s 内未收敛：mesher=%+v pending=%d",
				scene.Name, captureSettleTimeout, stats, pending)
		}
	}
	// PinVolatile 必须在收敛帧之后、最后一帧之前：收敛帧本身会推进那些随机器
	// 速度变化的量（帧间隔、权威 tick），在 Apply 里钉死会被它们重新覆盖。
	if scene.PinVolatile != nil {
		if err := scene.PinVolatile(app); err != nil {
			return fmt.Errorf("钉住易变读数: %w", err)
		}
	}
	if _, err := app.renderFrame(captureDrainMax); err != nil {
		return fmt.Errorf("渲染抓帧: %w", err)
	}
	pixels := app.renderer.Readback()
	img := bgraToNRGBA(pixels, captureWidth, captureHeight)
	// 无条件把场景图写进 dir——不管比对通不通过、要不要更新基线。
	// spec 要求 dir 里必须为每个场景产出一份与场景名同名的图像文件；
	// 之前只在比对失败或更新基线时才写，比对通过的正常路径反而拿不到图看。
	if err := writePNG(filepath.Join(dir, scene.Name+".png"), img); err != nil {
		return fmt.Errorf("写出场景图 %s: %w", scene.Name, err)
	}
	if updateGolden {
		if err := os.MkdirAll(captureGoldenDir, 0o755); err != nil {
			return fmt.Errorf("创建 golden 基线目录 %s: %w", captureGoldenDir, err)
		}
		if err := writePNG(filepath.Join(captureGoldenDir, scene.Name+".png"), img); err != nil {
			return err
		}
		fmt.Printf("已抓取场景 %s（写入基线）\n", scene.Name)
		return nil
	}
	diff, err := compareAgainstGolden(captureGoldenDir, dir, scene.Name, img, captureThresholds)
	fmt.Printf("已抓取场景 %s: %s\n", scene.Name, diff)
	return err
}
