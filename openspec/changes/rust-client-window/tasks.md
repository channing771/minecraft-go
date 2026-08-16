# Tasks: rust-client-window

## 1. Rust client crate

- [ ] 1.1 workspace 新增 `engine/crates/mornlea_client`(cdylib,winit +
      raw-window-handle darwin 依赖,Cargo.lock 锁定),实现窗口状态机与
      输入快照模型:键位映射表(29 键 bitmask)、有界 UTF-32 文本队列
      (1024 + overflow)、光标捕获 delta 合成;单测覆盖映射表、队列有界/
      溢出、快照编码布局(无头,不建真窗口);
      验证:`cd engine && cargo test -p mornlea_client && cargo clippy
      --all-targets -- -D warnings`。
- [ ] 1.2 导出 C ABI(`mornlea_client_abi_version`=1、window create/destroy、
      `window_poll` 单次快照、cursor captured/content size/floating/focus/
      cancel close、native NSWindow 查询),入口做 ABI 版本、空指针、缓冲
      长度、句柄有效性与主线程校验,违约返回错误状态且不写调用方缓冲;
      新增 `engine/include/mornlea_client.h`;`make rust` 同时构建两个
      crate;验证:`make rust` 与 crate 单测(校验拒绝路径含测试)。

## 2. Go 切换

- [ ] 2.1 重写 `internal/client/window.go`:cgo 链接 `libmornlea_client`,
      `Poll()` 单次 FFI 快照 + 帧内缓存,`Window` 公共方法集与语义不变,
      错误状态转稳定中文 panic 文案;新增快照解码/缓存语义与
      `DrainTextInput` 有界语义单测(注入固定字节,不开真实窗口);
      验证:`go test ./internal/client -race -count=1`。
- [ ] 2.2 `cmd/gfxspike` 切到 `client.NewWindow`;`go.mod`/`go.sum` 删除
      `go-gl/glfw`;确认生产源码零 GLFW 引用;
      验证:`go build ./... && grep -r go-gl/glfw --include='*.go' .` 无生产
      命中。

## 3. 门禁与收尾

- [ ] 3.1 全仓验证:`go test ./... -race`、`go test ./internal/archcheck
      -count=1`、`go vet ./...`、`gofmt -l .` 无输出、
      `openspec validate --all --strict --no-interactive`。
- [ ] 3.2 人工验收(用户执行):运行 `mornlea` 确认移动/跳跃/快捷栏、聊天
      中文 IME、光标捕获视角连续、Esc 与窗口关闭、调试面板按键;
      验证:用户确认清单通过。
- [ ] 3.3 更新 `docs/notes/progress.md` 与 CLAUDE.md/AGENTS.md(窗口/事件
      循环由 `mornlea_client` 独占、GLFW 移除、R1 完成);
      验证:文档与实现一致。
