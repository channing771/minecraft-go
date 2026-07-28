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
	lastSequence uint64
	hasView      bool
	dimension    core.DimensionID
	center       core.ChunkPos
	wanted       map[core.ChunkKey]struct{}
	player       *playerState
}

type pendingChunkChanges struct {
	baseRevision uint64
	changes      map[uint32]BlockChange
	dirty        map[int]struct{}
}

type Engine struct {
	base               BaseBlockLookup
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

func NewEngine(base BaseBlockLookup, viewRadius int) *Engine {
	if base == nil {
		panic("sim: nil base block lookup")
	}
	if viewRadius < 0 {
		panic("sim: negative view radius")
	}
	return &Engine{
		base:       base,
		viewRadius: viewRadius,
		dimensions: map[core.DimensionID]*Dimension{
			core.Overworld: NewDimension(core.Overworld, base),
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
		if command.Sequence <= session.lastSequence {
			continue
		}
		session.lastSequence = command.Sequence
		switch command.Kind {
		case CommandSetViewCenter:
			if session.player != nil {
				continue
			}
			session.hasView = true
			session.dimension = command.Dimension
			session.center = command.Center
			viewChanged = true
		case CommandBreakRay, CommandPlaceRay:
			interactions = append(interactions, command)
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
	viewChanged = viewChanged || engine.subscriptionsDirty
	engine.subscriptionsDirty = false
	if viewChanged {
		engine.reconcileSubscriptions(&result)
	}

	engine.applyGenerated(generated, &result)

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
	engine.advancePendingPlayers()

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
		if engine.dimensions[key.Dimension].BeginGeneration(key.Pos) {
			result.Generate = append(result.Generate, key)
		}
	}

	for key := range engine.wanted {
		if _, retained := union[key]; retained {
			continue
		}
		dimension := engine.dimensions[key.Dimension]
		if info, ok := dimension.Info(key.Pos); ok && info.State == ChunkReady {
			_ = dimension.Unload(key.Pos)
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
			_ = dimension.Unload(key.Pos)
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
	dimension := engine.dimensions[command.Dimension]
	originBlock, valid := rayOriginBlock(command.Origin)
	if !valid || !validDirection(command.Direction) ||
		session == nil || !session.hasView ||
		session.dimension != command.Dimension ||
		dimension == nil {
		return RejectInvalidRay, true
	}
	originKey := core.ChunkKey{
		Dimension: command.Dimension,
		Pos:       originBlock.Chunk(),
	}
	if _, subscribed := session.wanted[originKey]; !subscribed {
		return RejectInvalidRay, true
	}
	if command.Kind == CommandPlaceRay && !placeable(command.Block) {
		return RejectInvalidBlock, true
	}

	hit, ok, err := core.RaycastBlocks(
		command.Origin,
		command.Direction,
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
	case CommandBreakRay:
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
				command.Dimension,
				hit.Block,
				core.AirID,
				pending,
			)
		}
		return 0, false

	case CommandPlaceRay:
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
		if block != core.AirID || blockContains(target, command.Origin) {
			return RejectOccupied, true
		}
		_, changed, setErr := dimension.SetBlock(target, command.Block)
		if setErr != nil {
			return mapSetBlockError(setErr), true
		}
		if changed {
			engine.recordChange(
				command.Dimension,
				target,
				command.Block,
				pending,
			)
		}
		return 0, false
	}
	return RejectInvalidRay, true
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

func rayOriginBlock(origin mgl32.Vec3) (core.BlockPos, bool) {
	for _, component := range origin {
		if math.IsNaN(float64(component)) || math.IsInf(float64(component), 0) {
			return core.BlockPos{}, false
		}
	}
	x := math.Floor(float64(origin[0]))
	y := math.Floor(float64(origin[1]))
	z := math.Floor(float64(origin[2]))
	if x < math.MinInt32 || x > math.MaxInt32 ||
		y < math.MinInt32 || y > math.MaxInt32 ||
		z < math.MinInt32 || z > math.MaxInt32 {
		return core.BlockPos{}, false
	}
	return core.BlockPos{X: int32(x), Y: int32(y), Z: int32(z)}, true
}

func validDirection(direction mgl32.Vec3) bool {
	for _, component := range direction {
		if math.IsNaN(float64(component)) || math.IsInf(float64(component), 0) {
			return false
		}
	}
	length := math.Hypot(
		math.Hypot(float64(direction[0]), float64(direction[1])),
		float64(direction[2]),
	)
	return length >= 1e-6
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

func blockContains(block core.BlockPos, point mgl32.Vec3) bool {
	return point[0] >= float32(block.X) && point[0] < float32(block.X+1) &&
		point[1] >= float32(block.Y) && point[1] < float32(block.Y+1) &&
		point[2] >= float32(block.Z) && point[2] < float32(block.Z+1)
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
