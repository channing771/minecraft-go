package server

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/storage"
)

func TestCompanionPersistenceCoalescesToOneInflightAggregate(t *testing.T) {
	store := newControllableCompanionStore()
	p := newCompanionPersistence(store, storage.StoredCompanions{Revision: 7}, companionPersistenceTestConfig())
	t.Cleanup(p.Close)
	p.Observe([]companion.Body{companionBody(1, 10)})
	if err := p.Poll(10); err != nil {
		t.Fatal(err)
	}
	first := receiveCompanionSave(t, store)
	if first.Revision != 8 || first.Records[0].Position[0] != 10 {
		t.Fatalf("first save=%+v", first)
	}
	p.Observe([]companion.Body{companionBody(1, 20)})
	if err := p.Poll(20); err != nil {
		t.Fatal(err)
	}
	assertNoCompanionSave(t, store)
	store.complete(nil)
	pollCompanionPersistenceUntil(t, p, 20, func() bool { return len(store.started) != 0 })
	second := receiveCompanionSave(t, store)
	if second.Revision != 9 || second.Records[0].Position[0] != 20 {
		t.Fatalf("coalesced save=%+v", second)
	}
	store.complete(nil)
}

func TestCompanionPersistencePreservesInactiveRecords(t *testing.T) {
	store := newControllableCompanionStore()
	p := newCompanionPersistence(store, storage.StoredCompanions{
		Revision: 3,
		Records:  []companion.Body{companionBody(2, 2), companionBody(1, 1)},
	}, companionPersistenceTestConfig())
	t.Cleanup(p.Close)
	p.Observe([]companion.Body{companionBody(1, 10)})
	if err := p.Poll(10); err != nil {
		t.Fatal(err)
	}
	got := receiveCompanionSave(t, store)
	want := []companion.Body{companionBody(1, 10), companionBody(2, 2)}
	if got.Revision != 4 || !reflect.DeepEqual(got.Records, want) {
		t.Fatalf("save=%+v, want revision 4 records %+v", got, want)
	}
	store.complete(nil)
}

func TestCompanionPersistenceSaveFailureRetainsDirtyAndRetriesAtTick(t *testing.T) {
	store := newControllableCompanionStore()
	p := newCompanionPersistence(store, storage.StoredCompanions{}, companionPersistenceTestConfig())
	t.Cleanup(p.Close)
	p.Observe([]companion.Body{companionBody(1, 10)})
	_ = p.Poll(10)
	first := receiveCompanionSave(t, store)
	store.complete(errors.New("disk full"))
	if err := pollCompanionPersistenceError(t, p, 10); err == nil {
		t.Fatal("save failure was not surfaced")
	}
	if err := p.Poll(11); err != nil {
		t.Fatal(err)
	}
	assertNoCompanionSave(t, store)
	if err := p.Poll(12); err != nil {
		t.Fatal(err)
	}
	retry := receiveCompanionSave(t, store)
	if !reflect.DeepEqual(retry, first) {
		t.Fatalf("retry=%+v, want frozen %+v", retry, first)
	}
	store.complete(nil)
}

func TestCompanionPersistenceFlushFailureCanBeRetried(t *testing.T) {
	store := newControllableCompanionStore()
	p := newCompanionPersistence(store, storage.StoredCompanions{}, companionPersistenceTestConfig())
	t.Cleanup(p.Close)
	p.Observe([]companion.Body{companionBody(1, 10)})
	firstDone := make(chan error, 1)
	go func() { firstDone <- p.Flush(context.Background()) }()
	first := receiveCompanionSave(t, store)
	wantErr := errors.New("disk full")
	store.complete(wantErr)
	if err := <-firstDone; !errors.Is(err, wantErr) {
		t.Fatalf("first Flush error=%v", err)
	}
	secondDone := make(chan error, 1)
	go func() { secondDone <- p.Flush(context.Background()) }()
	retry := receiveCompanionSave(t, store)
	if !reflect.DeepEqual(retry, first) {
		t.Fatalf("Flush retry=%+v, want %+v", retry, first)
	}
	store.complete(nil)
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
}

