package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"minecraft-go/internal/core"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/storage"
)

type saveJob struct {
	Region    storage.RegionKey
	Snapshots []sim.ChunkSaveSnapshot
	Attempt   uint32
	Retry     bool
	RetryID   uint64
}

type saveCompletion struct {
	Job    saveJob
	Result storage.SaveResult
	Err    error
}

type retrySave struct {
	Job       saveJob
	Attempts  uint32
	NextTick  uint64
	LastError error
}

// PersistenceStatus 汇总权威区块的存档积压与最近一次存档结果。
type PersistenceStatus struct {
	DirtyChunks     int
	EstimatedBytes  int64
	InFlightChunks  int
	Backpressured   bool
	LastSuccess     time.Time
	LastError       string
	LastErrorAt     time.Time
	AutosaveDrained bool
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
			} else {
				server.lastSaveSuccess = time.Now()
				if completion.Job.Retry {
					server.finishRetryDispatch(completion.Job)
				}
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
	server.dispatchDueRetries(tick)
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
		job.Attempt = 1
		select {
		case server.saveJobs <- job:
		default:
			server.engine.FailPersistence(job.Snapshots)
		}
	}
}

func (server *Server) retainFailedSave(
	job saveJob,
	uncommitted []sim.ChunkSaveSnapshot,
	err error,
) {
	if job.Retry {
		server.finishRetryDispatch(job)
	}
	if len(uncommitted) == 0 {
		server.recordSaveFailure(job.Region, max(job.Attempt, 1), 0, err)
		return
	}
	attempt := job.Attempt
	if attempt == 0 {
		attempt = 1
	}
	nextTick := saturatingAddUint64(
		server.engine.TickCount(),
		retryDelay(server.config.RetryBaseTicks, server.config.RetryMaxTicks, attempt),
	)
	retryID := job.RetryID
	if retryID == 0 {
		retryID = server.allocateRetryID()
	}
	server.enqueueRetryCohort(retrySave{
		Job: saveJob{
			Region:    job.Region,
			Snapshots: mergeRetrySnapshots(nil, uncommitted),
			Retry:     true,
			RetryID:   retryID,
		},
		Attempts:  attempt,
		NextTick:  nextTick,
		LastError: err,
	})
	server.recordSaveFailure(job.Region, attempt, nextTick, err)
}

func (server *Server) finishRetryDispatch(job saveJob) {
	if retained, ok := server.retryInFlight[job.RetryID]; ok &&
		retained.Job.Attempt == job.Attempt {
		delete(server.retryInFlight, job.RetryID)
	}
}

