package sim

import (
	"sort"

	"minecraft-go/internal/core"
	"minecraft-go/internal/physics"
)

// Step 严格串行执行一个权威 tick。
func (engine *Engine) Step() TickResult {
	engine.tunables = ActiveTunables()
	engine.physicsTunables = physics.ActiveTunables()
	commands, acquired, generated := engine.takeInbox()
	sort.SliceStable(commands, func(i, j int) bool {
		if commands[i].Session != commands[j].Session {
			return commands[i].Session < commands[j].Session
		}
		return commands[i].Sequence < commands[j].Sequence
	})

	result := TickResult{Forget: make(map[SessionID][]core.ChunkKey)}
	interactions := make([]Command, 0, len(commands))
	containerMoves := make([]Command, 0, len(commands))
	// 命令阶段与后续掉落物/熔炉推进共用同一份待提交区块变更。
	pending := make(map[core.ChunkKey]*pendingChunkChanges)
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
		case CommandPlaceBlock:
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
				if session.player != nil {
					session.player.miningHeld = false
					session.player.mining = playerMiningState{}
				}
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
				player.mining = playerMiningState{}
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
			interactions = append(interactions, command)
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
			if reason, rejected := engine.openContainer(command.Session, command); rejected {
				result.Rejected = append(result.Rejected, Rejection{
					Session:  command.Session,
					Sequence: command.Sequence,
					Reason:   reason,
				})
			}
		case CommandMoveFurnaceStack:
			// 跨容器移动会改动区块，必须与其他交互共享同一批 pending 变化。
			containerMoves = append(containerMoves, command)
		case CommandCloseFurnace:
			// 关闭永远成功：客户端可以随时结束查看关系。
			session.viewContainer = false
			session.container = core.ContainerRef{}
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
		case CommandDropSelectedItem:
			if session.player == nil || session.player.lifecycle != PlayerActive {
				result.Rejected = append(result.Rejected, Rejection{
					Session:  command.Session,
					Sequence: command.Sequence,
					Reason:   RejectPlayerNotReady,
				})
				continue
			}
			if session.player.inventory.Hotbar.Selected >= core.HotbarSlots {
				result.Rejected = append(result.Rejected, Rejection{
					Session:  command.Session,
					Sequence: command.Sequence,
					Reason:   RejectInvalidSlot,
				})
				continue
			}
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

	// 阶段顺序契约：所有区块写者必须位于 reconcileSubscriptions 之后。订阅收缩会把
	// 干净区块（Revision == PersistedRevision）从 records 里立即删除，写在它之前的
	// 写者留下的 revision barrier 会在 finishChanges 取到 nil record 而崩溃，
	// 掉落物也随被删除的 record 一起消失。死亡结算是唯一会在写区块的同一 tick 里
	// 让玩家跳回出生锚点、从而收缩订阅的写者，因此这条契约对它尤其关键：
	// beginReset 置的 subscriptionsDirty 顺延到下一 tick 生效，而彼时 finishChanges
	// 已经推高 revision，区块转脏，RequestUnload 只会走 Unloading 分支。
	// settleDeaths 同时必须早于本 tick 末尾的状态发布，外部才观察不到生命值为 0 的
	// 中间状态。
	engine.settleDeaths(pending)

	for _, command := range interactions {
		switch command.Kind {
		case CommandPlaceBlock:
			if reason, rejected := engine.executePlacement(command, pending); rejected {
				result.Rejected = append(result.Rejected, Rejection{
					Session:  command.Session,
					Sequence: command.Sequence,
					Reason:   reason,
				})
			}
		case CommandSelectHotbar:
			player := engine.sessions[command.Session].player
			if player.inventory.Hotbar.Selected != command.Slot {
				player.inventory.Hotbar.Selected = command.Slot
				player.inventoryDirty = true
			}
		case CommandDropSelectedItem:
			if reason, rejected := engine.dropSelectedItem(engine.sessions[command.Session], pending); rejected {
				result.Rejected = append(result.Rejected, Rejection{
					Session:  command.Session,
					Sequence: command.Sequence,
					Reason:   reason,
				})
			}
		}
	}
	engine.advanceDrops(pending)
	engine.advanceFurnaces(pending)
	for _, command := range containerMoves {
		if reason, rejected := engine.applyContainerMove(command.Session, command, pending); rejected {
			result.Rejected = append(result.Rejected, Rejection{
				Session:  command.Session,
				Sequence: command.Sequence,
				Reason:   reason,
			})
		}
	}
	engine.advanceMining(pending, &result)
	engine.finishChanges(pending, &result)
	sortChunkKeys(result.Ready)

	result.Tick = engine.tick.Add(1)
	result.WorldTimeTicks = engine.advanceWorldTime()
	engine.publishInventories(&result)
	engine.publishContainers(&result)
	engine.publishPlayers(&result)
	return result
}
