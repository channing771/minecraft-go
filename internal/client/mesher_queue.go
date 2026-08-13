package client

import "github.com/channing771/mornlea/internal/core"

func (mesher *Mesher) markDirtyLocked(key core.SectionKey) {
	mesher.nextGeneration++
	if mesher.nextGeneration == 0 {
		mesher.nextGeneration++
	}
	mesher.dirty[key] = mesher.nextGeneration
	mesher.enqueueReadyLocked(key)
}

func (mesher *Mesher) enqueueReadyLocked(key core.SectionKey) {
	if mesher.isClosed {
		return
	}
	if _, dirty := mesher.dirty[key]; !dirty {
		return
	}
	if _, queued := mesher.queued[key]; queued {
		return
	}
	if _, inFlight := mesher.inFlight[key]; inFlight {
		return
	}
	mesher.ready.Add(key)
}

func (mesher *Mesher) removeQueued(key core.SectionKey, generation uint64) {
	mesher.mu.Lock()
	if mesher.queued[key] == generation {
		delete(mesher.queued, key)
		mesher.enqueueReadyLocked(key)
	}
	mesher.mu.Unlock()
}
