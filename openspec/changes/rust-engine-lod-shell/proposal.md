# Change: rust-engine-lod-shell

## Why

当前视野被单一 `ViewDistance` 半径卡死:它同时决定服务端同步范围、section
mesh 保留范围与 BFS 可见范围,看多远就必须同步多少,远方的山与地平线在
近环边缘断崖式消失。同时 engine 的确定性 worldgen(高度图 + 表层材质)
意味着客户端只要拿到种子就能在本地还原任意远处的地表——"种子即真相"
的远环不需要任何区块同步。本变更新增远环 LOD:近环(权威同步 + 精确
section mesh)完全不动,远环由客户端本地确定性生成纯地表壳 mesh,以距离
雾掩盖精度接缝,把可视距离从 `ViewDistance`(默认 32 chunk)扩展到
`lodFarMultiplier` 倍(默认 3),网络、存档、服务端模拟零改动。

## What Changes

- Rust engine:ABI 5→6(变基重编,原 3→4;main 的 fluid 系列占用 v4/v5),
  新增 `mornlea_lod_shell` 纯函数批量导出——输入
  perm 播种字节(与 `mornlea_worldgen_chunk` 同格式)、tile 原点、列数与
  LOD 步长(2/4/8);内部复用 worldgen 高度与表层材质选择,步长内列高取
  max(保守遮挡),同材质等高顶面贪心合并,高度断差生成侧裙 quad(接缝在
  构造上不可能裂开),输出带世界坐标 terrain UV 与斜坡着色权重的壳 quad
  流;status/两段式 overflow 语义与既有导出一致,无状态纯函数。
- Rust client:ABI 5→7(变基重编,原 4→5/6;v5 为 main 的 water pass),
  新增 `render_upload_lod_tile`/`render_drop_lod_tile`
  与远环独立 pipeline(大尺寸世界坐标 quad);距离雾按相机距离向天空色
  衰减,外缘带全雾,雾色与昼夜 tint 同源;帧序为天空 → 远环 → 近环
  terrain → 实体/HUD,远环写深度。
- 协议 v21→v23:`LoginSuccess` 增加 `WorldSeed uint64`(种子即真相的
  前提是种子到达客户端);v22 已被在飞的 authoritative-farming 占用,
  本变更排在其后落地(变基重编:本段在旧基线上原编号 v16→v18,main 合并
  fluid 系列至 v21 后顺延)。
- Go:新建 `internal/lod`(tile 环形队列,语义镜像 SectionScheduler:
  pending 覆盖、由近到远、界外丢弃;独立帧预算,不与近环共享);
  `internal/nativeabi` 新增绑定;config 新增 `lodEnabled`、
  `lodFarMultiplier`(默认 3)、`lodStep`(默认 4)调参。
- 视觉与门禁:既有 capture golden 因远景入画与注水地形重新生成(变化仅
  限远景带与水景,近处内容不变),新增 `far-horizon` 场景(变基排序:
  排在 `water-underwater` 之前、倒数第二);benchmark 远环默认禁用,
  scenario 保持 v17;用 `mornlea_worldgen_probe` 高度差分 + 壳覆盖性质
  结构测试替代第二套 Go 壳 oracle,海平面以下的窗口按 Ruling 22 钳到
  水面并取水材质(水下不发裙边)。

## Capabilities

### New Capabilities

- `rust-engine-lod-shell`:远环 LOD 的行为契约——确定性壳生成、种子经
  登录下发、远环呈现与雾、种子即真相的编辑语义、tile 调度与预算、
  配置与基准可比性。

### Modified Capabilities

无既有主规格的文本修改;`voxel-visual-presentation`、
`visual-verification` 描述的近环行为 MUST 原样成立(golden 更新仅因
新增远景带与水景),协议相关主规格随 v23 在归档时由本 capability 的迁移
说明覆盖。

## Impact

- 受影响包:`engine/crates/mornlea_engine`(lod 模块 + ffi)、
  `engine/crates/mornlea_client`(远环 pass + ffi)、`engine/include`
  两个头文件、`internal/nativeabi`、`internal/lod`(新建)、
  `internal/client`(上传绑定)、`internal/network`(LoginSuccess +
  协议版本)、`internal/config`、`cmd/mornlea`、`internal/archcheck`、
  capture golden 资产。
- 兼容性:engine ABI 5→6、client ABI 5→7(6=远环 tile 出口、7=雾 setter,
  变基重编——main 的 water pass 占用 v5 后整体顺延)、协议 v21→v23;
  区块 schema v9、玩家 schema v6、世界 metadata v2、`companions.ai`
  schema v4 均不变;benchmark scenario 保持 v17;M2 v15/M5 v14 基线
  原字节;Linux 专服不链接 client 库、不下发种子、零影响。
- 排序约束:协议版本上 v22 已被在飞的 authoritative-farming 占用,本变更
  重编为 v23 并 MUST 排在其后落地;若两变更的合并顺序对调,需互换版本号
  并同步改握手拒绝矩阵与 golden wire 测试。远环壳的 engine ABI 段在旧
  基线原编号 v4、client 段原编号 v5/v6,main 合并 fluid 系列(占用
  engine v4/v5 与 client v5)后分别重编为 v6 与 v6/v7。
- 性能:远环生成按 tile(4×4 chunk)分摊到帧预算;benchmark/perfcheck
  数值只记录。
- 回退:远环独立于近环管线,`lodEnabled=false` 即行为级回退;整支 revert
  回到当前基线,无存档迁移。

## 非目标

不迁移近环 mesh/光照/同步语义;不做远环橡树或任何非地表几何(确定性
橡树留在近环出现);不做编辑感知远环(同步半径外的人工修改不反映到
远环,雾化弱化感知,属接受的正确性边界);不做 quadtree 多级 LOD 与
GPU culling 远环接入(v1 tile 级视锥剔除);不改 HiZ、实体、HUD 与
capture 之外的视觉路径;不做跨平台渲染。
