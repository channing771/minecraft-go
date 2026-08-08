package server

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"minecraft-go/internal/network"
)

func BenchmarkEightPlayerInterest(b *testing.B) {
	config := hostTestConfig()
	config.MaxPlayers = 8
	config.ViewRadius = 0
	config.Workers = 1
	config.OutboxCapacity = 4096
	config.HeartbeatInterval = time.Hour
	config.HeartbeatTimeout = time.Hour
	running := NewWorld(config, flatTestGenerator{}, newHostTestStore())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	clients := make([]network.ClientEndpoint, 0, 8)
	drainErrors := make(chan error, 8)
	var drains sync.WaitGroup
	for index := range 8 {
		clientEndpoint, serverEndpoint := network.NewMemoryPair(4096)
		identity := multiplayerIdentity(byte(0xc0+index), multiplayerNames[index])
		if _, err := running.AttachSession(multiplayerSessionSpec(index, identity, serverEndpoint)); err != nil {
			cancel()
			b.Fatal(err)
		}
		clients = append(clients, clientEndpoint)
		drains.Add(1)
		go func(endpoint network.ClientEndpoint) {
			defer drains.Done()
			for {
				if _, err := endpoint.Recv(ctx); err != nil {
					if !errors.Is(err, context.Canceled) && !errors.Is(err, network.ErrClosed) {
						drainErrors <- err
					}
					return
				}
			}
		}(clientEndpoint)
	}
	b.Cleanup(func() {
		cancel()
		for _, endpoint := range clients {
			_ = endpoint.Close()
		}
		done := make(chan struct{})
		go func() {
			drains.Wait()
			close(done)
		}()
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		select {
		case <-done:
		case <-deadline.C:
			b.Error("receiver drains did not stop within 5s")
		}
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), waitDeadline)
		defer shutdownCancel()
		if err := running.Shutdown(shutdownCtx); err != nil {
			b.Errorf("Shutdown: %v", err)
		}
		close(drainErrors)
		for err := range drainErrors {
			b.Errorf("receiver drain: %v", err)
		}
	})
	for range 32 {
		running.StepForTest()
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		running.StepForTest()
	}
}
