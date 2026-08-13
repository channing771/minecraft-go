.DEFAULT_GOAL := help

GO := go
CARGO := cargo
RUST_DIR := engine
RUST_DYLIB := $(RUST_DIR)/target/release/libmornlea_mesh.dylib
APP := ./cmd/mornlea
BINARY := bin/mornlea
SERVER := ./cmd/mornlea-server
SERVER_BINARY := bin/mornlea-server
MORNLEA_DYLIB := bin/libmornlea_mesh.dylib
ARGS ?=

.PHONY: help run build test test-race test-multiplayer bench-multiplayer archcheck fmt clean visual-check visual-update rust rust-check

run test test-multiplayer bench-multiplayer visual-check visual-update: rust
build: rust
build: GO_BUILD_LDFLAGS := -extldflags=-Wl,-rpath,@loader_path
test-race: rust

help:
	@printf '%s\n' \
		'常用命令：' \
		'  make run              运行游戏，可通过 ARGS 传递参数' \
		'  make build            构建 bin/mornlea、bin/mornlea-server 与同目录 Rust dylib' \
		'  make test             运行全部测试' \
		'  make test-race        使用 race detector 运行全部测试' \
		'  make test-multiplayer 运行 M3C 八玩家与 v6 报告测试' \
		'  make bench-multiplayer 运行三组 M3C 多人微基准' \
		'  make archcheck        验证依赖闭包与无图形服务端边界' \
		'  make rust             构建固定版本的 Rust cdylib' \
		'  make rust-check       运行 Rust 格式、clippy 与单测' \
		'  make fmt              格式化全部 Rust 与 Go 源码' \
		'  make visual-check     跑视觉场景并与 golden 基线比对' \
		'  make visual-update    重新生成 golden 基线（VISUAL_OUT 覆盖输出目录）' \
		'  make clean            删除 bin 目录' \
		'  make help             显示此帮助'

run:
	$(GO) run $(APP) $(ARGS)

rust:
	cd $(RUST_DIR) && $(CARGO) build --locked --release

rust-check:
	cd $(RUST_DIR) && $(CARGO) fmt --check
	cd $(RUST_DIR) && $(CARGO) clippy --workspace --all-targets -- -D warnings
	cd $(RUST_DIR) && $(CARGO) test --workspace --locked

build:
	@mkdir -p $(dir $(BINARY))
	$(GO) build -ldflags='$(GO_BUILD_LDFLAGS)' -o $(BINARY) $(APP)
	CGO_ENABLED=0 $(GO) build -o $(SERVER_BINARY) $(SERVER)
	cp $(RUST_DYLIB) $(MORNLEA_DYLIB)

test:
	$(GO) test ./...

test-race:
	$(GO) test ./... -race

test-multiplayer:
	$(GO) test ./internal/client ./internal/server ./cmd/mornlea ./cmd/perfcheck \
		-run 'Test(PerfReportV6|ScenarioV6|PerfcheckV6|PerfcheckV5SameScenario|PerformanceThresholds|InterestObserver|HostStats|BenchmarkServerEpoch|BenchmarkServerMeasuredWindow)' -count=1

bench-multiplayer:
	$(GO) test ./internal/network ./internal/server ./internal/render -run '^$$' \
		-bench '(RemotePlayerStateCodec|EightPlayerInterest|RemoteAvatarNameTag)' -benchmem -count=3

archcheck:
	$(GO) test ./internal/archcheck -count=1
	test -z "$$($(GO) list -deps ./cmd/mornlea-server | rg 'internal/(client|mesh|render|gfx)|glfw|webgpu|x/image/font')"

fmt:
	cd $(RUST_DIR) && $(CARGO) fmt
	find . -type f -name '*.go' \
		-not -path './vendor/*' \
		-not -path './.worktrees/*' \
		-exec gofmt -w {} +

visual-check:
	$(GO) run $(APP) --capture $(or $(VISUAL_OUT),build/visual)

visual-update:
	$(GO) run $(APP) --capture $(or $(VISUAL_OUT),build/visual) --update-golden

clean:
	rm -rf bin
