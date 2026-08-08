package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"minecraft-go/internal/storage"
)

// waitMetadataSaves 等待 store 记录到至少 want 次 metadata 提交。
func waitMetadataSaves(t *testing.T, store *persistenceTestStore, want int) []storage.Metadata {
	t.Helper()
	deadline := time.Now().Add(waitDeadline)
	for {
		store.mu.Lock()
		saves := append([]storage.Metadata(nil), store.metadataSaves...)
		store.mu.Unlock()
		if len(saves) >= want {
			return saves
		}
		if time.Now().After(deadline) {
			t.Fatalf("metadata 提交次数 = %d，想要至少 %d", len(saves), want)
		}
		time.Sleep(time.Millisecond)
	}
}

func metadataSaveCount(store *persistenceTestStore) int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return len(store.metadataSaves)
}

// stepToAutosaveBoundary 推进到下一个自动保存边界并返回该 tick 的世界时间。
func stepToAutosaveBoundary(t *testing.T, running *Server) uint64 {
	t.Helper()
	for range int(running.config.AutosaveTicks) + 1 {
		result := running.StepForTest()
		if result.Tick%running.config.AutosaveTicks == 0 {
			return result.WorldTimeTicks
		}
	}
	t.Fatal("没有到达自动保存边界")
	return 0
}

func TestMetadataAutosaveCommitsLatestWorldTimeAtBoundary(t *testing.T) {
	store := newPersistenceTestStore()
	running := newPersistenceServer(t, store)
	running.config.AutosaveTicks = 4

	// 边界之前的普通 tick 不得单独触发 metadata 写入。
	running.StepForTest()
	running.StepForTest()
	if got := metadataSaveCount(store); got != 0 {
		t.Fatalf("边界前 metadata 提交次数 = %d，想要 0", got)
	}

	wantTime := stepToAutosaveBoundary(t, running)
	saves := waitMetadataSaves(t, store, 1)
	if saves[0].WorldTimeTicks != wantTime {
		t.Fatalf("提交世界时间 = %d，想要 %d", saves[0].WorldTimeTicks, wantTime)
	}
	if saves[0].Seed != store.metadata.Seed {
		t.Fatalf("提交种子 = %d，想要 %d", saves[0].Seed, store.metadata.Seed)
	}
}

func TestMetadataAutosaveKeepsAtMostOneInFlightAndMergesLatest(t *testing.T) {
	store := newPersistenceTestStore()
	release := make(chan struct{})
	entered := make(chan struct{}, 8)
	store.metadataRespond = func(call int, _ storage.Metadata) error {
		entered <- struct{}{}
		if call == 0 {
			<-release
		}
		return nil
	}
	running := newPersistenceServer(t, store)
	running.config.AutosaveTicks = 4

	first := stepToAutosaveBoundary(t, running)
	select {
	case <-entered:
	case <-time.After(waitDeadline):
		t.Fatal("首个 metadata 保存没有开始")
	}

	// in-flight 期间跨过更多保存边界：只允许一个 in-flight，并保留合并后的最新值。
	var latest uint64
	for range int(running.config.AutosaveTicks) * 3 {
		latest = running.StepForTest().WorldTimeTicks
	}
	if got := metadataSaveCount(store); got != 1 {
		t.Fatalf("in-flight 期间 metadata 提交次数 = %d，想要 1", got)
	}

	// 完成结算发生在下一个 Step 开头，之后合并后的 pending 才会被投递。
	close(release)
	deadline := time.Now().Add(waitDeadline)
	for metadataSaveCount(store) < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("合并后的 metadata 没有被提交，次数 = %d", metadataSaveCount(store))
		}
		running.StepForTest()
	}
	saves := waitMetadataSaves(t, store, 2)
	if saves[0].WorldTimeTicks != first {
		t.Fatalf("首次提交世界时间 = %d，想要 %d", saves[0].WorldTimeTicks, first)
	}
	// 合并后的第二次提交必须投递跨过的多个边界中的最新值，而不是最早那个。
	if saves[1].WorldTimeTicks < latest {
		t.Fatalf("合并后提交世界时间 = %d，想要至少 %d", saves[1].WorldTimeTicks, latest)
	}
	if len(saves) > 2 {
		t.Fatalf("跨过 3 个边界产生了 %d 次提交，想要合并为 2 次", len(saves))
	}
}

func TestMetadataAutosaveFailureRetriesWithBoundedBackoff(t *testing.T) {
	store := newPersistenceTestStore()
	injected := errors.New("injected metadata failure")
	store.metadataRespond = func(call int, _ storage.Metadata) error {
		if call == 0 {
			return injected
		}
		return nil
	}
	running := newPersistenceServer(t, store)
	running.config.AutosaveTicks = 4

	stepToAutosaveBoundary(t, running)
	waitMetadataSaves(t, store, 1)

	// 失败必须可观察，并按既有 tick 退避安排重试。
	deadline := time.Now().Add(waitDeadline)
	for {
		status := running.PersistenceStatus()
		if status.MetadataLastError != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("metadata 保存失败没有被记录")
		}
		running.StepForTest()
	}

	// 退避到期后自动重试并成功。
	for range int(running.config.RetryBaseTicks) * 2 {
		running.StepForTest()
	}
	saves := waitMetadataSaves(t, store, 2)
	if len(saves) < 2 {
		t.Fatalf("重试后 metadata 提交次数 = %d，想要至少 2", len(saves))
	}
	deadline = time.Now().Add(waitDeadline)
	for {
		if running.PersistenceStatus().MetadataLastError == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("成功重试后仍保留失败状态")
		}
		running.StepForTest()
	}
}

