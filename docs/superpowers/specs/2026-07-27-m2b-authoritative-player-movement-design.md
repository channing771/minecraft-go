# minecraft-go M2B 权威玩家移动设计

- 日期：2026-07-27
- 状态：待书面评审
- 上位设计：`docs/superpowers/specs/2026-07-26-minecraft-go-design.md`
- 前置阶段：`docs/superpowers/specs/2026-07-27-m2a-authoritative-interaction-design.md`
- 范围：权威玩家姿态、固定步移动、重力与碰撞、自动跨步、客户端预测与服务端和解、权威交互射线

---

## 1. 目标与边界

M2B 把 M2A 的自由飞行相机替换为第一个服务端权威玩家控制闭环：

1. 服务端拥有玩家位置、速度、视角与接地状态的最终决定权。
2. 客户端只发送移动和交互意图，不能发送位置、速度、射线起点或订阅中心。
3. 客户端用与服务端共享的固定步物理内核立即预测移动。
4. 服务端状态到达后，客户端回到权威状态并重放未确认输入。
5. 玩家具备 Minecraft-like 的行走、重力、跳跃、方块碰撞与 `0.6` 格自动跨步。
6. 挖掘与放置从权威玩家眼睛位置产生射线。
7. M2A 的世界一致性、32 区块视距和性能指标不得回退。

### 1.1 本阶段明确包含

- 每个 session 的权威玩家状态
- 固定 20 Hz 的共享移动内核
- `0.6 × 1.8` 玩家 AABB 与 `1.62` 格眼睛高度
- 地面加速与摩擦、较弱空中控制、重力、终端速度和跳跃
- 方块 AABB 碰撞、墙角滑动、撞头、接地与 `0.6` 格自动跨步
- 服务端安全出生、跌出世界恢复和卡入方块恢复
- 由权威玩家位置驱动的区块订阅
- 客户端输入历史、预测、确认、回滚重放和显示平滑
- 从权威眼睛位置产生的瞬时挖掘与无限方块放置
- 人工延迟下的端到端收敛测试
- 完全离屏的 M2B 性能与一致性门禁

### 1.2 本阶段明确不包含

- 疾跑、蹲伏、爬梯、游泳、飞行或旁观模式
- 半砖、台阶等新方块内容；只用测试碰撞体锁定自动跨步能力
- 摔落伤害、生命、死亡、床或生存模式重生
- 工具、挖掘时间、耐久、掉落物、快捷栏数量或方块消耗
- 实体碰撞、推挤、载具或其他玩家的实体同步
- 水流、方块更新、昼夜、光照传播或怪物 AI
- TCP、登录、二进制编解码、压缩或磁盘存档
- 客户端作弊检测、输入速率惩罚或联网反作弊策略

M2B 只完成单个内置服务端 session 的移动垂直切片。TCP、多玩家广播和恶意客户端连接策略属于 M3。

---

## 2. 方案选择

采用**客户端与服务端共享纯函数物理内核**：

- `internal/physics` 对只读碰撞查询执行单步运动，不持有世界、网络、时钟或 goroutine。
- `sim` 在权威 tick 中调用它并持有最终玩家状态。
- `client` 在固定步预测器中调用同一函数并持有未确认输入历史。
- 两端实现相同不是靠复制代码，而是直接调用同一个包。

不采用两套相似物理实现，因为常量、舍入或碰撞顺序会逐渐分叉。不采用服务端独占物理加画面外推，因为起步、停步和跳跃在有延迟时仍会迟钝，无法形成完整的输入重放闭环。

---

## 3. 包边界与所有权

### 3.1 新包 `internal/physics`

`physics` 只允许依赖 `core`，提供以下概念：

