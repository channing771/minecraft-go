//go:build darwin

package main

import (
	"fmt"
	"math"
	"runtime"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/assets"
	"minecraft-go/internal/client"
	"minecraft-go/internal/core"
	"minecraft-go/internal/gfx"
	"minecraft-go/internal/render"
	"minecraft-go/internal/worldgen"
)

type application struct {
	window   *client.Window
	dev      gfx.Device
	surface  gfx.Surface
	renderer *render.Renderer
	streamer *client.Streamer
	depth    *depthTarget
	camera   client.Camera
	center   core.ChunkPos
}

func newApplication(seed int64) (*application, error) {
	window, err := client.NewWindow(2560, 1440, "minecraft-go — M1 flyable world")
	if err != nil {
		return nil, err
	}
	fitFramebuffer(window, 2560, 1440)
	width, height := window.FramebufferSize()
	dev, surface, err := gfx.NewDevice(window.NativeHandle(), uint32(width), uint32(height))
	if err != nil {
		window.Close()
		return nil, err
	}

	reg := assets.NewRegistry()
	renderer := render.New(dev, reg, surface.Format())
	renderer.Resize(uint32(width), uint32(height))
	camera := client.Camera{
		Pos:    mgl32.Vec3{0, 110, 0},
		Pitch:  -0.25,
		FovY:   mgl32.DegToRad(70),
		Aspect: float32(width) / float32(height),
		Near:   0.1,
		Far:    2000,
	}
	app := &application{
		window:   window,
		dev:      dev,
		surface:  surface,
		renderer: renderer,
		streamer: client.NewStreamer(worldgen.New(seed), reg, max(1, runtime.NumCPU()-2)),
		depth:    newDepthTarget(dev, uint32(width), uint32(height)),
		camera:   camera,
		center:   cameraChunk(camera.Pos),
	}
	app.streamer.SetCenter(app.center, viewDistance)
	return app, nil
}

func fitFramebuffer(window *client.Window, targetWidth, targetHeight int) {
	contentWidth, contentHeight := window.ContentSize()
	framebufferWidth, framebufferHeight := window.FramebufferSize()
	if framebufferWidth <= 0 || framebufferHeight <= 0 {
		return
	}
	contentWidth = max(1, int(math.Round(float64(targetWidth*contentWidth)/float64(framebufferWidth))))
	contentHeight = max(1, int(math.Round(float64(targetHeight*contentHeight)/float64(framebufferHeight))))
	window.SetContentSize(contentWidth, contentHeight)
	window.Poll()
}

func (a *application) Close() {
	a.streamer.Close()
	a.renderer.Release()
	a.depth.Release()
	a.surface.Release()
	a.dev.Release()
	a.window.Close()
}

func (a *application) updateCenter() {
	center := cameraChunk(a.camera.Pos)
	if center == a.center {
		return
	}
	a.center = center
	a.streamer.SetCenter(center, viewDistance)
}

// frame 绘制一帧，返回 surface 是否实际取得了可呈现纹理。
func (a *application) frame(drainMax int) bool {
	width, height := a.window.FramebufferSize()
	if width == 0 || height == 0 {
		return false
	}
	if uint32(width) != a.depth.width || uint32(height) != a.depth.height {
		a.surface.Resize(uint32(width), uint32(height))
		a.depth.Release()
		a.depth = newDepthTarget(a.dev, uint32(width), uint32(height))
		a.renderer.Resize(uint32(width), uint32(height))
		a.camera.Aspect = float32(width) / float32(height)
	}

	a.renderer.BeginFrame()
	for _, result := range a.streamer.Drain(drainMax) {
		a.renderer.SetConnectivity(result.Pos, result.Conn)
		a.renderer.QueueSection(result.Pos, result.Quads)
	}
	a.renderer.FlushUploads(a.center)
	a.renderer.DropOutside(a.center, viewDistance)

	target := a.surface.Acquire()
	if target == nil {
		return false
	}
	encoder := a.dev.CreateCommandEncoder()
	a.renderer.Render(encoder, target, a.depth.view, render.Camera{
		ViewProj: a.camera.ViewProj(),
		Pos:      a.camera.Pos,
	})
	command := encoder.Finish()
	a.dev.Submit(command)
	command.Release()
	a.surface.Present()
	return true
}

func (a *application) framebufferLabel() string {
	width, height := a.window.FramebufferSize()
	return fmt.Sprintf("%dx%d", width, height)
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
