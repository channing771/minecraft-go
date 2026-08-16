//go:build darwin

package main

import (
	"context"
	"errors"
	"math"
	"os"
	"regexp"
	"strconv"
	"testing"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/config"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/lod"
	"github.com/channing771/mornlea/internal/network"
)

// TestLodFogDistancesAnchors 锁住雾距离推导的 0.5/0.75 半径锚点(design
// 「Go 编排」裁决):farRadiusBlocks = lodFarMultiplier × viewDistance × 16,
// start = 0.5×far、full = 0.75×far。默认几何 (32,3) 必须精确落在 Rust
// 渲染器的编译期默认 768/1152 上;非默认倍率下外缘全雾仍成立(full 恒在
// far 内侧 25% 起雾)。
func TestLodFogDistancesAnchors(t *testing.T) {
	cases := []struct {
		name          string
		viewDistance  int
		farMultiplier int
		wantStart     float32
		wantFull      float32
	}{
		{"默认几何 32×3", 32, 3, 768, 1152},
		{"最大合法 64×8", 64, 8, 4096, 6144},
		{"最小合法 2×2", 2, 2, 32, 48},
		{"非整除倍率 63×2", 63, 2, 1008, 1512},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			start, full := lodFogDistances(testCase.viewDistance, testCase.farMultiplier)
			if start != testCase.wantStart {
				t.Fatalf("start = %v, want %v", start, testCase.wantStart)
			}
			if full != testCase.wantFull {
				t.Fatalf("full = %v, want %v", full, testCase.wantFull)
			}
			// Rust 入口契约:start>0 且 full>start(NaN 拒绝),违反会 panic。
			if !(start > 0 && full > start) {
				t.Fatalf("推导结果违反渲染器契约: start=%v full=%v", start, full)
			}
			far := float32(testCase.viewDistance) * float32(testCase.farMultiplier) * 16
			if start != far*0.5 || full != far*0.75 {
				t.Fatalf("锚点漂移: start=%v full=%v far=%v", start, full, far)
			}
		})
	}
}

// TestLodFarTileRadius 锁住远环 tile 半径推导:tile 半径 = ceil(
// multiplier × viewDistance / 4)(tile = 4×4 chunk,向上取整保证全雾距离
// 之外不露天空缝),并在极端输入下饱和钳制到 int32 范围而不是溢出回绕
// (控制器裁决 1,顺带消化任务 E 的评审②)。
func TestLodFarTileRadius(t *testing.T) {
	cases := []struct {
		name          string
		viewDistance  int
		farMultiplier int
		want          int
	}{
		{"默认几何 32×3", 32, 3, 24},
		{"最大合法 64×8", 64, 8, 128},
		{"整除 16×2", 16, 2, 8},
		{"非整除向上取整 63×2", 63, 2, 32},
		{"非整除向上取整 5×3", 5, 3, 4},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := lodFarTileRadius(testCase.viewDistance, testCase.farMultiplier); got != testCase.want {
				t.Fatalf("lodFarTileRadius(%d, %d) = %d, want %d",
					testCase.viewDistance, testCase.farMultiplier, got, testCase.want)
			}
		})
	}
	t.Run("极端输入饱和钳制", func(t *testing.T) {
		got := lodFarTileRadius(math.MaxInt32, math.MaxInt32)
		if got != math.MaxInt32 {
			t.Fatalf("极端输入半径 = %d, want 饱和到 math.MaxInt32", got)
		}
		if got < 0 {
			t.Fatalf("半径不得溢出为负: %d", got)
		}
	})
}

