## MODIFIED Requirements

### Requirement: 新硬件基线只能由通过门禁的无窗口报告建立

新建或升级硬件基线 MUST 来自当前冻结的 scenario v14、Memory transport、`2560x1440` 无窗口正式报告。M5 Memory 报告 MUST 通过显式 `13:14` 迁移的完整性、硬件一致性和绝对门禁，并 MUST 与同一硬件、同一场景、全新路径的一次 TCP 报告通过既有跨 transport 比较后才能接受。首次或任何新候选的正式链均 MUST 绑定精确 HEAD 和明确的一次性授权；Memory producer 与 TCP producer 各 MUST 只执行一次。升级 MUST 记录被替代场景和失败证据，不得把旧场景报告与新场景报告作相对回归证明；M2 基线内容和路径 MUST 保持不变。

#### Scenario: M5 v14 正式链全部通过
- **GIVEN** scenario v14 生产代码和计划已提交、完整门禁与静稳预检通过，且已获得绑定精确 HEAD、两个全新临时路径和各一次 Memory/TCP producer 的明确授权
- **WHEN** M5 Memory producer 执行唯一一次正式运行并通过显式 `13:14` 完整性、硬件一致性和绝对门禁，且 M5 TCP producer 在其唯一一次正式运行中相对该 Memory 报告通过跨 transport 比较
- **THEN** 项目 MUST 接受该 Memory 报告的精确字节作为 M5 当前基线，并记录硬件、提交、命令、报告哈希和被替代的 scenario v13

#### Scenario: 正式链任一步失败
- **WHEN** 无窗口报告生成、迁移完整性、绝对门禁或跨 transport 比较任一步失败
- **THEN** 项目 MUST 立即停止正式链，保留不可提升证据，不得重跑失败步骤、放宽阈值、提升诊断报告或创建和覆盖正式基线

#### Scenario: 新候选以全新正式链重试
- **GIVEN** 一个 scenario v14 候选的正式链失败，后续实现修复不改变 workload 或测量口径
- **WHEN** 新候选完成门禁、静稳预检并获得绑定精确 HEAD 和新路径的明确授权
- **THEN** 项目 MUST 使用两个全新路径重新执行完整 Memory/TCP 正式链，旧候选与诊断输出 MUST 保持冻结且不可提升

#### Scenario: 诊断输出不得提升
- **WHEN** 项目使用缩短阶段、临时 instrumentation、禁用部分天空工作或其他非正式方法定位性能根因
- **THEN** 这些输出 MUST 明确标记为诊断证据，不得传给正式迁移比较、复制为基线或消耗新候选的一次性正式运行机会

#### Scenario: 落后于目标分支的候选不得进入正式链
- **GIVEN** 一个 scenario v14 候选未包含目标分支已归档的前置变更
- **WHEN** 项目准备建立 M5 v14 正式基线
- **THEN** 该候选及其诊断输出 MUST 只保留为不可提升证据；项目 MUST 先更新到目标分支、重新完成门禁与静稳预检，并为新的精确 HEAD 和两个全新路径取得明确授权

#### Scenario: 历史 v12 正式链保持可追溯
- **GIVEN** 一条历史 scenario v12 正式链的报告、提交和路径记录完整
- **WHEN** 调用方读取该历史证据
- **THEN** 系统 MUST 保持其可读取和可审计，且不得将其提升为 M5 v14 当前基线

#### Scenario: 计时缺陷修复后使用新的正式链
- **GIVEN** 一条历史正式链已因 `remote_gpu_complete` 分位数失败
- **WHEN** 项目提交计时实现修复并准备再次建立基线
- **THEN** 失败报告 MUST 只保留为诊断证据，系统 MUST 为新的精确 HEAD、两个全新路径和新的明确授权执行 Memory/TCP 各一次，不得把新执行视为对旧 HEAD 失败步骤的重跑

#### Scenario: 历史 v10 正式链保持可追溯
- **GIVEN** 一条历史 scenario v10 正式链的报告、提交和路径记录完整
- **WHEN** 调用方读取该历史证据
- **THEN** 系统 MUST 保持其可读取和可审计，且不得将其提升为 M5 v14 当前基线

#### Scenario: 宿主静稳预检不消耗正式运行机会
- **WHEN** 静稳预检或 Memory 启动前只读复核失败
- **THEN** 项目 MUST 不启动 producer、不创建正式输出且不把该次预检计为 Memory/TCP 正式运行；后续只有在重新通过预检并取得绑定新证据的明确授权后才能开始

#### Scenario: 不主动改写宿主状态制造通过
- **WHEN** 宿主静稳预检未通过
- **THEN** 自动执行 MUST NOT 终止用户进程、清理系统缓存、切换供电模式或删除既有证据来制造通过条件，只能停止并保留只读证据

