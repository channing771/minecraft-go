# hardware-performance-baselines Specification

## Purpose

为不同 Apple Silicon 硬件保存彼此独立、来源可审计的性能比较起点，避免把芯片和内存差异误判为代码性能变化。
## Requirements
### Requirement: 不同硬件使用独立基线
项目 SHALL 为硬件标识不同的正式报告保存独立基线，并 MUST 保留已经接受的其他硬件基线不变。

#### Scenario: 为 M5 建立基线
- **WHEN** 当前报告的硬件标识为 `Apple M5 / 24GiB`，而现有基线标识为 `Apple M2 / 16GiB`
- **THEN** 项目新增 M5 专用基线文件，并保持 M2 基线内容和路径不变

#### Scenario: 拒绝跨硬件比较
- **WHEN** 使用 M2 基线比较 M5 当前报告
- **THEN** 性能比较 MUST 拒绝该组合，且任何基线文件均不得被覆盖

### Requirement: 新硬件基线只能由通过门禁的无窗口报告建立
新建或升级硬件基线 MUST 来自当前冻结的 scenario v12、Memory transport、2560x1440 无窗口正式报告；该报告 MUST 通过完整性和绝对门禁，并 MUST 与同一硬件、同一场景的一次 TCP 报告通过现有跨 transport 比较后才能接受。升级既有硬件基线时 MUST 明确记录被替代场景和失败证据，不得把旧场景报告与新场景报告作相对回归证明。

#### Scenario: v12 正式链全部通过
- **GIVEN** scenario v12 生产代码和计划已提交，且 Memory/TCP 使用两个全新临时路径
- **WHEN** M5 Memory 报告通过显式 v11→v12 完整性和绝对门禁，且同次 M5 TCP 报告相对该 Memory 报告通过跨 transport 比较
- **THEN** 项目接受该 Memory 报告的精确字节作为 M5 当前基线，并记录硬件、提交、命令、报告哈希和被替代的场景

#### Scenario: 正式链任一步失败
- **WHEN** 无窗口报告生成、完整性门禁或跨 transport 比较任一步失败
- **THEN** 项目 MUST 停止正式链，不得重跑失败步骤、放宽阈值、提升诊断报告或创建和覆盖正式基线

#### Scenario: 计时缺陷修复后使用新的正式链
- **GIVEN** 一条 scenario v11 正式链已因 `remote_gpu_complete` 分位数跨越轮询节拍量化步长而在跨 transport 比较失败
- **WHEN** 项目提交 GPU 时间戳计时修复并准备再次建立基线
- **THEN** 失败报告 MUST 只保留为诊断证据，系统 MUST 使用新的精确 HEAD、两个全新路径和新的明确授权执行 Memory/TCP 各一次，不得把新执行视为对旧 HEAD 失败步骤的重跑

#### Scenario: v10 正式链全部通过
- **GIVEN** scenario v10 生产代码和计划已提交，且 Memory/TCP 使用两个全新临时路径
- **WHEN** M5 Memory 报告通过显式 v9→v10 完整性和绝对门禁，且同次 M5 TCP 报告相对该 Memory 报告通过跨 transport 比较
- **THEN** 项目接受该 Memory 报告的精确字节作为 M5 当前基线，并记录硬件、提交、命令、报告哈希和被替代的 v9 场景

#### Scenario: 宿主静稳预检不消耗正式运行机会
- **WHEN** 静稳预检或 Memory 启动前只读复核失败
- **THEN** 项目 MUST 不启动 producer、不创建正式输出且不把该次预检计为 Memory/TCP 正式运行；后续只有在重新通过预检并取得绑定新证据的明确授权后才能开始

#### Scenario: 不主动改写宿主状态制造通过
- **WHEN** 宿主静稳预检未通过
- **THEN** 自动执行 MUST NOT 终止用户进程、清理系统缓存、切换供电模式或删除既有证据来制造通过条件，只能停止并保留只读证据

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

#### Scenario: 被阻塞的候选保持冻结
- **GIVEN** 某个已冻结候选的正式链因门禁缺陷失败，而该候选自身的全仓门禁全部通过
- **WHEN** 项目转而修复门禁缺陷
- **THEN** 被阻塞候选的代码与规格 MUST 保持冻结不被重跑或改写，并在新基线建立后才继续其归档流程

#### Scenario: M2 基线保持不变
- **WHEN** M5 当前基线升级到 scenario v12
- **THEN** M2 基线的内容和路径 MUST 保持不变，且比较器仍 MUST 拒绝 M2/M5 跨硬件比较

