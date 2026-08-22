# authoritative-health Specification

## Purpose
为玩家提供由服务端唯一权威、跨重连与重启保真的生命值，使摔落成为真实代价，并让死亡把背包内容完整掉在世界里而不静默丢失任何物品。
## Requirements
### Requirement: 生命值是权威且有界的玩家状态
系统 SHALL 为每名玩家维护一个 `0..20` 的生命值，满值为 20。生命值 MUST 由服务端单写者拥有，随玩家存档持久化，并随权威玩家状态下发；客户端 MUST NOT 预测生命值。系统从网络或存档边界读取生命值时 MUST 拒绝越界值且不部分应用。

#### Scenario: 新玩家以满血开始
- **GIVEN** 一名从未保存过状态的玩家登录
- **WHEN** 服务端建立其权威状态
- **THEN** 生命值 MUST 为 20

#### Scenario: 生命值跨重连保真
- **GIVEN** 某玩家在生命值为 7 时断开连接
- **WHEN** 该玩家重新登录
- **THEN** 其生命值 MUST 仍为 7

#### Scenario: 生命值跨重启保真
- **GIVEN** 某玩家在生命值为 7 时正常关服
- **WHEN** 服务端重新启动且该玩家重新登录
- **THEN** 其生命值 MUST 仍为 7

#### Scenario: 越界生命值被整体拒绝
- **GIVEN** 一份声明生命值大于 20 的玩家状态
- **WHEN** 系统从网络或存档边界读取该状态
- **THEN** 系统 MUST 拒绝整份状态且不得部分应用其他合法字段

### Requirement: 摔落按固定曲线扣血且只在落地结算一次
系统 SHALL 追踪玩家离地后到达过的最高高度，并在"上一 tick 不在地面、这一 tick 在地面"的边沿用 `下落高度 = 峰值高度 − 落地高度` 结算一次伤害。伤害 MUST 为 `floor(下落高度) − 3`，结果为负时取 0。落地、传送、重生与维度 reset MUST 把峰值高度重置为当前高度。

#### Scenario: 三格及以内不扣血
- **GIVEN** 满血玩家从距地面 3 格处落下
- **WHEN** 玩家落地
- **THEN** 生命值 MUST 保持 20

#### Scenario: 四格扣一点
- **GIVEN** 满血玩家从距地面 4 格处落下
- **WHEN** 玩家落地
- **THEN** 生命值 MUST 变为 19

#### Scenario: 二十三格从满血致死
- **GIVEN** 满血玩家从距地面 23 格处落下
- **WHEN** 玩家落地
- **THEN** 伤害 MUST 为 20 且玩家死亡

#### Scenario: 正常跳跃不扣血
- **GIVEN** 站在平地上的满血玩家
- **WHEN** 玩家原地起跳并落回地面
- **THEN** 生命值 MUST 保持 20

#### Scenario: 落地只结算一次
- **GIVEN** 玩家刚因摔落扣血并停留在地面
- **WHEN** 系统继续推进若干 tick 且玩家未再离地
- **THEN** 生命值 MUST NOT 继续下降

### Requirement: 未受伤时生命值自动回复
系统 SHALL 在最后一次受伤后连续 100 tick 未再受伤、**且玩家饥饿值不低于 18** 时开始回复生命值，每 40 tick 回复 1 点，直到满值;每回复 1 点 MUST 累积固定的回血疲劳量(见 `authoritative-hunger`)。饥饿值低于 18 时 MUST NOT 回复。任何伤害 MUST 把计时清零并中断回复。满血时 MUST NOT 计时或回复。

#### Scenario: 延迟期内不回复
- **GIVEN** 某玩家刚受到伤害且生命值低于满值
- **WHEN** 系统推进 99 tick 且玩家未再受伤
- **THEN** 生命值 MUST 保持不变

#### Scenario: 延迟满足后按固定速率回复
- **GIVEN** 某玩家生命值为 10 且已连续 100 tick 未受伤
- **WHEN** 系统再推进 40 tick
- **THEN** 生命值 MUST 变为 11

#### Scenario: 受伤打断回复
- **GIVEN** 某玩家已连续 100 tick 未受伤并正在回复
- **WHEN** 玩家再次受到伤害
- **THEN** 回复 MUST 立即停止，且必须重新连续 100 tick 未受伤才能再次开始

#### Scenario: 满血不回复
- **GIVEN** 某玩家生命值为 20
- **WHEN** 系统推进任意 tick
- **THEN** 生命值 MUST 保持 20 且不产生额外状态发布

#### Scenario: 饥饿值不足时不回复
- **GIVEN** 某玩家生命值为 10、饥饿值为 17 且已连续 100 tick 未受伤
- **WHEN** 系统再推进 40 tick
- **THEN** 生命值 MUST 保持 10

#### Scenario: 饥饿伤害经同一入口且止于 1
- **GIVEN** 某玩家饥饿值为 0、生命值为 2
- **WHEN** 系统推进两个饥饿伤害间隔
- **THEN** 生命值 MUST 为 1 且 MUST NOT 继续下降,每次扣血 MUST 重置回血计时

### Requirement: 死亡在同一 tick 内结算并把背包掉在世界里
生命值降到 0 时，系统 SHALL 在同一个权威 tick 内完成：把 36 个物品栏格逐格掉到世界里、把玩家传回出生锚点、生命值回满、速度归零并重置摔落峰值。逐格放置 MUST 在放置成功后才清空该格，因此任何时刻每件物品要么在背包里、要么在地上。系统 MUST NOT 在死亡结算中静默销毁物品。

#### Scenario: 死亡清空背包并掉在世界里
- **GIVEN** 某玩家背包中有若干物品且生命值降到 0
- **WHEN** 系统完成该 tick
- **THEN** 玩家的 36 个格 MUST 全部为空，且这些物品 MUST 作为掉落物存在于世界中

