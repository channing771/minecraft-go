package server_test

import (
	"context"
	"testing"
	"time"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/server"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/storage"
	"minecraft-go/internal/world"
)

type chestMessages struct {
	states      []network.ChestState
	closes      []network.ContainerClosed
	inventories []network.InventoryState
}

// chestDrainTick 读取一个会话在本 tick 的全部消息并分类箱子状态，与
// furnaceDrainTick 使用同一套约定，只是消息类型换成箱子。
func chestDrainTick(
	t *testing.T,
	endpoint network.ClientEndpoint,
	throughTick uint64,
	ready *bool,
) chestMessages {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var got chestMessages
	for {
		message, err := endpoint.Recv(ctx)
		if err != nil {
			t.Fatalf("接收服务端消息: %v", err)
		}
		switch message := message.(type) {
		case network.ChestState:
			if err := message.Validate(); err != nil {
				t.Fatalf("非法箱子状态: %v", err)
			}
			got.states = append(got.states, message)
		case network.ContainerClosed:
			if err := message.Validate(); err != nil {
				t.Fatalf("非法关闭通知: %v", err)
			}
			got.closes = append(got.closes, message)
		case network.InventoryState:
			got.inventories = append(got.inventories, message)
		case network.PlayerState:
			*ready = message.Ready
			if message.ServerTick >= throughTick {
				return got
			}
		}
	}
}

// newChestWorld 构造一个 Memory 世界并在中心区块放好一个箱子。
func newChestWorld(t *testing.T, slot world.ChestSlot) (
	*server.Server,
	network.ClientEndpoint,
	func() (chestMessages, uint64),
) {
	t.Helper()
	running, clientEndpoint := newDropPublicationWorld(t)
	step := stepUntilChestReady(t, running, clientEndpoint)

	index, ok := world.ChunkBlockIndex(core.BlockPos{})
	if !ok {
		t.Fatal("箱子位置没有区块索引")
	}
	running.SetBlockForTest(core.BlockPos{}, core.ChestID)
	slot.Active = true
	slot.BlockIndex = index
	if slot.Generation == 0 {
		slot.Generation = 1
	}
	running.SetChunkChestForTest(core.ChunkKey{Dimension: core.Overworld}, 0, slot)
	return running, clientEndpoint, step
}

func stepUntilChestReady(
	t *testing.T,
	running *server.Server,
	endpoint network.ClientEndpoint,
) func() (chestMessages, uint64) {
	t.Helper()
	ready := false
	deadline := time.Now().Add(5 * time.Second)
	step := func() (chestMessages, uint64) {
		t.Helper()
		result := running.StepForTest()
		return chestDrainTick(t, endpoint, result.Tick, &ready), result.Tick
	}
	for !ready {
		if time.Now().After(deadline) {
			t.Fatal("等待玩家 Ready 超时")
		}
		step()
	}
	return step
}

func TestOpenChestSendsStateOnlyToViewer(t *testing.T) {
	var slot world.ChestSlot
	slot.Items[0] = core.ItemStack{Item: core.ItemStone, Count: 5}
	running, clientEndpoint, step := newChestWorld(t, slot)

	// 未打开界面时不会收到任何箱子状态。
	if before, _ := step(); len(before.states) != 0 {
		t.Fatalf("未打开界面就收到箱子状态: %+v", before.states)
	}

	sendClientMessage(t, clientEndpoint, network.OpenContainer{
		Sequence: 10, Pitch: lookDownAtFurnace,
	})
	deadline := time.Now().Add(5 * time.Second)
	var opened network.ChestState
	for opened.Chest.Generation == 0 {
		if time.Now().After(deadline) {
			t.Fatal("等待箱子状态超时")
		}
		messages, _ := step()
		if len(messages.states) > 0 {
			opened = messages.states[len(messages.states)-1]
		}
	}
	if opened.Chest.Slot != 0 || opened.Chest.Chunk != (core.ChunkPos{}) ||
		opened.Chest.Kind != core.ContainerKindChest {
		t.Fatalf("箱子引用 = %+v", opened.Chest)
	}
	if opened.Items[0] != slot.Items[0] {
		t.Fatalf("状态首格 = %+v，想要 %+v", opened.Items[0], slot.Items[0])
	}
	_ = running
}

