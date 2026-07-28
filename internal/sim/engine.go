package sim

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/physics"
	"minecraft-go/internal/world"
)

const (
	productionTickInterval = 50 * time.Millisecond
	maxCatchUpSteps        = 5
	interactionReach       = 6
)

type Clock interface {
	C() <-chan time.Time
	Stop()
}

type sessionState struct {
	lastSequence                uint64
	lastTrustedObserverSequence uint64
	hasView                     bool
	dimension                   core.DimensionID
	center                      core.ChunkPos
	wanted                      map[core.ChunkKey]struct{}
	player                      *playerState
}

type pendingChunkChanges struct {
	baseRevision uint64
	changes      map[uint32]BlockChange
	dirty        map[int]struct{}
}

type Engine struct {
	viewRadius         int
	dimensions         map[core.DimensionID]*Dimension
	sessions           map[SessionID]*sessionState
	wanted             map[core.ChunkKey]struct{}
	subscriptionsDirty bool

	inboxMu   sync.Mutex
	commands  []Command
	generated []GeneratedChunk
	tick      atomic.Uint64
}

func NewEngine(viewRadius int) *Engine {
	if viewRadius < 0 {
		panic("sim: negative view radius")
	}
	return &Engine{
		viewRadius: viewRadius,
		dimensions: map[core.DimensionID]*Dimension{
			core.Overworld: NewDimension(core.Overworld),
		},
		sessions: make(map[SessionID]*sessionState),
		wanted:   make(map[core.ChunkKey]struct{}),
	}
}

// Enqueue 可由 endpoint reader 并发调用。
func (engine *Engine) Enqueue(command Command) {
	engine.inboxMu.Lock()
	engine.commands = append(engine.commands, command)
	engine.inboxMu.Unlock()
}

// SubmitGenerated 可由生成 worker 并发调用，并转移 Chunk 所有权。
func (engine *Engine) SubmitGenerated(result GeneratedChunk) {
	engine.inboxMu.Lock()
	engine.generated = append(engine.generated, result)
	engine.inboxMu.Unlock()
}

// Step 严格串行执行一个权威 tick。
func (engine *Engine) Step() TickResult {
	commands, generated := engine.takeInbox()
	sort.SliceStable(commands, func(i, j int) bool {
		if commands[i].Session != commands[j].Session {
			return commands[i].Session < commands[j].Session
		}
		return commands[i].Sequence < commands[j].Sequence
	})

	result := TickResult{Forget: make(map[SessionID][]core.ChunkKey)}
	interactions := make([]Command, 0, len(commands))
	viewChanged := false
	for _, command := range commands {
		session := engine.session(command.Session)
		if command.Kind == CommandTrustedObserverCenter {
			if command.Sequence <= session.lastTrustedObserverSequence {
				continue
			}
			session.lastTrustedObserverSequence = command.Sequence
			if session.player != nil {
				continue
			}
			session.hasView = true
			session.dimension = command.Dimension
			session.center = command.Center
			viewChanged = true
			continue
		}
		if command.Sequence <= session.lastSequence {
			continue
		}
		session.lastSequence = command.Sequence
		switch command.Kind {
		case CommandBreakBlock, CommandPlaceBlock:
			if session.player == nil || session.player.lifecycle != PlayerActive {
				result.Rejected = append(result.Rejected, Rejection{
					Session:  command.Session,
					Sequence: command.Sequence,
					Reason:   RejectPlayerNotReady,
				})
				continue
			}
			if !validPlayerLook(command.Yaw, command.Pitch) {
				result.Rejected = append(result.Rejected, Rejection{
					Session:  command.Session,
					Sequence: command.Sequence,
					Reason:   RejectInvalidInput,
				})
				continue
			}
			player := session.player
			player.yaw = normalizeYaw(command.Yaw)
			player.pitch = command.Pitch
			player.input.Yaw = player.yaw
			interactions = append(interactions, command)
		case CommandPlayerInput:
			if session.player == nil || session.player.lifecycle != PlayerActive {
				result.Rejected = append(result.Rejected, Rejection{
					Session:  command.Session,
					Sequence: command.Sequence,
					Reason:   RejectPlayerNotReady,
				})
				continue
			}
			player := session.player
			player.lastInputSequence = command.Sequence
			if !validPlayerInput(command) {
				player.input = physics.Input{Yaw: player.yaw}
				result.Rejected = append(result.Rejected, Rejection{
					Session:  command.Session,
					Sequence: command.Sequence,
					Reason:   RejectInvalidInput,
				})
				continue
			}
			yaw := normalizeYaw(command.Yaw)
			player.input = physics.Input{
				MoveX: command.MoveX,
				MoveZ: command.MoveZ,
				Jump:  command.Jump,
				Yaw:   yaw,
			}
			player.yaw = yaw
			player.pitch = command.Pitch
		case CommandResync:
			result.Resync = append(result.Resync, ResyncRequest{
				Session:      command.Session,
				Sequence:     command.Sequence,
				Dimension:    command.Dimension,
				Chunk:        command.Chunk,
				HaveRevision: command.HaveRevision,
			})
		default:
			result.Rejected = append(result.Rejected, Rejection{
				Session:  command.Session,
				Sequence: command.Sequence,
				Reason:   RejectInvalidRay,
			})
		}
	}
	engine.applyGenerated(generated, &result)
	engine.advancePendingPlayersPreservingInputSequence()
	engine.advanceActivePlayers()
	playerViewChanged := engine.derivePlayerCenters()
	viewChanged = viewChanged || playerViewChanged || engine.subscriptionsDirty
	engine.subscriptionsDirty = false
	if viewChanged {
		engine.reconcileSubscriptions(&result)
	}

	pending := make(map[core.ChunkKey]*pendingChunkChanges)
	for _, command := range interactions {
		if reason, rejected := engine.executeInteraction(command, pending); rejected {
			result.Rejected = append(result.Rejected, Rejection{
				Session:  command.Session,
				Sequence: command.Sequence,
				Reason:   reason,
			})
		}
	}
	engine.finishChanges(pending, &result)

	result.Tick = engine.tick.Add(1)
	engine.publishPlayers(&result)
	return result
}

