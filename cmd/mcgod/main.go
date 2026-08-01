// Command mcgod starts the headless TCP dedicated server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/server"
	"minecraft-go/internal/storage"
	"minecraft-go/internal/worldgen"
)

type options struct {
	Listen string
	World  string
	Seed   int64
}

type mcgodHost interface {
	Run(context.Context, network.Listener) error
}

type dependencies struct {
	openDisk  func(context.Context, string, storage.OpenOptions) (storage.WorldStore, error)
	listenTCP func(string) (network.Listener, error)
	newHost   func(server.Config, server.Generator, storage.WorldStore) mcgodHost
	logger    *slog.Logger
}

func parseOptions(args []string) (options, error) {
	flags := flag.NewFlagSet("mcgod", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	listen := flags.String("listen", ":25565", "TCP 监听地址")
	world := flags.String("world", "worlds/default", "世界存档目录")
	seed := flags.Int64("seed", 42, "新世界种子")
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	if flags.NArg() != 0 {
		return options{}, fmt.Errorf("未知位置参数: %v", flags.Args())
	}
	if strings.TrimSpace(*world) == "" {
		return options{}, errors.New("--world 不能为空")
	}
	listenAddress := strings.TrimSpace(*listen)
	if listenAddress == "" {
		return options{}, errors.New("--listen 不能为空")
	}
	if _, err := net.ResolveTCPAddr("tcp", listenAddress); err != nil {
		return options{}, fmt.Errorf("无效 --listen %q: %w", *listen, err)
	}
	return options{Listen: listenAddress, World: filepath.Clean(*world), Seed: *seed}, nil
}

func defaultDependencies() dependencies {
	return dependencies{
		openDisk: func(ctx context.Context, world string, options storage.OpenOptions) (storage.WorldStore, error) {
			return storage.OpenDisk(ctx, world, options)
		},
		listenTCP: network.ListenTCP,
		newHost: func(config server.Config, generator server.Generator, store storage.WorldStore) mcgodHost {
			return server.NewHost(config, generator, store)
		},
		logger: slog.Default(),
	}
}

func run(ctx context.Context, args []string, injected dependencies) error {
	if ctx == nil {
		return errors.New("mcgod: nil context")
	}
	options, err := parseOptions(args)
	if err != nil {
		return err
	}
	dependencies := mergeDependencies(injected)
	store, err := dependencies.openDisk(ctx, options.World, storage.OpenOptions{Create: storage.Metadata{
		FormatVersion:  1,
		Seed:           options.Seed,
		SpawnDimension: core.Overworld,
	}})
	if err != nil {
		return fmt.Errorf("打开世界: %w", err)
	}
	listener, err := dependencies.listenTCP(options.Listen)
	if err != nil {
		return errors.Join(fmt.Errorf("监听 %q: %w", options.Listen, err), store.Close())
	}

	metadata := store.Metadata()
	dependencies.logger.Info("mcgod 已启动", "listen", listener.Addr(), "world", options.World, "protocol", network.ProtocolVersion)
	host := dependencies.newHost(server.DefaultConfig(metadata.Seed), worldgen.New(metadata.Seed), store)
	return host.Run(ctx, listener)
}

func mergeDependencies(injected dependencies) dependencies {
	defaults := defaultDependencies()
	if injected.openDisk != nil {
		defaults.openDisk = injected.openDisk
	}
	if injected.listenTCP != nil {
		defaults.listenTCP = injected.listenTCP
	}
	if injected.newHost != nil {
		defaults.newHost = injected.newHost
	}
	if injected.logger != nil {
		defaults.logger = injected.logger
	}
	return defaults
}

func runSignal(args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return run(ctx, args, defaultDependencies())
}

func main() {
	if err := runSignal(os.Args[1:]); err != nil {
		slog.Error("mcgod 退出失败", "error", err)
		os.Exit(1)
	}
}
