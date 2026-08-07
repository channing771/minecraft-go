## Why

2026-08-07 两条分支相继合入 `main`，各自的 PR 检查都是绿的，合并后的 `main` 连续两次红。其中一次是编译失败：`visual-verification` 给 `gfx.Texture` 增加了 `ReadLayer`，`m4i-authoritative-celestial-sky` 新增了测试替身 `skyTestTexture`，两条分支改的是不同文件，`git merge` 零文本冲突，合并结果不满足接口。

这类语义合并冲突的标准解法是"PR 必须与 main 同步后重跑才能合并"。但仓库当前有约 25% 的 CI 假失败率——最近 25 次运行 6 次红，失败测试几乎次次不同。在这个假失败率下要求"必须绿才能合"，实际效果是训练所有人反复点 re-run，最终以"已知抖动"为由绕过门禁。

所以必须先降假失败率，门禁才立得住。

假失败的成因已定位：`internal/server` 测试有 302 处墙钟期限，绝大多数是按本机（Apple M5）调出来的活性等待。CI 的 `macos-latest` 是共享且更慢的 runner，这些期限在其上余量不足。

## What Changes

- 把 `internal/server` 测试中的期限按四类归类，只抬高"活性等待"一类，替换为三个命名常量。
- `cmd/mcgo` 的 `TestScenarioV7EightSessionServerProbeIsRealAndBounded` 把采样收集预算从 `10s` 放宽到 `30s`。该预算目前正好等于 `measureMultiplayerServerProbe` 允许的下限，按构造零余量。
- 修复两个测试顺序假设错误：`TestWorldPersistsAcrossRestartAndGeneratorUpgrade` 与 `TestOpenFurnaceSendsStateOnlyToViewer`。
- 记录 `GOMAXPROCS=1` 作为慢 runner 的确定性 A/B 手段。

不改产品代码、不改协议、不改存档格式、不改任何性能阈值或资源上限，因此无迁移。

**明确不做**：不引入环境变量倍率旋钮。活性等待是放弃期限而非 `sleep`，条件成立即返回，因此在快机器上抬高它零成本；旋钮买来的"本地更快"不存在，代价却是本地与 CI 跑两套行为。

**明确不做**：不消除墙钟依赖（给 tick 循环注入假时钟、测试显式步进）。那是唯一的正解，但需要重写整套 `internal/server` 集成测试，改动本身的回归风险超过收益。

## Capabilities

### New Capabilities

- `test-timing-discipline`: 测试中墙钟期限的分类规则、活性等待的取值契约，以及禁改区的机械判据。

### Modified Capabilities

（无。本变更不改变任何产品行为契约。）

## Impact

- `internal/server/*_test.go`：33 个文件的期限站点。这是本变更的主体。
- `internal/server/deadline_test.go`（新增）：三个命名常量及其 GoDoc。
- `cmd/mcgo/benchmark_v6_test.go`：ScenarioV7 的收集预算。
- `internal/server/persistence_integration_test.go`、`internal/server/furnace_publication_test.go`：两处顺序假设修复。
- 依赖：不新增任何依赖。
- 产品代码：零改动。若诊断发现根因在产品代码，停手另开 change。
- 分支保护：`Require branches to be up to date before merging` 是 GitHub 仓库设置，不在代码仓库内，需要仓库管理员在假失败率降下来后手动开启。本变更不包含该操作，只把它记为前置条件已满足后的下一步。
