package server

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/world"
)

func TestTrustedObserverIsDisabledByDefault(t *testing.T) {
	running := newDefaultTestServer(t)
	err := running.SetTrustedObserverCenter(core.Overworld, core.ChunkPos{X: 99})
	if !errors.Is(err, ErrTrustedObserverDisabled) {
		t.Fatalf("err=%v", err)
	}
}

func TestTrustedObserverCoalescesCenterAndDrivesGeneration(t *testing.T) {
	running := newTrustedObserverTestServer(t)
	for x := int32(1); x <= 3; x++ {
		if err := running.SetTrustedObserverCenter(
			core.Overworld,
			core.ChunkPos{X: x},
		); err != nil {
			t.Fatal(err)
		}
	}
	result := running.StepForTest()
	if !containsChunk(result.Acquire, core.ChunkPos{X: 3}) ||
		containsChunk(result.Acquire, core.ChunkPos{X: 1}) || len(result.Generate) != 0 {
		t.Fatalf("Acquire=%+v Generate=%+v", result.Acquire, result.Generate)
	}
}

func TestTrustedObserverDoesNotRegisterPlayer(t *testing.T) {
	running := newTrustedObserverTestServer(t)
	if player, ok := running.PlayerStateFor(testSessionID); ok {
		t.Fatalf("trusted observer 注册了玩家: %+v", player)
	}
}

func TestTrustedObserverRejectsNonOverworldCenter(t *testing.T) {
	running := newTrustedObserverTestServer(t)
	if err := running.SetTrustedObserverCenter(
		core.DimensionID(99),
		core.ChunkPos{X: 7},
	); err == nil {
		t.Fatal("非 Overworld trusted center 未被拒绝")
	}
	if result := running.StepForTest(); len(result.Acquire) != 0 || len(result.Generate) != 0 {
		t.Fatalf("非法 center 驱动了 acquisition/generation: %+v", result)
	}
}

func TestTrustedObserverSequenceCannotBePoisonedByClientSequence(t *testing.T) {
	observerClient, observerEndpoint := network.NewMemoryPair(32)
	playerClient, playerEndpoint := network.NewMemoryPair(32)
	config := DefaultConfig(42)
	config.ViewRadius = 0
	config.Workers = 1
	config.TrustedObserver = true
	running := NewWorld(config, playerTestGenerator{}, testStore())
	if err := running.AttachTrustedObserver(observerEndpoint); err != nil {
		t.Fatal(err)
	}
	if _, err := running.AttachSession(registrySessionSpec(7, 1, playerEndpoint)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = observerClient.Close()
		_ = playerClient.Close()
		shutdownServerForTest(t, running)
	})

	if err := running.SetTrustedObserverCenter(
		core.Overworld,
		core.ChunkPos{X: 1},
	); err != nil {
		t.Fatal(err)
	}
	sendPlayerClientMessage(t, playerClient, network.RequestChunkResync{
		Sequence:  1_000,
		Dimension: core.Overworld,
		Chunk:     core.ChunkPos{X: 1},
	})
	waitForQueuedPlayerCommand(t, running)
	if first := running.StepForTest(); !containsChunk(first.Acquire, core.ChunkPos{X: 1}) {
		t.Fatalf("首个 trusted center 未驱动读取: %+v", first.Acquire)
	}

	if err := running.SetTrustedObserverCenter(
		core.Overworld,
		core.ChunkPos{X: 2},
	); err != nil {
		t.Fatal(err)
	}
	if second := running.StepForTest(); !containsChunk(second.Acquire, core.ChunkPos{X: 2}) {
		t.Fatalf("客户端 sequence 饿死后续 trusted center: %+v", second.Acquire)
	}
}

