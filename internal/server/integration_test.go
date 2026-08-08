package server_test

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"minecraft-go/internal/client"
	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/server"
)

func TestAuthoritativeInteractionRoundTrip(t *testing.T) {
	clientEndpoint, serverEndpoint := network.NewMemoryPair(256)
	config := server.DefaultConfig(42)
	config.ViewRadius = 1
	config.Workers = 1
	config.SnapshotChunks = 16
	config.SnapshotBytes = 1 << 20
	config.OutboxCapacity = 256
	running := newMemoryAttachedWorldWithHotbar(
		config, serverEndpoint, server.FlatTestGenerator{}, stockedTestHotbar(core.ItemStone),
	)
	mirror := client.NewMirror()

	interactionChunk := (core.BlockPos{X: 0, Y: 1, Z: -6}).Chunk()
	stepUntil(t, running, clientEndpoint, mirror, func() bool {
		chunk, chunkOK := mirror.Chunk(core.Overworld, interactionChunk)
		player, playerOK := playerStateForExternalTest(running)
		return chunkOK && chunk.Revision == 1 && playerOK && player.Ready
	})

	pitch := float32(-0.2)
	// M4B：挖掘只产生地面掉落物，放置改用登录时已确认的快捷栏物品。
	sendClientMessage(t, clientEndpoint, network.PlayerInput{
		Sequence: 1,
		Yaw:      0,
		Pitch:    pitch,
		Mining:   true,
	})
	broken := awaitInteractionChange(
		t, running, clientEndpoint, mirror, interactionChunk, 1, 2,
	)
	if broken.Block != core.AirID {
		t.Fatalf("挖掘结果 = %+v，想要空气", broken)
	}

	// 障碍消失后需要压低视角才能命中六格内的地面。
	sendClientMessage(t, clientEndpoint, network.PlaceBlock{
		Sequence: 2,
		Yaw:      0,
		Pitch:    -0.6,
		Slot:     0,
	})
	placed := awaitInteractionChange(
		t, running, clientEndpoint, mirror, interactionChunk, 2, 3,
	)
	if placed.Position == broken.Position || placed.Block != core.StoneID {
		t.Fatalf("放置结果 = %+v，想要放下快捷栏中的石头", placed)
	}
	authoritativeHash, authoritativeRevision, authoritativeOK := running.ChunkHash(
		core.Overworld,
		interactionChunk,
	)
	mirrorHash, mirrorRevision, mirrorOK := mirror.Hash(core.Overworld, interactionChunk)
	if !authoritativeOK || !mirrorOK ||
		authoritativeRevision != mirrorRevision ||
		authoritativeHash != mirrorHash {
		t.Fatalf(
			"交互后一致性失败: authoritative=(%x,%d,%v) mirror=(%x,%d,%v)",
			authoritativeHash,
			authoritativeRevision,
			authoritativeOK,
			mirrorHash,
			mirrorRevision,
			mirrorOK,
		)
	}

	serverContext, cancelServer := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	go func() { serverDone <- running.Run(serverContext) }()
	cancelServer()
	select {
	case err := <-serverDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Server.Run 退出错误 = %v", err)
		}
	case <-time.After(waitDeadline):
		t.Fatal("取消后 Server.Run 未在 1 秒内退出")
	}
	if err := clientEndpoint.Close(); err != nil {
		t.Fatalf("关闭客户端端点: %v", err)
	}
}

