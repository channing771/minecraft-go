package client

import (
	"log/slog"
	"sort"
	"sync"

	"minecraft-go/internal/assets"
	"minecraft-go/internal/core"
	"minecraft-go/internal/mesh"
	"minecraft-go/internal/world"
	"minecraft-go/internal/worldgen"
)

// MeshedSection 是一个已生成并网格化好的区段。
type MeshedSection struct {
	Pos        core.SectionPos
	Quads      []mesh.Quad
	Conn       mesh.Connectivity
	Generation uint64
}

// StreamStats 是流式加载器的只读诊断快照。
type StreamStats struct {
	CachedChunks, QueuedJobs, InFlightJobs int
	Generation                             uint64
}

type meshJob struct {
	center     core.ChunkPos
	chunks     map[core.ChunkPos]*world.Chunk
	generation uint64
}

// Streamer 在固定 worker 池上并行生成区块，并在 3×3 邻域齐备后网格化。
type Streamer struct {
	gen *worldgen.Generator
	reg *assets.Registry

	jobs    chan core.ChunkPos
	results chan MeshedSection
	closed  chan struct{}

	mu         sync.Mutex
	chunks     map[core.ChunkPos]*world.Chunk
	queued     map[core.ChunkPos]uint64
	inFlight   map[core.ChunkPos]uint64
	wanted     map[core.ChunkPos]bool
	meshed     map[core.ChunkPos]bool
	panicAt    map[core.ChunkPos]bool
	center     core.ChunkPos
	radius     int
	generation uint64

	wg        sync.WaitGroup
	closeOnce sync.Once
}

// NewStreamer 创建并启动流式加载器。
func NewStreamer(gen *worldgen.Generator, reg *assets.Registry, workers int) *Streamer {
	if workers < 1 {
		workers = 1
	}
	s := &Streamer{
		gen:      gen,
		reg:      reg,
		jobs:     make(chan core.ChunkPos, 16384),
		results:  make(chan MeshedSection, 4096),
		closed:   make(chan struct{}),
		chunks:   make(map[core.ChunkPos]*world.Chunk),
		queued:   make(map[core.ChunkPos]uint64),
		inFlight: make(map[core.ChunkPos]uint64),
		wanted:   make(map[core.ChunkPos]bool),
		meshed:   make(map[core.ChunkPos]bool),
		panicAt:  make(map[core.ChunkPos]bool),
	}
	s.wg.Add(workers)
	for range workers {
		go s.work()
	}
	return s
}

func (s *Streamer) work() {
	defer s.wg.Done()
	for {
		select {
		case <-s.closed:
			return
		case pos := <-s.jobs:
			s.handleOne(pos)
		}
	}
}

// handleOne 隔离单个生成任务的 panic，保证 worker 可以继续服务。
func (s *Streamer) handleOne(pos core.ChunkPos) {
	claimed := false
	defer func() {
		if r := recover(); r != nil {
			slog.Error("区块生成失败", "chunk", pos, "panic", r)
		}
		if claimed {
			s.mu.Lock()
			delete(s.inFlight, pos)
			s.mu.Unlock()
		}
	}()

	s.mu.Lock()
	delete(s.queued, pos)
	if !s.wanted[pos] || s.chunks[pos] != nil || s.inFlight[pos] != 0 {
		s.mu.Unlock()
		return
	}
	s.inFlight[pos] = s.generation
	claimed = true
	shouldPanic := s.panicAt[pos]
	delete(s.panicAt, pos)
	s.mu.Unlock()

	if shouldPanic {
		panic("测试注入的区块生成故障")
	}
	chunk := s.gen.GenerateChunk(pos)

	s.mu.Lock()
	delete(s.inFlight, pos)
	claimed = false
	if !s.wanted[pos] {
		s.mu.Unlock()
		return
	}
	s.chunks[pos] = chunk
	candidates := make([]core.ChunkPos, 0, 9)
	for dx := int32(-1); dx <= 1; dx++ {
		for dz := int32(-1); dz <= 1; dz++ {
			candidates = append(candidates, core.ChunkPos{X: pos.X + dx, Z: pos.Z + dz})
		}
	}
	meshJobs := s.claimReadyLocked(candidates)
	s.mu.Unlock()

	s.emitMeshJobs(meshJobs)
}

// claimReadyLocked 把邻域齐备的可见区块原子地标记为已网格化并快照输入。
func (s *Streamer) claimReadyLocked(candidates []core.ChunkPos) []meshJob {
	out := make([]meshJob, 0, len(candidates))
	for _, center := range candidates {
		if s.meshed[center] || chunkDistance(center, s.center) > s.radius {
			continue
		}
		ready := true
		snapshot := make(map[core.ChunkPos]*world.Chunk, 9)
		for dx := int32(-1); dx <= 1 && ready; dx++ {
			for dz := int32(-1); dz <= 1; dz++ {
				p := core.ChunkPos{X: center.X + dx, Z: center.Z + dz}
				ch := s.chunks[p]
				if ch == nil {
					ready = false
					break
				}
				snapshot[p] = ch
			}
		}
		if !ready {
			continue
		}
		s.meshed[center] = true
		out = append(out, meshJob{
			center:     center,
			chunks:     snapshot,
			generation: s.generation,
		})
	}
	return out
}