func TestCloseChestStopsServerState(t *testing.T) {
	var slot world.ChestSlot
	slot.Items[0] = core.ItemStack{Item: core.ItemStone, Count: 5}
	_, clientEndpoint, step := newChestWorld(t, slot)
	sendClientMessage(t, clientEndpoint, network.OpenContainer{
		Sequence: 10, Pitch: lookDownAtFurnace,
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("等待箱子状态超时")
		}
		if messages, _ := step(); len(messages.states) > 0 {
			break
		}
	}

	sendClientMessage(t, clientEndpoint, network.CloseContainer{Sequence: 11})
	// 关闭命令要先被接收并在下一个 tick 生效，随后必须彻底停止发送。
	deadline = time.Now().Add(5 * time.Second)
	stopped := false
	for !stopped {
		if time.Now().After(deadline) {
			t.Fatal("关闭后箱子状态一直没有停止")
		}
		messages, _ := step()
		stopped = len(messages.states) == 0
	}
	for range 3 {
		if after, _ := step(); len(after.states) != 0 {
			t.Fatalf("关闭后又开始发送箱子状态: %+v", after.states)
		}
	}
}

func TestChestMoveUpdatesBothSides(t *testing.T) {
	running, clientEndpoint, step := newChestWorld(t, world.ChestSlot{})
	running.SetPlayerInventoryForTest(1, func(inventory core.Inventory) core.Inventory {
		inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemCoal, Count: 3}
		return inventory
	})

	sendClientMessage(t, clientEndpoint, network.OpenContainer{
		Sequence: 10, Pitch: lookDownAtFurnace,
	})
	deadline := time.Now().Add(5 * time.Second)
	var ref core.ContainerRef
	for ref.Generation == 0 {
		if time.Now().After(deadline) {
			t.Fatal("等待箱子状态超时")
		}
		if messages, _ := step(); len(messages.states) > 0 {
			ref = messages.states[len(messages.states)-1].Chest
		}
	}

	sendClientMessage(t, clientEndpoint, network.MoveContainerStack{
		Sequence: 11, Container: ref, From: 0, To: core.ChestFirstSlot,
	})
	deadline = time.Now().Add(5 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("等待物品移入箱子超时")
		}
		step()
		chunk, _, ok := running.CloneReadyChunkForTest(core.ChunkKey{Dimension: core.Overworld})
		if !ok {
			continue
		}
		if got := chunk.Chest(0).Items[0]; got.Item == core.ItemCoal && got.Count == 3 {
			break
		}
	}
	snapshot, ok := running.PlayerSnapshotFor(1)
	if !ok {
		t.Fatal("玩家快照不可用")
	}
	if snapshot.Inventory.Hotbar.Slots[0] != (core.ItemStack{}) {
		t.Fatalf("来源格未清空: %+v", snapshot.Inventory.Hotbar.Slots[0])
	}
}

// newTwoPlayerChestWorld 构造一个附带两名玩家的 Memory 世界，供多查看者/非查看者测试复用。
func newTwoPlayerChestWorld(t *testing.T) (
	*server.Server, network.ClientEndpoint, network.ClientEndpoint,
) {
	t.Helper()
	firstClient, firstServerEndpoint := network.NewMemoryPair(1024)
	secondClient, secondServerEndpoint := network.NewMemoryPair(1024)
	config := hotbarTestConfig(2)
	running := server.NewWorld(config, server.FlatTestGenerator{}, storage.NewMemory(storage.Metadata{
		FormatVersion: 2, Seed: config.Seed,
		SpawnDimension: config.SpawnDimension, SpawnAnchor: config.SpawnAnchor,
	}))
	restore := sim.PlayerRestore{SpawnDimension: config.SpawnDimension, SpawnAnchor: config.SpawnAnchor}
	if _, err := running.AttachSession(externalSessionSpec(1, 1, firstServerEndpoint, restore)); err != nil {
		t.Fatalf("附加第一名玩家: %v", err)
	}
	if _, err := running.AttachSession(externalSessionSpec(2, 1, secondServerEndpoint, restore)); err != nil {
		t.Fatalf("附加第二名玩家: %v", err)
	}
	shutdownHotbarServer(t, running, firstClient, secondClient)
	return running, firstClient, secondClient
}

