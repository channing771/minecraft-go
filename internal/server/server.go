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

const (
	localSessionID sim.SessionID = 1
	inputCapacity                = 256
)

var ErrTrustedObserverDisabled = errors.New("server: trusted observer disabled")

type trustedObserverCenter struct {
	dimension core.DimensionID
	center    core.ChunkPos
}

type appliedTrustedObserverCenter struct {
	dimension core.DimensionID
	center    core.ChunkPos
	sequence  uint64
}

type Server struct {
	config    Config
	endpoint  network.ServerEndpoint
	generator Generator
	store     storage.Store
	engine    *sim.Engine
	session   *session

	ctx         context.Context
	cancel      context.CancelFunc
	saveCtx     context.Context
	cancelSaves context.CancelFunc

	incoming  chan sim.Command
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
	stepMu          sync.Mutex
	closeOnce       sync.Once
}

func New(
	config Config,
	endpoint network.ServerEndpoint,
	generator Generator,
	store storage.Store,
) *Server {
	config.validate()
	if endpoint == nil {
		panic("server: nil endpoint")
	}
	if generator == nil {
		panic("server: nil generator")
	}
	if store == nil {
		panic("server: nil store")
	}
	ctx, cancel := context.WithCancel(context.Background())
	saveCtx, cancelSaves := context.WithCancel(ctx)
	queueCapacity := max(1, config.Workers*2)
	server := &Server{
		config:          config,
		endpoint:        endpoint,
		generator:       generator,
		store:           store,
		engine:          sim.NewEngine(config.ViewRadius),
		ctx:             ctx,
		cancel:          cancel,
		saveCtx:         saveCtx,
		cancelSaves:     cancelSaves,
		incoming:        make(chan sim.Command, inputCapacity),
		jobs:            make(chan chunkJob, queueCapacity),
		acquired:        make(chan sim.AcquiredChunk, queueCapacity),
		generated:       make(chan sim.GeneratedChunk, queueCapacity),
		saveJobs:        make(chan saveJob, config.SaveWorkers*2),
		saveCompletions: make(chan saveCompletion, config.SaveWorkers*2),
		queued:          make(map[core.ChunkKey]struct{}),
	}
	if config.TrustedObserver {
		server.trustedObserverCenters = make(chan trustedObserverCenter, 1)
	} else {
		server.engine.RegisterSession(
			localSessionID,
			config.SpawnDimension,
			config.SpawnAnchor,
		)
	}

	server.session = newSession(
		ctx,
		endpoint,
		config.OutboxCapacity,
		&server.workers,
	)
	server.workers.Add(1)
	go server.endpointReader()
	server.workers.Add(config.Workers)
	for range config.Workers {
		go server.chunkWorker()
	}
	server.saveWorkers.Add(config.SaveWorkers)
	for range config.SaveWorkers {
		go server.saveWorker()
	}
	return server
}

// Step 执行一次服务端编排与权威模拟 tick。
func (server *Server) Step() sim.TickResult {
	started := time.Now()
	if server.config.TickObserver != nil {
		defer func() { server.config.TickObserver(time.Since(started)) }()
	}
	server.stepMu.Lock()
	defer server.stepMu.Unlock()

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
	server.cancelForgottenPending(result.Forget)
	server.appendChunkRequests(chunkJobLoad, result.Acquire)
	server.appendChunkRequests(chunkJobGenerate, result.Generate)
	server.scheduleChunkJobs()
	server.schedulePersistence(result.Tick)
	return result
}

// AppliedTrustedObserverCenter 返回最近一次由 Step 应用的 trusted center。
// 返回值均为副本；该状态只供 server-internal benchmark 同步使用。
func (server *Server) AppliedTrustedObserverCenter() (
	core.DimensionID,
	core.ChunkPos,
	uint64,
	bool,
) {
	server.stepMu.Lock()
	defer server.stepMu.Unlock()
	applied := server.appliedTrustedObserver
	if !server.config.TrustedObserver || applied.sequence == 0 {
		return 0, core.ChunkPos{}, 0, false
	}
	return applied.dimension, applied.center, applied.sequence, true
}

func (server *Server) SetTrustedObserverCenter(
	dimension core.DimensionID,
	center core.ChunkPos,
) error {
	if !server.config.TrustedObserver {
		return ErrTrustedObserverDisabled
	}
	if dimension != core.Overworld {
		return errors.New("server: trusted observer center must be overworld")
	}
	request := trustedObserverCenter{dimension: dimension, center: center}
	server.trustedObserverMu.Lock()
	defer server.trustedObserverMu.Unlock()
	select {
	case <-server.trustedObserverCenters:
	default:
	}
	select {
	case server.trustedObserverCenters <- request:
	default:
		panic("server: trusted observer queue invariant violated")
	}
	return nil
}

