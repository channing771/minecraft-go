## Context

动机见 `proposal.md`；可观察行为见两个 delta spec。本变更只增加客户端本地呈现：服务端仍是世界和交互的唯一权威，客户端仅从只读镜像派生当前帧目标。既有 `core.RaycastBlocks`、世界空间 name-tag、avatar 立方体资源与无窗口捕获链路已经提供所需能力，不建立通用 overlay、HUD 文本或第二套字体系统。

## Goals / Non-Goals

**Goals:**

- 用一个当前帧、可清空的本地目标状态驱动轮廓和中文名称。
- 固定轮廓几何、pass 顺序与容量，保持预热后稳定态零分配。
- 用正常无窗口渲染链路验证目标反馈及其遮挡。

**Non-Goals:**

- 不把目标状态放入 Predictor、网络消息、存档或服务端命令路径。
- 不改变服务器对放置、采掘、容器打开的权威射线裁决。
- 不增加采掘裂纹、通用文字系统、通用几何 overlay、协议或 schema 变更。

## Decisions

### 本地选择与名称

在应用服务端消息后、准备当前帧前，`cmd/mcgo` 使用当前相机、只读镜像与 `core.RaycastBlocks` 的固定 `6` 格距离计算目标。仅 Predictor ready、普通游戏界面、完整已加载且未 desynced 的路径以及已注册非空气方块成立时保留结果；任一条件失效立即清空。Task 2 的纯查询负责 ready、UI 与镜像路径边界，Task 4 的帧接线负责在断线或 reset 的当帧清空已呈现目标。`internal/core` 提供稳定的 `BlockID → 中文显示名` 查询，已注册 ID 必有非空名称，未知 ID 返回失败而非占位文案。

这样复用客户端镜像和既有射线逻辑，也不会把展示信息误用为权威交互结果。被否决的替代方案是服务端下发目标 packet：它重复了服务端已有裁决、增加协议状态，并不能替代交互时的服务端重验。

### 固定轮廓几何与 pass

`internal/render` 增加独立 `BlockOutlineRenderer`，复用已有 avatar 立方体顶点、shader、实例编码与相机 uniform。单位方块包围盒固定向外扩张 `0.003` 个世界单位，因此目标位置为 `position` 时整体 bounds 固定为 `position-0.003..position+1.003`；每根边的长边固定为 `1.006`，两个横截面轴固定为 `0.018`，颜色 alpha 固定为 `0.86`。一个目标恰好编码为十二个细长立方体实例，容量也恰为十二。该 renderer 拥有自己的固定 GPU 缓冲和 pipeline，不重构 avatar 或掉落物 renderer。

每帧顺序固定为：terrain → avatar → item drops → block outline → name tags → damage overlay → HUD → debug panel。轮廓共享本帧 `viewProj`、daylight 与 depth，开启 alpha 混合和深度测试、关闭深度写入。被否决的替代方案是把线框写进 terrain pass 或建立可扩展 overlay 框架：前者会污染 terrain 契约，后者当前没有第二个消费者。

### 名牌容量与数据边界

目标名称复用 name-tag atlas、世界空间布局和上传路径。固定 name-tag 容量从七个远端玩家扩大为八个实例：七名远端玩家加一个目标名称；目标不存在时不占实例。完整稳定态零分配测试使用一个有效目标和七名远端玩家，预热一次后把 current target 更新、outline prepare、NameTag prepare 与上传整条路径包进同一次 `AllocsPerRun` 断言，并继续锁定既有 dynamic upload 与 overflow 结构，不为测试增加新抽象。

`core` 只保存显示名查询；`cmd/mcgo` 拥有帧级目标状态；`render` 只接收不可变的当前帧输入。渲染 goroutine 不回写镜像或 Predictor，服务端路径不读取局部目标，维持现有依赖方向和并发边界。

### 捕获夹具与文件所有权

Task 2 只拥有 `internal/core/block_name.go`、其测试以及 `cmd/mcgo/target_block.go`、其测试。Task 3 只拥有 `internal/render/block_outline.go`、其测试和必要的 renderer 资源接线。Task 4 只拥有 `cmd/mcgo/app.go`、相关 app 测试、name-tag 容量与帧接线。Task 5 只拥有 `cmd/mcgo/capture.go`、其测试、新增 `target-block-feedback.png` 与实际受正常提示影响的 golden；`inventory-crafting.png` 严禁改动。Task 6 只更新本 change 的 `tasks.md` 并同步、归档本 change 的两个规格。

捕获场景在现有场景表末尾追加 `target-block-feedback`：固定正午、相机 `{0.5, 3.5, 2.5}`、Yaw/Pitch `0`，以 `{X: 0, Y: 3, Z: -3}` 的 `BrickID` 为唯一目标。夹具经正常 Mirror、Mesher、renderer、depth 和 name-tag 路径收敛，不得用抓帧专用开关隐藏反馈。

## Risks / Trade-offs

- [镜像区块未知导致误导性目标] → 路径必须完整已加载；任何未知或未 ready 状态立即清空。
- [轮廓与地形共面闪烁] → 固定 `0.003` 向外偏移，保持深度测试且不写深度。
- [世界空间名称抢占玩家名牌容量] → 容量精确扩为 7+1，目标缺失时不提交空实例。
- [共享场景背景变化掩盖目标问题] → 新增独立目标场景，运行并逐张复核全部场景，且冻结 `inventory-crafting` 的逐字节结果。

## Migration Plan

不涉及网络协议、存档、方块/物品 ID、玩家 schema、区块 schema 或世界 metadata，因而无需迁移。发布按 Task 2–5 的可独立提交顺序完成；如需回退，移除客户端目标呈现和新增 golden 即可，世界与服务端状态保持兼容。验证包括 core/cmd/render 单元测试、零分配检查、无窗口 `make visual-check`、`go test ./... -race`、`go vet ./...` 和 OpenSpec 严格校验。
