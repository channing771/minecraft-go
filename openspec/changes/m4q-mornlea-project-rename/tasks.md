## 4. Config 默认路径迁移

- [x] 4.1 以 TDD 修改 `internal/config/config.go`、创建 `internal/config/migration_test.go`，实现新路径优先、旧路径校验与规范化复制、0700/0600 权限、同目录临时文件 + `os.Link` no-clobber、赢家重读、仅 publisher 成功日志和失败清理；保持显式 `Load`/`Save` replace 语义及本任务内旧 `DefaultPath`，并只随本项更新本 `tasks.md`。Focused：`go test ./internal/config -run 'LoadDefault|DefaultPath' -count=1`；`go test ./internal/config -run 'UnsafeParent|UnsafeTarget|UnsafeConcurrentWinner|ConcurrentWinner|PublishFailure|PublishConfigExclusively' -race -count=1`；`go test ./internal/config -race -count=1`；`go test ./internal/archcheck -count=1`；`go vet ./internal/config`；`gofmt -w internal/config/config.go internal/config/migration_test.go`；`gofmt -l internal/config`；`openspec validate --all --strict --no-interactive`；`git diff --check`。Ignored report：`.superpowers/sdd/2026-08-12-m4q-mornlea-project-rename/task-4-report.md`。

## 5. Profile 默认迁移与并发创建

- [x] 5.1 以 TDD 修改 `internal/profile/profile.go`、`internal/profile/atomic.go`、`internal/profile/profile_test.go`，复用既有临时写入/fsync 流程实现 legacy profile 身份保真迁移、exclusive 首次创建、并发 loser 重读、requested-name replace-on-save、0700/0600 权限、仅 publisher 成功日志及失败不生成 PlayerID；保持自定义 path 与本任务内旧 `DefaultPath`，并只随本项更新本 `tasks.md`。Focused：`go test ./internal/profile -run 'DefaultPath|LoadOrCreateDefault|CustomPathSkips' -count=1`；`go test ./internal/profile -run 'Concurrent|PublishFailure|UnsafeParent|UnsafeTarget|MissingBoth' -race -count=1`；`go test ./internal/profile -race -count=1`；`go test ./internal/archcheck -count=1`；`go vet ./internal/profile`；`gofmt -w internal/profile/profile.go internal/profile/atomic.go internal/profile/profile_test.go`；`gofmt -l internal/profile`；`openspec validate --all --strict --no-interactive`；`git diff --check`。Ignored report：`.superpowers/sdd/2026-08-12-m4q-mornlea-project-rename/task-5-report.md`。

## 6. 客户端与专用服务端迁移路由

- [x] 6.1 以 TDD 原子修改 `internal/config/config.go`、`internal/config/migration_test.go`、`internal/profile/profile.go`、`internal/profile/profile_test.go`、`cmd/mcgo/main.go`、`cmd/mcgo/options.go`、`cmd/mcgo/run_test.go`、`cmd/mcgod/main.go`、`cmd/mcgod/main_test.go`：同时把两个公共 `DefaultPath` 切到 `mornlea`，让默认加载走迁移 API、显式 `--config` 跳过迁移、benchmark/capture config 保持编译默认值、材料迁移保持早返回；持久化 Task 7 初始 HEAD 与包含 Tasks 4–6 新测试的 entry manifest，并只随本项更新本 `tasks.md`。Ignored report：`.superpowers/sdd/2026-08-12-m4q-mornlea-project-rename/task-6-report.md`。

Focused commands（按顺序直接运行）：

```bash
make rust
go test ./internal/config ./internal/profile -run 'DefaultPathUsesMornlea|DefaultProfilePathUsesMornlea' -count=1
go test ./cmd/mcgo -run 'ResolveConfig|LoadApplicationIdentity|IgnoresUserConfig' -count=1
```

```bash
go test ./cmd/mcgod -run 'ResolveConfig|MigrateMaterialsReturnsBeforeConfig' -count=1
```

