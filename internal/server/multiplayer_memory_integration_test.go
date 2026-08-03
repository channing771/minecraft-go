package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/client"
	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/storage"
	"minecraft-go/internal/world"
)

const multiplayerClientCount = 8

var multiplayerNames = [...]string{"阿明", "Builder", "小麦", "Redstone", "星河", "Miner", "方块", "Alex"}

var multiplayerManualTarget = core.BlockPos{X: 0, Y: 1, Z: 6}

type multiplayerManualGenerator struct{}

func (multiplayerManualGenerator) GenerateChunk(position core.ChunkPos) *world.Chunk {
	chunk := flatTestGenerator{}.GenerateChunk(position)
	if multiplayerManualTarget.Chunk() == position {
		_, _, z := multiplayerManualTarget.Local()
		for x := 0; x < core.SectionSize; x++ {
			for depth := 0; z+depth < core.SectionSize; depth++ {
				chunk.SetBlock(x, multiplayerManualTarget.Y, z+depth, core.StoneID)
			}
		}
		chunk.Compact()
	}
	return chunk
}

type multiplayerScriptStep struct {
	Tick   uint64
	Player int
	Input  *network.PlayerInput
	Break  *network.BreakBlock
	Place  *network.PlaceBlock
}

type multiplayerRunResult struct {
	Transcript     [32]byte
	PlayerHashes   [multiplayerClientCount][32]byte
	ChunkHash      [32]byte
	ChunkRevision  uint64
	MirrorHashes   [multiplayerClientCount][32]byte
	MirrorRevision [multiplayerClientCount]uint64
}

type trackedMemoryStore struct {
	*storage.MemoryStore
	inFlight atomic.Int64
}

type delayedTask16AcceptListener struct {
	stream    network.ServerPacketStream
	returned  chan struct{}
	release   chan struct{}
	closeOnce sync.Once
}

func (listener *delayedTask16AcceptListener) Accept(context.Context) (network.ServerPacketStream, error) {
	close(listener.returned)
	<-listener.release
	return listener.stream, nil
}

func (*delayedTask16AcceptListener) Addr() string { return "delayed-task16" }

func (listener *delayedTask16AcceptListener) Close() error {
	listener.closeOnce.Do(func() { close(listener.release) })
	return nil
}

func (store *trackedMemoryStore) LoadChunk(ctx context.Context, key core.ChunkKey) (storage.StoredChunk, error) {
	store.inFlight.Add(1)
	defer store.inFlight.Add(-1)
	return store.MemoryStore.LoadChunk(ctx, key)
}

func TestEightMemorySessionsAreDeterministicFor2000Ticks(t *testing.T) {
	runEightMemorySessionsDeterminism(t, 2000)
}

func TestEightPlayerMemoryTCPParity(t *testing.T) {
	runEightPlayerMemoryTCPParity(t, 200)
}

