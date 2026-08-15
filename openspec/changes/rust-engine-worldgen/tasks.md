# Tasks: rust-engine-worldgen

## 1. Go oracle 提取

- [x] 1.1 把 `internal/worldgen` 的噪声/地形/矿石/橡树计算复制为 `oracle_test.go`
      内独立 oracle 实现,现有测试改为断言生产 API 与 oracle 一致;
      验证:`go test ./internal/worldgen -race -count=1`(此时生产仍为旧实现,
      差分应全部通过)。

## 2. Rust worldgen 内核

- [x] 2.1 engine 新增 `worldgen.rs`:perlin/fbm(逐条镜像 Go 运算顺序,禁
      `mul_add`)、地表分层、oreHash、橡树候选/树形/合并;单元测试覆盖
      固定语料;验证:`make rust`(含 cargo test)。
- [ ] 2.2 `ffi.rs` 导出 `mornlea_worldgen_chunk` 与 `mornlea_worldgen_probe`
      (magic `MGW1`、header 校验:min_y/max_y、perm<256、材料表无 air 冲突、
      缓冲长度;违约返回 StatusInput 且不写输出),engine ABI version 2→3,
      同步 `engine/include` 头文件;验证:`make rust`。

## 3. Go 绑定与生产切换

- [ ] 3.1 `internal/nativeabi` 新增 WorldgenChunk/WorldgenProbe 绑定与请求编码,
      含非法输入注入测试(输出缓冲不被修改、稳定中文错误);
      验证:`go test ./internal/nativeabi -race -count=1`。
- [ ] 3.2 `internal/worldgen` 生产路径切换:`New` 保留 perm 播种并预编码 header,
      `GenerateChunk` 一次 native 调用 + 仅非 air 回写 + `Compact`,
      `HeightAt`/`TerrainBlockAt`/`BaseBlockAt` 走 64-record batch probe;
      删除生产文件中的旧计算逻辑;`internal/archcheck` 登记
      worldgen→nativeabi 依赖边;
      验证:`go test ./internal/worldgen ./internal/archcheck -race -count=1`。

## 4. 差分与一致性门禁

- [ ] 4.1 新增差分 fuzz/语料测试:随机种子×区块 dense 逐位对比 oracle、
      probe 与 chunk 交叉一致、跨区块橡树拼合一致;
      验证:`go test ./internal/worldgen -race -count=1`。
- [ ] 4.2 运行既有矿石/橡树/自然材料行为测试与相关 golden,确认零改动通过;
      验证:`go test ./internal/worldgen ./internal/server -race -count=1`。

## 5. 收尾

- [ ] 5.1 基准记录:运行区块生成相关 benchmark 与 `cmd/perfcheck`,数值只记录;
      验证:命令完成且报告完整。
- [ ] 5.2 全量门禁:`gofmt -l .` 无输出、`go vet ./...`、
      `go test ./... -race`、`openspec validate --all --strict --no-interactive`。
- [ ] 5.3 更新 `docs/notes/progress.md` 与 CLAUDE.md/AGENTS.md 项目定位段
      (worldgen 加入 Rust 独占内核清单)。
