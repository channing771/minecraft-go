# voxel-visual-presentation Specification

## Purpose
为独立体素游戏定义一套无需外部版权素材、可由程序稳定生成且在不同窗口尺寸下清晰可辨的方块与 HUD 视觉语言。
## Requirements
### Requirement: 方块材质具有稳定且可辨识的像素图案

图形客户端产品路径 SHALL 在完整、确定性的 16×16 程序化材质注册表上，以经许可适配并内嵌的 Pixel Perfection 子集替换具有直接对应素材的 layer；没有内嵌映射的 layer MUST 保留程序化像素。程序化注册表 MUST 保持可独立构造，作为完整最终回退与测试基线。程序化 registry 与内嵌默认包合成的产品默认材质 MUST 让草方块、石砖、矿石、熔炉、铁块、箱子及新增的圆石、平滑石、沙子、砾石、原木、木板、树叶、玻璃、砖块、白色羊毛、红色瓦块、黏土、雪块和苔藓圆石具有不同于单纯随机噪声的稳定结构图案。仓库提供的程序化与内嵌材质 MUST NOT 使用 Mojang 或其他未经授权的二进制美术资源；内嵌第三方材质 MUST 携带其许可证、署名、来源与修改说明。本地用户 override 的像素内容、结构与许可证不属于仓库分发物契约。

terrain 材质采样相位 SHALL 由当前面的世界坐标轴决定；相同材质被 AO、天空光、贪心合并上限、区段或区块边界拆分时 MUST 保持连续，负世界坐标 MUST 继续周期采样。程序化草顶明暗簇 MUST 跨 16×16 边界包裹，程序化草侧缘 MUST 使用闭合周期序列，最右列与最左列的草缘高度差 MUST 不超过一个像素。

程序化 registry 与内嵌默认包中的树叶和玻璃 SHALL 使用 alpha 仅为 `0` 或 `255` 的基础层；无论像素来自产品默认还是用户 override，树叶和玻璃 MUST 继续走现有 atlas 与 terrain cutout 分类，透明像素 MUST 在 fragment 阶段按既有规则丢弃，其余像素 MUST 按该 pass 的既有规则写入深度目标。cutout mip MUST 使用保持覆盖率的降采样，其他不透明层 MUST 保持颜色平均语义。有效用户 override MAY 在任意 layer 使用 PNG 可表达的中间 alpha，loader MUST NOT 因此拒绝；该像素选择不得改变 render classification、几何、layer ID 或映射。

#### Scenario: 同一方块材质重复生成保持一致

- **GIVEN** 相同代码版本与方块注册表
- **WHEN** 重复创建两份程序化材质集合
- **THEN** 每个材质层的 RGBA 字节 MUST 完全一致

#### Scenario: 已映射 layer 使用内嵌默认像素

- **GIVEN** 一个逻辑 layer 在内嵌默认包中存在经许可且直接对应的素材
- **WHEN** 图形客户端构造产品默认材质集合
- **THEN** 该 layer MUST 使用内嵌默认像素而非程序化像素

#### Scenario: 未映射 layer 保持程序化结果

- **GIVEN** 一个逻辑 layer 在内嵌默认包中没有直接对应素材
- **WHEN** 图形客户端构造产品默认材质集合
- **THEN** 该 layer MUST 与独立程序化注册表中的对应 layer 逐字节一致

#### Scenario: 功能方块保留可辨识结构

- **GIVEN** 石砖、矿石、熔炉、铁块和箱子的程序化材质
- **WHEN** 检查其像素分布
- **THEN** 每种材质 MUST 包含与自身用途一致的边界、接缝、矿脉、面板或木板结构之一
- **AND** 不同功能方块 MUST NOT 退化为仅基色不同的同一随机噪声图案

#### Scenario: 草方块保留自然像素层次

- **GIVEN** 草方块的程序化顶面与侧面材质
- **WHEN** 检查其像素分布
- **THEN** 顶面 MUST 同时包含相邻的明暗草簇
- **AND** 侧面草缘 MUST 具有可辨识的深度变化与下垂像素
- **AND** 顶面成簇图案 MUST 跨边界继续，侧面最右列与最左列草缘高度差 MUST 不超过一个像素

#### Scenario: 十四种材料具有固定结构与分面

