package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/config"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/server"
	"github.com/channing771/mornlea/internal/storage"
)

// absentConfigArgs 返回指向本次测试临时目录下一个不存在文件的 --config 参数。
// 它让普通运行测试走显式 config.Load 的 Defaults 回落，避免读写开发者的默认目录。
func absentConfigArgs(t *testing.T) []string {
	t.Helper()
	return []string{"--config", filepath.Join(t.TempDir(), "absent.json")}
}

func legacyConfigPath(base string) string {
	return filepath.Join(base, "minecraft-go", "config.json")
}

func TestResolveConfigUsesDefaultMigration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir: %v", err)
	}
	legacyPath := legacyConfigPath(base)
	currentPath := filepath.Join(base, "mornlea", "config.json")
	legacy := config.Defaults()
	legacy.Physics.Gravity = 24
	if err := legacy.Save(legacyPath); err != nil {
		t.Fatalf("Save legacy config: %v", err)
	}

	got, err := resolveConfig(options{})
	if err != nil {
		t.Fatalf("resolveConfig: %v", err)
	}
	if got.Physics.Gravity != 24 {
		t.Fatalf("gravity = %v，want 24", got.Physics.Gravity)
	}
	if _, err := os.ReadFile(currentPath); err != nil {
		t.Fatalf("读取迁移后默认配置: %v", err)
	}
}

func TestResolveConfigExplicitPathSkipsDefaultMigration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir: %v", err)
	}
	currentPath := filepath.Join(base, "mornlea", "config.json")
	if err := os.MkdirAll(filepath.Dir(currentPath), 0o700); err != nil {
		t.Fatalf("MkdirAll current: %v", err)
	}
	const invalidDefault = `{"version":`
	if err := os.WriteFile(currentPath, []byte(invalidDefault), 0o600); err != nil {
		t.Fatalf("WriteFile current: %v", err)
	}
	explicitPath := filepath.Join(t.TempDir(), "explicit.json")
	explicit := config.Defaults()
	explicit.Physics.Gravity = 31
	if err := explicit.Save(explicitPath); err != nil {
		t.Fatalf("Save explicit config: %v", err)
	}

	got, err := resolveConfig(options{Config: explicitPath})
	if err != nil {
		t.Fatalf("resolveConfig: %v", err)
	}
	if got.Physics.Gravity != 31 {
		t.Fatalf("gravity = %v，want 31", got.Physics.Gravity)
	}
	contents, err := os.ReadFile(currentPath)
	if err != nil || string(contents) != invalidDefault {
		t.Fatalf("显式配置不得读取或修改默认配置，contents = %q, err = %v", contents, err)
	}
}

func TestDefaultOptions(t *testing.T) {
	got, err := parseOptions(nil)
	if err != nil || got.Listen != ":25565" || got.World != "worlds/default" || got.Seed != 42 || got.MaxPlayers != 8 {
		t.Fatalf("options=%+v err=%v", got, err)
	}
}

func TestServerProtocolV19IsCurrent(t *testing.T) {
	if network.ProtocolVersion != 19 {
		t.Fatalf("专用服务端协议版本 = %d，想要 19", network.ProtocolVersion)
	}
}

func TestOptionsMaxPlayers(t *testing.T) {
	got, err := parseOptions([]string{"--max-players=1"})
	if err != nil || got.MaxPlayers != 1 {
		t.Fatalf("explicit MaxPlayers options=%+v err=%v", got, err)
	}
	for _, value := range []string{"0", "9"} {
		if _, err := parseOptions([]string{"--max-players=" + value}); err == nil {
			t.Fatalf("parseOptions accepted explicit --max-players=%s", value)
		}
	}
}

