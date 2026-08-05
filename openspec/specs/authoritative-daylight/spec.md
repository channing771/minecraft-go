# authoritative-daylight Specification

## Purpose
为世界提供可持久化且多人一致的权威昼夜时间，并从权威方块确定性派生直射天空光，使露天、遮蔽空间、白天和夜晚形成有界且可验证的视觉差异。
## Requirements
### Requirement: 世界时间由服务端权威推进
服务端 SHALL 维护绝对 `WorldTimeTicks`，每个完成的权威 tick MUST 恰好增加 `1`，并以 `24000` tick 为一个显示昼夜周期。客户端 MUST 以最新有效权威玩家状态中的绝对时间决定昼夜相位，不得各自选择独立时间源。

#### Scenario: 两名玩家观察同一相位
- **GIVEN** 两名 Ready 玩家连接同一服务端
- **WHEN** 服务端发布同一个权威 tick 的玩家状态
- **THEN** Memory 或 TCP 客户端观察到的 `WorldTimeTicks` MUST 相同

#### Scenario: 每个权威 tick 只推进一次
- **GIVEN** 服务端当前绝对世界时间为 `23999`
- **WHEN** 服务端完成下一个权威 tick
- **THEN** 绝对时间 MUST 为 `24000`，显示相位 MUST 回到周期起点

#### Scenario: 旧状态不回退时间
- **GIVEN** 客户端已经接受一份较新 `ServerTick` 的玩家状态
- **WHEN** 客户端随后收到一份较旧或重复 `ServerTick` 的状态
- **THEN** 客户端 MUST 忽略该状态且不得回退已确认的世界时间

### Requirement: 世界时间通过 metadata v2 持久化
世界 metadata v2 SHALL 保存绝对 `WorldTimeTicks`。既有 metadata v1 世界 MUST 可迁移为 v2，迁移时世界时间取 `0`；自动保存 MUST 异步提交该保存边界观察到的最新权威时间，正常关服 MUST 持久化冻结后的最终权威时间，且 metadata I/O MUST NOT 阻塞权威 tick。

#### Scenario: v1 世界从黎明迁移
- **GIVEN** 一个 CRC 有效的 metadata v1 世界
- **WHEN** 新程序首次打开该世界
- **THEN** 系统 MUST 读取既有种子和出生信息、把 `WorldTimeTicks` 设为 `0`，并在下一次正常保存时写为 metadata v2

#### Scenario: 重启延续世界时间
- **GIVEN** 正常关服屏障已成功保存绝对时间 `12345`
- **WHEN** 服务端重新打开同一世界并完成初始化
- **THEN** 首份有效权威状态 MUST 从 `12345` 继续，而不是重置到客户端本地时间或默认相位

#### Scenario: 自动保存不阻塞 tick
- **GIVEN** metadata 保存底层 I/O 尚未完成
- **WHEN** 权威时钟继续产生 tick
- **THEN** simulation MUST 继续推进，且待保存时间 MUST 合并到最新值而不得形成无界保存队列

#### Scenario: metadata 原子保存失败保持可恢复
- **GIVEN** 磁盘上存在一份 CRC 有效的旧 metadata
- **WHEN** 新 metadata 在原子替换前失败，或在替换后的目录同步阶段失败
- **THEN** 替换前失败 MUST 保留旧文件；替换后失败 MUST 只留下 CRC 有效的完整旧版或完整新版，服务端 MUST 记录失败并按有界退避重试；最终关服仍失败时 MUST 返回错误

#### Scenario: 未来 metadata 稳定拒绝
- **GIVEN** 世界 metadata 声明高于 v2 的版本
- **WHEN** 当前程序尝试打开该世界
- **THEN** 打开 MUST 失败且原文件不得被覆盖

### Requirement: 直射天空光由权威方块确定性派生
系统 SHALL 对每个世界 X/Z 列使用最高非空气方块作为当前遮挡高度；采样位置严格高于该高度时天空光 MUST 为 `15`，否则 MUST 为 `0`。当前版本中空气是唯一透光方块，缺失邻区 MUST 按遮挡处理。该派生值 MUST 在 Memory/TCP 与重启前后保持一致，且不得作为独立负载写入协议或区块存档。

#### Scenario: 露天表面取得满天空光
- **GIVEN** 一个可见方块面的相邻空气位置严格高于该列最高非空气方块
- **WHEN** 客户端为该面生成网格
- **THEN** 面实例的天空光高四位 MUST 为 `15` 且方块光低四位 MUST 为 `0`

#### Scenario: 屋顶下方没有直射天空光
- **GIVEN** 一个可见方块面的相邻空气位置上方存在非空气方块
- **WHEN** 客户端为该面生成网格
- **THEN** 面实例的天空光高四位 MUST 为 `0`

