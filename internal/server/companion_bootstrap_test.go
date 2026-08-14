package server

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/storage"
)

func TestNewHostSkipsCompanionStoreWhenAIDisabled(t *testing.T) {
	store := newCompanionBootstrapStore()
	host, err := NewHost(context.Background(), hostTestConfig(), flatTestGenerator{}, store)
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	shutdownCompanionBootstrapHost(t, host)

	store.mu.Lock()
	loads, saves := store.companionLoads, store.companionSaves
	store.mu.Unlock()
	if loads != 0 || saves != 0 {
		t.Fatalf("AI disabled companion store calls = load %d save %d，want 0,0", loads, saves)
	}
}

func TestNewHostRestoresConfiguredBodiesAndPreservesInactiveRecords(t *testing.T) {
	activeID := companionBootstrapID(1)
	inactiveID := companionBootstrapID(2)
	active := companionBootstrapBody(activeID, 0.5)
	inactive := companionBootstrapBody(inactiveID, 20.5)
	store := newCompanionBootstrapStore()
	seedCompanionBootstrapStore(t, store, 7, []companion.Body{inactive, active})
	config := hostTestConfig()
	config.Companions = []companion.Definition{{ID: activeID, Name: "阿木"}}

	host, err := NewHost(context.Background(), config, flatTestGenerator{}, store)
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	cleanupCompanionBootstrapHost(t, host)
	gotActive := waitForCompanionBootstrapBody(t, host, activeID)
	if gotActive != active {
		t.Fatalf("恢复 active body = %+v，want %+v", gotActive, active)
	}
	records, revision := companionBootstrapRecords(host)
	if revision != 7 || !reflect.DeepEqual(records, []companion.Body{active, inactive}) {
		t.Fatalf("merged records revision=%d records=%+v", revision, records)
	}
}

func TestNewHostAddsConfiguredIDWithoutDeletingInactiveRecords(t *testing.T) {
	configuredID := companionBootstrapID(3)
	inactive := companionBootstrapBody(companionBootstrapID(4), 20.5)
	store := newCompanionBootstrapStore()
	seedCompanionBootstrapStore(t, store, 3, []companion.Body{inactive})
	config := hostTestConfig()
	config.Companions = []companion.Definition{{ID: configuredID, Name: "阿木"}}

	host, err := NewHost(context.Background(), config, flatTestGenerator{}, store)
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	cleanupCompanionBootstrapHost(t, host)
	spawned := waitForCompanionBootstrapBody(t, host, configuredID)
	records, revision := companionBootstrapRecords(host)
	if revision != 3 || len(records) != 2 || !slices.Contains(records, inactive) || !slices.Contains(records, spawned) {
		t.Fatalf("merged records revision=%d records=%+v，want inactive+spawned", revision, records)
	}
}

func TestNewHostRejectsSixtyFifthDistinctStoredOrNewCompanion(t *testing.T) {
	records := make([]companion.Body, companion.MaxStored)
	for index := range records {
		records[index] = companionBootstrapBody(companionBootstrapID(byte(index+1)), float32(index)+0.5)
	}
	store := newCompanionBootstrapStore()
	seedCompanionBootstrapStore(t, store, 9, records)
	config := hostTestConfig()
	config.Companions = []companion.Definition{{ID: companionBootstrapID(65), Name: "阿木"}}

	host, err := NewHost(context.Background(), config, flatTestGenerator{}, store)
	if err == nil || host != nil {
		if host != nil {
			cleanupCompanionBootstrapHost(t, host)
		}
		t.Fatalf("NewHost = %v, %v，want 65th rejection", host, err)
	}
	loads, saves := store.companionCallCounts()
	if loads != 1 || saves != 0 || store.syncCount() != 0 || store.closeCount() != 0 {
		t.Fatalf("failed constructor calls load/save/sync/close=%d/%d/%d/%d", loads, saves, store.syncCount(), store.closeCount())
	}
}

func TestNewHostRejectsCorruptOrFutureCompanionStoreBeforeWorkersStart(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "corrupt", err: storage.ErrCorrupt},
		{name: "future", err: storage.ErrFutureVersion},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newCompanionBootstrapStore()
			store.loadErr = fmt.Errorf("load companions: %w", test.err)
			config := hostTestConfig()
			config.Companions = []companion.Definition{{ID: companionBootstrapID(1), Name: "阿木"}}
			host, err := NewHost(context.Background(), config, flatTestGenerator{}, store)
			if host != nil || !errors.Is(err, test.err) {
				t.Fatalf("NewHost = %v, %v，want %v", host, err, test.err)
			}
			loads, saves := store.companionCallCounts()
			if loads != 1 || saves != 0 || store.syncCount() != 0 || store.closeCount() != 0 {
				t.Fatalf("failed constructor calls load/save/sync/close=%d/%d/%d/%d", loads, saves, store.syncCount(), store.closeCount())
			}
		})
	}
}

