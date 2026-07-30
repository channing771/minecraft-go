package server

import (
	"context"
	"errors"
	"sync"
	"time"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/storage"
)

type serverLifecycle uint8

const (
	serverRunning serverLifecycle = iota
	serverClosing
	serverClosed
)

type Server struct {
	config                    Config
	generator                 Generator
	store                     storage.Store
	engine                    *sim.Engine
	sessions                  map[sim.SessionID]*session
	trustedObserver           *session
	trustedObserverGeneration uint64

	ctx         context.Context
	cancel      context.CancelFunc
	saveCtx     context.Context
	cancelSaves context.CancelFunc

	incoming  chan incomingCommand
	jobs      chan chunkJob
	acquired  chan sim.AcquiredChunk
	generated chan sim.GeneratedChunk
	pending   []chunkJob
	queued    map[core.ChunkKey]struct{}

	trustedObserverMu       sync.Mutex
	trustedObserverCenters  chan trustedObserverCenter
	trustedObserverSequence uint64
	appliedTrustedObserver  appliedTrustedObserverCenter

	workers         sync.WaitGroup
	saveWorkers     sync.WaitGroup
	saveJobs        chan saveJob
	saveCompletions chan saveCompletion
	autosaveActive  bool
	retry           map[storage.RegionKey][]retrySave
	retryInFlight   map[uint64]retrySave
	nextRetryID     uint64
	backpressured   bool
	lastSaveSuccess time.Time
	lastSaveError   string
	lastSaveErrorAt time.Time
	stepMu          sync.Mutex
	shutdownGate    chan struct{}
	lifecycle       serverLifecycle
	runtimeDone     chan struct{}
	saveDone        chan struct{}
	closedDone      chan struct{}
	storePhase      storeShutdownPhase
}

func NewWorld(config Config, generator Generator, store storage.Store) *Server {
	config.validate()
	if generator == nil {
		panic("server: nil generator")
	}
	if store == nil {
		panic("server: nil store")
	}
	ctx, cancel := context.WithCancel(context.Background())
	saveCtx, cancelSaves := context.WithCancel(context.Background())
	shutdownGate := make(chan struct{}, 1)
	shutdownGate <- struct{}{}
	queueCapacity := max(1, config.Workers*2)
	server := &Server{
		config:          config,
		generator:       generator,
		store:           store,
		engine:          sim.NewEngine(config.ViewRadius),
		sessions:        make(map[sim.SessionID]*session),
		ctx:             ctx,
		cancel:          cancel,
		saveCtx:         saveCtx,
		cancelSaves:     cancelSaves,
		incoming:        make(chan incomingCommand, inputCapacity),
		jobs:            make(chan chunkJob, queueCapacity),
		acquired:        make(chan sim.AcquiredChunk, queueCapacity),
		generated:       make(chan sim.GeneratedChunk, queueCapacity),
		saveJobs:        make(chan saveJob, config.SaveWorkers*2),
		saveCompletions: make(chan saveCompletion, config.SaveWorkers*2),
		retry:           make(map[storage.RegionKey][]retrySave),
		retryInFlight:   make(map[uint64]retrySave),
		queued:          make(map[core.ChunkKey]struct{}),
		runtimeDone:     make(chan struct{}),
		saveDone:        make(chan struct{}),
		closedDone:      make(chan struct{}),
		shutdownGate:    shutdownGate,
	}
	if config.TrustedObserver {
		server.trustedObserverCenters = make(chan trustedObserverCenter, 1)
	}

	server.workers.Add(config.Workers)
	for range config.Workers {
		go server.chunkWorker()
	}
	server.saveWorkers.Add(config.SaveWorkers)
	for range config.SaveWorkers {
		go server.saveWorker()
	}
	go func() {
		server.workers.Wait()
		close(server.runtimeDone)
	}()
	go func() {
		server.saveWorkers.Wait()
		close(server.saveDone)
	}()
	return server
}

// New 暂时保留为单玩家测试与尚未迁移调用方的兼容包装。
func New(
	config Config,
	endpoint network.ServerEndpoint,
	generator Generator,
	store storage.Store,
) *Server {
	if endpoint == nil {
		panic("server: nil endpoint")
	}
	server := NewWorld(config, generator, store)
	if config.TrustedObserver {
		if err := server.AttachTrustedObserver(endpoint); err != nil {
			panic(err)
		}
		return server
	}
	compatibilityID := sim.SessionID(1)
	if _, err := server.AttachSession(SessionSpec{
		ID:         compatibilityID,
		Generation: 1,
		Endpoint:   endpoint,
		Restore: sim.PlayerRestore{
			SpawnDimension: server.config.SpawnDimension,
			SpawnAnchor:    server.config.SpawnAnchor,
		},
	}); err != nil {
		panic(err)
	}
	return server
}

func (server *Server) AttachSession(spec SessionSpec) (<-chan SessionExit, error) {
	server.stepMu.Lock()
	defer server.stepMu.Unlock()
	return server.attachSessionLocked(spec)
}

func (server *Server) DetachSession(
	id sim.SessionID,
	generation uint64,
	cause error,
) bool {
	server.stepMu.Lock()
	defer server.stepMu.Unlock()
	return server.detachSessionLocked(id, generation, cause)
}

func (server *Server) AttachTrustedObserver(endpoint network.ServerEndpoint) error {
	server.stepMu.Lock()
	defer server.stepMu.Unlock()
	return server.attachTrustedObserverLocked(endpoint)
}

func (server *Server) detachTrustedObserver(
	id sim.SessionID,
	generation uint64,
	cause error,
) bool {
	server.stepMu.Lock()
	defer server.stepMu.Unlock()
	return server.detachTrustedObserverLocked(id, generation, cause)
}