func TestTrustedObserverAppliedCenterWaitsForStepWithPreloadedTarget(t *testing.T) {
	client, endpoint := network.NewMemoryPair(32)
	generator := &gatedGenerator{release: make(chan struct{}), flat: true}
	config := DefaultConfig(42)
	config.ViewRadius = 1
	config.Workers = 1
	config.TrustedObserver = true
	running := newMemoryAttachedWorldForTest(config, endpoint, generator)
	t.Cleanup(func() {
		close(generator.release)
		_ = client.Close()
		shutdownServerForTest(t, running)
	})

	initial := core.ChunkPos{}
	target := core.ChunkPos{X: 1}
	if err := running.SetTrustedObserverCenter(core.Overworld, initial); err != nil {
		t.Fatal(err)
	}
	requested := running.StepForTest()
	if !containsChunk(requested.Acquire, target) {
		t.Fatalf("初始订阅未包含预加载目标: %+v", requested.Acquire)
	}
	submitServerAcquiredMisses(running, requested.Acquire)
	running.engine.Step()
	running.engine.SubmitGenerated(sim.GeneratedChunk{
		Dimension: core.Overworld,
		Pos:       target,
		Chunk:     generator.chunk(target),
	})
	ready := running.StepForTest()
	if !containsChunk(ready.Ready, target) {
		t.Fatalf("目标 chunk 未预加载: %+v", ready.Ready)
	}
	if _, _, ok := running.ChunkHash(core.Overworld, target); !ok {
		t.Fatal("预加载目标 hash 不可用")
	}

	dimension, center, sequence, ok := running.AppliedTrustedObserverCenter()
	if !ok || dimension != core.Overworld || center != initial || sequence != 1 {
		t.Fatalf("初始 applied center=(%d,%+v,%d,%v)", dimension, center, sequence, ok)
	}
	if err := running.SetTrustedObserverCenter(core.Overworld, target); err != nil {
		t.Fatal(err)
	}
	dimension, center, queuedSequence, ok := running.AppliedTrustedObserverCenter()
	if !ok || dimension != core.Overworld || center != initial || queuedSequence != sequence {
		t.Fatalf("queue 尚未 drain 就报告 applied=(%d,%+v,%d,%v)", dimension, center, queuedSequence, ok)
	}

	running.StepForTest()
	dimension, center, appliedSequence, ok := running.AppliedTrustedObserverCenter()
	if !ok || dimension != core.Overworld || center != target || appliedSequence <= sequence {
		t.Fatalf("Step 后 applied center=(%d,%+v,%d,%v)，先前 sequence=%d",
			dimension, center, appliedSequence, ok, sequence)
	}
}

func TestTranslatePlayerMessage(t *testing.T) {
	tests := []struct {
		name    string
		message network.ClientMessage
		want    sim.Command
	}{
		{
			name:    "drop selected item carries only the sequence",
			message: network.DropSelectedItem{Sequence: 17},
			want: sim.Command{
				Session:  testSessionID,
				Sequence: 17,
				Kind:     sim.CommandDropSelectedItem,
			},
		},
		{
			name: "input",
			message: network.PlayerInput{
				Sequence: 11,
				MoveX:    -1,
				MoveZ:    1,
				Jump:     true,
				Yaw:      0.75,
				Pitch:    -0.25,
				Mining:   true,
			},
			want: sim.Command{
				Session:  testSessionID,
				Sequence: 11,
				Kind:     sim.CommandPlayerInput,
				MoveX:    -1,
				MoveZ:    1,
				Jump:     true,
				Yaw:      0.75,
				Pitch:    -0.25,
				Mining:   true,
			},
		},
		{
			name: "place block uses only player look",
			message: network.PlaceBlock{
				Sequence: 13,
				Yaw:      1.5,
				Pitch:    -0.75,
				Slot:     4,
			},
			want: sim.Command{
				Session:  testSessionID,
				Sequence: 13,
				Kind:     sim.CommandPlaceBlock,
				Yaw:      1.5,
				Pitch:    -0.75,
				Slot:     4,
			},
		},
		{
			name:    "craft recipe carries only the recipe id",
			message: network.CraftRecipe{Sequence: 16, Recipe: core.RecipeStoneBricks},
			want: sim.Command{
				Session:  testSessionID,
				Sequence: 16,
				Kind:     sim.CommandCraftRecipe,
				Recipe:   core.RecipeStoneBricks,
			},
		},
		{
			name:    "move inventory stack carries only the slots",
			message: network.MoveInventoryStack{Sequence: 15, From: 2, To: 30},
			want: sim.Command{
				Session:  testSessionID,
				Sequence: 15,
				Kind:     sim.CommandMoveInventoryStack,
				Slot:     2,
				ToSlot:   30,
			},
		},
		{
			name:    "select hotbar carries only the slot",
			message: network.SelectHotbar{Sequence: 14, Slot: 7},
			want: sim.Command{
				Session:  testSessionID,
				Sequence: 14,
				Kind:     sim.CommandSelectHotbar,
				Slot:     7,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := translateClientMessage(testSessionID, test.message)
			if !ok || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("translateClientMessage(%#v) = %#v,%v，想要 %#v,true", test.message, got, ok, test.want)
			}
		})
	}

	reasons := []struct {
		sim     sim.RejectReason
		network network.RejectReason
	}{
		{sim: sim.RejectInvalidInput, network: network.RejectInvalidInput},
		{sim: sim.RejectPlayerNotReady, network: network.RejectPlayerNotReady},
	}
	for _, reason := range reasons {
		got, ok := networkRejectReason(reason.sim)
		if !ok || got != reason.network {
			t.Fatalf("networkRejectReason(%v) = %q,%v，想要 %q,true", reason.sim, got, ok, reason.network)
		}
	}
}

