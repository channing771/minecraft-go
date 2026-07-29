package server

import (
	"sort"
	"time"

	"minecraft-go/internal/core"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/storage"
)

type saveJob struct {
	Region    storage.RegionKey
	Snapshots []sim.ChunkSaveSnapshot
}

type saveCompletion struct {
	Job    saveJob
	Result storage.SaveResult
	Err    error
}

func (server *Server) saveWorker() {
	defer server.saveWorkers.Done()
	for {
		select {
		case <-server.saveCtx.Done():
			return
		case job := <-server.saveJobs:
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
	for {
		select {
		case completion := <-server.saveCompletions:
			uncommitted := make([]sim.ChunkSaveSnapshot, 0, len(completion.Job.Snapshots))
			for _, snapshot := range completion.Job.Snapshots {
				if revision, ok := completion.Result.Committed[snapshot.Key]; ok {
					server.applyCommittedSnapshot(snapshot, revision)
				} else if completion.Err != nil {
					uncommitted = append(uncommitted, snapshot)
				}
			}
			if completion.Err != nil {
				server.engine.FailPersistence(uncommitted)
			}
		default:
			return
		}
	}
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

func (server *Server) schedulePersistence(tick uint64) {
	server.dispatchPersistence(server.engine.PersistenceSnapshots(
		server.config.SaveChunks,
		server.config.SaveBytes,
		sim.SaveUrgent,
	))
	if tick%server.config.AutosaveTicks == 0 {
		server.autosaveActive = true
	}
	if !server.autosaveActive {
		return
	}
	server.dispatchPersistence(server.engine.PersistenceSnapshots(
		server.config.SaveChunks,
		server.config.SaveBytes,
		sim.SaveAll,
	))
	stats := server.engine.PersistenceStats()
	if stats.DirtyChunks == 0 && stats.InFlightChunks == 0 {
		server.autosaveActive = false
	}
}

func (server *Server) dispatchPersistence(snapshots []sim.ChunkSaveSnapshot) {
	for _, job := range groupSaveJobs(snapshots) {
		select {
		case server.saveJobs <- job:
		default:
			server.engine.FailPersistence(job.Snapshots)
		}
	}
}

func groupSaveJobs(snapshots []sim.ChunkSaveSnapshot) []saveJob {
	grouped := make(map[storage.RegionKey][]sim.ChunkSaveSnapshot)
	for _, snapshot := range snapshots {
		region, _ := storage.RegionFor(snapshot.Key)
		grouped[region] = append(grouped[region], snapshot)
	}
	regions := make([]storage.RegionKey, 0, len(grouped))
	for region := range grouped {
		regions = append(regions, region)
	}
	sort.Slice(regions, func(i, j int) bool {
		left, right := regions[i], regions[j]
		if left.Dimension != right.Dimension {
			return left.Dimension < right.Dimension
		}
		if left.X != right.X {
			return left.X < right.X
		}
		return left.Z < right.Z
	})
	jobs := make([]saveJob, 0, len(regions))
	for _, region := range regions {
		group := grouped[region]
		sort.Slice(group, func(i, j int) bool {
			return chunkKeyLessForSave(group[i].Key, group[j].Key)
		})
		jobs = append(jobs, saveJob{Region: region, Snapshots: group})
	}
	return jobs
}

func chunkKeyLessForSave(left, right core.ChunkKey) bool {
	if left.Dimension != right.Dimension {
		return left.Dimension < right.Dimension
	}
	if left.Pos.X != right.Pos.X {
		return left.Pos.X < right.Pos.X
	}
	return left.Pos.Z < right.Pos.Z
}
