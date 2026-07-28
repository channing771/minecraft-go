package client

import (
	"log/slog"
	"sort"
	"sync"

	"minecraft-go/internal/assets"
	"minecraft-go/internal/core"
	"minecraft-go/internal/mesh"
	"minecraft-go/internal/world"
)

const (
	mesherJobCapacity    = 1024
	mesherResultCapacity = 64
)

// ChunkStamp 记录网格任务创建时一个输入区块是否存在及其 revision。
type ChunkStamp struct {
	Dimension core.DimensionID
	Chunk     core.ChunkPos
	Present   bool
	Revision  uint64
}

// MeshedSection 是一个区段的网格化结果及其完整输入印章。
type MeshedSection struct {
	Dimension  core.DimensionID
	Pos        core.SectionPos
	Quads      []mesh.Quad
	Conn       mesh.Connectivity
	Stamps     []ChunkStamp
	Generation uint64
}

// MesherStats 是网格调度器的只读诊断快照。
type MesherStats struct {
	DirtySections  int
	QueuedJobs     int
	InFlightJobs   int
	ReadyResults   int
	ResultCapacity int
}

type mesherJob struct {
	key          core.SectionKey
	neighborhood *world.Neighborhood
	stamps       []ChunkStamp
	generation   uint64
}

type mesherResult struct {
	MeshedSection
	key core.SectionKey
}

// Mesher 在固定 worker 池上把主线程克隆出的不可变区段邻域网格化。
type Mesher struct {
	registry *assets.Registry
	jobs     chan mesherJob
	results  chan mesherResult
	closed   chan struct{}

	mu             sync.Mutex
	dirty          map[core.SectionKey]uint64
	queued         map[core.SectionKey]uint64
	inFlight       map[core.SectionKey]uint64
	panicAt        map[core.SectionKey]bool
	blockAt        map[core.SectionKey]chan struct{}
	nextGeneration uint64
	isClosed       bool

	wg        sync.WaitGroup
	closeOnce sync.Once
}

// NewMesher 创建一个有界、可关闭的增量网格调度器。
func NewMesher(registry *assets.Registry, workers int) *Mesher {
	if registry == nil {
		panic("client: nil mesher registry")
	}
	if workers < 1 {
		workers = 1
	}
	mesher := &Mesher{
		registry: registry,
		jobs:     make(chan mesherJob, mesherJobCapacity),
		results:  make(chan mesherResult, mesherResultCapacity),
		closed:   make(chan struct{}),
		dirty:    make(map[core.SectionKey]uint64),
		queued:   make(map[core.SectionKey]uint64),
		inFlight: make(map[core.SectionKey]uint64),
		panicAt:  make(map[core.SectionKey]bool),
		blockAt:  make(map[core.SectionKey]chan struct{}),
	}
	mesher.wg.Add(workers)
	for range workers {
		go mesher.work()
	}
	return mesher
}

// MarkDirty 标记区段需要重新网格化。重复标记会使已排队结果过期。
func (mesher *Mesher) MarkDirty(keys ...core.SectionKey) {
	mesher.mu.Lock()
	defer mesher.mu.Unlock()
	if mesher.isClosed {
		return
	}
	for _, key := range keys {
		if key.Pos.Y < 0 || key.Pos.Y >= core.SectionsPerChunk {
			continue
		}
		mesher.markDirtyLocked(key)
	}
}

// ForgetChunk 取消已遗忘区块的待处理区段；通道中已有任务会在领取时跳过。
func (mesher *Mesher) ForgetChunk(
	dimension core.DimensionID,
	position core.ChunkPos,
) {
	mesher.mu.Lock()
	defer mesher.mu.Unlock()
	for y := int32(0); y < core.SectionsPerChunk; y++ {
		key := core.SectionKey{
			Dimension: dimension,
			Pos: core.SectionPos{
				X: position.X,
				Y: y,
				Z: position.Z,
			},
		}
		delete(mesher.dirty, key)
		delete(mesher.queued, key)
		delete(mesher.panicAt, key)
		delete(mesher.blockAt, key)
	}
}

