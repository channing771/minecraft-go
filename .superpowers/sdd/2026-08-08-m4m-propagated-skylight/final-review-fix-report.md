# M4M final review fix report

## Status

PASS。终审的 2 项 Important 与 2 项 Minor 已全部修复；代码 TDD、OpenSpec restore/update/sync/rearchive、受影响验证与哈希保护均完成。无未解决 blocker。

## BASE / HEAD / commits

- FIX_BASE：`ef56307724caac017e4f2a35f261e99fe6cb9c08`
- 验证 HEAD：`749b3e07dbc9d9b5db425d0e53d1e36929bb6293`
- 核心修复提交：`749b3e07dbc9d9b5db425d0e53d1e36929bb6293`（`fix: 修正 M4M 终审契约`）
- 报告提交：本文件单独提交；其哈希由最终回报给出，避免报告自引用提交哈希。

## Findings 修复

### Important 1：主规格残留旧性能失败与一次性正式链

- 恢复唯一 change `m4m-propagated-skylight`，只修改 `openspec status --json` 返回的既有 proposal、design、delta specs 与 tasks 路径。
- 在 `bounded-benchmark-workload` delta 和主规格中把 p99、FPS、RSS、GPU、tick、队列高水位、绝对与相对阈值统一为 record-only：继续保存阈值、指标、噪声分类与历史场景口径，但数值不改变 producer、比较器、CI 或 Memory 基线提升。
- 删除旧的静稳预检、绑定路径、一次性授权、失败即停、禁止重跑门禁要求，并以 provenance-only、允许重新生成的要求替代。
- 把 tick 输入边界越界改为继续记录时间分解与积压量，不再改变退出或停止后续记录。
- 在 `hardware-performance-baselines` delta 和主规格中把稳定指标退化改为只记录；用“历史报告重判与重新采样互不限制”替换禁止重新采样、TCP 前置和失败即停契约。
- 保留并明确仍失败的边界：报告结构与必需字段、样本完整性和单调性、provenance、硬件/transport/scenario/commit 身份、迁移授权与方向、真实 overflow、数据丢失和 I/O 错误。
- 未删除仍适用的历史报告可读取/可审计、GPU 样本结构、噪声记录定义、固定 workload 与诊断可读性要求。

### Important 2：跨 transport 错误接受不同 scenario

- 新增真实行为测试 `TestCrossTransportComparisonRequiresMatchingScenario`：同一伪造 commit、Memory scenario v13、TCP scenario v14、显式 `13:14` 必须拒绝。
- 在现有跨 transport commit guard 前增加唯一一个 scenario guard；transport 不同时先要求 `ScenarioVersion` 相同，再检查 `GitCommit`。
- 同 transport 的合法 Memory v13→v14 `13:14` 迁移仍由既有测试覆盖并保持允许。

### Minor 1：历史 baseline 时态

- `docs/notes/perf-baseline.md` 历史 scenario v13 章节的“当前 `perfcheck`”改为“当时 `perfcheck`”。

### Minor 2：历史 M4M design 缺 superseded 注记

- 在历史设计文件顶部增加醒目中文“已被取代”注记，链接固定归档 OpenSpec 与两个主规格。
- 注记明确旧性能门禁、TCP 前置和一次性正式流程已被 record-only 契约取代；历史正文未重写。

## RED / GREEN / mutation

### RED

首个沙箱内命令在 Go build cache 处因权限失败，未触达测试逻辑，不计为 RED。按环境规则在沙箱外使用现有 Go 1.26 缓存重跑：

```text
TERM=xterm-256color zsh -ic 'gvm use go1.26 >/dev/null && go test ./cmd/perfcheck -run TestCrossTransportComparisonRequiresMatchingScenario -count=1'
--- FAIL: TestCrossTransportComparisonRequiresMatchingScenario
    main_test.go:649: 跨 transport scenario 不一致 error=<nil>
FAIL
```

失败原因精确为缺少跨 transport scenario guard，而非测试错误或环境错误。

### GREEN