func (s *Streamer) emitMeshJobs(jobs []meshJob) {
	for _, job := range jobs {
		get := func(pos core.ChunkPos) *world.Chunk { return job.chunks[pos] }
		for si := 0; si < core.SectionsPerChunk; si++ {
			n := world.NeighborhoodAt(get, job.center, si)
			result := MeshedSection{
				Pos: core.SectionPos{
					X: job.center.X,
					Y: int32(si),
					Z: job.center.Z,
				},
				Quads:      mesh.MeshSection(n, s.reg),
				Conn:       mesh.ComputeConnectivity(n.Center, s.reg),
				Generation: job.generation,
			}
			select {
			case s.results <- result:
			case <-s.closed:
				return
			}
		}
	}
}

// SetCenter 更新可见中心与半径，调度缺失区块并淘汰远端 CPU 缓存。
func (s *Streamer) SetCenter(center core.ChunkPos, radius int) {
	if radius < 0 {
		radius = 0
	}

	s.mu.Lock()
	s.center = center
	s.radius = radius
	s.generation++
	generation := s.generation

	halo := radius + 1
	wanted := make(map[core.ChunkPos]bool, (2*halo+1)*(2*halo+1))
	for dx := -halo; dx <= halo; dx++ {
		for dz := -halo; dz <= halo; dz++ {
			wanted[core.ChunkPos{X: center.X + int32(dx), Z: center.Z + int32(dz)}] = true
		}
	}
	s.wanted = wanted

	for p := range s.chunks {
		if !wanted[p] {
			delete(s.chunks, p)
		}
	}
	for p := range s.queued {
		if !wanted[p] {
			delete(s.queued, p)
		}
	}
	for p := range s.meshed {
		if chunkDistance(p, center) > radius {
			delete(s.meshed, p)
		}
	}

	candidates := make([]core.ChunkPos, 0, (2*radius+1)*(2*radius+1))
	for dx := -radius; dx <= radius; dx++ {
		for dz := -radius; dz <= radius; dz++ {
			candidates = append(candidates, core.ChunkPos{
				X: center.X + int32(dx),
				Z: center.Z + int32(dz),
			})
		}
	}
	meshJobs := s.claimReadyLocked(candidates)

	toQueue := make([]core.ChunkPos, 0, len(wanted))
	for p := range wanted {
		if s.chunks[p] == nil && s.queued[p] == 0 && s.inFlight[p] == 0 {
			s.queued[p] = generation
			toQueue = append(toQueue, p)
		}
	}
	sort.Slice(toQueue, func(i, j int) bool {
		di := distanceSquared(toQueue[i], center)
		dj := distanceSquared(toQueue[j], center)
		if di != dj {
			return di < dj
		}
		if toQueue[i].X != toQueue[j].X {
			return toQueue[i].X < toQueue[j].X
		}
		return toQueue[i].Z < toQueue[j].Z
	})
	s.mu.Unlock()

	for _, p := range toQueue {
		select {
		case s.jobs <- p:
		case <-s.closed:
			return
		}
	}
	s.emitMeshJobs(meshJobs)
}

// Drain 非阻塞地取出至多 max 个当前仍可见的结果。
func (s *Streamer) Drain(maxResults int) []MeshedSection {
	if maxResults <= 0 {
		return nil
	}
	out := make([]MeshedSection, 0, maxResults)
	for len(out) < maxResults {
		select {
		case result := <-s.results:
			s.mu.Lock()
			visible := chunkDistance(
				core.ChunkPos{X: result.Pos.X, Z: result.Pos.Z},
				s.center,
			) <= s.radius
			s.mu.Unlock()
			if visible {
				out = append(out, result)
			}
		default:
			return out
		}
	}
	return out
}

// Stats 返回流式加载器状态快照。
func (s *Streamer) Stats() StreamStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return StreamStats{
		CachedChunks: len(s.chunks),
		QueuedJobs:   len(s.queued),
		InFlightJobs: len(s.inFlight),
		Generation:   s.generation,
	}
}

// InjectPanicForTest 让指定区块的下一次生成任务 panic，仅供故障隔离测试。
func (s *Streamer) InjectPanicForTest(pos core.ChunkPos) {
	s.mu.Lock()
	s.panicAt[pos] = true
	s.mu.Unlock()
}

// Close 停止所有 worker；即使结果队列已满也不会死锁。
func (s *Streamer) Close() {
	s.closeOnce.Do(func() { close(s.closed) })
	s.wg.Wait()
}

func chunkDistance(a, b core.ChunkPos) int {
	dx := int(a.X - b.X)
	if dx < 0 {
		dx = -dx
	}
	dz := int(a.Z - b.Z)
	if dz < 0 {
		dz = -dz
	}
	return max(dx, dz)
}

func distanceSquared(a, b core.ChunkPos) int64 {
	dx := int64(a.X - b.X)
	dz := int64(a.Z - b.Z)
	return dx*dx + dz*dz
}
