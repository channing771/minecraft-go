package server

import (
	"fmt"
	"log/slog"
	"time"
)

// applyMetadataCompletion 结算唯一一份 in-flight metadata 保存。
// 失败按现有 tick 退避重试，并保留 pending 以便下次提交最新值。
func (server *Server) applyMetadataCompletion(completion saveCompletion) error {
	server.metadataSave.inFlight = false
	if completion.Err == nil {
		server.metadataSave.attempts = 0
		server.metadataSave.nextRetryTick = 0
		server.metadataSave.lastError = ""
		server.metadataSave.committed = max(
			server.metadataSave.committed, completion.Job.Metadata.WorldTimeTicks,
		)
		server.lastSaveSuccess = time.Now()
		return nil
	}
	server.metadataSave.attempts++
	server.metadataSave.pending = true
	server.metadataSave.nextRetryTick = saturatingAddUint64(
		server.engine.TickCount(),
		retryDelay(
			server.config.RetryBaseTicks,
			server.config.RetryMaxTicks,
			server.metadataSave.attempts,
		),
	)
	server.metadataSave.lastError = completion.Err.Error()
	server.metadataSave.lastErrorAt = time.Now()
	slog.Error(
		"世界 metadata 保存失败，将按 tick 退避重试",
		"attempt", server.metadataSave.attempts,
		"next_tick", server.metadataSave.nextRetryTick,
		"error", completion.Err,
	)
	return fmt.Errorf("save world metadata: %w", completion.Err)
}

// scheduleMetadataSave 在自动保存边界或退避到期时提交一份最新世界时间快照。
// 队列满或已有 in-flight 时保留 pending，不阻塞 tick，也不形成无界队列。
func (server *Server) scheduleMetadataSave(tick, worldTime uint64) {
	server.metadataSave.latest = worldTime
	if tick%server.config.AutosaveTicks == 0 {
		server.metadataSave.pending = true
	}
	if server.metadataSave.attempts != 0 && tick >= server.metadataSave.nextRetryTick {
		server.metadataSave.pending = true
	}
	if !server.metadataSave.pending || server.metadataSave.inFlight {
		return
	}
	metadata := server.store.Metadata()
	metadata.WorldTimeTicks = server.metadataSave.latest
	select {
	case server.saveJobs <- saveJob{
		Kind:     saveKindMetadata,
		Metadata: metadata,
		Attempt:  server.metadataSave.attempts + 1,
	}:
		server.metadataSave.pending = false
		server.metadataSave.inFlight = true
	default:
	}
}