```go
type State struct {
    Position mgl32.Vec3 // 脚底中心
    Velocity mgl32.Vec3
    OnGround bool
}

type Input struct {
    MoveX int8    // -1 左，0 中立，+1 右
    MoveZ int8    // -1 后退，0 中立，+1 前进
    Jump  bool
    Yaw   float32
}

type CollisionBoxSet struct {
    Loaded bool
    Count  uint8
    Boxes  [8]core.AABB // 方块局部坐标中的碰撞体
}

type CollisionSource interface {
    CollisionBoxes(core.BlockPos) CollisionBoxSet
}

type StepResult struct {
    State      State
    UsedStep   bool
    HitUnknown bool
}

func Step(state State, input Input, source CollisionSource) StepResult
```

`CollisionBoxSet.Loaded=false` 表示该方块所属区块未知。移动内核把未知方块当成整格封闭碰撞体，并在结果中设置 `HitUnknown`，因此客户端和服务端都不会预测或权威移动进未 Ready 区块。

M2B 生产世界的适配器只返回两种形态：空气没有碰撞体；其他现有方块返回一个 `[0,1]³` 整格碰撞体。`Boxes` 的固定容量让热路径无需按方块分配切片，也足以表达未来的台阶组合；超过八个碰撞体的方块不在本阶段承诺范围内。

### 3.2 服务端所有权

`sim.Engine` 的 world goroutine 继续是唯一写者。每个 `sessionState` 增加：

- 玩家生命周期：`PendingSpawn` 或 `Active`
- `physics.State`
- `DimensionID`、yaw 与 pitch
- 当前持续控制状态
- 最近消费的客户端全局 sequence
- 最近消费的 `PlayerInput` sequence
- 安全重置是否需要在下一份 Ready 状态中通知客户端

生成 worker、Transport reader 和 writer 都不能直接修改玩家状态。它们仍然只提交不可变结果或命令。

### 3.3 客户端所有权

客户端主线程独占：

- 当前预测物理状态与上一个预测状态
- 当前渲染视角
- 最多 256 条未确认移动输入
- 最近接受的 `ServerTick`
- 仅用于显示的纠正偏移及其剩余衰减时间

客户端 mesh worker 和 renderer 不感知玩家状态。`Mirror` 继续是碰撞查询的数据源；预测器只能读取它，不能用移动结果修改镜像。

### 3.4 依赖方向

新增依赖后仍保持单向：

```text
core ← physics
core ← world ← sim
physics ← sim
core ← network
client → core, physics, network, world, mesh, render
server → core, network, physics, world, worldgen, sim
```

`physics` 不得 import `world`、`sim`、`client`、`network` 或 `render`。`sim` 不得 import `client` 或 `render`。依赖白名单继续由 `internal/archcheck` 强制。

---

## 4. 玩家状态与物理常量

位置统一表示玩家**脚底中心**。所有碰撞、网络与预测状态都使用同一语义；只有相机构造时才加眼睛高度。

| 参数 | 固定值 |
|---|---:|
| 固定步长 | `50 ms` |
| 玩家宽度 | `0.6` 格 |
| 玩家高度 | `1.8` 格 |
| 眼睛高度 | `1.62` 格 |
| 自动跨步高度 | `0.6` 格 |
| 行走最高速度 | `4.3` 格/秒 |
| 有输入时地面加速度 | `40` 格/秒² |
| 无输入时地面减速度 | `50` 格/秒² |
| 空中加速度 | `8` 格/秒² |
| 跳跃初速度 | `8.4` 格/秒 |
| 重力 | `32` 格/秒² |
| 最大下落速度 | `78.4` 格/秒 |
| 碰撞 epsilon | `1e-5` 格 |
| 接地探针 | `1e-4` 格 |

M2B 不提供运行时修改这些值的游戏设置，防止客户端和服务端配置漂移。测试可通过碰撞夹具改变地形，但不能改变生产常量。

### 4.1 水平速度

`MoveX` 和 `MoveZ` 只能取 `-1`、`0` 或 `+1`。先按 yaw 把输入投影到水平面的右向量与前向量，再把非零合向量归一化，斜向行走不能超过 `4.3` 格/秒。

