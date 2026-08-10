## 1. RED 回归

- [x] 1.1 在 `internal/storage/chunk_container_height_test.go` 增加非零垂直 section 的合法 Furnace/Chest 存档往返 RED 回归，断言方块索引、内容和方块均无损；运行 `go test ./internal/storage -run TestChunkCodecRoundTripsContainersAcrossWorldSections -count=1`，确认失败原因是 section-local Y 误判。

## 2. 两行 GREEN 修复

- [x] 2.1 在 `internal/storage/chunk_codec.go` 的 Furnace 与 Chest 既有校验各以还原位置的完整世界 Y 调用 `Chunk.BlockAt`，不改 schema、protocol 或字节布局；运行 `go test ./internal/storage -run 'TestChunkCodecRoundTripsContainersAcrossWorldSections|TestChunkCodecRejectsInvalid(Furnace|Chest)Slots' -race -count=1`。

## 3. Mutation

- [x] 3.1 在 `internal/storage/chunk_furnace_test.go` 与 `internal/storage/chunk_chest_test.go` mutation 覆盖越界、重复和错误 Furnace/Chest 方块索引，以及非法 Furnace/Chest 固定槽（包括非法 active 标志或不符合 `Valid()` 的停用槽），断言 codec 返回 `ErrCorrupt` 且不接受或改写记录；临时恢复 local-Y 读取后运行 `go test ./internal/storage -run TestChunkCodecRoundTripsContainersAcrossWorldSections -race -count=1` 确认失败，恢复世界 Y 后确认通过。

## 4. Storage/full gates

- [x] 4.1 对 `internal/storage` 与全仓执行门禁：`zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -race -count=1 && go test ./internal/archcheck -count=1 && go test ./... -race && go vet ./...'`，随后运行 `gofmt -l .`、`openspec validate fix-container-world-y --strict --no-interactive`、`openspec validate --all --strict --no-interactive` 与 `git diff --check`；所有命令必须成功且 `gofmt -l .` 无输出。

## 5. 同步归档

- [x] 5.1 在全部任务完成和门禁通过后，将 `authoritative-furnaces` 与 `authoritative-chests` delta 同步至主规格并以 `openspec archive fix-container-world-y --yes` 归档；再次运行 `openspec validate --all --strict --no-interactive`、`git diff --check` 与 `git status --short`，仅提交归档结果。
