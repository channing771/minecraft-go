//go:build darwin

package main

import (
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/mesh"
	"github.com/channing771/mornlea/internal/render"
	"github.com/channing771/mornlea/internal/render/hud"
)

func (a *application) updateCenter() {
	center := cameraChunk(a.camera.Pos)
	if center == a.center {
		return
	}
	a.center = center
	if err := a.requestTrustedObserverCenter(center); err != nil {
		slog.Warn("更新视距中心失败", "error", err)
	}
}

func (a *application) requestTrustedObserverCenter(center core.ChunkPos) error {
	_, _, sequence, _ := a.server.AppliedTrustedObserverCenter()
	a.observerFloor = sequence
	return a.server.SetTrustedObserverCenter(core.Overworld, center)
}

func (a *application) nextSequence() uint64 {
	a.sequence++
	return a.sequence
}

// frame 应用服务端消息后绘制一帧。
func (a *application) frame(drainMax, meshWorkMax int, elapsed time.Duration) (bool, error) {
	a.drainServerMessages(drainMax)
	if a.receiver != nil {
		if err := a.receiver.Err(); err != nil {
			a.closeClientSession(err)
			return false, err
		}
	}
	health, ready := a.predictor.Health()
	a.damageStrength = a.damageFeedback.Update(health, ready, elapsed)
	if a.remotePlayers != nil {
		a.remotePlayers.Advance(elapsed)
	}
	if a.companions != nil {
		a.companions.Advance(elapsed)
	}
	return a.renderFrame(meshWorkMax)
}

