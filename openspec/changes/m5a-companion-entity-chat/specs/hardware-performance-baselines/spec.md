## MODIFIED Requirements

### Requirement: 新硬件基线只能由通过门禁的无窗口报告建立

本要求中的门禁 SHALL 只校验报告和数据的正确性，不校验性能数值。M5A 的完整 Memory/TCP scenario v16、`2560x1440` 无窗口报告 SHALL 作为可重复生成的 record-only 证据，MUST NOT 自动建立或提升硬件基线。既有 M2 Memory scenario v15 基线与 M5 Memory scenario v14 基线的精确字节和路径 MUST 保持不变；历史 scenario v6 至 v15 报告与 provenance MUST 保持可读取和可审计。Memory 与 TCP v16 producer MUST 各自独立生成、自比较并写入自身完整记录，不得要求另一 transport 先成功；只有调用方显式请求跨 transport 比较时，比较器才 MUST 校验两份报告的完整性、transport、硬件、scenario 与 commit 身份。性能数值 MUST 只记录；损坏、缺字段、样本不完整、身份不兼容、真实 overflow、数据丢失或 I/O 错误 MUST 使对应记录或比较失败。当前唯一跨 workload 授权 MUST 是 `15:16`，不得增加 `14:16`、恢复 `14:15` 或增加跨硬件迁移例外。

#### Scenario: M2 v15 与 M5 v14 基线保持精确不变

- **GIVEN** 项目已有已接受的 M2 scenario v15 与 M5 scenario v14 基线文件
- **WHEN** M5A 生成、比较或记录 scenario v16 报告
- **THEN** 两份既有基线的路径和精确字节 MUST 保持不变，任何 v16 报告 MUST NOT 自动覆盖或提升它们

#### Scenario: Memory v16 记录独立完成

- **GIVEN** 一次冻结提交上的完整有效 Memory scenario v16 无窗口运行
- **WHEN** producer 写入请求的 Memory 报告并以自身进行同场景校验
- **THEN** 项目 MUST 保存硬件、提交、transport、scenario、命令和报告哈希作为 record-only 证据，且 MUST NOT 要求 TCP 报告存在

#### Scenario: TCP v16 记录独立完成

- **GIVEN** 一次冻结提交上的完整有效 TCP scenario v16 无窗口运行
- **WHEN** producer 写入请求的 TCP 报告并以自身进行同场景校验
- **THEN** 项目 MUST 保存独立 record-only 证据，且 MUST NOT 要求与 Memory 的生成顺序或路径绑定

#### Scenario: 显式请求才执行跨 transport 比较

- **GIVEN** 两份已独立生成、同硬件、同 scenario v16 且同 commit 的完整 Memory 与 TCP 报告
- **WHEN** 调用方显式请求跨 transport 比较
- **THEN** 比较器 MUST 校验两份报告的 transport、硬件、scenario 和 commit 身份并输出 record-only 比较，且结果 MUST NOT 改变基线状态

#### Scenario: 性能退化不阻止记录

- **GIVEN** 完整有效的 Memory 或 TCP v16 报告
- **WHEN** 报告超过绝对阈值或跨 transport 比较显示性能变差
- **THEN** producer 或比较器 MUST 输出这些性能记录并返回成功，且 MUST NOT 因性能数值修改任何基线

#### Scenario: Memory 报告或身份无效只拒绝 Memory 记录

- **GIVEN** TCP 步骤可能尚未执行或已有独立结果
- **WHEN** Memory 报告损坏、缺少必需字段、样本不完整，或其 scenario、硬件、transport、commit 身份不符合请求
- **THEN** 项目 MUST 返回可读错误并拒绝该 Memory 记录，且不得修改任何基线或独立 TCP 记录

#### Scenario: TCP 报告无效不影响 Memory 记录

- **GIVEN** 一份完整有效的 Memory v16 record-only 报告
- **WHEN** TCP 报告损坏、缺少必需字段、样本不完整或自身数据无效
- **THEN** 项目 MUST 拒绝该 TCP 记录并返回可读错误，但 MUST NOT 删除、改写或重新分类 Memory 记录

#### Scenario: 跨 transport 比较输入无效只拒绝比较

- **GIVEN** 两份已独立生成的 Memory 与 TCP 记录
- **WHEN** 调用方显式请求比较，但输入损坏、缺字段、样本不完整，或 transport、硬件、scenario、commit 身份不匹配
- **THEN** 比较器 MUST 只拒绝该次比较，不得删除、改写或重新分类独立记录与既有基线

#### Scenario: 真实溢出或数据丢失保持正确性失败

- **GIVEN** 一份 Memory 或 TCP v16 报告
- **WHEN** 报告声明真实 overflow 或数据丢失
- **THEN** 项目 MUST 拒绝该记录或比较；若报告只有队列高水位升高，则 MUST 只记录该数值并返回成功

