package client

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/network"
)

func TestReceiverTryRecvDoesNotBlockWhileEndpointRecvBlocks(t *testing.T) {
	endpoint := newReceiverTestEndpoint()
	receiver := NewReceiver(endpoint, 1)
	t.Cleanup(func() { _ = receiver.Close() })

	result := make(chan struct{})
	go func() {
		_, ok := receiver.TryRecv()
		if ok {
			t.Error("TryRecv returned a message while endpoint Recv is blocked")
		}
		close(result)
	}()
	select {
	case <-result:
	case <-time.After(time.Second):
		t.Fatal("TryRecv blocked on endpoint Recv")
	}
}

func TestReceiverDeliversMessagesInReceiveOrder(t *testing.T) {
	endpoint := newReceiverTestEndpoint()
	receiver := NewReceiver(endpoint, 2)
	t.Cleanup(func() { _ = receiver.Close() })
	first := network.PlayerState{ServerTick: 1}
	second := network.PlayerState{ServerTick: 2}
	endpoint.deliver(first)
	endpoint.deliver(second)

	for _, want := range []network.ServerMessage{first, second} {
		var got network.ServerMessage
		var ok bool
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if got, ok = receiver.TryRecv(); ok {
				break
			}
			time.Sleep(time.Millisecond)
		}
		if !ok || got != want {
			t.Fatalf("TryRecv = (%#v, %v), want (%#v, true)", got, ok, want)
		}
	}
}

func TestReceiverClosesSlowConsumerWhenInboxIsFull(t *testing.T) {
	endpoint := newReceiverTestEndpoint()
	receiver := NewReceiver(endpoint, 1)
	t.Cleanup(func() { _ = receiver.Close() })
	endpoint.deliver(network.PlayerState{ServerTick: 1})
	endpoint.deliver(network.PlayerState{ServerTick: 2})

	deadline := time.Now().Add(time.Second)
	for receiver.Err() == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := receiver.Err(); err == nil {
		t.Fatal("receiver did not report a full inbox")
	}
	select {
	case <-receiver.done:
	case <-time.After(time.Second):
		t.Fatal("receiver did not finish after inbox overflow")
	}
	if !endpoint.isClosed() {
		t.Fatal("receiver did not close endpoint after inbox overflow")
	}
}

func TestReceiverCloseWakesBlockedReader(t *testing.T) {
	endpoint := newReceiverTestEndpoint()
	receiver := NewReceiver(endpoint, 1)
	if err := receiver.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !endpoint.isClosed() {
		t.Fatal("Close did not close endpoint")
	}
}

type receiverTestEndpoint struct {
	messages chan network.ServerMessage
	done     chan struct{}
	once     sync.Once
}

func newReceiverTestEndpoint() *receiverTestEndpoint {
	return &receiverTestEndpoint{
		messages: make(chan network.ServerMessage),
		done:     make(chan struct{}),
	}
}

func (endpoint *receiverTestEndpoint) Send(context.Context, network.ClientMessage) error {
	return nil
}

func (endpoint *receiverTestEndpoint) Recv(context.Context) (network.ServerMessage, error) {
	select {
	case message := <-endpoint.messages:
		return message, nil
	case <-endpoint.done:
		return nil, network.ErrClosed
	}
}

func (endpoint *receiverTestEndpoint) Close() error {
	endpoint.once.Do(func() { close(endpoint.done) })
	return nil
}

func (endpoint *receiverTestEndpoint) deliver(message network.ServerMessage) {
	select {
	case endpoint.messages <- message:
	case <-endpoint.done:
		panic("deliver to closed endpoint")
	}
}

func (endpoint *receiverTestEndpoint) isClosed() bool {
	select {
	case <-endpoint.done:
		return true
	default:
		return false
	}
}

var _ network.ClientEndpoint = (*receiverTestEndpoint)(nil)
var _ = errors.Is
