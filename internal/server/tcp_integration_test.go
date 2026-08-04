package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"math"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/klauspost/compress/zstd"

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

type miningParityGenerator struct{}

func (flatGenerator) GenerateChunk(position core.ChunkPos) *world.Chunk {
	return integrationChunk(position, core.StoneID)
}

func (changedGenerator) GenerateChunk(position core.ChunkPos) *world.Chunk {
	return integrationChunk(position, core.DirtID)
}

func (miningParityGenerator) GenerateChunk(position core.ChunkPos) *world.Chunk {
	chunk := integrationChunk(position, core.StoneID)
	for _, target := range []core.BlockPos{{X: -1, Y: 1, Z: -6}, {X: 1, Y: 1, Z: -6}} {
		if target.Chunk() != position {
			continue
		}
		x, _, z := target.Local()
		chunk.SetBlock(x, target.Y, z, core.StoneID)
	}
	chunk.Compact()
	return chunk
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
	// M4B：挖掉视线内的石头障碍会在原地留下掉落物，世界修改由该挖掘产生。
	sendIntegration(t, connected.Endpoint, network.PlayerInput{
		Sequence: 1, Yaw: 0, Pitch: -0.2, Mining: true,
	})
	waitIntegrationState(t, connected, func(message network.ServerMessage) bool {
		return integrationChangeSeen(message, position, core.AirID)
	})
	before := host.PlayerSnapshot(t, integrationPlayerID())
	sendIntegration(t, connected.Endpoint, network.PlayerInput{
		Sequence: 3, MoveX: 1, Yaw: 0, Pitch: -0.2,
	})
	waitIntegrationState(t, connected, func(message network.ServerMessage) bool {
		state, ok := message.(network.PlayerState)
		return ok && state.LastInputSequence >= 3 && state.Position != before.Current.Position
	})
	sendIntegration(t, connected.Endpoint, network.PlayerInput{
		Sequence: 4, Yaw: 0, Pitch: -0.2,
	})
	waitIntegrationState(t, connected, func(message network.ServerMessage) bool {
		state, ok := message.(network.PlayerState)
		return ok && state.LastInputSequence >= 4 && state.Velocity[0] == 0 && state.Velocity[2] == 0
	})
}

