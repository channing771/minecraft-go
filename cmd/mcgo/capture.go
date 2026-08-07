package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/physics"
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
const captureGlyphSettleFrames = 32

// captureScene 是一个视觉场景。三要素缺一不可：确定性的世界状态由固定种子与
// waitUntilLoaded 保证，固定的相机位姿与其余呈现状态由 Apply 设置，
// 抓帧时机由 WarmupFrames 固定——三者都必须是常量，任何一项随环境变化，
// 产出的图就不可比对。
type captureScene struct {
	Name string
	// WarmupFrames 是 Apply 之前空跑的帧数，用来让上传预算与网格化收敛。
	WarmupFrames int
	// Apply 在最后一帧渲染前执行，是场景对呈现状态的全部干预。
	// 它跑在 drainServerMessages 之后，因此设置的值不会被当帧的服务端消息覆盖。
	Apply func(*application) error
}

// captureScenes 是表驱动的场景清单，新增场景即新增一行。
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
			if app.remotePlayers == nil {
				return fmt.Errorf("avatar-nametag 需要远端玩家追踪器，当前为 nil")
			}
			// 昵称刻意混用 ASCII 与非 ASCII：字形 atlas 的分支在这两类上不同，
			// 只用 ASCII 会漏掉整条宽字符路径。
			spawn := network.RemotePlayerSpawn{
				// PlayerID{1} 不是合法 UUIDv4（第 6 字节高 4 位须为 4，第 8 字节
				// 高 2 位须为 10），applySpawn 会拒绝；这里改用与仓库测试同款的
				// 合法 UUIDv4 形状占位符。
				PlayerID:    core.PlayerID{0, 0, 0, 0, 0, 0, 0x40, 0, 0x80, 0, 0, 0, 0, 0, 0, 1},
				DisplayName: "测试Player",
				ServerTick:  1,
				Position:    app.camera.Pos.Add(mgl32.Vec3{0, 0, -6}),
			}
			return app.remotePlayers.Apply(spawn)
		},
	},
}

// runCapture 依次跑完全部视觉场景，把每张图写成 <dir>/<name>.png。
func runCapture(app *application, dir string) error {
	if width, height := app.framebufferSize(); width != captureWidth || height != captureHeight {
		return fmt.Errorf("capture framebuffer=%dx%d，要求精确 %dx%d",
			width, height, captureWidth, captureHeight)
	}
	if app.color == nil {
		return fmt.Errorf("capture 需要无头 offscreen 颜色纹理，当前为 nil")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建抓帧输出目录 %s: %w", dir, err)
	}
	// 复用 benchmark 的加载等待：同样的视距、同样的收敛判据。
	// 抓帧不另设视距，否则图里所见与真实客户端所见就会分歧，golden 随之失去意义。
	if _, err := waitUntilLoaded(app, 5*time.Minute); err != nil {
		return fmt.Errorf("固定场景加载: %w", err)
	}
	for _, scene := range captureScenes {
		if err := captureOne(app, dir, scene); err != nil {
			return fmt.Errorf("场景 %s: %w", scene.Name, err)
		}
		fmt.Printf("已抓取场景 %s\n", scene.Name)
	}
	return nil
}

func captureOne(app *application, dir string, scene captureScene) error {
	for i := 0; i < scene.WarmupFrames; i++ {
		if _, err := app.frame(captureDrainMax, captureDrainMax, physics.FixedDelta); err != nil {
			return fmt.Errorf("预热第 %d 帧: %w", i, err)
		}
	}
	// 最后一帧手工拆开 frame()：先收消息，再让场景覆盖呈现状态，最后渲染。
	// 顺序不能变——Apply 若跑在 drain 之前，设置的值会被当帧的服务端消息覆盖。
	app.drainServerMessages(captureDrainMax)
	if err := scene.Apply(app); err != nil {
		return fmt.Errorf("应用场景状态: %w", err)
	}
	for i := 0; i < captureGlyphSettleFrames; i++ {
		if _, err := app.renderFrame(captureDrainMax); err != nil {
			return fmt.Errorf("字形收敛第 %d 帧: %w", i, err)
		}
	}
	if _, err := app.renderFrame(captureDrainMax); err != nil {
		return fmt.Errorf("渲染抓帧: %w", err)
	}
	pixels := app.color.ReadLayer(0, 0)
	img := bgraToNRGBA(pixels, captureWidth, captureHeight)
	return writePNG(filepath.Join(dir, scene.Name+".png"), img)
}

// bgraToNRGBA 把回读到的 BGRA8 像素转成 PNG 需要的 NRGBA。
// 纹理格式是 sRGB，字节本身已是 sRGB 编码，与 PNG 的约定一致，只需交换 B/R；
// alpha 恒定写 255——渲染目标的 alpha 通道从未被任何管线约定过，
// 直接透传会让 golden 图随无关的管线细节漂移。
func bgraToNRGBA(pixels []byte, width, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for i := 0; i < width*height; i++ {
		src, dst := i*4, i*4
		img.Pix[dst+0] = pixels[src+2]
		img.Pix[dst+1] = pixels[src+1]
		img.Pix[dst+2] = pixels[src+0]
		img.Pix[dst+3] = 255
	}
	return img
}

func writePNG(path string, img *image.NRGBA) (returnErr error) {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("创建 %s: %w", path, err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil && returnErr == nil {
			returnErr = fmt.Errorf("关闭 %s: %w", path, closeErr)
		}
	}()
	if err := png.Encode(file, img); err != nil {
		return fmt.Errorf("编码 %s: %w", path, err)
	}
	return nil
}
