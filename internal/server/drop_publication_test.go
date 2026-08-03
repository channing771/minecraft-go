package server_test

import (
	"context"
	"math"
	"testing"
	"time"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/server"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/storage"
	"minecraft-go/internal/world"
)

type dropMessages struct {
	upserts []network.ItemDropUpserts
	removes []network.ItemDropRemoves
}

// dropDrainTick 读取一个会话在本 tick 的全部消息并分类掉落物差分。
func dropDrainTick(
	t *testing.T,
	endpoint network.ClientEndpoint,
	throughTick uint64,
	ready *bool,
) dropMessages {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var got dropMessages
	for {
		message, err := endpoint.Recv(ctx)
		if err != nil {
			t.Fatalf("接收服务端消息: %v", err)
		}
		switch message := message.(type) {
		case network.ItemDropUpserts:
			if err := message.Validate(); err != nil {
				t.Fatalf("非法 upsert 批次: %v", err)
			}
			got.upserts = append(got.upserts, message)
		case network.ItemDropRemoves:
			if err := message.Validate(); err != nil {
				t.Fatalf("非法 remove 批次: %v", err)
			}
			got.removes = append(got.removes, message)
		case network.PlayerState:
			*ready = message.Ready
			if message.ServerTick == throughTick {
				return got
			}
			if message.ServerTick > throughTick {
				t.Fatalf("PlayerState tick=%d，跳过目标 tick=%d", message.ServerTick, throughTick)
			}
		}
	}
}

func newDropPublicationWorld(t *testing.T) (*server.Server, network.ClientEndpoint) {
	t.Helper()
	clientEndpoint, serverEndpoint := network.NewMemoryPair(1024)
	config := hotbarTestConfig(1)
	running := newMemoryAttachedWorldForExternalTest(
		config, serverEndpoint, server.FlatTestGenerator{},
	)
	shutdownHotbarServer(t, running, clientEndpoint)
	return running, clientEndpoint
}

// stepUntilDropReady 推进到玩家 Ready，并返回随后可用的 tick 推进函数。
func stepUntilDropReady(
	t *testing.T,
	running *server.Server,
	endpoint network.ClientEndpoint,
) func() (dropMessages, uint64) {
	t.Helper()
	ready := false
	deadline := time.Now().Add(5 * time.Second)
	step := func() (dropMessages, uint64) {
		t.Helper()
		result := running.StepForTest()
		return dropDrainTick(t, endpoint, result.Tick, &ready), result.Tick
	}
	for !ready {
		if time.Now().After(deadline) {
			t.Fatal("等待玩家 Ready 超时")
		}
		step()
	}
	return step
}

func TestDropEnteringInterestSendsCurrentValue(t *testing.T) {
	running, clientEndpoint := newDropPublicationWorld(t)
	step := stepUntilDropReady(t, running, clientEndpoint)

	index, ok := world.ChunkBlockIndex(core.BlockPos{X: 1, Y: 0, Z: 1})
	if !ok {
		t.Fatal("测试方块没有区块索引")
	}
	running.SetChunkDropForTest(core.ChunkKey{Dimension: core.Overworld}, 3, world.DropSlot{
		Generation: 6, Active: true,
		Stack:      core.ItemStack{Item: core.ItemDirt, Count: 9},
		BlockIndex: index, PickupDelayTicks: 200,
	})

	messages, _ := step()
	if len(messages.upserts) != 1 || len(messages.upserts[0].Drops) != 1 {
		t.Fatalf("进入兴趣范围的 upsert = %+v", messages.upserts)
	}
	want := network.ItemDrop{
		ID: core.DropID{
			Dimension: core.Overworld, Chunk: core.ChunkPos{}, Slot: 3, Generation: 6,
		},
		BlockIndex: index, Item: core.ItemDirt, Count: 9,
	}
	if got := messages.upserts[0].Drops[0]; got != want {
		t.Fatalf("upsert = %+v，想要 %+v", got, want)
	}

	// 状态未变化时不重复发送。
	if repeat, _ := step(); len(repeat.upserts) != 0 || len(repeat.removes) != 0 {
		t.Fatalf("未变化仍发送差分: %+v", repeat)
	}
}