// integrationChangeSeen 报告消息是否包含指定位置的目标方块变化。
func integrationChangeSeen(
	message network.ServerMessage,
	position core.BlockPos,
	block core.BlockID,
) bool {
	changes, ok := message.(network.BlockChanges)
	if !ok {
		return false
	}
	for _, change := range changes.Changes {
		if change.Position == position && change.Block == block {
			return true
		}
	}
	return false
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
	case <-time.After(5 * time.Second):
		t.Fatal("host session lifecycle did not finish")
	}
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
		got.Inventory != want.Inventory || !equalIntegrationSafe(got.Safe, want.Safe) {
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
	movePlayerAndPlaceBlock(t, first, client, core.BlockPos{X: 0, Y: 1, Z: -6})
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

func TestCraftingSurvivesV2DiskRestartAndReconnectOrder(t *testing.T) {
	root := t.TempDir()
	key := core.ChunkKey{Dimension: core.Overworld}
	seedV2CraftingChunk(t, root, key)
	firstIdentity := integrationIdentity(0x81, "Crafter")
	secondIdentity := integrationIdentity(0x82, "Observer")
	spawn := integrationPlayerSnapshotAt(0.5, 1.001, 0.5, nil)
	spawn.Inventory.Hotbar.Slots[1] = core.ItemStack{Item: core.ItemStonePickaxe, Count: 1}
	seedIntegrationPlayer(t, root, firstIdentity, spawn)
	seedIntegrationPlayer(t, root, secondIdentity, integrationPlayerSnapshotAt(0.5, 1.001, 0.5, nil))

	firstHost := startDiskHost(t, root, "127.0.0.1:0", changedGenerator{})
	firstClient := dialIntegrationClient(t, firstHost.Addr, firstIdentity)
	witness := dialIntegrationClient(t, firstHost.Addr, secondIdentity)
	waitClientReadyFor(t, firstHost, firstClient, firstIdentity.PlayerID)
	waitClientReadyFor(t, firstHost, witness, secondIdentity.PlayerID)
	waitIntegrationCondition(t, "从 v2 区块拾取 4 石头", func() bool {
		return integrationItemCount(firstHost.PlayerSnapshot(t, firstIdentity.PlayerID).Inventory, core.ItemStone) == 4
	})

	sendIntegration(t, firstClient.Endpoint, network.CraftRecipe{
		Sequence: 1, Recipe: core.RecipeStoneBricks,
	})
	waitIntegrationInventory(t, firstClient, func(inventory core.Inventory) bool {
		return integrationItemCount(inventory, core.ItemStoneBrick) == 4
	})

	placed := make([]core.BlockPos, 0, 2)
	for sequence := uint64(2); sequence <= 3; sequence++ {
		sendIntegration(t, firstClient.Endpoint, network.PlaceBlock{
			Sequence: sequence, Yaw: 0, Pitch: -0.2, Slot: 0,
		})
		waitIntegrationState(t, firstClient, func(message network.ServerMessage) bool {
			changes, ok := message.(network.BlockChanges)
			if !ok {
				return false
			}
			for _, change := range changes.Changes {
				if change.Block == core.StoneBrickID {
					placed = append(placed, change.Position)
					return true
				}
			}
			return false
		})
	}
	if len(placed) != 2 || placed[0] == placed[1] {
		t.Fatalf("石砖放置位置 = %+v，想要两个不同位置", placed)
	}

	sendIntegration(t, firstClient.Endpoint, network.SelectHotbar{
		Sequence: 4, Slot: 1,
	})
	waitIntegrationInventory(t, firstClient, func(inventory core.Inventory) bool {
		return inventory.Hotbar.Selected == 1
	})
	sendIntegration(t, firstClient.Endpoint, network.PlayerInput{
		Sequence: 5, Yaw: 0, Pitch: -0.2, Mining: true,
	})
	waitIntegrationState(t, firstClient, func(message network.ServerMessage) bool {
		return integrationChangeSeen(message, placed[1], core.AirID)
	})
	sendIntegration(t, firstClient.Endpoint, network.PlayerInput{
		Sequence: 6, MoveZ: 1, Yaw: 0, Pitch: -0.2,
	})
	wantInventory := waitIntegrationInventory(t, firstClient, func(inventory core.Inventory) bool {
		return integrationItemCount(inventory, core.ItemStoneBrick) == 3
	})
	sendIntegration(t, firstClient.Endpoint, network.PlayerInput{Sequence: 7, Yaw: 0, Pitch: -0.2})
	waitIntegrationState(t, firstClient, func(message network.ServerMessage) bool {
		state, ok := message.(network.PlayerState)
		return ok && state.LastInputSequence >= 7
	})

	wantPlayer := firstHost.PlayerSnapshot(t, firstIdentity.PlayerID)
	if wantPlayer.Inventory != wantInventory {
		t.Fatalf("权威背包 = %+v，想要客户端确认 %+v", wantPlayer.Inventory, wantInventory)
	}
	assertCraftingChunkState(t, firstHost, placed[0], placed[1])
	if err := firstClient.Close(); err != nil {
		t.Fatal(err)
	}
	firstHost.WaitPlayerSaved(t, firstIdentity.PlayerID)
	if err := witness.Close(); err != nil {
		t.Fatal(err)
	}
	firstHost.WaitPlayerReleased(t, secondIdentity.PlayerID)
	firstHost.Shutdown(t)
	if schema := integrationStoredChunkSchema(t, root, key); schema != 4 {
		t.Fatalf("正常刷新后的区块 schema=%d，想要 4", schema)
	}

	secondHost := startDiskHost(t, root, "127.0.0.1:0", flatGenerator{})
	reconnectedWitness := dialIntegrationClient(t, secondHost.Addr, secondIdentity)
	waitClientReadyFor(t, secondHost, reconnectedWitness, secondIdentity.PlayerID)
	reconnected := dialIntegrationClient(t, secondHost.Addr, firstIdentity)
	waitClientReadyFor(t, secondHost, reconnected, firstIdentity.PlayerID)
	assertPlayerRestored(t, secondHost, firstIdentity.PlayerID, wantPlayer)
	if got := secondHost.PlayerSnapshot(t, secondIdentity.PlayerID).Inventory; got != (core.Inventory{}) {
		t.Fatalf("乱序重连污染第二身份背包: %+v", got)
	}
	assertCraftingChunkState(t, secondHost, placed[0], placed[1])
	_ = reconnected.Close()
	_ = reconnectedWitness.Close()
	secondHost.Shutdown(t)
}

func TestTCPPlayerAndWorldFailureMatrixProtocolVersionAndUnknownPacket(t *testing.T) {
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

		for _, version := range []byte{7, byte(network.ProtocolVersion + 1)} {
			raw, err := net.Dial("tcp", host.Addr)
			if err != nil {
				t.Fatal(err)
			}
			if err := network.WriteFrame(raw, 0, []byte{version}); err != nil {
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
			if err != nil || !ok || reject.Code != network.HandshakeVersionMismatch ||
				reject.ServerProtocolVersion != network.ProtocolVersion {
				t.Fatalf("v%d reject=(%#v,%v), want v%d HandshakeVersionMismatch",
					version, packet, err, network.ProtocolVersion)
			}
		}

		bad, err := net.Dial("tcp", host.Addr)
		if err != nil {
			t.Fatal(err)
		}
		waitForPreLoginCount(t, host.Host, 1)
		if _, err := bad.Write([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}); err != nil {
			t.Fatal(err)
		}
		_ = bad.Close()
		waitForPreLoginCount(t, host.Host, 0)
		slow, err := net.Dial("tcp", host.Addr)
		if err != nil {
			t.Fatal(err)
		}
		waitForPreLoginCount(t, host.Host, 1)

		if err := primary.Close(); err != nil {
			t.Fatal(err)
		}
		host.WaitPlayerSaved(t, primaryIdentity.PlayerID)

		violator := integrationIdentity(0x24, "OldBreakPacket")
		rawPlay, err := net.Dial("tcp", host.Addr)
		if err != nil {
			t.Fatal(err)
		}
		codec, err := network.NewCodec()
		if err != nil {
			_ = rawPlay.Close()
			t.Fatal(err)
		}
		packetID, payload, err := codec.EncodeClient(network.StateHandshake, network.ClientHello{ProtocolVersion: network.ProtocolVersion})
		if err == nil {
			err = network.WriteFrame(rawPlay, packetID, payload)
		}
		if err == nil {
			packetID, payload, err = network.ReadFrame(rawPlay)
		}
		if err == nil {
			_, err = codec.DecodeServer(network.StateHandshake, packetID, payload)
		}
		if err == nil {
			packetID, payload, err = codec.EncodeClient(network.StateLogin, network.LoginStart{
				PlayerID: violator.PlayerID, DisplayName: violator.DisplayName,
			})
		}
		if err == nil {
			err = network.WriteFrame(rawPlay, packetID, payload)
		}
		if err == nil {
			packetID, payload, err = network.ReadFrame(rawPlay)
		}
		if err == nil {
			var packet network.ServerPacket
			packet, err = codec.DecodeServer(network.StateLogin, packetID, payload)
			if success, ok := packet.(network.LoginSuccess); !ok || success.PlayerID != violator.PlayerID {
				err = fmt.Errorf("LoginSuccess=%#v", packet)
			}
		}
		_ = codec.Close()
		if err != nil {
			_ = rawPlay.Close()
			t.Fatalf("raw Play 登录: %v", err)
		}
		if err := network.WriteFrame(rawPlay, 1, nil); err != nil {
			_ = rawPlay.Close()
			t.Fatalf("发送废止 Play packet ID 1: %v", err)
		}
		host.WaitPlayerReleased(t, violator.PlayerID)
		_ = rawPlay.Close()

		waitForPreLoginCount(t, host.Host, 1)
		_ = slow.Close()
		waitForPreLoginCount(t, host.Host, 0)
		replacementIdentity := integrationIdentity(0x23, "Replacement")
		replacement := dialIntegrationClient(t, host.Addr, replacementIdentity)
		waitClientReadyFor(t, host, replacement, replacementIdentity.PlayerID)
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
		host.WaitPlayerReleased(t, corrupt.PlayerID)

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
	host.WaitPlayerReleased(t, firstIdentity.PlayerID)
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
	host.WaitPlayerReleased(t, firstIdentity.PlayerID)
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
	host.WaitPlayerReleased(t, differentIdentity.PlayerID)
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

func TestMemoryTCPMiningConvergence(t *testing.T) {
	memory := runMiningParityScript(t, "memory")
	tcp := runMiningParityScript(t, "tcp")
	if !reflect.DeepEqual(tcp, memory) {
		t.Fatalf("采掘 Memory/TCP 未收敛\nmemory=%+v\ntcp=%+v", memory, tcp)
	}
}

func TestMiningCompletionOraclesRejectOrderDuplicatesAndMirrorDivergence(t *testing.T) {
	target := core.BlockPos{X: 1, Y: 1, Z: -6}
	blockIndex, ok := world.ChunkBlockIndex(target)
	if !ok {
		t.Fatal("测试目标没有区块索引")
	}
	delta := network.BlockChanges{
		Dimension: core.Overworld, Chunk: target.Chunk(), BaseRevision: 1, NewRevision: 2,
		Changes: []network.BlockChange{{Position: target, Block: core.AirID}},
	}
	upsert := network.ItemDropUpserts{ServerTick: 2, Drops: []network.ItemDrop{{
		ID:         core.DropID{Dimension: core.Overworld, Chunk: target.Chunk(), Generation: 1},
		BlockIndex: blockIndex, Item: core.ItemStone, Count: 1,
	}}}
	inactive := network.PlayerState{ServerTick: 2}
	valid := []network.ServerMessage{delta, upsert, inactive}
	tests := []struct {
		name     string
		messages []network.ServerMessage
		wantErr  bool
	}{
		{name: "规范完成帧", messages: valid},
		{name: "交换 BlockChanges 和 drop", messages: []network.ServerMessage{upsert, delta, inactive}, wantErr: true},
		{name: "重复 drop upsert", messages: []network.ServerMessage{delta, upsert, upsert, inactive}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateMiningCompletionFrame(test.messages, target)
			if (err != nil) != test.wantErr {
				t.Fatalf("validateMiningCompletionFrame error=%v wantErr=%t", err, test.wantErr)
			}
		})
	}
	var hash [32]byte
	hash[0] = 1
	other := hash
	other[0] = 2
	if err := validateMiningMirrorConvergence(hash, 2, other, 2, true); err == nil {
		t.Fatal("authority/mirror hash 偏离未被拒绝")
	}
}

func validateMiningCompletionFrame(messages []network.ServerMessage, target core.BlockPos) error {
	if len(messages) != 3 {
		return fmt.Errorf("采掘完成帧消息数=%d，想要 3: %+v", len(messages), messages)
	}
	delta, ok := messages[0].(network.BlockChanges)
	if !ok {
		return fmt.Errorf("采掘完成帧第一条=%T，想要 BlockChanges", messages[0])
	}
	if err := delta.Validate(); err != nil {
		return fmt.Errorf("采掘 BlockChanges 非法: %w", err)
	}
	if delta.Dimension != core.Overworld || delta.Chunk != target.Chunk() ||
		len(delta.Changes) != 1 || delta.Changes[0] != (network.BlockChange{Position: target, Block: core.AirID}) {
		return fmt.Errorf("采掘 BlockChanges 未精确破坏目标: %+v", delta)
	}
	upserts, ok := messages[1].(network.ItemDropUpserts)
	if !ok {
		return fmt.Errorf("采掘完成帧第二条=%T，想要 ItemDropUpserts", messages[1])
	}
	if err := upserts.Validate(); err != nil {
		return fmt.Errorf("采掘 ItemDropUpserts 非法: %w", err)
	}
	blockIndex, indexed := world.ChunkBlockIndex(target)
	if !indexed || len(upserts.Drops) != 1 || upserts.Drops[0].BlockIndex != blockIndex ||
		upserts.Drops[0].Item != core.ItemStone || upserts.Drops[0].Count != 1 {
		return fmt.Errorf("采掘掉落物不唯一或内容不匹配: %+v", upserts)
	}
	state, ok := messages[2].(network.PlayerState)
	if !ok {
		return fmt.Errorf("采掘完成帧第三条=%T，想要 PlayerState", messages[2])
	}
	if err := state.Validate(); err != nil {
		return fmt.Errorf("采掘 PlayerState 非法: %w", err)
	}
	if state.ServerTick != upserts.ServerTick || canonicalMiningState(state) != (miningTranscriptEntry{}) {
		return fmt.Errorf("采掘完成帧未以同 tick 规范非活动状态收尾: state=%+v upserts=%+v", state, upserts)
	}
	return nil
}

func validateMiningMirrorConvergence(authorityHash [32]byte, authorityRevision uint64, mirrorHash [32]byte, mirrorRevision uint64, loaded bool) error {
	if !loaded {
		return errors.New("采掘 mirror 未加载")
	}
	if mirrorHash != authorityHash || mirrorRevision != authorityRevision {
		return fmt.Errorf("mirror/authority 未收敛: mirror=%x/%d authority=%x/%d",
			mirrorHash, mirrorRevision, authorityHash, authorityRevision)
	}
	return nil
}

type miningTranscriptEntry struct {
	Active        bool
	Target        core.BlockPos
	ProgressTicks uint16
	RequiredTicks uint16
	Harvestable   bool
}

type miningParityResult struct {
	ChunkHash     [32]byte
	ChunkRevision uint64
	InventoryHash [32]byte
	DropHash      [32]byte
	Progress      []miningTranscriptEntry
	FinalInactive miningTranscriptEntry
	Disconnected  bool
}

func runMiningParityScript(t *testing.T, transport string) miningParityResult {
	t.Helper()
	identity := integrationIdentity(0x72, "MiningParity")
	store := storage.NewMemory(storage.Metadata{
		FormatVersion: 1, Seed: 42, SpawnDimension: core.Overworld,
	})
	var inventory core.Inventory
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStonePickaxe, Count: 1}
	inventory.Hotbar.Slots[1] = core.ItemStack{Item: core.ItemIronPickaxe, Count: 1}
	inventory.Hotbar.Slots[2] = core.ItemStack{Item: core.ItemDirt, Count: 1}
	location := storage.PlayerLocation{Dimension: core.Overworld, Position: [3]float32{0.5, 1.001, 0.5}}
	if _, err := store.SavePlayer(context.Background(), storage.PlayerSave{
		PlayerID: identity.PlayerID, Revision: 1, DisplayName: identity.DisplayName,
		Current: location, Safe: &location, Inventory: inventory,
	}); err != nil {
		t.Fatal(err)
	}
	config := hostTestConfig()
	config.ViewRadius = 1
	config.AutosaveTicks = 1000
	host := NewHost(config, miningParityGenerator{}, store)
	endpoint, acceptDone, closeTransport := openParityTransport(t, host, transport, identity)
	defer closeTransport()
	mirror := client.NewMirror()
	drops := client.NewItemDrops()
	inventoryConfirmed := false
	ready := false
	for !ready || !inventoryConfirmed || !parityViewLoaded(mirror) {
		_, messages := parityStep(t, host, endpoint, mirror)
		for _, message := range messages {
			applyMiningParityMessage(t, drops, message)
			switch message := message.(type) {
			case network.PlayerState:
				assertValidIntegrationPlayerState(t, message)
				ready = ready || message.Ready
			case network.InventoryState:
				inventoryConfirmed = message.Inventory == inventory
			}
		}
	}

	step := func(command network.ClientMessage) (network.PlayerState, []network.ServerMessage) {
		if command != nil {
			sendIntegration(t, endpoint, command)
			waitIntegrationCondition(t, fmt.Sprintf("%s mining %T queued", transport, command), func() bool {
				return len(host.world.incoming) > 0
			})
		}
		_, messages := parityStep(t, host, endpoint, mirror)
		var state network.PlayerState
		for _, message := range messages {
			applyMiningParityMessage(t, drops, message)
			if current, ok := message.(network.PlayerState); ok {
				assertValidIntegrationPlayerState(t, current)
				state = current
			}
		}
		return state, messages
	}

	result := miningParityResult{Progress: make([]miningTranscriptEntry, 0, 45)}
	record := func(state network.PlayerState) {
		result.Progress = append(result.Progress, canonicalMiningState(state))
	}
	state, _ := step(network.PlayerInput{Sequence: 1, Pitch: -0.2, Mining: true})
	record(state)
	for range 3 {
		state, _ = step(nil)
		record(state)
	}
	state, _ = step(network.SelectHotbar{Sequence: 2, Slot: 1})
	record(state)
	if state.MiningProgressTicks != 1 || state.MiningRequiredTicks != 8 {
		t.Fatalf("%s 切换铁镐状态 = %+v", transport, state)
	}
	state, _ = step(network.PlayerInput{Sequence: 3, Pitch: -0.2})
	record(state)
	if state.MiningActive {
		t.Fatalf("%s 松键后仍活动: %+v", transport, state)
	}
	state, _ = step(network.SelectHotbar{Sequence: 4, Slot: 2})
	record(state)
	for tick := 1; tick <= 30; tick++ {
		var command network.ClientMessage
		if tick == 1 {
			command = network.PlayerInput{Sequence: 5, Pitch: -0.2, Mining: true}
		}
		var messages []network.ServerMessage
		state, messages = step(command)
		record(state)
		if tick < 30 && (!state.MiningActive || state.MiningProgressTicks != uint16(tick) ||
			state.MiningRequiredTicks != 30 || state.MiningHarvestable) {
			t.Fatalf("%s 错误工具 tick %d = %+v", transport, tick, state)
		}
		if tick == 30 {
			if state.MiningActive || miningParityHasDrop(messages) {
				t.Fatalf("%s 错误工具完成 = state=%+v messages=%+v", transport, state, messages)
			}
		}
	}
	state, _ = step(network.SelectHotbar{Sequence: 6, Slot: 1})
	record(state)
	const sideYaw = -0.16514868
	sideTarget := core.BlockPos{X: 1, Y: 1, Z: -6}
	var completionMessages []network.ServerMessage
	for tick := 1; tick <= 8; tick++ {
		var command network.ClientMessage
		if tick == 1 {
			command = network.PlayerInput{Sequence: 7, Yaw: sideYaw, Pitch: -0.2, Mining: true}
		}
		state, completionMessages = step(command)
		record(state)
		if tick < 8 && (!state.MiningActive || state.MiningProgressTicks != uint16(tick) ||
			state.MiningRequiredTicks != 8 || !state.MiningHarvestable) {
			t.Fatalf("%s 铁镐 tick %d = %+v", transport, tick, state)
		}
	}
	if err := validateMiningCompletionFrame(completionMessages, sideTarget); err != nil {
		t.Fatalf("%s 铁镐完成帧: %v", transport, err)
	}
	if state.MiningActive || len(drops.Presentations()) != 1 {
		t.Fatalf("%s 铁镐完成 = state=%+v drops=%+v", transport, state, drops.Presentations())
	}
	state, _ = step(network.PlayerInput{Sequence: 8, Yaw: sideYaw, Pitch: -0.2})
	result.FinalInactive = canonicalMiningState(state)
	if result.FinalInactive != (miningTranscriptEntry{}) {
		t.Fatalf("%s 最终非活动状态不规范: %+v", transport, result.FinalInactive)
	}

	host.mu.Lock()
	active := *host.activeByPlayer[identity.PlayerID]
	host.mu.Unlock()
	snapshot, ok := host.world.PlayerSnapshotFor(active.Session)
	if !ok {
		t.Fatalf("%s 采掘 player snapshot 不可用", transport)
	}
	result.InventoryHash = inventoryDigest(snapshot.Inventory)
	result.DropHash = itemDropDigest(drops.Presentations())
	authorityHash, authorityRevision, ok := host.world.ChunkHash(core.Overworld, core.ChunkPos{})
	if !ok {
		t.Fatalf("%s 采掘 chunk hash 不可用", transport)
	}
	result.ChunkHash, result.ChunkRevision, ok = mirror.Hash(core.Overworld, core.ChunkPos{})
	if err := validateMiningMirrorConvergence(
		authorityHash, authorityRevision, result.ChunkHash, result.ChunkRevision, ok,
	); err != nil {
		t.Fatalf("%s 采掘镜像收敛: %v", transport, err)
	}
	state, _ = step(network.PlayerInput{Sequence: 9, Yaw: -sideYaw, Pitch: -0.2, Mining: true})
	if !state.MiningActive {
		t.Fatalf("%s 断线前采掘未活动: %+v", transport, state)
	}
	if err := endpoint.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case err := <-acceptDone:
		if err != nil && !errors.Is(err, network.ErrClosed) {
			t.Fatalf("%s mining accept worker: %v", transport, err)
		}
	case <-ctx.Done():
		t.Fatalf("%s mining accept worker did not exit: %v", transport, ctx.Err())
	}
	host.world.StepForTest()
	_, present := host.world.PlayerSnapshotFor(active.Session)
	result.Disconnected = !present
	if present {
		t.Fatalf("%s 活动采掘玩家断线后仍存在", transport)
	}
	if err := host.Shutdown(ctx); err != nil {
		t.Fatalf("%s mining Host.Shutdown: %v", transport, err)
	}
	stored, err := store.LoadPlayer(ctx, identity.PlayerID)
	if err != nil {
		t.Fatalf("%s mining LoadPlayer: %v", transport, err)
	}
	if inventoryDigest(stored.Inventory) != result.InventoryHash {
		t.Fatalf("%s 断线持久化背包与最终快照不一致", transport)
	}
	return result
}

