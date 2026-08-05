# bounded-benchmark-workload Specification

## Purpose

保证性能报告记录的是有界且接近交互客户端的逐帧工作负载，并在工作负载语义变化时通过场景版本阻止静默混比。
## Requirements
### Requirement: 计时帧分别限制消息与网格工作
性能 benchmark 的预热和正式计时帧 MUST 分别应用消息排空上限与网格工作上限；网格工作上限 MUST 与交互客户端一致，消息积压不得隐式扩大单帧网格调度或回收量。

#### Scenario: 消息排空上限大于网格上限
- **WHEN** benchmark 的一帧允许排空至多 `4096` 条服务端消息
- **THEN** 该帧的网格调度和完成结果回收仍分别不得超过 `64`

#### Scenario: 载入阶段快速收敛
- **WHEN** benchmark 尚未进入预热或正式计时阶段
- **THEN** 系统 MAY 使用更高的网格工作上限完成初始载入，且这些帧不得进入延迟样本

### Requirement: 工作负载变化使用新场景版本
扩展固定长度玩家输入与状态、废止即时破坏消息并在权威 tick 增加有界采掘判定后的 benchmark 报告 MUST 标记为 scenario v10；既有 scenario v6/v7/v8/v9 报告与基线 MUST 保持可读取，比较器不得把不同 scenario 当作同一工作负载静默相对比较。v9 与 v10 之间 MUST 只通过显式授权迁移，且迁移只执行完整性与绝对门禁。

#### Scenario: v10 同场景比较
- **WHEN** baseline 与 current 都是完整的 scenario v10 报告
- **THEN** 比较器 MUST 使用既有绝对门禁和回归门禁完成比较

#### Scenario: v9 同场景比较
- **WHEN** baseline 与 current 都是完整的 scenario v9 报告
- **THEN** 比较器 MUST 使用既有绝对门禁和回归门禁完成比较

#### Scenario: v8 同场景比较
- **WHEN** baseline 与 current 都是完整的 scenario v8 报告
- **THEN** 比较器 MUST 使用既有绝对门禁和回归门禁完成比较

#### Scenario: v7 同场景比较
- **WHEN** baseline 与 current 都是完整的 scenario v7 报告
- **THEN** 比较器 MUST 使用既有绝对门禁和回归门禁完成比较

#### Scenario: v9 与 v10 不静默混比
- **WHEN** baseline 为 scenario v9、current 为 scenario v10 且没有显式迁移授权
- **THEN** 比较器 MUST 拒绝相对比较并说明场景版本不一致

#### Scenario: 显式授权 v9 到 v10 迁移
- **WHEN** 调用方显式授权 `9:10` 迁移且两份报告的硬件身份一致
- **THEN** 比较器 MUST 执行既有完整性与绝对门禁，并跳过不同 workload 之间无意义的相对回归判定

#### Scenario: v8 与 v9 不静默混比
- **WHEN** baseline 为 scenario v8、current 为 scenario v9 且没有显式迁移授权
- **THEN** 比较器 MUST 拒绝相对比较并说明场景版本不一致

#### Scenario: 退役的 v8 到 v9 迁移参数被拒绝
- **WHEN** 调用方使用已经退役的 `8:9` 迁移参数比较 scenario v8 与 v9
- **THEN** 比较器 MUST 拒绝比较并说明场景版本不一致

#### Scenario: v7 与 v8 不静默混比
- **WHEN** baseline 为 scenario v7、current 为 scenario v8
- **THEN** 比较器 MUST 拒绝相对比较并说明场景版本不一致

#### Scenario: 未授权的 v6 与 v7 比较
- **WHEN** baseline 为 scenario v6、current 为 scenario v7 且没有显式迁移授权
- **THEN** 比较器 MUST 拒绝比较并说明场景版本不一致

#### Scenario: 退役的 v6 到 v7 迁移参数被拒绝
- **WHEN** 调用方使用已经退役的 `6:7` 迁移参数比较 scenario v6 与 v7
- **THEN** 比较器 MUST 拒绝比较并说明场景版本不一致

#### Scenario: 历史报告保持可校验
- **WHEN** 调用方单独读取一份完整 scenario v6、v7、v8 或 v9 报告
- **THEN** 比较器 MUST 按该历史场景原有的完整性规则校验，不得要求历史报告满足 v10 新字段

#### Scenario: 跨硬件迁移
- **WHEN** 两份不同 scenario 报告的硬件身份不同
- **THEN** 比较器 MUST 拒绝比较，且不得以场景升级为由执行跨硬件归一化

