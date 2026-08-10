## MODIFIED Requirements

### Requirement: 视觉基线覆盖统一方块与 HUD 风格

系统 SHALL 通过既有无窗口固定场景记录并比对优化后的方块材质与 HUD 呈现。地形场景 MUST 覆盖程序化方块材质，HUD 场景 MUST 覆盖快捷栏、真实方块缩略图、数量阴影、耐久状态与左下角无背景爱心栏，并 MUST 以独立场景覆盖打开的背包与合成区域；更新基线时 MUST 继续执行既有显式更新与双阈值规则。

`materials-showcase` MUST 保持既有固定正午、固定相机和确定性夹具，并经正常客户端镜像、mesher、renderer 与 upload 路径收敛后无窗口抓取，不得创建或聚焦前台游戏窗口。夹具 MUST 同时覆盖 14 种新材料、八格连续草地、相邻玻璃、相邻树叶，以及原木顶面年轮与侧面树皮。既有双阈值 MUST 保持不变。

抓帧场景清单 MUST 在全部既有场景末尾追加 `target-block-feedback`。该场景 MUST 使用固定正午、固定相机和确定性夹具，且经正常客户端镜像、mesher、renderer、depth 与 name-tag 路径收敛后无窗口抓取。它 MUST 命中一个已注册材料方块，并同时可审查细轮廓、中文名称和正确的遮挡关系；抓帧或比对 MUST NOT 使用隐藏目标提示的专用开关。`inventory-crafting` 因打开背包而隐藏目标提示，其 golden MUST 保持逐字节不变。

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

- **GIVEN** 世界坐标 UV 改变了多个既有场景的共享地形背景
- **WHEN** 显式更新视觉基线
- **THEN** 系统 MUST 只写入实际变化的 golden，调用方 MUST 逐张复核全部场景图像后才能接受更新

#### Scenario: 目标反馈位于场景表末尾

- **GIVEN** 既有无窗口场景清单保持原顺序
- **WHEN** 注册 `target-block-feedback`
- **THEN** 它 MUST 位于场景表末尾，且此前场景的名称与顺序 MUST 保持不变

#### Scenario: 目标反馈通过正常渲染链路验证遮挡

- **GIVEN** `target-block-feedback` 的固定夹具命中一个已注册材料方块
- **WHEN** 场景完成预热、网格收敛和上传并抓帧
- **THEN** 图像 MUST 同时显示该方块的细轮廓、中文名称和被地形正确遮挡的边
- **AND** 场景 MUST 使用正常镜像、mesher、renderer、depth 与 name-tag 路径，不得创建或聚焦前台游戏窗口

#### Scenario: 打开背包的基线不受目标提示影响

- **GIVEN** `inventory-crafting` 场景打开背包
- **WHEN** 显式更新所有视觉基线
- **THEN** `inventory-crafting` MUST 不显示目标轮廓或名称
- **AND** 它的 golden MUST 与变更前逐字节相同

### Requirement: 天空光通道场景只在收敛后无窗口抓取

抓帧场景清单 MUST 保留既有 `skylight-tunnel` 和 `block-light-room`。两个场景 MUST 通过正常客户端镜像路径装入固定夹具，并经既有 dirty、调度、结果回收和 renderer upload 路径收敛后才抓取；抓帧 MUST NOT 创建或聚焦游戏窗口，既有视觉差异阈值 MUST 保持不变。设备型号 MUST NOT 作为该视觉语义验收的额外条件。`skylight-tunnel` MUST 保持露天入口、半遮蔽过渡区、超过传播距离的深处以及固定正午时间、相机位置和朝向。`block-light-room` MUST 是午夜的完整封闭房间，唯一照明源 MUST 是一个发光块；图像 MUST 显示室内由近到远的方块光衰减，房外和缺失邻区边界 MUST 无漏光。

#### Scenario: 收敛后的通道抓帧显示梯度

- **GIVEN** `skylight-tunnel` 的固定夹具已装入客户端镜像
- **WHEN** 无窗口抓帧完成预热、网格收敛和上传
- **THEN** 输出目录 MUST 包含名为 `skylight-tunnel` 的图像，且图像 MUST 显示从洞口到深处可辨认的递减天空光梯度

#### Scenario: 收敛后的封闭房间只由发光块照亮

- **GIVEN** `block-light-room` 的封闭房间夹具已装入客户端镜像，世界时间为午夜且房间内只有一个发光块
- **WHEN** 无窗口抓帧完成预热、网格收敛和上传
- **THEN** 输出目录 MUST 包含名为 `block-light-room` 的图像，图像 MUST 显示室内从光源由近到远的衰减，房外与边界 MUST 无亮缝

#### Scenario: 未收敛时拒绝抓取半成品

- **GIVEN** `skylight-tunnel` 或 `block-light-room` 的 Mesher 或待上传结果在有界预热内未收敛
- **WHEN** 抓帧程序准备写出该场景
- **THEN** 程序 MUST 返回包含场景名的错误，且 MUST NOT 写入该场景的新 golden

#### Scenario: 房间边界漏光超过阈值时失败

- **GIVEN** `block-light-room` 的实拍图在房外或缺失邻区边界出现非预期亮区
- **WHEN** 图像与已接受 golden 执行既有双阈值比对
- **THEN** 任一局部高差值或大面积中等差值超过既有阈值 MUST 使验证失败，且不得自动放宽阈值

#### Scenario: 新 golden 需完整场景复核

- **GIVEN** 调用方显式请求更新视觉基线
- **WHEN** 受支持设备的无窗口抓帧生成包含 `skylight-tunnel`、`block-light-room` 与末尾 `target-block-feedback` 的新图像集
- **THEN** 调用方 MUST 人工复核全部场景图像后才能接受新 golden，且任一场景未收敛、方块光语义不成立或比对超过既有阈值 MUST 失败；设备型号 MUST NOT 成为额外的接受或拒绝条件
