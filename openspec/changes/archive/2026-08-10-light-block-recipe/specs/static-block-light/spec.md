## MODIFIED Requirements

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

### Requirement: 发光块保持协议 v15 与区块 schema v8
线上协议 MUST 保持 v15，区块存档 MUST 保持 schema v8；两者的 packet、payload 和字段布局 MUST 保持不变。玩家 schema MUST 保持 v6，世界 metadata MUST 保持 v2；线上与存档 MUST NOT 新增天空光或方块光数组、packet 或 wire 字段，方块光 MUST 继续只从权威方块镜像派生。

#### Scenario: 旧协议在 Play 前拒绝
- **GIVEN** 客户端声明协议 v14 或更早版本
- **WHEN** 它连接协议 v15 服务端
- **THEN** 服务端 MUST 在进入 Play 前稳定拒绝，且不得协商或降级解码

#### Scenario: 玩家、区块和 metadata 版本保持不变
- **GIVEN** 玩家通过固定配方获得发光块物品并在世界中放置发光块
- **WHEN** 系统完成正常保存和重启
- **THEN** 发光块 MUST 通过玩家 schema v6 与区块 schema v8 保真恢复，世界 metadata MUST 仍为 v2，光照 MUST 从方块镜像重新派生

#### Scenario: 合成请求不增加 wire 字段
- **GIVEN** 玩家通过 Memory 或 TCP 请求发光方块固定配方
- **WHEN** 服务端接收并处理请求
- **THEN** 请求 MUST 继续只使用协议 v15 既有合成消息及 recipe ID 字段，且不得新增 packet、payload 字段或光照数组

## RENAMED Requirements

- FROM: `### Requirement: 发光块与物品 ID 稳定且没有正常获取入口`
- TO: `### Requirement: 发光块与物品 ID 稳定且具有固定配方入口`
- FROM: `### Requirement: 发光块兼容协议 v14 与区块 schema v7`
- TO: `### Requirement: 发光块保持协议 v15 与区块 schema v8`
