package server

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/client"
	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
)

type multiplayerTCPHost struct {
	Host *Host
	Addr string
	Done <-chan error
}

type multiplayerEvent struct {
	message network.ServerMessage
}

type multiplayerTCPClient struct {
	identity   network.Identity
	endpoint   network.ClientEndpoint
	receiver   *client.Receiver
	mirror     *client.Mirror
	remotes    *client.RemotePlayers
	local      network.PlayerState
	transcript []multiplayerEvent
	closed     bool
}

func TestMultiplayerTCPClientsSeeMoveEditAndDespawn(t *testing.T) {
	deadline, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	host := startMultiplayerTCPHost(t)
	var a, b *multiplayerTCPClient
	cleanupMultiplayerTCPTest(t, host, &a, &b)
	aIdentity := multiplayerIdentity(0xa1, "阿明")
	bIdentity := multiplayerIdentity(0xb2, "Builder")
	a = mustConnectMultiplayerTCPClient(t, deadline, host.Addr, aIdentity)
	b = mustConnectMultiplayerTCPClient(t, deadline, host.Addr, bIdentity)

	// Kills a driver that declares ready before applying the initial snapshot,
	// or a server that marks a login ready without sending the foot chunk.
	mustDrainMultiplayer(t, deadline, a, b, "both clients ready with foot snapshots", func() bool {
		return a.readyWithFootSnapshot() && b.readyWithFootSnapshot()
	})

	// Kills Spawn-before-snapshot ordering, duplicate Spawn, swapped identities,
	// and a driver that invents roster entries instead of consuming TCP messages.
	mustDrainMultiplayer(t, deadline, a, b, "each client sees the other after its snapshot", func() bool {
		return a.sawSingleSpawnAfterFootSnapshot(bIdentity) &&
			b.sawSingleSpawnAfterFootSnapshot(aIdentity)
	})
	// Kills a fixture that silently changes the fixed interaction block or starts
	// either mirror at a non-authoritative revision. Revision 1 is the snapshot.
	assertMirrorBlockAndRevision(t, a, playerIntegrationObstacle, core.StoneID, 1)
	assertMirrorBlockAndRevision(t, b, playerIntegrationObstacle, core.StoneID, 1)

	aStart := a.local.Position
	for sequence := uint64(1); sequence <= 8; sequence++ {
		mustSendMultiplayer(t, deadline, a, network.PlayerInput{
			Sequence: sequence,
			MoveZ:    1,
			Yaw:      0,
			Pitch:    -0.2,
		})
	}
	// Kills coalescing that reports stale/non-increasing ticks, movement that is
	// never replicated, and a driver that mistakes local PlayerState for remote state.
	mustDrainMultiplayer(t, deadline, a, b, "B sees A move on increasing ticks", func() bool {
		return b.sawRemoteMoveOnIncreasingTicks(aIdentity.PlayerID, aStart)
	})

	target := core.BlockPos{X: 0, Y: 1, Z: -6}
	mustSendMultiplayer(t, deadline, a, network.BreakBlock{
		Sequence: 9,
		Yaw:      0,
		Pitch:    -0.2,
	})
	// Kills one-sided block fanout, incorrect block IDs, revision-only equality,
	// hash-only equality, and a driver that compares a mirror with itself.
	mustDrainMultiplayer(t, deadline, a, b, "both mirrors converge after break", func() bool {
		return mirrorsConvergedAt(a, b, target, core.AirID, 2)
	})

	mustCloseMultiplayerTCPClient(t, b)
	// Kills a missing or duplicate Despawn. The later post-disconnect local state
	// provides a stream marker after which the terminal ordering is checked again.
	mustDrainMultiplayer(t, deadline, a, b, "A sees exactly one terminal B despawn", func() bool {
		return a.sawExactlyOneTerminalDespawn(bIdentity.PlayerID)
	})

	beforeContinue := a.local
	mustSendMultiplayer(t, deadline, a, network.PlayerInput{
		Sequence: 10,
		MoveX:    1,
		Yaw:      0,
		Pitch:    -0.2,
	})
	// Kills a disconnect path that stops the shared world/session, and a driver
	// that accepts an old local state instead of a post-disconnect acknowledgement.
	mustDrainMultiplayer(t, deadline, a, b, "A world continues after B disconnects", func() bool {
		return a.local.LastInputSequence >= 10 &&
			a.local.ServerTick > beforeContinue.ServerTick &&
			a.local.Position != beforeContinue.Position
	})
	// Kills a server that resumes broadcasting B after Despawn, or emits a second
	// Despawn while A's session continues to consume later world ticks.
	if !a.sawExactlyOneTerminalDespawn(bIdentity.PlayerID) {
		t.Fatalf("B did not have one terminal Despawn after A continued\n%s", multiplayerDiagnostics(a, b))
	}
}

