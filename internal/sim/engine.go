package sim

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/physics"
	"github.com/channing771/mornlea/internal/world"
)

const (
	productionTickInterval = 50 * time.Millisecond
	maxCatchUpSteps        = 5

	// defaultInteractionReach 是放置、挖掘与开启容器共用的最大交互距离的编译期默认值。
	// 唯一读取入口是 Tunables 快照，不得再以导出常量暴露——见 internal/archcheck
	// 的 TestTunableConstantsAreNotExported。
	defaultInteractionReach = 6
)

type Clock interface {
	C() <-chan time.Time
	Stop()
}

type sessionState struct {
	lastSequence                uint64
	lastTrustedObserverSequence uint64
	trustedObserver             bool
	hasView                     bool
	dimension                   core.DimensionID
	center                      core.ChunkPos
	wanted                      map[core.ChunkKey]struct{}
	player                      *playerState
	// 每名玩家同时最多查看一个容器（熔炉或箱子）；引用失效时由权威 tick 统一清除。
	container     core.ContainerRef
	viewContainer bool
}

type pendingChunkChanges struct {
	baseRevision uint64
	changes      map[uint32]BlockChange
	dirty        map[int]struct{}
}

type Engine struct {
	viewRadius         int
	dimensions         map[core.DimensionID]*Dimension
	sessions           map[SessionID]*sessionState
	wanted             map[core.ChunkKey]struct{}
	inFlightSaves      map[core.ChunkKey]persistenceInFlight
	subscriptionsDirty bool

	// 掉落物 tick 的复用 scratch，避免每 tick 分配固定上限集合。
	dropKeySeen            map[core.ChunkKey]struct{}
	dropKeyScratch         []core.ChunkKey
	containerViewerScratch []SessionID
	dropSessionScratch     []SessionID

	inboxMu   sync.Mutex
	commands  []Command
	acquired  []AcquiredChunk
	generated []GeneratedChunk
	tick      atomic.Uint64
	// worldTime 是权威绝对世界时间，只由 simulation owner 在 Step 中推进。
	worldTime atomic.Uint64

	// tunables 与 physicsTunables 在每次 Step 入口刷新一次，同一 tick 内全程使用，
	// 保证单个 tick 的所有判定基于同一份参数。
	tunables        Tunables
	physicsTunables physics.Tunables
}

// NewEngine 创建权威引擎。worldTime 是从 metadata 恢复的绝对世界时间。
func NewEngine(viewRadius int, worldTime uint64) *Engine {
	if viewRadius < 0 {
		panic("sim: negative view radius")
	}
	engine := &Engine{
		viewRadius: viewRadius,
		dimensions: map[core.DimensionID]*Dimension{
			core.Overworld: NewDimension(core.Overworld),
		},
		sessions:      make(map[SessionID]*sessionState),
		wanted:        make(map[core.ChunkKey]struct{}),
		inFlightSaves: make(map[core.ChunkKey]persistenceInFlight),
	}
	engine.worldTime.Store(worldTime)
	// 初始化快照，使未经 Step 就被调用的方法（例如 RegisterPlayer 的出生扫描）
	// 也有可用的参数快照。
	engine.tunables = ActiveTunables()
	engine.physicsTunables = physics.ActiveTunables()
	return engine
}

// WorldTime 返回最近一个完成 tick 的绝对世界时间。
func (engine *Engine) WorldTime() uint64 { return engine.worldTime.Load() }

// advanceWorldTime 把绝对世界时间推进恰好一个 tick 并返回新值。
func (engine *Engine) advanceWorldTime() uint64 { return engine.worldTime.Add(1) }

// Enqueue 可由 endpoint reader 并发调用。
func (engine *Engine) Enqueue(command Command) {
	engine.inboxMu.Lock()
	engine.commands = append(engine.commands, command)
	engine.inboxMu.Unlock()
}

// SubmitGenerated 可由生成 worker 并发调用，并转移 Chunk 所有权。
func (engine *Engine) SubmitGenerated(result GeneratedChunk) {
	engine.inboxMu.Lock()
	engine.generated = append(engine.generated, result)
	engine.inboxMu.Unlock()
}

// SubmitAcquired 可由区块读取 worker 并发调用，并转移 Chunk 所有权。
func (engine *Engine) SubmitAcquired(result AcquiredChunk) {
	engine.inboxMu.Lock()
	engine.acquired = append(engine.acquired, result)
	engine.inboxMu.Unlock()
}

func (engine *Engine) TickCount() uint64 {
	return engine.tick.Load()
}

func (engine *Engine) CloneReadyChunk(
	key core.ChunkKey,
) (*world.Chunk, uint64, bool) {
	dimension := engine.dimensions[key.Dimension]
	if dimension == nil {
		return nil, 0, false
	}
	return dimension.CloneReadyChunk(key.Pos)
}

func (engine *Engine) ChunkHash(
	key core.ChunkKey,
) ([32]byte, uint64, bool) {
	chunk, revision, ok := engine.CloneReadyChunk(key)
	if !ok {
		return [32]byte{}, 0, false
	}
	return chunk.Hash(), revision, true
}

func (engine *Engine) ChunkInfo(
	key core.ChunkKey,
) (ChunkInfo, bool) {
	dimension := engine.dimensions[key.Dimension]
	if dimension == nil {
		return ChunkInfo{}, false
	}
	return dimension.Info(key.Pos)
}

func (engine *Engine) takeInbox() ([]Command, []AcquiredChunk, []GeneratedChunk) {
	engine.inboxMu.Lock()
	commands := append([]Command(nil), engine.commands...)
	acquired := append([]AcquiredChunk(nil), engine.acquired...)
	generated := append([]GeneratedChunk(nil), engine.generated...)
	engine.commands = engine.commands[:0]
	engine.acquired = engine.acquired[:0]
	engine.generated = engine.generated[:0]
	engine.inboxMu.Unlock()
	return commands, acquired, generated
}