func applyMiningParityMessage(t *testing.T, drops *client.ItemDrops, message network.ServerMessage) {
	t.Helper()
	switch message.(type) {
	case network.ItemDropUpserts, network.ItemDropRemoves:
		if err := drops.Apply(message); err != nil {
			t.Fatalf("采掘掉落镜像 Apply(%T): %v", message, err)
		}
	}
}

func assertValidIntegrationPlayerState(t *testing.T, state network.PlayerState) {
	t.Helper()
	if err := state.Validate(); err != nil {
		t.Fatalf("PlayerState tick=%d 非法: %v", state.ServerTick, err)
	}
}

func canonicalMiningState(state network.PlayerState) miningTranscriptEntry {
	return miningTranscriptEntry{
		Active: state.MiningActive, Target: state.MiningTarget,
		ProgressTicks: state.MiningProgressTicks, RequiredTicks: state.MiningRequiredTicks,
		Harvestable: state.MiningHarvestable,
	}
}

func miningParityHasDrop(messages []network.ServerMessage) bool {
	for _, message := range messages {
		if _, ok := message.(network.ItemDropUpserts); ok {
			return true
		}
	}
	return false
}

func inventoryDigest(inventory core.Inventory) [32]byte {
	var encoded bytes.Buffer
	encoded.WriteByte(inventory.Hotbar.Selected)
	for slot := range core.InventorySlots {
		stack, _ := inventory.Slot(uint8(slot))
		_ = binary.Write(&encoded, binary.LittleEndian, uint16(stack.Item))
		encoded.WriteByte(stack.Count)
	}
	return sha256.Sum256(encoded.Bytes())
}