func startMultiplayerTCPHost(t *testing.T) multiplayerTCPHost {
	t.Helper()
	listener, err := network.ListenTCP("127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenTCP loopback: %v", err)
	}
	config := hostTestConfig()
	config.MaxPlayers = 2
	config.ViewRadius = 1
	config.OutboxCapacity = 512
	config.ShutdownTimeout = 5 * time.Second
	host := NewHost(config, flatTestGenerator{}, newHostTestStore())
	done := make(chan error, 1)
	go func() { done <- host.Run(context.Background(), listener) }()
	return multiplayerTCPHost{Host: host, Addr: listener.Addr(), Done: done}
}

func multiplayerIdentity(last byte, displayName string) network.Identity {
	return network.Identity{
		PlayerID: core.PlayerID{
			0x31, 0x52, 0x73, 0x94, 0xb5, 0xd6, 0x47, 0xf8,
			0x89, 0xaa, 0xcb, 0xec, 0x0d, 0x2e, 0x4f, last,
		},
		DisplayName: displayName,
	}
}

func connectMultiplayerTCPClient(
	ctx context.Context,
	address string,
	identity network.Identity,
) (*multiplayerTCPClient, error) {
	stream, err := network.DialTCP(ctx, address)
	if err != nil {
		return nil, fmt.Errorf("DialTCP %s: %w", identity.DisplayName, err)
	}
	endpoint, err := network.LoginClient(ctx, stream, identity)
	if err != nil {
		_ = stream.Close()
		return nil, fmt.Errorf("LoginClient %s: %w", identity.DisplayName, err)
	}
	return &multiplayerTCPClient{
		identity: identity,
		endpoint: endpoint,
		receiver: client.NewReceiver(endpoint, 1024),
		mirror:   client.NewMirror(),
		remotes:  client.NewRemotePlayers(),
	}, nil
}

func mustConnectMultiplayerTCPClient(
	t *testing.T,
	ctx context.Context,
	address string,
	identity network.Identity,
) *multiplayerTCPClient {
	t.Helper()
	connected, err := connectMultiplayerTCPClient(ctx, address, identity)
	if err != nil {
		t.Fatal(err)
	}
	return connected
}

func (connected *multiplayerTCPClient) drainUntil(ctx context.Context, done func() bool) error {
	for !done() {
		progressed, err := connected.drainOne()
		if err != nil {
			return err
		}
		if progressed {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			runtime.Gosched()
		}
	}
	return nil
}

func (connected *multiplayerTCPClient) drainOne() (bool, error) {
	message, ok := connected.receiver.TryRecv()
	if !ok {
		if err := connected.receiver.Err(); err != nil {
			return false, err
		}
		return false, nil
	}
	connected.transcript = append(connected.transcript, multiplayerEvent{message: message})
	switch message := message.(type) {
	case network.PlayerState:
		connected.local = message
	case network.ChunkSnapshot, network.BlockChanges, network.ForgetChunks, network.CommandRejected:
		update, err := connected.mirror.Apply(message)
		if err != nil {
			return true, fmt.Errorf("Mirror.Apply(%T): %w", message, err)
		}
		if update.Resync != nil {
			return true, fmt.Errorf("Mirror.Apply(%T) requested resync %+v", message, *update.Resync)
		}
		if update.Rejected != nil {
			return true, fmt.Errorf("command rejected: %+v", *update.Rejected)
		}
	case network.RemotePlayerSpawn, network.RemotePlayerStates, network.RemotePlayerDespawn:
		if err := connected.remotes.Apply(message); err != nil {
			return true, fmt.Errorf("RemotePlayers.Apply(%T): %w", message, err)
		}
	case network.KeepAlive:
		connected.transcript = connected.transcript[:len(connected.transcript)-1]
	}
	return true, nil
}

