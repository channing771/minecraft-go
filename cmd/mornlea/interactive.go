//go:build darwin

package main

import (
	"log/slog"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/companion"
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
	enterWasDown := false
	backspaceWasDown := false
	panelSaveWasDown := false
	panelResetAllWasDown := false
	var input client.InputState
	// `textInputBuffer` 与 `chatInput.runes` 同以 `companion.MaxPlanCommandBytes`
	// 为界（M5E 递延 2 的清偿，E7 同源化收口）：rune 编码后每字符至少 1 字节，
	// 满上限指令即使单帧全部到达也不会在 drain 层截断。两处界一旦分叉，多余
	// 输入先在有效界较小的那一层被拦下——drain 层截断时置 textOverflow，
	// `chatInput` 的字节上限则置 `overflow` 并在提交时整体拒发，两层都不静默。
	var textInputBuffer [companion.MaxPlanCommandBytes]rune

	for !app.window.ShouldClose() {
		app.window.Poll()

		now := time.Now()
		dt := min(now.Sub(lastFrame), 100*time.Millisecond)
		lastFrame = now
		capturedBeforeDrain := app.window.CursorCaptured()
		app.drainServerMessages(64)
		if err := app.receiver.Err(); err != nil {
			app.closeClientSession(err)
			return err
		}
		justCaptured := !capturedBeforeDrain && app.window.CursorCaptured()
		if justCaptured {
			lastMouseX, lastMouseY = app.window.CursorPos()
		}
		textInput, textOverflow := app.window.DrainTextInput(textInputBuffer[:0])
		chatWasOpen := app.chatInput.open
		if chatWasOpen {
			if textOverflow {
				app.chatInput.overflow = true
			}
			for _, char := range textInput {
				app.chatInput.Append(char)
			}
		}

		escapeDown := app.window.KeyDown(client.KeyEscape)
		enterDown := app.window.KeyDown(client.KeyEnter)
		enterPressed := enterDown && !enterWasDown
		backspaceDown := app.window.KeyDown(client.KeyBackspace)
		chatCanceled := false
		if escapeDown && !escapeWasDown {
			switch {
			case app.chatInput.open:
				chatCanceled = true
				app.chatInput.Cancel()
				app.window.SetCursorCaptured(true)
				lastMouseX, lastMouseY = app.window.CursorPos()
				justCaptured = true
			case app.inventoryOpen:
				// 背包打开时 Escape 只关闭背包并重新捕获鼠标。
				app.setInventoryOpen(false)
				lastMouseX, lastMouseY = app.window.CursorPos()
				justCaptured = true
			default:
				app.window.SetCursorCaptured(false)
			}
		}
		if chatCanceled {
			enterPressed = false
		}
		escapeWasDown = escapeDown
		if app.chatInput.open && backspaceDown && !backspaceWasDown {
			app.chatInput.Backspace()
		}
		backspaceWasDown = backspaceDown

		// 调试面板按键：F3/F5/F6、方向键与 Enter 都是边沿触发（按一下走一步），
		// Shift/Alt 是电平读取的修饰键。面板不存在时（未开 --dev）整段直接跳过，
		// 方向键既不驱动面板、也从不驱动玩家移动（移动只读 WASD）。
		if app.panel != nil {
			toggleDown := app.window.KeyDown(client.KeyF3)
			upDown := app.window.KeyDown(client.KeyUp)
			downDown := app.window.KeyDown(client.KeyDown)
			leftDown := app.window.KeyDown(client.KeyLeft)
			rightDown := app.window.KeyDown(client.KeyRight)
			saveDown := app.window.KeyDown(client.KeyF5)
			resetAllDown := app.window.KeyDown(client.KeyF6)
			panelBlocked := chatWasOpen || app.chatInput.open

			keys := panelKeys{
				Toggle: !panelBlocked && toggleDown && !panelToggleWasDown,
				Up:     !panelBlocked && upDown && !panelUpWasDown,
				Down:   !panelBlocked && downDown && !panelDownWasDown,
				Left:   !panelBlocked && leftDown && !panelLeftWasDown,
				Right:  !panelBlocked && rightDown && !panelRightWasDown,
				Enter: !panelBlocked && !app.inventoryOpen && !app.containerOpen() &&
					enterPressed && app.panel.visible,
				Save:     !panelBlocked && saveDown && !panelSaveWasDown,
				ResetAll: !panelBlocked && resetAllDown && !panelResetAllWasDown,
				Shift:    app.window.KeyDown(client.KeyLeftShift),
				Alt:      app.window.KeyDown(client.KeyLeftAlt),
			}
			panelToggleWasDown = toggleDown
			panelUpWasDown = upDown
			panelDownWasDown = downDown
			panelLeftWasDown = leftDown
			panelRightWasDown = rightDown
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

		if enterPressed {
			switch {
			case app.chatInput.open:
				if command, ok := app.chatInput.Submit(); ok {
					if err := app.send(command); err != nil {
						slog.Warn("发送伙伴聊天失败", "error", err)
					}
					app.window.SetCursorCaptured(true)
					lastMouseX, lastMouseY = app.window.CursorPos()
					justCaptured = true
				}
			case app.inventoryOpen || app.containerOpen() || (app.panel != nil && app.panel.visible):
				// 更高优先级的界面消费 Enter。
			default:
				app.chatInput.Open()
				app.window.SetCursorCaptured(false)
				app.applyInteractiveInput(0, client.Movement{}, client.Actions{}, false)
			}
		}
		enterWasDown = enterDown
		chatBlockedThisFrame := chatWasOpen || app.chatInput.open

		clickDown := app.window.PrimaryButtonDown()
		if clickDown && !clickWasDown && !app.window.CursorCaptured() && !app.inventoryOpen && !app.chatInput.open {
			app.window.SetCursorCaptured(true)
			lastMouseX, lastMouseY = app.window.CursorPos()
			justCaptured = true
		}
		clickWasDown = clickDown
		captured := app.window.CursorCaptured()
		if captured && !justCaptured && !app.inventoryOpen && !app.chatInput.open {
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
			app.inventoryOpen || chatBlockedThisFrame,
		)
		if actions.ToggleInventory && !chatBlockedThisFrame {
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
		if app.inventoryOpen || chatBlockedThisFrame {
			// 界面打开时持续发送中性输入，避免服务端沿用上一帧移动。
			movement = client.Movement{}
		}
		app.applyInteractiveCursorInput(
			dt, movement, actions, captured && !chatBlockedThisFrame, justCaptured,
		)
		app.remotePlayers.Advance(dt)
		if app.companions != nil {
			app.companions.Advance(dt)
		}
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
		// 手持食物时「使用」键按住即进食，这是进食位**唯一**的置位来源。
		// 客户端只上行意图：不扣本地背包、不改本地饥饿值，服务端才是权威。
		Eating: allowActions && actions.Use && a.holdingFood(),
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
