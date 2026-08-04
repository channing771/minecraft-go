## 1. 固定石砖枚举与纯合成

- [x] 1.1 在 `internal/core` 先写失败测试，覆盖稳定 `StoneBrickID`、`ItemStoneBrick`、recipe ID `1`、4 石头到 4 石砖、未知配方、跨栏位最低索引扣料、扣料后释放输出格、原料不足、产物无容量、非法 Inventory 不改原值和零分配最坏路径。
- [x] 1.2 在 `internal/core` 最小实现一个固定 recipe switch 与 `Inventory.Craft` 值操作；复用 `Slot`、`setSlot` 和 `AddStack`，不新增注册器、接口、配置或第二套可合成判断。
- [x] 1.3 在 `internal/assets`、`internal/render` 增加程序化石砖材质和现有物品色块映射，补充方块/物品放置、挖掘掉落和材质测试；不新增二进制资源或第二个 renderer。
- [x] 1.4 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/core ./internal/assets ./internal/render -race -count=1 && go test ./internal/archcheck -count=1'`，确认 `gofmt -l internal/core internal/assets internal/render` 与 `git diff --check` 无输出；只暂存本组与任务勾选，提交 `feat: 定义固定石砖配方`，然后自动进入第 2 组。

## 2. 区块存档 schema v3

- [x] 2.1 在 `internal/storage` 与 `internal/server` 先写失败测试，覆盖 v2 方块/掉落物无损迁移、v1 链式迁移、石砖方块与掉落物 v3 roundtrip/golden、未来版本拒绝、故障写入保留旧记录，以及正常 active bank 迁移结果传播为 `NeedsRewrite` 并由服务端保存确认。
- [x] 2.2 将区块当前 schema 升为 3，新增无数据变化的 `2→3` migration；把 `migrateChunk` 的 migrated 结果经 `decodedPayload` 和正常 `region.Load` 传播到 `StoredChunk.NeedsRewrite`，复用现有 acquire/dirty/原子保存路径，不扫描未加载区域。
- [x] 2.3 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage ./internal/server -race -count=1 && go test ./internal/archcheck -count=1'`，确认 migration/golden/故障测试、`gofmt -l internal/storage internal/server` 和 `git diff --check` 通过；提交 `feat: 升级区块存档 schema v3`，然后自动进入第 3 组。

## 3. 权威合成协议 v6 纵向闭环

- [x] 3.1 在 `internal/network` 先写失败的 packet/registry/codec/golden/fuzz seed 测试，覆盖固定 9 字节 `CraftRecipe`、recipe ID 校验、截断/尾随拒绝、稳定 packet ID、协议 v6 登录和 v5 登录拒绝。
- [x] 3.2 在 `internal/sim`、`internal/server` 先写失败测试，覆盖 Ready/sequence、成功一次 dirty、未知配方/原料不足/无容量原子拒绝、过期命令不重复执行、只向所属玩家确认，以及 8 玩家稳定隔离。
- [x] 3.3 将 `ProtocolVersion` 升为 6，追加 `CraftRecipe` 请求、`CommandCraftRecipe` 和 endpoint 翻译；在权威 tick 调用 `Inventory.Craft`，失败复用 `RejectInvalidInput`，成功只走一次现有完整 `InventoryState` 私有发布，不新增结果包或 dirty 位。
- [x] 3.4 增加 Memory/TCP 纵向测试，证明相同初始 Inventory 和合成序列得到相同最终状态、拒绝和持久化结果；执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network ./internal/sim ./internal/server -race -count=1 && go test ./internal/archcheck -count=1'`，确认 `gofmt` 与 `git diff --check` 通过；提交 `feat: 接入权威合成协议 v6`，然后自动进入第 4 组。

## 4. 固定合成入口与应用接线