// TestLodNearTileRadius 锁住 Ruling 19 的内半径推导:inner =
// floor(viewDistance/4)+1,使壳的最小覆盖块 inner×64 ≥ 近 mesh 覆盖
// 半径 viewDistance×16(带与近 mesh 零重叠、无缝衔接的代数保证)。
func TestLodNearTileRadius(t *testing.T) {
	cases := []struct {
		viewDistance int
		want         int
	}{
		{32, 9},
		{2, 1},
		{64, 17},
		{5, 2},
		{63, 16},
	}
	for _, testCase := range cases {
		if got := lodNearTileRadius(testCase.viewDistance); got != testCase.want {
			t.Fatalf("lodNearTileRadius(%d) = %d, want %d",
				testCase.viewDistance, got, testCase.want)
		}
	}
	for viewDistance := 2; viewDistance <= 64; viewDistance++ {
		inner := lodNearTileRadius(viewDistance)
		if inner*64 < viewDistance*16 {
			t.Fatalf("viewDistance=%d 内半径 %d 的最小覆盖块 %d < 近 mesh 半径 %d(零重叠被破坏)",
				viewDistance, inner, inner*64, viewDistance*16)
		}
	}
	if got := lodNearTileRadius(0); got != 1 {
		t.Fatalf("防御分支 lodNearTileRadius(0) = %d, want 1", got)
	}
}

// TestLodTileFromChunkFloorSemantics 锁住 chunk→tile 换算的 floor 语义:
// tile 覆盖 chunk [tile×4, tile×4+4),负坐标向负无穷取整(算术右移),
// 西缘 block = tile_x×64。
func TestLodTileFromChunkFloorSemantics(t *testing.T) {
	cases := []struct {
		chunk core.ChunkPos
		want  lod.TilePos
	}{
		{core.ChunkPos{X: 0, Z: 0}, lod.TilePos{X: 0, Z: 0}},
		{core.ChunkPos{X: 3, Z: -1}, lod.TilePos{X: 0, Z: -1}},
		{core.ChunkPos{X: 4, Z: -4}, lod.TilePos{X: 1, Z: -1}},
		{core.ChunkPos{X: 7, Z: -5}, lod.TilePos{X: 1, Z: -2}},
		{core.ChunkPos{X: -1, Z: 8}, lod.TilePos{X: -1, Z: 2}},
	}
	for _, testCase := range cases {
		if got := lodTileFromChunk(testCase.chunk); got != testCase.want {
			t.Fatalf("lodTileFromChunk(%v) = %v, want %v", testCase.chunk, got, testCase.want)
		}
	}
}

// TestLodRingDomainWithinCapacityAtMaxLegalConfig 是 5.2 的显式验收
// (Ruling 18 第三点):按 config 最大合法值推导环形入队域规模,断言不触
// client 渲染器的 MAX_LOD_TILES 容量。容量数值在 Go 侧是镜像常量
// lodMaxTiles,用 Rust 源文件字面量同步断言锚定,防止双源漂移。
func TestLodRingDomainWithinCapacityAtMaxLegalConfig(t *testing.T) {
	// viewDistance 的合法上限从 config.Fields() 读取(单一权威),
	// lodFarMultiplier 的上限 8 与 config 的钳制区间锚定(见
	// internal/config 的 LodFarMultiplierMax)。
	viewDistanceMax := 0
	for _, field := range config.Fields() {
		if field.Group == "render" && field.Name == "viewDistance" {
			viewDistanceMax = int(field.Max)
		}
	}
	if viewDistanceMax != 64 {
		t.Fatalf("render.viewDistance 的 Fields 上限 = %d, want 64(改动需要同步 Ruling 18 容量推导)", viewDistanceMax)
	}
	const farMultiplierMax = config.LodFarMultiplierMax
	tileRadius := lodFarTileRadius(viewDistanceMax, farMultiplierMax)
	fullRing := lodRingTileCount(tileRadius)
	if tileRadius != 128 {
		t.Fatalf("最大合法配置 tile 半径 = %d, want 128(64×8/4)", tileRadius)
	}
	if fullRing != 66049 {
		t.Fatalf("最大合法配置全方环 = %d, want 66049((2×128+1)²)", fullRing)
	}
	if fullRing > lodMaxTiles {
		t.Fatalf("环形入队域 %d 超出 tile 表容量 %d,全方播种会触发 CAPACITY", fullRing, lodMaxTiles)
	}
}