// StepForTest 显式推进一个完整 tick，供无头确定性集成测试使用。
func (server *Server) StepForTest() sim.TickResult {
	return server.Step()
}

func (server *Server) Run(ctx context.Context) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			server.Close()
			return ctx.Err()
		case <-server.ctx.Done():
			return nil
		case <-ticker.C:
			server.Step()
		}
	}
}

func (server *Server) Close() {
	server.closeOnce.Do(func() {
		server.cancel()
		server.cancelSaves()
		server.session.close()
		_ = server.endpoint.Close()
		server.workers.Wait()
		server.saveWorkers.Wait()
	})
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

// PlayerState 返回本地玩家权威状态的副本。
func (server *Server) PlayerState() (sim.PlayerUpdate, bool) {
	server.stepMu.Lock()
	defer server.stepMu.Unlock()
	return server.engine.Player(localSessionID)
}

// ChunkHash 返回权威 Ready 区块的逻辑哈希与 revision，不暴露可变指针。
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

func (server *Server) endpointReader() {
	defer server.workers.Done()
	for {
		message, err := server.endpoint.Recv(server.ctx)
		if err != nil {
			if !errors.Is(err, context.Canceled) &&
				!errors.Is(err, network.ErrClosed) {
				slog.Warn("服务端 endpoint reader 退出", "error", err)
			}
			return
		}
		command, ok := translateClientMessage(message)
		if !ok {
			continue
		}
		select {
		case server.incoming <- command:
		case <-server.ctx.Done():
			return
		}
	}
}

func translateClientMessage(message network.ClientMessage) (sim.Command, bool) {
	switch message := message.(type) {
	case network.PlayerInput:
		return sim.Command{
			Session:  localSessionID,
			Sequence: message.Sequence,
			Kind:     sim.CommandPlayerInput,
			MoveX:    message.MoveX,
			MoveZ:    message.MoveZ,
			Jump:     message.Jump,
			Yaw:      message.Yaw,
			Pitch:    message.Pitch,
		}, true
	case network.BreakBlock:
		return sim.Command{
			Session:  localSessionID,
			Sequence: message.Sequence,
			Kind:     sim.CommandBreakBlock,
			Yaw:      message.Yaw,
			Pitch:    message.Pitch,
		}, true
	case network.PlaceBlock:
		return sim.Command{
			Session:  localSessionID,
			Sequence: message.Sequence,
			Kind:     sim.CommandPlaceBlock,
			Yaw:      message.Yaw,
			Pitch:    message.Pitch,
			Block:    message.Block,
		}, true
	case network.RequestChunkResync:
		return sim.Command{
			Session:      localSessionID,
			Sequence:     message.Sequence,
			Kind:         sim.CommandResync,
			Dimension:    message.Dimension,
			Chunk:        message.Chunk,
			HaveRevision: message.HaveRevision,
		}, true
	default:
		return sim.Command{}, false
	}
}

func (server *Server) drainTrustedObserverCenter() (
	trustedObserverCenter,
	uint64,
	bool,
) {
	if server.trustedObserverCenters == nil {
		return trustedObserverCenter{}, 0, false
	}
	select {
	case request := <-server.trustedObserverCenters:
		server.trustedObserverSequence++
		server.session.hasView = true
		server.session.viewDimension = request.dimension
		server.session.viewCenter = request.center
		server.engine.Enqueue(sim.Command{
			Session:   localSessionID,
			Sequence:  server.trustedObserverSequence,
			Kind:      sim.CommandTrustedObserverCenter,
			Dimension: request.dimension,
			Center:    request.center,
		})
		return request, server.trustedObserverSequence, true
	default:
		return trustedObserverCenter{}, 0, false
	}
}

func (server *Server) drainIncoming() {
	var commands []sim.Command
	for {
		select {
		case command := <-server.incoming:
			commands = append(commands, command)
		default:
			for _, command := range commands {
				server.engine.Enqueue(command)
			}
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

func (server *Server) appendChunkRequests(kind chunkJobKind, keys []core.ChunkKey) {
	for _, key := range keys {
		if _, exists := server.queued[key]; exists {
			continue
		}
		server.queued[key] = struct{}{}
		server.pending = append(server.pending, chunkJob{Kind: kind, Key: key})
	}
}

func (server *Server) cancelForgottenPending(
	forgotten map[sim.SessionID][]core.ChunkKey,
) {
	keys := forgotten[localSessionID]
	if len(keys) == 0 || len(server.pending) == 0 {
		return
	}
	remove := make(map[core.ChunkKey]struct{}, len(keys))
	for _, key := range keys {
		remove[key] = struct{}{}
	}
	kept := server.pending[:0]
	for _, job := range server.pending {
		if _, stale := remove[job.Key]; stale {
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
