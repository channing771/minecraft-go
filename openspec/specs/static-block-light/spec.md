# static-block-light Specification

## Purpose

为完整不透明方块提供可放置、可挖回且由客户端从权威方块镜像确定性派生的静态方块光，并以固定资源上限贯通存档、协议、网格和无窗口渲染验证。

## Requirements

### Requirement: 发光块与物品 ID 稳定且具有固定配方入口
系统 SHALL 保持稳定的 `LightBlockID = ChestID + 1` 与 `ItemLightBlock = ItemChest + 1`。发光块物品 MUST 以 `64` 为单格上限，MUST 可按普通完整方块规则放置，并 MUST 在使用正确镐采掘后掉落一个发光块物品；正常生存流程 MUST 允许玩家通过固定配方消耗 4 个玻璃并获得 4 个发光方块。

#### Scenario: 已有发光块物品可放置并挖回
- **GIVEN** 玩家持有一个有效发光块物品
- **WHEN** 玩家按普通整格放置规则成功放置并用正确镐完成采掘
- **THEN** 权威世界 MUST 先出现发光块，随后 MUST 恢复为空气并产生一个发光块物品掉落

#### Scenario: 正常生存流程可合成首批发光块
- **GIVEN** 世界和玩家状态中都没有发光块或发光块物品，且玩家持有 4 个玻璃
- **WHEN** 玩家成功请求对应的固定配方
- **THEN** 系统 MUST 消耗 4 个玻璃并向玩家完整物品状态加入 4 个发光方块

### Requirement: 客户端从权威方块镜像确定性派生静态方块光
客户端 SHALL 只从已接受的权威方块镜像派生静态方块光。`LightBlockID` MUST 发出等级 `15`，其余已知或未知方块 MUST 发出 `0`；光在六个轴向上 MUST 仅向 `AirID` 相邻格传播并每格衰减 `1`，任何其他方块即使未来被标记为透明也 MUST 阻断方块光，多个光源在同一格 MUST 取最大值。缺失邻区 MUST 按非空气且无发光处理，服务端 MUST NOT 计算、存储或传输光照数组。

#### Scenario: 单光源按距离衰减
- **GIVEN** 一个发光块周围有连续空气且没有其他光源
- **WHEN** 客户端从相同权威方块镜像派生方块光
- **THEN** 源格 MUST 为 `15`、相邻空气 MUST 为 `14`、距离 `14` 的空气 MUST 为 `1`，距离 `15` 的空气 MUST 为 `0`

#### Scenario: 非空气方块与缺失邻区阻断传播
- **GIVEN** 发光块与目标位置之间存在任一非 `AirID` 方块或尚未加载的邻区
- **WHEN** 客户端派生边界处的方块光
- **THEN** 光 MUST NOT 因该方块当前或未来的透明属性而穿过边界产生亮缝，邻区到达后 MUST 从最新镜像重新收敛

#### Scenario: 多光源结果确定且取最大值
- **GIVEN** 两个发光块可经不同距离照到同一空气格
- **WHEN** 客户端重复从同一权威方块镜像派生光照
- **THEN** 该格 MUST 取两条路径中的较高等级，且每次构建的 packed 光照 MUST 相同

### Requirement: 方块光更新在既有有界 mesher 路径收敛
客户端 SHALL 复用既有 dirty、worker、generation、revision 和 presence 边界更新方块光。普通权威方块变化的唯一 dirty 区段 MUST 不超过 `27` 个，并 MUST 完整覆盖所有实际受影响区段；改变列顶的变化 MUST 不超过 `216` 个；区块加载或遗忘 MUST 使相邻结果失效。稳定构建 MUST 复用固定容量工作内存，不得分配无界传播队列。

#### Scenario: 普通放置与移除保持 27 个区段上限
- **GIVEN** 一个不改变列顶的 `AirID` 与 `LightBlockID` 之间的权威变化
- **WHEN** 变化到达客户端镜像
- **THEN** 客户端标记的唯一 dirty 区段数 MUST 不超过 `27`、MUST 包含所有实际受影响区段，并最终显示变化后的方块光

#### Scenario: 列顶变化保持 216 个区段上限
- **GIVEN** 发光块的放置或移除改变所在列的最高非空气方块
- **WHEN** 变化到达客户端镜像
- **THEN** 客户端标记的唯一 dirty 区段数 MUST 不超过 `216`，且不得建立方块光专用无界集合

#### Scenario: 过期发光结果不得发布
- **GIVEN** 含发光块的网格任务已排队但权威镜像随后移除了光源
- **WHEN** 旧任务在较新 revision、generation 或 presence 状态之后完成
- **THEN** 客户端 MUST 拒绝旧结果并重新排队，最终发布的方块光 MUST 为最新镜像的结果

### Requirement: packed 光照在 shader 中按最大值合成
地形实例的 packed 光照 MUST 以高四位表示 `0..15` 天空光、低四位表示 `0..15` 方块光。shader MUST 计算 `sky_base = 0.08 + sky*(daylight-0.08)` 与 `base = max(sky_base, block)`，随后再乘既有面朝向与 AO；方块光 MUST 不受昼夜相位影响。

#### Scenario: 午夜由方块光主导
- **GIVEN** 一个地形面天空光为 `0` 且方块光为 `15`
- **WHEN** 客户端在午夜绘制该面
- **THEN** 该面的合光基础亮度 MUST 为 `1`，且不得退回到 `0.08` 的最低环境亮度

#### Scenario: 天空光与方块光竞争时取最大值
- **GIVEN** 一个地形面的天空光和方块光都非零
- **WHEN** 客户端在任意昼夜相位绘制该面
- **THEN** 基础亮度 MUST 取天空光曲线与归一化方块光的较大者，面朝向与 AO MUST 仍继续降低最终亮度

### Requirement: 发光块保持当前协议 v16 与既有存档布局
线上协议 MUST 为 v16，区块存档 MUST 保持 schema v8；M5A 协议升级 MUST 保持发光块既有 message ID、payload 与字段布局不变。玩家 schema MUST 保持 v6，世界 metadata MUST 保持 v2；线上与存档 MUST NOT 新增天空光或方块光数组、packet 或 wire 字段，方块光 MUST 继续只从权威方块镜像派生。

#### Scenario: 旧协议在 Play 前拒绝
- **GIVEN** 客户端声明协议 v15 或更早版本
- **WHEN** 它连接协议 v16 服务端
- **THEN** 服务端 MUST 在进入 Play 前稳定拒绝，且不得协商或降级解码

#### Scenario: 玩家、区块和 metadata 版本保持不变
- **GIVEN** 玩家通过固定配方获得发光块物品并在世界中放置发光块
- **WHEN** 系统完成正常保存和重启
- **THEN** 发光块 MUST 通过玩家 schema v6 与区块 schema v8 保真恢复，世界 metadata MUST 仍为 v2，光照 MUST 从方块镜像重新派生

#### Scenario: 合成请求不增加既有 wire 字段
- **GIVEN** 玩家通过 Memory 或 TCP 请求发光方块固定配方
- **WHEN** v16 服务端接收并处理请求
- **THEN** 请求 MUST 继续使用既有合成消息及 recipe ID 字段，且不得新增发光块专用 packet、payload 字段或光照数组