// Schedule 按确定性顺序至多投递 maxJobs 个 dirty 区段。
// Mirror 只在调用线程上读取，worker 仅接收克隆后的不可变邻域。
func (mesher *Mesher) Schedule(mirror *Mirror, maxJobs int) {
	if mirror == nil || maxJobs <= 0 {
		return
	}

	mesher.mu.Lock()
	if mesher.isClosed {
		mesher.mu.Unlock()
		return
	}
	candidates := make([]core.SectionKey, 0, len(mesher.dirty))
	for key := range mesher.dirty {
		if _, queued := mesher.queued[key]; queued {
			continue
		}
		if _, inFlight := mesher.inFlight[key]; inFlight {
			continue
		}
		candidates = append(candidates, key)
	}
	mesher.mu.Unlock()
	sortSectionKeySlice(candidates)

	scheduled := 0
	for _, key := range candidates {
		if scheduled >= maxJobs {
			return
		}

		mesher.mu.Lock()
		generation, dirty := mesher.dirty[key]
		_, queued := mesher.queued[key]
		_, inFlight := mesher.inFlight[key]
		closed := mesher.isClosed
		mesher.mu.Unlock()
		if closed {
			return
		}
		if !dirty || queued || inFlight {
			continue
		}

		neighborhood, stamps, ok := cloneNeighborhood(mirror, key)
		if !ok {
			continue
		}
		job := mesherJob{
			key:          key,
			neighborhood: neighborhood,
			stamps:       stamps,
			generation:   generation,
		}

		mesher.mu.Lock()
		current, stillDirty := mesher.dirty[key]
		_, queued = mesher.queued[key]
		_, inFlight = mesher.inFlight[key]
		if mesher.isClosed || !stillDirty || current != generation || queued || inFlight {
			mesher.mu.Unlock()
			continue
		}
		mesher.queued[key] = generation
		mesher.mu.Unlock()

		select {
		case mesher.jobs <- job:
			scheduled++
		case <-mesher.closed:
			mesher.removeQueued(key, generation)
			return
		default:
			mesher.removeQueued(key, generation)
			return
		}
	}
}

// Drain 非阻塞地取出至多 maxResults 个印章仍匹配的结果。
func (mesher *Mesher) Drain(mirror *Mirror, maxResults int) []MeshedSection {
	if mirror == nil || maxResults <= 0 {
		return nil
	}
	accepted := make([]MeshedSection, 0, maxResults)
	for len(accepted) < maxResults {
		select {
		case result := <-mesher.results:
			valid := stampsMatch(mirror, result.Stamps)
			mesher.mu.Lock()
			generation, dirty := mesher.dirty[result.key]
			generationMatches := dirty && generation == result.Generation
			if valid && generationMatches {
				delete(mesher.dirty, result.key)
				accepted = append(accepted, result.MeshedSection)
			} else if !valid {
				chunkPos := core.ChunkPos{X: result.key.Pos.X, Z: result.key.Pos.Z}
				if _, present := mirror.Chunk(result.key.Dimension, chunkPos); present {
					if !dirty || generation == result.Generation {
						mesher.markDirtyLocked(result.key)
					}
				} else {
					delete(mesher.dirty, result.key)
				}
			}
			mesher.mu.Unlock()
		default:
			return accepted
		}
	}
	return accepted
}

// Stats 返回调度器状态快照。
func (mesher *Mesher) Stats() MesherStats {
	mesher.mu.Lock()
	defer mesher.mu.Unlock()
	return MesherStats{
		DirtySections:  len(mesher.dirty),
		QueuedJobs:     len(mesher.queued),
		InFlightJobs:   len(mesher.inFlight),
		ReadyResults:   len(mesher.results),
		ResultCapacity: cap(mesher.results),
	}
}

// InjectPanicForTest 让指定区段的下一次任务 panic，仅供故障隔离测试。
func (mesher *Mesher) InjectPanicForTest(key core.SectionKey) {
	mesher.mu.Lock()
	if !mesher.isClosed {
		mesher.panicAt[key] = true
	}
	mesher.mu.Unlock()
}

// BlockForTest 阻塞指定区段的下一次任务，返回解除阻塞的幂等函数。
func (mesher *Mesher) BlockForTest(key core.SectionKey) func() {
	blocked := make(chan struct{})
	mesher.mu.Lock()
	if !mesher.isClosed {
		mesher.blockAt[key] = blocked
	}
	mesher.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { close(blocked) }) }
}

// Close 停止所有 worker；结果队列满或测试任务阻塞时也会立即唤醒。
func (mesher *Mesher) Close() {
	mesher.closeOnce.Do(func() {
		mesher.mu.Lock()
		mesher.isClosed = true
		close(mesher.closed)
		mesher.mu.Unlock()
	})
	mesher.wg.Wait()
}

func (mesher *Mesher) work() {
	defer mesher.wg.Done()
	for {
		select {
		case <-mesher.closed:
			return
		default:
		}
		select {
		case <-mesher.closed:
			return
		case job := <-mesher.jobs:
			mesher.handle(job)
		}
	}
}

