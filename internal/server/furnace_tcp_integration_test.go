package server

import (
	"context"
	"math"
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/world"
)

// TestFurnaceSharedByTwoPlayersOverTCP 覆盖两名玩家共享同一个熔炉的完整闭环：
// 同时打开、交错投料、推进出锭、一人取走后另一人看到输出为空，旧引用被拒绝。
func TestFurnaceSharedByTwoPlayersOverTCP(t *testing.T) {
	root := t.TempDir()
	key := core.ChunkKey{Dimension: core.Overworld}
	firstIdentity := integrationIdentity(0x91, "Smelter")
	secondIdentity := integrationIdentity(0x92, "Helper")
	spawn := integrationPlayerSnapshotAt(0.5, 1.001, 0.5, nil)
	seedIntegrationPlayer(t, root, firstIdentity, spawn)
	seedIntegrationPlayer(t, root, secondIdentity, spawn)

	host := startDiskHost(t, root, "127.0.0.1:0", changedGenerator{})
	firstClient := dialIntegrationClient(t, host.Addr, firstIdentity)
	secondClient := dialIntegrationClient(t, host.Addr, secondIdentity)
	waitClientReadyFor(t, host, firstClient, firstIdentity.PlayerID)
	waitClientReadyFor(t, host, secondClient, secondIdentity.PlayerID)

	index, ok := world.ChunkBlockIndex(core.BlockPos{})
	if !ok {
		t.Fatal("熔炉位置没有区块索引")
	}
	host.Host.world.SetBlockForTest(core.BlockPos{}, core.FurnaceID)
	host.Host.world.SetChunkFurnaceForTest(key, 0, world.FurnaceSlot{
		Generation: 1, Active: true, BlockIndex: index,
		Input: core.ItemStack{Item: core.ItemRawIron, Count: 2},
		Fuel:  core.ItemStack{Item: core.ItemCoal, Count: 1},
	})

	// 两名玩家同时打开同一个熔炉。
	sendIntegration(t, firstClient.Endpoint, network.OpenContainer{
		Sequence: 10, Pitch: -float32(math.Pi)/2 + 0.01,
	})
	sendIntegration(t, secondClient.Endpoint, network.OpenContainer{
		Sequence: 10, Pitch: -float32(math.Pi)/2 + 0.01,
	})
	firstRef := waitFurnaceState(t, firstClient, func(network.FurnaceState) bool { return true })
	secondRef := waitFurnaceState(t, secondClient, func(network.FurnaceState) bool { return true })
	if firstRef.Furnace != secondRef.Furnace {
		t.Fatalf("两名查看者的引用不同: %+v vs %+v", firstRef.Furnace, secondRef.Furnace)
	}

	// 推进到产出第一个铁锭，两端必须同时看到。
	produced := func(state network.FurnaceState) bool {
		return state.Output.Item == core.ItemIronIngot && state.Output.Count >= 1
	}
	waitFurnaceState(t, firstClient, produced)
	waitFurnaceState(t, secondClient, produced)

	// 第一名玩家取走输出后，另一名玩家必须看到输出为空。
	sendIntegration(t, firstClient.Endpoint, network.MoveContainerStack{
		Sequence: 11, Container: firstRef.Furnace,
		From: core.FurnaceOutputSlot, To: 0,
	})
	emptied := func(state network.FurnaceState) bool {
		return state.Output.Item == core.ItemNone
	}
	waitFurnaceState(t, secondClient, emptied)

	// 旧 generation 的引用必须稳定拒绝。
	stale := firstRef.Furnace
	stale.Generation++
	sendIntegration(t, secondClient.Endpoint, network.MoveContainerStack{
		Sequence: 12, Container: stale, From: 0, To: core.FurnaceInputSlot,
	})
	waitIntegrationRejection(t, secondClient, 12, network.RejectInvalidInput)

	if err := firstClient.Close(); err != nil {
		t.Fatal(err)
	}
	if err := secondClient.Close(); err != nil {
		t.Fatal(err)
	}
	host.Shutdown(t)
}

