package server

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/assets"
	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/mesh"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/storage"
)

type staticBlockLightResult struct {
	BeforeLight   uint8
	PlacedLight   uint8
	RemovedLight  uint8
	ChunkHash     [32]byte
	ChunkRevision uint64
	InventoryHash [32]byte
	DropHash      [32]byte
}

func TestStaticBlockLightMemoryTCPParity(t *testing.T) {
	memory := runStaticBlockLightScript(t, "memory")
	tcp := runStaticBlockLightScript(t, "tcp")
	if !reflect.DeepEqual(memory, tcp) {
		t.Fatalf("Memory/TCP 方块光结果不一致\nmemory=%+v\ntcp=%+v", memory, tcp)
	}
	t.Logf("Memory=%+v\nTCP=%+v", memory, tcp)
	if memory.BeforeLight != 0 || memory.PlacedLight == 0 || memory.RemovedLight != 0 {
		t.Fatalf("方块光生命周期=%+v", memory)
	}
}

func TestStaticBlockLightMissingFaceIsNotDark(t *testing.T) {
	light, found := meshedBlockLight(nil, core.BlockPos{X: 1, Y: 0, Z: -5})
	if found {
		t.Fatalf("缺失可见面被误报为方块光 %d", light)
	}
}

func runStaticBlockLightScript(t *testing.T, transport string) staticBlockLightResult {
	t.Helper()
	identity := integrationIdentity(0x73, "BlockLightParity")
	store := storage.NewMemory(storage.Metadata{
		FormatVersion: 2, Seed: 42, SpawnDimension: core.Overworld,
	})
	stoneFull, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	var inventory core.Inventory
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemLightBlock, Count: 1}
	inventory.Hotbar.Slots[1] = core.ItemStack{
		Item: core.ItemStonePickaxe, Count: 1, Durability: stoneFull,
	}
	location := storage.PlayerLocation{
		Dimension: core.Overworld,
		Position:  [3]float32{0.5, 1.001, 0.5},
	}
	if _, err := store.SavePlayer(context.Background(), wellFedPlayerSave(storage.PlayerSave{
		PlayerID: identity.PlayerID, Revision: 1, DisplayName: identity.DisplayName,
		Current: location, Safe: &location, Inventory: inventory,
	})); err != nil {
		t.Fatal(err)
	}

	config := hostTestConfig()
	config.ViewRadius = 1
	config.AutosaveTicks = 1000
	host := mustNewHost(t, config, flatGenerator{}, store)
	endpoint, acceptDone, closeTransport := openParityTransport(t, host, transport, identity)
	t.Cleanup(func() {
		_ = endpoint.Close()
		ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
		defer cancel()
		_ = host.Shutdown(ctx)
		closeTransport()
	})

	transportMirror := client.NewMirror()
	lightMirror := client.NewMirror()
	drops := client.NewItemDrops()
	mesher := client.NewMesher(assets.NewRegistry(), 1)
	t.Cleanup(mesher.Close)
	placed := core.BlockPos{X: 0, Y: 1, Z: -5}
	sampled := core.BlockPos{X: 1, Y: 0, Z: -5}
	sampleKey := staticBlockLightSectionKey(sampled)

	applyMessages := func(messages []network.ServerMessage) {
		applyStaticBlockLightMessages(t, lightMirror, mesher, drops, sampleKey, messages)
	}
	step := func(command network.ClientMessage) (network.PlayerState, []network.ServerMessage) {
		if command != nil {
			sendIntegration(t, endpoint, command)
			waitIntegrationCondition(t, fmt.Sprintf("%s block light %T queued", transport, command), func() bool {
				return len(host.world.incoming) > 0
			})
		}
		_, messages := parityStep(t, host, endpoint, transportMirror)
		applyMessages(messages)
		for _, message := range messages {
			if state, ok := message.(network.PlayerState); ok {
				assertValidIntegrationPlayerState(t, state)
				return state, messages
			}
		}
		t.Fatalf("%s 方块光 tick 缺少 PlayerState: %+v", transport, messages)
		return network.PlayerState{}, nil
	}

	ready := false
	inventoryReady := false
	for !ready || !inventoryReady || !parityViewLoaded(lightMirror) {
		state, messages := step(nil)
		ready = ready || state.Ready
		if current, ok := staticBlockLightInventory(messages); ok && current == inventory {
			inventoryReady = true
		}
	}
	beforeRevision := staticBlockLightRevision(t, lightMirror, placed.Chunk())
	result := staticBlockLightResult{
		BeforeLight: awaitStaticBlockLight(t, mesher, lightMirror, sampled, beforeRevision, "放置前"),
	}

	_, placementMessages := step(network.PlaceBlock{
		Sequence: 1, Yaw: 0, Pitch: -0.2, Slot: 0,
	})
	if !staticBlockLightChangeSeen(placementMessages, placed, core.LightBlockID) {
		t.Fatalf("%s 放置帧未出现目标发光块: %+v", transport, placementMessages)
	}
	placedInventory := inventory
	placedInventory.Hotbar.Slots[0] = core.ItemStack{}
	if current, ok := staticBlockLightInventory(placementMessages); !ok || current != placedInventory {
		t.Fatalf("%s 放置未原子扣减发光块: got=%+v ok=%t want=%+v", transport, current, ok, placedInventory)
	}
	if block, loaded := lightMirror.BlockAt(core.Overworld, placed); !loaded || block != core.LightBlockID {
		t.Fatalf("%s 放置后镜像方块=(%d,%t)，想要 (%d,true)", transport, block, loaded, core.LightBlockID)
	}
	placedRevision := staticBlockLightRevision(t, lightMirror, placed.Chunk())
	result.PlacedLight = awaitStaticBlockLight(
		t, mesher, lightMirror, sampled, placedRevision, "放置后",
	)

	_, selectionMessages := step(network.SelectHotbar{Sequence: 2, Slot: 1})
	selectedInventory := placedInventory
	selectedInventory.Hotbar.Selected = 1
	if current, ok := staticBlockLightInventory(selectionMessages); !ok || current != selectedInventory {
		t.Fatalf("%s 切槽状态: got=%+v ok=%t want=%+v", transport, current, ok, selectedInventory)
	}

	var completionMessages []network.ServerMessage
	for tick := 1; tick <= 15; tick++ {
		var command network.ClientMessage
		if tick == 1 {
			command = network.PlayerInput{Sequence: 3, Yaw: 0, Pitch: -0.2, Mining: true}
		}
		state, messages := step(command)
		completionMessages = messages
		if tick < 15 {
			if !state.MiningActive || state.MiningTarget != placed ||
				state.MiningProgressTicks != uint16(tick) ||
				state.MiningRequiredTicks != 15 || !state.MiningHarvestable {
				t.Fatalf("%s 发光块采掘 tick %d: %+v", transport, tick, state)
			}
		} else if state.MiningActive {
			t.Fatalf("%s 发光块 15 tick 后仍在采掘: %+v", transport, state)
		}
	}
	if !staticBlockLightChangeSeen(completionMessages, placed, core.AirID) {
		t.Fatalf("%s 采掘完成帧未移除发光块: %+v", transport, completionMessages)
	}
	minedInventory := selectedInventory
	minedInventory.Hotbar.Slots[1].Durability = stoneFull - 1
	if current, ok := staticBlockLightInventory(completionMessages); !ok || current != minedInventory {
		t.Fatalf("%s 采掘未精确扣减石镐耐久: got=%+v ok=%t want=%+v", transport, current, ok, minedInventory)
	}
	presentations := drops.Presentations()
	if len(presentations) != 1 || presentations[0].Item != core.ItemLightBlock ||
		presentations[0].Count != 1 || presentations[0].Durability != 0 {
		t.Fatalf("%s 发光块掉落不唯一或内容错误: %+v", transport, presentations)
	}
	if block, loaded := lightMirror.BlockAt(core.Overworld, placed); !loaded || block != core.AirID {
		t.Fatalf("%s 挖回后镜像方块=(%d,%t)，想要 (%d,true)", transport, block, loaded, core.AirID)
	}
	removedRevision := staticBlockLightRevision(t, lightMirror, placed.Chunk())
	result.RemovedLight = awaitStaticBlockLight(
		t, mesher, lightMirror, sampled, removedRevision, "挖回后",
	)

	host.mu.Lock()
	active := *host.activeByPlayer[identity.PlayerID]
	host.mu.Unlock()
	snapshot, ok := host.world.PlayerSnapshotFor(active.Session)
	if !ok {
		t.Fatalf("%s 方块光 player snapshot 不可用", transport)
	}
	if snapshot.Inventory != minedInventory {
		t.Fatalf("%s 最终权威背包=%+v，想要 %+v", transport, snapshot.Inventory, minedInventory)
	}
	result.InventoryHash = inventoryDigest(snapshot.Inventory)
	result.DropHash = itemDropDigest(presentations)
	authorityHash, authorityRevision, ok := host.world.ChunkHash(core.Overworld, placed.Chunk())
	if !ok {
		t.Fatalf("%s 方块光 authority chunk 不可用", transport)
	}
	result.ChunkHash, result.ChunkRevision, ok = lightMirror.Hash(core.Overworld, placed.Chunk())
	if err := validateMiningMirrorConvergence(
		authorityHash, authorityRevision, result.ChunkHash, result.ChunkRevision, ok,
	); err != nil {
		t.Fatalf("%s 方块光镜像收敛: %v", transport, err)
	}

	mesher.Close()
	if err := endpoint.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
	defer cancel()
	select {
	case err := <-acceptDone:
		if err != nil && !errors.Is(err, network.ErrClosed) {
			t.Fatalf("%s 方块光 accept worker: %v", transport, err)
		}
	case <-ctx.Done():
		t.Fatalf("%s 方块光 accept worker 未退出: %v", transport, ctx.Err())
	}
	host.world.StepForTest()
	if _, present := host.world.PlayerSnapshotFor(active.Session); present {
		t.Fatalf("%s 方块光玩家断线后仍存在", transport)
	}
	if err := host.Shutdown(ctx); err != nil {
		t.Fatalf("%s 方块光 Host.Shutdown: %v", transport, err)
	}
	storedPlayer, err := store.LoadPlayer(ctx, identity.PlayerID)
	if err != nil {
		t.Fatalf("%s 方块光 LoadPlayer: %v", transport, err)
	}
	if storedPlayer.Inventory != minedInventory || inventoryDigest(storedPlayer.Inventory) != result.InventoryHash {
		t.Fatalf("%s 持久化背包未收敛: %+v", transport, storedPlayer.Inventory)
	}
	storedChunk, err := store.LoadChunk(ctx, core.ChunkKey{
		Dimension: core.Overworld, Pos: placed.Chunk(),
	})
	if err != nil {
		t.Fatalf("%s 方块光 LoadChunk: %v", transport, err)
	}
	if storedChunk.Revision != result.ChunkRevision || storedChunk.Chunk.Hash() != result.ChunkHash {
		t.Fatalf("%s 持久化区块未收敛: hash=%x revision=%d", transport, storedChunk.Chunk.Hash(), storedChunk.Revision)
	}
	storedDrops := 0
	for slot := 0; slot < core.DropsPerChunk; slot++ {
		drop := storedChunk.Chunk.Drop(slot)
		if !drop.Active {
			continue
		}
		storedDrops++
		if drop.Stack != (core.ItemStack{Item: core.ItemLightBlock, Count: 1}) {
			t.Fatalf("%s 持久化掉落错误: slot=%d drop=%+v", transport, slot, drop)
		}
	}
	if storedDrops != 1 {
		t.Fatalf("%s 持久化发光块掉落数量=%d，想要 1", transport, storedDrops)
	}
	closeTransport()
	return result
}