func TestCompanionPersistenceDoesNotHoldMutexDuringStoreSave(t *testing.T) {
	store := newControllableCompanionStore()
	p := newCompanionPersistence(store, storage.StoredCompanions{}, companionPersistenceTestConfig())
	t.Cleanup(p.Close)
	mutexFree := make(chan bool, 1)
	store.setOnSave(func() {
		free := p.mu.TryLock()
		if free {
			p.mu.Unlock()
		}
		mutexFree <- free
	})
	p.Observe([]companion.Body{companionBody(1, 10)})
	if err := p.Poll(10); err != nil {
		t.Fatal(err)
	}
	_ = receiveCompanionSave(t, store)
	free := <-mutexFree
	store.complete(nil)
	if !free {
		t.Fatal("worker held mutex during SaveCompanions")
	}
}

func TestCompanionPersistenceChangeDuringInflightRemainsDirty(t *testing.T) {
	store := newControllableCompanionStore()
	p := newCompanionPersistence(store, storage.StoredCompanions{}, companionPersistenceTestConfig())
	t.Cleanup(p.Close)
	p.Observe([]companion.Body{companionBody(1, 10)})
	_ = p.Poll(10)
	_ = receiveCompanionSave(t, store)
	p.Observe([]companion.Body{companionBody(1, 20)})
	store.complete(nil)
	pollCompanionPersistenceUntil(t, p, 11, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.dirty && !p.inFlight
	})
}

func TestCompanionPersistenceRetryDoesNotAliasStoreSaveInput(t *testing.T) {
	store := newControllableCompanionStore()
	store.mutateNextSave()
	p := newCompanionPersistence(store, storage.StoredCompanions{}, companionPersistenceTestConfig())
	t.Cleanup(p.Close)
	p.Observe([]companion.Body{companionBody(1, 10)})
	_ = p.Poll(10)
	mutated := receiveCompanionSave(t, store)
	if mutated.Records[0].Position[0] != 999 {
		t.Fatal("test store did not mutate input")
	}
	store.complete(errors.New("disk full"))
	_ = pollCompanionPersistenceError(t, p, 10)
	_ = p.Poll(12)
	retry := receiveCompanionSave(t, store)
	if retry.Records[0].Position[0] != 10 {
		t.Fatalf("retry aliased Store input: %+v", retry)
	}
	store.complete(nil)
}

func TestCompanionPersistenceFlushWaitsForInflightAndWritesLatestOnce(t *testing.T) {
	t.Run("inherited", func(t *testing.T) {
		store := newControllableCompanionStore()
		p := newCompanionPersistence(store, storage.StoredCompanions{}, companionPersistenceTestConfig())
		t.Cleanup(p.Close)
		p.Observe([]companion.Body{companionBody(1, 10)})
		_ = p.Poll(10)
		_ = receiveCompanionSave(t, store)
		p.Observe([]companion.Body{companionBody(1, 20)})
		flushed := make(chan error, 1)
		go func() { flushed <- p.Flush(context.Background()) }()
		assertNoCompanionSave(t, store)
		store.complete(nil)
		latest := receiveCompanionSave(t, store)
		if latest.Revision != 2 || latest.Records[0].Position[0] != 20 {
			t.Fatalf("latest save=%+v", latest)
		}
		store.complete(nil)
		if err := <-flushed; err != nil {
			t.Fatal(err)
		}
		assertNoCompanionSave(t, store)
	})

	t.Run("self dispatched", func(t *testing.T) {
		store := newControllableCompanionStore()
		p := newCompanionPersistence(store, storage.StoredCompanions{}, companionPersistenceTestConfig())
		t.Cleanup(p.Close)
		p.Observe([]companion.Body{companionBody(1, 10)})
		flushed := make(chan error, 1)
		go func() { flushed <- p.Flush(context.Background()) }()
		first := receiveCompanionSave(t, store)
		if first.Revision != 1 || first.Records[0].Position[0] != 10 {
			t.Fatalf("first save=%+v", first)
		}
		p.Observe([]companion.Body{companionBody(1, 20)})
		store.complete(nil)
		latest := receiveCompanionSave(t, store)
		if latest.Revision != 2 || latest.Records[0].Position[0] != 20 {
			t.Fatalf("latest save=%+v", latest)
		}
		store.complete(nil)
		if err := <-flushed; err != nil {
			t.Fatal(err)
		}
		assertNoCompanionSave(t, store)
	})
}