#### Scenario: 报告 I/O 错误仍失败

- **GIVEN** producer 已取得完整报告内容
- **WHEN** 无法写出请求的 Memory 或 TCP 报告
- **THEN** producer MUST 返回包含 I/O 上下文的错误，且不得修改既有基线或另一 transport 的记录

#### Scenario: 性能记录允许重新生成

- **GIVEN** 一次同类记录已经生成过
- **WHEN** 调用方再次请求生成 Memory 或 TCP 性能记录
- **THEN** producer MUST 在报告参数有效时执行，且不得要求绑定路径、宿主静稳快照或一次性正式授权

#### Scenario: 非 v16 workload 报告不得冒充当前记录

- **GIVEN** 报告使用缩短阶段、临时 instrumentation、禁用部分光照工作或不符合冻结 scenario v16 的上传布局
- **WHEN** 项目归类该报告
- **THEN** 项目 MUST 保留其诊断价值，但不得把它标记为当前 v16 record-only 证据或提升为任何基线

#### Scenario: 历史报告保持可追溯

- **GIVEN** 一份 scenario v6 至 v15 的历史报告、提交和路径记录完整
- **WHEN** 调用方读取该历史证据
- **THEN** 系统 MUST 保持其可读取和可审计，且不得将其直接提升或改写为 scenario v16

### Requirement: 调用者明确选择匹配硬件的基线

性能比较 SHALL 继续通过显式路径选择硬件专用基线或 record-only 报告，不引入自动硬件探测或跨硬件归一化。比较器 MUST 验证报告结构、硬件、scenario、transport 和 commit 身份；同一 scenario 的绝对指标和相对回归结果 MUST 只输出为记录，任何性能数值变差 MUST 返回成功。不同 scenario 之间 MUST 只接受显式 `15:16` 迁移并跳过相对比较；M2 v15 与 M5 v14 既有基线仍可由调用方显式选择，并只能与相同历史 scenario 比较。scenario v16 报告 MUST 保持 record-only，不得因路径选择或比较成功自动成为基线。

#### Scenario: v16 后续比较只记录性能

- **GIVEN** 调用者在同一硬件上生成两份完整 scenario v16 报告
- **WHEN** 显式传入匹配硬件、scenario、transport 和 commit 规则的路径进行比较
- **THEN** 比较器 MUST 输出既有绝对指标与相对回归记录，性能退化 MUST 返回成功，且任何路径都 MUST 不被自动提升为基线

#### Scenario: M2 v15 历史同场景比较

- **GIVEN** 当前 M2 基线是完整 scenario v15 报告
- **WHEN** 调用者显式选择该基线与完整 M2 scenario v15 当前报告
- **THEN** 比较器 MUST 验证报告身份并输出既有性能记录，且性能数值变差 MUST 返回成功

#### Scenario: M5 v14 历史同场景比较

- **GIVEN** 当前 M5 基线是完整 scenario v14 报告
- **WHEN** 调用者显式选择该基线与完整 M5 scenario v14 当前报告
- **THEN** 比较器 MUST 验证报告身份并输出该场景适用的性能记录，且性能数值变差 MUST 返回成功

#### Scenario: v15 与 v16 未授权混比被拒绝

- **GIVEN** baseline 为 scenario v15 且 current 为 scenario v16
- **WHEN** 调用者没有提供显式 `15:16` 迁移授权
- **THEN** 比较器 MUST 拒绝该组合并说明场景版本不一致，且不得修改任何报告或基线文件

#### Scenario: v15 到 v16 显式迁移

- **GIVEN** 调用者显式选择同硬件、同 transport 的完整 v15 baseline 与完整 v16 current
- **WHEN** 调用者提供 `15:16` 迁移
- **THEN** 比较器 MUST 验证完整性与硬件身份、输出绝对性能记录并跳过相对回归判定，且性能数值不理想 MUST 返回成功

#### Scenario: 旧迁移授权不再接受

- **GIVEN** baseline 与 current 的 scenario 不同
- **WHEN** 调用者传入 `6:15`、`14:15`、`14:16` 或其他非 `15:16` 授权
- **THEN** 比较器 MUST 拒绝该授权，且历史 v6 至 v15 报告只能分别进行同版本比较

#### Scenario: 跨硬件比较被拒绝

- **GIVEN** 当前报告与显式选择的 baseline 或 record-only 报告硬件身份不同
- **WHEN** 调用者请求比较或迁移
- **THEN** 比较器 MUST 拒绝比较，且不得执行跨硬件归一化

#### Scenario: 不自动选择基线

- **GIVEN** 本机同时存在 M2、M5 或 v16 record-only 路径信息
- **WHEN** 调用者未显式提供与当前报告硬件和用途匹配的路径
- **THEN** 比较工具 MUST NOT 根据本机硬件自动选择、改写、提升或归一化任何基线或报告