func TestCanceledMultiplayerPacketAcceptClosesLateStreamAndJoinsWorker(t *testing.T) {
	clientStream, serverStream := network.NewMemoryStreamPair(1)
	t.Cleanup(func() { _ = clientStream.Close() })
	listener := &delayedTask16AcceptListener{
		stream: serverStream, returned: make(chan struct{}), release: make(chan struct{}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	worker := startMultiplayerPacketAccept(ctx, listener)
	<-listener.returned
	cancel()

	accepted, err := awaitMultiplayerPacketAccept(ctx, listener, worker)
	if accepted != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled accept=(%T, %v), want (nil, context.Canceled)", accepted, err)
	}
	select {
	case <-worker.done:
	default:
		t.Fatal("canceled accept worker was not joined before return")
	}
	if err := clientStream.Send(context.Background(), network.StatePlay, network.PlayerInput{}); !errors.Is(err, network.ErrClosed) {
		t.Fatalf("late accepted stream remained live: Send=%v, want network.ErrClosed", err)
	}
}

func fixedEightPlayerScript(ticks uint64) []multiplayerScriptStep {
	steps := make([]multiplayerScriptStep, 0, int(ticks)*multiplayerClientCount+int(ticks/20))
	for tick := uint64(1); tick <= ticks; tick++ {
		sign := int8(1)
		if ((tick - 1) / 10 % 2) != 0 {
			sign = -1
		}
		for player := 0; player < multiplayerClientCount; player++ {
			input := network.PlayerInput{Sequence: tick*2 - 1, Yaw: 0, Pitch: -0.2}
			switch player % 4 {
			case 0:
				input.MoveX = sign
			case 1:
				input.MoveX = -sign
			case 2:
				input.MoveZ = sign
			case 3:
				input.MoveZ = -sign
			}
			steps = append(steps, multiplayerScriptStep{Tick: tick, Player: player, Input: &input})
		}
		if tick%20 == 0 {
			player := int((tick/20 - 1) % multiplayerClientCount)
			if (tick/20)%2 == 1 {
				command := network.BreakBlock{Sequence: tick * 2, Yaw: math.Pi, Pitch: -0.2}
				steps = append(steps, multiplayerScriptStep{Tick: tick, Player: player, Break: &command})
			} else {
				command := network.PlaceBlock{Sequence: tick * 2, Yaw: math.Pi, Pitch: -0.2, Slot: 0}
				steps = append(steps, multiplayerScriptStep{Tick: tick, Player: player, Place: &command})
			}
		}
	}
	return steps
}

func canonicalMultiplayerTranscript(events []multiplayerEvent) [32]byte {
	hash := sha256.New()
	var baseTick uint64
	for _, event := range events {
		if state, ok := event.message.(network.PlayerState); ok {
			baseTick = state.ServerTick - 1
			break
		}
	}
	for _, event := range events {
		canonicalMultiplayerEvent(hash, event.message, baseTick)
	}
	var result [32]byte
	copy(result[:], hash.Sum(nil))
	return result
}

func canonicalMultiplayerEvent(output interface{ Write([]byte) (int, error) }, value network.ServerMessage, baseTick uint64) {
	write := func(format string, args ...any) { _, _ = fmt.Fprintf(output, format+"\n", args...) }
	switch message := value.(type) {
	case network.PlayerState:
		write("PlayerState|tick=%d|sequence=%d|dimension=%d|position=%08x,%08x,%08x|velocity=%08x,%08x,%08x|yaw=%08x|pitch=%08x|ground=%t|ready=%t|reset=%t",
			message.ServerTick-baseTick, message.LastInputSequence, message.Dimension,
			math.Float32bits(message.Position[0]), math.Float32bits(message.Position[1]), math.Float32bits(message.Position[2]),
			math.Float32bits(message.Velocity[0]), math.Float32bits(message.Velocity[1]), math.Float32bits(message.Velocity[2]),
			math.Float32bits(message.Yaw), math.Float32bits(message.Pitch), message.OnGround, message.Ready, message.Reset)
	case network.RemotePlayerSpawn:
		write("RemotePlayerSpawn|tick=%d|player=%s|name=%q|dimension=%d|position=%08x,%08x,%08x|yaw=%08x|pitch=%08x",
			message.ServerTick-baseTick, message.PlayerID, message.DisplayName, message.Dimension,
			math.Float32bits(message.Position[0]), math.Float32bits(message.Position[1]), math.Float32bits(message.Position[2]),
			math.Float32bits(message.Yaw), math.Float32bits(message.Pitch))
	case network.RemotePlayerStates:
		write("RemotePlayerStates|tick=%d|count=%d", message.ServerTick-baseTick, len(message.Players))
		for _, state := range message.Players {
			write("RemotePlayerState|tick=%d|player=%s|dimension=%d|position=%08x,%08x,%08x|yaw=%08x|pitch=%08x|reset=%t",
				message.ServerTick-baseTick, state.PlayerID, state.Dimension,
				math.Float32bits(state.Position[0]), math.Float32bits(state.Position[1]), math.Float32bits(state.Position[2]),
				math.Float32bits(state.Yaw), math.Float32bits(state.Pitch), state.Reset)
		}
	case network.RemotePlayerDespawn:
		write("RemotePlayerDespawn|player=%s", message.PlayerID)
	case network.ChunkSnapshot:
		write("ChunkSnapshot|dimension=%d|chunk=%d,%d|revision=%d", message.Dimension, message.Chunk.X, message.Chunk.Z, message.Revision)
		for _, section := range message.Sections {
			write("Section|y=%d|storage=%d|single=%d|palette=%v|packed=%v", section.Y, section.Storage, section.Single, section.Palette, section.Packed)
		}
	case network.BlockChanges:
		write("BlockChanges|dimension=%d|chunk=%d,%d|revision=%d>%d|count=%d", message.Dimension, message.Chunk.X, message.Chunk.Z, message.BaseRevision, message.NewRevision, len(message.Changes))
		for _, change := range message.Changes {
			write("BlockChange|position=%d,%d,%d|block=%d", change.Position.X, change.Position.Y, change.Position.Z, change.Block)
		}
	case network.ForgetChunks:
		write("ForgetChunks|dimension=%d|chunks=%v", message.Dimension, message.Chunks)
	case network.CommandRejected:
		write("CommandRejected|sequence=%d|reason=%s", message.Sequence, message.Reason)
	case network.ItemDropUpserts, network.ItemDropRemoves:
		// 掉落物差分按连接批次交付，边界批次会随传输而变；
		// Memory/TCP 一致性由最终镜像状态覆盖，不进入逐消息 transcript。
	case network.HotbarState:
		write("HotbarState|selected=%d", message.Hotbar.Selected)
		for index, stack := range message.Hotbar.Slots {
			write("HotbarSlot|index=%d|item=%d|count=%d", index, stack.Item, stack.Count)
		}
	case network.KeepAlive:
		// Tokens come from runtime heartbeat timing and are intentionally excluded.
	case network.Disconnect:
		write("Disconnect|code=%d|message=%q", message.Code, message.Message)
	default:
		write("Unknown|type=%T|value=%+v", value, value)
	}
}

func runEightMemorySessionsDeterminism(t *testing.T, ticks uint64) {
	t.Helper()
	first := runEightManualMultiplayer(t, "direct-memory", ticks)
	second := runEightManualMultiplayer(t, "direct-memory", ticks)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("same seed/script diverged\nfirst=%+v\nsecond=%+v", first, second)
	}
}

