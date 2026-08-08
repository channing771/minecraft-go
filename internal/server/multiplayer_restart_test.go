package server

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/storage"
	"minecraft-go/internal/world"
)

var multiplayerRestartTarget = core.BlockPos{X: 8, Y: 1, Z: 2}

type multiplayerRestartGenerator struct{}

func (multiplayerRestartGenerator) GenerateChunk(position core.ChunkPos) *world.Chunk {
	chunk := flatTestGenerator{}.GenerateChunk(position)
	if multiplayerRestartTarget.Chunk() == position {
		x, _, z := multiplayerRestartTarget.Local()
		chunk.SetBlock(x, multiplayerRestartTarget.Y, z, core.StoneID)
		chunk.Compact()
	}
	return chunk
}

type multiplayerRestartHost struct {
	host         *Host
	addr         string
	done         <-chan error
	listener     network.Listener
	shutdownOnce *sync.Once
	shutdownErr  *error
}

func TestEightPlayersSurviveDiskRestart(t *testing.T) {
	runEightPlayersSurviveDiskRestart(t)
}

func TestInspectionDiskStoreCleanupIsIdempotent(t *testing.T) {
	calls := 0
	cleanup := registerTask16IdempotentCleanup(t, "inspection probe", func() error {
		calls++
		return nil
	})
	if err := cleanup(); err != nil {
		t.Fatalf("first cleanup: %v", err)
	}
	if err := cleanup(); err != nil {
		t.Fatalf("second cleanup: %v", err)
	}
	if calls != 1 {
		t.Fatalf("cleanup calls=%d, want 1", calls)
	}
}

