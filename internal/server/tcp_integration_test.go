package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
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

type integrationHost struct {
	Host *Host
	Addr string
	Done <-chan error
}

type integrationClient struct {
	Endpoint network.ClientEndpoint
	Mirror   *client.Mirror
}

type flatGenerator struct{}

type changedGenerator struct{}

func (flatGenerator) GenerateChunk(position core.ChunkPos) *world.Chunk {
	return integrationChunk(position, core.StoneID)
}

func (changedGenerator) GenerateChunk(position core.ChunkPos) *world.Chunk {
	return integrationChunk(position, core.DirtID)
}

func integrationChunk(position core.ChunkPos, fill core.BlockID) *world.Chunk {
	chunk := world.NewChunk(position)
	for z := 0; z < core.SectionSize; z++ {
		for x := 0; x < core.SectionSize; x++ {
			chunk.SetBlock(x, core.MinY, z, core.BedrockID)
			for y := int32(core.MinY + 1); y < 0; y++ {
				chunk.SetBlock(x, y, z, fill)
			}
			chunk.SetBlock(x, 0, z, core.GrassID)
		}
	}
	if position == (core.BlockPos{X: 0, Y: 1, Z: -6}).Chunk() {
		x, _, z := (core.BlockPos{X: 0, Y: 1, Z: -6}).Local()
		chunk.SetBlock(x, 1, z, core.StoneID)
	}
	chunk.Compact()
	return chunk
}

func integrationPlayerID() core.PlayerID {
	return core.PlayerID{0x9d, 0x16, 0xa0, 0x86, 0x33, 0x8b, 0x4e, 0x82, 0x8a, 0x51, 0x7a, 0x72, 0x42, 0x13, 0x6e, 0x11}
}

func startDiskHost(t *testing.T, root, address string, generator Generator) integrationHost {
	t.Helper()
	store, err := storage.OpenDisk(context.Background(), root, storage.OpenOptions{Create: storage.Metadata{
		FormatVersion: 1, Seed: 42, SpawnDimension: core.Overworld,
	}})
	if err != nil {
		t.Fatalf("OpenDisk: %v", err)
	}
	listener, err := network.ListenTCP(address)
	if err != nil {
		_ = store.Close()
		t.Fatalf("ListenTCP: %v", err)
	}
	config := DefaultConfig(store.Metadata().Seed)
	config.ViewRadius = 1
	config.Workers = 2
	config.SaveWorkers = 1
	config.SnapshotChunks = 16
	config.SnapshotBytes = 1 << 20
	config.OutboxCapacity = 256
	config.AutosaveTicks = 20
	config.RetryBaseTicks = 1
	config.RetryMaxTicks = 4
	config.ShutdownTimeout = 5 * time.Second
	host := NewHost(config, generator, store)
	done := make(chan error, 1)
	go func() { done <- host.Run(context.Background(), listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := host.Shutdown(ctx); err != nil {
			t.Errorf("cleanup Host.Shutdown: %v", err)
		}
	})
	return integrationHost{Host: host, Addr: listener.Addr(), Done: done}
}

func dialIntegrationClient(t *testing.T, address string, identity network.Identity) integrationClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := network.DialTCP(ctx, address)
	if err != nil {
		t.Fatalf("DialTCP: %v", err)
	}
	endpoint, err := network.LoginClient(ctx, stream, identity)
	if err != nil {
		t.Fatalf("LoginClient: %v", err)
	}
	return integrationClient{Endpoint: endpoint, Mirror: client.NewMirror()}
}

func (c integrationClient) Close() error {
	return c.Endpoint.Close()
}

func waitClientReady(t *testing.T, host integrationHost, connected integrationClient) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ready := false
	loadedOrigin := false
	loadedInteraction := false
	for !ready || !loadedOrigin || !loadedInteraction {
		message, err := connected.Endpoint.Recv(ctx)
		if err != nil {
			t.Fatalf("wait ready Recv: %v", err)
		}
		applyIntegrationMessage(t, connected.Mirror, message)
		switch message := message.(type) {
		case network.PlayerState:
			ready = message.Ready
		case network.ChunkSnapshot:
			loadedOrigin = loadedOrigin || message.Chunk == (core.ChunkPos{})
			loadedInteraction = loadedInteraction ||
				message.Chunk == (core.BlockPos{X: 0, Y: 1, Z: -5}).Chunk()
		}
	}
	if _, ok := host.PlayerSnapshotFor(t, integrationPlayerID()); !ok {
		t.Fatal("ready client has no authoritative player snapshot")
	}
}