- 接地且有输入：水平速度向目标速度以 `40` 格/秒² 靠近。
- 接地且无输入：水平速度向零以 `50` 格/秒² 靠近。
- 空中：水平速度向目标方向以 `8` 格/秒² 靠近，结果长度上限为 `4.3` 格/秒。

“靠近”按向量差方向限制本 tick 最大速度变化量，不分别钳制 X/Z；否则斜向加速度会更大。

### 4.2 垂直速度

- `OnGround && Jump` 时把 Y 速度设为 `8.4` 并离地。
- 其他情况每步减去 `32 × 0.05`，最低钳制到 `-78.4`。
- 向下碰撞把 Y 速度清零并设为接地。
- 向上碰撞把 Y 速度清零。
- 持续按住跳跃时，玩家每次重新接地都可再次跳起；M2B 不单独实现跳跃按键冷却。

### 4.3 数值约束

物理内核只接受有限的 position、velocity 和 yaw。非有限状态是调用方不变式破坏；服务端在调用前进入安全重置，客户端在调用前关闭非法会话，因此生产路径不把 NaN/Inf 传入 `physics.Step`。

状态使用 `float32`，协议同样传 IEEE-754 `float32`。M2B 不引入定点数；客户端和服务端共享代码且和解允许小误差。确定性测试在同一 GOOS/GOARCH 上断言逐字段一致，跨平台网络一致性由 M3 的编解码与传输测试覆盖。

---

## 5. 碰撞与自动跨步

### 5.1 普通移动

每个固定步按以下顺序执行：

1. 校验输入并计算目标水平速度。
2. 应用地面加速/减速或空中控制。
3. 应用跳跃或重力。
4. 以 `velocity × 0.05` 得到期望位移。
5. 枚举起点 AABB 与目标 AABB 联合范围内的方块碰撞体。
6. 固定按 `Y → X → Z` 顺序裁剪位移。
7. 对被裁剪的轴清零速度，并根据向下接触更新 `OnGround`。
8. 用 `1e-4` 格向下探针确认静止玩家仍有支撑；走出悬崖后必须在当前 tick 失去接地。

轴顺序不得基于 map 遍历或平台条件改变。候选方块按 Y、X、Z 递增顺序查询；相同输入与相同碰撞数据必须得到相同结果。

### 5.2 自动跨步

同时满足以下条件时尝试跨步备选路径：

- 普通路径的 X 或 Z 位移被裁剪；
- 玩家在本步开始时接地，或普通路径产生了向下接触；
- 请求的水平位移非零；
- 玩家没有撞到未知区块边界。

备选路径固定为：

1. 向上裁剪最多 `0.6` 格。
2. 在升高后分别裁剪 X 和 Z。
3. 向下裁剪到支撑面，但最多下降“实际上升量 + 本步原始向下位移”。
4. 确认最终 AABB 无重叠且有向下支撑。

只有备选路径的水平位移平方严格大于普通路径时才采用。相等时保留普通路径，避免边角处来回切换。半高测试碰撞体可跨越；一整格障碍因上升上限为 `0.6` 而不能跨越。

### 5.3 与未知区块的关系

碰撞 broad phase 中任一方块 `Loaded=false` 时，该格按完整实心方块处理。结果可以沿已知边界滑动，但不能进入未知格，也不能通过自动跨步越过未知边界。

服务端订阅半径仍比客户端渲染半径多一圈，正常移动不会频繁撞到加载边缘。高速 benchmark 使用独立的可信观察者，不改变正常玩家速度上限。

### 5.4 放置碰撞

服务端放置目标方块前，用该方块的实际碰撞体与权威玩家完整 AABB 做相交测试：

- 任一碰撞体与玩家重叠则拒绝 `occupied`。
- 无碰撞体方块未来可以放在玩家所在格；M2B 现有可放置方块都是整格碰撞体。
- 不再使用“目标方块是否包含射线 origin”的 M2A 近似判断。