```bash
make rust
go test ./cmd/mcgo ./cmd/mcgod -run 'ResolveConfig|LoadApplicationIdentity|IgnoresUserConfig|MigrateMaterialsReturnsBeforeConfig' -race -count=1
go test ./internal/config ./internal/profile -race -count=1
gofmt -w internal/config/config.go internal/config/migration_test.go internal/profile/profile.go internal/profile/profile_test.go cmd/mcgo/main.go cmd/mcgo/options.go cmd/mcgo/run_test.go cmd/mcgod/main.go cmd/mcgod/main_test.go
gofmt -l cmd/mcgo cmd/mcgod
git diff --check
```

```bash
make rust
go test ./cmd/mcgo ./cmd/mcgod ./internal/config ./internal/profile -race -count=1
go test ./internal/archcheck -count=1
go vet ./cmd/mcgo ./cmd/mcgod ./internal/config ./internal/profile
openspec validate --all --strict --no-interactive
git add internal/config/config.go internal/config/migration_test.go internal/profile/profile.go internal/profile/profile_test.go cmd/mcgo/main.go cmd/mcgo/options.go cmd/mcgo/run_test.go cmd/mcgod/main.go cmd/mcgod/main_test.go openspec/changes/m4q-mornlea-project-rename/tasks.md
git diff --cached --check
git commit -m "feat: 接入 Mornlea 默认数据迁移"
```

```bash
mornlea_invariants="$(git rev-parse --git-common-dir)/mornlea-m4q-evidence"
rg -n '^func (Test|Benchmark|Fuzz)[A-Za-z0-9_]+' --glob '*_test.go' \
  | sed -E 's/:([0-9]+):func /:/' \
  | sed -E 's/\(.*//' \
  | LC_ALL=C sort > "$mornlea_invariants/task6-test-entries"
git rev-parse HEAD > "$mornlea_invariants/task6-head"
```

## 7. 原子切换项目身份

- [x] 7.1 在一个可构建提交中修改 `go.mod` 与所有精确导入 `minecraft-go/internal/...` 的 tracked Go 文件；移动 `cmd/mcgo/**` → `cmd/mornlea/**`、`cmd/mcgod/**` → `cmd/mornlea-server/**`、`engine/crates/mcgo_mesh/**` → `engine/crates/mornlea_mesh/**`、`engine/include/mcgo_engine.h` → `engine/include/mornlea_engine.h`；修改 `engine/Cargo.toml`、`engine/Cargo.lock`、`engine/crates/mornlea_mesh/Cargo.toml`、`engine/crates/mornlea_mesh/build.rs`、`engine/crates/mornlea_mesh/src/ffi.rs`、`internal/mesh/native_abi.go`、`Makefile`、`.github/workflows/ci.yml`、`.gitignore`、`.codex/hooks.json`、`scripts/agent-hooks/guard.mjs`、`scripts/agent-hooks/guard.test.mjs`、`internal/archcheck/helpers_test.go`、`internal/archcheck/platform_test.go`、`internal/archcheck/source_guards_test.go`、`cmd/gfxspike/main.go`、`internal/client/perf.go`、`internal/logging/logging.go`、`internal/render/avatar_test.go`、`internal/storage/region_crash_test.go`；创建 `internal/archcheck/identity_test.go`，并只随本项更新本 `tasks.md`。保持 ABI version/status、算法、Go API、fixture/golden/baseline 与测试入口清单，除 6 项精确重命名和新增 `TestMornleaCurrentIdentity`。Ignored report：`.superpowers/sdd/2026-08-12-m4q-mornlea-project-rename/task-7-report.md`。