func runEightPlayerMemoryTCPParity(t *testing.T, ticks uint64) {
	t.Helper()
	memory := runEightManualMultiplayer(t, "login-memory", ticks)
	tcp := runEightManualMultiplayer(t, "login-tcp", ticks)
	if !reflect.DeepEqual(memory, tcp) {
		t.Fatalf("memory/TCP business results differ\nmemory=%+v\ntcp=%+v", memory, tcp)
	}
}

func runEightManualMultiplayer(t *testing.T, transport string, ticks uint64) multiplayerRunResult {
	t.Helper()
	const seed int64 = 160016
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{}}
	memory := storage.NewMemory(storage.Metadata{FormatVersion: 1, Seed: seed, SpawnDimension: core.Overworld})
	if _, err := memory.SaveBatch(context.Background(), []storage.ChunkSave{{Key: key, Revision: 1, Chunk: multiplayerManualGenerator{}.GenerateChunk(key.Pos)}}); err != nil {
		t.Fatalf("seed initial wanted union: %v", err)
	}
	tracked := &trackedMemoryStore{MemoryStore: memory}
	config := hostTestConfig()
	config.Seed = seed
	config.MaxPlayers = multiplayerClientCount
	config.ViewRadius = 0
	config.Workers = 1
	config.OutboxCapacity = 4096
	config.HeartbeatInterval = time.Hour
	config.HeartbeatTimeout = time.Hour
	clients := make([]*multiplayerTCPClient, multiplayerClientCount)
	exits := make([]<-chan SessionExit, multiplayerClientCount)
	transportClosers := make([]func() error, 0, multiplayerClientCount)
	running := NewWorld(config, multiplayerManualGenerator{}, tracked)
	var cleanupOnce sync.Once
	var cleanupErr error
	cleanup := func() error {
		cleanupOnce.Do(func() {
			cleanupErr = cleanupEightManualMultiplayer(running, clients, exits, transportClosers)
		})
		return cleanupErr
	}
	t.Cleanup(func() {
		if err := cleanup(); err != nil {
			t.Errorf("cleanup %s multiplayer: %v", transport, err)
		}
	})
	for index := range clients {
		identity := multiplayerIdentity(byte(0x80+index), multiplayerNames[index])
		var endpoint network.ClientEndpoint
		var exit <-chan SessionExit
		switch transport {
		case "direct-memory":
			clientEndpoint, serverEndpoint := network.NewMemoryPair(4096)
			var err error
			exit, err = running.AttachSession(multiplayerSessionSpec(index, identity, serverEndpoint))
			if err != nil {
				_ = clientEndpoint.Close()
				_ = serverEndpoint.Close()
				t.Fatalf("AttachSession player %d: %v", index, err)
			}
			endpoint = clientEndpoint
		case "login-memory", "login-tcp":
			loginCtx, loginCancel := context.WithTimeout(context.Background(), 5*time.Second)
			clientStream, serverStream, closeTransport := multiplayerPacketStreams(t, loginCtx, transport)
			transportClosers = append(transportClosers, closeTransport)
			serverDone := make(chan error, 1)
			go func(index int, identity network.Identity) {
				pending, err := network.BeginServerLogin(loginCtx, serverStream)
				if err == nil && pending.Identity() != identity {
					err = fmt.Errorf("identity=%+v, want %+v", pending.Identity(), identity)
				}
				if err == nil {
					err = pending.Accept(loginCtx, func(serverEndpoint network.ServerEndpoint) error {
						var attachErr error
						exit, attachErr = running.AttachSession(multiplayerSessionSpec(index, identity, serverEndpoint))
						return attachErr
					})
				}
				serverDone <- err
			}(index, identity)
			var loginErr error
			endpoint, loginErr = network.LoginClient(loginCtx, clientStream, identity)
			var serverErr error
			select {
			case serverErr = <-serverDone:
			case <-loginCtx.Done():
				serverErr = fmt.Errorf("server login result: %w", loginCtx.Err())
				_ = closeTransport()
			}
			loginCancel()
			if loginErr != nil {
				t.Fatalf("%s LoginClient player %d: %v", transport, index, loginErr)
			}
			if serverErr != nil {
				t.Fatalf("%s server login player %d: %v", transport, index, serverErr)
			}
		default:
			t.Fatalf("unknown transport %q", transport)
		}
		clients[index] = &multiplayerTCPClient{
			identity: identity, endpoint: endpoint, receiver: client.NewReceiver(endpoint, 4096),
			mirror: client.NewMirror(), remotes: client.NewRemotePlayers(),
		}
		connected := clients[index]
		t.Cleanup(func() {
			if err := closeTask16Client(connected); err != nil {
				t.Errorf("cleanup %s client %s: %v", transport, connected.identity.DisplayName, err)
			}
		})
		exits[index] = exit
	}

	warmCtx, warmCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer warmCancel()
	for {
		result := running.StepForTest()
		drainMultiplayerClientsToTick(t, warmCtx, transport, clients, result.Tick)
		if manualMultiplayerStable(running, tracked, clients, key) {
			result = running.StepForTest()
			drainMultiplayerClientsToTick(t, warmCtx, transport, clients, result.Tick)
			if manualMultiplayerStable(running, tracked, clients, key) {
				break
			}
		}
		if err := warmCtx.Err(); err != nil {
			t.Fatalf("%s warm-up: %v\n%s", transport, err, multiplayerDiagnosticsMany(clients))
		}
		runtime.Gosched()
	}
	for _, connected := range clients {
		connected.transcript = nil
	}

	steps := fixedEightPlayerScript(ticks)
	stepIndex := 0
	initialChunk := key.Pos
	measurementStartTick := running.TickCount()
	for tick := uint64(1); tick <= ticks; tick++ {
		if got := len(running.incoming); got != 0 {
			t.Fatalf("%s tick %d starts with incoming=%d\n%s", transport, tick, got, multiplayerDiagnosticsMany(clients))
		}
		expected := 0
		pendingSequences := make([]string, 0, 9)
		for stepIndex < len(steps) && steps[stepIndex].Tick == tick {
			step := steps[stepIndex]
			var message network.ClientMessage
			switch {
			case step.Input != nil:
				message = *step.Input
				pendingSequences = append(pendingSequences, fmt.Sprintf("p%d/input/%d", step.Player, step.Input.Sequence))
			case step.Break != nil:
				if multiplayerManualTarget.Chunk() != initialChunk {
					t.Fatalf("break target escaped initial chunk: %+v", multiplayerManualTarget)
				}
				message = *step.Break
				pendingSequences = append(pendingSequences, fmt.Sprintf("p%d/break/%d", step.Player, step.Break.Sequence))
			case step.Place != nil:
				if multiplayerManualTarget.Chunk() != initialChunk {
					t.Fatalf("place target escaped initial chunk: %+v", multiplayerManualTarget)
				}
				message = *step.Place
				pendingSequences = append(pendingSequences, fmt.Sprintf("p%d/place/%d", step.Player, step.Place.Sequence))
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			err := clients[step.Player].endpoint.Send(ctx, message)
			cancel()
			if err != nil {
				t.Fatalf("%s tick %d player %d Send(%T): %v", transport, tick, step.Player, message, err)
			}
			expected++
			stepIndex++
		}
		barrierCtx, barrierCancel := context.WithTimeout(context.Background(), 3*time.Second)
		for len(running.incoming) != expected && barrierCtx.Err() == nil {
			runtime.Gosched()
		}
		barrierErr := barrierCtx.Err()
		barrierCancel()
		if barrierErr != nil {
			t.Fatalf("%s ingress barrier tick=%d expected=%d actual=%d pending=%v: %v\n%s",
				transport, tick, expected, len(running.incoming), pendingSequences, barrierErr, multiplayerDiagnosticsMany(clients))
		}
		result := running.StepForTest()
		if want := measurementStartTick + tick; result.Tick != want {
			t.Fatalf("%s measurement tick %d produced raw ServerTick=%d, want %d", transport, tick, result.Tick, want)
		}
		drainCtx, drainCancel := context.WithTimeout(context.Background(), 3*time.Second)
		drainMultiplayerClientsToTick(t, drainCtx, transport, clients, result.Tick)
		drainCancel()
		if got := len(running.incoming); got != 0 {
			t.Fatalf("%s tick %d ends with incoming=%d\n%s", transport, tick, got, multiplayerDiagnosticsMany(clients))
		}
		for index, connected := range clients {
			foot := vec3BlockPos(connected.local.Position).Chunk()
			if foot != initialChunk {
				t.Fatalf("%s tick %d player %d foot chunk=%+v, want %+v; position=%v", transport, tick, index, foot, initialChunk, connected.local.Position)
			}
		}
	}

	var outcome multiplayerRunResult
	combined := sha256.New()
	for index, connected := range clients {
		assertRawBusinessTicks(t, transport, index, connected.transcript, measurementStartTick, ticks)
		_, _ = combined.Write([]byte{byte(index)})
		digest := canonicalMultiplayerTranscript(connected.transcript)
		_, _ = combined.Write(digest[:])
		snapshot, ok := running.PlayerSnapshotFor(sim.SessionID(index + 1))
		if !ok {
			t.Fatalf("%s player %d snapshot unavailable", transport, index)
		}
		outcome.PlayerHashes[index] = snapshotDigest(snapshot)
		mirrorHash, revision, ok := connected.mirror.Hash(key.Dimension, key.Pos)
		if !ok {
			t.Fatalf("%s player %d mirror hash unavailable", transport, index)
		}
		outcome.MirrorHashes[index], outcome.MirrorRevision[index] = mirrorHash, revision
	}
	copy(outcome.Transcript[:], combined.Sum(nil))
	var ok bool
	outcome.ChunkHash, outcome.ChunkRevision, ok = running.ChunkHash(key.Dimension, key.Pos)
	if !ok {
		t.Fatalf("%s authoritative chunk hash unavailable", transport)
	}
	for index := 1; index < multiplayerClientCount; index++ {
		if outcome.MirrorHashes[index] != outcome.MirrorHashes[0] || outcome.MirrorRevision[index] != outcome.MirrorRevision[0] {
			t.Fatalf("%s mirrors did not converge: hashes=%x revisions=%v", transport, outcome.MirrorHashes, outcome.MirrorRevision)
		}
	}
	if outcome.MirrorHashes[0] != outcome.ChunkHash || outcome.MirrorRevision[0] != outcome.ChunkRevision {
		t.Fatalf("%s mirror/authority mismatch mirror=%x/%d authority=%x/%d", transport, outcome.MirrorHashes[0], outcome.MirrorRevision[0], outcome.ChunkHash, outcome.ChunkRevision)
	}

	if err := cleanup(); err != nil {
		t.Fatalf("cleanup %s multiplayer: %v", transport, err)
	}
	return outcome
}