func stepUntilTwoPlayerChestReady(
	t *testing.T,
	running *server.Server,
	first, second network.ClientEndpoint,
) func() (chestMessages, chestMessages, uint64) {
	t.Helper()
	firstReady, secondReady := false, false
	deadline := time.Now().Add(5 * time.Second)
	step := func() (chestMessages, chestMessages, uint64) {
		t.Helper()
		result := running.StepForTest()
		firstMessages := chestDrainTick(t, first, result.Tick, &firstReady)
		secondMessages := chestDrainTick(t, second, result.Tick, &secondReady)
		return firstMessages, secondMessages, result.Tick
	}
	for !firstReady || !secondReady {
		if time.Now().After(deadline) {
			t.Fatal("等待两名玩家 Ready 超时")
		}
		step()
	}
	return step
}

func TestTwoViewersReceiveIdenticalChestStateAndNonViewerReceivesNone(t *testing.T) {
	running, firstClient, secondClient := newTwoPlayerChestWorld(t)
	step := stepUntilTwoPlayerChestReady(t, running, firstClient, secondClient)

	index, ok := world.ChunkBlockIndex(core.BlockPos{})
	if !ok {
		t.Fatal("箱子位置没有区块索引")
	}
	running.SetBlockForTest(core.BlockPos{}, core.ChestID)
	running.SetChunkChestForTest(core.ChunkKey{Dimension: core.Overworld}, 0, world.ChestSlot{
		Generation: 1, Active: true, BlockIndex: index,
		Items: [core.ChestSlots]core.ItemStack{0: {Item: core.ItemStone, Count: 9}},
	})

	// 只有第一名玩家打开箱子；第二名玩家仍订阅同一区块但从未打开界面。
	sendClientMessage(t, firstClient, network.OpenContainer{
		Sequence: 10, Pitch: lookDownAtFurnace,
	})
	deadline := time.Now().Add(5 * time.Second)
	var firstState network.ChestState
	for firstState.Chest.Generation == 0 {
		if time.Now().After(deadline) {
			t.Fatal("等待第一名玩家箱子状态超时")
		}
		first, second, _ := step()
		if len(second.states) != 0 {
			t.Fatalf("未打开界面的第二名玩家收到箱子状态: %+v", second.states)
		}
		if len(first.states) > 0 {
			firstState = first.states[len(first.states)-1]
		}
	}

	// 第二名玩家随后打开同一个箱子，必须看到与第一名玩家完全一致的完整状态。
	sendClientMessage(t, secondClient, network.OpenContainer{
		Sequence: 10, Pitch: lookDownAtFurnace,
	})
	deadline = time.Now().Add(5 * time.Second)
	var secondState network.ChestState
	for secondState.Chest.Generation == 0 {
		if time.Now().After(deadline) {
			t.Fatal("等待第二名玩家箱子状态超时")
		}
		_, second, _ := step()
		if len(second.states) > 0 {
			secondState = second.states[len(second.states)-1]
		}
	}
	if firstState.Chest != secondState.Chest || firstState.Items != secondState.Items {
		t.Fatalf("两名查看者的箱子状态不一致: %+v vs %+v", firstState, secondState)
	}

	// 第一名玩家把快捷栏物品移入箱子：第二名玩家必须看到同一份箱子更新，
	// 但绝不能收到属于第一名玩家的完整物品状态。
	running.SetPlayerInventoryForTest(1, func(inventory core.Inventory) core.Inventory {
		inventory.Hotbar.Slots[1] = core.ItemStack{Item: core.ItemCoal, Count: 2}
		return inventory
	})
	sendClientMessage(t, firstClient, network.MoveContainerStack{
		Sequence: 11, Container: firstState.Chest, From: 1, To: core.ChestFirstSlot + 1,
	})
	deadline = time.Now().Add(5 * time.Second)
	updated := false
	for !updated {
		if time.Now().After(deadline) {
			t.Fatal("等待第二名玩家看到跨端箱子更新超时")
		}
		first, second, _ := step()
		if len(second.inventories) != 0 {
			t.Fatalf("第二名玩家收到了不属于自己的完整物品状态: %+v", second.inventories)
		}
		for _, state := range second.states {
			if state.Items[1].Item == core.ItemCoal && state.Items[1].Count == 2 {
				updated = true
			}
		}
		_ = first
	}
}