`TestMornleaCurrentIdentity` 必须成为 current code/build 的最终机械身份门禁，不得整文件排除 8 个 compatibility allowlist 文件。除 design 中既有精确用途外，只新增 `cmd/mornlea/run_test.go:legacyDataPath` 与 `cmd/mornlea-server/main_test.go:legacyConfigPath` 两个 helper declaration tuple，各允许一个完整 `minecraft-go` string literal；前者只供两个 client default-migration tests，后者只供 server default-migration test，禁止在调用点重复或通过拼接/转义隐藏。完整 allowlist 固定为 41 个 tuple、45 个 expected matches。门禁必须扫描 design 中的完整 roots、逐个解析每个大小写不敏感旧 token match，并用硬编码 allowlist tuple 断言精确 path、完整 string literal、Go AST string-literal span、所在声明/测试用途和正整数 expected count；每个 tuple 的实际计数必须精确相等。任何未消费、额外、缺失、移动、comment/identifier match，或旧 import、command、symbol、env match 均失败；expected count 不得从待测源码动态生成。Task 8 的独立 `git grep` 只负责该测试未覆盖的当前 docs。

Focused commands（按顺序直接运行）：

```bash
test "$(git status --porcelain | wc -l | tr -d ' ')" = 0
mornlea_invariants="$(git rev-parse --git-common-dir)/mornlea-m4q-evidence"
test "$(git rev-parse HEAD)" = "$(cat "$mornlea_invariants/task6-head")"
old_import_count=$(git grep -l '"minecraft-go/internal/' -- '*.go' | wc -l | tr -d ' ')
test "$old_import_count" -gt 0
git ls-files 'cmd/mcgo/**' | LC_ALL=C sort > "$mornlea_invariants/client-files.before"
git ls-files 'cmd/mcgod/**' | LC_ALL=C sort > "$mornlea_invariants/server-files.before"
git ls-files 'engine/crates/mcgo_mesh/**' | LC_ALL=C sort > "$mornlea_invariants/rust-files.before"

cp "$mornlea_invariants/task6-test-entries" "$mornlea_invariants/test-entries.before"

for file in cmd/mcgo/testdata/golden/*.png; do
  hash=$(shasum -a 256 "$file" | awk '{print $1}')
  printf '%s  %s\n' "$hash" "${file##*/}"
done | LC_ALL=C sort > "$mornlea_invariants/goldens.before"
cmp "$mornlea_invariants/golden.sha256" "$mornlea_invariants/goldens.before"
```

```bash
go test ./internal/archcheck -run 'Mornlea|Identity' -count=1
```

```bash
test ! -e cmd/mcgo
test ! -e cmd/mcgod
test ! -e engine/crates/mcgo_mesh
test ! -e engine/include/mcgo_engine.h
test -d cmd/mornlea
test -d cmd/mornlea-server
test -d engine/crates/mornlea_mesh
test -f engine/include/mornlea_engine.h
test -z "$(git grep -l '"minecraft-go/internal/' -- '*.go' || true)"
```

