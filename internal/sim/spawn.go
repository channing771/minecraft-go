package sim

import (
	"log/slog"
	"math"
	"sort"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/physics"
)

const spawnRadius = int32(16)

type spawnColumn struct {
	X, Z int32
}

func spawnCandidates(anchor core.ChunkPos) []spawnColumn {
	anchorX := anchor.X << core.SectionShift
	anchorZ := anchor.Z << core.SectionShift
	candidates := make([]spawnColumn, 0, (spawnRadius*2+1)*(spawnRadius*2+1))
	for x := anchorX - spawnRadius; x <= anchorX+spawnRadius; x++ {
		for z := anchorZ - spawnRadius; z <= anchorZ+spawnRadius; z++ {
			candidates = append(candidates, spawnColumn{X: x, Z: z})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		li := int64(candidates[i].X-anchorX)*int64(candidates[i].X-anchorX) +
			int64(candidates[i].Z-anchorZ)*int64(candidates[i].Z-anchorZ)
		lj := int64(candidates[j].X-anchorX)*int64(candidates[j].X-anchorX) +
			int64(candidates[j].Z-anchorZ)*int64(candidates[j].Z-anchorZ)
		if li != lj {
			return li < lj
		}
		if candidates[i].X != candidates[j].X {
			return candidates[i].X < candidates[j].X
		}
		return candidates[i].Z < candidates[j].Z
	})
	return candidates
}

func spawnCandidateChunks(candidates []spawnColumn) []core.ChunkPos {
	unique := make(map[core.ChunkPos]struct{})
	for _, candidate := range candidates {
		chunk := (core.BlockPos{X: candidate.X, Z: candidate.Z}).Chunk()
		unique[chunk] = struct{}{}
	}
	chunks := make([]core.ChunkPos, 0, len(unique))
	for chunk := range unique {
		chunks = append(chunks, chunk)
	}
	sort.Slice(chunks, func(i, j int) bool {
		if chunks[i].X != chunks[j].X {
			return chunks[i].X < chunks[j].X
		}
		return chunks[i].Z < chunks[j].Z
	})
	return chunks
}

type dimensionCollisionSource struct {
	dimension *Dimension
}

func (source dimensionCollisionSource) CollisionBoxes(
	position core.BlockPos,
) physics.CollisionBoxSet {
	block, ready := source.dimension.BlockAt(position)
	return physics.BlockCollisionBoxes(block, ready)
}

func (engine *Engine) advancePendingPlayers() {
	sessions := make([]SessionID, 0, len(engine.sessions))
	for id, session := range engine.sessions {
		if session.player != nil && session.player.lifecycle == PlayerPendingSpawn {
			sessions = append(sessions, id)
		}
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i] < sessions[j] })
	for _, id := range sessions {
		engine.advancePendingPlayer(id, engine.sessions[id])
	}
}

func (engine *Engine) advancePendingPlayer(id SessionID, session *sessionState) {
	player := session.player
	dimension := engine.dimensions[session.dimension]
	if player.exhausted {
		if !spawnRevisionsChanged(dimension, player) {
			if (engine.tick.Load()+1)%100 == 0 {
				slog.Warn("玩家仍在等待可用出生点", "session", id)
			}
			return
		}
		player.exhausted = false
		player.exhaustedRevisions = nil
		player.nextCandidate = 0
	}

	source := dimensionCollisionSource{dimension: dimension}
	for player.nextCandidate < len(player.candidates) {
		candidate := player.candidates[player.nextCandidate]
		engine.retainSpawnChunk(session, (core.BlockPos{X: candidate.X, Z: candidate.Z}).Chunk())
		position, valid, ready := findSpawnInColumn(candidate, dimension, source)
		if !ready {
			engine.retryFailedSpawnChunk(session, (core.BlockPos{X: candidate.X, Z: candidate.Z}).Chunk())
			return
		}
		if valid {
			player.lifecycle = PlayerActive
			player.state = physics.State{Position: position, OnGround: true}
			player.input = physics.Input{}
			player.lastInputSequence = 0
			player.reset = true
			session.center = (core.BlockPos{X: candidate.X, Z: candidate.Z}).Chunk()
			engine.subscriptionsDirty = true
			return
		}
		player.nextCandidate++
	}

	player.exhausted = true
	player.exhaustedRevisions = make([]uint64, len(player.candidateChunks))
	for index, chunk := range player.candidateChunks {
		info, ok := dimension.Info(chunk)
		if !ok || info.State != ChunkReady {
			panic("sim: exhausted spawn candidate chunk is not ready")
		}
		player.exhaustedRevisions[index] = info.Revision
	}
}

