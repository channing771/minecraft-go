# Design: rust-engine-worldgen

## Context

见 proposal.md「Why」。现状:`internal/worldgen` 全部为 Go 生产实现——`newPerlin`
用 Go `math/rand.Shuffle` 播种 512 项 perm 表,`fbm`/`at` 求 2D Perlin 噪声,
`generatedBlockAt` 做地表分层与矿石替换(整数 splitmix 系哈希),`tree.go` 以 8×8
候选格哈希放置橡树。调用方(server.TerrainProbe、cmd/mornlea*、gfxspike)只使用
`worldgen.New`/`GenerateChunk`/`HeightAt`/`TerrainBlockAt`/`BaseBlockAt`。
engine 现有 ABI v2(mesh/light/collision/raycast/step)。

## Goals / Non-Goals

- Goals:worldgen 全部计算迁入 engine;调用方与 `world.Chunk`/palette 零改动;
  同种子逐位一致,全平台差分门禁(见「平台假设」)。
- Non-Goals:不改任何方块输出;不移植 Go `math/rand`;不改存档编解码与
  `Compact` 行为;不做多线程区块生成。

## Decisions

### 两个 ABI 入口,magic `MGW1`,engine ABI 2→3

- `mornlea_worldgen_chunk`:一次生成整区块。header(layout v1):magic、layout
  version、seed(i64)、chunk_x/chunk_z(i32)、min_y/max_y(i32,校验必须等于
  −64/320)、材料表(13×u16:air/stone/dirt/grass/bedrock/snow/sand/clay/gravel/
  iron_ore/coal_ore/oak_log/leaves)、perm 表(512×u8)。输出:dense
  16×16×384×u16 block id,布局 `[y-min_y][lz][lx]`(y 外层,便于 Go 顺序回写),
  共 196608 字节。
- `mornlea_worldgen_probe`:batch 单点查询,沿用 raycast 的 64-record batch
  模式。每 record:mode(u32:0=height/1=terrain/2=base)、wx/wy/wz(i32);
  输出每 record 8 字节:height(i32)+ block id(u16)+ reserved。header 与 chunk
  入口共用 seed/材料表/perm 布局。
- 备选被否:单入口「probe 复用整块生成」——TerrainProbe 与 material_migration
  逐点扫描,整块生成代价不可接受;「chunk-only、单点留 Go」——违反无生产 Go
  fallback 纪律。

### 材料表与 perm 表由 Go 传入

Block 注册表与随机源所有权留在 Go:Rust 不硬编码 BlockID、不内置 RNG。perm 表
播种(`rand.NewSource`+`Shuffle`)是 Go 语义,移植风险高收益零,Go 算好 512 字节
随 header 传入。engine 校验 perm 值 <256、材料表无 air 冲突,违约返回 StatusInput
且不触碰输出缓冲。

### 浮点逐条镜像,禁用 FMA

fbm/perlin 只含 f64 加、乘、除、`Floor` 与截断转换——全部 IEEE 正确舍入运算,
无 libm 超越函数。Rust 侧逐条镜像 Go 运算顺序,禁止 `mul_add` 与任何重结合;
`int32(f64)` 截断语义与 Rust `as i32` 一致(输出高度在界内,饱和语义不触发)。
因此差分门禁可全平台运行,不需要 physics-step 的 arm64 限定。

### 区块回写保持 Go 侧语义

Go 收到 dense 数组后按 y/z/x 顺序仅对非 air 值调用 `chunk.SetBlock`,再
`Compact`——与现状「只写到地表高度 + 树只写原木/树叶」的写入集合完全一致,
palette 构建路径不变。橡树的「原木优先、树叶仅覆盖空气」合并规则在 Rust 内
对 dense 数组执行,顺序镜像 Go `applyOakTrees`。

### 旧 Go 实现降为测试 oracle

`noise.go`/`generator.go`/`ore.go`/`tree.go` 的计算逻辑整体移入
`oracle_test.go`(同 physics-step 模式),生产文件只留 API 壳、perm 播种、
native 调用与回写。差分测试:固定语料 + 随机种子×区块 fuzz,逐位比较
oracle 与生产输出;probe 与 chunk 交叉一致性测试。

## 依赖与并发

- 依赖方向:worldgen → nativeabi → C ABI;archcheck 需为 `internal/worldgen`
  登记 nativeabi 依赖边(现无)。
- Generator 无内部可变状态;perm 表与材料表在 `New` 时定格,每次调用只读传入,
  并发调用安全性与现状一致。
- 区块生成在调用方 goroutine 同步执行,不阻塞权威 tick 的既有约束由调用方
  维持(现状不变)。

## 兼容与回退

- 存档/协议/benchmark 字节不变;旧世界新生成区块逐位一致(由 oracle 差分与
  既有 golden 保证)。
- engine ABI 2→3,Go 与 `libmornlea_engine` 仍为同版本 release unit,
  `$ORIGIN` 约定不变。
- 回退:单 PR revert;oracle 即旧实现副本,可随时移回生产。

## 平台假设

worldgen 内核只用 IEEE 基本运算,Go/Rust 在 arm64 与 amd64 上均逐位一致,
差分门禁全平台启用;若 CI 实测出现平台差异,按 physics-step 先例收缩门禁范围
并记录原因,而不是放宽比较精度。
