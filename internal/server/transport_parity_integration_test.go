package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/sim"
	"github.com/channing771/mornlea/internal/storage"
	"github.com/channing771/mornlea/internal/world"
)

type miningParityGenerator struct{}

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
	full, _ := core.ItemMaxDurability(core.ItemIronPickaxe)
	var hotbar core.Hotbar
	hotbar.Selected = 1
	hotbar.Slots[1] = core.ItemStack{Item: core.ItemIronPickaxe, Count: 1, Durability: full - 1}
	inventory := network.InventoryState{Inventory: core.Inventory{Hotbar: hotbar}}
	wrongDurability := inventory
	wrongDurability.Inventory.Hotbar.Slots[1].Durability--
	inactive := network.PlayerState{ServerTick: 2}
	valid := []network.ServerMessage{delta, upsert, inventory, inactive}
	tests := []struct {
		name     string
		messages []network.ServerMessage
		wantErr  bool
	}{
		{name: "规范完成帧", messages: valid},
		{name: "交换 BlockChanges 和 drop", messages: []network.ServerMessage{upsert, delta, inventory, inactive}, wantErr: true},
		{name: "重复 drop upsert", messages: []network.ServerMessage{delta, upsert, upsert, inventory, inactive}, wantErr: true},
		{name: "缺少 InventoryState", messages: []network.ServerMessage{delta, upsert, inactive}, wantErr: true},
		{name: "工具耐久错误", messages: []network.ServerMessage{delta, upsert, wrongDurability, inactive}, wantErr: true},
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
	if len(messages) != 4 {
		return fmt.Errorf("采掘完成帧消息数=%d，想要 4: %+v", len(messages), messages)
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
	inventory, ok := messages[2].(network.InventoryState)
	if !ok {
		return fmt.Errorf("采掘完成帧第三条=%T，想要 InventoryState", messages[2])
	}
	if err := inventory.Validate(); err != nil {
		return fmt.Errorf("采掘 InventoryState 非法: %w", err)
	}
	full, _ := core.ItemMaxDurability(core.ItemIronPickaxe)
	hotbar := inventory.Inventory.Hotbar
	if hotbar.Selected != 1 || hotbar.Slots[1] != (core.ItemStack{
		Item: core.ItemIronPickaxe, Count: 1, Durability: full - 1,
	}) {
		return fmt.Errorf("采掘 InventoryState 未精确扣减选中铁镐耐久: %+v", inventory)
	}
	state, ok := messages[3].(network.PlayerState)
	if !ok {
		return fmt.Errorf("采掘完成帧第四条=%T，想要 PlayerState", messages[3])
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
		FormatVersion: 2, Seed: 42, SpawnDimension: core.Overworld,
	})
	stoneFull, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	ironFull, _ := core.ItemMaxDurability(core.ItemIronPickaxe)
	var inventory core.Inventory
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: stoneFull}
	inventory.Hotbar.Slots[1] = core.ItemStack{Item: core.ItemIronPickaxe, Count: 1, Durability: ironFull}
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
	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
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
		FormatVersion: 2, Seed: 42, SpawnDimension: core.Overworld,
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
	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
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
	// 两次运行在脚本开始前的 tick 数不同，绝对世界时间不属于业务对等内容；
	// 同一服务端上多客户端的时间一致性由多人昼夜测试覆盖。
	lastState.WorldTimeTicks = 0
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
		ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
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
	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
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
	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
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
		message.WorldTimeTicks = 0
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
