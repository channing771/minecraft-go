package server_test

import (
	"context"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/server"
	"github.com/channing771/mornlea/internal/sim"
	"github.com/channing771/mornlea/internal/storage"
	"github.com/channing771/mornlea/internal/world"
)

type dropMessages struct {
	upserts     []network.ItemDropUpserts
	removes     []network.ItemDropRemoves
	inventories []network.InventoryState
	rejected    []network.CommandRejected
}

type dropClientMirrors struct {
	chunks    *client.Mirror
	drops     *client.ItemDrops
	inventory client.InventoryMirror
	player    network.PlayerState
}

func newDropClientMirrors() *dropClientMirrors {
	return &dropClientMirrors{chunks: client.NewMirror(), drops: client.NewItemDrops()}
}

func dropPlayerBlockPos(state network.PlayerState) core.BlockPos {
	return core.BlockPos{
		X: int32(math.Floor(float64(state.Position.X()))),
		Y: int32(math.Floor(float64(state.Position.Y()))),
		Z: int32(math.Floor(float64(state.Position.Z()))),
	}
}

// apply 应用真实服务端消息到客户端只读镜像。
func (mirrors *dropClientMirrors) apply(t *testing.T, message network.ServerMessage) {
	t.Helper()
	switch message := message.(type) {
	case network.ChunkSnapshot, network.BlockChanges, network.ForgetChunks:
		update, err := mirrors.chunks.Apply(message)
		if err != nil {
			t.Fatalf("应用区块镜像消息 %T: %v", message, err)
		}
		if update.Resync != nil {
			t.Fatalf("区块镜像请求重同步: %+v", *update.Resync)
		}
	case network.ItemDropUpserts, network.ItemDropRemoves:
		if err := mirrors.drops.Apply(message); err != nil {
			t.Fatalf("应用掉落物镜像消息 %T: %v", message, err)
		}
	case network.InventoryState:
		if err := mirrors.inventory.Apply(message); err != nil {
			t.Fatalf("应用背包镜像: %v", err)
		}
	case network.PlayerState:
		mirrors.player = message
	}
}

// dropDrainTick 读取一个会话在本 tick 的全部消息并分类掉落物差分。
func dropDrainTick(
	t *testing.T,
	endpoint network.ClientEndpoint,
	throughTick uint64,
	ready *bool,
	mirrors ...*dropClientMirrors,
) dropMessages {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
	defer cancel()
	var got dropMessages
	for {
		message, err := endpoint.Recv(ctx)
		if err != nil {
			t.Fatalf("接收服务端消息: %v", err)
		}
		if len(mirrors) != 0 {
			mirrors[0].apply(t, message)
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
		case network.InventoryState:
			got.inventories = append(got.inventories, message)
		case network.CommandRejected:
			got.rejected = append(got.rejected, message)
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
	deadline := time.Now().Add(waitDeadline)
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

	deadline := time.Now().Add(waitDeadline)
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
	deadline := time.Now().Add(waitDeadline)
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

	sendClientMessage(t, firstClient, network.PlayerInput{
		Sequence: 1, Yaw: 0, Pitch: -float32(math.Pi)/2 + 0.01, Mining: true,
	})
	var created network.ItemDrop
	deadline := time.Now().Add(waitDeadline)
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
		ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
		defer cancel()
		if err := second.Shutdown(ctx); err != nil {
			t.Errorf("second Shutdown: %v", err)
		}
		if err := secondStore.Close(); err != nil {
			t.Errorf("second store Close: %v", err)
		}
	}()

	key := core.ChunkKey{Dimension: created.ID.Dimension, Pos: created.ID.Chunk}
	deadline = time.Now().Add(waitDeadline)
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
	ctx, cancel := context.WithTimeout(context.Background(), longWaitDeadline)
	defer cancel()
	if err := running.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("store Close: %v", err)
	}
}

