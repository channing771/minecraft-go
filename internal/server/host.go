package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/storage"
)

const hostPreLoginCapacity = 16

var (
	errHostSessionIDExhausted = errors.New("server: host session IDs exhausted")
	errHostAlreadyOnline      = errors.New("server: player is already online")
	errHostServerFull         = errors.New("server: host is full")
	errHostLoginNotReserved   = errors.New("server: login reservation is no longer current")
	errHostSessionRegistered  = errors.New("server: session is already registered")
)

type Host struct {
	config  Config
	world   *Server
	players *playerPersistence

	preLogin        chan struct{}
	mu              sync.Mutex
	activeByPlayer  map[core.PlayerID]*activeLogin
	activeBySession map[sim.SessionID]*activeLogin
	preLoginStreams map[uint64]*pendingLoginStream
	nextPreLogin    uint64
	nextSession     sim.SessionID
	nextGeneration  uint64
	listener        network.Listener
	runtimeCancel   context.CancelFunc
	runtimeDone     chan error
	acceptWG        sync.WaitGroup
	pendingWG       sync.WaitGroup
	sessionWG       sync.WaitGroup
	shutdownGate    chan struct{}
	closing         bool
}

// HostStats 是不暴露内部 map/channel 的瞬时有界队列快照。
type HostStats struct {
	ActivePlayers         int
	MaxSessionOutboxDepth int
	PlayerSaveJobDepth    int
	PlayerSaveDoneDepth   int
}

// Stats 分别短暂取得 host、world 与 player persistence 锁；从不嵌套持锁。
func (h *Host) Stats() HostStats {
	h.mu.Lock()
	stats := HostStats{ActivePlayers: len(h.activeBySession)}
	h.mu.Unlock()

	h.world.stepMu.Lock()
	for _, current := range h.world.sessions {
		if current != nil {
			stats.MaxSessionOutboxDepth = max(stats.MaxSessionOutboxDepth, len(current.outbox))
		}
	}
	h.world.stepMu.Unlock()

	h.players.mu.Lock()
	stats.PlayerSaveJobDepth = len(h.players.jobs)
	stats.PlayerSaveDoneDepth = len(h.players.completions)
	h.players.mu.Unlock()
	return stats
}

