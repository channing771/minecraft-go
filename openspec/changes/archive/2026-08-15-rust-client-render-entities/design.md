# Design: rust-client-render-entities

## Context

见 proposal.md「Why」。现状:一帧 pass 顺序为 terrain → avatar → item drop
→ block outline → name tag → damage overlay → HUD(hotbar 家族)→ debug
panel(app_frame.go)。各 Go pass 已把语义输入编码为 GPU 字节流(如
`encodeAvatarPartsInto` 的 instance 布局、HUD `encode.go` 顶点、debug panel
quad/glyph);字形图集为异步光栅化 + 每帧预算限流的 R8 矩形增量上传
(`GlyphAtlas.FlushUploads`),HUD 图集由 `buildHotbarTextureAtlas` 一次性
构建。R2a 的 Rust 渲染器已有 terrain/sky/cull/HiZ 与 frame v1 协议
(client ABI v2),oak grove 对照零像素差异。

## Goals / Non-Goals

- Goals:Rust 离屏渲染器补齐 7 类 pass;frame v2 单次 FFI;完整帧双后端
  对照;字形/HUD 图集字节同源。
- Non-Goals:生产切换与 surface(R2c);删除 Go 渲染代码;迁移 CPU 准备
  逻辑(排序/布局/光栅化留 Go);golden/阈值修改。

## Decisions

### CPU 准备留 Go,pass 段搬运既有字节编码

各 pass 的 Go prepare 已产出 GPU 就绪字节(instance/顶点/uniform);R2b 不
重新发明格式,`internal/render` 把这些编码以最小导出面暴露(或测试侧直接
调用既有导出入口拿到字节),Go 装配器把它们塞进 frame v2 的 pass 段。Rust
侧按 Go 的缓冲布局直接消费,杜绝两套编码漂移。

### frame v2:固定头 + sections + TLV pass 段

- v1 头(192B)保持;`layout version` 字段升为 2;
- sections 列表之后追加 pass 段序列,每段 `tag u32 + length u32 + bytes`
  (4 字节对齐),已定义 tag:avatar instances、drop instances、
  outline(位置+参数)、overlay strength、name tag 顶点、HUD 顶点、
  debug panel 顶点;未知 tag 或长度越界返回 StatusInput 且不渲染;
- 每类 pass 段至多出现一次,缺席表示该 pass 本帧为空。

### 图集入口(client ABI 2→3)

- `render_upload_glyph_rect(handle, x, y, w, h, pixels)`:R8 字形矩形,
  镜像 Go `GlyphAtlas.FlushUploads` 对自身纹理的写入;Go 在同一处同步写
  两个后端,字节必然同源;
- `render_upload_hud_atlas(handle, width, height, pixels)`:一次性 RGBA;
- 字形图集尺寸与格式与 Go 常量一致,由 create 时建纹理。

### 管线镜像

各 pass 的 pipeline 状态逐一镜像 Go 构造参数:avatar/drop(顶点缓冲 + 实
例化 indexed indirect、深度写、Less)、outline(LessEqual、线框/盒面)、
name tag(billboard、alpha blend、深度测试不写)、overlay/HUD/debug panel
(屏幕空间、alpha blend、无深度);新 shader 单源:avatar.wgsl、
name_tag.wgsl、damage_overlay.wgsl、debug_panel.wgsl、hud/hotbar.wgsl
(outline 复用 block_outline.wgsl)。pass 录制顺序严格照 app_frame。

### 双后端对照升级

对照测试从纯地形升级为完整帧:场景装配复用既有 capture 场景的 prepare
路径(直接构造 avatar/名牌/HUD 输入),Go 侧按 app_frame 顺序录制整帧,
Rust 侧一次 frame v2;比较沿用 `diffThreshold`。字形收敛沿用 capture 的
settle 帧策略,两个后端在同一收敛点取帧。

## 依赖与并发

- 依赖方向不变:client ABI 只被 `internal/client` 接触;`internal/render`
  只可能新增导出,不新增依赖边;archcheck 白名单不变。
- 渲染器句柄仍在进程级 Mutex 表;字形光栅化 worker 归 Go,Rust 只消费
  矩形字节。
- frame v2 编码在调用方栈上构建,发送后不复用(与消息不可变约定一致)。

## 兼容与回退

- client ABI 2→3:R2a 入口签名不变(frame 校验按 layout version 区分,
  v2 起接受 pass 段);Go binary 与 dylib 同版本 release unit 不变。
- 生产零变化;单 PR revert 无生产影响。

## 验证方法

- Rust 单测(无头):frame v2 解析(TLV 合法/未知 tag/越界拒绝)、字形
  矩形上传校验、各 pass smoke(非全零、同输入逐字节稳定)。
- Go 对照测试(darwin):完整帧场景(地形+avatar+名牌+HUD+面板)双后端
  `diffThreshold` 内;调用计数断言。
- 门禁:`make rust`、focused race、archcheck、全量 race、gofmt/vet、
  openspec strict;既有 golden 零改动。

## 平台假设

同 R2a:渲染与对照仅 darwin;Linux CI 只验证空库编译。两后端同为
wgpu-native/Metal,基于 R2a 零差异经验,完整帧预期同样接近零差异;出现
系统性偏差时查根因记录,不放宽阈值。
