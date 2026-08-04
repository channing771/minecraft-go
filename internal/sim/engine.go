package sim

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
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
	trustedObserver             bool
	hasView                     bool
	dimension                   core.DimensionID
	center                      core.ChunkPos
	wanted                      map[core.ChunkKey]struct{}
	player                      *playerState
	// 每名玩家最多查看一个熔炉；引用失效时由权威 tick 统一清除。
	furnace     core.FurnaceRef
	viewFurnace bool
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
	inFlightSaves      map[core.ChunkKey]persistenceInFlight
	subscriptionsDirty bool

	// 掉落物 tick 的复用 scratch，避免每 tick 分配固定上限集合。
	dropKeySeen          map[core.ChunkKey]struct{}
	dropKeyScratch       []core.ChunkKey
	furnaceViewerScratch []SessionID
	dropSessionScratch   []SessionID

	inboxMu   sync.Mutex
	commands  []Command
	acquired  []AcquiredChunk
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
		sessions:      make(map[SessionID]*sessionState),
		wanted:        make(map[core.ChunkKey]struct{}),
		inFlightSaves: make(map[core.ChunkKey]persistenceInFlight),
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

// SubmitAcquired 可由区块读取 worker 并发调用，并转移 Chunk 所有权。
func (engine *Engine) SubmitAcquired(result AcquiredChunk) {
	engine.inboxMu.Lock()
	engine.acquired = append(engine.acquired, result)
	engine.inboxMu.Unlock()
}

