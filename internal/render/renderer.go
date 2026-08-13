package render

import (
	_ "embed"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/assets"
	"minecraft-go/internal/core"
	"minecraft-go/internal/gfx"
	"minecraft-go/internal/mesh"
)

const (
	bytesPerPoolFace      = 8
	bytesPerVisibleFace   = 16
	defaultPoolFaces      = 4_500_000
	defaultOriginSlots    = 128 * 1024
	defaultUploadPerFrame = 4 << 20
)

//go:embed shader/terrain.wgsl
var terrainShader string

//go:embed shader/sky.wgsl
var skyShader string

// Camera 是 Renderer 每帧所需的相机数据。
type Camera struct {
	ViewProj    mgl32.Mat4
	ViewProjInv mgl32.Mat4
	Pos         mgl32.Vec3
	CloudOffset CloudOffset
	// Daylight 是本帧的固定昼夜亮度，SkyColor 是对应的天空背景色，
	// 两者都来自同一个 DayNightAt 相位。
	SunDirection   [3]float32
	Daylight       float32
	StarVisibility float32
	SkyColor       [4]float32
}

// FrameStats 是最近一次 Render 的 CPU 侧候选集统计。
// CandidateFaces 是 GPU 背面/Hi-Z 剔除前的实例上限。
type FrameStats struct {
	CandidateSections int
	CandidateBytes    int
	CandidateFaces    int
}

type sectionSlot struct {
	alloc     Alloc
	origin    [4]int32
	originIdx uint32
	packed    []uint64 // Task 13 的 CPU 展开路径；Task 14 会改由 compute 读取 faces。
}

// Renderer 管理区段面池、上传队列与单次 indirect draw。
type Renderer struct {
	dev    gfx.Device
	pool   *Pool
	budget *UploadBudget

	faces     gfx.Buffer
	instances gfx.Buffer
	origins   gfx.Buffer
	camera    gfx.Buffer
	skyCamera gfx.Buffer
	indirect  gfx.Buffer
	index     gfx.Buffer
	zeroArgs  gfx.Buffer
	cull      *culler
	hiz       *hiZ

	atlas       gfx.Texture
	atlasView   gfx.TextureView
	sampler     gfx.Sampler
	pipeline    gfx.RenderPipeline
	bind        gfx.BindGroup
	skyPipeline gfx.RenderPipeline
	skyBind     gfx.BindGroup

	sections map[core.SectionPos]sectionSlot
	pending  map[core.SectionPos][]mesh.Quad
	// connectivity 包含无可绘制面（全空气/全实心）的区段；
	// 可见性 BFS 需要它们作为通路或阻挡，不能只记录 sections。
	connectivity map[core.SectionPos]mesh.Connectivity

	maxOriginSlots uint32
	nextOrigin     uint32
	freeOrigins    []uint32

	haveLastCamera bool
	lastCamera     Camera
	lastFrameStats FrameStats

	visibilityScratch mesh.VisibilityScratch
	visibleSections   []core.SectionPos
	sectionRecords    []byte
	terrainCameraData [80]byte
	skyCameraData     [112]byte
}

// New 创建使用默认 M1 容量与 4 MB/帧上传预算的渲染器。
func New(dev gfx.Device, reg *assets.Registry, colorFmt gfx.TextureFormat) *Renderer {
	return newRenderer(dev, reg, colorFmt, defaultPoolFaces, defaultUploadPerFrame, defaultOriginSlots)
}

