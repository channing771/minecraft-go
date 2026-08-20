## MODIFIED Requirements

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