func cleanupEightManualMultiplayer(
	running *Server,
	clients []*multiplayerTCPClient,
	exits []<-chan SessionExit,
	transportClosers []func() error,
) error {
	var cleanupErr error
	for index, exit := range exits {
		if exit == nil {
			continue
		}
		select {
		case result := <-exit:
			exits[index] = nil
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf(
				"player %d exited before cleanup: SessionExit cause=%v", index, result.Err,
			))
		default:
		}
	}
	for _, connected := range clients {
		if err := closeTask16Client(connected); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("close client %s: %w", connected.identity.DisplayName, err))
		}
	}
	for _, closeTransport := range transportClosers {
		if err := closeTransport(); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("close transport: %w", err))
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := running.Shutdown(ctx); err != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("Server.Shutdown: %w", err))
	}
	for index, exit := range exits {
		if exit == nil {
			continue
		}
		select {
		case result := <-exit:
			if result.Err != nil && !errors.Is(result.Err, network.ErrClosed) {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("player %d exit: %w", index, result.Err))
			}
		case <-ctx.Done():
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("player %d exit result: %w", index, ctx.Err()))
		}
	}
	return cleanupErr
}

func assertRawBusinessTicks(
	t *testing.T,
	transport string,
	player int,
	events []multiplayerEvent,
	measurementStartTick uint64,
	ticks uint64,
) {
	t.Helper()
	var localCount, remoteCount uint64
	for _, event := range events {
		switch message := event.message.(type) {
		case network.PlayerState:
			localCount++
			if want := measurementStartTick + localCount; message.ServerTick != want {
				t.Fatalf("%s player %d local raw ServerTick=%d at relative=%d, want %d",
					transport, player, message.ServerTick, localCount, want)
			}
		case network.RemotePlayerStates:
			remoteCount++
			if want := measurementStartTick + remoteCount; message.ServerTick != want {
				t.Fatalf("%s player %d remote raw ServerTick=%d at relative=%d, want %d",
					transport, player, message.ServerTick, remoteCount, want)
			}
		}
	}
	if localCount != ticks || remoteCount != ticks {
		t.Fatalf("%s player %d raw business tick counts local=%d remote=%d, want %d/%d",
			transport, player, localCount, remoteCount, ticks, ticks)
	}
}

