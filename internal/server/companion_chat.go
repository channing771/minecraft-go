package server

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/sim"
)

type incomingChat struct {
	sessionID  sim.SessionID
	generation uint64
	command    network.ChatCommand
}

type chatDelivery struct {
	event     network.ChatEvent
	recipient sim.SessionID
}

func parseCompanionAddress(text string) (string, string, network.ChatRejectReason) {
	if err := (network.ChatCommand{Text: text}).Validate(); err != nil ||
		!strings.HasPrefix(text, "@") {
		return "", "", network.ChatRejectInvalidFormat
	}
	remainder := text[1:]
	separator := strings.IndexFunc(remainder, unicode.IsSpace)
	if separator <= 0 {
		return "", "", network.ChatRejectInvalidFormat
	}
	name := remainder[:separator]
	command := strings.TrimSpace(remainder[separator:])
	if command == "" || companion.ValidateName(name) != nil ||
		(network.ChatCommand{Text: command}).Validate() != nil {
		return "", "", network.ChatRejectInvalidFormat
	}
	return name, command, network.ChatRejectNone
}

func (server *Server) enqueueIncomingChat(sessionCtx context.Context, chat incomingChat) {
	select {
	case server.incomingChats <- chat:
	case <-sessionCtx.Done():
	case <-server.ctx.Done():
	}
}

// drainIncomingChats 在持有 stepMu 的 tick 边界调用。
func (server *Server) drainIncomingChats() []chatDelivery {
	pending := len(server.incomingChats)
	if pending > inputCapacity {
		pending = inputCapacity
	}
	deliveries := make([]chatDelivery, 0, pending)
	for range pending {
		chat := <-server.incomingChats
		current := server.sessions[chat.sessionID]
		if current == nil || current.generation != chat.generation || current.closed() {
			continue
		}
		if server.nextChatEventID == ^uint64(0) {
			server.closePublicationSessionLocked(
				current,
				fmt.Errorf("server: chat event ID exhausted"),
			)
			continue
		}

		name, command, reason := parseCompanionAddress(chat.command.Text)
		server.nextChatEventID++
		event := network.ChatEvent{
			EventID:    server.nextChatEventID,
			PlayerID:   current.playerID,
			PlayerName: current.displayName,
		}
		recipient := chat.sessionID
		if reason != network.ChatRejectNone {
			event.Kind = network.ChatEventRejected
			event.RejectReason = network.ChatRejectInvalidFormat
		} else if definition, ok := server.companionsByName[name]; ok {
			event.CompanionID = definition.ID
			event.CompanionName = definition.Name
			event.Command = command
			// 寻址成功即尝试进入该伙伴的任务 FIFO：满员同步拒绝且只回发令者，
			// 绝不发起模型请求，也绝不影响既有队列内容。发令者事实在同一
			// tick 边界冻结，指令的规划输入不随其后续移动漂移。
			if server.companionManager != nil && !server.companionManager.enqueueCommand(
				definition,
				companion.TaskCommand(command),
				server.companionManager.captureIssuer(
					current.playerID, current.displayName, chat.sessionID,
				),
			) {
				event.Kind = network.ChatEventRejected
				event.RejectReason = network.ChatRejectQueueFull
			} else {
				event.Kind = network.ChatEventAccepted
				recipient = 0
			}
		} else {
			event.CompanionName = name
			event.Kind = network.ChatEventRejected
			event.RejectReason = network.ChatRejectUnknownCompanion
		}
		if err := event.Validate(); err != nil {
			server.closePublicationSessionLocked(
				current,
				fmt.Errorf("server: validate chat event %d: %w", event.EventID, err),
			)
			continue
		}
		deliveries = append(deliveries, chatDelivery{event: event, recipient: recipient})
	}
	return deliveries
}
