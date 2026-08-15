## 1. Task 2：修复同身份 TCP 重连 fixture

- [x] 1.1 在 `internal/server/daylight_multiplayer_test.go` 的关闭与同身份重连之间复用 `waitForPlayerReleased`，同时等待 Host 的 player/session 两个 active index；不得新增 sleep、重试、生产 hook 或 deadline 变更。
- [x] 1.2 记录 run `31813426121` 的 `0.65s`、`LoginAlreadyOnline/玩家已在线` source RED，并运行 `make rust && go test ./internal/server -run '^TestReconnectContinuesWorldTimeWithoutRollback$' -race -count=100`；移除 barrier 的 mutation 仅作 RED 证据后恢复。

## 2. Task 3：修复健康 Memory endpoint 的 reader 顺序

- [x] 2.1 在 `internal/server/host_lifecycle_test.go` 先 `monitorEndpointProgress/Recv` 并登记 cleanup，后 `waitReady`；保持 `HeartbeatInterval=20ms`、`HeartbeatTimeout=150ms`、`waitDeadline` 与生产代码不变。
- [x] 2.2 记录 run `31813364557` 的 `30.00s`、`player did not become ready` source RED，并运行两个 fixture 的 `go test ./internal/server -run 'Test(ReconnectContinuesWorldTimeWithoutRollback|HostHeartbeatTimeoutCleanupIsIsolated)' -race -count=100` 与 `go test ./internal/server -race -count=1`；reader-after-ready mutation 恢复后不得留在树中。

## 3. Task 4：将唯一 Rust crate/library 改名为 `mornlea_engine`

- [x] 3.1 仅移动 `engine/crates/mornlea_mesh` 到 `engine/crates/mornlea_engine`，更新 Cargo workspace/package、锁文件、install name、Make/cgo/current identity 文档与 Hook fixture；保留 `mornlea_engine_abi_version`、`mornlea_mesh_section`、mesh layout/status `0..9`、算法和 `crate-type`。
- [x] 3.2 在 `internal/archcheck` 建立 identity RED/GREEN，验证一个 std-only crate、无旧 artifact 名和 macOS 相邻 dylib；运行 `make rust`、Cargo fmt/clippy/test/metadata/tree、`go test ./internal/mesh ./internal/client -race -count=1`、`go test ./internal/archcheck -count=1`、`node --test scripts/agent-hooks/guard.test.mjs`、脱离 `engine/target` 参数探针及现有 headless `visual-check`。

## 4. Task 5：建立唯一 `internal/nativeabi` bridge

- [x] 4.1 新建 `internal/nativeabi` 为唯一 engine header/cgo/status 叶子，将 mesh cgo 的 ABI/version/status/slice forwarding 迁入，保留 `internal/mesh` 私有兼容包装和既有错误文本；架构与 Hook 只允许该 leaf 直接接触 C/native symbol。
- [x] 4.2 以 nativeabi/mesh 原子性、overlap、canary、ABI/status、`#cgo noescape`/`nocallback` 审计和 `AllocsPerRun` 覆盖 bridge；运行 `make rust`、`go test ./internal/nativeabi ./internal/mesh ./internal/client -race -count=1`、escape diagnostics、`go test ./internal/archcheck -count=1`、Hook tests、`go vet ./internal/nativeabi ./internal/mesh`。

## 5. Task 6：实现 collision ABI 与 test-only Go oracle parity

- [x] 5.1 在 `engine/include/mornlea_engine.h`、`ffi.rs`、`collision.rs` 与 `internal/nativeabi` 追加 collision v1 entry point；冻结 64/196/16-byte little-endian layout、双层输入校验、catch_unwind、无部分 output 和 ABI v1/status 复用。
- [x] 5.2 在 `internal/physics/*_test.go` 机械保留旧 Go collision/step oracle，并覆盖 Y/X/Z、unknown closed、严格 step、Count 8/9/255 clamp、4096 成功/4097 查询前 panic、并发、逐位 parity、fuzz 与零 allocation；运行 Cargo fmt/clippy/test、focused physics/nativeabi race、fuzz 30s、archcheck 与 targeted mutation RED/GREEN。

## 6. Task 7：将生产 collision 切到 Rust 并交付 Linux server bundle

