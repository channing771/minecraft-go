package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
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

func (p *playerPersistence) Flush(ctx context.Context) error {
	if ctx == nil {
		panic("server: nil player flush context")
	}
	p.completionMu.Lock()
	defer p.completionMu.Unlock()
	if err := p.drainInheritedCompletions(ctx); err != nil {
		return err
	}
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
			players := p.sortedPlayersLocked(func(player *cachedPlayer) bool {
				return !player.loading && !player.inFlight && player.dirty
			})
			if len(players) != 0 {
				player := players[0]
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
			case <-p.scheduler.ctx.Done():
				return p.scheduler.ctx.Err()
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
		case <-p.scheduler.ctx.Done():
			return p.scheduler.ctx.Err()
		}
	}
}

func (p *playerPersistence) CloseWorker() {
	p.closeOnce.Do(func() {
		p.scheduler.CloseJobs()
		p.scheduler.Wait()
		close(p.done)
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

func (p *playerPersistence) drainCompletionsLocked(tick uint64) error {
	completions := make([]playerSaveCompletion, 0, playerCacheCapacity)
	draining := true
	for draining {
		select {
		case completion := <-p.completions:
			completions = append(completions, completion)
		default:
			draining = false
		}
	}
	return p.applyCompletionBatchLocked(completions, tick)
}

func (p *playerPersistence) drainInheritedCompletions(ctx context.Context) error {
	p.mu.Lock()
	p.flushBarrier = true
	inherited := make(map[playerSaveIdentity]struct{}, playerCacheCapacity)
	for id, player := range p.cache {
		if player.inFlight {
			inherited[playerSaveIdentity{
				playerID: id,
				revision: player.inFlightRevision,
			}] = struct{}{}
		}
	}
	if len(inherited) == 0 {
		p.flushBarrier = false
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()

	completions := make([]playerSaveCompletion, 0, len(inherited))
	var waitErr error
	for len(inherited) != 0 && waitErr == nil {
		select {
		case completion := <-p.completions:
			identity := playerSaveIdentity{
				playerID: completion.Job.Save.PlayerID,
				revision: completion.Job.Save.Revision,
			}
			if _, ok := inherited[identity]; ok {
				completions = append(completions, completion)
				delete(inherited, identity)
			} else {
				// The barrier prevents any new dispatch after its exact-key snapshot,
				// so a foreign key is necessarily a stale/duplicate completion. Consume
				// it without applying it to the current in-flight generation or errors.
				continue
			}
		case <-ctx.Done():
			waitErr = ctx.Err()
		case <-p.scheduler.ctx.Done():
			waitErr = p.scheduler.ctx.Err()
		}
	}
	p.mu.Lock()
	applyErr := p.applyCompletionBatchWithDispatchLocked(completions, 0, false)
	p.flushBarrier = false
	p.mu.Unlock()
	return errors.Join(applyErr, waitErr)
}

func (p *playerPersistence) applyCompletionBatchLocked(
	completions []playerSaveCompletion,
	tick uint64,
) error {
	return p.applyCompletionBatchWithDispatchLocked(completions, tick, true)
}

func (p *playerPersistence) applyCompletionBatchWithDispatchLocked(
	completions []playerSaveCompletion,
	tick uint64,
	dispatchFollowup bool,
) error {
	sort.Slice(completions, func(left, right int) bool {
		leftJob, rightJob := completions[left].Job, completions[right].Job
		if compared := bytes.Compare(leftJob.Save.PlayerID[:], rightJob.Save.PlayerID[:]); compared != 0 {
			return compared < 0
		}
		return leftJob.Save.Revision < rightJob.Save.Revision
	})
	var result error
	for _, completion := range completions {
		if err := p.applyCompletionWithDispatchLocked(
			completion,
			tick,
			dispatchFollowup,
		); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

func (p *playerPersistence) applyCompletionLocked(
	completion playerSaveCompletion,
	tick uint64,
) error {
	return p.applyCompletionWithDispatchLocked(completion, tick, true)
}

func (p *playerPersistence) applyCompletionWithDispatchLocked(
	completion playerSaveCompletion,
	tick uint64,
	dispatchFollowup bool,
) error {
	player := p.cache[completion.Job.Save.PlayerID]
	if player == nil || player.loading || !player.inFlight ||
		player.inFlightRevision != completion.Job.Save.Revision {
		return nil
	}
	player.inFlight = false
	player.inFlightRevision = 0
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
	if player.dirty && player.forcePending && dispatchFollowup {
		p.dispatchLocked(playerSaveJob{
			Save:    player.save(player.persisted + 1),
			Attempt: 1,
		})
	}
	return nil
}

func (p *playerPersistence) dispatchLocked(job playerSaveJob) bool {
	player := p.cache[job.Save.PlayerID]
	if player == nil || player.loading || player.inFlight || p.flushBarrier {
		return false
	}
	if p.scheduler.TrySubmit(job) {
		player.inFlight = true
		player.inFlightRevision = job.Save.Revision
		if player.matchesSave(job.Save) {
			player.forcePending = false
		}
		return true
	}
	return false
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

func (p *playerPersistence) sortedPlayersLocked(
	include func(*cachedPlayer) bool,
) []*cachedPlayer {
	players := make([]*cachedPlayer, 0, len(p.cache))
	for _, player := range p.cache {
		if include(player) {
			players = append(players, player)
		}
	}
	sort.Slice(players, func(left, right int) bool {
		return bytes.Compare(players[left].id[:], players[right].id[:]) < 0
	})
	return players
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
