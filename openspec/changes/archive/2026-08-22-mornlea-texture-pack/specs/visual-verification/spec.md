## ADDED Requirements

### Requirement: 视觉基线固定使用内嵌默认材质

无窗口 capture 与其 golden SHALL 使用内嵌默认材质，MUST NOT 应用本机用户材质覆盖。受默认材质替换影响的场景 MUST 经完整渲染链路重新生成并逐图复核；既有双阈值比较规则 MUST 保持不变。

#### Scenario: 本机覆盖不影响视觉基线

- **GIVEN** 本机配置了一个有效用户材质目录
- **WHEN** 生成或比对 capture 场景
- **THEN** 输出 MUST 使用内嵌默认材质
- **AND** 用户目录内容 MUST NOT 改变任何 golden 或比较结果

#### Scenario: 默认视觉变化仍使用既有阈值

- **GIVEN** 内嵌默认材质改变了多个已映射 layer 的像素
- **WHEN** 显式更新并复核受影响的视觉基线
- **THEN** 更新后的场景 MUST 继续使用既有双阈值比较
- **AND** MUST NOT 通过放宽阈值接受材质或渲染缺陷

### Requirement: 远环与水下场景顺序及近环保护保持不变

抓帧场景清单 MUST 保留 `far-horizon` 为倒数第二个场景，并 MUST 保留 `water-underwater` 为唯一末场景。重建材质视觉基线时，系统 MUST 在写入任何 golden 前，以两个 disposable application 和相同生效 registry、世界种子、场景状态、相机及渲染配置分别抓取启用与禁用 LOD 的 `far-horizon`；两次 control 除 `lodEnabled` 外 MUST 等价。系统 MUST 复用既有几何推导的顶部与底部受保护行，对两张当前帧执行逐像素近环比较；任一受保护行不同 MUST 拒绝整次更新且不得覆盖任何 golden。每个已经成功构造的 control application MUST 在成功、后续构造失败或 guard 失败路径关闭；guard 通过并关闭两者后，系统 MUST 再构造一个 fresh LOD-on application，且只有该 application MAY 按正常完整场景顺序执行正式 capture 与写盘。该 control MUST NOT 依赖旧 golden 是否存在，既有视觉比较阈值 MUST 保持不变。

#### Scenario: 远环紧邻末尾水下场景

- **GIVEN** 完整 capture 场景清单
- **WHEN** 检查其末尾顺序
- **THEN** `far-horizon` MUST 位于 `water-underwater` 之前
- **AND** `far-horizon` MUST 是倒数第二个场景，`water-underwater` MUST 是唯一末场景

#### Scenario: 重立默认材质基线先执行材质无关近环 control

- **GIVEN** 调用方显式请求为新的内嵌默认材质更新整套 golden
- **WHEN** 系统准备覆盖第一张 golden
- **THEN** 系统 MUST 先用同一生效 registry 和两个 disposable application 完成 LOD on/off `far-horizon` 成对抓帧并执行受保护行比较
- **AND** 该 control MUST 在旧 golden 缺失时仍执行
- **AND** 任一近环差异 MUST 使整次更新失败且所有既有 golden 保持不变

#### Scenario: 正式 capture 从 fresh application 开始

- **GIVEN** LOD on/off control 已通过
- **WHEN** 系统开始正式完整 capture
- **THEN** 两个 control application MUST 已关闭
- **AND** 正式 `runCapture` MUST 接收一个未执行过 `far-horizon` control scene 的 fresh LOD-on application
- **AND** 正式场景 MUST 按普通 capture 的既有完整顺序运行

#### Scenario: control 生命周期失败时全部关闭

- **GIVEN** 任一 control application 构造失败、近环 guard 失败，或 fresh 正式 application 构造失败
- **WHEN** 更新路径返回错误
- **THEN** 每个已经成功构造的 application MUST 被关闭
- **AND** 正式 capture MUST NOT 在 guard 失败或 control application 尚未关闭时开始

