package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"minecraft-go/internal/storage"
)

func (server *Server) recordSaveFailure(
	region storage.RegionKey,
	attempt uint32,
	nextTick uint64,
	err error,
) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	wrapped := fmt.Errorf("save region %+v: %w", region, err)
	server.lastSaveError = wrapped.Error()
	server.lastSaveErrorAt = time.Now()
	attributes := []any{
		"operation", "save",
		"region", region,
		"attempt", attempt,
		"next_tick", nextTick,
		"error", wrapped,
	}
	if store, ok := server.store.(interface{ WorldPath() string }); ok {
		if path := store.WorldPath(); path != "" {
			attributes = append(attributes, "world_path", path)
		}
	}
	slog.Error("区块存档失败，将按 tick 退避重试", attributes...)
}

func (server *Server) updatePersistenceBackpressure() {
	stats := server.engine.PersistenceStats()
	server.backpressured = nextPersistenceBackpressure(
		server.backpressured,
		stats.EstimatedBytes,
		server.config.UnsavedBytes,
	)
}

func nextPersistenceBackpressure(current bool, estimated, limit int64) bool {
	if !current {
		return estimated >= limit
	}
	remainder := limit % 10
	threshold := limit/10*9 + (remainder*9+9)/10
	return estimated >= threshold
}

// PersistenceStatus 返回当前存档积压、背压和最近完成状态的值副本。
func (server *Server) PersistenceStatus() PersistenceStatus {
	server.stepMu.Lock()
	defer server.stepMu.Unlock()
	stats := server.engine.PersistenceStats()
	return PersistenceStatus{
		DirtyChunks:    stats.DirtyChunks,
		EstimatedBytes: stats.EstimatedBytes,
		InFlightChunks: stats.InFlightChunks,
		Backpressured:  server.backpressured,
		LastSuccess:    server.lastSaveSuccess,
		LastError:      server.lastSaveError,
		LastErrorAt:    server.lastSaveErrorAt,
		AutosaveDrained: !server.autosaveActive && stats.DirtyChunks == 0 &&
			stats.InFlightChunks == 0 && len(server.retry) == 0 &&
			len(server.retryInFlight) == 0,
		MetadataPending:   server.metadataSave.pending,
		MetadataInFlight:  server.metadataSave.inFlight,
		MetadataLastError: server.metadataSave.lastError,
	}
}
