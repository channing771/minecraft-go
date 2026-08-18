package client

import (
	"errors"
	"fmt"

	"github.com/channing771/mornlea/internal/network"
)

// ChatEventCapacity 是客户端事件环保留的最近聊天事件容量。32 的出处：
// openspec companion-client-presentation 规格——客户端 MUST 保存最近 32 条
// 严格递增 EventID 的 ChatEvent。事件环按该容量环形覆盖最旧事件；
// cmd/mornlea 的 chatEventBuffer 复用缓冲也按同一常量分配（E9 同源化），
// 保证 Events 回放始终零扩容，两侧容量不可能各自漂移。
const ChatEventCapacity = 32

var ErrChatEventProtocol = errors.New("chat event protocol error")

type ChatEvents struct {
	values [ChatEventCapacity]network.ChatEvent
	start  int
	count  int
	lastID uint64
}

func (events *ChatEvents) Apply(event network.ChatEvent) error {
	if err := event.Validate(); err != nil {
		return fmt.Errorf("%w: ChatEvent: %v", ErrChatEventProtocol, err)
	}
	if events.count > 0 && event.EventID <= events.lastID {
		return fmt.Errorf(
			"%w: ChatEvent ID %d is not newer than %d",
			ErrChatEventProtocol, event.EventID, events.lastID,
		)
	}
	if events.count < ChatEventCapacity {
		events.values[(events.start+events.count)%ChatEventCapacity] = event
		events.count++
	} else {
		events.values[events.start] = event
		events.start = (events.start + 1) % ChatEventCapacity
	}
	events.lastID = event.EventID
	return nil
}

func (events *ChatEvents) Events(dst []network.ChatEvent) []network.ChatEvent {
	for index := range events.count {
		dst = append(dst, events.values[(events.start+index)%ChatEventCapacity])
	}
	return dst
}

func (events *ChatEvents) Reset() {
	*events = ChatEvents{}
}
