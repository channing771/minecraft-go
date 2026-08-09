## MODIFIED Requirements

### Requirement: 新硬件基线只能由通过门禁的无窗口报告建立

本要求中的门禁 SHALL 只校验报告和数据的正确性，不校验性能数值。新建或升级硬件基线 MUST 来自当前冻结的 scenario v14、Memory transport、`2560x1440` 无窗口完整报告。M5 Memory 报告通过显式 `13:14` 迁移的完整性和硬件身份校验后，项目 MUST 立即接受其精确字节；TCP 报告缺失或延迟 MUST NOT 阻止该提升。TCP producer MUST 独立生成和写入自身完整记录，不得要求关联 Memory 报告或自动执行跨 transport 比较。只有调用方显式请求跨 transport 比较时，比较器才 MUST 校验两份报告的完整性、transport、硬件、scenario 和 commit 身份，并输出不影响基线的性能记录；比较请求的结构或身份校验失败 MUST 只拒绝该次比较，不得影响独立 TCP 记录或有效 Memory 基线。Memory 报告损坏、缺字段、样本不完整、scenario 迁移未授权或方向不兼容、硬件身份不兼容、真实 overflow 或数据丢失以及 I/O 错误 MUST 阻止基线提升；在独立生成阶段发现的 TCP 自身报告错误 MUST 只使 TCP 记录失败，不得撤销或阻止有效 Memory 报告的提升。生成记录和提升基线 MUST NOT 依赖绑定路径、宿主静稳快照、一次性正式授权、失败即停或禁止重跑；M2 基线内容和路径 MUST 保持不变。

#### Scenario: 完整 M5 v14 Memory 报告立即提升基线
- **GIVEN** 一份完整有效的 scenario v14 Memory 无窗口报告
- **WHEN** Memory 报告完成显式 `13:14` 完整性和硬件身份校验
- **THEN** 项目 MUST 接受 Memory 报告的精确字节作为 M5 当前基线，并记录硬件、提交、命令、报告哈希和被替代的 scenario v13

#### Scenario: TCP 记录独立完成
- **WHEN** 调用方独立请求生成和写入完整 TCP 报告，无论 Memory 报告是否已提升或是否存在
- **THEN** producer MUST 写入 TCP 记录，且 MUST NOT 要求与 Memory 的身份匹配或自动启动跨 transport 比较

#### Scenario: 显式请求才执行跨 transport 比较
- **GIVEN** 两份已独立生成的完整 Memory 与 TCP 报告
- **WHEN** 调用方显式请求跨 transport 比较
- **THEN** 比较器 MUST 校验两份报告的 transport、硬件、scenario 和 commit 身份并输出 record-only 比较，且结果 MUST NOT 改变基线状态

#### Scenario: 性能退化不阻止基线提升
- **WHEN** 完整有效的 M5 v14 Memory 或 TCP 报告超过绝对阈值，或跨 transport 比较显示性能变差
- **THEN** 比较 MUST 输出这些性能记录并返回成功，且项目 MUST NOT 仅因性能数值拒绝 Memory 基线提升

#### Scenario: Memory 报告或身份无效阻止提升
- **WHEN** Memory 报告损坏、缺少必需字段、样本不完整，或其 scenario、硬件或 transport 身份不符合提升请求
- **THEN** 项目 MUST 返回可读错误，且不得把该 Memory 报告提升为当前基线

#### Scenario: TCP 报告无效不影响 Memory 基线
- **GIVEN** 一份已满足提升条件的 M5 v14 Memory 报告
- **WHEN** TCP 报告损坏、缺少必需字段、样本不完整或自身数据无效
- **THEN** 项目 MUST 拒绝该 TCP 记录并返回可读错误，但 MUST NOT 阻止或撤销 Memory 基线提升

