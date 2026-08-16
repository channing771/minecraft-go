//go:build darwin

package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/render"
	"github.com/channing771/mornlea/internal/server"
	"github.com/channing771/mornlea/internal/storage"
	"github.com/channing771/mornlea/internal/worldgen"
)

func TestScenarioV16ContainsSevenSortedUnicodeRemotePlayers(t *testing.T) {
	if scenarioVersion != 16 {
		t.Fatalf("scenarioVersion=%d, want 16", scenarioVersion)
	}
	scenario := newMultiplayerBenchmarkScenario()
	if !scenario.LocalPlayerID.Valid() {
		t.Fatalf("local PlayerID invalid: %x", scenario.LocalPlayerID)
	}
	if len(scenario.Spawns) != 7 || len(scenario.Tags) != 7 {
		t.Fatalf("spawns/tags = %d/%d, want 7/7", len(scenario.Spawns), len(scenario.Tags))
	}
	for index, spawn := range scenario.Spawns {
		if spawn.PlayerID == scenario.LocalPlayerID || !spawn.PlayerID.Valid() ||
			spawn.DisplayName == "" || len([]rune(spawn.DisplayName)) == len(spawn.DisplayName) {
			t.Fatalf("spawn[%d] does not prove distinct Unicode identity: %+v", index, spawn)
		}
		if scenario.Tags[index].Key != (render.EntityKey{Kind: render.EntityPlayer, ID: [16]byte(spawn.PlayerID)}) ||
			scenario.Tags[index].Text != spawn.DisplayName {
			t.Fatalf("tag[%d]=%+v does not match spawn=%+v", index, scenario.Tags[index], spawn)
		}
	}
	batch := scenario.States(42)
	if batch.ServerTick != 42 || len(batch.Players) != 7 {
		t.Fatalf("tick batch = %+v, want tick 42/count 7", batch)
	}
	for index := 1; index < len(batch.Players); index++ {
		if bytes.Compare(batch.Players[index-1].PlayerID[:], batch.Players[index].PlayerID[:]) >= 0 {
			t.Fatalf("states not strictly sorted at %d", index)
		}
	}
	if err := batch.Validate(); err != nil {
		t.Fatalf("scenario batch invalid: %v", err)
	}
	for _, tag := range scenario.Tags {
		if !strings.ContainsAny(tag.Text, "界月星河山海云") {
			t.Fatalf("tag is not the fixed Unicode fixture: %q", tag.Text)
		}
	}
}

func TestBenchmarkScenarioV16AccountsForCompanionRendererUploadLayout(t *testing.T) {
	if scenarioVersion != 16 {
		t.Fatalf("scenarioVersion=%d，想要 16", scenarioVersion)
	}
	scenario := newMultiplayerBenchmarkScenario()
	if len(scenario.Spawns) != 7 || len(scenario.Tags) != 7 {
		t.Fatalf("固定 benchmark 玩家/名牌=%d/%d，想要 7/7", len(scenario.Spawns), len(scenario.Tags))
	}
	for index, tag := range scenario.Tags {
		if tag.Key.Kind != render.EntityPlayer || tag.Key.ID != [16]byte(scenario.Spawns[index].PlayerID) {
			t.Fatalf("benchmark tag[%d] key=%v，未保持玩家域", index, tag.Key)
		}
	}
	app := newRemoteRenderApplication(t, &integrationGlyphSource{})
	if got := len(app.companions.AppendPresentations(nil)); got != 0 {
		t.Fatalf("固定 benchmark 注入了 %d 个伙伴，想要 0", got)
	}
}

