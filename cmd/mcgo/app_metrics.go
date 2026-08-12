//go:build darwin

package main

import (
	"math"
	"slices"
	"sync"
	"time"

	"minecraft-go/internal/client"
)

type tickRecorder struct {
	mu      sync.Mutex
	sampler *client.PerfSampler
}

type saveRecorder struct {
	mu      sync.Mutex
	samples []float64
	next    int
	count   int
}

func newSaveRecorder(capacity int) *saveRecorder {
	return &saveRecorder{samples: make([]float64, max(1, capacity))}
}

func newPerformanceRecorders(benchmark bool) (*tickRecorder, *saveRecorder) {
	ticks := newTickRecorder(100_000)
	if !benchmark {
		return ticks, nil
	}
	return ticks, newSaveRecorder(100_000)
}

func (recorder *saveRecorder) add(duration time.Duration) {
	recorder.mu.Lock()
	recorder.samples[recorder.next] = float64(duration.Nanoseconds()) / float64(time.Millisecond)
	recorder.next = (recorder.next + 1) % len(recorder.samples)
	recorder.count = min(recorder.count+1, len(recorder.samples))
	recorder.mu.Unlock()
}

func (recorder *saveRecorder) reset() {
	recorder.mu.Lock()
	recorder.next = 0
	recorder.count = 0
	recorder.mu.Unlock()
}

func (recorder *saveRecorder) summary() client.PersistenceSummary {
	recorder.mu.Lock()
	ordered := make([]float64, recorder.count)
	start := 0
	if recorder.count == len(recorder.samples) {
		start = recorder.next
	}
	for index := range recorder.count {
		ordered[index] = recorder.samples[(start+index)%len(recorder.samples)]
	}
	recorder.mu.Unlock()
	if len(ordered) == 0 {
		return client.PersistenceSummary{}
	}
	slices.Sort(ordered)
	percentile := func(p float64) float64 {
		index := int(math.Ceil(p*float64(len(ordered)))) - 1
		return ordered[max(0, min(index, len(ordered)-1))]
	}
	return client.PersistenceSummary{
		Snapshots: int64(len(ordered)),
		P50MS:     percentile(0.50),
		P95MS:     percentile(0.95),
		P99MS:     percentile(0.99),
		MaxMS:     ordered[len(ordered)-1],
	}
}

func newTickRecorder(capacity int) *tickRecorder {
	return &tickRecorder{sampler: client.NewPerfSampler(capacity)}
}

func (recorder *tickRecorder) add(duration time.Duration) {
	recorder.mu.Lock()
	recorder.sampler.Add(client.FrameSample{FrameMS: float64(duration.Microseconds()) / 1000})
	recorder.mu.Unlock()
}

func (recorder *tickRecorder) reset() {
	recorder.mu.Lock()
	recorder.sampler.Reset()
	recorder.mu.Unlock()
}

func (recorder *tickRecorder) summary() client.PhaseSummary {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return recorder.sampler.Summary(0)
}
