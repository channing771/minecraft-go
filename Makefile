.DEFAULT_GOAL := help

GO := go
APP := ./cmd/mcgo
BINARY := bin/mcgo
ARGS ?=

.PHONY: help run build test fmt clean

help:
	@printf '%s\n' \
		'常用命令：' \
		'  make run              运行游戏，可通过 ARGS 传递参数' \
		'  make build            构建客户端到 bin/mcgo' \
		'  make test             运行全部测试' \
		'  make fmt              格式化全部 Go 源码' \
		'  make clean            删除 bin 目录' \
		'  make help             显示此帮助'

run:
	$(GO) run $(APP) $(ARGS)

build:
	@mkdir -p $(dir $(BINARY))
	$(GO) build -o $(BINARY) $(APP)

test:
	$(GO) test ./...

fmt:
	find . -type f -name '*.go' \
		-not -path './vendor/*' \
		-not -path './.worktrees/*' \
		-exec gofmt -w {} +

clean:
	rm -rf bin