func newRenderer(
	dev gfx.Device,
	reg *assets.Registry,
	colorFmt gfx.TextureFormat,
	poolFaces, uploadPerFrame, originSlots uint32,
) *Renderer {
	r := &Renderer{
		dev:            dev,
		pool:           NewPool(poolFaces),
		budget:         NewUploadBudget(uploadPerFrame),
		sections:       make(map[core.SectionPos]sectionSlot),
		pending:        make(map[core.SectionPos][]mesh.Quad),
		connectivity:   make(map[core.SectionPos]mesh.Connectivity),
		maxOriginSlots: originSlots,
	}

	r.faces = dev.CreateBuffer(gfx.BufferDesc{
		Label: "terrain face pool",
		Size:  uint64(poolFaces) * bytesPerPoolFace,
		Usage: gfx.BufferUsageStorage | gfx.BufferUsageCopyDst | gfx.BufferUsageCopySrc,
	})
	r.instances = dev.CreateBuffer(gfx.BufferDesc{
		Label: "terrain visible instances",
		Size:  uint64(poolFaces) * bytesPerVisibleFace,
		Usage: gfx.BufferUsageStorage | gfx.BufferUsageCopyDst,
	})
	r.origins = dev.CreateBuffer(gfx.BufferDesc{
		Label: "terrain section origins",
		Size:  uint64(originSlots) * 16,
		Usage: gfx.BufferUsageStorage | gfx.BufferUsageCopyDst,
	})
	r.camera = dev.CreateBuffer(gfx.BufferDesc{
		Label: "terrain camera",
		Size:  80,
		Usage: gfx.BufferUsageUniform | gfx.BufferUsageCopyDst,
	})
	r.skyCamera = dev.CreateBuffer(gfx.BufferDesc{
		Label: "sky uniform",
		Size:  112,
		Usage: gfx.BufferUsageUniform | gfx.BufferUsageCopyDst,
	})
	r.indirect = dev.CreateBuffer(gfx.BufferDesc{
		Label: "terrain indirect args",
		Size:  5 * 4,
		Usage: gfx.BufferUsageIndirect | gfx.BufferUsageStorage | gfx.BufferUsageCopyDst,
	})
	r.index = dev.CreateBuffer(gfx.BufferDesc{
		Label: "terrain quad indices",
		Size:  6 * 4,
		Usage: gfx.BufferUsageIndex | gfx.BufferUsageCopyDst,
	})
	r.index.Write(0, uint32sToBytes([]uint32{0, 1, 2, 0, 2, 3}))
	r.zeroArgs = dev.CreateBuffer(gfx.BufferDesc{
		Label: "terrain zero indirect template",
		Size:  5 * 4,
		Usage: gfx.BufferUsageCopySrc | gfx.BufferUsageCopyDst,
	})
	r.zeroArgs.Write(0, uint32sToBytes([]uint32{6, 0, 0, 0, 0}))
	r.cull = newCuller(dev, r.faces, r.instances, r.indirect, originSlots)

	r.atlas, r.sampler = reg.UploadTo(dev)
	r.atlasView = r.atlas.View(gfx.TextureViewDesc{
		Dimension: gfx.TextureViewDimension2DArray,
	})

	layout := gfx.BindGroupLayout{
		Label: "terrain layout",
		Entries: []gfx.BindGroupLayoutEntry{
			{Binding: 0, Type: gfx.BindingUniformBuffer, VisibleIn: gfx.StageVertex},
			{Binding: 1, Type: gfx.BindingStorageBufferRO, VisibleIn: gfx.StageVertex},
			{Binding: 2, Type: gfx.BindingStorageBufferRO, VisibleIn: gfx.StageVertex},
			{
				Binding: 3, Type: gfx.BindingSampledTextureFloat,
				VisibleIn: gfx.StageFragment, ViewDimension: gfx.TextureViewDimension2DArray,
			},
			{Binding: 4, Type: gfx.BindingSampler, VisibleIn: gfx.StageFragment},
		},
	}
	module := dev.CreateShaderModule(terrainShader)
	r.pipeline = dev.CreateRenderPipeline(gfx.RenderPipelineDesc{
		Label:         "terrain",
		Shader:        module,
		VertexEntry:   "vs_main",
		FragmentEntry: "fs_main",
		BindGroups:    []gfx.BindGroupLayout{layout},
		ColorFormat:   colorFmt,
		DepthFormat:   gfx.FormatDepth32Float,
		DepthWrite:    true,
	})
	module.Release()

	r.bind = dev.CreateBindGroup(gfx.BindGroupDesc{
		Label:  "terrain resources",
		Layout: layout,
		Entries: []gfx.BindGroupEntry{
			{Binding: 0, Buffer: r.camera},
			{Binding: 1, Buffer: r.instances},
			{Binding: 2, Buffer: r.origins},
			{Binding: 3, Texture: r.atlasView},
			{Binding: 4, Sampler: r.sampler},
		},
	})

	skyLayout := gfx.BindGroupLayout{
		Label: "sky layout",
		Entries: []gfx.BindGroupLayoutEntry{
			{Binding: 0, Type: gfx.BindingUniformBuffer, VisibleIn: gfx.StageVertex | gfx.StageFragment},
		},
	}
	skyModule := dev.CreateShaderModule(skyShader)
	r.skyPipeline = dev.CreateRenderPipeline(gfx.RenderPipelineDesc{
		Label:         "sky",
		Shader:        skyModule,
		VertexEntry:   "vs_main",
		FragmentEntry: "fs_main",
		BindGroups:    []gfx.BindGroupLayout{skyLayout},
		ColorFormat:   colorFmt,
		DepthFormat:   gfx.FormatDepth32Float,
		DepthWrite:    false,
	})
	skyModule.Release()
	r.skyBind = dev.CreateBindGroup(gfx.BindGroupDesc{
		Label:  "sky resources",
		Layout: skyLayout,
		Entries: []gfx.BindGroupEntry{
			{Binding: 0, Buffer: r.skyCamera},
		},
	})
	return r
}

// BeginFrame 重置本帧上传预算。
func (r *Renderer) BeginFrame() { r.budget.BeginFrame() }

// UploadBudget returns the frame-scoped budget shared by terrain and glyph uploads.
func (r *Renderer) UploadBudget() *UploadBudget { return r.budget }

func (r *Renderer) PendingUploads() int { return len(r.pending) }

func (r *Renderer) LastFrameStats() FrameStats { return r.lastFrameStats }

// Release 释放 Renderer 持有的全部 GPU 资源。
func (r *Renderer) Release() {
	if r.dev == nil {
		return
	}
	defer func() { r.dev = nil }()
	if r.cull != nil {
		r.cull.Release()
	}
	if r.hiz != nil {
		r.hiz.Release()
	}
	if r.bind != nil {
		r.bind.Release()
	}
	if r.skyBind != nil {
		r.skyBind.Release()
	}
	if r.pipeline != nil {
		r.pipeline.Release()
	}
	if r.skyPipeline != nil {
		r.skyPipeline.Release()
	}
	if r.atlasView != nil {
		r.atlasView.Release()
	}
	if r.sampler != nil {
		r.sampler.Release()
	}
	if r.atlas != nil {
		r.atlas.Release()
	}
	for _, b := range []gfx.Buffer{
		r.zeroArgs, r.index, r.indirect, r.skyCamera, r.camera, r.origins, r.instances, r.faces,
	} {
		if b != nil {
			b.Release()
		}
	}
}
