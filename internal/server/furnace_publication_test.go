package server_test

import (
	"context"
	"math"
	"testing"
	"time"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/server"
	"minecraft-go/internal/world"
)

type furnaceMessages struct {
	states []network.FurnaceState
	closes []network.ContainerClosed
}

// furnaceDrainTick 读取一个会话在本 tick 的全部消息并分类熔炉状态。
func furnaceDrainTick(
	t *testing.T,
	endpoint network.ClientEndpoint,
	throughTick uint64,
	ready *bool,
) furnaceMessages {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var got furnaceMessages
	for {
		message, err := endpoint.Recv(ctx)
		if err != nil {
			t.Fatalf("接收服务端消息: %v", err)
		}
		switch message := message.(type) {
		case network.FurnaceState:
			if err := message.Validate(); err != nil {
				t.Fatalf("非法熔炉状态: %v", err)
			}
			got.states = append(got.states, message)
		case network.ContainerClosed:
			if err := message.Validate(); err != nil {
				t.Fatalf("非法关闭通知: %v", err)
			}
			got.closes = append(got.closes, message)
		case network.PlayerState:
			*ready = message.Ready
			if message.ServerTick >= throughTick {
				return got
			}
		}
	}
}

// newFurnaceWorld 构造一个 Memory 世界并在中心区块放好一个熔炉。
func newFurnaceWorld(t *testing.T, slot world.FurnaceSlot) (
	*server.Server,
	network.ClientEndpoint,
	func() (furnaceMessages, uint64),
) {
	t.Helper()
	running, clientEndpoint := newDropPublicationWorld(t)
	step := stepUntilFurnaceReady(t, running, clientEndpoint)

	index, ok := world.ChunkBlockIndex(core.BlockPos{})
	if !ok {
		t.Fatal("熔炉位置没有区块索引")
	}
	running.SetBlockForTest(core.BlockPos{}, core.FurnaceID)
	slot.Active = true
	slot.BlockIndex = index
	if slot.Generation == 0 {
		slot.Generation = 1
	}
	running.SetChunkFurnaceForTest(core.ChunkKey{Dimension: core.Overworld}, 0, slot)
	return running, clientEndpoint, step
}

func stepUntilFurnaceReady(
	t *testing.T,
	running *server.Server,
	endpoint network.ClientEndpoint,
) func() (furnaceMessages, uint64) {
	t.Helper()
	ready := false
	deadline := time.Now().Add(5 * time.Second)
	step := func() (furnaceMessages, uint64) {
		t.Helper()
		result := running.StepForTest()
		return furnaceDrainTick(t, endpoint, result.Tick, &ready), result.Tick
	}
	for !ready {
		if time.Now().After(deadline) {
			t.Fatal("等待玩家 Ready 超时")
		}
		step()
	}
	return step
}

// lookDownAtFurnace 是俯视脚下熔炉的视角。
const lookDownAtFurnace = -float32(math.Pi)/2 + 0.01

func TestOpenFurnaceSendsStateOnlyToViewer(t *testing.T) {
	running, clientEndpoint, step := newFurnaceWorld(t, world.FurnaceSlot{
		Input: core.ItemStack{Item: core.ItemRawIron, Count: 4},
		Fuel:  core.ItemStack{Item: core.ItemCoal, Count: 1},
	})

	// 未打开界面时不会收到任何熔炉状态。
	if before, _ := step(); len(before.states) != 0 {
		t.Fatalf("未打开界面就收到熔炉状态: %+v", before.states)
	}

	sendClientMessage(t, clientEndpoint, network.OpenContainer{
		Sequence: 10, Pitch: lookDownAtFurnace,
	})
	deadline := time.Now().Add(5 * time.Second)
	var opened network.FurnaceState
	for opened.Furnace.Generation == 0 {
		if time.Now().After(deadline) {
			t.Fatal("等待熔炉状态超时")
		}
		messages, _ := step()
		if len(messages.states) > 0 {
			opened = messages.states[len(messages.states)-1]
		}
	}
	if opened.Furnace.Slot != 0 || opened.Furnace.Chunk != (core.ChunkPos{}) {
		t.Fatalf("熔炉引用 = %+v", opened.Furnace)
	}
	if opened.Input.Item != core.ItemRawIron {
		t.Fatalf("状态输入 = %+v", opened.Input)
	}
	_ = running
}

func TestCloseFurnaceStopsServerState(t *testing.T) {
	_, clientEndpoint, step := newFurnaceWorld(t, world.FurnaceSlot{
		Input: core.ItemStack{Item: core.ItemRawIron, Count: 4},
		Fuel:  core.ItemStack{Item: core.ItemCoal, Count: 1},
	})
	sendClientMessage(t, clientEndpoint, network.OpenContainer{
		Sequence: 10, Pitch: lookDownAtFurnace,
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("等待熔炉状态超时")
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
			t.Fatal("关闭后熔炉状态一直没有停止")
		}
		messages, _ := step()
		stopped = len(messages.states) == 0
	}
	for range 3 {
		if after, _ := step(); len(after.states) != 0 {
			t.Fatalf("关闭后又开始发送熔炉状态: %+v", after.states)
		}
	}
}