func runEightPlayersSurviveDiskRestart(t *testing.T) {
	t.Helper()
	const seed int64 = 160016
	root := t.TempDir()
	identities := make([]network.Identity, multiplayerClientCount)
	seedStore, err := storage.OpenDisk(context.Background(), root, storage.OpenOptions{Create: storage.Metadata{
		FormatVersion: 2, Seed: seed, SpawnDimension: core.Overworld,
	}})
	if err != nil {
		t.Fatalf("OpenDisk seed: %v", err)
	}
	for index := range identities {
		identities[index] = multiplayerIdentity(byte(0xa0+index), multiplayerNames[index])
		location := storage.PlayerLocation{Dimension: core.Overworld, Position: [3]float32{8.5, 1.001, 8.5}}
		if _, err := seedStore.SavePlayer(context.Background(), storage.PlayerSave{
			PlayerID: identities[index].PlayerID, Revision: 1, DisplayName: identities[index].DisplayName,
			Current: location, Safe: &location,
		}); err != nil {
			_ = seedStore.Close()
			t.Fatalf("seed player %d: %v", index, err)
		}
	}
	if err := seedStore.Close(); err != nil {
		t.Fatalf("close seed store: %v", err)
	}

	var clients []*multiplayerTCPClient
	first := startMultiplayerRestartHost(t, root, seed)
	t.Cleanup(func() {
		if err := shutdownRestartHost(first, clients); err != nil {
			t.Errorf("cleanup first restart host: %v", err)
		}
	})
	clients = connectRestartClients(t, first.addr, identities, nil)
	waitRestartClientsReady(t, clients)
	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
	if err := clients[0].endpoint.Send(ctx, network.PlayerInput{Sequence: 1, Yaw: 0, Pitch: -0.2, Mining: true}); err != nil {
		cancel()
		t.Fatalf("break edited chunk: %v", err)
	}
	cancel()
	waitRestartMirrorRevision(t, clients, multiplayerRestartTarget.Chunk(), 2)

	directions := [multiplayerClientCount][2]int8{{1, 0}, {-1, 0}, {0, 1}, {0, -1}, {1, 1}, {1, -1}, {-1, 1}, {-1, -1}}
	startTick := clients[0].local.ServerTick
	for index, connected := range clients {
		sequence := uint64(1)
		if index == 0 {
			sequence = 2
		}
		ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
		err := connected.endpoint.Send(ctx, network.PlayerInput{
			Sequence: sequence, MoveX: directions[index][0], MoveZ: directions[index][1], Yaw: 0, Pitch: -0.2,
		})
		cancel()
		if err != nil {
			t.Fatalf("move player %d: %v", index, err)
		}
	}
	waitRestartTick(t, clients, startTick+30)
	for index, connected := range clients {
		sequence := uint64(2)
		if index == 0 {
			sequence = 3
		}
		ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
		err := connected.endpoint.Send(ctx, network.PlayerInput{Sequence: sequence, Yaw: 0, Pitch: -0.2})
		cancel()
		if err != nil {
			t.Fatalf("stop player %d: %v", index, err)
		}
	}
	waitRestartSequences(t, clients, []uint64{3, 2, 2, 2, 2, 2, 2, 2})

	expectedSnapshots := make(map[core.PlayerID]sim.PlayerSnapshot, multiplayerClientCount)
	currentPositions := make(map[mgl32.Vec3]core.PlayerID, multiplayerClientCount)
	safePositions := make(map[mgl32.Vec3]core.PlayerID, multiplayerClientCount)
	for index, identity := range identities {
		active := activeLoginForPlayer(t, first.host, identity.PlayerID)
		if active.Name != identity.DisplayName {
			t.Fatalf("player %d name=%q, want %q", index, active.Name, identity.DisplayName)
		}
		snapshot, ok := first.host.world.PlayerSnapshotFor(active.Session)
		if !ok || snapshot.Safe == nil {
			t.Fatalf("player %d missing current/safe snapshot: %+v ok=%t", index, snapshot, ok)
		}
		if vec3BlockPos(snapshot.Current.Position).Chunk() != (core.ChunkPos{}) {
			t.Fatalf("player %d left loaded safe chunk: %v", index, snapshot.Current.Position)
		}
		if prior, duplicate := currentPositions[snapshot.Current.Position]; duplicate {
			t.Fatalf("players %s and %s have duplicate positions %v", prior, identity.PlayerID, snapshot.Current.Position)
		}
		currentPositions[snapshot.Current.Position] = identity.PlayerID
		if vec3BlockPos(snapshot.Safe.Position).Chunk() != (core.ChunkPos{}) {
			t.Fatalf("player %d safe location left loaded chunk: %v", index, snapshot.Safe.Position)
		}
		if prior, duplicate := safePositions[snapshot.Safe.Position]; duplicate {
			t.Fatalf("players %s and %s have duplicate safe positions %v", prior, identity.PlayerID, snapshot.Safe.Position)
		}
		safePositions[snapshot.Safe.Position] = identity.PlayerID
		assertRestartDirection(t, index, directions[index], snapshot.Current.Position)
		assertRestartDirection(t, index, directions[index], snapshot.Safe.Position)
		expectedSnapshots[identity.PlayerID] = snapshot
	}
	if len(currentPositions) != multiplayerClientCount || len(safePositions) != multiplayerClientCount {
		t.Fatalf("distinct restart locations current=%d safe=%d, want %d/%d",
			len(currentPositions), len(safePositions), multiplayerClientCount, multiplayerClientCount)
	}
	chunkHash, chunkRevision, ok := first.host.world.ChunkHash(core.Overworld, multiplayerRestartTarget.Chunk())
	if !ok || chunkRevision < 2 {
		t.Fatalf("edited chunk unavailable before restart: hash=%x revision=%d ok=%t", chunkHash, chunkRevision, ok)
	}
	diskStore, ok := first.host.world.store.(*storage.DiskStore)
	if !ok {
		t.Fatalf("restart world store type=%T, want *storage.DiskStore", first.host.world.store)
	}
	expectedStored := waitRestartSnapshotsPersisted(t, diskStore, identities, expectedSnapshots)
	if err := shutdownRestartHost(first, clients); err != nil {
		t.Fatalf("shutdown first restart host: %v", err)
	}

	inspectStore, err := storage.OpenDisk(context.Background(), root, storage.OpenOptions{})
	if err != nil {
		t.Fatalf("reopen DiskStore for inspection: %v", err)
	}
	closeInspectStore := registerTask16IdempotentCleanup(t, "inspection DiskStore", inspectStore.Close)
	for _, identity := range identities {
		stored, err := inspectStore.LoadPlayer(context.Background(), identity.PlayerID)
		if err != nil {
			t.Fatalf("LoadPlayer %s after shutdown: %v", identity.PlayerID, err)
		}
		if stored.Revision <= 1 || stored.DisplayName != identity.DisplayName {
			t.Fatalf("stored player %s revision/name=%d/%q, want >1/%q", identity.PlayerID, stored.Revision, stored.DisplayName, identity.DisplayName)
		}
		assertStoredMatchesSnapshot(t, stored, expectedSnapshots[identity.PlayerID])
		if !reflect.DeepEqual(stored, expectedStored[identity.PlayerID]) {
			t.Fatalf("stored player %s changed across client close/shutdown: got=%+v pre-close=%+v",
				identity.PlayerID, stored, expectedStored[identity.PlayerID])
		}
	}
	storedChunk, err := inspectStore.LoadChunk(context.Background(), core.ChunkKey{Dimension: core.Overworld, Pos: multiplayerRestartTarget.Chunk()})
	if err != nil {
		t.Fatalf("LoadChunk after shutdown: %v", err)
	}
	if storedChunk.Revision != chunkRevision || storedChunk.Chunk.Hash() != chunkHash {
		t.Fatalf("stored edited chunk=%x/%d, want %x/%d", storedChunk.Chunk.Hash(), storedChunk.Revision, chunkHash, chunkRevision)
	}
	if err := closeInspectStore(); err != nil {
		t.Fatalf("close inspection store: %v", err)
	}

	var reconnected []*multiplayerTCPClient
	second := startMultiplayerRestartHost(t, root, seed)
	t.Cleanup(func() {
		if err := shutdownRestartHost(second, reconnected); err != nil {
			t.Errorf("cleanup second restart host: %v", err)
		}
	})
	order := []int{5, 2, 7, 0, 6, 1, 4, 3}
	reconnected = connectRestartClients(t, second.addr, identities, order)
	waitRestartClientsReady(t, reconnected)
	seenSessions := make(map[sim.SessionID]struct{}, multiplayerClientCount)
	for index, identity := range identities {
		active := activeLoginForPlayer(t, second.host, identity.PlayerID)
		if active.Session == 0 {
			t.Fatalf("restarted player %d has zero runtime SessionID", index)
		}
		if _, duplicate := seenSessions[active.Session]; duplicate {
			t.Fatalf("restarted SessionID %d reused concurrently", active.Session)
		}
		seenSessions[active.Session] = struct{}{}
		if active.Name != identity.DisplayName {
			t.Fatalf("restarted player %d name=%q, want %q", index, active.Name, identity.DisplayName)
		}
		got, ok := second.host.world.PlayerSnapshotFor(active.Session)
		if !ok || !reflect.DeepEqual(got, expectedSnapshots[identity.PlayerID]) {
			t.Fatalf("restarted player %d snapshot=%+v ok=%t, want %+v", index, got, ok, expectedSnapshots[identity.PlayerID])
		}
		stored, err := second.host.world.store.(storage.WorldStore).LoadPlayer(context.Background(), identity.PlayerID)
		if err != nil || !reflect.DeepEqual(stored, expectedStored[identity.PlayerID]) {
			t.Fatalf("restarted player %d persisted fields=%+v err=%v, want %+v", index, stored, err, expectedStored[identity.PlayerID])
		}
	}
	// SessionID is runtime-only: uniqueness/non-zero is asserted above, but it is
	// intentionally excluded from every per-PlayerID persisted-field comparison.
	restartedHash, restartedRevision, ok := second.host.world.ChunkHash(core.Overworld, multiplayerRestartTarget.Chunk())
	if !ok || restartedHash != chunkHash || restartedRevision != chunkRevision {
		t.Fatalf("restarted edited chunk=%x/%d/ok=%t, want %x/%d/true", restartedHash, restartedRevision, ok, chunkHash, chunkRevision)
	}
	if err := shutdownRestartHost(second, reconnected); err != nil {
		t.Fatalf("shutdown second restart host: %v", err)
	}
}

