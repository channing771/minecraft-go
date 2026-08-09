## MODIFIED Requirements

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
