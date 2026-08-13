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

	"minecraft-go/internal/config"
	"minecraft-go/internal/core"
	"minecraft-go/internal/logging"
	"minecraft-go/internal/network"
	"minecraft-go/internal/server"
	"minecraft-go/internal/storage"
	"minecraft-go/internal/worldgen"
)

type options struct {
	Listen           string
	World            string
	Seed             int64
	MaxPlayers       int
	MigrateMaterials bool
	Backup           string
	// Config 是调参配置文件路径；留空表示使用 config.DefaultPath()。
	Config string
}

type mcgodHost interface {
	Run(context.Context, network.Listener) error
}

type dependencies struct {
	openDisk         func(context.Context, string, storage.OpenOptions) (storage.WorldStore, error)
	listenTCP        func(string) (network.Listener, error)
	newHost          func(server.Config, server.Generator, storage.WorldStore) mcgodHost
	migrateMaterials func(context.Context, string, string) error
	logger           *slog.Logger
}

func parseOptions(args []string) (options, error) {
	flags := flag.NewFlagSet("mcgod", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	listen := flags.String("listen", ":25565", "TCP 监听地址")
	world := flags.String("world", "worlds/default", "世界存档目录")
	seed := flags.Int64("seed", 42, "新世界种子")
	maxPlayers := flags.Int("max-players", 8, "最大玩家数（1..8，默认 8）")
	configPath := flags.String("config", "", "配置文件路径，留空使用默认路径")
	migrate := flags.Bool("migrate-materials", false, "离线迁移旧世界自然材料")
	backup := flags.String("backup", "", "材料迁移完整备份目录")
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
	if *maxPlayers < 1 || *maxPlayers > 8 {
		return options{}, fmt.Errorf("--max-players 必须在 1..8 之间，实际为 %d", *maxPlayers)
	}
	backupPath := strings.TrimSpace(*backup)
	if *migrate && backupPath == "" {
		return options{}, errors.New("--migrate-materials 必须配合非空 --backup")
	}
	if !*migrate && backupPath != "" {
		return options{}, errors.New("--backup 只能配合 --migrate-materials")
	}
	if backupPath != "" {
		backupPath = filepath.Clean(backupPath)
	}
	return options{
		Listen: listenAddress, World: filepath.Clean(*world), Seed: *seed, MaxPlayers: *maxPlayers,
		Config: *configPath, MigrateMaterials: *migrate, Backup: backupPath,
	}, nil
}

// resolveConfig 决定 mcgod 本次运行的生效配置：Config 非空时从该路径加载，
// 否则用默认路径。mcgod 不消费渲染组，调用方应只取 Logging/Physics/Sim。
func resolveConfig(opts options) (config.Config, error) {
	if opts.Config != "" {
		return config.Load(opts.Config)
	}
	return config.LoadDefault()
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
		migrateMaterials: migrateMaterials,
		logger:           slog.Default(),
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
	if options.MigrateMaterials {
		dependencies := mergeDependencies(injected)
		return dependencies.migrateMaterials(ctx, options.World, options.Backup)
	}
	effective, err := resolveConfig(options)
	if err != nil {
		return fmt.Errorf("加载配置: %w", err)
	}
	// 内层 handler 的 Level 固定为 LevelDebug：过滤全部交给 logging 包的包装器，
	// 内层不得二次过滤，否则模块放宽会失效。
	logging.Install(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}), effective.Logging)
	// mcgod 不消费渲染组：effective.Render 就地丢弃，只 Apply physics 与 sim。
	effective.Apply()
	dependencies := mergeDependencies(injected)
	store, err := dependencies.openDisk(ctx, options.World, storage.OpenOptions{Create: storage.Metadata{
		FormatVersion:  2,
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
	config := server.DefaultConfig(metadata.Seed)
	config.MaxPlayers = options.MaxPlayers
	host := dependencies.newHost(config, worldgen.New(metadata.Seed), store)
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
	if injected.migrateMaterials != nil {
		defaults.migrateMaterials = injected.migrateMaterials
	}
	if injected.logger != nil {
		defaults.logger = injected.logger
	}
	return defaults
}

func runSignal(args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// 传空 dependencies：run 内部在 logging.Install 之后才调用 mergeDependencies，
	// 这样默认 logger 取到的是刚装配的 slog.Default()，而不是 Install 之前的旧默认值。
	return run(ctx, args, dependencies{})
}

func main() {
	if err := runSignal(os.Args[1:]); err != nil {
		slog.Error("mcgod 退出失败", "error", err)
		os.Exit(1)
	}
}
