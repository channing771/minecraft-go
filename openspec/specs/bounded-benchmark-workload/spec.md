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

Avatar、NameTag 与 Hotbar HUD 为容纳最多七名远端玩家、四个伙伴、一个目标标签和固定聊天 overlay 而改变固定 GPU 上传布局、offset 与每帧写入字节数后，benchmark 报告 MUST 标记为 scenario v16。被测进程因流体呈现与生存能力而改变（mesh registry 条目加宽、quad 位布局改写、光照传播改为按方块查衰减表、新增水面绘制阶段与其固定容量实例缓冲、物理 `StepInput` 头版本递增）后，benchmark 报告 MUST 标记为 scenario v17。被测进程因农业而再次改变（mesh registry 条目上限与实际烘焙条目数同时抬高，使每次 mesh 调用的 FFI 输入变长；合成面板行数增加，使 Hotbar HUD 的固定上传布局、glyph offset、总容量与空聊天帧每帧实际写入字节数全部移动；权威 tick 新增一个每 tick 枚举活动兴趣范围内全部区段的作物阶段）后，benchmark 报告 MUST 标记为 scenario v18。被测进程因饥饿而再次改变（Hotbar HUD 新增饥饿条，使其固定上传布局、glyph offset、总容量与空聊天帧每帧实际写入字节数全部再次移动；HUD 图集在既有爱心之后新增两列程序化图标；权威 tick 新增饥饿三层状态的推进与结算）后，benchmark 报告 MUST 标记为 scenario v19。benchmark 的世界内容 MUST 与「注水是否默认开启」这一产品决策解耦：benchmark 路径的注水门控 MUST 被钉死为关闭，MUST NOT 读取用户配置，也 MUST NOT 随默认值翻转而改变，因此 v17、v18 与 v19 的被测世界 MUST 与 v16 逐格一致，且 MUST NOT 含任何农业方块。固定 benchmark 输入 MUST 继续是七名远端玩家、零伙伴且不注入聊天；v17、v18 与 v19 MUST 保持 `2560×1440` 离屏目标、阶段时长、运动、样本、指标、绝对阈值与 `20%` 相对回归阈值不变，其与前一场景的唯一差异 MUST 是上述被测进程自身的变化。p99、FPS、RSS、GPU、tick、队列高水位、绝对阈值和相对回归结果 MUST 只记录和报告，不得导致 producer、比较器或 CI 失败；只要报告结构、字段和样本完整且数据有效，producer MUST 写出 JSON。既有 scenario v6 至 v18 报告与基线 MUST 保持可读取，比较器不得把不同 scenario 静默作相对比较。当前不同场景之间 MUST 只接受唯一显式 `18:19` 迁移；该迁移 MUST 校验报告完整性与硬件身份并跳过跨 workload 的相对回归判定。历史 `17:18`、`16:17`、`15:16`、`14:15` 与更早迁移只作为既有报告和归档证据，不再是当前可授权迁移。任何其他迁移参数、损坏或缺字段报告、样本不完整、硬件身份不兼容、transport 或 commit 身份不一致、真实 overflow 或数据丢失以及 I/O 错误 MUST 继续失败；队列高水位数值本身 MUST 只记录。

#### Scenario: v16 固定上传布局完整

- **GIVEN** scenario v16 使用固定七名远端玩家、零伙伴和空聊天输入
- **WHEN** producer 准备 Avatar、NameTag 与 Hotbar HUD 固定上传
- **THEN** Avatar MUST 容纳 66 个 body parts、instance 区 5280 bytes、indirect offset 5536、总上传 5556 bytes
- **AND** NameTag MUST 容纳 12 个标签、background 区 768 bytes、glyph offset 1024、glyph 区 24576 bytes、总上传 25600 bytes
- **AND** Hotbar HUD MUST 容纳 236 个 quad 与 700 个 glyph、glyph offset 11776、总容量 45376 bytes，空聊天帧实际写入 MUST 为 11776 bytes

#### Scenario: v17 只因被测进程自身的变化区别于 v16

- **GIVEN** scenario v17 使用与 v16 相同的七名远端玩家、零伙伴和空聊天输入
- **WHEN** producer 准备该场景
- **THEN** 离屏目标、阶段时长、运动、样本与指标 MUST 与 v16 相同
- **AND** 二者的唯一差异 MUST 是被测进程自身因流体呈现与生存而改变的部分（registry 条目宽度、quad 位布局、光照衰减查表、水面绘制阶段与其固定实例缓冲、`StepInput` 头版本）