// TestLodMaxTilesMirrorsRustConstant 是镜像常量的同步断言:Go 侧
// lodMaxTiles 必须与 Rust client 渲染器的 MAX_LOD_TILES 源字面量一致,
// 否则容量论证出现双源漂移(一侧改动另一侧不知情)。
func TestLodMaxTilesMirrorsRustConstant(t *testing.T) {
	source, err := os.ReadFile("../../engine/crates/mornlea_client/src/render/lod.rs")
	if err != nil {
		t.Fatalf("读取 Rust lod.rs: %v", err)
	}
	pattern := regexp.MustCompile(`pub const MAX_LOD_TILES: usize = (\d+);`)
	match := pattern.FindSubmatch(source)
	if match == nil {
		t.Fatalf("lod.rs 中未找到 MAX_LOD_TILES 常量声明")
	}
	rustValue, err := strconv.Atoi(string(match[1]))
	if err != nil {
		t.Fatalf("解析 Rust 容量字面量 %q: %v", match[1], err)
	}
	if rustValue != lodMaxTiles {
		t.Fatalf("Rust MAX_LOD_TILES = %d 与 Go 镜像 lodMaxTiles = %d 漂移", rustValue, lodMaxTiles)
	}
}

// TestLodWiringEnabled 锁住接线开关:lodEnabled=false 或 benchmark 观察者
// 路径都不建 Scheduler(设计「Go 编排」:禁用时零参与;benchmark 的
// viewDistance 路径无远环需求,5.4 再显式关闭)。
func TestLodWiringEnabled(t *testing.T) {
	defaults := config.Defaults().Render
	if !lodWiringEnabled(defaults, false) {
		t.Fatal("默认配置(启用 + 非 benchmark)必须接线远环")
	}
	disabled := defaults
	disabled.LodEnabled = false
	if lodWiringEnabled(disabled, false) {
		t.Fatal("lodEnabled=false 必须零参与")
	}
	if lodWiringEnabled(defaults, true) {
		t.Fatal("benchmark 观察者路径必须零参与")
	}
}

// newLodConnectionTestApplication 构造一个远端连接形态的完整 application:
// fake 登录返回固定世界种子,真实离屏渲染器(无 GPU 时跳过)。远端路径与
// 单机共用同一登录状态机,种子经 LoginSuccess 流入装配点。
func newLodConnectionTestApplication(
	t *testing.T,
	render config.Render,
	loginSeed uint64,
) *application {
	t.Helper()
	rawEndpoint, _ := network.NewMemoryPair(1)
	endpoint := &connectionTestEndpoint{ClientEndpoint: rawEndpoint}
	t.Cleanup(func() { _ = rawEndpoint.Close() })
	stream := &connectionTestClientStream{}
	window := &connectionTestWindow{}
	dependencies := connectionTestDependencies(t)
	dependencies.dialTCP = func(context.Context, string) (network.ClientPacketStream, error) {
		return stream, nil
	}
	dependencies.loginClient = func(
		context.Context, network.ClientPacketStream, network.Identity,
	) (network.ClientEndpoint, uint64, error) {
		return endpoint, loginSeed, nil
	}
	dependencies.newWindow = func(int, int, string) (applicationWindow, error) {
		return window, nil
	}
	dependencies.newWindowedRenderer = func(applicationWindow) (*client.Renderer, error) {
		renderer, err := client.NewRenderer(64, 64)
		if errors.Is(err, client.ErrNoGPUAdapter) {
			t.Skip("无 GPU 适配器")
		}
		return renderer, err
	}
	options := remoteConnectionOptions()
	options.Render = render
	app, err := newApplicationWithDependencies(options, dependencies)
	if err != nil {
		t.Fatalf("newApplication: %v", err)
	}
	return app
}

