package server

import (
	"context"
	"log/slog"
	"sync"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
)

type publication struct {
	snapshotSent bool
	lastRevision uint64
	resyncQueued bool
}

type snapshotRequest struct {
	resync bool
}

type session struct {
	endpoint network.ServerEndpoint
	ctx      context.Context
	cancel   context.CancelFunc
	outbox   chan network.ServerMessage
	workers  *sync.WaitGroup

	mu        sync.Mutex
	isClosed  bool
	closeOnce sync.Once

	hasView          bool
	viewDimension    core.DimensionID
	viewCenter       core.ChunkPos
	publications     map[core.ChunkKey]*publication
	pendingSnapshots map[core.ChunkKey]snapshotRequest
}

func newSession(
	parent context.Context,
	endpoint network.ServerEndpoint,
	capacity int,
	workers *sync.WaitGroup,
) *session {
	if capacity < 1 {
		panic("server: session outbox capacity must be positive")
	}
	ctx, cancel := context.WithCancel(parent)
	session := &session{
		endpoint:         endpoint,
		ctx:              ctx,
		cancel:           cancel,
		outbox:           make(chan network.ServerMessage, capacity),
		workers:          workers,
		publications:     make(map[core.ChunkKey]*publication),
		pendingSnapshots: make(map[core.ChunkKey]snapshotRequest),
	}
	workers.Add(1)
	go session.writeLoop()
	return session
}

// enqueue 永不等待 writer；满队列会关闭慢 session。
func (session *session) enqueue(message network.ServerMessage) bool {
	session.mu.Lock()
	if session.isClosed {
		session.mu.Unlock()
		return false
	}
	select {
	case session.outbox <- message:
		session.mu.Unlock()
		return true
	default:
		session.isClosed = true
		session.mu.Unlock()
		slog.Warn("慢客户端 outbox 已满，关闭 session")
		session.shutdown()
		return false
	}
}

func (session *session) close() {
	session.mu.Lock()
	if session.isClosed {
		session.mu.Unlock()
		session.shutdown()
		return
	}
	session.isClosed = true
	session.mu.Unlock()
	session.shutdown()
}

func (session *session) shutdown() {
	session.closeOnce.Do(func() {
		session.cancel()
		_ = session.endpoint.Close()
	})
}

func (session *session) closed() bool {
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.isClosed
}

func (session *session) writeLoop() {
	defer session.workers.Done()
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.Error("session writer panic 已隔离", "panic", recovered)
			session.close()
		}
	}()
	for {
		select {
		case <-session.ctx.Done():
			return
		case message := <-session.outbox:
			if err := session.endpoint.Send(session.ctx, message); err != nil {
				session.close()
				return
			}
		}
	}
}