#### Scenario: v18 只因被测进程自身的变化区别于 v17

- **GIVEN** scenario v18 使用与 v17 相同的七名远端玩家、零伙伴和空聊天输入
- **WHEN** producer 准备该场景
- **THEN** 离屏目标、阶段时长、运动、样本与指标 MUST 与 v17 相同
- **AND** 被测世界 MUST 仍不注水且不含任何农业方块
- **AND** 二者的唯一差异 MUST 是被测进程自身因农业而改变的部分（mesh registry 条目上限与烘焙条目数、Hotbar HUD 固定上传布局与 glyph offset、权威 tick 的作物阶段）

#### Scenario: v18 的 Hotbar HUD 固定上传布局已移动

- **GIVEN** scenario v18 使用与 v17 相同的空聊天输入
- **WHEN** producer 准备 Hotbar HUD 固定上传
- **THEN** Hotbar HUD MUST 容纳 247 个 quad 与 700 个 glyph、glyph offset 12288、总容量 45888 bytes，空聊天帧实际写入 MUST 为 12288 bytes
- **AND** v17 的同一组数值 MUST 是 238 个 quad、glyph offset 11776、总容量 45376 bytes（v16 的 236 个 quad 与 v17 的 238 个只差氧气条，两者的 quad 区对齐到同一个 256 字节边界，故 offset 与总容量在 v16→v17 未移动），因此 v17 与 v18 两代报告的每帧上传字节数 MUST NOT 被直接比较

#### Scenario: v19 只因被测进程自身的变化区别于 v18

- **GIVEN** scenario v19 使用与 v18 相同的七名远端玩家、零伙伴和空聊天输入
- **WHEN** producer 准备该场景
- **THEN** 离屏目标、阶段时长、运动、样本与指标 MUST 与 v18 相同
- **AND** 被测世界 MUST 仍不注水且不含任何农业方块
- **AND** 二者的唯一差异 MUST 是被测进程自身因饥饿而改变的部分（Hotbar HUD 的饥饿条与其固定上传布局、HUD 图集新增的两列程序化图标、权威 tick 的饥饿状态推进）

#### Scenario: v19 的 Hotbar HUD 固定上传布局已移动

- **GIVEN** scenario v19 使用与 v18 相同的空聊天输入
- **WHEN** producer 准备 Hotbar HUD 固定上传
- **THEN** Hotbar HUD MUST 容纳 267 个 quad 与 700 个 glyph、glyph offset 13312、总容量 46912 bytes，空聊天帧实际写入 MUST 为 13312 bytes
- **AND** v18 的同一组数值 MUST 是 247 个 quad、glyph offset 12288、总容量 45888 bytes（饥饿条的 20 个 quad 使 quad 区跨过下一个 256 字节对齐边界，offset 与总容量因此同时移动），所以 v18 与 v19 两代报告的每帧上传字节数 MUST NOT 被直接比较

#### Scenario: benchmark 世界内容不随注水默认值漂移

- **GIVEN** 一份把注水门控写为开启的用户配置，且编译期默认值也是开启
- **WHEN** 以 benchmark 路径启动 producer
- **THEN** 该次运行的注水门控 MUST 为关闭
- **AND** benchmark 的被测世界 MUST 与注水默认开启之前逐格一致

#### Scenario: v19 同场景比较只记录性能

- **GIVEN** baseline 与 current 都是完整有效且身份兼容的 scenario v19 报告
- **WHEN** 比较器执行同场景比较
- **THEN** 比较器 MUST 输出既有绝对指标和相对回归记录，且任何性能数值变差都 MUST 返回成功

#### Scenario: v18 同场景比较只记录性能

- **GIVEN** baseline 与 current 都是完整有效且身份兼容的 scenario v18 报告
- **WHEN** 比较器执行同场景比较
- **THEN** 比较器 MUST 输出既有绝对指标和相对回归记录，且任何性能数值变差都 MUST 返回成功

#### Scenario: 只接受 18:19 跨场景迁移

