## Why

仓库目前没有任何日志分级或配置文件设施，调参只能改源码重编译。

**日志现状**：两套并存且都不可控。`internal/server`、`internal/sim`、`internal/client/mesher` 用 `slog.Default()`（约 20 处）；`internal/gfx`、`cmd/mcgo`、`cmd/gfxspike` 用 `log.Printf`（约 22 处）。没有等级开关，没有模块开关。想看 gfx 的细节就得连带吞下所有输出，想静音某个刷屏的子系统只能改代码。

**调参现状**：物理与玩法常量散落在各包的 `const` 里（`internal/physics` 的 `Gravity`、`WalkSpeed`、`JumpSpeed`、`StepHeight` 等；`internal/sim` 的 `interactionReach`、`RegenIntervalTicks`、`DropLifetimeTicks`、`spawnRadius` 等）。调整手感需要改源码 → 重编译 → 重启 → 重新走到测试位置，一轮几分钟，而手感调参本质上需要几十轮。

**配置现状**：完全没有配置文件。`cmd/mcgo` 与 `cmd/mcgod` 只有命令行 flag，全部是启动参数，没有一项是可持久化的偏好。

本变更补上三样东西：一份 JSON 配置文件、一套按模块分级的日志、一个 `--dev` 门控的游戏内实时调参面板。

## What Changes

- 新增 `internal/logging`：一个从记录调用点 PC 反查包路径得到模块名的 `slog.Handler` 包装器，提供全局等级过滤与按模块等级覆盖；现有约 20 处 `slog` 调用点一行不改。
- 把散落的 `log.Printf`（`internal/gfx`、`cmd/mcgo`、`cmd/gfxspike`，约 22 处）统一改为 `slog`，接入同一条可过滤链路；`log.Fatalf` 保持不变。
- 新增 `internal/config`：JSON 配置文件的加载、区间钳制、告警与原子保存；默认路径与 `profile.json` 同目录同格式，`--config` 可覆盖。
- `internal/physics` 与 `internal/sim` 各自新增 `Tunables` 结构体与 `atomic.Pointer[Tunables]` 快照管道（`DefaultTunables` / `SetTunables` / `ActiveTunables`）；原来对应的导出常量降为未导出默认值，唯一读取入口是快照，由 `internal/archcheck` 守住。
- `cmd/mcgo` 新增 `--dev`（门控调试面板）与 `--config` 旗标；`cmd/mcgod` 新增 `--config` 旗标；两者启动时都装配 `internal/logging` 与加载 / 应用 `internal/config`。
- `internal/render` 新增调试面板渲染器：`--dev` 开启后 `F3` 切换显隐，展示帧时、坐标、tick 等基础读数，以及 physics / sim / render 三组可调参数；全部走方向键步进，无文本输入框。
- `internal/archcheck/deps_test.go` 的依赖白名单新增 `internal/logging` 与 `internal/config` 两条，并新增两条守卫测试：可调参数不得再以导出常量暴露；`internal/config` 只能被 `cmd/*` 导入。

**明确不做**：

- 联机时不下发服务端配置。`internal/physics` 的常量同时被客户端预测与服务端权威模拟消费，客户端单方面调整会让预测与权威跑在不同参数上并持续回弹，违反"服务端唯一权威、客户端镜像只读"这条架构边界。因此连接远程服务端时，面板中 physics 与 sim 两组灰显只读并标注"服务端控制"，render 组仍可写。
- `viewDistance` 不做实时调整。它决定客户端向服务端申请的订阅半径，运行中修改需要重新协商订阅、增删区块与重建网格，还牵动按当前视距标定的堆软上限。本次面板中该项只读并标注"重启生效"，仅由配置文件读入；把它做成实时项属于独立变更。
- 以下结构性常量不进配置，因为其取值直接决定线上协议编码宽度、存档 schema 字节布局或预分配缓冲容量，改动即破坏兼容性：`core.InventorySlots`、`core.ChestSlots`、`core.DropsPerChunk`、`core.FurnacesPerChunk`、`core.ChestsPerChunk`、`core.MaxSessionDrops`、`physics.FixedDelta`、`physics.FixedDeltaSeconds`、`physics.PlayerWidth`、`physics.PlayerHeight`、`physics.CollisionEpsilon`、`physics.GroundProbe`、sim 的 tick 间隔与追帧上限、各渲染器实例容量上限。

## Capabilities

### New Capabilities

- `module-scoped-logging`: 按模块分级过滤日志输出的行为契约。
- `tunable-constants`: 物理与模拟常量可由配置文件与调试面板调整的行为契约。

### Modified Capabilities

（无。）

## Impact

- **受影响包**：新增 `internal/logging`、`internal/config`；修改 `internal/physics`、`internal/sim`、`internal/render`、`internal/client`（按键枚举）、`cmd/mcgo`、`cmd/mcgod`、`cmd/gfxspike`、`internal/gfx`、`internal/archcheck`。
- **协议 / 存档**：零改动。本变更不触及 `internal/network` 或存档 schema。
- **并发**：面板运行在主 / 渲染 goroutine，权威 tick 运行在另一条 goroutine；两者通过 `atomic.Pointer[Tunables]` 整体替换通信，读取方在函数或 tick 入口取一次快照后全程使用局部值，写入方只做一次指针交换，无锁无数据竞争，须以 `-race` 验证。
- **性能**：物理固定步（20Hz）与模拟 tick 各增加一次原子读，热路径开销需以 benchmark 证明不可测量；`cmd/perfcheck` 基线比对与视觉抓帧路径强制使用编译默认值，不得读用户配置文件，避免本机配置污染基线结论。
- **兼容性**：无配置文件时，物理与模拟的生效参数与本变更之前的编译常量逐字段相等，默认行为不变；配置文件字段缺失取默认、越界钳制并告警、未知字段忽略并告警、JSON 语法错误或不认识的 `version` 报错退出。
- **依赖方向**：`internal/config` 只允许被 `cmd/*` 导入，`internal/*` 不得导入它；`internal/config` 不依赖 `internal/render`，渲染组参数以纯数据结构返回，避免 `cmd/mcgod` 传递性拖入图形依赖。
