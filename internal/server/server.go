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

const (
	localSessionID sim.SessionID = 1
	inputCapacity                = 256
)

type Server struct {
	config    Config
	endpoint  network.ServerEndpoint
	generator Generator
	engine    *sim.Engine

	ctx    context.Context
	cancel context.CancelFunc

	incoming  chan sim.Command
	jobs      chan core.ChunkKey
	generated chan sim.GeneratedChunk
	pending   []core.ChunkKey
	queued    map[core.ChunkKey]struct{}

	workers   sync.WaitGroup
	stepMu    sync.Mutex
	closeOnce sync.Once
}

func New(
	config Config,
	endpoint network.ServerEndpoint,
	generator Generator,
) *Server {
	config.validate()
	if endpoint == nil {
		panic("server: nil endpoint")
	}
	if generator == nil {
		panic("server: nil generator")
	}
	ctx, cancel := context.WithCancel(context.Background())
	queueCapacity := max(1, config.Workers*2)
	server := &Server{
		config:    config,
		endpoint:  endpoint,
		generator: generator,
		engine:    sim.NewEngine(generator.BaseBlockAt, config.ViewRadius),
		ctx:       ctx,
		cancel:    cancel,
		incoming:  make(chan sim.Command, inputCapacity),
		jobs:      make(chan core.ChunkKey, queueCapacity),
		generated: make(chan sim.GeneratedChunk, queueCapacity),
		queued:    make(map[core.ChunkKey]struct{}),
	}

	server.workers.Add(1)
	go server.endpointReader()
	server.workers.Add(config.Workers)
	for range config.Workers {
		go server.generationWorker()
	}
	return server
}

// Step 执行一次服务端编排与权威模拟 tick。
func (server *Server) Step() sim.TickResult {
	server.stepMu.Lock()
	defer server.stepMu.Unlock()

	server.drainIncoming()
	server.drainGenerated()
	result := server.engine.Step()
	server.cancelForgottenPending(result.Forget)
	server.appendGenerationRequests(result.Generate)
	server.scheduleGeneration()
	return result
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
		_ = server.endpoint.Close()
		server.workers.Wait()
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
	case network.SetViewCenter:
		return sim.Command{
			Session:   localSessionID,
			Sequence:  message.Sequence,
			Kind:      sim.CommandSetViewCenter,
			Dimension: message.Dimension,
			Center:    message.Center,
		}, true
	case network.BreakRay:
		return sim.Command{
			Session:   localSessionID,
			Sequence:  message.Sequence,
			Kind:      sim.CommandBreakRay,
			Dimension: message.Dimension,
			Origin:    message.Origin,
			Direction: message.Direction,
		}, true
	case network.PlaceRay:
		return sim.Command{
			Session:   localSessionID,
			Sequence:  message.Sequence,
			Kind:      sim.CommandPlaceRay,
			Dimension: message.Dimension,
			Origin:    message.Origin,
			Direction: message.Direction,
			Block:     message.Block,
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

func (server *Server) drainIncoming() {
	var commands []sim.Command
	var latestView sim.Command
	hasView := false
	for {
		select {
		case command := <-server.incoming:
			if command.Kind == sim.CommandSetViewCenter {
				if !hasView || command.Sequence > latestView.Sequence {
					latestView = command
					hasView = true
				}
				continue
			}
			commands = append(commands, command)
		default:
			if hasView {
				commands = append(commands, latestView)
			}
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

func (server *Server) appendGenerationRequests(keys []core.ChunkKey) {
	for _, key := range keys {
		if _, exists := server.queued[key]; exists {
			continue
		}
		server.queued[key] = struct{}{}
		server.pending = append(server.pending, key)
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
	for _, key := range server.pending {
		if _, stale := remove[key]; stale {
			delete(server.queued, key)
			continue
		}
		kept = append(kept, key)
	}
	server.pending = kept
}

func (server *Server) scheduleGeneration() {
	for len(server.pending) != 0 {
		key := server.pending[0]
		select {
		case server.jobs <- key:
			server.pending = server.pending[1:]
			delete(server.queued, key)
		default:
			return
		}
	}
}