func (engine *Engine) Run(ctx context.Context, clock Clock) error {
	if clock == nil {
		clock = newTickerClock(productionTickInterval)
	}
	defer clock.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case tickTime, ok := <-clock.C():
			if !ok {
				return nil
			}
			steps := 1
			missed := int(time.Since(tickTime) / productionTickInterval)
			if missed > 0 {
				steps += min(missed, maxCatchUpSteps-1)
			}
			if missed >= maxCatchUpSteps {
				slog.Warn(
					"权威 tick 落后，限制追赶并重新定基准",
					"missed_ticks",
					missed,
				)
			}
			for range steps {
				engine.Step()
			}
		}
	}
}

func (engine *Engine) TickCount() uint64 {
	return engine.tick.Load()
}

func (engine *Engine) CloneReadyChunk(
	key core.ChunkKey,
) (*world.Chunk, uint64, bool) {
	dimension := engine.dimensions[key.Dimension]
	if dimension == nil {
		return nil, 0, false
	}
	return dimension.CloneReadyChunk(key.Pos)
}

func (engine *Engine) ChunkHash(
	key core.ChunkKey,
) ([32]byte, uint64, bool) {
	chunk, revision, ok := engine.CloneReadyChunk(key)
	if !ok {
		return [32]byte{}, 0, false
	}
	return chunk.Hash(), revision, true
}

func (engine *Engine) ChunkInfo(
	key core.ChunkKey,
) (ChunkInfo, bool) {
	dimension := engine.dimensions[key.Dimension]
	if dimension == nil {
		return ChunkInfo{}, false
	}
	return dimension.Info(key.Pos)
}

func (engine *Engine) takeInbox() ([]Command, []GeneratedChunk) {
	engine.inboxMu.Lock()
	commands := append([]Command(nil), engine.commands...)
	generated := append([]GeneratedChunk(nil), engine.generated...)
	engine.commands = engine.commands[:0]
	engine.generated = engine.generated[:0]
	engine.inboxMu.Unlock()
	return commands, generated
}

func (engine *Engine) session(id SessionID) *sessionState {
	session := engine.sessions[id]
	if session == nil {
		session = &sessionState{wanted: make(map[core.ChunkKey]struct{})}
		engine.sessions[id] = session
	}
	return session
}

