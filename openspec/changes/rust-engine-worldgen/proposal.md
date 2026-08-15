# Change: rust-engine-worldgen

## Why

世界生成（高度图地形、地表分层、矿石、橡树）仍是 Go 生产实现,是最后一块留在 Go 的
确定性计算内核。前序变更已把 mesh/light、collision resolver、raycast DDA 与物理
tick 积分迁入 Rust `mornlea_engine`;本变更把 worldgen 也迁入,使全部确定性内核统一
由 engine 独占生产,Go 只保留领域 API、seed→perm 表播种与编码。同种子世界必须与
现有 Go 输出逐位一致(arm64 门禁),既有世界与测试语料不受影响。

## What Changes

- Rust:新增 `mornlea_worldgen_chunk`(一次调用生成整区块 dense block-ID 数组,
  含地形分层、矿石与橡树)与 `mornlea_worldgen_probe`(batch 单点查询,服务
  HeightAt/TerrainBlockAt/BaseBlockAt);engine ABI version 2→3。
- Go:`worldgen.Generator` 公共 API 形状不变;`newPerlin` 的 seed→512 项 perm 表
  仍由 Go `math/rand` 计算并随调用传给 engine;`GenerateChunk` 改为一次 native
  调用 + 写入 `world.Chunk` 并 Compact;单点查询走 batch probe。旧 Go 噪声/地形/
  矿石/橡树逻辑移入 `_test.go` oracle,生产无 Go fallback。
- 行为零变化:区块 schema v8、协议 v16、benchmark scenario v16、世界 metadata v2
  均不变;`deterministic-ore-generation`、`deterministic-tree-generation`、
  `natural-material-generation` 等既有行为规格不修改。

## Capabilities

### New Capabilities

- `rust-engine-worldgen`: Rust engine 独占世界生成生产路径的行为契约——同种子
  逐位一致、Go oracle 差分门禁、perm 表所有权与 ABI 输入校验拒绝。

### Modified Capabilities

无。既有 worldgen 行为规格(矿石、橡树、自然材料)描述的可观察行为不变,本变更
只迁移生产实现载体。

## Impact

- 受影响包:internal/worldgen、internal/nativeabi、engine/crates/mornlea_engine、
  engine/include。
- 调用方零改动:internal/server(TerrainProbe)、cmd/mornlea、cmd/mornlea-server、
  cmd/gfxspike 继续使用 `worldgen.New`/`GenerateChunk`/`HeightAt` 等现有入口。
- 兼容性:engine ABI +1(2→3);Go binary 与 `libmornlea_engine.so` 仍为不可跨版本
  混装的 release unit,`$ORIGIN` 约定不变;既有 mesh/light/raycast/collision/step
  ABI 不动。存档与协议字节不变,旧世界加载后再生成的区块与迁移前逐位一致。
- 性能:区块生成从逐 block Go 计算改为一次 native 调用 + 一次 dense 数组回写;
  benchmark/perfcheck 数值只记录,不改变退出状态。
- 并发:Generator 保持无内部可变状态、可并发调用的纯函数语义。

## 非目标

不改变任何地形/矿石/橡树的可观察输出;不迁移存档编解码、`world.Chunk`/palette
数据结构或光照传播调用方;不移植 Go `math/rand`(perm 表仍由 Go 播种);不引入
生产 Go fallback。