func TestFurnaceMoveUpdatesBothSides(t *testing.T) {
	running, clientEndpoint, step := newFurnaceWorld(t, world.FurnaceSlot{})
	running.SetPlayerInventoryForTest(1, func(inventory core.Inventory) core.Inventory {
		inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemCoal, Count: 3}
		return inventory
	})

	sendClientMessage(t, clientEndpoint, network.OpenContainer{
		Sequence: 10, Pitch: lookDownAtFurnace,
	})
	deadline := time.Now().Add(5 * time.Second)
	var ref core.FurnaceRef
	for ref.Generation == 0 {
		if time.Now().After(deadline) {
			t.Fatal("等待熔炉状态超时")
		}
		if messages, _ := step(); len(messages.states) > 0 {
			ref = messages.states[len(messages.states)-1].Furnace
		}
	}

	sendClientMessage(t, clientEndpoint, network.MoveContainerStack{
		Sequence: 11, Container: ref, From: 0, To: core.FurnaceFuelSlot,
	})
	deadline = time.Now().Add(5 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("等待燃料移入超时")
		}
		step()
		chunk, _, ok := running.CloneReadyChunkForTest(core.ChunkKey{Dimension: core.Overworld})
		if !ok {
			continue
		}
		if got := chunk.Furnace(0).Fuel; got.Item == core.ItemCoal && got.Count == 3 {
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

// TestFurnaceRestartRestoresTimersWithoutCatchUp 覆盖 schema v4 的重启闭环：
// 三格物品、熔炼进度与剩余燃烧 tick 原值恢复，且停服墙钟不补算进度。
func TestFurnaceRestartRestoresTimersWithoutCatchUp(t *testing.T) {
	root := t.TempDir()
	key := core.ChunkKey{Dimension: core.Overworld}
	index, ok := world.ChunkBlockIndex(core.BlockPos{})
	if !ok {
		t.Fatal("熔炉位置没有区块索引")
	}
	want := world.FurnaceSlot{
		Generation: 3, Active: true, BlockIndex: index,
		Input:         core.ItemStack{Item: core.ItemRawIron, Count: 4},
		Fuel:          core.ItemStack{Item: core.ItemCoal, Count: 2},
		Output:        core.ItemStack{Item: core.ItemIronIngot, Count: 1},
		ProgressTicks: 137, BurnTicks: 1463,
	}

	first, firstStore, firstClient := newDropDiskWorld(t, root)
	step := stepUntilDropReady(t, first, firstClient)
	first.SetBlockForTest(core.BlockPos{}, core.FurnaceID)
	first.SetChunkFurnaceForTest(key, 0, want)
	step()
	flushDropWorld(t, first, firstStore)

	// 停服期间的真实时间不得产生任何进度。
	time.Sleep(20 * time.Millisecond)

	second, secondStore, secondClient := newDropDiskWorld(t, root)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := second.Shutdown(ctx); err != nil {
			t.Errorf("second Shutdown: %v", err)
		}
		if err := secondStore.Close(); err != nil {
			t.Errorf("second store Close: %v", err)
		}
	}()
	restart := stepUntilDropReady(t, second, secondClient)

	deadline := time.Now().Add(5 * time.Second)
	var restored world.FurnaceSlot
	for !restored.Active {
		if time.Now().After(deadline) {
			t.Fatal("等待重启后加载熔炉区块超时")
		}
		restart()
		chunk, _, ok := second.CloneReadyChunkForTest(key)
		if !ok {
			continue
		}
		restored = chunk.Furnace(0)
	}
	if restored.Input != want.Input || restored.Fuel != want.Fuel {
		t.Fatalf("重启后三格 = %+v，想要 %+v", restored, want)
	}
	// 恢复后每推进一个 tick，进度加一且燃烧减一；两者的增量必须严格相等，
	// 因此计时是从存档值继续，而不是被重置或按停服墙钟补算。
	if restored.ProgressTicks < want.ProgressTicks {
		t.Fatalf("重启后进度 = %d，低于存档值 %d", restored.ProgressTicks, want.ProgressTicks)
	}
	advanced := int(restored.ProgressTicks) - int(want.ProgressTicks)
	burned := int(want.BurnTicks) - int(restored.BurnTicks)
	if advanced != burned {
		t.Fatalf("进度增量 %d 与燃烧减量 %d 不一致", advanced, burned)
	}
	// 停服 20ms 约合数百个 tick；若按墙钟补算，增量会远大于实际推进的 tick 数。
	if advanced > 16 {
		t.Fatalf("重启后进度增加 %d，疑似按墙钟补算", advanced)
	}
	if restored.Generation != want.Generation {
		t.Fatalf("重启后 generation = %d，想要 %d", restored.Generation, want.Generation)
	}
}
