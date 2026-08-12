## MODIFIED Requirements

### Requirement: 程序化天空表达昼夜层次
天空 SHALL 按视线仰角和既有昼夜亮度显示连续的地平线到天顶渐变；太阳在地平线上方时 SHALL 显示为固定角直径的暖白圆盘，月亮在地平线上方时 SHALL 显示为固定角直径的冷白圆盘。星空 MUST 由视线世界方向确定性生成，只在夜间渐入，且不得因帧号、相机位置或连接 transport 改变图案。程序化方块云 MUST 依相机世界坐标产生规定视差，且不得改变天空渐变、太阳、月亮或星点的既有屏幕位置。

#### Scenario: 正午只显示日间天空与太阳
- **GIVEN** 权威相位为 `6000`
- **WHEN** 相机看向天顶
- **THEN** 画面 MUST 显示日间天空和太阳圆盘，月亮与星空 MUST 不可见

#### Scenario: 午夜显示月亮与固定星空
- **GIVEN** 权威相位为 `18000`
- **WHEN** 相机看向天顶并在原地旋转后再转回相同朝向
- **THEN** 画面 MUST 显示月亮和夜间星空，且转回后星点图案 MUST 与原图案一致

#### Scenario: 相机平移只使云产生世界坐标视差
- **GIVEN** 权威时间和相机朝向不变
- **WHEN** 相机只改变世界位置
- **THEN** 天空渐变、太阳、月亮和星点的屏幕位置 MUST 保持不变，云图案 MUST 按相机世界坐标产生视差

## ADDED Requirements

### Requirement: 程序化方块云遵循固定世界网格与时间偏移
云层 SHALL 固定在世界 Y=`192` 的平面上，以 `16` block 为 cell、以 `4×4` cells 为 macro，并以固定十字形覆盖形态填充活动 macro。macro MUST 仅在 `hash(macro) & 3 != 0` 时活动，即四个低两位结果中有三个活动，理论活动 macro 比例为 `3/4`；每个活动 macro 的五格十字形使理论总 cell 覆盖率为 `5/16 × 3/4 = 15/64`，约 `23.4%`。相同世界时间与相机 MUST 得到相同云图案；世界时间每前进 `80` tick，云图案 MUST 向东移动 `1` block。

#### Scenario: 相同输入保持相同图案
- **GIVEN** 两帧使用相同世界时间、相机位置和相机朝向
- **WHEN** 客户端绘制天空
- **THEN** 两帧的云图案 MUST 完全相同

#### Scenario: 固定样本保持三分之四 macro 激活与覆盖率
- **GIVEN** 固定 macro 样本为所有 `macro.x, macro.y ∈ [-8, 7]` 的 `16×16=256` 个正负坐标，且 `hash(macro)` 固定为 `hash_cell(vec3u(bitcast<u32>(macro.x), bitcast<u32>(macro.y), 0u))`
- **WHEN** 系统按 `hash(macro) & 3 != 0` 计算活动 macro 并填充十字形
- **THEN** 每个 macro MUST 仅在低两位不为 `0` 时活动，低两位 `0/1/2/3` 的计数 MUST 分别为 `72/69/62/53`；活动 macro MUST 为 `184` 个、填充 cell MUST 为 `920` 个（总 cell 为 `4096` 个），实际覆盖率 MUST 为 `920/4096=22.4609375%`，并与理论活动比例 `3/4` 及理论覆盖率 `15/64=23.4375%` 一致

#### Scenario: 八十 tick 东移一个 block
- **GIVEN** 相机位置和朝向保持不变
- **WHEN** 世界时间从任意 tick 前进 `80` tick
- **THEN** 云图案 MUST 相对世界坐标向东移动 `1` block

#### Scenario: 相机世界坐标产生视差
- **GIVEN** 世界时间和相机朝向保持不变，且视线可以与云平面相交
- **WHEN** 相机沿世界 X 或 Z 平移
- **THEN** 屏幕中的云图案 MUST 发生与该世界坐标变化一致的视差

### Requirement: 云层仅在可见交点绘制并保持昼夜遮挡
相机位于云平面上方、视线没有与云平面的正交交点或视线接近地平线时，云层 MUST 不绘制或以固定地平线 fade 淡出。可见云 SHALL 使用既有昼夜颜色，并在星空、月亮和太阳绘制后以固定 alpha `0.82` 混合，因此云 MUST 遮挡这些天体；地形深度 MUST 继续覆盖整个天空及云层。

#### Scenario: 上方或无正交交点不绘制云
- **GIVEN** 相机位于 Y=`192` 的云平面上方，或当前视线不能与该平面形成正交交点
- **WHEN** 客户端绘制天空
- **THEN** 该像素 MUST 不绘制云

#### Scenario: 地平线附近云平滑淡出
- **GIVEN** 视线接近地平线且仍可与云平面相交
- **WHEN** 客户端绘制天空
- **THEN** 云层 MUST 以固定 fade 平滑淡出，不得出现硬边或闪烁

#### Scenario: 云遮挡天体但被地形遮挡
- **GIVEN** 云与太阳、月亮或星点在同一屏幕位置，且可见地形面也覆盖该位置
- **WHEN** 客户端完成该帧绘制
- **THEN** 云 MUST 覆盖星空、月亮和太阳，地形面 MUST 覆盖云及全部天空内容

### Requirement: 云层不增加资源或 CPU 热路径成本
程序化方块云 SHALL 复用既有 sky uniform、fullscreen sky draw 和 shader hash，不得创建资源、增加 draw、加载资源或在稳定渲染帧产生 Go 堆分配。

#### Scenario: 稳定帧保持固定资源和零分配
- **GIVEN** 渲染器已经完成初始化且 viewport、相机和世界时间保持稳定
- **WHEN** 连续绘制含云的天空帧
- **THEN** 每帧 MUST 仍只有一次三顶点天空 draw、一次固定大小 uniform 更新和零 Go 堆分配，且 MUST NOT 创建或加载任何新资源
