package client

import (
	"math"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
)

const (
	remoteSnapshotCapacity = 4
	remoteInterpolationLag = 2.0
	remoteTickRate         = 20.0
	remoteMaxElapsed       = time.Second / time.Duration(remoteTickRate)
	remoteResetDistance    = float32(8)
)

type remoteSnapshot struct {
	tick      uint64
	dimension core.DimensionID
	position  mgl32.Vec3
	yaw       float32
	pitch     float32
}

type remoteSnapshotRing struct {
	values [remoteSnapshotCapacity]remoteSnapshot
	start  int
	count  int
}

func (ring *remoteSnapshotRing) append(snapshot remoteSnapshot) {
	if ring.count < remoteSnapshotCapacity {
		index := (ring.start + ring.count) % remoteSnapshotCapacity
		ring.values[index] = snapshot
		ring.count++
		return
	}
	ring.values[ring.start] = snapshot
	ring.start = (ring.start + 1) % remoteSnapshotCapacity
}

func (ring *remoteSnapshotRing) reset(snapshot remoteSnapshot) {
	ring.values = [remoteSnapshotCapacity]remoteSnapshot{}
	ring.start = 0
	ring.count = 1
	ring.values[0] = snapshot
}

func (ring *remoteSnapshotRing) at(index int) remoteSnapshot {
	return ring.values[(ring.start+index)%remoteSnapshotCapacity]
}

func (ring *remoteSnapshotRing) latest() remoteSnapshot {
	return ring.at(ring.count - 1)
}

func (ring *remoteSnapshotRing) sample(target float64) remoteSnapshot {
	oldest := ring.at(0)
	if target <= float64(oldest.tick) {
		return oldest
	}
	latest := ring.latest()
	if target >= float64(latest.tick) {
		return latest
	}
	for index := 1; index < ring.count; index++ {
		to := ring.at(index)
		if target > float64(to.tick) {
			continue
		}
		from := ring.at(index - 1)
		alpha := float32((target - float64(from.tick)) / float64(to.tick-from.tick))
		return interpolateRemoteSnapshot(from, to, alpha)
	}
	return latest
}

func (actor *remoteActor) pushSnapshot(snapshot remoteSnapshot, reset bool) {
	if actor.snapshots.count > 0 {
		latest := actor.snapshots.latest()
		delta := snapshot.position.Sub(latest.position)
		reset = reset ||
			snapshot.dimension != latest.dimension ||
			delta.Dot(delta) > remoteResetDistance*remoteResetDistance
	}
	if reset || actor.snapshots.count == 0 {
		actor.snapshots.reset(snapshot)
	} else {
		actor.snapshots.append(snapshot)
	}
	actor.elapsed = 0
	actor.lastTick = snapshot.tick
	actor.setPresentation(snapshot)
}

func (players *RemotePlayers) Advance(elapsed time.Duration) {
	for _, player := range players.players {
		player.advance(elapsed)
	}
}

func (actor *remoteActor) advance(elapsed time.Duration) {
	if elapsed > 0 {
		if elapsed >= remoteMaxElapsed-actor.elapsed {
			actor.elapsed = remoteMaxElapsed
		} else {
			actor.elapsed += elapsed
		}
	}
	latest := actor.snapshots.latest()
	if actor.snapshots.count < 3 {
		actor.setPresentation(latest)
		return
	}
	target := float64(latest.tick) + actor.elapsed.Seconds()*remoteTickRate - remoteInterpolationLag
	actor.setPresentation(actor.snapshots.sample(target))
}

func (actor *remoteActor) setPresentation(snapshot remoteSnapshot) {
	actor.dimension = snapshot.dimension
	actor.position = snapshot.position
	actor.yaw = snapshot.yaw
	actor.pitch = snapshot.pitch
}

func interpolateRemoteSnapshot(from, to remoteSnapshot, alpha float32) remoteSnapshot {
	position := from.position.Mul(1 - alpha).Add(to.position.Mul(alpha))
	yaw := normalizeRemoteAngle(from.yaw)
	yawDelta := normalizeRemoteAngle(normalizeRemoteAngle(to.yaw) - yaw)
	return remoteSnapshot{
		tick:      from.tick,
		dimension: from.dimension,
		position:  position,
		yaw:       normalizeRemoteAngle(yaw + yawDelta*alpha),
		pitch:     from.pitch + (to.pitch-from.pitch)*alpha,
	}
}

func normalizeRemoteAngle(angle float32) float32 {
	wrapped := float32(math.Mod(float64(angle)+math.Pi, 2*math.Pi))
	if wrapped < 0 {
		wrapped += 2 * math.Pi
	}
	return wrapped - math.Pi
}