func multiplayerSessionSpec(index int, identity network.Identity, endpoint network.ServerEndpoint) SessionSpec {
	location := sim.PlayerLocation{Dimension: core.Overworld, Position: mgl32.Vec3{0.5, 1.001, 0.5}}
	return SessionSpec{
		ID: sim.SessionID(index + 1), Generation: 1,
		PlayerID: identity.PlayerID, DisplayName: identity.DisplayName, Endpoint: endpoint,
		Restore: sim.PlayerRestore{Current: &location, Safe: &location, SpawnDimension: core.Overworld},
	}
}

type multiplayerPacketAcceptResult struct {
	stream network.ServerPacketStream
	err    error
}

type multiplayerPacketAcceptWorker struct {
	cancel context.CancelFunc
	result <-chan multiplayerPacketAcceptResult
	done   <-chan struct{}
}

func startMultiplayerPacketAccept(
	ctx context.Context,
	listener network.Listener,
) multiplayerPacketAcceptWorker {
	acceptCtx, cancel := context.WithCancel(ctx)
	results := make(chan multiplayerPacketAcceptResult, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		stream, err := listener.Accept(acceptCtx)
		results <- multiplayerPacketAcceptResult{stream: stream, err: err}
	}()
	return multiplayerPacketAcceptWorker{cancel: cancel, result: results, done: done}
}

