package network

import (
	"context"
	"sync"
)

type memoryPair struct {
	clientToServer chan ClientPacket
	serverToClient chan ServerPacket
	done           chan struct{}

	closeOnce sync.Once
	sendMu    sync.Mutex
	closing   bool
	senders   sync.WaitGroup
}

type memoryClientStream struct {
	pair *memoryPair
}

type memoryServerStream struct {
	pair *memoryPair
	peer string
}

// NewMemoryStreamPair 创建一对共享关闭状态的有界 packet stream。
func NewMemoryStreamPair(capacity int) (ClientPacketStream, ServerPacketStream) {
	if capacity < 1 {
		panic("network: memory transport capacity must be positive")
	}
	pair := &memoryPair{
		clientToServer: make(chan ClientPacket, capacity),
		serverToClient: make(chan ServerPacket, capacity),
		done:           make(chan struct{}),
	}
	return &memoryClientStream{pair: pair}, &memoryServerStream{pair: pair, peer: "memory"}
}

// NewMemoryPair 将内存 packet stream 包装成已附着的 Play endpoint。
func NewMemoryPair(capacity int) (ClientEndpoint, ServerEndpoint) {
	client, server := NewMemoryStreamPair(capacity)
	return newClientPlayEndpoint(client), newServerPlayEndpoint(server)
}

func (stream *memoryClientStream) Send(ctx context.Context, state State, packet ClientPacket) error {
	if err := ValidateClientPacket(state, packet); err != nil {
		return err
	}
	return memorySend(ctx, stream.pair, stream.pair.clientToServer, packet)
}

func (stream *memoryClientStream) Recv(ctx context.Context, state State) (ServerPacket, error) {
	packet, err := memoryReceive(ctx, stream.pair, stream.pair.serverToClient)
	if err != nil {
		return nil, err
	}
	if err := ValidateServerPacket(state, packet); err != nil {
		stream.pair.close()
		return nil, protocolViolation(err)
	}
	return packet, nil
}

func (stream *memoryClientStream) Close() error {
	stream.pair.close()
	return nil
}

func (stream *memoryServerStream) Send(ctx context.Context, state State, packet ServerPacket) error {
	if err := ValidateServerPacket(state, packet); err != nil {
		return err
	}
	return memorySend(ctx, stream.pair, stream.pair.serverToClient, packet)
}

func (stream *memoryServerStream) Recv(ctx context.Context, state State) (ClientPacket, error) {
	packet, err := memoryReceive(ctx, stream.pair, stream.pair.clientToServer)
	if err != nil {
		return nil, err
	}
	if err := validateDecodedClientWirePacket(state, packet); err != nil {
		stream.pair.close()
		return nil, protocolViolation(err)
	}
	return packet, nil
}

func (stream *memoryServerStream) Peer() string {
	return stream.peer
}

func (stream *memoryServerStream) Close() error {
	stream.pair.close()
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

var _ ClientPacketStream = (*memoryClientStream)(nil)
var _ ServerPacketStream = (*memoryServerStream)(nil)
