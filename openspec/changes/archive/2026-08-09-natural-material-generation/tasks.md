## 2. 用同一纯判断生成四种自然材料

- [x] 2.1 在 `internal/worldgen` 增加 RED 测试，固定 `seed=42` 覆盖沙子、砾石、黏土、雪块的出现与相邻区域、负坐标、区块边界、全 Y 单点/整区块一致性和矿石不覆盖自然材料；运行 `go test ./internal/worldgen -run "NaturalMaterial|BaseBlockAtMatches|Ore" -count=1` 确认失败。
- [x] 2.2 修改 `internal/worldgen/generator.go`，按 design.md 的固定常量和优先级实现共享自然材料纯判断，并仅对最终石头保留原有矿石判断；运行 `go test ./internal/worldgen -race -count=1`。
- [x] 2.3 更新 `internal/worldgen/testdata/golden_seed42.txt` 与相关 worldgen 测试，仅接受批准的生成变化；运行 `go test ./internal/worldgen -run TestGenerateChunkGolden -update -count=1`、`go test ./internal/worldgen -race -count=1`、`gofmt -w internal/worldgen`、`gofmt -l internal/worldgen` 和 `git diff --check`。

## 3. 为 DiskStore 增加确定性只读区块键枚举

- [x] 3.1 在 `internal/storage` 增加 RED 测试，覆盖持久区块键按维度、区块 X、区块 Z 的稳定只读枚举、空世界和非 Overworld 键；运行 `go test ./internal/storage -run "ChunkKeys" -count=1` 确认失败。
- [x] 3.2 修改 `internal/storage` 的具体 `DiskStore`，实现不扩张运行时 `storage.Store` 的确定性只读区块键枚举；运行 `go test ./internal/storage -race -count=1`。
- [x] 3.3 复核枚举不写入区块或改变 revision；运行 `gofmt -w internal/storage`、`gofmt -l internal/storage` 和 `git diff --check`。

## 4. 在世界锁内创建可验证的完整备份

- [x] 4.1 在 `internal/storage` 增加 RED 故障注入测试，覆盖具体 `DiskStore.Backup` 的完整复制与同步、源目录内备份拒绝、symlink/临时文件处理、既有目录拒绝和匹配 `.mcgo-world-backup-v1.json` 的幂等复用；运行 `go test ./internal/storage -run "Backup" -count=1` 确认失败。
- [x] 4.2 修改 `internal/storage` 的具体 `DiskStore`，实现带 `.mcgo-world-backup-v1.json` 身份的外部完整 `Backup`，并保证失败或取消时源世界逐字节不变；不增加迁移、进度或 `worldgen` 依赖，运行 `go test ./internal/storage -race -count=1`。
- [x] 4.3 运行备份故障注入与格式检查：`gofmt -w internal/storage`、`gofmt -l internal/storage` 和 `git diff --check`。

## 5. 实现七种自然值的幂等迁移内核

- [x] 5.1 在 `cmd/mcgod/material_migration_test.go` 增加 RED 纯函数测试，覆盖只重算石头、泥土、草、沙子、砾石、黏土和雪块，保留空气、矿石、其他方块及掉落物、熔炉和箱子负载，并验证输入区块不变和无变化 revision 不递增；运行 `go test ./cmd/mcgod -run "MigrateNatural" -count=1` 确认失败。
- [x] 5.2 在 `cmd/mcgod/material_migration.go` 实现七种自然值纯迁移和离线 runner，由 `cmd/mcgod` 同时依赖 `storage` 与 `worldgen`；runner 使用具体 `DiskStore` 的 `ChunkKeys`、`Backup`、既有世界锁、`SaveBatch` 与 `Sync`，仅在有变化时以 `revision+1` 保存，且不修改 `internal/storage` 的迁移逻辑或依赖；运行 `go test ./cmd/mcgod -race -count=1`。
- [x] 5.3 在 `cmd/mcgod/material_migration.go` 实现 `material-migration-v1.json`：稳定排序后每 `32` 个扫描键成批，每批依次 `SaveBatch`、`Sync`、原子记录 `LastKey`，最终不足 `32` 个键同样记录进度后再标记完成；在 `material_migration_test.go` 故障注入保存、进度写入和完成标记失败，覆盖最终 partial batch 重跑不重复 revision 与完成后幂等，运行 `go test ./cmd/mcgod -race -count=1`、`gofmt -w cmd/mcgod`、`gofmt -l cmd/mcgod` 和 `git diff --check`。

## 6. 接入互斥的 mcgod 离线命令

- [x] 6.1 在 `cmd/mcgod` 入口测试中增加 RED 覆盖：`--migrate-materials` 必须配合 `--backup`、迁移不监听 TCP/不启动服务端、锁冲突失败和同参数续跑；运行 `go test ./cmd/mcgod -run "MigrateMaterials" -count=1` 确认失败。
- [x] 6.2 修改 `cmd/mcgod` 入口，接入 `mcgod --world <世界目录> --migrate-materials --backup <备份目录>` 并调用 `material_migration.go` 的 runner，拒绝与常驻服务启动混用；运行 `go test ./cmd/mcgod -race -count=1`。
- [x] 6.3 使用真实临时磁盘世界执行命令级验证，确认 schema v8、protocol v15、metadata v2 不变；运行 `go test ./cmd/mcgod ./internal/storage -race -count=1`、`gofmt -w cmd/mcgod internal/storage`、`gofmt -l cmd/mcgod internal/storage` 和 `git diff --check`。

## 7. 真实磁盘纵向验证、归档与 PR

- [x] 7.1 在 `cmd/mcgod` 完成真实磁盘纵向迁移测试，并保留 `internal/storage` 的 `ChunkKeys`/`Backup` 专项测试；覆盖完整备份、稳定顺序、七种值迁移、非自然负载保留、保存/进度/完成标记失败续跑、最终 partial batch 与完成后幂等和 future schema 拒绝，运行 `go test ./internal/storage ./cmd/mcgod -race -count=1`。
- [x] 7.2 运行收尾门禁：`go test ./internal/archcheck -count=1`、`go test ./... -race`、`go vet ./...`、`gofmt -l .`、`git diff --check`、`openspec validate natural-material-generation --strict --no-interactive` 和 `openspec validate --all --strict --no-interactive`。
- [x] 7.3 同步 delta spec 到主规格、核对所有任务和验证证据后归档 change，并以独立提交、分支和 PR 交付；在 PR 描述中明确七种自然材料建筑会被强制重算、恢复依赖完整备份且 schema v8/protocol v15/metadata v2 未修改。
