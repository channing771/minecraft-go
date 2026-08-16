# Change: rust-client-render-entities

## Why

R2a 已交付纯离屏的 Rust 世界地形渲染器并以零像素差异通过双后端对照;客户端
一帧的其余 pass(avatar、掉落物、目标方块轮廓、名牌、伤害红边、HUD 家族、
调试面板)仍只有 Go 实现。本期(R2b)把这些 pass 补齐到 Rust 离屏渲染器,
使双后端对照覆盖**完整 capture 帧**,为 R2c 的生产切换扫清最后的呈现差距;
生产渲染本期仍由 Go 执行。

## What Changes

- Rust:`mornlea_client` render 模块新增 avatar、item drop、block outline、
  name tag(billboard 字形)、damage overlay、HUD、debug panel 各 pass,
  pipeline/blend/depth 语义逐一镜像 Go;新增字形图集(R8 增量矩形上传)与
  HUD 图集纹理;新 WGSL 继续 `include_str!` 单源直引 Go shader。
- 协议:frame payload 升级为 v2——R2a 固定头 + 可见 section 列表 + 变长
  pass 段(tag+len+bytes:avatar/drop instance 流、outline、overlay 强度、
  名牌/HUD/调试面板顶点流);每帧仍恰好一次渲染 FFI。client ABI 2→3,
  新增 `render_upload_glyph_rect` 与 `render_upload_hud_atlas` 低频入口。
- Go:各 pass 的 CPU 准备逻辑(avatar 排序/部件构建、掉落动画相位、字形
  布局、HUD layout)原样保留并复用其既有字节编码,新增把这些字节流装配进
  frame v2 的编码器;`internal/client` 绑定扩展。
- 门禁:双后端对照测试升级为完整场景帧(地形 + 实体 + 文本 + HUD),
  沿用既有 `diffThreshold`;生产渲染路径与 golden 基线零改动。

## Capabilities

### New Capabilities

- `rust-client-render-entities`: Rust 离屏渲染器完整帧呈现的行为契约——
  实体/文本/HUD 各 pass 与 Go 渲染器的双后端图像一致、frame v2 单次 FFI、
  字形与 HUD 图集字节同源、ABI 校验拒绝与生产路径不变。

### Modified Capabilities

无。`rust-client-render-terrain` 等既有规格行为不变,本期只扩展平行实现的
覆盖面;既有视觉规格描述的可观察行为不变。

## Impact

- 受影响包:`engine/crates/mornlea_client`、`internal/client`、
  `internal/render`(仅导出既有编码供装配复用,不改行为)、`cmd/mornlea`
  (对照测试扩展)、`engine/include/mornlea_client.h`。
- 兼容性:client ABI 2→3;协议 v16、存档 schema、benchmark scenario v16、
  golden 基线字节均不变;Linux 专服不受影响。
- 性能:每帧仍一次渲染 FFI,pass 段总量与现有 GPU 上传字节同量级;数值
  只记录。
- 并发:渲染器句柄仍在进程级 Mutex 表;字形光栅化 worker 归 Go,Rust 侧
  只消费矩形上传。

## 非目标

不接窗口 surface;不切换生产渲染入口;不删除 Go 渲染代码;不迁移字形
光栅化、HUD layout 与 avatar/掉落物的 CPU 准备逻辑(留 Go,产字节流);
不修改 golden 与 `diffThreshold`。