func itemDropDigest(drops []client.ItemDropPresentation) [32]byte {
	var encoded bytes.Buffer
	for _, drop := range drops {
		_ = binary.Write(&encoded, binary.LittleEndian, int32(drop.ID.Dimension))
		_ = binary.Write(&encoded, binary.LittleEndian, drop.ID.Chunk.X)
		_ = binary.Write(&encoded, binary.LittleEndian, drop.ID.Chunk.Z)
		_ = binary.Write(&encoded, binary.LittleEndian, drop.ID.Slot)
		_ = binary.Write(&encoded, binary.LittleEndian, drop.ID.Generation)
		_ = binary.Write(&encoded, binary.LittleEndian, drop.BlockIndex)
		_ = binary.Write(&encoded, binary.LittleEndian, uint16(drop.Item))
		encoded.WriteByte(drop.Count)
	}
	return sha256.Sum256(encoded.Bytes())
}

type parityResult struct {
	Transcript      []string
	PlayerHash      [32]byte
	ChunkHash       [32]byte
	ChunkRevision   uint64
	MirrorHash      [32]byte
	MirrorRevision  uint64
	Inventory       core.Inventory
	StoredInventory core.Inventory
	DisconnectTick  bool
}

func runParityTranscript(t *testing.T, transport string) parityResult {
	t.Helper()
	identity := integrationIdentity(0x71, "Parity")
	store := storage.NewMemory(storage.Metadata{
		FormatVersion: 1, Seed: 42, SpawnDimension: core.Overworld,
	})
	var initialInventory core.Inventory
	initialInventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 4}
	location := storage.PlayerLocation{Dimension: core.Overworld, Position: [3]float32{0.5, 1.001, 0.5}}
	if _, err := store.SavePlayer(context.Background(), storage.PlayerSave{
		PlayerID: identity.PlayerID, Revision: 1, DisplayName: identity.DisplayName,
		Current: location, Safe: &location, Inventory: initialInventory,
	}); err != nil {
		t.Fatal(err)
	}
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
		network.CraftRecipe{Sequence: 2, Recipe: core.RecipeStoneBricks},
		network.CraftRecipe{Sequence: 3, Recipe: core.RecipeStoneBricks},
		network.PlaceBlock{Sequence: 4, Yaw: 0, Pitch: -0.2, Slot: 0},
	}
	for sequence := uint64(5); sequence < 35; sequence++ {
		commands = append(commands, network.PlayerInput{
			Sequence: sequence, Yaw: 0, Pitch: -0.2, Mining: true,
		})
	}
	commands = append(commands,
		network.RequestChunkResync{
			Sequence: 35, Dimension: core.Overworld,
			Chunk: (core.BlockPos{X: 0, Y: 1, Z: -5}).Chunk(), HaveRevision: 0,
		},
		network.PlayerInput{Sequence: 36, Yaw: 0, Pitch: -0.2},
	)
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
	snapshot, ok := host.world.PlayerSnapshotFor(active.Session)
	if !ok {
		t.Fatal("parity player snapshot unavailable")
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
		Inventory:      snapshot.Inventory,
		DisconnectTick: disconnect.Tick > 0,
	}
	if err := host.Shutdown(ctx); err != nil {
		t.Fatalf("parity Host.Shutdown: %v", err)
	}
	stored, err := store.LoadPlayer(ctx, identity.PlayerID)
	if err != nil {
		t.Fatalf("parity LoadPlayer: %v", err)
	}
	result.StoredInventory = stored.Inventory
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
		case network.ChunkSnapshot, network.KeepAlive, network.InventoryState,
			network.ItemDropUpserts, network.ItemDropRemoves:
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
	case network.InventoryState:
		return []string{fmt.Sprintf("InventoryState:%+v", message.Inventory)}
	case network.KeepAlive, network.Disconnect:
		return nil
	default:
		t.Fatalf("unexpected parity business message %T", message)
		return nil
	}
}

