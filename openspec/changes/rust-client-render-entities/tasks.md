# Tasks: rust-client-render-entities

## 1. 协议与图集

- [x] 1.1 frame v2 协议:Rust 解析器支持 layout v2 的 TLV pass 段(合法/
      未知 tag/长度越界/重复段拒绝,违约不渲染不触碰 target),v1 输入按
      纯地形帧继续接受;单测覆盖解析矩阵;
      验证:`cd engine && cargo test -p mornlea_client && cargo clippy
      --all-targets -- -D warnings`。
- [x] 1.2 图集入口与 ABI 2→3:`render_upload_glyph_rect`(R8 增量矩形,
      界内校验)与 `render_upload_hud_atlas`(一次性 RGBA,长度精确匹配),
      同步 `mornlea_client.h`;拒绝路径单测;验证:`make rust` + crate 单测。

## 2. Rust pass 移植

- [x] 2.1 avatar 与 item drop pass:顶点/索引缓冲、instance 消费与 indexed
      indirect,镜像 Go avatar.go/drop.go 的缓冲布局与管线状态;smoke 单测;
      验证:crate 单测。
- [x] 2.2 block outline 与 damage overlay pass:LessEqual 轮廓与全屏 alpha
      blend 红边;smoke 单测;验证:crate 单测。
- [x] 2.3 name tag、HUD 与 debug panel pass:billboard 字形、屏幕空间顶点
      流与字形/HUD 图集采样,镜像各自 wgsl 的 bind 布局;pass 录制顺序
      严格照 app_frame;smoke 单测;验证:crate 单测。

## 3. Go 装配与绑定

- [x] 3.1 `internal/client` 绑定扩展:frame v2 编码器(pass 段装配)、
      glyph rect 与 HUD atlas 上传、调用计数;`internal/render` 按需最小
      导出既有字节编码;绑定与编码单测;
      验证:`go test ./internal/client ./internal/render -race -count=1`。

## 4. 双后端完整帧门禁

- [ ] 4.1 对照测试升级:含 avatar+名牌+HUD+调试面板的完整场景帧双后端
      `diffThreshold` 内一致(含字形收敛 settle);调用计数断言(每帧
      1 次 render FFI、无变化 0 次上传);
      验证:`go test ./cmd/mornlea -race -count=1 -run DualBackend`。
- [ ] 4.2 既有 golden 与 capture 测试零改动通过;
      验证:`go test ./cmd/mornlea -race -count=1`。

## 5. 收尾

- [ ] 5.1 全量门禁:`gofmt -l .` 无输出、`go vet ./...`、
      `go test ./... -race`、archcheck、
      `openspec validate --all --strict --no-interactive`。
- [ ] 5.2 更新 `docs/notes/progress.md`(R2b 完整帧平行渲染交付);
      验证:文档与实现一致。
