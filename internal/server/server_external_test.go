package server_test

import (
	"context"
	"testing"

	"minecraft-go/internal/server"
)

func shutdownExternalServerForTest(t *testing.T, running *server.Server) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
	defer cancel()
	if err := running.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown cleanup error=%v", err)
	}
}