#### Scenario: 真正的远景带差异不阻止材质基线更新

- **GIVEN** LOD on/off 成对抓帧只在几何推导的远景带存在差异，受保护的顶部与底部行逐像素一致
- **WHEN** 系统执行材质 golden 更新
- **THEN** 近环 control MUST 通过
- **AND** 系统 MAY 在继续使用既有双阈值的前提下写入经复核的内嵌默认材质 golden

## MODIFIED Requirements

### Requirement: 视觉基线覆盖统一方块与 HUD 风格

系统 SHALL 通过既有无窗口固定场景记录并比对当前产品默认方块材质与 HUD 呈现。地形场景 MUST 覆盖内嵌默认 layer 与没有内嵌映射时的程序化回退，HUD 场景 MUST 覆盖快捷栏、从同一生效 registry 采样的真实方块缩略图、数量阴影、耐久状态与左下角无背景爱心栏，并 MUST 以独立场景覆盖打开的背包与合成区域；更新基线时 MUST 继续执行既有显式更新与双阈值规则。

`materials-showcase` MUST 保持既有固定正午、固定相机和确定性夹具，并经与交互客户端相同的完整呈现链路收敛后无窗口抓取，不得创建或聚焦前台游戏窗口。夹具 MUST 同时覆盖 14 种新材料、八格连续草地、相邻玻璃、相邻树叶，以及原木顶面年轮与侧面树皮。既有双阈值 MUST 保持不变。

抓帧场景清单 MUST 保留 `target-block-feedback`、`oak-grove` 与 `ai-companion` 的既有名称及相对顺序，`ai-companion` MUST 继续紧随 `oak-grove`，并 MUST 保留当前末尾的 `water-surface-slope`、倒数第二的 `far-horizon` 与唯一末场景 `water-underwater`。`target-block-feedback` MUST 使用固定正午、固定相机和确定性夹具，且经与交互客户端相同的完整呈现链路收敛后无窗口抓取。它 MUST 命中一个已注册材料方块，并同时可审查细轮廓、中文名称和正确的遮挡关系；抓帧或比对 MUST NOT 使用隐藏目标提示的专用开关。`inventory-crafting` 因打开背包而隐藏目标提示，其目标提示隐藏状态、背包与合成区域语义 MUST 保持不变；若内嵌默认材质、程序化回退或共享地形背景改变可观察像素，其 golden MAY 在逐图复核后更新。`oak-grove` MUST 使用固定世界种子、固定生成区块、固定正午和固定相机，并经与交互客户端相同的完整呈现链路收敛后无窗口抓取，不得创建或聚焦前台游戏窗口。`ai-companion` MUST 使用固定世界时间、相机、伙伴身份、维度、位置与朝向，显示中文名牌“阿木”、一条 accepted ChatEvent 和打开的 `@阿木 挖石头` 输入，并经统一的人形、名牌和聊天 HUD 呈现链路无窗口抓取。

#### Scenario: 地形与 HUD 风格变化产生可审查基线

- **GIVEN** 既有固定场景与渲染链路可用
- **WHEN** 显式更新本变更影响的视觉基线
- **THEN** `terrain-noon` MUST 包含当前内嵌默认材质及没有内嵌映射 layer 的程序化回退
- **AND** `hud-hotbar-health` MUST 包含当前快捷栏、从同一生效 registry 采样的真实方块缩略图、紧凑两位间距的数量数字、耐久状态与左下角无背景爱心栏
- **AND** `inventory-crafting` MUST 包含打开的 3×9 背包区、1×9 快捷栏区和固定合成区域
- **AND** 三张图 MUST 由无窗口完整渲染链路产出

#### Scenario: 远端玩家场景只继承地形背景变化

- **GIVEN** 远端玩家与名牌的渲染逻辑没有变化，但场景共享的当前产品默认地形背景发生变化
- **WHEN** 更新本变更影响的视觉基线
- **THEN** `avatar-nametag` MUST 继承当前地形背景
- **AND** 远端玩家轮廓、颜色与名牌文字 MUST 保持既有可观察语义