func (engine *Engine) retainSpawnChunk(
	session *sessionState,
	chunk core.ChunkPos,
) {
	if _, retained := session.player.spawnWanted[chunk]; retained {
		return
	}
	session.player.spawnWanted[chunk] = struct{}{}
	engine.subscriptionsDirty = true
}

func (engine *Engine) retryFailedSpawnChunk(
	session *sessionState,
	chunk core.ChunkPos,
) {
	info, ok := engine.dimensions[session.dimension].Info(chunk)
	if !ok || info.State == ChunkFailed {
		engine.subscriptionsDirty = true
	}
}

func findSpawnInColumn(
	candidate spawnColumn,
	dimension *Dimension,
	source dimensionCollisionSource,
) (mgl32.Vec3, bool, bool) {
	for y := int32(core.MaxY - 1); y >= core.MinY; y-- {
		blockPosition := core.BlockPos{X: candidate.X, Y: y, Z: candidate.Z}
		block, ready := dimension.BlockAt(blockPosition)
		if !ready {
			return mgl32.Vec3{}, false, false
		}
		boxes := physics.BlockCollisionBoxes(block, true)
		if block == core.AirID || boxes.Count == 0 {
			continue
		}
		position := mgl32.Vec3{float32(candidate.X) + 0.5, float32(y) + 1, float32(candidate.Z) + 0.5}
		free, neighborsReady := playerBoundsAreFree(position, source)
		if !neighborsReady {
			return mgl32.Vec3{}, false, false
		}
		if free {
			return position, true, true
		}
	}
	return mgl32.Vec3{}, false, true
}

func playerBoundsAreFree(
	position mgl32.Vec3,
	source dimensionCollisionSource,
) (bool, bool) {
	bounds := physics.PlayerBounds(position)
	minX, maxX := blockSpan(bounds.Min.X(), bounds.Max.X())
	minY, maxY := blockSpan(bounds.Min.Y(), bounds.Max.Y())
	minZ, maxZ := blockSpan(bounds.Min.Z(), bounds.Max.Z())
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			for z := minZ; z <= maxZ; z++ {
				blockPosition := core.BlockPos{X: x, Y: y, Z: z}
				boxes := source.CollisionBoxes(blockPosition)
				if !boxes.Loaded {
					return false, false
				}
				count := min(int(boxes.Count), len(boxes.Boxes))
				for index := 0; index < count; index++ {
					offset := mgl32.Vec3{float32(x), float32(y), float32(z)}
					box := core.AABB{
						Min: boxes.Boxes[index].Min.Add(offset),
						Max: boxes.Boxes[index].Max.Add(offset),
					}
					if bounds.Overlaps(box) {
						return false, true
					}
				}
			}
		}
	}
	return true, true
}

func blockSpan(minimum, maximum float32) (int32, int32) {
	return int32(math.Floor(float64(minimum))), int32(math.Ceil(float64(maximum))) - 1
}

func spawnRevisionsChanged(dimension *Dimension, player *playerState) bool {
	for index, chunk := range player.candidateChunks {
		info, ok := dimension.Info(chunk)
		if !ok || info.State != ChunkReady || info.Revision != player.exhaustedRevisions[index] {
			return true
		}
	}
	return false
}
