package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/sim"
	"github.com/channing771/mornlea/internal/storage"
)

const hostPreLoginCapacity = 16

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

func NewHost(
	ctx context.Context,
	config Config,
	generator Generator,
	store storage.WorldStore,
) (*Host, error) {
	if ctx == nil {
		return nil, errors.New("server: nil host constructor context")
	}
	config.validate()
	if store == nil {
		panic("server: nil host store")
	}
	if generator == nil {
		panic("server: nil generator")
	}
	var companions *companionPersistence
	if len(config.Companions) != 0 {
		// 伙伴启用即要求模型运行时就绪。config.Load 已在配置层守住静态完整性，
		// 这里是第二道边界：直接构造 server.Config 的入口（测试、未来的嵌入方）
		// 也不能带着残缺模型设置启动。校验放在 LoadCompanions 之前，失败时
		// 不触碰任何存档。错误只引用字段名与环境变量名，绝不回显密钥值。
		if err := config.AIModel.Validate(); err != nil {
			return nil, fmt.Errorf("server: 伙伴配置缺少可用的 AI 模型设置: %w", err)
		}
		if isHTTPSEndpoint(config.AIModel.Endpoint) && config.AIAPIKey == "" {
			return nil, fmt.Errorf(
				"server: AI endpoint 为 https 但未解析到 API 密钥（AIAPIKey 为空，检查环境变量 %q）",
				config.AIModel.APIKeyEnv,
			)
		}
		loaded, err := store.LoadCompanions(ctx)
		if errors.Is(err, storage.ErrCompanionsNotFound) {
			loaded = storage.StoredCompanions{}
		} else if err != nil {
			return nil, fmt.Errorf("load companions: %w", err)
		}
		ids := make(map[companion.ID]struct{}, len(loaded.Records)+len(config.Companions))
		for _, body := range loaded.Records {
			ids[body.ID] = struct{}{}
		}
		for _, definition := range config.Companions {
			ids[definition.ID] = struct{}{}
		}
		if len(ids) > companion.MaxStored {
			return nil, fmt.Errorf(
				"server: companion active+inactive count %d exceeds %d",
				len(ids), companion.MaxStored,
			)
		}
		companions = newCompanionPersistence(store, loaded, config)
	}
	gate := make(chan struct{}, 1)
	gate <- struct{}{}
	return &Host{
		config:          config,
		world:           newWorld(config, generator, store, companions),
		players:         newPlayerPersistence(store, config),
		preLogin:        make(chan struct{}, hostPreLoginCapacity),
		activeByPlayer:  make(map[core.PlayerID]*activeLogin),
		activeBySession: make(map[sim.SessionID]*activeLogin),
		preLoginStreams: make(map[uint64]*pendingLoginStream),
		runtimeDone:     make(chan error, 1),
		shutdownGate:    gate,
	}, nil
}

// isHTTPSEndpoint 报告 endpoint 是否使用 https scheme。只服务于 NewHost 的
// 密钥边界检查：调用前 endpoint 必已通过 companion.ModelSettings.Validate，
// 因此这里不重复完整形态校验；解析失败按非 https 处理，让 Validate 的错误
// 保持唯一的形态权威。
func isHTTPSEndpoint(endpoint string) bool {
	parsed, err := url.Parse(endpoint)
	return err == nil && parsed.Scheme == "https"
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
