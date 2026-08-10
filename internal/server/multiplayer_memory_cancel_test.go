package server

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"minecraft-go/internal/network"
)

type delayedTask16AcceptListener struct {
	stream    network.ServerPacketStream
	returned  chan struct{}
	release   chan struct{}
	closeOnce sync.Once
}

func (listener *delayedTask16AcceptListener) Accept(context.Context) (network.ServerPacketStream, error) {
	close(listener.returned)
	<-listener.release
	return listener.stream, nil
}

func (*delayedTask16AcceptListener) Addr() string { return "delayed-task16" }

func (listener *delayedTask16AcceptListener) Close() error {
	listener.closeOnce.Do(func() { close(listener.release) })
	return nil
}

type multiplayerPacketAcceptResult struct {
	stream network.ServerPacketStream
	err    error
}

type multiplayerPacketAcceptWorker struct {
	cancel context.CancelFunc
	result <-chan multiplayerPacketAcceptResult
	done   <-chan struct{}
}

func startMultiplayerPacketAccept(
	ctx context.Context,
	listener network.Listener,
) multiplayerPacketAcceptWorker {
	acceptCtx, cancel := context.WithCancel(ctx)
	results := make(chan multiplayerPacketAcceptResult, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		stream, err := listener.Accept(acceptCtx)
		results <- multiplayerPacketAcceptResult{stream: stream, err: err}
	}()
	return multiplayerPacketAcceptWorker{cancel: cancel, result: results, done: done}
}

func awaitMultiplayerPacketAccept(
	ctx context.Context,
	listener network.Listener,
	worker multiplayerPacketAcceptWorker,
) (network.ServerPacketStream, error) {
	select {
	case result := <-worker.result:
		worker.cancel()
		if err := waitMultiplayerPacketAcceptWorker(worker.done); err != nil {
			if result.stream != nil {
				_ = result.stream.Close()
			}
			return nil, errors.Join(result.err, err)
		}
		return result.stream, result.err
	case <-ctx.Done():
		return nil, stopMultiplayerPacketAccept(listener, worker, ctx.Err())
	}
}

func stopMultiplayerPacketAccept(
	listener network.Listener,
	worker multiplayerPacketAcceptWorker,
	cause error,
) error {
	worker.cancel()
	closeErr := listener.Close()
	joinCtx, joinCancel := context.WithTimeout(context.Background(), waitDeadline)
	defer joinCancel()
	var result multiplayerPacketAcceptResult
	select {
	case result = <-worker.result:
	case <-joinCtx.Done():
		return errors.Join(cause, closeErr, fmt.Errorf("accept result join: %w", joinCtx.Err()))
	}
	if result.stream != nil {
		closeErr = errors.Join(closeErr, result.stream.Close())
	}
	select {
	case <-worker.done:
	case <-joinCtx.Done():
		return errors.Join(cause, closeErr, result.err, fmt.Errorf("accept worker join: %w", joinCtx.Err()))
	}
	if errors.Is(result.err, network.ErrClosed) || errors.Is(result.err, context.Canceled) {
		result.err = nil
	}
	return errors.Join(cause, closeErr, result.err)
}

func waitMultiplayerPacketAcceptWorker(done <-chan struct{}) error {
	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
	defer cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("accept worker join: %w", ctx.Err())
	}
}

func TestCanceledMultiplayerPacketAcceptClosesLateStreamAndJoinsWorker(t *testing.T) {
	clientStream, serverStream := network.NewMemoryStreamPair(1)
	t.Cleanup(func() { _ = clientStream.Close() })
	listener := &delayedTask16AcceptListener{
		stream: serverStream, returned: make(chan struct{}), release: make(chan struct{}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	worker := startMultiplayerPacketAccept(ctx, listener)
	<-listener.returned
	cancel()

	accepted, err := awaitMultiplayerPacketAccept(ctx, listener, worker)
	if accepted != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled accept=(%T, %v), want (nil, context.Canceled)", accepted, err)
	}
	select {
	case <-worker.done:
	default:
		t.Fatal("canceled accept worker was not joined before return")
	}
	if err := clientStream.Send(context.Background(), network.StatePlay, network.PlayerInput{}); !errors.Is(err, network.ErrClosed) {
		t.Fatalf("late accepted stream remained live: Send=%v, want network.ErrClosed", err)
	}
}