---

## 6. 出生与安全恢复

### 6.1 PendingSpawn

新 session 从 `PendingSpawn` 开始。默认可信出生锚点为主世界区块 `(0,0)`；这是服务端配置，不来自客户端消息。

`server.New` 在构造本地 session 时显式向 `sim.Engine` 注册玩家，不等待第一条客户端消息。首个服务端 tick 因此能以出生锚点建立订阅并请求区块生成。

PendingSpawn 期间：

- 区块订阅以出生锚点为中心。
- 服务端仍发布世界快照。
- 服务端每 tick 发布 `Ready=false` 的玩家状态。
- 客户端不运行预测，不发送需要 Ready 玩家才能执行的交互。

### 6.2 安全点搜索

服务端在出生锚点周围半径 16 格的方块列中搜索。候选列按以下全序排列：

1. 到锚点中心的水平距离平方；
2. 世界 X；
3. 世界 Z。

对每列从 `MaxY-1` 向 `MinY` 扫描，取最高整格实心支撑面，并验证其上方完整 `0.6 × 1.8` 玩家 AABB 无碰撞。候选所需区块未 Ready 时停止在该候选等待，不能跳到后续已完成候选，否则 worker 完成时机会影响出生结果。

首个合法候选的位置是 `(x+0.5, supportY+1, z+0.5)`。找到后进入 `Active`，清零速度与持续输入，设置 `OnGround=true`，并在第一份 Ready 状态中发送 `Reset=true`。

全部候选均不合法时保持 PendingSpawn，按 5 秒一次的速率记录警告；相关区块 revision 改变后从第一个候选重新扫描。M2B 的确定性高度图地形必须能在默认锚点找到合法位置，端到端测试锁定这一条件。

### 6.3 跌出与卡入恢复

以下情况退出 Active 并重新进入同一出生锚点的 PendingSpawn：

- 脚底 Y 小于 `MinY - 16`；
- position 或 velocity 含 NaN/Inf；
- 玩家 AABB 在 tick 开始时与实心碰撞体相交，并且无法向上解除。

卡入时先按 `1/16` 格增量尝试最多上移 1 格，采用第一个完整无碰撞位置；成功则继续 Active，不发送 Reset。若碰撞查询含未知区块或 16 个位置均失败，进入 PendingSpawn。

PendingSpawn 对外发布的位置使用出生锚点中心与 `MaxY+1` 的有限占位值；`Ready=false` 时客户端不得把该值用于碰撞或相机平滑。真正安全位置就绪后才以 `Reset=true` 激活。

---

## 7. 协议

### 7.1 客户端到服务端

```go
type PlayerInput struct {
    Sequence uint64
    MoveX    int8
    MoveZ    int8
    Jump     bool
    Yaw      float32
    Pitch    float32
}

type BreakBlock struct {
    Sequence uint64
    Yaw      float32
    Pitch    float32
}

type PlaceBlock struct {
    Sequence uint64
    Yaw      float32
    Pitch    float32
    Block    core.BlockID
}
```

`PlayerInput` 每个客户端固定步发送一条。`BreakBlock` 和 `PlaceBlock` 是鼠标按下沿产生的离散动作，携带点击瞬间的视角，但不携带位置、速度、接地状态、射线或订阅中心。

所有消息共享现有 session 全局 sequence：从 1 开始严格递增；重复或过期消息幂等忽略；可靠有序传输下允许跳号用于诊断。

旧 `BreakRay` 与 `PlaceRay` 从封闭消息集合移除。正常客户端不再发送 `SetViewCenter`；权威玩家位置是正常 session 唯一的订阅来源。

### 7.2 服务端到客户端

```go
type PlayerState struct {
    ServerTick        uint64
    LastInputSequence uint64
    Dimension         core.DimensionID
    Position          mgl32.Vec3
    Velocity          mgl32.Vec3
    Yaw               float32
    Pitch             float32
    OnGround          bool
    Ready             bool
    Reset             bool
}
```