func TestServerPublishesPlayerStateAndInputAck(t *testing.T) {
	clientEndpoint, serverEndpoint := network.NewMemoryPair(32)
	config := DefaultConfig(42)
	config.ViewRadius, config.Workers = 0, 1
	config.SpawnAnchor = core.ChunkPos{X: 4, Z: -3}
	running := newMemoryAttachedWorldForTest(config, serverEndpoint, playerTestGenerator{})
	t.Cleanup(func() {
		_ = clientEndpoint.Close()
		shutdownServerForTest(t, running)
	})

	registered, ok := running.PlayerStateFor(testSessionID)
	if !ok || registered.Session != testSessionID || registered.Ready ||
		registered.Dimension != core.Overworld || registered.ViewCenter != config.SpawnAnchor {
		t.Fatalf("New 后 PlayerState = %+v,%v", registered, ok)
	}
	first := running.StepForTest()
	wantAcquire := []core.ChunkKey{{Dimension: core.Overworld, Pos: config.SpawnAnchor}}
	if !reflect.DeepEqual(first.Acquire, wantAcquire) || len(first.Generate) != 0 {
		t.Fatalf("首 tick Acquire = %+v Generate=%+v，想要 %+v", first.Acquire, first.Generate, wantAcquire)
	}
	waitForReadyPlayer(t, running, clientEndpoint)

	sendPlayerClientMessage(t, clientEndpoint, network.PlayerInput{
		Sequence: 1,
		MoveZ:    1,
		Yaw:      0,
		Pitch:    0,
	})
	waitForQueuedPlayerCommand(t, running)
	result := running.StepForTest()
	state := receivePlayerStateForTick(t, clientEndpoint, result.Tick)
	if !state.Ready || state.LastInputSequence != 1 || state.ServerTick != result.Tick || state.ServerTick == 0 {
		t.Fatalf("state=%+v result.Tick=%d", state, result.Tick)
	}
}