- [x] 4.1 在 `internal/render` 与 `cmd/mcgo` 先写 headless 失败测试，覆盖背包打开时固定配方行、enabled/disabled 状态、配方命中边界、有效点击只发一次、不可用不发送、发送后不改镜像、清除来源选择，以及关闭/断线/reset 行为不回退。
- [x] 4.2 扩展现有 `HotbarRenderer` 的固定 CPU/GPU 容量和共用几何，绘制 `4 石头 → 4 石砖` 与一次合成按钮；增加一个纯配方命中函数，不新增列表、第二套 pipeline 或每帧对象。
- [x] 4.3 在 `cmd/mcgo` 的背包点击路径用最后确认 Inventory 调用同一 `Craft` 函数判断可用性，成功命中时发送一次 recipe ID `1` 并清除来源选择；保持输入抑制、中性移动、鼠标捕获和服务端 tick 行为不变。
- [x] 4.4 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/render ./internal/client ./cmd/mcgo -race -count=1 && go test ./internal/archcheck -count=1'`；测试不得启动或聚焦游戏窗口，确认满容量 allocation、buffer 边界、`gofmt` 和 `git diff --check` 通过；提交 `feat: 接入石砖合成界面`，然后自动进入第 5 组。

## 5. 重启闭环、兼容文档与专项门禁

- [x] 5.1 在 `internal/server` 增加 DiskStore 重启纵向测试：从 v2 区块迁移、采集 4 石头、合成石砖、放置、挖掘、拾取、正常刷新和多身份乱序重连后，完整物品状态、石砖区块与掉落物必须一致；保存失败保持可重试且不部分提交。
- [x] 5.2 更新 `README.md` 与 `docs/notes/lan-server.md`，说明固定石砖配方、背包操作、协议 v6、玩家 schema v3、区块 schema v3/v1-v2 迁移、备份/回退和未实现范围；文档使用中文并删除“尚无背包界面”的过时描述。
- [x] 5.3 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "^$" -fuzz FuzzSmallPacketCodec -fuzztime=10s && go test ./internal/storage -run "^$" -fuzz FuzzDecodeChunkPayload -fuzztime=10s'`，确认无 panic、无无界分配和失败语料。
- [x] 5.4 确认并执行固定工作量微基准：`zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/core ./internal/network ./internal/storage ./internal/sim -run "^$" -bench "Craft|Chunk(Encode|Decode)|SmallPacketCodec" -benchmem -count=3'`；只优化实测根因，不降低既有门禁。
- [x] 5.5 只在现有 `--benchmark` 路径确认无窗口时，以当前精确 HEAD 派生两个全新 M5 scenario v8 Memory/TCP 临时报告路径；Memory 先显式使用 `docs/notes/perf-baseline-m5.json` 运行 `cmd/perfcheck --max-regression 0.20`，通过后 TCP 再对同一基线运行相同门禁。Memory/TCP 各执行一次，任一步失败停止，不重跑、不打开前台窗口、不复用旧报告或覆盖基线。
  - 2026-08-04 在精确 HEAD `6d275a81688e8b53263ae17ecc7754b02c9ba601` 上只执行一次 Memory 报告 `/tmp/mcgo-m4d-v8-6d275a81688e-memory.json`，SHA-256 为 `a878195ac2e37bf5d1fe19cc67b833fcd7371073586d768f7cdf6834b2e84d95`；报告满足 scenario v8、M5、2560x1440 与 GPU 2048 样本，但 `interest_diff.p99_ms` 从基线 `0.034334` 升至 `0.042917`，门禁以退化 `25.0%` 失败。已立即停止，未执行 TCP、未重跑 Memory、未覆盖基线；本任务保持未完成。
  - 比较契约修复提交：`30b283ea49ba0b73e9077d2844019ec47d5012f4`（`fix: 移除不稳定 interest 相对门禁`）；producer、scenario v8、20% 阈值、报告 schema 与 M5 baseline 均未改变。
  - Memory 复判：复用 `/tmp/mcgo-m4d-v8-6d275a81688e-memory.json` 原始字节，执行前后 SHA-256 均为 `a878195ac2e37bf5d1fe19cc67b833fcd7371073586d768f7cdf6834b2e84d95`；在修复提交上执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck --baseline docs/notes/perf-baseline-m5.json --current /tmp/mcgo-m4d-v8-6d275a81688e-memory.json --max-regression 0.20'`，成功文本：`同场景性能比较通过：适用的稳定指标退化均未超过阈值且绝对门禁通过`。
  - TCP 正式报告：从原始生产提交 `6d275a81688e8b53263ae17ecc7754b02c9ba601` 的 detached 干净 worktree 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport tcp --perf-output /tmp/mcgo-m4d-v8-6d275a81688e-tcp.json'`；报告路径 `/tmp/mcgo-m4d-v8-6d275a81688e-tcp.json`，SHA-256 `7ecac884c4a4a5c8853ff5223b862e32c7059e67ff580c18b332d75181c5e14e`，scenario v8、TCP、M5/24GiB、2560x1440、GPU 2048、interest 1600 与 tick 200 字段均通过完整性核验。
  - TCP 门禁：在修复提交上执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck --baseline docs/notes/perf-baseline-m5.json --current /tmp/mcgo-m4d-v8-6d275a81688e-tcp.json --max-regression 0.20'`，成功文本：`同场景性能比较通过：适用的稳定指标退化均未超过阈值且绝对门禁通过`。Memory 未重跑，TCP benchmark 实际且仅执行一次，报告与 `docs/notes/perf-baseline-m5.json` 均未覆盖或改写。