func waitIntegrationInventory(
	t *testing.T,
	connected integrationClient,
	done func(core.Inventory) bool,
) core.Inventory {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		message, err := connected.Endpoint.Recv(ctx)
		if err != nil {
			t.Fatalf("等待物品状态: %v", err)
		}
		applyIntegrationMessage(t, connected.Mirror, message)
		switch message := message.(type) {
		case network.InventoryState:
			if done(message.Inventory) {
				return message.Inventory
			}
		case network.CommandRejected:
			t.Fatalf("物品状态等待期间命令被拒绝: %+v", message)
		}
	}
}

func integrationItemCount(inventory core.Inventory, item core.ItemID) int {
	total := 0
	for slot := range core.InventorySlots {
		stack, _ := inventory.Slot(uint8(slot))
		if stack.Item == item {
			total += int(stack.Count)
		}
	}
	return total
}

func assertCraftingChunkState(
	t *testing.T,
	host integrationHost,
	placed, mined core.BlockPos,
) {
	t.Helper()
	key := core.ChunkKey{Dimension: core.Overworld, Pos: placed.Chunk()}
	chunk, _, ok := host.Host.world.CloneReadyChunkForTest(key)
	if !ok {
		t.Fatalf("石砖区块 %+v 未 Ready", key)
	}
	placedX, _, placedZ := placed.Local()
	minedX, _, minedZ := mined.Local()
	if got := chunk.BlockAt(placedX, placed.Y, placedZ); got != core.StoneBrickID {
		t.Fatalf("保留位置 %+v 方块=%d，想要石砖；回收位置=%+v", placed, got, mined)
	}
	if got := chunk.BlockAt(minedX, mined.Y, minedZ); got != core.AirID {
		t.Fatalf("回收位置方块=%d，想要空气", got)
	}
	for slot := range core.DropsPerChunk {
		if drop := chunk.Drop(slot); drop.Active {
			t.Fatalf("拾取后仍有活动掉落物: slot=%d drop=%+v", slot, drop)
		}
	}
}

