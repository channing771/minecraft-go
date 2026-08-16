# Tasks: rust-client-render-terrain

## 1. Rust 渲染器基建

- [x] 1.1 `mornlea_client` 新增 wgpu 依赖与 render 模块骨架:device/queue
      初始化、离屏 color(BGRA8UnormSrgb,对齐 Go capture)+depth(Depth32Float)target、shader `include_str!` 单源
      内嵌(terrain/sky/cull/hiz_build/hiz_copy)与存在性单测;
      验证:`cd engine && cargo test -p mornlea_client && cargo clippy
      --all-targets -- -D warnings`。
- [x] 1.2 terrain pass 移植:atlas layer 纹理与 sampler、section 存储
      (紧凑 Quad 缓冲 + origin/record 编码镜像 Go sectionRecordBytes)、
      实例化绘制与 uniform 布局对齐 Go;小场景离屏 smoke 单测(回读非全零、
      同输入两次渲染逐字节一致);验证:crate 单测。
- [x] 1.3 GPU culling + HiZ 移植:cull compute dispatch、indirect draw、
      hiz_build/hiz_copy mip 链,顺序与参数镜像 Go `cull.go`/`hiz.go`;
      遮挡场景单测(剔除后绘制计数下降且图像稳定);验证:crate 单测。
- [x] 1.4 sky/云 pass 移植与日照 uniform;昼夜两个时间点的 smoke 单测;
      验证:crate 单测。

## 2. ABI 与 Go 绑定

- [x] 2.1 client ABI 1→2:导出 render create/destroy、upload_atlas、
      upload_section、drop_section、frame、readback,全部入口校验
      (版本/指针/长度/句柄/quad 对齐/列表越界)违约不触碰缓冲,含拒绝
      路径单测;同步 `mornlea_client.h`;验证:`make rust` + crate 单测。
- [x] 2.2 `internal/client` 新增 render 绑定(错误状态转稳定中文文案)与
      `assets.Registry` layer 像素导出;绑定层拒绝注入测试;
      验证:`go test ./internal/client ./internal/assets -race -count=1`。

## 3. 双后端对照门禁

- [x] 3.1 `cmd/mornlea` 新增 darwin 对照测试:capture 地形场景(至少
      materials-showcase 地形帧与 oak grove)同数据驱动双后端,回读经
      `diffThreshold` 双阈值比较;帧循环调用计数断言(每帧 1 次 render
      FFI、无变化不 upload);验证:`go test ./cmd/mornlea -race -count=1
      -run 'DualBackend|Capture'`。
- [x] 3.2 既有视觉 golden 与 capture 测试零改动通过,确认生产路径未变;
      验证:`go test ./cmd/mornlea -race -count=1`。

## 4. 收尾

- [x] 4.1 全量门禁:`gofmt -l .` 无输出、`go vet ./...`、
      `go test ./... -race`、archcheck、
      `openspec validate --all --strict --no-interactive`。
- [x] 4.2 更新 `docs/notes/progress.md`(R2a 平行渲染器与对照门禁交付,
      生产仍为 Go 渲染器);验证:文档与实现一致。
