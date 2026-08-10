package server

import (
	"context"
	"runtime"
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
		FormatVersion: 2, Seed: 42, SpawnDimension: core.Overworld,
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
	host := NewHost(config, generator, store)
	done := make(chan error, 1)
	go func() { done <- host.Run(context.Background(), listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
		defer cancel()
		if err := host.Shutdown(ctx); err != nil {
			t.Errorf("cleanup Host.Shutdown: %v", err)
		}
	})
	return integrationHost{Host: host, Addr: listener.Addr(), Done: done}
}

func dialIntegrationClient(t *testing.T, address string, identity network.Identity) integrationClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
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

// SessionFor 返回某个身份当前占用的权威会话 ID，供纵向脚本直接构造权威场景。
func (h integrationHost) SessionFor(t *testing.T, id core.PlayerID) sim.SessionID {
	t.Helper()
	h.Host.mu.Lock()
	defer h.Host.mu.Unlock()
	active := h.Host.activeByPlayer[id]
	if active == nil || active.Session == 0 {
		t.Fatalf("player %s has no active session", id)
	}
	return active.Session
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

func (h integrationHost) WaitPlayerReleased(t *testing.T, id core.PlayerID) {
	t.Helper()
	waitIntegrationCondition(t, "host player slot release", func() bool {
		h.Host.mu.Lock()
		defer h.Host.mu.Unlock()
		return h.Host.activeByPlayer[id] == nil
	})
	done := make(chan struct{})
	go func() {
		h.Host.sessionWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(waitDeadline):
		t.Fatal("host session lifecycle did not finish")
	}
}

func (h integrationHost) Shutdown(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
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
	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
	defer cancel()
	if err := endpoint.Send(ctx, message); err != nil {
		t.Fatalf("Send(%T): %v", message, err)
	}
}

func waitIntegrationCondition(t *testing.T, label string, condition func() bool) {
	t.Helper()
	deadline := time.NewTimer(longWaitDeadline)
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

func integrationIdentity(last byte, name string) network.Identity {
	id := integrationPlayerID()
	id[len(id)-1] = last
	return network.Identity{PlayerID: id, DisplayName: name}
}

func waitClientReadyFor(t *testing.T, host integrationHost, connected integrationClient, id core.PlayerID) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), longWaitDeadline)
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
		FormatVersion: 2, Seed: 42, SpawnDimension: core.Overworld,
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
		Yaw: snapshot.Yaw, Pitch: snapshot.Pitch, Inventory: snapshot.Inventory,
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
