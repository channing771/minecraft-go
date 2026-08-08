## 1. 程序化方块材质

- [x] 1.1 在 `internal/assets/procedural_test.go` 增加石砖错缝、矿石簇、熔炉边框、铁块面板和箱子木板结构的失败测试；运行 `go test ./internal/assets -race -count=1` 确认 red。
- [x] 1.2 在 `internal/assets` 复用现有 16×16 RGBA 与确定性 hash 生成最小结构图案；运行 `go test ./internal/assets -race -count=1` 确认 green。

## 2. 统一 HUD 样式与尺寸适配

- [x] 2.1 在 `internal/render/hotbar_test.go` 增加面板层级、双层物品色块、合成状态、分段生命值、640×360 边界及缩放命中一致性的失败测试；运行 `go test ./internal/render -race -count=1` 确认 red。
- [x] 2.2 在 `internal/render/hotbar.go` 复用现有 quad、颜色与布局函数实现统一样式、分段生命条和固定统一缩放，并重算固定容量；运行 `go test ./internal/render -race -count=1` 确认 green 与预热零分配。

## 3. 无窗口视觉基线

- [x] 3.1 用既有显式无窗口抓帧路径更新三个共享地形背景的场景，查看三张 PNG 并确认 `avatar-nametag` 只改变地形背景；运行项目现有 visual check 命令确认双阈值通过。

## 4. 收尾验证

- [x] 4.1 对修改的 Go 文件运行 `gofmt`，执行 `go test ./internal/assets ./internal/render -race -count=1`、`go test ./internal/archcheck -count=1` 与 `git diff --check`。
- [x] 4.2 执行 `go test ./... -race`、`go vet ./...`、`gofmt -l .` 和 `openspec validate --all --strict --no-interactive`，不得放宽既有门禁。

## 5. 视觉反馈修订

- [x] 5.1 为草顶面/侧缘、真实方块缩略图、背包分组面板和每颗两点的爱心栏增加失败测试；运行 `go test ./internal/assets ./internal/render -race -count=1` 确认 red。
- [x] 5.2 在现有程序化材质、HUD quad pipeline 与应用构造链完成最小实现；运行受影响包 race 测试确认 green 与预热零分配。
- [x] 5.3 追加 `inventory-crafting` 无窗口场景，更新并查看四张受影响 PNG，运行既有 visual check 确认双阈值通过。

## 6. 修订收尾验证

- [x] 6.1 执行 `gofmt`、受影响包 race 测试、archcheck、`go test ./... -race`、`go vet ./...`、`gofmt -l .`、`git diff --check` 与严格 OpenSpec 验证。

## 7. 左下生命栏与数量数字反馈

- [x] 7.1 为左下固定锚点、无背景爱心、打开背包不缩放，以及隐藏单件数量、数字阴影/前景层级增加失败测试；运行 `go test ./internal/render ./cmd/mcgo -race -count=1` 确认 red。
- [x] 7.2 在现有 `appendHealthBar` 与 `appendHotbarCountScaled` 完成最小实现并重算固定容量；运行受影响包 race 测试确认 green 与预热零分配。
- [x] 7.3 更新并查看四张无窗口视觉 golden，运行既有 visual check 确认双阈值通过。

## 8. 本轮收尾验证

- [x] 8.1 执行 `gofmt`、受影响包 race 测试、archcheck、`go test ./... -race`、`go vet ./...`、`gofmt -l .`、`git diff --check` 与严格 OpenSpec 验证。

## 9. 两位数量间距反馈

- [x] 9.1 为两位数量收紧 2px tracking、保持最右数字锚点及阴影/前景同步增加失败测试；运行 `go test ./internal/render -race -count=1` 确认 red。
- [x] 9.2 在现有 `appendHotbarCountScaled` 加入最小 tracking 调整；运行受影响包 race 测试确认 green 与预热零分配。
- [x] 9.3 更新并查看四张无窗口视觉 golden，运行既有 visual check 确认双阈值通过。

## 10. 本轮收尾验证

- [x] 10.1 执行 `gofmt`、受影响包 race 测试、archcheck、`go test ./... -race`、`go vet ./...`、`gofmt -l .`、`git diff --check` 与严格 OpenSpec 验证。