func TestAuthoritativeMiningMemoryLifecycle(t *testing.T) {
	clientEndpoint, serverEndpoint := network.NewMemoryPair(256)
	config := server.DefaultConfig(42)
	config.ViewRadius = 1
	config.Workers = 1
	config.SnapshotChunks = 16
	config.SnapshotBytes = 1 << 20
	config.OutboxCapacity = 256
	stoneFull, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	ironFull, _ := core.ItemMaxDurability(core.ItemIronPickaxe)
	var hotbar core.Hotbar
	hotbar.Slots[0] = core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: stoneFull}
	hotbar.Slots[1] = core.ItemStack{Item: core.ItemIronPickaxe, Count: 1, Durability: ironFull}
	hotbar.Slots[2] = core.ItemStack{Item: core.ItemDirt, Count: 1}
	running := newMemoryAttachedWorldWithHotbar(config, serverEndpoint, server.FlatTestGenerator{}, hotbar)
	t.Cleanup(func() { shutdownExternalServerForTest(t, running) })
	mirror := client.NewMirror()
	target := core.BlockPos{X: 0, Y: 1, Z: -6}
	ready, inventoryConfirmed := false, false
	stepUntilCollect(t, running, clientEndpoint, mirror, func(message network.ServerMessage) {
		switch message := message.(type) {
		case network.PlayerState:
			assertValidMiningPlayerState(t, message)
			ready = ready || message.Ready
		case network.InventoryState:
			inventoryConfirmed = message.Inventory.Hotbar == hotbar
		}
	}, func() bool {
		_, loaded := mirror.Chunk(core.Overworld, target.Chunk())
		return ready && inventoryConfirmed && loaded
	})

	sendClientMessage(t, clientEndpoint, network.PlayerInput{Sequence: 1, Pitch: -0.2, Mining: true})
	stoneProgress := waitMemoryMiningState(t, running, clientEndpoint, mirror, func(state network.PlayerState) bool {
		return state.MiningActive && state.MiningProgressTicks == 1
	})
	if stoneProgress.MiningTarget != target || stoneProgress.MiningRequiredTicks != 15 || !stoneProgress.MiningHarvestable {
		t.Fatalf("石镐首 tick = %+v", stoneProgress)
	}
	for want := uint16(2); want <= 4; want++ {
		state := nextMemoryMiningState(t, running, clientEndpoint, mirror, nil)
		if state.MiningProgressTicks != want || state.MiningRequiredTicks != 15 {
			t.Fatalf("石镐进度 = %d/%d，想要 %d/15", state.MiningProgressTicks, state.MiningRequiredTicks, want)
		}
	}

	sendClientMessage(t, clientEndpoint, network.SelectHotbar{Sequence: 2, Slot: 1})
	reset := waitMemoryMiningState(t, running, clientEndpoint, mirror, func(state network.PlayerState) bool {
		return state.MiningActive && state.MiningRequiredTicks == 8
	})
	if reset.MiningProgressTicks != 1 || !reset.MiningHarvestable {
		t.Fatalf("切换铁镐后未从 1 重置: %+v", reset)
	}

	sendClientMessage(t, clientEndpoint, network.PlayerInput{Sequence: 3, Pitch: -0.2})
	released := waitMemoryMiningState(t, running, clientEndpoint, mirror, func(state network.PlayerState) bool {
		return state.LastInputSequence == 3
	})
	if released.MiningActive {
		t.Fatalf("松键后采掘仍活动: %+v", released)
	}

	sendClientMessage(t, clientEndpoint, network.SelectHotbar{Sequence: 4, Slot: 2})
	selectedWrongTool := false
	stepUntilCollect(t, running, clientEndpoint, mirror, func(message network.ServerMessage) {
		switch message := message.(type) {
		case network.PlayerState:
			assertValidMiningPlayerState(t, message)
		case network.InventoryState:
			selectedWrongTool = message.Inventory.Hotbar.Selected == 2
		}
	}, func() bool { return selectedWrongTool })
	sendClientMessage(t, clientEndpoint, network.PlayerInput{Sequence: 5, Pitch: -0.2, Mining: true})
	wrongTool := make([]network.PlayerState, 0, 30)
	var completionMessages []network.ServerMessage
	for len(wrongTool) < 30 {
		tickMessages := make([]network.ServerMessage, 0, 2)
		state := nextMemoryMiningState(t, running, clientEndpoint, mirror, func(message network.ServerMessage) {
			// 异步生成完成的相邻快照可与采掘完成同 tick 发布；它不属于完成帧的 delta+PlayerState 契约。
			if _, ok := message.(network.ChunkSnapshot); ok {
				return
			}
			tickMessages = append(tickMessages, message)
		})
		if state.LastInputSequence < 5 {
			continue
		}
		wrongTool = append(wrongTool, state)
		if len(wrongTool) == 30 {
			completionMessages = tickMessages
		}
	}
	for index, state := range wrongTool[:29] {
		want := uint16(index + 1)
		if !state.MiningActive || state.MiningProgressTicks != want || state.MiningRequiredTicks != 30 || state.MiningHarvestable {
			t.Fatalf("错误工具进度[%d] = %+v", index, state)
		}
	}
	assertWrongToolMiningCompletionFrame(t, completionMessages, target)
	completionContext, cancelCompletion := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelCompletion()
	if message, err := clientEndpoint.Recv(completionContext); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("采掘完成后意外 server message = %#v, err=%v", message, err)
	}
	if block, loaded := mirror.BlockAt(core.Overworld, target); !loaded || block != core.AirID {
		t.Fatalf("错误工具完成后方块 = %d,%t", block, loaded)
	}

	running.SetBlockForTest(target, core.StoneID)
	sendClientMessage(t, clientEndpoint, network.PlayerInput{Sequence: 6, Pitch: -0.2, Mining: true})
	waitMemoryMiningState(t, running, clientEndpoint, mirror, func(state network.PlayerState) bool {
		return state.LastInputSequence == 6 && state.MiningActive
	})
	if err := clientEndpoint.Close(); err != nil {
		t.Fatalf("关闭 Memory 客户端: %v", err)
	}
	deadline := time.Now().Add(waitDeadline)
	for {
		running.StepForTest()
		if _, ok := playerStateForExternalTest(running); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Memory 断线后采掘会话未清除")
		}
	}
}