func awaitMultiplayerPacketAccept(
	ctx context.Context,
	listener network.Listener,
	worker multiplayerPacketAcceptWorker,
) (network.ServerPacketStream, error) {
	select {
	case result := <-worker.result:
		worker.cancel()
		if err := waitMultiplayerPacketAcceptWorker(worker.done); err != nil {
			if result.stream != nil {
				_ = result.stream.Close()
			}
			return nil, errors.Join(result.err, err)
		}
		return result.stream, result.err
	case <-ctx.Done():
		return nil, stopMultiplayerPacketAccept(listener, worker, ctx.Err())
	}
}

func stopMultiplayerPacketAccept(
	listener network.Listener,
	worker multiplayerPacketAcceptWorker,
	cause error,
) error {
	worker.cancel()
	closeErr := listener.Close()
	joinCtx, joinCancel := context.WithTimeout(context.Background(), time.Second)
	defer joinCancel()
	var result multiplayerPacketAcceptResult
	select {
	case result = <-worker.result:
	case <-joinCtx.Done():
		return errors.Join(cause, closeErr, fmt.Errorf("accept result join: %w", joinCtx.Err()))
	}
	if result.stream != nil {
		closeErr = errors.Join(closeErr, result.stream.Close())
	}
	select {
	case <-worker.done:
	case <-joinCtx.Done():
		return errors.Join(cause, closeErr, result.err, fmt.Errorf("accept worker join: %w", joinCtx.Err()))
	}
	if errors.Is(result.err, network.ErrClosed) || errors.Is(result.err, context.Canceled) {
		result.err = nil
	}
	return errors.Join(cause, closeErr, result.err)
}

