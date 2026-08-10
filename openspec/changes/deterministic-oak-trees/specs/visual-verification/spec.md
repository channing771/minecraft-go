## MODIFIED Requirements

### Requirement: 视觉基线覆盖统一方块与 HUD 风格

系统 SHALL 通过既有无窗口固定场景记录并比对优化后的方块材质与 HUD 呈现。地形场景 MUST 覆盖程序化方块材质，HUD 场景 MUST 覆盖快捷栏、真实方块缩略图、数量阴影、耐久状态与左下角无背景爱心栏，并 MUST 以独立场景覆盖打开的背包与合成区域；更新基线时 MUST 继续执行既有显式更新与双阈值规则。

`materials-showcase` MUST 保持既有固定正午、固定相机和确定性夹具，并经正常客户端镜像、mesher、renderer 与 upload 路径收敛后无窗口抓取，不得创建或聚焦前台游戏窗口。夹具 MUST 同时覆盖 14 种新材料、八格连续草地、相邻玻璃、相邻树叶，以及原木顶面年轮与侧面树皮。既有双阈值 MUST 保持不变。

抓帧场景清单 MUST 保留 `target-block-feedback`，并在全部既有场景末尾追加 `oak-grove`。`target-block-feedback` MUST 使用固定正午、固定相机和确定性夹具，且经正常客户端镜像、mesher、renderer、depth 与 name-tag 路径收敛后无窗口抓取。它 MUST 命中一个已注册材料方块，并同时可审查细轮廓、中文名称和正确的遮挡关系；抓帧或比对 MUST NOT 使用隐藏目标提示的专用开关。`inventory-crafting` 因打开背包而隐藏目标提示，其目标提示隐藏状态、背包与合成区域语义 MUST 保持不变；若世界生成改变共享地形背景，其 golden MAY 在逐图复核后更新。`oak-grove` MUST 使用固定世界种子、固定生成区块、固定正午和固定相机，并经正常客户端 mirror、mesher、renderer 与 upload 路径收敛后无窗口抓取，不得创建或聚焦前台游戏窗口。

#### Scenario: 地形与 HUD 风格变化产生可审查基线

- **WHEN** 显式更新本变更影响的视觉基线
- **THEN** `terrain-noon` MUST 包含当前程序化地形材质
- **AND** `hud-hotbar-health` MUST 包含当前快捷栏、真实方块缩略图、紧凑两位间距的数量数字、耐久状态与左下角无背景爱心栏
- **AND** `inventory-crafting` MUST 包含打开的 3×9 背包区、1×9 快捷栏区和固定合成区域
- **AND** 三张图 MUST 由无窗口完整渲染链路产出

#### Scenario: 远端玩家场景只继承地形背景变化

- **GIVEN** 远端玩家与名牌的渲染逻辑没有变化，但场景共享的程序化地形背景发生变化
- **WHEN** 更新本变更影响的视觉基线
- **THEN** `avatar-nametag` MUST 继承当前地形背景
- **AND** 远端玩家轮廓、颜色与名牌文字 MUST 保持既有可观察语义

#### Scenario: 材料展示保持既有验收夹具

- **WHEN** `materials-showcase` 完成网格和上传收敛并抓帧
- **THEN** 图像 MUST 同时显示 14 种新材料各一个近景样本及多方块表面、跨至少一个 AO 或天空光拆分边界的八格连续草地、两个相邻玻璃方块、两个相邻树叶方块，以及原木顶面年轮与侧面树皮
- **AND** 玻璃后方方块 MUST 可见，树叶孔洞和光照 MUST 可辨认，相同 cutout 方块的内部面 MUST 不可见

#### Scenario: 材料展示只走无窗口完整链路

- **WHEN** 生成或比对 `materials-showcase`
- **THEN** 抓帧 MUST 使用固定正午、固定相机和正常镜像、mesher、renderer、upload 链路
- **AND** MUST NOT 创建或聚焦前台游戏窗口，且 MUST 继续使用现有双阈值

#### Scenario: 共享地形变化需完整复核

- **GIVEN** 世界生成或世界坐标 UV 改变了多个既有场景的共享地形背景
- **WHEN** 显式更新视觉基线
- **THEN** 系统 MUST 只写入实际变化的 golden，调用方 MUST 逐张复核全部场景图像后才能接受更新

#### Scenario: 橡树林位于场景表末尾

- **GIVEN** 既有无窗口场景清单保持原顺序
- **WHEN** 注册 `oak-grove`
- **THEN** `oak-grove` MUST 位于场景表末尾，且此前场景的名称与顺序 MUST 保持不变

#### Scenario: 橡树林通过正常渲染链路抓取

- **GIVEN** `oak-grove` 的固定世界种子、生成区块、正午时间与相机已经装入客户端镜像
- **WHEN** 场景完成预热、网格收敛和上传并抓帧
- **THEN** 图像 MUST 由正常 mirror、mesher、renderer 与 upload 路径产出，且 MUST 显示固定橡树地貌
- **AND** 抓帧 MUST NOT 创建或聚焦前台游戏窗口，且 MUST 继续使用既有双阈值

#### Scenario: 目标反馈通过正常渲染链路验证遮挡

- **GIVEN** `target-block-feedback` 的固定夹具命中一个已注册材料方块
- **WHEN** 场景完成预热、网格收敛和上传并抓帧
- **THEN** 图像 MUST 同时显示该方块的细轮廓、中文名称和被地形正确遮挡的边
- **AND** 场景 MUST 使用正常镜像、mesher、renderer、depth 与 name-tag 路径，不得创建或聚焦前台游戏窗口

#### Scenario: 打开背包的基线不受目标提示影响

- **GIVEN** `inventory-crafting` 场景打开背包
- **WHEN** 显式更新所有视觉基线
- **THEN** `inventory-crafting` MUST 不显示目标轮廓或名称
- **AND** 背包与合成区域的可观察语义 MUST 保持不变
- **AND** 只有经逐图复核确认由共享地形背景变化引起时，它的 golden MAY 更新
