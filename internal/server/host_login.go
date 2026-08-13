package server

import (
	"context"
	"errors"
	"log/slog"
	"sort"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/sim"
	"github.com/channing771/mornlea/internal/storage"
)

var (
	errHostSessionIDExhausted = errors.New("server: host session IDs exhausted")
	errHostAlreadyOnline      = errors.New("server: player is already online")
	errHostServerFull         = errors.New("server: host is full")
	errHostLoginNotReserved   = errors.New("server: login reservation is no longer current")
	errHostSessionRegistered  = errors.New("server: session is already registered")
)

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

func hostPlayerLoadReject(err error) (network.LoginRejectCode, string) {
	if errors.Is(err, storage.ErrCorrupt) || errors.Is(err, storage.ErrFutureVersion) {
		return network.LoginPlayerDataCorrupt, "玩家数据已损坏"
	}
	return network.LoginStoreUnavailable, "玩家数据暂不可用"
}
