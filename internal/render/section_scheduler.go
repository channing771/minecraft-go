//go:build darwin

package render

import (
	"cmp"
	"math"
	"slices"

	"github.com/channing771/mornlea/internal/assets"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/mesh"
)

// SectionSink 接收已打包的 section face 字节(Rust 渲染器上传入口的抽象)。
//
// 两条流按 material 分开:opaque 走接 GPU culling 的单次 indirect terrain
// draw,water 走独立的半透明 water pass。两条流的元素格式完全相同(8 字节
// packed quad),分流只是分组,不改动任何一条 quad。
type SectionSink interface {
	UploadSection(x, y, z int32, opaque, water []byte)
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
	uploaded     map[core.SectionPos]int
	keys         []core.SectionPos
	packed       []byte
	// water 是水面 quad 的独立打包缓冲,与 packed 同为跨帧复用的 scratch,
	// 保证预热后逐帧零分配。
	water []byte
}

// NewSectionScheduler 创建调度器;budget 与旧渲染器同为每帧字节预算。
func NewSectionScheduler(sink SectionSink, uploadPerFrame uint32) *SectionScheduler {
	return &SectionScheduler{
		sink:         sink,
		budget:       NewUploadBudget(uploadPerFrame),
		pending:      make(map[core.SectionPos][]mesh.Quad),
		connectivity: make(map[core.SectionPos]mesh.Connectivity),
		uploaded:     make(map[core.SectionPos]int),
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
	// 用 slices.SortFunc 而非 sort.Slice:后者要为 reflect swapper 逃逸一次闭包,
	// 每帧一次堆分配;而 water pass 的边界写死了「预热后不产生每帧堆分配」。
	slices.SortFunc(s.keys, func(a, b core.SectionPos) int {
		return cmp.Compare(schedulerDistance2(a, center), schedulerDistance2(b, center))
	})
	for _, p := range s.keys {
		quads := s.pending[p]
		bytes := uint64(len(quads)) * 8
		if bytes > math.MaxUint32 || !s.budget.TryConsume(uint32(bytes)) {
			continue
		}
		// 两条 scratch 都按最坏情况(整段全是同一类)预留,避免分流后
		// 因比例波动反复扩容。
		if cap(s.packed) < len(quads)*8 {
			s.packed = make([]byte, 0, len(quads)*8)
		}
		if cap(s.water) < len(quads)*8 {
			s.water = make([]byte, 0, len(quads)*8)
		}
		s.packed, s.water = s.packed[:0], s.water[:0]
		for _, q := range quads {
			// 按 material 分流:水面 quad 进 water 流。判别只看材质层,
			// 不看角高度——水的**底面**不带角高度(四角全 0),靠角高度
			// 判别会把它漏回不透明 pass。
			target := &s.packed
			if q.Mat == assets.LayerWater {
				target = &s.water
			}
			value := q.Pack()
			for i := 0; i < 8; i++ {
				*target = append(*target, byte(value>>(8*i)))
			}
		}
		s.sink.UploadSection(p.X, p.Y, p.Z, s.packed, s.water)
		s.uploaded[p] = len(quads)
		delete(s.pending, p)
	}
}

// PendingUploads 返回待冲刷的区段数,供测试与收敛循环。
func (s *SectionScheduler) PendingUploads() int { return len(s.pending) }

// FrameStats 按本帧可见列表计算候选统计(镜像旧渲染器 LastFrameStats:
// 已上传且可见的 section 数、record 字节与面数)。
func (s *SectionScheduler) FrameStats(visible []core.SectionPos) FrameStats {
	stats := FrameStats{}
	for _, p := range visible {
		faces, ok := s.uploaded[p]
		if !ok {
			continue
		}
		stats.CandidateSections++
		stats.CandidateBytes += 32
		stats.CandidateFaces += faces
	}
	return stats
}

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
	return schedulerAbs32(p.X-center.X) > int32(radius) || schedulerAbs32(p.Z-center.Z) > int32(radius)
}

func schedulerAbs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// FrameStats 是最近一帧的 CPU 侧候选统计(承接旧渲染器的同名结构)。
type FrameStats struct {
	CandidateSections int
	CandidateBytes    int
	CandidateFaces    int
}
