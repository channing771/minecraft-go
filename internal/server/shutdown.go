package server

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"minecraft-go/internal/sim"
	"minecraft-go/internal/storage"
)

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
		server.drainAcquired()
		server.drainGenerated()
		server.engine.Step()
		if hasTrustedCenter {
			server.appliedTrustedObserver = appliedTrustedObserverCenter{
				dimension: trustedCenter.dimension,
				center:    trustedCenter.center,
				sequence:  trustedSequence,
			}
		}
		server.session.close()
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