func movePlayerAndPlaceBlock(
	t *testing.T,
	host integrationHost,
	connected integrationClient,
	position core.BlockPos,
) {
	t.Helper()
	sendIntegration(t, connected.Endpoint, network.PlaceBlock{
		Sequence: 1, Yaw: 0, Pitch: -0.2, Block: core.DirtID,
	})
	waitIntegrationState(t, connected, func(message network.ServerMessage) bool {
		changes, ok := message.(network.BlockChanges)
		if !ok {
			return false
		}
		for _, change := range changes.Changes {
			if change.Position == position && change.Block == core.DirtID {
				return true
			}
		}
		return false
	})
	before := host.PlayerSnapshot(t, integrationPlayerID())
	sendIntegration(t, connected.Endpoint, network.PlayerInput{
		Sequence: 2, MoveX: 1, Yaw: 0, Pitch: -0.2,
	})
	waitIntegrationState(t, connected, func(message network.ServerMessage) bool {
		state, ok := message.(network.PlayerState)
		return ok && state.LastInputSequence >= 2 && state.Position != before.Current.Position
	})
}

func (h integrationHost) PlayerSnapshot(t *testing.T, id core.PlayerID) sim.PlayerSnapshot {
	t.Helper()
	snapshot, ok := h.PlayerSnapshotFor(t, id)
	if !ok {
		t.Fatalf("player %s snapshot unavailable", id)
	}
	return snapshot
}

func (h integrationHost) PlayerSnapshotFor(t *testing.T, id core.PlayerID) (sim.PlayerSnapshot, bool) {
	t.Helper()
	h.Host.mu.Lock()
	active := h.Host.activeByPlayer[id]
	if active == nil || active.Session == 0 {
		h.Host.mu.Unlock()
		return sim.PlayerSnapshot{}, false
	}
	session := active.Session
	h.Host.mu.Unlock()
	return h.Host.world.PlayerSnapshotFor(session)
}

func (h integrationHost) ChunkHash(t *testing.T, position core.ChunkPos) ([32]byte, uint64) {
	t.Helper()
	hash, revision, ok := h.Host.world.ChunkHash(core.Overworld, position)
	if !ok {
		t.Fatalf("chunk %+v hash unavailable", position)
	}
	return hash, revision
}

func (h integrationHost) WaitPlayerSaved(t *testing.T, id core.PlayerID) {
	t.Helper()
	waitIntegrationCondition(t, "player save completion", func() bool {
		h.Host.mu.Lock()
		active := h.Host.activeByPlayer[id]
		h.Host.mu.Unlock()
		h.Host.players.mu.Lock()
		cache := h.Host.players.cache[id]
		saved := cache != nil && cache.persisted > 0 && !cache.dirty && !cache.inFlight && cache.retry == nil
		h.Host.players.mu.Unlock()
		return active == nil && saved
	})
}

