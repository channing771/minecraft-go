> 完整设计记录、被否决的替代方案与理由见
> `docs/superpowers/specs/2026-08-08-logging-and-debug-tuning-design.md`。
> 完整实施步骤见 `docs/superpowers/plans/2026-08-08-logging-and-debug-tuning.md`。
> 本文件只记录实现选择。

## `--dev` 只门控面板，配置文件始终生效

`cmd/mcgo` 的 `--dev` 旗标只控制调试面板是否可用，不控制配置文件是否被读取——配置文件里调过的值在不开 `--dev` 时同样生效。代价是游戏行为不再仅由代码决定，而是由代码加本机配置文件共同决定；为不让这个代价蔓延到自动化验证，性能门禁与抓帧路径强制使用编译默认值（见下）。

## 联机时物理与模拟组只读

`internal/physics` 的常量同时被客户端预测与服务端权威模拟消费。客户端单方面调整会让预测与权威跑在不同参数上，位置持续回弹，违反"服务端唯一权威、客户端镜像只读"这条架构边界。因此单机（内置服务端，客户端与模拟同进程）下面板可写 physics 与 sim 组；连远程服务端时这两组灰显只读并标注"服务端控制"，render 组仍可写。

**被否决的替代方案**：服务端下发配置 + 客户端请求修改。能让联机也可调，但需要协议版本升级、新增消息类型与权限模型，工作量可能超过本次其余全部内容之和；只下发不可写同样需要动协议，收益不足。

## 每包自持 atomic 快照，而非集中式 tunables 包

面板从主 / 渲染 goroutine 写值，服务端权威 tick 在另一条 goroutine 读值，普通包级 `var` 在这里是真实数据竞争。采用的方案：`internal/physics` 与 `internal/sim` 各自持有一个 `atomic.Pointer[Tunables]`，读取方在函数或 tick 入口取一次快照存入局部变量后全程使用局部值；写入方只做一次指针交换。物理固定步 20Hz、模拟每 tick 一次，各一次原子读，热路径开销可忽略。

**被否决的替代方案**：

- 新建 `internal/tunables` 叶子包集中所有常量。好处是默认值与 JSON schema 同源，但会把参数从其所属领域包（如 `physics.Gravity`）搬到一个与领域无关的包，破坏包的自然归属，且 archcheck 白名单里几乎每个包都要新增依赖，收益不抵搬迁成本。
- 纯参数注入、零全局。最干净，但 `sim` 引擎跑在独立 goroutine 上，面板改值需要新造一条命令通道进引擎；`physics.Step` 改签名还会波及大量既有测试。

## 可调常量一律降为未导出默认值

凡是进入 `Tunables` 的常量，其原有导出常量降级为未导出默认值，唯一读取入口是快照。这不是洁癖：例如 `physics.EyeHeight` 目前有权威侧与客户端相机两类读取点，若该常量变成可调项而导出常量仍在，只要有一处漏改，相机视线高度与服务端射线原点就会静默错位——玩家瞄准的方块和服务端判定的方块不是同一个。用 `internal/archcheck` 的守卫测试守住这条不变量，防止后续回退。

## 自动化验证不得读用户配置

`internal/config` 只允许被 `cmd/*` 导入，`internal/*` 一律不得导入它，由 `internal/archcheck` 守住。`cmd/mcgo` 的 `--benchmark` 与 `--capture-dir` 路径强制使用编译默认值，忽略配置文件。否则一台机器上的本地调参会污染性能基线比对和 golden 截图，让 `cmd/perfcheck` 与视觉验证的结论依赖于开发者本机的配置文件内容。

## 日志模块名从调用点 PC 反查，而非改调用点

`internal/logging` 提供一个 `slog.Handler` 包装器，由 `cmd/mcgo` 与 `cmd/mcgod` 启动时通过 `slog.SetDefault` 装上。`Enabled()` 用所有模块中的最低等级做快速门：全部为 info 时，`slog.Debug` 调用被零成本丢弃。只有记录通过快速门后，`Handle()` 才做一次 `runtime.CallersFrames` 从 `Record.PC` 反查包路径，映射为模块名，再按该模块的等级精确过滤。好处是现有约 20 处 `slog` 调用一行不用改，将来新写的日志也自动归入正确模块；代价是模块名与包路径绑定，无法做出与包结构不同的分组。

**被否决的替代方案**：每个调用点显式带 `module` 属性。`slog.Handler.Enabled()` 拿不到属性，过滤只能在 `Handle()` 里做，既没省下反查成本，又要改几十处调用点。

## 可调值管道

`internal/physics` 与 `internal/sim` 各新增：

```go
type Tunables struct { /* 各组可调字段 */ }

func DefaultTunables() Tunables    // 逐字段等于原常量值
func SetTunables(Tunables)         // 只由 cmd 启动装配与调试面板调用
func ActiveTunables() Tunables     // 内部 atomic.Pointer 快照
```

`physics.Step` 在函数首行取一次快照存入局部变量，函数体内所有相关常量读取改为读该局部变量的字段。`sim` 在每 tick 起始处取一次快照，同 tick 内所有判定共用该快照——这同时保证了单个 tick 内参数不会中途变化，模拟仍是确定性的。调用方签名全部不变，既有测试无需改动。

### 明确排除的常量

以下为结构性常量，取值直接决定线上协议编码宽度、存档 schema 中的字节布局或预分配缓冲容量，改动即破坏兼容性，一律不进配置：`core.InventorySlots`、`core.ChestSlots`、`core.DropsPerChunk`、`core.FurnacesPerChunk`、`core.ChestsPerChunk`、`core.MaxSessionDrops`、`physics.FixedDelta` 与 `physics.FixedDeltaSeconds`、`physics.PlayerWidth`、`physics.PlayerHeight`、`physics.CollisionEpsilon`、`physics.GroundProbe`、`sim` 的 tick 间隔与追帧上限、各渲染器的实例容量上限。

## 依赖方向

`internal/archcheck/deps_test.go` 的 `allowed` 表新增两条：

```go
"internal/logging": {},
"internal/config":  {"internal/core", "internal/physics", "internal/sim", "internal/logging"},
```

`internal/config` 刻意不依赖 `internal/render`：渲染组的值由它以纯数据结构返回，`cmd/mcgo` 读出后作为普通参数传给 app 与渲染器。这样 `cmd/mcgod` 导入 `config` 也不会传递性地拖入图形依赖，既有的 `TestMCGodHasNoGraphicsDependencies` 门禁无需放宽。渲染组的值只在主 goroutine 上被读写（面板与渲染同线程），因此不需要原子快照。

## 风险与回退

**主要风险**：可调常量降为未导出这一步波及面广，一次改完难以定位回归。**缓解**：分两步落地——先建立 `Tunables` 管道并证明默认值行为逐位不变（此时原常量仍存在，只是无人读取），再删除原常量并切换所有读取点；任一步出问题，回退点都是干净的提交边界。

**次要风险**：配置文件始终生效意味着一份损坏或极端的配置会改变游戏手感且不易察觉。**缓解**：越界钳制与面板上始终并列显示默认值，让偏离一眼可见。
