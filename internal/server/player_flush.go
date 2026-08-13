package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/channing771/mornlea/internal/core"
)

type playerSaveKey struct {
	playerID core.PlayerID
	revision uint64
}

func (p *playerPersistence) Flush(ctx context.Context) error {
	if ctx == nil {
		panic("server: nil player flush context")
	}
	p.completionMu.Lock()
	defer p.completionMu.Unlock()

	attempted := make(map[playerSaveKey]struct{}, playerCacheCapacity)
	failures := make(map[playerSaveKey]error, playerCacheCapacity)
	p.mu.Lock()
	p.flushBarrier = true
	inherited := make(map[playerSaveKey]struct{}, playerCacheCapacity)
	for id, player := range p.cache {
		if !player.inFlight {
			continue
		}
		key := playerSaveKey{playerID: id, revision: player.inFlightRevision}
		inherited[key] = struct{}{}
		attempted[key] = struct{}{}
	}
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.flushBarrier = false
		p.mu.Unlock()
	}()

	if err := p.waitInheritedFlushCompletions(ctx, inherited, failures); err != nil {
		return joinPlayerFlushErrors(failures, err)
	}
	for {
		if err := ctx.Err(); err != nil {
			return joinPlayerFlushErrors(failures, err)
		}

		p.mu.Lock()
		if !p.hasDirtyOrInFlightLocked() {
			p.mu.Unlock()
			return joinPlayerFlushErrors(failures, nil)
		}
		dispatched := false
		if !p.hasInFlightLocked() {
			for _, player := range p.sortedPlayersLocked(func(player *cachedPlayer) bool {
				return !player.loading && !player.inFlight && player.dirty
			}) {
				job := playerFlushJob(player)
				key := playerSaveKey{playerID: job.Save.PlayerID, revision: job.Save.Revision}
				if _, alreadyAttempted := attempted[key]; alreadyAttempted {
					continue
				}
				if p.dispatchFlushLocked(job) {
					attempted[key] = struct{}{}
					dispatched = true
				}
				break
			}
		}
		inFlight := p.hasInFlightLocked()
		p.mu.Unlock()
		if !inFlight && !dispatched {
			return joinPlayerFlushErrors(failures, nil)
		}

		select {
		case completion := <-p.completions:
			key := playerSaveKey{
				playerID: completion.Job.Save.PlayerID,
				revision: completion.Job.Save.Revision,
			}
			p.mu.Lock()
			err := p.applyCompletionWithDispatchLocked(completion, 0, false)
			p.mu.Unlock()
			if err != nil {
				failures[key] = err
			}
		case <-ctx.Done():
			return joinPlayerFlushErrors(failures, ctx.Err())
		case <-p.scheduler.ctx.Done():
			return joinPlayerFlushErrors(failures, p.scheduler.ctx.Err())
		}
	}
}

func (p *playerPersistence) waitInheritedFlushCompletions(
	ctx context.Context,
	inherited map[playerSaveKey]struct{},
	failures map[playerSaveKey]error,
) error {
	for len(inherited) != 0 {
		select {
		case completion := <-p.completions:
			key := playerSaveKey{
				playerID: completion.Job.Save.PlayerID,
				revision: completion.Job.Save.Revision,
			}
			if _, ok := inherited[key]; !ok {
				continue
			}
			delete(inherited, key)
			p.mu.Lock()
			err := p.applyCompletionWithDispatchLocked(completion, 0, false)
			p.mu.Unlock()
			if err != nil {
				failures[key] = err
			}
		case <-ctx.Done():
			return ctx.Err()
		case <-p.scheduler.ctx.Done():
			return p.scheduler.ctx.Err()
		}
	}
	return nil
}

func playerFlushJob(player *cachedPlayer) playerSaveJob {
	if player.retry != nil {
		return *player.retry
	}
	return playerSaveJob{
		Save:    player.save(player.persisted + 1),
		Attempt: 1,
	}
}

func (p *playerPersistence) dispatchFlushLocked(job playerSaveJob) bool {
	player := p.cache[job.Save.PlayerID]
	if player == nil || player.loading || player.inFlight {
		return false
	}
	if !p.scheduler.TrySubmit(job) {
		return false
	}
	player.inFlight = true
	player.inFlightRevision = job.Save.Revision
	if player.matchesSave(job.Save) {
		player.forcePending = false
	}
	if player.retry != nil {
		player.retry = nil
	}
	return true
}

func joinPlayerFlushErrors(failures map[playerSaveKey]error, terminal error) error {
	keys := make([]playerSaveKey, 0, len(failures))
	for key := range failures {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(left, right int) bool {
		if compared := bytes.Compare(keys[left].playerID[:], keys[right].playerID[:]); compared != 0 {
			return compared < 0
		}
		return keys[left].revision < keys[right].revision
	})
	joined := make([]error, 0, len(keys)+1)
	for _, key := range keys {
		joined = append(joined, fmt.Errorf(
			"save player %s revision %d: %w",
			key.playerID,
			key.revision,
			failures[key],
		))
	}
	if terminal != nil {
		joined = append(joined, terminal)
	}
	return errors.Join(joined...)
}