func (h integrationHost) Shutdown(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.Host.Shutdown(ctx); err != nil {
		t.Fatalf("Host.Shutdown: %v", err)
	}
	select {
	case err := <-h.Done:
		if err != nil {
			t.Fatalf("Host.Run: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("Host.Run did not exit: %v", ctx.Err())
	}
}

func assertPlayerRestored(t *testing.T, host integrationHost, id core.PlayerID, want sim.PlayerSnapshot) {
	t.Helper()
	got := host.PlayerSnapshot(t, id)
	if got.Current != want.Current || got.Yaw != want.Yaw || got.Pitch != want.Pitch ||
		!equalIntegrationSafe(got.Safe, want.Safe) {
		t.Fatalf("restored player=%+v, want %+v", got, want)
	}
}

func assertChunkHash(
	t *testing.T,
	host integrationHost,
	position core.ChunkPos,
	wantHash [32]byte,
	wantRevision uint64,
) {
	t.Helper()
	gotHash, gotRevision := host.ChunkHash(t, position)
	if gotHash != wantHash || gotRevision != wantRevision {
		t.Fatalf("chunk %+v=(%x,%d), want (%x,%d)", position, gotHash, gotRevision, wantHash, wantRevision)
	}
}

func assertMirrorMatchesAuthority(t *testing.T, host integrationHost, connected integrationClient) {
	t.Helper()
	wantHash, wantRevision := host.ChunkHash(t, core.ChunkPos{})
	gotHash, gotRevision, ok := connected.Mirror.Hash(core.Overworld, core.ChunkPos{})
	if !ok || gotHash != wantHash || gotRevision != wantRevision {
		t.Fatalf("mirror=(%x,%d,%v), authority=(%x,%d)", gotHash, gotRevision, ok, wantHash, wantRevision)
	}
}

func waitIntegrationState(
	t *testing.T,
	connected integrationClient,
	condition func(network.ServerMessage) bool,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	seen := make([]string, 0, 16)
	for {
		message, err := connected.Endpoint.Recv(ctx)
		if err != nil {
			t.Fatalf("Recv after %v: %v", seen, err)
		}
		seen = append(seen, fmt.Sprintf("%T:%+v", message, message))
		if len(seen) > 12 {
			seen = seen[len(seen)-12:]
		}
		applyIntegrationMessage(t, connected.Mirror, message)
		if condition(message) {
			return
		}
	}
}

func applyIntegrationMessage(t *testing.T, mirror *client.Mirror, message network.ServerMessage) {
	t.Helper()
	switch message.(type) {
	case network.ChunkSnapshot, network.BlockChanges, network.ForgetChunks, network.CommandRejected:
		update, err := mirror.Apply(message)
		if err != nil {
			t.Fatalf("Mirror.Apply(%T): %v", message, err)
		}
		if update.Resync != nil {
			t.Fatalf("unexpected mirror resync: %+v", *update.Resync)
		}
	}
}

func sendIntegration(t *testing.T, endpoint network.ClientEndpoint, message network.ClientMessage) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := endpoint.Send(ctx, message); err != nil {
		t.Fatalf("Send(%T): %v", message, err)
	}
}

func waitIntegrationCondition(t *testing.T, label string, condition func() bool) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	stop := make(chan struct{})
	defer close(stop)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if condition() {
				return
			}
			select {
			case <-stop:
				return
			default:
			}
			runtime.Gosched()
		}
	}()
	select {
	case <-done:
	case <-deadline.C:
		t.Fatalf("timed out waiting for %s", label)
	}
}

func equalIntegrationSafe(left, right *sim.PlayerLocation) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func TestTCPPlayerAndWorldSurviveDisconnectAndRestart(t *testing.T) {
	root := t.TempDir()
	id := integrationPlayerID()
	first := startDiskHost(t, root, "127.0.0.1:0", flatGenerator{})
	client := dialIntegrationClient(t, first.Addr, network.Identity{
		PlayerID: id, DisplayName: "Chen",
	})
	waitClientReady(t, first, client)
	movePlayerAndPlaceBlock(t, first, client, core.BlockPos{X: 0, Y: 1, Z: -5})
	wantPlayer := first.PlayerSnapshot(t, id)
	wantHash, wantRevision := first.ChunkHash(t, core.ChunkPos{})
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	first.WaitPlayerSaved(t, id)
	first.Shutdown(t)

	second := startDiskHost(t, root, "127.0.0.1:0", changedGenerator{})
	reconnected := dialIntegrationClient(t, second.Addr, network.Identity{
		PlayerID: id, DisplayName: "Chen2",
	})
	waitClientReady(t, second, reconnected)
	assertPlayerRestored(t, second, id, wantPlayer)
	assertChunkHash(t, second, core.ChunkPos{}, wantHash, wantRevision)
	assertMirrorMatchesAuthority(t, second, reconnected)
	if err := reconnected.Close(); err != nil {
		t.Fatal(err)
	}
	second.Shutdown(t)
}

