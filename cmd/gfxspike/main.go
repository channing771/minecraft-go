//go:build darwin

// Command gfxspike 是 M0 技术验证程序。
// 它的唯一职责是证明 compute shader 能决定 indirect draw 的实例数。
//
// M0 只验证 macOS/Metal 这一条最不确定的路径，所以本文件带 darwin 构建约束；
// 跨平台窗口句柄分支留到需要时再加。
package main

import (
	"log"
	"runtime"
	"unsafe"

	"github.com/go-gl/glfw/v3.3/glfw"

	"minecraft-go/internal/gfx"
)

func init() {
	// GLFW 与图形 API 都要求所有调用发生在同一个 OS 线程上。
	runtime.LockOSThread()
}

func main() {
	if err := glfw.Init(); err != nil {
		log.Fatalf("glfw 初始化失败: %v", err)
	}
	defer glfw.Terminate()

	// 不创建任何 OpenGL 上下文——渲染交给 WebGPU。
	glfw.WindowHint(glfw.ClientAPI, glfw.NoAPI)
	win, err := glfw.CreateWindow(1280, 720, "gfxspike", nil, nil)
	if err != nil {
		log.Fatalf("创建窗口失败: %v", err)
	}
	defer win.Destroy()

	// surface 要按帧缓冲（像素）尺寸配置，Retina 屏上它是窗口逻辑尺寸的两倍。
	fbWidth, fbHeight := win.GetFramebufferSize()

	probe, err := gfx.NewProbe(cocoaHandle(win), uint32(fbWidth), uint32(fbHeight))
	if err != nil {
		log.Fatalf("创建 WebGPU 设备失败: %v", err)
	}
	defer probe.Close()

	for !win.ShouldClose() {
		glfw.PollEvents()
		if err := probe.Frame(); err != nil {
			log.Fatalf("渲染帧失败: %v", err)
		}
	}
}

// cocoaHandle 把 GLFW 窗口的 NSWindow 指针转成与窗口库无关的 uintptr，
// 好让 internal/gfx 不必知道 GLFW 的存在。
// go-gl/glfw v3.3 的 GetCocoaWindow 返回 unsafe.Pointer。
func cocoaHandle(win *glfw.Window) uintptr {
	return uintptr(unsafe.Pointer(win.GetCocoaWindow()))
}