func (engine *Engine) reconcileSubscriptions(result *TickResult) {
	union := make(map[core.ChunkKey]struct{})
	for sessionID, session := range engine.sessions {
		next := make(map[core.ChunkKey]struct{})
		if session.hasView && engine.dimensions[session.dimension] != nil {
			for dz := -engine.viewRadius; dz <= engine.viewRadius; dz++ {
				for dx := -engine.viewRadius; dx <= engine.viewRadius; dx++ {
					key := core.ChunkKey{
						Dimension: session.dimension,
						Pos: core.ChunkPos{
							X: session.center.X + int32(dx),
							Z: session.center.Z + int32(dz),
						},
					}
					next[key] = struct{}{}
					union[key] = struct{}{}
				}
			}
		}
		if session.player != nil && session.player.lifecycle == PlayerPendingSpawn {
			for chunk := range session.player.spawnWanted {
				key := core.ChunkKey{Dimension: session.dimension, Pos: chunk}
				next[key] = struct{}{}
				union[key] = struct{}{}
			}
		}
		for key := range session.wanted {
			if _, retained := next[key]; !retained {
				result.Forget[sessionID] = append(result.Forget[sessionID], key)
			}
		}
		sortChunkKeys(result.Forget[sessionID])
		session.wanted = next
	}

	candidates := make([]core.ChunkKey, 0)
	for key := range union {
		_, wasWanted := engine.wanted[key]
		dimension := engine.dimensions[key.Dimension]
		info, exists := dimension.Info(key.Pos)
		if !wasWanted || exists && info.State == ChunkFailed {
			candidates = append(candidates, key)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		leftDistance := engine.subscriptionDistanceSquared(candidates[i])
		rightDistance := engine.subscriptionDistanceSquared(candidates[j])
		if leftDistance != rightDistance {
			return leftDistance < rightDistance
		}
		return chunkKeyLess(candidates[i], candidates[j])
	})
	for _, key := range candidates {
		dimension := engine.dimensions[key.Dimension]
		if dimension.CancelUnload(key.Pos) {
			result.Ready = append(result.Ready, key)
			continue
		}
		if dimension.BeginGeneration(key.Pos) {
			result.Generate = append(result.Generate, key)
		}
	}

	for key := range engine.wanted {
		if _, retained := union[key]; retained {
			continue
		}
		dimension := engine.dimensions[key.Dimension]
		if info, ok := dimension.Info(key.Pos); ok && info.State == ChunkReady {
			dimension.RequestUnload(key.Pos)
		}
	}
	engine.wanted = union
}

func (engine *Engine) subscriptionDistanceSquared(key core.ChunkKey) int64 {
	distance := int64(math.MaxInt64)
	for _, session := range engine.sessions {
		if _, wanted := session.wanted[key]; !wanted {
			continue
		}
		dx := int64(key.Pos.X - session.center.X)
		dz := int64(key.Pos.Z - session.center.Z)
		candidate := dx*dx + dz*dz
		if candidate < distance {
			distance = candidate
		}
	}
	return distance
}

func (engine *Engine) applyGenerated(
	generated []GeneratedChunk,
	result *TickResult,
) {
	sort.SliceStable(generated, func(i, j int) bool {
		left := core.ChunkKey{
			Dimension: generated[i].Dimension,
			Pos:       generated[i].Pos,
		}
		right := core.ChunkKey{
			Dimension: generated[j].Dimension,
			Pos:       generated[j].Pos,
		}
		return chunkKeyLess(left, right)
	})
	for _, generatedChunk := range generated {
		key := core.ChunkKey{
			Dimension: generatedChunk.Dimension,
			Pos:       generatedChunk.Pos,
		}
		dimension := engine.dimensions[key.Dimension]
		if dimension == nil {
			continue
		}
		info, ok := dimension.Info(key.Pos)
		if !ok || info.State != ChunkGenerating {
			continue
		}
		if generatedChunk.Err != nil {
			dimension.MarkFailed(key.Pos, generatedChunk.Err)
			continue
		}
		if err := dimension.ApplyGenerated(key.Pos, generatedChunk.Chunk); err != nil {
			dimension.MarkFailed(key.Pos, err)
			continue
		}
		if _, wanted := engine.wanted[key]; !wanted {
			dimension.RequestUnload(key.Pos)
			continue
		}
		result.Ready = append(result.Ready, key)
	}
}