func TestTCPPlayerAndWorldFailureMatrix(t *testing.T) {
	t.Run("server full version mismatch and hostile peers leave listener healthy", func(t *testing.T) {
		host := startDiskHost(t, t.TempDir(), "127.0.0.1:0", flatGenerator{})
		host.Host.mu.Lock()
		host.Host.config.MaxPlayers = 1
		host.Host.mu.Unlock()
		primaryIdentity := integrationIdentity(0x21, "Primary")
		primary := dialIntegrationClient(t, host.Addr, primaryIdentity)
		waitClientReadyFor(t, host, primary, primaryIdentity.PlayerID)

		_, err := loginIntegrationClient(host.Addr, integrationIdentity(0x22, "Second"))
		assertRemoteCode(t, err, network.StateLogin, uint8(network.LoginServerFull))

		raw, err := net.Dial("tcp", host.Addr)
		if err != nil {
			t.Fatal(err)
		}
		if err := network.WriteFrame(raw, 0, []byte{byte(network.ProtocolVersion + 1)}); err != nil {
			_ = raw.Close()
			t.Fatal(err)
		}
		packetID, payload, err := network.ReadFrame(raw)
		_ = raw.Close()
		if err != nil {
			t.Fatal(err)
		}
		codec, err := network.NewCodec()
		if err != nil {
			t.Fatal(err)
		}
		packet, err := codec.DecodeServer(network.StateHandshake, packetID, payload)
		_ = codec.Close()
		reject, ok := packet.(network.HandshakeReject)
		if err != nil || !ok || reject.Code != network.HandshakeVersionMismatch {
			t.Fatalf("version reject=(%#v,%v), want HandshakeVersionMismatch", packet, err)
		}

		bad, err := net.Dial("tcp", host.Addr)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := bad.Write([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}); err != nil {
			t.Fatal(err)
		}
		_ = bad.Close()
		slow, err := net.Dial("tcp", host.Addr)
		if err != nil {
			t.Fatal(err)
		}

		if err := primary.Close(); err != nil {
			t.Fatal(err)
		}
		host.WaitPlayerSaved(t, primaryIdentity.PlayerID)
		replacementIdentity := integrationIdentity(0x23, "Replacement")
		replacement := dialIntegrationClient(t, host.Addr, replacementIdentity)
		waitClientReadyFor(t, host, replacement, replacementIdentity.PlayerID)
		_ = slow.Close()
		_ = replacement.Close()
		host.Shutdown(t)
	})

	t.Run("corrupt player rejects only that identity", func(t *testing.T) {
		root := t.TempDir()
		corrupt := integrationIdentity(0x31, "Corrupt")
		seedIntegrationPlayer(t, root, corrupt, integrationPlayerSnapshotAt(0.5, 1, 0.5, nil))
		path := filepath.Join(root, "players", corrupt.PlayerID.String()+".player")
		encoded, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		encoded[len(encoded)-1] ^= 0x80
		if err := os.WriteFile(path, encoded, 0o600); err != nil {
			t.Fatal(err)
		}

		host := startDiskHost(t, root, "127.0.0.1:0", flatGenerator{})
		_, err = loginIntegrationClient(host.Addr, corrupt)
		assertRemoteCode(t, err, network.StateLogin, uint8(network.LoginPlayerDataCorrupt))

		healthyIdentity := integrationIdentity(0x32, "Healthy")
		healthy := dialIntegrationClient(t, host.Addr, healthyIdentity)
		waitClientReadyFor(t, host, healthy, healthyIdentity.PlayerID)
		_ = healthy.Close()
		host.Shutdown(t)
	})
}

func TestTCPPlayerAndWorldRestoreFallbackMatrix(t *testing.T) {
	current := sim.PlayerLocation{Dimension: core.Overworld, Position: mgl32.Vec3{0.5, 1, 0.5}}
	safe := sim.PlayerLocation{Dimension: core.Overworld, Position: mgl32.Vec3{2.5, 1, 0.5}}

	t.Run("blocked current falls back to safe", func(t *testing.T) {
		root := t.TempDir()
		identity := integrationIdentity(0x41, "FallbackSafe")
		seedIntegrationPlayer(t, root, identity, sim.PlayerSnapshot{Current: current, Safe: &safe})
		host := startDiskHost(t, root, "127.0.0.1:0", blockedGenerator{
			core.BlockPos{X: 0, Y: 1, Z: 0}: core.StoneID,
		})
		connected := dialIntegrationClient(t, host.Addr, identity)
		waitClientReadyFor(t, host, connected, identity.PlayerID)
		got := host.PlayerSnapshot(t, identity.PlayerID)
		if got.Current != safe {
			t.Fatalf("blocked current restored to %+v, want safe %+v", got.Current, safe)
		}
		_ = connected.Close()
		host.Shutdown(t)
	})

	t.Run("blocked safe falls back to deterministic spawn", func(t *testing.T) {
		var restored [2]sim.PlayerLocation
		for run := range 2 {
			root := t.TempDir()
			identity := integrationIdentity(byte(0x51+run), fmt.Sprintf("Spawn%d", run))
			seedIntegrationPlayer(t, root, identity, sim.PlayerSnapshot{Current: current, Safe: &safe})
			host := startDiskHost(t, root, "127.0.0.1:0", blockedGenerator{
				core.BlockPos{X: 0, Y: 1, Z: 0}: core.StoneID,
				core.BlockPos{X: 2, Y: 1, Z: 0}: core.StoneID,
			})
			connected := dialIntegrationClient(t, host.Addr, identity)
			waitClientReadyFor(t, host, connected, identity.PlayerID)
			restored[run] = host.PlayerSnapshot(t, identity.PlayerID).Current
			_ = connected.Close()
			host.Shutdown(t)
		}
		if restored[0] == current || restored[0] == safe || restored[0] != restored[1] {
			t.Fatalf("spawn fallback runs=%+v, want equal and distinct from current/safe", restored)
		}
	})
}

