package render

import (
	"encoding/binary"
	"math"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/gfx"
	"minecraft-go/internal/mesh"
)

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
	binary.LittleEndian.PutUint32(out[84:], cam.CloudOffset.MacroX)
	for i, value := range [...]float32{cam.Pos[0], cam.Pos[1], cam.Pos[2], cam.CloudOffset.Local} {
		binary.LittleEndian.PutUint32(out[96+i*4:], math.Float32bits(value))
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