#### Scenario: 跨 transport 比较输入无效只拒绝比较
- **GIVEN** 两份已独立生成的 Memory 与 TCP 记录
- **WHEN** 调用方显式请求比较，但比较输入损坏、缺字段、样本不完整，或 transport、硬件、scenario、commit 身份不匹配
- **THEN** 比较器 MUST 只拒绝该次比较，不得删除、改写或重新分类独立记录，且 MUST NOT 阻止或撤销 Memory 基线提升

#### Scenario: 真实溢出或数据丢失保持正确性失败
- **WHEN** Memory 报告声明真实 overflow 或数据丢失
- **THEN** 项目 MUST 拒绝提升；若 TCP 报告声明这些错误则 MUST 只拒绝 TCP 记录或比较；若任一报告只有队列高水位升高，则 MUST 只记录该数值

#### Scenario: 报告与基线 I/O 错误仍失败
- **WHEN** producer 无法写出请求的报告，或项目无法精确写入请求的基线位置
- **THEN** 项目 MUST 返回包含 I/O 上下文的错误；TCP 写入错误 MUST NOT 影响已有效提升的 Memory 基线

#### Scenario: 性能记录允许重新生成
- **WHEN** 调用方再次请求生成 Memory 或 TCP 性能记录
- **THEN** producer MUST 在报告参数有效时执行，且不得要求绑定路径、宿主静稳快照或一次性正式授权

#### Scenario: 非 v14 workload 报告不得提升
- **WHEN** 报告使用缩短阶段、临时 instrumentation、禁用部分天空工作或其他不符合冻结 scenario v14 的 workload
- **THEN** 项目 MUST 保留其诊断价值，但不得把它提升为 M5 v14 当前基线

#### Scenario: 历史报告保持可追溯
- **GIVEN** 一份 scenario v6 至 v13 的历史报告、提交和路径记录完整
- **WHEN** 调用方读取该历史证据
- **THEN** 系统 MUST 保持其可读取和可审计，且不得将其直接提升为 M5 v14 当前基线

#### Scenario: M2 基线保持不变
- **WHEN** M5 当前基线升级到 scenario v14
- **THEN** M2 基线的内容和路径 MUST 保持不变，且比较器仍 MUST 拒绝 M2/M5 跨硬件比较

### Requirement: 调用者明确选择匹配硬件的基线

性能比较 SHALL 继续通过显式基线路径选择硬件专用文件，不引入自动硬件探测或跨硬件归一化。比较器 MUST 验证报告结构、硬件、scenario、transport 和 commit 身份；同一 scenario 的绝对指标和相对回归结果 MUST 只输出为记录，任何性能数值变差 MUST 返回成功。不同 scenario 之间 MUST 只接受显式 `13:14` 迁移并跳过相对比较；历史 M5 基线仍可由调用方显式选择并只与相同历史 scenario 比较。

#### Scenario: M5 v14 后续比较只记录性能
- **WHEN** 调用者在 M5 上生成完整 scenario v14 报告并显式传入 M5 当前基线文件
- **THEN** 比较器 MUST 验证报告身份并输出既有绝对指标与相对回归记录，且性能退化 MUST 返回成功

#### Scenario: M5 v13 与 v14 未授权混比被拒绝
- **WHEN** 调用者使用 M5 scenario v14 基线比较 v13 当前报告且没有显式 `13:14` 迁移授权
- **THEN** 比较器 MUST 拒绝该组合并说明场景版本不一致，且不得修改任何基线文件

#### Scenario: M5 v13 到 v14 显式迁移
- **WHEN** 调用者显式选择 M5 scenario v13 基线、完整 v14 当前报告和 `13:14` 迁移
- **THEN** 比较器 MUST 验证完整性与硬件身份、输出绝对性能记录并跳过相对回归判定，且性能数值不理想 MUST 返回成功

#### Scenario: M5 历史同场景比较
- **WHEN** 调用者显式选择一份 M5 scenario v6 至 v13 历史基线与相同历史 scenario 的完整当前报告
- **THEN** 比较器 MUST 验证报告身份并输出该场景适用的性能记录，且性能数值变差 MUST 返回成功