#### Scenario: 缺失邻区不产生亮缝
- **GIVEN** 网格采样需要一个尚未加载的相邻区块
- **WHEN** 客户端生成边界区段网格
- **THEN** 缺失邻区 MUST 同时按实心和无天空光处理；邻区到达后相关 revision 印章 MUST 使旧结果失效并重新网格化

#### Scenario: 重启不保存派生光照
- **GIVEN** 一个已保存的区块只包含权威方块、掉落物和熔炉数据
- **WHEN** 服务端和客户端从相同方块数据重建区块
- **THEN** 两端派生的最高遮挡与直射天空光 MUST 相同，且区块 schema MUST 保持 v4

### Requirement: 遮挡变化只重做受影响的有界区段
最高遮挡改变时，客户端 SHALL 使新旧遮挡高度之间以及面和 AO 采样需要的水平相邻区段失效；无关高度和无关区块不得因昼夜时间推进而重新网格化。单次方块变化产生的天空光 dirty 集合 MUST 不超过 `96` 个区段，并继续由现有有界调度器合并和限流。

#### Scenario: 放置屋顶使下方变暗
- **GIVEN** 一个露天列的最高遮挡高度为 `Y=64`
- **WHEN** 权威方块变更在同列 `Y=80` 放置一个非空气方块并到达客户端
- **THEN** `Y=65..80` 涉及的区段与必要水平邻居 MUST 重新网格化，屋顶下方后续网格 MUST 使用天空光 `0`

#### Scenario: 移除屋顶恢复直射天空光
- **GIVEN** 一个列的最高遮挡位于 `Y=80`，其下一个非空气方块位于 `Y=64`
- **WHEN** `Y=80` 被权威移除并到达客户端
- **THEN** `Y=65..80` 涉及的区段与必要水平邻居 MUST 重新网格化，重新暴露位置 MUST 使用天空光 `15`

#### Scenario: 时间推进不触发重网格
- **GIVEN** 方块镜像及其 revision 没有变化
- **WHEN** `WorldTimeTicks` 进入新的昼夜相位
- **THEN** 已上传地形网格 MUST 保持有效，亮度变化 MUST 只更新每帧固定大小的渲染状态

### Requirement: 昼夜呈现使用固定亮度曲线
显示相位 `p = WorldTimeTicks mod 24000` SHALL 使用 `sun = max(0, sin(2πp/24000))` 和 `daylight = 0.15 + 0.85×sun`。天空光为 `s` 的地形基础亮度 MUST 为 `0.08 + (s/15)×(daylight-0.08)`，再乘既有朝向和 AO；远端玩家、掉落物和天空背景 SHALL 使用同一 `daylight`/`sun` 相位。HUD 与昵称 MUST 不受世界明暗影响。

#### Scenario: 正午露天达到全亮
- **GIVEN** 绝对世界时间显示相位为 `6000` 且地形面天空光为 `15`
- **WHEN** 客户端绘制该帧
- **THEN** `daylight` 和该面的昼夜基础亮度 MUST 都为 `1`

#### Scenario: 午夜保留最低可见度
- **GIVEN** 显示相位为 `18000`
- **WHEN** 客户端绘制露天面、遮蔽面、远端玩家和掉落物
- **THEN** 露天、玩家和掉落物昼夜亮度 MUST 为 `0.15`，遮蔽面基础亮度 MUST 为 `0.08`

#### Scenario: HUD 与昵称保持可读
- **GIVEN** 显示相位处于夜间
- **WHEN** 客户端绘制快捷栏、容器、采掘进度和远端昵称
- **THEN** 这些屏幕空间元素 MUST 保持既有颜色且不得乘世界昼夜亮度

### Requirement: 昼夜与直射天空光保持固定资源上限
每个区块的最高遮挡派生状态 MUST 恰好使用 `512` 字节固定存储；单次列顶下降时最多扫描世界高度 `384` 个方块。稳定世界时间推进 MUST NOT 产生堆分配、启动 goroutine、执行磁盘 I/O、扩展无界队列或触发地形重网格。

#### Scenario: 稳定昼夜 tick 工作量固定
- **GIVEN** 八名 Ready 玩家且本 tick 没有方块变化或保存屏障
- **WHEN** 服务端推进一次权威 tick
- **THEN** 世界时间推进 MUST 为常数工作量且不得产生由昼夜功能引入的堆分配或 I/O

#### Scenario: 极端列顶移除仍然有界
- **GIVEN** 一个列的最高非空气方块位于世界顶部且其余位置为空气
- **WHEN** 该最高方块被移除
- **THEN** 系统 MUST 在最多 `384` 次方块检查内得到新的遮挡高度，且不得启动独立光照任务或增长动态传播队列

