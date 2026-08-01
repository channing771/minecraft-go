package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/server"
	"minecraft-go/internal/storage"
)

func TestDefaultOptions(t *testing.T) {
	got, err := parseOptions(nil)
	if err != nil || got.Listen != ":25565" || got.World != "worlds/default" || got.Seed != 42 {
		t.Fatalf("options=%+v err=%v", got, err)
	}
}

func TestParseOptionsRejectsInvalidArguments(t *testing.T) {
	for _, args := range [][]string{
		{"unexpected"},
		{"--listen", ""},
		{"--listen", "not a tcp address"},
		{"--world", ""},
	} {
		if _, err := parseOptions(args); err == nil {
			t.Fatalf("parseOptions(%v) accepted invalid options", args)
		}
	}
}

func TestRunOpensWorldBeforeListeningAndUsesStoredSeed(t *testing.T) {
	var events []string
	store := storage.NewMemory(storage.Metadata{
		FormatVersion:  1,
		Seed:           91,
		SpawnDimension: core.Overworld,
	})
	host := &mcgodTestHost{runErr: errors.New("stop after assembly")}
	var logs bytes.Buffer
	err := run(context.Background(), []string{"--listen", "127.0.0.1:25565", "--world", "worlds/./demo", "--seed", "7"}, dependencies{
		openDisk: func(_ context.Context, world string, options storage.OpenOptions) (storage.WorldStore, error) {
			events = append(events, "open:"+world)
			if options.Create.Seed != 7 || options.Create.SpawnDimension != core.Overworld {
				t.Fatalf("create metadata=%+v", options.Create)
			}
			return store, nil
		},
		listenTCP: func(address string) (network.Listener, error) {
			events = append(events, "listen:"+address)
			return mcgodTestListener{addr: "127.0.0.1:25565"}, nil
		},
		newHost: func(config server.Config, _ server.Generator, got storage.WorldStore) mcgodHost {
			if got != store || config.Seed != 91 {
				t.Fatalf("host store=%T seed=%d, want persisted seed 91", got, config.Seed)
			}
			return host
		},
		logger: slog.New(slog.NewTextHandler(&logs, nil)),
	})
	if !errors.Is(err, host.runErr) {
		t.Fatalf("run error=%v, want %v", err, host.runErr)
	}
	if got, want := strings.Join(events, ","), "open:worlds/demo,listen:127.0.0.1:25565"; got != want {
		t.Fatalf("assembly order=%q, want %q", got, want)
	}
	for _, field := range []string{"listen=127.0.0.1:25565", "world=worlds/demo", "protocol=2"} {
		if !strings.Contains(logs.String(), field) {
			t.Fatalf("startup log %q lacks %q", logs.String(), field)
		}
	}
}

func TestRunClosesWorldWhenListeningFails(t *testing.T) {
	store := &mcgodClosingStore{WorldStore: storage.NewMemory(storage.Metadata{FormatVersion: 1})}
	listenErr := errors.New("address already in use")
	err := run(context.Background(), nil, dependencies{
		openDisk:  func(context.Context, string, storage.OpenOptions) (storage.WorldStore, error) { return store, nil },
		listenTCP: func(string) (network.Listener, error) { return nil, listenErr },
	})
	if !errors.Is(err, listenErr) || store.closes != 1 {
		t.Fatalf("run error=%v closes=%d, want listener error and one close", err, store.closes)
	}
}

func TestRunCancellationLetsHostPerformSafeShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	host := &mcgodTestHost{shutdownOnCancel: true, started: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, nil, dependencies{
			openDisk: func(context.Context, string, storage.OpenOptions) (storage.WorldStore, error) {
				return storage.NewMemory(storage.Metadata{FormatVersion: 1}), nil
			},
			listenTCP: func(string) (network.Listener, error) { return mcgodTestListener{addr: "127.0.0.1:9"}, nil },
			newHost:   func(server.Config, server.Generator, storage.WorldStore) mcgodHost { return host },
		})
	}()
	<-host.started
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run cancellation error=%v", err)
	}
	if host.shutdowns() != 1 {
		t.Fatalf("host shutdown count=%d, want 1", host.shutdowns())
	}
}

func TestRunPreservesFlushFailures(t *testing.T) {
	for _, want := range []error{errors.New("player flush failed"), errors.New("chunk flush failed")} {
		t.Run(want.Error(), func(t *testing.T) {
			err := run(context.Background(), nil, dependencies{
				openDisk: func(context.Context, string, storage.OpenOptions) (storage.WorldStore, error) {
					return storage.NewMemory(storage.Metadata{FormatVersion: 1}), nil
				},
				listenTCP: func(string) (network.Listener, error) { return mcgodTestListener{}, nil },
				newHost: func(server.Config, server.Generator, storage.WorldStore) mcgodHost {
					return &mcgodTestHost{runErr: want}
				},
			})
			if !errors.Is(err, want) {
				t.Fatalf("run error=%v, want root cause %v", err, want)
			}
		})
	}
}

func TestRunCancellationDoesNotMaskFlushFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	want := errors.New("chunk flush failed after SIGTERM")
	host := &mcgodTestHost{
		runErr:           want,
		shutdownOnCancel: true,
		started:          make(chan struct{}),
	}
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, nil, dependencies{
			openDisk: func(context.Context, string, storage.OpenOptions) (storage.WorldStore, error) {
				return storage.NewMemory(storage.Metadata{FormatVersion: 1}), nil
			},
			listenTCP: func(string) (network.Listener, error) { return mcgodTestListener{}, nil },
			newHost:   func(server.Config, server.Generator, storage.WorldStore) mcgodHost { return host },
		})
	}()
	<-host.started
	cancel()
	if err := <-done; !errors.Is(err, want) {
		t.Fatalf("run error=%v, want unmasked flush error %v", err, want)
	}
}

type mcgodTestHost struct {
	runErr           error
	shutdownOnCancel bool
	started          chan struct{}
	mu               sync.Mutex
	shutdown         int
}

func (host *mcgodTestHost) Run(ctx context.Context, _ network.Listener) error {
	if host.started == nil {
		host.started = make(chan struct{})
	}
	close(host.started)
	if !host.shutdownOnCancel {
		return host.runErr
	}
	<-ctx.Done()
	host.mu.Lock()
	host.shutdown++
	host.mu.Unlock()
	return host.runErr
}

func (host *mcgodTestHost) shutdowns() int {
	host.mu.Lock()
	defer host.mu.Unlock()
	return host.shutdown
}

type mcgodTestListener struct{ addr string }

func (listener mcgodTestListener) Accept(context.Context) (network.ServerPacketStream, error) {
	return nil, network.ErrClosed
}
func (listener mcgodTestListener) Addr() string { return listener.addr }
func (mcgodTestListener) Close() error          { return nil }

type mcgodClosingStore struct {
	storage.WorldStore
	closes int
}

func (store *mcgodClosingStore) Close() error {
	store.closes++
	return store.WorldStore.Close()
}
