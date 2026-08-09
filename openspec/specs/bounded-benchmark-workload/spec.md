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
横向天空光传播改变固定 benchmark 的网格 CPU/GPU workload 后，benchmark 报告 MUST 标记为 scenario v14。v14 MUST 保持 `2560×1440` 离屏目标、阶段时长、运动、样本、指标、绝对阈值和 `20%` 相对回归阈值不变；交互客户端和无窗口 benchmark 的 still/flying 帧 MUST 执行相同传播后的网格工作。p99、FPS、RSS、GPU、tick、队列高水位、绝对阈值和相对回归结果 MUST 只记录和报告，不得导致 producer、比较器或 CI 失败；只要报告结构、字段和样本完整且数据有效，producer MUST 写出 JSON。既有 scenario v6 至 v13 报告与基线 MUST 保持可读取，比较器不得把不同 scenario 静默作相对比较。scenario v13 与 v14 之间 MUST 只通过唯一显式 `13:14` 迁移；该迁移 MUST 校验报告完整性和硬件身份，并跳过跨 workload 的相对回归判定。任何其他迁移参数、损坏或缺字段报告、样本不完整、硬件身份不兼容、transport 或 commit 身份不一致、真实 overflow 或数据丢失以及 I/O 错误 MUST 继续失败；队列高水位数值本身 MUST 只记录。

#### Scenario: v14 同场景比较只记录性能
- **WHEN** baseline 与 current 都是完整有效、身份兼容的 scenario v14 报告
- **THEN** 比较器 MUST 输出既有绝对指标和相对回归记录，且任何性能数值变差都 MUST 返回成功

#### Scenario: v13 同场景比较只记录性能
- **WHEN** baseline 与 current 都是完整有效、身份兼容的 scenario v13 报告
- **THEN** 比较器 MUST 输出该场景既有绝对指标和相对回归记录，且任何性能数值变差都 MUST 返回成功

#### Scenario: v12 同场景比较只记录性能
- **WHEN** baseline 与 current 都是完整有效、身份兼容的 scenario v12 报告
- **THEN** 比较器 MUST 输出该场景既有性能比较记录，且任何性能数值变差都 MUST 返回成功

#### Scenario: v11 同场景比较只记录性能
- **WHEN** baseline 与 current 都是完整有效、身份兼容的 scenario v11 报告
- **THEN** 比较器 MUST 输出该场景既有性能比较记录，且任何性能数值变差都 MUST 返回成功

#### Scenario: v10 同场景比较只记录性能
- **WHEN** baseline 与 current 都是完整有效、身份兼容的 scenario v10 报告
- **THEN** 比较器 MUST 输出该场景既有性能比较记录，且任何性能数值变差都 MUST 返回成功

#### Scenario: v9 同场景比较只记录性能
- **WHEN** baseline 与 current 都是完整有效、身份兼容的 scenario v9 报告
- **THEN** 比较器 MUST 输出该场景既有性能比较记录，且任何性能数值变差都 MUST 返回成功

#### Scenario: v8 同场景比较只记录性能
- **WHEN** baseline 与 current 都是完整有效、身份兼容的 scenario v8 报告
- **THEN** 比较器 MUST 输出该场景既有性能比较记录，且任何性能数值变差都 MUST 返回成功

#### Scenario: v7 同场景比较只记录性能
- **WHEN** baseline 与 current 都是完整有效、身份兼容的 scenario v7 报告
- **THEN** 比较器 MUST 输出该场景既有性能比较记录，且任何性能数值变差都 MUST 返回成功

#### Scenario: v6 同场景比较只记录性能
- **WHEN** baseline 与 current 都是完整有效、身份兼容的 scenario v6 报告
- **THEN** 比较器 MUST 输出该场景既有性能比较记录，且任何性能数值变差都 MUST 返回成功

#### Scenario: v13 与 v14 不静默混比
- **WHEN** baseline 为 scenario v13、current 为 scenario v14 且没有显式迁移授权
- **THEN** 比较器 MUST 拒绝相对比较并说明场景版本不一致

#### Scenario: 显式授权 v13 到 v14 的迁移
- **WHEN** 调用方显式授权 `13:14` 迁移且两份报告完整有效、硬件身份一致
- **THEN** 比较器 MUST 输出绝对性能记录、跳过不同 workload 间的相对回归判定，且性能数值不理想 MUST 返回成功

#### Scenario: 唯一迁移之外的参数被拒绝
- **WHEN** 调用方使用 `12:13`、`11:13`、`12:14`、`14:13` 或其他非 `13:14` 参数
- **THEN** 比较器 MUST 拒绝比较并说明迁移方向不兼容

#### Scenario: 历史报告保持可读取
- **WHEN** 调用方单独读取一份完整 scenario v6 至 v13 报告
- **THEN** 比较器 MUST 按该历史场景原有完整性规则校验，不得要求其满足 v14 的场景版本

#### Scenario: 跨硬件迁移被拒绝
- **WHEN** 两份 scenario 不同且硬件身份不同的报告请求迁移比较
- **THEN** 比较器 MUST 拒绝比较，且不得以场景升级为由执行跨硬件归一化

