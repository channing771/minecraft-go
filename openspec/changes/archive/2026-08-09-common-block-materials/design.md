## Context

参见 `proposal.md` 的动机。当前稳定编号止于 `ChestID` / `ItemChest`；方块使用确定性的 16×16 程序化材质与固定 2D array atlas，terrain quad 以局部原点重置 UV。`mesh.Registry.Opaque` 同时承担自身可绘制、邻面遮挡及 AO/天空光遮挡，不能表达可见但不完全遮光的玻璃和树叶。

服务端继续权威持有世界、玩家背包、放置、采掘与掉落状态；客户端只从权威镜像派生网格、AO、天空光与呈现。Memory 与 TCP 必须共享协议、codec 和校验。渲染仍受固定 atlas、单一 terrain pass、8 字节 quad 实例和预热后零分配约束。

受影响范围为 `internal/core`、`internal/sim`、`internal/physics`、`internal/network`、`internal/world`、`internal/storage`、`internal/server`、`internal/assets`、`internal/mesh`、`internal/client`、`internal/render` 与 `cmd/mcgo`，以及对应测试、golden 和性能记录。不新增包、依赖或外部资源。

## Goals / Non-Goals

**Goals:**

- 按固定顺序追加 14 组稳定方块与物品编号，并锁定注册、堆叠、放置、掉落和采掘规则。
- 在不扩大服务端权威面和传输分叉的前提下完成协议 v14、玩家 schema v6、区块 schema v7 的语义升级。
- 从共享 terrain 路径消除草地跨 quad、区段和区块的 UV 相位断层。
- 在现有 atlas 与 pass 中支持有完整碰撞、但不完全遮挡 AO/天空光的玻璃和树叶 cutout。
- 只向存档缺失的玩家提供一次固定材料包，并用无窗口固定场景验收全部可观察行为。

**Non-Goals:**

- 不修改世界生成或配方，不实现沙子/砾石重力、树叶腐烂、雪融化或玻璃破碎损失。
- 不增加横向原木、薄雪层或其他方块状态与非完整方块几何。
- 不实现真半透明、透明排序、反射、法线图、PBR、动画材质或第二个 terrain pass。
- 不增加工具类型、渲染框架、通用 `BlockDefinition`、数据驱动材质系统或外部 bitmap 资产。

## Decisions

### 1. 固定追加编号并继续扩展现有注册 switch

`BlockID` 在 `ChestID` 后、`ItemID` 在 `ItemChest` 后，依次追加圆石、平滑石、沙子、砾石、竖向橡木原木、橡木木板、树叶、玻璃、砖块、白色羊毛、红色瓦块、黏土、完整雪块和苔藓圆石。`core` 增加 `RegisteredBlock` 作为方块编号合法性的基础判断，现有 `RegisteredItem`、`ItemPlacement`、`BlockDrop` 与 `ItemStackLimit` 继续用固定 switch 表达一一对应关系。

`network` 与 `storage` 在各自信任边界调用注册判断，未知编号立即返回带上下文的错误；`assets.Registry.Material` 不再承担未知编号降级。编号一经提交不得重排或复用。

否决通用 `BlockDefinition` 或跨包描述表：当前只有固定材料、没有方块状态、脚本行为或运行时扩展消费者，新增框架只会扩大依赖和迁移面。

### 2. 服务端统一执行材料规则与缺失玩家初始化

14 种物品统一为堆叠上限 64，放置写入对应方块，可采收时掉落自身。采掘按三组固定规则进入现有权威计时路径：沙子等软材料 5 tick 且任意手持物可采收；原木和木板 15 tick 且任意手持物可采收；五种石质材料复用 `StoneID` 的 30/15/8 tick 与工具采收规则。玻璃也掉落自身，不模拟破碎损失。

只有 `PlayerStore.LoadPlayer` 返回 `ErrPlayerNotFound` 时，`newMissingCachedPlayer` 才在固定 27 格背包前 14 格按注册顺序各写入 64 个材料，快捷栏保持为空。已有存档、identity migration 和未确认登录均不写入材料包；确认前断开不落盘，确认保存后按已有玩家恢复。

玩家与世界状态只由服务端 goroutine 修改；网络发送成功后的快照继续视为不可变。客户端不预测或补发材料包，也不持有独立的材料规则分支。

### 3. 只升级语义版本，保持 wire 与存档布局

协议常量从 v13 升为 v14，使旧客户端在 handshake 阶段明确拒绝；Memory 与 TCP 复用同一版本检查、codec 和消息合法性校验。玩家 schema 从 v5 升为 v6、区块 schema 从 v6 升为 v7，编码布局不变。

玩家 v5→v6 identity migration 保留背包、耐久、位置与生命值，不注入材料包。区块 v6→v7 identity migration 保留 palette、掉落物、熔炉、箱子与 revision。新材料继续由既有稳定 `uint16` palette 表达。旧数据沿用 `NeedsRewrite` 机制重写；future schema 和未知方块/物品明确拒绝。

该选择避免一次无实际布局变化的数据重编码。若回退代码，新 schema 对旧程序是 future schema，必须拒绝而不能猜测解码；回退前需恢复兼容程序或保留新版本读取能力。

### 4. 分离面可见性与完全遮挡

