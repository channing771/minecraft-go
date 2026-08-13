package server

import (
	"context"
	"sync"

	"github.com/channing771/mornlea/internal/storage"
)

const (
	playerSaveWorkerCount  = 2
	playerSaveJobCapacity  = 16
	playerSaveDoneCapacity = 2
)

type playerSaveScheduler struct {
	store       storage.PlayerStore
	jobs        chan playerSaveJob
	completions chan playerSaveCompletion
	ctx         context.Context
	cancel      context.CancelFunc
	waitGroup   sync.WaitGroup
	submitMu    sync.RWMutex
	closed      bool
	closeOnce   sync.Once
}

func newPlayerSaveScheduler(store storage.PlayerStore) *playerSaveScheduler {
	ctx, cancel := context.WithCancel(context.Background())
	scheduler := &playerSaveScheduler{
		store:       store,
		jobs:        make(chan playerSaveJob, playerSaveJobCapacity),
		completions: make(chan playerSaveCompletion, playerSaveDoneCapacity),
		ctx:         ctx,
		cancel:      cancel,
	}
	scheduler.waitGroup.Add(playerSaveWorkerCount)
	for range playerSaveWorkerCount {
		go scheduler.worker()
	}
	return scheduler
}

func (scheduler *playerSaveScheduler) CloseJobs() {
	scheduler.closeOnce.Do(func() {
		scheduler.submitMu.Lock()
		scheduler.closed = true
		close(scheduler.jobs)
		scheduler.cancel()
		scheduler.submitMu.Unlock()
	})
}

func (scheduler *playerSaveScheduler) Wait() {
	scheduler.waitGroup.Wait()
}

func (scheduler *playerSaveScheduler) TrySubmit(job playerSaveJob) bool {
	scheduler.submitMu.RLock()
	defer scheduler.submitMu.RUnlock()
	if scheduler.closed {
		return false
	}
	select {
	case scheduler.jobs <- job:
		return true
	default:
		return false
	}
}

func (scheduler *playerSaveScheduler) worker() {
	defer scheduler.waitGroup.Done()
	for {
		select {
		case <-scheduler.ctx.Done():
			return
		case job, ok := <-scheduler.jobs:
			if !ok {
				return
			}
			revision, err := scheduler.store.SavePlayer(
				scheduler.ctx,
				clonePlayerSave(job.Save),
			)
			completion := playerSaveCompletion{
				Job:      job,
				Revision: revision,
				Err:      err,
			}
			select {
			case scheduler.completions <- completion:
			case <-scheduler.ctx.Done():
				return
			}
		}
	}
}
