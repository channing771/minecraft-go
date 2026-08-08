## ADDED Requirements

### Requirement: 天空光通道场景只在收敛后无窗口抓取

抓帧场景清单 MUST 在既有场景末尾增加 `skylight-tunnel`。该场景 MUST 通过正常客户端镜像快照路径装入固定 `3×3` 夹具，包含露天入口、半遮蔽过渡区和超过传播距离的深处，并固定正午时间、相机位置与朝向。场景 MUST 经既有 dirty、调度、结果回收和 renderer upload 路径收敛后才抓取；抓帧 MUST NOT 创建或聚焦游戏窗口，既有视觉差异阈值 MUST 保持不变。

#### Scenario: 收敛后的通道抓帧显示梯度
- **GIVEN** `skylight-tunnel` 的固定夹具已装入客户端镜像
- **WHEN** 无窗口抓帧完成预热、网格收敛和上传
- **THEN** 输出目录 MUST 包含名为 `skylight-tunnel` 的图像，且图像 MUST 显示从洞口到深处可辨认的递减天空光梯度

#### Scenario: 未收敛时拒绝抓取半成品
- **GIVEN** `skylight-tunnel` 的 Mesher 或待上传结果在有界预热内未收敛
- **WHEN** 抓帧程序准备写出该场景
- **THEN** 程序 MUST 返回包含场景名的错误，且 MUST NOT 写入该场景的新 golden

#### Scenario: 新 golden 需完整场景复核
- **GIVEN** 调用方显式请求更新视觉基线
- **WHEN** M5 无窗口抓帧生成包含 `skylight-tunnel` 的新图像集
- **THEN** 调用方 MUST 人工复核全部场景图像后才能接受新 golden，且既有场景不得被该夹具污染
