## MODIFIED Requirements

### Requirement: 新硬件基线只能由通过门禁的无窗口报告建立
新建或升级硬件基线 MUST 来自当前冻结的 scenario v11、Memory transport、2560x1440 无窗口正式报告；该报告 MUST 通过完整性和绝对门禁，并 MUST 与同一硬件、同一场景的一次 TCP 报告通过现有跨 transport 比较后才能接受。M4G 正式链开始前 M4F scenario v10 基线和归档 MUST 已完成；升级既有硬件基线时 MUST 明确记录被替代场景和失败证据，不得把旧场景报告与新场景报告作相对回归证明。

#### Scenario: M4F 基线尚未完成
- **GIVEN** M5 当前基线尚未升级为已归档 M4F 的 scenario v10
- **WHEN** 项目准备运行 M4G scenario v11 正式链
- **THEN** 正式链 MUST 不得开始，也不得以 v9 或未提升的诊断报告作为 v10→v11 迁移起点

#### Scenario: v11 正式链全部通过
- **GIVEN** scenario v11 生产代码和计划已提交，M5 v10 基线已完成，且 Memory/TCP 使用两个全新临时路径
- **WHEN** M5 Memory 报告通过显式 v10→v11 完整性和绝对门禁，且同次 M5 TCP 报告相对该 Memory 报告通过跨 transport 比较
- **THEN** 项目接受该 Memory 报告的精确字节作为 M5 当前基线，并记录硬件、提交、命令、报告哈希和被替代的 v10 场景

#### Scenario: 正式链任一步失败
- **WHEN** 无窗口报告生成、完整性门禁或跨 transport 比较任一步失败
- **THEN** 项目 MUST 停止正式链，不得重跑失败步骤、放宽阈值、提升诊断报告或创建和覆盖正式基线

#### Scenario: 工作负载修复后重新开始
- **WHEN** 旧场景的正式链已经失败，且 benchmark 工作负载修复后升级为新场景
- **THEN** 项目 MUST 提交更新后的计划、重新取得一次性正式授权并使用全新路径执行完整报告链，且不得提升旧场景输出或修复诊断报告

#### Scenario: 阶段屏障修复后使用新的正式链
- **GIVEN** 一条 v8 正式链已因 GPU 完成尾部退化停止，且报告揭示测量阶段缺少同步收尾屏障
- **WHEN** 项目提交阶段屏障修复并准备再次建立基线
- **THEN** 失败报告 MUST 只保留为诊断证据，系统 MUST 使用新的精确 HEAD、两个全新路径和新的明确授权执行 Memory/TCP 各一次，不得把新执行视为对旧 HEAD 失败步骤的重跑

#### Scenario: v7 失败报告不得提升
- **GIVEN** M4D 的 scenario v7 报告因 `remote_gpu_complete` 尾部波动失败
- **WHEN** 后续场景修复完成并准备重建 M5 基线
- **THEN** 项目 MUST 使用新提交和全新路径重新取得正式报告，不得复制、改名或覆盖该 v7 失败报告

#### Scenario: M2 基线保持不变
- **WHEN** M5 当前基线从 scenario v10 升级到 v11
- **THEN** M2 基线的内容和路径 MUST 保持不变，且比较器仍 MUST 拒绝 M2/M5 跨硬件比较

### Requirement: 调用者明确选择匹配硬件的基线
性能比较 SHALL 继续通过显式基线路径选择硬件专用文件，不引入自动硬件探测或跨硬件归一化。M5 当前基线升级为 scenario v11 后，后续 M5 报告 MUST 使用相同 scenario 才能执行相对回归比较。

#### Scenario: M5 v11 后续回归比较
- **WHEN** 调用者在 M5 上生成后续 scenario v11 报告并显式传入 M5 当前基线文件
- **THEN** 比较器 MUST 验证硬件和场景相同，再执行既有稳定指标与绝对门禁

#### Scenario: M5 v10 当前报告被拒绝
- **WHEN** 调用者使用 M5 scenario v11 基线比较 scenario v10 当前报告且没有显式迁移授权
- **THEN** 比较器 MUST 拒绝该组合并说明场景版本不一致，且不得修改任何基线文件

#### Scenario: 不自动选择基线
- **WHEN** 调用者未显式提供与当前报告硬件匹配的基线路径
- **THEN** 比较工具 MUST NOT 根据本机硬件自动选择、改写或归一化任何基线
