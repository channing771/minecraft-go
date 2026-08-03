## 1. 固定区块掉落物模型

- [x] 1.1 在 `internal/core`、`internal/world` 先写失败测试，覆盖 `DropID` 校验/排序、32 槽上限、同位置合并、最低空槽、generation 复用与耗尽、`Clone` 独立性、容量估算和稳定掉落物哈希。
- [x] 1.2 最小实现 `core.DropID`、固定 `[32]DropSlot` 及区块上的查询/预检/提交方法；保持 `Chunk.Hash` 的方块语义，不新增 slice/map、随机 ID 或通用实体抽象。
- [x] 1.3 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/core ./internal/world -race -count=1 && go test ./internal/archcheck -count=1'`，并确认 `git diff --check` 通过。
- [x] 1.4 只暂存本组代码、测试和本文件勾选，不包含 `midscene_run/` 或其他改动；提交 `feat: 定义有界区块掉落物`。提交成功后自动进入第 2 组。

## 2. 有界协议 v4

- [x] 2.1 在 `internal/network` 先写失败的 packet/registry/codec/golden/fuzz seed 测试，覆盖两种最多 32 项的掉落物消息、严格 ID 顺序、非法字段、零方块 revision barrier、v3 握手拒绝和新的容量拒绝原因。
- [x] 2.2 把 `ProtocolVersion` 升为 4，在既有 packet 表尾追加 `ItemDropUpserts`/`ItemDropRemoves`，显式编码 `DropID` 字段，并在任何切片分配前校验计数和剩余字节；不增加协商或降级路径。
- [x] 2.3 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -race -count=1 && go test ./internal/archcheck -count=1'`，并确认所有 v4 golden 稳定、`git diff --check` 通过。
- [x] 2.4 只暂存本组代码、测试和本文件勾选；提交 `feat: 升级掉落物协议 v4`。提交成功后自动进入第 3 组。

## 3. 区块存档 schema v2

- [x] 3.1 在 `internal/storage` 先写失败测试，覆盖带掉落物的 v2 roundtrip/golden、v1 迁移为空槽、未来版本与非法槽拒绝、故障写入保留旧记录，以及掉落物与方块同记录重启恢复。
- [x] 3.2 扩展 `chunkDTO`、逻辑 codec、migration 和容量估算，以固定 32 槽编码 generation 与活动状态；复用现有 envelope、region 原子提交和 `NeedsRewrite`，不新增文件或事务接口。
- [x] 3.3 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage ./internal/world -race -count=1 && go test ./internal/archcheck -count=1'`，并确认 golden、迁移和故障测试通过，`git diff --check` 通过。
- [x] 3.4 只暂存本组代码、测试和本文件勾选；提交 `feat: 持久化区块掉落物`。提交成功后自动进入第 4 组。

## 4. 权威生成、拾取与过期

- [x] 4.1 在 `internal/sim` 先写失败测试，覆盖挖掘创建/合并且不直接入栏、32 槽满时原子拒绝、满快捷栏仍可挖掘、10 tick 延迟、6000 活动 tick 过期、无人时暂停、部分拾取和多人稳定竞争。
- [x] 4.2 在现有单写者 tick 中增加固定半径 2 的 `dropWanted` 并与区块获取集合取并集；按 `ChunkKey`、`SessionID`、`DropID` 稳定扫描，复用 `Hotbar.Add`，并让纯掉落物变化通过零方块 batch 推进同一 `ChunkRecord.Revision`。
- [x] 4.3 在 `internal/server` 映射新的掉落物容量拒绝；执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim ./internal/server -race -count=1 && go test ./internal/archcheck -count=1'`，并确认 `git diff --check` 通过。
- [x] 4.4 只暂存本组代码、测试和本文件勾选；提交 `feat: 权威生成并拾取掉落物`。提交成功后自动进入第 5 组。

## 5. 服务端兴趣差分发布

- [ ] 5.1 在 `internal/sim`、`internal/server` 先写失败测试，覆盖单会话最多 800 项的有序快照、进入/离开范围、合并/部分拾取更新、remove 先于同槽新 generation upsert、32 项分批、多人隔离和慢连接背压。
- [ ] 5.2 实现可复用目标切片的 `Engine` 掉落物快照查询；在现有 session/publication 中保存已发布 map 与固定 scratch，每 tick 做最多 800 项差分，只有 enqueue 成功才更新镜像。
- [ ] 5.3 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim ./internal/server ./internal/network -race -count=1 && go test ./internal/archcheck -count=1'`，并确认 Memory/TCP 消息序列测试及 `git diff --check` 通过。
- [ ] 5.4 只暂存本组代码、测试和本文件勾选；提交 `feat: 按兴趣发布掉落物`。提交成功后自动进入第 6 组。