```bash
mornlea_invariants="$(git rev-parse --git-common-dir)/mornlea-m4q-evidence"
make rust

for file in cmd/mornlea/testdata/golden/*.png; do
  hash=$(shasum -a 256 "$file" | awk '{print $1}')
  printf '%s  %s\n' "$hash" "${file##*/}"
done | LC_ALL=C sort > "$mornlea_invariants/goldens.after"
cmp "$mornlea_invariants/goldens.before" "$mornlea_invariants/goldens.after"

sed 's#^cmd/mcgo/#cmd/mornlea/#' "$mornlea_invariants/client-files.before" > "$mornlea_invariants/client-files.expected"
git ls-files 'cmd/mornlea/**' | LC_ALL=C sort > "$mornlea_invariants/client-files.after"
diff -u "$mornlea_invariants/client-files.expected" "$mornlea_invariants/client-files.after"
sed 's#^cmd/mcgod/#cmd/mornlea-server/#' "$mornlea_invariants/server-files.before" > "$mornlea_invariants/server-files.expected"
git ls-files 'cmd/mornlea-server/**' | LC_ALL=C sort > "$mornlea_invariants/server-files.after"
diff -u "$mornlea_invariants/server-files.expected" "$mornlea_invariants/server-files.after"
sed 's#^engine/crates/mcgo_mesh/#engine/crates/mornlea_mesh/#' "$mornlea_invariants/rust-files.before" > "$mornlea_invariants/rust-files.expected"
git ls-files 'engine/crates/mornlea_mesh/**' | LC_ALL=C sort > "$mornlea_invariants/rust-files.after"
diff -u "$mornlea_invariants/rust-files.expected" "$mornlea_invariants/rust-files.after"

sed \
  -e 's#cmd/mcgo/#cmd/mornlea/#g' \
  -e 's#cmd/mcgod/#cmd/mornlea-server/#g' \
  -e 's/TestMCGodHasNoGraphicsDependencies/TestMornleaServerHasNoGraphicsDependencies/' \
  -e 's/TestMcgoUsesLoginStreamsInsteadOfAttachedServerEndpoints/TestMornleaUsesLoginStreamsInsteadOfAttachedServerEndpoints/' \
  -e 's/TestMcgoBenchmarkTCPPathUsesTheSharedLoginStateMachine/TestMornleaBenchmarkTCPPathUsesTheSharedLoginStateMachine/' \
  -e 's/TestMCGodProcessReleasesWorldLockAfterSIGTERM/TestMornleaServerProcessReleasesWorldLockAfterSIGTERM/' \
  -e 's/TestMCGodProcessSaveFailureExitsNonzero/TestMornleaServerProcessSaveFailureExitsNonzero/' \
  -e 's/TestMCGodProcess/TestMornleaServerProcess/' \
  "$mornlea_invariants/test-entries.before" > "$mornlea_invariants/test-entries.expected"
printf '%s\n' 'internal/archcheck/identity_test.go:TestMornleaCurrentIdentity' \
  >> "$mornlea_invariants/test-entries.expected"
LC_ALL=C sort -o "$mornlea_invariants/test-entries.expected" "$mornlea_invariants/test-entries.expected"

rg -n '^func (Test|Benchmark|Fuzz)[A-Za-z0-9_]+' --glob '*_test.go' \
  | sed -E 's/:([0-9]+):func /:/' \
  | sed -E 's/\(.*//' \
  | LC_ALL=C sort > "$mornlea_invariants/test-entries.after"
diff -u "$mornlea_invariants/test-entries.expected" "$mornlea_invariants/test-entries.after"

go list ./...
go test ./cmd/mornlea ./cmd/mornlea-server ./internal/mesh ./internal/archcheck -race -count=1
node --test scripts/agent-hooks/guard.test.mjs
```

```bash
make rust-check
make build
test -x bin/mornlea
test -x bin/mornlea-server
test -f bin/libmornlea_mesh.dylib
otool -D bin/libmornlea_mesh.dylib | rg -Fx '@rpath/libmornlea_mesh.dylib'
otool -L bin/mornlea | rg -F '@rpath/libmornlea_mesh.dylib'
otool -l bin/mornlea | rg -F 'path @loader_path'
nm -gU bin/libmornlea_mesh.dylib | rg 'mornlea_(engine_abi_version|mesh_section)'
! nm -gU bin/libmornlea_mesh.dylib | rg 'mcgo_'
go test ./internal/mesh -run 'Native|Parity|MeshSection' -race -count=1

CGO_ENABLED=0 GOOS=linux go build -o /private/tmp/mornlea-server ./cmd/mornlea-server
test -z "$(CGO_ENABLED=0 GOOS=linux go list -deps ./cmd/mornlea-server | rg 'internal/(client|mesh|render|gfx)|glfw|webgpu|x/image/font')"
```

```bash
go test ./internal/archcheck -run 'WebGPU|Server|LoginStreams' -count=1
go test ./internal/archcheck -run '^TestMornleaCurrentIdentity$' -count=1
```