func TestRunPassesMaxPlayersToHost(t *testing.T) {
	want := errors.New("stop after config capture")
	var got int
	err := run(context.Background(), append([]string{"--max-players=3"}, absentConfigArgs(t)...), dependencies{
		openDisk: func(context.Context, string, storage.OpenOptions) (storage.WorldStore, error) {
			return storage.NewMemory(storage.Metadata{FormatVersion: 2, Seed: 42}), nil
		},
		listenTCP: func(string) (network.Listener, error) { return mornleaServerTestListener{}, nil },
		newHost: func(_ context.Context, config server.Config, _ server.Generator, _ storage.WorldStore) (mornleaServerHost, error) {
			got = config.MaxPlayers
			return &mornleaServerTestHost{runErr: want}, nil
		},
	})
	if !errors.Is(err, want) || got != 3 {
		t.Fatalf("run error=%v MaxPlayers=%d, want %v and 3", err, got, want)
	}
}

func TestRunInjectsAICompanionsIntoDedicatedServer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	id, err := companion.ParseID("00112233-4455-4677-8899-aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	// M5B 起非空伙伴必须携带完整模型设置才能通过 config.Load；这里用免密钥的
	// loopback 形态，保持本测试"专服注入伙伴且不改写配置文件"的主题不变。
	cfg.AI = &config.AI{
		ModelSettings: companion.ModelSettings{
			Endpoint: "http://127.0.0.1:1/v1",
			Model:    "test-model",
		},
		Companions: []companion.Definition{{ID: id, Name: "阿木"}},
	}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	want := errors.New("stop after config capture")
	var got []companion.Definition
	err = run(context.Background(), []string{"--config", path}, dependencies{
		openDisk: func(context.Context, string, storage.OpenOptions) (storage.WorldStore, error) {
			return storage.NewMemory(storage.Metadata{FormatVersion: 2, Seed: 42}), nil
		},
		listenTCP: func(string) (network.Listener, error) { return mornleaServerTestListener{}, nil },
		newHost: func(_ context.Context, config server.Config, _ server.Generator, _ storage.WorldStore) (mornleaServerHost, error) {
			got = config.Companions
			config.Companions[0].Name = "已改"
			return &mornleaServerTestHost{runErr: want}, nil
		},
	})
	if !errors.Is(err, want) || len(got) != 1 || got[0].ID != id {
		t.Fatalf("run error=%v companions=%+v", err, got)
	}
	reloaded, loadErr := config.Load(path)
	if loadErr != nil || reloaded.CompanionDefinitions()[0].Name != "阿木" {
		t.Fatalf("专服注入修改了配置值：%+v, %v", reloaded.CompanionDefinitions(), loadErr)
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

func TestParseOptionsMigrateMaterialsRequiresBackup(t *testing.T) {
	for _, args := range [][]string{
		{"--migrate-materials"},
		{"--migrate-materials", "--backup", "   "},
		{"--backup", "backups/world"},
	} {
		if _, err := parseOptions(args); err == nil {
			t.Fatalf("parseOptions(%v) 接受了不互斥的迁移参数", args)
		}
	}

	got, err := parseOptions([]string{
		"--world", "worlds/./legacy",
		"--migrate-materials",
		"--backup", "backups/./legacy",
	})
	if err != nil {
		t.Fatalf("parseOptions 拒绝有效迁移参数: %v", err)
	}
	if !got.MigrateMaterials || got.World != "worlds/legacy" || got.Backup != "backups/legacy" {
		t.Fatalf("迁移 options = %+v", got)
	}
}

func TestRunMigrateMaterialsReturnsBeforeConfigAndServerAssembly(t *testing.T) {
	invalidConfig := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(invalidConfig, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	want := errors.New("迁移已停止")
	ctx := context.Background()
	called := 0
	err := run(ctx, []string{
		"--world", "worlds/./legacy",
		"--migrate-materials",
		"--backup", "backups/./legacy",
		"--config", invalidConfig,
	}, dependencies{
		migrateMaterials: func(gotContext context.Context, world, backup string) error {
			called++
			if gotContext != ctx || world != "worlds/legacy" || backup != "backups/legacy" {
				t.Fatalf("迁移调用 context/world/backup = %v/%q/%q", gotContext, world, backup)
			}
			return want
		},
		openDisk: func(context.Context, string, storage.OpenOptions) (storage.WorldStore, error) {
			t.Fatal("迁移模式打开了普通 server store")
			return nil, nil
		},
		listenTCP: func(string) (network.Listener, error) {
			t.Fatal("迁移模式监听了 TCP")
			return nil, nil
		},
		newHost: func(context.Context, server.Config, server.Generator, storage.WorldStore) (mornleaServerHost, error) {
			t.Fatal("迁移模式构造了 host")
			return nil, nil
		},
	})
	if !errors.Is(err, want) || called != 1 {
		t.Fatalf("run 迁移错误/调用次数 = %v/%d，期望 %v/1", err, called, want)
	}
}

func TestRunMigrateMaterialsReturnsWorldLockConflict(t *testing.T) {
	ctx := context.Background()
	worldPath := filepath.Join(t.TempDir(), "world")
	store, err := storage.OpenDisk(ctx, worldPath, storage.OpenOptions{Create: storage.Metadata{
		FormatVersion: 2,
		Seed:          42,
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	err = run(ctx, []string{
		"--world", worldPath,
		"--migrate-materials",
		"--backup", filepath.Join(t.TempDir(), "backup"),
	}, migrationOnlyDependencies(t))
	if !errors.Is(err, storage.ErrWorldLocked) {
		t.Fatalf("锁冲突迁移错误 = %v，期望 ErrWorldLocked", err)
	}
	if _, err := os.Stat(filepath.Join(worldPath, materialMigrationStateName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("锁冲突后出现迁移状态，Stat 错误: %v", err)
	}
}

func TestRunMigrateMaterialsDoesNotCreateMissingWorld(t *testing.T) {
	root := t.TempDir()
	worldPath := filepath.Join(root, "missing-world")
	err := run(context.Background(), []string{
		"--world", worldPath,
		"--migrate-materials",
		"--backup", filepath.Join(root, "backup"),
	}, migrationOnlyDependencies(t))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("缺失 world.meta 的迁移错误 = %v，期望 os.ErrNotExist", err)
	}
	if _, err := os.Stat(worldPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("迁移创建了缺失世界，Stat 错误: %v", err)
	}
}

func TestRunMigrateMaterialsCompletesAndRerunsWithSameArguments(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	worldPath := filepath.Join(root, "world")
	backupPath := filepath.Join(root, "backup")
	store, err := storage.OpenDisk(ctx, worldPath, storage.OpenOptions{Create: storage.Metadata{
		FormatVersion:  2,
		Seed:           42,
		SpawnDimension: core.Overworld,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	args := []string{
		"--world", worldPath,
		"--migrate-materials",
		"--backup", backupPath,
	}
	for attempt := 1; attempt <= 2; attempt++ {
		if err := run(ctx, args, migrationOnlyDependencies(t)); err != nil {
			t.Fatalf("第 %d 次材料迁移失败: %v", attempt, err)
		}
	}
	if state := readMaterialMigrationStateForTest(t, worldPath); !state.Complete {
		t.Fatal("同参数重跑后迁移状态未完成")
	}
	reopened, err := storage.OpenDisk(ctx, worldPath, storage.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := reopened.Metadata().FormatVersion; got != 2 {
		t.Fatalf("迁移后 metadata 版本 = %d，期望 2", got)
	}
	if network.ProtocolVersion != 19 {
		t.Fatalf("迁移命令改变了协议版本: %d", network.ProtocolVersion)
	}
}

func migrationOnlyDependencies(t *testing.T) dependencies {
	t.Helper()
	return dependencies{
		openDisk: func(context.Context, string, storage.OpenOptions) (storage.WorldStore, error) {
			t.Fatal("迁移模式打开了普通 server store")
			return nil, nil
		},
		listenTCP: func(string) (network.Listener, error) {
			t.Fatal("迁移模式监听了 TCP")
			return nil, nil
		},
		newHost: func(context.Context, server.Config, server.Generator, storage.WorldStore) (mornleaServerHost, error) {
			t.Fatal("迁移模式构造了 host")
			return nil, nil
		},
	}
}

func TestRunOpensWorldBeforeListeningAndUsesStoredSeed(t *testing.T) {
	var events []string
	store := storage.NewMemory(storage.Metadata{
		FormatVersion:  2,
		Seed:           91,
		SpawnDimension: core.Overworld,
	})
	host := &mornleaServerTestHost{runErr: errors.New("stop after assembly")}
	var logs bytes.Buffer
	args := append([]string{"--listen", "127.0.0.1:25565", "--world", "worlds/./demo", "--seed", "7"}, absentConfigArgs(t)...)
	err := run(context.Background(), args, dependencies{
		openDisk: func(_ context.Context, world string, options storage.OpenOptions) (storage.WorldStore, error) {
			events = append(events, "open:"+world)
			if options.Create.Seed != 7 || options.Create.SpawnDimension != core.Overworld {
				t.Fatalf("create metadata=%+v", options.Create)
			}
			return store, nil
		},
		listenTCP: func(address string) (network.Listener, error) {
			events = append(events, "listen:"+address)
			return mornleaServerTestListener{addr: "127.0.0.1:25565"}, nil
		},
		newHost: func(_ context.Context, config server.Config, _ server.Generator, got storage.WorldStore) (mornleaServerHost, error) {
			if got != store || config.Seed != 91 {
				t.Fatalf("host store=%T seed=%d, want persisted seed 91", got, config.Seed)
			}
			return host, nil
		},
		logger: slog.New(slog.NewTextHandler(&logs, nil)),
	})
	if !errors.Is(err, host.runErr) {
		t.Fatalf("run error=%v, want %v", err, host.runErr)
	}
	if got, want := strings.Join(events, ","), "open:worlds/demo,listen:127.0.0.1:25565"; got != want {
		t.Fatalf("assembly order=%q, want %q", got, want)
	}
	for _, field := range []string{"listen=127.0.0.1:25565", "world=worlds/demo", fmt.Sprintf("protocol=%d", network.ProtocolVersion)} {
		if !strings.Contains(logs.String(), field) {
			t.Fatalf("startup log %q lacks %q", logs.String(), field)
		}
	}
}

func TestRunClosesWorldWhenListeningFails(t *testing.T) {
	store := &mornleaServerClosingStore{WorldStore: storage.NewMemory(storage.Metadata{FormatVersion: 2})}
	listenErr := errors.New("address already in use")
	err := run(context.Background(), absentConfigArgs(t), dependencies{
		openDisk:  func(context.Context, string, storage.OpenOptions) (storage.WorldStore, error) { return store, nil },
		listenTCP: func(string) (network.Listener, error) { return nil, listenErr },
	})
	if !errors.Is(err, listenErr) || store.closes != 1 {
		t.Fatalf("run error=%v closes=%d, want listener error and one close", err, store.closes)
	}
}

func TestNewHostFailureClosesDedicatedListenerAndStore(t *testing.T) {
	wantErr := errors.New("companion bootstrap failed")
	store := &mornleaServerClosingStore{WorldStore: storage.NewMemory(storage.Metadata{FormatVersion: 2})}
	listener := &mornleaServerClosingListener{}
	var logs bytes.Buffer
	ctx := context.WithValue(context.Background(), struct{}{}, "constructor-context")
	err := run(ctx, absentConfigArgs(t), dependencies{
		openDisk:  func(context.Context, string, storage.OpenOptions) (storage.WorldStore, error) { return store, nil },
		listenTCP: func(string) (network.Listener, error) { return listener, nil },
		newHost: func(got context.Context, _ server.Config, _ server.Generator, _ storage.WorldStore) (mornleaServerHost, error) {
			if got != ctx {
				t.Fatalf("NewHost context = %v，want caller context", got)
			}
			return nil, wantErr
		},
		logger: slog.New(slog.NewTextHandler(&logs, nil)),
	})
	if !errors.Is(err, wantErr) || store.closes != 1 || listener.closes != 1 {
		t.Fatalf("run error=%v store/listener closes=%d/%d", err, store.closes, listener.closes)
	}
	if strings.Contains(logs.String(), "mornlea-server 已启动") {
		t.Fatalf("Host 构造失败却记录了启动日志：%q", logs.String())
	}
}

func TestRunCancellationLetsHostPerformSafeShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	host := &mornleaServerTestHost{shutdownOnCancel: true, started: make(chan struct{})}
	args := absentConfigArgs(t)
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, args, dependencies{
			openDisk: func(context.Context, string, storage.OpenOptions) (storage.WorldStore, error) {
				return storage.NewMemory(storage.Metadata{FormatVersion: 2}), nil
			},
			listenTCP: func(string) (network.Listener, error) { return mornleaServerTestListener{addr: "127.0.0.1:9"}, nil },
			newHost: func(context.Context, server.Config, server.Generator, storage.WorldStore) (mornleaServerHost, error) {
				return host, nil
			},
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
			err := run(context.Background(), absentConfigArgs(t), dependencies{
				openDisk: func(context.Context, string, storage.OpenOptions) (storage.WorldStore, error) {
					return storage.NewMemory(storage.Metadata{FormatVersion: 2}), nil
				},
				listenTCP: func(string) (network.Listener, error) { return mornleaServerTestListener{}, nil },
				newHost: func(context.Context, server.Config, server.Generator, storage.WorldStore) (mornleaServerHost, error) {
					return &mornleaServerTestHost{runErr: want}, nil
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
	host := &mornleaServerTestHost{
		runErr:           want,
		shutdownOnCancel: true,
		started:          make(chan struct{}),
	}
	args := absentConfigArgs(t)
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, args, dependencies{
			openDisk: func(context.Context, string, storage.OpenOptions) (storage.WorldStore, error) {
				return storage.NewMemory(storage.Metadata{FormatVersion: 2}), nil
			},
			listenTCP: func(string) (network.Listener, error) { return mornleaServerTestListener{}, nil },
			newHost: func(context.Context, server.Config, server.Generator, storage.WorldStore) (mornleaServerHost, error) {
				return host, nil
			},
		})
	}()
	<-host.started
	cancel()
	if err := <-done; !errors.Is(err, want) {
		t.Fatalf("run error=%v, want unmasked flush error %v", err, want)
	}
}

type mornleaServerTestHost struct {
	runErr           error
	shutdownOnCancel bool
	started          chan struct{}
	mu               sync.Mutex
	shutdown         int
}

func (host *mornleaServerTestHost) Run(ctx context.Context, _ network.Listener) error {
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

func (host *mornleaServerTestHost) shutdowns() int {
	host.mu.Lock()
	defer host.mu.Unlock()
	return host.shutdown
}

type mornleaServerTestListener struct{ addr string }

func (listener mornleaServerTestListener) Accept(context.Context) (network.ServerPacketStream, error) {
	return nil, network.ErrClosed
}
func (listener mornleaServerTestListener) Addr() string { return listener.addr }
func (mornleaServerTestListener) Close() error          { return nil }

type mornleaServerClosingListener struct{ closes int }

func (*mornleaServerClosingListener) Accept(context.Context) (network.ServerPacketStream, error) {
	return nil, network.ErrClosed
}
func (*mornleaServerClosingListener) Addr() string { return "127.0.0.1:9" }
func (listener *mornleaServerClosingListener) Close() error {
	listener.closes++
	return nil
}

type mornleaServerClosingStore struct {
	storage.WorldStore
	closes int
}

func (store *mornleaServerClosingStore) Close() error {
	store.closes++
	return store.WorldStore.Close()
}

func TestParseOptionsAcceptsConfig(t *testing.T) {
	options, err := parseOptions([]string{"--config", "/tmp/x.json"})
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	if options.Config != "/tmp/x.json" {
		t.Fatalf("Config = %q", options.Config)
	}
}

func TestParseOptionsConfigDefaultsEmpty(t *testing.T) {
	options, err := parseOptions([]string{})
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	if options.Config != "" {
		t.Fatalf("Config 默认应为空（表示使用默认路径），实际 %q", options.Config)
	}
}
