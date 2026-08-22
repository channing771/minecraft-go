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

抓帧场景清单 MUST 保留 `far-horizon` 为倒数第二个场景，并 MUST 保留 `water-underwater` 为唯一末场景。重建材质视觉基线时，系统 MUST 在写入任何 golden 前，以相同生效 registry、世界种子、场景状态、相机和渲染配置分别抓取启用与禁用 LOD 的 `far-horizon`；两次抓帧除 `lodEnabled` 外 MUST 等价。系统 MUST 复用既有几何推导的顶部与底部受保护行，对两张当前帧执行逐像素近环比较；任一受保护行不同 MUST 拒绝整次更新且不得覆盖任何 golden。该 control MUST NOT 依赖旧 golden 是否存在，既有视觉比较阈值 MUST 保持不变。

#### Scenario: 远环紧邻末尾水下场景

- **GIVEN** 完整 capture 场景清单
- **WHEN** 检查其末尾顺序
- **THEN** `far-horizon` MUST 位于 `water-underwater` 之前
- **AND** `far-horizon` MUST 是倒数第二个场景，`water-underwater` MUST 是唯一末场景

#### Scenario: 重立默认材质基线先执行材质无关近环 control

- **GIVEN** 调用方显式请求为新的内嵌默认材质更新整套 golden
- **WHEN** 系统准备覆盖第一张 golden
- **THEN** 系统 MUST 先用同一生效 registry 完成 LOD on/off `far-horizon` 成对抓帧并执行受保护行比较
- **AND** 该 control MUST 在旧 golden 缺失时仍执行
- **AND** 任一近环差异 MUST 使整次更新失败且所有既有 golden 保持不变

#### Scenario: 真正的远景带差异不阻止材质基线更新

- **GIVEN** LOD on/off 成对抓帧只在几何推导的远景带存在差异，受保护的顶部与底部行逐像素一致
- **WHEN** 系统执行材质 golden 更新
- **THEN** 近环 control MUST 通过
- **AND** 系统 MAY 在继续使用既有双阈值的前提下写入经复核的内嵌默认材质 golden
