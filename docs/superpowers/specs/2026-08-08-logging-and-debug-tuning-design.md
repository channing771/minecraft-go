# 分模块日志与调试面板常量调参

日期：2026-08-08
状态：设计已批准，待实施

## 1. 起因

仓库目前没有任何日志分级或配置文件设施，调参只能改源码重编译。

**日志现状**：两套并存且都不可控。`internal/server`、`internal/sim`、`internal/client/mesher` 用 `slog.Default()`（约 20 处）；`internal/gfx`、`cmd/mcgo`、`cmd/gfxspike` 用 `log.Printf`（约 22 处）。没有等级开关，没有模块开关。想看 gfx 的细节就得连带吞下所有输出，想静音某个刷屏的子系统只能改代码。

**调参现状**：物理与玩法常量散落在各包的 `const` 里（`internal/physics/types.go` 的 `Gravity`、`WalkSpeed`、`JumpSpeed`、`StepHeight`；`internal/sim` 的 `interactionReach`、`RegenIntervalTicks`、`DropLifetimeTicks`、`spawnRadius` 等）。调整手感需要改源码 → 重编译 → 重启 → 重新走到测试位置，一轮几分钟，而手感调参本质上需要几十轮。

**配置现状**：完全没有配置文件。`cmd/mcgo` 与 `cmd/mcgod` 只有命令行 flag（world / seed / listen / max-players 等），全部是启动参数，没有一项是可持久化的偏好。

本设计补上三样东西：一份 JSON 配置文件、一套按模块分级的日志、一个游戏内实时调参面板。

## 2. 目标与非目标

### 目标

- 日志统一到 `slog` 单一链路，支持全局等级与按模块等级覆盖。
- 物理、模拟、渲染三组常量可从配置文件读入，并在游戏内面板实时调整。
- 面板同时显示基础运行读数（帧时、坐标、tick、区块数）。
- 专用服务端 `mcgod` 复用同一份配置（物理 / 模拟 / 日志三组）。

### 非目标（本次明确不做）

- 日志写文件与轮转。
- 游戏内日志窗口。
- 运行时与性能旋钮（tick 间隔、outbox 容量、内存上限、网格化并发度）——这些直接压在现有性能门禁和资源上限契约上。
- 面板文本输入框。调参全部走方向键步进。
- 服务端把生效配置下发给客户端。联机时客户端不改物理 / 模拟，因此不需要这条通道。

## 3. 关键决策

### 3.1 `--dev` 只门控面板，配置文件始终生效

`cmd/mcgo` 新增 `--dev` 旗标。它控制调试面板是否可用，**不**控制配置文件是否被读取。配置文件里调过的值在不开 `--dev` 时同样生效。

代价是游戏行为不再仅由代码决定，而是由代码加本机配置文件共同决定。为了不让这个代价蔓延到自动化验证，见 3.5 的隔离规则。

### 3.2 联机时物理与模拟组只读

`internal/physics` 的常量同时被客户端预测和服务端权威模拟消费。客户端单方面调整重力，等于让预测和权威跑在不同参数上，位置会持续回弹——这直接违反"服务端是唯一权威、客户端镜像只读"这条架构边界。

因此：单机（内置服务端，客户端与模拟同进程）下面板可写物理与模拟组；连远程服务端时这两组灰显只读并标注"服务端控制"，渲染组仍可写。

被否决的替代方案：**服务端下发配置 + 客户端请求修改**。它能让联机也可调，但需要协议 v11 → v12、新增消息类型与权限模型，工作量可能超过本设计其余全部内容之和。**只下发不可写**同样需要动协议，收益不足。

### 3.3 每包自持 atomic 快照，而不是集中式 tunables 包

面板从主 / 渲染 goroutine 写值，服务端权威 tick 在另一条 goroutine 读值。普通包级 `var` 在这里是真实数据竞争，不是理论风险。

采用的方案：`internal/physics` 与 `internal/sim` 各自持有一个 `atomic.Pointer[Tunables]`，读取方在函数入口取一次快照存入局部变量后全程使用局部值。物理固定步 20Hz、模拟每 tick 一次，各一次原子读，在热路径上的开销可以忽略。写入方只做一次指针交换。

被否决的替代方案：

- **新建 `internal/tunables` 叶子包集中所有常量**。好处是默认值与 JSON schema 同源。代价是把 `Gravity` 从 `physics` 搬到一个与领域无关的包，破坏包的自然归属，且 archcheck 白名单里几乎每个包都要加它。收益不抵搬迁成本。
- **纯参数注入，零全局**。最干净，但 `sim` 引擎跑在独立 goroutine 上，面板改值需要新造一条命令通道进引擎；为了省掉一个原子指针反而多一条消息路径。且 `physics.Step` 改签名会波及大量既有测试。

