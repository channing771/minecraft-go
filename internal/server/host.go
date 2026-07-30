package server

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/storage"
)

const hostPreLoginCapacity = 16

var errHostSessionIDExhausted = errors.New("server: host session IDs exhausted")

type Host struct {
	config  Config
	world   *Server
	players *playerPersistence

	preLogin        chan struct{}
	mu              sync.Mutex
	active          *activeLogin
	preLoginStreams map[uint64]network.ServerPacketStream
	nextPreLogin    uint64
	nextSession     sim.SessionID
	nextGeneration  uint64
	listener        network.Listener
	runtimeCancel   context.CancelFunc
	runtimeDone     chan error
	workers         sync.WaitGroup
	shutdownGate    chan struct{}
	closing         bool
}

type activeLogin struct {
	PlayerID   core.PlayerID
	Name       string
	Session    sim.SessionID
	Generation uint64
}

func NewHost(config Config, generator Generator, store storage.WorldStore) *Host {
	if store == nil {
		panic("server: nil host store")
	}
	gate := make(chan struct{}, 1)
	gate <- struct{}{}
	return &Host{
		config:          config,
		world:           NewWorld(config, generator, store),
		players:         newPlayerPersistence(store, config),
		preLogin:        make(chan struct{}, hostPreLoginCapacity),
		preLoginStreams: make(map[uint64]network.ServerPacketStream),
		runtimeDone:     make(chan error, 1),
		shutdownGate:    gate,
	}
}

func (h *Host) Run(ctx context.Context, listener network.Listener) error {
	if ctx == nil {
		panic("server: nil host run context")
	}
	h.mu.Lock()
	if h.closing {
		h.mu.Unlock()
		return network.ErrClosed
	}
	if h.runtimeCancel != nil {
		h.mu.Unlock()
		return errors.New("server: host is already running")
	}
	runCtx, cancel := context.WithCancel(context.Background())
	h.listener = listener
	h.runtimeCancel = cancel
	if listener != nil {
		h.workers.Add(1)
	}
	h.mu.Unlock()
	go func() { h.runtimeDone <- h.world.RunTicks(runCtx) }()
	if listener != nil {
		go func() {
			defer h.workers.Done()
			h.acceptLoop(runCtx, listener)
		}()
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-h.runtimeDone:
			h.mu.Lock()
			closing := h.closing
			h.mu.Unlock()
			if closing && (errors.Is(err, context.Canceled) || err == nil) {
				return nil
			}
			return err
		case <-ticker.C:
			h.pollPlayers()
		case <-ctx.Done():
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), h.config.ShutdownTimeout)
			err := h.Shutdown(shutdownCtx)
			shutdownCancel()
			return err
		}
	}
}

func (h *Host) acceptLoop(ctx context.Context, listener network.Listener) {
	for {
		stream, err := listener.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, network.ErrClosed) {
				return
			}
			slog.Warn("接纳连接失败，继续监听", "error", err)
			continue
		}
		streamID, err := h.acquirePreLogin(stream)
		if err != nil {
			continue
		}
		go func(stream network.ServerPacketStream, streamID uint64) {
			if err := h.acceptStream(ctx, stream, streamID); err != nil &&
				!errors.Is(err, network.ErrClosed) && !errors.Is(err, context.Canceled) {
				slog.Warn("连接登录失败", "peer", stream.Peer(), "error", err)
			}
		}(stream, streamID)
	}
}

func (h *Host) pollPlayers() {
	tick := h.world.TickCount()
	h.mu.Lock()
	var active activeLogin
	hasActive := h.active != nil && h.active.Session != 0
	if hasActive {
		active = *h.active
	}
	h.mu.Unlock()
	if hasActive {
		if snapshot, ok := h.world.PlayerSnapshotFor(active.Session); ok {
			if err := h.players.Observe(active.PlayerID, active.Name, snapshot, tick, false); err != nil {
				slog.Warn("观察在线玩家快照失败", "error", err)
			}
		}
	}
	if err := h.players.Poll(tick); err != nil {
		slog.Warn("玩家自动保存失败，保留重试", "error", err)
	}
}

func (h *Host) AcceptStream(ctx context.Context, stream network.ServerPacketStream) error {
	if ctx == nil {
		panic("server: nil host stream context")
	}
	if stream == nil {
		return errors.New("server: nil login stream")
	}
	streamID, err := h.acquirePreLogin(stream)
	if err != nil {
		return err
	}
	return h.acceptStream(ctx, stream, streamID)
}

func (h *Host) acquirePreLogin(stream network.ServerPacketStream) (uint64, error) {
	if stream == nil {
		return 0, errors.New("server: nil login stream")
	}
	select {
	case h.preLogin <- struct{}{}:
	default:
		_ = stream.Close()
		return 0, network.ErrClosed
	}
	h.mu.Lock()
	if h.closing || h.nextPreLogin == ^uint64(0) {
		h.mu.Unlock()
		<-h.preLogin
		_ = stream.Close()
		return 0, network.ErrClosed
	}
	h.nextPreLogin++
	streamID := h.nextPreLogin
	h.preLoginStreams[streamID] = stream
	h.workers.Add(1)
	h.mu.Unlock()
	return streamID, nil
}