## 6. 客户端掉落物镜像

- [ ] 6.1 在 `internal/client` 先写失败测试，覆盖未知 upsert 新增、同 ID 完整替换、未知 remove 拒绝、非法批次整体回滚、800 项容量预检、稳定 presentation 顺序与会话/维度 reset。
- [ ] 6.2 参照现有 `RemotePlayers` 的严格协议错误和 reset 模式，实现独立 `ItemDrops`；预分配 map/scratch，批次先完整验证再应用，不引入插值、预测或网络依赖反向引用。
- [ ] 6.3 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/client -race -count=1 && go test ./internal/archcheck -count=1'`，包含 `testing.AllocsPerRun` 的稳定容量检查，并确认 `git diff --check` 通过。
- [ ] 6.4 只暂存本组代码、测试和本文件勾选；提交 `feat: 维护客户端掉落物镜像`。提交成功后自动进入第 7 组。

## 7. 固定容量呈现与应用接线

- [ ] 7.1 在 `internal/render`、`cmd/mcgo` 先写 headless 失败测试，覆盖最多 800 个 instance、物品颜色映射、由 server tick/ID 得到的稳定动画、消息分派、下一帧更新以及断线/维度 reset；测试不得启动或聚焦游戏窗口。
- [ ] 7.2 复用现有立方体几何、`gfx` buffer 和 presentation conversion，增加固定 800 instance 的掉落物渲染器并接入 `application`；不新增贴图、二进制资源、每实体 GPU 对象或 `cmd/mcgod` 图形依赖。
- [ ] 7.3 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/client ./internal/render ./cmd/mcgo -race -count=1 && go test ./internal/archcheck -count=1'`，确认 800 项提交无持续资源增长且 `git diff --check` 通过。
- [ ] 7.4 只暂存本组代码、测试和本文件勾选；提交 `feat: 渲染可见掉落物`。提交成功后自动进入第 8 组。

## 8. 纵向闭环、文档与最终门禁

- [ ] 8.1 在 `internal/server` 增加 DiskStore 重启与 Memory/TCP 纵向测试：挖掘产生掉落物、部分/竞争拾取、离开再进入兴趣范围、正常刷新重启恢复必须得到相同最终方块、掉落物和快捷栏状态。
- [ ] 8.2 在 `internal/sim`、`internal/network`、`internal/storage`、`internal/server`、`internal/client`、`internal/render` 增加最坏合法 6400 槽扫描、32 项 codec、32 槽存档、800 项差分/镜像/渲染微基准或 allocation 门禁；不得放宽现有阈值。
- [ ] 8.3 更新 `README.md` 与 `docs/notes/lan-server.md`：说明 M4B 操作、协议 v4、区块 schema v2/v1 迁移、备份/回退、固定容量、可信 LAN 边界和未实现范围；文档使用中文。
- [ ] 8.4 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "^$" -fuzz FuzzSmallPacketCodec -fuzztime=10s && go test ./internal/storage -run "^$" -fuzz FuzzDecodeChunkPayload -fuzztime=10s'`，确认无 panic、无无界分配和失败语料。
- [ ] 8.5 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim ./internal/network ./internal/storage ./internal/server ./internal/client ./internal/render -run "^$" -bench "ItemDrop|Chunk(Encode|Decode)|EightPlayerInterest" -benchmem -count=3'`，保存结果并只在实测回退时优化根因，不降低门禁。
- [ ] 8.6 以不聚焦窗口的固定 benchmark 模式生成 `/tmp/mcgo-m4b-current.json`，再用 `cmd/perfcheck` 对 `docs/notes/perf-baseline.json` 做同场景 20% 比较；若运行环境不能保证不聚焦窗口，则跳过该运行并明确记录，绝不改用前台测试或覆盖已接受基线。
- [ ] 8.7 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race && go vet ./... && go test ./internal/archcheck -count=1 && gofmt -l .'`、`openspec validate --all --strict --no-interactive` 和 `git diff --check`；`gofmt -l .` 必须无输出，且 `midscene_run/` 保持未暂存。
- [ ] 8.8 只暂存本组实现、测试、中文文档和本文件勾选；提交 `chore: 关闭 M4B 持久掉落物`。提交成功后停止实现，等待同步规格、归档和推送指令。
