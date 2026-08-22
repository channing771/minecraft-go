package server

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/sim"
)

const inputCapacity = 256

type incomingCommand struct {
	Session    sim.SessionID
	Generation uint64
	Command    sim.Command
}

type inputIngressBoundary struct {
	sequence uint64
	want     int
	done     chan struct{}

	mu       sync.Mutex
	sessions map[sim.SessionID]struct{}
	closed   bool
}

func newInputIngressBoundary(sequence uint64, want int) *inputIngressBoundary {
	return &inputIngressBoundary{
		sequence: sequence,
		want:     want,
		done:     make(chan struct{}),
		sessions: make(map[sim.SessionID]struct{}, want),
	}
}

func (boundary *inputIngressBoundary) observe(command incomingCommand) {
	if command.Command.Kind != sim.CommandPlayerInput ||
		command.Command.Sequence != boundary.sequence {
		return
	}
	boundary.mu.Lock()
	defer boundary.mu.Unlock()
	if boundary.closed {
		return
	}
	boundary.sessions[command.Session] = struct{}{}
	if len(boundary.sessions) == boundary.want {
		boundary.closed = true
		close(boundary.done)
	}
}

type trustedObserverCenter struct {
	dimension core.DimensionID
	center    core.ChunkPos
}

type appliedTrustedObserverCenter struct {
	dimension core.DimensionID
	center    core.ChunkPos
	sequence  uint64
}

func (server *Server) endpointReader(current *session) {
	defer server.workers.Done()
	for {
		message, err := current.endpoint.Recv(current.ctx)
		if err != nil {
			if !errors.Is(err, context.Canceled) &&
				!errors.Is(err, network.ErrClosed) {
				slog.Warn(
					"服务端 endpoint reader 退出",
					"session", current.id,
					"error", err,
				)
			}
			current.fail(err)
			return
		}
		if reply, ok := message.(network.KeepAliveReply); ok {
			if !current.acceptHeartbeatReply(reply.Token) {
				current.fail(errInvalidHeartbeatReply)
				return
			}
			continue
		}
		if command, ok := message.(network.ChatCommand); ok {
			server.enqueueIncomingChat(current.ctx, incomingChat{
				sessionID:  current.id,
				generation: current.generation,
				command:    command,
			})
			continue
		}
		command, ok := translateClientMessage(current.id, message)
		if !ok {
			current.fail(errUnknownClientMessage)
			return
		}
		server.enqueueIncoming(current.ctx, incomingCommand{
			Session:    current.id,
			Generation: current.generation,
			Command:    command,
		})
	}
}

func translateClientMessage(
	id sim.SessionID,
	message network.ClientMessage,
) (sim.Command, bool) {
	switch message := message.(type) {
	case network.PlayerInput:
		return sim.Command{
			Session:  id,
			Sequence: message.Sequence,
			Kind:     sim.CommandPlayerInput,
			MoveX:    message.MoveX,
			MoveZ:    message.MoveZ,
			Jump:     message.Jump,
			Yaw:      message.Yaw,
			Pitch:    message.Pitch,
			Mining:   message.Mining,
			Eating:   message.Eating,
		}, true
	case network.PlaceBlock:
		return sim.Command{
			Session:  id,
			Sequence: message.Sequence,
			Kind:     sim.CommandPlaceBlock,
			Yaw:      message.Yaw,
			Pitch:    message.Pitch,
			Slot:     message.Slot,
		}, true
	case network.SelectHotbar:
		return sim.Command{
			Session:  id,
			Sequence: message.Sequence,
			Kind:     sim.CommandSelectHotbar,
			Slot:     message.Slot,
		}, true
	case network.OpenContainer:
		return sim.Command{
			Session:  id,
			Sequence: message.Sequence,
			Kind:     sim.CommandOpenFurnace,
			Yaw:      message.Yaw,
			Pitch:    message.Pitch,
		}, true
	case network.MoveContainerStack:
		return sim.Command{
			Session:  id,
			Sequence: message.Sequence,
			Kind:     sim.CommandMoveFurnaceStack,
			Furnace:  message.Container,
			Slot:     message.From,
			ToSlot:   message.To,
		}, true
	case network.TillSoil:
		// 与 OpenContainer 同形：只搬运序号与朝向，目标与栏位都由 sim 从权威
		// 状态取得，server 不做第二次校验。
		return sim.Command{
			Session:  id,
			Sequence: message.Sequence,
			Kind:     sim.CommandTillSoil,
			Yaw:      message.Yaw,
			Pitch:    message.Pitch,
		}, true
	case network.CloseContainer:
		return sim.Command{
			Session:  id,
			Sequence: message.Sequence,
			Kind:     sim.CommandCloseFurnace,
		}, true
	case network.DropSelectedItem:
		// 只搬运序号：栏位与位置都由 sim 从权威状态取得，server 不做第二次校验。
		return sim.Command{
			Session:  id,
			Sequence: message.Sequence,
			Kind:     sim.CommandDropSelectedItem,
		}, true
	case network.CraftRecipe:
		return sim.Command{
			Session:  id,
			Sequence: message.Sequence,
			Kind:     sim.CommandCraftRecipe,
			Recipe:   message.Recipe,
		}, true
	case network.MoveInventoryStack:
		return sim.Command{
			Session:  id,
			Sequence: message.Sequence,
			Kind:     sim.CommandMoveInventoryStack,
			Slot:     message.From,
			ToSlot:   message.To,
		}, true
	case network.RequestChunkResync:
		return sim.Command{
			Session:      id,
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

// drainTrustedObserverCenter runs during Step while server.stepMu is held.
func (server *Server) drainTrustedObserverCenter() (
	trustedObserverCenter,
	uint64,
	bool,
) {
	if server.trustedObserverCenters == nil || server.trustedObserver == nil {
		return trustedObserverCenter{}, 0, false
	}
	select {
	case request := <-server.trustedObserverCenters:
		server.trustedObserverSequence++
		server.trustedObserver.hasView = true
		server.trustedObserver.viewDimension = request.dimension
		server.trustedObserver.viewCenter = request.center
		server.engine.Enqueue(sim.Command{
			Session:   trustedObserverSessionID,
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

func (server *Server) enqueueIncoming(
	sessionCtx context.Context,
	command incomingCommand,
) {
	select {
	case server.incoming <- command:
		if boundary := server.inputBoundary.Load(); boundary != nil {
			boundary.observe(command)
		}
	case <-sessionCtx.Done():
	case <-server.ctx.Done():
	}
}

// drainIncoming runs during Step while server.stepMu is held.
func (server *Server) drainIncoming() {
	var commands []sim.Command
	for {
		select {
		case incoming := <-server.incoming:
			current := server.sessions[incoming.Session]
			if current == nil || current.generation != incoming.Generation {
				continue
			}
			incoming.Command.Session = incoming.Session
			commands = append(commands, incoming.Command)
		default:
			for _, command := range commands {
				server.engine.Enqueue(command)
			}
			return
		}
	}
}
