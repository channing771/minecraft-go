## 1. 稳定 ID、物品映射与采掘规则

- [x] 1.1 目标：`internal/core`、`internal/sim`、`internal/physics`。追加 14 组稳定 BlockID/ItemID，增加 RegisteredBlock，并完成 stack/place/drop/mining/完整碰撞测试。
  验证：`go test ./internal/core ./internal/sim ./internal/physics -race -count=1`

## 2. 协议与存档语义版本

- [x] 2.1 目标：`internal/network`、`internal/world`、`internal/storage`。升级协议 v14、玩家 schema v6、区块 schema v7，增加 identity migration、未知方块拒绝和 Memory/TCP round-trip。
  验证：`go test ./internal/network ./internal/world ./internal/storage -race -count=1`

## 3. 缺失玩家材料包

- [x] 3.1 目标：`internal/server`。仅为 ErrPlayerNotFound 玩家生成背包前 14 格各 64 个材料，验证已有玩家、确认前中断和确认后重载。
  验证：`go test ./internal/server -race -count=1`

## 4. 面可见性与遮挡语义

- [x] 4.1 目标：`internal/assets`、`internal/mesh`、`internal/client`、`internal/render`。为 mesh.Registry 增加 FaceVisible，分离 cutout 可绘制面与 Opaque 的 AO/天空光语义。
  验证：`go test ./internal/assets ./internal/mesh ./internal/client ./internal/render -race -count=1`

## 5. 世界 UV 与单 pass cutout

- [x] 5.1 目标：`internal/render`、`internal/assets`、`internal/mesh`。增加 Leaves/Glass layers、世界坐标 UV、fragment discard 和覆盖保持 mip，并锁定 8 字节实例格式。
  验证：`go test ./internal/render ./internal/assets ./internal/mesh -race -count=1`

## 6. 14 种程序化材质与草地闭合

- [x] 6.1 目标：`internal/assets`、`internal/render`。生成固定 16×16 材质、原木/雪分面和 grass wrap，验证确定性、结构、alpha 与周期边界。
  验证：`go test ./internal/assets ./internal/render -race -count=1`

## 7. 无窗口材料展示与性能记录

- [x] 7.1 目标：`cmd/mcgo`、`internal/mesh`、`internal/render`、视觉 golden 与本地性能记录。追加 materials-showcase，更新并逐张复核实际变化的 golden，记录 mesh/render benchmark、quad 和上传量。
  验证：`go test ./cmd/mcgo -race -count=1`；`make visual-update VISUAL_OUT=/private/tmp/common-block-materials-visual`；`make visual-check VISUAL_OUT=/private/tmp/common-block-materials-visual-check`

## 8. 全仓验证、主规格同步与归档

- [ ] 8.1 目标：全仓、`openspec/specs/common-block-materials`、`openspec/specs/voxel-visual-presentation`、`openspec/specs/visual-verification` 与归档 change。完成全仓 race/vet/gofmt/archcheck/diff/OpenSpec strict，智能同步三份主规格，更新 M4N 基线说明并归档 change。
  验证：`go test ./... -race`；`go vet ./...`；`gofmt -l .`；`git diff --check`；`openspec validate --all --strict --no-interactive`