func (mesher *Mesher) handle(job mesherJob) {
	claimed := false
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.Error("区段网格化失败", "section", job.key, "panic", recovered)
		}
		if claimed {
			mesher.mu.Lock()
			if mesher.inFlight[job.key] == job.generation {
				delete(mesher.inFlight, job.key)
			}
			mesher.mu.Unlock()
		}
	}()

	mesher.mu.Lock()
	if mesher.queued[job.key] != job.generation || mesher.isClosed {
		mesher.mu.Unlock()
		return
	}
	delete(mesher.queued, job.key)
	mesher.inFlight[job.key] = job.generation
	claimed = true
	shouldPanic := mesher.panicAt[job.key]
	delete(mesher.panicAt, job.key)
	blocked := mesher.blockAt[job.key]
	delete(mesher.blockAt, job.key)
	mesher.mu.Unlock()

	if blocked != nil {
		select {
		case <-blocked:
		case <-mesher.closed:
			return
		}
	}
	if shouldPanic {
		panic("测试注入的区段网格化故障")
	}

	result := mesherResult{
		MeshedSection: MeshedSection{
			Dimension:  job.key.Dimension,
			Pos:        job.key.Pos,
			Quads:      mesh.MeshSection(job.neighborhood, mesher.registry),
			Conn:       mesh.ComputeConnectivity(job.neighborhood.Center, mesher.registry),
			Stamps:     job.stamps,
			Generation: job.generation,
		},
		key: job.key,
	}
	select {
	case mesher.results <- result:
	case <-mesher.closed:
	}
}

func (mesher *Mesher) markDirtyLocked(key core.SectionKey) {
	mesher.nextGeneration++
	if mesher.nextGeneration == 0 {
		mesher.nextGeneration++
	}
	mesher.dirty[key] = mesher.nextGeneration
}

func (mesher *Mesher) removeQueued(key core.SectionKey, generation uint64) {
	mesher.mu.Lock()
	if mesher.queued[key] == generation {
		delete(mesher.queued, key)
	}
	mesher.mu.Unlock()
}

func cloneNeighborhood(
	mirror *Mirror,
	key core.SectionKey,
) (*world.Neighborhood, []ChunkStamp, bool) {
	centerPos := core.ChunkPos{X: key.Pos.X, Z: key.Pos.Z}
	center, present := mirror.Chunk(key.Dimension, centerPos)
	if !present || center.Chunk == nil || key.Pos.Y < 0 || key.Pos.Y >= core.SectionsPerChunk {
		return nil, nil, false
	}

	neighborhood := &world.Neighborhood{
		Center: center.Chunk.Section(int(key.Pos.Y)).Clone(),
	}
	stamps := make([]ChunkStamp, 0, 9)
	for dz := int32(-1); dz <= 1; dz++ {
		for dx := int32(-1); dx <= 1; dx++ {
			chunkPos := core.ChunkPos{X: centerPos.X + dx, Z: centerPos.Z + dz}
			chunk, loaded := mirror.Chunk(key.Dimension, chunkPos)
			stamp := ChunkStamp{
				Dimension: key.Dimension,
				Chunk:     chunkPos,
				Present:   loaded,
			}
			if loaded {
				stamp.Revision = chunk.Revision
			}
			stamps = append(stamps, stamp)
			if !loaded || chunk.Chunk == nil {
				continue
			}
			for dy := int32(-1); dy <= 1; dy++ {
				sectionY := key.Pos.Y + dy
				if sectionY < 0 || sectionY >= core.SectionsPerChunk {
					continue
				}
				if dx == 0 && dy == 0 && dz == 0 {
					neighborhood.Around[1][1][1] = neighborhood.Center
					continue
				}
				neighborhood.Around[dx+1][dy+1][dz+1] =
					chunk.Chunk.Section(int(sectionY)).Clone()
			}
		}
	}
	return neighborhood, stamps, true
}

func stampsMatch(mirror *Mirror, stamps []ChunkStamp) bool {
	for _, stamp := range stamps {
		chunk, present := mirror.Chunk(stamp.Dimension, stamp.Chunk)
		if present != stamp.Present {
			return false
		}
		if present && chunk.Revision != stamp.Revision {
			return false
		}
	}
	return true
}

func sortSectionKeySlice(keys []core.SectionKey) {
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Dimension != keys[j].Dimension {
			return keys[i].Dimension < keys[j].Dimension
		}
		if keys[i].Pos.X != keys[j].Pos.X {
			return keys[i].Pos.X < keys[j].Pos.X
		}
		if keys[i].Pos.Z != keys[j].Pos.Z {
			return keys[i].Pos.Z < keys[j].Pos.Z
		}
		return keys[i].Pos.Y < keys[j].Pos.Y
	})
}