func (server *Server) allocateRetryID() uint64 {
	for {
		server.nextRetryID++
		if server.nextRetryID == 0 {
			server.nextRetryID++
		}
		if _, exists := server.retryInFlight[server.nextRetryID]; exists {
			continue
		}
		found := false
		for _, cohorts := range server.retry {
			for _, cohort := range cohorts {
				if cohort.Job.RetryID == server.nextRetryID {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			return server.nextRetryID
		}
	}
}

func (server *Server) enqueueRetryCohort(incoming retrySave) {
	unique := make([]sim.ChunkSaveSnapshot, 0, len(incoming.Job.Snapshots))
	for _, snapshot := range incoming.Job.Snapshots {
		if !server.ownsRetrySnapshot(snapshot) {
			unique = append(unique, snapshot)
		}
	}
	incoming.Job.Snapshots = unique
	if len(unique) == 0 {
		return
	}
	cohorts := server.retry[incoming.Job.Region]
	for index := range cohorts {
		if cohorts[index].Attempts != incoming.Attempts ||
			cohorts[index].NextTick != incoming.NextTick {
			continue
		}
		cohorts[index].Job.Snapshots = mergeRetrySnapshots(
			cohorts[index].Job.Snapshots,
			incoming.Job.Snapshots,
		)
		cohorts[index].LastError = incoming.LastError
		server.retry[incoming.Job.Region] = cohorts
		return
	}
	server.retry[incoming.Job.Region] = append(cohorts, incoming)
}

func (server *Server) ownsRetrySnapshot(snapshot sim.ChunkSaveSnapshot) bool {
	for _, cohorts := range server.retry {
		for _, cohort := range cohorts {
			for _, owned := range cohort.Job.Snapshots {
				if owned.Key == snapshot.Key && owned.Revision == snapshot.Revision {
					return true
				}
			}
		}
	}
	for _, cohort := range server.retryInFlight {
		for _, owned := range cohort.Job.Snapshots {
			if owned.Key == snapshot.Key && owned.Revision == snapshot.Revision {
				return true
			}
		}
	}
	return false
}

func (server *Server) dispatchDueRetries(tick uint64) {
	type dueRetry struct {
		region storage.RegionKey
		cohort retrySave
	}
	due := make([]dueRetry, 0)
	for region, cohorts := range server.retry {
		for _, cohort := range cohorts {
			if cohort.NextTick <= tick {
				due = append(due, dueRetry{region: region, cohort: cohort})
			}
		}
	}
	sort.Slice(due, func(i, j int) bool {
		left, right := due[i], due[j]
		if left.cohort.NextTick != right.cohort.NextTick {
			return left.cohort.NextTick < right.cohort.NextTick
		}
		if left.region != right.region {
			return regionKeyLess(left.region, right.region)
		}
		return left.cohort.Job.RetryID < right.cohort.Job.RetryID
	})
	for _, candidate := range due {
		retained, exists := server.pendingRetryCohort(
			candidate.region,
			candidate.cohort.Job.RetryID,
		)
		if !exists {
			continue
		}
		attempt := retained.Attempts
		if attempt < ^uint32(0) {
			attempt++
		}
		job := saveJob{
			Region:    candidate.region,
			Snapshots: append([]sim.ChunkSaveSnapshot(nil), retained.Job.Snapshots...),
			Attempt:   attempt,
			Retry:     true,
			RetryID:   retained.Job.RetryID,
		}
		select {
		case server.saveJobs <- job:
			server.removePendingRetryCohort(candidate.region, job.RetryID)
			retained.Job = job
			server.retryInFlight[job.RetryID] = retained
		default:
			return
		}
	}
}

func (server *Server) pendingRetryCohort(
	region storage.RegionKey,
	retryID uint64,
) (retrySave, bool) {
	for _, cohort := range server.retry[region] {
		if cohort.Job.RetryID == retryID {
			return cohort, true
		}
	}
	return retrySave{}, false
}

func (server *Server) removePendingRetryCohort(
	region storage.RegionKey,
	retryID uint64,
) {
	cohorts := server.retry[region]
	kept := make([]retrySave, 0, len(cohorts))
	for _, cohort := range cohorts {
		if cohort.Job.RetryID != retryID {
			kept = append(kept, cohort)
		}
	}
	if len(kept) == 0 {
		delete(server.retry, region)
		return
	}
	server.retry[region] = kept
}

func mergeRetrySnapshots(
	existing []sim.ChunkSaveSnapshot,
	incoming []sim.ChunkSaveSnapshot,
) []sim.ChunkSaveSnapshot {
	byKey := make(map[core.ChunkKey]sim.ChunkSaveSnapshot, len(existing)+len(incoming))
	for _, snapshot := range existing {
		byKey[snapshot.Key] = snapshot
	}
	for _, snapshot := range incoming {
		current, exists := byKey[snapshot.Key]
		if !exists || snapshot.Revision > current.Revision {
			byKey[snapshot.Key] = snapshot
		}
	}
	merged := make([]sim.ChunkSaveSnapshot, 0, len(byKey))
	for _, snapshot := range byKey {
		merged = append(merged, snapshot)
	}
	sort.Slice(merged, func(i, j int) bool {
		return chunkKeyLessForSave(merged[i].Key, merged[j].Key)
	})
	return merged
}

func retryDelay(base, maximum uint64, attempts uint32) uint64 {
	delay := base
	for i := uint32(1); i < attempts && delay < maximum; i++ {
		if delay > maximum/2 {
			return maximum
		}
		delay *= 2
	}
	return min(delay, maximum)
}

func saturatingAddUint64(left, right uint64) uint64 {
	if left > ^uint64(0)-right {
		return ^uint64(0)
	}
	return left + right
}

func (server *Server) recordSaveFailure(
	region storage.RegionKey,
	attempt uint32,
	nextTick uint64,
	err error,
) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	wrapped := fmt.Errorf("save region %+v: %w", region, err)
	server.lastSaveError = wrapped.Error()
	server.lastSaveErrorAt = time.Now()
	attributes := []any{
		"operation", "save",
		"region", region,
		"attempt", attempt,
		"next_tick", nextTick,
		"error", wrapped,
	}
	if store, ok := server.store.(interface{ WorldPath() string }); ok {
		if path := store.WorldPath(); path != "" {
			attributes = append(attributes, "world_path", path)
		}
	}
	slog.Error("区块存档失败，将按 tick 退避重试", attributes...)
}

func (server *Server) updatePersistenceBackpressure() {
	stats := server.engine.PersistenceStats()
	server.backpressured = nextPersistenceBackpressure(
		server.backpressured,
		stats.EstimatedBytes,
		server.config.UnsavedBytes,
	)
}

func nextPersistenceBackpressure(current bool, estimated, limit int64) bool {
	if !current {
		return estimated >= limit
	}
	remainder := limit % 10
	threshold := limit/10*9 + (remainder*9+9)/10
	return estimated >= threshold
}

// PersistenceStatus 返回当前存档积压、背压和最近完成状态的值副本。
func (server *Server) PersistenceStatus() PersistenceStatus {
	server.stepMu.Lock()
	defer server.stepMu.Unlock()
	stats := server.engine.PersistenceStats()
	return PersistenceStatus{
		DirtyChunks:    stats.DirtyChunks,
		EstimatedBytes: stats.EstimatedBytes,
		InFlightChunks: stats.InFlightChunks,
		Backpressured:  server.backpressured,
		LastSuccess:    server.lastSaveSuccess,
		LastError:      server.lastSaveError,
		LastErrorAt:    server.lastSaveErrorAt,
		AutosaveDrained: !server.autosaveActive && stats.DirtyChunks == 0 &&
			stats.InFlightChunks == 0 && len(server.retry) == 0 &&
			len(server.retryInFlight) == 0,
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
		return regionKeyLess(regions[i], regions[j])
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

func regionKeyLess(left, right storage.RegionKey) bool {
	if left.Dimension != right.Dimension {
		return left.Dimension < right.Dimension
	}
	if left.X != right.X {
		return left.X < right.X
	}
	return left.Z < right.Z
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