// newDropWorldWithHotbar 与 newDropPublicationWorld 相同，但玩家快捷栏预置了物品。
func newDropWorldWithHotbar(
	t *testing.T,
	item core.ItemID,
) (*server.Server, network.ClientEndpoint) {
	t.Helper()
	clientEndpoint, serverEndpoint := network.NewMemoryPair(1024)
	config := hotbarTestConfig(1)
	running := newMemoryAttachedWorldWithHotbar(
		config, serverEndpoint, server.FlatTestGenerator{}, stockedTestHotbar(item),
	)
	shutdownHotbarServer(t, running, clientEndpoint)
	return running, clientEndpoint
}

func TestDropSelectedItemPublishesInventoryAndDrop(t *testing.T) {
	running, clientEndpoint := newDropWorldWithHotbar(t, core.ItemCoal)
	step := stepUntilDropReady(t, running, clientEndpoint)

	sendClientMessage(t, clientEndpoint, network.DropSelectedItem{Sequence: 1})
	var messages dropMessages
	deadline := time.Now().Add(waitDeadline)
	for len(messages.upserts) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("主动丢弃没有产生掉落物 upsert")
		}
		messages, _ = step()
	}

	if len(messages.rejected) != 0 {
		t.Fatalf("成功路径产生了拒绝：%+v", messages.rejected)
	}
	if len(messages.upserts) != 1 || len(messages.upserts[0].Drops) != 1 {
		t.Fatalf("upsert = %+v，想要恰好一条", messages.upserts)
	}
	drop := messages.upserts[0].Drops[0]
	// 已注册的非放置物品同样可以经线上掉落物传播。
	if drop.Item != core.ItemCoal || drop.Count != 1 {
		t.Fatalf("掉落物 = %+v，想要一个煤炭", drop)
	}
	if len(messages.inventories) == 0 {
		t.Fatal("所属玩家没有收到完整背包状态")
	}
	final := messages.inventories[len(messages.inventories)-1]
	if got := final.Inventory.Hotbar.Slots[0]; got.Count != core.MaxStackCount-1 {
		t.Fatalf("发布的快捷栏 = %+v，想要少一个煤炭", got)
	}
}

