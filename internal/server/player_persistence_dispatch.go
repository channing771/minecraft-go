package server

import (
	"bytes"
	"sort"
)

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