#### Scenario: 跨硬件比较被拒绝
- **WHEN** 当前报告与显式选择的基线硬件身份不同
- **THEN** 比较器 MUST 拒绝比较，且不得执行跨硬件归一化

#### Scenario: 不自动选择基线
- **WHEN** 调用者未显式提供与当前报告硬件匹配的基线路径
- **THEN** 比较工具 MUST NOT 根据本机硬件自动选择、改写或归一化任何基线

### Requirement: 同硬件回归只比较统计稳定指标
对于 scenario v6 及后续版本的同硬件、同 transport 报告，性能比较器 MUST 继续为具有稳定语义和可重复证据的字段输出相对回归记录。历史 `interest_diff` 字段 MUST 继续接受报告完整性校验，但其 p50、p95、p99 和 max 不得作为相对回归记录；该字段表示单会话完整发布时间，不得解释为纯兴趣差分耗时。任何稳定指标的退化数值 MUST 只记录并返回成功。

#### Scenario: 单会话发布时间尾部波动不进入回归记录
- **GIVEN** 两份同硬件、同 scenario、同 transport 的完整报告仅有 `interest_diff` 分位数相对变化超过配置阈值
- **WHEN** 性能比较器执行同场景比较
- **THEN** 比较 MUST 不把 `interest_diff` 的 p50、p95、p99 或 max 输出为相对回归，并 MUST 返回成功

#### Scenario: 单会话发布时间仍须完整有效
- **GIVEN** current 报告的 `interest_diff` 样本不足、数值非正或分位数不单调
- **WHEN** 性能比较器校验该报告
- **THEN** 比较 MUST 拒绝该报告并说明完整性错误

#### Scenario: 其他稳定服务端指标退化只记录
- **GIVEN** 两份同硬件、同 scenario、同 transport 的完整报告中，server tick 的适用分位数相对退化严格超过配置阈值
- **WHEN** 性能比较器执行同场景比较
- **THEN** 比较 MUST 输出对应 server tick 退化记录并返回成功

## REMOVED Requirements

### Requirement: 比较契约修正不得通过重新采样获利

**Reason**: record-only 决策取消一次性正式链、失败即停、禁止重跑和 TCP 前置，旧要求不再适用。
**Migration**: 历史报告继续按原始字节和 provenance 可读、可重判；调用方也可重新生成独立 Memory 或 TCP 记录，性能数值不得影响退出或 Memory 基线提升。

## ADDED Requirements

### Requirement: 历史报告重判与重新采样互不限制
项目 SHALL 保持历史报告原始字节、provenance 和统计口径可读取，并 MAY 对其重新执行 record-only 比较。调用方也 MAY 重新生成 Memory 或 TCP 报告；重新采样、步骤顺序或 TCP 缺失 MUST NOT 阻止完整有效的 Memory 报告提升基线。报告结构、样本、provenance、身份、迁移、真实 overflow、数据丢失和 I/O 错误仍 MUST 校验。

#### Scenario: 既有 Memory 报告可重新判定
- **GIVEN** 一份原始字节和 provenance 完整的历史 Memory 报告
- **WHEN** 当前比较器重新判定该报告
- **THEN** 比较器 MUST 保留其历史统计口径并输出 record-only 结果

#### Scenario: 重新生成记录不消耗唯一机会
- **WHEN** 调用方再次生成 Memory 或 TCP 报告
- **THEN** producer MUST 在输入和输出有效时执行，且不得因已经运行过同一步而拒绝

#### Scenario: TCP 步骤不阻止 Memory 提升
- **GIVEN** 一份完整有效且身份匹配的 Memory 报告
- **WHEN** TCP 尚未生成、生成失败或重复生成
- **THEN** 这些 TCP 状态 MUST NOT 阻止或撤销 Memory 基线提升