func TestRemovingAllCompanionConfigDisablesAIAndLeavesFileUntouched(t *testing.T) {
	want := companionBootstrapBody(companionBootstrapID(1), 4.5)
	store := newCompanionBootstrapStore()
	seedCompanionBootstrapStore(t, store, 11, []companion.Body{want})
	host, err := NewHost(context.Background(), hostTestConfig(), flatTestGenerator{}, store)
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	shutdownCompanionBootstrapHost(t, host)

	loads, saves := store.companionCallCounts()
	stored, err := store.MemoryStore.LoadCompanions(context.Background())
	if err != nil {
		t.Fatalf("LoadCompanions: %v", err)
	}
	if loads != 0 || saves != 0 || stored.Revision != 11 || !reflect.DeepEqual(stored.Records, []companion.Body{want}) {
		t.Fatalf("AI-disabled file changed: calls=%d/%d stored=%+v", loads, saves, stored)
	}
}

func TestCompanionShutdownFlushFailureIsRetryable(t *testing.T) {
	id := companionBootstrapID(1)
	store := newCompanionBootstrapStore()
	seedCompanionBootstrapStore(t, store, 1, []companion.Body{companionBootstrapBody(id, 0.5)})
	wantErr := errors.New("companion disk full")
	store.saveErrors = []error{wantErr, nil}
	config := hostTestConfig()
	config.Companions = []companion.Definition{{ID: id, Name: "阿木"}}
	host, err := NewHost(context.Background(), config, flatTestGenerator{}, store)
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	latest := companionBootstrapBody(id, 9.5)
	host.world.companions.Observe([]companion.Body{latest})

	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
	err = host.Shutdown(ctx)
	cancel()
	if !errors.Is(err, wantErr) {
		t.Fatalf("first Shutdown error=%v，want %v", err, wantErr)
	}
	if store.syncCount() != 0 || store.closeCount() != 0 || host.world.companions.closed {
		t.Fatalf("failed Flush closed resources: sync=%d close=%d persistenceClosed=%v", store.syncCount(), store.closeCount(), host.world.companions.closed)
	}
	shutdownCompanionBootstrapHost(t, host)
	saves := store.companionSaveSnapshot()
	if len(saves) != 2 || saves[0].Revision != 2 || saves[1].Revision != 2 || !reflect.DeepEqual(saves[1].Records, []companion.Body{latest}) {
		t.Fatalf("retry saves=%+v，want same revision 2 and latest body", saves)
	}
}

func TestCompanionShutdownPersistsBodyCreatedByFinalStepBeforeSync(t *testing.T) {
	id := companionBootstrapID(1)
	store := newCompanionBootstrapStore()
	config := hostTestConfig()
	config.Companions = []companion.Definition{{ID: id, Name: "阿木"}}
	host, err := NewHost(context.Background(), config, flatTestGenerator{}, store)
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	host.world.StepForTest()
	waitForCompanionBootstrapChannel(t, host.world.acquired)
	host.world.StepForTest()
	waitForCompanionBootstrapChannel(t, host.world.generated)
	if bodies := host.world.engine.CompanionBodies(); len(bodies) != 0 {
		t.Fatalf("伙伴在 final step 前已激活：%+v", bodies)
	}

	shutdownCompanionBootstrapHost(t, host)
	saves := store.companionSaveSnapshot()
	if len(saves) != 1 || saves[0].Revision != 1 || len(saves[0].Records) != 1 || saves[0].Records[0].ID != id {
		t.Fatalf("final-step companion saves=%+v", saves)
	}
	assertCompanionBootstrapEventOrder(t, store.eventsSnapshot())
}

func TestCompanionShutdownOrdersSaveBeforeStoreSyncAndClose(t *testing.T) {
	id := companionBootstrapID(1)
	store := newCompanionBootstrapStore()
	seedCompanionBootstrapStore(t, store, 1, []companion.Body{companionBootstrapBody(id, 0.5)})
	config := hostTestConfig()
	config.Companions = []companion.Definition{{ID: id, Name: "阿木"}}
	host, err := NewHost(context.Background(), config, flatTestGenerator{}, store)
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	host.world.companions.Observe([]companion.Body{companionBootstrapBody(id, 8.5)})
	shutdownCompanionBootstrapHost(t, host)
	assertCompanionBootstrapEventOrder(t, store.eventsSnapshot())
}

