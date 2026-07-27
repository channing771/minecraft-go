//go:build darwin

// Command mcgo 启动可自由飞行的 M1 地形客户端。
package main

import (
	"log"
	"math"
	"runtime"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/assets"
	"minecraft-go/internal/client"
	"minecraft-go/internal/core"
	"minecraft-go/internal/gfx"
	"minecraft-go/internal/render"
	"minecraft-go/internal/worldgen"
)

const viewDistance = 32

func init() {
	runtime.LockOSThread()
}

func main() {
	win, err := client.NewWindow(2560, 1440, "minecraft-go — M1 flyable world")
	if err != nil {
		log.Fatalf("创建窗口失败: %v", err)
	}
	defer win.Close()

	fbWidth, fbHeight := win.FramebufferSize()
	dev, surface, err := gfx.NewDevice(win.NativeHandle(), uint32(fbWidth), uint32(fbHeight))
	if err != nil {
		log.Fatalf("创建 GPU 设备失败: %v", err)
	}
	defer dev.Release()
	defer surface.Release()

	reg := assets.NewRegistry()
	renderer := render.New(dev, reg, surface.Format())
	defer renderer.Release()
	renderer.Resize(uint32(fbWidth), uint32(fbHeight))

	depth := newDepthTarget(dev, uint32(fbWidth), uint32(fbHeight))
	defer func() { depth.Release() }()

	workerCount := max(1, runtime.NumCPU()-2)
	streamer := client.NewStreamer(worldgen.New(42), reg, workerCount)
	defer streamer.Close()

	camera := client.Camera{
		Pos:    mgl32.Vec3{0, 110, 0},
		Pitch:  -0.25,
		FovY:   mgl32.DegToRad(70),
		Aspect: float32(fbWidth) / float32(fbHeight),
		Near:   0.1,
		Far:    2000,
	}
	center := cameraChunk(camera.Pos)
	streamer.SetCenter(center, viewDistance)

	win.SetCursorCaptured(true)
	lastMouseX, lastMouseY := win.CursorPos()
	lastFrame := time.Now()
	escapeWasDown := false
	clickWasDown := false

	for !win.ShouldClose() {
		win.Poll()

		now := time.Now()
		dt := float32(now.Sub(lastFrame).Seconds())
		lastFrame = now
		dt = min(dt, 0.1)

		escapeDown := win.KeyDown(client.KeyEscape)
		if escapeDown && !escapeWasDown {
			win.SetCursorCaptured(false)
		}
		escapeWasDown = escapeDown

		clickDown := win.PrimaryButtonDown()
		if clickDown && !clickWasDown && !win.CursorCaptured() {
			win.SetCursorCaptured(true)
			lastMouseX, lastMouseY = win.CursorPos()
		}
		clickWasDown = clickDown

		if win.CursorCaptured() {
			mouseX, mouseY := win.CursorPos()
			camera.Rotate(
				float32(mouseX-lastMouseX)*0.002,
				float32(lastMouseY-mouseY)*0.002,
			)
			lastMouseX, lastMouseY = mouseX, mouseY

			var fwd, right, up float32
			if win.KeyDown(client.KeyW) {
				fwd++
			}
			if win.KeyDown(client.KeyS) {
				fwd--
			}
			if win.KeyDown(client.KeyD) {
				right++
			}
			if win.KeyDown(client.KeyA) {
				right--
			}
			if win.KeyDown(client.KeySpace) {
				up++
			}
			if win.KeyDown(client.KeyLeftShift) {
				up--
			}
			speed := float32(30)
			if win.KeyDown(client.KeyLeftControl) {
				speed = 120
			}
			camera.Move(fwd*speed*dt, right*speed*dt, up*speed*dt)
		}

		newCenter := cameraChunk(camera.Pos)
		if newCenter != center {
			center = newCenter
			streamer.SetCenter(center, viewDistance)
		}

		width, height := win.FramebufferSize()
		if width == 0 || height == 0 {
			continue
		}
		if uint32(width) != depth.width || uint32(height) != depth.height {
			surface.Resize(uint32(width), uint32(height))
			depth.Release()
			depth = newDepthTarget(dev, uint32(width), uint32(height))
			renderer.Resize(uint32(width), uint32(height))
			camera.Aspect = float32(width) / float32(height)
		}

		renderer.BeginFrame()
		for _, result := range streamer.Drain(64) {
			renderer.SetConnectivity(result.Pos, result.Conn)
			renderer.QueueSection(result.Pos, result.Quads)
		}
		renderer.FlushUploads(center)
		renderer.DropOutside(center, viewDistance)

		target := surface.Acquire()
		if target == nil {
			continue
		}
		encoder := dev.CreateCommandEncoder()
		renderer.Render(encoder, target, depth.view, render.Camera{
			ViewProj: camera.ViewProj(),
			Pos:      camera.Pos,
		})
		command := encoder.Finish()
		dev.Submit(command)
		command.Release()
		surface.Present()
	}
}

func cameraChunk(pos mgl32.Vec3) core.ChunkPos {
	return core.BlockPos{
		X: int32(math.Floor(float64(pos.X()))),
		Z: int32(math.Floor(float64(pos.Z()))),
	}.Chunk()
}

type depthTarget struct {
	texture       gfx.Texture
	view          gfx.TextureView
	width, height uint32
}

func newDepthTarget(dev gfx.Device, width, height uint32) *depthTarget {
	texture := dev.CreateTexture(gfx.TextureDesc{
		Label:     "main depth",
		Width:     width,
		Height:    height,
		Format:    gfx.FormatDepth32Float,
		Dimension: gfx.TextureDimension2D,
		Usage:     gfx.TextureUsageRenderTarget | gfx.TextureUsageBinding,
	})
	view := texture.View(gfx.TextureViewDesc{
		Dimension: gfx.TextureViewDimension2D,
		Aspect:    gfx.AspectDepthOnly,
	})
	return &depthTarget{
		texture: texture,
		view:    view,
		width:   width,
		height:  height,
	}
}

func (d *depthTarget) Release() {
	if d.view != nil {
		d.view.Release()
		d.view = nil
	}
	if d.texture != nil {
		d.texture.Release()
		d.texture = nil
	}
}
