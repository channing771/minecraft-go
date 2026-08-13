package server_test

import (
	"context"
	"testing"

	"github.com/channing771/mornlea/internal/server"
)

func shutdownExternalServerForTest(t *testing.T, running *server.Server) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
	defer cancel()
	if err := running.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown cleanup error=%v", err)
	}
}