func TestDropSelectedItemTwoMemorySessionsConverge(t *testing.T) {
	firstClient, firstServer := network.NewMemoryPair(1024)
	secondClient, secondServer := network.NewMemoryPair(1024)
	running := newMemoryAttachedWorldWithHotbar(
		hotbarTestConfig(2), firstServer, server.FlatTestGenerator{}, stockedTestHotbar(core.ItemCoal),
	)
	if _, err := running.AttachSession(externalSessionSpec(2, 1, secondServer, sim.PlayerRestore{
		SpawnDimension: core.Overworld,
	})); err != nil {
		t.Fatalf("附加第二个会话: %v", err)
	}
	shutdownHotbarServer(t, running, firstClient, secondClient)
	firstMirrors, secondMirrors := newDropClientMirrors(), newDropClientMirrors()
	firstReady, secondReady := false, false
	deadline := time.Now().Add(waitDeadline)
	for !firstReady || !secondReady {
		if time.Now().After(deadline) {
			t.Fatal("等待两名 Memory 玩家 Ready 超时")
		}
		result := running.StepForTest()
		dropDrainTick(t, firstClient, result.Tick, &firstReady, firstMirrors)
		dropDrainTick(t, secondClient, result.Tick, &secondReady, secondMirrors)
	}
	beforeFirst, ok := firstMirrors.inventory.State()
	if !ok {
		t.Fatal("发起者缺少初始完整背包")
	}
	beforeSecond, ok := secondMirrors.inventory.State()
	if !ok {
		t.Fatal("第二会话缺少初始完整背包")
	}
	wantFirst := beforeFirst
	wantFirst.Hotbar.Slots[0].Count--

	sendClientMessage(t, firstClient, network.DropSelectedItem{Sequence: 1})
	firstInventories, secondInventories := 0, 0
	deadline = time.Now().Add(waitDeadline)
	for {
		if time.Now().After(deadline) {
			t.Fatal("等待双会话 Memory 主动丢弃收敛超时")
		}
		result := running.StepForTest()
		first := dropDrainTick(t, firstClient, result.Tick, &firstReady, firstMirrors)
		second := dropDrainTick(t, secondClient, result.Tick, &secondReady, secondMirrors)
		firstInventories += len(first.inventories)
		secondInventories += len(second.inventories)
		if len(first.rejected) != 0 || len(second.rejected) != 0 {
			t.Fatalf("成功路径产生拒绝: first=%+v second=%+v", first.rejected, second.rejected)
		}
		firstDrops := firstMirrors.drops.Presentations()
		secondDrops := secondMirrors.drops.Presentations()
		gotFirst, firstOK := firstMirrors.inventory.State()
		if firstOK && gotFirst == wantFirst && len(firstDrops) == 1 && len(secondDrops) == 1 {
			break
		}
	}

	if firstInventories != 1 || secondInventories != 0 {
		t.Fatalf("成功后的完整背包发布数 = %d/%d，想要 1/0", firstInventories, secondInventories)
	}
	if got, _ := secondMirrors.inventory.State(); got != beforeSecond {
		t.Fatalf("第二会话背包镜像改变: got=%+v want=%+v", got, beforeSecond)
	}
	firstDrop := firstMirrors.drops.Presentations()[0]
	secondDrop := secondMirrors.drops.Presentations()[0]
	if firstDrop != secondDrop || firstDrop.Item != core.ItemCoal || firstDrop.Count != 1 {
		t.Fatalf("双方掉落物镜像不一致: first=%+v second=%+v", firstDrop, secondDrop)
	}
	position := dropPlayerBlockPos(firstMirrors.player)
	wantIndex, ok := world.ChunkBlockIndex(position)
	if !ok || firstDrop.BlockIndex != wantIndex {
		t.Fatalf("掉落物 block index=%d，想要脚底 %+v 的 %d", firstDrop.BlockIndex, position, wantIndex)
	}
	_, firstRevision, firstOK := firstMirrors.chunks.Hash(firstDrop.ID.Dimension, firstDrop.ID.Chunk)
	_, secondRevision, secondOK := secondMirrors.chunks.Hash(secondDrop.ID.Dimension, secondDrop.ID.Chunk)
	_, authorityRevision, authorityOK := running.ChunkHash(firstDrop.ID.Dimension, firstDrop.ID.Chunk)
	if !firstOK || !secondOK || !authorityOK || firstRevision != secondRevision || firstRevision != authorityRevision {
		t.Fatalf("承载区块 revision = first:%d/%t second:%d/%t authority:%d/%t",
			firstRevision, firstOK, secondRevision, secondOK, authorityRevision, authorityOK)
	}
}

