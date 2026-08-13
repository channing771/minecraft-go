package server

import (
	"context"
	"errors"
	"sync"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/sim"
	"github.com/channing771/mornlea/internal/storage"
)

var ErrPlayerPersistenceBackpressure = errors.New("server: player persistence backpressure")

const playerCacheCapacity = 16

type playerPersistence struct {
	store        storage.PlayerStore
	config       Config
	mu           sync.Mutex
	completionMu sync.Mutex
	cache        map[core.PlayerID]*cachedPlayer
	flushBarrier bool
	scheduler    *playerSaveScheduler
	jobs         chan playerSaveJob
	completions  chan playerSaveCompletion
	done         chan struct{}
	closeOnce    sync.Once
}

type cachedPlayer struct {
	id                  core.PlayerID
	name, pendingName   string
	persisted           uint64
	snapshot            sim.PlayerSnapshot
	hasSnapshot         bool
	hasObservedSnapshot bool
	missing             bool
	missingConfirmed    bool
	dirty               bool
	active, inFlight    bool
	inFlightRevision    uint64
	forcePending        bool
	retry               *playerSaveJob
	loadDone            chan struct{}
	loadErr             error
	loading             bool
}

type playerSaveJob struct {
	Save     storage.PlayerSave
	Attempt  uint32
	NextTick uint64
}

type playerSaveCompletion struct {
	Job      playerSaveJob
	Revision uint64
	Err      error
}

type playerSaveIdentity struct {
	playerID core.PlayerID
	revision uint64
}

func newPlayerPersistence(store storage.PlayerStore, config Config) *playerPersistence {
	scheduler := newPlayerSaveScheduler(store)
	persistence := &playerPersistence{
		store:       store,
		config:      config,
		cache:       make(map[core.PlayerID]*cachedPlayer),
		scheduler:   scheduler,
		jobs:        scheduler.jobs,
		completions: scheduler.completions,
		done:        make(chan struct{}),
	}
	return persistence
}

func (p *playerPersistence) Prepare(
	ctx context.Context,
	id core.PlayerID,
	name string,
	metadata storage.Metadata,
) (sim.PlayerRestore, error) {
	for {
		p.mu.Lock()
		if player := p.cache[id]; player != nil {
			if player.pendingName != "" && player.pendingName != name {
				p.mu.Unlock()
				return sim.PlayerRestore{}, ErrPlayerPersistenceBackpressure
			}
			if player.loading {
				loadDone := player.loadDone
				p.mu.Unlock()
				select {
				case <-loadDone:
				case <-ctx.Done():
					return sim.PlayerRestore{}, ctx.Err()
				}
				p.mu.Lock()
				loadErr := player.loadErr
				if loadErr != nil {
					p.mu.Unlock()
					return sim.PlayerRestore{}, loadErr
				}
				if p.cache[id] != player || player.loading || player.pendingName != name {
					p.mu.Unlock()
					return sim.PlayerRestore{}, ErrPlayerPersistenceBackpressure
				}
				restore := player.restore(metadata)
				p.mu.Unlock()
				return restore, nil
			}
			player.pendingName = name
			restore := player.restore(metadata)
			p.mu.Unlock()
			return restore, nil
		}

		p.evictCleanLocked()
		if len(p.cache) >= playerCacheCapacity {
			p.mu.Unlock()
			return sim.PlayerRestore{}, ErrPlayerPersistenceBackpressure
		}
		placeholder := &cachedPlayer{
			id:          id,
			pendingName: name,
			loadDone:    make(chan struct{}),
			loading:     true,
		}
		p.cache[id] = placeholder
		p.mu.Unlock()

		stored, err := p.store.LoadPlayer(ctx, id)
		p.mu.Lock()
		loadDone := placeholder.loadDone
		if p.cache[id] != placeholder {
			placeholder.loadErr = ErrPlayerPersistenceBackpressure
			placeholder.loading = false
			close(loadDone)
			p.mu.Unlock()
			return sim.PlayerRestore{}, ErrPlayerPersistenceBackpressure
		}
		switch {
		case errors.Is(err, storage.ErrPlayerNotFound):
			loaded := newMissingCachedPlayer(id, name, metadata)
			*placeholder = *loaded
			placeholder.loadDone = loadDone
		case err != nil:
			placeholder.loadErr = err
			placeholder.loading = false
			if p.cache[id] == placeholder {
				delete(p.cache, id)
			}
			close(loadDone)
			p.mu.Unlock()
			return sim.PlayerRestore{}, err
		default:
			loaded := cachedPlayerFromStored(stored, name)
			*placeholder = *loaded
			placeholder.loadDone = loadDone
		}
		placeholder.loading = false
		close(loadDone)
		restore := placeholder.restore(metadata)
		p.mu.Unlock()
		return restore, nil
	}
}