func startMultiplayerRestartHost(t *testing.T, root string, seed int64) multiplayerRestartHost {
	t.Helper()
	store, err := storage.OpenDisk(context.Background(), root, storage.OpenOptions{Create: storage.Metadata{
		FormatVersion: 2, Seed: seed, SpawnDimension: core.Overworld,
	}})
	if err != nil {
		t.Fatalf("OpenDisk host: %v", err)
	}
	listener, err := network.ListenTCP("127.0.0.1:0")
	if err != nil {
		_ = store.Close()
		t.Fatalf("ListenTCP restart: %v", err)
	}
	config := hostTestConfig()
	config.Seed = seed
	config.MaxPlayers = multiplayerClientCount
	config.ViewRadius = 0
	config.OutboxCapacity = 512
	config.AutosaveTicks = 20
	config.ShutdownTimeout = 5 * time.Second
	host := NewHost(config, multiplayerRestartGenerator{}, store)
	done := make(chan error, 1)
	go func() { done <- host.Run(context.Background(), listener) }()
	return multiplayerRestartHost{
		host: host, addr: listener.Addr(), done: done, listener: listener,
		shutdownOnce: &sync.Once{}, shutdownErr: new(error),
	}
}

func connectRestartClients(t *testing.T, address string, identities []network.Identity, order []int) []*multiplayerTCPClient {
	t.Helper()
	if order == nil {
		order = make([]int, len(identities))
		for index := range order {
			order[index] = index
		}
	}
	requests := make([]task16ConcurrentLoginRequest, len(order))
	for position, index := range order {
		requests[position] = task16ConcurrentLoginRequest{index: index, identity: identities[index]}
	}
	clients, err := connectTask16ConcurrentClients(t, address, requests, 10*time.Second)
	if err != nil {
		t.Fatalf("restart concurrent login: %v", err)
	}
	return clients
}

