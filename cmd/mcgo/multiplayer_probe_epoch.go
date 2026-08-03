//go:build darwin

package main

import (
	"context"
	"sync"
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

type benchmarkServerMeasurementGate struct {
	advance  chan struct{}
	advanced chan struct{}
	stop     chan struct{}
	stopOnce sync.Once
}

func newBenchmarkServerMeasurementGate() *benchmarkServerMeasurementGate {
	return &benchmarkServerMeasurementGate{
		advance:  make(chan struct{}),
		advanced: make(chan struct{}),
		stop:     make(chan struct{}),
	}
}

func (gate *benchmarkServerMeasurementGate) abort() {
	gate.stopOnce.Do(func() { close(gate.stop) })
}

type benchmarkServerEpoch struct {
	phase         atomic.Uint32
	measuredTicks atomic.Int64
	overflow      atomic.Bool
	signals       chan benchmarkServerTickSignal
	ticks         *client.LatencyRecorder
	interest      *client.LatencyRecorder
	measurement   atomic.Pointer[benchmarkServerMeasurementGate]
}

func newBenchmarkServerEpoch() *benchmarkServerEpoch {
	return &benchmarkServerEpoch{
		signals:  make(chan benchmarkServerTickSignal, benchmarkServerSignalCapacity),
		ticks:    client.NewLatencyRecorder(512),
		interest: client.NewLatencyRecorder(4096),
	}
}

func (epoch *benchmarkServerEpoch) beginWarmup() {
	epoch.abortMeasurement()
	epoch.drainSignals()
	epoch.overflow.Store(false)
	epoch.phase.Store(uint32(benchmarkServerEpochWarmup))
}

func (epoch *benchmarkServerEpoch) beginMeasurement(armInput func() error) error {
	epoch.abortMeasurement()
	epoch.phase.Store(uint32(benchmarkServerEpochIdle))
	epoch.drainSignals()
	epoch.ticks.Reset()
	epoch.interest.Reset()
	epoch.measuredTicks.Store(0)
	epoch.overflow.Store(false)
	gate := newBenchmarkServerMeasurementGate()
	epoch.measurement.Store(gate)
	if armInput != nil {
		if err := armInput(); err != nil {
			gate.abort()
			epoch.phase.Store(uint32(benchmarkServerEpochDone))
			return err
		}
	}
	epoch.phase.Store(uint32(benchmarkServerEpochMeasuring))
	return nil
}

func (epoch *benchmarkServerEpoch) abortMeasurement() {
	if gate := epoch.measurement.Load(); gate != nil {
		gate.abort()
	}
	epoch.phase.CompareAndSwap(
		uint32(benchmarkServerEpochMeasuring),
		uint32(benchmarkServerEpochDone),
	)
}

func (epoch *benchmarkServerEpoch) advanceMeasurement(ctx context.Context) error {
	gate := epoch.measurement.Load()
	if gate == nil {
		return context.Canceled
	}
	select {
	case gate.advance <- struct{}{}:
	case <-gate.stop:
		return context.Canceled
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-gate.advanced:
		return nil
	case <-gate.stop:
		return context.Canceled
	case <-ctx.Done():
		return ctx.Err()
	}
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
	final := false
	if measured {
		epoch.ticks.Add(duration)
		final = epoch.measuredTicks.Add(1) == benchmarkServerMeasuredTicks
	}
	gate := epoch.measurement.Load()
	select {
	case epoch.signals <- benchmarkServerTickSignal{measured: measured}:
	default:
		epoch.overflow.Store(true)
	}
	if !measured || gate == nil {
		return
	}
	select {
	case <-gate.advance:
		if final {
			epoch.phase.CompareAndSwap(
				uint32(benchmarkServerEpochMeasuring),
				uint32(benchmarkServerEpochDone),
			)
		}
		select {
		case gate.advanced <- struct{}{}:
		case <-gate.stop:
		}
	case <-gate.stop:
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