```bash
gofmt -w internal/archcheck/identity_test.go $(git diff --name-only --diff-filter=ACMR -- '*.go')
cargo fmt --manifest-path engine/Cargo.toml -- --check
cargo clippy --manifest-path engine/Cargo.toml --workspace --all-targets --locked -- -D warnings
go test ./cmd/mornlea ./cmd/mornlea-server ./internal/mesh ./internal/archcheck -race -count=1
go vet ./...
test -z "$(gofmt -l .)"
openspec validate --all --strict --no-interactive
git diff --check
git add -A -- go.mod cmd engine internal Makefile .github .gitignore .codex scripts openspec/changes/m4q-mornlea-project-rename/tasks.md
git diff --cached --check
git commit -m "refactor: 切换 Mornlea 项目身份"
```

## 8. 当前文档与迁移说明

- [ ] 8.1 修改 `README.md`、`AGENTS.md`、`CLAUDE.md`、`openspec/config.yaml`、`docs/openspec.md`、`docs/notes/lan-server.md`，创建 `docs/notes/mornlea-migration.md`，并只随本项更新本 `tasks.md`；统一当前产品/module/命令/artifact/路径/Hook 身份，保持 `AGENTS.md` 与 `CLAUDE.md` 相同，以单一迁移说明记录新旧映射、只复制不删除、回退单向性和历史资料边界，不改 `docs/superpowers/**`、归档 OpenSpec 或历史性能证据。Ignored report：`.superpowers/sdd/2026-08-12-m4q-mornlea-project-rename/task-8-report.md`。

Focused commands（按顺序直接运行）：

```bash
cmp AGENTS.md CLAUDE.md
current_doc_matches=$(mktemp /private/tmp/mornlea-doc-matches.XXXXXX)
git grep -n -I -i -E 'minecraft[-_]go|mcgo' -- \
  README.md AGENTS.md CLAUDE.md openspec/config.yaml docs/openspec.md docs/notes/lan-server.md \
  > "$current_doc_matches" || test $? = 1
historical_design_link='docs/superpowers/specs/2026-07-26-minecraft-go-design.md'
test "$(rg -o -F "$historical_design_link" "$current_doc_matches" | wc -l | tr -d ' ')" = 1
old_doc_tokens=$(rg -o -i 'minecraft[-_]go|mcgo' "$current_doc_matches")
test "$(printf '%s\n' "$old_doc_tokens" | wc -l | tr -d ' ')" = 1
test "$old_doc_tokens" = 'minecraft-go'
```

```bash
make rust
make help
set +e
go run ./cmd/mornlea-server -h >/private/tmp/mornlea-server-help.txt 2>&1
server_help_rc=$?
set -e
test "$server_help_rc" = 1
rg -F 'flag: help requested' /private/tmp/mornlea-server-help.txt
go test ./cmd/mornlea ./cmd/mornlea-server ./internal/archcheck -count=1
openspec validate --all --strict --no-interactive
git diff --check
```

```bash
git add README.md AGENTS.md CLAUDE.md openspec/config.yaml docs/openspec.md docs/notes/lan-server.md docs/notes/mornlea-migration.md openspec/changes/m4q-mornlea-project-rename/tasks.md
git diff --cached --check
git commit -m "docs: 更新 Mornlea 当前入口"
```

## 9. 不变量、全仓门禁与独立评审

- [ ] 9.1 以 Tasks 1–8 producer HEAD 为唯一被验收实现：除经 M4Q plan update 明确批准的缺陷修复外不改生产文件；门禁后仅修改 `openspec/changes/m4q-mornlea-project-rename/design.md` 写入 Task 1 merge/origin heads、静态/golden manifest hashes、Tasks 1–8 producer HEAD、performance report hash 与 embedded producer HEAD、精确双基线视觉证据、archive state 和仓库外操作权限事实。发布仅含 `design.md` 的证据提交后，请求独立 broad review，并停在 `READY_FOR_REVIEW`。Ignored report：`.superpowers/sdd/2026-08-12-m4q-mornlea-project-rename/task-9-report.md`。

Focused commands（按顺序直接运行）：

