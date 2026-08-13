package client

import (
	"log/slog"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/mesh"
	"github.com/channing771/mornlea/internal/world"
)

func (mesher *Mesher) work() {
	defer mesher.wg.Done()
	light := mesh.NewLightScratch()
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
			mesher.handle(job, light)
		}
	}
}

func (mesher *Mesher) handle(job mesherJob, light *mesh.LightScratch) {
	claimed := false
	defer func() {
		recovered := recover()
		if recovered != nil {
			slog.Error("区段网格化失败", "section", job.key, "panic", recovered)
		}
		if !claimed {
			return
		}
		mesher.mu.Lock()
		if mesher.inFlight[job.key] == job.generation {
			delete(mesher.inFlight, job.key)
		}
		current, dirty := mesher.dirty[job.key]
		if recovered != nil || (dirty && current != job.generation) {
			mesher.enqueueReadyLocked(job.key)
		}
		mesher.mu.Unlock()
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
			Quads:      mesh.MeshSection(job.neighborhood, mesher.registry, light),
			Conn:       mesh.ComputeConnectivity(job.neighborhood.Center, mesher.registry),
			Stamps:     job.stamps,
			Generation: job.generation,
		},
		key: job.key,
	}
	mesher.mu.Lock()
	if mesher.inFlight[job.key] == job.generation {
		delete(mesher.inFlight, job.key)
		current, dirty := mesher.dirty[job.key]
		if dirty && current != job.generation {
			mesher.enqueueReadyLocked(job.key)
		}
	}
	mesher.mu.Unlock()
	claimed = false
	select {
	case mesher.results <- result:
	case <-mesher.closed:
	}
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
		Center:   center.Chunk.Section(int(key.Pos.Y)).Clone(),
		SectionY: int(key.Pos.Y),
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
			// 高度表与 section 取自同一份 revision 印章，保证光照输入同代。
			neighborhood.Heights[dx+1][dz+1] = chunk.Chunk.Heights()
			neighborhood.HeightsPresent[dx+1][dz+1] = true
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