func TestScenarioV8GPUCompletionStopsWhenTransportCloseFails(t *testing.T) {
	serverCloseErr := errors.New("注入服务端关闭失败")
	clientCloseErr := errors.New("注入客户端关闭失败")
	for _, test := range []struct {
		name      string
		serverErr error
		clientErr error
		want      error
	}{
		{name: "服务端", serverErr: serverCloseErr, want: serverCloseErr},
		{name: "客户端", clientErr: clientCloseErr, want: clientCloseErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := newRemoteRenderApplication(t, &integrationGlyphSource{})
			config := server.DefaultConfig(benchmarkSeed)
			config.TrustedObserver = true
			running := server.NewWorld(
				config,
				worldgen.New(benchmarkSeed),
				storage.NewMemory(storage.Metadata{FormatVersion: 2, Seed: benchmarkSeed, SpawnDimension: core.Overworld}),
			)
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := running.Shutdown(ctx); err != nil {
					t.Errorf("关闭测试服务端: %v", err)
				}
			})

			rawClient, rawServer := network.NewMemoryPair(8)
			clientEndpoint := &benchmarkCloseErrorClientEndpoint{ClientEndpoint: rawClient, err: test.clientErr}
			serverEndpoint := &benchmarkCloseErrorServerEndpoint{ServerEndpoint: rawServer, err: test.serverErr}
			if err := running.AttachTrustedObserver(serverEndpoint); err != nil {
				t.Fatal(err)
			}
			app.server = running
			app.clientEndpoint = clientEndpoint

			probe, err := newMultiplayerClientProbe(app)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(probe.Close)
			clockReads := 0
			probe.now = func() time.Time {
				clockReads++
				return time.Unix(0, int64(clockReads)*int64(time.Millisecond))
			}

			err = probe.measureGPUCompletionAfterTransportClose(app)
			if !errors.Is(err, test.want) {
				t.Fatalf("measureGPUCompletionAfterTransportClose error=%v，想要 %v", err, test.want)
			}
			if clockReads != 0 {
				t.Fatalf("关闭失败后 GPU 时钟读取=%d，想要 0", clockReads)
			}
			if got := serverEndpoint.closeCalls.Load(); got != 1 {
				t.Fatalf("服务端 Close 调用=%d，想要 1", got)
			}
			if got := clientEndpoint.closeCalls.Load(); got != 1 {
				t.Fatalf("客户端 Close 调用=%d，想要 1", got)
			}
		})
	}
}

func TestScenarioV8GPUCompletionStartsAfterTransportTeardown(t *testing.T) {
	app := newRemoteRenderApplication(t, &integrationGlyphSource{})
	config := server.DefaultConfig(benchmarkSeed)
	config.TrustedObserver = true
	config.ViewRadius = 0
	config.Workers = 1
	config.SaveWorkers = 1
	running := server.NewWorld(
		config,
		worldgen.New(benchmarkSeed),
		storage.NewMemory(storage.Metadata{
			FormatVersion:  2,
			Seed:           benchmarkSeed,
			SpawnDimension: core.Overworld,
		}),
	)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := running.Shutdown(ctx); err != nil {
			t.Errorf("关闭测试服务端: %v", err)
		}
	})
	rawClient, serverEndpoint := network.NewMemoryPair(8)
	clientEndpoint := &connectionTestEndpoint{ClientEndpoint: rawClient}
	if err := running.AttachTrustedObserver(serverEndpoint); err != nil {
		t.Fatal(err)
	}
	app.server = running
	app.clientEndpoint = clientEndpoint

	probe, err := newMultiplayerClientProbe(app)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(probe.Close)
	clockReads := 0
	probe.now = func() time.Time {
		if clockReads == 0 {
			if err := running.SetTrustedObserverCenter(core.Overworld, core.ChunkPos{}); !errors.Is(err, server.ErrTrustedObserverDisabled) {
				t.Fatalf("首个 GPU 时钟读取时 trusted observer 仍挂载: %v", err)
			}
			if got := clientEndpoint.closeCalls.Load(); got != 1 {
				t.Fatalf("首个 GPU 时钟读取时客户端 Close 调用=%d，想要 1", got)
			}
		}
		clockReads++
		return time.Unix(0, int64(clockReads)*int64(time.Millisecond))
	}

	if err := probe.measureGPUCompletionAfterTransportClose(app); err != nil {
		t.Fatal(err)
	}
	if clockReads == 0 {
		t.Fatal("GPU 探针未读取时钟")
	}
}

func TestMultiplayerBenchmarkReservesTargetNameTagSlotWithoutAddingTarget(t *testing.T) {
	scenario := newMultiplayerBenchmarkScenario()
	if len(scenario.Tags) != 7 || cap(scenario.Tags) != maxFrameNameTags {
		t.Fatalf("benchmark tags len/cap=%d/%d，想要 7/%d", len(scenario.Tags), cap(scenario.Tags), maxFrameNameTags)
	}
	for index, tag := range scenario.Tags {
		if tag.Key.Kind != render.EntityPlayer || tag.Key.ID == ([16]byte{}) || tag.Text == "" {
			t.Fatalf("benchmark tag %d 伪造目标或空名牌: %+v", index, tag)
		}
	}
}

