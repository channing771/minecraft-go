//go:build darwin

package main

import (
	"context"
	"errors"
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
	measured  bool
	scheduled time.Time
}

type benchmarkServerInputBoundary func(context.Context, uint64, func() error) error

type benchmarkServerEpoch struct {
	phase         atomic.Uint32
	observedTicks atomic.Uint64
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
	epoch.abortMeasurement()
	epoch.drainSignals()
	epoch.overflow.Store(false)
	epoch.phase.Store(uint32(benchmarkServerEpochWarmup))
}

func (epoch *benchmarkServerEpoch) beginMeasurement(
	ctx context.Context,
	inputBoundary benchmarkServerInputBoundary,
	armInput func() error,
) error {
	epoch.abortMeasurement()
	epoch.phase.Store(uint32(benchmarkServerEpochIdle))
	epoch.drainSignals()
	epoch.ticks.Reset()
	epoch.interest.Reset()
	epoch.measuredTicks.Store(0)
	epoch.overflow.Store(false)
	if ctx == nil {
		epoch.phase.Store(uint32(benchmarkServerEpochDone))
		return errors.New("缺少 measurement context")
	}
	armBoundary := epoch.observedTicks.Load()
	if inputBoundary == nil {
		epoch.phase.Store(uint32(benchmarkServerEpochDone))
		return errors.New("缺少服务端 input boundary")
	}
	err := inputBoundary(ctx, 1, func() error {
		if epoch.observedTicks.Load() != armBoundary {
			return errors.New("服务端 tick 在首组输入 arm 前完成")
		}
		if armInput != nil {
			if err := armInput(); err != nil {
				return err
			}
		}
		if epoch.observedTicks.Load() != armBoundary {
			return errors.New("服务端 tick 在首组输入 arm 期间完成")
		}
		epoch.phase.Store(uint32(benchmarkServerEpochMeasuring))
		return nil
	})
	if err != nil {
		epoch.phase.Store(uint32(benchmarkServerEpochDone))
		return err
	}
	if err := ctx.Err(); err != nil {
		epoch.phase.Store(uint32(benchmarkServerEpochDone))
		return err
	}
	return nil
}

func (epoch *benchmarkServerEpoch) abortMeasurement() {
	epoch.phase.CompareAndSwap(
		uint32(benchmarkServerEpochMeasuring),
		uint32(benchmarkServerEpochDone),
	)
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
	epoch.observeScheduledTick(time.Now(), duration)
}

func (epoch *benchmarkServerEpoch) observeScheduledTick(
	scheduled time.Time,
	duration time.Duration,
) {
	epoch.observedTicks.Add(1)
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
	select {
	case epoch.signals <- benchmarkServerTickSignal{
		measured: measured, scheduled: scheduled,
	}:
	default:
		epoch.overflow.Store(true)
	}
	if final {
		epoch.phase.CompareAndSwap(
			uint32(benchmarkServerEpochMeasuring),
			uint32(benchmarkServerEpochDone),
		)
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
