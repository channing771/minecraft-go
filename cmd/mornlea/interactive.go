//go:build darwin

package main

import (
	"log/slog"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/physics"
)

func runInteractive(app *application) error {
	app.window.SetCursorCaptured(true)
	lastMouseX, lastMouseY := app.window.CursorPos()
	lastFrame := time.Now()
	escapeWasDown := false
	clickWasDown := false
	panelToggleWasDown := false
	panelUpWasDown := false
	panelDownWasDown := false
	panelLeftWasDown := false
	panelRightWasDown := false
	panelEnterWasDown := false
	panelSaveWasDown := false
	panelResetAllWasDown := false
	var input client.InputState

	for !app.window.ShouldClose() {
		app.window.Poll()

		now := time.Now()
		dt := min(now.Sub(lastFrame), 100*time.Millisecond)
		lastFrame = now
		app.drainServerMessages(64)
		if err := app.receiver.Err(); err != nil {
			app.closeClientSession(err)
			return err
		}

		escapeDown := app.window.KeyDown(client.KeyEscape)
		if escapeDown && !escapeWasDown {
			// 背包打开时 Escape 只关闭背包并重新捕获鼠标。
			if app.inventoryOpen {
				app.setInventoryOpen(false)
				lastMouseX, lastMouseY = app.window.CursorPos()
			} else {
				app.window.SetCursorCaptured(false)
			}
		}
		escapeWasDown = escapeDown

		// 调试面板按键：F3/F5/F6、方向键与 Enter 都是边沿触发（按一下走一步），
		// Shift/Alt 是电平读取的修饰键。面板不存在时（未开 --dev）整段直接跳过，
		// 方向键既不驱动面板、也从不驱动玩家移动（移动只读 WASD）。
		if app.panel != nil {
			toggleDown := app.window.KeyDown(client.KeyF3)
			upDown := app.window.KeyDown(client.KeyUp)
			downDown := app.window.KeyDown(client.KeyDown)
			leftDown := app.window.KeyDown(client.KeyLeft)
			rightDown := app.window.KeyDown(client.KeyRight)
			enterDown := app.window.KeyDown(client.KeyEnter)
			saveDown := app.window.KeyDown(client.KeyF5)
			resetAllDown := app.window.KeyDown(client.KeyF6)

			keys := panelKeys{
				Toggle:   toggleDown && !panelToggleWasDown,
				Up:       upDown && !panelUpWasDown,
				Down:     downDown && !panelDownWasDown,
				Left:     leftDown && !panelLeftWasDown,
				Right:    rightDown && !panelRightWasDown,
				Enter:    enterDown && !panelEnterWasDown,
				Save:     saveDown && !panelSaveWasDown,
				ResetAll: resetAllDown && !panelResetAllWasDown,
				Shift:    app.window.KeyDown(client.KeyLeftShift),
				Alt:      app.window.KeyDown(client.KeyLeftAlt),
			}
			panelToggleWasDown = toggleDown
			panelUpWasDown = upDown
			panelDownWasDown = downDown
			panelLeftWasDown = leftDown
			panelRightWasDown = rightDown
			panelEnterWasDown = enterDown
			panelSaveWasDown = saveDown
			panelResetAllWasDown = resetAllDown

			if app.panel.handleKeys(keys, app.remote()) {
				app.applyPanelChange()
			}
			// 面板隐藏时 F5 不落盘：设计文档要求配置文件"不自动创建"，
			// 面板关着时误触 F5 不该在 config.DefaultPath() 悄悄创建/覆盖它。
			if keys.Save && app.panel.visible {
				if err := app.panel.save(app.configPath); err != nil {
					slog.Warn("保存调试面板配置失败", "error", err)
				}
			}
		}

		clickDown := app.window.PrimaryButtonDown()
		justCaptured := false
		if clickDown && !clickWasDown && !app.window.CursorCaptured() && !app.inventoryOpen {
			app.window.SetCursorCaptured(true)
			lastMouseX, lastMouseY = app.window.CursorPos()
			justCaptured = true
		}
		clickWasDown = clickDown
		captured := app.window.CursorCaptured()
		if captured && !justCaptured && !app.inventoryOpen {
			mouseX, mouseY := app.window.CursorPos()
			// baseMouseSensitivity 是键鼠灵敏度默认为 1 时对应的原始弧度/像素系数；
			// Render.MouseSensitivity 是相对该基线的倍率，默认值 1 保持行为不变。
			const baseMouseSensitivity = 0.002
			sensitivity := baseMouseSensitivity * app.render.MouseSensitivity
			app.camera.Rotate(
				float32(mouseX-lastMouseX)*sensitivity,
				float32(lastMouseY-mouseY)*sensitivity,
			)
			lastMouseX, lastMouseY = mouseX, mouseY
		}

		number := pressedHotbarNumber(app.window)
		actions := input.Update(
			clickDown, app.window.SecondaryButtonDown(), number,
			app.window.KeyDown(client.KeyE), app.window.KeyDown(client.KeyQ),
			app.inventoryOpen,
		)
		if actions.ToggleInventory {
			app.setInventoryOpen(!app.inventoryOpen)
			if !app.inventoryOpen {
				lastMouseX, lastMouseY = app.window.CursorPos()
			}
		}
		if app.inventoryOpen && actions.Click {
			width, height := app.framebufferSize()
			cursorX, cursorY := app.window.CursorPos()
			app.clickInventorySlot(cursorX, cursorY, uint32(width), uint32(height))
		}

		movement := client.MovementFromKeys(
			app.window.KeyDown(client.KeyW),
			app.window.KeyDown(client.KeyA),
			app.window.KeyDown(client.KeyS),
			app.window.KeyDown(client.KeyD),
			app.window.KeyDown(client.KeySpace),
		)
		if app.inventoryOpen {
			// 界面打开时持续发送中性输入，避免服务端沿用上一帧移动。
			movement = client.Movement{}
		}
		app.applyInteractiveCursorInput(dt, movement, actions, captured, justCaptured)
		app.remotePlayers.Advance(dt)
		if _, err := app.renderFrame(steadyFrameMeshWorkMax); err != nil {
			return err
		}
	}
	return nil
}