func TestPlayerStatePublicationOrder(t *testing.T) {
	running, client, generator := newPublicationServer(t, 0, 8, 1<<20, true)

	requested := running.StepForTest()
	if len(requested.Acquire) != 1 {
		t.Fatalf("首 tick Acquire = %+v", requested.Acquire)
	}
	firstState := recvServerMessage(t, client)
	if state, ok := firstState.(network.PlayerState); !ok || state.ServerTick != requested.Tick {
		t.Fatalf("首 tick message = %#v", firstState)
	}
	submitServerAcquiredMisses(running, requested.Acquire)
	generated := running.engine.Step()
	if len(generated.Generate) != 1 {
		t.Fatalf("Generate=%+v", generated.Generate)
	}

	running.engine.SubmitGenerated(sim.GeneratedChunk{
		Dimension: core.Overworld,
		Pos:       core.ChunkPos{},
		Chunk:     generator.chunk(core.ChunkPos{}),
	})
	ready := running.StepForTest()
	readyMessages := []network.ServerMessage{
		recvServerMessage(t, client),
		recvServerMessage(t, client),
		recvServerMessage(t, client),
	}
	if _, ok := readyMessages[0].(network.ChunkSnapshot); !ok {
		t.Fatalf("Ready tick 首消息 = %T，想要 ChunkSnapshot", readyMessages[0])
	}
	// 初始快捷栏必须先于使客户端开始交互的 Ready 玩家状态。
	if state, ok := readyMessages[1].(network.InventoryState); !ok ||
		state.Inventory != (core.Inventory{}) {
		t.Fatalf("Ready tick 次消息 = %#v，想要空物品状态", readyMessages[1])
	}
	readyState, ok := readyMessages[2].(network.PlayerState)
	if !ok || readyState.ServerTick != ready.Tick ||
		readyState.LastInputSequence != 0 ||
		readyState.Dimension != core.Overworld ||
		readyState.Position != (mgl32.Vec3{0.5, 1, 0.5}) ||
		readyState.Velocity != (mgl32.Vec3{}) ||
		readyState.Yaw != 0 || readyState.Pitch != 0 ||
		!readyState.OnGround || !readyState.Ready || !readyState.Reset {
		t.Fatalf("Ready tick 尾消息 = %#v", readyMessages[2])
	}

	running.engine.Enqueue(sim.Command{
		Session: testSessionID, Sequence: 1, Kind: sim.CommandPlayerInput,
		Yaw: 0, Pitch: -1.5, Mining: true,
	})
	for range 4 {
		if primed := running.engine.Step(); len(primed.Changes) != 0 {
			t.Fatalf("采掘完成前出现变更: %+v", primed.Changes)
		}
	}
	running.incoming <- incomingCommand{
		Session: testSessionID, Generation: 1,
		Command: sim.Command{
			Session:  testSessionID,
			Sequence: 2,
			Kind:     sim.CommandSelectHotbar,
			Slot:     core.HotbarSlots,
		},
	}
	changed := running.StepForTest()
	changedMessages := []network.ServerMessage{
		recvServerMessage(t, client),
		recvServerMessage(t, client),
		recvServerMessage(t, client),
		recvServerMessage(t, client),
	}
	if _, ok := changedMessages[0].(network.BlockChanges); !ok {
		t.Fatalf("change tick 首消息 = %T，想要 BlockChanges", changedMessages[0])
	}
	// 挖掘只产生掉落物：本 tick 在拒绝之前发布一次掉落物 upsert。
	upserts, ok := changedMessages[1].(network.ItemDropUpserts)
	if !ok || len(upserts.Drops) != 1 || upserts.Drops[0].Item != core.ItemGrass {
		t.Fatalf("change tick 次消息 = %#v", changedMessages[1])
	}
	rejection, ok := changedMessages[2].(network.CommandRejected)
	if !ok || rejection.Sequence != 2 || rejection.Reason != network.RejectInvalidSlot {
		t.Fatalf("change tick 第三条消息 = %#v", changedMessages[2])
	}
	state, ok := changedMessages[3].(network.PlayerState)
	if !ok || state.ServerTick != changed.Tick || state.LastInputSequence != 1 {
		t.Fatalf("change tick 尾消息 = %#v", changedMessages[3])
	}

	forgottenKey := core.ChunkKey{
		Dimension: core.Overworld,
		Pos:       core.ChunkPos{},
	}
	running.sessions[testSessionID].publications[forgottenKey] = &publication{snapshotSent: true}
	player, ok := running.engine.Player(testSessionID)
	if !ok {
		t.Fatal("本地玩家不存在")
	}
	forgetTick := changed.Tick + 1
	running.publish(sim.TickResult{
		Tick: forgetTick,
		Forget: map[sim.SessionID][]core.ChunkKey{
			testSessionID: {forgottenKey},
		},
		Players: []sim.PlayerUpdate{player},
	})
	forgetMessage := recvServerMessage(t, client)
	forget, ok := forgetMessage.(network.ForgetChunks)
	if !ok || forget.Dimension != core.Overworld ||
		!reflect.DeepEqual(forget.Chunks, []core.ChunkPos{{}}) {
		t.Fatalf("forget tick 首消息 = %#v", forgetMessage)
	}
	forgetStateMessage := recvServerMessage(t, client)
	forgetState, ok := forgetStateMessage.(network.PlayerState)
	if !ok || forgetState.ServerTick != forgetTick {
		t.Fatalf("forget tick 尾消息 = %#v", forgetStateMessage)
	}
}