```bash
git status --short --branch
test -z "$(git status --porcelain)"
mornlea_invariants="$(git rev-parse --git-common-dir)/mornlea-m4q-evidence"
task1_head=$(cat "$mornlea_invariants/task1-head")
test -n "$task1_head"
git rev-parse HEAD > "$mornlea_invariants/task9-producer-head"
shasum -a 256 -c "$mornlea_invariants/static.sha256"

for file in cmd/mornlea/testdata/golden/*.png; do
  hash=$(shasum -a 256 "$file" | awk '{print $1}')
  printf '%s  %s\n' "$hash" "${file##*/}"
done | LC_ALL=C sort > "$mornlea_invariants/golden-after.sha256"
diff -u "$mornlea_invariants/golden.sha256" "$mornlea_invariants/golden-after.sha256"

test -z "$(git diff --name-only "$task1_head"..HEAD -- \
  'internal/network/testdata/**' 'internal/storage/testdata/**' \
  'internal/worldgen/testdata/**' 'docs/notes/perf-baseline*.json')"
```

```bash
make rust
go test ./internal/network -run 'Protocol|Golden|Fixture' -race -count=1
go test ./internal/storage -run 'Fixture|Golden|Schema|Magic|Future' -race -count=1
go test ./cmd/mornlea -run 'ScenarioV15|BenchmarkScenarioVersion|Capture' -race -count=1
go test ./internal/mesh -run 'Native|Parity|ABI|Light|Mesh' -race -count=1
```