func waitMultiplayerPacketAcceptWorker(done <-chan struct{}) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("accept worker join: %w", ctx.Err())
	}
}

func multiplayerPacketStreams(
	t *testing.T,
	ctx context.Context,
	transport string,
) (network.ClientPacketStream, network.ServerPacketStream, func() error) {
	t.Helper()
	if transport == "login-memory" {
		clientStream, serverStream := network.NewMemoryStreamPair(4096)
		var closeOnce sync.Once
		var closeErr error
		closeTransport := func() error {
			closeOnce.Do(func() {
				closeErr = errors.Join(clientStream.Close(), serverStream.Close())
			})
			return closeErr
		}
		t.Cleanup(func() {
			if err := closeTransport(); err != nil {
				t.Errorf("cleanup memory packet streams: %v", err)
			}
		})
		return clientStream, serverStream, closeTransport
	}
	listener, err := network.ListenTCP("127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenTCP: %v", err)
	}
	var clientStream network.ClientPacketStream
	var serverStream network.ServerPacketStream
	var closeOnce sync.Once
	var closeErr error
	closeTransport := func() error {
		closeOnce.Do(func() {
			if clientStream != nil {
				closeErr = errors.Join(closeErr, clientStream.Close())
			}
			if serverStream != nil {
				closeErr = errors.Join(closeErr, serverStream.Close())
			}
			closeErr = errors.Join(closeErr, listener.Close())
		})
		return closeErr
	}
	t.Cleanup(func() {
		if err := closeTransport(); err != nil {
			t.Errorf("cleanup TCP packet streams: %v", err)
		}
	})
	acceptWorker := startMultiplayerPacketAccept(ctx, listener)
	clientStream, err = network.DialTCP(ctx, listener.Addr())
	if err != nil {
		acceptErr := stopMultiplayerPacketAccept(listener, acceptWorker, err)
		_ = closeTransport()
		t.Fatalf("DialTCP: %v", acceptErr)
	}
	serverStream, err = awaitMultiplayerPacketAccept(ctx, listener, acceptWorker)
	if err != nil {
		_ = closeTransport()
		t.Fatalf("Accept TCP: %v", err)
	}
	return clientStream, serverStream, closeTransport
}

