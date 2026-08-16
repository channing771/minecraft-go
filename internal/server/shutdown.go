package server

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/channing771/mornlea/internal/sim"
	"github.com/channing771/mornlea/internal/storage"
)

func waitForHostWorkers(ctx context.Context, workers *sync.WaitGroup) error {
	done := make(chan struct{})
	go func() {
		workers.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type storeShutdownPhase uint8

const (
	storeShutdownNeedsSync storeShutdownPhase = iota
	storeShutdownNeedsClose
	storeShutdownClosed
)

// Shutdown freezes authority, drains persistence, and releases Store ownership.
// A failed call leaves the frozen Server retryable by a later call.
func (server *Server) Shutdown(ctx context.Context) error {
	if ctx == nil {
		panic("server: nil shutdown context")
	}
	select {
	case <-server.shutdownGate:
	default:
		select {
		case <-server.shutdownGate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	defer func() { server.shutdownGate <- struct{}{} }()

	server.stepMu.Lock()
	if server.lifecycle == serverClosed {
		server.stepMu.Unlock()
		return nil
	}
	var freezeErr error
	if server.lifecycle == serverRunning {
		server.lifecycle = serverClosing
		freezeErr = server.drainSaveCompletionsWithError()
		trustedCenter, trustedSequence, hasTrustedCenter := server.drainTrustedObserverCenter()
		server.drainIncoming()
		_ = server.drainIncomingChats()
		server.drainAcquired()
		server.drainGenerated()
		// 关服顺序（spec：companion-task-queue）：聊天接受已随生命周期冻结与
		// 会话拆除停止，这里取消在途模型请求，再冻结队列与 actor 状态并做
		// 最终 AI 保存，最后才轮到世界存储的同步与关闭（见本函数末段）。
		if server.companionManager != nil {
			server.companionManager.beginShutdown()
		}
		server.engine.Step()
		if server.companions != nil {
			server.companions.Observe(
				server.engine.CompanionBodies(),
				server.companionManagerTaskStates(),
			)
		}
		if hasTrustedCenter {
			server.appliedTrustedObserver = appliedTrustedObserverCenter{
				dimension: trustedCenter.dimension,
				center:    trustedCenter.center,
				sequence:  trustedSequence,
			}
		}
		for _, id := range server.sortedSessionIDsLocked() {
			current := server.sessions[id]
			server.detachSessionLocked(id, current.generation, nil)
		}
		if server.trustedObserver != nil {
			server.detachTrustedObserverLocked(
				server.trustedObserver.id,
				server.trustedObserver.generation,
				nil,
			)
		}
		server.cancel()
	}
	server.stepMu.Unlock()

	if err := waitForServerWorkers(ctx, server.runtimeDone); err != nil {
		return server.shutdownOwnerContextError(err, freezeErr, nil)
	}
	if freezeErr != nil {
		return server.persistenceErrorWithContext(freezeErr, ctx)
	}
	if err := server.flushFrozen(ctx); err != nil {
		return err
	}
	if err := server.flushMetadata(ctx); err != nil {
		return err
	}
	if server.companions != nil {
		if err := server.companions.Flush(ctx); err != nil {
			return server.persistenceErrorWithContext(
				fmt.Errorf("flush companions: %w", err), ctx,
			)
		}
	}
	if server.storePhase == storeShutdownNeedsSync {
		if err := server.store.Sync(ctx); err != nil {
			return server.persistenceErrorWithContext(fmt.Errorf("sync world: %w", err), ctx)
		}
		server.storePhase = storeShutdownNeedsClose
	}
	if err := ctx.Err(); err != nil {
		return server.shutdownOwnerContextError(err, nil, nil)
	}
	if server.storePhase == storeShutdownNeedsClose {
		if err := server.store.Close(); err != nil {
			return server.persistenceErrorWithContext(fmt.Errorf("close world: %w", err), ctx)
		}
		server.storePhase = storeShutdownClosed
	}
	if server.companions != nil {
		server.companions.Close()
	}
	if server.companionManager != nil {
		server.companionManager.close()
	}
	server.cancelSaves()
	<-server.saveDone

	server.stepMu.Lock()
	server.autosaveActive = false
	server.lifecycle = serverClosed
	close(server.closedDone)
	server.stepMu.Unlock()
	return nil
}

func (server *Server) flushFrozen(ctx context.Context) error {
	var pending []saveJob
	for {
		server.stepMu.Lock()
		if err := server.drainSaveCompletionsWithError(); err != nil {
			server.releasePendingShutdownJobs(pending)
			server.stepMu.Unlock()
			return server.persistenceErrorWithContext(err, ctx)
		}

		for server.dispatchShutdownRetry() {
		}
		if len(pending) == 0 && len(server.retry) == 0 {
			snapshots := server.engine.PersistenceSnapshots(
				server.config.SaveChunks,
				server.config.SaveBytes,
				sim.SaveAll,
			)
			if len(snapshots) != 0 {
				pending = groupSaveJobs(snapshots)
				for index := range pending {
					pending[index].Attempt = 1
				}
			}
		}
	dispatchPending:
		for len(pending) != 0 {
			select {
			case server.saveJobs <- pending[0]:
				pending = pending[1:]
			default:
				break dispatchPending
			}
		}
		stats := server.engine.PersistenceStats()
		drained := len(pending) == 0 && stats.DirtyChunks == 0 &&
			stats.InFlightChunks == 0 && len(server.retry) == 0 &&
			len(server.retryInFlight) == 0
		server.stepMu.Unlock()
		if drained {
			return nil
		}

		if len(pending) != 0 {
			select {
			case server.saveJobs <- pending[0]:
				pending = pending[1:]
			case completion := <-server.saveCompletions:
				server.stepMu.Lock()
				err := server.applySaveCompletion(completion)
				if err != nil {
					server.releasePendingShutdownJobs(pending)
					pending = nil
				}
				server.stepMu.Unlock()
				if err != nil {
					return server.persistenceErrorWithContext(err, ctx)
				}
			case <-ctx.Done():
				return server.shutdownOwnerContextError(ctx.Err(), nil, pending)
			}
			continue
		}

		select {
		case completion := <-server.saveCompletions:
			server.stepMu.Lock()
			err := server.applySaveCompletion(completion)
			server.stepMu.Unlock()
			if err != nil {
				return server.persistenceErrorWithContext(err, ctx)
			}
		case <-ctx.Done():
			return server.shutdownOwnerContextError(ctx.Err(), nil, nil)
		}
	}
}

// flushMetadata 把冻结后的最终权威世界时间纳入可重试关服屏障。
// 失败时返回错误并保留可重试状态，磁盘上的旧 metadata 保持完整。
func (server *Server) flushMetadata(ctx context.Context) error {
	server.stepMu.Lock()
	target := server.engine.WorldTime()
	server.metadataSave.latest = target
	server.stepMu.Unlock()

	for {
		server.stepMu.Lock()
		if err := server.drainSaveCompletionsWithError(); err != nil {
			server.stepMu.Unlock()
			return server.persistenceErrorWithContext(err, ctx)
		}
		inFlight := server.metadataSave.inFlight
		done := !inFlight && server.metadataSave.committed >= target
		metadata := server.store.Metadata()
		metadata.WorldTimeTicks = target
		server.stepMu.Unlock()
		if done {
			return nil
		}

		if !inFlight {
			// 冻结后没有其他调度者，阻塞投递只等待 worker 取走这份最终快照。
			select {
			case server.saveJobs <- saveJob{
				Kind:     saveKindMetadata,
				Metadata: metadata,
				Attempt:  1,
			}:
				server.stepMu.Lock()
				server.metadataSave.pending = false
				server.metadataSave.inFlight = true
				server.stepMu.Unlock()
			case <-ctx.Done():
				return server.shutdownOwnerContextError(ctx.Err(), nil, nil)
			}
		}

		select {
		case completion := <-server.saveCompletions:
			server.stepMu.Lock()
			err := server.applySaveCompletion(completion)
			server.stepMu.Unlock()
			if err != nil {
				return server.persistenceErrorWithContext(err, ctx)
			}
		case <-ctx.Done():
			return server.shutdownOwnerContextError(ctx.Err(), nil, nil)
		}
	}
}

func (server *Server) persistenceErrorWithContext(err error, ctx context.Context) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return server.shutdownOwnerContextError(ctxErr, err, nil)
	}
	return err
}

func (server *Server) releasePendingShutdownJobs(jobs []saveJob) {
	for _, job := range jobs {
		server.engine.FailPersistence(job.Snapshots)
	}
}

func (server *Server) dispatchShutdownRetry() bool {
	type candidate struct {
		region storage.RegionKey
		retry  retrySave
	}
	candidates := make([]candidate, 0)
	for region, cohorts := range server.retry {
		for _, cohort := range cohorts {
			candidates = append(candidates, candidate{region: region, retry: cohort})
		}
	}
	if len(candidates) == 0 {
		return false
	}
	sort.Slice(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		if left.retry.NextTick != right.retry.NextTick {
			return left.retry.NextTick < right.retry.NextTick
		}
		if left.region != right.region {
			return regionKeyLess(left.region, right.region)
		}
		return left.retry.Job.RetryID < right.retry.Job.RetryID
	})
	selected := candidates[0]
	attempt := selected.retry.Attempts
	if attempt < ^uint32(0) {
		attempt++
	}
	job := saveJob{
		Region:    selected.region,
		Snapshots: append([]sim.ChunkSaveSnapshot(nil), selected.retry.Job.Snapshots...),
		Attempt:   attempt,
		Retry:     true,
		RetryID:   selected.retry.Job.RetryID,
	}
	select {
	case server.saveJobs <- job:
		server.removePendingRetryCohort(selected.region, job.RetryID)
		selected.retry.Job = job
		server.retryInFlight[job.RetryID] = selected.retry
		return true
	default:
		return false
	}
}

func waitForServerWorkers(ctx context.Context, done <-chan struct{}) error {
	select {
	case <-done:
		return nil
	default:
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (server *Server) shutdownOwnerContextError(
	ctxErr error,
	persistenceErr error,
	pending []saveJob,
) error {
	server.stepMu.Lock()
	server.releasePendingShutdownJobs(pending)
	readyErr := server.drainSaveCompletionsWithError()
	unresolvedErr := server.unresolvedSaveErrorLocked()
	server.stepMu.Unlock()
	return errors.Join(persistenceErr, readyErr, unresolvedErr, ctxErr)
}

func (server *Server) unresolvedSaveErrorLocked() error {
	type unresolved struct {
		region  storage.RegionKey
		retryID uint64
		err     error
	}
	entries := make([]unresolved, 0, len(server.retry)+len(server.retryInFlight))
	for region, cohorts := range server.retry {
		for _, cohort := range cohorts {
			if cohort.LastError != nil {
				entries = append(entries, unresolved{
					region: region, retryID: cohort.Job.RetryID, err: cohort.LastError,
				})
			}
		}
	}
	for retryID, cohort := range server.retryInFlight {
		if cohort.LastError != nil {
			entries = append(entries, unresolved{
				region: cohort.Job.Region, retryID: retryID, err: cohort.LastError,
			})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].region != entries[j].region {
			return regionKeyLess(entries[i].region, entries[j].region)
		}
		return entries[i].retryID < entries[j].retryID
	})
	errs := make([]error, 0, len(entries))
	for _, entry := range entries {
		errs = append(errs, fmt.Errorf(
			"unresolved save region %+v retry %d: %w",
			entry.region,
			entry.retryID,
			entry.err,
		))
	}
	return errors.Join(errs...)
}
