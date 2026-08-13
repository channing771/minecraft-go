//go:build darwin

package main

import "testing"

// Mutation killed: restoring the 300,000-sample recorders violates the fixed
// bounded capacity selected to cover the observed ~54,000-frame scenario.
func TestBenchmarkLatencyCapacityCoversObservedScenarioWithinBound(t *testing.T) {
	const observedScenarioSamples = 54_607
	if benchmarkLatencyCapacity <= observedScenarioSamples {
		t.Fatalf("benchmark latency capacity=%d must cover %d observed samples", benchmarkLatencyCapacity, observedScenarioSamples)
	}
	if got, want := benchmarkLatencyCapacity, 131_072; got != want {
		t.Fatalf("benchmark latency capacity=%d want bounded capacity %d", got, want)
	}
}
