.DEFAULT_GOAL := help

GO := go
CARGO := cargo
RUST_MANIFEST := engine/Cargo.toml
APP := ./cmd/mcgo
BINARY := bin/mcgo
ARGS ?=

.PHONY: help run build test test-multiplayer bench-multiplayer archcheck fmt clean visual-check visual-update rust

help:
	@printf '%s\n' \
		'常用命令：' \
		'  make run              运行游戏，可通过 ARGS 传递参数' \
		'  make build            构建客户端到 bin/mcgo' \
		'  make test             运行全部测试' \
		'  make test-multiplayer 运行 M3C 八玩家与 v6 报告测试' \
		'  make bench-multiplayer 运行三组 M3C 多人微基准' \
		'  make archcheck        验证依赖闭包与无图形服务端边界' \
		'  make fmt              格式化全部 Go 源码' \
		'  make visual-check     跑视觉场景并与 golden 基线比对' \
		'  make visual-update    重新生成 golden 基线（VISUAL_OUT 覆盖输出目录）' \
		'  make clean            删除 bin 目录' \
		'  make help             显示此帮助'

run:
	$(GO) run $(APP) $(ARGS)

rust:
	$(CARGO) build --manifest-path $(RUST_MANIFEST) --locked --release

build:
	@mkdir -p $(dir $(BINARY))
	$(GO) build -o $(BINARY) $(APP)

test:
	$(GO) test ./...

test-multiplayer:
	$(GO) test ./internal/client ./internal/server ./cmd/mcgo ./cmd/perfcheck \
		-run 'Test(PerfReportV6|ScenarioV6|PerfcheckV6|PerfcheckV5SameScenario|PerformanceThresholds|InterestObserver|HostStats|BenchmarkServerEpoch|BenchmarkServerMeasuredWindow)' -count=1

bench-multiplayer:
	$(GO) test ./internal/network ./internal/server ./internal/render -run '^$$' \
		-bench '(RemotePlayerStateCodec|EightPlayerInterest|RemoteAvatarNameTag)' -benchmem -count=3

archcheck:
	$(GO) test ./internal/archcheck -count=1
	test -z "$$($(GO) list -deps ./cmd/mcgod | rg 'internal/(client|render|gfx)|glfw|webgpu|x/image/font')"

fmt:
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