#### Scenario: 材料展示保持既有验收夹具

- **GIVEN** `materials-showcase` 的固定夹具已装入客户端镜像
- **WHEN** `materials-showcase` 完成网格和上传收敛并抓帧
- **THEN** 图像 MUST 同时显示 14 种新材料各一个近景样本及多方块表面、跨至少一个 AO 或天空光拆分边界的八格连续草地、两个相邻玻璃方块、两个相邻树叶方块，以及原木顶面年轮与侧面树皮
- **AND** 玻璃后方方块 MUST 可见，树叶孔洞和光照 MUST 可辨认，相同 cutout 方块的内部面 MUST 不可见

#### Scenario: 材料展示只走无窗口完整链路

- **GIVEN** `materials-showcase` 使用固定正午与固定相机
- **WHEN** 生成或比对 `materials-showcase`
- **THEN** 抓帧 MUST 使用与交互客户端相同的完整呈现链路
- **AND** MUST NOT 创建或聚焦前台游戏窗口，且 MUST 继续使用现有双阈值

#### Scenario: 共享视觉变化需完整复核

- **GIVEN** 内嵌默认材质、程序化回退、世界生成或世界坐标 UV 改变了多个既有场景的共享可观察像素
- **WHEN** 显式更新视觉基线
- **THEN** 系统 MUST 只写入实际变化的 golden，调用方 MUST 逐张复核全部场景图像后才能接受更新

#### Scenario: 伙伴场景与当前末尾顺序并存

- **GIVEN** 完整无窗口场景清单
- **WHEN** 检查 `target-block-feedback` 之后的场景名称与顺序
- **THEN** `oak-grove` 与 `ai-companion` MUST 保持既有名称，且 `ai-companion` MUST 紧随 `oak-grove`
- **AND** `water-surface-slope` MUST 位于 `ai-companion` 之后，`far-horizon` MUST 是倒数第二个场景，`water-underwater` MUST 是唯一末场景

#### Scenario: 橡树林通过正常渲染链路抓取

- **GIVEN** `oak-grove` 的固定世界种子、生成区块、正午时间与相机已经装入客户端镜像
- **WHEN** 场景完成预热、网格收敛和上传并抓帧
- **THEN** 图像 MUST 由与交互客户端相同的完整呈现链路产出，且 MUST 显示固定橡树地貌
- **AND** 抓帧 MUST NOT 创建或聚焦前台游戏窗口，且 MUST 继续使用既有双阈值

#### Scenario: AI 伙伴通过统一呈现链路抓取

- **GIVEN** `ai-companion` 已重置前一场景的 remote、companion、chat、inventory、panel、container、mining、damage 和 item-drop 状态，并装入固定伙伴和聊天夹具
- **WHEN** 场景完成预热和上传并抓帧
- **THEN** 图像 MUST 由统一的人形、名牌与聊天 HUD 呈现链路产出，且 MUST 同时显示伙伴人形、中文名牌“阿木”、accepted 事件与打开的 `@阿木 挖石头` 输入
- **AND** 抓帧 MUST NOT 创建或聚焦前台游戏窗口，且 MUST 继续使用既有双阈值

#### Scenario: 目标反馈通过正常渲染链路验证遮挡

- **GIVEN** `target-block-feedback` 的固定夹具命中一个已注册材料方块
- **WHEN** 场景完成预热、网格收敛和上传并抓帧
- **THEN** 图像 MUST 同时显示该方块的细轮廓、中文名称和被地形正确遮挡的边
- **AND** 场景 MUST 使用与交互客户端相同的完整呈现链路并保持正确遮挡，不得创建或聚焦前台游戏窗口

#### Scenario: 打开背包的基线不受目标提示影响

- **GIVEN** `inventory-crafting` 场景打开背包
- **WHEN** 显式更新所有视觉基线
- **THEN** `inventory-crafting` MUST 不显示目标轮廓或名称
- **AND** 背包与合成区域的可观察语义 MUST 保持不变
- **AND** 只有经逐图复核确认由当前产品默认材质或共享地形背景变化引起时，它的 golden MAY 更新

