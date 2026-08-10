# M4N 静态方块光设计

## 1. 背景

当前 M4M 已从客户端接受的权威方块镜像派生 `0..15` 传播天空光，并把它写入 `mesh.Quad.Light` 的高四位。低四位仍固定为 `0`；夜间和封闭洞穴只有最低环境亮度，世界中也没有可放置的静态光源。

M4N 补齐最小方块光闭环：新增一个完整、不透明、固定亮度的发光块及对应物品；客户端继续只从权威方块镜像派生光照，不给服务端、协议或存档增加光照数组。

## 2. 目标与非目标

### 目标

- 追加稳定的发光块与物品 ID，不重排既有编号。
- 发光块固定发出等级 `15` 的方块光；空气中沿六个轴向每格衰减 `1`。
- 发光块物品可堆叠、放置和挖回，但本批没有任何正常游戏内获取入口。
- 方块光跨已加载区段与区块传播，并在放置、移除、加载和遗忘后有界收敛。
- `Quad.Light` 高四位继续表示天空光，低四位表示方块光；两者在 shader 中按最大值合光。
- Memory 与 TCP 继续共享相同权威放置、采掘和镜像路径。

### 非目标

- 不添加配方、初始发放、世界生成或管理命令。
- 不实现真实火把模型、非整格碰撞、附着面、支撑方块或支撑消失掉落。
- 不实现透明或半透明透光、彩色光、可调光源等级。
- 不让燃烧熔炉或其他方块实体动态发光。
- 不在服务端计算、存储或传输方块光数组。

## 3. 数据所有权与稳定资源

服务端仍只权威拥有方块和玩家物品状态。方块光是客户端对已接受权威方块镜像的确定性派生值，与 M4M 天空光采用相同所有权边界。

`internal/core` 在既有枚举末尾追加：

- `LightBlockID = ChestID + 1`；
- `ItemLightBlock = ItemChest + 1`。

发光块是完整不透明立方体，使用独立程序化材质。`ItemLightBlock` 的单格上限为 `64`，`ItemPlacement` 把它映射到 `LightBlockID`，`BlockDrop` 做反向映射。它不出现在 `Recipe` 的任何分支，也不增加 `RecipeID`。

放置沿用普通整格方块的权威射线、目标为空、区块 Ready、玩家 AABB 不重叠、sequence 和物品原子扣减规则。采掘规则复用石砖：空手或普通物品需 `30` tick 且不掉落，石镐需 `15` tick、铁镐需 `8` tick，两种镐均可挖回发光块。

由于没有配方、初始发放和世界生成，正常生存流程不会产生第一个发光块物品。自动测试可通过既有测试装配注入物品；一旦物品存在，放置和回收形成守恒闭环。

## 4. 双通道派生光照

### 4.1 注册表边界

`mesh.Registry` 增加 `Emission(world.BlockID) uint8`。生产注册表只对 `LightBlockID` 返回 `15`，其余方块和未知 ID 返回 `0`。返回值必须落在 `0..15`；生产表使用固定常量，不引入运行时配置。

现有 `SkyLightScratch` 更名为 `LightScratch`，仍由每个 mesher worker 独占并跨任务复用。它继续包含：

- 一个 `48 × 48 × 48` 的 `uint8` levels 数组；
- 一个同体积的 `uint32` FIFO 队列；
- 固定的 head/tail 索引。

levels 的高四位保存天空光，低四位保存方块光。该表示复用现有字节，不增加每个 worker 的 scratch 容量。

### 4.2 构建顺序

一次区段网格化按固定顺序构建两条通道：

1. 清零 levels，按 M4M 规则寻找直射天空光起点并传播高四位。
2. 清空队列索引，不清除已经得到的高四位。
3. 扫描同一 `48³` 邻域，把所有发光块的低四位置为 `15`，并在开始传播前统一入队。
4. 从全部等级 `15` 的光源执行多源 FIFO，只向非不透明邻格传播低四位 `current - 1`。

发光块自身可以作为不透明源入队，但传播不得进入其他不透明方块。所有源在传播前同时入队，因此首次到达空气格的路径就是最短路径，每个空气格最多入队一次，既有单体积队列上限仍成立。

距离以六向 Manhattan 距离计算：源格为 `15`，相邻空气为 `14`，距离 `14` 的空气为 `1`，距离 `15` 为 `0`。多个光源在同一格取最大值。缺失邻区继续由 `Neighborhood` 表现为不透明且无发光，不能产生边界亮缝。