type controllableCompanionStore struct {
	mu      sync.Mutex
	started chan storage.CompanionSave
	results chan error
	mutate  bool
	onSave  func()
}

func newControllableCompanionStore() *controllableCompanionStore {
	return &controllableCompanionStore{
		started: make(chan storage.CompanionSave, 4),
		results: make(chan error),
	}
}

func (store *controllableCompanionStore) LoadCompanions(context.Context) (storage.StoredCompanions, error) {
	return storage.StoredCompanions{}, storage.ErrCompanionsNotFound
}

func (store *controllableCompanionStore) SaveCompanions(ctx context.Context, save storage.CompanionSave) error {
	store.mu.Lock()
	mutate, onSave := store.mutate, store.onSave
	store.mutate = false
	store.mu.Unlock()
	if mutate && len(save.Records) != 0 {
		save.Records[0].Position[0] = 999
	}
	copy := cloneCompanionSaveForTest(save)
	select {
	case store.started <- copy:
	case <-ctx.Done():
		return ctx.Err()
	}
	if onSave != nil {
		onSave()
	}
	select {
	case err := <-store.results:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (store *controllableCompanionStore) complete(err error) { store.results <- err }

func (store *controllableCompanionStore) mutateNextSave() {
	store.mu.Lock()
	store.mutate = true
	store.mu.Unlock()
}

func (store *controllableCompanionStore) setOnSave(onSave func()) {
	store.mu.Lock()
	store.onSave = onSave
	store.mu.Unlock()
}

func companionPersistenceTestConfig() Config {
	config := DefaultConfig(42)
	config.AutosaveTicks = 10
	config.RetryBaseTicks = 2
	config.RetryMaxTicks = 8
	return config
}

func companionBody(id, position byte) companion.Body {
	return companion.Body{
		ID:        companion.ID{0, 0, 0, 0, 0, 0, 0x40, 0, 0x80, 0, 0, 0, 0, 0, 0, id},
		Dimension: core.Overworld,
		Position:  [3]float32{float32(position), 70, -float32(position)},
	}
}

func cloneCompanionSaveForTest(save storage.CompanionSave) storage.CompanionSave {
	copy := save
	copy.Records = append([]companion.Body(nil), save.Records...)
	return copy
}

func receiveCompanionSave(t *testing.T, store *controllableCompanionStore) storage.CompanionSave {
	t.Helper()
	select {
	case save := <-store.started:
		return save
	case <-time.After(waitDeadline):
		t.Fatal("SaveCompanions was not started")
		return storage.CompanionSave{}
	}
}

func assertNoCompanionSave(t *testing.T, store *controllableCompanionStore) {
	t.Helper()
	select {
	case save := <-store.started:
		t.Fatalf("unexpected SaveCompanions(%+v)", save)
	case <-time.After(50 * time.Millisecond):
	}
}

func pollCompanionPersistenceUntil(t *testing.T, p *companionPersistence, tick uint64, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitDeadline)
	for !done() {
		if err := p.Poll(tick); err != nil {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("companion persistence did not reach expected state")
		}
		time.Sleep(time.Millisecond)
	}
}

func pollCompanionPersistenceError(t *testing.T, p *companionPersistence, tick uint64) error {
	t.Helper()
	deadline := time.Now().Add(waitDeadline)
	for {
		if err := p.Poll(tick); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			t.Fatal("companion persistence did not surface SaveCompanions error")
		}
		time.Sleep(time.Millisecond)
	}
}

var _ storage.CompanionStore = (*controllableCompanionStore)(nil)