- [x] 6.1 将 checked collision prism encoder 与一次 native 调用放入 `internal/physics`，保持 `physics.Step` 签名、Go tunable/input/state 所有权和完整成功后发布；删除生产 Go resolver，仅留 `_test.go` oracle，且不添加 fallback/pool/interface。
- [x] 6.2 更新 `Makefile`、`.github/workflows/ci.yml`、archcheck、Hook 和当前文档，提供原生 Ubuntu amd64 `mornlea-server + libmornlea_engine.so` `$ORIGIN` bundle，并证明依赖闭包无 client/mesh/render/gfx/WebGPU/窗口栈。
- [x] 6.3 运行 physics/client/sim/server/commands race、4097 零查询、public unknown mutation、3 项 physics benchmark（5 次）、macOS dylib 脱离 target probe 和 Linux CI job 所需的 symbol/loader/`go list -deps` gate。

## 7. Task 8：将 `core.RaycastBlocks` 的 DDA 切到 Rust

- [x] 7.1 在 header/Rust/nativeabi 追加独立 raycast v1 batch ABI，冻结 40-byte input、64-byte cursor、20-byte record 与 1280-byte output；实现 ABI 原子性、cursor/output 本地发布、64-record 上限和无跨调用所有权。
- [x] 7.2 用 test-only Go DDA oracle 覆盖 origin/signed-zero、负坐标 floor、严格 XYZ tie、精确 endpoint、int32 wrapping、多 batch、callback 懒惰与 error identity；生产 `core.RaycastBlocks` 仅保留既有校验/一次归一化/Point 并逐 record callback，不留 Go traversal fallback。
- [x] 7.3 运行 `make rust-check`、nativeabi/core race、raycast fuzz 30s、`BenchmarkRaycastBlocks` 5 次、downstream race、archcheck/Hook/vet/gofmt，并分别证明 tie、endpoint、cursor 与 callback-consumption mutation RED 后恢复 GREEN。

## 8. Task 9：冻结 HEAD 并生成累计正确性、视觉与性能证据

- [ ] 8.1 在所有重型命令前冻结 clean producer HEAD，逐字节核对 M2 v15/M5 v14 baseline JSON、11 张 golden 与 scenario v16 身份；不更新 baseline、golden、threshold 或 fixture。
- [ ] 8.2 在同一 producer 上运行 `make rust-check`、native/core/physics/mesh race、两个 CI fixture `-race -count=100`、server/client/sim race、archcheck、Hook、三项 fuzz、mesh/physics/raycast benchmark 各 5 次、scenario v16 Memory/TCP 报告和 11 个 headless visual-check；性能仅记录，完整性/overflow/数据丢失/I/O/native loading/正确性仍为门禁。
- [ ] 8.3 验证 macOS 四 symbol、相邻 dylib、`@loader_path`、脱离 Cargo target 参数探针，并在 tracked provenance note 记录 producer、命令与输出；报告文件保持 ignored。

## 9. Task 10：完成累计独立评审与本地 gates

- [ ] 9.1 使用独立只读评审核验两条 happens-before、单 std-only `mornlea_engine`、ABI v1/nativeabi leaf、collision/raycast layouts/atomicity/parity/laziness、macOS/Linux bundle、无图形专服和所有冻结 identity；对每项 finding 先复现或证伪，任何生产修复按范围重新冻结并重跑 Task 8 证据。
- [ ] 9.2 仅在 Critical/Important/Minor 均为零后，运行 `gofmt -l .`、`go vet ./...`、`go test ./... -race`、`openspec validate rust-engine-collision-raycast-ci-stability --strict --no-interactive`、`openspec validate --all --strict --no-interactive`，勾选全部本地任务并确认只剩下 10.1 的真实 Linux PR-CI gate；archive 不是 active checkbox。

## 10. Task 11：经授权验证真实 Linux PR-CI

- [ ] 10.1 在用户明确授权发布 draft PR 后，确认 exact HEAD 的 macOS `test` 与 Ubuntu `linux-server` 均成功；日志必须证明 `libmornlea_engine.so`、`$ORIGIN`、脱离 target 参数解析和两条历史 flaky tests 均通过。此项完成后才可同步主 specs 并用官方 archive workflow 归档；不得自行 push、merge 或 archive。