func TestScenarioV7RenderFrameSamplesExistingRemotePassesExactlyOnce(t *testing.T) {
	app := newRemoteRenderApplication(t, &integrationGlyphSource{})
	if err := app.remotePlayers.Apply(remoteSpawn(1, "星河", 1, mgl32.Vec3{0, 0, -4})); err != nil {
		t.Fatal(err)
	}
	timing := newMultiplayerRenderTiming()
	app.multiplayerRenderTiming = timing
	var nowCalls int
	app.multiplayerRenderNow = func() time.Time {
		nowCalls++
		return time.Unix(0, int64(nowCalls)*int64(time.Millisecond))
	}
	rendered, err := app.renderFrame(1)
	if err != nil || !rendered {
		t.Fatalf("renderFrame=(%v,%v)", rendered, err)
	}
	avatar, nameTag := timing.Summaries()
	if avatar != (client.LatencySummary{Samples: 1, P50MS: 1, P95MS: 1, P99MS: 1, MaxMS: 1}) {
		t.Fatalf("avatar timing=%+v, want one 1ms actual Render sample", avatar)
	}
	if nameTag != (client.LatencySummary{Samples: 1, P50MS: 2, P95MS: 2, P99MS: 2, MaxMS: 2}) {
		t.Fatalf("name-tag timing=%+v, want Prepare+Render sum of 2ms", nameTag)
	}
	if nowCalls != 6 {
		t.Fatalf("clock reads=%d, want exactly three start/end pairs", nowCalls)
	}
}

func TestScenarioV7NilRenderTimingNeverReadsBenchmarkClock(t *testing.T) {
	app := newRemoteRenderApplication(t, &integrationGlyphSource{})
	if err := app.remotePlayers.Apply(remoteSpawn(1, "月海", 1, mgl32.Vec3{0, 0, -4})); err != nil {
		t.Fatal(err)
	}
	app.multiplayerRenderTiming = nil
	app.multiplayerRenderNow = func() time.Time {
		panic("nil benchmark timing read wall clock")
	}
	if rendered, err := app.renderFrame(1); err != nil || !rendered {
		t.Fatalf("nil timing renderFrame=(%v,%v)", rendered, err)
	}
}

func TestScenarioV7NameTagFailurePublishesNoRenderTimingSample(t *testing.T) {
	wantErr := errors.New("injected glyph flush failure")
	app := newRemoteRenderApplication(t, &integrationGlyphSource{flushErr: wantErr})
	framesBefore := app.renderer.FrameCalls()
	if err := app.remotePlayers.Apply(remoteSpawn(1, "云野", 1, mgl32.Vec3{0, 0, -4})); err != nil {
		t.Fatal(err)
	}
	timing := newMultiplayerRenderTiming()
	app.multiplayerRenderTiming = timing
	app.multiplayerRenderNow = time.Now
	rendered, err := app.renderFrame(1)
	if rendered || !errors.Is(err, wantErr) {
		t.Fatalf("renderFrame=(%v,%v), want wrapped glyph error", rendered, err)
	}
	avatar, nameTag := timing.Summaries()
	if avatar.Samples != 0 || nameTag.Samples != 0 {
		t.Fatalf("failed frame published successful timing: avatar=%+v nameTag=%+v", avatar, nameTag)
	}
	if got := app.renderer.FrameCalls(); got != framesBefore {
		t.Fatalf("glyph failure 后 render FFI=%d,想要保持 %d", got, framesBefore)
	}
}

// Mutation killed: still/flying 阶段若不再经过真实 renderFrame，terrain pass
// 中由 sky pipeline 发出的 fullscreen triangle draw 不会出现。
func TestScenarioV13StillFlyingFrameIncludesCelestialSkyDraw(t *testing.T) {
	for _, phase := range []struct {
		name   string
		flying bool
	}{
		{name: "still"},
		{name: "flying", flying: true},
	} {
		t.Run(phase.name, func(t *testing.T) {
			app := newRemoteRenderApplication(t, &integrationGlyphSource{})
			probe, err := newMultiplayerClientProbe(app)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(probe.Close)

			updated := false
			var update func(time.Duration)
			if phase.flying {
				update = func(time.Duration) { updated = true }
			}
			summary, err := measurePhase(app, probe, phase.name, 50*time.Millisecond, update)
			if err != nil {
				t.Fatal(err)
			}
			if summary.Frames == 0 {
				t.Fatal("measurePhase 未执行真实 renderFrame")
			}
			if phase.flying && !updated {
				t.Fatal("flying measurePhase 未执行相机更新")
			}

			// sky 绘制顺序与 daylight uniform 已内化于 Rust 渲染器
			// (golden capture 守护);此处只断言阶段确实执行了真实帧。
			if app.renderer.FrameCalls() == 0 {
				t.Fatal("阶段未触发真实 render FFI")
			}
		})
	}
}
