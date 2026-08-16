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
	p.Observe([]companion.Body{companionBody(1, 10)}, nil)
	if err := p.Poll(10); err != nil {
		t.Fatal(err)
	}
	first := receiveCompanionSave(t, store)
	if first.Revision != 8 || first.Records[0].Position[0] != 10 {
		t.Fatalf("first save=%+v", first)
	}
	p.Observe([]companion.Body{companionBody(1, 20)}, nil)
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
	p.Observe([]companion.Body{companionBody(1, 10)}, nil)
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
	p.Observe([]companion.Body{companionBody(1, 10)}, nil)
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
	p.Observe([]companion.Body{companionBody(1, 10)}, nil)
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
	p.Observe([]companion.Body{companionBody(1, 20)}, nil)
	store.complete(nil)
	latest := receiveCompanionSaveBeforeFlushReturns(t, store, secondDone)
	if latest.Revision != 2 || latest.Records[0].Position[0] != 20 {
		t.Fatalf("Flush latest=%+v, want revision 2 body 20", latest)
	}
	store.complete(nil)
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	assertNoCompanionSave(t, store)
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
	p.Observe([]companion.Body{companionBody(1, 10)}, nil)
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
	p.Observe([]companion.Body{companionBody(1, 10)}, nil)
	_ = p.Poll(10)
	_ = receiveCompanionSave(t, store)
	p.Observe([]companion.Body{companionBody(1, 20)}, nil)
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
	p.Observe([]companion.Body{companionBody(1, 10)}, nil)
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
	for _, test := range []struct {
		name      string
		inherited bool
	}{
		{name: "inherited", inherited: true},
		{name: "self dispatched"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newControllableCompanionStore()
			p := newCompanionPersistence(store, storage.StoredCompanions{}, companionPersistenceTestConfig())
			t.Cleanup(p.Close)
			p.Observe([]companion.Body{companionBody(1, 10)}, nil)
			flushed := make(chan error, 1)
			if test.inherited {
				if err := p.Poll(10); err != nil {
					t.Fatal(err)
				}
				_ = receiveCompanionSave(t, store)
				go func() { flushed <- p.Flush(context.Background()) }()
			} else {
				go func() { flushed <- p.Flush(context.Background()) }()
				first := receiveCompanionSaveBeforeFlushReturns(t, store, flushed)
				if first.Revision != 1 || first.Records[0].Position[0] != 10 {
					t.Fatalf("first save=%+v", first)
				}
			}

			p.Observe([]companion.Body{companionBody(1, 20)}, nil)
			store.complete(nil)
			followup := receiveCompanionSaveBeforeFlushReturns(t, store, flushed)
			if followup.Revision != 2 || followup.Records[0].Position[0] != 20 {
				t.Fatalf("follow-up save=%+v, want revision 2 body 20", followup)
			}
			p.Observe([]companion.Body{companionBody(1, 30)}, nil)
			store.complete(nil)
			select {
			case save := <-store.started:
				t.Fatalf("same Flush wrote more than one follow-up: %+v", save)
			case err := <-flushed:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(companionPersistenceTestDeadline):
				t.Fatal("Flush did not return after its single follow-up")
			}

			next := make(chan error, 1)
			go func() { next <- p.Flush(context.Background()) }()
			latest := receiveCompanionSaveBeforeFlushReturns(t, store, next)
			if latest.Revision != 3 || latest.Records[0].Position[0] != 30 {
				t.Fatalf("next Flush save=%+v, want revision 3 body 30", latest)
			}
			store.complete(nil)
			select {
			case err := <-next:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(companionPersistenceTestDeadline):
				t.Fatal("next Flush did not return")
			}
			assertNoCompanionSave(t, store)
		})
	}
}

