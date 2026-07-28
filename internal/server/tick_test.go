package server_test

import (
	"testing"
	"time"

	"minecraft-go/internal/network"
	"minecraft-go/internal/server"
)

func TestServerStepReportsTickDuration(t *testing.T) {
	clientEndpoint, serverEndpoint := network.NewMemoryPair(8)
	config := server.DefaultConfig(1)
	config.Workers = 1
	var samples int
	var duration time.Duration
	config.TickObserver = func(sample time.Duration) {
		samples++
		duration = sample
	}
	running := server.New(config, serverEndpoint, emptyGenerator{})
	t.Cleanup(func() {
		_ = clientEndpoint.Close()
		running.Close()
	})

	running.StepForTest()
	if samples != 1 || duration <= 0 {
		t.Fatalf("tick samples=%d duration=%s", samples, duration)
	}
}
