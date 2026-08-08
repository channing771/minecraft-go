## 1. 邻域与传播内核

- [x] 1.1 在 `internal/world` 先写 `Neighborhood` 的 `[-16,31]`、缺失和越界失败测试，再最小实现采样并重构；运行 `go test ./internal/world -race -count=1`，通过后只提交本任务文件。
- [x] 1.2 在 `internal/mesh` 先写固定 `48³` scratch、多源 BFS、直射/侧向/跨边界/遮挡/未知邻区/队列边界失败测试，再最小实现并重构；运行 `go test ./internal/mesh -race -count=1` 与相关 benchmark，通过后只提交本任务文件。
- [x] 1.3 先为 `Quad.Light` 高四位、低四位 `0` 和稳定任务无传播 scratch 分配写失败测试，再最小接入并重构；运行 `go test ./internal/mesh -race -count=1`，通过后只提交本任务文件。

## 2. 镜像失效与并发收敛

- [ ] 2.1 在 `internal/client` 先写普通方块变化 dirty `27` 上限和重复合并失败测试，再最小扩展范围并重构；运行 `go test ./internal/client -race -count=1`，通过后只提交本任务文件。
- [ ] 2.2 在 `internal/client` 先写列顶变化 dirty `216`、加载、遗忘、revision 变化和 worker panic 重排失败测试，再最小扩展范围并重构；运行 `go test ./internal/client -race -count=1`，通过后只提交本任务文件。
- [ ] 2.3 先写封闭洞口后光消失、重开恢复及派生光不改变区块 hash/revision 的失败集成测试，再最小接通权威变化并重构；运行 `go test ./internal/client ./internal/server -race -count=1`，通过后只提交本任务文件。

## 3. 无窗口视觉验证

- [ ] 3.1 先写 `skylight-tunnel` 场景顺序、固定 `3×3` 夹具、正午相机和未收敛场景名错误的失败测试，再最小实现并重构；运行对应 capture 测试，通过后只提交本任务文件。
- [ ] 3.2 先以旧 golden 运行无窗口 `--capture` 取得预期视觉失败，再最小更新候选图并人工复核全部四张场景；运行 `make visual-check`，不得启动交互式客户端，通过后只提交本任务文件。

## 4. Benchmark 与 M5 基线

- [ ] 4.1 先写 scenario v14、唯一 `13:14`、旧迁移拒绝、Memory/TCP parity 和基线选择的失败测试，再最小升级 producer 与 `cmd/perfcheck` 并重构；运行相关 `go test ./cmd/perfcheck ./internal/... -race -count=1`，通过后只提交本任务文件。
- [ ] 4.2 先写完整 `48³` 传播、跨边界输入和稳定 Mesher 无逐任务 scratch 分配的失败测试，再最小加入无猜测阈值的微基准并重构；运行相关 `go test -bench`，通过后只提交本任务文件。
- [ ] 4.3 先验证未通过静稳预检或缺少绑定精确 HEAD 的一次性授权时正式链被拒绝；在冻结候选全量验证通过后，以全新路径各执行一次 M5 Memory/TCP v14 producer，并在 `13:14` 完整性/绝对门禁和跨 transport 比较通过后替换 M5 基线、记录证据；通过后只提交本任务文件，M2 不变。

## 5. 收尾验证与归档准备

- [ ] 5.1 先为兼容性说明缺失写文档检查，再最小更新受影响中文开发文档并复核协议 v13、玩家 schema v5、区块 schema v6、metadata v2 不变及回退策略；运行 `git diff --check`，通过后只提交本任务文件。
- [ ] 5.2 先运行全量门禁以暴露剩余失败，再最小修复并重构；运行 `gofmt -l .`、`go vet ./...`、`go test ./internal/archcheck -count=1`、`go test ./... -race`、相关 benchmark/perfcheck、`make visual-check` 和 `openspec validate --all --strict --no-interactive`，全部通过后只提交收尾任务文件。
