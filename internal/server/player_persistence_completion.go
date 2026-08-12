package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
)

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