每个服务端 tick 为活动 session 发布一条 `PlayerState`：

- `LastInputSequence` 是已消费的最大 `PlayerInput.Sequence`，不是最大交互 sequence。
- 同 tick 多条移动输入被合并时，它等于被消费的最新一条，较早移动输入也视为已确认。
- 没有新移动输入时保持上一确认值。
- `Reset=true` 只在 PendingSpawn 转入 Active 的第一条 Ready 状态出现一次。
- `Reset=true` 必须同时满足 `Ready=true`。
- 同一 session 的 `ServerTick` 严格递增。

新增稳定拒绝原因：

```text
invalid_input
player_not_ready
```

M2A 已有的 `no_target`、`chunk_not_ready`、`protected_block`、`invalid_block` 与 `occupied` 继续保留。

### 7.3 输入校验

- `MoveX`、`MoveZ` 必须在 `[-1,1]`。
- yaw 与 pitch 必须有限。
- pitch 必须在 `[-π/2+0.01, π/2-0.01]`；yaw 归一化到 `[-π,π)`。
- 非法 `PlayerInput` 返回 `invalid_input`，该 sequence 被消费并成为确认值，本 tick 使用中立移动且清除持续 Jump。
- 非法交互视角返回 `invalid_input`，不改变玩家 look 或世界。
- PendingSpawn 的交互返回 `player_not_ready`。

服务端永远不接收客户端位置或速度，因此不需要“允许误差范围内相信客户端位置”。

---

## 8. 权威 tick 与订阅

每个 tick 的顺序扩展为：

1. 截取客户端命令与生成 worker 结果。
2. 命令按 `(sessionID, sequence)` 稳定排序，完成重复过滤与输入校验。
3. 同 session 的多条 `PlayerInput` 只保留 sequence 最大者作为本 tick 控制；离散交互不得合并或静默丢弃。
4. 按 `(dimension, chunkX, chunkZ)` 应用生成结果。
5. PendingSpawn session 尝试确定性安全点；Active session 先做卡入/非有限状态检查，再调用一次 `physics.Step`。
6. 从 PendingSpawn 锚点或 Active 权威位置计算订阅中心，完成订阅进入、离开与生成调度。
7. 按命令 sequence 执行本 tick 的挖掘与放置意图。
8. 对修改区段 Compact，聚合方块增量并递增 revision。
9. 在既有预算内发布快照、增量、卸载、拒绝与 `PlayerState`。
10. `Tick++`。

物理每个服务端 tick 最多执行一次，客户端发送速度或积压消息数不能让玩家在单 tick 内执行多步。没有新 `PlayerInput` 时沿用上一份持续控制状态；可靠输入流短暂抖动不会立即停步。

订阅中心取权威脚底位置所在区块。玩家被未知区块边界阻挡后，当前位置仍会让下一圈区块进入半径 33 的服务端订阅，生成完成后即可继续前进。

---

## 9. 权威交互

### 9.1 视角

有效的 `PlayerInput` 更新持续移动和 look。`BreakBlock` / `PlaceBlock` 的有效 yaw/pitch 更新点击瞬间 look；这样交互不必等待下一个 50 ms 输入采样。

玩家水平移动方向使用该 tick 最终有效 yaw。pitch 不参与移动，只参与眼睛射线。

### 9.2 射线生成

服务端从权威状态构造：

```text
origin = Position + (0, 1.62, 0)
direction = forward(Yaw, Pitch)
maxDistance = 6.0
```

随后复用 `core.RaycastBlocks`。客户端只做准星预览；服务端不读取客户端预览结果。

### 9.3 动作顺序

物理在交互之前推进，因此本 tick 的动作从移动后的权威眼睛位置发出。同 tick 多个合法动作按全局 sequence 执行；前一个动作产生的世界修改对后一个动作可见。