### 3.4 可调常量一律降为未导出默认值

凡是进入 `Tunables` 的常量，其原有导出常量降级为未导出默认值（`physics.Gravity` → `physics.defaultGravity`），唯一读取入口是快照。

这条不是洁癖。`physics.EyeHeight` 目前有 5 个读取点：权威侧 4 处（`internal/sim/mining.go:114`、`engine.go:739`、`container.go:68`、`container.go:162`，全部用于构造交互射线原点）与客户端相机 1 处（`cmd/mcgo/main.go:354`）。若该常量变成可调项而导出常量仍在，只要有一处漏改，相机视线高度与服务端射线原点就会静默错位——玩家瞄准的方块和服务端判定的方块不是同一个。用 archcheck 测试守住，防止后续回退。

### 3.5 自动化验证不得读用户配置

`internal/config` 只允许被 `cmd/*` 导入，`internal/*` 一律不得导入它。`cmd/mcgo` 的 `--benchmark` 与 `--capture-dir` 路径强制使用 `Defaults()`，忽略配置文件。

否则一台机器上的本地调参会污染性能基线比对和 golden 截图，让 `cmd/perfcheck` 与视觉验证的结论依赖于开发者本机的配置文件内容。这条同样用 archcheck 测试守住。

### 3.6 日志模块名从调用点 PC 反查，而非改调用点

`internal/logging` 提供一个 `slog.Handler` 包装器，`cmd/mcgo` 与 `cmd/mcgod` 启动时通过 `slog.SetDefault` 装上。

- `Enabled()` 用所有模块中的**最低**等级做快速门。全部为 info 时，`slog.Debug` 调用被零成本丢弃。
- 只有记录通过快速门后，`Handle()` 才做一次 `runtime.CallersFrames` 从 `Record.PC` 反查包路径，映射为模块名，再按该模块的等级精确过滤。

好处是现有 20 余处 `slog` 调用一行不用改，将来新写的日志也自动归入正确模块。代价是模块名与包路径绑定（`minecraft-go/internal/sim` → `sim`），无法做出与包结构不同的分组——而本设计要的正是按包分组。

被否决的替代方案：**每个调用点显式带 `module` 属性**。`slog.Handler.Enabled()` 拿不到属性，过滤只能在 `Handle()` 里做，既没省下反查成本，又要改几十处调用点。

## 4. 配置文件

### 4.1 位置与格式

默认路径 `os.UserConfigDir()/minecraft-go/config.json`，与既有的 `profile.json` 同目录、同格式（`internal/profile/profile.go:36` 已确立该约定）。`--config <path>` 可覆盖。

```json
{
  "version": 1,
  "logging": {
    "default": "info",
    "modules": { "gfx": "debug", "storage": "warn" }
  },
  "physics": {
    "gravity": 32,
    "walkSpeed": 4.3,
    "jumpSpeed": 8.4,
    "stepHeight": 0.6,
    "eyeHeight": 1.62,
    "groundAcceleration": 40,
    "groundDeceleration": 50,
    "airAcceleration": 8,
    "terminalFallSpeed": 78.4
  },
  "sim": {
    "interactionReach": 6,
    "regenDelayTicks": 100,
    "regenIntervalTicks": 40,
    "dropPickupDelayTicks": 10,
    "playerDropPickupDelayTicks": 40,
    "dropLifetimeTicks": 6000,
    "dropPickupRange": 1.25,
    "spawnRadius": 16,
    "furnaceSmeltTicks": 200,
    "furnaceBurnTicks": 1600
  },
  "render": {
    "viewDistance": 32,
    "fovDegrees": 70,
    "mouseSensitivity": 0.002
  }
}
```

三个渲染组默认值取自现有代码：`viewDistance` 见 `cmd/mcgo/main.go:27`，`fovDegrees` 见 `cmd/mcgo/app.go:453` 的 `mgl32.DegToRad(70)`，`mouseSensitivity` 是 `cmd/mcgo/main.go:248` 的内联字面量 `0.002`（弧度/像素），本次将其提为具名可调项。

注意这三项当前都不住在 `internal/render` 里，而是 `cmd/mcgo` 层的常量与字面量。这与 §8 的依赖方向一致：`internal/config` 只返回纯数据，由 `cmd/mcgo` 消费。

### 4.2 加载语义

