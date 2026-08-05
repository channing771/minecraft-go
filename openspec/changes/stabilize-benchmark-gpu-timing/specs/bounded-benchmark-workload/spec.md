## MODIFIED Requirements

### Requirement: 远端 GPU 完成探针边界稳定
scenario v12 及后续场景 SHALL 在固定 2560x1440 离屏目标上采集 `remote_gpu_complete`。一个样本 MUST 是一批固定数量远端角色与昵称绘制在同一个 command buffer 中提交、只等待一次完成的总耗时除以该批次数量；样本 MUST NOT 是单次提交到阻塞轮询返回的墙钟差，也不得包含标签准备、命令编码或资源释放。批次数量与样本数 MUST 固定且记录在报告中。自动执行 MUST 保持无窗口，不得启动或聚焦交互式客户端。

#### Scenario: 样本反映绘制成本而非轮询节拍
- **GIVEN** 宿主的完成等待实现存在固定节拍开销
- **WHEN** benchmark 记录一次 `remote_gpu_complete` 样本
- **THEN** 该节拍在一个样本内 MUST 最多出现一次并被批次数量摊薄，样本值 MUST 表示每次绘制的平均成本

#### Scenario: 分位数不再被量化到固定步长
- **GIVEN** 一次完整的 scenario v12 运行
- **WHEN** 比较 `remote_gpu_complete` 的 p50 与 p95
- **THEN** p95 相对 p50 的比值 MUST 明显小于 `2`，不得呈现相邻取值成整数倍的量化分布

#### Scenario: 空绘制与真实绘制可区分
- **GIVEN** 同一台设备上分别以相同批次数量提交空绘制与完整远端角色绘制
- **WHEN** 两者都按 `remote_gpu_complete` 的方式取样
- **THEN** 完整绘制的中位数 MUST 明显大于空绘制的中位数，证明指标携带绘制信号

#### Scenario: 首个样本等待传输收尾完成
- **GIVEN** benchmark 的 still/flying 阶段已经结束
- **WHEN** 系统关闭客户端会话并准备采集 `remote_gpu_complete`
- **THEN** 服务端 MUST 显式卸载 trusted observer 并同步关闭其 endpoint，且首个样本 MUST 在该操作返回后才开始

#### Scenario: 不依赖异步 writer 失败形成屏障
- **WHEN** Memory 或 TCP 对端关闭尚未触发 trusted observer writer 失败
- **THEN** benchmark MUST 仍能主动完成 observer 卸载，不得通过休眠、轮询超时或等待下一次发送来开始 GPU 采样

#### Scenario: 传输关闭失败时停止采样
- **WHEN** 服务端 trusted observer endpoint 或客户端 endpoint 的显式关闭返回错误
- **THEN** benchmark MUST 在首个 GPU 样本前失败，不得继续生成或写出可提升的性能报告

#### Scenario: v12 报告样本完整
- **WHEN** benchmark 成功生成一份 scenario v12 报告
- **THEN** `remote_gpu_complete.samples` MUST 等于配置的固定样本数，且 p50、p95、p99 和 max MUST 完整、为正并保持单调

#### Scenario: 自动性能验证不创建窗口
- **WHEN** 开发者或 CI 运行 scenario v12 benchmark
- **THEN** 系统 MUST 使用 headless device 和离屏纹理，不得创建、启动或聚焦游戏窗口

### Requirement: 工作负载变化使用新场景版本
改用批量分摊计时并加入阶段间冷却窗口后的 benchmark 报告 MUST 标记为 scenario v12；既有 scenario v6/v7/v8/v9/v10/v11 报告与基线 MUST 保持可读取，比较器不得把不同 scenario 当作同一工作负载静默相对比较。当前基线场景与 v12 之间 MUST 只通过唯一一条显式授权迁移，该迁移 MUST 反映真实的基线历史而非版本号连续性，且只执行完整性与绝对门禁。

#### Scenario: v12 同场景比较
- **WHEN** baseline 与 current 都是完整的 scenario v12 报告
- **THEN** 比较器 MUST 使用既有绝对门禁和回归门禁完成比较

#### Scenario: v11 同场景比较
- **WHEN** baseline 与 current 都是完整的 scenario v11 报告
- **THEN** 比较器 MUST 使用既有绝对门禁和回归门禁完成比较

#### Scenario: 当前基线与 v12 不静默混比
- **WHEN** baseline 为当前基线场景 v10、current 为 scenario v12 且没有显式迁移授权
- **THEN** 比较器 MUST 拒绝相对比较并说明场景版本不一致

#### Scenario: 显式授权当前基线到 v12 的迁移
- **WHEN** 调用方显式授权 `10:12` 迁移且两份报告的硬件身份一致
- **THEN** 比较器 MUST 执行既有完整性与绝对门禁，并跳过不同 workload 之间无意义的相对回归判定

#### Scenario: 从未成为基线的中间版本不提供迁移
- **GIVEN** scenario v11 的正式链因 GPU 计时缺陷失败，v11 从未成为任何硬件的基线
- **WHEN** 调用方使用 `11:12` 或 `10:11` 迁移参数
- **THEN** 比较器 MUST 拒绝比较并说明场景版本不一致