- **GIVEN** 一份 scenario v18 基线与一份 scenario v19 当前报告
- **WHEN** 比较器以显式 `18:19` 迁移参数运行
- **THEN** 比较器 MUST 校验报告完整性与硬件身份并跳过跨 workload 的相对回归判定
- **AND** 任何其他迁移参数（含历史的 `17:18` 与 `16:17`）MUST 失败

#### Scenario: v17 同场景比较只记录性能

- **GIVEN** baseline 与 current 都是完整有效且身份兼容的 scenario v17 报告
- **WHEN** 比较器执行同场景比较
- **THEN** 比较器 MUST 输出既有绝对指标和相对回归记录，且任何性能数值变差都 MUST 返回成功

#### Scenario: v16 同场景比较只记录性能

- **GIVEN** baseline 与 current 都是完整有效且身份兼容的 scenario v16 报告
- **WHEN** 比较器执行同场景比较
- **THEN** 比较器 MUST 输出既有绝对指标和相对回归记录，且任何性能数值变差都 MUST 返回成功

#### Scenario: v15 同场景比较只记录性能

- **GIVEN** baseline 与 current 都是完整有效且身份兼容的 scenario v15 报告
- **WHEN** 比较器执行同场景比较
- **THEN** 比较器 MUST 输出该场景既有绝对指标和相对回归记录，且任何性能数值变差都 MUST 返回成功

### Requirement: 性能阈值保持不变
scenario v8 及后续场景（包括 v12）MUST 继续保存现有 still、flying、RSS、服务端 tick、GPU 和 Memory/TCP 比较阈值以及 `20%` 相对回归阈值，作为历史可比的性能记录口径。任何 p99、FPS、RSS、GPU、tick、队列高水位、绝对阈值或相对阈值结果 MUST 只写入记录并返回成功，不得影响 producer、比较器、CI 或 Memory 基线提升。

#### Scenario: v12 飞行尾延迟超限只记录
- **WHEN** scenario v12 的 flying p99 大于或等于 `12ms`
- **THEN** 比较器 MUST 记录该结果并返回成功

#### Scenario: v12 权威 tick 达到绝对阈值只记录
- **WHEN** scenario v12 的服务端 tick p99 达到既有绝对阈值
- **THEN** 比较器 MUST 保留该阈值与结果并返回成功

#### Scenario: v10 飞行尾延迟超限只记录
- **WHEN** scenario v10 的 flying p99 大于或等于 `12ms`
- **THEN** 比较器 MUST 记录该结果并返回成功

#### Scenario: v10 GPU 稳定分位数退化超限只记录
- **WHEN** 同硬件、同 scenario v10 的 `remote_gpu_complete` 受检分位数退化超过 `20%` 且绝对增量超过该指标的最小有意义增量
- **THEN** 比较器 MUST 保留阈值与退化记录并返回成功

#### Scenario: v10 权威 tick 达到绝对阈值只记录
- **WHEN** scenario v10 的服务端 tick p99 达到既有绝对阈值
- **THEN** 比较器 MUST 保留该阈值与结果并返回成功

### Requirement: 宿主静稳信息只作 provenance 记录
项目 MAY 记录自然冷却时长、load average、供电、电量、低电量模式和遗留进程作为性能报告 provenance，但这些状态及旧阈值 MUST NOT 成为 producer、比较器、CI 或 Memory 基线提升的前置条件。执行 MUST NOT 依赖绑定临时路径、一次性授权、失败即停或禁止重跑。

#### Scenario: 静稳状态不满足旧阈值仍可记录
- **WHEN** 任一宿主状态不满足旧静稳阈值
- **THEN** producer MUST 继续生成结构与样本完整的报告，且该状态 MUST NOT 阻止 Memory 基线提升

#### Scenario: 性能记录允许重新生成
- **WHEN** 调用方再次请求生成 Memory 或 TCP 性能记录
- **THEN** producer MUST 在输入与输出有效时执行，不得要求新的绑定路径或一次性授权

### Requirement: 远端 GPU 完成探针边界稳定
scenario v12 及后续场景 SHALL 在固定 2560x1440 离屏目标上采集 `remote_gpu_complete`。一个样本 MUST 是一批固定数量远端角色与昵称绘制在同一个 command buffer 中提交、只等待一次完成的总耗时除以该批次数量；样本 MUST NOT 是单次提交到阻塞轮询返回的墙钟差，也不得包含标签准备、命令编码或资源释放。批次数量与样本数 MUST 固定且记录在报告中。自动执行 MUST 保持无窗口，不得启动或聚焦交互式客户端。GPU 数值及其比率只作性能记录，不得改变退出状态或 Memory 基线提升。

