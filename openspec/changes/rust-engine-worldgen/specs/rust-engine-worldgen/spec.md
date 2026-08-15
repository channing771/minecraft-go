# rust-engine-worldgen Delta Spec

## Purpose

把世界生成(地形分层、矿石、橡树)交由 Rust engine 唯一生产实现,Go 只保留 seed 播种、领域 API 与编码,并以 Go 测试 oracle 保证同种子世界逐位一致。

## ADDED Requirements

### Requirement: 世界生成由 Rust engine 独占生产

高度图地形、地表分层(草/土/石/基岩/雪/沙/黏土/砂砾)、矿石替换与橡树结构 MUST
由 Rust engine 独占生产;Go 生产路径 MUST 不包含噪声求值、地形分层、矿石哈希或
树候选实现,旧 Go 实现只允许存在于测试 oracle。

#### Scenario: 同种子区块与 Go oracle 逐位一致

- GIVEN 任意种子与任意区块坐标
- WHEN 调用 worldgen.GenerateChunk
- THEN 区块内每个 (x,y,z) 的 BlockID 与测试内 Go oracle 实现完全一致

#### Scenario: 单点查询与整块生成一致

- GIVEN 任意种子、世界坐标 (wx,wz) 与合法 Y
- WHEN 分别调用 HeightAt/TerrainBlockAt/BaseBlockAt 与 GenerateChunk
- THEN 单点结果与该坐标所在生成区块的对应方块一致,且与 Go oracle 一致

#### Scenario: 跨区块橡树一致

- GIVEN 一棵候选橡树的树冠横跨相邻两个区块
- WHEN 分别生成这两个区块
- THEN 两个区块内该树的原木与树叶方块拼合后与 oracle 树形一致,原木优先、
  树叶仅覆盖空气的规则保持不变

### Requirement: perm 表由 Go 播种并随调用传入

seed→512 项 Perlin perm 表 MUST 继续由 Go `math/rand` 语义计算;engine MUST 只
消费传入的 perm 表,不得内置任何随机源。相同 seed 在迁移前后 MUST 产生相同世界。

#### Scenario: 相同 seed 迁移前后世界不变

- GIVEN 迁移前某 seed 生成过的任意区块字节
- WHEN 迁移后用同一 seed 再生成同一区块
- THEN 方块内容逐位一致,既有存档加载后新生成区块与旧世界无缝衔接

### Requirement: worldgen ABI 输入校验拒绝

engine worldgen 入口收到非法输入(错误 magic、长度不符、材料表存在重复 ID、Y 范围
非法)时 MUST 返回输入错误状态且 MUST 不修改输出缓冲;Go 侧 MUST 把该状态转换为
带稳定中文文案的错误报告,MUST 不产出部分生成的区块。

#### Scenario: 非法输入被拒绝且输出缓冲不变

- GIVEN 构造的非法 worldgen 请求(如材料表两项 ID 相同或输出缓冲长度不符)
- WHEN 调用 engine worldgen 入口
- THEN 返回输入错误状态,输出缓冲保持调用前内容,Go 报告稳定中文错误

### Requirement: 既有世界生成行为规格保持不变

`deterministic-ore-generation`、`deterministic-tree-generation`、
`natural-material-generation` 中描述的全部可观察行为 MUST 保持逐字不变地继续
成立;本变更 MUST NOT 改变任何方块输出。

#### Scenario: 既有行为测试继续通过

- GIVEN 既有矿石、橡树与自然材料测试语料
- WHEN 在迁移后的生产路径上运行这些测试
- THEN 全部断言不修改而通过