func TestFurnaceInputKindResetSurvivesTCPRestart(t *testing.T) {
	root := t.TempDir()
	key := core.ChunkKey{Dimension: core.Overworld}
	identity := integrationIdentity(0x93, "Glassmaker")
	spawn := integrationPlayerSnapshotAt(0.5, 1.001, 0.5, nil)
	spawn.Inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemSand, Count: 3}
	seedIntegrationPlayer(t, root, identity, spawn)

	first := startDiskHost(t, root, "127.0.0.1:0", changedGenerator{})
	firstClient := dialIntegrationClient(t, first.Addr, identity)
	waitClientReadyFor(t, first, firstClient, identity.PlayerID)
	index, ok := world.ChunkBlockIndex(core.BlockPos{})
	if !ok {
		t.Fatal("熔炉位置没有区块索引")
	}
	first.Host.world.SetBlockForTest(core.BlockPos{}, core.FurnaceID)
	first.Host.world.SetChunkFurnaceForTest(key, 0, world.FurnaceSlot{
		Generation: 1, Active: true, BlockIndex: index,
		Input:         core.ItemStack{Item: core.ItemRawIron, Count: 2},
		Output:        core.ItemStack{Item: core.ItemBrick, Count: 1},
		ProgressTicks: 137,
		BurnTicks:     1463,
	})

	sendIntegration(t, firstClient.Endpoint, network.OpenContainer{
		Sequence: 10, Pitch: -float32(math.Pi)/2 + 0.01,
	})
	opened := waitFurnaceState(t, firstClient, func(network.FurnaceState) bool { return true })
	sendIntegration(t, firstClient.Endpoint, network.MoveContainerStack{
		Sequence: 11, Container: opened.Furnace, From: 0, To: core.FurnaceInputSlot,
	})
	wantInput := core.ItemStack{Item: core.ItemSand, Count: 3}
	switched := waitFurnaceState(t, firstClient, func(state network.FurnaceState) bool {
		return state.Input == wantInput
	})
	if switched.ProgressTicks != 0 || switched.BurnTicks != 1463 {
		t.Fatalf("TCP 切换状态 = %+v，想要进度清零且保留燃烧 tick", switched)
	}

	if err := firstClient.Close(); err != nil {
		t.Fatal(err)
	}
	first.Shutdown(t)

	second := startDiskHost(t, root, "127.0.0.1:0", changedGenerator{})
	secondClient := dialIntegrationClient(t, second.Addr, identity)
	waitClientReadyFor(t, second, secondClient, identity.PlayerID)
	sendIntegration(t, secondClient.Endpoint, network.OpenContainer{
		Sequence: 12, Pitch: -float32(math.Pi)/2 + 0.01,
	})
	restored := waitFurnaceState(t, secondClient, func(state network.FurnaceState) bool {
		return state.Input == wantInput
	})
	if restored.ProgressTicks != 0 || restored.BurnTicks != 1463 ||
		restored.Output != (core.ItemStack{Item: core.ItemBrick, Count: 1}) {
		t.Fatalf("TCP 重连并重启后的熔炉 = %+v，想要进度清零且保留燃烧 tick", restored)
	}

	if err := secondClient.Close(); err != nil {
		t.Fatal(err)
	}
	second.Shutdown(t)
}

// waitFurnaceState 等待某个客户端收到满足条件的熔炉状态。
func waitFurnaceState(
	t *testing.T,
	connected integrationClient,
	accept func(network.FurnaceState) bool,
) network.FurnaceState {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), longWaitDeadline)
	defer cancel()
	for {
		message, err := connected.Endpoint.Recv(ctx)
		if err != nil {
			t.Fatalf("等待熔炉状态: %v", err)
		}
		applyIntegrationMessage(t, connected.Mirror, message)
		if state, ok := message.(network.FurnaceState); ok && accept(state) {
			return state
		}
	}
}

// waitIntegrationRejection 等待某个客户端收到指定序号的拒绝。
func waitIntegrationRejection(
	t *testing.T,
	connected integrationClient,
	sequence uint64,
	reason network.RejectReason,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), longWaitDeadline)
	defer cancel()
	for {
		message, err := connected.Endpoint.Recv(ctx)
		if err != nil {
			t.Fatalf("等待拒绝: %v", err)
		}
		applyIntegrationMessage(t, connected.Mirror, message)
		if rejected, ok := message.(network.CommandRejected); ok && rejected.Sequence == sequence {
			if rejected.Reason != reason {
				t.Fatalf("拒绝原因 = %q，想要 %q", rejected.Reason, reason)
			}
			return
		}
	}
}
