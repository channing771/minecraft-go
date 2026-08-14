package server

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/klauspost/compress/zstd"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/sim"
	"github.com/channing771/mornlea/internal/storage"
	"github.com/channing771/mornlea/internal/world"
)

type controlledInteractionGenerator struct {
	started chan<- struct{}
	release <-chan struct{}
}

func (generator controlledInteractionGenerator) GenerateChunk(position core.ChunkPos) *world.Chunk {
	if position == (core.ChunkPos{Z: -1}) {
		select {
		case generator.started <- struct{}{}:
		default:
		}
		<-generator.release
	}
	return integrationChunk(position, core.DirtID)
}

func waitClientReady(t *testing.T, host integrationHost, connected integrationClient) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), longWaitDeadline)
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
	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
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
	stoneFull, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	spawn.Inventory.Hotbar.Slots[1] = core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: stoneFull}
	seedIntegrationPlayer(t, root, firstIdentity, spawn)
	seedIntegrationPlayer(t, root, secondIdentity, integrationPlayerSnapshotAt(0.5, 1.001, 0.5, nil))

	firstStarted := make(chan struct{}, 1)
	firstRelease := make(chan struct{})
	var firstReleaseOnce sync.Once
	firstHost := startDiskHost(t, root, "127.0.0.1:0", controlledInteractionGenerator{
		started: firstStarted,
		release: firstRelease,
	})
	t.Cleanup(func() { firstReleaseOnce.Do(func() { close(firstRelease) }) })
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
	interaction := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{Z: -1}}
	waitIntegrationCondition(t, "首次交互区块生成开始", func() bool {
		select {
		case <-firstStarted:
			return true
		default:
			return false
		}
	})
	if _, _, ready := firstHost.Host.world.CloneReadyChunkForTest(interaction); ready {
		t.Fatal("首次受控交互区块在释放生成前已经 Ready")
	}
	waitIntegrationCondition(t, "首次合成重启交互区块 Ready", func() bool {
		firstReleaseOnce.Do(func() { close(firstRelease) })
		_, _, ready := firstHost.Host.world.CloneReadyChunkForTest(interaction)
		return ready
	})

	placed := make([]core.BlockPos, 0, 2)
	for sequence := uint64(2); sequence <= 3; sequence++ {
		sendIntegration(t, firstClient.Endpoint, network.PlaceBlock{
			Sequence: sequence, Yaw: 0, Pitch: -0.2, Slot: 0,
		})
		waitIntegrationState(t, firstClient, func(message network.ServerMessage) bool {
			if rejected, ok := message.(network.CommandRejected); ok {
				t.Fatalf("放置 sequence=%d 被拒绝: %+v", sequence, rejected)
			}
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
	if schema := integrationStoredChunkSchema(t, root, key); schema != 8 {
		t.Fatalf("正常刷新后的区块 schema=%d，想要 8", schema)
	}

	secondHost := startDiskHost(t, root, "127.0.0.1:0", flatGenerator{})
	reconnectedWitness := dialIntegrationClient(t, secondHost.Addr, secondIdentity)
	waitClientReadyFor(t, secondHost, reconnectedWitness, secondIdentity.PlayerID)
	reconnected := dialIntegrationClient(t, secondHost.Addr, firstIdentity)
	waitClientReadyFor(t, secondHost, reconnected, firstIdentity.PlayerID)
	waitIntegrationCondition(t, "重启后合成交互区块 Ready", func() bool {
		_, _, ready := secondHost.Host.world.CloneReadyChunkForTest(interaction)
		return ready
	})
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

		for _, version := range []byte{7, 8, 9, 10, byte(network.ProtocolVersion + 1)} {
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
	host := mustNewHost(t, config, generator, store)
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

func loginIntegrationClient(address string, identity network.Identity) (network.ClientEndpoint, error) {
	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
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

func waitIntegrationInventory(
	t *testing.T,
	connected integrationClient,
	done func(core.Inventory) bool,
) core.Inventory {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), longWaitDeadline)
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
		FormatVersion: 2, Seed: 42, SpawnDimension: core.Overworld,
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
	// 逻辑负载末尾是固定长度的箱子槽；标注为 v5 之前的 schema 时必须先截掉，
	// 否则后续基于“末尾是熔炉+掉落物”的偏移计算会算错位置。
	if schema < 6 {
		logical = logical[:len(logical)-core.ChestsPerChunk*world.ChestSlotBytes]
	}
	// schema v4 及更早的掉落物槽没有 Durability；当前 v5 槽的 offset 8:10
	// 是耐久字段，按固定槽顺序原地删除后才能交给旧 decoder。
	if schema < 5 {
		furnaceBytes := core.FurnacesPerChunk * world.FurnaceSlotBytes
		dropBytes := core.DropsPerChunk * world.DropSlotBytes
		dropStart := len(logical) - furnaceBytes - dropBytes
		write := dropStart
		for slot := range core.DropsPerChunk {
			start := dropStart + slot*world.DropSlotBytes
			write += copy(logical[write:], logical[start:start+8])
			write += copy(logical[write:], logical[start+10:start+world.DropSlotBytes])
		}
		write += copy(logical[write:], logical[dropStart+dropBytes:])
		logical = logical[:write]
	}
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