加入最小 guard 后同一命令退出 `0`：

```text
ok  minecraft-go/cmd/perfcheck  0.637s
```

### Mutation

临时移除新增 guard 后，同一测试再次以 `error=<nil>` 失败；恢复 guard 后包级 race 验证通过。mutation 证明测试保护的是新增 scenario 校验。

## OpenSpec restore / update / sync / rearchive

1. 确认 active 目标不存在后，将 `openspec/changes/archive/2026-08-08-m4m-propagated-skylight` 恢复为 `openspec/changes/m4m-propagated-skylight`。
2. `openspec status --change m4m-propagated-skylight --json` 返回 proposal、4 个 delta specs、design、tasks 全部 `done`；只编辑其 `existingOutputPaths`。
3. 修订 proposal/design 与两个性能 delta，新增并勾选审计任务 `5.3`；active change strict 校验为 `18 passed, 0 failed`。
4. 按 `openspec-sync-specs` 智能同步全部 delta：`authoritative-daylight` 与 `visual-verification` 已一致；更新 `bounded-benchmark-workload` 与 `hardware-performance-baselines`，保留未触及的结构、样本、历史报告和诊断要求。
5. 归档前确认 artifacts 全部 `done`、tasks 无 `- [ ]`、delta 的新增/修改/删除已反映到主规格，并读取 `openspec instructions archive --change ... --json` 的 context/guidance。
6. 重新归档到固定路径 `openspec/changes/archive/2026-08-08-m4m-propagated-skylight`；`.openspec.yaml` 存在。
7. 归档后 `openspec list --json` 为 `changes: []`，strict 校验为 `17 passed, 0 failed`。

## 验证与哈希

最终新鲜验证：

```text
go test ./cmd/perfcheck -race -count=1
ok  minecraft-go/cmd/perfcheck  3.353s

go vet ./cmd/perfcheck
exit 0

gofmt -l .
无输出

git diff --check
无输出

openspec validate --all --strict --no-interactive
Totals: 17 passed, 0 failed (17 items)
```

基线哈希未变：

- M2 `docs/notes/perf-baseline.json`：`b2d04877004c0cfae5884416d1ef7dbe1d6d5daed95dbda1a392604520cb7f93`
- M5 `docs/notes/perf-baseline-m5.json`：`5a34fe091cb1aacfee0172db90b5a7f66571202d230e7542660dd8e703132483`

按 fix wave 指令未重跑全仓 race、GPU capture 或 producer。

## 文件清单

- `cmd/perfcheck/main.go`
- `cmd/perfcheck/main_test.go`
- `docs/notes/perf-baseline.md`
- `docs/superpowers/specs/2026-08-08-m4m-propagated-skylight-design.md`
- `openspec/changes/archive/2026-08-08-m4m-propagated-skylight/proposal.md`
- `openspec/changes/archive/2026-08-08-m4m-propagated-skylight/design.md`
- `openspec/changes/archive/2026-08-08-m4m-propagated-skylight/tasks.md`
- `openspec/changes/archive/2026-08-08-m4m-propagated-skylight/specs/bounded-benchmark-workload/spec.md`
- `openspec/changes/archive/2026-08-08-m4m-propagated-skylight/specs/hardware-performance-baselines/spec.md`
- `openspec/specs/bounded-benchmark-workload/spec.md`
- `openspec/specs/hardware-performance-baselines/spec.md`
- `.superpowers/sdd/2026-08-08-m4m-propagated-skylight/final-review-fix-report.md`

## 自审 / 顾虑

- 自审未发现未覆盖 finding、额外抽象、配置扩展、协议/存档变化或基线字节变化。
- 代码改动仅一个 guard 与一个测试；规格改动只消除与 record-only 冲突的门禁语义并保留正确性边界。
- 唯一验证范围说明：遵循明确指令，未重跑已有新鲜的全仓 race、GPU capture 或 producer；本 wave 的代码风险由定向 RED/GREEN/mutation、包级 race 和受影响 vet 覆盖。
