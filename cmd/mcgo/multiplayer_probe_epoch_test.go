//go:build darwin

package main

import (
	"context"
	"testing"
	"time"
)

func runBenchmarkTestInputBoundary(_ context.Context, _ uint64, action func() error) error {
	return action()
}

func observeMeasuredBenchmarkTick(
	t *testing.T,
	epoch *benchmarkServerEpoch,
	duration time.Duration,
) benchmarkServerTickSignal {
	t.Helper()
	epoch.observeTick(duration)
	signal := <-epoch.signals
	return signal
}

func TestBenchmarkServerEpochIgnoresWarmupAndStopsAtExactWindow(t *testing.T) {
	epoch := newBenchmarkServerEpoch()
	epoch.beginWarmup()
	for range benchmarkServerWarmupTicks {
		for range 8 {
			epoch.observeInterest(9 * time.Millisecond)
		}
		epoch.observeTick(9 * time.Millisecond)
		if signal := <-epoch.signals; signal.measured {
			t.Fatal("warm-up tick marked measured")
		}
	}
	if err := epoch.beginMeasurement(context.Background(), runBenchmarkTestInputBoundary, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	for tick := 1; tick <= benchmarkServerMeasuredTicks; tick++ {
		for range 8 {
			epoch.observeInterest(time.Duration(tick) * time.Microsecond)
		}
		if signal := observeMeasuredBenchmarkTick(
			t, epoch, time.Duration(tick)*time.Microsecond,
		); !signal.measured {
			t.Fatalf("tick %d not marked measured", tick)
		}
	}

	epoch.observeInterest(time.Second)
	epoch.observeTick(time.Second)
	if got := epoch.ticks.Summary().Samples; got != benchmarkServerMeasuredTicks {
		t.Fatalf("tick samples=%d want=%d", got, benchmarkServerMeasuredTicks)
	}
	if got := epoch.interest.Summary().Samples; got != benchmarkServerInterestSamples {
		t.Fatalf("interest samples=%d want=%d", got, benchmarkServerInterestSamples)
	}
	select {
	case signal := <-epoch.signals:
		t.Fatalf("done epoch emitted signal: %+v", signal)
	default:
	}
	if epoch.overflow.Load() {
		t.Fatal("complete epoch reported overflow")
	}
}

func TestBenchmarkServerEpochDropsStaleWarmupSignalsBeforeMeasurement(t *testing.T) {
	epoch := newBenchmarkServerEpoch()
	epoch.beginWarmup()
	epoch.observeTick(time.Millisecond)
	if err := epoch.beginMeasurement(context.Background(), runBenchmarkTestInputBoundary, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if signal := observeMeasuredBenchmarkTick(t, epoch, 2*time.Millisecond); !signal.measured {
		t.Fatalf("stale warm-up signal survived reset: %+v", signal)
	}
	if got := epoch.ticks.Summary().Samples; got != 1 {
		t.Fatalf("measured samples=%d want=1", got)
	}
}

func TestBenchmarkServerEpochReportsSignalOverflowWithoutBlocking(t *testing.T) {
	epoch := newBenchmarkServerEpoch()
	epoch.beginWarmup()
	for range cap(epoch.signals) + 1 {
		epoch.observeTick(time.Microsecond)
	}
	if !epoch.overflow.Load() {
		t.Fatal("signal overflow not reported")
	}
}

func TestBenchmarkServerEpochArmsInputBeforeMeasurementGate(t *testing.T) {
	epoch := newBenchmarkServerEpoch()
	epoch.beginWarmup()
	armed := false
	err := epoch.beginMeasurement(context.Background(), runBenchmarkTestInputBoundary, func() error {
		if epoch.measuring() {
			t.Fatal("measurement gate opened before input arm")
		}
		epoch.observeInterest(time.Second)
		armed = true
		return nil
	})
	if err != nil || !armed || !epoch.measuring() {
		t.Fatalf("beginMeasurement err=%v armed=%v measuring=%v", err, armed, epoch.measuring())
	}
	if signal := observeMeasuredBenchmarkTick(t, epoch, time.Millisecond); !signal.measured {
		t.Fatalf("first post-arm tick not measured: %+v", signal)
	}
	if got := epoch.ticks.Summary().Samples; got != 1 {
		t.Fatalf("post-arm samples=%d want=1", got)
	}
}

func TestBenchmarkServerEpochRejectsTickCompletedWhileArmingFirstInput(t *testing.T) {
	epoch := newBenchmarkServerEpoch()
	epoch.beginWarmup()
	err := epoch.beginMeasurement(context.Background(), runBenchmarkTestInputBoundary, func() error {
		epoch.observeTick(time.Millisecond)
		return nil
	})
	if err == nil {
		t.Fatal("beginMeasurement accepted a tick completed while arming input 1")
	}
	if epoch.measuring() {
		t.Fatal("failed input arm left measurement enabled")
	}
}

func TestBenchmarkServerEpochObserverDoesNotWaitForController(t *testing.T) {
	epoch := newBenchmarkServerEpoch()
	if err := epoch.beginMeasurement(context.Background(), runBenchmarkTestInputBoundary, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	returned := make(chan struct{})
	go func() {
		epoch.observeTick(time.Millisecond)
		close(returned)
	}()
	if signal := <-epoch.signals; !signal.measured {
		t.Fatalf("first tick signal=%+v", signal)
	}
	select {
	case <-returned:
	case <-time.After(25 * time.Millisecond):
		t.Fatal("tick observer waited for benchmark controller")
	}
}

func TestBenchmarkServerEpochPreservesScheduledTickTime(t *testing.T) {
	epoch := newBenchmarkServerEpoch()
	epoch.beginWarmup()
	scheduled := time.Now().Add(-25 * time.Millisecond)
	epoch.observeScheduledTick(scheduled, time.Millisecond)
	if signal := <-epoch.signals; !signal.scheduled.Equal(scheduled) {
		t.Fatalf("scheduled tick=%s want=%s", signal.scheduled, scheduled)
	}
}
