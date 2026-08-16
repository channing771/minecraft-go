# Design: rust-client-render-terrain

## Context

见 proposal.md「Why」。现状:Go `internal/render` 经 `internal/gfx`
(oliverbestmann/webgpu,底层 wgpu-native/Metal)渲染;terrain pass 消费
`internal/mesh` 的 8 字节紧凑 Quad(X/Y/Z/W/H 4bit 打包 + Face + Mat +
AO + Light),材质是 `internal/assets.Registry` 程序化生成的 layer atlas,
GPU culling 与 HiZ 用 cull.wgsl/hiz_build.wgsl/hiz_copy.wgsl,天空与程序化
方块云在 sky.wgsl。可见性(BFS 连通性 + frustum)在 Go CPU 侧算出 section
列表。无窗口 capture 以 640×360 回读 + `diffThreshold` 双阈值比对 golden。
`mornlea_client` crate(R1)已有窗口 ABI v1 与 thread-local 句柄表。

## Goals / Non-Goals

- Goals:纯离屏 Rust 世界渲染器(terrain/sky/云/cull/HiZ)、双后端图像
  对照门禁、mesh 单次过境、每帧单次渲染 FFI。
- Non-Goals:窗口 surface 呈现;实体/文本/HUD/debug;生产入口切换;删除
  Go 渲染代码;BFS 可见性迁移(纯 CPU 遍历,留 Go);golden/阈值修改。

## Decisions

### render 模块进 `mornlea_client`,client ABI 1→2

渲染器与窗口同属客户端呈现栈,共用 crate 与 thread-local 句柄表(渲染器
句柄独立于窗口句柄,R2a 不关联窗口)。新增 wgpu crate 依赖(darwin
target);选择与 Go 绑定内嵌 wgpu-native 相同主版本线,降低跨后端行为
差异。备选被否:独立 render crate——与 R2c 接 surface 时必须和窗口同库,
拆开只增加构建面。

### WGSL 单源:`include_str!` 直引 Go 侧 shader 文件

双后端共存期间 terrain/sky/cull/hiz_build/hiz_copy 必须逐字节同源,Rust 侧
用 `include_str!("../../../../internal/render/shader/<name>.wgsl")` 编译期
内嵌,杜绝复制漂移;R2c 删除 Go 渲染器时再把文件迁入 crate。相对路径脆弱
性由 crate 单测(存在性 + 非空)兜底。

### ABI 入口族(均带校验,违约不触碰调用方缓冲)

- `mornlea_client_render_create(abi, width, height, out_handle)`:离屏
  color(RGBA8)+depth target 与全部 pipeline;
- `mornlea_client_render_upload_atlas(abi, handle, layers, layer_bytes)`:
  Go `assets.Registry` 导出的 layer 像素一次性上传(材质所有权留 Go,
  Rust 不重新生成);
- `mornlea_client_render_upload_section(abi, handle, section_pos, quads,
  quads_len)`:紧凑 Quad 原字节(8 的倍数校验),变脏才调用;
- `mornlea_client_render_drop_section(abi, handle, section_pos)`;
- `mornlea_client_render_frame(abi, handle, frame, frame_len)`:固定头
  (view/proj 矩阵、相机位置、日照时间、标志)+ 可见 section 列表
  (Go BFS+frustum 结果);一帧一次;
- `mornlea_client_render_readback(abi, handle, out, out_len)`:回读 RGBA
  (长度必须精确匹配),golden 对照专用。

### GPU 管线镜像 Go 实现

pass 顺序、bind group 布局、record 编码(sectionRecordBytes 结构)、
culling dispatch 与 HiZ mip 链构建逐一镜像 Go `renderer_draw.go`/`cull.go`/
`hiz.go`;uniform 布局与 clear 值保持一致,保证同输入同输出。indirect
draw 参数与 Go 路径一致。

### 双后端对照测试落在 `cmd/mornlea`

capture 场景装配(世界、相机、日照)已在 cmd/mornlea;新增 darwin-only
对照测试:同一场景状态分别喂 Go Renderer 与 Rust 渲染器(同 quads、同
可见列表、同 atlas 字节),各自回读后用现有 `diffThreshold` 比较。仅比较
世界地形层(本期 Rust 不渲染实体/文本的场景元素,对照场景选取或裁剪为
纯地形视图,如 materials-showcase 的地形帧与 oak grove)。

## 依赖与并发

- 依赖方向:`internal/client` 仍是 client ABI 唯一接触点;cmd/mornlea 经
  client 包调用渲染绑定;archcheck 白名单不变(client 已在 cmd 依赖内)。
- 线程:渲染器句柄限创建线程(thread-local 表,同窗口);Rust 侧不自建
  线程;wgpu 内部线程不外泄状态。
- 生命周期:destroy 后句柄失效;readback 用完成同步(map + poll)阻塞至
  数据就绪,离屏路径无 surface 时序问题。

## 兼容与回退

- 生产零变化:golden、协议、存档、benchmark 全部不动;平行实现只被测试
  调用。回退:单 PR revert,无生产影响。
- client ABI 1→2:窗口入口签名不变,仅新增 render 入口;Go binary 与
  `libmornlea_client.dylib` 仍为同版本 release unit。

## 验证方法

- Rust crate 单测(darwin,无窗口):shader 单源存在性、frame/upload 输入
  校验拒绝、离屏 smoke(小场景渲染 + 回读非全零)。
- Go 对照测试(darwin):capture 地形场景双后端 `diffThreshold` 内一致;
  帧循环调用计数(每帧 1 次 render FFI)。
- 门禁:`make rust`、focused race、archcheck、全量 race、gofmt/vet、
  openspec strict;既有视觉 golden 测试零改动通过。

## 平台假设

渲染与对照仅在 darwin/Apple Silicon 运行(仓库图形基准平台);Linux CI
只验证 crate 空库编译与专服构建。两后端同为 wgpu-native/Metal,预期图像
差异接近零;若出现系统性偏差,查根因并记录,不放宽 `diffThreshold`。
