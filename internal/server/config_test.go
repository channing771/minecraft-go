package server

import (
	"context"
	"strconv"
	"testing"
	"time"
)

func TestDefaultConfigUsesEightMaxPlayers(t *testing.T) {
	if got := DefaultConfig(42).MaxPlayers; got != 8 {
		t.Fatalf("DefaultConfig MaxPlayers = %d, want 8", got)
	}
}

func TestNewHostNormalizesZeroMaxPlayers(t *testing.T) {
	config := DefaultConfig(42)
	config.MaxPlayers = 0
	host := NewHost(config, flatTestGenerator{}, newHostTestStore())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := host.Shutdown(ctx); err != nil {
			t.Errorf("Host cleanup Shutdown: %v", err)
		}
	})
	if got := host.config.MaxPlayers; got != 8 {
		t.Fatalf("Host config MaxPlayers = %d, want normalized 8", got)
	}
}

func TestNewHostRejectsOutOfRangeMaxPlayers(t *testing.T) {
	for _, maxPlayers := range []int{-1, 9} {
		t.Run(strconv.Itoa(maxPlayers), func(t *testing.T) {
			config := DefaultConfig(42)
			config.MaxPlayers = maxPlayers
			defer func() {
				if recover() == nil {
					t.Fatalf("NewHost accepted MaxPlayers = %d", maxPlayers)
				}
			}()
			_ = NewHost(config, flatTestGenerator{}, newHostTestStore())
		})
	}
}