func (a *application) applyInteractiveCursorInput(
	elapsed time.Duration,
	movement client.Movement,
	actions client.Actions,
	captured bool,
	justCaptured bool,
) {
	if !captured {
		movement = client.Movement{}
	}
	a.applyInteractiveInput(elapsed, movement, actions, captured && !justCaptured && !a.inventoryOpen)
}

// pressedHotbarNumber 返回当前按下的快捷栏数字键 1..9，没有按下时返回 0。
func pressedHotbarNumber(window applicationWindow) int {
	for index := range core.HotbarSlots {
		if window.KeyDown(client.Key1 + client.Key(index)) {
			return index + 1
		}
	}
	return 0
}

func (a *application) applyInteractiveInput(
	elapsed time.Duration,
	movement client.Movement,
	actions client.Actions,
	allowActions bool,
) {
	if allowActions {
		if actions.Select {
			a.selectHotbarSlot(actions.SelectSlot)
		}
		if actions.Place {
			a.placeBlock()
		}
		if actions.Drop {
			a.dropSelectedItem()
		}
	}

	if _, ready := a.predictor.State(); !ready {
		return
	}
	control := client.Control{
		MoveX:  movement.MoveX,
		MoveZ:  movement.MoveZ,
		Jump:   movement.Jump,
		Yaw:    a.camera.Yaw,
		Pitch:  a.camera.Pitch,
		Mining: allowActions && actions.Mining,
	}
	if err := a.predictor.Advance(
		elapsed,
		control,
		client.MirrorCollisionSource{Mirror: a.mirror, Dimension: core.Overworld},
		a.nextSequence,
		func(input network.PlayerInput) error { return a.send(input) },
	); err != nil {
		slog.Warn("推进玩家预测失败", "error", err)
	}
	if feet, ok := a.predictor.PresentationPosition(elapsed); ok {
		// 相机视线高度必须与服务端交互射线原点使用同一份参数，否则玩家瞄准的方块
		// 与服务端判定的方块不是同一个。
		a.camera.Pos = feet.Add(mgl32.Vec3{0, physics.ActiveTunables().EyeHeight, 0})
		a.center = cameraChunk(a.camera.Pos)
	}
}
