//go:build darwin

package main

import (
	"log/slog"
	"runtime"

	"minecraft-go/internal/client"
	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/render/hud"
)

func (a *application) drainServerMessages(maxMessages int) {
	if maxMessages <= 0 {
		return
	}
	for range maxMessages {
		if a.receiver == nil {
			return
		}
		message, ok := a.receiver.TryRecv()
		if !ok {
			runtime.Gosched()
			message, ok = a.receiver.TryRecv()
			if !ok {
				return
			}
		}
		if state, ok := message.(network.PlayerState); ok {
			if state.ServerTick <= a.serverTick {
				continue
			}
			result, err := a.predictor.ApplyPlayerState(state, client.MirrorCollisionSource{
				Mirror:    a.mirror,
				Dimension: core.Overworld,
			})
			if err != nil {
				a.closeClientSession(err)
				return
			}
			a.serverTick = state.ServerTick
			a.worldTimeTicks = state.WorldTimeTicks
			if state.Reset || !state.MiningActive {
				a.miningOverlay = hud.MiningOverlay{}
			} else {
				a.miningOverlay = hud.MiningOverlay{
					Active:        true,
					ProgressTicks: state.MiningProgressTicks,
					RequiredTicks: state.MiningRequiredTicks,
					Harvestable:   state.MiningHarvestable,
				}
			}
			if state.Reset {
				if a.containerOpen() {
					a.clearContainerUI()
				} else {
					a.inventorySource = -1
				}
			}
			if result.ResetView {
				a.camera.Yaw = result.Yaw
				a.camera.Pitch = result.Pitch
			}
			continue
		}
		if state, ok := message.(network.InventoryState); ok {
			if err := a.inventory.Apply(state); err != nil {
				a.closeClientSession(err)
				return
			}
			continue
		}
		if state, ok := message.(network.FurnaceState); ok {
			previous, opened := a.furnace.Ref()
			if err := a.furnace.Apply(state); err != nil {
				a.closeClientSession(err)
				return
			}
			// 熔炉与箱子互斥：新熔炉状态到达时丢弃可能过期的箱子镜像，
			// 否则两个镜像会同时报告 opened，点击分流会用错容器。
			a.chest.Reset()
			if !opened || previous != state.Furnace {
				a.inventorySource = -1
			}
			a.inventoryOpen = true
			if a.window != nil {
				a.window.SetCursorCaptured(false)
			}
			continue
		}
		if state, ok := message.(network.ChestState); ok {
			previous, opened := a.chest.Ref()
			if err := a.chest.Apply(state); err != nil {
				a.closeClientSession(err)
				return
			}
			a.furnace.Reset()
			if !opened || previous != state.Chest {
				a.inventorySource = -1
			}
			a.inventoryOpen = true
			if a.window != nil {
				a.window.SetCursorCaptured(false)
			}
			continue
		}
		if closed, ok := message.(network.ContainerClosed); ok {
			furnaceCurrent, furnaceOpened := a.furnace.Ref()
			if err := a.furnace.Close(closed); err != nil {
				a.closeClientSession(err)
				return
			}
			chestCurrent, chestOpened := a.chest.Ref()
			if err := a.chest.Close(closed); err != nil {
				a.closeClientSession(err)
				return
			}
			if (furnaceOpened && furnaceCurrent == closed.Container) ||
				(chestOpened && chestCurrent == closed.Container) {
				a.clearContainerUI()
			}
			continue
		}
		switch message.(type) {
		case network.ItemDropUpserts, network.ItemDropRemoves:
			if err := a.itemDrops.Apply(message); err != nil {
				a.closeClientSession(err)
				return
			}
			continue
		}
		switch message.(type) {
		case network.RemotePlayerSpawn, *network.RemotePlayerSpawn,
			network.RemotePlayerDespawn, *network.RemotePlayerDespawn,
			network.RemotePlayerStates, *network.RemotePlayerStates:
			if err := a.remotePlayers.Apply(message); err != nil {
				a.closeClientSession(err)
				return
			}
			continue
		}
		update, err := a.mirror.Apply(message)
		if err != nil {
			a.closeClientSession(err)
			return
		}
		switch message := message.(type) {
		case network.ChunkSnapshot:
			if message.Dimension == core.Overworld {
				a.loadedChunks[message.Chunk] = struct{}{}
			}
		case network.ForgetChunks:
			if message.Dimension == core.Overworld {
				for _, position := range message.Chunks {
					delete(a.loadedChunks, position)
				}
			}
		}
		if update.Resync != nil {
			update.Resync.Sequence = a.nextSequence()
			if err := a.send(update.Resync); err != nil {
				slog.Warn("发送区块 resync 失败", "error", err)
			}
		}
		if update.Rejected != nil {
			slog.Warn("权威命令被拒绝",
				"sequence", update.Rejected.Sequence, "reason", update.Rejected.Reason)
		}
		a.mesher.MarkDirty(update.Dirty...)
		for _, key := range update.Forgotten {
			if key.Dimension != core.Overworld {
				continue
			}
			a.renderer.DropSection(key.Pos)
			if key.Pos.Y == 0 {
				a.mesher.ForgetChunk(key.Dimension, core.ChunkPos{X: key.Pos.X, Z: key.Pos.Z})
			}
		}
	}
}

// dropSelectedItem 请求把权威选中栏位中的一个物品丢到脚下。
// 客户端不预测：不读也不改本地背包镜像，不创建本地掉落物。
