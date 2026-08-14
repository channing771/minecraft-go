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
	"github.com/channing771/mornlea/internal/gfx"
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
	Companions         []companion.Definition
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
}

type application struct {
	window                 applicationWindow
	dev                    gfx.Device
	surface                gfx.Surface
	color                  gfx.Texture
	colorView              gfx.TextureView
	frameWidth             int
	frameHeight            int
	renderer               *render.Renderer
	remotePlayers          *client.RemotePlayers
	companions             *client.Companions
	chatEvents             *client.ChatEvents
	chatInput              chatInput
	chatEventBuffer        [32]network.ChatEvent
	chatLines              [6]string
	chatLineCount          int
	formattedChatEventID   uint64
	remotePresentations    []client.RemotePresentation
	companionPresentations []client.CompanionPresentation
	remoteAvatars          []render.Avatar
	remoteNameTags         []render.NameTag
	avatarRenderer         *render.AvatarRenderer
	nameTagRenderer        *render.NameTagRenderer
	hotbarRenderer         *hud.HotbarRenderer
	damageOverlayRenderer  *render.DamageOverlayRenderer
	damageFeedback         damageFeedback
	damageStrength         float32
	debugPanelRenderer     *render.DebugPanelRenderer
	// panel 是调试面板的交互状态；只在 applicationOptions.Dev 为真时创建，
	// 与 debugPanelRenderer 一同保持 nil/非 nil 同步。
	panel *panelState
	// configPath 是调试面板 F5 保存时的目标路径，来自 applicationOptions.ConfigPath。
	configPath string
	// panelLastFrameAt 是上一帧调试面板读数的采样时刻，用于计算 PanelReadout.FrameMillis。
	panelLastFrameAt     time.Time
	inventory            client.InventoryMirror
	furnace              client.FurnaceMirror
	chest                client.ChestMirror
	miningOverlay        hud.MiningOverlay
	itemDropRenderer     *render.ItemDropRenderer
	blockOutlineRenderer *render.BlockOutlineRenderer
	itemDrops            *client.ItemDrops
	itemDropInstances    []render.ItemDrop
	inventoryOpen        bool
	inventorySource      int
	serverTick           uint64
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
	depth                   *depthTarget
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
	NativeHandle() gfx.NativeWindowHandle
	Close()
}

type applicationHost interface {
	Run(context.Context, network.Listener) error
	AcceptStream(context.Context, network.ServerPacketStream) error
	Shutdown(context.Context) error
}
