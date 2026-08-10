## 1. OpenSpec 契约

- [x] 1.1 在 `proposal.md`、`design.md` 与两个 delta spec 中固定本地目标、十二边深度轮廓、中文名称、7+1 名牌容量、末尾无窗口场景及兼容性边界；验证：`openspec validate target-block-feedback --strict --no-interactive`。

## 2. 中文名称与本地目标选择

- [x] 2.1 在 `internal/core/block_name.go` 与 `internal/core/block_name_test.go` 增加稳定的注册 `BlockID` 中文名查询，覆盖全部已注册 ID 的非空结果和未知 ID 失败；验证：`go test ./internal/core -race -count=1`。
- [x] 2.2 在 `cmd/mcgo/target_block.go` 与 `cmd/mcgo/target_block_test.go` 从相机、只读镜像和 `core.RaycastBlocks` 派生六格本地目标，覆盖完整已加载路径、缺失或 desynced 区块、空气/未注册/超距、未 ready 与 UI 的查询边界；验证：`go test ./cmd/mcgo -race -count=1`。

## 3. 固定十二边深度轮廓 renderer

- [x] 3.1 在 `internal/render/block_outline.go` 与 `internal/render/block_outline_test.go` 实现容量恰为十二的方块边轮廓，固定 expand `0.003`、width `0.018`、alpha `0.86`，并复用现有立方体资源、实例编码和相机 uniform；几何测试明确断言 bounds `position-0.003..position+1.003`、长边 `1.006`、两个横截面轴 `0.018` 与 alpha `0.86`；验证：`go test ./internal/render -race -count=1`。
- [x] 3.2 在同一 renderer 测试中锁定 alpha 混合、`CompareLessEqual` 深度测试且不写 depth、被地形遮挡、固定十二实例、dynamic upload/overflow 结构与幂等释放；在 `internal/gfx/gfx.go`、`internal/gfx/wgpu.go` 与 `internal/gfx/wgpu_test.go` 增加最小 opt-in 选择与 WebGPU 映射，其他 pipeline 的零值仍映射为 `Less`；验证：`go test ./internal/gfx ./internal/render -race -count=1`。

## 4. 正常帧接线

- [x] 4.1 修改 `cmd/mcgo/app.go`、相关 `cmd/mcgo/*_test.go` 与必要的 `internal/render` name-tag 容量接线，使每帧在应用服务端消息后计算本地目标，断线或 reset 的当帧清空已呈现目标，并将 name-tag 容量固定为七名远端玩家加一个目标名称；验证：`go test ./cmd/mcgo -race -count=1`。
- [x] 4.2 将 render 顺序固定为 terrain → avatar → item drops → block outline → name tags → damage overlay → HUD → debug panel，并证明轮廓共享本帧 `viewProj/daylight/depth`，目标不存在时不提交空名牌；验证：`go test ./cmd/mcgo -race -count=1`。
- [x] 4.3 在 `cmd/mcgo` 与 `internal/render` 相关测试中以“一个有效目标 + 七名远端玩家”为固定输入，预热一次后用 `AllocsPerRun` 包住 current target 更新、outline prepare、NameTag prepare/上传整条稳定路径并断言分配为 `0`，同时继续锁定既有 dynamic upload/overflow 结构且不新增抽象；验证：`go test ./cmd/mcgo ./internal/render -race -count=1`。
- [x] 4.4 确认服务端命令、Predictor、网络消息和存档均不读取或保存本地目标；验证：`go test ./internal/archcheck -count=1` 与 `go test ./cmd/mcgo -race -count=1`。

## 5. 无窗口目标反馈场景

- [ ] 5.1 修改 `cmd/mcgo/capture.go` 与 `cmd/mcgo/capture_test.go`，在场景表末尾注册 `target-block-feedback`；夹具固定正午、相机 `{0.5, 3.5, 2.5}`、Yaw/Pitch `0`，只在 `{X: 0, Y: 3, Z: -3}` 放置 `BrickID`，并断言当前目标为该砖块及名称“砖块”；验证：`go test ./cmd/mcgo -race -count=1`。
- [ ] 5.2 生成 `cmd/mcgo/testdata/golden/target-block-feedback.png`，仅更新正常目标提示实际改变的既有 golden，逐张复核完整场景且保持 `inventory-crafting.png` 逐字节不变；验证：`zsh -ic 'make visual-update VISUAL_OUT=/private/tmp/target-block-feedback-visual-update'`、`test -f cmd/mcgo/testdata/golden/target-block-feedback.png`、`test -z "$(git diff --name-only -- cmd/mcgo/testdata/golden/inventory-crafting.png)"` 和 `zsh -ic 'make visual-check VISUAL_OUT=/private/tmp/target-block-feedback-visual-check'`。

## 6. 收尾、同步与归档

- [ ] 6.1 回读 `proposal.md`、两个 delta spec、`design.md` 与本文件，完成每项实现后勾选对应任务；验证：`openspec validate target-block-feedback --strict --no-interactive`。
- [ ] 6.2 同步 `voxel-visual-presentation` 与 `visual-verification` 主规格并归档本 change，不修改其他 change；验证：`openspec validate --all --strict --no-interactive`、`openspec archive target-block-feedback --yes` 与 `test -z "$(openspec list --json | grep target-block-feedback || true)"`。
- [ ] 6.3 完成收尾质量门禁；验证：`gofmt -l .`、`go test ./internal/archcheck -count=1`、`go test ./... -race`、`go vet ./...`、`git diff --check`、`openspec validate --all --strict --no-interactive` 和无窗口 `make visual-check`。
