package network

import (
	"context"
	"sync"
)

type memoryPair struct {
	clientToServer chan ClientMessage
	serverToClient chan ServerMessage
	done           chan struct{}

	closeOnce sync.Once
	sendMu    sync.Mutex
	closing   bool
	senders   sync.WaitGroup
}

type memoryClientEndpoint struct {
	pair *memoryPair
}

type memoryServerEndpoint struct {
	pair *memoryPair
}

// NewMemoryPair 创建一对共享关闭状态的有界内存端点。
func NewMemoryPair(capacity int) (ClientEndpoint, ServerEndpoint) {
	if capacity < 1 {
		panic("network: memory transport capacity must be positive")
	}
	pair := &memoryPair{
		clientToServer: make(chan ClientMessage, capacity),
		serverToClient: make(chan ServerMessage, capacity),
		done:           make(chan struct{}),
	}
	return &memoryClientEndpoint{pair: pair}, &memoryServerEndpoint{pair: pair}
}

func (endpoint *memoryClientEndpoint) Send(
	ctx context.Context,
	message ClientMessage,
) error {
	return memorySend(ctx, endpoint.pair, endpoint.pair.clientToServer, message)
}

func (endpoint *memoryClientEndpoint) Recv(
	ctx context.Context,
) (ServerMessage, error) {
	return memoryReceive(ctx, endpoint.pair, endpoint.pair.serverToClient)
}

func (endpoint *memoryClientEndpoint) Close() error {
	endpoint.pair.close()
	return nil
}

func (endpoint *memoryServerEndpoint) Send(
	ctx context.Context,
	message ServerMessage,
) error {
	return memorySend(ctx, endpoint.pair, endpoint.pair.serverToClient, message)
}

func (endpoint *memoryServerEndpoint) Recv(
	ctx context.Context,
) (ClientMessage, error) {
	return memoryReceive(ctx, endpoint.pair, endpoint.pair.clientToServer)
}

func (endpoint *memoryServerEndpoint) Close() error {
	endpoint.pair.close()
	return nil
}

func (pair *memoryPair) beginSend() bool {
	pair.sendMu.Lock()
	defer pair.sendMu.Unlock()
	if pair.closing {
		return false
	}
	pair.senders.Add(1)
	return true
}

func (pair *memoryPair) close() {
	pair.closeOnce.Do(func() {
		pair.sendMu.Lock()
		pair.closing = true
		close(pair.done)
		pair.sendMu.Unlock()
		pair.senders.Wait()
	})
}

func memorySend[T any](
	ctx context.Context,
	pair *memoryPair,
	channel chan<- T,
	message T,
) error {
	if !pair.beginSend() {
		return ErrClosed
	}
	defer pair.senders.Done()

	select {
	case channel <- message:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-pair.done:
		return ErrClosed
	}
}

func memoryReceive[T any](
	ctx context.Context,
	pair *memoryPair,
	channel <-chan T,
) (T, error) {
	var zero T

	select {
	case message := <-channel:
		return message, nil
	default:
	}

	select {
	case message := <-channel:
		return message, nil
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-pair.done:
		pair.senders.Wait()
		select {
		case message := <-channel:
			return message, nil
		default:
			return zero, ErrClosed
		}
	}
}

var _ ClientEndpoint = (*memoryClientEndpoint)(nil)
var _ ServerEndpoint = (*memoryServerEndpoint)(nil)
