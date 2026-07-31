package server

import (
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

type playerPersistence struct {
	store        storage.PlayerStore
	config       Config
	prepareMu    sync.Mutex
	mu           sync.Mutex
	completionMu sync.Mutex
	cache        *cachedPlayer
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
	p.prepareMu.Lock()
	defer p.prepareMu.Unlock()
	p.mu.Lock()
	if p.cache != nil {
		if p.cache.id == id {
			p.cache.pendingName = name
			restore := p.cache.restore(metadata)
			p.mu.Unlock()
			return restore, nil
		}
		if p.cache.dirty || p.cache.inFlight {
			p.mu.Unlock()
			return sim.PlayerRestore{}, ErrPlayerPersistenceBackpressure
		}
		p.cache = nil
	}
	p.mu.Unlock()

	stored, err := p.store.LoadPlayer(ctx, id)
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case errors.Is(err, storage.ErrPlayerNotFound):
		p.cache = newMissingCachedPlayer(id, name, metadata)
	case err != nil:
		return sim.PlayerRestore{}, err
	default:
		p.cache = cachedPlayerFromStored(stored, name)
	}
	return p.cache.restore(metadata), nil
}

func (p *playerPersistence) Activate(id core.PlayerID, name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cache == nil || p.cache.id != id || p.cache.pendingName != name {
		return ErrPlayerPersistenceBackpressure
	}
	p.cache.active = true
	return nil
}

func (p *playerPersistence) Confirm(id core.PlayerID) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cache == nil || p.cache.id != id || !p.cache.active {
		return
	}
	becamePersistable := p.cache.missing && !p.cache.missingConfirmed
	if becamePersistable {
		p.cache.missingConfirmed = true
	}
	if becamePersistable || p.cache.name != p.cache.pendingName {
		p.cache.name = p.cache.pendingName
		p.cache.dirty = true
	}
	p.cache.pendingName = ""
	p.cache.active = false
}

func (p *playerPersistence) Abort(id core.PlayerID) {
	p.prepareMu.Lock()
	defer p.prepareMu.Unlock()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cache == nil || p.cache.id != id {
		return
	}
	p.cache.pendingName = ""
	p.cache.active = false
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
	if p.cache == nil || p.cache.id != id {
		return ErrPlayerPersistenceBackpressure
	}
	snapshotChanged := !p.cache.hasSnapshot || !playerSnapshotsEqual(p.cache.snapshot, snapshot)
	if snapshotChanged {
		p.cache.snapshot = clonePlayerSnapshot(snapshot)
		p.cache.hasSnapshot = true
		p.cache.hasObservedSnapshot = true
	}
	if p.cache.missing && !p.cache.missingConfirmed {
		return nil
	}
	if snapshotChanged {
		p.cache.dirty = true
	}
	if force {
		p.cache.forcePending = true
	}
	if force && p.cache.dirty && !p.cache.inFlight {
		if p.cache.retry != nil {
			job := *p.cache.retry
			if p.dispatchLocked(job) {
				p.cache.retry = nil
			}
		} else {
			p.dispatchLocked(playerSaveJob{
				Save:     p.cache.save(p.cache.persisted + 1),
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
	if p.cache == nil || p.cache.inFlight {
		return err
	}
	if p.cache.retry != nil {
		if p.cache.retry.NextTick <= tick {
			job := *p.cache.retry
			if p.dispatchLocked(job) {
				p.cache.retry = nil
			}
		}
		return err
	}
	if !p.cache.dirty || tick%p.config.AutosaveTicks != 0 {
		return err
	}
	p.dispatchLocked(playerSaveJob{
		Save:    p.cache.save(p.cache.persisted + 1),
		Attempt: 1,
	})
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
		if p.cache == nil || !p.cache.dirty && !p.cache.inFlight {
			p.mu.Unlock()
			return nil
		}
		if !p.cache.inFlight {
			if p.cache.retry != nil {
				job := *p.cache.retry
				if p.dispatchLocked(job) {
					p.cache.retry = nil
				}
			} else {
				p.dispatchLocked(playerSaveJob{
					Save:    p.cache.save(p.cache.persisted + 1),
					Attempt: 1,
				})
			}
		}
		inFlight := p.cache.inFlight
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
	if p.cache == nil || p.cache.id != completion.Job.Save.PlayerID || !p.cache.inFlight {
		return nil
	}
	p.cache.inFlight = false
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
		p.cache.retry = &retry
		p.cache.dirty = true
		return err
	}
	p.cache.persisted = completion.Revision
	p.cache.missing = false
	p.cache.missingConfirmed = false
	p.cache.retry = nil
	p.cache.dirty = !p.cache.matchesSave(completion.Job.Save)
	if p.cache.dirty && p.cache.forcePending {
		p.dispatchLocked(playerSaveJob{
			Save:    p.cache.save(p.cache.persisted + 1),
			Attempt: 1,
		})
	}
	return nil
}

func (p *playerPersistence) dispatchLocked(job playerSaveJob) bool {
	if p.cache == nil || p.cache.inFlight {
		return false
	}
	select {
	case p.jobs <- job:
		p.cache.inFlight = true
		if p.cache.matchesSave(job.Save) {
			p.cache.forcePending = false
		}
		return true
	default:
		return false
	}
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
