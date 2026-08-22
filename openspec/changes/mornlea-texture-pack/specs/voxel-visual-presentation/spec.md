## ADDED Requirements

### Requirement: 产品默认使用内嵌材质并保留程序化最终回退

图形客户端 SHALL 以经许可适配并内嵌的 Pixel Perfection 子集替换有直接对应关系的程序化 layer；没有内嵌映射的 layer MUST 继续使用现有确定性程序化材质。程序化注册表 MUST 保持可独立构造，作为完整最终回退与测试基线。

#### Scenario: 已映射 layer 使用内嵌默认像素

- **GIVEN** 一个逻辑 layer 在内嵌默认包中存在直接对应素材
- **WHEN** 图形客户端构造产品默认材质集合
- **THEN** 该 layer MUST 使用内嵌默认像素而非程序化像素

#### Scenario: 未映射 layer 保持程序化结果

- **GIVEN** 一个逻辑 layer 在内嵌默认包中没有直接对应素材
- **WHEN** 图形客户端构造产品默认材质集合
- **THEN** 该 layer MUST 与独立程序化注册表中的对应 layer 逐字节一致

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
