//go:build darwin

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"minecraft-go/internal/network"
	"minecraft-go/internal/storage"
)

func TestApplicationStoreInteractiveUsesSelectedDiskWorld(t *testing.T) {
	worldPath := filepath.Join(t.TempDir(), "chosen-world")
	opened, err := openApplicationStore(context.Background(), applicationOptions{
		Seed:      42,
		WorldPath: worldPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := opened.(*storage.DiskStore); !ok {
		t.Fatalf("interactive store=%T，想要 *storage.DiskStore", opened)
	}
	if err := opened.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(worldPath, "world.meta")); err != nil {
		t.Fatalf("选择的世界没有 metadata: %v", err)
	}
}

func TestApplicationStoreBenchmarkUsesMemoryWithoutTouchingWorldPath(t *testing.T) {
	worldPath := filepath.Join(t.TempDir(), "must-not-exist")
	opened, err := openApplicationStore(context.Background(), applicationOptions{
		Seed:      benchmarkSeed,
		Benchmark: true,
		WorldPath: worldPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := opened.(*storage.MemoryStore); !ok {
		t.Fatalf("benchmark store=%T，想要 *storage.MemoryStore", opened)
	}
	if err := opened.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(worldPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("benchmark 触碰了 WorldPath，Stat error=%v", err)
	}
}

func TestApplicationStoreCaptureUsesMemoryWithoutTouchingWorldPath(t *testing.T) {
	worldPath := filepath.Join(t.TempDir(), "must-not-exist")
	opened, err := openApplicationStore(context.Background(), applicationOptions{
		Seed:       42,
		CaptureDir: filepath.Join(t.TempDir(), "shots"),
		WorldPath:  worldPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := opened.(*storage.MemoryStore); !ok {
		t.Fatalf("capture store=%T，想要 *storage.MemoryStore", opened)
	}
	if err := opened.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(worldPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("capture 触碰了 WorldPath，Stat error=%v", err)
	}
}

func TestApplicationConnectionRemoteNeverOpensLocalStore(t *testing.T) {
	worldPath := filepath.Join(t.TempDir(), "must-not-exist")
	opened, err := openApplicationStore(context.Background(), applicationOptions{
		Seed:      42,
		Connect:   "127.0.0.1:25565",
		WorldPath: worldPath,
	})
	if err != nil || opened != nil {
		t.Fatalf("remote store = (%T, %v), want (nil, nil)", opened, err)
	}
	if _, err := os.Stat(worldPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remote touched WorldPath, Stat error=%v", err)
	}
}

func TestApplicationStoreExistingMetadataSeedWins(t *testing.T) {
	worldPath := filepath.Join(t.TempDir(), "existing-world")
	first, err := openApplicationStore(context.Background(), applicationOptions{
		Seed:      42,
		WorldPath: worldPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := openApplicationStore(context.Background(), applicationOptions{
		Seed:      99,
		WorldPath: worldPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()
	if got := reopened.Metadata().Seed; got != 42 {
		t.Fatalf("reopened metadata seed=%d，想要既有存档 seed 42", got)
	}
}

func TestMainOptionsDefaultsAndWorldOverride(t *testing.T) {
	defaults, err := parseMainOptions(nil)
	if err != nil {
		t.Fatal(err)
	}
	if defaults.Application.Seed != 42 || defaults.Application.Benchmark ||
		defaults.Application.WorldPath != "worlds/default" {
		t.Fatalf("default options=%+v", defaults)
	}

	selected, err := parseMainOptions([]string{"--world", "testdata/my-world"})
	if err != nil {
		t.Fatal(err)
	}
	if selected.Application.WorldPath != "testdata/my-world" {
		t.Fatalf("selected world=%q", selected.Application.WorldPath)
	}

	benchmark, err := parseMainOptions([]string{
		"--benchmark", "--perf-output", "report.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !benchmark.Application.Benchmark || benchmark.Application.Seed != benchmarkSeed {
		t.Fatalf("benchmark options=%+v", benchmark)
	}
}

func TestMainOptionsRejectMissingBenchmarkOutputBeforeConstruction(t *testing.T) {
	constructed := 0
	err := runWithDependencies([]string{"--benchmark"}, runDependencies{
		loadIdentity: testIdentityLoader,
		newApplication: func(applicationOptions) (*application, error) {
			constructed++
			return nil, errors.New("不应构造应用")
		},
	})
	if err == nil {
		t.Fatal("run error=nil，想要缺少 --perf-output 的错误")
	}
	if constructed != 0 {
		t.Fatalf("缺少 --perf-output 时构造应用 %d 次", constructed)
	}
}

func TestRunWithDependenciesPropagatesLifecycleErrorsAndAlwaysCloses(t *testing.T) {
	constructionErr := errors.New("构造失败")
	benchmarkErr := errors.New("benchmark 失败")
	closeErr := errors.New("持久化关服失败")
	tests := []struct {
		name             string
		args             []string
		constructionErr  error
		benchmarkErr     error
		closeErr         error
		wantConstruction int
		wantInteractive  int
		wantBenchmark    int
		wantClose        int
		wantErrors       []error
	}{
		{
			name:             "construction error",
			constructionErr:  constructionErr,
			wantConstruction: 1,
			wantErrors:       []error{constructionErr},
		},
		{
			name:             "interactive success and close error",
			closeErr:         closeErr,
			wantConstruction: 1,
			wantInteractive:  1,
			wantClose:        1,
			wantErrors:       []error{closeErr},
		},
		{
			name:             "benchmark error and successful close",
			args:             []string{"--benchmark", "--perf-output", "report.json"},
			benchmarkErr:     benchmarkErr,
			wantConstruction: 1,
			wantBenchmark:    1,
			wantClose:        1,
			wantErrors:       []error{benchmarkErr},
		},
		{
			name:             "benchmark and close errors",
			args:             []string{"--benchmark", "--perf-output", "report.json"},
			benchmarkErr:     benchmarkErr,
			closeErr:         closeErr,
			wantConstruction: 1,
			wantBenchmark:    1,
			wantClose:        1,
			wantErrors:       []error{benchmarkErr, closeErr},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			constructionCalls := 0
			interactiveCalls := 0
			benchmarkCalls := 0
			closeCalls := 0
			gotErr := runWithDependencies(test.args, runDependencies{
				loadIdentity: testIdentityLoader,
				newApplication: func(applicationOptions) (*application, error) {
					constructionCalls++
					if test.constructionErr != nil {
						return nil, test.constructionErr
					}
					serverDone := make(chan error, 1)
					if test.closeErr == nil {
						serverDone <- context.Canceled
					} else {
						serverDone <- test.closeErr
					}
					return &application{
						serverCancel: func() {},
						serverDone:   serverDone,
						releaseResources: func() {
							closeCalls++
						},
					}, nil
				},
				runInteractive: func(*application) error {
					interactiveCalls++
					return nil
				},
				runBenchmark: func(*application, string) error {
					benchmarkCalls++
					return test.benchmarkErr
				},
			})

			if constructionCalls != test.wantConstruction ||
				interactiveCalls != test.wantInteractive ||
				benchmarkCalls != test.wantBenchmark || closeCalls != test.wantClose {
				t.Fatalf(
					"calls construction=%d interactive=%d benchmark=%d close=%d，想要 %d/%d/%d/%d",
					constructionCalls, interactiveCalls, benchmarkCalls, closeCalls,
					test.wantConstruction, test.wantInteractive, test.wantBenchmark,
					test.wantClose,
				)
			}
			for _, wantErr := range test.wantErrors {
				if !errors.Is(gotErr, wantErr) {
					t.Errorf("run error=%v，不包含 %v", gotErr, wantErr)
				}
			}
		})
	}
}

func testIdentityLoader(*string) (network.Identity, error) {
	return network.Identity{}, nil
}
