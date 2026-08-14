//go:build darwin

package client

import (
	"fmt"
	"unsafe"

	"github.com/go-gl/glfw/v3.3/glfw"

	"github.com/channing771/mornlea/internal/gfx"
)

// Key 是主程序需要的最小按键集合。
type Key uint8

const (
	KeyW Key = iota
	KeyA
	KeyS
	KeyD
	KeySpace
	KeyLeftShift
	KeyLeftControl
	KeyEscape
	Key1
	Key2
	Key3
	Key4
	Key5
	Key6
	Key7
	Key8
	Key9
	KeyE
	KeyQ
	// 以下按键为调试面板交互追加，必须保持在末尾以免改变既有常量的 iota 取值。
	KeyF3
	KeyF5
	KeyF6
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyEnter
	KeyLeftAlt
	KeyBackspace
)

var glfwKeys = [...]glfw.Key{
	KeyW:           glfw.KeyW,
	KeyA:           glfw.KeyA,
	KeyS:           glfw.KeyS,
	KeyD:           glfw.KeyD,
	KeySpace:       glfw.KeySpace,
	KeyLeftShift:   glfw.KeyLeftShift,
	KeyLeftControl: glfw.KeyLeftControl,
	KeyEscape:      glfw.KeyEscape,
	Key1:           glfw.Key1,
	Key2:           glfw.Key2,
	Key3:           glfw.Key3,
	Key4:           glfw.Key4,
	Key5:           glfw.Key5,
	Key6:           glfw.Key6,
	Key7:           glfw.Key7,
	Key8:           glfw.Key8,
	Key9:           glfw.Key9,
	KeyE:           glfw.KeyE,
	KeyQ:           glfw.KeyQ,
	KeyF3:          glfw.KeyF3,
	KeyF5:          glfw.KeyF5,
	KeyF6:          glfw.KeyF6,
	KeyUp:          glfw.KeyUp,
	KeyDown:        glfw.KeyDown,
	KeyLeft:        glfw.KeyLeft,
	KeyRight:       glfw.KeyRight,
	KeyEnter:       glfw.KeyEnter,
	KeyLeftAlt:     glfw.KeyLeftAlt,
	KeyBackspace:   glfw.KeyBackspace,
}

// Window 封装 GLFW 窗口、输入和原生句柄。
type Window struct {
	raw               *glfw.Window
	captured          bool
	closed            bool
	textInput         [1024]rune
	textInputCount    int
	textInputOverflow bool
}

func NewWindow(width, height int, title string) (*Window, error) {
	if err := glfw.Init(); err != nil {
		return nil, fmt.Errorf("初始化 GLFW: %w", err)
	}
	glfw.WindowHint(glfw.ClientAPI, glfw.NoAPI)
	raw, err := glfw.CreateWindow(width, height, title, nil, nil)
	if err != nil {
		glfw.Terminate()
		return nil, fmt.Errorf("创建窗口: %w", err)
	}
	window := &Window{raw: raw}
	raw.SetCharCallback(func(_ *glfw.Window, char rune) {
		window.enqueueTextInput(char)
	})
	return window, nil
}

func (w *Window) Poll() { glfw.PollEvents() }

func (w *Window) enqueueTextInput(char rune) {
	if w.textInputCount == len(w.textInput) {
		w.textInputOverflow = true
		return
	}
	w.textInput[w.textInputCount] = char
	w.textInputCount++
}

// DrainTextInput 返回自上次 drain 后收到的字符与固定队列 overflow，并清空窗口队列。
func (w *Window) DrainTextInput(dst []rune) ([]rune, bool) {
	dst = append(dst, w.textInput[:w.textInputCount]...)
	overflow := w.textInputOverflow
	w.textInputCount = 0
	w.textInputOverflow = false
	return dst, overflow
}

func (w *Window) ShouldClose() bool { return w.raw.ShouldClose() }

func (w *Window) CancelClose() { w.raw.SetShouldClose(false) }

func (w *Window) FramebufferSize() (int, int) { return w.raw.GetFramebufferSize() }

func (w *Window) ContentSize() (int, int) { return w.raw.GetSize() }

func (w *Window) SetContentSize(width, height int) { w.raw.SetSize(width, height) }

func (w *Window) SetFloating(floating bool) {
	value := glfw.False
	if floating {
		value = glfw.True
	}
	w.raw.SetAttrib(glfw.Floating, value)
}

func (w *Window) Focus() { w.raw.Focus() }

func (w *Window) CursorPos() (float64, float64) { return w.raw.GetCursorPos() }

func (w *Window) KeyDown(key Key) bool {
	if int(key) >= len(glfwKeys) {
		return false
	}
	state := w.raw.GetKey(glfwKeys[key])
	return state == glfw.Press || state == glfw.Repeat
}

func (w *Window) PrimaryButtonDown() bool {
	state := w.raw.GetMouseButton(glfw.MouseButtonLeft)
	return state == glfw.Press
}

func (w *Window) SecondaryButtonDown() bool {
	state := w.raw.GetMouseButton(glfw.MouseButtonRight)
	return state == glfw.Press
}

func (w *Window) SetCursorCaptured(captured bool) {
	if captured == w.captured {
		return
	}
	w.captured = captured
	mode := glfw.CursorNormal
	if captured {
		mode = glfw.CursorDisabled
	}
	w.raw.SetInputMode(glfw.CursorMode, mode)
}

func (w *Window) CursorCaptured() bool { return w.captured }

// NativeHandle 返回 macOS 的 NSWindow*；gfx 负责把它接到 Metal surface。
func (w *Window) NativeHandle() gfx.NativeWindowHandle {
	return gfx.NativeWindowHandle{
		Kind:    gfx.HandleKindNSWindow,
		Pointer: uintptr(unsafe.Pointer(w.raw.GetCocoaWindow())),
	}
}

func (w *Window) Close() {
	if w.closed {
		return
	}
	w.closed = true
	w.raw.Destroy()
	glfw.Terminate()
}
