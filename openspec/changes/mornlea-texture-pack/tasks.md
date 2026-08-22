## 2. 实现有界目录材质包 loader

- [ ] 2.1 在 `internal/assets/pack_test.go` 用 `testing/fstest.MapFS` 添加失败测试，覆盖有效单层覆盖、缺失回退、manifest/PNG/尺寸/上限/普通文件校验、未知 manifest 字段告警、固定映射顺序、未知文件不读取以及失败时注册表不变；运行 `go test ./internal/assets -run 'TestApplyPack' -count=1` 确认先失败。
- [ ] 2.2 在 `internal/assets/blocks.go` 添加固定 39 项逻辑名到既有 layer 的内部映射及完整性测试，不暴露新 mutation API。
- [ ] 2.3 在 `internal/assets/pack.go` 仅用标准库实现 v1 manifest 与 PNG 的有界读取、16×16 RGBA 规范化、逐层缺失回退和临时集合验证后的原子应用。
- [ ] 2.4 运行 `gofmt -w internal/assets/pack.go internal/assets/pack_test.go internal/assets/blocks.go`、`go test ./internal/assets -race -count=1` 与 `git diff --check`，通过规格与质量评审后提交 loader。

## 3. 固定来源并内嵌 Pixel Perfection 子集

- [ ] 3.1 在仓库外临时 clone Pixel Perfection，核验 CC BY-SA 4.0，并把上游 `master` 解析为完整 commit SHA；逐项确认 `design.md` 批准的源路径存在，路径不符时先更新设计并取得裁决。
- [ ] 3.2 先在 `internal/assets/default_pack_test.go` 写 provenance 失败测试，要求完整映射、16×16 PNG、SHA-256、无额外 PNG 及四个元数据文件；运行 `go test ./internal/assets -run TestEmbeddedDefaultPackProvenance -count=1` 确认因资产缺失而失败。
- [ ] 3.3 直接复制并重命名批准子集到 `internal/assets/packs/pixel_perfection/`，添加固定 commit 的 `pack.json`、`ATTRIBUTION.md`、完整 `LICENSE.txt` 与逐文件 `PROVENANCE.json`；五个无直接对应 layer 保持程序化，重跑 provenance 测试通过。
- [ ] 3.4 为默认注册表、五层回退、确定性 atlas、用户逐层覆盖与无效用户包失败添加测试，再在 `internal/assets/default_pack.go` 内嵌默认包并实现最小产品构造路径。
- [ ] 3.5 运行 `gofmt -w internal/assets/default_pack.go internal/assets/default_pack_test.go`、`go test ./internal/assets -race -count=1` 与 `git diff --check`，通过规格与质量评审后提交内嵌资产。

## 4. 在配置 v1 增加启动时路径

- [ ] 4.1 在 `internal/config/config_test.go` 添加失败测试，覆盖空默认值、相对/绝对路径解析、非字符串错误、原文 Save 往返、已知顶层字段、数值面板隔离和 `CurrentVersion == 1`；运行 `go test ./internal/config -run 'Test.*TexturePack' -count=1` 确认先失败。
- [ ] 4.2 在 `internal/config/config.go` 添加原文 `texturePackPath` 与不序列化的解析后路径，按配置文件目录解析相对值，不加入 `Fields()`、调试分组或权威参数。
- [ ] 4.3 运行 `gofmt -w internal/config/config.go internal/config/config_test.go`、`go test ./internal/config -race -count=1` 与 `git diff --check`，通过规格与质量评审后提交配置变更。

## 5. 接入全部图形客户端启动模式

- [ ] 5.1 在 `cmd/mornlea` 的 `runWithDependencies` seam 添加失败测试，证明本地与远程模式使用解析后的本地路径，benchmark/capture 即使用户配置非空也得到空路径。
- [ ] 5.2 只把解析后的材质路径加入客户端 `applicationOptions`，复用既有 benchmark/capture 默认配置隔离，不修改专用服务端。
- [ ] 5.3 添加启动副作用顺序失败测试：材质构造返回 sentinel error 时，dial、store、host、window 与 offscreen renderer 均不得被调用；同时覆盖空路径默认包与非空目录覆盖。
- [ ] 5.4 把 registry 构造移到客户端启动最前端，失败时返回带路径上下文的错误，并复用同一 registry 完成 atlas、HUD 和 mesh；不得加入第二套加载路径。
- [ ] 5.5 运行 `gofmt -w cmd/mornlea/app.go cmd/mornlea/app_dependencies.go cmd/mornlea/app_startup.go cmd/mornlea/app_startup_test.go cmd/mornlea/main.go cmd/mornlea/run_test.go`、`go test ./cmd/mornlea -race -count=1`、`go test ./internal/archcheck -count=1` 与 `git diff --check`，通过规格与质量评审后提交启动接线。