func applyStaticBlockLightMessages(
	t *testing.T,
	mirror *client.Mirror,
	mesher *client.Mesher,
	drops *client.ItemDrops,
	sampleKey core.SectionKey,
	messages []network.ServerMessage,
) {
	t.Helper()
	for _, message := range messages {
		switch message.(type) {
		case network.ChunkSnapshot, network.BlockChanges, network.ForgetChunks, network.CommandRejected:
			update, err := mirror.Apply(message)
			if err != nil {
				t.Fatalf("方块光 Mirror.Apply(%T): %v", message, err)
			}
			if update.Resync != nil {
				t.Fatalf("方块光 mirror 意外 resync: %+v", *update.Resync)
			}
			if update.Rejected != nil {
				t.Fatalf("方块光命令被拒绝: %+v", *update.Rejected)
			}
			for _, key := range update.Forgotten {
				if key == sampleKey {
					mesher.ForgetChunk(key.Dimension, core.ChunkPos{X: key.Pos.X, Z: key.Pos.Z})
				}
			}
			for _, key := range update.Dirty {
				if key == sampleKey {
					mesher.MarkDirty(key)
				}
			}
		case network.ItemDropUpserts, network.ItemDropRemoves:
			if err := drops.Apply(message); err != nil {
				t.Fatalf("方块光掉落镜像 Apply(%T): %v", message, err)
			}
		}
	}
}

