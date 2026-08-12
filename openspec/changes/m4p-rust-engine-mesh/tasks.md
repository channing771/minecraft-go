## 2. pinned Cargo、ABI identity 与最小链接闭环

- [x] 2.1 创建 `engine/Cargo.toml`、`engine/Cargo.lock`、`engine/rust-toolchain.toml`、`engine/crates/mcgo_mesh/Cargo.toml`、`engine/crates/mcgo_mesh/src/lib.rs`、`engine/crates/mcgo_mesh/src/ffi.rs`、`engine/include/mcgo_engine.h`、`internal/mesh/native_abi.go` 与 `internal/mesh/native_abi_test.go`，并修改 `.gitignore`、`Makefile` 与本 `tasks.md`，建立 pinned Cargo、ABI identity 和最小静态链接闭环。验证：`make rust && cargo test --manifest-path engine/Cargo.toml --workspace --locked && go test ./internal/mesh -run '^TestNativeABIVersionMatchesGo$' -count=1`。

## 3. registry snapshot 与版本化 neighborhood 输入

- [ ] 3.1 创建 `engine/crates/mcgo_mesh/src/input.rs`、`internal/mesh/registry.go`、`internal/mesh/native_input.go` 与 `internal/mesh/native_input_test.go`，并修改 `engine/crates/mcgo_mesh/src/lib.rs`、`engine/crates/mcgo_mesh/src/ffi.rs`、`engine/include/mcgo_engine.h`、`internal/mesh/greedy.go`、`internal/assets/blocks.go`、`internal/assets/blocks_test.go`、`internal/mesh/greedy_test.go`、`internal/mesh/light_internal_test.go`、`internal/mesh/light_test.go` 与本 `tasks.md`，实现可验证的 registry snapshot 与版本化 neighborhood input。验证：`make rust && cargo test --manifest-path engine/Cargo.toml --workspace --locked && go test ./internal/assets ./internal/mesh -run 'Registry|NativeInput' -count=1 && go test ./internal/mesh ./internal/assets -race -count=1`。

## 4. Rust 有界天空光与方块光

- [ ] 4.1 创建 `engine/crates/mcgo_mesh/src/light.rs`，并修改 `engine/crates/mcgo_mesh/src/lib.rs`、`engine/crates/mcgo_mesh/src/input.rs`、`engine/crates/mcgo_mesh/src/ffi.rs` 与本 `tasks.md`，实现固定容量的 Rust 天空光和方块光。验证：`cargo test --manifest-path engine/Cargo.toml -p mcgo_mesh light && make rust && go test ./internal/mesh -run '^TestNativeInputValidAirNeighborhoodReturnsZeroQuads$' -count=1`。

## 5. Rust AO、greedy mesh 与 packed quad

- [ ] 5.1 创建 `engine/crates/mcgo_mesh/src/quad.rs` 与 `engine/crates/mcgo_mesh/src/greedy.rs`，并修改 `engine/crates/mcgo_mesh/src/lib.rs`、`engine/crates/mcgo_mesh/src/ffi.rs`、`engine/include/mcgo_engine.h` 与本 `tasks.md`，实现 Rust AO、greedy mesh 和精确 packed quad 输出。验证：`cargo test --manifest-path engine/Cargo.toml --workspace --locked && make rust && go test ./internal/mesh -run 'NativeABI|NativeInput' -count=1`。

## 6. Go 生产 MeshSection 切到 Rust，保留 test-only oracle

- [ ] 6.1 创建 `internal/mesh/native.go`、`internal/mesh/greedy_oracle_test.go` 与 `internal/mesh/light_oracle_test.go`，并修改 `internal/mesh/native_abi.go`、`internal/mesh/native_input.go`、`internal/mesh/greedy.go`、`internal/mesh/light.go`、`internal/mesh/light_internal_test.go` 与本 `tasks.md`；仅在 constructor 签名要求时修改 `internal/client/mesher_worker.go` 和 `cmd/gfxspike/main.go`，使 Rust 成为生产 `MeshSection` 且 Go 仅作 test-only oracle。验证：`make rust && go test ./internal/mesh -race -count=1 && go test ./internal/client -run 'Mesher|Light|Dirty' -race -count=1 && go test ./internal/render -run 'Mesh|Light|Cull' -race -count=1 && go test ./cmd/gfxspike -count=1 && go test ./internal/archcheck -count=1`。

## 7. 跨语言 parity、错误原子性与并发

- [ ] 7.1 创建 `internal/mesh/native_parity_test.go`，并修改 `internal/mesh/native_abi_test.go`、`engine/crates/mcgo_mesh/src/ffi.rs`、`engine/crates/mcgo_mesh/src/input.rs`、`engine/crates/mcgo_mesh/src/light.rs`、`engine/crates/mcgo_mesh/src/greedy.rs`、`engine/crates/mcgo_mesh/src/quad.rs` 与本 `tasks.md`，覆盖逐位 parity、原子错误与多 worker 并发。验证：`make rust && cargo test --manifest-path engine/Cargo.toml --workspace --locked && go test ./internal/mesh -run 'Parity|Native|Light|Mesh' -race -count=1 && go test ./internal/mesh -run '^$' -fuzz FuzzNativeMeshRejectsMalformedInput -fuzztime=10s`。

## 8. Make、CI、Hook 与开发文档

- [ ] 8.1 修改 `Makefile`、`.github/workflows/ci.yml`、`scripts/agent-hooks/guard.mjs`、`scripts/agent-hooks/guard.test.mjs`、`AGENTS.md`、`CLAUDE.md`、`README.md`、`openspec/config.yaml` 与本 `tasks.md`，令 Make、CI、Hook 与开发文档一致采用 Rust-first canonical 构建，且保持 Linux 无 CGO 服务端独立。验证：`make rust-check && make test && make test-race && node --test scripts/agent-hooks/guard.test.mjs && CGO_ENABLED=0 GOOS=linux go build ./cmd/mcgod && cmp AGENTS.md CLAUDE.md && openspec validate --all --strict --no-interactive`。

## 9. 下游保真、全仓门禁与独立评审

- [ ] 9.1 完成下游保真、全仓门禁和独立评审；所有 gates/review 后只修改本 `tasks.md`，并在本计划 SDD workspace 写入 ignored `task-9-report.md`。验证：`make rust-check && make rust && go test ./internal/mesh ./internal/assets ./internal/client ./internal/render -race -count=1 && go test ./cmd/mcgo -run 'Capture|BlockLightRoom|MaterialsShowcase' -race -count=1 && go test ./internal/mesh -run '^$' -fuzz FuzzNativeMeshRejectsMalformedInput -fuzztime=10s && go test ./internal/archcheck -count=1 && CGO_ENABLED=0 GOOS=linux go build ./cmd/mcgod && test -z "$(CGO_ENABLED=0 GOOS=linux go list -deps ./cmd/mcgod | rg 'internal/(client|mesh|render|gfx)|glfw|webgpu|x/image/font')" && M4P_VISUAL_OUT=$(mktemp -d /private/tmp/mcgo-m4p-rust-mesh-visual.XXXXXX) && VISUAL_OUT="$M4P_VISUAL_OUT" make visual-check && go test ./internal/mesh -run '^$' -bench BenchmarkMeshTerrainSection -benchmem -count=5 && go test ./internal/render -run '^$' -bench 'Mesh|Light' -benchmem -count=3 && make test-race && go vet ./... && gofmt -l . && cargo fmt --manifest-path engine/Cargo.toml --check && openspec validate --all --strict --no-interactive && git diff --check && cmp AGENTS.md CLAUDE.md`。