func TestDropPickupSendsUpsertThenRemove(t *testing.T) {
	running, clientEndpoint := newDropPublicationWorld(t)
	step := stepUntilDropReady(t, running, clientEndpoint)

	// 放在玩家当前脚下方块，使其落在 1.25 格拾取范围内。
	player, ok := playerStateForExternalTest(running)
	if !ok {
		t.Fatal("玩家状态不可用")
	}
	feet := core.BlockPos{
		X: int32(math.Floor(float64(player.State.Position.X()))),
		Y: int32(math.Floor(float64(player.State.Position.Y()))),
		Z: int32(math.Floor(float64(player.State.Position.Z()))),
	}
	index, ok := world.ChunkBlockIndex(feet)
	if !ok {
		t.Fatal("脚下方块没有区块索引")
	}
	key := core.ChunkKey{Dimension: core.Overworld, Pos: feet.Chunk()}
	id := core.DropID{Dimension: core.Overworld, Chunk: feet.Chunk(), Slot: 0, Generation: 1}
	// 留出一个拾取延迟 tick，使镜像先收到 upsert 再收到 remove。
	running.SetChunkDropForTest(key, 0, world.DropSlot{
		Generation: 1, Active: true,
		Stack:      core.ItemStack{Item: core.ItemGrass, Count: 2},
		BlockIndex: index, PickupDelayTicks: 2,
	})

	if messages, _ := step(); len(messages.upserts) != 1 ||
		messages.upserts[0].Drops[0].ID != id {
		t.Fatalf("拾取前的 upsert = %+v", messages.upserts)
	}

	deadline := time.Now().Add(5 * time.Second)
	removed := false
	for !removed {
		if time.Now().After(deadline) {
			t.Fatal("等待掉落物被拾完超时")
		}
		messages, _ := step()
		for _, batch := range messages.removes {
			for _, got := range batch.IDs {
				if got == id {
					removed = true
				}
			}
		}
	}
}

func TestDropLeavingInterestSendsRemove(t *testing.T) {
	running, clientEndpoint := newDropPublicationWorld(t)
	step := stepUntilDropReady(t, running, clientEndpoint)

	index, ok := world.ChunkBlockIndex(core.BlockPos{X: 1, Y: 0, Z: 1})
	if !ok {
		t.Fatal("测试方块没有区块索引")
	}
	key := core.ChunkKey{Dimension: core.Overworld}
	id := core.DropID{Dimension: core.Overworld, Slot: 4, Generation: 2}
	running.SetChunkDropForTest(key, 4, world.DropSlot{
		Generation: 2, Active: true,
		Stack:      core.ItemStack{Item: core.ItemStone, Count: 1},
		BlockIndex: index, PickupDelayTicks: 200,
	})
	if messages, _ := step(); len(messages.upserts) != 1 {
		t.Fatalf("初始 upsert = %+v", messages.upserts)
	}

	running.SetChunkDropForTest(key, 4, world.DropSlot{Generation: 2})
	messages, _ := step()
	if len(messages.removes) != 1 || len(messages.removes[0].IDs) != 1 ||
		messages.removes[0].IDs[0] != id {
		t.Fatalf("移除差分 = %+v", messages.removes)
	}
}

func TestDropBatchesStayWithinProtocolLimit(t *testing.T) {
	running, clientEndpoint := newDropPublicationWorld(t)
	step := stepUntilDropReady(t, running, clientEndpoint)

	index, ok := world.ChunkBlockIndex(core.BlockPos{X: 1, Y: 0, Z: 1})
	if !ok {
		t.Fatal("测试方块没有区块索引")
	}
	key := core.ChunkKey{Dimension: core.Overworld}
	for slot := range core.DropsPerChunk {
		running.SetChunkDropForTest(key, slot, world.DropSlot{
			Generation: 1, Active: true,
			Stack:      core.ItemStack{Item: core.ItemStone, Count: 1},
			BlockIndex: index, PickupDelayTicks: 200,
		})
	}

	messages, _ := step()
	total := 0
	for _, batch := range messages.upserts {
		if len(batch.Drops) > network.MaxItemDropBatch {
			t.Fatalf("批次 %d 项，超过协议上限", len(batch.Drops))
		}
		total += len(batch.Drops)
	}
	if total != core.DropsPerChunk {
		t.Fatalf("共发送 %d 项，想要 %d", total, core.DropsPerChunk)
	}
}

