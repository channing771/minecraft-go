//go:build darwin

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"runtime"
	"sync"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/assets"
	"minecraft-go/internal/client"
	"minecraft-go/internal/core"
	"minecraft-go/internal/gfx"
	"minecraft-go/internal/network"
	"minecraft-go/internal/render"
	"minecraft-go/internal/server"
)

type application struct {
	window         *client.Window
	dev            gfx.Device
	surface        gfx.Surface
	renderer       *render.Renderer
	clientEndpoint network.ClientEndpoint
	server         *server.Server
	serverCancel   context.CancelFunc
	serverDone     chan error
	mirror         *client.Mirror
	mesher         *client.Mesher
	depth          *depthTarget
	camera         client.Camera
	center         core.ChunkPos
	sequence       uint64
	selectedBlock  core.BlockID
	loadedChunks   map[core.ChunkPos]struct{}
	closeOnce      sync.Once
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
	clientEndpoint, serverEndpoint := network.NewMemoryPair(256)
	config := server.DefaultConfig(seed)
	config.ViewRadius = viewDistance + 1
	running := server.NewEmbedded(config, serverEndpoint)
	serverContext, serverCancel := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	go func() { serverDone <- running.Run(serverContext) }()

	app := &application{
		window:         window,
		dev:            dev,
		surface:        surface,
		renderer:       renderer,
		clientEndpoint: clientEndpoint,
		server:         running,
		serverCancel:   serverCancel,
		serverDone:     serverDone,
		mirror:         client.NewMirror(),
		mesher:         client.NewMesher(reg, max(1, runtime.NumCPU()-2)),
		depth:          newDepthTarget(dev, uint32(width), uint32(height)),
		camera:         camera,
		center:         cameraChunk(camera.Pos),
		selectedBlock:  core.StoneID,
		loadedChunks:   make(map[core.ChunkPos]struct{}),
	}
	if err := app.sendViewCenter(app.center); err != nil {
		app.Close()
		return nil, fmt.Errorf("发送初始视距中心: %w", err)
	}
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
	a.closeOnce.Do(func() {
		_ = a.clientEndpoint.Close()
		a.serverCancel()
		if err := <-a.serverDone; err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("内置服务端退出: %v", err)
		}
		a.mesher.Close()
		a.renderer.Release()
		a.depth.Release()
		a.surface.Release()
		a.dev.Release()
		a.window.Close()
	})
}

func (a *application) updateCenter() {
	center := cameraChunk(a.camera.Pos)
	if center == a.center {
		return
	}
	a.center = center
	if err := a.sendViewCenter(center); err != nil {
		log.Printf("更新视距中心失败: %v", err)
	}
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
	a.drainServerMessages(drainMax)
	a.mesher.Schedule(a.mirror, drainMax)
	for _, result := range a.mesher.Drain(a.mirror, drainMax) {
		if result.Dimension != core.Overworld {
			continue
		}
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

func (a *application) drainServerMessages(maxMessages int) {
	if maxMessages <= 0 {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for range maxMessages {
		message, err := a.clientEndpoint.Recv(ctx)
		if errors.Is(err, context.Canceled) || errors.Is(err, network.ErrClosed) {
			return
		}
		if err != nil {
			log.Printf("接收内置服务端消息失败: %v", err)
			return
		}
		update, err := a.mirror.Apply(message)
		if err != nil {
			log.Printf("服务端协议数据非法，关闭会话: %v", err)
			_ = a.clientEndpoint.Close()
			a.serverCancel()
			return
		}
		switch message := message.(type) {
		case network.ChunkSnapshot:
			if message.Dimension == core.Overworld {
				a.loadedChunks[message.Chunk] = struct{}{}
			}
		case network.ForgetChunks:
			if message.Dimension == core.Overworld {
				for _, position := range message.Chunks {
					delete(a.loadedChunks, position)
				}
			}
		}
		if update.Resync != nil {
			a.sequence++
			update.Resync.Sequence = a.sequence
			if err := a.send(update.Resync); err != nil {
				log.Printf("发送区块 resync 失败: %v", err)
			}
		}
		if update.Rejected != nil {
			log.Printf("权威命令被拒绝: sequence=%d reason=%s",
				update.Rejected.Sequence, update.Rejected.Reason)
		}
		a.mesher.MarkDirty(update.Dirty...)
		for _, key := range update.Forgotten {
			if key.Dimension != core.Overworld {
				continue
			}
			a.renderer.DropSection(key.Pos)
			if key.Pos.Y == 0 {
				a.mesher.ForgetChunk(key.Dimension, core.ChunkPos{X: key.Pos.X, Z: key.Pos.Z})
			}
		}
	}
}

func (a *application) sendViewCenter(center core.ChunkPos) error {
	a.sequence++
	return a.send(network.SetViewCenter{
		Sequence:  a.sequence,
		Dimension: core.Overworld,
		Center:    center,
	})
}

func (a *application) breakBlock() {
	a.sequence++
	if err := a.send(network.BreakRay{
		Sequence:  a.sequence,
		Dimension: core.Overworld,
		Origin:    a.camera.Pos,
		Direction: a.camera.Forward(),
	}); err != nil {
		log.Printf("发送挖掘命令失败: %v", err)
	}
}

func (a *application) placeBlock() {
	a.sequence++
	if err := a.send(network.PlaceRay{
		Sequence:  a.sequence,
		Dimension: core.Overworld,
		Origin:    a.camera.Pos,
		Direction: a.camera.Forward(),
		Block:     a.selectedBlock,
	}); err != nil {
		log.Printf("发送放置命令失败: %v", err)
	}
}

func (a *application) send(message network.ClientMessage) error {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	return a.clientEndpoint.Send(ctx, message)
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