| 情况 | 行为 |
| --- | --- |
| 文件不存在 | 全部使用编译默认值，不报错，**不自动创建文件** |
| 字段缺失 | 该字段使用默认值（反序列化进已填默认值的结构体，天然如此） |
| 字段越界 | 钳制到合法区间并 `slog.Warn`，进程正常启动 |
| 未知字段 | 忽略并 `slog.Warn`（不启用 `DisallowUnknownFields`，保留向后兼容余地） |
| JSON 语法错误 | 报错退出。这是明确的用户错误，静默回退到默认值会更难排查 |
| `version` 不认识 | 报错退出，提示升级或删除配置文件 |

每个可调项在 `internal/config` 中带一个合法区间，钳制与面板步进共用同一份区间定义。

### 4.3 保存

只有面板的保存键会写文件。写入采用临时文件 + `rename` 原子替换，避免写到一半崩溃留下半截 JSON。保存内容是当前生效的全部三组值，不做增量合并。

## 5. 可调值管道

`internal/physics` 与 `internal/sim` 各新增：

```go
type Tunables struct { /* 各组可调字段 */ }

func DefaultTunables() Tunables    // 逐字段等于原常量值
func SetTunables(Tunables)         // 只由 cmd 启动装配与调试面板调用
func ActiveTunables() Tunables     // 内部 atomic.Pointer 快照
```

`physics.Step` 在函数首行取一次快照存入局部变量，函数体内 12 处常量读取改为读该局部变量的字段。`sim` 在每 tick 起始处取一次快照，同 tick 内所有判定共用该快照——这同时保证了单个 tick 内参数不会中途变化，模拟仍是确定性的。

调用方签名全部不变，既有测试无需改动。

### 5.1 明确排除的常量

以下为结构性常量，取值直接决定线上协议编码宽度、存档 schema 中的字节布局或预分配缓冲容量，改动即破坏兼容性，一律不进配置：

`core.InventorySlots`、`core.ChestSlots`、`core.DropsPerChunk`、`core.FurnacesPerChunk`、`core.ChestsPerChunk`、`core.MaxSessionDrops`、`physics.FixedDelta` 与 `physics.FixedDeltaSeconds`、`sim` 的 tick 间隔与追帧上限、各渲染器的实例容量上限。

## 6. 调试面板

### 6.1 渲染

新增 `internal/render/debug_panel.go`。复用既有的 `GlyphAtlas`（`internal/render/font_atlas.go`，Noto Sans CJK，1024 槽按需光栅化，name tag 已在使用）与 `hotbar.go` 的屏幕空间透明 pass 模式。不引入新字体资源，不新增第三方依赖。

固定容量：最大 64 行、字形实例数组预分配，与 hotbar / drop 渲染器一致，热路径无动态增长。

`--dev` 未开启时面板不可用；开启后 `F3` 切换显隐。关闭状态下整个 pass 跳过，不产出任何实例。

### 6.2 内容

**读数区**：帧时 p50 与 FPS、玩家坐标与朝向、当前 tick 与昼夜时刻、已加载区块数、运行模式（单机 / 联机及地址）。这些数据客户端均已持有。

**参数区**：按 physics / sim / render 分组，每行显示「名称 · 当前值 · 默认值」。

### 6.3 交互

全部走方向键，不做文本输入框（省去 IME、光标、焦点管理一整套）：

| 按键 | 行为 |
| --- | --- |
| `↑` / `↓` | 选择行 |
| `←` / `→` | 按步长增减 |
| `Shift` + `←` / `→` | ×10 粗调 |
| `Alt` + `←` / `→` | ×0.1 细调 |
| `Enter` | 当前行重置为默认值 |
| `F5` | 保存到配置文件 |
| `F6` | 全部重置为默认值 |

联机时 physics 与 sim 两组灰显只读并标注"服务端控制"，方向键跳过这些行；render 组仍可写。

### 6.4 `viewDistance` 是配置文件项，不是实时项

`fovDegrees` 与 `mouseSensitivity` 只影响相机矩阵与输入换算，都在主 goroutine 上、每帧重新读取，实时调整安全。

`viewDistance` 不同。它在 `cmd/mcgo/app.go:318` 通过 `config.ViewRadius = viewDistance + 1` 决定客户端向服务端申请的订阅半径，运行中修改需要重新协商订阅、增删区块与重建网格，还会牵动 `cmd/mcgo/main.go` 的堆软上限（该 1500MiB 取值是按视距 32 实测标定的）。

因此 `viewDistance` 在面板中显示为只读并标注"重启生效"，仅由配置文件读入。把它做成实时项所需的订阅重协商属于独立变更，本次不做。

## 7. 日志

`internal/logging` 提供 handler 包装器与 `Install(config)`，由 `cmd/mcgo` 与 `cmd/mcgod` 在启动时调用 `slog.SetDefault` 装上。模块名取自包路径末段：`server`、`sim`、`client`、`gfx`、`storage`、`network`、`render`、`mesh`、`worldgen`。

