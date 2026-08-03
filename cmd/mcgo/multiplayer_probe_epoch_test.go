//go:build darwin

package main

import (
	"testing"
	"time"
)

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
	if err := epoch.beginMeasurement(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	for tick := 1; tick <= benchmarkServerMeasuredTicks; tick++ {
		for range 8 {
			epoch.observeInterest(time.Duration(tick) * time.Microsecond)
		}
		epoch.observeTick(time.Duration(tick) * time.Microsecond)
		if signal := <-epoch.signals; !signal.measured {
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
	if err := epoch.beginMeasurement(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	epoch.observeTick(2 * time.Millisecond)
	if signal := <-epoch.signals; !signal.measured {
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
	err := epoch.beginMeasurement(func() error {
		if epoch.measuring() {
			t.Fatal("measurement gate opened before input arm")
		}
		epoch.observeInterest(time.Second)
		epoch.observeTick(time.Second)
		select {
		case signal := <-epoch.signals:
			t.Fatalf("idle arm recorded a tick: %+v", signal)
		default:
		}
		armed = true
		return nil
	})
	if err != nil || !armed || !epoch.measuring() {
		t.Fatalf("beginMeasurement err=%v armed=%v measuring=%v", err, armed, epoch.measuring())
	}
	epoch.observeTick(time.Millisecond)
	if signal := <-epoch.signals; !signal.measured {
		t.Fatalf("first post-arm tick not measured: %+v", signal)
	}
	if got := epoch.ticks.Summary().Samples; got != 1 {
		t.Fatalf("post-arm samples=%d want=1", got)
	}
}