#### Scenario: 完整报告不因性能数值中止写入
- **WHEN** producer 已取得完整有效的 v14 样本，但一个或多个性能指标超过绝对阈值或相对记录值
- **THEN** producer MUST 写出完整 JSON，且不得因这些性能数值返回失败

#### Scenario: 无效或不完整报告仍失败
- **WHEN** 报告损坏、缺少必需字段、样本不完整，或 transport 与 commit 身份和比较请求不一致
- **THEN** producer 或比较器 MUST 返回错误，且不得把该报告视为完整性能记录

#### Scenario: 队列高水位只记录而真实溢出失败
- **WHEN** 报告只显示队列高水位升高但没有 overflow 或数据丢失
- **THEN** 比较器 MUST 记录该数值并返回成功；若报告声明真实 overflow 或数据丢失，则 MUST 返回错误

#### Scenario: 报告写入错误仍失败
- **WHEN** producer 无法把完整报告写到请求的输出位置
- **THEN** producer MUST 返回包含 I/O 上下文的错误

#### Scenario: 实现优化保持 scenario v14
- **GIVEN** 优化前后的传播语义、固定分辨率、阶段时长、样本数和统计口径完全相同
- **WHEN** 项目只减少实现开销或修复资源滞留
- **THEN** producer MUST 继续标记 scenario v14，且比较器 MUST 继续输出既有 v14 性能记录

#### Scenario: workload 或测量口径变化不能藏在 v14
- **WHEN** 性能修复改变传播语义、固定分辨率、阶段时长、样本数、场景运动、指标定义或其他 benchmark workload
- **THEN** 项目 MUST 在再次生成报告前升级场景版本并修订迁移规则，不得把变化后的报告标记为 scenario v14

### Requirement: 性能阈值保持不变
scenario v8 及后续场景（包括 v12）MUST 继续使用现有 still、flying、RSS、服务端 tick、GPU 和 Memory/TCP 比较阈值；GPU 计时口径变化与冷却窗口不得提高 `20%` 相对回归阈值或放宽绝对门禁。

#### Scenario: v12 飞行尾延迟超限
- **WHEN** scenario v12 的 flying p99 大于或等于 `12ms`
- **THEN** 性能门禁 MUST 失败

#### Scenario: v12 权威 tick 绝对门禁保持不变
- **WHEN** scenario v12 的服务端 tick p99 达到既有绝对上限
- **THEN** 性能门禁 MUST 失败，不得因更换计时方式而放宽上限

#### Scenario: v10 飞行尾延迟超限
- **WHEN** scenario v10 的 flying p99 大于或等于 `12ms`
- **THEN** 性能门禁 MUST 失败

#### Scenario: v10 GPU 稳定分位数退化超限
- **WHEN** 同硬件、同 scenario v10 的 `remote_gpu_complete` 受检分位数退化超过 `20%` 且绝对增量超过该指标的最小有意义增量
- **THEN** 性能门禁 MUST 失败，不得因功能增加而提高阈值

#### Scenario: v10 权威 tick 绝对门禁保持不变
- **WHEN** scenario v10 的服务端 tick p99 达到既有绝对上限
- **THEN** 性能门禁 MUST 失败，不得因增加功能而放宽上限

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
scenario v12 及后续场景 SHALL 在固定 2560x1440 离屏目标上采集 `remote_gpu_complete`。一个样本 MUST 是一批固定数量远端角色与昵称绘制在同一个 command buffer 中提交、只等待一次完成的总耗时除以该批次数量；样本 MUST NOT 是单次提交到阻塞轮询返回的墙钟差，也不得包含标签准备、命令编码或资源释放。批次数量与样本数 MUST 固定且记录在报告中。自动执行 MUST 保持无窗口，不得启动或聚焦交互式客户端。

#### Scenario: 样本反映绘制成本而非轮询节拍
- **GIVEN** 宿主的完成等待实现存在固定节拍开销
- **WHEN** benchmark 记录一次 `remote_gpu_complete` 样本
- **THEN** 该节拍在一个样本内 MUST 最多出现一次并被批次数量摊薄，样本值 MUST 表示每次绘制的平均成本

#### Scenario: 分位数不再被量化到固定步长
- **GIVEN** 一次完整的 scenario v12 运行
- **WHEN** 比较 `remote_gpu_complete` 的 p50 与 p95
- **THEN** p95 相对 p50 的比值 MUST 明显小于 `2`，不得呈现相邻取值成整数倍的量化分布

#### Scenario: 样本只覆盖提交与完成轮询
- **GIVEN** 一批远端绘制命令已经完成编码
- **WHEN** benchmark 记录一次 `remote_gpu_complete` 样本
- **THEN** 计时 MUST 紧邻该批命令提交开始并在阻塞轮询返回时结束，准备、编码和释放事件均位于计时区间之外

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