func seedV2CraftingChunk(t *testing.T, root string, key core.ChunkKey) {
	t.Helper()
	chunk := integrationChunk(key.Pos, core.StoneID)
	index, ok := world.ChunkBlockIndex(core.BlockPos{X: 0, Y: 1, Z: 0})
	if !ok {
		t.Fatal("测试掉落物位置无区块索引")
	}
	chunk.SetDrop(0, world.DropSlot{
		Generation: 1, Active: true,
		Stack: core.ItemStack{Item: core.ItemStone, Count: 4}, BlockIndex: index,
	})
	store, err := storage.OpenDisk(context.Background(), root, storage.OpenOptions{Create: storage.Metadata{
		FormatVersion: 1, Seed: 42, SpawnDimension: core.Overworld,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveBatch(context.Background(), []storage.ChunkSave{{
		Key: key, Revision: 1, Chunk: chunk,
	}}); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	rewriteIntegrationChunkSchema(t, root, key, 2)
}

func rewriteIntegrationChunkSchema(t *testing.T, root string, key core.ChunkKey, schema uint32) {
	t.Helper()
	data, path, bankOffset, entryOffset, payloadOffset, sectorCount, payloadLength := integrationRegionEntry(t, root, key)
	payload := data[payloadOffset : payloadOffset+payloadLength]
	decodedLength := binary.LittleEndian.Uint32(payload[36:])
	compressedLength := binary.LittleEndian.Uint32(payload[40:])
	decoder, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	logical, err := decoder.DecodeAll(payload[44:44+compressedLength], make([]byte, 0, decodedLength))
	decoder.Close()
	if err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(logical[4:], schema)
	// 逻辑负载末尾是固定长度的熔炉槽；标注为 v4 之前的 schema 时必须一并截掉，
	// 否则旧版本解码会把它们当作尾随字节而拒绝整个区块。
	if schema < 4 {
		logical = logical[:len(logical)-core.FurnacesPerChunk*world.FurnaceSlotBytes]
	}
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1), zstd.WithEncoderCRC(true))
	if err != nil {
		t.Fatal(err)
	}
	compressed := encoder.EncodeAll(logical, nil)
	encoder.Close()
	next := append([]byte(nil), payload[:44]...)
	binary.LittleEndian.PutUint32(next[8:], schema)
	binary.LittleEndian.PutUint32(next[36:], uint32(len(logical)))
	binary.LittleEndian.PutUint32(next[40:], uint32(len(compressed)))
	next = append(next, compressed...)
	if len(next) > sectorCount*4096 {
		t.Fatalf("v%d 测试 payload=%d，超过 extent=%d", schema, len(next), sectorCount*4096)
	}
	clear(data[payloadOffset : payloadOffset+sectorCount*4096])
	copy(data[payloadOffset:], next)
	binary.LittleEndian.PutUint32(data[entryOffset+8:], uint32(len(next)))
	binary.LittleEndian.PutUint32(data[entryOffset+20:], crc32.Checksum(next, crc32.MakeTable(crc32.Castagnoli)))
	bank := data[bankOffset : bankOffset+7*4096]
	binary.LittleEndian.PutUint32(bank[60:], 0)
	binary.LittleEndian.PutUint32(bank[60:], crc32.Checksum(bank, crc32.MakeTable(crc32.Castagnoli)))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func integrationStoredChunkSchema(t *testing.T, root string, key core.ChunkKey) uint32 {
	t.Helper()
	data, _, _, _, payloadOffset, _, payloadLength := integrationRegionEntry(t, root, key)
	if payloadLength < 12 {
		t.Fatalf("区块 payload 过短: %d", payloadLength)
	}
	return binary.LittleEndian.Uint32(data[payloadOffset+8:])
}

func integrationRegionEntry(
	t *testing.T,
	root string,
	key core.ChunkKey,
) (data []byte, path string, bankOffset, entryOffset, payloadOffset, sectorCount, payloadLength int) {
	t.Helper()
	region, slot := storage.RegionFor(key)
	path = filepath.Join(root, "dimensions", fmt.Sprint(region.Dimension), "regions", fmt.Sprintf("r.%d.%d.region", region.X, region.Z))
	var err error
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	bestGeneration := uint64(0)
	for _, start := range []int{1 * 4096, 8 * 4096} {
		entry := start + 64 + slot*24
		offset := int(binary.LittleEndian.Uint32(data[entry:])) * 4096
		generation := binary.LittleEndian.Uint64(data[start+24:])
		if offset == 0 || generation < bestGeneration {
			continue
		}
		bestGeneration = generation
		bankOffset = start
		entryOffset = entry
		payloadOffset = offset
		sectorCount = int(binary.LittleEndian.Uint32(data[entry+4:]))
		payloadLength = int(binary.LittleEndian.Uint32(data[entry+8:]))
	}
	if payloadOffset == 0 || payloadLength == 0 {
		t.Fatalf("区块 %+v 没有活动 region entry", key)
	}
	return
}

// TestFurnaceSharedByTwoPlayersOverTCP 覆盖两名玩家共享同一个熔炉的完整闭环：
// 同时打开、交错投料、推进出锭、一人取走后另一人看到输出为空，旧引用被拒绝。
func TestFurnaceSharedByTwoPlayersOverTCP(t *testing.T) {
	root := t.TempDir()
	key := core.ChunkKey{Dimension: core.Overworld}
	firstIdentity := integrationIdentity(0x91, "Smelter")
	secondIdentity := integrationIdentity(0x92, "Helper")
	spawn := integrationPlayerSnapshotAt(0.5, 1.001, 0.5, nil)
	seedIntegrationPlayer(t, root, firstIdentity, spawn)
	seedIntegrationPlayer(t, root, secondIdentity, spawn)

	host := startDiskHost(t, root, "127.0.0.1:0", changedGenerator{})
	firstClient := dialIntegrationClient(t, host.Addr, firstIdentity)
	secondClient := dialIntegrationClient(t, host.Addr, secondIdentity)
	waitClientReadyFor(t, host, firstClient, firstIdentity.PlayerID)
	waitClientReadyFor(t, host, secondClient, secondIdentity.PlayerID)

	index, ok := world.ChunkBlockIndex(core.BlockPos{})
	if !ok {
		t.Fatal("熔炉位置没有区块索引")
	}
	host.Host.world.SetBlockForTest(core.BlockPos{}, core.FurnaceID)
	host.Host.world.SetChunkFurnaceForTest(key, 0, world.FurnaceSlot{
		Generation: 1, Active: true, BlockIndex: index,
		Input: core.ItemStack{Item: core.ItemRawIron, Count: 2},
		Fuel:  core.ItemStack{Item: core.ItemCoal, Count: 1},
	})

	// 两名玩家同时打开同一个熔炉。
	sendIntegration(t, firstClient.Endpoint, network.OpenFurnace{
		Sequence: 10, Pitch: -float32(math.Pi)/2 + 0.01,
	})
	sendIntegration(t, secondClient.Endpoint, network.OpenFurnace{
		Sequence: 10, Pitch: -float32(math.Pi)/2 + 0.01,
	})
	firstRef := waitFurnaceState(t, firstClient, func(network.FurnaceState) bool { return true })
	secondRef := waitFurnaceState(t, secondClient, func(network.FurnaceState) bool { return true })
	if firstRef.Furnace != secondRef.Furnace {
		t.Fatalf("两名查看者的引用不同: %+v vs %+v", firstRef.Furnace, secondRef.Furnace)
	}

	// 推进到产出第一个铁锭，两端必须同时看到。
	produced := func(state network.FurnaceState) bool {
		return state.Output.Item == core.ItemIronIngot && state.Output.Count >= 1
	}
	waitFurnaceState(t, firstClient, produced)
	waitFurnaceState(t, secondClient, produced)

	// 第一名玩家取走输出后，另一名玩家必须看到输出为空。
	sendIntegration(t, firstClient.Endpoint, network.MoveFurnaceStack{
		Sequence: 11, Furnace: firstRef.Furnace,
		From: core.FurnaceOutputSlot, To: 0,
	})
	emptied := func(state network.FurnaceState) bool {
		return state.Output.Item == core.ItemNone
	}
	waitFurnaceState(t, secondClient, emptied)

	// 旧 generation 的引用必须稳定拒绝。
	stale := firstRef.Furnace
	stale.Generation++
	sendIntegration(t, secondClient.Endpoint, network.MoveFurnaceStack{
		Sequence: 12, Furnace: stale, From: 0, To: core.FurnaceInputSlot,
	})
	waitIntegrationRejection(t, secondClient, 12, network.RejectInvalidInput)

	if err := firstClient.Close(); err != nil {
		t.Fatal(err)
	}
	if err := secondClient.Close(); err != nil {
		t.Fatal(err)
	}
	host.Shutdown(t)
}

// waitFurnaceState 等待某个客户端收到满足条件的熔炉状态。
func waitFurnaceState(
	t *testing.T,
	connected integrationClient,
	accept func(network.FurnaceState) bool,
) network.FurnaceState {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for {
		message, err := connected.Endpoint.Recv(ctx)
		if err != nil {
			t.Fatalf("等待熔炉状态: %v", err)
		}
		applyIntegrationMessage(t, connected.Mirror, message)
		if state, ok := message.(network.FurnaceState); ok && accept(state) {
			return state
		}
	}
}

// waitIntegrationRejection 等待某个客户端收到指定序号的拒绝。
func waitIntegrationRejection(
	t *testing.T,
	connected integrationClient,
	sequence uint64,
	reason network.RejectReason,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for {
		message, err := connected.Endpoint.Recv(ctx)
		if err != nil {
			t.Fatalf("等待拒绝: %v", err)
		}
		applyIntegrationMessage(t, connected.Mirror, message)
		if rejected, ok := message.(network.CommandRejected); ok && rejected.Sequence == sequence {
			if rejected.Reason != reason {
				t.Fatalf("拒绝原因 = %q，想要 %q", rejected.Reason, reason)
			}
			return
		}
	}
}
