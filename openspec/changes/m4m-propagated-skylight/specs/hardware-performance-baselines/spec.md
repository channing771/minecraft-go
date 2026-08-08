## MODIFIED Requirements

### Requirement: 新硬件基线只能由通过门禁的无窗口报告建立

新建或升级硬件基线 MUST 来自当前冻结的 scenario v14、Memory transport、`2560x1440` 无窗口正式报告。M5 Memory 报告 MUST 通过显式 `13:14` 迁移的完整性、硬件一致性和绝对门禁，并 MUST 与同一硬件、同一场景、全新路径的一次 TCP 报告通过既有跨 transport 比较后才能接受。升级 MUST 记录被替代场景和失败证据，不得把旧场景报告与新场景报告作相对回归证明；M2 基线内容和路径 MUST 保持不变。

#### Scenario: M5 v14 正式链全部通过
- **GIVEN** scenario v14 生产代码和计划已提交、完整门禁通过、静稳预检完成且 Memory/TCP 使用两个全新临时路径
- **WHEN** M5 Memory 报告通过显式 `13:14` 完整性、硬件一致性和绝对门禁，且同次 M5 TCP 报告相对该 Memory 报告通过跨 transport 比较
- **THEN** 项目 MUST 接受该 Memory 报告的精确字节作为 M5 当前基线，并记录硬件、提交、命令、报告哈希和被替代的 scenario v13

#### Scenario: 正式链任一步失败
- **WHEN** 无窗口报告生成、迁移完整性、绝对门禁或跨 transport 比较任一步失败
- **THEN** 项目 MUST 立即停止正式链，保留不可提升证据，不得重跑失败步骤、放宽阈值、提升诊断报告或创建和覆盖正式基线

#### Scenario: 新候选以全新正式链重试
- **GIVEN** 一个 scenario v14 候选的正式链失败，后续实现修复不改变 workload 或测量口径
- **WHEN** 新候选完成门禁、静稳预检并获得绑定精确 HEAD 和新路径的明确授权
- **THEN** 项目 MUST 使用两个全新路径重新执行完整 Memory/TCP 正式链，旧候选与诊断输出 MUST 保持冻结且不可提升

#### Scenario: M2 基线保持不变
- **WHEN** M5 当前基线升级到 scenario v14
- **THEN** M2 基线的内容和路径 MUST 保持不变，且比较器仍 MUST 拒绝 M2/M5 跨硬件比较

#### Scenario: M5 v14 后续回归比较
- **WHEN** 调用者在 M5 上生成后续 scenario v14 报告并显式传入 M5 当前基线文件
- **THEN** 比较器 MUST 验证硬件和场景相同，再执行既有稳定指标与绝对门禁

#### Scenario: 旧 M5 scenario 不得作为当前基线
- **WHEN** 调用者使用 M5 scenario v14 基线比较 v13 或更早的当前报告且没有显式 `13:14` 迁移授权
- **THEN** 比较器 MUST 拒绝该组合并说明场景版本不一致，且不得修改任何基线文件