func mustDrainMultiplayer(
	t *testing.T,
	ctx context.Context,
	first, second *multiplayerTCPClient,
	label string,
	done func() bool,
) {
	t.Helper()
	if second == nil {
		if err := first.drainUntil(ctx, done); err != nil {
			t.Fatalf("%s: %v\n%s", label, err, multiplayerDiagnostics(first, second))
		}
		return
	}
	for !done() {
		progressed := false
		for _, connected := range []*multiplayerTCPClient{first, second} {
			got, err := connected.drainOne()
			if err != nil {
				t.Fatalf("%s for %s: %v\n%s", label, connected.identity.DisplayName, err, multiplayerDiagnostics(first, second))
			}
			progressed = progressed || got
			if done() {
				return
			}
		}
		if progressed {
			continue
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%s: %v\n%s", label, ctx.Err(), multiplayerDiagnostics(first, second))
		default:
			runtime.Gosched()
		}
	}
}

func mustSendMultiplayer(
	t *testing.T,
	ctx context.Context,
	connected *multiplayerTCPClient,
	message network.ClientMessage,
) {
	t.Helper()
	if err := connected.endpoint.Send(ctx, message); err != nil {
		t.Fatalf("%s Send(%T): %v\n%s", connected.identity.DisplayName, message, err, multiplayerDiagnostics(connected, nil))
	}
}

func mustCloseMultiplayerTCPClient(t *testing.T, connected *multiplayerTCPClient) {
	t.Helper()
	if connected == nil || connected.closed {
		return
	}
	connected.closed = true
	if err := connected.receiver.Close(); err != nil && !errors.Is(err, network.ErrClosed) {
		t.Fatalf("close client %s: %v", connected.identity.DisplayName, err)
	}
}

func cleanupMultiplayerTCPTest(
	t *testing.T,
	host multiplayerTCPHost,
	first, second **multiplayerTCPClient,
) {
	t.Helper()
	t.Cleanup(func() {
		for _, connected := range []*multiplayerTCPClient{*first, *second} {
			if connected == nil || connected.closed {
				continue
			}
			connected.closed = true
			if err := connected.receiver.Close(); err != nil && !errors.Is(err, network.ErrClosed) {
				t.Errorf("cleanup client %s: %v", connected.identity.DisplayName, err)
			}
		}

		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := host.Host.Shutdown(shutdown); err != nil {
			t.Errorf("cleanup Host.Shutdown: %v", err)
		}
		select {
		case err := <-host.Done:
			if err != nil {
				t.Errorf("unconsumed Host.Run error: %v", err)
			}
		case <-shutdown.Done():
			t.Errorf("Host.Run cleanup: %v", shutdown.Err())
		}
	})
}

func (connected *multiplayerTCPClient) readyWithFootSnapshot() bool {
	if !connected.local.Ready {
		return false
	}
	_, ok := connected.mirror.Chunk(connected.local.Dimension, vec3BlockPos(connected.local.Position).Chunk())
	return ok
}

func (connected *multiplayerTCPClient) sawSingleSpawnAfterFootSnapshot(want network.Identity) bool {
	spawnIndex := -1
	snapshotIndex := -1
	spawnCount := 0
	for index, event := range connected.transcript {
		switch message := event.message.(type) {
		case network.RemotePlayerSpawn:
			if message.PlayerID != want.PlayerID {
				continue
			}
			spawnCount++
			if message.DisplayName != want.DisplayName {
				return false
			}
			spawnIndex = index
			footChunk := vec3BlockPos(message.Position).Chunk()
			for candidate, prior := range connected.transcript[:index] {
				if snapshot, ok := prior.message.(network.ChunkSnapshot); ok &&
					snapshot.Dimension == message.Dimension && snapshot.Chunk == footChunk {
					snapshotIndex = candidate
				}
			}
		}
	}
	presentations := connected.remotes.Presentations()
	return spawnCount == 1 && snapshotIndex >= 0 && spawnIndex > snapshotIndex &&
		len(presentations) == 1 && presentations[0].PlayerID == want.PlayerID &&
		presentations[0].DisplayName == want.DisplayName
}

func (connected *multiplayerTCPClient) sawRemoteMoveOnIncreasingTicks(
	playerID core.PlayerID,
	start mgl32.Vec3,
) bool {
	var ticks []uint64
	position := start
	for _, event := range connected.transcript {
		states, ok := event.message.(network.RemotePlayerStates)
		if !ok {
			continue
		}
		for _, state := range states.Players {
			if state.PlayerID != playerID {
				continue
			}
			if len(ticks) > 0 && states.ServerTick <= ticks[len(ticks)-1] {
				return false
			}
			ticks = append(ticks, states.ServerTick)
			position = state.Position
		}
	}
	return len(ticks) >= 2 && position != start
}

