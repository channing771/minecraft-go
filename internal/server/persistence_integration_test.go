package server_test

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"minecraft-go/internal/client"
	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/server"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/storage"
	"minecraft-go/internal/world"
)

func TestWorldPersistsAcrossRestartAndGeneratorUpgrade(t *testing.T) {
	root := filepath.Join(t.TempDir(), "world")
	generatorA := newCountingPersistenceGenerator(core.StoneID)
	first := newPersistentHarness(t, root, generatorA, false, nil)
	first.waitReady()
	first.placeAndBreakScript()
	wantHash, wantRevision := first.authoritativeChunk(interactionPersistenceChunk)
	visitedByA := generatorA.calledPositions()
	if len(visitedByA) != 9 {
		t.Fatalf("generator A visited=%v，想要 view radius 1 的 9 个区块", visitedByA)
	}
	if err := first.shutdown(); err != nil {
		t.Fatalf("first Shutdown: %v", err)
	}

	generatorB := newCountingPersistenceGenerator(core.DirtID)
	second := newPersistentHarness(t, root, generatorB, false, nil)
	second.waitReady()
	gotHash, gotRevision := second.authoritativeChunk(interactionPersistenceChunk)
	if gotHash != wantHash || gotRevision != wantRevision {
		t.Fatalf(
			"restart=(%x,%d), want=(%x,%d)",
			gotHash,
			gotRevision,
			wantHash,
			wantRevision,
		)
	}
	second.assertMirrorMatches(interactionPersistenceChunk)
	if calls := generatorB.totalCalls(); calls != 0 {
		t.Fatalf("generator B 在已探索启动视图调用 %d 次: %v", calls, generatorB.calledPositions())
	}
	second.moveViewToUnvisitedChunk()
	for _, position := range generatorB.calledPositions() {
		if containsChunkPos(visitedByA, position) {
			t.Fatalf("generator B 为已探索区块 %+v 重新生成；A visited=%v", position, visitedByA)
		}
	}
	if err := second.shutdown(); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
}

func TestWorldPersistsDirtyUnloadReload(t *testing.T) {
	root := filepath.Join(t.TempDir(), "world")
	generator := newCountingPersistenceGenerator(core.StoneID)
	harness := newPersistentHarness(t, root, generator, true, nil)
	harness.setTrustedCenter(core.ChunkPos{})
	harness.waitTrustedReady(core.ChunkPos{})
	wantHash, wantRevision := harness.authoritativeChunk(core.ChunkPos{})

	harness.setTrustedCenter(core.ChunkPos{X: 3})
	harness.stepUntil(func() bool {
		_, present := harness.running.ChunkInfo(core.Overworld, core.ChunkPos{})
		_, mirrored := harness.mirror.Chunk(core.Overworld, core.ChunkPos{})
		return !present && !mirrored
	})

	harness.setTrustedCenter(core.ChunkPos{})
	harness.waitTrustedReady(core.ChunkPos{})
	gotHash, gotRevision := harness.authoritativeChunk(core.ChunkPos{})
	if gotHash != wantHash || gotRevision != wantRevision {
		t.Fatalf("dirty unload/reload=(%x,%d), want=(%x,%d)", gotHash, gotRevision, wantHash, wantRevision)
	}
	if calls := generator.callsFor(core.ChunkPos{}); calls != 1 {
		t.Fatalf("origin generation calls=%d，想要 unload 后从 Store reload", calls)
	}
	if err := harness.shutdown(); err != nil {
		t.Fatal(err)
	}
}