func (server *Server) SetTrustedObserverCenter(
	dimension core.DimensionID,
	center core.ChunkPos,
) error {
	server.stepMu.Lock()
	defer server.stepMu.Unlock()
	return server.setTrustedObserverCenterLocked(dimension, center)
}

func (server *Server) AppliedTrustedObserverCenter() (
	core.DimensionID,
	core.ChunkPos,
	uint64,
	bool,
) {
	server.stepMu.Lock()
	defer server.stepMu.Unlock()
	return server.appliedTrustedObserverCenterLocked()
}

// Step 执行一次服务端编排与权威模拟 tick。
func (server *Server) Step() sim.TickResult {
	server.stepMu.Lock()
	defer server.stepMu.Unlock()
	if server.lifecycle != serverRunning {
		return sim.TickResult{}
	}
	started := time.Now()
	if server.config.TickObserver != nil {
		defer func() { server.config.TickObserver(time.Since(started)) }()
	}

	server.drainSaveCompletions()
	trustedCenter, trustedSequence, hasTrustedCenter := server.drainTrustedObserverCenter()
	server.drainIncoming()
	server.drainAcquired()
	server.drainGenerated()
	result := server.engine.Step()
	if hasTrustedCenter {
		server.appliedTrustedObserver = appliedTrustedObserverCenter{
			dimension: trustedCenter.dimension,
			center:    trustedCenter.center,
			sequence:  trustedSequence,
		}
	}
	server.publish(result)
	server.cancelUnwantedPending()
	server.appendChunkRequests(chunkJobLoad, result.Acquire)
	server.appendChunkRequests(chunkJobGenerate, result.Generate)
	server.schedulePersistence(result.Tick)
	server.updatePersistenceBackpressure()
	if !server.backpressured {
		server.scheduleChunkJobs()
	}
	return result
}

// StepForTest 显式推进一个完整 tick，供无头确定性集成测试使用。
func (server *Server) StepForTest() sim.TickResult {
	return server.Step()
}

func (server *Server) RunTicks(ctx context.Context) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-server.ctx.Done():
			select {
			case <-server.closedDone:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		case <-ticker.C:
			server.Step()
		}
	}
}

// Run 暂时保留为测试兼容包装；RunTicks 返回后执行既有安全关服。
func (server *Server) Run(ctx context.Context) error {
	runErr := server.RunTicks(ctx)
	if errors.Is(runErr, context.Canceled) ||
		errors.Is(runErr, context.DeadlineExceeded) {
		return server.shutdownAfterRunCancellation(runErr)
	}
	return runErr
}

func (server *Server) shutdownAfterRunCancellation(runErr error) error {
	shutdownCtx, cancel := context.WithTimeout(
		context.Background(),
		server.config.ShutdownTimeout,
	)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return err
	}
	return runErr
}

func (server *Server) ChunkInfo(
	dimension core.DimensionID,
	pos core.ChunkPos,
) (sim.ChunkInfo, bool) {
	server.stepMu.Lock()
	defer server.stepMu.Unlock()
	return server.engine.ChunkInfo(core.ChunkKey{
		Dimension: dimension,
		Pos:       pos,
	})
}

func (server *Server) PlayerStateFor(id sim.SessionID) (sim.PlayerUpdate, bool) {
	server.stepMu.Lock()
	defer server.stepMu.Unlock()
	return server.engine.Player(id)
}

func (server *Server) PlayerSnapshotFor(id sim.SessionID) (sim.PlayerSnapshot, bool) {
	server.stepMu.Lock()
	defer server.stepMu.Unlock()
	return server.engine.PlayerSnapshot(id)
}

func (server *Server) TickCount() uint64 {
	server.stepMu.Lock()
	defer server.stepMu.Unlock()
	return server.engine.TickCount()
}

// PlayerState 暂时保留为单玩家兼容包装。
func (server *Server) PlayerState() (sim.PlayerUpdate, bool) {
	server.stepMu.Lock()
	defer server.stepMu.Unlock()
	ids := server.sortedSessionIDsLocked()
	if len(ids) == 0 {
		return sim.PlayerUpdate{}, false
	}
	return server.engine.Player(ids[0])
}

func (server *Server) ChunkHash(
	dimension core.DimensionID,
	pos core.ChunkPos,
) ([32]byte, uint64, bool) {
	server.stepMu.Lock()
	defer server.stepMu.Unlock()
	return server.engine.ChunkHash(core.ChunkKey{
		Dimension: dimension,
		Pos:       pos,
	})
}

func (server *Server) drainAcquired() {
	for {
		select {
		case result := <-server.acquired:
			server.engine.SubmitAcquired(result)
		default:
			return
		}
	}
}

func (server *Server) drainGenerated() {
	for {
		select {
		case result := <-server.generated:
			server.engine.SubmitGenerated(result)
		default:
			return
		}
	}
}

func (server *Server) appendChunkRequests(kind chunkJobKind, keys []core.ChunkKey) {
	for _, key := range keys {
		if _, exists := server.queued[key]; exists {
			continue
		}
		server.queued[key] = struct{}{}
		server.pending = append(server.pending, chunkJob{Kind: kind, Key: key})
	}
}

func (server *Server) cancelUnwantedPending() {
	if len(server.pending) == 0 {
		return
	}
	kept := server.pending[:0]
	for _, job := range server.pending {
		if !server.engine.WantsChunk(job.Key) {
			delete(server.queued, job.Key)
			continue
		}
		kept = append(kept, job)
	}
	server.pending = kept
}

func (server *Server) scheduleChunkJobs() {
	for len(server.pending) != 0 {
		job := server.pending[0]
		select {
		case server.jobs <- job:
			server.pending = server.pending[1:]
			delete(server.queued, job.Key)
		default:
			return
		}
	}
}