```bash
mornlea_invariants="$(git rev-parse --git-common-dir)/mornlea-m4q-evidence"
go test ./internal/network -run '^$' -fuzz '^FuzzSmallPacketCodec$' -fuzztime=10s
go test ./internal/storage -run '^$' -fuzz '^FuzzDecodeChunkPayload$' -fuzztime=10s
go test ./internal/mesh -run '^$' -fuzz '^FuzzNativeMeshRejectsMalformedInput$' -fuzztime=10s

current_visual=$(mktemp -d /private/tmp/mornlea-m4q-visual.XXXXXX)
current_home=$(mktemp -d /private/tmp/mornlea-m4q-home.XXXXXX)
hardware_chip="$(system_profiler SPHardwareDataType | sed -n 's/^[[:space:]]*Chip: //p')"
test -n "$hardware_chip"
printf '视觉验证硬件 Chip: %s\n' "$hardware_chip"
current_profile_dir="$current_home/Library/Application Support/mornlea"
mkdir -p "$current_profile_dir"
chmod 0700 "$current_profile_dir"
printf '%s' '{"version":1,"player_id":"00112233-4455-4677-8899-aabbccddeeff","display_name":"Capture"}' > "$current_profile_dir/profile.json"
chmod 0600 "$current_profile_dir/profile.json"
set +e
HOME="$current_home" VISUAL_OUT="$current_visual" make visual-check > "$current_visual/run.log" 2>&1
current_visual_rc=$?
set -e

baseline_visual=$(mktemp -d /private/tmp/mornlea-m4q-main-visual.XXXXXX)
baseline_home=$(mktemp -d /private/tmp/mornlea-m4q-main-home.XXXXXX)
baseline_profile_dir="$baseline_home/Library/Application Support/minecraft-go"
mkdir -p "$baseline_profile_dir"
chmod 0700 "$baseline_profile_dir"
cp "$current_profile_dir/profile.json" "$baseline_profile_dir/profile.json"
chmod 0600 "$baseline_profile_dir/profile.json"
(
  baseline_tree=$(mktemp -d /private/tmp/mornlea-m4q-main-tree.XXXXXX)
  rmdir "$baseline_tree"
  restore_tree() {
    test ! -e "$baseline_tree/.git" || git worktree remove "$baseline_tree"
  }
  trap 'restore_tree' EXIT
  trap 'restore_tree; exit 129' HUP
  trap 'restore_tree; exit 130' INT
  trap 'restore_tree; exit 143' TERM
  git worktree add --detach "$baseline_tree" "$(cat "$mornlea_invariants/task1-origin-main")"
  (
    cd "$baseline_tree"
    set +e
    HOME="$baseline_home" VISUAL_OUT="$baseline_visual" make visual-check > "$baseline_visual/run.log" 2>&1
    baseline_visual_rc=$?
    printf '%s\n' "$baseline_visual_rc" > "$baseline_visual/baseline_visual_rc"
    set -e
  )
  restore_tree
  trap - EXIT HUP INT TERM
)
test -f "$baseline_visual/baseline_visual_rc"
baseline_visual_rc=$(cat "$baseline_visual/baseline_visual_rc")
test -n "$baseline_visual_rc"
test "$baseline_visual_rc" -ge 0

for scene in terrain-noon hud-hotbar-health avatar-nametag inventory-crafting debug-panel skylight-tunnel block-light-room materials-showcase target-block-feedback oak-grove; do
  cmp "$current_visual/${scene}.png" "$baseline_visual/${scene}.png"
done
case "$hardware_chip" in
  'Apple M2')
    test "$current_visual_rc" -ne 0
    test "$baseline_visual_rc" -ne 0
    for artifact in materials-showcase-actual.png materials-showcase-diff.png oak-grove-actual.png oak-grove-diff.png; do
      cmp "$current_visual/$artifact" "$baseline_visual/$artifact"
    done
    rg '^已抓取场景 (materials-showcase|oak-grove):' "$current_visual/run.log" > "$current_visual/failures.txt"
    rg '^已抓取场景 (materials-showcase|oak-grove):' "$baseline_visual/run.log" > "$baseline_visual/failures.txt"
    cmp "$current_visual/failures.txt" "$baseline_visual/failures.txt"
    rg -Fx '已抓取场景 materials-showcase: 最大通道差 1，差异像素 26/230400（0.0113%），首个差异像素在 (172,26)' "$current_visual/failures.txt"
    rg -Fx '已抓取场景 oak-grove: 最大通道差 47，差异像素 10/230400（0.0043%），首个差异像素在 (89,86)' "$current_visual/failures.txt"
    test "$(find "$current_visual" -maxdepth 1 -name '*-actual.png' | wc -l | tr -d ' ')" = 2
    test "$(find "$current_visual" -maxdepth 1 -name '*-diff.png' | wc -l | tr -d ' ')" = 2
    test "$(find "$baseline_visual" -maxdepth 1 -name '*-actual.png' | wc -l | tr -d ' ')" = 2
    test "$(find "$baseline_visual" -maxdepth 1 -name '*-diff.png' | wc -l | tr -d ' ')" = 2
    ;;
  *)
    test "$current_visual_rc" -eq 0
    test "$baseline_visual_rc" -eq 0
    test "$(find "$current_visual" -maxdepth 1 \( -name '*-actual.png' -o -name '*-diff.png' \) | wc -l | tr -d ' ')" = 0
    test "$(find "$baseline_visual" -maxdepth 1 \( -name '*-actual.png' -o -name '*-diff.png' \) | wc -l | tr -d ' ')" = 0
    ;;
esac
```

```bash
make rust-check
make build
otool -D bin/libmornlea_mesh.dylib | rg -Fx '@rpath/libmornlea_mesh.dylib'
otool -L bin/mornlea | rg -F '@rpath/libmornlea_mesh.dylib'
otool -l bin/mornlea | rg -F 'path @loader_path'

(
  mornlea_target_backup=$(mktemp -d /private/tmp/mornlea-target.XXXXXX)
  restore_target() {
    if test -d "$mornlea_target_backup/target" && test ! -e engine/target; then
      mv "$mornlea_target_backup/target" engine/target
    fi
  }
  trap 'restore_target' EXIT
  trap 'restore_target; exit 129' HUP
  trap 'restore_target; exit 130' INT
  trap 'restore_target; exit 143' TERM
  mv engine/target "$mornlea_target_backup/target"
  set +e
  bin/mornlea -h >/private/tmp/mornlea-help.txt 2>&1
  mornlea_help_status=$?
  set -e
  test "$mornlea_help_status" = 1
  rg -F 'flag: help requested' /private/tmp/mornlea-help.txt
  ! rg -i 'dyld|Library not loaded' /private/tmp/mornlea-help.txt
  restore_target
  trap - EXIT HUP INT TERM
)

CGO_ENABLED=0 GOOS=linux go build -o /private/tmp/mornlea-server ./cmd/mornlea-server
test -z "$(CGO_ENABLED=0 GOOS=linux go list -deps ./cmd/mornlea-server | rg 'internal/(client|mesh|render|gfx)|glfw|webgpu|x/image/font')"
```