### Requirement: 性能阈值保持不变
scenario v8 及后续场景（包括 v10）MUST 继续使用现有 still、flying、RSS、服务端 tick、GPU 和 Memory/TCP 比较阈值；协议与采掘工作量变化不得提高 `20%` 相对回归阈值或放宽绝对门禁。

#### Scenario: v10 飞行尾延迟超限
- **WHEN** scenario v10 的 flying p99 大于或等于 `12ms`
- **THEN** 性能门禁 MUST 失败

#### Scenario: v10 GPU 稳定分位数退化超限
- **WHEN** 同硬件、同 scenario v10 的 `remote_gpu_complete` 任一受检分位数退化超过 `20%`
- **THEN** 性能门禁 MUST 失败，不得因功能增加而提高阈值

#### Scenario: v10 权威 tick 绝对门禁保持不变
- **WHEN** scenario v10 的服务端 tick p99 达到既有绝对上限
- **THEN** 性能门禁 MUST 失败，不得因增加采掘射线而放宽上限

### Requirement: 正式工作负载只在宿主静稳预检通过后启动
用于建立或升级硬件基线的 scenario v10 正式工作负载 MUST 在冻结候选完整门禁结束并自然冷却至少 5 分钟后启动。正式授权前 MUST 间隔至少 30 秒采集两次宿主状态；两次采样的 1 分钟和 5 分钟 load average 均 MUST 小于 `6.0`，且宿主 MUST 使用 AC 供电、关闭低电量模式、电池电量不少于 `50%`，并且不存在遗留 `mcgo` 或 `perfcheck` 进程。

#### Scenario: 静稳预检通过
- **GIVEN** 冻结候选的最后一个完整门禁进程已退出至少 5 分钟
- **WHEN** 两次相隔至少 30 秒的采样都满足负载、供电、电量、低电量模式和遗留进程条件
- **THEN** 项目 MAY 请求绑定精确 HEAD 和全新输出路径的一次性正式授权

#### Scenario: 静稳预检失败
- **WHEN** 任一次采样不满足任一静稳条件
- **THEN** 项目 MUST 在启动 benchmark producer 和请求一次性正式授权前停止，不得创建、删除或覆盖正式报告

#### Scenario: 正式启动前状态已经变化
- **GIVEN** 静稳预检曾经通过且用户已经授权精确正式边界
- **WHEN** Memory producer 启动前的只读复核不再满足相同的供电、低电量模式、负载或遗留进程条件
- **THEN** 项目 MUST 停止且不得消耗该正式运行机会，重新预检后必须重新请求授权

### Requirement: 远端 GPU 完成探针边界稳定
scenario v8 及后续场景 SHALL 在固定 2560x1440 离屏目标上采集恰好 2048 个远端角色与昵称绘制样本；每个样本 MUST 只包含提交命令到阻塞轮询完成的时间，不得包含标签准备、命令编码或资源释放。自动执行 MUST 保持无窗口，不得启动或聚焦交互式客户端。

#### Scenario: 样本只覆盖提交与完成轮询
- **GIVEN** 一个远端绘制命令已经完成编码
- **WHEN** benchmark 记录一次 `remote_gpu_complete` 样本
- **THEN** 计时 MUST 紧邻命令提交开始并在阻塞轮询返回时结束，准备、编码和释放事件均位于计时区间之外

#### Scenario: 首个样本等待传输收尾完成
- **GIVEN** benchmark 的 still/flying 阶段已经结束
- **WHEN** 系统关闭客户端会话并准备采集 `remote_gpu_complete`
- **THEN** 服务端 MUST 显式卸载 trusted observer 并同步关闭其 endpoint，且首个计时样本 MUST 在该操作返回后才开始

#### Scenario: 不依赖异步 writer 失败形成屏障
- **WHEN** Memory 或 TCP 对端关闭尚未触发 trusted observer writer 失败
- **THEN** benchmark MUST 仍能主动完成 observer 卸载，不得通过休眠、轮询超时或等待下一次发送来开始 GPU 采样

#### Scenario: 传输关闭失败时停止采样
- **WHEN** 服务端 trusted observer endpoint 或客户端 endpoint 的显式关闭返回错误
- **THEN** benchmark MUST 在首个 GPU 计时样本前失败，不得继续生成或写出可提升的性能报告

#### Scenario: v10 报告样本完整
- **WHEN** benchmark 成功生成一份 scenario v10 报告
- **THEN** `remote_gpu_complete.samples` MUST 等于 `2048`，且 p50、p95、p99 和 max MUST 完整、为正并保持单调

#### Scenario: 自动性能验证不创建窗口
- **WHEN** 开发者或 CI 运行 scenario v10 benchmark
- **THEN** 系统 MUST 使用 headless device 和离屏纹理，不得创建、启动或聚焦游戏窗口