func assertWrongToolMiningCompletionFrame(t *testing.T, messages []network.ServerMessage, target core.BlockPos) {
	t.Helper()
	if len(messages) != 2 {
		t.Fatalf("错误工具完成帧消息数=%d，想要 2: %+v", len(messages), messages)
	}
	delta, ok := messages[0].(network.BlockChanges)
	if !ok {
		t.Fatalf("错误工具完成帧第一条=%T，想要 BlockChanges", messages[0])
	}
	if err := delta.Validate(); err != nil {
		t.Fatalf("错误工具 BlockChanges 非法: %v", err)
	}
	if delta.Dimension != core.Overworld || delta.Chunk != target.Chunk() ||
		len(delta.Changes) != 1 || delta.Changes[0] != (network.BlockChange{Position: target, Block: core.AirID}) {
		t.Fatalf("错误工具未精确破坏目标: %+v", delta)
	}
	state, ok := messages[1].(network.PlayerState)
	if !ok {
		t.Fatalf("错误工具完成帧第二条=%T，想要 PlayerState", messages[1])
	}
	assertValidMiningPlayerState(t, state)
	if state.MiningActive || state.MiningTarget != (core.BlockPos{}) || state.MiningProgressTicks != 0 ||
		state.MiningRequiredTicks != 0 || state.MiningHarvestable {
		t.Fatalf("错误工具完成帧未以规范非活动状态收尾: %+v", state)
	}
}

func waitMemoryMiningState(
	t *testing.T,
	running *server.Server,
	endpoint network.ClientEndpoint,
	mirror *client.Mirror,
	done func(network.PlayerState) bool,
) network.PlayerState {
	t.Helper()
	var matched network.PlayerState
	stepUntilCollect(t, running, endpoint, mirror, func(message network.ServerMessage) {
		state, ok := message.(network.PlayerState)
		if !ok {
			return
		}
		assertValidMiningPlayerState(t, state)
		if done(state) {
			matched = state
		}
	}, func() bool { return matched.ServerTick != 0 })
	return matched
}

func nextMemoryMiningState(
	t *testing.T,
	running *server.Server,
	endpoint network.ClientEndpoint,
	mirror *client.Mirror,
	collect func(network.ServerMessage),
) network.PlayerState {
	t.Helper()
	result := running.StepForTest()
	var state network.PlayerState
	drainServerMessages(t, endpoint, mirror, func(message network.ServerMessage) {
		if collect != nil {
			collect(message)
		}
		if current, ok := message.(network.PlayerState); ok {
			assertValidMiningPlayerState(t, current)
			state = current
		}
	}, result.Tick)
	return state
}

func assertValidMiningPlayerState(t *testing.T, state network.PlayerState) {
	t.Helper()
	if err := state.Validate(); err != nil {
		t.Fatalf("PlayerState tick=%d 非法: %v", state.ServerTick, err)
	}
}

// awaitInteractionChange 等待唯一一份 base→new 的 delta，并返回其中唯一的方块变化。
func awaitInteractionChange(
	t *testing.T,
	running *server.Server,
	endpoint network.ClientEndpoint,
	mirror *client.Mirror,
	chunk core.ChunkPos,
	baseRevision uint64,
	newRevision uint64,
	mesher ...*client.Mesher,
) network.BlockChange {
	t.Helper()
	var matching []network.BlockChanges
	stepUntilCollect(t, running, endpoint, mirror, func(message network.ServerMessage) {
		if delta, ok := message.(network.BlockChanges); ok && delta.Chunk == chunk {
			matching = append(matching, delta)
		}
	}, func() bool {
		_, revision, ok := mirror.Hash(core.Overworld, chunk)
		return ok && revision == newRevision
	}, mesher...)
	if len(matching) != 1 || matching[0].BaseRevision != baseRevision ||
		matching[0].NewRevision != newRevision || len(matching[0].Changes) != 1 {
		t.Fatalf(
			"交互 delta = %+v，想要唯一 %d→%d 的单方块变化",
			matching,
			baseRevision,
			newRevision,
		)
	}
	change := matching[0].Changes[0]
	assertMirrorBlock(t, mirror, change.Position, change.Block)
	return change
}