// Step 严格串行执行一个权威 tick。
func (engine *Engine) Step() TickResult {
	commands, acquired, generated := engine.takeInbox()
	sort.SliceStable(commands, func(i, j int) bool {
		if commands[i].Session != commands[j].Session {
			return commands[i].Session < commands[j].Session
		}
		return commands[i].Sequence < commands[j].Sequence
	})

	result := TickResult{Forget: make(map[SessionID][]core.ChunkKey)}
	interactions := make([]Command, 0, len(commands))
	furnaceMoves := make([]Command, 0, len(commands))
	viewChanged := false
	for _, command := range commands {
		session := engine.sessions[command.Session]
		if session == nil {
			continue
		}
		if command.Kind == CommandTrustedObserverCenter {
			if !session.trustedObserver {
				continue
			}
			if command.Sequence <= session.lastTrustedObserverSequence {
				continue
			}
			session.lastTrustedObserverSequence = command.Sequence
			session.hasView = true
			session.dimension = command.Dimension
			session.center = command.Center
			viewChanged = true
			continue
		}
		if session.trustedObserver {
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
				player.miningHeld = false
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
			player.miningHeld = command.Mining
			player.yaw = yaw
			player.pitch = command.Pitch
		case CommandSelectHotbar:
			if session.player == nil || session.player.lifecycle != PlayerActive {
				result.Rejected = append(result.Rejected, Rejection{
					Session:  command.Session,
					Sequence: command.Sequence,
					Reason:   RejectPlayerNotReady,
				})
				continue
			}
			if command.Slot >= core.HotbarSlots {
				result.Rejected = append(result.Rejected, Rejection{
					Session:  command.Session,
					Sequence: command.Sequence,
					Reason:   RejectInvalidSlot,
				})
				continue
			}
			player := session.player
			if player.inventory.Hotbar.Selected != command.Slot {
				player.inventory.Hotbar.Selected = command.Slot
				player.inventoryDirty = true
			}
		case CommandMoveInventoryStack:
			if session.player == nil || session.player.lifecycle != PlayerActive {
				result.Rejected = append(result.Rejected, Rejection{
					Session:  command.Session,
					Sequence: command.Sequence,
					Reason:   RejectPlayerNotReady,
				})
				continue
			}
			if command.Slot >= core.InventorySlots || command.ToSlot >= core.InventorySlots {
				result.Rejected = append(result.Rejected, Rejection{
					Session:  command.Session,
					Sequence: command.Sequence,
					Reason:   RejectInvalidSlot,
				})
				continue
			}
			player := session.player
			next, ok := player.inventory.MoveStack(command.Slot, command.ToSlot)
			if !ok {
				result.Rejected = append(result.Rejected, Rejection{
					Session:  command.Session,
					Sequence: command.Sequence,
					Reason:   RejectInvalidInput,
				})
				continue
			}
			player.inventory = next
			player.inventoryDirty = true
		case CommandOpenFurnace:
			if reason, rejected := engine.openFurnace(command.Session, command); rejected {
				result.Rejected = append(result.Rejected, Rejection{
					Session:  command.Session,
					Sequence: command.Sequence,
					Reason:   reason,
				})
			}
		case CommandMoveFurnaceStack:
			// 跨容器移动会改动区块，必须与其他交互共享同一批 pending 变化。
			furnaceMoves = append(furnaceMoves, command)
		case CommandCloseFurnace:
			// 关闭永远成功：客户端可以随时结束查看关系。
			session.viewFurnace = false
			session.furnace = core.FurnaceRef{}
		case CommandCraftRecipe:
			if session.player == nil || session.player.lifecycle != PlayerActive {
				result.Rejected = append(result.Rejected, Rejection{
					Session:  command.Session,
					Sequence: command.Sequence,
					Reason:   RejectPlayerNotReady,
				})
				continue
			}
			player := session.player
			next, ok := player.inventory.Craft(command.Recipe)
			if !ok {
				result.Rejected = append(result.Rejected, Rejection{
					Session:  command.Session,
					Sequence: command.Sequence,
					Reason:   RejectInvalidInput,
				})
				continue
			}
			player.inventory = next
			player.inventoryDirty = true
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
	var currentWanted map[core.ChunkKey]struct{}
	if len(acquired) != 0 || len(generated) != 0 {
		currentWanted = engine.wantedSnapshot()
	}
	engine.applyAcquired(acquired, currentWanted, &result)
	engine.applyGenerated(generated, currentWanted, &result)
	engine.advancePendingPlayersPreservingInputSequence()
	engine.advanceActivePlayers()
	playerViewChanged := engine.derivePlayerCenters()
	viewChanged = viewChanged || playerViewChanged || engine.subscriptionsDirty
	engine.subscriptionsDirty = false
	if viewChanged {
		engine.reconcileSubscriptions(&result)
	}

	pending := make(map[core.ChunkKey]*pendingChunkChanges)
	engine.advanceDrops(pending)
	engine.advanceFurnaces(pending)
	for _, command := range furnaceMoves {
		if reason, rejected := engine.applyFurnaceMove(command.Session, command, pending); rejected {
			result.Rejected = append(result.Rejected, Rejection{
				Session:  command.Session,
				Sequence: command.Sequence,
				Reason:   reason,
			})
		}
	}
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
	sortChunkKeys(result.Ready)

	result.Tick = engine.tick.Add(1)
	engine.publishInventories(&result)
	engine.publishFurnaces(&result)
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

func (engine *Engine) takeInbox() ([]Command, []AcquiredChunk, []GeneratedChunk) {
	engine.inboxMu.Lock()
	commands := append([]Command(nil), engine.commands...)
	acquired := append([]AcquiredChunk(nil), engine.acquired...)
	generated := append([]GeneratedChunk(nil), engine.generated...)
	engine.commands = engine.commands[:0]
	engine.acquired = engine.acquired[:0]
	engine.generated = engine.generated[:0]
	engine.inboxMu.Unlock()
	return commands, acquired, generated
}

func (engine *Engine) RegisterObserverSession(id SessionID) {
	if engine.sessions[id] != nil {
		panic("sim: duplicate registered session")
	}
	engine.sessions[id] = &sessionState{
		trustedObserver: true,
		wanted:          make(map[core.ChunkKey]struct{}),
	}
}

// WantsChunk reports whether the current union subscription still needs key.
// Callers serialize this query with Step and session lifecycle operations.
func (engine *Engine) WantsChunk(key core.ChunkKey) bool {
	_, wanted := engine.wanted[key]
	return wanted
}

// SessionWantsChunk reports whether id currently subscribes to key.
// Callers serialize this query with Step and session lifecycle operations.
func (engine *Engine) SessionWantsChunk(id SessionID, key core.ChunkKey) bool {
	session := engine.sessions[id]
	if session == nil {
		return false
	}
	_, wanted := session.wanted[key]
	return wanted
}

func (engine *Engine) reconcileSubscriptions(result *TickResult) {
	union := make(map[core.ChunkKey]struct{})
	for sessionID, session := range engine.sessions {
		next := engine.sessionWantedSnapshot(session)
		for key := range next {
			union[key] = struct{}{}
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
		if dimension.BeginLoading(key.Pos) {
			result.Acquire = append(result.Acquire, key)
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

func (engine *Engine) wantedSnapshot() map[core.ChunkKey]struct{} {
	wanted := make(map[core.ChunkKey]struct{})
	for _, session := range engine.sessions {
		for key := range engine.sessionWantedSnapshot(session) {
			wanted[key] = struct{}{}
		}
	}
	return wanted
}

func (engine *Engine) sessionWantedSnapshot(
	session *sessionState,
) map[core.ChunkKey]struct{} {
	wanted := make(map[core.ChunkKey]struct{})
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
				wanted[key] = struct{}{}
			}
		}
	}
	if session.player != nil && session.player.lifecycle == PlayerPendingSpawn {
		for key := range session.player.restoreWanted {
			wanted[key] = struct{}{}
		}
		for chunk := range session.player.spawnWanted {
			wanted[core.ChunkKey{Dimension: session.dimension, Pos: chunk}] = struct{}{}
		}
	}
	return wanted
}

func (engine *Engine) applyAcquired(
	acquired []AcquiredChunk,
	wanted map[core.ChunkKey]struct{},
	result *TickResult,
) {
	sort.SliceStable(acquired, func(i, j int) bool {
		return chunkKeyLess(acquired[i].Key, acquired[j].Key)
	})
	for _, acquiredChunk := range acquired {
		key := acquiredChunk.Key
		dimension := engine.dimensions[key.Dimension]
		if dimension == nil {
			continue
		}
		info, ok := dimension.Info(key.Pos)
		if !ok || info.State != ChunkLoading {
			continue
		}
		switch {
		case acquiredChunk.Err != nil:
			dimension.MarkLoadFailed(key.Pos, acquiredChunk.Err)
		case acquiredChunk.Missing:
			if _, retained := wanted[key]; !retained {
				dimension.DropLoading(key.Pos)
			} else if dimension.MarkGenerating(key.Pos) {
				result.Generate = append(result.Generate, key)
			}
		default:
			err := dimension.ApplyLoaded(
				key.Pos,
				acquiredChunk.Chunk,
				acquiredChunk.Revision,
				acquiredChunk.PersistedRevision,
				acquiredChunk.NeedsRewrite,
				acquiredChunk.Recovered,
			)
			if err != nil {
				dimension.MarkLoadFailed(key.Pos, err)
				continue
			}
			if _, retained := wanted[key]; !retained {
				dimension.RequestUnload(key.Pos)
				continue
			}
			result.Ready = append(result.Ready, key)
		}
	}
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
	wanted map[core.ChunkKey]struct{},
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
		if _, retained := wanted[key]; !retained {
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
	player := session.player
	// 放置先从请求栏位解析物品和方块，并在快捷栏副本上预演扣除。
	var placement core.BlockID
	var consumed core.Hotbar
	if command.Kind == CommandPlaceBlock {
		if command.Slot >= core.HotbarSlots {
			return RejectInvalidSlot, true
		}
		block, ok := core.ItemPlacement(player.inventory.Hotbar.Slots[command.Slot].Item)
		if !ok {
			return RejectInvalidBlock, true
		}
		next, ok := player.inventory.Hotbar.Consume(command.Slot)
		if !ok {
			return RejectInvalidBlock, true
		}
		placement = block
		consumed = next
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
		item, ok := core.BlockDrop(block)
		if !ok {
			return RejectProtectedBlock, true
		}
		// 先预检所属区块的掉落物容量，容量不足时方块保持不变。
		record, recordOK := dimension.records[hit.Block.Chunk()]
		blockIndex, indexOK := world.ChunkBlockIndex(hit.Block)
		if !recordOK || record.Chunk == nil || !indexOK {
			return RejectChunkNotReady, true
		}
		// 熔炉的本体与三格内容必须一次性全部掉落，否则整条命令拒绝。
		furnaceSlot, isFurnace := -1, false
		var batch [core.DropsPerChunk]world.DropSlot
		var dropSlot int
		if block == core.FurnaceID {
			slot, found := record.Chunk.FurnaceAt(blockIndex)
			if !found {
				return RejectChunkNotReady, true
			}
			furnace := record.Chunk.Furnace(slot)
			next, batchOK := record.Chunk.PrepareDropBatch([4]core.ItemStack{
				{Item: item, Count: 1},
				furnace.Input,
				furnace.Fuel,
				furnace.Output,
			}, blockIndex, DropPickupDelayTicks)
			if !batchOK {
				return RejectDropCapacity, true
			}
			batch, furnaceSlot, isFurnace = next, slot, true
		} else {
			var capacityOK bool
			dropSlot, capacityOK = record.Chunk.PrepareDrop(item, blockIndex)
			if !capacityOK {
				return RejectDropCapacity, true
			}
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
			if isFurnace {
				record.Chunk.DeactivateFurnace(furnaceSlot)
				record.Chunk.CommitDropBatch(batch)
			} else {
				record.Chunk.CommitDrop(dropSlot, item, blockIndex, DropPickupDelayTicks)
			}
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
			placement,
			target,
			player.state.Position,
		)
		if occupied {
			return RejectOccupied, true
		}
		// 放置熔炉必须先预留槽位；槽位耗尽时不改方块也不扣物品。
		targetRecord, targetOK := dimension.records[target.Chunk()]
		targetIndex, targetIndexed := world.ChunkBlockIndex(target)
		furnaceSlot, reserveFurnace := -1, false
		if placement == core.FurnaceID {
			if !targetOK || targetRecord.Chunk == nil || !targetIndexed {
				return RejectChunkNotReady, true
			}
			slot, ok := targetRecord.Chunk.PrepareFurnace(targetIndex)
			if !ok {
				return RejectFurnaceCapacity, true
			}
			furnaceSlot, reserveFurnace = slot, true
		}
		_, changed, setErr := dimension.SetBlock(target, placement)
		if setErr != nil {
			return mapSetBlockError(setErr), true
		}
		if changed {
			engine.recordChange(
				dimensionID,
				target,
				placement,
				pending,
			)
			if reserveFurnace {
				targetRecord.Chunk.CommitFurnace(furnaceSlot, targetIndex)
			}
			player.inventory.Hotbar = consumed
			player.inventoryDirty = true
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

// sortChunkKeys 用泛型排序避免 sort.Slice 的反射 swapper 分配，
// 使权威 tick 的热路径保持零分配。
func sortChunkKeys(keys []core.ChunkKey) {
	slices.SortFunc(keys, func(left, right core.ChunkKey) int {
		switch {
		case chunkKeyLess(left, right):
			return -1
		case chunkKeyLess(right, left):
			return 1
		default:
			return 0
		}
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
