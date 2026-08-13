package server

import (
	"errors"
	"fmt"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/sim"
	"github.com/channing771/mornlea/internal/storage"
)

type chunkJobKind uint8

const (
	chunkJobLoad chunkJobKind = iota
	chunkJobGenerate
)

type chunkJob struct {
	Kind chunkJobKind
	Key  core.ChunkKey
}

func (server *Server) chunkWorker() {
	defer server.workers.Done()
	for {
		select {
		case <-server.ctx.Done():
			return
		case job := <-server.jobs:
			switch job.Kind {
			case chunkJobLoad:
				result := server.runAcquisition(job.Key)
				select {
				case server.acquired <- result:
				case <-server.ctx.Done():
					return
				}
			case chunkJobGenerate:
				result := runGeneration(server.generator, job.Key)
				select {
				case server.generated <- result:
				case <-server.ctx.Done():
					return
				}
			default:
				panic(fmt.Sprintf("server: unknown chunk job kind %d", job.Kind))
			}
		}
	}
}

func (server *Server) runAcquisition(key core.ChunkKey) sim.AcquiredChunk {
	stored, err := server.store.LoadChunk(server.ctx, key)
	result := sim.AcquiredChunk{Key: key}
	switch {
	case err == nil:
		result.Chunk = stored.Chunk
		result.Revision = stored.Revision
		result.PersistedRevision = stored.PersistedRevision
		result.NeedsRewrite = stored.NeedsRewrite
		result.Recovered = stored.Recovered
	case errors.Is(err, storage.ErrChunkNotFound):
		result.Missing = true
	default:
		result.Err = fmt.Errorf("load %v: %w", key, err)
	}
	return result
}