func assertContiguousInteraction(
	t *testing.T,
	running *server.Server,
	endpoint network.ClientEndpoint,
	mirror *client.Mirror,
	baseRevision uint64,
	newRevision uint64,
	position core.BlockPos,
	wantBlock core.BlockID,
) {
	t.Helper()
	var matching []network.BlockChanges
	stepUntilCollect(t, running, endpoint, mirror, func(message network.ServerMessage) {
		if delta, ok := message.(network.BlockChanges); ok && delta.Chunk == position.Chunk() {
			matching = append(matching, delta)
		}
	}, func() bool {
		_, revision, ok := mirror.Hash(core.Overworld, position.Chunk())
		return ok && revision == newRevision
	})
	if len(matching) != 1 ||
		matching[0].BaseRevision != baseRevision ||
		matching[0].NewRevision != newRevision {
		t.Fatalf(
			"交互 delta = %+v，想要唯一 %d→%d",
			matching,
			baseRevision,
			newRevision,
		)
	}
	assertMirrorBlock(t, mirror, position, wantBlock)
}

func assertMirrorBlock(
	t *testing.T,
	mirror *client.Mirror,
	position core.BlockPos,
	want core.BlockID,
) {
	t.Helper()
	got, loaded := mirror.BlockAt(core.Overworld, position)
	if !loaded || got != want {
		t.Fatalf("BlockAt(%+v) = %d,%v，想要 %d,true", position, got, loaded, want)
	}
}

func sendClientMessage(
	t *testing.T,
	endpoint network.ClientEndpoint,
	message network.ClientMessage,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
	defer cancel()
	if err := endpoint.Send(ctx, message); err != nil {
		t.Fatalf("发送 %#v: %v", message, err)
	}
}

func stepUntil(
	t *testing.T,
	running *server.Server,
	endpoint network.ClientEndpoint,
	mirror *client.Mirror,
	done func() bool,
	mesher ...*client.Mesher,
) {
	t.Helper()
	stepUntilCollect(t, running, endpoint, mirror, nil, done, mesher...)
}

func stepUntilCollect(
	t *testing.T,
	running *server.Server,
	endpoint network.ClientEndpoint,
	mirror *client.Mirror,
	collect func(network.ServerMessage),
	done func() bool,
	mesher ...*client.Mesher,
) {
	t.Helper()
	deadline := time.Now().Add(waitDeadline)
	for !done() {
		result := running.StepForTest()
		drainServerMessages(t, endpoint, mirror, collect, result.Tick, mesher...)
		if time.Now().After(deadline) {
			t.Fatalf("等待权威状态超时；mirror center=%+v", mirrorChunkSummary(mirror))
		}
	}
}

func drainServerMessages(
	t *testing.T,
	endpoint network.ClientEndpoint,
	mirror *client.Mirror,
	collect func(network.ServerMessage),
	throughTick uint64,
	mesher ...*client.Mesher,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
	defer cancel()
	for {
		message, err := endpoint.Recv(ctx)
		if err != nil {
			t.Fatalf("接收服务端消息: %v", err)
		}
		if collect != nil {
			collect(message)
		}
		switch message.(type) {
		case network.InventoryState, network.ItemDropUpserts, network.ItemDropRemoves:
			// 快捷栏与掉落物由独立的只读镜像消费，不进入世界镜像。
			continue
		}
		if state, ok := message.(network.PlayerState); ok {
			if state.ServerTick == throughTick {
				return
			}
			if state.ServerTick > throughTick {
				t.Fatalf("PlayerState tick=%d，跳过目标 tick=%d", state.ServerTick, throughTick)
			}
			continue
		}
		update, err := mirror.Apply(message)
		if err != nil {
			t.Fatalf("Mirror.Apply(%T): %v", message, err)
		}
		if update.Resync != nil {
			t.Fatalf("无头一致性场景意外需要 resync: %+v", update.Resync)
		}
		if update.Rejected != nil {
			t.Fatalf("权威命令被拒绝: %+v", update.Rejected)
		}
		for _, current := range mesher {
			if current != nil {
				current.MarkDirty(update.Dirty...)
			}
		}
	}
}

// waitForMesherStats 等待异步 Mesher 到达可观察状态，不额外推进权威 tick。
func waitForMesherStats(
	t *testing.T,
	mesher *client.Mesher,
	ready func(client.MesherStats) bool,
) {
	t.Helper()
	deadline := time.Now().Add(waitDeadline)
	for {
		stats := mesher.Stats()
		if ready(stats) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待 Mesher 状态超时: %+v", stats)
		}
		runtime.Gosched()
	}
}

func mirrorChunkSummary(mirror *client.Mirror) any {
	chunk, ok := mirror.Chunk(core.Overworld, core.ChunkPos{})
	if !ok {
		return "missing"
	}
	return struct {
		Revision uint64
		Desynced bool
	}{Revision: chunk.Revision, Desynced: chunk.Desynced}
}
