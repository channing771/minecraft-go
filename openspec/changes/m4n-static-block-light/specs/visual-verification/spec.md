## MODIFIED Requirements

### Requirement: 天空光通道场景只在收敛后无窗口抓取

抓帧场景清单 MUST 保留既有 `skylight-tunnel`，并在全部既有场景末尾增加 `block-light-room`。两个场景 MUST 通过正常客户端镜像路径装入固定夹具，并经既有 dirty、调度、结果回收和 renderer upload 路径收敛后才抓取；抓帧 MUST NOT 创建或聚焦游戏窗口，既有视觉差异阈值 MUST 保持不变。设备型号 MUST NOT 作为该视觉语义验收的额外条件。`skylight-tunnel` MUST 保持露天入口、半遮蔽过渡区、超过传播距离的深处以及固定正午时间、相机位置和朝向。`block-light-room` MUST 是午夜的完整封闭房间，唯一照明源 MUST 是一个发光块；图像 MUST 显示室内由近到远的方块光衰减，房外和缺失邻区边界 MUST 无漏光。

#### Scenario: 收敛后的通道抓帧显示梯度
- **GIVEN** `skylight-tunnel` 的固定夹具已装入客户端镜像
- **WHEN** 无窗口抓帧完成预热、网格收敛和上传
- **THEN** 输出目录 MUST 包含名为 `skylight-tunnel` 的图像，且图像 MUST 显示从洞口到深处可辨认的递减天空光梯度

#### Scenario: 收敛后的封闭房间只由发光块照亮
- **GIVEN** `block-light-room` 的封闭房间夹具已装入客户端镜像，世界时间为午夜且房间内只有一个发光块
- **WHEN** 无窗口抓帧完成预热、网格收敛和上传
- **THEN** 场景清单末尾 MUST 产出名为 `block-light-room` 的图像，图像 MUST 显示室内从光源由近到远的衰减，房外与边界 MUST 无亮缝

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
- **WHEN** 受支持设备的无窗口抓帧生成包含 `skylight-tunnel` 与末尾 `block-light-room` 的新图像集
- **THEN** 调用方 MUST 人工复核全部场景图像后才能接受新 golden，且任一场景未收敛、方块光语义不成立或比对超过既有阈值 MUST 失败；设备型号 MUST NOT 成为额外的接受或拒绝条件