### Requirement: 调用者明确选择匹配硬件的基线
性能比较 SHALL 继续通过显式基线路径选择硬件专用文件，不引入自动硬件探测或跨硬件归一化。M5 当前基线升级为 scenario v12 后，后续 M5 报告 MUST 使用相同 scenario 才能执行相对回归比较。

#### Scenario: M5 v12 后续回归比较
- **WHEN** 调用者在 M5 上生成后续 scenario v12 报告并显式传入 M5 当前基线文件
- **THEN** 比较器 MUST 验证硬件和场景相同，再执行既有稳定指标与绝对门禁

#### Scenario: M5 v11 当前报告被拒绝
- **WHEN** 调用者使用 M5 scenario v12 基线比较 scenario v11 当前报告且没有显式迁移授权
- **THEN** 比较器 MUST 拒绝该组合并说明场景版本不一致，且不得修改任何基线文件

#### Scenario: M5 v10 后续回归比较
- **WHEN** 调用者在 M5 上生成后续 scenario v10 报告并显式传入 M5 当前基线文件
- **THEN** 比较器 MUST 验证硬件和场景相同，再执行既有稳定指标与绝对门禁

#### Scenario: M5 v9 当前报告被拒绝
- **WHEN** 调用者使用 M5 scenario v10 基线比较 scenario v9 当前报告且没有显式迁移授权
- **THEN** 比较器 MUST 拒绝该组合并说明场景版本不一致，且不得修改任何基线文件

#### Scenario: 不自动选择基线
- **WHEN** 调用者未显式提供与当前报告硬件匹配的基线路径
- **THEN** 比较工具 MUST NOT 根据本机硬件自动选择、改写或归一化任何基线

### Requirement: 同硬件回归只比较统计稳定指标
对于 scenario v6 及后续版本的同硬件、同 transport 报告，性能比较器 MUST 只对具有稳定语义和可重复证据的字段执行相对回归比较。历史 `interest_diff` 字段 MUST 继续接受报告完整性校验，但其 p50、p95、p99 和 max 不得作为相对回归失败依据；该字段表示单会话完整发布时间，不得解释为纯兴趣差分耗时。

#### Scenario: 单会话发布时间尾部波动不阻断回归
- **GIVEN** 两份同硬件、同 scenario、同 transport 的完整报告仅有 `interest_diff` 分位数相对变化超过配置阈值
- **WHEN** 性能比较器执行同场景回归比较
- **THEN** 比较 MUST 不因 `interest_diff` 的 p50、p95、p99 或 max 失败

#### Scenario: 单会话发布时间仍须完整有效
- **GIVEN** current 报告的 `interest_diff` 样本不足、数值非正或分位数不单调
- **WHEN** 性能比较器校验该报告
- **THEN** 比较 MUST 拒绝该报告并说明完整性错误

#### Scenario: 其他稳定服务端指标仍然判退化
- **GIVEN** 两份同硬件、同 scenario、同 transport 的完整报告中，server tick 的适用分位数相对退化严格超过配置阈值
- **WHEN** 性能比较器执行同场景回归比较
- **THEN** 比较 MUST 失败且指出对应 server tick 指标

### Requirement: 比较契约修正不得通过重新采样获利
当一份不可变正式报告只因已证实不稳定的相对指标失败，而 benchmark producer、工作负载、报告 schema 和绝对门禁均未变化时，项目 SHALL 在修正比较契约后重新判定该报告的原始字节，不得重新运行同一步采样、改写报告或覆盖基线。后续尚未执行的正式步骤 MAY 在原始精确生产提交上按既有一次性规则继续。

#### Scenario: 重新判定既有 Memory 报告
- **GIVEN** M4D scenario v8 Memory 报告的哈希和原始字节保持不变，且旧比较只因 `interest_diff` 相对波动失败
- **WHEN** 修正后的比较器重新判定该报告
- **THEN** 系统 MUST 复用原始报告并执行全部仍适用的完整性、绝对和相对门禁，不得启动新的 Memory benchmark

#### Scenario: 继续唯一一次 TCP 步骤
- **GIVEN** 重新判定的 Memory 报告通过，原计划中的 TCP 报告尚未生成
- **WHEN** 正式链继续
- **THEN** 项目 MAY 从与 Memory 相同的原始精确生产提交生成一次无窗口 TCP 报告；任一步失败 MUST 立即停止且不得重跑或覆盖基线
