package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/storage"
)

var ErrPlayerPersistenceBackpressure = errors.New("server: player persistence backpressure")

const playerCacheCapacity = 16

type playerPersistence struct {
	store        storage.PlayerStore
	config       Config
	mu           sync.Mutex
	completionMu sync.Mutex
	cache        map[core.PlayerID]*cachedPlayer
	jobs         chan playerSaveJob
	completions  chan playerSaveCompletion
	ctx          context.Context
	cancel       context.CancelFunc
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

func newPlayerPersistence(store storage.PlayerStore, config Config) *playerPersistence {
	ctx, cancel := context.WithCancel(context.Background())
	persistence := &playerPersistence{
		store:       store,
		config:      config,
		cache:       make(map[core.PlayerID]*cachedPlayer),
		jobs:        make(chan playerSaveJob, 1),
		completions: make(chan playerSaveCompletion, 1),
		ctx:         ctx,
		cancel:      cancel,
		done:        make(chan struct{}),
	}
	go persistence.saveWorker()
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
	if p.hasInFlightLocked() {
		return err
	}
	if player := p.nextRetryLocked(tick); player != nil {
		job := *player.retry
		if p.dispatchLocked(job) {
			player.retry = nil
		}
		return err
	}
	if tick%p.config.AutosaveTicks != 0 {
		return err
	}
	if player := p.nextDirtyLocked(false); player != nil {
		p.dispatchLocked(playerSaveJob{
			Save:    player.save(player.persisted + 1),
			Attempt: 1,
		})
	}
	return err
}

func (p *playerPersistence) Flush(ctx context.Context) error {
	if ctx == nil {
		panic("server: nil player flush context")
	}
	p.completionMu.Lock()
	defer p.completionMu.Unlock()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		p.mu.Lock()
		if err := p.drainCompletionsLocked(0); err != nil {
			p.mu.Unlock()
			return err
		}
		if !p.hasDirtyOrInFlightLocked() {
			p.mu.Unlock()
			return nil
		}
		if !p.hasInFlightLocked() {
			if player := p.nextDirtyLocked(true); player != nil {
				if player.retry != nil {
					job := *player.retry
					if p.dispatchLocked(job) {
						player.retry = nil
					}
				} else {
					p.dispatchLocked(playerSaveJob{
						Save:    player.save(player.persisted + 1),
						Attempt: 1,
					})
				}
			}
		}
		inFlight := p.hasInFlightLocked()
		p.mu.Unlock()
		if !inFlight {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-p.ctx.Done():
				return p.ctx.Err()
			default:
			}
			continue
		}

		select {
		case completion := <-p.completions:
			p.mu.Lock()
			err := p.applyCompletionLocked(completion, 0)
			p.mu.Unlock()
			if err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		case <-p.ctx.Done():
			return p.ctx.Err()
		}
	}
}

func (p *playerPersistence) CloseWorker() {
	p.closeOnce.Do(func() {
		p.cancel()
		<-p.done
	})
}

func (player *cachedPlayer) restore(metadata storage.Metadata) sim.PlayerRestore {
	restore := sim.PlayerRestore{
		SpawnDimension: metadata.SpawnDimension,
		SpawnAnchor:    metadata.SpawnAnchor,
	}
	if !player.hasSnapshot || player.missing && !player.hasObservedSnapshot {
		return restore
	}
	current := player.snapshot.Current
	restore.Current = &current
	if player.snapshot.Safe != nil {
		safe := *player.snapshot.Safe
		restore.Safe = &safe
	}
	restore.Yaw = player.snapshot.Yaw
	restore.Pitch = player.snapshot.Pitch
	return restore
}

func cachedPlayerFromStored(stored storage.StoredPlayer, pendingName string) *cachedPlayer {
	snapshot := sim.PlayerSnapshot{
		Current: sim.PlayerLocation{
			Dimension: stored.Current.Dimension,
			Position:  mgl32.Vec3(stored.Current.Position),
		},
		Yaw:   stored.Yaw,
		Pitch: stored.Pitch,
	}
	if stored.Safe != nil {
		snapshot.Safe = &sim.PlayerLocation{
			Dimension: stored.Safe.Dimension,
			Position:  mgl32.Vec3(stored.Safe.Position),
		}
	}
	return &cachedPlayer{
		id:                  stored.PlayerID,
		name:                stored.DisplayName,
		pendingName:         pendingName,
		persisted:           stored.Revision,
		snapshot:            snapshot,
		hasSnapshot:         true,
		hasObservedSnapshot: true,
		dirty:               stored.NeedsRewrite,
	}
}

func newMissingCachedPlayer(
	id core.PlayerID,
	name string,
	metadata storage.Metadata,
) *cachedPlayer {
	anchor := metadata.SpawnAnchor
	return &cachedPlayer{
		id:          id,
		pendingName: name,
		snapshot: sim.PlayerSnapshot{Current: sim.PlayerLocation{
			Dimension: metadata.SpawnDimension,
			Position: mgl32.Vec3{
				float32(anchor.X)*core.SectionSize + 0.5,
				core.MaxY + 1,
				float32(anchor.Z)*core.SectionSize + 0.5,
			},
		}},
		hasSnapshot: true,
		missing:     true,
	}
}

func (p *playerPersistence) saveWorker() {
	defer close(p.done)
	for {
		select {
		case <-p.ctx.Done():
			return
		case job := <-p.jobs:
			revision, err := p.store.SavePlayer(p.ctx, clonePlayerSave(job.Save))
			completion := playerSaveCompletion{
				Job:      job,
				Revision: revision,
				Err:      err,
			}
			select {
			case p.completions <- completion:
			case <-p.ctx.Done():
				return
			}
		}
	}
}