func waitRestartClientsReady(t *testing.T, clients []*multiplayerTCPClient) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), longWaitDeadline)
	defer cancel()
	for !eightTCPClientsReady(clients) {
		drainAllMultiplayerAvailable(t, clients)
		if err := ctx.Err(); err != nil {
			t.Fatalf("restart clients ready: %v\n%s", err, multiplayerDiagnosticsMany(clients))
		}
		runtime.Gosched()
	}
}

func waitRestartMirrorRevision(t *testing.T, clients []*multiplayerTCPClient, chunk core.ChunkPos, revision uint64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
	defer cancel()
	for {
		drainAllMultiplayerAvailable(t, clients)
		ready := true
		for _, connected := range clients {
			_, got, ok := connected.mirror.Hash(core.Overworld, chunk)
			ready = ready && ok && got >= revision
		}
		if ready {
			return
		}
		if err := ctx.Err(); err != nil {
			t.Fatalf("mirror revision %d: %v\n%s", revision, err, multiplayerDiagnosticsMany(clients))
		}
		runtime.Gosched()
	}
}

func waitRestartTick(t *testing.T, clients []*multiplayerTCPClient, tick uint64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
	defer cancel()
	for {
		drainAllMultiplayerAvailable(t, clients)
		ready := true
		for _, connected := range clients {
			ready = ready && connected.local.ServerTick >= tick
		}
		if ready {
			return
		}
		if err := ctx.Err(); err != nil {
			t.Fatalf("wait restart tick %d: %v\n%s", tick, err, multiplayerDiagnosticsMany(clients))
		}
		runtime.Gosched()
	}
}