// renderFrame 绘制一帧，返回 surface 是否实际取得了可呈现纹理。
func (a *application) renderFrame(workMax int) (bool, error) {
	blockTargetReset := a.blockTargetReset
	width, height := a.framebufferSize()
	if width == 0 || height == 0 {
		return false, nil
	}
	a.remotePresentations = a.remotePlayers.AppendPresentations(a.remotePresentations[:0])
	a.remoteAvatars, a.remoteNameTags = remoteRenderPresentationsSortedInto(
		a.remoteAvatars[:0],
		a.remoteNameTags[:0],
		a.remotePresentations,
	)
	if a.companions != nil {
		a.companionPresentations = a.companions.AppendPresentations(a.companionPresentations[:0])
		a.remoteAvatars, a.remoteNameTags = appendCompanionRenderPresentationsInto(
			a.remoteAvatars,
			a.remoteNameTags,
			a.companionPresentations,
		)
	}
	blockOutline := render.BlockOutline{}
	if !blockTargetReset && !a.clientSessionClosed {
		a.remoteNameTags, blockOutline = a.appendCurrentBlockTarget(a.remoteNameTags)
	}
	avatars, tags := a.remoteAvatars, a.remoteNameTags
	if err := validateEntityPresentationCounts(avatars, tags); err != nil {
		return false, fmt.Errorf("准备实体呈现: %w", err)
	}
	a.blockTargetReset = false
	if a.window != nil && (width != a.frameWidth || height != a.frameHeight) {
		a.renderer.Resize(width, height)
		a.frameWidth, a.frameHeight = width, height
		a.camera.Aspect = float32(width) / float32(height)
	}

	a.scheduler.BeginFrame()
	a.mesher.Schedule(a.mirror, workMax)
	for _, result := range a.mesher.Drain(a.mirror, workMax) {
		if result.Dimension != core.Overworld {
			continue
		}
		a.scheduler.SetConnectivity(result.Pos, result.Conn)
		a.scheduler.QueueSection(result.Pos, result.Quads)
	}
	a.scheduler.FlushUploads(a.center)
	renderTiming := a.multiplayerRenderTiming
	var renderNow func() time.Time
	var nameTagDuration time.Duration
	if renderTiming != nil {
		renderNow = a.multiplayerRenderNow
		if renderNow == nil {
			renderNow = time.Now
		}
		started := renderNow()
		if err := a.nameTagRenderer.Prepare(tags, a.scheduler.UploadBudget()); err != nil {
			return false, fmt.Errorf("准备世界名牌: %w", err)
		}
		nameTagDuration = renderNow().Sub(started)
	} else if err := a.nameTagRenderer.Prepare(tags, a.scheduler.UploadBudget()); err != nil {
		return false, fmt.Errorf("准备世界名牌: %w", err)
	}
	inventory, inventoryConfirmed := a.inventory.State()
	var overlay *hud.FurnaceOverlay
	if furnace, opened := a.furnace.State(); opened {
		overlay = &hud.FurnaceOverlay{
			Input:         furnace.Input,
			Fuel:          furnace.Fuel,
			Output:        furnace.Output,
			ProgressTicks: furnace.ProgressTicks,
			BurnTicks:     furnace.BurnTicks,
		}
	}
	var chestOverlay *hud.ChestOverlay
	if chest, opened := a.chest.State(); opened {
		chestOverlay = &hud.ChestOverlay{Items: chest.Items}
	}
	// 生命值、氧气和聊天都独立于背包确认状态；未确认时 renderer 只跳过物品布局。
	health, healthReady := a.predictor.Health()
	oxygen, oxygenReady := a.predictor.Oxygen()
	chatOverlay := a.chatOverlay()
	hudVisible := inventoryConfirmed || (healthReady && !a.clientSessionClosed) ||
		chatOverlay.Open || len(chatOverlay.Lines) != 0
	if hudVisible {
		if err := a.hotbarRenderer.Prepare(
			inventory, inventoryConfirmed, a.inventoryOpen, a.inventorySource, overlay, chestOverlay,
			a.miningOverlay, hud.HealthOverlay{Confirmed: healthReady, Value: health},
			hud.OxygenOverlay{Confirmed: oxygenReady, Value: oxygen}, chatOverlay,
			uint32(width), uint32(height), a.scheduler.UploadBudget(),
		); err != nil {
			return false, fmt.Errorf("准备快捷栏 HUD: %w", err)
		}
	}
	if a.debugPanelRenderer != nil {
		readout, rows := a.panelFrameInput(time.Now())
		if err := a.debugPanelRenderer.Prepare(
			a.panel.visible, readout, rows,
			uint32(width), uint32(height), a.scheduler.UploadBudget(),
		); err != nil {
			return false, fmt.Errorf("准备调试面板: %w", err)
		}
	}
	a.scheduler.DropOutside(a.center, a.render.ViewDistance)

	// 每帧只从最后确认的权威世界时间计算一次昼夜;ViewProj 及其逆矩阵同样只计算一次。
	dayNight := render.DayNightAt(a.worldTimeTicks)
	cloud := render.CloudOffsetAt(a.worldTimeTicks)
	viewProj := a.camera.ViewProj()
	viewProjInv := viewProj.Inv()

	// 可见列表:BFS 连通性 + frustum,与旧 Go 渲染器同一算法与顺序。
	a.visibleSections = mesh.VisibleSectionsInto(
		a.visibleSections[:0], &a.visibleScratch,
		cameraSectionPos(a.camera.Pos), 32,
		core.FrustumFrom(viewProj), a.scheduler.Connectivity,
	)
	a.lastFrameStats = a.scheduler.FrameStats(a.visibleSections)
	if cap(a.rustVisible) < len(a.visibleSections) {
		a.rustVisible = make([][3]int32, 0, len(a.visibleSections))
	}
	a.rustVisible = a.rustVisible[:0]
	for _, p := range a.visibleSections {
		a.rustVisible = append(a.rustVisible, [3]int32{p.X, p.Y, p.Z})
	}

	var started time.Time
	if renderTiming != nil {
		started = renderNow()
	}
	a.avatarStream = a.entityEncoder.EncodeAvatarInstances(a.avatarStream, avatars)
	if renderTiming != nil {
		renderTiming.recordAvatar(renderNow().Sub(started))
		started = renderNow()
	}
	a.itemDropInstances = appendItemDropInstances(
		a.itemDropInstances[:0], a.itemDrops.Presentations(),
	)
	a.dropStream = a.entityEncoder.EncodeItemDropInstances(a.dropStream, a.serverTick, a.itemDropInstances)
	a.outlineStream = a.entityEncoder.EncodeBlockOutlineInstances(a.outlineStream, blockOutline)

	right := mgl32.Vec3{
		float32(math.Cos(float64(a.camera.Yaw))),
		0,
		-float32(math.Sin(float64(a.camera.Yaw))),
	}
	billboard := render.BillboardCamera{
		ViewProj: viewProj,
		Right:    right,
		Up:       right.Cross(a.camera.Forward()).Normalize(),
	}
	a.billboardBytes = render.EncodeBillboardCameraBytes(a.billboardBytes, billboard)
	nameTagBackgrounds, nameTagGlyphs := a.nameTagRenderer.FrameStreams()
	nameTagSegment := client.EncodeQuadSegment(
		a.billboardBytes, nameTagBackgrounds, nameTagGlyphs, 64,
	)
	if renderTiming != nil {
		renderTiming.recordNameTag(nameTagDuration + renderNow().Sub(started))
	}
	var hudSegment []byte
	if hudVisible {
		hudViewport, hudQuads, hudGlyphs := a.hotbarRenderer.FrameStreams()
		hudSegment = client.EncodeQuadSegment(hudViewport, hudQuads, hudGlyphs, 48)
	}
	var debugSegment []byte
	if a.debugPanelRenderer != nil {
		panelViewport, panelQuads, panelGlyphs := a.debugPanelRenderer.FrameStreams()
		debugSegment = client.EncodeQuadSegment(panelViewport, panelQuads, panelGlyphs, 48)
	}

	rendered := a.renderer.RenderFrame(client.RenderFrame{
		ViewProj:         viewProj,
		ViewProjInv:      viewProjInv,
		Pos:              a.camera.Pos,
		Daylight:         dayNight.Daylight,
		SunDirection:     dayNight.SunDirection,
		StarVisibility:   dayNight.StarVisibility,
		SkyColor:         dayNight.ClearColor,
		CloudMacroX:      cloud.MacroX,
		CloudLocal:       cloud.Local,
		Visible:          a.rustVisible,
		AvatarInstances:  a.avatarStream,
		DropInstances:    a.dropStream,
		OutlineInstances: a.outlineStream,
		OverlayStrength:  a.damageStrength,
		NameTagSegment:   nameTagSegment,
		HUDSegment:       hudSegment,
		DebugSegment:     debugSegment,
	})
	if !rendered {
		return false, nil
	}
	return true, nil
}
