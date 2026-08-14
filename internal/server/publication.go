package server

import (
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/sim"
)

func (server *Server) publish(result sim.TickResult) {
	players := make(map[sim.SessionID]sim.PlayerUpdate, len(result.Players))
	for _, player := range result.Players {
		players[player.Session] = player
	}
	var definitions map[companion.ID]companion.Definition
	if len(server.config.Companions) != 0 {
		definitions = make(map[companion.ID]companion.Definition, len(server.config.Companions))
		for _, definition := range server.config.Companions {
			definitions[definition.ID] = definition
		}
	}
	for _, id := range server.sortedPublicationIDsLocked() {
		current := server.publicationSessionLocked(id)
		if current == nil || current.closed() {
			continue
		}
		if observer := server.config.InterestObserver; observer != nil {
			started := time.Now()
			server.publishSession(current, result, players, definitions)
			observer(time.Since(started))
		} else {
			server.publishSession(current, result, players, definitions)
		}
	}
}

func (server *Server) publishSession(
	current *session,
	result sim.TickResult,
	players map[sim.SessionID]sim.PlayerUpdate,
	definitions map[companion.ID]companion.Definition,
) {
	companions, ok := server.companionPublicationCandidates(current, result.Companions, definitions)
	if !ok {
		return
	}
	server.updateSessionView(current, players[current.id])
	server.queueReadyAndResync(current, result)
	server.queueCompanionSnapshots(current, companions)
	if !server.publishRemoteDespawns(current, players) {
		return
	}
	if !server.publishCompanionDespawns(current, companions) {
		return
	}
	if !server.publishForget(current, result.Forget[current.id]) {
		return
	}
	deltas := server.classifyDeltas(current, result.Changes)
	if !server.publishSnapshots(current) || !server.publishDeltas(current, deltas) {
		return
	}
	if !server.publishCompanionSpawnsAndStates(current, result.Tick, companions) {
		return
	}
	if !server.publishRemoteSpawnsAndStates(current, result.Tick, players) {
		return
	}
	if !server.publishDrops(current, result.Tick) {
		return
	}
	server.publishLocalResult(current, result, players[current.id])
}

func (server *Server) updateSessionView(current *session, player sim.PlayerUpdate) {
	if player.Session != current.id {
		return
	}
	current.hasView = true
	current.viewDimension = player.Dimension
	current.viewCenter = player.ViewCenter
}

func (server *Server) publishLocalResult(
	current *session,
	result sim.TickResult,
	playerUpdate sim.PlayerUpdate,
) {
	for _, rejection := range result.Rejected {
		if rejection.Session != current.id {
			continue
		}
		reason, ok := networkRejectReason(rejection.Reason)
		if !ok {
			slog.Error(
				"未知 sim rejection",
				"session", current.id,
				"reason", rejection.Reason,
			)
			server.closePublicationSessionLocked(
				current,
				fmt.Errorf(
					"server: unknown sim rejection: %d",
					rejection.Reason,
				),
			)
			return
		}
		if !current.enqueue(network.CommandRejected{
			Sequence: rejection.Sequence,
			Reason:   reason,
		}) {
			server.closePublicationSessionLocked(current, errSessionOutboxFull)
			return
		}
	}
	// 完整物品状态只发给所属会话，并排在使客户端开始交互的 Ready 状态之前。
	for _, update := range result.Inventories {
		if update.Session != current.id {
			continue
		}
		if !current.enqueue(network.InventoryState{Inventory: update.Inventory}) {
			server.closePublicationSessionLocked(current, errSessionOutboxFull)
			return
		}
	}
	// 熔炉状态只发给当前查看者；仅订阅区块但未打开界面的玩家不会收到。
	for _, update := range result.Furnaces {
		if update.Session != current.id {
			continue
		}
		if !current.enqueue(network.FurnaceState{
			Furnace:       update.Furnace,
			Input:         update.Input,
			Fuel:          update.Fuel,
			Output:        update.Output,
			ProgressTicks: update.ProgressTicks,
			BurnTicks:     update.BurnTicks,
		}) {
			server.closePublicationSessionLocked(current, errSessionOutboxFull)
			return
		}
	}
	// 箱子状态只发给当前查看者；仅订阅区块但未打开界面的玩家不会收到。
	for _, update := range result.Chests {
		if update.Session != current.id {
			continue
		}
		if !current.enqueue(network.ChestState{
			Chest: update.Chest,
			Items: update.Items,
		}) {
			server.closePublicationSessionLocked(current, errSessionOutboxFull)
			return
		}
	}
	// FurnaceEnds 混装熔炉与箱子两种 Kind 的失效项：core.ContainerRef 本身携带
	// Kind，ContainerClosed 不区分容器种类，因此这里不需要也不应该按 Kind 过滤。
	for _, ended := range result.FurnaceEnds {
		if ended.Session != current.id {
			continue
		}
		if !current.enqueue(network.ContainerClosed{Container: ended.Furnace}) {
			server.closePublicationSessionLocked(current, errSessionOutboxFull)
			return
		}
	}
	if playerUpdate.Session == current.id {
		if !current.enqueue(network.PlayerState{
			ServerTick:          result.Tick,
			LastInputSequence:   playerUpdate.LastInputSequence,
			Dimension:           playerUpdate.Dimension,
			Position:            playerUpdate.State.Position,
			Velocity:            playerUpdate.State.Velocity,
			Yaw:                 playerUpdate.Yaw,
			Pitch:               playerUpdate.Pitch,
			OnGround:            playerUpdate.State.OnGround,
			Ready:               playerUpdate.Ready,
			Reset:               playerUpdate.Reset,
			MiningActive:        playerUpdate.Mining.Active,
			MiningTarget:        playerUpdate.Mining.Target,
			MiningProgressTicks: playerUpdate.Mining.ProgressTicks,
			MiningRequiredTicks: playerUpdate.Mining.RequiredTicks,
			MiningHarvestable:   playerUpdate.Mining.Harvestable,
			// 生命值只随本人的权威玩家状态下发，不进入任何远端玩家消息。
			Health:         playerUpdate.Health,
			WorldTimeTicks: playerUpdate.WorldTimeTicks,
		}) {
			server.closePublicationSessionLocked(current, errSessionOutboxFull)
		}
	}
}

func (server *Server) closePublicationSessionLocked(current *session, cause error) {
	if current == server.trustedObserver {
		server.detachTrustedObserverLocked(
			current.id,
			current.generation,
			cause,
		)
		return
	}
	server.detachSessionLocked(current.id, current.generation, cause)
}

func (server *Server) sortedPublicationIDsLocked() []sim.SessionID {
	ids := server.sortedSessionIDsLocked()
	if server.trustedObserver != nil {
		ids = append(ids, server.trustedObserver.id)
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	}
	return ids
}

func (server *Server) publicationSessionLocked(id sim.SessionID) *session {
	if server.trustedObserver != nil && server.trustedObserver.id == id {
		return server.trustedObserver
	}
	return server.sessions[id]
}