func TestMetadataAutosaveDoesNotBlockStepWhenQueueIsFull(t *testing.T) {
	store := newPersistenceTestStore()
	release := make(chan struct{})
	defer close(release)
	store.metadataRespond = func(int, storage.Metadata) error {
		<-release
		return nil
	}
	running := newPersistenceServer(t, store)
	running.config.AutosaveTicks = 2

	// 占满 save 队列：metadata 无法投递时必须保留 pending 而不阻塞 Step。
	for len(running.saveJobs) < cap(running.saveJobs) {
		running.saveJobs <- saveJob{Kind: saveKindMetadata}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 8 {
			running.StepForTest()
		}
	}()
	select {
	case <-done:
	case <-time.After(waitDeadline):
		t.Fatal("save 队列满时 Step 被阻塞")
	}
	if !running.PersistenceStatus().MetadataPending {
		t.Fatal("队列满后没有保留待提交的 metadata")
	}
}

func setMetadataRespond(store *shutdownTestStore, respond func(int, storage.Metadata) error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.metadataRespond = respond
}

func shutdownMetadataSaves(store *shutdownTestStore) []storage.Metadata {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]storage.Metadata(nil), store.metadataSaves...)
}

func TestShutdownFlushesFinalWorldTimeBeforeSync(t *testing.T) {
	store := newShutdownTestStore()
	running, _ := newShutdownTestServer(t, store)
	for range 5 {
		running.StepForTest()
	}

	if err := shutdownWithDeadline(running, waitDeadline); err != nil {
		t.Fatalf("Shutdown 错误 = %v", err)
	}
	// 冻结会再走一个最终 tick，屏障必须保存冻结后的精确时间。
	want := running.engine.WorldTime()
	saves := shutdownMetadataSaves(store)
	if len(saves) == 0 {
		t.Fatal("关服没有保存世界 metadata")
	}
	if got := saves[len(saves)-1].WorldTimeTicks; got != want {
		t.Fatalf("关服保存的世界时间 = %d，想要冻结后的 %d", got, want)
	}
	if got := store.eventLog(); len(got) < 2 || got[len(got)-2] != "sync" || got[len(got)-1] != "close" {
		t.Fatalf("关服事件顺序 = %v，想要以 sync、close 结束", got)
	}
}

func TestShutdownMetadataFailureIsRetryableAndKeepsOwnership(t *testing.T) {
	store := newShutdownTestStore()
	injected := errors.New("injected shutdown metadata failure")
	setMetadataRespond(store, func(int, storage.Metadata) error { return injected })
	running, _ := newShutdownTestServer(t, store)
	running.StepForTest()

	if err := shutdownWithDeadline(running, waitDeadline); !errors.Is(err, injected) {
		t.Fatalf("首次 Shutdown 错误 = %v，想要注入的 metadata 失败", err)
	}
	if syncCalls, closeCalls := store.lifecycleCalls(); syncCalls != 0 || closeCalls != 0 {
		t.Fatalf("metadata 失败后 Sync=%d Close=%d，想要 0,0", syncCalls, closeCalls)
	}
	if !store.worldOwned() {
		t.Fatal("metadata 失败释放了 Store 所有权")
	}

	setMetadataRespond(store, nil)
	if err := shutdownWithDeadline(running, waitDeadline); err != nil {
		t.Fatalf("重试 Shutdown 错误 = %v", err)
	}
	want := running.engine.WorldTime()
	saves := shutdownMetadataSaves(store)
	if got := saves[len(saves)-1].WorldTimeTicks; got != want {
		t.Fatalf("重试保存的世界时间 = %d，想要 %d", got, want)
	}
	if syncCalls, closeCalls := store.lifecycleCalls(); syncCalls != 1 || closeCalls != 1 {
		t.Fatalf("重试后 Sync=%d Close=%d，想要 1,1", syncCalls, closeCalls)
	}
}

func TestShutdownMetadataContextTimeoutDefersToRetry(t *testing.T) {
	store := newShutdownTestStore()
	running, _ := newShutdownTestServer(t, store)
	running.StepForTest()

	ctx, cancel := context.WithCancel(context.Background())
	setMetadataRespond(store, func(int, storage.Metadata) error {
		cancel()
		return context.Canceled
	})
	if err := running.Shutdown(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Shutdown 错误 = %v，想要 context.Canceled", err)
	}
	if syncCalls, closeCalls := store.lifecycleCalls(); syncCalls != 0 || closeCalls != 0 {
		t.Fatalf("取消后 Sync=%d Close=%d，想要 0,0", syncCalls, closeCalls)
	}
	if !store.worldOwned() {
		t.Fatal("取消的关服释放了 Store 所有权")
	}

	setMetadataRespond(store, nil)
	recoverShutdownAfterExpectedFailure(t, running, context.Canceled)
	if store.worldOwned() {
		t.Fatal("成功关服仍保留 Store 所有权")
	}
}
