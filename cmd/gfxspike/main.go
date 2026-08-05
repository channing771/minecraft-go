//go:build darwin

// Command gfxspike 是 M1 的第一帧地形验证程序。
//
// 它生成 8×8 个确定性区块，贪心网格化后通过单次 indirect draw 渲染。
// 所有 GPU 调用都经过 internal/gfx，main 不依赖底层 WebGPU 绑定。
package main

import (
	"log"
	"runtime"
	"unsafe"

	"github.com/go-gl/glfw/v3.3/glfw"
	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/assets"
	"minecraft-go/internal/core"
	"minecraft-go/internal/gfx"
	"minecraft-go/internal/mesh"
	"minecraft-go/internal/render"
	"minecraft-go/internal/world"
	"minecraft-go/internal/worldgen"
)

func init() {
	runtime.LockOSThread()
}

func main() {
	if err := glfw.Init(); err != nil {
		log.Fatalf("glfw 初始化失败: %v", err)
	}
	defer glfw.Terminate()

	glfw.WindowHint(glfw.ClientAPI, glfw.NoAPI)
	win, err := glfw.CreateWindow(1280, 720, "minecraft-go — M1 terrain", nil, nil)
	if err != nil {
		log.Fatalf("创建窗口失败: %v", err)
	}
	defer win.Destroy()

	fbWidth, fbHeight := win.GetFramebufferSize()
	dev, surface, err := gfx.NewDevice(cocoaHandle(win), uint32(fbWidth), uint32(fbHeight))
	if err != nil {
		log.Fatalf("创建 GPU 设备失败: %v", err)
	}
	defer dev.Release()
	defer surface.Release()

	reg := assets.NewRegistry()
	renderer := render.New(dev, reg, surface.Format())
	defer renderer.Release()
	renderer.Resize(uint32(fbWidth), uint32(fbHeight))

	chunks := generateTerrain()
	queueMeshes(renderer, reg, chunks)
	log.Printf("terrain: 已生成 %d 个区块，排队 %d 个区段网格", len(chunks), renderer.PendingUploads())

	depth := newDepthTarget(dev, uint32(fbWidth), uint32(fbHeight))
	defer depth.Release()

	for !win.ShouldClose() {
		glfw.PollEvents()

		w, h := win.GetFramebufferSize()
		if w == 0 || h == 0 {
			continue
		}
		surface.Resize(uint32(w), uint32(h))
		if depth.width != uint32(w) || depth.height != uint32(h) {
			depth.Release()
			depth = newDepthTarget(dev, uint32(w), uint32(h))
			renderer.Resize(uint32(w), uint32(h))
		}

		renderer.BeginFrame()
		renderer.FlushUploads(core.ChunkPos{X: 4, Z: 4})

		target := surface.Acquire()
		if target == nil {
			continue
		}

		cam := fixedCamera(float32(w) / float32(h))
		encoder := dev.CreateCommandEncoder()
		renderer.Render(encoder, target, depth.view, cam)
		cmd := encoder.Finish()
		dev.Submit(cmd)
		cmd.Release()
		surface.Present()
	}
}

func generateTerrain() map[core.ChunkPos]*world.Chunk {
	gen := worldgen.New(42)
	chunks := make(map[core.ChunkPos]*world.Chunk, 8*8)
	for cx := int32(0); cx < 8; cx++ {
		for cz := int32(0); cz < 8; cz++ {
			pos := core.ChunkPos{X: cx, Z: cz}
			chunks[pos] = gen.GenerateChunk(pos)
		}
	}
	return chunks
}

func queueMeshes(r *render.Renderer, reg *assets.Registry, chunks map[core.ChunkPos]*world.Chunk) {
	get := func(p core.ChunkPos) *world.Chunk { return chunks[p] }
	for pos := range chunks {
		for si := 0; si < core.SectionsPerChunk; si++ {
			n := world.NeighborhoodAt(get, pos, si)
			sectionPos := core.SectionPos{X: pos.X, Y: int32(si), Z: pos.Z}
			r.SetConnectivity(sectionPos, mesh.ComputeConnectivity(n.Center, reg))
			quads := mesh.MeshSection(n, reg)
			if len(quads) == 0 {
				continue
			}
			r.QueueSection(sectionPos, quads)
		}
	}
}

func fixedCamera(aspect float32) render.Camera {
	pos := mgl32.Vec3{96, 140, 96}
	target := mgl32.Vec3{64, 48, 64}
	view := mgl32.LookAtV(pos, target, mgl32.Vec3{0, 1, 0})
	proj := core.Perspective(mgl32.DegToRad(55), aspect, 0.1, 1000)
	viewProj := proj.Mul4(view)
	noon := render.DayNightAt(6000)
	return render.Camera{
		ViewProj:       viewProj,
		ViewProjInv:    viewProj.Inv(),
		Pos:            pos,
		SunDirection:   noon.SunDirection,
		Daylight:       noon.Daylight,
		StarVisibility: noon.StarVisibility,
		SkyColor:       noon.ClearColor,
	}
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
	return &depthTarget{texture: texture, view: view, width: width, height: height}
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

func cocoaHandle(win *glfw.Window) gfx.NativeWindowHandle {
	return gfx.NativeWindowHandle{
		Kind:    gfx.HandleKindNSWindow,
		Pointer: uintptr(unsafe.Pointer(win.GetCocoaWindow())),
	}
}