```bash
mornlea_invariants="$(git rev-parse --git-common-dir)/mornlea-m4q-evidence"
go test ./internal/mesh -run '^$' -bench BenchmarkMeshTerrainSection -benchmem -count=5
go test ./internal/render -run '^$' -bench 'Mesh|Light' -benchmem -count=3

MORNLEA_PERF_DIR=$(mktemp -d /private/tmp/mornlea-m4q-perf.XXXXXX)
go run ./cmd/mornlea --benchmark --benchmark-transport memory --perf-output "$MORNLEA_PERF_DIR/memory-v15.json"
go run ./cmd/perfcheck --baseline "$MORNLEA_PERF_DIR/memory-v15.json" --current "$MORNLEA_PERF_DIR/memory-v15.json" --max-regression 0.20
producer_head=$(cat "$mornlea_invariants/task9-producer-head")
test "$(jq -r .git_commit "$MORNLEA_PERF_DIR/memory-v15.json")" = "$producer_head"
cp "$MORNLEA_PERF_DIR/memory-v15.json" "$mornlea_invariants/task9-memory-v15.json"
cmp "$MORNLEA_PERF_DIR/memory-v15.json" "$mornlea_invariants/task9-memory-v15.json"
shasum -a 256 "$mornlea_invariants/task9-memory-v15.json" > "$mornlea_invariants/task9-perf.sha256"
```

```bash
make test-race
go test ./internal/archcheck -count=1
go vet ./...
test -z "$(gofmt -l .)"
cargo fmt --manifest-path engine/Cargo.toml -- --check
cargo clippy --manifest-path engine/Cargo.toml --workspace --all-targets --locked -- -D warnings
node --test scripts/agent-hooks/guard.test.mjs
cmp AGENTS.md CLAUDE.md
openspec validate --all --strict --no-interactive
git diff --check
```

```bash
go test ./internal/archcheck -run 'Mornlea|Identity' -count=1
test ! -e cmd/mcgo
test ! -e cmd/mcgod
test ! -e engine/crates/mcgo_mesh
test ! -e engine/include/mcgo_engine.h
test ! -e bin/mcgo
test ! -e bin/libmcgo_mesh.dylib
git status --short --branch
```

更新 `design.md` 的 exact provenance 后运行：

```bash
openspec validate m4q-mornlea-project-rename --strict --no-interactive
git diff --check
git add openspec/changes/m4q-mornlea-project-rename/design.md
git diff --cached --check
git commit -m "docs: 记录 M4Q 最终验证证据"
test "$(git diff-tree --no-commit-id --name-only -r HEAD)" = 'openspec/changes/m4q-mornlea-project-rename/design.md'
test -z "$(git status --porcelain)"
```

独立 reviewer 必须检查 `$(cat "$(git rev-parse --git-common-dir)/mornlea-m4q-evidence/task1-head")..HEAD` 与 Task 9 report，并输出 Critical / Important / Minor findings、Spec compliance、data-loss/concurrency safety、allowlist precision、artifact invariance、build/server closure 和最终 `APPROVED` 或 `NEEDS_FIXES`。

Task 9 在独立评审处停止；执行者在批准前不得勾选 9.1、不得声称完成、归档、合并或 push。Task 10 独立负责处理评审结论、批准闭环、勾选 9.1 与归档，因此不作为自我指涉的 OpenSpec checkbox。外部 Task 11 仅在源码 PR 合并后由 operator 执行 GitHub repository、remote、本地根目录与 GitNexus 改名，不属于 OpenSpec implementation checkbox。