func TestCompanionPersistenceFlushCancellationKeepsWorkerAndRetry(t *testing.T) {
	store := newControllableCompanionStore()
	p := newCompanionPersistence(store, storage.StoredCompanions{}, companionPersistenceTestConfig())
	t.Cleanup(p.Close)
	p.Observe([]companion.Body{companionBody(1, 10)}, nil)
	if err := p.Poll(10); err != nil {
		t.Fatal(err)
	}
	first := receiveCompanionSave(t, store)

	ctx, cancel := context.WithCancel(context.Background())
	canceled := make(chan error, 1)
	go func() { canceled <- p.Flush(ctx) }()
	waitForCompanionFlushWait(t, p)
	cancel()
	select {
	case err := <-canceled:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled Flush error=%v, want context.Canceled", err)
		}
	case <-time.After(companionPersistenceTestDeadline):
		t.Fatal("Flush ignored caller cancellation")
	}
	if err := p.ctx.Err(); err != nil {
		t.Fatalf("caller cancellation canceled internal worker: %v", err)
	}

	wantErr := errors.New("disk full after caller cancellation")
	store.complete(wantErr)
	collected := make(chan error, 1)
	go func() { collected <- p.Flush(context.Background()) }()
	select {
	case save := <-store.started:
		t.Fatalf("collecting failed in-flight dispatched another save: %+v", save)
	case err := <-collected:
		if !errors.Is(err, wantErr) {
			t.Fatalf("collecting Flush error=%v, want %v", err, wantErr)
		}
	case <-time.After(companionPersistenceTestDeadline):
		t.Fatal("new Flush did not collect failed in-flight save")
	}

	replayed := make(chan error, 1)
	go func() { replayed <- p.Flush(context.Background()) }()
	retry := receiveCompanionSaveBeforeFlushReturns(t, store, replayed)
	if !reflect.DeepEqual(retry, first) {
		t.Fatalf("retry=%+v, want frozen %+v", retry, first)
	}
	store.complete(nil)
	select {
	case err := <-replayed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(companionPersistenceTestDeadline):
		t.Fatal("replayed Flush did not return")
	}
}

func TestCompanionPersistenceDefersQueueWithoutBodyRecordUntilActivation(t *testing.T) {
	store := newControllableCompanionStore()
	p := newCompanionPersistence(store, storage.StoredCompanions{}, companionPersistenceTestConfig())
	t.Cleanup(p.Close)
	// 出生扫描在途的伙伴可以先收到排队指令：队列没有身体记录不能编码，
	// 保存载荷丢弃它而不是失败，激活后的首次保存补上完整载荷。
	pending := companion.TaskQueueState{
		ID:      companionBody(9, 90).ID,
		Pending: []companion.TaskCommand{"出生前的指令"},
	}
	p.Observe(nil, []companion.TaskQueueState{pending})
	if err := p.Poll(10); err != nil {
		t.Fatal(err)
	}
	first := receiveCompanionSave(t, store)
	if first.Revision != 1 || len(first.Queues) != 0 {
		t.Fatalf("无身体记录的保存载荷=%+v，想要 revision=1 且无队列", first)
	}
	store.complete(nil)

	// 身体激活前 dirty 保持（排队指令尚未落盘），期间可能重复保存无队列
	// 载荷；激活后逐个收尾，直到出现包含排队指令的保存。
	p.Observe([]companion.Body{companionBody(9, 90)}, []companion.TaskQueueState{pending})
	var withQueue storage.CompanionSave
	deadline := time.Now().Add(waitDeadline)
	for time.Now().Before(deadline) {
		if err := p.Poll(20); err != nil {
			t.Fatal(err)
		}
		select {
		case save := <-store.started:
			if len(save.Queues) != 0 {
				withQueue = save
			}
			store.complete(nil)
		default:
		}
		if len(withQueue.Queues) != 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	want := []storage.StoredCompanionQueue{{
		ID:      companionBody(9, 90).ID,
		Pending: []string{"出生前的指令"},
	}}
	if !reflect.DeepEqual(withQueue.Queues, want) {
		t.Fatalf("激活后的保存载荷=%+v，想要 queues=%+v", withQueue.Queues, want)
	}
	pollCompanionPersistenceUntil(t, p, 30, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return !p.dirty && !p.inFlight
	})
}

type controllableCompanionStore struct {
	mu      sync.Mutex
	started chan storage.CompanionSave
	results chan error
	mutate  bool
	onSave  func()
}

const companionPersistenceTestDeadline = time.Second

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
	copy.Queues = cloneStoredQueues(save.Queues)
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

func receiveCompanionSaveBeforeFlushReturns(
	t *testing.T,
	store *controllableCompanionStore,
	flushed <-chan error,
) storage.CompanionSave {
	t.Helper()
	select {
	case save := <-store.started:
		return save
	case err := <-flushed:
		t.Fatalf("Flush returned before required save: %v", err)
		return storage.CompanionSave{}
	case <-time.After(companionPersistenceTestDeadline):
		t.Fatal("SaveCompanions was not started before Flush deadline")
		return storage.CompanionSave{}
	}
}

func waitForCompanionFlushWait(t *testing.T, p *companionPersistence) {
	t.Helper()
	deadline := time.Now().Add(companionPersistenceTestDeadline)
	for {
		if p.completionMu.TryLock() {
			p.completionMu.Unlock()
		} else {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("Flush did not start waiting for in-flight save")
		}
		time.Sleep(time.Millisecond)
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
