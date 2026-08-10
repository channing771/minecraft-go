## ADDED Requirements

### Requirement: 本地目标方块提供深度正确的轮廓与中文名称

系统 SHALL 在普通游戏界面中，以当前相机位置和朝向、客户端只读方块镜像以及既有 `6` 格交互距离执行本地方块射线。仅当 Predictor ready、射线路径完整已加载、命中已注册的非空气方块且没有打开背包或容器时，系统 MUST 显示该目标的细轮廓和中文名称。任何路径未知或未加载、空气、未注册 ID、超距、未 ready、背包或容器打开、断开或 reset 时，系统 MUST 立即清空目标显示状态，不得显示占位名称或陈旧目标。

轮廓 MUST 以十二根细长立方体覆盖单位方块包围盒的十二条边，并固定向外扩张 `0.003` 个世界单位；每根边的长边 MUST 为 `1.006` 个世界单位，两个横截面轴 MUST 为 `0.018` 个世界单位，颜色 alpha MUST 为 `0.86`。它 MUST 位于世界实体之后、HUD 之前的 alpha pass，使用现有深度附件进行深度测试、不得写入深度，并启用 alpha 混合；被目标本体或其他地形遮挡的边 MUST 不得穿透显示。所有当前注册方块 MUST 有非空中文显示名，未知 ID 查询 MUST 失败。

目标名称 MUST 复用世界空间 name-tag 的可观察样式并锚定在目标方块上方。name-tag 固定容量 MUST 恰为七名远端玩家加一个目标名称；无目标时不得占用目标实例。轮廓固定容量 MUST 恰为十二个实例。初始化后，在稳定目标状态下更新目标、准备几何和上传 MUST 不产生堆分配。

#### Scenario: 有效命中显示轮廓与中文名称

- **GIVEN** Predictor 已 ready、普通游戏界面打开，且完整已加载的六格内射线命中一个已注册非空气方块
- **WHEN** 客户端准备当前帧
- **THEN** 系统 MUST 显示该方块的十二边轮廓和对应的非空中文名称
- **AND** 名称 MUST 锚定在该方块上方

#### Scenario: 不完整或无效射线不显示陈旧目标

- **GIVEN** 当前或上一帧存在已显示的目标
- **WHEN** 射线路径遇到未知或未加载区块，或结果为空气、未注册 ID 或超出六格
- **THEN** 系统 MUST 清空轮廓和名称
- **AND** 系统 MUST NOT 显示占位名称、陈旧轮廓或陈旧名称

#### Scenario: UI、未 ready 与连接状态隐藏目标

- **GIVEN** 当前已显示一个有效目标
- **WHEN** 打开背包或容器，或 Predictor 变为未 ready、连接断开或发生 reset
- **THEN** 系统 MUST 在该帧隐藏轮廓和名称
- **AND** 目标状态 MUST NOT 进入网络消息或持久化内容

#### Scenario: 全部注册方块具有中文名而未知 ID 失败

- **GIVEN** 当前方块注册表和一个未注册方块 ID
- **WHEN** 分别查询每个已注册 ID 与该未注册 ID 的中文显示名
- **THEN** 每个已注册 ID MUST 返回非空中文名称
- **AND** 未注册 ID MUST 返回失败且不得返回占位字符串

#### Scenario: 轮廓尊重地形深度且不写深度

- **GIVEN** 目标方块的部分边被自身或其他地形遮挡
- **WHEN** 绘制目标轮廓
- **THEN** 系统 MUST 只绘制可见边，并以十二个实例覆盖单位方块包围盒的十二条边
- **AND** 几何 bounds MUST 为 `position-0.003..position+1.003`，每根边的长边 MUST 为 `1.006`、两个横截面轴 MUST 为 `0.018`，颜色 alpha MUST 为 `0.86`
- **AND** 轮廓 pass MUST 使用 alpha 混合和深度测试，且 MUST NOT 写入深度附件

#### Scenario: 固定容量在稳定态不分配

- **GIVEN** 七名远端玩家、一个有效目标和已完成一次预热的渲染器
- **WHEN** 连续执行 current target 更新、轮廓准备、name-tag 准备和上传的完整稳定路径
- **THEN** name-tag 实例数 MUST 不超过八个，轮廓实例数 MUST 不超过十二个
- **AND** 完整稳定路径 MUST 不产生堆分配，dynamic upload 与 overflow 结构 MUST 保持固定有界