#### Scenario: v10 报告样本完整
- **WHEN** 比较器读取一份历史 scenario v10 报告
- **THEN** `remote_gpu_complete.samples` MUST 等于 `2048`，且 p50、p95、p99 和 max MUST 完整、为正并保持单调

#### Scenario: 自动性能验证不创建窗口
- **WHEN** 开发者或 CI 运行 scenario v12 benchmark
- **THEN** 系统 MUST 使用 headless device 和离屏纹理，不得创建、启动或聚焦游戏窗口

### Requirement: 相对回归门禁只作用于超过测量噪声的变化
性能比较 SHALL 为每个受相对回归判定的指标声明其最小有意义增量；当两份报告之间的绝对增量小于等于该值时，比较器 MUST NOT 报告相对回归，因为该变化落在宿主的量化步长或运行间测量噪声之内。最小有意义增量 MUST 由同硬件的实测波动确定，并 MUST 按分位数分别设定——同一指标的中位数与尾分位数可能有量级不同的固有波动。该规则 MUST NOT 削弱完整性与绝对上限门禁，也 MUST NOT 抑制超过该增量的退化。比较器 MUST 在报告失败时指明所用的判定类型。

#### Scenario: 噪声级变化不报告回归
- **GIVEN** 某个微秒级墙钟指标在两次运行之间的绝对增量落在实测噪声之内
- **WHEN** 比较器比较两份同场景报告
- **THEN** 该指标 MUST NOT 因相对变化超过 `20%` 而失败，但其完整性与绝对上限门禁 MUST 继续生效

#### Scenario: 超过噪声的退化仍被判定
- **GIVEN** 某个指标的绝对增量超过其最小有意义增量
- **WHEN** 同硬件、同场景的该指标退化超过 `20%`
- **THEN** 性能门禁 MUST 失败

#### Scenario: 分位数各自设定下限
- **GIVEN** 某个指标的中位数跨运行稳定而尾分位数固有波动接近两倍
- **WHEN** 比较器判定该指标
- **THEN** 中位数 MUST 继续接受相对判定，尾分位数 MUST 按其实测波动豁免，且两者 MUST 分别声明而非共用同一下限

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

### Requirement: 错过 tick 输入边界的失败必须报出时间分解

服务端探针因错过 tick 输入边界而失败时，错误信息 MUST 包含足以判定该次超时归属的时间分解，MUST NOT 只报"错过了期限"。

分解 MUST 至少能区分以下三种归属，因为它们的正确处置互相冲突：

- **被测系统慢**：tick 自身的执行耗时接近或超过整个边界预算。此种情况 MUST 按性能回归处理，MUST NOT 用跳过或重试掩盖。
- **信号投递延迟**：从调度到发布之间耗时过大。
- **测量方落后**：信号在缓冲中排队等待测量方取出。

分解 MUST 包含取出信号时的待处理信号积压量——该项单独一项即可判定测量方是否落后。

放宽期限本身 MUST NOT 作为本要求的替代实现：期限值与所有界限断言保持不变，本要求只改变失败信息的内容。

#### Scenario: 超时错误报出各分段

- **GIVEN** 一次探针运行错过了 tick 输入边界
- **WHEN** 该失败被报告
- **THEN** 错误信息 MUST 包含总耗时、超出量、tick 自身耗时，以及取出信号时的积压量
- **AND** 在发布时刻可得时 MUST 另外包含调度到发布、发布到取出两段耗时

#### Scenario: 发布时刻不可得时不得报出无意义分段

- **GIVEN** 某个信号缺少发布时刻
- **WHEN** 该信号导致的超时被报告
- **THEN** 错误信息 MUST 标注发布时刻缺失
- **AND** MUST NOT 报出依赖发布时刻计算得出的分段

#### Scenario: 判定条件不因本要求改变

- **GIVEN** 一次探针运行
- **WHEN** 比较实现本要求前后的通过与失败条件
- **THEN** 两者 MUST 完全一致——本要求只改变失败信息的内容，不改变任何测试的通过与否

### Requirement: 诊断代码必须可在不依赖失败环境的前提下验证

只在失败路径执行的诊断代码 MUST 具备不依赖复现该失败的验证手段。一段从不执行的诊断分支等同于未实现——它会在真正需要时才暴露缺陷，而那时已经没有第二次机会。

当目标失败形态无法在开发环境复现时，诊断信息的组装 MUST 可被独立于其触发条件地测试。

诊断代码 MUST NOT 依赖临时调整共享配置常量来触发验证，除非已确认该常量不被其他行为使用。

#### Scenario: 诊断组装可被独立测试

- **GIVEN** 一段只在失败时执行的诊断信息组装逻辑
- **WHEN** 为其编写验证
- **THEN** MUST 能在不制造真实失败的前提下，以构造的输入直接验证其输出

#### Scenario: 共享常量不得被临时改动用于验证

- **GIVEN** 某个配置常量同时被诊断路径与其他行为使用
- **WHEN** 需要验证诊断路径
- **THEN** MUST NOT 通过临时改动该常量来触发，因为那会连带改变其他行为，使验证结果不可信
