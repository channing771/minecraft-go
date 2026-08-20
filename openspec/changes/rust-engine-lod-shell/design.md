# Design: rust-engine-lod-shell

## Context

见 proposal.md「Why」。现状:近环由权威区块同步 → `mornlea_mesh_section`
(engine ABI v3)生成 section mesh → `SectionScheduler` 每帧预算上传 →
Rust 渲染器 terrain pass(GPU culling + HiZ)。`ViewDistance`(默认 32,
readOnly)同时是同步半径、保留半径与可见半径。worldgen 全量在
`mornlea_engine`(2D Perlin/fbm 高度图,海平面 64,雪线 88,沙线 62,
黏土/砾石噪声,表层分层),种子只存在于服务端 config 与 storage metadata,
客户端登录拿不到。近环 quad 的 `Quad.Pack()` 是 section 局部 4-bit 编码
(X/Y/Z 各 4 bit、W/H ≤16),装不下远环的世界坐标大 quad,远环必须走
独立编码与独立上传入口。

## Goals / Non-Goals

- Goals:可视距离扩展到 `lodFarMultiplier`×;远环确定性纯地表壳;种子经
  v18 登录下发;近环行为逐位不变;benchmark 可比性保持。
- Non-Goals:见 proposal「非目标」;另不重开 go-rust-division.md 的归属
  裁决(壳生成天然落在 engine 侧)。

## Decisions

### 壳生成算法与接缝策略(而非 quadtree LOD / 降采样)

对每个 tile(固定 4×4 chunk = 64×64 列)按 `lodStep` 列合并:步长窗内
高度取 **max**(保守遮挡:矮处被高处挡住,绝不漏视到"下方"),材质取
最高列的 worldgen 表层材质;同材质等高的相邻窗贪心合并为一个顶面 quad;
相邻窗高度断差处生成朝向断差方向的侧裙 quad。**优点**:接缝在构造上
不可能裂开(裙边永远补齐断差),不需要 quadtree 的跨级缝合;几何量最小
(纯壳,无地下内部面)。**被否决的替代**:(a) 整块 `mornlea_worldgen_chunk`
生成 + 降采样 mesh——4× 半径即 16× 面积的完整生成,CPU/内存不可行,
且产生无意义的地下面;(b) Go 侧 `worldgen_probe` 逐点拼壳——Go 生产
路径出现体素级 O(N) 循环与高频 FFI 往返,踩 division 文档红线。顶面用
世界坐标 terrain UV(与近环同源同图集);着色 = 天空光满档 × 法线朝向
权重(斜坡变暗),昼夜 tint 由渲染侧统一计算,不做第二套昼夜。

### engine ABI 3→4:`mornlea_lod_shell`

签名与既有批量导出同风格(caller-owned 输入/输出 + status);输入 =
perm 播种字节(格式与 `mornlea_worldgen_chunk` 完全一致)+ tile 原点
(chunk 坐标,int32×2)+ 列数(固定 64)+ `lodStep`(合法值 2/4/8);
输出 = 壳 quad 流,每 quad 字段:世界 X/Z(block,int32)、Y(int32)、
宽/深(block,uint16)、面朝向(top/side)、材质 ID、着色权重(uint8),
位布局在实现时定稿并写入头文件注释。status 复用 0..9:`ABI_VERSION`
拒绝旧版本、`INVALID_ARGUMENT`/`INPUT` 拒绝非法步长与越界 tile、
`SCRATCH`/`OUTPUT_OVERFLOW` 两段式探测(先报所需容量,扩容重试成功)、
panic catch → 9。确定性契约:同 perm + 同 tile + 同步长 → 全平台逐位
一致输出,与 worldgen 差分门禁同一标准。

### 种子下发与协议版本(而非服务端下发远环几何)

`LoginSuccess` 增加 `WorldSeed uint64`。单机(内置权威服务端)与 TCP
远程共用同一登录协议,两条路径自动一致。协议 v16→v18:v17 已被 M5B
分支占用,本变更排在其后;若落地顺序对调,两分支互换版本号并同步
golden wire 测试。版本不匹配沿用既有握手拒绝,不产生半兼容会话。
种子暴露不构成新风险:TCP 面向可信局域网且无加密,抓包本可读全部区块。
**被否决的替代**:服务端按需下发远环几何/高度摘要——把"每客户端一次
8 字节"变成持续带宽与协议面,且与确定性本地生成的成本优势相反。

### client ABI 4→5 与远环 pass