被拒绝动作不修改区块、revision、覆盖层或玩家物理状态。放置额外执行 §5.4 的完整玩家 AABB 检查。

---

## 10. 客户端预测与和解

### 10.1 固定步预测

渲染循环维护 duration accumulator。每积累 `50 ms`：

1. 从当前按键与视角生成一条 `PlayerInput`。
2. 分配全局 sequence 并发送。
3. 把 `(sequence, physics.Input)` 追加到历史。
4. 用当前 Mirror 碰撞查询执行一次 `physics.Step`。

单帧最多补跑 5 步。超过部分被丢弃并重新以当前时间为基准，防止暂停或断点后无限追赶。相机 yaw/pitch 仍按渲染帧更新；移动物理只在固定步采样。

PendingSpawn 时不执行以上四步。客户端可以继续旋转视角，但第一条 Ready 状态到达前不发送移动或交互。

### 10.2 历史

每条历史记录只包含重放所需的 sequence 与 `physics.Input`。容量固定为 256：

- 未满时按 sequence 递增追加。
- 收到确认后移除 `sequence <= LastInputSequence` 的记录。
- 达到容量时进入 `predictionSuspended`：停止预测，发送一条不进入历史的中立 `PlayerInput`，记录其 `suspendSequence`，并保留最后画面。
- 中立输入发送失败时，每 50 ms 用新的 sequence 重试，但不恢复预测。
- 只有收到 `LastInputSequence >= suspendSequence` 的权威状态后才清空旧历史、以该状态为新起点并恢复预测。

不得覆盖最旧历史，也不得让服务端无限沿用最后一个非中立输入。256 步等于 12.8 秒，正常内存 Transport 不应触达；命中上限是连接或服务端严重失步信号，不是常规环形覆盖场景。

### 10.3 和解

收到 `PlayerState` 时先验证：

- `ServerTick` 大于最近接受值；旧状态幂等忽略。
- dimension 为已知维度。
- position、velocity、yaw、pitch 全部有限且 pitch 在范围内。
- `LastInputSequence` 不大于客户端已发出的最大移动 input sequence。
- `Reset=true` 时 `Ready` 必须为 true。

合法 Ready 状态按以下顺序应用：

1. 保存和解前的预测位置与当前显示位置。
2. 把模拟状态替换为服务端 position、velocity、OnGround。
3. 删除已确认历史。
4. 按 sequence 递增重放剩余历史，每条恰好一步。
5. 比较重放后位置与和解前预测位置，选择显示策略。

`Ready=false` 时清空预测 accumulator，保留历史为空，并等待 Ready。`Reset=true` 或维度变化时同时清空历史、前后插值状态和显示纠正偏移。

普通 Ready 状态不覆盖本地实时 yaw/pitch；它们只用于校验服务端接受的 look。`Reset=true` 或维度变化时，本地视角改为服务端 yaw/pitch，避免安全重置后首个交互使用旧方向。

### 10.4 显示平滑

模拟状态永远立即和解；只有相机和未来本地玩家模型使用显示偏移：

- 误差 `< 1/128` 格：不创建偏移。
- `1/128 ≤ 误差 < 0.5` 格：令 `displayOffset = oldDisplayedPosition - newPredictedPosition`，在精确 `100 ms` 内线性衰减为零。
- 误差 `≥ 0.5` 格、dimension 变化或 `Reset=true`：显示偏移立即清零并跳到权威重放结果。

新纠正在衰减期间到达时，从当时实际显示位置重新计算偏移，不叠加旧偏移。速度、碰撞、订阅和交互永远不读取显示偏移。

### 10.5 渲染插值

相机脚底位置在上一个与当前预测固定步状态之间按 accumulator 的 `0..1` 比例线性插值，再加显示偏移和 `1.62` 眼睛高度。视角直接使用当前渲染帧 yaw/pitch，不做 20 Hz 插值。