保留 `mesh.Registry.Opaque(id)`，只表示 AO 与天空光的完全遮挡。新增由当前方块与邻方块共同决定的 `FaceVisible` 规则：空气或未注册方块不产生面；相邻完全不透明方块遮住当前面；同类玻璃与同类树叶剔除内部面；不透明方块与 cutout 相邻时保留不透明方块面；玻璃与树叶相接时不生成重叠共面内部表面。

玻璃和树叶继续使用标准完整方块碰撞。该分离只改变网格、AO 与客户端派生天空光的可观察语义，不改变物理形状或服务端权威状态。

否决继续复用单一 `Opaque` 布尔值：它无法同时满足 cutout 自身可见、邻面可见、AO/天空光可穿过和内部面剔除。

### 5. 用世界坐标派生 UV，不扩展 quad 实例

terrain shader 使用区段 origin 与 quad 局部顶点已有信息，按当前面的两个世界空间轴直接得到周期 UV。相同材质即使被 AO、天空光、贪心网格上限、区段或区块边界拆分，也由同一世界坐标决定采样相位；负坐标依赖现有 repeat sampler。

不增加 quad 字段，8 字节实例格式保持不变。该改动统一影响所有 terrain 材质，因此必须重新抓取并逐张复核完整无窗口场景，只提交实际变化的 golden。

否决只修纹理边缘：它不能消除 quad 局部 UV 原点重置造成的相位断层。

### 6. 在同一 atlas 与 pass 中实现 alpha cutout

树叶和玻璃基础层只生成 alpha `0` 或 `255`；现有 terrain fragment shader 在低于固定阈值时 `discard`，其余像素按不透明颜色写入现有深度目标。cutout mip 在 atlas 初始化时使用保持覆盖率的降采样，其他不透明层继续颜色平均。材质与 mip 均在注册表构造时一次性生成，不进入每帧热路径。

此方案不提供颜色叠加或半透明层次，玻璃通过像素边框、高光和透明孔洞表达，树叶通过不规则孔洞表达。否决真半透明：混合、排序、深度策略和额外 pass 会扩大行为与性能契约，而本批验收不需要这些能力。

### 7. 冻结程序化材质结构与草地周期边界

全部新材质保持确定性的 16×16 RGBA 与自然像素写实风格。圆石、平滑石、沙子、砾石、木板、砖块、羊毛、瓦块、黏土和苔藓圆石按批准设计生成各自结构；竖向原木顶底面复用同组年轮、侧面使用纵向树皮；雪块顶面与冷蓝侧面分开；树叶约三成基础像素透明，玻璃具有透明中心、像素边框与少量对角高光。

草顶明暗簇跨边界包裹，草侧缘使用闭合周期序列，最右列到最左列的草缘高度变化不超过一个像素。生成结果用字节确定性、结构差异、分面、alpha 和周期边界测试锁定，不引入外部美术资产。

### 8. 无窗口验收与 record-only 性能证据

在现有 `captureScenes` 末尾追加 `materials-showcase`，使用固定正午、相机与确定性夹具，经正常镜像、mesher、renderer 和 upload 路径收敛后抓取。夹具覆盖 14 种材料、八格连续且跨 AO 或天空光拆分边界的草地、相邻玻璃、相邻树叶，以及原木顶面和侧面。抓帧不得创建或聚焦前台窗口，既有双阈值不放宽。

实现前后记录 `BenchmarkMeshTerrainSection`、`BenchmarkMeshChunk`、quad 数与上传量。性能数值只记录；实例格式、固定 atlas、固定容量、无每帧分配和单 pass 是结构契约，真实 overflow、数据丢失、I/O 或报告结构错误仍失败。

## Risks / Trade-offs

- [世界坐标 UV 改变既有地形纹理相位] → 完整无窗口抓取并逐张查看全部场景，只提交实际变化的 golden。
- [cutout 增加可见面与 overdraw] → 剔除同类和不同 cutout 内部共面，记录 mesh、quad 与 upload 指标，不增加 pass。
- [cutout mip 让远处孔洞消失或膨胀] → 使用固定覆盖率测试和 `materials-showcase` 远景样本锁定行为。
- [新增编号漏接映射或信任边界] → 用固定清单表格测试覆盖注册、放置、掉落、协议和存档。
- [材料包污染已有玩家] → 仅允许 `ErrPlayerNotFound` 分支创建，并用 migration、确认前断开和重载测试证明不重复。
- [语义版本回退后旧程序无法读取新存档] → 依赖明确 future-schema 拒绝，回退时先部署保留新 schema 读取能力的兼容版本。

## Migration Plan

1. 先冻结注册顺序、协议/存档版本和视觉验收规格，再记录未改生产代码时的 terrain mesh 基线。
2. 追加 ID、注册与采掘规则；随后升级协议 v14、玩家 v6 和区块 v7，并完成 identity migration 与未知编号拒绝测试。
3. 接入缺失玩家材料包，再分离面可见性与遮挡语义，最后实现世界 UV、单 pass cutout、材质与 mip。
4. 生成 `materials-showcase`，执行完整无窗口抓帧并逐张复核实际变化；记录实现后性能证据。
5. 运行受影响包 race、全仓 race、archcheck、vet、gofmt、diff 与 OpenSpec strict；同步三份主规格后归档。

回滚代码时同时回退协议与 schema 常量；已经写成玩家 v6 或区块 v7 的数据不得由不认识 future schema 的旧程序强行读取。稳定编号不得在回滚后的后续版本中重排或复用。
