# voxel-visual-presentation Specification

## Purpose
为独立体素游戏定义一套无需外部版权素材、可由程序稳定生成且在不同窗口尺寸下清晰可辨的方块与 HUD 视觉语言。
## Requirements
### Requirement: 程序化方块材质具有稳定且可辨识的像素图案

系统 SHALL 继续以确定性的 16×16 程序化材质呈现方块，并 MUST 让草方块、石砖、矿石、熔炉、铁块、箱子及新增的圆石、平滑石、沙子、砾石、原木、木板、树叶、玻璃、砖块、白色羊毛、红色瓦块、黏土、雪块和苔藓圆石具有不同于单纯随机噪声的稳定结构图案。材质 MUST NOT 使用 Mojang 或其他未经授权的二进制美术资源。

terrain 材质采样相位 SHALL 由当前面的世界坐标轴决定；相同材质被 AO、天空光、贪心合并上限、区段或区块边界拆分时 MUST 保持连续，负世界坐标 MUST 继续周期采样。草顶明暗簇 MUST 跨 16×16 边界包裹，草侧缘 MUST 使用闭合周期序列，最右列与最左列的草缘高度差 MUST 不超过一个像素。

树叶和玻璃 SHALL 使用现有 atlas 与 terrain pass 中 alpha 仅为 `0` 或 `255` 的 cutout 材质；透明像素 MUST 在 fragment 阶段丢弃，其余像素 MUST 按不透明颜色写入现有深度目标。cutout mip MUST 使用保持覆盖率的降采样，其他不透明层 MUST 保持颜色平均语义。

#### Scenario: 同一方块材质重复生成保持一致

- **GIVEN** 相同代码版本与方块注册表
- **WHEN** 重复创建两份程序化材质集合
- **THEN** 每个材质层的 RGBA 字节 MUST 完全一致

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
- **WHEN** 生成完整材质集合
- **THEN** 每种材料 MUST 具有确定性的 16×16 RGBA 图案并保持设计规定的结构特征
- **AND** 原木顶底面 MUST 显示同一组年轮、侧面 MUST 显示纵向树皮，雪块顶面与侧面 MUST 使用各自固定图案

#### Scenario: 世界坐标保持纹理相位连续
- **GIVEN** 同一材质表面跨越 quad、区段或区块边界，且边界任一侧可能位于负世界坐标
- **WHEN** terrain shader 采样该表面
- **THEN** 两侧 UV 相位 MUST 由相同世界坐标周期确定，且 MUST NOT 因 quad 局部原点重置而产生接缝

#### Scenario: cutout alpha 与 mip 保持孔洞覆盖
- **GIVEN** 树叶或玻璃的基础层与后续 mip
- **WHEN** 检查 alpha 取值和各级透明覆盖
- **THEN** 基础层 alpha MUST 仅为 `0` 或 `255`，透明像素 MUST 被丢弃，覆盖保持 mip MUST 防止边框或叶簇在远处整体消失

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

### Requirement: 可放置方块使用真实程序化材质缩略图

系统 SHALL 在快捷栏、背包、合成、熔炉和箱子栏位中为可放置方块显示该方块注册表材质生成的缩略图。非方块物品 MAY 继续使用程序化色块。缩略图 MUST NOT 引入外部或未经授权的美术资源。

#### Scenario: 方块与非方块物品使用对应呈现

- **GIVEN** 同一栏位视图中存在草方块与工具
- **WHEN** 绘制物品内容
- **THEN** 草方块 MUST 使用 `GrassID` 注册表材质的像素缩略图
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

系统 SHALL 保持 HUD 像素几何与命中几何一致，并 MUST 在 640×360 及更大的 framebuffer 中让九格快捷栏完整落在屏幕内；打开背包时，完整的固定合成区域 MUST 通过统一缩放落在 framebuffer 内且不得相互重叠。

#### Scenario: 640×360 关闭 HUD 完整可见

- **WHEN** 在 640×360 framebuffer 绘制快捷栏与爱心栏
- **THEN** 所有栏位、选中边界和左下角无背景爱心 MUST 位于 framebuffer 边界内

#### Scenario: 640×360 打开固定合成区域

- **WHEN** 在 640×360 framebuffer 打开背包与固定合成区域
- **THEN** 全部背包栏位、配方行与合成按钮 MUST 位于 framebuffer 边界内
- **AND** 命中测试 MUST 与缩放后的绘制矩形一致

### Requirement: 视觉优化保持固定有界渲染成本

系统 MUST 复用现有 HUD render pass、字体图集与固定容量上传缓冲；热路径在预热后 MUST 保持零分配，且不得新增外部依赖、UI 框架或每帧动态资源。terrain 材质 MUST 继续使用固定 2D array atlas 与单一现有 terrain pass，quad 实例格式 MUST 保持 `8` 字节，不得增加第二个透明 pass、透明排序或每帧材质资源创建。

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
