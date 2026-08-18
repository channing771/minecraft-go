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

// companionStopCommand 是唯一绕过 FIFO 的控制指令文本：精确等于「停止」
// （大小写敏感、无参数）。判定基准与 Accepted 事件的指令字段一致——都使用
// 寻址解析 trim 后的指令文本，因此「停止移动」「stop」等多字文本按普通指令
// 入队，绝不触发旁路。
const companionStopCommand = "停止"

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
	// len(chan) 语言级恒不超过 cap(chan)，而 incomingChats 全部构造点的
	// 缓冲恰为 inputCapacity，本值天然以 inputCapacity 为上界。
	pending := len(server.incomingChats)
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
		// 停止旁路：寻址成功、目标已配置且指令文本 trim 后精确等于「停止」时
		// 绕过 FIFO。成功的停止不在这里产生聊天投递——TaskStopped 广播由本
		// tick 稍后的任务编排（advanceCompanionTasks）从事件事实组装并复用
		// 同一 EventID 计数器，因此这里不消耗编号；不可停止（非持续跟随或
		// 空闲）以 NotFollowing 只回发令者，携带完整伙伴身份与指令。未知
		// 目标的「停止」不在此拦截，落入下方分支按 UnknownCompanion 拒绝。
		if reason == network.ChatRejectNone && command == companionStopCommand &&
			server.companionManager != nil {
			if definition, ok := server.companionsByName[name]; ok {
				if server.companionManager.stopCompanion(definition) {
					continue
				}
				server.nextChatEventID++
				notFollowing := network.ChatEvent{
					EventID:       server.nextChatEventID,
					PlayerID:      current.playerID,
					PlayerName:    current.displayName,
					CompanionID:   definition.ID,
					CompanionName: definition.Name,
					Kind:          network.ChatEventRejected,
					RejectReason:  network.ChatRejectNotFollowing,
					Command:       command,
				}
				if err := notFollowing.Validate(); err != nil {
					server.closePublicationSessionLocked(
						current,
						fmt.Errorf("server: validate chat event %d: %w", notFollowing.EventID, err),
					)
					continue
				}
				deliveries = append(deliveries,
					chatDelivery{event: notFollowing, recipient: chat.sessionID})
				continue
			}
		}
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
			// 寻址成功（停止旁路已在前置分支拦截）即尝试进入该伙伴的任务
			// FIFO：满员同步拒绝且只回发令者，绝不发起模型请求，也绝不影响
			// 既有队列内容。发令者事实在同一 tick 边界冻结，指令的规划输入
			// 不随其后续移动漂移。
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