交互消息使用当前渲染视角；准星预览使用当前预测眼睛位置，但最终命中仍以服务端权威眼睛位置为准。

---

## 11. 错误与关闭

### 11.1 非法输入

非法输入属于不可信消息，不得 panic。M2B 返回稳定拒绝并使用安全的中立控制。M3 接入 TCP 时可在连接层增加违规计数与断开策略，不改变 `sim` 的校验结果。

### 11.2 非法服务端状态

服务端消息在内存模式下仍按不可信协议数据验证。客户端收到非法 `PlayerState`：

1. 不修改当前预测器或相机。
2. 记录带 server tick 的错误。
3. 关闭当前 Transport 并取消内置服务端。

### 11.3 关闭顺序

沿用 M2A 顺序并在停止输入后增加预测器关闭：

1. 停止窗口产生输入和动作。
2. 停止客户端固定步预测器。
3. 关闭客户端 Transport。
4. server 停止接收命令并取消 tick/生成 worker。
5. 取消 client mesh worker，排空结果。
6. 释放 renderer、surface、device 与 window。

预测器没有独立 goroutine；关闭只清空 accumulator、历史和显示偏移。

---

## 12. 测试策略

### 12.1 `physics`

表驱动测试覆盖：

- 静止落地、自由下落、终端速度、跳跃与撞头
- 六个轴向碰撞、墙角滑动与固定 Y/X/Z 顺序
- 对角输入归一化与 yaw 旋转
- 负坐标和区块边界
- 接地离开悬崖后同 tick 失去接地
- 半格碰撞体跨步成功
- 一整格障碍跨步失败
- 无头部空间时不跨步
- 普通与跨步水平距离相等时保留普通路径
- 未加载格阻挡普通移动与跨步

模糊/属性测试覆盖：

- 任意有限合法初态执行后仍为有限值。
- 结果玩家 AABB 不与任何已加载碰撞体相交。
- 水平速度不超过规则上限，向下速度不超过终端速度。
- 同一碰撞夹具与输入脚本重复运行得到逐字段相同结果。

新增 `BenchmarkStepPlayerFlat`、`BenchmarkStepPlayerColliding` 与 `BenchmarkStepPlayerStepping`；三个热路径目标均为 `0 allocs/op`。

### 12.2 `sim`

- Active 玩家每个 `Engine.Step` 恰好执行一次物理。
- 同 tick 多条 `PlayerInput` 只采用最新状态并确认到最新 sequence。
- 非法最新输入确认 sequence、返回 `invalid_input` 且使用中立控制。
- 没有新输入时沿用上一持续控制。
- 订阅中心只随权威位置或 PendingSpawn 锚点变化。
- 客户端无法通过消息直接改变位置、速度或订阅中心。
- 跌出世界、非有限状态和无法解除的卡入触发确定性 PendingSpawn。
- 可向上解除的卡入采用最小 `1/16` 格位移，不重置。
- 同种子与同输入脚本的世界、overlay、revision 和玩家状态 hash 一致。

### 12.3 `network`

- 新消息保持内存 Transport FIFO 与值所有权语义。
- `PlayerState` 的旧 server tick 在客户端幂等忽略。
- 非有限或越界状态被原子拒绝，预测器不留下半应用状态。
- 关闭继续唤醒玩家输入和状态上的阻塞 Send/Recv。

### 12.4 `client`

- 无延迟时预测与权威状态一致且历史及时清空。
- 固定延迟下回到权威状态并按 sequence 重放所有未确认输入。
- 同 tick 被服务端合并的输入通过最大确认 sequence 一并删除。
- history 达到 256 后暂停预测、发送中立恢复输入，确认 `suspendSequence` 后恢复，且不覆盖旧记录。
- Mirror 未加载边界阻止预测进入。
- 小误差在 100 ms 内衰减，大误差与 Reset 立即跳转。
- 新纠正到达时以实际显示位置重建偏移。
- Ready=false 时不运行物理、不发送移动或交互。