func awaitStaticBlockLight(
	t *testing.T,
	mesher *client.Mesher,
	mirror *client.Mirror,
	target core.BlockPos,
	revision uint64,
	stage string,
) uint8 {
	t.Helper()
	key := staticBlockLightSectionKey(target)
	deadline := time.Now().Add(waitDeadline)
	for {
		mesher.Schedule(mirror, 1)
		for _, section := range mesher.Drain(mirror, 1) {
			if section.Dimension != key.Dimension || section.Pos != key.Pos {
				continue
			}
			stamped := false
			for _, stamp := range section.Stamps {
				if stamp.Dimension == key.Dimension && stamp.Chunk == target.Chunk() {
					if !stamp.Present || stamp.Revision != revision {
						t.Fatalf("%s 网格 revision=%+v，想要 %d", stage, stamp, revision)
					}
					stamped = true
					break
				}
			}
			if !stamped {
				t.Fatalf("%s 网格缺少目标区块 stamp: %+v", stage, section.Stamps)
			}
			light, found := meshedBlockLight(section.Quads, target)
			if !found {
				t.Fatalf("%s 找不到覆盖 %+v 的可见 +Y 面", stage, target)
			}
			return light
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待%s网格超时: key=%+v revision=%d stats=%+v", stage, key, revision, mesher.Stats())
		}
		time.Sleep(integrationPollInterval)
	}
}

