//go:build darwin

package main

import (
	"fmt"
	"math"
	"runtime"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/gfx"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/render"
)

const (
	fixedBenchmarkFrameDuration = 50 * time.Millisecond
	benchmarkOutboxLimit        = 512
	benchmarkLatencyCapacity    = 131_072
)

type multiplayerRenderTiming struct {
	avatarSubmit  *client.LatencyRecorder
	nameTagSubmit *client.LatencyRecorder
}

func newMultiplayerRenderTiming() *multiplayerRenderTiming {
	return &multiplayerRenderTiming{
		avatarSubmit:  client.NewLatencyRecorder(benchmarkLatencyCapacity),
		nameTagSubmit: client.NewLatencyRecorder(benchmarkLatencyCapacity),
	}
}

func (timing *multiplayerRenderTiming) recordAvatar(duration time.Duration) {
	timing.avatarSubmit.Add(duration)
}

func (timing *multiplayerRenderTiming) recordNameTag(duration time.Duration) {
	timing.nameTagSubmit.Add(duration)
}

func (timing *multiplayerRenderTiming) Summaries() (client.LatencySummary, client.LatencySummary) {
	return timing.avatarSubmit.Summary(), timing.nameTagSubmit.Summary()
}

type multiplayerBenchmarkScenario struct {
	LocalPlayerID core.PlayerID
	Spawns        []network.RemotePlayerSpawn
	Tags          []render.NameTag
}

func newMultiplayerBenchmarkScenario() multiplayerBenchmarkScenario {
	local := benchmarkPlayerID(0)
	names := [...]string{"星野", "月河", "云山", "海界", "星河", "月海", "云野"}
	spawns := make([]network.RemotePlayerSpawn, len(names))
	tags := make([]render.NameTag, len(names), maxFrameNameTags)
	for index, name := range names {
		angle := float64(index) * 2 * math.Pi / float64(len(names))
		position := mgl32.Vec3{float32(math.Cos(angle)) * 4, 80, float32(math.Sin(angle))*4 - 8}
		playerID := benchmarkPlayerID(index + 1)
		spawns[index] = network.RemotePlayerSpawn{
			PlayerID: playerID, DisplayName: name, ServerTick: 1,
			Dimension: core.Overworld, Position: position,
		}
		tags[index] = render.NameTag{
			PlayerID: playerID, Text: name,
			Anchor: position.Add(mgl32.Vec3{0, 2.05, 0}),
		}
	}
	return multiplayerBenchmarkScenario{LocalPlayerID: local, Spawns: spawns, Tags: tags}
}

func (scenario multiplayerBenchmarkScenario) States(tick uint64) network.RemotePlayerStates {
	states := make([]network.RemotePlayerState, len(scenario.Spawns))
	for index, spawn := range scenario.Spawns {
		phase := float64(tick)*0.035 + float64(index)*2*math.Pi/float64(len(scenario.Spawns))
		position := spawn.Position.Add(mgl32.Vec3{
			float32(math.Sin(phase)) * 1.5,
			0,
			float32(math.Cos(phase)) * 1.5,
		})
		states[index] = network.RemotePlayerState{
			PlayerID: spawn.PlayerID, Dimension: core.Overworld, Position: position,
			Yaw:   float32(math.Atan2(math.Sin(phase), math.Cos(phase))),
			Pitch: float32(math.Sin(phase*0.5)) * 0.15,
		}
	}
	return network.RemotePlayerStates{ServerTick: tick, Players: states}
}

func benchmarkPlayerID(index int) core.PlayerID {
	return core.PlayerID{
		0x10, 0, 0, byte(index + 1), 0x20, 0x30, 0x40, byte(index + 1),
		0x80, 0x50, 0x60, 0x70, 0x80, 0x90, 0xa0, byte(index + 1),
	}
}

type multiplayerClientProbe struct {
	app      *application
	scenario multiplayerBenchmarkScenario
	codec    *network.Codec
	roster   *client.RemotePlayers

	encode       *client.LatencyRecorder
	decode       *client.LatencyRecorder
	rosterApply  *client.LatencyRecorder
	interpolate  *client.LatencyRecorder
	renderTiming *multiplayerRenderTiming
	gpuComplete  *client.LatencyRecorder
	now          func() time.Time
	tick         uint64
}

func newMultiplayerClientProbe(app *application) (*multiplayerClientProbe, error) {
	codec, err := network.NewCodec()
	if err != nil {
		return nil, err
	}
	probe := &multiplayerClientProbe{
		app:          app,
		scenario:     newMultiplayerBenchmarkScenario(),
		codec:        codec,
		roster:       client.NewRemotePlayers(),
		encode:       client.NewLatencyRecorder(benchmarkLatencyCapacity),
		decode:       client.NewLatencyRecorder(benchmarkLatencyCapacity),
		rosterApply:  client.NewLatencyRecorder(benchmarkLatencyCapacity),
		interpolate:  client.NewLatencyRecorder(benchmarkLatencyCapacity),
		renderTiming: newMultiplayerRenderTiming(),
		gpuComplete:  client.NewLatencyRecorder(client.ScenarioV12GPUCompletionSamples),
		now:          time.Now,
		tick:         1,
	}
	for _, spawn := range probe.scenario.Spawns {
		if err := probe.roster.Apply(spawn); err != nil {
			probe.Close()
			return nil, err
		}
		if err := app.remotePlayers.Apply(spawn); err != nil {
			probe.Close()
			return nil, err
		}
	}
	app.multiplayerRenderTiming = probe.renderTiming
	app.multiplayerRenderNow = time.Now
	return probe, nil
}

