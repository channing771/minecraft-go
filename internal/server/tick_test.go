package server_test

import (
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/server"
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
	running := newMemoryAttachedWorldForExternalTest(config, serverEndpoint, emptyGenerator{})
	t.Cleanup(func() {
		_ = clientEndpoint.Close()
		shutdownExternalServerForTest(t, running)
	})

	running.StepForTest()
	if samples != 1 || duration <= 0 {
		t.Fatalf("tick samples=%d duration=%s", samples, duration)
	}
}
