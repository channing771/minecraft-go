## 1. 邻域与传播内核

- [ ] 1.1 在 `internal/world` 为 `Neighborhood` 添加 `[-16,31]` 有界采样及缺失/越界屏障测试；先写失败测试，再运行 `go test ./internal/world -race -count=1`。
- [ ] 1.2 在 `internal/mesh` 实现固定 `48³` 传播 scratch、多源 BFS 和直射/侧向/跨边界/遮挡/未知邻区/队列边界测试；运行 `go test ./internal/mesh -race -count=1` 与相关 benchmark。
- [ ] 1.3 将传播值接入 `Quad.Light` 高四位，保持低四位为 `0` 且稳定任务不分配传播数组或队列；运行 `go test ./internal/mesh -race -count=1`。

## 2. 镜像失效与并发收敛

- [ ] 2.1 在 `internal/client` 扩展普通方块变化的 dirty 范围到最多 `27` 个区段，并添加上限与重复合并测试；运行 `go test ./internal/client -race -count=1`。
- [ ] 2.2 在 `internal/client` 扩展列顶变化的 dirty 范围到最多 `216` 个区段，并测试加载、遗忘、revision 变化与 worker panic 后重排；运行 `go test ./internal/client -race -count=1`。
- [ ] 2.3 添加权威方块变化集成测试，验证封闭洞口后光消失、重开后恢复且派生光不改变区块 hash 或 revision；运行相关 `go test ./internal/client ./internal/server -race -count=1`。

## 3. 无窗口视觉验证

- [ ] 3.1 在现有 capture 场景清单末尾实现 `skylight-tunnel` 固定 `3×3` 夹具、正午相机和收敛检查；为未收敛返回场景名错误编写测试。
- [ ] 3.2 仅用无窗口 `--capture` 路径生成候选图，人工复核全部四张场景后更新 golden；运行 `make visual-check`，不得启动交互式客户端。

## 4. Benchmark 与 M5 基线

- [ ] 4.1 在 benchmark producer 与 `cmd/perfcheck` 升级 scenario v14，只接受 `13:14`，并覆盖完整性、旧迁移拒绝、Memory/TCP parity 和基线选择；运行相关 `go test ./cmd/perfcheck ./internal/... -race -count=1`。
- [ ] 4.2 为完整 `48³` 传播、跨边界输入和稳定 Mesher 任务加入无猜测阈值的微基准；运行相关 `go test -bench` 并确认无逐任务 scratch 分配。
- [ ] 4.3 在冻结候选全量验证与静稳预检通过、获得一次性授权后，以全新路径各执行一次 M5 Memory/TCP v14 正式链；仅在 `13:14` 完整性/绝对门禁和跨 transport 比较均通过后替换 M5 基线，M2 不变。

## 5. 收尾验证与归档准备

- [ ] 5.1 更新受影响中文开发文档，说明协议 v13、玩家 schema v5、区块 schema v6、metadata v2 不变及回退策略；运行 `git diff --check`。
- [ ] 5.2 运行 `gofmt -l .`、`go vet ./...`、`go test ./internal/archcheck -count=1`、`go test ./... -race`、相关 benchmark/perfcheck、`make visual-check` 和 `openspec validate --all --strict --no-interactive`。