#### Scenario: 样本反映绘制成本而非轮询节拍
- **GIVEN** 宿主的完成等待实现存在固定节拍开销
- **WHEN** benchmark 记录一次 `remote_gpu_complete` 样本
- **THEN** 该节拍在一个样本内 MUST 最多出现一次并被批次数量摊薄，样本值 MUST 表示每次绘制的平均成本

#### Scenario: 分位数比率只记录
- **GIVEN** 一次完整的 scenario v12 运行
- **WHEN** 比较 `remote_gpu_complete` 的 p50 与 p95
- **THEN** 项目 MUST 记录两者比率，且比率数值 MUST NOT 导致 producer、比较器或基线提升失败

#### Scenario: 样本只覆盖提交与完成轮询
- **GIVEN** 一批远端绘制命令已经完成编码
- **WHEN** benchmark 记录一次 `remote_gpu_complete` 样本
- **THEN** 计时 MUST 紧邻该批命令提交开始并在阻塞轮询返回时结束，准备、编码和释放事件均位于计时区间之外

#### Scenario: 空绘制与真实绘制差异只记录
- **GIVEN** 同一台设备上分别以相同批次数量提交空绘制与完整远端角色绘制
- **WHEN** 两者都按 `remote_gpu_complete` 的方式取样
- **THEN** 项目 MUST 记录两者的中位数差异，且差异数值 MUST NOT 改变退出状态

#### Scenario: 首个样本等待传输收尾完成
- **GIVEN** benchmark 的 still/flying 阶段已经结束
- **WHEN** 系统关闭客户端会话并准备采集 `remote_gpu_complete`
- **THEN** 服务端 MUST 显式卸载 trusted observer 并同步关闭其 endpoint，且首个样本 MUST 在该操作返回后才开始

#### Scenario: 不依赖异步 writer 失败形成屏障
- **WHEN** Memory 或 TCP 对端关闭尚未触发 trusted observer writer 失败
- **THEN** benchmark MUST 仍能主动完成 observer 卸载，不得通过休眠、轮询超时或等待下一次发送来开始 GPU 采样

#### Scenario: 传输关闭失败时停止采样
- **WHEN** 服务端 trusted observer endpoint 或客户端 endpoint 的显式关闭返回错误
- **THEN** benchmark MUST 在首个 GPU 样本前返回 I/O 错误，不得写出样本不完整的性能报告

#### Scenario: v12 报告样本完整
- **WHEN** benchmark 成功生成一份 scenario v12 报告
- **THEN** `remote_gpu_complete.samples` MUST 等于配置的固定样本数，且 p50、p95、p99 和 max MUST 完整、为正并保持单调

#### Scenario: v10 报告样本完整
- **WHEN** 比较器读取一份历史 scenario v10 报告
- **THEN** `remote_gpu_complete.samples` MUST 等于 `2048`，且 p50、p95、p99 和 max MUST 完整、为正并保持单调

#### Scenario: 自动性能验证不创建窗口
- **WHEN** 开发者或 CI 运行 scenario v12 benchmark
- **THEN** 系统 MUST 使用 headless device 和离屏纹理，不得创建、启动或聚焦游戏窗口

### Requirement: 相对回归记录区分测量噪声
性能比较 SHALL 保留每个指标及分位数的最小有意义增量，使输出能区分测量噪声与超过噪声的变化。无论绝对增量或相对变化多大，结果 MUST 只作性能记录并返回成功；该分类 MUST NOT 削弱报告结构、样本、provenance、身份、迁移、真实 overflow、数据丢失或 I/O 错误校验。

#### Scenario: 噪声级变化保持可读
- **GIVEN** 某个微秒级墙钟指标在两次运行之间的绝对增量落在实测噪声之内
- **WHEN** 比较器比较两份同场景报告
- **THEN** 输出 MUST 标记该变化位于噪声范围并返回成功

