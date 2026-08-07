package sim

import (
	"fmt"
	"sort"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

type SaveMode uint8

const (
	SaveUrgent SaveMode = iota
	SaveAll
)

type ChunkSaveSnapshot struct {
	Key            core.ChunkKey
	Revision       uint64
	EstimatedBytes int
	Chunk          *world.Chunk
}

type PersistedChunk struct {
	Key      core.ChunkKey
	Revision uint64
}

type PersistenceStats struct {
	DirtyChunks    int
	EstimatedBytes int64
	InFlightChunks int
	UnloadWaiting  int
}

type persistenceCandidate struct {
	key    core.ChunkKey
	record *ChunkRecord
}

type persistenceInFlight struct {
	revision       uint64
	estimatedBytes int
}

func (engine *Engine) PersistenceSnapshots(
	maxChunks int,
	maxBytes int,
	mode SaveMode,
) []ChunkSaveSnapshot {
	candidates := make([]persistenceCandidate, 0)
	for dimensionID, dimension := range engine.dimensions {
		for pos, record := range dimension.records {
			key := core.ChunkKey{Dimension: dimensionID, Pos: pos}
			_, inFlight := engine.persistenceInFlight(key, record)
			if record.Chunk == nil || !record.Dirty() || inFlight ||
				mode == SaveUrgent && !record.UnloadRequested {
				continue
			}
			candidates = append(candidates, persistenceCandidate{
				key: key, record: record,
			})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		if left.record.UnloadRequested != right.record.UnloadRequested {
			return left.record.UnloadRequested
		}
		return chunkKeyLess(left.key, right.key)
	})

	if engine.inFlightSaves == nil {
		engine.inFlightSaves = make(map[core.ChunkKey]persistenceInFlight)
	}
	snapshots := make([]ChunkSaveSnapshot, 0, min(maxChunks, len(candidates)))
	estimatedBytes := 0
	for _, candidate := range candidates {
		estimate := estimateChunkBytes(candidate.record.Chunk)
		if len(snapshots) > 0 &&
			(len(snapshots) >= maxChunks || estimatedBytes+estimate > maxBytes) {
			break
		}
		clone := candidate.record.Chunk.Clone()
		candidate.record.SaveInFlightRevision = candidate.record.Revision
		engine.inFlightSaves[candidate.key] = persistenceInFlight{
			revision:       candidate.record.Revision,
			estimatedBytes: estimate,
		}
		snapshots = append(snapshots, ChunkSaveSnapshot{
			Key:            candidate.key,
			Revision:       candidate.record.Revision,
			EstimatedBytes: estimate,
			Chunk:          clone,
		})
		estimatedBytes += estimate
	}
	return snapshots
}

// TouchChunkForTest 直接递增一个已加载区块的 revision，仅供测试把经由
// SetChunkChestForTest/SetChunkFurnaceForTest 等原始状态覆写标记为脏，
// 从而不必驱动真实命令即可验证持久化的保存/重试路径。
func (engine *Engine) TouchChunkForTest(key core.ChunkKey) {
	dimension := engine.dimensions[key.Dimension]
	if dimension == nil {
		return
	}
	if record, ok := dimension.records[key.Pos]; ok {
		record.Revision++
	}
}

func (engine *Engine) ApplyPersisted(acks []PersistedChunk) {
	for _, ack := range acks {
		dimension := engine.dimensions[ack.Key.Dimension]
		if dimension == nil {
			continue
		}
		record := dimension.records[ack.Key.Pos]
		if record == nil {
			continue
		}
		if ack.Revision > record.Revision {
			panic(fmt.Sprintf(
				"sim: persisted revision %d exceeds current revision %d at %+v",
				ack.Revision,
				record.Revision,
				ack.Key,
			))
		}
		record.PersistedRevision = max(record.PersistedRevision, ack.Revision)
		inFlight, exists := engine.persistenceInFlight(ack.Key, record)
		if exists && inFlight.revision == ack.Revision {
			record.SaveInFlightRevision = 0
			delete(engine.inFlightSaves, ack.Key)
		}
		if ack.Revision == record.Revision {
			record.NeedsRewrite = false
		}
		dimension.deleteCleanUnloading(ack.Key.Pos)
	}
}

func (engine *Engine) FailPersistence(snapshots []ChunkSaveSnapshot) {
	for _, snapshot := range snapshots {
		dimension := engine.dimensions[snapshot.Key.Dimension]
		if dimension == nil {
			continue
		}
		record := dimension.records[snapshot.Key.Pos]
		if record == nil {
			continue
		}
		inFlight, exists := engine.persistenceInFlight(snapshot.Key, record)
		if !exists || inFlight.revision != snapshot.Revision {
			continue
		}
		record.SaveInFlightRevision = 0
		delete(engine.inFlightSaves, snapshot.Key)
	}
}

func (engine *Engine) PersistenceStats() PersistenceStats {
	var stats PersistenceStats
	for dimensionID, dimension := range engine.dimensions {
		for pos, record := range dimension.records {
			if record.Dirty() && record.Chunk != nil {
				stats.DirtyChunks++
				stats.EstimatedBytes += int64(estimateChunkBytes(record.Chunk))
			}
			key := core.ChunkKey{Dimension: dimensionID, Pos: pos}
			if inFlight, exists := engine.persistenceInFlight(key, record); exists {
				stats.InFlightChunks++
				stats.EstimatedBytes += int64(inFlight.estimatedBytes)
			}
			if record.UnloadRequested {
				stats.UnloadWaiting++
			}
		}
	}
	return stats
}

func estimateChunkBytes(chunk *world.Chunk) int {
	return chunk.PayloadBytes()
}

func (engine *Engine) persistenceInFlight(
	key core.ChunkKey,
	record *ChunkRecord,
) (persistenceInFlight, bool) {
	inFlight, exists := engine.inFlightSaves[key]
	marked := record.SaveInFlightRevision != 0
	if exists != marked || exists && inFlight.revision != record.SaveInFlightRevision {
		panic(fmt.Sprintf(
			"sim: inconsistent persistence in-flight state at %+v",
			key,
		))
	}
	return inFlight, exists
}
