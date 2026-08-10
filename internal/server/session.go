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
)

var (
	ErrInvalidSession          = errors.New("server: invalid session")
	ErrSessionExists           = errors.New("server: session already exists")
	ErrTrustedObserverDisabled = errors.New("server: trusted observer disabled")

	errHeartbeatTimeout      = errors.New("server: heartbeat timeout")
	errInvalidHeartbeatReply = errors.New("server: invalid heartbeat reply")
	errSessionOutboxFull     = errors.New("server: session outbox full")
	errUnknownClientMessage  = errors.New("server: unknown client message")
)

type SessionSpec struct {
	ID          sim.SessionID
	Generation  uint64
	PlayerID    core.PlayerID
	DisplayName string
	Endpoint    network.ServerEndpoint
	Restore     sim.PlayerRestore
}

type SessionExit struct {
	ID          sim.SessionID
	Generation  uint64
	Snapshot    sim.PlayerSnapshot
	HasSnapshot bool
	Err         error
}
type publication struct {
	snapshotSent bool
	lastRevision uint64
	resyncQueued bool
}

type snapshotRequest struct {
	resync bool
}

type session struct {
	id          sim.SessionID
	generation  uint64
	playerID    core.PlayerID
	displayName string
	endpoint    network.ServerEndpoint
	ctx         context.Context
	cancel      context.CancelFunc
	outbox      chan network.ServerMessage
	workers     *sync.WaitGroup
	exit        chan SessionExit
	detach      func(sim.SessionID, uint64, error) bool

	mu               sync.Mutex
	isClosed         bool
	closeOnce        sync.Once
	closeErr         error
	failOnce         sync.Once
	nextToken        uint64
	outstandingToken uint64
	heartbeatReply   chan uint64

	hasView          bool
	viewDimension    core.DimensionID
	viewCenter       core.ChunkPos
	publications     map[core.ChunkKey]*publication
	pendingSnapshots map[core.ChunkKey]snapshotRequest
	visiblePlayers   map[core.PlayerID]visiblePlayer

	// 掉落物差分状态：已发布镜像与三块复用 scratch，容量固定为 MaxSessionDrops。
	publishedDrops    map[core.DropID]sim.DropSnapshot
	dropScratch       []sim.DropSnapshot
	dropUpsertScratch []network.ItemDrop
	dropRemoveScratch []core.DropID
}

func newSession(
	parent context.Context,
	spec SessionSpec,
	config Config,
	workers *sync.WaitGroup,
	clock heartbeatClock,
	detach func(sim.SessionID, uint64, error) bool,
) *session {
	if config.OutboxCapacity < 1 {
		panic("server: session outbox capacity must be positive")
	}
	if clock == nil {
		panic("server: nil heartbeat clock")
	}
	ctx, cancel := context.WithCancel(parent)
	current := &session{
		id:                spec.ID,
		generation:        spec.Generation,
		playerID:          spec.PlayerID,
		displayName:       spec.DisplayName,
		endpoint:          spec.Endpoint,
		ctx:               ctx,
		cancel:            cancel,
		outbox:            make(chan network.ServerMessage, config.OutboxCapacity),
		workers:           workers,
		exit:              make(chan SessionExit, 1),
		detach:            detach,
		heartbeatReply:    make(chan uint64, 1),
		publications:      make(map[core.ChunkKey]*publication),
		pendingSnapshots:  make(map[core.ChunkKey]snapshotRequest),
		visiblePlayers:    make(map[core.PlayerID]visiblePlayer),
		publishedDrops:    make(map[core.DropID]sim.DropSnapshot, sim.MaxSessionDrops),
		dropScratch:       make([]sim.DropSnapshot, 0, sim.MaxSessionDrops),
		dropUpsertScratch: make([]network.ItemDrop, 0, sim.MaxSessionDrops),
		dropRemoveScratch: make([]core.DropID, 0, sim.MaxSessionDrops),
	}
	workers.Add(2)
	go current.writeLoop()
	go current.heartbeatLoop(clock, config.HeartbeatInterval, config.HeartbeatTimeout)
	return current
}

func newObserverSession(
	parent context.Context,
	id sim.SessionID,
	generation uint64,
	endpoint network.ServerEndpoint,
	capacity int,
	workers *sync.WaitGroup,
	detach func(sim.SessionID, uint64, error) bool,
) *session {
	if capacity < 1 {
		panic("server: session outbox capacity must be positive")
	}
	ctx, cancel := context.WithCancel(parent)
	current := &session{
		id:               id,
		generation:       generation,
		endpoint:         endpoint,
		ctx:              ctx,
		cancel:           cancel,
		outbox:           make(chan network.ServerMessage, capacity),
		workers:          workers,
		detach:           detach,
		heartbeatReply:   make(chan uint64, 1),
		publications:     make(map[core.ChunkKey]*publication),
		pendingSnapshots: make(map[core.ChunkKey]snapshotRequest),
		visiblePlayers:   make(map[core.PlayerID]visiblePlayer),
		publishedDrops:   make(map[core.DropID]sim.DropSnapshot),
	}
	workers.Add(1)
	go current.writeLoop()
	return current
}

