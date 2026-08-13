package server

import (
	"errors"
	"fmt"
	"time"

	"github.com/channing771/mornlea/internal/sim"
	"github.com/channing771/mornlea/internal/storage"
)

func (server *Server) saveWorker() {
	defer server.saveWorkers.Done()
	for {
		select {
		case <-server.saveCtx.Done():
			return
		case job := <-server.saveJobs:
			if job.Kind == saveKindMetadata {
				err := server.store.SaveMetadata(server.saveCtx, job.Metadata)
				select {
				case server.saveCompletions <- saveCompletion{Job: job, Err: err}:
				case <-server.saveCtx.Done():
					return
				}
				continue
			}
			saves := make([]storage.ChunkSave, len(job.Snapshots))
			for index, snapshot := range job.Snapshots {
				saves[index] = storage.ChunkSave{
					Key:      snapshot.Key,
					Revision: snapshot.Revision,
					Chunk:    snapshot.Chunk,
				}
			}
			started := time.Now()
			result, err := server.store.SaveBatch(server.saveCtx, saves)
			if server.config.SaveObserver != nil {
				server.config.SaveObserver(time.Since(started))
			}
			select {
			case server.saveCompletions <- saveCompletion{Job: job, Result: result, Err: err}:
			case <-server.saveCtx.Done():
				return
			}
		}
	}
}

func (server *Server) drainSaveCompletions() {
	_ = server.drainSaveCompletionsWithError()
}

func (server *Server) drainSaveCompletionsWithError() error {
	var result error
	for {
		select {
		case completion := <-server.saveCompletions:
			result = errors.Join(result, server.applySaveCompletion(completion))
		default:
			return result
		}
	}
}

func (server *Server) applySaveCompletion(completion saveCompletion) error {
	if completion.Job.Kind == saveKindMetadata {
		return server.applyMetadataCompletion(completion)
	}
	uncommitted := make([]sim.ChunkSaveSnapshot, 0, len(completion.Job.Snapshots))
	for _, snapshot := range completion.Job.Snapshots {
		if revision, ok := completion.Result.Committed[snapshot.Key]; ok {
			server.applyCommittedSnapshot(snapshot, revision)
		} else {
			uncommitted = append(uncommitted, snapshot)
		}
	}
	err := completion.Err
	if err == nil && len(uncommitted) != 0 {
		err = errors.New("save result omitted submitted chunks")
	}
	if err != nil {
		server.retainFailedSave(completion.Job, uncommitted, err)
		return fmt.Errorf("save region %+v: %w", completion.Job.Region, err)
	}
	server.lastSaveSuccess = time.Now()
	if completion.Job.Retry {
		server.finishRetryDispatch(completion.Job)
	}
	return nil
}

func (server *Server) applyCommittedSnapshot(
	snapshot sim.ChunkSaveSnapshot,
	committedRevision uint64,
) {
	info, exists := server.engine.ChunkInfo(snapshot.Key)
	if !exists || committedRevision < snapshot.Revision ||
		committedRevision > info.Revision {
		server.engine.FailPersistence([]sim.ChunkSaveSnapshot{snapshot})
		return
	}
	if committedRevision > snapshot.Revision {
		server.engine.FailPersistence([]sim.ChunkSaveSnapshot{snapshot})
		if committedRevision >= info.Revision {
			return
		}
	}
	server.engine.ApplyPersisted([]sim.PersistedChunk{{
		Key: snapshot.Key, Revision: committedRevision,
	}})
}