`MeshSection` 仍采样可见方块面相邻的空气格，但直接把 packed byte 写入 `Quad.Light`，不再只写天空光左移后的值。

## 5. 失效与并发边界

权威方块变化进入客户端镜像后，继续复用 M4M 的 dirty 集合、worker 队列、generation 和 presence 校验：

- 不改变列顶的普通变化最多标记周围 `27` 个区段；等级 `15` 的方块光影响半径不超过 `14` 格，该范围足以覆盖全部受影响区段。
- 改变列顶的放置或移除继续使用最多 `216` 个区段的天空光路径；不另建方块光专用集合。
- 区块加载或遗忘继续让相邻区段失效；邻区到达后边界光照重新收敛。
- worker 完成时镜像 revision、generation 或 presence 已变化，旧网格必须拒绝并重新排队。

方块光不增加服务端 tick 工作、goroutine、网络消息或磁盘任务。跨 goroutine 发送后的 neighborhood 与 mesher 结果继续视为不可变。

## 6. 渲染合光

`terrain.wgsl` 从 `Quad.Light` 解出：

```text
sky   = high_nibble / 15
block = low_nibble / 15
sky_base = 0.08 + sky * (daylight - 0.08)
base = max(sky_base, block)
```

`base` 之后继续乘既有面朝向系数和 AO。这样方块光不受昼夜影响，天空光仍按权威世界时间变化，完全黑暗处仍保留既有 `0.08` 最低可见度。发光块自身可见面采样相邻空气的等级 `14`，因此在面朝向和 AO 之前取得 `14/15` 的方块光亮度。

本批不新增 pipeline、bind group、uniform、纹理文件或 draw call。发光块材质由现有程序化 atlas 生成，快捷栏继续复用注册方块顶面生成物品图标。

## 7. 协议与存档兼容

### 7.1 线上协议 v14

协议从 `v13` 升为 `v14`。packet ID、payload 长度和字段布局全部不变，唯一变化是稳定 `BlockID` / `ItemID` 的合法语义集合扩展。v13 及更早客户端在进入 Play 前稳定拒绝，避免旧客户端把发光块按默认石头材质呈现，或在收到发光块物品时中途解码失败。

线上不发送天空光或方块光数组。Chunk snapshot、BlockChanges、InventoryState 和 ItemDrop 继续只携带原有方块或物品字段。

### 7.2 区块 schema v7

区块 schema 从 `v6` 升为 `v7`，payload 布局不变；迁移 registry 增加 `6: no-op`。这与历史 v2→v3 的语义门禁相同：新程序读取 v6 后按原字节语义迁移并在后续保存写成 v7，只支持 v6 的旧程序看到 v7 envelope 时返回 future version，而不是把发光块当成普通石头继续运行。

新增 v7 golden，既有 v6 golden 保持不变并作为迁移输入。区块 fuzz 语料同时保留两版。

### 7.3 其余格式

玩家 schema 保持 v5：物品堆布局不变，新程序只扩展已注册物品集合；旧程序遇到含 `ItemLightBlock` 的 v5 玩家记录会按既有校验整体拒绝且不得覆盖。世界 metadata 保持 v2。

升级前必须正常关服并备份完整世界目录。回退到协议 v13 / 区块 schema v6 的程序前必须停服并恢复备份；不提供降级写回。

## 8. 错误处理

- 发光块物品的数量、耐久和栏位状态继续由 `ItemStack.Valid` 校验；非法值整体拒绝，不部分应用。
- 未知 `ItemID` 继续在网络和玩家存档边界拒绝；未知方块的 `Emission` 固定为 `0`。
- 协议版本不匹配在握手阶段拒绝，不进入 Play。
- 缺失邻区按不透明且黑暗处理；这是一条确定性边界，不等待同步 I/O。
- 光照 FIFO 超过固定体积表示内部不变式被破坏，保持 panic；测试必须证明生产算法恰好受容量约束。
- 过期 mesher 结果沿用既有丢弃和重新排队路径，不上传错误网格。

## 9. 测试与视觉验收

### 9.1 资源、协议与存档

- 锁定两个新增稳定 ID、堆叠上限、放置/掉落映射、无配方和独立材质层。
- 覆盖普通放置拒绝、物品原子扣减、三种采掘工具结果和掉落容量不足。
- 锁定协议 v14 握手、v13 拒绝、既有 packet ID 与 payload 布局不漂移。
- 覆盖区块 v6→v7 no-op 迁移、v7 roundtrip、v6/v7 golden、future version、CRC、截断和 fuzz；玩家 schema v5 roundtrip 发光块物品。

### 9.2 光照、并发与渲染

