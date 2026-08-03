//go:build darwin

package main

import (
	"sync/atomic"
	"time"

	"minecraft-go/internal/client"
)

const (
	benchmarkServerWarmupTicks     = 20
	benchmarkServerMeasuredTicks   = 200
	benchmarkServerInterestSamples = 8 * benchmarkServerMeasuredTicks
	benchmarkServerSignalCapacity  = benchmarkServerWarmupTicks + benchmarkServerMeasuredTicks + 16
)

type benchmarkServerEpochPhase uint32

const (
	benchmarkServerEpochIdle benchmarkServerEpochPhase = iota
	benchmarkServerEpochWarmup
	benchmarkServerEpochMeasuring
	benchmarkServerEpochDone
)

type benchmarkServerTickSignal struct {
	measured bool
}

type benchmarkServerEpoch struct {
	phase         atomic.Uint32
	measuredTicks atomic.Int64
	overflow      atomic.Bool
	signals       chan benchmarkServerTickSignal
	ticks         *client.LatencyRecorder
	interest      *client.LatencyRecorder
}

func newBenchmarkServerEpoch() *benchmarkServerEpoch {
	return &benchmarkServerEpoch{
		signals:  make(chan benchmarkServerTickSignal, benchmarkServerSignalCapacity),
		ticks:    client.NewLatencyRecorder(512),
		interest: client.NewLatencyRecorder(4096),
	}
}

func (epoch *benchmarkServerEpoch) beginWarmup() {
	epoch.drainSignals()
	epoch.overflow.Store(false)
	epoch.phase.Store(uint32(benchmarkServerEpochWarmup))
}

func (epoch *benchmarkServerEpoch) beginMeasurement(armInput func() error) error {
	epoch.phase.Store(uint32(benchmarkServerEpochIdle))
	epoch.drainSignals()
	epoch.ticks.Reset()
	epoch.interest.Reset()
	epoch.measuredTicks.Store(0)
	epoch.overflow.Store(false)
	if armInput != nil {
		if err := armInput(); err != nil {
			epoch.phase.Store(uint32(benchmarkServerEpochDone))
			return err
		}
	}
	epoch.phase.Store(uint32(benchmarkServerEpochMeasuring))
	return nil
}

func (epoch *benchmarkServerEpoch) measuring() bool {
	return benchmarkServerEpochPhase(epoch.phase.Load()) == benchmarkServerEpochMeasuring
}

func (epoch *benchmarkServerEpoch) observeInterest(duration time.Duration) {
	if epoch.measuring() {
		epoch.interest.Add(duration)
	}
}

func (epoch *benchmarkServerEpoch) observeTick(duration time.Duration) {
	phase := benchmarkServerEpochPhase(epoch.phase.Load())
	if phase != benchmarkServerEpochWarmup && phase != benchmarkServerEpochMeasuring {
		return
	}
	measured := phase == benchmarkServerEpochMeasuring
	if measured {
		epoch.ticks.Add(duration)
		if epoch.measuredTicks.Add(1) == benchmarkServerMeasuredTicks {
			epoch.phase.CompareAndSwap(
				uint32(benchmarkServerEpochMeasuring),
				uint32(benchmarkServerEpochDone),
			)
		}
	}
	select {
	case epoch.signals <- benchmarkServerTickSignal{measured: measured}:
	default:
		epoch.overflow.Store(true)
	}
}

func (epoch *benchmarkServerEpoch) drainSignals() {
	for {
		select {
		case <-epoch.signals:
		default:
			return
		}
	}
}
