package client

import (
	"errors"
	"fmt"

	"github.com/channing771/mornlea/internal/network"
)

const chatEventCapacity = 32

var ErrChatEventProtocol = errors.New("chat event protocol error")

type ChatEvents struct {
	values [chatEventCapacity]network.ChatEvent
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
	if events.count < chatEventCapacity {
		events.values[(events.start+events.count)%chatEventCapacity] = event
		events.count++
	} else {
		events.values[events.start] = event
		events.start = (events.start + 1) % chatEventCapacity
	}
	events.lastID = event.EventID
	return nil
}

func (events *ChatEvents) Events(dst []network.ChatEvent) []network.ChatEvent {
	for index := range events.count {
		dst = append(dst, events.values[(events.start+index)%chatEventCapacity])
	}
	return dst
}

func (events *ChatEvents) Reset() {
	*events = ChatEvents{}
}
