# SDD ledger — m5e-deferred-clearing

计划：openspec/changes/m5e-deferred-clearing/tasks.md（三任务组 + 收尾）
工作区：.worktrees/m5e-deferred-clearing（分支 claude/m5e-deferred-clearing，基线 origin/main @ 08932d9）
设计：docs/superpowers/specs/2026-08-22-m5e-deferred-clearing-design.md（1526e16）
实施计划：docs/superpowers/plans/2026-08-22-m5e-deferred-clearing.md

## 预检记录（控制会话）

- 三流并行确认：authoritative-hunger（主工作区，执行中）、bedrock-survival-hud
  （.worktrees/bedrock-survival-hud，提案就绪）；本变更改动面与两流零文件交集。
- 六项递延在 08932d9 逐行核实有效；递延 4/5 再递延并归入领地流（见 proposal「延期与放弃」）。
- skip_specs 探针验证：`schema: spec-driven` + `skip_specs: true` 通过
  `openspec validate --strict`（探针目录已清理，`--all` 55 项全绿）。

预检扫描（任务对/自洽性，逐行核对）：

| 对象 | 核对结果 |
|---|---|
| T1↔T2 | T2 消费的 `companion.MaxPlanCommandBytes` 是既有常量，非 T1 产物；零耦合 |
| T1↔T3 | 不同包；T1 提示词字节锁不变，server 侧无感知 |
| T2↔T3 | 文件集不相交 |
| T1 自洽 | Sprintf 引用的 `planEnvRadiusBlocks`/`planEnvVerticalBlocks` 存在于 plan_types.go:36/:40；头段拆分保持逐位拼接 |
| T2 自洽 | `ChatEvent` 字段 Kind/CompanionName/Command/RejectReason 经 chat.go 实际用法核对存在；两枚举均 uint8、200 未占用 |
| T3 自洽 | 两个 helper 调用点均在 `t` 作用域；worker 重排对照实际代码行核实；两结果 channel 均 MaxActive 缓冲 |
| 计划 vs 评审观感 | D1/D4 为「锁现状」用例（先绿后变异验证），与 red-first TDD 不同属计划本意，非测试无效 |

Ruling: 模型选择——本 harness 的 Agent 工具无 model 参数，无法按 SKILL.md 指定分级模型；全部以 general-purpose 派发并在评审侧用最仔细的输入约束补偿 — 若评审质量不足将体现为修复轮增加，成本是更多轮次而非质量下降。

## 任务记录

Task 1: dispatched (BASE 693db2e, implementer agent_fec61641, 45 tool uses)
Task 1: Ruling: Step 4 变异形式——brief 字面变异（必填出现判定 `has(...)` 改指针判 nil）实测不红（null 仍经「缺少」分支拒绝、用例只断言错误类别），实现者改证完整回归形态（指针判定 + 删 nil 屏障 → 两用例各自 nil panic 红，栈迹可交叉印证），与新用例注释的「nil 解引用防 panic 屏障」语义严格一致，采纳 — 若误纳，代价是「出现判定单独退化为指针判定且不删屏障」这一退化不被消息级断言捕获（记录于下条 minor）。
Task 1: minor (deferred): planner_test.go 宿主测试只断言错误类别，「显式 null 记为字段出现」契约在错误消息层未锁；消息级区分需后续调整宿主断言（brief 断言形态固有，非实现缺陷，评审已确认披露完整）
Task 1: complete (commits 693db2e..21ce9e2, review clean — Spec ✅ / Approved, 0 Critical, 0 Important)

Task 2: dispatched (BASE 597f044, implementer agent_aea7520e, 23 tool uses)
Task 2: minor (deferred, plan-mandated): `interactive.go` 新注释末句「两处界一旦分叉，较大一侧会在另一侧之前静默截断」方向写反——静默截断发生在**较小**的有效界一侧（若上限增大而缓冲不变，drain 层先截断），且 `chatInput` 的字节上限是置 overflow 拒收而非静默截断；注释为计划原文逐字强制，实现者无自由度（评审 Minor-1，终审裁决是否入修复波）
Task 2: complete (commits 597f044..4ee2be7, review clean — Spec ✅ / Approved, 0 Critical, 0 Important)

Task 3: dispatched (BASE c762813, implementer agent_b7aae507, 37 tool uses)
Task 3: Ruling: 两处计划文本偏离按计划自身回退指示处理——(a) brief 注释模板 `` `Dialogue` `` 标识符全仓不存在（真实方法为 `Do`），archcheck 门禁拒收，实现者按根因修复改为 `` `m.dialogue.Do` ``，语义不变；(b) Step 2 `-run 'TestCompanionStageAcceptance'` 无匹配宿主（计划已预置「以实际宿主名为准」），实际宿主为 `TestM5StageAcceptancePersonaDialogueEndToEnd` — 两处若误裁，代价仅为注释措辞/测试过滤名与计划文本不一致，无行为影响。
Task 3: minor (deferred): 释放改为裸语句后，`Plan`/`Do` 内部未恢复 panic 将跳过令牌释放（旧 defer 会释放）——worker goroutine panic 即杀进程，泄漏令牌不可观察，且该结构为计划显式指定；记录权衡供分支评审知悉（评审 Minor-1）
Task 3: complete (commits c762813..5f9f2a7, review clean — Spec ✅ / Approved, 0 Critical, 0 Important；评审另核实信号量全部触点单次释放核算成立、ctx 路径不变、per-companion in-flight 旗标独立兜住并发上界)

## 终审与修复波

终审门禁（四件套，08932d9 未移动、无需 rebase）：`make rust` EXIT=0（缓存命中）；`go test ./... -race -count=1` EXIT=0、24 包全 ok（hunger 7.2 记录的已知红灯本次未现）；`go vet ./...` EXIT=0；`gofmt -l .` 空；`openspec validate --all --strict` 56/56。

终审（独立代理）：**Merge-ready after fixes**——D1–D6 全数交付且无多余；D6 四项语义断言逐项 verified（全部 `m.semaphore` 触点单次释放配对、无代码依赖「令牌持有至结果投递」、ctx 路径不变、双 channel 缓冲 + per-companion in-flight 旗标给出比设计更强的界）；提示词字节同一性双路验证（从 08932d9 提取旧 const 与新锁测试字面 cmp 逐位相等 1065 bytes + 运行时锁测试）。递延 minor 三角裁：Task 1 defer（错误身份契约已锁，消息级分类不成比例）、Task 2 fix-now（事实反向注释恰在 D3 要文档化的不变量上，且无领地流兜底）、Task 3 defer（panic 即杀进程，不可观察）。

修复波（单次派发，112eb71）：两项注释修正——interactive.go 截断方向勘正（较小有效界先拦、两层经 `textOverflow`/`overflow` 均非静默、提交整体拒发）；companion_dialogue.go 取消契约释放时序同步。偏离：`textOverflow` 为 `:=` 局部变量、不在 archcheck 反引号门禁的声明索引内（门禁自文档「不含函数参数与局部变量」），故不加反引号——门禁实证。
复审（scoped）：**PASS**——两项 ADDRESSED（逐行对代码核verified：chat.go:41-42/:62-63、dialogueWorker 实际释放序）、修复 diff 零新破坏（纯注释行）、偏离维持。

终局：Ready to merge。


