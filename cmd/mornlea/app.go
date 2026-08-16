//go:build darwin

package main

import (
	"context"
	"sync"
	"time"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/config"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/lod"
	"github.com/channing771/mornlea/internal/mesh"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/render"
	"github.com/channing771/mornlea/internal/render/hud"
	"github.com/channing771/mornlea/internal/server"
)

const (
	maxFrameAvatars  = 11
	maxFrameNameTags = 12
)

type applicationOptions struct {
	Companions []companion.Definition
	// AIModel 是从配置 ai 组解析出的模型运行时设置；AI 关闭（无伙伴）时为
	// 零值，本地权威服务端据此保持 M5B 的启动边界（见 server.NewHost）。
	AIModel companion.ModelSettings
	// AIAPIKey 是按 AIModel.APIKeyEnv 从环境变量解析出的密钥值。它只在
	// 进程内存中向本地 server.Config 传递，不写日志、不落配置文件与存档。
	AIAPIKey           string
	Seed               int64
	Benchmark          bool
	BenchmarkTransport string
	WorldPath          string
	Connect            string
	Identity           *network.Identity
	// CaptureDir 非空时进入视觉抓帧模式：走无头设备，按固定场景抓帧写 PNG。
	CaptureDir string
	// Dev 为真时启用调试面板（F3 切换）；不影响配置文件是否生效。
	Dev bool
	// Render 是渲染相关的生效配置（视距、FOV、鼠标灵敏度），由 cmd/mornlea 从
	// 加载后的 config.Config 下传并自行消费——config.Config.Apply 不处理它。
	Render config.Render
	// ConfigPath 是调试面板 F5 保存时的目标路径；只在 Dev 为真时使用。
	ConfigPath string
	// FluidEnabled 是配置 fluidEnabled 的生效值，下传给本地权威世界的
	// worldgen.New 门控海平面注水。远程连接模式下不使用它——世界内容由
	// 服务端权威决定。
	FluidEnabled bool
}

type application struct {
	window applicationWindow
	// renderer 是 Rust 渲染器句柄:窗口模式呈现到 surface,无头模式离屏。
	renderer *client.Renderer
	// scheduler 承接 mesh 上传调度(pending/预算/近距优先),下沉给 renderer。
	scheduler   *render.SectionScheduler
	frameWidth  int
	frameHeight int
	// lodScheduler 是远环 LOD 壳的生成/上传调度器;lodEnabled=false 或
	// benchmark 观察者路径下保持 nil(零参与:不建、不消费种子、帧循环只做
	// nil 检查)。生命周期随 application,Close 时先于渲染器释放。
	lodScheduler *lod.Scheduler
	// lodTileCenter 是最近一次已播种/增量入队的 tile 中心,用于跨 tile 边界
	// 检测;初始值在 attachLodScheduler 全环播种时写入。
	lodTileCenter lod.TilePos
	// visibleScratch/visibleSections 是每帧可见性计算的复用缓冲。
	visibleScratch  mesh.VisibilityScratch
	visibleSections []core.SectionPos
	rustVisible     [][3]int32
	avatarStream    []byte
	dropStream      []byte
	outlineStream   []byte
	billboardBytes  []byte
	entityEncoder   render.InstanceEncoder
	lastFrameStats  render.FrameStats
	remotePlayers   *client.RemotePlayers
	companions      *client.Companions
	chatEvents      *client.ChatEvents
	chatInput       chatInput
	// chatEventBuffer 是 refreshChatLines 的复用缓冲，容量与 client.ChatEventCapacity
	// 同源（E9/C9）：事件环最多回放 32 条，缓冲按同一常量分配保证零扩容刷新。
	chatEventBuffer [client.ChatEventCapacity]network.ChatEvent
	// chatLines 的 6 是 HUD 聊天显示行数：openspec companion-client-presentation
	// 规格「HUD 显示最近最多 6 条」，与 internal/render/hud/chat.go 的 maxChatLines 同值。
	chatLines              [6]string
	chatLineCount          int
	formattedChatEventID   uint64
	remotePresentations    []client.RemotePresentation
	companionPresentations []client.CompanionPresentation
	remoteAvatars          []render.Avatar
	remoteNameTags         []render.NameTag
	nameTagRenderer        *render.NameTagRenderer
	hotbarRenderer         *hud.HotbarRenderer
	damageFeedback         damageFeedback
	damageStrength         float32
	debugPanelRenderer     *render.DebugPanelRenderer
	// panel 是调试面板的交互状态；只在 applicationOptions.Dev 为真时创建，
	// 与 debugPanelRenderer 一同保持 nil/非 nil 同步。
	panel *panelState
	// configPath 是调试面板 F5 保存时的目标路径，来自 applicationOptions.ConfigPath。
	configPath string
	// panelLastFrameAt 是上一帧调试面板读数的采样时刻，用于计算 PanelReadout.FrameMillis。
	panelLastFrameAt  time.Time
	inventory         client.InventoryMirror
	furnace           client.FurnaceMirror
	chest             client.ChestMirror
	miningOverlay     hud.MiningOverlay
	itemDrops         *client.ItemDrops
	itemDropInstances []render.ItemDrop
	inventoryOpen     bool
	inventorySource   int
	serverTick        uint64
	// worldTimeTicks 是最后确认的权威绝对世界时间，只在接受更新状态时前进。
	worldTimeTicks          uint64
	glyphAtlas              *render.GlyphAtlas
	clientEndpoint          network.ClientEndpoint
	receiver                *client.Receiver
	server                  *server.Server
	host                    applicationHost
	serverCancel            context.CancelFunc
	serverDone              chan error
	mirror                  *client.Mirror
	predictor               *client.Predictor
	mesher                  *client.Mesher
	camera                  client.Camera
	center                  core.ChunkPos
	sequence                uint64
	loadedChunks            map[core.ChunkPos]struct{}
	ticks                   *tickRecorder
	saves                   *saveRecorder
	observerFloor           uint64
	benchmarkTransport      string
	multiplayerRenderTiming *multiplayerRenderTiming
	multiplayerRenderNow    func() time.Time
	closeOnce               sync.Once
	closeErr                error
	clientCloseOnce         sync.Once
	clientCloseErr          error
	clientSessionClosed     bool
	blockTargetReset        bool
	releaseResources        func()
	// render 是渲染相关的生效配置快照，在构造时从 applicationOptions.Render 复制，
	// 供渲染热路径（DropOutside 视距、鼠标灵敏度等）读取，不随配置文件热更新。
	render config.Render
}

type applicationWindow interface {
	SetCursorCaptured(bool)
	CursorPos() (float64, float64)
	ShouldClose() bool
	Poll()
	DrainTextInput([]rune) ([]rune, bool)
	KeyDown(client.Key) bool
	PrimaryButtonDown() bool
	SecondaryButtonDown() bool
	CursorCaptured() bool
	FramebufferSize() (int, int)
	ContentSize() (int, int)
	SetContentSize(int, int)
	CancelClose()
	Close()
}

type applicationHost interface {
	Run(context.Context, network.Listener) error
	AcceptStream(context.Context, network.ServerPacketStream) error
	Shutdown(context.Context) error
}
