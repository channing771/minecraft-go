package render

import (
	"math"
	"sort"

	"minecraft-go/internal/core"
	"minecraft-go/internal/mesh"
)

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
