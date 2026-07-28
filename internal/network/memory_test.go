package network_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"minecraft-go/internal/network"
)

func TestMemoryTransportPreservesOrderBothDirections(t *testing.T) {
	client, server := network.NewMemoryPair(100)
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	for sequence := uint64(1); sequence <= 100; sequence++ {
		if err := client.Send(ctx, network.PlayerInput{Sequence: sequence}); err != nil {
			t.Fatal(err)
		}
		if err := server.Send(ctx, network.CommandRejected{
			Sequence: sequence,
			Reason:   network.RejectNoTarget,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for sequence := uint64(1); sequence <= 100; sequence++ {
		clientMessage, err := server.Recv(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got := clientMessage.(network.PlayerInput).Sequence; got != sequence {
			t.Fatalf("client→server sequence = %d，想要 %d", got, sequence)
		}

		serverMessage, err := client.Recv(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got := serverMessage.(network.CommandRejected).Sequence; got != sequence {
			t.Fatalf("server→client sequence = %d，想要 %d", got, sequence)
		}
	}
}

func TestMemoryTransportAppliesCapacityBackpressure(t *testing.T) {
	client, server := network.NewMemoryPair(1)
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := client.Send(ctx, network.PlayerInput{Sequence: 1}); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		close(started)
		result <- client.Send(ctx, network.PlayerInput{Sequence: 2})
	}()
	<-started
	select {
	case err := <-result:
		t.Fatalf("第二次 Send 未背压，返回 %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	first, err := server.Recv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.(network.PlayerInput).Sequence != 1 {
		t.Fatal("先收到的不是 sequence 1")
	}
	if err := <-result; err != nil {
		t.Fatalf("解除背压后的 Send: %v", err)
	}
	second, err := server.Recv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second.(network.PlayerInput).Sequence != 2 {
		t.Fatal("后收到的不是 sequence 2")
	}
}

func TestMemoryTransportCancellationReleasesBlockedSend(t *testing.T) {
	client, _ := network.NewMemoryPair(1)
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Send(
		context.Background(),
		network.PlayerInput{Sequence: 1},
	); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		close(started)
		result <- client.Send(ctx, network.PlayerInput{Sequence: 2})
	}()
	<-started
	select {
	case err := <-result:
		t.Fatalf("Send 未阻塞，返回 %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("取消后的 Send = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("取消没有释放 Send")
	}
}

func TestMemoryTransportEitherCloseWakesPeerReceive(t *testing.T) {
	tests := []struct {
		name string
		run  func(context.Context, network.ClientEndpoint, network.ServerEndpoint) error
	}{
		{
			name: "client closes server receive",
			run: func(
				ctx context.Context,
				client network.ClientEndpoint,
				server network.ServerEndpoint,
			) error {
				if err := client.Close(); err != nil {
					return err
				}
				_, err := server.Recv(ctx)
				return err
			},
		},
		{
			name: "server closes client receive",
			run: func(
				ctx context.Context,
				client network.ClientEndpoint,
				server network.ServerEndpoint,
			) error {
				if err := server.Close(); err != nil {
					return err
				}
				_, err := client.Recv(ctx)
				return err
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, server := network.NewMemoryPair(1)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := tc.run(ctx, client, server); !errors.Is(err, network.ErrClosed) {
				t.Fatalf("Recv after peer close = %v", err)
			}
		})
	}
}

func TestMemoryTransportCloseWakesBlockedSend(t *testing.T) {
	client, server := network.NewMemoryPair(1)
	if err := client.Send(
		context.Background(),
		network.PlayerInput{Sequence: 1},
	); err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		close(started)
		result <- client.Send(
			context.Background(),
			network.PlayerInput{Sequence: 2},
		)
	}()
	<-started
	select {
	case err := <-result:
		t.Fatalf("Send 未阻塞，返回 %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, network.ErrClosed) {
			t.Fatalf("关闭后的 Send = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("关闭没有释放 Send")
	}
}

func TestMemoryTransportDrainsQueuedMessagesBeforeClosed(t *testing.T) {
	client, server := network.NewMemoryPair(2)
	for sequence := uint64(1); sequence <= 2; sequence++ {
		if err := client.Send(
			context.Background(),
			network.PlayerInput{Sequence: sequence},
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for sequence := uint64(1); sequence <= 2; sequence++ {
		message, err := server.Recv(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got := message.(network.PlayerInput).Sequence; got != sequence {
			t.Fatalf("sequence = %d，想要 %d", got, sequence)
		}
	}
	if _, err := server.Recv(ctx); !errors.Is(err, network.ErrClosed) {
		t.Fatalf("排空后的 Recv = %v", err)
	}
}

func TestMemoryTransportConcurrentCloseIsSafe(t *testing.T) {
	client, server := network.NewMemoryPair(1)
	var wait sync.WaitGroup
	wait.Add(100)
	for i := 0; i < 100; i++ {
		endpoint := i
		go func() {
			defer wait.Done()
			if endpoint&1 == 0 {
				_ = client.Close()
			} else {
				_ = server.Close()
			}
		}()
	}
	wait.Wait()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Send(ctx, network.PlayerInput{}); !errors.Is(err, network.ErrClosed) {
		t.Fatalf("并发 Close 后 Send = %v", err)
	}
}

func TestNewMemoryPairRejectsNonPositiveCapacity(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("capacity 0 没有 panic")
		}
	}()
	network.NewMemoryPair(0)
}
