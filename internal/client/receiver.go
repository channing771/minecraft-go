package client

import (
	"context"
	"errors"
	"sync"

	"minecraft-go/internal/network"
)

var errReceiverInboxFull = errors.New("client: server message consumer is too slow")

// Receiver moves blocking endpoint reads into a bounded queue for frame consumers.
type Receiver struct {
	endpoint  network.ClientEndpoint
	inbox     chan network.ServerMessage
	done      chan struct{}
	closeOnce sync.Once
	mu        sync.Mutex
	err       error
	closing   bool
	closeErr  error
}

func NewReceiver(endpoint network.ClientEndpoint, capacity int) *Receiver {
	if endpoint == nil {
		panic("client: nil receiver endpoint")
	}
	if capacity < 1 {
		panic("client: receiver capacity must be positive")
	}
	receiver := &Receiver{
		endpoint: endpoint,
		inbox:    make(chan network.ServerMessage, capacity),
		done:     make(chan struct{}),
	}
	go receiver.read()
	return receiver
}

func (receiver *Receiver) read() {
	defer close(receiver.done)
	for {
		message, err := receiver.endpoint.Recv(context.Background())
		if err != nil {
			receiver.mu.Lock()
			if !receiver.closing && receiver.err == nil {
				receiver.err = err
			}
			receiver.mu.Unlock()
			return
		}
		select {
		case receiver.inbox <- message:
		default:
			receiver.mu.Lock()
			if receiver.err == nil {
				receiver.err = errReceiverInboxFull
			}
			receiver.mu.Unlock()
			receiver.closeEndpoint()
			return
		}
	}
}

func (receiver *Receiver) TryRecv() (network.ServerMessage, bool) {
	select {
	case message := <-receiver.inbox:
		return message, true
	default:
		return nil, false
	}
}

func (receiver *Receiver) Err() error {
	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	return receiver.err
}

func (receiver *Receiver) Close() error {
	receiver.mu.Lock()
	receiver.closing = true
	receiver.mu.Unlock()
	receiver.closeEndpoint()
	<-receiver.done
	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	return receiver.closeErr
}

func (receiver *Receiver) closeEndpoint() {
	receiver.closeOnce.Do(func() {
		err := receiver.endpoint.Close()
		receiver.mu.Lock()
		receiver.closeErr = err
		receiver.mu.Unlock()
	})
}