### 12.5 交互与端到端

无头端到端脚本使用真实 MemoryTransport、server、Mirror 与 predictor：

1. 等待确定性出生点 Ready。
2. 向前行走、停止并验证摩擦减速。
3. 跳过低障碍、撞上一格墙并验证不穿透。
4. 从权威眼睛位置挖掘，再尝试向玩家身体内放置并得到 `occupied`。
5. 人工延迟所有服务端状态 `150 ms`，继续行走和转向。
6. 停止输入、排空消息，断言客户端历史为空。
7. 比较客户端与服务端位置、速度、接地状态、世界 hash 与 revision。

最终状态的 float 字段使用 `1e-5` 容差；世界 hash 与 revision 必须精确相等。测试同时断言取消后所有 goroutine 在 1 秒内退出。

---

## 13. 性能门禁

### 13.1 离屏场景 v3

性能场景提升为 `scenario_version = 3`，继续使用不创建 GLFW 窗口的 headless WebGPU device 和 2560×1440 离屏纹理，运行时不得把任何窗口带到前台。

场景保留：

- 10 秒预热
- 60 秒静止
- 120 秒以 48 格/秒沿固定路径高速流式移动
- 32 区块渲染半径与 33 区块 halo
- 固定种子、相机路径、消息/网格预算和性能采样字段

高速路径由 benchmark harness 的**可信观察者**直接向 `server` 提交内部订阅中心命令。该命令仍由 world goroutine 应用，但不是 `network.ClientMessage`，不会出现在正常客户端或未来 TCP 协议中。正常内置服务端始终使用权威玩家位置驱动订阅。

v3 不再在高速相机路径中执行玩家交互，而是抽查可信观察者最后中心区块的 server/Mirror hash；权威玩家移动与交互由 §12.5 的无头端到端脚本门禁。这样既保留 M2A 的高速区块流式压力，也不为正常玩家开放飞行或任意订阅能力。

### 13.2 绝对门禁

- 静止与高速流式阶段均 `≥100 fps`
- frame p99 `<12 ms`
- 峰值 RSS `<2 GiB`
- server tick p99 `<10 ms`
- 不得出现单次 server tick `≥50 ms`
- 最后抽查区块的 server/Mirror hash 与 revision 一致
- 物理微基准 `0 allocs/op`

同一开发机建立 v3 基线后，frame p50/p95/p99、tick p50/p95/p99、RSS、加载时间或快照时间任一退化超过 20% 判红。GitHub 托管 CI 只运行平台无关测试、race、vet、格式和微基准可执行性，不比较跨机器绝对帧时间。

---

## 14. M2B 出口条件

以下条件必须同时满足：

- 正常客户端不再用自由飞行相机直接修改玩家位置。
- WASD、地面加减速、空中控制、重力、跳跃、碰撞与 `0.6` 格跨步符合 §4–§5。
- 服务端是位置、速度、接地、交互射线起点与订阅中心的唯一权威。
- 客户端协议不包含玩家位置、速度、射线起点或正常 session 的订阅中心。
- 客户端预测在人工 `150 ms` 状态延迟下立即响应，停止输入并排空后收敛到权威状态。
- PendingSpawn、跌出世界和卡入方块恢复不会产生 NaN、穿墙或非确定性出生结果。
- 玩家不能把整格可放置方块放进自身权威 AABB。
- 确定性重放同时覆盖世界与玩家状态。
- `go test ./... -race -count=1` 通过。
- `go vet ./...` 与 `gofmt -l .` 通过。
- 依赖方向与 WebGPU 隔离门禁通过。
- 2560×1440 离屏 v3 场景满足 §13 的全部绝对门禁。

M2B 完成后，M2 的“交互与内置服务端”里程碑闭合。下一子项目按总设计进入 M3：区域存档、格式迁移、TCP 传输、登录状态机与多玩家同步。