func (p *playerPersistence) Activate(id core.PlayerID, name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	player := p.cache[id]
	if player == nil || player.loading || player.pendingName != name {
		return ErrPlayerPersistenceBackpressure
	}
	player.active = true
	return nil
}

func (p *playerPersistence) Confirm(id core.PlayerID) {
	p.mu.Lock()
	defer p.mu.Unlock()
	player := p.cache[id]
	if player == nil || player.loading || !player.active || player.pendingName == "" {
		return
	}
	becamePersistable := player.missing && !player.missingConfirmed
	if becamePersistable {
		player.missingConfirmed = true
	}
	if becamePersistable || player.name != player.pendingName {
		player.name = player.pendingName
		player.dirty = true
	}
	player.pendingName = ""
}

func (p *playerPersistence) Abort(id core.PlayerID) {
	p.mu.Lock()
	player := p.cache[id]
	if player == nil {
		p.mu.Unlock()
		return
	}
	if player.loading {
		loadDone := player.loadDone
		p.mu.Unlock()
		<-loadDone
		p.mu.Lock()
	}
	defer p.mu.Unlock()
	if p.cache[id] != player || player.loading {
		return
	}
	player.pendingName = ""
	player.active = false
	if player.missing && !player.missingConfirmed && p.cache[id] == player {
		delete(p.cache, id)
	}
}

func (p *playerPersistence) Deactivate(id core.PlayerID) {
	p.mu.Lock()
	defer p.mu.Unlock()
	player := p.cache[id]
	if player == nil || player.loading {
		return
	}
	player.active = false
	if p.cache[id] == player && player.evictable() {
		delete(p.cache, id)
	}
}

func (p *playerPersistence) Observe(
	id core.PlayerID,
	_ string,
	snapshot sim.PlayerSnapshot,
	tick uint64,
	force bool,
) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	player := p.cache[id]
	if player == nil || player.loading {
		return ErrPlayerPersistenceBackpressure
	}
	snapshotChanged := !player.hasSnapshot || !playerSnapshotsEqual(player.snapshot, snapshot)
	if snapshotChanged {
		player.snapshot = clonePlayerSnapshot(snapshot)
		player.hasSnapshot = true
		player.hasObservedSnapshot = true
	}
	if player.missing && !player.missingConfirmed {
		return nil
	}
	if snapshotChanged {
		player.dirty = true
	}
	if force {
		player.forcePending = true
	}
	if force && player.dirty && !player.inFlight {
		if player.retry != nil {
			job := *player.retry
			if p.dispatchLocked(job) {
				player.retry = nil
			}
		} else {
			p.dispatchLocked(playerSaveJob{
				Save:     player.save(player.persisted + 1),
				Attempt:  1,
				NextTick: tick,
			})
		}
	}
	return nil
}

func (p *playerPersistence) Poll(tick uint64) error {
	p.completionMu.Lock()
	defer p.completionMu.Unlock()
	p.mu.Lock()
	defer p.mu.Unlock()
	err := p.drainCompletionsLocked(tick)
	for _, player := range p.sortedPlayersLocked(func(player *cachedPlayer) bool {
		return !player.loading && !player.inFlight && player.retry != nil &&
			player.retry.NextTick <= tick
	}) {
		job := *player.retry
		if p.dispatchLocked(job) {
			player.retry = nil
		}
	}
	if tick%p.config.AutosaveTicks != 0 {
		return err
	}
	for _, player := range p.sortedPlayersLocked(func(player *cachedPlayer) bool {
		return !player.loading && !player.inFlight && player.dirty && player.retry == nil
	}) {
		p.dispatchLocked(playerSaveJob{
			Save:    player.save(player.persisted + 1),
			Attempt: 1,
		})
	}
	return err
}

func (p *playerPersistence) CloseWorker() {
	p.closeOnce.Do(func() {
		p.scheduler.CloseJobs()
		p.scheduler.Wait()
		p.completionMu.Lock()
		p.mu.Lock()
		_ = p.drainCompletionsLocked(0)
		p.mu.Unlock()
		p.completionMu.Unlock()
		close(p.done)
	})
}