func (probe *multiplayerClientProbe) Close() {
	if probe != nil && probe.app != nil && probe.app.multiplayerRenderTiming == probe.renderTiming {
		probe.app.multiplayerRenderTiming = nil
		probe.app.multiplayerRenderNow = nil
		probe.app = nil
	}
	if probe != nil && probe.codec != nil {
		probe.codec.Close()
		probe.codec = nil
	}
}

func (probe *multiplayerClientProbe) sampleFrame(app *application, tick uint64) error {
	batch := probe.statesNearCamera(tick, app.camera.Pos)
	started := time.Now()
	packetID, payload, err := probe.codec.EncodeServer(network.StatePlay, batch)
	probe.encode.Add(time.Since(started))
	if err != nil {
		return fmt.Errorf("编码远端状态: %w", err)
	}
	started = time.Now()
	decoded, err := probe.codec.DecodeServer(network.StatePlay, packetID, payload)
	probe.decode.Add(time.Since(started))
	if err != nil {
		return fmt.Errorf("解码远端状态: %w", err)
	}
	message, ok := decoded.(network.ServerMessage)
	if !ok {
		return fmt.Errorf("解码远端状态得到非 ServerMessage: %T", decoded)
	}
	started = time.Now()
	if err := app.remotePlayers.Apply(message); err != nil {
		return fmt.Errorf("应用远端 roster: %w", err)
	}
	probe.rosterApply.Add(time.Since(started))
	if err := probe.roster.Apply(message); err != nil {
		return fmt.Errorf("应用 GPU 样本 roster: %w", err)
	}
	started = time.Now()
	probe.roster.Advance(fixedBenchmarkFrameDuration)
	probe.interpolate.Add(time.Since(started))
	return nil
}

func (probe *multiplayerClientProbe) statesNearCamera(tick uint64, camera mgl32.Vec3) network.RemotePlayerStates {
	batch := probe.scenario.States(tick)
	for index := range batch.Players {
		base := probe.scenario.Spawns[index].Position
		batch.Players[index].Position = batch.Players[index].Position.Sub(base).Add(camera).Add(
			mgl32.Vec3{0, -1.6, -6},
		)
	}
	return batch
}

func benchmarkBillboardCamera(app *application) render.BillboardCamera {
	right := mgl32.Vec3{
		float32(math.Cos(float64(app.camera.Yaw))), 0,
		-float32(math.Sin(float64(app.camera.Yaw))),
	}
	return render.BillboardCamera{
		ViewProj: app.camera.ViewProj(), Right: right,
		Up: right.Cross(app.camera.Forward()).Normalize(),
	}
}

// gpuCompletionChunks 是一个样本拆成的 command buffer 数量。
const gpuCompletionChunks = client.ScenarioV12GPUCompletionBatch /
	client.ScenarioV12GPUCompletionChunk

func (probe *multiplayerClientProbe) measureGPUCompletion(app *application) error {
	avatars, tags := remoteRenderPresentations(probe.roster.Presentations())
	// 一个样本是一批绘制只等待一次完成的总耗时除以批次数量：
	// Poll 的固定节拍在样本内只出现一次，被摊薄到可忽略。
	for range client.ScenarioV12GPUCompletionSamples {
		if err := app.nameTagRenderer.Prepare(tags, app.renderer.UploadBudget()); err != nil {
			return err
		}
		commands := make([]gfx.CommandBuffer, 0, gpuCompletionChunks)
		for range gpuCompletionChunks {
			encoder := app.dev.CreateCommandEncoder()
			for range client.ScenarioV12GPUCompletionChunk {
				app.avatarRenderer.Render(encoder, app.colorView, app.depth.view, render.Camera{
					ViewProj: app.camera.ViewProj(), Pos: app.camera.Pos,
				}, avatars)
				app.nameTagRenderer.Render(
					encoder, app.colorView, app.depth.view, benchmarkBillboardCamera(app),
				)
			}
			commands = append(commands, encoder.Finish())
		}
		started := probe.now()
		app.dev.Submit(commands...)
		app.dev.Poll(true)
		probe.gpuComplete.Add(probe.now().Sub(started) / client.ScenarioV12GPUCompletionBatch)
		for _, command := range commands {
			command.Release()
		}
		// 计时区间之外再推进一次设备队列，确保本样本的 command buffer 被回收，
		// 不会累积到下一个样本触发 Metal 的预算上限。
		app.dev.Poll(true)
		// 每个样本都回收：ru_maxrss 是进程生命周期的历史峰值，一旦被推高就无法
		// 降回，因此必须阻止批量分摊产生的对象在采样过程中累积。
		runtime.GC()
	}
	return nil
}

func (probe *multiplayerClientProbe) Summary() client.MultiplayerSummary {
	avatarSubmit, nameTagSubmit := probe.renderTiming.Summaries()
	return client.MultiplayerSummary{
		RemoteStateEncode:      probe.encode.Summary(),
		RemoteStateDecode:      probe.decode.Summary(),
		RosterApply:            probe.rosterApply.Summary(),
		Interpolation:          probe.interpolate.Summary(),
		AvatarSubmit:           avatarSubmit,
		NameTagSubmit:          nameTagSubmit,
		RemoteGPUComplete:      probe.gpuComplete.Summary(),
		RemoteGPUCompleteBatch: client.ScenarioV12GPUCompletionBatch,
	}
}