type companionBootstrapStore struct {
	*hostTestStore
	mu               sync.Mutex
	companionLoads   int
	companionSaves   int
	loadErr          error
	saveErrors       []error
	companionSaveLog []storage.CompanionSave
}

func newCompanionBootstrapStore() *companionBootstrapStore {
	return &companionBootstrapStore{hostTestStore: newHostTestStore()}
}

func (store *companionBootstrapStore) LoadCompanions(ctx context.Context) (storage.StoredCompanions, error) {
	store.mu.Lock()
	store.companionLoads++
	err := store.loadErr
	store.mu.Unlock()
	if err != nil {
		return storage.StoredCompanions{}, err
	}
	return store.MemoryStore.LoadCompanions(ctx)
}

func (store *companionBootstrapStore) SaveCompanions(ctx context.Context, save storage.CompanionSave) error {
	store.mu.Lock()
	store.companionSaves++
	store.companionSaveLog = append(store.companionSaveLog, storage.CompanionSave{
		Revision: save.Revision,
		Records:  slices.Clone(save.Records),
	})
	var err error
	if len(store.saveErrors) != 0 {
		err = store.saveErrors[0]
		store.saveErrors = store.saveErrors[1:]
	}
	store.mu.Unlock()
	store.hostTestStore.mu.Lock()
	store.hostTestStore.events = append(store.hostTestStore.events, "companion-save")
	store.hostTestStore.mu.Unlock()
	if err != nil {
		return err
	}
	return store.MemoryStore.SaveCompanions(ctx, save)
}

func (store *companionBootstrapStore) companionCallCounts() (int, int) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.companionLoads, store.companionSaves
}

func (store *companionBootstrapStore) companionSaveSnapshot() []storage.CompanionSave {
	store.mu.Lock()
	defer store.mu.Unlock()
	result := make([]storage.CompanionSave, len(store.companionSaveLog))
	for index, save := range store.companionSaveLog {
		result[index] = storage.CompanionSave{Revision: save.Revision, Records: slices.Clone(save.Records)}
	}
	return result
}

func companionBootstrapID(number byte) companion.ID {
	return companion.ID{0, 0, 0, 0, 0, 0, 0x40, 0, 0x80, 0, 0, 0, 0, 0, 0, number}
}

func companionBootstrapBody(id companion.ID, x float32) companion.Body {
	return companion.Body{ID: id, Dimension: core.Overworld, Position: [3]float32{x, 1, 0.5}}
}

func seedCompanionBootstrapStore(t *testing.T, store *companionBootstrapStore, revision uint64, records []companion.Body) {
	t.Helper()
	if err := store.MemoryStore.SaveCompanions(context.Background(), storage.CompanionSave{Revision: revision, Records: records}); err != nil {
		t.Fatalf("seed SaveCompanions: %v", err)
	}
}

func cleanupCompanionBootstrapHost(t *testing.T, host *Host) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
		defer cancel()
		if err := host.Shutdown(ctx); err != nil {
			t.Errorf("Host cleanup Shutdown: %v", err)
		}
	})
}

func waitForCompanionBootstrapBody(t *testing.T, host *Host, id companion.ID) companion.Body {
	t.Helper()
	deadline := time.Now().Add(waitDeadline)
	for time.Now().Before(deadline) {
		host.world.StepForTest()
		bodies := host.world.engine.CompanionBodies()
		for _, body := range bodies {
			if body.ID == id {
				return body
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("伙伴 %s 未激活", id)
	return companion.Body{}
}

func companionBootstrapRecords(host *Host) ([]companion.Body, uint64) {
	host.world.companions.mu.Lock()
	defer host.world.companions.mu.Unlock()
	return slices.Clone(host.world.companions.records), host.world.companions.persisted
}

func waitForCompanionBootstrapChannel[T any](t *testing.T, channel <-chan T) {
	t.Helper()
	deadline := time.Now().Add(waitDeadline)
	for len(channel) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(channel) == 0 {
		t.Fatal("等待伙伴区块 worker 超时")
	}
}

func assertCompanionBootstrapEventOrder(t *testing.T, events []string) {
	t.Helper()
	want := []string{"companion-save", "sync", "close"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("shutdown events=%v，want %v", events, want)
	}
}

func shutdownCompanionBootstrapHost(t *testing.T, host *Host) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
	defer cancel()
	if err := host.Shutdown(ctx); err != nil {
		t.Fatalf("Host Shutdown: %v", err)
	}
}