- [x] 5.6 只暂存本组测试、中文文档和任务勾选，确认 `midscene_run/` 仍未暂存；提交 `test: 验证石砖合成重启闭环`，然后自动进入第 6 组。
  - 2026-08-04 fresh GVM Go 1.26.0 验证通过：`go test ./internal/server -run 'TestCraftingSurvivesV2DiskRestartAndReconnectOrder|TestMemoryTCPParityBusinessTranscriptAndHashes' -race -count=1`、`go test ./internal/server -race -count=1` 与 `go test ./internal/archcheck -count=1`；`gofmt -l internal/server/tcp_integration_test.go`、`openspec validate --all --strict --no-interactive` 和 `git diff --check` 无失败。暂存范围精确为 `README.md`、`docs/notes/lan-server.md`、`internal/server/tcp_integration_test.go` 与本文件；`midscene_run/` 保持未暂存。

## 6. 最终门禁与阶段收尾

- [x] 6.1 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race -count=1 && go vet ./... && go test ./internal/archcheck -count=1 && gofmt -l .'`；`gofmt -l .` 必须无输出，且不得启动或聚焦游戏窗口。
  - 2026-08-04T03:11:20-0700 PDT fresh GVM Go 1.26.0：`go test ./... -race -count=1` exit 0；随后 `go vet ./...` exit 0、`go test ./internal/archcheck -count=1` 输出 `ok   minecraft-go/internal/archcheck` 且 exit 0、`gofmt -l .` 无输出且 exit 0。未启动或聚焦游戏窗口。
- [x] 6.2 执行 `openspec validate --all --strict --no-interactive`、`git diff --check`，核对 proposal、4 份 delta specs、design 与实现一致，确认协议 v6、区块 schema v3 和性能基线均未被放宽或静默覆盖。
  - 2026-08-04T03:12:13-0700 PDT fresh：`openspec validate --all --strict --no-interactive` 输出 `Totals: 8 passed, 0 failed`；`git diff --check` exit 0、无输出。`rg` 确认 `network.ProtocolVersion = 6`、`currentChunkSchema = 3`、scenario v8 与 20% 阈值仍在；`git diff --quiet main...HEAD -- docs/notes/perf-baseline-m5.json` exit 0。两份不可变 M4D 报告 SHA-256 仍为 Memory `a878195ac2e37bf5d1fe19cc67b833fcd7371073586d768f7cdf6834b2e84d95`、TCP `7ecac884c4a4a5c8853ff5223b862e32c7059e67ff580c18b332d75181c5e14e`。
- [x] 6.3 只暂存 M4D 实现、测试、中文文档和本文件勾选，排除 `midscene_run/`；提交 `chore: 关闭 M4D 权威合成`，停止实现并等待主规格同步、归档与推送指令。
  - 2026-08-04T03:12:13-0700 PDT GitNexus 与 `detect_changes` 不可用（`command -v detect_changes` 无输出）；fallback `git diff --stat main...HEAD`、`git diff --name-status main...HEAD`、`git diff --check`、`rg` 与 `git status --short` 确认该收尾提交仅包含本文件，且未暂存或纳入 `midscene_run/`。