`render_upload_lod_tile(abi, x, z, quads, len)` / `render_drop_lod_tile`
(整 tile 生命周期替换,复用近环 section 的覆盖语义:重复上传同 tile 即
整体替换)。远环独立 pipeline:quad 顶点为世界坐标大四边形,不复用
section 网格编码;帧序 天空 → 远环 → 近环 → 实体/HUD,远环写深度,
近环自然覆盖。距离雾在远环 WGSL 内按相机距离向天空色 mix,外缘带
(最外 25%)全雾;近环 v1 不雾化。雾色与昼夜 tint 同源(复用 day/night
uniform,不新增状态)。剔除:v1 仅 tile 级视锥剔除(每帧 ≤ 数十 tile),
不进 HiZ/GPU culling。

### Go 编排:`internal/lod` 与独立预算

`internal/lod.Scheduler` 语义镜像 `SectionScheduler`:pending map 覆盖
最新、按与中心 chunk 距离升序冲刷、`DropOutside` 丢弃远环半径外的
pending 与已上传 tile(触发 `render_drop_lod_tile`);生成调用与上传
共享一个**独立帧预算**(默认与近环同量级,数值只记录),绝不与近环
`SectionScheduler` 共享预算。入队时机:登录成功取得种子后播种全环;
玩家跨 tile 边界时增量入队新进入范围的 tile。`internal/nativeabi` 新增
`LodShell` 绑定;archcheck 登记 `internal/lod`(依赖 core/nativeabi,
不得依赖 render/sim/network)。配置:`lodEnabled`(默认 true;benchmark
与 capture 既有场景按需关闭)、`lodFarMultiplier`(默认 3,范围 2..8)、
`lodStep`(默认 4);`viewDistance` 保持 readOnly。

### oracle 保留方案(而非第二套 Go 壳实现)

不写 Go 版壳 mesh oracle(违反"不留第二套生产实现"的纪律且维护成本
高)。替代覆盖:(a) 高度差分——用既有 `mornlea_worldgen_probe` 逐列
采样,断言壳的窗高 == 窗内 max(worldgen 高度);(b) 结构性质测试——
每个 tile 的顶面恰好覆盖全部列一次、断差处裙边闭合(边界遍历无洞);
(c) Rust 侧固定 seed/tile/step 的输出字节 golden 回归(确定性门禁)。

## 依赖与并发

- 依赖方向:`internal/lod` → `internal/core` + `internal/nativeabi`;
  `cmd/mornlea` 装配 `internal/lod` 与 `internal/client`;engine/client
  ABI 互不耦合,Linux 专服不链接 client 库、不涉及种子下发。
- 并发:壳生成在客户端 worker goroutine 内按帧预算批量执行(镜像字形
  worker 模型),结果经不可变字节切片跨 goroutine 传递(发送后视为不可变,
  遵循既有消息纪律);渲染器 tile 表与既有 renderer 同一 Mutex 模型。

## 兼容与回退

- engine ABI 3→4、client ABI 4→5:新增出口,既有入口签名不变;两库
  仍互不耦合,Linux 专服 release unit(engine so)照旧,client 库版本
  与 darwin 客户端一起演进。
- 协议 v18:M2 v15/M5 v14 基线原字节;握手版本拒绝覆盖新旧客户端组合;
  golden wire 测试更新到 v18 字节。
- benchmark scenario v16 不迁移,producer 默认 `lodEnabled=false`,
  基准输出结构不变;LOD 专项数值另存记录。
- 回退:`lodEnabled=false` 是行为级回退开关;远环资产(tile 表、pass)
  随禁用不参与帧循环;整支 revert 无存档/协议残留(v18 字段不再下发即
  回 v16 语义——但 v18 已合并则保持版本号,由后续 change 裁决)。

## 验证方法

- Rust:`cargo test -p mornlea_engine -p mornlea_client`、
  `cargo clippy --all-targets -- -D warnings`、`make rust`;壳确定性
  golden、入口校验拒绝、两段式 overflow 单测;远环 pass 无头入口单测。
- Go:`internal/lod` 调度行为测试;probe 高度差分与壳覆盖结构测试;
  `internal/network` v18 编解码与握手拒绝测试;capture golden 重新生成
  (变化仅远景带)并新增 `far-horizon` 场景;受影响包 `-race`、全量
  `go test ./... -race`、archcheck、vet、gofmt、openspec strict。
- 真窗:CGEvent 注入验收(复用 R1/R2c 工具)——远景入画、雾过渡平滑、
  移动跨 tile 无闪烁、关闭路径干净。

## 平台假设

渲染仅 darwin;远环 pass 同此假设。Linux CI 验证 engine 空库编译与专服
bundle;确定性 golden 在全平台 CI 上一致。性能数值只记录,报告完整性、
真实 overflow 与数据丢失仍是门禁。