// TestApplicationLodWiringEnabledSeedsBandFromLoginSeed 验证启用路径的
// 登录种子→Scheduler 播种链路:登录成功取得 WorldSeed 后,装配点立即以
// 初始 tile 中心播种远环带(Ruling 19:默认几何 32×3 → 带 [9,24] →
// (2×24+1)²−(2×8+1)² = 2112 个 pending,近环内盘不入队),且推导后的
// 雾参数已下发给渲染器(非法推导会让 SetLodFog panic,构造即失败)。
func TestApplicationLodWiringEnabledSeedsBandFromLoginSeed(t *testing.T) {
	const loginSeed = uint64(0xC0FFEE)
	app := newLodConnectionTestApplication(t, config.Defaults().Render, loginSeed)
	defer func() {
		if err := app.Close(); err != nil {
			t.Fatalf("application Close: %v", err)
		}
	}()
	if app.lodScheduler == nil {
		t.Fatal("lodEnabled=true 时必须构造远环 Scheduler")
	}
	inner, outer := lodNearTileRadius(32), lodFarTileRadius(32, config.LodFarMultiplierDefault)
	if got, want := app.lodScheduler.PendingUploads(), lodBandTileCount(inner, outer); got != want {
		t.Fatalf("初始带状播种 pending = %d, want %d(带 [%d,%d])", got, want, inner, outer)
	}
	if app.lodTileCenter != (lod.TilePos{X: 0, Z: 0}) {
		t.Fatalf("初始 tile 中心 = %v, want (0,0)", app.lodTileCenter)
	}
	// 跑一帧证明接线后的帧循环可用(冲刷走既有预算,不阻塞)。
	if _, err := app.renderFrame(steadyFrameMeshWorkMax); err != nil {
		t.Fatalf("renderFrame: %v", err)
	}
}

// TestApplicationLodWiringNonDefaultGeometry 验证非默认倍率(32×8 的雾
// 推导 + 带 [9,64])也能构造成功——雾锚点 4096/6144 违约会让
// SetLodFog 在装配点 panic。
func TestApplicationLodWiringNonDefaultGeometry(t *testing.T) {
	render := config.Defaults().Render
	render.LodFarMultiplier = config.LodFarMultiplierMax
	app := newLodConnectionTestApplication(t, render, 42)
	defer func() {
		if err := app.Close(); err != nil {
			t.Fatalf("application Close: %v", err)
		}
	}()
	if app.lodScheduler == nil {
		t.Fatal("非默认倍率下启用路径必须构造远环 Scheduler")
	}
	inner, outer := lodNearTileRadius(render.ViewDistance), lodFarTileRadius(render.ViewDistance, render.LodFarMultiplier)
	if got, want := app.lodScheduler.PendingUploads(), lodBandTileCount(inner, outer); got != want {
		t.Fatalf("32×8 带状播种 pending = %d, want %d(带 [%d,%d])", got, want, inner, outer)
	}
}

// TestApplicationLodWiringDisabledZeroParticipation 验证禁用路径的零参与:
// 不建 Scheduler(种子不消费、渲染器远环 pass 空转即零成本),帧循环照常。
func TestApplicationLodWiringDisabledZeroParticipation(t *testing.T) {
	render := config.Defaults().Render
	render.LodEnabled = false
	app := newLodConnectionTestApplication(t, render, uint64(0xDEAD))
	defer func() {
		if err := app.Close(); err != nil {
			t.Fatalf("application Close: %v", err)
		}
	}()
	if app.lodScheduler != nil {
		t.Fatal("lodEnabled=false 时不得构造远环 Scheduler")
	}
	if _, err := app.renderFrame(steadyFrameMeshWorkMax); err != nil {
		t.Fatalf("禁用路径 renderFrame: %v", err)
	}
}