func TestDropSelectedItemCapacityFailureIsolatedBetweenMemorySessions(t *testing.T) {
	firstClient, firstServer := network.NewMemoryPair(1024)
	secondClient, secondServer := network.NewMemoryPair(1024)
	running := newMemoryAttachedWorldWithHotbar(
		hotbarTestConfig(2), firstServer, server.FlatTestGenerator{}, stockedTestHotbar(core.ItemCoal),
	)
	if _, err := running.AttachSession(externalSessionSpec(2, 1, secondServer, sim.PlayerRestore{
		SpawnDimension: core.Overworld,
	})); err != nil {
		t.Fatalf("附加第二个会话: %v", err)
	}
	shutdownHotbarServer(t, running, firstClient, secondClient)
	firstMirrors, secondMirrors := newDropClientMirrors(), newDropClientMirrors()
	firstReady, secondReady := false, false
	deadline := time.Now().Add(waitDeadline)
	for !firstReady || !secondReady {
		if time.Now().After(deadline) {
			t.Fatal("等待两名 Memory 玩家 Ready 超时")
		}
		result := running.StepForTest()
		dropDrainTick(t, firstClient, result.Tick, &firstReady, firstMirrors)
		dropDrainTick(t, secondClient, result.Tick, &secondReady, secondMirrors)
	}
	position := dropPlayerBlockPos(firstMirrors.player)
	key := core.ChunkKey{Dimension: firstMirrors.player.Dimension, Pos: position.Chunk()}
	for slot := range core.DropsPerChunk {
		running.SetChunkDropForTest(key, slot, world.DropSlot{
			Generation: uint32(slot + 1), Active: true,
			Stack:      core.ItemStack{Item: core.ItemDirt, Count: core.MaxStackCount},
			BlockIndex: uint32(slot), PickupDelayTicks: 200,
		})
	}
	result := running.StepForTest()
	dropDrainTick(t, firstClient, result.Tick, &firstReady, firstMirrors)
	dropDrainTick(t, secondClient, result.Tick, &secondReady, secondMirrors)
	if len(firstMirrors.drops.Presentations()) != core.DropsPerChunk ||
		len(secondMirrors.drops.Presentations()) != core.DropsPerChunk {
		t.Fatalf("容量场景未进入双方镜像: first=%d second=%d",
			len(firstMirrors.drops.Presentations()), len(secondMirrors.drops.Presentations()))
	}
	beforeFirst, _ := firstMirrors.inventory.State()
	beforeSecond, _ := secondMirrors.inventory.State()
	beforeFirstDrops := append([]client.ItemDropPresentation(nil), firstMirrors.drops.Presentations()...)
	beforeSecondDrops := append([]client.ItemDropPresentation(nil), secondMirrors.drops.Presentations()...)

	sendClientMessage(t, firstClient, network.DropSelectedItem{Sequence: 7})
	var firstAfter, secondAfter dropMessages
	deadline = time.Now().Add(waitDeadline)
	for len(firstAfter.rejected) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("等待双会话 Memory 容量拒绝超时")
		}
		result = running.StepForTest()
		first := dropDrainTick(t, firstClient, result.Tick, &firstReady, firstMirrors)
		second := dropDrainTick(t, secondClient, result.Tick, &secondReady, secondMirrors)
		firstAfter.upserts = append(firstAfter.upserts, first.upserts...)
		firstAfter.removes = append(firstAfter.removes, first.removes...)
		firstAfter.inventories = append(firstAfter.inventories, first.inventories...)
		firstAfter.rejected = append(firstAfter.rejected, first.rejected...)
		secondAfter.upserts = append(secondAfter.upserts, second.upserts...)
		secondAfter.removes = append(secondAfter.removes, second.removes...)
		secondAfter.inventories = append(secondAfter.inventories, second.inventories...)
		secondAfter.rejected = append(secondAfter.rejected, second.rejected...)
	}
	if len(firstAfter.rejected) != 1 || firstAfter.rejected[0] != (network.CommandRejected{
		Sequence: 7, Reason: network.RejectDropCapacity,
	}) {
		t.Fatalf("发起者拒绝 = %+v", firstAfter.rejected)
	}
	if len(secondAfter.rejected) != 0 || len(secondAfter.inventories) != 0 ||
		len(secondAfter.upserts) != 0 || len(secondAfter.removes) != 0 {
		t.Fatalf("容量失败影响第二会话: %+v", secondAfter)
	}
	if len(firstAfter.inventories) != 0 || len(firstAfter.upserts) != 0 || len(firstAfter.removes) != 0 {
		t.Fatalf("容量失败仍向发起者发布副作用: %+v", firstAfter)
	}
	if got, _ := firstMirrors.inventory.State(); got != beforeFirst {
		t.Fatalf("发起者背包镜像改变: got=%+v want=%+v", got, beforeFirst)
	}
	if got, _ := secondMirrors.inventory.State(); got != beforeSecond {
		t.Fatalf("第二会话背包镜像改变: got=%+v want=%+v", got, beforeSecond)
	}
	if got := firstMirrors.drops.Presentations(); !slices.Equal(got, beforeFirstDrops) {
		t.Fatalf("发起者掉落物镜像改变: got=%+v want=%+v", got, beforeFirstDrops)
	}
	if got := secondMirrors.drops.Presentations(); !slices.Equal(got, beforeSecondDrops) {
		t.Fatalf("第二会话掉落物镜像改变: got=%+v want=%+v", got, beforeSecondDrops)
	}
	firstSnapshot, firstOK := running.PlayerSnapshotFor(1)
	secondSnapshot, secondOK := running.PlayerSnapshotFor(2)
	if !firstOK || firstSnapshot.Inventory != beforeFirst || !secondOK || secondSnapshot.Inventory != beforeSecond {
		t.Fatalf("容量失败改变权威背包: first=%+v/%t second=%+v/%t",
			firstSnapshot.Inventory, firstOK, secondSnapshot.Inventory, secondOK)
	}

	// 两个会话在隔离拒绝后都必须继续处理合法命令。
	sendClientMessage(t, firstClient, network.PlayerInput{Sequence: 8})
	sendClientMessage(t, secondClient, network.PlayerInput{Sequence: 1})
	deadline = time.Now().Add(waitDeadline)
	for firstMirrors.player.LastInputSequence < 8 || secondMirrors.player.LastInputSequence < 1 {
		if time.Now().After(deadline) {
			t.Fatalf("容量拒绝后会话未继续推进: first=%d second=%d",
				firstMirrors.player.LastInputSequence, secondMirrors.player.LastInputSequence)
		}
		result = running.StepForTest()
		dropDrainTick(t, firstClient, result.Tick, &firstReady, firstMirrors)
		dropDrainTick(t, secondClient, result.Tick, &secondReady, secondMirrors)
	}
}