// enqueue 永不等待 writer；满队列会关闭慢 session。
func (current *session) enqueue(message network.ServerMessage) bool {
	current.mu.Lock()
	if current.isClosed {
		current.mu.Unlock()
		return false
	}
	select {
	case current.outbox <- message:
		current.mu.Unlock()
		return true
	default:
		current.mu.Unlock()
		slog.Warn("慢客户端 outbox 已满，关闭 session", "session", current.id)
		current.fail(errSessionOutboxFull)
		return false
	}
}

func (current *session) close() {
	current.fail(network.ErrClosed)
}

func (current *session) shutdown() {
	current.closeOnce.Do(func() {
		current.mu.Lock()
		current.isClosed = true
		current.mu.Unlock()
		current.cancel()
		current.closeErr = current.endpoint.Close()
	})
}

// disconnectCodeFor 把会话关闭原因映射成协议的 DisconnectCode。
// 第二个返回值为 false 表示不应向客户端发送断开原因。
//
// 这里刻意用白名单而不是黑名单：只有三个具名原因会发送。黑名单会在将来
// 新增关闭原因时默认放行，而"默认发送"恰恰是风险所在——writeLoop 自身
// Send 失败或 panic 时也会调用 fail，此时 socket 已不可信，再发一次不仅
// 徒劳，还会在 writeLoop 的调用栈内构成重入。白名单让这两种情况自然落在
// 不发送的一侧，无需额外判断。
//
// network.ErrClosed 与 context.Canceled 同样不发送：客户端已经走了，
// 发了没人收。
//
// DisconnectServerShutdown 与 DisconnectInternalError 不在此表内：
// 关服走 detachSessionLocked(id, generation, nil)，cause 为 nil，
// 根本不经过带具名错误的 fail；而没有任何具名原因映射到 InternalError。
//
// errSessionOutboxFull 刻意不在表内：它由 enqueue 在 outbox 满时同步调用 fail
// 触发，而 enqueue 声明「永不等待 writer」并位于每 tick、每会话、每消息的发布
// 路径上。让它进入白名单会使发送的期限变成 enqueue 的阻塞，直接打破该不变量
// （既有测试 TestSessionFullOutboxClosesWithoutBlocking 守着它）。
//
// 另外两条理由方向一致：outbox 满恰恰意味着客户端没在消费，Disconnect 送不到；
// 且它是这几个原因里唯一已有服务端日志的（"慢客户端 outbox 已满，关闭 session"），
// 可诊断性本来就不缺。
func disconnectCodeFor(err error) (network.DisconnectCode, bool) {
	switch {
	case err == nil:
		return 0, false
	case errors.Is(err, errHeartbeatTimeout):
		return network.DisconnectTimeout, true
	case errors.Is(err, errInvalidHeartbeatReply):
		return network.DisconnectProtocolViolation, true
	case errors.Is(err, errUnknownClientMessage):
		return network.DisconnectProtocolViolation, true
	default:
		return 0, false
	}
}

// sessionDisconnectSendTimeout 是发送断开原因的上界。
//
// 它是上界而非等待：socket 正常时写入立即返回，只有对端已死、缓冲区满、
// 或 writeLoop 正持有 tcpStream 的 writeOwner 时才会走到期限。session.fail
// 处在慢客户端清理与关服的公共路径上，因此这个值必须小到不产生可感知的停顿。
const sessionDisconnectSendTimeout = 200 * time.Millisecond

// sendDisconnect 在关闭前尽力告知客户端断开原因，失败一律忽略。
//
// 必须在 shutdown() 之前调用：shutdown 会 cancel 会话上下文并关闭 endpoint，
// 之后就发不出任何东西了。也正因如此，这里不能用 current.ctx——它即将被取消——
// 而要用一个独立的短期限上下文。
//
// 直接调 endpoint.Send 而不走 outbox：fail 本身就是会话的终态路径，此刻把
// 消息塞进 outbox 没有意义——shutdown() 马上要 cancel 会话上下文，writer
// 未必还能消费到它；而且若走 enqueue，outbox 满时 enqueue 会同步再次调用
// fail，落进同一个 failOnce.Do 内部重入，是死锁而非单纯冗余。直接 Send 是
// 这条路径上唯一确定能把包送出去、且不会重入 failOnce 的方式。
func (current *session) sendDisconnect(err error) {
	code, ok := disconnectCodeFor(err)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(
		context.Background(), sessionDisconnectSendTimeout,
	)
	defer cancel()
	_ = current.endpoint.Send(ctx, network.Disconnect{
		Code:    code,
		Message: err.Error(),
	})
}

func (current *session) fail(err error) {
	current.failOnce.Do(func() {
		current.sendDisconnect(err)
		current.shutdown()
		if current.detach != nil {
			go current.detach(current.id, current.generation, err)
		}
	})
}

func (current *session) closed() bool {
	current.mu.Lock()
	defer current.mu.Unlock()
	return current.isClosed
}
func (current *session) writeLoop() {
	defer current.workers.Done()
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.Error(
				"session writer panic 已隔离",
				"session", current.id,
				"panic", recovered,
			)
			current.fail(errors.New("server: session writer panic"))
		}
	}()
	for {
		select {
		case <-current.ctx.Done():
			return
		case message := <-current.outbox:
			if err := current.endpoint.Send(current.ctx, message); err != nil {
				current.fail(err)
				return
			}
		}
	}
}
