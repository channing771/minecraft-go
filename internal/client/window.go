//go:build darwin

package client

import (
	"fmt"
	"unsafe"

	"github.com/go-gl/glfw/v3.3/glfw"

	"minecraft-go/internal/gfx"
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
}

// Window 封装 GLFW 窗口、输入和原生句柄。
type Window struct {
	raw      *glfw.Window
	captured bool
	closed   bool
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
	return &Window{raw: raw}, nil
}

func (w *Window) Poll() { glfw.PollEvents() }

func (w *Window) ShouldClose() bool { return w.raw.ShouldClose() }

func (w *Window) FramebufferSize() (int, int) { return w.raw.GetFramebufferSize() }

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