- **GIVEN** 固定注册顺序中的 14 种新材料
- **WHEN** 生成完整程序化材质集合
- **THEN** 每种材料 MUST 具有确定性的 16×16 RGBA 图案并保持设计规定的结构特征
- **AND** 原木顶底面 MUST 显示同一组年轮、侧面 MUST 显示纵向树皮，雪块顶面与侧面 MUST 使用各自固定图案

#### Scenario: 世界坐标保持纹理相位连续

- **GIVEN** 同一材质表面跨越 quad、区段或区块边界，且边界任一侧可能位于负世界坐标
- **WHEN** terrain shader 采样该表面
- **THEN** 两侧 UV 相位 MUST 由相同世界坐标周期确定，且 MUST NOT 因 quad 局部原点重置而产生接缝

#### Scenario: 产品默认 cutout alpha 与 mip 保持孔洞覆盖

- **GIVEN** 程序化 registry 或内嵌默认包中的树叶或玻璃基础层与后续 mip
- **WHEN** 检查 alpha 取值和各级透明覆盖
- **THEN** 基础层 alpha MUST 仅为 `0` 或 `255`，透明像素 MUST 被丢弃，覆盖保持 mip MUST 防止边框或叶簇在远处整体消失

#### Scenario: 用户中间 alpha 不改变材质语义

- **GIVEN** 一个有效用户包为树叶、玻璃或其他已知 layer 提供带中间 alpha 的 16×16 RGBA PNG
- **WHEN** 客户端应用该 override 并生成网格与 atlas
- **THEN** loader MUST 接受该像素输入
- **AND** 该 layer MUST 继续使用既有 render classification、几何、layer ID 与面映射

### Requirement: 材质替换不改变现有呈现契约

内嵌默认或用户 override 替换 layer 像素时，系统 MUST 保持现有世界坐标 UV、atlas layer 顺序与尺寸、mip 生成及 Rust atlas 上传形状不变。树叶、玻璃和作物 MUST 保持既有 alpha cutout 分类与几何；水 MUST 保持 water pass、斜水面几何与透明排序；植物 MUST 保持交叉斜面几何。远环 LOD MUST 继续按既有 material ID 采样同一 atlas。用户 override 的任意有效 RGBA 像素 MUST NOT 改变这些契约。

#### Scenario: 默认包不改变 atlas 与上传形状

- **GIVEN** 程序化注册表与应用内嵌默认包后的注册表
- **WHEN** 分别生成 atlas 与 mip 数据并上传
- **THEN** 两者的 layer 数、layer 顺序、atlas 尺寸与每级上传形状 MUST 相同
- **AND** 既有 material ID 到 layer 的关系 MUST 不变

#### Scenario: 透明与植物几何保持原契约

- **GIVEN** 内嵌默认包替换了树叶、玻璃或作物 layer，而水仍使用程序化回退
- **WHEN** 系统生成网格并绘制这些材质
- **THEN** 树叶、玻璃和作物 MUST 继续遵守既有 cutout 与几何契约
- **AND** 水的 pass、斜面几何和透明排序 MUST 保持不变

### Requirement: 物品栏及相邻容器使用统一层级

系统 SHALL 在同一 HUD 绘制管线中为快捷栏、背包、合成、熔炉和箱子呈现一致的深色面板、栏位表面、物品色块与间距。当前选中栏位、移动来源栏位和可执行合成 MUST 使用互相可区分的状态色，且 MUST NOT 改变对应命中区域或交互语义。

#### Scenario: 快捷栏状态层级清晰

- **GIVEN** 已确认的物品栏状态与当前选中栏位
- **WHEN** 绘制关闭状态的 HUD
- **THEN** 九格快捷栏 MUST 位于统一面板内
- **AND** 选中栏位 MUST 与普通栏位有高对比边界
- **AND** 物品 MUST 使用一致的内嵌色块样式显示

#### Scenario: 打开背包时相邻区域保持同一风格

- **GIVEN** 背包已打开且当前显示合成、熔炉或箱子区域
- **WHEN** 绘制 HUD
- **THEN** 背包与相邻区域 MUST 使用相同的像素尺度、栏位表面、面板边距和状态色语义
- **AND** 3×9 背包区与 1×9 快捷栏区 MUST 通过同一外框内的分组表面清晰区分

#### Scenario: 栏位数量数字保持材质上的可读性

- **GIVEN** 栏位分别包含一件物品与多件堆叠物品
- **WHEN** 绘制栏位数量
- **THEN** 单件物品 MUST NOT 显示冗余数字 `1`
- **AND** 多件物品的数字 MUST 右下对齐并使用深色像素阴影与高对比暖白前景
- **AND** 两位数量 MUST 使用紧凑且固定的字间距，同时保持最右数字的右下锚点不变

