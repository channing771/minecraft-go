package server

import (
	"time"

	"github.com/channing771/mornlea/internal/network"
)

type heartbeatTimer interface {
	C() <-chan time.Time
	Stop()
}

type heartbeatClock interface {
	NewTimer(time.Duration) heartbeatTimer
}

type realHeartbeatClock struct{}

func (realHeartbeatClock) NewTimer(duration time.Duration) heartbeatTimer {
	return &realHeartbeatTimer{timer: time.NewTimer(duration)}
}

type realHeartbeatTimer struct {
	timer *time.Timer
}

func (timer *realHeartbeatTimer) C() <-chan time.Time {
	return timer.timer.C
}

func (timer *realHeartbeatTimer) Stop() {
	timer.timer.Stop()
}

func (current *session) acceptHeartbeatReply(token uint64) bool {
	current.mu.Lock()
	if current.isClosed || token == 0 || token != current.outstandingToken {
		current.mu.Unlock()
		return false
	}
	current.outstandingToken = 0
	current.mu.Unlock()
	select {
	case current.heartbeatReply <- token:
	default:
	}
	return true
}

func (current *session) heartbeatLoop(
	clock heartbeatClock,
	interval time.Duration,
	timeout time.Duration,
) {
	defer current.workers.Done()
	intervalTimer := clock.NewTimer(interval)
	defer func() { intervalTimer.Stop() }()
	var timeoutTimer heartbeatTimer
	var timeoutC <-chan time.Time
	var timeoutToken uint64
	defer func() {
		if timeoutTimer != nil {
			timeoutTimer.Stop()
		}
	}()

	for {
		select {
		case <-current.ctx.Done():
			return
		case token := <-current.heartbeatReply:
			if timeoutTimer != nil && token == timeoutToken {
				timeoutTimer.Stop()
				timeoutTimer = nil
				timeoutC = nil
				timeoutToken = 0
			}
		case <-intervalTimer.C():
			intervalTimer.Stop()
			intervalTimer = clock.NewTimer(interval)

			current.mu.Lock()
			if current.outstandingToken != 0 {
				current.mu.Unlock()
				continue
			}
			current.nextToken++
			token := current.nextToken
			current.outstandingToken = token
			current.mu.Unlock()

			if !current.enqueue(network.KeepAlive{Token: token}) {
				return
			}
			if timeoutTimer != nil {
				timeoutTimer.Stop()
			}
			timeoutTimer = clock.NewTimer(timeout)
			timeoutC = timeoutTimer.C()
			timeoutToken = token
		case <-timeoutC:
			current.mu.Lock()
			timedOut := current.outstandingToken != 0
			current.mu.Unlock()
			if timedOut {
				current.fail(errHeartbeatTimeout)
				return
			}
			timeoutTimer = nil
			timeoutC = nil
			timeoutToken = 0
		}
	}
}
