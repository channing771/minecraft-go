# Tasks: rust-client-render-cutover

## 1. Rust surface 模式

- [x] 1.1 client ABI 3→4:`render_create_windowed`(窗口句柄→wgpu surface,
      Bgra8UnormSrgb/FIFO,窗口线程表)与 `render_resize`(重建 target/HiZ
      与 surface 配置);`render_frame` 窗口模式 acquire/present,失败返回
      SKIPPED 状态;入口校验拒绝单测;同步 `mornlea_client.h`;
      验证:`cd engine && cargo test -p mornlea_client && cargo clippy
      --all-targets -- -D warnings && make -C .. rust`。

## 2. Go 切换段(Go 渲染栈保留为死代码)

- [ ] 2.1 `GlyphAtlas` 改收 `GlyphSink` 接口(tofu 与 FlushUploads 写入走
      sink),生产 sink 适配 `client.Renderer.UploadGlyphRect`;HUD 图集经
      `UploadHUDAtlas` 上传;既有字形语义测试保持;
      验证:`go test ./internal/render ./internal/render/hud -race -count=1`。
- [ ] 2.2 `cmd/mornlea` 帧循环切换:app 装配 RenderFrame(相机/可见列表/
      pass 段)+ 每帧一次 RenderFrame,窗口模式接 windowed 渲染器,resize
      接 render_resize;capture/materials-showcase/ai-companion/benchmark/
      gfxspike 切离屏 `client.Renderer`;
      验证:`go build ./... && go test ./cmd/mornlea -race -count=1`
      (全部 golden 零改动通过——核心门禁)。

## 3. 删除段

- [ ] 3.1 删除 `internal/gfx`、`oliverbestmann/webgpu` 依赖、render/hud 包
      GPU 半部(pipeline/pool/cull/hiz 与各 renderer 的 GPU 路径)、
      `assets.UploadTo`、双后端对照测试;CPU 半部保留并收敛;
      验证:`go build ./... && go test ./internal/render
      ./internal/render/hud ./internal/assets ./cmd/mornlea -race -count=1`。
- [ ] 3.2 archcheck 与文档:白名单删 gfx、webgpu 全仓禁止、依赖表更新;
      CLAUDE.md/AGENTS.md 项目定位与 `docs/notes/progress.md` 改写;
      验证:`go test ./internal/archcheck -count=1`。

## 4. 收尾

- [ ] 4.1 全量门禁:`gofmt -l .` 无输出、`go vet ./...`、
      `go test ./... -race`、`openspec validate --all --strict
      --no-interactive`;benchmark/perfcheck 数值记录。
- [ ] 4.2 真实窗口自动化验收(CGEvent 注入,复用 R1 工具):启动截图内容
      比对、SetContentSize 后画面正确、关闭路径干净退出;
      验证:截图与退出码。