#### Scenario: 死亡后回到出生锚点并满血
- **GIVEN** 某玩家在远离出生点处死亡
- **WHEN** 系统完成该 tick
- **THEN** 玩家位置 MUST 为其出生锚点，生命值 MUST 为 20，速度 MUST 为零

#### Scenario: 生命值为零的状态不对外发布
- **GIVEN** 某玩家因摔落伤害导致生命值降到 0
- **WHEN** 系统发布该 tick 的权威玩家状态
- **THEN** 发布的生命值 MUST 为重生后的 20，外部 MUST NOT 观察到生命值为 0 的中间状态

#### Scenario: 带耐久的工具无损掉落
- **GIVEN** 某玩家背包中有一把已磨损的镐且死亡
- **WHEN** 系统完成死亡结算
- **THEN** 该镐 MUST 作为掉落物存在且耐久值保持不变

### Requirement: 死亡掉落按环形外扩且只写已加载区块
死亡掉落 SHALL 从死亡所在区块开始按半径 0、1、2… 逐圈外扩寻找掉落槽，同圈内按稳定顺序遍历，结果必须可复现。系统 MUST NOT 写入未加载或未 Ready 的区块。搜索在扫完全部已加载区块后终止；此时仍未放下的格 MUST 保留在背包中跟随重生，MUST NOT 阻止死亡，也 MUST NOT 销毁物品。

#### Scenario: 优先放在死亡区块
- **GIVEN** 死亡所在区块有充足空闲掉落槽
- **WHEN** 系统结算死亡掉落
- **THEN** 全部物品 MUST 落在死亡所在区块，且同类物品按既有堆叠上限合并

#### Scenario: 死亡区块放不下时溢出到邻近区块
- **GIVEN** 死亡所在区块的掉落槽已满而邻近已加载区块有空位
- **WHEN** 系统结算死亡掉落
- **THEN** 放不下的物品 MUST 落在邻近已加载区块中，且玩家背包对应格 MUST 被清空

#### Scenario: 不写未加载区块
- **GIVEN** 死亡点附近存在未加载的区块
- **WHEN** 系统结算死亡掉落
- **THEN** 系统 MUST NOT 写入这些区块，也 MUST NOT 为此触发同步加载

#### Scenario: 放置顺序可复现
- **GIVEN** 两次从相同世界状态、相同死亡位置与相同背包内容结算死亡
- **WHEN** 系统完成两次结算
- **THEN** 两次的掉落物分布 MUST 完全相同

### Requirement: 图形客户端显示权威生命值

图形客户端 SHALL 显示服务端确认的生命值，客户端 MUST NOT 预测伤害或回复。客户端已有上一份确认生命值基线且新确认值更低时，系统 SHALL 立即显示红色屏幕边缘反馈；反馈 MUST 持续 `180ms` 并按剩余时间线性淡出，连续确认下降 MUST 重新开始完整持续时间。首次确认值、生命值不变或增加、Predictor not-ready MUST NOT 开始新反馈；not-ready 与会话清理 MUST 清除旧反馈。反馈 MUST 覆盖世界画面但位于生命值、背包、容器 HUD 与调试面板下方。

#### Scenario: 显示服务端确认值

- **GIVEN** 客户端收到生命值为 12 的权威玩家状态
- **WHEN** 客户端绘制 HUD
- **THEN** HUD MUST 显示 12
- **AND** 在收到该状态之前 MUST NOT 显示预测值

#### Scenario: 首次确认只建立基线

- **GIVEN** Predictor 尚未 ready
- **WHEN** 客户端首次收到生命值为 12 的 ready 权威状态
- **THEN** HUD MUST 显示 12
- **AND** 受伤反馈 MUST NOT 出现

#### Scenario: 确认生命值下降立即触发并线性淡出

- **GIVEN** 客户端上一份确认生命值为 12
- **WHEN** 新确认生命值变为 7
- **THEN** 当前帧屏幕边缘反馈透明度 MUST 为峰值
- **AND** 经过 90ms 且没有新伤害时，屏幕边缘反馈透明度 MUST 为峰值的一半
- **AND** 经过完整 180ms 后反馈 MUST 消失

#### Scenario: 连续确认伤害重置计时

- **GIVEN** 一次确认伤害的反馈已淡出 90ms
- **WHEN** 确认生命值再次下降
- **THEN** 当前帧屏幕边缘反馈透明度 MUST 恢复峰值
- **AND** MUST 再保持一个完整 180ms 的淡出周期

#### Scenario: 回复与重生不误触发

- **GIVEN** 客户端已有一份确认生命值基线
- **WHEN** 新确认生命值不变或增加
- **THEN** 系统 MUST NOT 开始新的受伤反馈

#### Scenario: not-ready 清除旧反馈

- **GIVEN** 受伤反馈仍在显示
- **WHEN** Predictor 变为 not-ready 或客户端会话被清理
- **THEN** 反馈 MUST 立即消失
- **AND** 下一次 ready 的首份生命值 MUST 只建立新基线

#### Scenario: 反馈不染色 HUD

- **GIVEN** 受伤反馈、生命 HUD、容器 HUD 与调试面板同时可见
- **WHEN** 客户端绘制一帧
- **THEN** 世界画面边缘 MUST 显示红色反馈
- **AND** 生命 HUD、容器 HUD 与调试面板 MUST 保持原色且清晰可读

#### Scenario: 自动验证不打开窗口

- **GIVEN** CI 或开发者运行生命值、反馈或渲染测试
- **WHEN** 自动验证执行
- **THEN** 测试 MUST 可在不启动或聚焦交互式游戏窗口的情况下完成