func TestDropSelectedItemCapacityFailureOnlyRejectsRequester(t *testing.T) {
	running, clientEndpoint := newDropWorldWithHotbar(t, core.ItemCoal)
	step := stepUntilDropReady(t, running, clientEndpoint)

	// 用满堆且物品不匹配的槽占满 32 个容量。
	key := core.ChunkKey{Dimension: core.Overworld}
	for slot := range core.DropsPerChunk {
		running.SetChunkDropForTest(key, slot, world.DropSlot{
			Generation: 1, Active: true,
			Stack:      core.ItemStack{Item: core.ItemDirt, Count: core.MaxStackCount},
			BlockIndex: uint32(slot), PickupDelayTicks: 200,
		})
	}
	// 先把占位掉落物的进入兴趣 upsert 消化掉。
	step()

	sendClientMessage(t, clientEndpoint, network.DropSelectedItem{Sequence: 7})
	var messages dropMessages
	deadline := time.Now().Add(waitDeadline)
	for len(messages.rejected) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("容量已满没有返回拒绝")
		}
		messages, _ = step()
	}

	if got := messages.rejected[0]; got.Sequence != 7 ||
		got.Reason != network.RejectDropCapacity {
		t.Fatalf("拒绝 = %+v，想要 sequence 7 的 drop_capacity", got)
	}
	if len(messages.upserts) != 0 || len(messages.removes) != 0 {
		t.Fatalf("拒绝仍发布了掉落物差分：%+v %+v", messages.upserts, messages.removes)
	}
	if len(messages.inventories) != 0 {
		t.Fatalf("拒绝仍发布了背包更新：%+v", messages.inventories)
	}

	// 拒绝之后会话仍然健康：后续合法命令继续推进。
	sendClientMessage(t, clientEndpoint, network.SelectHotbar{Sequence: 8, Slot: 3})
	if _, tick := step(); tick == 0 {
		t.Fatal("拒绝后会话不再推进")
	}
}

