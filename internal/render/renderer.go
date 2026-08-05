package render

import (
	_ "embed"
	"encoding/binary"
	"math"
	"sort"

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
	skyCameraData     [96]byte
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
		Size:  96,
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

// QueueSection 排队一个区段的最新网格；同位置的新结果覆盖旧 pending 结果。
func (r *Renderer) QueueSection(p core.SectionPos, quads []mesh.Quad) {
	if len(quads) == 0 {
		r.DropSection(p)
		return
	}
	r.pending[p] = append([]mesh.Quad(nil), quads...)
}

// SetConnectivity 登记区段的六面连通性。即使区段没有可绘制面也必须登记，
// 因为全空气区段是 BFS 通路，全实心区段是阻挡。
func (r *Renderer) SetConnectivity(p core.SectionPos, c mesh.Connectivity) {
	r.connectivity[p] = c
}

// Resize 重建与 viewport 尺寸相关的 Hi-Z 金字塔。
func (r *Renderer) Resize(width, height uint32) {
	if width == 0 || height == 0 {
		return
	}
	if r.hiz != nil && r.hiz.viewportW == width && r.hiz.viewportH == height {
		return
	}
	if r.hiz != nil {
		r.hiz.Release()
	}
	r.hiz = newHiZ(r.dev, width, height)
	r.haveLastCamera = false
}

// FlushUploads 按与中心区块的水平距离从近到远上传。
func (r *Renderer) FlushUploads(center core.ChunkPos) {
	keys := make([]core.SectionPos, 0, len(r.pending))
	for p := range r.pending {
		keys = append(keys, p)
	}
	sort.Slice(keys, func(i, j int) bool {
		return sectionDistance2(keys[i], center) < sectionDistance2(keys[j], center)
	})

	for _, p := range keys {
		quads, ok := r.pending[p]
		if !ok {
			continue
		}
		bytes := uint64(len(quads)) * bytesPerPoolFace
		if bytes > math.MaxUint32 || !r.budget.TryConsume(uint32(bytes)) {
			continue
		}
		if r.uploadOne(p, quads) {
			delete(r.pending, p)
		}
	}
}

func sectionDistance2(p core.SectionPos, center core.ChunkPos) int64 {
	dx := int64(p.X - center.X)
	dz := int64(p.Z - center.Z)
	return dx*dx + dz*dz
}

func (r *Renderer) uploadOne(p core.SectionPos, quads []mesh.Quad) bool {
	required := uint32(len(quads))
	old, existed := r.sections[p]

	var alloc Alloc
	oldFreed := false
	if existed && required <= old.alloc.Size {
		alloc = old.alloc
	} else {
		var ok bool
		alloc, ok = r.pool.Alloc(required)
		if !ok && existed {
			r.pool.Free(old.alloc)
			oldFreed = true
			delete(r.sections, p)
			alloc, ok = r.pool.Alloc(required)
			if !ok {
				r.releaseOrigin(old.originIdx)
				return false
			}
		} else if !ok {
			return false
		}
		if existed && !oldFreed {
			r.pool.Free(old.alloc)
		}
	}

	originIdx := old.originIdx
	if !existed {
		var ok bool
		originIdx, ok = r.takeOrigin()
		if !ok {
			r.pool.Free(alloc)
			return false
		}
	}

	packed := make([]uint64, len(quads))
	for i, q := range quads {
		packed[i] = q.Pack()
	}
	r.faces.Write(uint64(alloc.Offset)*bytesPerPoolFace, uint64sToBytes(packed))

	min := p.MinCorner()
	origin := [4]int32{min.X, min.Y, min.Z, 0}
	r.origins.Write(uint64(originIdx)*16, int32sToBytes(origin[:]))
	r.sections[p] = sectionSlot{
		alloc: alloc, origin: origin, originIdx: originIdx, packed: packed,
	}
	return true
}

func (r *Renderer) takeOrigin() (uint32, bool) {
	if n := len(r.freeOrigins); n > 0 {
		idx := r.freeOrigins[n-1]
		r.freeOrigins = r.freeOrigins[:n-1]
		return idx, true
	}
	if r.nextOrigin >= r.maxOriginSlots {
		return 0, false
	}
	idx := r.nextOrigin
	r.nextOrigin++
	return idx, true
}

func (r *Renderer) releaseOrigin(idx uint32) {
	r.freeOrigins = append(r.freeOrigins, idx)
}

func (r *Renderer) PendingUploads() int { return len(r.pending) }

func (r *Renderer) LastFrameStats() FrameStats { return r.lastFrameStats }

func (r *Renderer) DropSection(p core.SectionPos) {
	delete(r.pending, p)
	if slot, ok := r.sections[p]; ok {
		r.pool.Free(slot.alloc)
		r.releaseOrigin(slot.originIdx)
		delete(r.sections, p)
	}
}

func (r *Renderer) DropOutside(center core.ChunkPos, radius int) {
	for p := range r.pending {
		if abs32Render(p.X-center.X) > int32(radius) || abs32Render(p.Z-center.Z) > int32(radius) {
			delete(r.pending, p)
		}
	}
	for p := range r.sections {
		if abs32Render(p.X-center.X) > int32(radius) || abs32Render(p.Z-center.Z) > int32(radius) {
			r.DropSection(p)
		}
	}
	for p := range r.connectivity {
		if abs32Render(p.X-center.X) > int32(radius) || abs32Render(p.Z-center.Z) > int32(radius) {
			delete(r.connectivity, p)
		}
	}
}

func abs32Render(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// Render 在 CPU 上只遍历候选区段，GPU 完成逐面背面剔除、实例压缩与计数。
func (r *Renderer) Render(
	enc gfx.CommandEncoder,
	target, depth gfx.TextureView,
	cam Camera,
) {
	origin := cameraSection(cam.Pos)
	frustum := core.FrustumFrom(cam.ViewProj)
	r.visibleSections = mesh.VisibleSectionsInto(
		r.visibleSections, &r.visibilityScratch, origin, 32, frustum,
		func(p core.SectionPos) (mesh.Connectivity, bool) {
			c, ok := r.connectivity[p]
			return c, ok
		})

	records := r.sectionRecords[:0]
	if available := cap(records); available < len(r.visibleSections)*sectionRecordBytes {
		records = make([]byte, 0, len(r.visibleSections)*sectionRecordBytes)
	}
	candidates := 0
	candidateFaces := 0
	for _, p := range r.visibleSections {
		slot, ok := r.sections[p]
		if !ok || len(slot.packed) == 0 {
			continue
		}
		offset := len(records)
		records = append(records, make([]byte, sectionRecordBytes)...)
		rec := records[offset:]
		for i, v := range slot.origin {
			binary.LittleEndian.PutUint32(rec[i*4:], uint32(v))
		}
		binary.LittleEndian.PutUint32(rec[16:], slot.alloc.Offset)
		binary.LittleEndian.PutUint32(rec[20:], uint32(len(slot.packed)))
		binary.LittleEndian.PutUint32(rec[24:], slot.originIdx)
		candidates++
		candidateFaces += len(slot.packed)
	}
	r.sectionRecords = records
	r.lastFrameStats = FrameStats{
		CandidateSections: candidates,
		CandidateBytes:    len(records),
		CandidateFaces:    candidateFaces,
	}
	if len(records) > 0 {
		r.cull.sections.Write(0, records)
	}
	writeCameraBytes(r.terrainCameraData[:], cam)
	r.camera.Write(0, r.terrainCameraData[:])
	writeSkyCameraBytes(r.skyCameraData[:], cam)
	r.skyCamera.Write(0, r.skyCameraData[:])
	enc.CopyBufferToBuffer(r.zeroArgs, 0, r.indirect, 0, 20)
	useHiZ := r.hiz != nil && r.hiz.valid && r.haveLastCamera &&
		cameraStable(r.lastCamera, cam)
	r.cull.dispatchCamera(enc, candidates, cam, r.hiz, useHiZ)

	pass := enc.BeginRenderPass(gfx.RenderPassDesc{
		Label:      "terrain pass",
		ColorView:  target,
		DepthView:  depth,
		ClearColor: cam.SkyColor,
		LoadClear:  true,
	})
	pass.SetPipeline(r.skyPipeline)
	pass.SetBindGroup(0, r.skyBind)
	pass.Draw(3, 1)
	pass.SetPipeline(r.pipeline)
	pass.SetBindGroup(0, r.bind)
	pass.SetIndexBuffer(r.index, 0)
	pass.DrawIndexedIndirect(r.indirect, 0)
	pass.End()

	if r.hiz != nil {
		r.hiz.build(enc, depth)
		r.lastCamera = cam
		r.haveLastCamera = true
	}
}

// cameraStable 采取保守策略：任何可辨认的矩阵变化都禁用一帧 Hi-Z。
// 这比 1 方块/2° 阈值更严格，只会少剔除，不会因历史深度错位制造破洞。
func cameraStable(a, b Camera) bool {
	if b.Pos.Sub(a.Pos).Len() > 1 {
		return false
	}
	return a.ViewProj.ApproxEqualThreshold(b.ViewProj, 1e-5)
}

func cameraSection(pos mgl32.Vec3) core.SectionPos {
	block := core.BlockPos{
		X: int32(math.Floor(float64(pos[0]))),
		Y: int32(math.Floor(float64(pos[1]))),
		Z: int32(math.Floor(float64(pos[2]))),
	}
	y := int32(block.SectionIndex())
	if y < 0 {
		y = 0
	} else if y >= core.SectionsPerChunk {
		y = core.SectionsPerChunk - 1
	}
	return core.SectionPos{X: block.Chunk().X, Y: y, Z: block.Chunk().Z}
}

func writeCameraBytes(out []byte, cam Camera) {
	for i, value := range cam.ViewProj {
		binary.LittleEndian.PutUint32(out[i*4:], math.Float32bits(value))
	}
	for i, value := range [...]float32{cam.Pos[0], cam.Pos[1], cam.Pos[2], cam.Daylight} {
		binary.LittleEndian.PutUint32(out[64+i*4:], math.Float32bits(value))
	}
}

func writeSkyCameraBytes(out []byte, cam Camera) {
	for i, value := range cam.ViewProjInv {
		binary.LittleEndian.PutUint32(out[i*4:], math.Float32bits(value))
	}
	for i, value := range [...]float32{
		cam.SunDirection[0], cam.SunDirection[1], cam.SunDirection[2], cam.Daylight,
		cam.StarVisibility,
	} {
		binary.LittleEndian.PutUint32(out[64+i*4:], math.Float32bits(value))
	}
}

func uint32sToBytes(values []uint32) []byte {
	out := make([]byte, len(values)*4)
	for i, v := range values {
		binary.LittleEndian.PutUint32(out[i*4:], v)
	}
	return out
}

func int32sToBytes(values []int32) []byte {
	out := make([]byte, len(values)*4)
	for i, v := range values {
		binary.LittleEndian.PutUint32(out[i*4:], uint32(v))
	}
	return out
}

func uint64sToBytes(values []uint64) []byte {
	out := make([]byte, len(values)*8)
	for i, v := range values {
		binary.LittleEndian.PutUint64(out[i*8:], v)
	}
	return out
}

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