func TestWorldPersistsSavedRewriteBecomesClean(t *testing.T) {
	root := filepath.Join(t.TempDir(), "world")
	key := persistenceChunkKey(core.ChunkPos{})
	seedChunk(t, root, storage.ChunkSave{
		Key:      key,
		Revision: 7,
		Chunk:    persistenceChunk(core.ChunkPos{}, core.StoneID),
	})

	var rewrite *migrationRewriteStore
	harness := newPersistentHarness(
		t,
		root,
		newCountingPersistenceGenerator(core.DirtID),
		true,
		func(store storage.Store) storage.Store {
			rewrite = &migrationRewriteStore{Store: store, key: key}
			return rewrite
		},
	)
	harness.setTrustedCenter(core.ChunkPos{})
	harness.waitTrustedReady(core.ChunkPos{})
	if status := harness.running.PersistenceStatus(); status.DirtyChunks != 1 {
		t.Fatalf("rewrite load persistence status=%+v，想要 dirty rewrite", status)
	}
	if err := harness.shutdown(); err != nil {
		t.Fatal(err)
	}
	if rewrite.saveCalls() == 0 {
		t.Fatal("NeedsRewrite chunk 没有经过 Server 保存/确认路径")
	}

	store := openPersistentDiskStore(t, root)
	stored, err := store.LoadChunk(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Revision != 7 || stored.PersistedRevision != 7 || stored.NeedsRewrite || stored.Recovered {
		t.Fatalf("reopened rewrite=%+v，想要 clean committed revision 7", stored)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCorruptStoredChunkRecoversAndPromotesRevision(t *testing.T) {
	root := filepath.Join(t.TempDir(), "world")
	key := persistenceChunkKey(core.ChunkPos{})
	old := persistenceChunk(core.ChunkPos{}, core.StoneID)
	newer := persistenceChunk(core.ChunkPos{}, core.DirtID)
	seedChunk(t, root,
		storage.ChunkSave{Key: key, Revision: 1, Chunk: old},
		storage.ChunkSave{Key: key, Revision: 2, Chunk: newer},
	)
	entries := regionEntriesForChunk(t, root, key)
	if len(entries) != 2 || entries[0].revision != 1 || entries[1].revision != 2 {
		t.Fatalf("seeded region entries=%+v，想要 revisions 1,2", entries)
	}
	flipPayloadByte(t, regionPathFor(root, key), entries[1])

	generator := newCountingPersistenceGenerator(core.GrassID)
	harness := newPersistentHarness(t, root, generator, true, nil)
	harness.setTrustedCenter(core.ChunkPos{})
	harness.waitTrustedReady(core.ChunkPos{})
	gotHash, gotRevision := harness.authoritativeChunk(core.ChunkPos{})
	if gotHash != old.Hash() || gotRevision != 3 {
		t.Fatalf("recovered=(%x,%d)，想要 old hash=%x promoted revision=3", gotHash, gotRevision, old.Hash())
	}
	harness.assertMirrorMatches(core.ChunkPos{})
	if generator.totalCalls() != 0 {
		t.Fatalf("corrupt recovery called generator: %v", generator.calledPositions())
	}
	if err := harness.shutdown(); err != nil {
		t.Fatal(err)
	}

	store := openPersistentDiskStore(t, root)
	stored, err := store.LoadChunk(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Chunk.Hash() != old.Hash() || stored.Revision != 3 ||
		stored.PersistedRevision != 3 || stored.NeedsRewrite || stored.Recovered {
		t.Fatalf("promoted reopen=%+v hash=%x", stored, stored.Chunk.Hash())
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCorruptStoredChunkBothCopiesFailWithoutGeneration(t *testing.T) {
	root := filepath.Join(t.TempDir(), "world")
	key := persistenceChunkKey(core.ChunkPos{})
	seedChunk(t, root,
		storage.ChunkSave{Key: key, Revision: 1, Chunk: persistenceChunk(core.ChunkPos{}, core.StoneID)},
		storage.ChunkSave{Key: key, Revision: 2, Chunk: persistenceChunk(core.ChunkPos{}, core.DirtID)},
	)
	for _, entry := range regionEntriesForChunk(t, root, key) {
		flipPayloadByte(t, regionPathFor(root, key), entry)
	}

	generator := newCountingPersistenceGenerator(core.GrassID)
	harness := newPersistentHarness(t, root, generator, true, nil)
	harness.setTrustedCenter(core.ChunkPos{})
	harness.stepUntil(func() bool {
		info, ok := harness.running.ChunkInfo(core.Overworld, core.ChunkPos{})
		return ok && info.State == sim.ChunkFailed
	})
	info, _ := harness.running.ChunkInfo(core.Overworld, core.ChunkPos{})
	if !errors.Is(info.Err, storage.ErrCorrupt) {
		t.Fatalf("ChunkFailed error=%v，想要 %v", info.Err, storage.ErrCorrupt)
	}
	if generator.totalCalls() != 0 {
		t.Fatalf("both-copy corruption called generator: %v", generator.calledPositions())
	}
	if err := harness.shutdown(); err != nil {
		t.Fatal(err)
	}
}

func TestPersistentShutdownReportsRootRetainsOwnershipAndRetries(t *testing.T) {
	root := filepath.Join(t.TempDir(), "world")
	diskFull := errors.New("injected disk full")
	var failing *recoverablePersistenceStore
	harness := newPersistentHarness(
		t,
		root,
		newCountingPersistenceGenerator(core.StoneID),
		true,
		func(store storage.Store) storage.Store {
			failing = &recoverablePersistenceStore{Store: store, err: diskFull, failing: true}
			return failing
		},
	)
	t.Cleanup(failing.recover)
	harness.setTrustedCenter(core.ChunkPos{})
	harness.waitTrustedReady(core.ChunkPos{})
	wantHash, wantRevision := harness.authoritativeChunk(core.ChunkPos{})

	err := harness.shutdown()
	if !errors.Is(err, diskFull) {
		t.Fatalf("first Shutdown error=%v，想要 root %v", err, diskFull)
	}
	status := harness.running.PersistenceStatus()
	if status.DirtyChunks == 0 || status.InFlightChunks == 0 {
		t.Fatalf("failed Shutdown lost persistence state: %+v", status)
	}
	if competing, openErr := storage.OpenDisk(context.Background(), root, storage.OpenOptions{}); !errors.Is(openErr, storage.ErrWorldLocked) {
		if competing != nil {
			_ = competing.Close()
		}
		t.Fatalf("failed Shutdown world ownership error=%v，想要 %v", openErr, storage.ErrWorldLocked)
	}

	failing.recover()
	if err := harness.shutdown(); err != nil {
		t.Fatalf("retry Shutdown: %v", err)
	}
	store := openPersistentDiskStore(t, root)
	stored, err := store.LoadChunk(context.Background(), persistenceChunkKey(core.ChunkPos{}))
	if err != nil {
		t.Fatal(err)
	}
	if stored.Chunk.Hash() != wantHash || stored.Revision != wantRevision {
		t.Fatalf("retry persisted=(%x,%d)，想要 (%x,%d)", stored.Chunk.Hash(), stored.Revision, wantHash, wantRevision)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPersistentShutdownReturnsAllGoroutinesWithinSharedDeadline(t *testing.T) {
	root := filepath.Join(t.TempDir(), "world")
	harness := newPersistentHarness(
		t,
		root,
		newCountingPersistenceGenerator(core.StoneID),
		true,
		nil,
	)
	harness.setTrustedCenter(core.ChunkPos{})
	harness.waitTrustedReady(core.ChunkPos{})

	deadline := time.Now().Add(waitDeadline)
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		defer cancel()
		done <- harness.running.Shutdown(ctx)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
		harness.closed = true
		_ = harness.clientEndpoint.Close()
	case <-time.After(time.Until(deadline)):
		t.Fatalf("Shutdown 未在共享 deadline 内返回\n%s", persistentGoroutineDump())
	}
	for time.Now().Before(deadline) {
		dump := persistentGoroutineDump()
		if !containsPersistentServerGoroutine(dump) {
			return
		}
		runtime.Gosched()
	}
	t.Fatalf("runtime/session/save goroutine 未在共享 deadline 内退出\n%s", persistentGoroutineDump())
}

var interactionPersistenceChunk = (core.BlockPos{X: 0, Y: 1, Z: -5}).Chunk()

type persistentHarness struct {
	t              *testing.T
	root           string
	clientEndpoint network.ClientEndpoint
	running        *server.Server
	mirror         *client.Mirror
	generator      *countingPersistenceGenerator
	trusted        bool
	sequence       uint64
	closed         bool
}

func newPersistentHarness(
	t *testing.T,
	root string,
	generator *countingPersistenceGenerator,
	trusted bool,
	decorate func(storage.Store) storage.Store,
) *persistentHarness {
	t.Helper()
	store := storage.Store(openPersistentDiskStore(t, root))
	if decorate != nil {
		store = decorate(store)
	}
	clientEndpoint, serverEndpoint := network.NewMemoryPair(4096)
	config := server.DefaultConfig(42)
	config.ViewRadius = 1
	if trusted {
		config.ViewRadius = 0
	}
	config.Workers = 1
	config.SnapshotChunks = 16
	config.SnapshotBytes = 4 << 20
	config.OutboxCapacity = 4096
	config.TrustedObserver = trusted
	config.SaveWorkers = 1
	config.SaveChunks = 16
	config.SaveBytes = 4 << 20
	config.AutosaveTicks = 1 << 60
	config.RetryBaseTicks = 1
	config.RetryMaxTicks = 4
	harness := &persistentHarness{
		t:              t,
		root:           root,
		clientEndpoint: clientEndpoint,
		running:        newAttachedPersistentWorldForExternalTest(config, serverEndpoint, generator, store),
		mirror:         client.NewMirror(),
		generator:      generator,
		trusted:        trusted,
	}
	t.Cleanup(func() {
		if harness.closed {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
		_ = harness.running.Shutdown(ctx)
		cancel()
		_ = harness.clientEndpoint.Close()
	})
	return harness
}

func (h *persistentHarness) waitReady() {
	h.t.Helper()
	h.stepUntil(func() bool {
		player, playerOK := playerStateForExternalTest(h.running)
		if !playerOK || !player.Ready {
			return false
		}
		for z := int32(-1); z <= 1; z++ {
			for x := int32(-1); x <= 1; x++ {
				if _, ok := h.mirror.Chunk(core.Overworld, core.ChunkPos{X: x, Z: z}); !ok {
					return false
				}
			}
		}
		return true
	})
}

func (h *persistentHarness) placeAndBreakScript() {
	h.t.Helper()
	pitch := float32(-0.2)
	interactionChunk := (core.BlockPos{X: 0, Y: 1, Z: -6}).Chunk()

	// 先挖掘获得物品，再从栏位 0 放回同一位置。
	baseRevision := h.authoritativeRevision(interactionChunk)
	h.sequence++
	sendClientMessage(h.t, h.clientEndpoint, network.PlayerInput{
		Sequence: h.sequence,
		Yaw:      0,
		Pitch:    pitch,
		Mining:   true,
	})
	broken := awaitInteractionChange(
		h.t, h.running, h.clientEndpoint, h.mirror,
		interactionChunk, baseRevision, baseRevision+1,
	)
	if broken.Block != core.AirID {
		h.t.Fatalf("挖掘结果 = %+v，想要空气", broken)
	}

	baseRevision = h.authoritativeRevision(interactionChunk)
	h.sequence++
	// 障碍消失后需要压低视角才能命中六格内的地面。
	sendClientMessage(h.t, h.clientEndpoint, network.PlaceBlock{
		Sequence: h.sequence,
		Yaw:      0,
		Pitch:    -0.6,
		Slot:     0,
	})
	placed := awaitInteractionChange(
		h.t, h.running, h.clientEndpoint, h.mirror,
		interactionChunk, baseRevision, baseRevision+1,
	)
	if placed.Position == broken.Position || placed.Block != core.StoneID {
		h.t.Fatalf("放置结果 = %+v，想要放下刚采集的石头", placed)
	}
}

func (h *persistentHarness) authoritativeRevision(position core.ChunkPos) uint64 {
	h.t.Helper()
	_, revision, ok := h.running.ChunkHash(core.Overworld, position)
	if !ok {
		h.t.Fatalf("authority chunk %+v unavailable", position)
	}
	return revision
}

func (h *persistentHarness) authoritativeChunk(position core.ChunkPos) ([32]byte, uint64) {
	h.t.Helper()
	hash, revision, ok := h.running.ChunkHash(core.Overworld, position)
	if !ok {
		h.t.Fatalf("authority chunk %+v unavailable", position)
	}
	return hash, revision
}

func (h *persistentHarness) assertMirrorMatches(position core.ChunkPos) {
	h.t.Helper()
	authorityHash, authorityRevision := h.authoritativeChunk(position)
	mirrorHash, mirrorRevision, ok := h.mirror.Hash(core.Overworld, position)
	if !ok || mirrorHash != authorityHash || mirrorRevision != authorityRevision {
		h.t.Fatalf(
			"chunk %+v authority=(%x,%d) mirror=(%x,%d,%v)",
			position,
			authorityHash,
			authorityRevision,
			mirrorHash,
			mirrorRevision,
			ok,
		)
	}
}

func (h *persistentHarness) moveViewToUnvisitedChunk() {
	h.t.Helper()
	for range 600 {
		h.sequence++
		sendClientMessage(h.t, h.clientEndpoint, network.PlayerInput{
			Sequence: h.sequence,
			MoveZ:    1,
		})
		h.step()
		if h.generator.totalCalls() > 0 {
			return
		}
	}
	h.t.Fatalf("移动 600 tick 后 generator B 未用于未探索区块；player=%+v", h.playerSummary())
}

func (h *persistentHarness) playerSummary() any {
	state, ok := playerStateForExternalTest(h.running)
	return struct {
		State any
		OK    bool
	}{State: state, OK: ok}
}

func (h *persistentHarness) setTrustedCenter(position core.ChunkPos) {
	h.t.Helper()
	if err := h.running.SetTrustedObserverCenter(core.Overworld, position); err != nil {
		h.t.Fatal(err)
	}
}

func (h *persistentHarness) waitTrustedReady(position core.ChunkPos) {
	h.t.Helper()
	h.stepUntil(func() bool {
		info, ok := h.running.ChunkInfo(core.Overworld, position)
		_, mirrored := h.mirror.Chunk(core.Overworld, position)
		return ok && info.State == sim.ChunkReady && mirrored
	})
	h.assertMirrorMatches(position)
}

func (h *persistentHarness) stepUntil(done func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(waitDeadline)
	for !done() {
		h.step()
		if time.Now().After(deadline) {
			h.t.Fatalf(
				"persistent harness wait timeout; trusted=%v status=%+v calls=%v",
				h.trusted,
				h.running.PersistenceStatus(),
				h.generator.calledPositions(),
			)
		}
		runtime.Gosched()
	}
}

func (h *persistentHarness) step() {
	h.t.Helper()
	result := h.running.StepForTest()
	if !h.trusted {
		drainServerMessages(h.t, h.clientEndpoint, h.mirror, nil, result.Tick)
		return
	}
	h.drainTrustedMessages()
}

func (h *persistentHarness) drainTrustedMessages() {
	h.t.Helper()
	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		message, err := h.clientEndpoint.Recv(ctx)
		cancel()
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, network.ErrClosed) {
			return
		}
		if err != nil {
			h.t.Fatalf("trusted Recv: %v", err)
		}
		if _, ok := message.(network.PlayerState); ok {
			continue
		}
		update, err := h.mirror.Apply(message)
		if err != nil {
			h.t.Fatalf("trusted Mirror.Apply(%T): %v", message, err)
		}
		if update.Resync != nil || update.Rejected != nil {
			h.t.Fatalf("trusted update requested correction: %+v", update)
		}
	}
}

func (h *persistentHarness) shutdown() error {
	h.t.Helper()
	if h.closed {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
	err := h.running.Shutdown(ctx)
	cancel()
	if err != nil {
		return err
	}
	h.closed = true
	return h.clientEndpoint.Close()
}

type countingPersistenceGenerator struct {
	mu     sync.Mutex
	marker core.BlockID
	calls  map[core.ChunkPos]int
}

func newCountingPersistenceGenerator(marker core.BlockID) *countingPersistenceGenerator {
	return &countingPersistenceGenerator{
		marker: marker,
		calls:  make(map[core.ChunkPos]int),
	}
}

func (generator *countingPersistenceGenerator) GenerateChunk(position core.ChunkPos) *world.Chunk {
	generator.mu.Lock()
	generator.calls[position]++
	generator.mu.Unlock()
	return persistenceChunk(position, generator.marker)
}

func (generator *countingPersistenceGenerator) callsFor(position core.ChunkPos) int {
	generator.mu.Lock()
	defer generator.mu.Unlock()
	return generator.calls[position]
}

func (generator *countingPersistenceGenerator) totalCalls() int {
	generator.mu.Lock()
	defer generator.mu.Unlock()
	total := 0
	for _, calls := range generator.calls {
		total += calls
	}
	return total
}

func (generator *countingPersistenceGenerator) calledPositions() []core.ChunkPos {
	generator.mu.Lock()
	defer generator.mu.Unlock()
	positions := make([]core.ChunkPos, 0, len(generator.calls))
	for position := range generator.calls {
		positions = append(positions, position)
	}
	sort.Slice(positions, func(i, j int) bool {
		if positions[i].X != positions[j].X {
			return positions[i].X < positions[j].X
		}
		return positions[i].Z < positions[j].Z
	})
	return positions
}

func persistenceChunk(position core.ChunkPos, marker core.BlockID) *world.Chunk {
	chunk := server.FlatTestGenerator{}.GenerateChunk(position)
	chunk.SetBlock(core.SectionSize-1, 10, core.SectionSize-1, marker)
	chunk.Compact()
	return chunk
}

func containsChunkPos(positions []core.ChunkPos, wanted core.ChunkPos) bool {
	for _, position := range positions {
		if position == wanted {
			return true
		}
	}
	return false
}

func persistenceChunkKey(position core.ChunkPos) core.ChunkKey {
	return core.ChunkKey{Dimension: core.Overworld, Pos: position}
}

func openPersistentDiskStore(t *testing.T, root string) *storage.DiskStore {
	t.Helper()
	store, err := storage.OpenDisk(context.Background(), root, storage.OpenOptions{
		Create: storage.Metadata{
			FormatVersion:  2,
			Seed:           42,
			SpawnDimension: core.Overworld,
			SpawnAnchor:    core.ChunkPos{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func seedChunk(t *testing.T, root string, saves ...storage.ChunkSave) {
	t.Helper()
	store := openPersistentDiskStore(t, root)
	for _, save := range saves {
		if _, err := store.SaveBatch(context.Background(), []storage.ChunkSave{save}); err != nil {
			_ = store.Close()
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

type migrationRewriteStore struct {
	storage.Store
	mu        sync.Mutex
	key       core.ChunkKey
	marked    bool
	saveCount int
}

func (store *migrationRewriteStore) LoadChunk(
	ctx context.Context,
	key core.ChunkKey,
) (storage.StoredChunk, error) {
	stored, err := store.Store.LoadChunk(ctx, key)
	if err != nil {
		return stored, err
	}
	store.mu.Lock()
	if key == store.key && !store.marked {
		stored.NeedsRewrite = true
		store.marked = true
	}
	store.mu.Unlock()
	return stored, nil
}

func (store *migrationRewriteStore) SaveBatch(
	ctx context.Context,
	saves []storage.ChunkSave,
) (storage.SaveResult, error) {
	store.mu.Lock()
	store.saveCount++
	store.mu.Unlock()
	return store.Store.SaveBatch(ctx, saves)
}

func (store *migrationRewriteStore) saveCalls() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.saveCount
}

type recoverablePersistenceStore struct {
	storage.Store
	mu      sync.Mutex
	err     error
	failing bool
}

func (store *recoverablePersistenceStore) SaveBatch(
	ctx context.Context,
	saves []storage.ChunkSave,
) (storage.SaveResult, error) {
	store.mu.Lock()
	failing, err := store.failing, store.err
	store.mu.Unlock()
	if failing {
		return storage.SaveResult{Committed: map[core.ChunkKey]uint64{}}, err
	}
	return store.Store.SaveBatch(ctx, saves)
}

func (store *recoverablePersistenceStore) recover() {
	store.mu.Lock()
	store.failing = false
	store.mu.Unlock()
}

type persistenceRegionEntry struct {
	generation uint64
	offset     uint32
	revision   uint64
}

func regionEntriesForChunk(
	t *testing.T,
	root string,
	key core.ChunkKey,
) []persistenceRegionEntry {
	t.Helper()
	data, err := os.ReadFile(regionPathFor(root, key))
	if err != nil {
		t.Fatal(err)
	}
	_, slot := storage.RegionFor(key)
	const sectorSize = 4096
	const entrySize = 24
	const entriesOffset = 64
	entries := make([]persistenceRegionEntry, 0, 2)
	for _, bankSector := range []int{1, 8} {
		bankOffset := bankSector * sectorSize
		entryOffset := bankOffset + entriesOffset + slot*entrySize
		offset := binary.LittleEndian.Uint32(data[entryOffset:])
		if offset == 0 {
			continue
		}
		entries = append(entries, persistenceRegionEntry{
			generation: binary.LittleEndian.Uint64(data[bankOffset+24:]),
			offset:     offset,
			revision:   binary.LittleEndian.Uint64(data[entryOffset+12:]),
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].generation < entries[j].generation })
	return entries
}

func regionPathFor(root string, key core.ChunkKey) string {
	region, _ := storage.RegionFor(key)
	return filepath.Join(
		root,
		"dimensions",
		fmt.Sprint(region.Dimension),
		"regions",
		fmt.Sprintf("r.%d.%d.region", region.X, region.Z),
	)
}

func flipPayloadByte(t *testing.T, path string, entry persistenceRegionEntry) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	offset := int64(entry.offset) * 4096
	var value [1]byte
	if _, err := file.ReadAt(value[:], offset); err != nil {
		t.Fatal(err)
	}
	value[0] ^= 0xff
	if _, err := file.WriteAt(value[:], offset); err != nil {
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		t.Fatal(err)
	}
}

func persistentGoroutineDump() string {
	buffer := make([]byte, 1<<20)
	length := runtime.Stack(buffer, true)
	return string(buffer[:length])
}

func containsPersistentServerGoroutine(dump string) bool {
	for _, name := range []string{
		"(*Server).endpointReader",
		"(*Server).chunkWorker",
		"(*Server).saveWorker",
		"(*session).writeLoop",
	} {
		if strings.Contains(dump, name) {
			return true
		}
	}
	return false
}