#### Scenario: 历史报告保持可校验
- **WHEN** 调用方单独读取一份完整 scenario v6、v7、v8、v9、v10 或 v11 报告
- **THEN** 比较器 MUST 按该历史场景原有的完整性规则校验，不得要求历史报告满足 v12 新字段

#### Scenario: 跨硬件迁移
- **WHEN** 两份不同 scenario 报告的硬件身份不同
- **THEN** 比较器 MUST 拒绝比较，且不得以场景升级为由执行跨硬件归一化

### Requirement: 性能阈值保持不变
scenario v8 及后续场景（包括 v12）MUST 继续使用现有 still、flying、RSS、服务端 tick、GPU 和 Memory/TCP 比较阈值；GPU 计时口径变化与冷却窗口不得提高 `20%` 相对回归阈值或放宽绝对门禁。

#### Scenario: v12 飞行尾延迟超限
- **WHEN** scenario v12 的 flying p99 大于或等于 `12ms`
- **THEN** 性能门禁 MUST 失败

#### Scenario: v12 权威 tick 绝对门禁保持不变
- **WHEN** scenario v12 的服务端 tick p99 达到既有绝对上限
- **THEN** 性能门禁 MUST 失败，不得因更换计时方式而放宽上限

## ADDED Requirements

### Requirement: 相对回归门禁只作用于分辨率足够的指标
性能比较 SHALL 只对测量分辨率远细于 `20%` 判定阈值的指标施加相对回归门禁；测量结果被宿主实现量化到固定步长的指标 MUST NOT 施加相对回归门禁，只能施加绝对上限门禁。比较器 MUST 在报告失败时指明所用的判定类型。

#### Scenario: 量化指标不施加相对门禁
- **GIVEN** 某个指标的测量结果被量化到固定步长，使相邻取值之间的相对差远大于 `20%`
- **WHEN** 比较器比较两份同场景报告
- **THEN** 该指标 MUST NOT 因跨越量化步长而报告相对回归失败，但其绝对上限门禁 MUST 继续生效

#### Scenario: 高分辨率指标保留相对门禁
- **GIVEN** 某个指标的测量分辨率远细于 `20%` 判定阈值
- **WHEN** 同硬件、同场景的该指标退化超过 `20%`
- **THEN** 性能门禁 MUST 失败

### Requirement: 客户端进程使用固定 Go 堆软上限
客户端进程 SHALL 设置一个固定的 Go 堆软上限，使高周转阶段不会把尚未回收的空闲堆累积进进程 RSS 峰值。该上限 MUST 明显高于实测活跃堆峰值，避免 GC 长期贴近上限运行；加上非 Go 分配后 MUST 仍明显低于既有 RSS 门禁。设置该上限 MUST NOT 改变任何被采集指标的定义、样本数或阶段时长，也 MUST NOT 放宽 RSS 门禁本身。

#### Scenario: 空闲堆不再进入 RSS 峰值
- **GIVEN** flying 阶段的密集区块周转产生大量短命分配
- **WHEN** 系统记录进程 RSS 峰值
- **THEN** 峰值 MUST 明显低于不设上限时的峰值，且活跃数据量 MUST NOT 因此减少

#### Scenario: 上限保留足够余量
- **GIVEN** 实测的活跃堆峰值
- **WHEN** 选定 Go 堆软上限
- **THEN** 上限 MUST 高于活跃堆峰值并保留足够余量，使 still 与 flying 的帧时间分位数 MUST NOT 因 GC 变频繁而越过既有绝对门禁

### Requirement: benchmark 阶段之间执行固定冷却
benchmark SHALL 在预热与 still、still 与 flying、flying 与 GPU 采样之间，以及 GPU 采样之后各执行一段固定时长的冷却；冷却期间 MUST NOT 提交渲染工作或推进相机脚本，并 SHALL 回收上一阶段产生的对象，避免其把后续阶段的 RSS 峰值推高。冷却时长 MUST 记录在报告中，且 MUST NOT 改变任何被采集指标的定义、样本数或阶段时长。

#### Scenario: 冷却不改变被测量
- **GIVEN** 一次完整的 scenario v12 运行
- **WHEN** 系统采集 still、flying 与 GPU 指标
- **THEN** 各阶段的时长、样本数与统计口径 MUST 与冷却引入前完全一致

#### Scenario: GPU 采样不紧接满载阶段
- **GIVEN** flying 阶段刚刚结束
- **WHEN** 系统准备采集 `remote_gpu_complete`
- **THEN** 采样 MUST 在冷却窗口结束之后才开始

#### Scenario: 采样产生的对象不推高后续 RSS 峰值
- **GIVEN** GPU 采样阶段分配了大量一次性图形对象
- **WHEN** 系统进入后续阶段并记录 RSS 峰值
- **THEN** 冷却 MUST 先回收这些对象，且既有 RSS 上限门禁 MUST 保持不变

#### Scenario: 报告记录冷却时长
- **WHEN** benchmark 成功生成一份 scenario v12 报告
- **THEN** 报告 MUST 包含所用的冷却时长，使该运行可被精确复现