func TestTCPPlayerAndWorldSaveFailureRecovery(t *testing.T) {
	saveErr := errors.New("injected player save failure")
	store := newHostTestStore()
	store.setSaveError(saveErr)
	host := startIntegrationHostWithStore(t, store, flatGenerator{})
	firstIdentity := integrationIdentity(0x61, "Retrying")
	first := dialIntegrationClient(t, host.Addr, firstIdentity)
	waitClientReadyFor(t, host, first, firstIdentity.PlayerID)
	sendIntegration(t, first.Endpoint, network.PlayerInput{Sequence: 1, MoveX: 1})
	waitIntegrationState(t, first, func(message network.ServerMessage) bool {
		state, ok := message.(network.PlayerState)
		return ok && state.LastInputSequence == 1 && state.Position[0] > 0.5
	})
	want := host.PlayerSnapshot(t, firstIdentity.PlayerID)
	_ = first.Close()
	waitIntegrationCondition(t, "failed player save retained for retry", func() bool {
		host.Host.players.mu.Lock()
		defer host.Host.players.mu.Unlock()
		cache := host.Host.players.cache[firstIdentity.PlayerID]
		return cache != nil && cache.retry != nil && cache.dirty && !cache.inFlight
	})

	same := dialIntegrationClient(t, host.Addr, network.Identity{
		PlayerID: firstIdentity.PlayerID, DisplayName: "RetryingRenamed",
	})
	waitClientReadyFor(t, host, same, firstIdentity.PlayerID)
	assertPlayerRestored(t, host, firstIdentity.PlayerID, want)

	_, err := loginIntegrationClient(host.Addr, integrationIdentity(0x62, "Blocked"))
	assertRemoteCode(t, err, network.StateLogin, uint8(network.LoginServerFull))
	_ = same.Close()
	waitIntegrationCondition(t, "same-ID disconnect retry retained", func() bool {
		host.Host.players.mu.Lock()
		defer host.Host.players.mu.Unlock()
		cache := host.Host.players.cache[firstIdentity.PlayerID]
		return cache != nil && cache.retry != nil && cache.dirty
	})

	differentIdentity := integrationIdentity(0x62, "IndependentRetry")
	different := dialIntegrationClient(t, host.Addr, differentIdentity)
	waitClientReadyFor(t, host, different, differentIdentity.PlayerID)
	_ = different.Close()
	store.setSaveError(nil)
	waitIntegrationCondition(t, "player save retry success", func() bool {
		host.Host.players.mu.Lock()
		defer host.Host.players.mu.Unlock()
		cache := host.Host.players.cache[firstIdentity.PlayerID]
		return cache != nil && cache.persisted > 0 && !cache.dirty && !cache.inFlight && cache.retry == nil
	})

	otherIdentity := integrationIdentity(0x63, "AfterRetry")
	other := dialIntegrationClient(t, host.Addr, otherIdentity)
	waitClientReadyFor(t, host, other, otherIdentity.PlayerID)
	_ = other.Close()
	host.Shutdown(t)
}

type blockedGenerator map[core.BlockPos]core.BlockID

func (generator blockedGenerator) GenerateChunk(position core.ChunkPos) *world.Chunk {
	chunk := integrationChunk(position, core.StoneID)
	for block, id := range generator {
		if block.Chunk() != position {
			continue
		}
		x, _, z := block.Local()
		chunk.SetBlock(x, block.Y, z, id)
	}
	chunk.Compact()
	return chunk
}

func integrationIdentity(last byte, name string) network.Identity {
	id := integrationPlayerID()
	id[len(id)-1] = last
	return network.Identity{PlayerID: id, DisplayName: name}
}