func (engine *Engine) executeInteraction(
	command Command,
	pending map[core.ChunkKey]*pendingChunkChanges,
) (RejectReason, bool) {
	session := engine.sessions[command.Session]
	if command.Kind != CommandBreakBlock && command.Kind != CommandPlaceBlock {
		return RejectInvalidRay, true
	}
	if session == nil || session.player == nil ||
		session.player.lifecycle != PlayerActive {
		return RejectPlayerNotReady, true
	}
	dimensionID := session.dimension
	origin := session.player.state.Position.Add(mgl32.Vec3{0, physics.EyeHeight, 0})
	direction := LookDirection(command.Yaw, command.Pitch)
	dimension := engine.dimensions[dimensionID]
	originBlock := core.BlockPos{
		X: int32(math.Floor(float64(origin.X()))),
		Y: int32(math.Floor(float64(origin.Y()))),
		Z: int32(math.Floor(float64(origin.Z()))),
	}
	if !session.hasView || dimension == nil {
		return RejectInvalidRay, true
	}
	originKey := core.ChunkKey{
		Dimension: dimensionID,
		Pos:       originBlock.Chunk(),
	}
	if _, subscribed := session.wanted[originKey]; !subscribed {
		return RejectInvalidRay, true
	}
	if command.Kind == CommandPlaceBlock && !placeable(command.Block) {
		return RejectInvalidBlock, true
	}

	hit, ok, err := core.RaycastBlocks(
		origin,
		direction,
		interactionReach,
		func(position core.BlockPos) (bool, error) {
			block, ready := dimension.BlockAt(position)
			if !ready {
				return false, ErrChunkNotReady
			}
			return block != core.AirID, nil
		},
	)
	if err != nil {
		if errors.Is(err, ErrChunkNotReady) {
			return RejectChunkNotReady, true
		}
		return RejectInvalidRay, true
	}
	if !ok {
		return RejectNoTarget, true
	}

	switch command.Kind {
	case CommandBreakBlock:
		block, ready := dimension.BlockAt(hit.Block)
		if !ready {
			return RejectChunkNotReady, true
		}
		if block == core.BedrockID {
			return RejectProtectedBlock, true
		}
		_, changed, setErr := dimension.SetBlock(hit.Block, core.AirID)
		if setErr != nil {
			return mapSetBlockError(setErr), true
		}
		if changed {
			engine.recordChange(
				dimensionID,
				hit.Block,
				core.AirID,
				pending,
			)
		}
		return 0, false

	case CommandPlaceBlock:
		if hit.Face == core.BlockFaceNone {
			return RejectOccupied, true
		}
		target := adjacentBlock(hit.Block, hit.Face)
		if target.Y < core.MinY || target.Y >= core.MaxY {
			return RejectChunkNotReady, true
		}
		block, ready := dimension.BlockAt(target)
		if !ready {
			return RejectChunkNotReady, true
		}
		occupied := block != core.AirID || placementOverlapsPlayer(
			command.Block,
			target,
			session.player.state.Position,
		)
		if occupied {
			return RejectOccupied, true
		}
		_, changed, setErr := dimension.SetBlock(target, command.Block)
		if setErr != nil {
			return mapSetBlockError(setErr), true
		}
		if changed {
			engine.recordChange(
				dimensionID,
				target,
				command.Block,
				pending,
			)
		}
		return 0, false
	}
	return RejectInvalidRay, true
}

func validPlayerInput(command Command) bool {
	return command.MoveX >= -1 && command.MoveX <= 1 &&
		command.MoveZ >= -1 && command.MoveZ <= 1 &&
		validPlayerLook(command.Yaw, command.Pitch)
}

func validPlayerLook(yaw, pitch float32) bool {
	const maxPitch = float32(math.Pi/2 - 0.01)
	return finiteInputComponent(yaw) && finiteInputComponent(pitch) &&
		pitch >= -maxPitch && pitch <= maxPitch
}

func finiteInputComponent(value float32) bool {
	return !math.IsNaN(float64(value)) && !math.IsInf(float64(value), 0)
}

func normalizeYaw(yaw float32) float32 {
	normalized := math.Mod(float64(yaw)+math.Pi, 2*math.Pi)
	if normalized < 0 {
		normalized += 2 * math.Pi
	}
	return float32(normalized - math.Pi)
}

