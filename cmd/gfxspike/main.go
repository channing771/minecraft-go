//go:build darwin

// Command gfxspike 是图形技术验证:以 Rust 渲染器绘制固定 8×8 生成地形。
// R2c 切换后它是 windowed 渲染路径最小化的自证程序,不再接触任何 GPU 绑定。
package main

import (
	"log"
	"log/slog"
	"runtime"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/assets"
	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/mesh"
	"github.com/channing771/mornlea/internal/render"
	"github.com/channing771/mornlea/internal/world"
	"github.com/channing771/mornlea/internal/worldgen"
)

func init() {
	runtime.LockOSThread()
}

func main() {
	win, err := client.NewWindow(1280, 720, "Mornlea — M1 terrain")
	if err != nil {
		log.Fatalf("创建窗口失败: %v", err)
	}
	defer win.Close()

	renderer, err := client.NewWindowedRenderer(win)
	if err != nil {
		log.Fatalf("创建渲染器失败: %v", err)
	}
	defer renderer.Close()

	reg := assets.NewRegistry()
	layers, pixels := reg.AtlasPixels()
	renderer.UploadAtlas(layers, pixels)
	scheduler := render.NewSectionScheduler(renderer, 4<<20)

	chunks := generateTerrain()
	connectivity := queueMeshes(scheduler, reg, chunks)
	slog.Info("terrain 就绪", "chunks", len(chunks), "pendingMeshes", scheduler.PendingUploads())

	width, height := win.FramebufferSize()
	var scratch mesh.VisibilityScratch
	var visible []core.SectionPos
	for !win.ShouldClose() {
		win.Poll()

		w, h := win.FramebufferSize()
		if w == 0 || h == 0 {
			continue
		}
		if w != width || h != height {
			renderer.Resize(w, h)
			width, height = w, h
		}

		scheduler.BeginFrame()
		scheduler.FlushUploads(core.ChunkPos{X: 4, Z: 4})

		frame := fixedFrame(float32(w) / float32(h))
		visible = mesh.VisibleSectionsInto(
			visible[:0], &scratch,
			core.SectionPos{X: 4, Y: core.SectionsPerChunk - 1, Z: 4}, 32,
			core.FrustumFrom(frame.ViewProj),
			func(p core.SectionPos) (mesh.Connectivity, bool) {
				c, ok := connectivity[p]
				return c, ok
			})
		for _, p := range visible {
			frame.Visible = append(frame.Visible, [3]int32{p.X, p.Y, p.Z})
		}
		renderer.RenderFrame(frame)
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

func queueMeshes(
	scheduler *render.SectionScheduler,
	reg *assets.Registry,
	chunks map[core.ChunkPos]*world.Chunk,
) map[core.SectionPos]mesh.Connectivity {
	get := func(p core.ChunkPos) *world.Chunk { return chunks[p] }
	light := mesh.NewLightScratch()
	connectivity := make(map[core.SectionPos]mesh.Connectivity)
	for pos := range chunks {
		for si := 0; si < core.SectionsPerChunk; si++ {
			n := world.NeighborhoodAt(get, pos, si)
			sectionPos := core.SectionPos{X: pos.X, Y: int32(si), Z: pos.Z}
			conn := mesh.ComputeConnectivity(n.Center, reg)
			connectivity[sectionPos] = conn
			scheduler.SetConnectivity(sectionPos, conn)
			quads := mesh.MeshSection(n, reg, light)
			if len(quads) == 0 {
				continue
			}
			scheduler.QueueSection(sectionPos, quads)
		}
	}
	return connectivity
}

func fixedFrame(aspect float32) client.RenderFrame {
	pos := mgl32.Vec3{96, 140, 96}
	target := mgl32.Vec3{64, 48, 64}
	view := mgl32.LookAtV(pos, target, mgl32.Vec3{0, 1, 0})
	proj := core.Perspective(mgl32.DegToRad(55), aspect, 0.1, 1000)
	viewProj := proj.Mul4(view)
	noon := render.DayNightAt(6000)
	return client.RenderFrame{
		ViewProj:       viewProj,
		ViewProjInv:    viewProj.Inv(),
		Pos:            pos,
		SunDirection:   noon.SunDirection,
		Daylight:       noon.Daylight,
		StarVisibility: noon.StarVisibility,
		SkyColor:       noon.ClearColor,
	}
}
