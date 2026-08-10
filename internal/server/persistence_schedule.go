package server

import (
	"sort"

	"minecraft-go/internal/core"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/storage"
)

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