func TestDropDiffStaysWithSessionOwner(t *testing.T) {
	firstClient, firstServer := network.NewMemoryPair(1024)
	secondClient, secondServer := network.NewMemoryPair(1024)
	running := newMemoryAttachedWorldForExternalTest(
		hotbarTestConfig(2), firstServer, server.FlatTestGenerator{},
	)
	if _, err := running.AttachSession(externalSessionSpec(2, 1, secondServer, sim.PlayerRestore{
		SpawnDimension: core.Overworld,
	})); err != nil {
		t.Fatalf("附加第二个会话: %v", err)
	}
	shutdownHotbarServer(t, running, firstClient, secondClient)

	firstReady, secondReady := false, false
	deadline := time.Now().Add(5 * time.Second)
	for !firstReady || !secondReady {
		if time.Now().After(deadline) {
			t.Fatal("等待两名玩家 Ready 超时")
		}
		result := running.StepForTest()
		dropDrainTick(t, firstClient, result.Tick, &firstReady)
		dropDrainTick(t, secondClient, result.Tick, &secondReady)
	}

	// 远离两名玩家的兴趣半径之外的区块不得进入任何镜像。
	far := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: 32}}
	running.SetChunkDropForTest(far, 0, world.DropSlot{
		Generation: 1, Active: true,
		Stack:            core.ItemStack{Item: core.ItemStone, Count: 1},
		PickupDelayTicks: 200,
	})
	result := running.StepForTest()
	first := dropDrainTick(t, firstClient, result.Tick, &firstReady)
	second := dropDrainTick(t, secondClient, result.Tick, &secondReady)
	if len(first.upserts) != 0 || len(second.upserts) != 0 {
		t.Fatalf("兴趣范围外的掉落物被发布: first=%+v second=%+v", first, second)
	}
}

// TestDropSurvivesShutdownAndRestart 覆盖挖掘产生掉落物、正常刷新关服、
// 从同一世界目录重启后掉落物 ID、物品、数量与方块位置一致。
func TestDropSurvivesShutdownAndRestart(t *testing.T) {
	root := t.TempDir()
	first, firstStore, firstClient := newDropDiskWorld(t, root)
	step := stepUntilDropReady(t, first, firstClient)

	sendClientMessage(t, firstClient, network.BreakBlock{
		Sequence: 1, Yaw: 0, Pitch: -float32(math.Pi)/2 + 0.01,
	})
	var created network.ItemDrop
	deadline := time.Now().Add(5 * time.Second)
	for created.Count == 0 {
		if time.Now().After(deadline) {
			t.Fatal("等待挖掘产生掉落物超时")
		}
		messages, _ := step()
		for _, batch := range messages.upserts {
			if len(batch.Drops) != 0 {
				created = batch.Drops[0]
			}
		}
	}
	flushDropWorld(t, first, firstStore)

	second, secondStore, _ := newDropDiskWorld(t, root)
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

	key := core.ChunkKey{Dimension: created.ID.Dimension, Pos: created.ID.Chunk}
	deadline = time.Now().Add(5 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("等待重启后区块 Ready 超时")
		}
		second.StepForTest()
		chunk, _, ok := second.CloneReadyChunkForTest(key)
		if !ok {
			continue
		}
		got := chunk.Drop(int(created.ID.Slot))
		if !got.Active || got.Generation != created.ID.Generation ||
			got.Stack.Item != created.Item || got.Stack.Count != created.Count ||
			got.BlockIndex != created.BlockIndex {
			t.Fatalf("重启后掉落物 = %+v，想要与 %+v 一致", got, created)
		}
		return
	}
}

func newDropDiskWorld(
	t *testing.T,
	root string,
) (*server.Server, *storage.DiskStore, network.ClientEndpoint) {
	t.Helper()
	store := openPersistentDiskStore(t, root)
	clientEndpoint, serverEndpoint := network.NewMemoryPair(4096)
	t.Cleanup(func() { _ = clientEndpoint.Close() })
	running := newAttachedPersistentWorldForExternalTest(
		hotbarTestConfig(1), serverEndpoint, server.FlatTestGenerator{}, store,
	)
	return running, store, clientEndpoint
}

// flushDropWorld 正常关服并刷写全部待持久区块。
func flushDropWorld(t *testing.T, running *server.Server, store *storage.DiskStore) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := running.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("store Close: %v", err)
	}
}