func startIntegrationHostWithStore(t *testing.T, store storage.WorldStore, generator Generator) integrationHost {
	t.Helper()
	listener, err := network.ListenTCP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	config := hostTestConfig()
	config.ViewRadius = 1
	config.AutosaveTicks = 20
	config.RetryBaseTicks = 1
	config.RetryMaxTicks = 2
	config.ShutdownTimeout = 5 * time.Second
	host := NewHost(config, generator, store)
	done := make(chan error, 1)
	go func() { done <- host.Run(context.Background(), listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := host.Shutdown(ctx); err != nil {
			t.Errorf("cleanup Host.Shutdown: %v", err)
		}
	})
	return integrationHost{Host: host, Addr: listener.Addr(), Done: done}
}

func loginIntegrationClient(address string, identity network.Identity) (network.ClientEndpoint, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := network.DialTCP(ctx, address)
	if err != nil {
		return nil, err
	}
	return network.LoginClient(ctx, stream, identity)
}

func assertRemoteCode(t *testing.T, err error, state network.State, code uint8) {
	t.Helper()
	var remote *network.RemoteError
	if !errors.As(err, &remote) || remote.State != state || remote.Code != code {
		t.Fatalf("remote error=%#v, want state=%d code=%d", err, state, code)
	}
}

func waitClientReadyFor(t *testing.T, host integrationHost, connected integrationClient, id core.PlayerID) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ready := false
	for !ready {
		message, err := connected.Endpoint.Recv(ctx)
		if err != nil {
			t.Fatalf("wait ready Recv: %v", err)
		}
		applyIntegrationMessage(t, connected.Mirror, message)
		switch message := message.(type) {
		case network.PlayerState:
			ready = message.Ready
		}
	}
	if _, ok := host.PlayerSnapshotFor(t, id); !ok {
		t.Fatalf("ready client %s has no authoritative player snapshot", id)
	}
}