func (engine *Engine) recordChange(
	dimensionID core.DimensionID,
	position core.BlockPos,
	block core.BlockID,
	pending map[core.ChunkKey]*pendingChunkChanges,
) {
	key := core.ChunkKey{
		Dimension: dimensionID,
		Pos:       position.Chunk(),
	}
	changeSet := pending[key]
	if changeSet == nil {
		record := engine.dimensions[dimensionID].records[key.Pos]
		changeSet = &pendingChunkChanges{
			baseRevision: record.Revision,
			changes:      make(map[uint32]BlockChange),
			dirty:        make(map[int]struct{}),
		}
		pending[key] = changeSet
	}
	index, ok := world.ChunkBlockIndex(position)
	if !ok {
		panic("sim: changed block has no chunk index")
	}
	changeSet.changes[index] = BlockChange{
		Position: position,
		Block:    block,
	}
	changeSet.dirty[position.SectionIndex()] = struct{}{}
}

func (engine *Engine) finishChanges(
	pending map[core.ChunkKey]*pendingChunkChanges,
	result *TickResult,
) {
	keys := make([]core.ChunkKey, 0, len(pending))
	for key := range pending {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return chunkKeyLess(keys[i], keys[j])
	})
	for _, key := range keys {
		changeSet := pending[key]
		record := engine.dimensions[key.Dimension].records[key.Pos]
		for sectionIndex := range changeSet.dirty {
			record.Chunk.Section(sectionIndex).Blocks.Compact()
		}
		record.Revision++

		indices := make([]uint32, 0, len(changeSet.changes))
		for index := range changeSet.changes {
			indices = append(indices, index)
		}
		sort.Slice(indices, func(i, j int) bool {
			return indices[i] < indices[j]
		})
		changes := make([]BlockChange, 0, len(indices))
		for _, index := range indices {
			changes = append(changes, changeSet.changes[index])
		}
		result.Changes = append(result.Changes, ChunkChangeBatch{
			Dimension:    key.Dimension,
			Chunk:        key.Pos,
			BaseRevision: changeSet.baseRevision,
			NewRevision:  record.Revision,
			Changes:      changes,
		})
	}
}

func placeable(block core.BlockID) bool {
	return block == core.StoneID || block == core.DirtID || block == core.GrassID
}

func adjacentBlock(block core.BlockPos, face core.BlockFace) core.BlockPos {
	switch face {
	case core.BlockFaceNegX:
		block.X--
	case core.BlockFacePosX:
		block.X++
	case core.BlockFaceNegY:
		block.Y--
	case core.BlockFacePosY:
		block.Y++
	case core.BlockFaceNegZ:
		block.Z--
	case core.BlockFacePosZ:
		block.Z++
	default:
		panic(fmt.Sprintf("sim: invalid hit face %d", face))
	}
	return block
}

func placementOverlapsPlayer(
	block core.BlockID,
	position core.BlockPos,
	playerPosition mgl32.Vec3,
) bool {
	playerBounds := physics.PlayerBounds(playerPosition)
	boxes := physics.BlockCollisionBoxes(block, true)
	offset := mgl32.Vec3{float32(position.X), float32(position.Y), float32(position.Z)}
	for index := 0; index < min(int(boxes.Count), len(boxes.Boxes)); index++ {
		box := core.AABB{
			Min: boxes.Boxes[index].Min.Add(offset),
			Max: boxes.Boxes[index].Max.Add(offset),
		}
		if playerBounds.Overlaps(box) {
			return true
		}
	}
	return false
}

func mapSetBlockError(err error) RejectReason {
	if errors.Is(err, ErrChunkNotReady) || errors.Is(err, ErrBlockOutOfWorld) {
		return RejectChunkNotReady
	}
	return RejectInvalidRay
}

func sortChunkKeys(keys []core.ChunkKey) {
	sort.Slice(keys, func(i, j int) bool {
		return chunkKeyLess(keys[i], keys[j])
	})
}

func chunkKeyLess(left, right core.ChunkKey) bool {
	if left.Dimension != right.Dimension {
		return left.Dimension < right.Dimension
	}
	if left.Pos.X != right.Pos.X {
		return left.Pos.X < right.Pos.X
	}
	return left.Pos.Z < right.Pos.Z
}

type tickerClock struct {
	ticker *time.Ticker
}

func newTickerClock(interval time.Duration) *tickerClock {
	return &tickerClock{ticker: time.NewTicker(interval)}
}

func (clock *tickerClock) C() <-chan time.Time {
	return clock.ticker.C
}

func (clock *tickerClock) Stop() {
	clock.ticker.Stop()
}
