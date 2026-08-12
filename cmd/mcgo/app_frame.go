//go:build darwin

package main

import (
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/render"
	"minecraft-go/internal/render/hud"
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
	return a.renderFrame(meshWorkMax)
}

// renderFrame 绘制一帧，返回 surface 是否实际取得了可呈现纹理。
func (a *application) renderFrame(workMax int) (bool, error) {
	blockTargetReset := a.blockTargetReset
	a.blockTargetReset = false
	width, height := a.framebufferSize()
	if width == 0 || height == 0 {
		return false, nil
	}
	if a.surface != nil && (uint32(width) != a.depth.width || uint32(height) != a.depth.height) {
		a.surface.Resize(uint32(width), uint32(height))
		a.depth.Release()
		a.depth = newDepthTarget(a.dev, uint32(width), uint32(height))
		a.renderer.Resize(uint32(width), uint32(height))
		a.camera.Aspect = float32(width) / float32(height)
	}

	a.renderer.BeginFrame()
	a.mesher.Schedule(a.mirror, workMax)
	for _, result := range a.mesher.Drain(a.mirror, workMax) {
		if result.Dimension != core.Overworld {
			continue
		}
		a.renderer.SetConnectivity(result.Pos, result.Conn)
		a.renderer.QueueSection(result.Pos, result.Quads)
	}
	a.renderer.FlushUploads(a.center)
	a.remotePresentations = a.remotePlayers.AppendPresentations(a.remotePresentations[:0])
	a.remoteAvatars, a.remoteNameTags = remoteRenderPresentationsSortedInto(
		a.remoteAvatars[:0],
		a.remoteNameTags[:0],
		a.remotePresentations,
	)
	blockOutline := render.BlockOutline{}
	if !blockTargetReset && !a.clientSessionClosed {
		a.remoteNameTags, blockOutline = a.appendCurrentBlockTarget(a.remoteNameTags)
	}
	avatars, tags := a.remoteAvatars, a.remoteNameTags
	renderTiming := a.multiplayerRenderTiming
	var renderNow func() time.Time
	var nameTagDuration time.Duration
	if renderTiming != nil {
		renderNow = a.multiplayerRenderNow
		if renderNow == nil {
			renderNow = time.Now
		}
		started := renderNow()
		if err := a.nameTagRenderer.Prepare(tags, a.renderer.UploadBudget()); err != nil {
			return false, fmt.Errorf("准备世界名牌: %w", err)
		}
		nameTagDuration = renderNow().Sub(started)
	} else if err := a.nameTagRenderer.Prepare(tags, a.renderer.UploadBudget()); err != nil {
		return false, fmt.Errorf("准备世界名牌: %w", err)
	}
	inventory, inventoryConfirmed := a.inventory.State()
	if inventoryConfirmed {
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
		// 生命值的确认状态独立于背包：Predictor 尚未收到权威状态时 ready 为
		// false，HUD 绝不画出预测或陈旧的生命值。
		health, healthReady := a.predictor.Health()
		if err := a.hotbarRenderer.Prepare(
			inventory, a.inventoryOpen, a.inventorySource, overlay, chestOverlay,
			a.miningOverlay, hud.HealthOverlay{Confirmed: healthReady, Value: health},
			uint32(width), uint32(height), a.renderer.UploadBudget(),
		); err != nil {
			return false, fmt.Errorf("准备快捷栏 HUD: %w", err)
		}
	}
	if a.debugPanelRenderer != nil {
		readout, rows := a.panelFrameInput(time.Now())
		if err := a.debugPanelRenderer.Prepare(
			a.panel.visible, readout, rows,
			uint32(width), uint32(height), a.renderer.UploadBudget(),
		); err != nil {
			return false, fmt.Errorf("准备调试面板: %w", err)
		}
	}
	a.renderer.DropOutside(a.center, a.render.ViewDistance)

	target := a.colorView
	if a.surface != nil {
		target = a.surface.Acquire()
		if target == nil {
			return false, nil
		}
	}
	encoder := a.dev.CreateCommandEncoder()
	// 每帧只从最后确认的权威世界时间计算一次昼夜；ViewProj 及其逆矩阵同样只计算一次，
	// terrain、avatar、item-drop、block-outline 与天空共用同一正向矩阵和 daylight。
	dayNight := render.DayNightAt(a.worldTimeTicks)
	viewProj := a.camera.ViewProj()
	viewProjInv := viewProj.Inv()
	a.renderer.Render(encoder, target, a.depth.view, render.Camera{
		ViewProj:       viewProj,
		ViewProjInv:    viewProjInv,
		Pos:            a.camera.Pos,
		SunDirection:   dayNight.SunDirection,
		Daylight:       dayNight.Daylight,
		StarVisibility: dayNight.StarVisibility,
		SkyColor:       dayNight.ClearColor,
	})
	var started time.Time
	if renderTiming != nil {
		started = renderNow()
	}
	entityCamera := render.Camera{
		ViewProj: viewProj,
		Pos:      a.camera.Pos,
		Daylight: dayNight.Daylight,
		SkyColor: dayNight.ClearColor,
	}
	a.avatarRenderer.Render(encoder, target, a.depth.view, entityCamera, avatars)
	if renderTiming != nil {
		renderTiming.recordAvatar(renderNow().Sub(started))
		started = renderNow()
	}
	a.itemDropInstances = appendItemDropInstances(
		a.itemDropInstances[:0], a.itemDrops.Presentations(),
	)
	a.itemDropRenderer.Render(
		encoder, target, a.depth.view, entityCamera, a.serverTick, a.itemDropInstances,
	)
	a.blockOutlineRenderer.Render(
		encoder, target, a.depth.view, entityCamera, blockOutline,
	)
	right := mgl32.Vec3{
		float32(math.Cos(float64(a.camera.Yaw))),
		0,
		-float32(math.Sin(float64(a.camera.Yaw))),
	}
	a.nameTagRenderer.Render(encoder, target, a.depth.view, render.BillboardCamera{
		ViewProj: viewProj,
		Right:    right,
		Up:       right.Cross(a.camera.Forward()).Normalize(),
	})
	if renderTiming != nil {
		renderTiming.recordNameTag(nameTagDuration + renderNow().Sub(started))
	}
	a.damageOverlayRenderer.Render(encoder, target, a.damageStrength)
	// HUD 在全部世界 pass 与 damage overlay 之后绘制。
	if inventoryConfirmed {
		a.hotbarRenderer.Render(encoder, target)
	}
	// 调试面板是最上层：必须在 HUD 之后绘制，否则会被背包/容器界面盖住。
	if a.debugPanelRenderer != nil {
		a.debugPanelRenderer.Render(encoder, target)
	}
	command := encoder.Finish()
	a.dev.Submit(command)
	command.Release()
	if a.surface != nil {
		a.surface.Present()
	}
	return true, nil
}
