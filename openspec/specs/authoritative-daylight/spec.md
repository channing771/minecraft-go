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
系统 SHALL 仅从客户端已接受的权威方块镜像派生 `0..15` 总天空光。每个世界 X/Z 列严格高于最高非空气方块的空气单元 MUST 是亮度 `15` 的直射起点；非直射天空光每横向传播一格 MUST 减 `1`，从起点计第 `15` 格 MUST 为 `1`，下一格 MUST 为 `0`。不透明方块 MUST 阻断传播；流体 MUST 透光但每格 MUST 额外衰减，且竖直向下穿过流体 MUST NOT 无损；除空气与流体外的方块仍 MUST 阻断，缺失或未知邻区 MUST 同时按阻挡和黑暗处理。传播 MUST 跨已加载区段和区块连续。地形 packed 光照的高四位 MUST 保存天空光，低四位 MUST 保存独立派生的方块光；系统不得把任一派生值作为独立负载写入线上协议、玩家 schema、区块 schema 或 metadata。

#### Scenario: 露天表面取得满天空光
- **GIVEN** 一个可见方块面的相邻空气位置严格高于该列最高非空气方块
- **WHEN** 客户端为该面生成网格
- **THEN** 面实例的天空光高四位 MUST 为 `15`，方块光低四位 MUST 独立反映相邻空气的派生方块光

#### Scenario: 洞口内按距离递减
- **GIVEN** 一条由不透明方块围成、只有一个露天开口的空气通道
- **WHEN** 客户端为通道生成网格
- **THEN** 从直射起点计第 `15` 格的天空光高四位 MUST 为 `1`，下一格 MUST 为 `0`，且中间每横向格 MUST 恰好递减 `1`

#### Scenario: 不透明屋顶阻断但侧向开口可照亮
- **GIVEN** 一个可见方块面的相邻空气位置上方存在非空气方块，且一侧有到露天的空气路径
- **WHEN** 客户端为该面生成网格
- **THEN** 该面 MUST 使用由侧向路径派生的非零天空光，而不是仅因失去直射光变为 `0`

#### Scenario: 水面之下随深度变暗但不立刻归零
- **GIVEN** 一片露天水体，其正上方没有任何非流体遮挡
- **WHEN** 客户端派生水下不同深度的天空光
- **THEN** 紧邻水面之下的位置 MUST 大于 `0`
- **AND** 更深处的天空光 MUST NOT 高于更浅处，足够深处 MUST 到达 `0`


#### Scenario: 缺失邻区不产生亮缝
- **GIVEN** 网格采样需要一个尚未加载的相邻区块
- **WHEN** 客户端生成边界区段网格
- **THEN** 缺失邻区 MUST 同时按实心、无天空光和无方块光处理；邻区到达后旧结果 MUST 失效并重新网格化

#### Scenario: 重启不保存派生光照
- **GIVEN** 一个已保存的区块只包含权威方块、掉落物和熔炉数据
- **WHEN** 服务端和客户端从相同方块数据重建区块
- **THEN** 客户端 MUST 从相同方块镜像重新派生相同天空光和方块光，且区块 schema MUST NOT 因派生光照而变化
### Requirement: 遮挡变化只重做受影响的有界区段
客户端 SHALL 在权威方块变化、区块加载和遗忘后异步重算受影响的派生天空光；昼夜时间推进不得触发重网格。普通方块变化的天空光 dirty 集合 MUST 不超过 `27` 个区段；若变化改变该列最高非空气方块，dirty 集合 MUST 不超过 `216` 个区段。既有有界调度器 MUST 合并重复 dirty 项并限流；执行期间相关镜像 revision 或 presence 变化时，过期结果 MUST 被拒绝且当前区段重新排队。

#### Scenario: 普通变化保持 27 个区段上限
- **GIVEN** 一个不改变所在列最高非空气方块的权威方块变化
- **WHEN** 变化到达客户端镜像
- **THEN** 客户端标记的唯一 dirty 区段数 MUST 不超过 `27`

#### Scenario: 列顶变化保持 216 个区段上限
- **GIVEN** 一个改变所在列最高非空气方块的权威方块变化
- **WHEN** 变化到达客户端镜像并跨越世界高度中的多个区段
- **THEN** 客户端标记的唯一 dirty 区段数 MUST 不超过 `216`

#### Scenario: 封闭与重开洞口收敛到最新镜像
- **GIVEN** 已加载镜像中有一条被侧向天空光照亮的洞口通道
- **WHEN** 权威方块变化先封闭再重开该洞口，且任一旧网格任务在变化后才完成
- **THEN** 客户端 MUST 不应用旧结果，最终网格 MUST 分别反映封闭后的黑暗和重开后的传播光

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