func (p *playerPersistence) drainCompletionsLocked(tick uint64) error {
	var result error
	for {
		select {
		case completion := <-p.completions:
			if err := p.applyCompletionLocked(completion, tick); err != nil {
				result = errors.Join(result, err)
			}
		default:
			return result
		}
	}
}

func (p *playerPersistence) applyCompletionLocked(
	completion playerSaveCompletion,
	tick uint64,
) error {
	player := p.cache[completion.Job.Save.PlayerID]
	if player == nil || player.loading || !player.inFlight {
		return nil
	}
	player.inFlight = false
	err := completion.Err
	if err == nil && completion.Revision != completion.Job.Save.Revision {
		err = fmt.Errorf(
			"server: player save revision %d does not match submitted %d",
			completion.Revision,
			completion.Job.Save.Revision,
		)
	}
	if err != nil {
		retry := completion.Job
		attempt := retry.Attempt
		if attempt == 0 {
			attempt = 1
		}
		retry.NextTick = saturatingAddUint64(
			tick,
			retryDelay(p.config.RetryBaseTicks, p.config.RetryMaxTicks, attempt),
		)
		if attempt < ^uint32(0) {
			retry.Attempt = attempt + 1
		}
		player.retry = &retry
		player.dirty = true
		return err
	}
	player.persisted = completion.Revision
	player.missing = false
	player.missingConfirmed = false
	player.retry = nil
	player.dirty = !player.matchesSave(completion.Job.Save)
	if player.dirty && player.forcePending {
		p.dispatchLocked(playerSaveJob{
			Save:    player.save(player.persisted + 1),
			Attempt: 1,
		})
	}
	return nil
}

func (p *playerPersistence) dispatchLocked(job playerSaveJob) bool {
	player := p.cache[job.Save.PlayerID]
	if player == nil || player.loading || player.inFlight {
		return false
	}
	select {
	case p.jobs <- job:
		player.inFlight = true
		if player.matchesSave(job.Save) {
			player.forcePending = false
		}
		return true
	default:
		return false
	}
}

func (p *playerPersistence) evictCleanLocked() {
	for id, player := range p.cache {
		if player.evictable() && p.cache[id] == player {
			delete(p.cache, id)
		}
	}
}

func (p *playerPersistence) hasInFlightLocked() bool {
	for _, player := range p.cache {
		if player.inFlight {
			return true
		}
	}
	return false
}

func (p *playerPersistence) hasDirtyOrInFlightLocked() bool {
	for _, player := range p.cache {
		if player.dirty || player.inFlight {
			return true
		}
	}
	return false
}

func (p *playerPersistence) nextRetryLocked(tick uint64) *cachedPlayer {
	var next *cachedPlayer
	for _, player := range p.cache {
		if player.loading || player.retry == nil || player.retry.NextTick > tick {
			continue
		}
		if next == nil || bytes.Compare(player.id[:], next.id[:]) < 0 {
			next = player
		}
	}
	return next
}

func (p *playerPersistence) nextDirtyLocked(includeRetry bool) *cachedPlayer {
	var next *cachedPlayer
	for _, player := range p.cache {
		if player.loading || !player.dirty || !includeRetry && player.retry != nil {
			continue
		}
		if next == nil || bytes.Compare(player.id[:], next.id[:]) < 0 {
			next = player
		}
	}
	return next
}

func (player *cachedPlayer) evictable() bool {
	return !player.loading && !player.active && player.pendingName == "" &&
		!player.dirty && !player.inFlight && player.retry == nil
}

func (player *cachedPlayer) save(revision uint64) storage.PlayerSave {
	save := storage.PlayerSave{
		PlayerID:    player.id,
		Revision:    revision,
		DisplayName: player.name,
		Current: storage.PlayerLocation{
			Dimension: player.snapshot.Current.Dimension,
			Position:  [3]float32(player.snapshot.Current.Position),
		},
		Yaw:   player.snapshot.Yaw,
		Pitch: player.snapshot.Pitch,
	}
	if player.snapshot.Safe != nil {
		save.Safe = &storage.PlayerLocation{
			Dimension: player.snapshot.Safe.Dimension,
			Position:  [3]float32(player.snapshot.Safe.Position),
		}
	}
	return save
}

func (player *cachedPlayer) matchesSave(save storage.PlayerSave) bool {
	if !player.hasSnapshot || player.id != save.PlayerID || player.name != save.DisplayName ||
		player.snapshot.Current.Dimension != save.Current.Dimension ||
		[3]float32(player.snapshot.Current.Position) != save.Current.Position ||
		player.snapshot.Yaw != save.Yaw || player.snapshot.Pitch != save.Pitch {
		return false
	}
	if player.snapshot.Safe == nil || save.Safe == nil {
		return player.snapshot.Safe == nil && save.Safe == nil
	}
	return player.snapshot.Safe.Dimension == save.Safe.Dimension &&
		[3]float32(player.snapshot.Safe.Position) == save.Safe.Position
}

func clonePlayerSave(save storage.PlayerSave) storage.PlayerSave {
	clone := save
	if save.Safe != nil {
		safe := *save.Safe
		clone.Safe = &safe
	}
	return clone
}

func clonePlayerSnapshot(snapshot sim.PlayerSnapshot) sim.PlayerSnapshot {
	clone := snapshot
	if snapshot.Safe != nil {
		safe := *snapshot.Safe
		clone.Safe = &safe
	}
	return clone
}

func playerSnapshotsEqual(left, right sim.PlayerSnapshot) bool {
	if left.Current != right.Current || left.Yaw != right.Yaw || left.Pitch != right.Pitch {
		return false
	}
	if left.Safe == nil || right.Safe == nil {
		return left.Safe == nil && right.Safe == nil
	}
	return *left.Safe == *right.Safe
}
