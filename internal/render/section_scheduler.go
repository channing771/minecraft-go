//go:build darwin

package render

import (
	"math"
	"sort"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/mesh"
)

// SectionSink 接收已打包的 section face 字节(Rust 渲染器上传入口的抽象)。
type SectionSink interface {
	UploadSection(x, y, z int32, packed []byte)
	DropSection(x, y, z int32)
}

// SectionScheduler 复用 Go 渲染器的 mesh 上传调度语义(pending 覆盖、每帧
// 字节预算、近距优先、connectivity 登记),把结果冲刷进 SectionSink。
// GPU 池分配已由 Rust 渲染器内部处理,这里只保留 CPU 侧调度。
type SectionScheduler struct {
	sink         SectionSink
	budget       *UploadBudget
	pending      map[core.SectionPos][]mesh.Quad
	connectivity map[core.SectionPos]mesh.Connectivity
	uploaded     map[core.SectionPos]struct{}
	keys         []core.SectionPos
	packed       []byte
}

// NewSectionScheduler 创建调度器;budget 与旧渲染器同为每帧字节预算。
func NewSectionScheduler(sink SectionSink, uploadPerFrame uint32) *SectionScheduler {
	return &SectionScheduler{
		sink:         sink,
		budget:       NewUploadBudget(uploadPerFrame),
		pending:      make(map[core.SectionPos][]mesh.Quad),
		connectivity: make(map[core.SectionPos]mesh.Connectivity),
		uploaded:     make(map[core.SectionPos]struct{}),
	}
}

// BeginFrame 重置本帧上传预算。
func (s *SectionScheduler) BeginFrame() { s.budget.BeginFrame() }

// UploadBudget 返回共享的帧预算(字形冲刷等复用)。
func (s *SectionScheduler) UploadBudget() *UploadBudget { return s.budget }

// QueueSection 排队区段最新网格;空网格立即下沉为 drop。
func (s *SectionScheduler) QueueSection(p core.SectionPos, quads []mesh.Quad) {
	if len(quads) == 0 {
		delete(s.pending, p)
		if _, ok := s.uploaded[p]; ok {
			delete(s.uploaded, p)
			s.sink.DropSection(p.X, p.Y, p.Z)
		}
		return
	}
	s.pending[p] = append([]mesh.Quad(nil), quads...)
}

// SetConnectivity 登记区段六面连通性(全空气/全实心区段也必须登记)。
func (s *SectionScheduler) SetConnectivity(p core.SectionPos, c mesh.Connectivity) {
	s.connectivity[p] = c
}

// Connectivity 供可见性 BFS 查询。
func (s *SectionScheduler) Connectivity(p core.SectionPos) (mesh.Connectivity, bool) {
	c, ok := s.connectivity[p]
	return c, ok
}

// FlushUploads 按与中心区块的水平距离从近到远上传,预算耗尽即停。
func (s *SectionScheduler) FlushUploads(center core.ChunkPos) {
	s.keys = s.keys[:0]
	for p := range s.pending {
		s.keys = append(s.keys, p)
	}
	sort.Slice(s.keys, func(i, j int) bool {
		return schedulerDistance2(s.keys[i], center) < schedulerDistance2(s.keys[j], center)
	})
	for _, p := range s.keys {
		quads := s.pending[p]
		bytes := uint64(len(quads)) * 8
		if bytes > math.MaxUint32 || !s.budget.TryConsume(uint32(bytes)) {
			continue
		}
		if cap(s.packed) < len(quads)*8 {
			s.packed = make([]byte, 0, len(quads)*8)
		}
		s.packed = s.packed[:0]
		for _, q := range quads {
			value := q.Pack()
			for i := 0; i < 8; i++ {
				s.packed = append(s.packed, byte(value>>(8*i)))
			}
		}
		s.sink.UploadSection(p.X, p.Y, p.Z, s.packed)
		s.uploaded[p] = struct{}{}
		delete(s.pending, p)
	}
}

// PendingUploads 返回待冲刷的区段数,供测试与收敛循环。
func (s *SectionScheduler) PendingUploads() int { return len(s.pending) }

// DropOutside 丢弃视距外的 pending、connectivity 与已上传区段。
func (s *SectionScheduler) DropOutside(center core.ChunkPos, radius int) {
	for p := range s.pending {
		if schedulerOutside(p, center, radius) {
			delete(s.pending, p)
		}
	}
	for p := range s.uploaded {
		if schedulerOutside(p, center, radius) {
			delete(s.uploaded, p)
			s.sink.DropSection(p.X, p.Y, p.Z)
		}
	}
	for p := range s.connectivity {
		if schedulerOutside(p, center, radius) {
			delete(s.connectivity, p)
		}
	}
}

func schedulerDistance2(p core.SectionPos, center core.ChunkPos) int64 {
	dx := int64(p.X - center.X)
	dz := int64(p.Z - center.Z)
	return dx*dx + dz*dz
}

func schedulerOutside(p core.SectionPos, center core.ChunkPos, radius int) bool {
	return abs32Render(p.X-center.X) > int32(radius) || abs32Render(p.Z-center.Z) > int32(radius)
}
