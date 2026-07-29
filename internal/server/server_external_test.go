package server_test

import (
	"context"
	"testing"
	"time"

	"minecraft-go/internal/server"
)

func shutdownExternalServerForTest(t *testing.T, running *server.Server) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := running.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown cleanup error=%v", err)
	}
}