func waitRestartSequences(t *testing.T, clients []*multiplayerTCPClient, sequences []uint64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
	defer cancel()
	for {
		drainAllMultiplayerAvailable(t, clients)
		ready := true
		for index, connected := range clients {
			ready = ready && connected.local.LastInputSequence >= sequences[index]
		}
		if ready {
			return
		}
		if err := ctx.Err(); err != nil {
			t.Fatalf("wait restart sequences %v: %v\n%s", sequences, err, multiplayerDiagnosticsMany(clients))
		}
		runtime.Gosched()
	}
}

func assertStoredMatchesSnapshot(t *testing.T, stored storage.StoredPlayer, snapshot sim.PlayerSnapshot) {
	t.Helper()
	if !storedMatchesSnapshot(stored, snapshot) {
		t.Fatalf("stored player fields=%+v, want snapshot=%+v", stored, snapshot)
	}
}

func storedMatchesSnapshot(stored storage.StoredPlayer, snapshot sim.PlayerSnapshot) bool {
	return stored.Current.Dimension == snapshot.Current.Dimension && stored.Current.Position == [3]float32(snapshot.Current.Position) &&
		stored.Yaw == snapshot.Yaw && stored.Pitch == snapshot.Pitch && stored.Safe != nil && snapshot.Safe != nil &&
		stored.Safe.Dimension == snapshot.Safe.Dimension && stored.Safe.Position == [3]float32(snapshot.Safe.Position)
}

func assertRestartDirection(t *testing.T, index int, direction [2]int8, position mgl32.Vec3) {
	t.Helper()
	deltaX := position.X() - 8.5
	deltaZ := position.Z() - 8.5
	if direction[0] > 0 && deltaX <= 0 || direction[0] < 0 && deltaX >= 0 || direction[0] == 0 && deltaX != 0 ||
		direction[1] > 0 && deltaZ >= 0 || direction[1] < 0 && deltaZ <= 0 || direction[1] == 0 && deltaZ != 0 {
		t.Fatalf("player %d position=%v does not match direction=%v from [8.5 1.001 8.5]", index, position, direction)
	}
}

func waitRestartSnapshotsPersisted(
	t *testing.T,
	store *storage.DiskStore,
	identities []network.Identity,
	snapshots map[core.PlayerID]sim.PlayerSnapshot,
) map[core.PlayerID]storage.StoredPlayer {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), longWaitDeadline)
	defer cancel()
	last := make(map[core.PlayerID]storage.StoredPlayer, len(identities))
	lastErr := make(map[core.PlayerID]error, len(identities))
	for {
		matched := true
		for _, identity := range identities {
			stored, err := store.LoadPlayer(ctx, identity.PlayerID)
			if err != nil {
				lastErr[identity.PlayerID] = err
				matched = false
				continue
			}
			last[identity.PlayerID] = stored
			delete(lastErr, identity.PlayerID)
			if stored.Revision <= 1 || stored.DisplayName != identity.DisplayName ||
				!storedMatchesSnapshot(stored, snapshots[identity.PlayerID]) {
				matched = false
			}
		}
		if matched {
			return last
		}
		if err := ctx.Err(); err != nil {
			t.Fatalf("final snapshots were not persisted before client close: %v\nlast=%+v\nerrors=%+v", err, last, lastErr)
		}
		runtime.Gosched()
	}
}

func shutdownRestartHost(running multiplayerRestartHost, clients []*multiplayerTCPClient) error {
	if running.shutdownOnce == nil || running.shutdownErr == nil {
		return fmt.Errorf("restart host missing idempotent shutdown state")
	}
	running.shutdownOnce.Do(func() {
		*running.shutdownErr = cleanupTask16TCPHost(running.host, running.listener, running.done, clients)
	})
	return *running.shutdownErr
}

func registerTask16IdempotentCleanup(
	t *testing.T,
	label string,
	closeResource func() error,
) func() error {
	t.Helper()
	var once sync.Once
	var closeErr error
	cleanup := func() error {
		once.Do(func() { closeErr = closeResource() })
		return closeErr
	}
	t.Cleanup(func() {
		if err := cleanup(); err != nil {
			t.Errorf("cleanup %s: %v", label, err)
		}
	})
	return cleanup
}
