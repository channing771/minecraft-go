## 1. 修复 HUD 固定缓冲边界

- [x] 1.1 在 `internal/render/hotbar_test.go` 增加 quad 与 glyph 固定区间必须 256 字节对齐且互不重叠的 RED 测试；运行 `go test ./internal/render -run 'TestHotbarBufferRegionsDoNotOverlap' -count=1`，确认测试因当前 `4096` offset 落在最大 quad 区间内而失败。
- [x] 1.2 在 `internal/render/hotbar.go` 把 glyph offset 改为最大 quad 区间末尾向上对齐到 256 字节的编译期常量，保持固定实例上限且不增加每帧分配；运行 `go test ./internal/render -run 'TestHotbarBufferRegionsDoNotOverlap|TestHotbarRenderer' -race -count=1`。

## 2. 展示并命中三条既有配方

- [x] 2.1 在 `internal/render/hotbar_test.go` 和 `cmd/mcgo/app_test.go` 先增加 RED 测试，覆盖三条配方同时绘制、各自独立可用颜色、按钮与背包格互不重叠、三个按钮返回稳定 ID、熔炉 overlay 替换全部配方行、每个可用按钮只发送一次正确 `CraftRecipe`、不可用按钮不发送且客户端镜像不变；运行 `go test ./internal/render ./cmd/mcgo -run 'Recipe|Craft' -count=1`，确认熔炉和铁块入口缺失导致失败。
- [x] 2.2 在 `internal/render/hotbar.go` 增加固定的石砖、熔炉、铁块显示顺序，复用 `core.Recipe`、`Inventory.Craft`、现有实例类型和同一套几何计算垂直绘制三行，并让 `RecipeButtonAt` 返回命中行的 recipe ID；不得增加选择状态、注册器、动态 buffer 或第二套 pipeline。运行 `go test ./internal/render ./cmd/mcgo -run 'Recipe|Craft|FurnaceOverlay' -race -count=1`。

## 3. 同步 M4E 项目事实

- [x] 3.1 更新 `AGENTS.md`、`CLAUDE.md` 和 `openspec/config.yaml` 的当前基线，准确列出 M4E 已交付的权威快捷栏、持久掉落物、36 格背包、固定合成、矿石与共享熔炉，以及协议 v7、玩家 schema v3、区块 schema v4；保留可信 LAN 无认证/加密边界。运行 `rg -n 'M3C|当前实现处于 M4C|协议 v5' AGENTS.md CLAUDE.md openspec/config.yaml`，预期无输出。
- [x] 3.2 最小更新 `README.md` 的操作方式和当前限制，说明普通背包同时显示石砖、熔炉和铁块三条固定配方入口，不声明配方选择、合成网格或其他未实现能力；运行 `rg -n '三条固定配方|仍只显示石砖入口' README.md`，确认新说明存在且旧限制不存在。

## 4. 完整验证

- [x] 4.1 对本变更触及的 Go 文件运行 `gofmt`，再运行 `gofmt -l .`，预期无输出。
- [x] 4.2 运行 `go test ./internal/core ./internal/network ./internal/render ./cmd/mcgo -race -count=1`，验证既有三条配方语义、协议 v7、固定 UI 和客户端权威交互。
- [x] 4.3 运行 `go test ./internal/archcheck -count=1`，确认依赖方向未变化。
- [x] 4.4 运行 `go test ./... -race` 和 `go vet ./...`，不得放宽现有正确性、资源或性能门禁。
- [x] 4.5 运行 `openspec validate --all --strict --no-interactive` 和 `git diff --check`，确认 OpenSpec 产物、实现与文档一致；不得启动或聚焦交互式游戏窗口。