func meshedBlockLight(quads []mesh.Quad, target core.BlockPos) (uint8, bool) {
	lx, ly, lz := target.Local()
	for _, quad := range quads {
		if quad.Face != mesh.FacePosY || int(quad.Y) != ly {
			continue
		}
		if int(quad.X) <= lx && lx < int(quad.X)+int(quad.H) &&
			int(quad.Z) <= lz && lz < int(quad.Z)+int(quad.W) {
			return quad.Light & 0x0f, true
		}
	}
	return 0, false
}

func staticBlockLightSectionKey(position core.BlockPos) core.SectionKey {
	return core.SectionKey{Dimension: core.Overworld, Pos: position.Section()}
}

func staticBlockLightRevision(t *testing.T, mirror *client.Mirror, position core.ChunkPos) uint64 {
	t.Helper()
	_, revision, ok := mirror.Hash(core.Overworld, position)
	if !ok {
		t.Fatalf("方块光镜像缺少区块 %+v", position)
	}
	return revision
}

func staticBlockLightChangeSeen(
	messages []network.ServerMessage,
	position core.BlockPos,
	block core.BlockID,
) bool {
	for _, message := range messages {
		changes, ok := message.(network.BlockChanges)
		if !ok {
			continue
		}
		for _, change := range changes.Changes {
			if change.Position == position && change.Block == block {
				return true
			}
		}
	}
	return false
}

func staticBlockLightInventory(messages []network.ServerMessage) (core.Inventory, bool) {
	for index := len(messages) - 1; index >= 0; index-- {
		if state, ok := messages[index].(network.InventoryState); ok {
			return state.Inventory, true
		}
	}
	return core.Inventory{}, false
}
