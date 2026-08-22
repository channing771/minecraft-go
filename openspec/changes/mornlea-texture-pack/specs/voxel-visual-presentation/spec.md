## MODIFIED Requirements

### Requirement: 方块材质具有稳定且可辨识的像素图案

图形客户端产品路径 SHALL 在完整、确定性的 16×16 程序化材质注册表上，以经许可适配并内嵌的 Pixel Perfection 子集替换具有直接对应素材的 layer；没有内嵌映射的 layer MUST 保留程序化像素。程序化注册表 MUST 保持可独立构造，作为完整最终回退与测试基线。产品生效材质 MUST 让草方块、石砖、矿石、熔炉、铁块、箱子及新增的圆石、平滑石、沙子、砾石、原木、木板、树叶、玻璃、砖块、白色羊毛、红色瓦块、黏土、雪块和苔藓圆石具有不同于单纯随机噪声的稳定结构图案。材质 MUST NOT 使用 Mojang 或其他未经授权的二进制美术资源；内嵌第三方材质 MUST 携带其许可证、署名、来源与修改说明。

terrain 材质采样相位 SHALL 由当前面的世界坐标轴决定；相同材质被 AO、天空光、贪心合并上限、区段或区块边界拆分时 MUST 保持连续，负世界坐标 MUST 继续周期采样。程序化草顶明暗簇 MUST 跨 16×16 边界包裹，程序化草侧缘 MUST 使用闭合周期序列，最右列与最左列的草缘高度差 MUST 不超过一个像素。

树叶和玻璃 SHALL 使用现有 atlas 与 terrain pass 中 alpha 仅为 `0` 或 `255` 的 cutout 材质；透明像素 MUST 在 fragment 阶段丢弃，其余像素 MUST 按不透明颜色写入现有深度目标。cutout mip MUST 使用保持覆盖率的降采样，其他不透明层 MUST 保持颜色平均语义。

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

#### Scenario: cutout alpha 与 mip 保持孔洞覆盖

- **GIVEN** 树叶或玻璃的基础层与后续 mip
- **WHEN** 检查 alpha 取值和各级透明覆盖
- **THEN** 基础层 alpha MUST 仅为 `0` 或 `255`，透明像素 MUST 被丢弃，覆盖保持 mip MUST 防止边框或叶簇在远处整体消失

### Requirement: 可放置方块使用生效注册表材质缩略图

系统 SHALL 在快捷栏、背包、合成、熔炉和箱子栏位中为可放置方块显示当前生效方块注册表材质生成的缩略图；该注册表 MUST 与世界 terrain 采样使用同一套程序化、内嵌默认及可选用户覆盖后的 layer。非方块物品 MAY 继续使用程序化色块。缩略图 MUST NOT 引入 Mojang 或其他未经授权的美术资源。

#### Scenario: 方块与非方块物品使用对应呈现

- **GIVEN** 同一栏位视图中存在草方块与工具
- **WHEN** 绘制物品内容
- **THEN** 草方块 MUST 使用当前生效 `GrassID` 注册表材质的像素缩略图
- **AND** 工具 MUST 继续使用可辨识的程序化色块

## ADDED Requirements

### Requirement: 材质替换不改变现有呈现契约

替换 layer 像素时，系统 MUST 保持现有世界坐标 UV、atlas layer 顺序与尺寸、mip 生成及 Rust atlas 上传形状不变。树叶、玻璃和作物 MUST 保持既有 alpha cutout 分类与几何；水 MUST 保持 water pass、斜水面几何与透明排序；植物 MUST 保持交叉斜面几何。远环 LOD MUST 继续按既有 material ID 采样同一 atlas。

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

## RENAMED Requirements

- FROM: `### Requirement: 程序化方块材质具有稳定且可辨识的像素图案`
- TO: `### Requirement: 方块材质具有稳定且可辨识的像素图案`
- FROM: `### Requirement: 可放置方块使用真实程序化材质缩略图`
- TO: `### Requirement: 可放置方块使用生效注册表材质缩略图`