### Requirement: 可放置方块使用生效注册表材质缩略图

系统 SHALL 在快捷栏、背包、合成、熔炉和箱子栏位中为可放置方块显示当前生效方块注册表材质生成的缩略图；该注册表 MUST 与世界 terrain 采样使用同一套程序化、内嵌默认及可选用户覆盖后的 layer。非方块物品 MAY 继续使用程序化色块。仓库提供的缩略图像素 MUST 遵守上述授权边界；本地用户 override MUST 原样进入同一缩略图采样路径，不要求仓库验证其像素结构或许可证。

#### Scenario: 方块与非方块物品使用对应呈现

- **GIVEN** 同一栏位视图中存在草方块与工具
- **WHEN** 绘制物品内容
- **THEN** 草方块 MUST 使用当前生效 `GrassID` 注册表材质的像素缩略图
- **AND** 工具 MUST 继续使用可辨识的程序化色块

### Requirement: 生命值以屏幕左下角无背景的一排爱心显示

系统 SHALL 把服务端确认的 0..20 生命值显示为固定在 framebuffer 左下角的一排十颗像素爱心；爱心区域 MUST NOT 绘制面板、黑色底板或其他背景。每颗 MUST 表示两点生命，奇数生命值 MUST 以半颗表达。未确认生命值时 MUST NOT 绘制爱心。

#### Scenario: 满血、半段和零血均可判读

- **WHEN** 权威生命值分别为 20、9 和 0
- **THEN** 爱心栏 MUST 分别显示十颗满心、四颗满心加一颗半心、以及十颗空心

#### Scenario: 打开背包不移动或缩小生命栏

- **WHEN** 在同一 framebuffer 打开或关闭背包
- **THEN** 爱心栏 MUST 保持相同的左下角锚点与像素尺度
- **AND** 爱心下沿与左沿 MUST 保持安全边距

#### Scenario: 未确认生命值不显示

- **GIVEN** 客户端尚未收到权威生命值
- **WHEN** 绘制 HUD
- **THEN** 系统 MUST NOT 绘制预测值、陈旧值或占位爱心

### Requirement: HUD 在无窗口视觉场景尺寸下保持可读

系统 SHALL 保持 HUD 像素几何与命中几何一致，并 MUST 在 640×360 及更大的 framebuffer 中让九格快捷栏完整落在屏幕内；打开背包时，完整的八行固定合成区域 MUST 通过统一整体缩放落在 framebuffer 内且不得相互重叠。每个配方按钮的命中矩形 MUST 与其绘制矩形来自同一组缩放后几何。

#### Scenario: 640×360 关闭 HUD 完整可见

- **WHEN** 在 640×360 framebuffer 绘制快捷栏与爱心栏
- **THEN** 所有栏位、选中边界和左下角无背景爱心 MUST 位于 framebuffer 边界内

#### Scenario: 640×360 打开八行固定合成区域

- **WHEN** 在 640×360 framebuffer 打开背包与固定合成区域
- **THEN** 全部背包栏位、八条配方行与合成按钮 MUST 位于 framebuffer 边界内
- **AND** 每个按钮的命中测试 MUST 使用与缩放后绘制矩形相同的几何

### Requirement: 视觉优化保持固定有界渲染成本

系统 MUST 复用现有 HUD render pass、字体图集与固定容量上传缓冲；热路径在预热后 MUST 保持零分配，且不得新增外部依赖、UI 框架或每帧动态资源。terrain 材质 MUST 继续使用固定 2D array atlas 与单一现有 terrain pass，quad 实例格式 MUST 保持 `8` 字节，不得增加每帧材质资源创建。

系统 MAY 增加**恰好一个**额外的半透明绘制阶段用于水面，且该阶段 MUST 受下列边界约束：排序粒度 MUST NOT 细于区段，MUST NOT 引入每帧动态资源创建，MUST NOT 使 quad 实例格式超过 `8` 字节，MUST NOT 为其新增第二个字体图集或 HUD 上传缓冲。除该阶段外，系统仍 MUST NOT 增加其他透明 pass 或更细粒度的透明排序。

#### Scenario: 最坏 HUD 布局仍受固定容量约束