func (connected *multiplayerTCPClient) sawExactlyOneTerminalDespawn(playerID core.PlayerID) bool {
	despawns := 0
	despawnIndex := -1
	for index, event := range connected.transcript {
		switch message := event.message.(type) {
		case network.RemotePlayerDespawn:
			if message.PlayerID == playerID {
				despawns++
				despawnIndex = index
			}
		case network.RemotePlayerStates:
			if despawnIndex < 0 || index <= despawnIndex {
				continue
			}
			for _, state := range message.Players {
				if state.PlayerID == playerID {
					return false
				}
			}
		}
	}
	return despawns == 1
}

func assertMirrorBlockAndRevision(
	t *testing.T,
	connected *multiplayerTCPClient,
	position core.BlockPos,
	wantBlock core.BlockID,
	wantRevision uint64,
) {
	t.Helper()
	block, loaded := connected.mirror.BlockAt(core.Overworld, position)
	_, revision, hashed := connected.mirror.Hash(core.Overworld, position.Chunk())
	if !loaded || block != wantBlock || !hashed || revision != wantRevision {
		t.Fatalf("%s mirror block %+v=(id=%d loaded=%v revision=%d hashed=%v), want (%d,true,%d,true)",
			connected.identity.DisplayName, position, block, loaded, revision, hashed, wantBlock, wantRevision)
	}
}

func mirrorsConvergedAt(
	left, right *multiplayerTCPClient,
	position core.BlockPos,
	wantBlock core.BlockID,
	wantRevision uint64,
) bool {
	leftBlock, leftLoaded := left.mirror.BlockAt(core.Overworld, position)
	rightBlock, rightLoaded := right.mirror.BlockAt(core.Overworld, position)
	leftHash, leftRevision, leftHashed := left.mirror.Hash(core.Overworld, position.Chunk())
	rightHash, rightRevision, rightHashed := right.mirror.Hash(core.Overworld, position.Chunk())
	return leftLoaded && rightLoaded && leftBlock == wantBlock && rightBlock == wantBlock &&
		leftHashed && rightHashed && leftRevision == wantRevision && rightRevision == wantRevision &&
		leftHash == rightHash
}

func vec3BlockPos(position mgl32.Vec3) core.BlockPos {
	return core.BlockPos{
		X: int32(math.Floor(float64(position[0]))),
		Y: int32(math.Floor(float64(position[1]))),
		Z: int32(math.Floor(float64(position[2]))),
	}
}

func multiplayerDiagnostics(first, second *multiplayerTCPClient) string {
	var output strings.Builder
	for _, connected := range []*multiplayerTCPClient{first, second} {
		if connected == nil {
			continue
		}
		fmt.Fprintf(&output, "%s local=%+v roster=%+v transcript:\n", connected.identity.DisplayName, connected.local, connected.remotes.Presentations())
		for index, event := range connected.transcript {
			fmt.Fprintf(&output, "  %03d %s\n", index, multiplayerEventSummary(event.message))
		}
	}
	return output.String()
}

func multiplayerEventSummary(message network.ServerMessage) string {
	switch message := message.(type) {
	case network.PlayerState:
		return fmt.Sprintf("PlayerState tick=%d input=%d pos=%v ready=%v reset=%v", message.ServerTick, message.LastInputSequence, message.Position, message.Ready, message.Reset)
	case network.ChunkSnapshot:
		return fmt.Sprintf("ChunkSnapshot chunk=%+v revision=%d", message.Chunk, message.Revision)
	case network.BlockChanges:
		return fmt.Sprintf("BlockChanges chunk=%+v revision=%d->%d changes=%+v", message.Chunk, message.BaseRevision, message.NewRevision, message.Changes)
	case network.RemotePlayerSpawn:
		return fmt.Sprintf("RemotePlayerSpawn id=%s name=%q tick=%d pos=%v", message.PlayerID, message.DisplayName, message.ServerTick, message.Position)
	case network.RemotePlayerStates:
		return fmt.Sprintf("RemotePlayerStates tick=%d players=%+v", message.ServerTick, message.Players)
	case network.RemotePlayerDespawn:
		return fmt.Sprintf("RemotePlayerDespawn id=%s", message.PlayerID)
	default:
		return fmt.Sprintf("%T %+v", message, message)
	}
}