#### Scenario: 默认材质变化允许复核后更新既有 golden

- **GIVEN** 内嵌默认材质改变了一个或多个既有场景的可观察像素
- **WHEN** 调用方显式更新视觉基线
- **THEN** 版本控制中的 golden 变化 MUST 只包含实际变化的场景，且调用方 MUST 逐张复核全部候选图后才能接受
- **AND** 系统 MUST NOT 仅因既有 golden 被当前产品默认材质改变而拒绝整次更新

### Requirement: 天空光通道场景只在收敛后无窗口抓取

抓帧场景清单 MUST 保留既有 `skylight-tunnel` 和 `block-light-room`，并 MUST 保留 `far-horizon` 为倒数第二个场景、`water-underwater` 为整个清单的唯一末场景。两个光照场景 MUST 通过与交互客户端相同的世界状态与呈现链路装入固定夹具，并在全部有限后台工作收敛后才抓取；抓帧 MUST NOT 创建或聚焦游戏窗口，既有视觉差异阈值 MUST 保持不变。设备型号 MUST NOT 作为该视觉语义验收的额外条件。`skylight-tunnel` MUST 保持露天入口、半遮蔽过渡区、超过传播距离的深处以及固定正午时间、相机位置和朝向。`block-light-room` MUST 是午夜的完整封闭房间，唯一照明源 MUST 是一个发光块；图像 MUST 显示室内由近到远的方块光衰减，房外和缺失邻区边界 MUST 无漏光。

#### Scenario: 收敛后的通道抓帧显示梯度

- **GIVEN** `skylight-tunnel` 的固定夹具已装入客户端镜像
- **WHEN** 无窗口抓帧完成预热、网格收敛和上传
- **THEN** 输出目录 MUST 包含名为 `skylight-tunnel` 的图像，且图像 MUST 显示从洞口到深处可辨认的递减天空光梯度

#### Scenario: 收敛后的封闭房间只由发光块照亮

- **GIVEN** `block-light-room` 的封闭房间夹具已装入客户端镜像，世界时间为午夜且房间内只有一个发光块
- **WHEN** 无窗口抓帧完成预热、网格收敛和上传
- **THEN** 输出目录 MUST 包含名为 `block-light-room` 的图像，图像 MUST 显示室内从光源由近到远的衰减，房外与边界 MUST 无亮缝

#### Scenario: 未收敛时拒绝抓取半成品

- **GIVEN** `skylight-tunnel` 或 `block-light-room` 的有限后台工作在有界预热内未收敛
- **WHEN** 抓帧程序准备写出该场景
- **THEN** 程序 MUST 返回包含场景名的错误，且 MUST NOT 写入该场景的新 golden

#### Scenario: 房间边界漏光超过阈值时失败

- **GIVEN** `block-light-room` 的实拍图在房外或缺失邻区边界出现非预期亮区
- **WHEN** 图像与已接受 golden 执行既有双阈值比对
- **THEN** 任一局部高差值或大面积中等差值超过既有阈值 MUST 使验证失败，且不得自动放宽阈值

#### Scenario: 新 golden 需完整场景复核

- **GIVEN** 调用方显式请求更新视觉基线
- **WHEN** 受支持设备的无窗口抓帧生成包含 `skylight-tunnel`、`block-light-room`、`target-block-feedback`、`oak-grove`、`ai-companion`、倒数第二的 `far-horizon` 与末尾 `water-underwater` 的新图像集
- **THEN** 调用方 MUST 人工复核全部场景图像后才能接受新 golden，且任一场景未收敛、方块光、伙伴、水下或远环语义不成立，或比对超过既有阈值 MUST 失败
- **AND** 由当前内嵌默认材质引起的既有 golden 变化 MAY 在完整复核后接受，设备型号 MUST NOT 成为额外的接受或拒绝条件