- **GIVEN** 满背包、满箱子、全部快捷栏耐久条与满生命值
- **WHEN** 准备一帧 HUD
- **THEN** quad 与 glyph 实例数 MUST 不超过编译期固定容量
- **AND** 预热后的布局准备 MUST 不产生堆分配

#### Scenario: 新 terrain 材质保持既有实例与 pass
- **GIVEN** 同一帧包含全部 14 种新材料、玻璃和树叶 cutout
- **WHEN** 准备并绘制 terrain
- **THEN** 系统 MUST 只使用现有 atlas 与 terrain pass，quad 实例 MUST 保持 8 字节
- **AND** 预热后 MUST 不因材质或 cutout 增加每帧堆分配或动态资源

#### Scenario: 水面阶段不突破实例格式与资源边界
- **GIVEN** 同一帧包含大面积水面与不透明地形
- **WHEN** 准备并绘制该帧
- **THEN** 水面 quad 实例 MUST 保持 8 字节
- **AND** 预热后 MUST 不产生每帧动态资源创建或堆分配
- **AND** 系统 MUST NOT 出现水面阶段之外的第二个额外透明 pass
### Requirement: 文本按字体原生度量渲染，窄字符不得丢失

系统绘制文本时，字形四边形的尺寸与其采样的图集区域 MUST 等尺寸，使字形以字体原生比例呈现。任一方向上的缩放比 MUST NOT 依赖字形自身的宽窄。

该要求覆盖全部文本呈现处：快捷栏与容器的数量数字、远端玩家名牌、调试面板的标签与数值。

#### Scenario: 窄字符与宽字符同样可辨

- **GIVEN** 一段同时包含最窄字形（如 `i`、`.`、`r`、`t`）与较宽字形（如 `w`、`S`）的文本
- **WHEN** 绘制该文本
- **THEN** 每个字符 MUST 可辨识
- **AND** MUST NOT 出现某些字符缺失而另一些正常的情况

#### Scenario: 采样区与四边形等尺寸

- **GIVEN** 任意已光栅化的字形
- **WHEN** 比较它的四边形尺寸与它采样的图集区域尺寸
- **THEN** 两者 MUST 在一个像素以内相等

### Requirement: 行距容纳字体的实际字形跨度

绘制多行文本时，行距 MUST 不小于该字体在所用字号下实际字形的上下跨度，使相邻行的升部与下伸部不重叠。

#### Scenario: 相邻行不重叠

- **GIVEN** 上一行含下伸部字符、下一行含升部字符
- **WHEN** 绘制这两行
- **THEN** 两行的字形 MUST NOT 相互重叠

### Requirement: 本地目标方块提供深度正确的轮廓与中文名称

系统 SHALL 在普通游戏界面中，以当前相机位置和朝向、客户端只读方块镜像以及既有 `6` 格交互距离执行本地方块射线。仅当 Predictor ready、射线路径完整已加载、命中已注册的非空气方块且没有打开背包或容器时，系统 MUST 显示该目标的细轮廓和中文名称。任何路径未知或未加载、空气、未注册 ID、超距、未 ready、背包或容器打开、断开或 reset 时，系统 MUST 立即清空目标显示状态，不得显示占位名称或陈旧目标。

轮廓 MUST 以十二根细长立方体覆盖单位方块包围盒的十二条边，并固定向外扩张 `0.003` 个世界单位；每根边的长边 MUST 为 `1.006` 个世界单位，两个横截面轴 MUST 为 `0.018` 个世界单位，颜色 alpha MUST 为 `0.86`。它 MUST 位于世界实体之后、HUD 之前的 alpha pass，使用现有深度附件执行 `CompareLessEqual` 深度测试、不得写入深度，并启用 alpha 混合；被目标本体或其他地形遮挡的边 MUST 不得穿透显示。所有当前注册方块 MUST 有非空中文显示名，未知 ID 查询 MUST 失败。

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
- **AND** 轮廓 pass MUST 使用 alpha 混合和 `CompareLessEqual` 深度测试，且 MUST NOT 写入深度附件

#### Scenario: 固定容量在稳定态不分配

- **GIVEN** 七名远端玩家、一个有效目标和已完成一次预热的渲染器
- **WHEN** 连续执行 current target 更新、轮廓准备、name-tag 准备和上传的完整稳定路径
- **THEN** name-tag 实例数 MUST 不超过八个，轮廓实例数 MUST 不超过十二个
- **AND** 完整稳定路径 MUST 不产生堆分配，dynamic upload 与 overflow 结构 MUST 保持固定有界
