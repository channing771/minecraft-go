## Why

当前材料少且 terrain quad 各自重置 UV，相邻草块会在 AO、光照、区段或区块拆分处出现纹理断层；现有 Opaque 语义也无法同时表达可见但不遮光的玻璃和树叶。

## What Changes

- 只追加 14 种标准立方体材料及对应物品，全部可堆叠、放置、采掘并掉落自身。
- 新增 RegisteredBlock 并在协议/存档边界拒绝未知方块。
- 协议升级到 v15、玩家 schema 到 v6、区块 schema 到 v8，布局不变并做 identity migration。
- terrain 使用世界坐标 UV；玻璃和树叶使用同一 atlas、同一 pass 的 alpha cutout。
- 缺失玩家首次获得背包材料包；已有玩家不变。
- 新增无窗口 materials-showcase，并记录而不门禁性能数值。

## Capabilities

### New Capabilities

- `common-block-materials`：定义固定材料注册、权威放置采掘、缺失玩家材料包、语义版本和 cutout 方块行为。

### Modified Capabilities

- `voxel-visual-presentation`：改为世界坐标 UV，并增加固定程序化材质与单 pass alpha cutout 呈现。
- `visual-verification`：在现有无窗口场景末尾增加材料展示验收场景。

## Impact

影响 core/sim/network/world/storage/server/assets/mesh/render/cmd/mcgo；不新增依赖、外部资源、世界生成、配方、方块状态或渲染 pass。

线上协议由 v14 升为 v15，玩家 schema 由 v5 升为 v6，区块 schema 由 v7 升为 v8；字节布局不变，旧数据执行 identity migration，future schema 与未知编号明确拒绝。服务端权威边界和现有并发模型不变。渲染热路径继续使用固定 atlas、单一 terrain pass 与 8 字节实例格式，性能数值只记录，真实 overflow、数据丢失、报告结构和 I/O 错误仍失败。回退代码后，新 schema 会作为 future schema 被旧程序明确拒绝。