#### Scenario: 超过噪声的退化只记录
- **GIVEN** 某个指标的绝对增量超过其最小有意义增量
- **WHEN** 同硬件、同场景的该指标退化超过 `20%`
- **THEN** 输出 MUST 标记该退化超过噪声与相对阈值并返回成功

#### Scenario: 分位数各自保留噪声下限
- **GIVEN** 某个指标的中位数跨运行稳定而尾分位数固有波动接近两倍
- **WHEN** 比较器记录该指标
- **THEN** 中位数与尾分位数 MUST 分别使用各自的最小有意义增量，不得共用同一下限

### Requirement: 客户端进程使用固定 Go 堆软上限
客户端进程 SHALL 设置一个固定的 Go 堆软上限，使高周转阶段不会把尚未回收的空闲堆累积进进程 RSS 峰值。该上限 MUST 高于实测活跃堆峰值并保持固定，不得改变任何被采集指标的定义、样本数或阶段时长。RSS 与帧时间相对既有阈值的结果 MUST 只记录，不得改变退出状态或 Memory 基线提升。

#### Scenario: 空闲堆影响只记录
- **GIVEN** flying 阶段的密集区块周转产生大量短命分配
- **WHEN** 系统记录进程 RSS 峰值
- **THEN** 项目 MUST 保存峰值及其与历史记录的差异，且该数值 MUST NOT 改变退出状态

#### Scenario: 上限保留活跃堆余量
- **GIVEN** 实测的活跃堆峰值
- **WHEN** 选定 Go 堆软上限
- **THEN** 上限 MUST 高于活跃堆峰值；still、flying 与 RSS 相对既有阈值的结果 MUST 只记录

### Requirement: benchmark 阶段之间执行固定冷却
benchmark SHALL 在预热与 still、still 与 flying、flying 与 GPU 采样之间，以及 GPU 采样之后各执行一段固定时长的冷却；冷却期间 MUST NOT 提交渲染工作或推进相机脚本，并 SHALL 回收上一阶段产生的对象，避免其把后续阶段的 RSS 峰值推高。冷却时长 MUST 记录在报告中，且 MUST NOT 改变任何被采集指标的定义、样本数或阶段时长。冷却前后的性能数值及其阈值比较只作记录。

#### Scenario: 冷却不改变被测量
- **GIVEN** 一次完整的 scenario v12 运行
- **WHEN** 系统采集 still、flying 与 GPU 指标
- **THEN** 各阶段的时长、样本数与统计口径 MUST 与冷却引入前完全一致

#### Scenario: GPU 采样不紧接满载阶段
- **GIVEN** flying 阶段刚刚结束
- **WHEN** 系统准备采集 `remote_gpu_complete`
- **THEN** 采样 MUST 在冷却窗口结束之后才开始

#### Scenario: 采样后的 RSS 只记录
- **GIVEN** GPU 采样阶段分配了大量一次性图形对象
- **WHEN** 系统进入后续阶段并记录 RSS 峰值
- **THEN** 冷却 MUST 先回收这些对象，且 RSS 数值及其阈值比较 MUST 只记录

#### Scenario: 报告记录冷却时长
- **WHEN** benchmark 成功生成一份 scenario v12 报告
- **THEN** 报告 MUST 包含所用的冷却时长，使该运行可被精确复现

### Requirement: 错过 tick 输入边界时记录时间分解
服务端探针错过 tick 输入边界时 SHALL 记录总耗时、超出量、tick 自身耗时和取出信号时的积压量；发布时刻可得时还 SHALL 记录调度到发布、发布到取出两段耗时。缺少发布时刻时 MUST 明确标注缺失，不得输出依赖该时刻的无意义分段。该越界及其数值 MUST NOT 改变退出状态、停止后续记录或阻止 Memory 基线提升。

#### Scenario: 越界记录各分段后继续
- **GIVEN** 一次探针运行错过了 tick 输入边界
- **WHEN** benchmark 记录该事件
- **THEN** 输出 MUST 包含可得的时间分解与积压量，并继续完成结构与样本完整的报告

#### Scenario: 发布时刻不可得时不伪造分段
- **GIVEN** 某个信号缺少发布时刻
- **WHEN** benchmark 记录该信号的 tick 越界
- **THEN** 输出 MUST 标注发布时刻缺失，且 MUST NOT 报出依赖发布时刻计算的分段

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