func seedIntegrationPlayer(
	t *testing.T,
	root string,
	identity network.Identity,
	snapshot sim.PlayerSnapshot,
) {
	t.Helper()
	store, err := storage.OpenDisk(context.Background(), root, storage.OpenOptions{Create: storage.Metadata{
		FormatVersion: 1, Seed: 42, SpawnDimension: core.Overworld,
	}})
	if err != nil {
		t.Fatal(err)
	}
	save := storage.PlayerSave{
		PlayerID: identity.PlayerID, Revision: 1, DisplayName: identity.DisplayName,
		Current: storage.PlayerLocation{
			Dimension: snapshot.Current.Dimension,
			Position:  [3]float32(snapshot.Current.Position),
		},
		Yaw: snapshot.Yaw, Pitch: snapshot.Pitch,
	}
	if snapshot.Safe != nil {
		save.Safe = &storage.PlayerLocation{
			Dimension: snapshot.Safe.Dimension,
			Position:  [3]float32(snapshot.Safe.Position),
		}
	}
	if _, err := store.SavePlayer(context.Background(), save); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func integrationPlayerSnapshotAt(x, y, z float32, safe *sim.PlayerLocation) sim.PlayerSnapshot {
	return sim.PlayerSnapshot{
		Current: sim.PlayerLocation{Dimension: core.Overworld, Position: mgl32.Vec3{x, y, z}},
		Safe:    safe,
	}
}

func TestMemoryTCPParityBusinessTranscriptAndHashes(t *testing.T) {
	memory := runParityTranscript(t, "memory")
	tcp := runParityTranscript(t, "tcp")
	if !reflect.DeepEqual(tcp, memory) {
		t.Fatalf("TCP parity result differs\nmemory=%+v\ntcp=%+v", memory, tcp)
	}
	snapshots := 0
	for _, entry := range memory.Transcript {
		if strings.HasPrefix(entry, "ChunkSnapshot:") {
			snapshots++
		}
	}
	if snapshots < 9 {
		t.Fatalf("parity readiness transcript has %d snapshots, want at least 9", snapshots)
	}
	if last := memory.Transcript[len(memory.Transcript)-1]; !strings.HasPrefix(last, "DisconnectTick:") {
		t.Fatalf("parity transcript ends with %q, want disconnect tick", last)
	}
}

type parityResult struct {
	Transcript     []string
	PlayerHash     [32]byte
	ChunkHash      [32]byte
	ChunkRevision  uint64
	MirrorHash     [32]byte
	MirrorRevision uint64
	DisconnectTick bool
}

func runParityTranscript(t *testing.T, transport string) parityResult {
	t.Helper()
	identity := integrationIdentity(0x71, "Parity")
	store := storage.NewMemory(storage.Metadata{
		FormatVersion: 1, Seed: 42, SpawnDimension: core.Overworld,
	})
	config := hostTestConfig()
	config.ViewRadius = 1
	config.AutosaveTicks = 1000
	host := NewHost(config, flatGenerator{}, store)
	endpoint, acceptDone, closeTransport := openParityTransport(t, host, transport, identity)
	mirror := client.NewMirror()
	transcript := make([]string, 0, 64)
	readinessMessages := make([]network.ServerMessage, 0, 64)

	ready := false
	for !ready || !parityViewLoaded(mirror) {
		_, messages := parityStep(t, host, endpoint, mirror)
		for _, message := range messages {
			readinessMessages = append(readinessMessages, message)
			if state, ok := message.(network.PlayerState); ok && state.Ready {
				ready = true
			}
		}
	}
	transcript = append(transcript, parityReadinessTranscript(t, mirror, readinessMessages)...)

	commands := []network.ClientMessage{
		network.PlayerInput{Sequence: 1, MoveX: 1, Yaw: 0, Pitch: -0.2},
		network.PlaceBlock{Sequence: 2, Yaw: 0, Pitch: -0.2, Block: core.DirtID},
		network.BreakBlock{Sequence: 3, Yaw: 0, Pitch: -0.2},
		network.RequestChunkResync{
			Sequence: 4, Dimension: core.Overworld,
			Chunk: (core.BlockPos{X: 0, Y: 1, Z: -5}).Chunk(), HaveRevision: 0,
		},
		network.PlayerInput{Sequence: 5, Yaw: 0, Pitch: -0.2},
	}
	for _, command := range commands {
		sendIntegration(t, endpoint, command)
		waitIntegrationCondition(t, fmt.Sprintf("%s %T queued", transport, command), func() bool {
			return len(host.world.incoming) > 0
		})
		_, messages := parityStep(t, host, endpoint, mirror)
		for _, message := range messages {
			transcript = append(transcript, parityBusinessMessage(t, mirror, message)...)
		}
	}

	host.mu.Lock()
	active := *host.activeByPlayer[identity.PlayerID]
	host.mu.Unlock()
	playerHash, ok := host.world.engine.PlayerHash(active.Session)
	if !ok {
		t.Fatal("parity player hash unavailable")
	}
	position := (core.BlockPos{X: 0, Y: 1, Z: -5}).Chunk()
	chunkHash, chunkRevision, ok := host.world.ChunkHash(core.Overworld, position)
	if !ok {
		t.Fatal("parity chunk hash unavailable before disconnect")
	}
	mirrorHash, mirrorRevision, ok := mirror.Hash(core.Overworld, position)
	if !ok {
		t.Fatal("parity mirror hash unavailable before disconnect")
	}
	if err := endpoint.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case err := <-acceptDone:
		if err != nil && !errors.Is(err, network.ErrClosed) {
			t.Fatalf("parity accept worker: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("parity accept worker did not exit: %v", ctx.Err())
	}
	disconnect := host.world.StepForTest()
	_, playerPresent := host.world.PlayerSnapshotFor(active.Session)
	_, _, chunkPresent := host.world.ChunkHash(core.Overworld, position)
	transcript = append(transcript, fmt.Sprintf(
		"DisconnectTick:player-present=%t:chunk-present=%t", playerPresent, chunkPresent,
	))
	if playerPresent {
		t.Fatal("parity player remains after deterministic disconnect tick")
	}
	if chunkPresent {
		t.Fatal("parity chunk remains loaded after deterministic disconnect tick")
	}
	result := parityResult{
		Transcript: transcript, PlayerHash: playerHash,
		ChunkHash: chunkHash, ChunkRevision: chunkRevision,
		MirrorHash: mirrorHash, MirrorRevision: mirrorRevision,
		DisconnectTick: disconnect.Tick > 0,
	}
	if err := host.Shutdown(ctx); err != nil {
		t.Fatalf("parity Host.Shutdown: %v", err)
	}
	closeTransport()
	return result
}

func parityReadinessTranscript(
	t *testing.T,
	mirror *client.Mirror,
	messages []network.ServerMessage,
) []string {
	t.Helper()
	var lastState network.PlayerState
	hasState := false
	for _, message := range messages {
		switch message := message.(type) {
		case network.PlayerState:
			lastState = message
			hasState = true
		case network.ChunkSnapshot, network.KeepAlive:
		default:
			t.Fatalf("unexpected parity readiness message %T", message)
		}
	}
	if !hasState || !lastState.Ready {
		t.Fatalf("parity readiness ended without ready PlayerState: %+v", lastState)
	}
	lastState.ServerTick = 0
	transcript := []string{fmt.Sprintf("PlayerState:%+v", lastState)}
	for x := int32(-1); x <= 1; x++ {
		for z := int32(-1); z <= 1; z++ {
			position := core.ChunkPos{X: x, Z: z}
			hash, revision, ok := mirror.Hash(core.Overworld, position)
			if !ok {
				t.Fatalf("parity readiness mirror missing %+v", position)
			}
			transcript = append(transcript, fmt.Sprintf(
				"ChunkSnapshot:%d:%d:%d:%x", x, z, revision, hash,
			))
		}
	}
	return transcript
}

func openParityTransport(
	t *testing.T,
	host *Host,
	transport string,
	identity network.Identity,
) (network.ClientEndpoint, <-chan error, func()) {
	t.Helper()
	var clientStream network.ClientPacketStream
	var serverStream network.ServerPacketStream
	closeTransport := func() {}
	switch transport {
	case "memory":
		clientStream, serverStream = network.NewMemoryStreamPair(256)
	case "tcp":
		listener, err := network.ListenTCP("127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		accepted := make(chan struct {
			stream network.ServerPacketStream
			err    error
		}, 1)
		go func() {
			stream, err := listener.Accept(context.Background())
			accepted <- struct {
				stream network.ServerPacketStream
				err    error
			}{stream: stream, err: err}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		clientStream, err = network.DialTCP(ctx, listener.Addr())
		cancel()
		if err != nil {
			_ = listener.Close()
			t.Fatal(err)
		}
		serverResult := <-accepted
		if serverResult.err != nil {
			_ = clientStream.Close()
			_ = listener.Close()
			t.Fatal(serverResult.err)
		}
		serverStream = serverResult.stream
		closeTransport = func() { _ = listener.Close() }
	default:
		t.Fatalf("unknown parity transport %q", transport)
	}
	acceptDone := make(chan error, 1)
	go func() { acceptDone <- host.AcceptStream(context.Background(), serverStream) }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	endpoint, err := network.LoginClient(ctx, clientStream, identity)
	cancel()
	if err != nil {
		closeTransport()
		t.Fatal(err)
	}
	return endpoint, acceptDone, closeTransport
}

func parityStep(
	t *testing.T,
	host *Host,
	endpoint network.ClientEndpoint,
	mirror *client.Mirror,
) (sim.TickResult, []network.ServerMessage) {
	t.Helper()
	result := host.world.StepForTest()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	messages := make([]network.ServerMessage, 0, 16)
	for {
		message, err := endpoint.Recv(ctx)
		if err != nil {
			t.Fatalf("parity tick %d Recv: %v", result.Tick, err)
		}
		applyIntegrationMessage(t, mirror, message)
		messages = append(messages, message)
		if state, ok := message.(network.PlayerState); ok {
			if state.ServerTick != result.Tick {
				t.Fatalf("parity PlayerState tick=%d, want %d", state.ServerTick, result.Tick)
			}
			return result, messages
		}
	}
}

func parityViewLoaded(mirror *client.Mirror) bool {
	for x := int32(-1); x <= 1; x++ {
		for z := int32(-1); z <= 1; z++ {
			if _, ok := mirror.Chunk(core.Overworld, core.ChunkPos{X: x, Z: z}); !ok {
				return false
			}
		}
	}
	return true
}

func parityBusinessMessage(
	t *testing.T,
	mirror *client.Mirror,
	message network.ServerMessage,
) []string {
	t.Helper()
	switch message := message.(type) {
	case network.ChunkSnapshot:
		hash, revision, ok := mirror.Hash(message.Dimension, message.Chunk)
		if !ok {
			t.Fatalf("parity snapshot mirror missing %+v", message.Chunk)
		}
		return []string{fmt.Sprintf("ChunkSnapshot:%d:%d:%d:%x", message.Chunk.X, message.Chunk.Z, revision, hash)}
	case network.BlockChanges:
		return []string{fmt.Sprintf("BlockChanges:%+v", message)}
	case network.ForgetChunks:
		return []string{fmt.Sprintf("ForgetChunks:%+v", message)}
	case network.PlayerState:
		message.ServerTick = 0
		return []string{fmt.Sprintf("PlayerState:%+v", message)}
	case network.CommandRejected:
		return []string{fmt.Sprintf("CommandRejected:%+v", message)}
	case network.KeepAlive, network.Disconnect:
		return nil
	default:
		t.Fatalf("unexpected parity business message %T", message)
		return nil
	}
}