func (h *Host) acceptStream(
	ctx context.Context,
	stream network.ServerPacketStream,
	streamID uint64,
) error {
	defer func() {
		_ = stream.Close()
		h.mu.Lock()
		delete(h.preLoginStreams, streamID)
		h.mu.Unlock()
		h.workers.Done()
		<-h.preLogin
	}()

	pending, err := network.BeginServerLogin(ctx, stream)
	if err != nil {
		return err
	}
	identity := pending.Identity()

	h.mu.Lock()
	if h.closing || h.active != nil {
		h.mu.Unlock()
		return pending.Reject(ctx, network.LoginServerFull, "服务器已有玩家在线")
	}
	h.active = &activeLogin{PlayerID: identity.PlayerID, Name: identity.DisplayName}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		if h.active != nil && h.active.PlayerID == identity.PlayerID {
			h.active = nil
		}
		h.mu.Unlock()
	}()

	restore, err := h.players.Prepare(
		pending.Context(),
		identity.PlayerID,
		identity.DisplayName,
		h.world.store.Metadata(),
	)
	if err != nil {
		h.players.Abort(identity.PlayerID)
		code, message := hostPlayerLoadReject(err)
		_ = pending.Reject(ctx, code, message)
		return err
	}

	h.mu.Lock()
	if h.nextSession == ^sim.SessionID(0) || h.nextGeneration == ^uint64(0) {
		h.mu.Unlock()
		h.players.Abort(identity.PlayerID)
		_ = pending.Reject(ctx, network.LoginInternalError, "服务端会话编号已耗尽")
		return errHostSessionIDExhausted
	}
	h.nextSession++
	h.nextGeneration++
	sessionID := h.nextSession
	generation := h.nextGeneration
	h.active.Session = sessionID
	h.active.Generation = generation
	h.mu.Unlock()

	var exit <-chan SessionExit
	err = pending.Accept(ctx, func(endpoint network.ServerEndpoint) error {
		var attachErr error
		exit, attachErr = h.world.AttachSession(SessionSpec{
			ID: sessionID, Generation: generation, Endpoint: endpoint, Restore: restore,
		})
		if attachErr != nil {
			return attachErr
		}
		if activateErr := h.players.Activate(identity.PlayerID, identity.DisplayName); activateErr != nil {
			h.world.DetachSession(sessionID, generation, activateErr)
			return activateErr
		}
		return nil
	})
	if err != nil {
		if exit != nil {
			h.world.DetachSession(sessionID, generation, err)
			<-exit
		}
		h.players.Abort(identity.PlayerID)
		return err
	}
	h.players.Confirm(identity.PlayerID)

	result := <-exit
	if result.HasSnapshot {
		_ = h.players.Observe(identity.PlayerID, identity.DisplayName, result.Snapshot, h.world.TickCount(), true)
	}
	return result.Err
}

func (h *Host) Shutdown(ctx context.Context) error {
	if ctx == nil {
		panic("server: nil host shutdown context")
	}
	select {
	case <-h.shutdownGate:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { h.shutdownGate <- struct{}{} }()

	h.world.stepMu.Lock()
	closed := h.world.lifecycle == serverClosed
	h.world.stepMu.Unlock()
	if closed {
		h.players.CloseWorker()
		return nil
	}

	h.mu.Lock()
	h.closing = true
	listener := h.listener
	var active activeLogin
	hasActive := h.active != nil
	if hasActive {
		active = *h.active
	}
	streams := make([]network.ServerPacketStream, 0, len(h.preLoginStreams))
	for _, stream := range h.preLoginStreams {
		streams = append(streams, stream)
	}
	h.mu.Unlock()

	var listenerErr error
	if listener != nil {
		listenerErr = listener.Close()
	}
	if hasActive && active.Session != 0 {
		h.world.DetachSession(active.Session, active.Generation, network.ErrClosed)
	}
	for _, stream := range streams {
		_ = stream.Close()
	}
	if err := waitForHostWorkers(ctx, &h.workers); err != nil {
		return errors.Join(listenerErr, err)
	}
	if err := h.players.Flush(ctx); err != nil {
		return errors.Join(listenerErr, err)
	}

	h.mu.Lock()
	runtimeCancel := h.runtimeCancel
	h.mu.Unlock()
	if runtimeCancel != nil {
		runtimeCancel()
	}
	if err := h.world.Shutdown(ctx); err != nil {
		return errors.Join(listenerErr, err)
	}
	h.players.CloseWorker()
	return listenerErr
}

func hostPlayerLoadReject(err error) (network.LoginRejectCode, string) {
	if errors.Is(err, storage.ErrCorrupt) || errors.Is(err, storage.ErrFutureVersion) {
		return network.LoginPlayerDataCorrupt, "玩家数据已损坏"
	}
	return network.LoginStoreUnavailable, "玩家数据暂不可用"
}