func manualMultiplayerStable(running *Server, store *trackedMemoryStore, clients []*multiplayerTCPClient, key core.ChunkKey) bool {
	running.stepMu.Lock()
	info, ready := running.engine.ChunkInfo(key)
	queuesEmpty := len(running.pending) == 0 && len(running.jobs) == 0 && len(running.acquired) == 0 &&
		len(running.generated) == 0 && len(running.incoming) == 0 && len(running.queued) == 0
	allPlayersReady := true
	for index := range clients {
		update, ok := running.engine.Player(sim.SessionID(index + 1))
		allPlayersReady = allPlayersReady && ok && update.Ready
	}
	running.stepMu.Unlock()
	if !ready || info.State != sim.ChunkReady || !queuesEmpty || !allPlayersReady || store.inFlight.Load() != 0 {
		return false
	}
	for _, connected := range clients {
		if !connected.readyWithFootSnapshot() {
			return false
		}
	}
	return true
}

func drainMultiplayerClientsToTick(t *testing.T, ctx context.Context, transport string, clients []*multiplayerTCPClient, tick uint64) {
	t.Helper()
	for {
		complete := true
		progressed := false
		for index, connected := range clients {
			for connected.local.ServerTick < tick {
				got, err := drainOneTask16(connected)
				if err != nil {
					t.Fatalf("%s tick %d drain player %d: %v\n%s", transport, tick, index, err, multiplayerDiagnosticsMany(clients))
				}
				if !got {
					complete = false
					break
				}
				progressed = true
			}
			if connected.local.ServerTick < tick {
				complete = false
			}
		}
		if complete {
			return
		}
		if err := ctx.Err(); err != nil {
			t.Fatalf("%s drain to tick %d: %v\n%s", transport, tick, err, multiplayerDiagnosticsMany(clients))
		}
		if !progressed {
			runtime.Gosched()
		}
	}
}

func multiplayerDiagnosticsMany(clients []*multiplayerTCPClient) string {
	var output bytes.Buffer
	for index, connected := range clients {
		fmt.Fprintf(&output, "player=%d %s localTick=%d sequence=%d ready=%t roster=%d events=%d\n",
			index, connected.identity.DisplayName, connected.local.ServerTick,
			connected.local.LastInputSequence, connected.local.Ready,
			len(connected.remotes.Presentations()), len(connected.transcript))
		start := max(0, len(connected.transcript)-8)
		for _, event := range connected.transcript[start:] {
			fmt.Fprintf(&output, "  %s\n", multiplayerEventSummary(event.message))
		}
	}
	return output.String()
}

func snapshotDigest(snapshot sim.PlayerSnapshot) [32]byte {
	var encoded bytes.Buffer
	_ = binary.Write(&encoded, binary.LittleEndian, int32(snapshot.Current.Dimension))
	for _, value := range snapshot.Current.Position {
		_ = binary.Write(&encoded, binary.LittleEndian, math.Float32bits(value))
	}
	_ = binary.Write(&encoded, binary.LittleEndian, math.Float32bits(snapshot.Yaw))
	_ = binary.Write(&encoded, binary.LittleEndian, math.Float32bits(snapshot.Pitch))
	if snapshot.Safe == nil {
		encoded.WriteByte(0)
	} else {
		encoded.WriteByte(1)
		_ = binary.Write(&encoded, binary.LittleEndian, int32(snapshot.Safe.Dimension))
		for _, value := range snapshot.Safe.Position {
			_ = binary.Write(&encoded, binary.LittleEndian, math.Float32bits(value))
		}
	}
	return sha256.Sum256(encoded.Bytes())
}
