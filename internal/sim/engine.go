package sim

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/fluid"
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
	viewRadius int
	// seed 是世界种子，构造后只读。它由 host 从 storage.Metadata.Seed 传入，
	// 与 worldgen.New 拿到的是**同一个值**——作物生长抽样必须与地形生成同源，
	// 否则同一个存档在两处会表现出两套"世界身份"。
	//
	// 权威模拟不 import storage（archcheck 的依赖白名单禁止），因此种子只能经
	// 构造参数注入，不在 sim 内引入第二个种子来源。测试一律传 0。
	seed               int64
	dimensions         map[core.DimensionID]*Dimension
	sessions           map[SessionID]*sessionState
	companions         map[companion.ID]*companionState
	wanted             map[core.ChunkKey]struct{}
	inFlightSaves      map[core.ChunkKey]persistenceInFlight
	subscriptionsDirty bool

	// fluidQueues 是流体待更新队列，**按维度各持一个实例**（原因见
	// fluidQueue 的注释：internal/fluid 的处理全序不含维度）。队列不持久化，
	// 重启与区块进入推进范围时由 advanceFluids 的边界重扫恢复（design.md D5）。
	fluidQueues map[core.DimensionID]*fluid.Queue
	// fluidScope 是上一 tick 的流体推进范围，fluidScopeNext 是构建本 tick 范围
	// 用的复用 scratch；两者每 tick 交换，用来识别「本 tick 新进入范围」的区块
	// 并对其重扫。
	fluidScope     map[core.ChunkKey]struct{}
	fluidScopeNext map[core.ChunkKey]struct{}
	// fluidDimensionScratch 是维度排序的复用缓冲。
	fluidDimensionScratch []core.DimensionID
	// fluidRescan 是跨 tick 的边界重扫待办，见 fluidRescanState。
	fluidRescan fluidRescanState

	// cropCellScratch 是作物随机 tick 抽样下标的复用缓冲；抽样每 tick 执行
	// 「活动区块数 × 24 个区段」次，不复用就会在权威 tick 上产生同量级的分配。
	cropCellScratch []int
	// cropCellsExamined 是**最近一个 tick** 里被作物随机 tick 考察过的格数。
	//
	// 它是 spec「单个 tick 内被考察的格数 MUST NOT 随世界中作物的数量增长」
	// 这条成本契约的可读计数：该断言无法从方块结果观察（两个世界的作物数不同，
	// 方块结果本来就不同），只能靠一个显式计数。生产代码只写不读，包内测试读。
	cropCellsExamined int

	// 掉落物 tick 的复用 scratch，避免每 tick 分配固定上限集合。
	dropKeySeen            map[core.ChunkKey]struct{}
	dropKeyScratch         []core.ChunkKey
	containerViewerScratch []SessionID
	dropSessionScratch     []SessionID

	inboxMu          sync.Mutex
	commands         []Command
	companionActions []CompanionAction
	acquired         []AcquiredChunk
	generated        []GeneratedChunk
	tick             atomic.Uint64
	// worldTime 是权威绝对世界时间，只由 simulation owner 在 Step 中推进。
	worldTime atomic.Uint64

	// stepPhaseObserver 仅供包内测试观测 Step 的阶段进入顺序。玩家命令 →
	// 伙伴 action → 统一物理是规格契约，但三个阶段写互不相交的状态、无法从
	// 外部结果观察先后，只能用显式探针锁定；生产代码恒为 nil，nil 判断零开销。
	stepPhaseObserver func(stepPhase)

	// tunables 与 physicsTunables 在每次 Step 入口刷新一次，同一 tick 内全程使用，
	// 保证单个 tick 的所有判定基于同一份参数。
	tunables        Tunables
	physicsTunables physics.Tunables
}

// NewEngine 创建权威引擎。worldTime 是从 metadata 恢复的绝对世界时间，
// seed 是世界种子（与 worldgen.New 同值，见 Engine.seed 的说明）。
func NewEngine(viewRadius int, worldTime uint64, seed int64) *Engine {
	if viewRadius < 0 {
		panic("sim: negative view radius")
	}
	engine := &Engine{
		viewRadius: viewRadius,
		seed:       seed,
		dimensions: map[core.DimensionID]*Dimension{
			core.Overworld: NewDimension(core.Overworld),
		},
		sessions:      make(map[SessionID]*sessionState),
		companions:    make(map[companion.ID]*companionState),
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