## 6. 文档化格式并打包第三方 notices

- [ ] 6.1 先运行 `make build` 并确认 `bin/third-party/pixel-perfection/ATTRIBUTION.md` 尚不存在，保留现有 Makefile 变更。
- [ ] 6.2 新建 `docs/texture-packs.md`，记录 v1 目录、完整逻辑名、16×16 PNG、逐层回退、错误语义、路径解析、启动时/目录限制、不兼容格式及 notices 位置；README 只增加短链接。
- [ ] 6.3 在 macOS 客户端 `build` 产物中逐字节复制 `ATTRIBUTION.md`、`LICENSE.txt` 与 `PROVENANCE.json`，不得加入 Linux 专用服务端 release unit。
- [ ] 6.4 运行 `make build`、三个 `cmp`、`test -z "$(go list -deps ./cmd/mornlea-server | rg 'internal/assets')"` 与 `git diff --check`，通过规格与质量评审后提交文档和打包变更。

## 7. 在 LOD 基线上重建并检查视觉 golden

- [ ] 7.1 读取并测试当前 capture 场景结构，确认不重建或改名 `far-horizon`，其保持倒数第二且 `water-underwater` 保持末尾；运行 `go test ./cmd/mornlea -run 'TestCapture.*Scene|Test.*WaterUnderwater|Test.*Far.*Horizon' -count=1`。
- [ ] 7.2 运行 `make visual-check VISUAL_OUT=build/visual-texture-pack-before-update`，确认只因默认材质像素变化而失败；崩溃、缺场景、mesh、alpha、超时或门禁变化须先修复。
- [ ] 7.3 备份 tracked golden 后以空用户覆盖运行 `make visual-update VISUAL_OUT=build/visual-texture-pack-update` 重建整套基线；更新失败立即恢复备份，不修改阈值或 LOD 近环 guard。
- [ ] 7.4 人工逐图检查至少 `materials-showcase`、HUD/inventory、农业、`water-surface-slope`、末尾 `water-underwater` 与倒数第二 `far-horizon`，把接受范围和裁决写入 ledger。
- [ ] 7.5 运行 `make visual-check VISUAL_OUT=build/visual-texture-pack-final` 与 `git diff --check`，通过规格与质量评审后提交 golden。

## 8. 更新长期基线并完成全量验证

- [ ] 8.1 逐字节同步更新 `AGENTS.md` 与 `CLAUDE.md` 当前能力，更新 `docs/notes/progress.md` 里程碑与 attribution；记录协议 v23、engine ABI v6、client ABI v7、benchmark scenario v18 均未由本变更推进。
- [ ] 8.2 仅在每项实现及规格/质量双评审完成后勾选 `tasks.md`，并在 `ledger.md` 记录 implementer、reviewer、轮次、发现、修复与 controller ruling。
- [ ] 8.3 运行 `gofmt -l .`、`go test ./internal/assets ./internal/config ./cmd/mornlea -race -count=1`、`go test ./internal/archcheck -count=1`、`cmp -s AGENTS.md CLAUDE.md` 与 `git diff --check`。
- [ ] 8.4 运行 `make rust`、`make rust-check`、`go test ./... -race`、`go vet ./...`、`make build`、`make visual-check VISUAL_OUT=build/visual-texture-pack-final-review` 与 `openspec validate --all --strict --no-interactive`，不得放宽性能、overflow、完整性或数据丢失门禁。
- [ ] 8.5 由独立终审者对完整分支核验加载/回退/错误和副作用顺序、有界原子应用、39 名映射、来源许可、自动化与服务端隔离、LOD 后视觉结果及无版本迁移；修复后重跑受影响与全量门禁。
- [ ] 8.6 提交已对账的长期文档与 OpenSpec 状态，确认工作区只剩已知无关改动，并在得到明确批准前不归档 change。
