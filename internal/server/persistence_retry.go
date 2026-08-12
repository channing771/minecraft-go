package server

import (
	"sort"

	"minecraft-go/internal/core"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/storage"
)

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