- 覆盖 `15/14/1/0` 距离边界、不透明阻断、多源取最大、跨区段、跨区块、缺失邻区和重复构建确定性。
- 证明 scratch 容量精确、稳定构建不分配、最坏多源输入不溢出队列。
- 覆盖放置与移除的 `27/216` dirty 上限、邻区到达、快速先放后拆和旧任务完成后的拒绝。
- shader headless 测试锁定白天、午夜、天空光与方块光竞争，以及 AO/面朝向仍参与最终明暗。
- 新增无窗口 `block-light-room` golden：封闭房间中央只有一个发光块，图像必须可辨认地表现由近到远衰减，房间外不得出现亮缝。提交前人工打开该图确认语义，不启动交互式客户端。

### 9.3 纵向闭环

Memory 与 TCP 使用同一脚本：向玩家测试物品栏注入一个发光块 → 放置 → 等待镜像和网格收敛 → 验证洞内非零方块光 → 用石镐挖回 → 验证掉落物与最终熄灭。两种 transport 的最终区块、物品、掉落物和 packed 光照必须一致。

## 10. 性能与 scenario v15

方块光复用现有 levels 与 FIFO，不增加每 worker scratch 容量；稳定任务不新增堆分配。代价是一遍固定 `48³` 光源扫描，以及仅在存在光源时执行的有界低四位 BFS。

该路径改变 still/flying 场景中的生产 mesher 工作量，因此 benchmark scenario 从 `v14` 升为 `v15`，不能把结果与 v14 做相对比较。active change 必须为硬件基线规格增加唯一显式 `14:15` 迁移：

- 生成完整有效的 M5 Memory v15 报告并按现行规则提升为 M5 当前基线；
- TCP v15 独立记录，只有显式请求时才与同次 Memory 做跨 transport 比较；
- 性能数值继续只记录，报告结构、身份、真实 overflow、数据丢失和 I/O 错误仍失败；
- M2 scenario v6 基线的内容和路径保持不变；既有 M5 v14 报告保留为历史证据。

自动性能验证保持无窗口，不启动或聚焦游戏客户端。

## 11. 受影响组件

- `internal/core/block.go`、`item.go` 及测试：稳定 ID、物品映射和采掘资源契约。
- `internal/assets/blocks.go`、程序化材质及测试：独立材质和 `Emission`。
- `internal/mesh`、`internal/client/mesher.go`、`mirror.go` 及测试：双通道 scratch、传播、dirty 与过期结果拒绝。
- `internal/render/shader/terrain.wgsl` 与 headless 测试：方块光解码和合光。
- `internal/network`、`internal/storage` 及 golden/fuzz：协议 v14、区块 schema v7、玩家 schema v5 保真。
- `internal/sim`、`internal/server`：放置、采掘、Memory/TCP 纵向闭环。
- `cmd/mcgo`、`cmd/perfcheck`、视觉 fixture 与性能文档：scenario v15、抓帧和 M5 记录。
- `README.md`、`docs/notes/lan-server.md`、`AGENTS.md`、`CLAUDE.md`、`openspec/config.yaml`：已实现能力、限制与版本说明。

## 12. 被否决的替代方案

### 独立 `BlockLightScratch`

职责表面上更独立，但会复制 `48³` levels、队列和邻域遍历，并增加每个 worker 的固定内存。高低四位本来就是同一个实例字段，复用 packed scratch 更直接。

### 服务端权威光照数组

静态方块光完全由权威方块确定。服务端再保存和发送数组会引入第二份可失真的状态、协议与 schema 负载、tick 调度和更多故障路径，没有提供新的可观察能力。

### 直接实现真实火把

当前网格、碰撞和不透明判断都以完整立方体为边界。真实火把还需要非整格几何、附着方向、支撑规则和支撑消失处理，会把光照闭环与另一套方块形状系统绑在一起。M4N 先只证明方块光链路。

## 13. 交付顺序

1. 创建并严格校验 `m4n-static-block-light` OpenSpec change。
2. 追加稳定资源、协议 v14、区块 schema v7 与兼容测试。
3. 实现 packed 双通道传播及固定容量测试。
4. 接入客户端 dirty、shader 与 headless 测试。
5. 完成 Memory/TCP 纵向闭环和 `block-light-room` golden。
6. 升级 scenario v15，生成并记录 M5 Memory/TCP 报告，保持 M2 基线不变。
7. 更新中文文档，运行受影响包 race、archcheck、全仓 race、vet、gofmt、fuzz/golden、视觉与 OpenSpec 严格门禁。