// RunAtInputBoundary 在完整 world tick 之间执行 action，并等待 wantPlayers 个不同
// session 的指定输入序号进入 world ingress。action 不得调用会再次取得 world step 锁的方法。
func (h *Host) RunAtInputBoundary(
	ctx context.Context,
	sequence uint64,
	wantPlayers int,
	action func() error,
) error {
	if ctx == nil {
		return errors.New("server: nil input boundary context")
	}
	if action == nil {
		return errors.New("server: nil input boundary action")
	}
	if sequence == 0 || wantPlayers <= 0 {
		return fmt.Errorf(
			"server: invalid input boundary sequence=%d players=%d",
			sequence, wantPlayers,
		)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	h.world.stepMu.Lock()
	defer h.world.stepMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	boundary := newInputIngressBoundary(sequence, wantPlayers)
	if !h.world.inputBoundary.CompareAndSwap(nil, boundary) {
		return errors.New("server: input boundary already active")
	}
	defer h.world.inputBoundary.CompareAndSwap(boundary, nil)
	if err := action(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-boundary.done:
		if err := ctx.Err(); err != nil {
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-h.world.ctx.Done():
		return h.world.ctx.Err()
	}
}

type pendingLoginStream struct {
	stream network.ServerPacketStream
	cancel context.CancelFunc
}

type activeLogin struct {
	PlayerID   core.PlayerID
	Name       string
	Session    sim.SessionID
	Generation uint64
}

func NewHost(config Config, generator Generator, store storage.WorldStore) *Host {
	config.validate()
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
		activeByPlayer:  make(map[core.PlayerID]*activeLogin),
		activeBySession: make(map[sim.SessionID]*activeLogin),
		preLoginStreams: make(map[uint64]*pendingLoginStream),
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
		h.acceptWG.Add(1)
	}
	h.mu.Unlock()
	go func() { h.runtimeDone <- h.world.RunTicks(runCtx) }()
	if listener != nil {
		go func() {
			defer h.acceptWG.Done()
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
	for _, active := range h.activeLogins() {
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

func (h *Host) activeLogins() []activeLogin {
	h.mu.Lock()
	active := make([]activeLogin, 0, len(h.activeBySession))
	for _, entry := range h.activeBySession {
		active = append(active, *entry)
	}
	h.mu.Unlock()
	sort.Slice(active, func(left, right int) bool {
		return active[left].Session < active[right].Session
	})
	return active
}

func (h *Host) reserveLogin(playerID core.PlayerID) (*activeLogin, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.activeByPlayer[playerID] != nil {
		return nil, errHostAlreadyOnline
	}
	if h.closing || len(h.activeByPlayer) >= h.config.MaxPlayers {
		return nil, errHostServerFull
	}
	entry := &activeLogin{PlayerID: playerID}
	h.activeByPlayer[playerID] = entry
	return entry, nil
}

func (h *Host) promoteLogin(entry *activeLogin, session sim.SessionID, generation uint64) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if entry == nil || h.activeByPlayer[entry.PlayerID] != entry {
		return errHostLoginNotReserved
	}
	if h.activeBySession[session] != nil {
		return errHostSessionRegistered
	}
	entry.Session = session
	entry.Generation = generation
	h.activeBySession[session] = entry
	return nil
}

func (h *Host) releaseLogin(entry *activeLogin) {
	if entry == nil {
		return
	}
	h.mu.Lock()
	if h.activeByPlayer[entry.PlayerID] == entry {
		delete(h.activeByPlayer, entry.PlayerID)
	}
	if entry.Session != 0 && h.activeBySession[entry.Session] == entry {
		delete(h.activeBySession, entry.Session)
	}
	h.mu.Unlock()
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
	h.preLoginStreams[streamID] = &pendingLoginStream{stream: stream}
	h.pendingWG.Add(1)
	h.mu.Unlock()
	return streamID, nil
}

func (h *Host) acceptStream(
	ctx context.Context,
	stream network.ServerPacketStream,
	streamID uint64,
) (resultErr error) {
	pendingCtx, cancelPending := context.WithCancel(ctx)
	h.bindPendingCancel(streamID, cancelPending)
	var active *activeLogin
	var activated, confirmed, promoted bool
	defer func() {
		cancelPending()
		_ = stream.Close()
		if active != nil {
			if !confirmed {
				h.players.Abort(active.PlayerID)
			}
			if activated {
				h.players.Deactivate(active.PlayerID)
			}
			h.releaseLogin(active)
		}
		h.finishStreamLifecycle(streamID, promoted)
	}()

	pending, err := network.BeginServerLogin(pendingCtx, stream)
	if err != nil {
		return err
	}
	identity := pending.Identity()

	active, err = h.reserveLogin(identity.PlayerID)
	if errors.Is(err, errHostAlreadyOnline) {
		return pending.Reject(ctx, network.LoginAlreadyOnline, "玩家已在线")
	}
	if err != nil {
		return pending.Reject(ctx, network.LoginServerFull, "服务器已满")
	}
	h.mu.Lock()
	active.Name = identity.DisplayName
	h.mu.Unlock()

	restore, err := h.players.Prepare(
		pending.Context(),
		identity.PlayerID,
		identity.DisplayName,
		h.world.store.Metadata(),
	)
	if err != nil {
		code, message := hostPlayerLoadReject(err)
		_ = pending.Reject(ctx, code, message)
		return err
	}

	h.mu.Lock()
	if h.nextSession == ^sim.SessionID(0) || h.nextGeneration == ^uint64(0) {
		h.mu.Unlock()
		_ = pending.Reject(ctx, network.LoginInternalError, "服务端会话编号已耗尽")
		return errHostSessionIDExhausted
	}
	h.nextSession++
	h.nextGeneration++
	sessionID := h.nextSession
	generation := h.nextGeneration
	h.mu.Unlock()

	var exit <-chan SessionExit
	err = pending.Accept(ctx, func(endpoint network.ServerEndpoint) error {
		var attachErr error
		exit, attachErr = h.world.AttachSession(SessionSpec{
			ID: sessionID, Generation: generation,
			PlayerID: identity.PlayerID, DisplayName: identity.DisplayName,
			Endpoint: endpoint, Restore: restore,
		})
		if attachErr != nil {
			return attachErr
		}
		if activateErr := h.players.Activate(identity.PlayerID, identity.DisplayName); activateErr != nil {
			h.world.DetachSession(sessionID, generation, activateErr)
			return activateErr
		}
		activated = true
		promoted = true
		if promoteErr := h.promotePendingLogin(active, sessionID, generation, streamID); promoteErr != nil {
			promoted = false
			h.world.DetachSession(sessionID, generation, promoteErr)
			return promoteErr
		}
		return nil
	})
	if err != nil {
		if exit != nil {
			return h.collectSessionExit(active, sessionID, generation, exit, err)
		}
		return err
	}
	h.players.Confirm(identity.PlayerID)
	confirmed = true

	return h.collectSessionExit(active, sessionID, generation, exit, nil)
}

func (h *Host) bindPendingCancel(streamID uint64, cancel context.CancelFunc) {
	h.mu.Lock()
	pending := h.preLoginStreams[streamID]
	closing := h.closing
	if pending != nil {
		pending.cancel = cancel
	}
	h.mu.Unlock()
	if closing || pending == nil {
		cancel()
	}
}

func (h *Host) promotePendingLogin(
	entry *activeLogin,
	session sim.SessionID,
	generation uint64,
	streamID uint64,
) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closing || entry == nil || h.activeByPlayer[entry.PlayerID] != entry ||
		h.preLoginStreams[streamID] == nil {
		return errHostLoginNotReserved
	}
	if h.activeBySession[session] != nil {
		return errHostSessionRegistered
	}
	h.sessionWG.Add(1)
	entry.Session = session
	entry.Generation = generation
	h.activeBySession[session] = entry
	delete(h.preLoginStreams, streamID)
	h.pendingWG.Done()
	<-h.preLogin
	return nil
}

func (h *Host) finishStreamLifecycle(streamID uint64, promoted bool) {
	if promoted {
		h.sessionWG.Done()
		return
	}
	h.mu.Lock()
	if h.preLoginStreams[streamID] != nil {
		delete(h.preLoginStreams, streamID)
		h.pendingWG.Done()
		<-h.preLogin
	}
	h.mu.Unlock()
}

func (h *Host) collectSessionExit(
	active *activeLogin,
	session sim.SessionID,
	generation uint64,
	exit <-chan SessionExit,
	cause error,
) error {
	if cause != nil {
		h.world.DetachSession(session, generation, cause)
	}
	result := <-exit
	var observeErr error
	if result.HasSnapshot {
		observeErr = h.players.Observe(
			active.PlayerID,
			active.Name,
			result.Snapshot,
			h.world.TickCount(),
			true,
		)
	}
	if cause != nil {
		return errors.Join(cause, observeErr)
	}
	return errors.Join(result.Err, observeErr)
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
	h.mu.Unlock()

	var listenerErr error
	if listener != nil {
		listenerErr = listener.Close()
	}
	if err := h.waitAcceptLoop(ctx); err != nil {
		return errors.Join(listenerErr, err)
	}
	h.closePendingLogins()
	if err := waitForHostWorkers(ctx, &h.pendingWG); err != nil {
		return errors.Join(listenerErr, err)
	}
	for _, active := range h.activeLogins() {
		h.world.DetachSession(active.Session, active.Generation, network.ErrClosed)
	}
	if err := waitForHostWorkers(ctx, &h.sessionWG); err != nil {
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

func (h *Host) waitAcceptLoop(ctx context.Context) error {
	return waitForHostWorkers(ctx, &h.acceptWG)
}

func (h *Host) closePendingLogins() {
	h.mu.Lock()
	ids := make([]uint64, 0, len(h.preLoginStreams))
	for id := range h.preLoginStreams {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(left, right int) bool { return ids[left] < ids[right] })
	pending := make([]*pendingLoginStream, 0, len(ids))
	for _, id := range ids {
		login := h.preLoginStreams[id]
		pending = append(pending, &pendingLoginStream{
			stream: login.stream,
			cancel: login.cancel,
		})
	}
	h.mu.Unlock()
	for _, login := range pending {
		if login == nil {
			continue
		}
		if login.cancel != nil {
			login.cancel()
		}
		_ = login.stream.Close()
	}
}

func (h *Host) waitPendingLogins() {
	h.pendingWG.Wait()
}

func hostPlayerLoadReject(err error) (network.LoginRejectCode, string) {
	if errors.Is(err, storage.ErrCorrupt) || errors.Is(err, storage.ErrFutureVersion) {
		return network.LoginPlayerDataCorrupt, "玩家数据已损坏"
	}
	return network.LoginStoreUnavailable, "玩家数据暂不可用"
}