// TestDroppedItemSurvivesShutdownAndRestart 证明主动丢弃产生的掉落物跨正常关服持久：
// 重开后保留 ID、物品、方块索引，且年龄与剩余拾取延迟是从持久值继续推进而非重置。
//
// 玩家背包一侧只在关服前断言：本夹具只装配 Server，不含 host 层的玩家保存调度，
// 重新 Attach 时总是用固定 restore 覆盖背包。背包跨重启由带 host 的真实 TCP
// 纵向测试覆盖，见 TestTCPDropSelectedItemSurvivesRestart。
func TestDroppedItemSurvivesShutdownAndRestart(t *testing.T) {
	root := t.TempDir()
	first, firstStore, firstClient := newDropDiskWorld(t, root)
	step := stepUntilDropReady(t, first, firstClient)

	before, ok := first.PlayerSnapshotFor(1)
	if !ok {
		t.Fatal("玩家未 Active")
	}
	beforeCount := before.Inventory.Hotbar.Slots[0].Count
	if beforeCount == 0 {
		t.Fatal("测试夹具的选中栏位为空")
	}

	sendClientMessage(t, firstClient, network.DropSelectedItem{Sequence: 1})
	var created network.ItemDrop
	deadline := time.Now().Add(waitDeadline)
	for created.Count == 0 {
		if time.Now().After(deadline) {
			t.Fatal("等待主动丢弃产生掉落物超时")
		}
		messages, _ := step()
		for _, batch := range messages.upserts {
			if len(batch.Drops) != 0 {
				created = batch.Drops[0]
			}
		}
	}

	// 多推进几个 tick 让掉落物累积年龄并消耗部分拾取延迟。
	for range 5 {
		step()
	}
	afterDrop, ok := first.PlayerSnapshotFor(1)
	if !ok {
		t.Fatal("丢弃后玩家不可用")
	}
	if got := afterDrop.Inventory.Hotbar.Slots[0].Count; got != beforeCount-1 {
		t.Fatalf("丢弃后快捷栏 = %d，想要 %d", got, beforeCount-1)
	}
	key := core.ChunkKey{Dimension: created.ID.Dimension, Pos: created.ID.Chunk}
	live, _, ok := first.CloneReadyChunkForTest(key)
	if !ok {
		t.Fatal("关服前区块不可用")
	}
	wantSlot := live.Drop(int(created.ID.Slot))

	flushDropWorld(t, first, firstStore)

	second, secondStore, _ := newDropDiskWorld(t, root)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
		defer cancel()
		if err := second.Shutdown(ctx); err != nil {
			t.Errorf("second Shutdown: %v", err)
		}
		if err := secondStore.Close(); err != nil {
			t.Errorf("second store Close: %v", err)
		}
	}()

	deadline = time.Now().Add(waitDeadline)
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
		// 不可变字段必须逐字节保留。
		if !got.Active || got.Generation != wantSlot.Generation ||
			got.Stack != wantSlot.Stack || got.BlockIndex != wantSlot.BlockIndex {
			t.Fatalf("重启后掉落物 = %+v，想要与 %+v 一致", got, wantSlot)
		}
		// 年龄与剩余延迟是从持久值继续推进的，不得被重置：
		// 年龄只增不减，剩余延迟只减不增且仍未归零。
		if got.AgeTicks < wantSlot.AgeTicks {
			t.Fatalf("重启后年龄 = %d，早于关服前的 %d", got.AgeTicks, wantSlot.AgeTicks)
		}
		if got.PickupDelayTicks > wantSlot.PickupDelayTicks {
			t.Fatalf("重启后剩余延迟 = %d，多于关服前的 %d",
				got.PickupDelayTicks, wantSlot.PickupDelayTicks)
		}
		if got.PickupDelayTicks == 0 {
			t.Fatal("重启后剩余延迟已归零，无法证明它是从持久值继续")
		}
		return
	}
}
