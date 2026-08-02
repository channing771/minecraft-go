# M3C scenario-upgrade 性能迁移策略设计

日期：2026-08-02
状态：设计方向已批准，书面规格待复核

## 背景

M3C 把性能场景从 v5 升级为 v6。v6 增加七个远端玩家的插值、Avatar、Unicode 昵称和真实八会话服务端探针，因此它与 v5 不是相同工作负载。现有 `perfcheck` 虽要求显式 `--allow-scenario-upgrade 5:6`，放行版本检查后仍对所有共有字段执行 20% 相对比较。

三次满足 v6 绝对门禁的 Memory 报告证明相同核心指标稳定，但单帧最大值、Darwin `ru_maxrss` 高水位和 persistence p95 会阻断 v5→v6 迁移。最大值与 RSS 在三次报告间明显波动；persistence p50/p99 保持在 20% 内，而 p95 持续超过 v5 20% 线。这些差异不能通过放宽 v6 的绝对性能目标解决，也不应以重复采样挑选有利结果。

## 决策

性能比较分为两种明确模式：

1. **相同 scenario 回归比较**：保留现有行为，对全部共有指标执行严格 20% 相对退化门禁。
2. **显式 v5→v6 迁移验证**：只验证版本迁移许可、报告完整性、硬件一致性，以及当前 v6 报告的全部绝对门禁；不执行跨场景相对比较。

只有精确的 `--allow-scenario-upgrade 5:6` 能进入迁移验证。缺少参数、反向迁移、跳版本和其他版本组合继续拒绝。

## 验证顺序

`compareReportsWithScenarioUpgrade` 必须按以下顺序验证：

1. 判断是否为相同 scenario，或显式允许的 `5:6`。
2. 按各自版本验证 baseline 与 current 的 schema、必填字段、样本数量和字段顺序关系。
3. 验证硬件标识完全一致。
4. current 为 v6 时执行全部 v6 绝对门禁：still/flying FPS、frame p99、各阶段与 multiplayer RSS、tick p99/max、队列高水位。
5. 若为显式 `5:6`，在上述验证后成功返回，不执行相对比较。
6. 若 scenario 相同，继续执行现有 load、snapshot、tick、persistence、protocol、player persistence、phase、RSS、FPS 和 v6 multiplayer 的相对比较。

验证失败的报告不得覆盖 accepted baseline。

## CLI 行为

成功输出必须准确描述执行的模式：

- 相同 scenario：说明同场景回归比较通过，全部相对退化未超过阈值。
- 显式 `5:6`：说明场景迁移验证通过，报告完整、硬件一致且当前 v6 绝对门禁通过。

错误输出和退出码保持现有契约：参数或报告错误退出非零；相对或绝对门禁失败退出 1。

## TDD 与变异证明

实现前先增加失败测试，证明：

- v5→v6 在共有指标明显超过 20% 但 current v6 绝对门禁通过时，显式迁移验证应通过。
- 同一输入缺少迁移参数时仍因 scenario 不同而失败。
- v5→v6 的 current v6 违反任一绝对门禁时仍失败。
- v5→v6 的报告缺字段、样本不足或硬件不同时仍失败。
- 反向、跳版本和错误参数仍失败。
- v5→v5 与 v6→v6 的共有指标严格大于 20% 时仍失败，恰好 20% 时仍通过。
- CLI 两种成功信息与实际模式一致。

最小实现完成后临时移除迁移分支的提前返回，新增跨版本测试必须转红；恢复源码后全部测试转绿。临时绕过同场景相对比较时，同场景回归测试也必须转红。

## 性能接受流程

策略实现、限定测试、race 和独立代码审查通过后，按顺序执行：

1. 生成唯一一次新的 v6 Memory 报告并验证全部绝对门禁。
2. 用 accepted v5 baseline 对该 Memory 报告执行显式 `5:6` 迁移验证。
3. 生成唯一一次新的 v6 TCP 报告。
4. 对 Memory→TCP 执行相同 scenario 的严格 20% 比较。
5. 以上全部通过后，精确复制 Memory JSON 更新 accepted v6 baseline，并保留尾换行。
6. 再生成唯一一次 v6 current Memory 报告，对 accepted v6 baseline 执行严格 20% 同场景回归。
7. 完成格式、单测、race、fuzz、vet、archcheck、Linux CGO0 构建、physics 零分配、GitNexus 变更检测与最终审查。

任一步失败立即停止，不重跑、不覆盖 baseline、不修改 20% 阈值或 v6 绝对阈值。

## 范围

允许修改：

- `cmd/perfcheck/main.go`
- `cmd/perfcheck/main_test.go`
- M3C 计划、任务 brief/report、进度账本和性能基线文档

不修改 benchmark 工作负载、报告 schema、v6 绝对门禁、Host outbox、renderer、client/server 业务逻辑或 accepted baseline，直到性能接受流程允许更新 baseline。

## 风险控制

规则变化只影响显式 `5:6` 分支。实现前对将修改的现有符号执行 GitNexus upstream impact；若风险为 HIGH 或 CRITICAL，先报告并暂停。相同 scenario 路径必须通过现有测试和新增变异证明保持行为不变。实现任务与审查任务由不同子代理完成。