func TestConfigRejectsUnsupportedSpawnDimension(t *testing.T) {
	config := DefaultConfig(1)
	config.SpawnDimension = core.DimensionID(99)
	defer func() {
		if recover() == nil {
			t.Fatal("非 Overworld 出生维度未 panic")
		}
	}()
	config.validate()
}

func waitForReadyPlayer(
	t *testing.T,
	running *Server,
	client network.ClientEndpoint,
) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		result := running.StepForTest()
		state := receivePlayerStateForTick(t, client, result.Tick)
		if state.Ready {
			return
		}
	}
	t.Fatal("等待 ready PlayerState 超时")
}

func waitForQueuedPlayerCommand(t *testing.T, running *Server) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for len(running.incoming) == 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if len(running.incoming) == 0 {
		t.Fatal("endpoint reader 未翻译玩家命令")
	}
}

func receivePlayerStateForTick(
	t *testing.T,
	client network.ClientEndpoint,
	tick uint64,
) network.PlayerState {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for {
		message, err := client.Recv(ctx)
		if err != nil {
			t.Fatalf("接收 PlayerState: %v", err)
		}
		if state, ok := message.(network.PlayerState); ok {
			if state.ServerTick == tick {
				return state
			}
			if state.ServerTick > tick {
				t.Fatalf("PlayerState tick=%d，跳过目标 tick=%d", state.ServerTick, tick)
			}
		}
	}
}

func sendPlayerClientMessage(
	t *testing.T,
	client network.ClientEndpoint,
	message network.ClientMessage,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Send(ctx, message); err != nil {
		t.Fatalf("发送 %#v: %v", message, err)
	}
}

type playerTestGenerator struct{}

func newDefaultTestServer(t *testing.T) *Server {
	t.Helper()
	_, endpoint := network.NewMemoryPair(32)
	config := DefaultConfig(42)
	config.ViewRadius = 0
	config.Workers = 1
	running := newMemoryAttachedWorldForTest(config, endpoint, playerTestGenerator{})
	t.Cleanup(func() { shutdownServerForTest(t, running) })
	return running
}

func newTrustedObserverTestServer(t *testing.T) *Server {
	t.Helper()
	_, endpoint := network.NewMemoryPair(32)
	config := DefaultConfig(42)
	config.ViewRadius = 0
	config.Workers = 1
	config.TrustedObserver = true
	running := newMemoryAttachedWorldForTest(config, endpoint, playerTestGenerator{})
	t.Cleanup(func() { shutdownServerForTest(t, running) })
	return running
}

func containsChunk(keys []core.ChunkKey, pos core.ChunkPos) bool {
	for _, key := range keys {
		if key.Dimension == core.Overworld && key.Pos == pos {
			return true
		}
	}
	return false
}

func (playerTestGenerator) GenerateChunk(position core.ChunkPos) *world.Chunk {
	chunk := world.NewChunk(position)
	for z := 0; z < core.SectionSize; z++ {
		for x := 0; x < core.SectionSize; x++ {
			worldX := position.X<<core.SectionShift + int32(x)
			worldZ := position.Z<<core.SectionShift + int32(z)
			chunk.SetBlock(x, 0, z, playerTestGenerator{}.BaseBlockAt(core.BlockPos{X: worldX, Y: 0, Z: worldZ}))
		}
	}
	chunk.Compact()
	return chunk
}

func (playerTestGenerator) BaseBlockAt(position core.BlockPos) core.BlockID {
	if position.Y == 0 {
		return core.GrassID
	}
	return core.AirID
}