#### Scenario: 工作负载修复后重新开始
- **WHEN** 旧场景的正式链已经失败，且 benchmark workload 修复后升级为 scenario v14
- **THEN** 项目 MUST 提交更新后的计划、重新取得一次性正式授权并使用全新路径执行完整报告链，且不得提升旧场景输出或修复诊断报告

#### Scenario: 阶段屏障修复后使用新的正式链
- **GIVEN** 一条历史正式链已因 GPU 完成尾部退化停止，且报告揭示测量阶段缺少同步收尾屏障
- **WHEN** 项目提交阶段屏障修复并准备再次建立基线
- **THEN** 失败报告 MUST 只保留为诊断证据，系统 MUST 为新的精确 HEAD、两个全新路径和新的明确授权执行 Memory/TCP 各一次，不得把新执行视为对旧 HEAD 失败步骤的重跑

#### Scenario: 历史 v7 失败报告不得提升
- **GIVEN** M4D 的 scenario v7 报告曾因 `remote_gpu_complete` 尾部波动失败
- **WHEN** 后续场景完成并准备重建 M5 基线
- **THEN** 项目 MUST 使用新提交和全新路径重新取得正式报告，不得复制、改名或覆盖该 v7 失败报告

#### Scenario: 被阻塞的候选保持冻结
- **GIVEN** 某个已冻结候选的正式链失败，而该候选自身的全仓门禁全部通过
- **WHEN** 项目转而修复门禁缺陷或候选实现
- **THEN** 被阻塞候选的提交 MUST 保持可追溯且不被重跑或改写，并在新基线建立后才继续其归档流程

#### Scenario: M2 基线保持不变
- **WHEN** M5 当前基线升级到 scenario v14
- **THEN** M2 基线的内容和路径 MUST 保持不变，且比较器仍 MUST 拒绝 M2/M5 跨硬件比较

### Requirement: 调用者明确选择匹配硬件的基线

性能比较 SHALL 继续通过显式基线路径选择硬件专用文件，不引入自动硬件探测或跨硬件归一化。M5 当前基线升级为 scenario v14 后，后续 M5 报告 MUST 使用相同 scenario 才能执行相对回归比较；历史 M5 基线仍可由调用方显式选择并只与相同历史 scenario 比较。

#### Scenario: M5 v14 后续回归比较
- **WHEN** 调用者在 M5 上生成后续 scenario v14 报告并显式传入 M5 当前基线文件
- **THEN** 比较器 MUST 验证硬件和场景相同，再执行既有稳定指标与绝对门禁

#### Scenario: M5 v13 当前报告被拒绝
- **WHEN** 调用者使用 M5 scenario v14 基线比较 v13 或更早的当前报告且没有显式 `13:14` 迁移授权
- **THEN** 比较器 MUST 拒绝该组合并说明场景版本不一致，且不得修改任何基线文件

#### Scenario: M5 v13 历史回归比较
- **WHEN** 调用者显式选择一份 M5 scenario v13 历史基线与同场景当前报告
- **THEN** 比较器 MUST 验证硬件和场景相同，再执行该历史场景适用的稳定指标与绝对门禁

#### Scenario: M5 v12 当前报告被拒绝
- **WHEN** 调用者使用 M5 scenario v13 历史基线比较 scenario v12 当前报告且没有显式 `13:14` 迁移授权
- **THEN** 比较器 MUST 拒绝该组合并说明场景版本不一致，且不得修改任何基线文件

#### Scenario: M5 v12 历史回归比较
- **WHEN** 调用者显式选择一份 M5 scenario v12 历史基线与同场景当前报告
- **THEN** 比较器 MUST 验证硬件和场景相同，再执行该历史场景适用的稳定指标与绝对门禁

#### Scenario: M5 v11 当前报告被拒绝
- **WHEN** 调用者使用 M5 scenario v12 历史基线比较 scenario v11 当前报告且没有显式 `13:14` 迁移授权
- **THEN** 比较器 MUST 拒绝该组合并说明场景版本不一致，且不得修改任何基线文件

#### Scenario: M5 v10 历史回归比较
- **WHEN** 调用者显式选择一份 M5 scenario v10 历史基线与同场景当前报告
- **THEN** 比较器 MUST 验证硬件和场景相同，再执行该历史场景适用的稳定指标与绝对门禁

#### Scenario: M5 v9 当前报告被拒绝
- **WHEN** 调用者使用 M5 scenario v10 历史基线比较 scenario v9 当前报告且没有显式 `13:14` 迁移授权
- **THEN** 比较器 MUST 拒绝该组合并说明场景版本不一致，且不得修改任何基线文件

#### Scenario: 不自动选择基线
- **WHEN** 调用者未显式提供与当前报告硬件匹配的基线路径
- **THEN** 比较工具 MUST NOT 根据本机硬件自动选择、改写或归一化任何基线
