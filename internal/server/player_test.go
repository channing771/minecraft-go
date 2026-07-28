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
	if !containsChunk(result.Generate, core.ChunkPos{X: 3}) ||
		containsChunk(result.Generate, core.ChunkPos{X: 1}) {
		t.Fatalf("Generate=%+v", result.Generate)
	}
}

func TestTrustedObserverDoesNotRegisterPlayer(t *testing.T) {
	running := newTrustedObserverTestServer(t)
	if player, ok := running.PlayerState(); ok {
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
	if result := running.StepForTest(); len(result.Generate) != 0 {
		t.Fatalf("非法 center 驱动了生成: %+v", result.Generate)
	}
}

func TestTrustedObserverSequenceCannotBePoisonedByClientSequence(t *testing.T) {
	client, endpoint := network.NewMemoryPair(32)
	config := DefaultConfig(42)
	config.ViewRadius = 0
	config.Workers = 1
	config.TrustedObserver = true
	running := New(config, endpoint, playerTestGenerator{})
	t.Cleanup(func() {
		_ = client.Close()
		running.Close()
	})

	if err := running.SetTrustedObserverCenter(
		core.Overworld,
		core.ChunkPos{X: 1},
	); err != nil {
		t.Fatal(err)
	}
	sendPlayerClientMessage(t, client, network.RequestChunkResync{
		Sequence:  1_000,
		Dimension: core.Overworld,
		Chunk:     core.ChunkPos{X: 1},
	})
	waitForQueuedPlayerCommand(t, running)
	if first := running.StepForTest(); !containsChunk(first.Generate, core.ChunkPos{X: 1}) {
		t.Fatalf("首个 trusted center 未驱动生成: %+v", first.Generate)
	}

	if err := running.SetTrustedObserverCenter(
		core.Overworld,
		core.ChunkPos{X: 2},
	); err != nil {
		t.Fatal(err)
	}
	if second := running.StepForTest(); !containsChunk(second.Generate, core.ChunkPos{X: 2}) {
		t.Fatalf("客户端 sequence 饿死后续 trusted center: %+v", second.Generate)
	}
}

func TestPlayerMessageTranslation(t *testing.T) {
	tests := []struct {
		name    string
		message network.ClientMessage
		want    sim.Command
	}{
		{
			name: "input",
			message: network.PlayerInput{
				Sequence: 11,
				MoveX:    -1,
				MoveZ:    1,
				Jump:     true,
				Yaw:      0.75,
				Pitch:    -0.25,
			},
			want: sim.Command{
				Session:  localSessionID,
				Sequence: 11,
				Kind:     sim.CommandPlayerInput,
				MoveX:    -1,
				MoveZ:    1,
				Jump:     true,
				Yaw:      0.75,
				Pitch:    -0.25,
			},
		},
		{
			name: "break block uses only player look",
			message: network.BreakBlock{
				Sequence: 12,
				Yaw:      -1.25,
				Pitch:    0.5,
			},
			want: sim.Command{
				Session:  localSessionID,
				Sequence: 12,
				Kind:     sim.CommandBreakBlock,
				Yaw:      -1.25,
				Pitch:    0.5,
			},
		},
		{
			name: "place block uses only player look",
			message: network.PlaceBlock{
				Sequence: 13,
				Yaw:      1.5,
				Pitch:    -0.75,
				Block:    core.DirtID,
			},
			want: sim.Command{
				Session:  localSessionID,
				Sequence: 13,
				Kind:     sim.CommandPlaceBlock,
				Yaw:      1.5,
				Pitch:    -0.75,
				Block:    core.DirtID,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := translateClientMessage(test.message)
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
	running := New(config, serverEndpoint, playerTestGenerator{})
	t.Cleanup(func() {
		_ = clientEndpoint.Close()
		running.Close()
	})

	registered, ok := running.PlayerState()
	if !ok || registered.Session != localSessionID || registered.Ready ||
		registered.Dimension != core.Overworld || registered.ViewCenter != config.SpawnAnchor {
		t.Fatalf("New 后 PlayerState = %+v,%v", registered, ok)
	}
	first := running.StepForTest()
	wantGenerate := []core.ChunkKey{{Dimension: core.Overworld, Pos: config.SpawnAnchor}}
	if !reflect.DeepEqual(first.Generate, wantGenerate) {
		t.Fatalf("首 tick Generate = %+v，想要 %+v", first.Generate, wantGenerate)
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
	if len(requested.Generate) != 1 {
		t.Fatalf("首 tick Generate = %+v", requested.Generate)
	}
	firstState := recvServerMessage(t, client)
	if state, ok := firstState.(network.PlayerState); !ok || state.ServerTick != requested.Tick {
		t.Fatalf("首 tick message = %#v", firstState)
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
	}
	if _, ok := readyMessages[0].(network.ChunkSnapshot); !ok {
		t.Fatalf("Ready tick 首消息 = %T，想要 ChunkSnapshot", readyMessages[0])
	}
	readyState, ok := readyMessages[1].(network.PlayerState)
	if !ok || readyState.ServerTick != ready.Tick ||
		readyState.LastInputSequence != 0 ||
		readyState.Dimension != core.Overworld ||
		readyState.Position != (mgl32.Vec3{0.5, 1, 0.5}) ||
		readyState.Velocity != (mgl32.Vec3{}) ||
		readyState.Yaw != 0 || readyState.Pitch != 0 ||
		!readyState.OnGround || !readyState.Ready || !readyState.Reset {
		t.Fatalf("Ready tick 尾消息 = %#v", readyMessages[1])
	}

	running.incoming <- sim.Command{
		Session:  localSessionID,
		Sequence: 1,
		Kind:     sim.CommandBreakBlock,
		Yaw:      0,
		Pitch:    -1.5,
	}
	running.incoming <- sim.Command{
		Session:  localSessionID,
		Sequence: 2,
		Kind:     sim.CommandPlayerInput,
		MoveX:    2,
	}
	changed := running.StepForTest()
	changedMessages := []network.ServerMessage{
		recvServerMessage(t, client),
		recvServerMessage(t, client),
		recvServerMessage(t, client),
	}
	if _, ok := changedMessages[0].(network.BlockChanges); !ok {
		t.Fatalf("change tick 首消息 = %T，想要 BlockChanges", changedMessages[0])
	}
	rejection, ok := changedMessages[1].(network.CommandRejected)
	if !ok || rejection.Sequence != 2 || rejection.Reason != network.RejectInvalidInput {
		t.Fatalf("change tick 次消息 = %#v", changedMessages[1])
	}
	state, ok := changedMessages[2].(network.PlayerState)
	if !ok || state.ServerTick != changed.Tick || state.LastInputSequence != 2 {
		t.Fatalf("change tick 尾消息 = %#v", changedMessages[2])
	}

	forgottenKey := core.ChunkKey{
		Dimension: core.Overworld,
		Pos:       core.ChunkPos{},
	}
	running.session.publications[forgottenKey] = &publication{snapshotSent: true}
	player, ok := running.engine.Player(localSessionID)
	if !ok {
		t.Fatal("本地玩家不存在")
	}
	forgetTick := changed.Tick + 1
	running.publish(sim.TickResult{
		Tick: forgetTick,
		Forget: map[sim.SessionID][]core.ChunkKey{
			localSessionID: {forgottenKey},
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
	running := New(config, endpoint, playerTestGenerator{})
	t.Cleanup(running.Close)
	return running
}

func newTrustedObserverTestServer(t *testing.T) *Server {
	t.Helper()
	_, endpoint := network.NewMemoryPair(32)
	config := DefaultConfig(42)
	config.ViewRadius = 0
	config.Workers = 1
	config.TrustedObserver = true
	running := New(config, endpoint, playerTestGenerator{})
	t.Cleanup(running.Close)
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