同时把散落的 `log.Printf` 换成 `slog`：`internal/gfx/wgpu.go`（3 处）、`cmd/mcgo`（约 15 处）、`cmd/gfxspike`（4 处）。

`log.Fatalf` 保持不变——那是启动期致命错误，不应走可被过滤的通道。

## 8. 依赖方向

`internal/archcheck/deps_test.go` 的 `allowed` 表新增两条：

```go
"internal/logging": {},
"internal/config":  {"internal/core", "internal/physics", "internal/sim"},
```

`internal/config` 刻意不依赖 `internal/render`。渲染组的值由它以纯数据结构返回，`cmd/mcgo` 读出后作为普通参数传给 app 与渲染器。这样 `mcgod` 导入 `config` 也不会传递性地拖入图形依赖，既有的 `TestMCGodHasNoGraphicsDependencies` 门禁无需放宽。

渲染组的值只在主 goroutine 上被读写（面板与渲染同线程），因此不需要原子快照。

## 9. 验证

| 范围 | 要证明的事 |
| --- | --- |
| `internal/physics` | `DefaultTunables()` 逐字段等于原常量值，保证默认行为逐位不变；快照生效；并发测试：一边 `Step` 一边 `SetTunables`，`-race` 下无竞争 |
| `internal/sim` | tick 起始取快照生效；同一 tick 内参数不变；`interactionReach` 等改动影响权威判定 |
| `internal/config` | 加载 round-trip；字段缺失取默认；越界钳制并告警；未知字段忽略；JSON 语法错误报错；`version` 不认识报错；原子保存；文件不存在不报错 |
| `internal/logging` | 从 PC 反查包名正确；`Enabled()` 快速门在全 info 时拒绝 debug；单模块开 debug 时精确放行；未知包路径的兜底行为 |
| `internal/render` | 面板布局：行数与字形数不超上限；联机时 physics/sim 只读态；`viewDistance` 始终只读；关闭时不产出实例 |
| `internal/archcheck` | 白名单新增两包；可调常量不得再以导出 const 暴露；`internal/*` 不得导入 `internal/config` |
| `cmd/mcgo` | `--dev` 门控面板；`--benchmark` 与 `--capture-dir` 强制 `Defaults()` |
| `cmd/mcgod` | `--config` 生效；缺省时用默认值 |

门禁命令：

```bash
go test ./internal/physics ./internal/sim ./internal/config ./internal/logging ./internal/render ./internal/archcheck -race -count=1
go test ./... -race
go vet ./...
gofmt -l .
go test ./internal/render -bench . -run '^$'    # 面板关闭时零额外开销
# cmd/perfcheck 基线比对：证明物理快照未拖慢固定步
openspec validate --all --strict --no-interactive
```

## 10. 受影响文件

**新增**

- `internal/config/`（含 `_test.go`）
- `internal/logging/`（含 `_test.go`）
- `internal/render/debug_panel.go`、`debug_panel_test.go`、对应 shader

**改动**

- `internal/physics/types.go`、`internal/physics/motion.go`
- `internal/sim/`：`engine.go`、`drop.go`、`health_regen.go`、`spawn.go`、`furnace.go` 等常量读取点
- `internal/client/window.go`：`Key` 枚举新增面板所需按键（F3/F5/F6、方向键、Enter、LeftAlt）
- `cmd/mcgo/main.go`（含 `physics.EyeHeight` 相机读取点与面板输入接线）、`cmd/mcgo/app.go`
- `cmd/mcgod/main.go`
- `cmd/gfxspike/main.go`
- `internal/gfx/wgpu.go`
- `internal/archcheck/deps_test.go`

## 11. 风险与回退

**主要风险**：3.4 的"可调常量降为未导出"波及面广，一次改完难以定位回归。

**缓解**：分两步落地。第一步只建立 `Tunables` 管道并证明默认值行为逐位不变——此时原常量仍然存在，只是无人读取；第二步再删除原常量并切换所有读取点。任一步出问题，回退点都是干净的提交边界。

**次要风险**：配置文件始终生效（3.1）意味着一份损坏或极端的配置会改变游戏手感且不易察觉。缓解手段是越界钳制（4.2）与面板上始终并列显示默认值，让偏离一眼可见。

## 12. OpenSpec 归属

本变更新增两个内部包、跨五个包重构常量、修改 archcheck 依赖白名单，按 `CLAUDE.md` 的门槛属于必须走 OpenSpec change 的类别。实施前需先建立 change 并产出 `proposal.md`、delta specs、`design.md` 与 `tasks.md`。
