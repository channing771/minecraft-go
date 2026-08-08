# 分模块日志与调试面板常量调参 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 给项目加上一份 JSON 配置文件、一套按模块分级的 slog 日志，以及一个 `--dev` 门控的游戏内实时常量调参面板。

**Architecture:** `internal/physics` 与 `internal/sim` 各自持有 `atomic.Pointer[Tunables]`，读取方在函数或 tick 入口取一次快照后全程使用局部值，写入方做一次指针交换，从而在面板（主 goroutine）与权威 tick（另一 goroutine）之间无锁无竞争。`internal/config` 负责 JSON 加载、区间钳制与原子保存，只被 `cmd/*` 导入，保证自动化验证不读用户配置。`internal/logging` 提供一个从调用点 PC 反查包名的 `slog.Handler` 包装器，现有日志调用点一行不改。

**Tech Stack:** Go 1.26 标准库（`log/slog`、`encoding/json`、`sync/atomic`、`runtime`）。零新增第三方依赖。面板复用既有的 `internal/render.GlyphAtlas` 与 `hotbar.wgsl` 的屏幕空间实例化模式。

**设计文档:** `docs/superpowers/specs/2026-08-08-logging-and-debug-tuning-design.md`

## Global Constraints

- Go 代码必须通过 `gofmt`；`gofmt -l .` 无输出。
- 代码注释与 GoDoc 使用中文；Go 标识符、wire magic、协议字段名保留英文。
- 不得新增第三方依赖。
- 不得放宽既有正确性、资源上限或性能门禁来让测试通过。
- 服务端是世界与玩家状态的唯一权威；客户端镜像只读。联机时面板不得写 physics/sim 两组。
- 跨 goroutine 共享的可调值只能通过 `atomic.Pointer` 整体替换，不得使用普通 `var`。
- `internal/config` 只允许被 `cmd/*` 导入；`internal/*` 一律不得导入它。
- `internal/archcheck/deps_test.go` 的 `allowed` 表是依赖白名单的唯一事实来源，新增包必须登记。
- 只有 `internal/gfx` 可直接导入 WebGPU 绑定；`mcgod` 不得传递性依赖任何图形包。
- 自动测试不得启动或聚焦前台游戏窗口。
- 新增或修改行为时先写失败测试（red-green-refactor）。
- 保留用户已有及与本任务无关的工作区改动（当前 `midscene_run/log/` 下有未提交改动，不要动它们）。

---

## Task 1: 建立 OpenSpec change

本变更新增两个内部包、跨五个包重构常量、修改 archcheck 依赖白名单，按 `CLAUDE.md` 属于必须走 OpenSpec 的类别。Stop hook 也会要求关联 active change，**先做这一步，否则后续每次提交都会被 hook 拦下**。

**Files:**
- Create: `openspec/changes/logging-and-debug-tuning/.openspec.yaml`
- Create: `openspec/changes/logging-and-debug-tuning/proposal.md`
- Create: `openspec/changes/logging-and-debug-tuning/design.md`
- Create: `openspec/changes/logging-and-debug-tuning/tasks.md`
- Create: `openspec/changes/logging-and-debug-tuning/specs/module-scoped-logging/spec.md`
- Create: `openspec/changes/logging-and-debug-tuning/specs/tunable-constants/spec.md`

**Interfaces:**
- Consumes: 无。
- Produces: 一个 active change 目录，后续所有任务的提交都关联它。

- [ ] **Step 1: 创建 change 目录与元数据**

```bash
mkdir -p openspec/changes/logging-and-debug-tuning/specs/module-scoped-logging
mkdir -p openspec/changes/logging-and-debug-tuning/specs/tunable-constants
```

`.openspec.yaml`：

```yaml
schema: spec-driven
created: 2026-08-08
```

- [ ] **Step 2: 写 proposal.md**

沿用 `openspec/changes/session-disconnect-reason/proposal.md` 的结构（`## Why` / `## What Changes` / `## Capabilities` / `## Impact`）。内容从设计文档 §1、§2、§3 提炼，务必包含设计文档中的三条"明确不做"：

- 联机时不下发服务端配置，物理/sim 组在面板中只读（设计 §3.2）。
- `viewDistance` 不做实时调整，仅配置文件读入、重启生效（设计 §6.4）。
- 结构性常量不进配置：`core.InventorySlots`、`core.ChestSlots`、`core.DropsPerChunk`、`core.FurnacesPerChunk`、`core.ChestsPerChunk`、`core.MaxSessionDrops`、`physics.FixedDelta`、`physics.FixedDeltaSeconds`、`physics.PlayerWidth`、`physics.PlayerHeight`、`physics.CollisionEpsilon`、`physics.GroundProbe`、sim 的 tick 间隔与追帧上限、各渲染器实例容量上限（设计 §5.1）。

`## Capabilities` 段落：

```markdown
### New Capabilities

- `module-scoped-logging`: 按模块分级过滤日志输出的行为契约。
- `tunable-constants`: 物理与模拟常量可由配置文件与调试面板调整的行为契约。

### Modified Capabilities

（无。）
```

- [ ] **Step 3: 写 specs/module-scoped-logging/spec.md**

只描述可观察行为，不写实现。至少覆盖：

```markdown
## Purpose

使运行中的客户端与专用服务端能够按子系统分别控制日志详细程度，而不必在"全部静音"和"全部刷屏"之间二选一。

## ADDED Requirements

### Requirement: 全局等级过滤

进程 SHALL 有一个全局日志等级。低于该等级的日志记录 MUST NOT 出现在输出中。

#### Scenario: 低于全局等级的记录被丢弃

- **GIVEN** 全局等级为 info
- **WHEN** 某处产生一条 debug 记录
- **THEN** 输出中 MUST NOT 包含该记录

### Requirement: 按模块覆盖等级

配置 SHALL 能为单个模块指定不同于全局等级的等级。该模块的记录 MUST 按其自身等级过滤，其余模块 MUST 不受影响。

模块的归属 MUST 由记录产生的位置决定，MUST NOT 要求日志调用点显式声明模块。

#### Scenario: 单模块放宽

- **GIVEN** 全局等级为 info，模块 gfx 的等级为 debug
- **WHEN** gfx 与 sim 各产生一条 debug 记录
- **THEN** 输出 MUST 包含 gfx 的那条
- **AND** 输出 MUST NOT 包含 sim 的那条

#### Scenario: 单模块收紧

- **GIVEN** 全局等级为 info，模块 storage 的等级为 error
- **WHEN** storage 产生一条 warn 记录
- **THEN** 输出 MUST NOT 包含该记录

### Requirement: 全局关闭时不承担识别代价

当没有任何模块被放宽到低于全局等级时，低于全局等级的记录 MUST 在不进行模块识别的情况下被丢弃。
```

- [ ] **Step 4: 写 specs/tunable-constants/spec.md**

至少覆盖以下要求，每条带可判定的 Given/When/Then：

- **默认行为不变**：无配置文件时，物理与模拟的生效参数 MUST 与本变更之前的编译常量逐字段相等。
- **配置文件容错**：字段缺失取默认；越界钳制到合法区间并告警且进程 MUST 正常启动；未知字段忽略并告警；JSON 语法错误与不认识的 `version` MUST 报错退出。
- **单次推进内参数不变**：一次固定步或一次权威 tick 内，参数 MUST 全程使用同一份快照，MUST NOT 中途变化。
- **联机时权威参数只读**：连接远程服务端时，客户端 MUST NOT 修改物理与模拟参数。
- **自动化验证不受本机配置影响**：性能门禁与抓帧比对路径 MUST 使用编译默认值，MUST NOT 读取用户配置文件。
- **唯一读取入口**：物理与模拟的可调参数 MUST 只能通过快照读取，MUST NOT 另有可直接读到编译期值的导出入口。

- [ ] **Step 5: 写 design.md 与 tasks.md**

`design.md` 从设计文档 §3（关键决策，含被否决的替代方案）、§5（可调值管道）、§8（依赖方向）提炼。

`tasks.md` 是本计划 Task 2–11 的可勾选映射，格式参考 `openspec/changes/session-disconnect-reason/tasks.md`：顶部用引用块指向本计划文件，然后按任务组列出可勾选项与验证命令。收尾任务组必须包含 `gofmt`、`go vet ./...`、`go test ./... -race` 与 `openspec validate --all --strict --no-interactive`。

- [ ] **Step 6: 校验并提交**

Run: `openspec validate --all --strict --no-interactive`
Expected: 通过，无 error。

```bash
git add openspec/changes/logging-and-debug-tuning
git commit -m "docs: 建立分模块日志与调试面板调参的 OpenSpec change"
```

---

## Task 2: `internal/logging` 分模块 handler

**Files:**
- Create: `internal/logging/logging.go`
- Create: `internal/logging/logging_test.go`
- Modify: `internal/archcheck/deps_test.go:212-238`（`allowed` 表新增 `"internal/logging": {}`）

**Interfaces:**
- Consumes: 无。
- Produces:
  - `type Config struct { Default slog.Level; Modules map[string]slog.Level }`
  - `func New(inner slog.Handler, config Config) slog.Handler`
  - `func ParseLevel(text string) (slog.Level, error)`
  - `func Install(inner slog.Handler, config Config)`（内部调用 `slog.SetDefault`）

- [ ] **Step 1: 在 archcheck 白名单登记新包**

`internal/archcheck/deps_test.go` 的 `allowed` 表中加一行（放在 `"internal/gfx": {}` 之后，保持字母序邻近）：

```go
	"internal/logging":    {},
```

- [ ] **Step 2: 写失败测试**

创建 `internal/logging/logging_test.go`。注意测试文件的包名是 `logging`（内部测试），因此其中产生的记录反查出的模块名就是 `logging`。

```go
package logging

import (
	"bytes"
	"context"
	"log/slog"
	"runtime"
	"strings"
	"testing"
	"time"
)

// callerRecord 构造一条 PC 指向本测试文件的记录，其模块名应为 logging。
func callerRecord(level slog.Level, message string) slog.Record {
	var pcs [1]uintptr
	runtime.Callers(2, pcs[:])
	return slog.NewRecord(time.Now(), level, message, pcs[0])
}

func newTestHandler(config Config) (slog.Handler, *bytes.Buffer) {
	var buffer bytes.Buffer
	inner := slog.NewTextHandler(&buffer, &slog.HandlerOptions{Level: slog.LevelDebug})
	return New(inner, config), &buffer
}

func TestGlobalLevelDropsLowerRecords(t *testing.T) {
	handler, buffer := newTestHandler(Config{Default: slog.LevelInfo})
	if handler.Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("全局 info 下 debug 必须被 Enabled 快速门拒绝")
	}
	if err := handler.Handle(context.Background(), callerRecord(slog.LevelDebug, "掉弃")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if buffer.Len() != 0 {
		t.Fatalf("低于全局等级的记录不该输出，实际 %q", buffer.String())
	}
}

func TestModuleOverrideLoosensSingleModule(t *testing.T) {
	handler, buffer := newTestHandler(Config{
		Default: slog.LevelInfo,
		Modules: map[string]slog.Level{"logging": slog.LevelDebug},
	})
	if !handler.Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("存在放宽到 debug 的模块时，Enabled 快速门必须放行 debug")
	}
	if err := handler.Handle(context.Background(), callerRecord(slog.LevelDebug, "放行")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !strings.Contains(buffer.String(), "放行") {
		t.Fatalf("被放宽模块的 debug 必须输出，实际 %q", buffer.String())
	}
}

func TestModuleOverrideDoesNotLeakToOtherModules(t *testing.T) {
	handler, buffer := newTestHandler(Config{
		Default: slog.LevelInfo,
		Modules: map[string]slog.Level{"gfx": slog.LevelDebug},
	})
	// 记录来自 logging 包，不是被放宽的 gfx。
	if err := handler.Handle(context.Background(), callerRecord(slog.LevelDebug, "不该出现")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if buffer.Len() != 0 {
		t.Fatalf("未被放宽的模块 debug 不该输出，实际 %q", buffer.String())
	}
}

func TestModuleOverrideTightensSingleModule(t *testing.T) {
	handler, buffer := newTestHandler(Config{
		Default: slog.LevelInfo,
		Modules: map[string]slog.Level{"logging": slog.LevelError},
	})
	if err := handler.Handle(context.Background(), callerRecord(slog.LevelWarn, "不该出现")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if buffer.Len() != 0 {
		t.Fatalf("被收紧模块的 warn 不该输出，实际 %q", buffer.String())
	}
}

func TestModuleForPC(t *testing.T) {
	var pcs [1]uintptr
	runtime.Callers(1, pcs[:])
	if module := moduleForPC(pcs[0]); module != "logging" {
		t.Fatalf("模块名 = %q，want logging", module)
	}
	if module := moduleForPC(0); module != "" {
		t.Fatalf("零 PC 的模块名 = %q，want 空", module)
	}
}

func TestParseLevel(t *testing.T) {
	for text, want := range map[string]slog.Level{
		"debug": slog.LevelDebug, "DEBUG": slog.LevelDebug,
		"info": slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError,
	} {
		got, err := ParseLevel(text)
		if err != nil {
			t.Fatalf("ParseLevel(%q): %v", text, err)
		}
		if got != want {
			t.Fatalf("ParseLevel(%q) = %v，want %v", text, got, want)
		}
	}
	if _, err := ParseLevel("verbose"); err == nil {
		t.Fatal("未知等级必须报错")
	}
}

func TestWithAttrsPreservesFiltering(t *testing.T) {
	handler, buffer := newTestHandler(Config{Default: slog.LevelInfo})
	derived := handler.WithAttrs([]slog.Attr{slog.String("k", "v")})
	if err := derived.Handle(context.Background(), callerRecord(slog.LevelDebug, "掉弃")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if buffer.Len() != 0 {
		t.Fatalf("WithAttrs 派生的 handler 必须保持过滤，实际 %q", buffer.String())
	}
}
```

- [ ] **Step 3: 跑测试确认失败原因是缺实现而非别的**

Run: `go test ./internal/logging -run . -count=1`
Expected: 编译失败，`undefined: New` / `undefined: Config` 等。

- [ ] **Step 4: 实现 `internal/logging/logging.go`**

```go
// Package logging 提供按模块分级过滤的 slog handler。
//
// 模块名从记录的调用点 PC 反查包路径末段得到（minecraft-go/internal/sim → sim），
// 因此日志调用点不需要显式声明自己属于哪个模块，新写的日志也自动归入正确模块。
package logging

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
)

// Config 是日志等级配置。Modules 的键是模块名，即包路径末段。
type Config struct {
	Default slog.Level
	Modules map[string]slog.Level
}

// ParseLevel 把配置文件中的等级文本解析为 slog.Level，大小写不敏感。
func ParseLevel(text string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("logging: 未知日志等级 %q", text)
	}
}

type handler struct {
	inner    slog.Handler
	config   Config
	minLevel slog.Level
}

// New 用 config 包装 inner。
//
// minLevel 取全局等级与所有模块等级中的最小值，供 Enabled 做快速门：没有任何模块
// 被放宽时，低于全局等级的记录在这里就被拒绝，不产生记录也不做模块反查。只有当某个
// 模块被显式放宽时，Handle 才会为通过快速门的记录做一次 runtime.CallersFrames。
func New(inner slog.Handler, config Config) slog.Handler {
	minimum := config.Default
	for _, level := range config.Modules {
		if level < minimum {
			minimum = level
		}
	}
	return &handler{inner: inner, config: config, minLevel: minimum}
}

// Install 把包装后的 handler 装为进程默认 logger。只应由 cmd 启动装配调用。
func Install(inner slog.Handler, config Config) {
	slog.SetDefault(slog.New(New(inner, config)))
}

func (h *handler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.minLevel
}

func (h *handler) Handle(ctx context.Context, record slog.Record) error {
	threshold := h.config.Default
	if len(h.config.Modules) != 0 {
		if level, ok := h.config.Modules[moduleForPC(record.PC)]; ok {
			threshold = level
		}
	}
	if record.Level < threshold {
		return nil
	}
	return h.inner.Handle(ctx, record)
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &handler{inner: h.inner.WithAttrs(attrs), config: h.config, minLevel: h.minLevel}
}

func (h *handler) WithGroup(name string) slog.Handler {
	return &handler{inner: h.inner.WithGroup(name), config: h.config, minLevel: h.minLevel}
}

// moduleForPC 从调用点 PC 反查模块名，取包路径末段。
// frame.Function 形如 minecraft-go/internal/sim.(*Engine).Step，返回 sim。
// 反查不出时返回空串，此时按全局等级处理。
func moduleForPC(pc uintptr) string {
	if pc == 0 {
		return ""
	}
	frame, _ := runtime.CallersFrames([]uintptr{pc}).Next()
	name := frame.Function
	if name == "" {
		return ""
	}
	if slash := strings.LastIndex(name, "/"); slash >= 0 {
		name = name[slash+1:]
	}
	if dot := strings.Index(name, "."); dot >= 0 {
		name = name[:dot]
	}
	return name
}
```

- [ ] **Step 5: 跑测试确认转绿**

Run: `go test ./internal/logging ./internal/archcheck -race -count=1`
Expected: PASS

- [ ] **Step 6: 变异验证快速门**

把 `Enabled` 临时改成 `return true`，确认 `TestGlobalLevelDropsLowerRecords` 变红。**若仍然通过，说明快速门没有被断言守住，必须加强测试**。恢复后确认 `git diff internal/logging/logging.go` 只剩正常实现。

- [ ] **Step 7: 提交**

```bash
git add internal/logging internal/archcheck/deps_test.go
git commit -m "feat: 增加按模块分级过滤的 slog handler"
```

---

## Task 3: 统一日志到 slog

把散落的 `log.Printf` 换成 `slog`，让所有日志走同一条可过滤链路。`log.Fatalf` 保持不变——那是启动期致命错误，不应被过滤掉。

**Files:**
- Modify: `internal/gfx/wgpu.go:312`、`:344`、`:748`、`:760`
- Modify: `cmd/mcgo/main.go:199`、`:351`
- Modify: `cmd/mcgo/app.go:824`、`:851`、`:1204`、`:1208`、`:1231`、`:1249`、`:1258`、`:1274`、`:1293`、`:1336`、`:1368`、`:1379`、`:1386`、`:1399`
- Modify: `cmd/gfxspike/main.go:58`

**Interfaces:**
- Consumes: 无（本任务不引入 `internal/logging`，仅做调用形态统一）。
- Produces: 全仓库日志统一为 `slog`，为 Task 8 装配 handler 做好准备。

- [ ] **Step 1: 替换 `internal/gfx/wgpu.go`**

四处改写，等级按语义选择——前两条是启动期信息，后两条是可恢复的跳帧：

```go
// 原：log.Printf("gfx: 后端=%v 适配器=%q 类型=%v", info.BackendType, info.Device, info.AdapterType)
slog.Info("gfx 设备就绪",
    "backend", info.BackendType, "adapter", info.Device, "type", info.AdapterType)

// 原：log.Printf("gfx: surface 格式=%v present 模式=%v", caps.Formats, caps.PresentModes)
slog.Info("gfx surface 能力", "formats", caps.Formats, "presentModes", caps.PresentModes)

// 原：log.Printf("gfx: 获取 surface 纹理失败，跳过本帧: %v", err)
slog.Warn("获取 surface 纹理失败，跳过本帧", "error", err)

// 原：log.Printf("gfx: 创建 surface 纹理视图失败，跳过本帧: %v", err)
slog.Warn("创建 surface 纹理视图失败，跳过本帧", "error", err)
```

同步把 import 里的 `"log"` 换成 `"log/slog"`（若该文件不再使用 `log`）。

- [ ] **Step 2: 替换 `cmd/mcgo`**

`main.go:199` 位于 `main()` 的错误退出路径，保留其"打印后 `os.Exit(1)`"的语义，改为：

```go
	if err := run(os.Args[1:]); err != nil {
		slog.Error("mcgo 退出失败", "error", err)
		os.Exit(1)
	}
```

`main.go:351` 与 `app.go` 的 14 处发送/操作失败一律改为 `slog.Warn`，键名统一用 `"error"`。示例：

```go
// 原：log.Printf("推进玩家预测失败: %v", err)
slog.Warn("推进玩家预测失败", "error", err)

// 原：log.Printf("权威命令被拒绝: sequence=%d reason=%s", ...)
slog.Warn("权威命令被拒绝", "sequence", sequence, "reason", reason)

// 原：log.Printf("关闭客户端会话: %v", cause)
slog.Info("关闭客户端会话", "cause", cause)
```

- [ ] **Step 3: 替换 `cmd/gfxspike/main.go:58`**

```go
// 原：log.Printf("terrain: 已生成 %d 个区块，排队 %d 个区段网格", len(chunks), renderer.PendingUploads())
slog.Info("terrain 就绪", "chunks", len(chunks), "pendingMeshes", renderer.PendingUploads())
```

同文件的 `log.Fatalf`（`:32`、`:39`、`:46`）**不动**，因此 `"log"` import 保留。

- [ ] **Step 4: 确认没有遗漏**

Run: `grep -rn "log\.Printf" --include="*.go" internal cmd`
Expected: 无输出。

Run: `grep -rn "log\.Fatalf" --include="*.go" internal cmd`
Expected: 只剩 `cmd/gfxspike/main.go` 的三处。

- [ ] **Step 5: 回归**

Run: `go test ./internal/gfx ./cmd/mcgo ./cmd/gfxspike -count=1 && go vet ./... && gofmt -l .`
Expected: 测试 PASS，`go vet` 无输出，`gofmt -l .` 无输出。

- [ ] **Step 6: 提交**

```bash
git add internal/gfx/wgpu.go cmd/mcgo cmd/gfxspike
git commit -m "refactor: 把剩余 log.Printf 统一到 slog"
```

---

## Task 4: `physics.Tunables` 管道

本任务只建立管道并证明默认值行为逐位不变，**原有导出常量仍然保留**，Task 7 才删除它们。这样任一步出问题回退点都干净。

**Files:**
- Create: `internal/physics/tunables.go`
- Create: `internal/physics/tunables_test.go`
- Modify: `internal/physics/motion.go:10-70`
- Modify: `internal/physics/step.go:12`

**Interfaces:**
- Consumes: 无。
- Produces:
  - `type Tunables struct { EyeHeight, StepHeight, WalkSpeed, GroundAcceleration, GroundDeceleration, AirAcceleration, JumpSpeed, Gravity, TerminalFallSpeed float32 }`
  - `func DefaultTunables() Tunables`
  - `func SetTunables(Tunables)`
  - `func ActiveTunables() Tunables`

- [ ] **Step 1: 写失败测试**

创建 `internal/physics/tunables_test.go`：

```go
package physics_test

import (
	"sync"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/physics"
)

// TestDefaultTunablesMatchLegacyConstants 是本次重构的行为不变式：
// 默认参数必须逐字段等于重构前的编译常量，否则手感会静默改变。
func TestDefaultTunablesMatchLegacyConstants(t *testing.T) {
	tunables := physics.DefaultTunables()
	for _, check := range []struct {
		name       string
		got, want  float32
	}{
		{"EyeHeight", tunables.EyeHeight, 1.62},
		{"StepHeight", tunables.StepHeight, 0.6},
		{"WalkSpeed", tunables.WalkSpeed, 4.3},
		{"GroundAcceleration", tunables.GroundAcceleration, 40},
		{"GroundDeceleration", tunables.GroundDeceleration, 50},
		{"AirAcceleration", tunables.AirAcceleration, 8},
		{"JumpSpeed", tunables.JumpSpeed, 8.4},
		{"Gravity", tunables.Gravity, 32},
		{"TerminalFallSpeed", tunables.TerminalFallSpeed, 78.4},
	} {
		if check.got != check.want {
			t.Errorf("%s = %v，want %v", check.name, check.got, check.want)
		}
	}
}

func TestActiveTunablesDefaultsToDefaultTunables(t *testing.T) {
	if physics.ActiveTunables() != physics.DefaultTunables() {
		t.Fatal("未经设置时生效参数必须等于默认参数")
	}
}

// TestSetTunablesAffectsStep 证明快照确实被 Step 消费。
func TestSetTunablesAffectsStep(t *testing.T) {
	t.Cleanup(func() { physics.SetTunables(physics.DefaultTunables()) })

	source := emptyCollisionSource{}
	state := physics.State{Position: mgl32.Vec3{0, 64, 0}}

	physics.SetTunables(physics.DefaultTunables())
	slow := physics.Step(state, physics.Input{}, source)

	heavy := physics.DefaultTunables()
	heavy.Gravity *= 2
	physics.SetTunables(heavy)
	fast := physics.Step(state, physics.Input{}, source)

	if !(fast.State.Velocity.Y() < slow.State.Velocity.Y()) {
		t.Fatalf("加倍重力后竖直速度应更负：fast=%v slow=%v",
			fast.State.Velocity.Y(), slow.State.Velocity.Y())
	}
}

// TestStepUsesOneSnapshotPerCall 证明单次固定步内参数不中途变化。
func TestStepUsesOneSnapshotPerCall(t *testing.T) {
	t.Cleanup(func() { physics.SetTunables(physics.DefaultTunables()) })

	source := emptyCollisionSource{}
	state := physics.State{Position: mgl32.Vec3{0, 64, 0}}
	physics.SetTunables(physics.DefaultTunables())
	want := physics.Step(state, physics.Input{}, source)

	// 同样的输入重复推进，结果必须逐位一致。
	for i := 0; i < 16; i++ {
		if got := physics.Step(state, physics.Input{}, source); got != want {
			t.Fatalf("第 %d 次结果 %v != %v", i, got, want)
		}
	}
}

// TestConcurrentStepAndSetTunables 在 -race 下证明快照读写无竞争。
func TestConcurrentStepAndSetTunables(t *testing.T) {
	t.Cleanup(func() { physics.SetTunables(physics.DefaultTunables()) })

	source := emptyCollisionSource{}
	state := physics.State{Position: mgl32.Vec3{0, 64, 0}}
	var group sync.WaitGroup
	group.Add(2)
	go func() {
		defer group.Done()
		for i := 0; i < 2000; i++ {
			physics.Step(state, physics.Input{}, source)
		}
	}()
	go func() {
		defer group.Done()
		for i := 0; i < 2000; i++ {
			tunables := physics.DefaultTunables()
			tunables.Gravity = float32(20 + i%20)
			physics.SetTunables(tunables)
		}
	}()
	group.Wait()
}

// emptyCollisionSource 是没有任何方块的世界，玩家自由下落。
type emptyCollisionSource struct{}

func (emptyCollisionSource) BlockCollision(x, y, z int32) (physics.CollisionBoxSet, bool) {
	return physics.CollisionBoxSet{}, true
}
```

> 若 `internal/physics` 的既有测试里已有等价的空碰撞源辅助类型，改用它而不是新增 `emptyCollisionSource`——先 `grep -rn "CollisionSource" internal/physics/*_test.go` 确认。同理，`BlockCollision` 的确切签名以 `internal/physics/types.go` 的 `CollisionSource` 接口定义为准。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/physics -run Tunables -count=1`
Expected: 编译失败，`undefined: physics.Tunables`。

- [ ] **Step 3: 实现 `internal/physics/tunables.go`**

```go
package physics

import "sync/atomic"

// Tunables 是可在运行时调整的物理参数。
//
// 它按值传递并整体替换。读取方在函数入口取一次快照后全程使用该快照，因此单次固定步
// 内参数不会中途变化，模拟仍然确定性。写入只做一次原子指针交换，读写之间无锁无竞争。
//
// 只有 cmd 的启动装配与调试面板应当调用 SetTunables。
type Tunables struct {
	EyeHeight          float32
	StepHeight         float32
	WalkSpeed          float32
	GroundAcceleration float32
	GroundDeceleration float32
	AirAcceleration    float32
	JumpSpeed          float32
	Gravity            float32
	TerminalFallSpeed  float32
}

// DefaultTunables 返回编译期默认参数。它是配置文件缺省时的取值，
// 也是调试面板"重置"的目标值。
func DefaultTunables() Tunables {
	return Tunables{
		EyeHeight:          EyeHeight,
		StepHeight:         StepHeight,
		WalkSpeed:          WalkSpeed,
		GroundAcceleration: GroundAcceleration,
		GroundDeceleration: GroundDeceleration,
		AirAcceleration:    AirAcceleration,
		JumpSpeed:          JumpSpeed,
		Gravity:            Gravity,
		TerminalFallSpeed:  TerminalFallSpeed,
	}
}

var active atomic.Pointer[Tunables]

func init() {
	defaults := DefaultTunables()
	active.Store(&defaults)
}

// SetTunables 整体替换生效参数。
func SetTunables(tunables Tunables) { active.Store(&tunables) }

// ActiveTunables 返回当前生效参数的快照。
func ActiveTunables() Tunables { return *active.Load() }
```

> Task 7 会把 `DefaultTunables` 引用的导出常量改名为未导出的 `defaultEyeHeight` 等，届时本函数体一并更新。

- [ ] **Step 4: 让 `Step` 消费快照**

`internal/physics/motion.go` 改写 `Step` 与 `movementTarget`：

```go
// Step 推进一个固定步，并解析方块碰撞。
//
// 参数在函数入口取一次快照，全程使用该快照，因此单次固定步内参数不会中途变化。
func Step(state State, input Input, source CollisionSource) StepResult {
	tunables := ActiveTunables()
	validate(state, input)
	beganGrounded := state.OnGround

	target := movementTarget(input, tunables.WalkSpeed)
	horizontal := mgl32.Vec3{state.Velocity.X(), 0, state.Velocity.Z()}
	if state.OnGround {
		if target.Len() == 0 {
			horizontal = moveToward(horizontal, mgl32.Vec3{}, tunables.GroundDeceleration*FixedDeltaSeconds)
		} else {
			horizontal = moveToward(horizontal, target, tunables.GroundAcceleration*FixedDeltaSeconds)
		}
	} else {
		horizontal = moveToward(horizontal, target, tunables.AirAcceleration*FixedDeltaSeconds)
		if horizontal.Len() > tunables.WalkSpeed {
			horizontal = horizontal.Normalize().Mul(tunables.WalkSpeed)
		}
	}
	state.Velocity[0], state.Velocity[2] = horizontal.X(), horizontal.Z()

	if state.OnGround && input.Jump {
		state.Velocity[1] = tunables.JumpSpeed
		state.OnGround = false
	} else {
		state.Velocity[1] = max(
			state.Velocity.Y()-tunables.Gravity*FixedDeltaSeconds,
			-tunables.TerminalFallSpeed,
		)
	}
	displacement := state.Velocity.Mul(FixedDeltaSeconds)
	move := resolveMove(state, displacement, source)
	usedStep := false
	if (move.clipped[0] || move.clipped[2]) &&
		(beganGrounded || move.onGround) &&
		(displacement.X() != 0 || displacement.Z() != 0) {
		if stepped, ok := resolveStepMove(state, displacement, source, tunables.StepHeight); ok &&
			horizontalDistanceSquared(state.Position, stepped.position) >
				horizontalDistanceSquared(state.Position, move.position) {
			move = stepped
			usedStep = true
		}
	}
	state.Position = move.position
	state.OnGround = move.onGround
	for axis, clipped := range move.clipped {
		if clipped {
			state.Velocity[axis] = 0
		}
	}

	return StepResult{State: state, UsedStep: usedStep, HitUnknown: move.hitUnknown}
}

func movementTarget(input Input, walkSpeed float32) mgl32.Vec3 {
	yawSin := float32(math.Sin(float64(input.Yaw)))
	yawCos := float32(math.Cos(float64(input.Yaw)))
	forward := mgl32.Vec3{-yawSin, 0, -yawCos}
	right := mgl32.Vec3{yawCos, 0, -yawSin}
	intent := right.Mul(float32(input.MoveX)).Add(forward.Mul(float32(input.MoveZ)))
	if intent.Len() == 0 {
		return mgl32.Vec3{}
	}
	return intent.Normalize().Mul(walkSpeed)
}
```

`internal/physics/step.go` 给 `resolveStepMove` 加参数：

```go
func resolveStepMove(
	state State, displacement mgl32.Vec3, source CollisionSource, stepHeight float32,
) (moveResult, bool) {
	rise, riseClipped, riseUnknown := clipAxis(result.position, 1, stepHeight, source)
	// ……其余不变
```

> `resolveStepMove` 的完整既有签名与函数体以 `internal/physics/step.go` 为准，本步只加一个 `stepHeight float32` 尾参并把第 12 行的 `StepHeight` 换成它。

- [ ] **Step 5: 跑测试确认转绿**

Run: `go test ./internal/physics -race -count=1`
Expected: PASS。**既有物理测试必须全部通过且不需要修改**——它们是"默认行为逐位不变"的最强证据。若有测试变红，说明重构改变了行为，停手排查，不要改测试期望。

- [ ] **Step 6: 全仓回归**

Run: `go test ./internal/sim ./internal/client ./internal/server -race -count=1`
Expected: PASS

- [ ] **Step 7: 提交**

```bash
git add internal/physics
git commit -m "feat: 增加物理参数运行时快照管道"
```

---

## Task 5: `sim.Tunables` 管道

同样只建管道，不删旧常量。

**Files:**
- Create: `internal/sim/tunables.go`
- Create: `internal/sim/tunables_test.go`
- Modify: `internal/sim/engine.go:122`（`Step` 入口刷新快照）、`:774`、`:739`
- Modify: `internal/sim/health_regen.go:19`、`:22`
- Modify: `internal/sim/drop.go:92`、`:166`、`:368`
- Modify: `internal/sim/mining.go:114`、`:118`、`:236`、`:266`、`:305`
- Modify: `internal/sim/container.go:54`、`:68`、`:73`、`:162`
- Modify: `internal/sim/death.go:152`
- Modify: `internal/sim/furnace.go:61`、`:65`
- Modify: `internal/sim/spawn.go:23-25`

**Interfaces:**
- Consumes: `physics.ActiveTunables()`、`physics.Tunables`（Task 4）
- Produces:
  - `type Tunables struct { InteractionReach float32; RegenDelayTicks, RegenIntervalTicks uint32; DropPickupDelayTicks, PlayerDropPickupDelayTicks, DropLifetimeTicks uint32; DropPickupRange float32; SpawnRadius int32; FurnaceSmeltTicks, FurnaceBurnTicks uint32 }`
  - `func DefaultTunables() Tunables`
  - `func SetTunables(Tunables)`
  - `func ActiveTunables() Tunables`

> 各字段的具体 Go 类型以其现有使用点为准：`interactionReach` 参与浮点距离比较、`spawnRadius` 现为 `int32` 且参与循环边界、tick 计数现为无类型常量参与整数比较。实现时先看使用点再定类型，避免引入类型转换噪音。

- [ ] **Step 1: 写失败测试**

创建 `internal/sim/tunables_test.go`（内部测试包 `sim`，因为要断言 Engine 的私有快照字段）：

```go
package sim

import (
	"testing"

	"minecraft-go/internal/core"
)

func TestDefaultTunablesMatchLegacyConstants(t *testing.T) {
	tunables := DefaultTunables()
	for _, check := range []struct {
		name      string
		got, want float64
	}{
		{"InteractionReach", float64(tunables.InteractionReach), 6},
		{"RegenDelayTicks", float64(tunables.RegenDelayTicks), 100},
		{"RegenIntervalTicks", float64(tunables.RegenIntervalTicks), 40},
		{"DropPickupDelayTicks", float64(tunables.DropPickupDelayTicks), 10},
		{"PlayerDropPickupDelayTicks", float64(tunables.PlayerDropPickupDelayTicks), 40},
		{"DropLifetimeTicks", float64(tunables.DropLifetimeTicks), 6000},
		{"DropPickupRange", float64(tunables.DropPickupRange), 1.25},
		{"SpawnRadius", float64(tunables.SpawnRadius), 16},
		{"FurnaceSmeltTicks", float64(tunables.FurnaceSmeltTicks), float64(core.FurnaceSmeltTicks)},
		{"FurnaceBurnTicks", float64(tunables.FurnaceBurnTicks), float64(core.FurnaceBurnTicks)},
	} {
		if check.got != check.want {
			t.Errorf("%s = %v，want %v", check.name, check.got, check.want)
		}
	}
}

func TestActiveTunablesDefaultsToDefaultTunables(t *testing.T) {
	if ActiveTunables() != DefaultTunables() {
		t.Fatal("未经设置时生效参数必须等于默认参数")
	}
}

// TestEngineRefreshesSnapshotAtTickStart 证明快照在 tick 入口刷新，
// 且同一 tick 内不再变化。
func TestEngineRefreshesSnapshotAtTickStart(t *testing.T) {
	t.Cleanup(func() { SetTunables(DefaultTunables()) })

	engine := newTestEngine(t)

	changed := DefaultTunables()
	changed.InteractionReach = 3
	SetTunables(changed)

	engine.Step()
	if engine.tunables.InteractionReach != 3 {
		t.Fatalf("tick 后引擎快照 InteractionReach = %v，want 3",
			engine.tunables.InteractionReach)
	}

	// tick 之间修改，在下一次 Step 之前引擎快照不应改变。
	again := DefaultTunables()
	again.InteractionReach = 5
	SetTunables(again)
	if engine.tunables.InteractionReach != 3 {
		t.Fatal("引擎快照必须只在 tick 入口刷新")
	}
	engine.Step()
	if engine.tunables.InteractionReach != 5 {
		t.Fatalf("下一次 tick 后应刷新为 5，实际 %v", engine.tunables.InteractionReach)
	}
}
```

> `newTestEngine` 是占位名。实现时先 `grep -rn "func newTestEngine\|NewEngine(" internal/sim/*_test.go` 找到既有的引擎构造辅助函数并复用；`internal/sim` 的测试里已有多处构造 Engine 的模式。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/sim -run Tunables -count=1`
Expected: 编译失败，`undefined: DefaultTunables`。

- [ ] **Step 3: 实现 `internal/sim/tunables.go`**

结构与 `internal/physics/tunables.go` 完全一致：`Tunables` 结构体、`DefaultTunables()`（字段值取自当前各常量与 `core.FurnaceSmeltTicks` / `core.FurnaceBurnTicks`）、`var active atomic.Pointer[Tunables]`、`init()` 存默认值、`SetTunables`、`ActiveTunables`。GoDoc 说明"读取方在 tick 入口取一次快照，同 tick 内全程使用"。

- [ ] **Step 4: 给 Engine 加快照字段并在 tick 入口刷新**

`internal/sim/engine.go` 的 `Engine` 结构体新增两个字段：

```go
	// tunables 与 physicsTunables 在每次 Step 入口刷新一次，同一 tick 内全程使用，
	// 保证单个 tick 的所有判定基于同一份参数。
	tunables        Tunables
	physicsTunables physics.Tunables
```

`func (engine *Engine) Step() TickResult` 的第一行：

```go
func (engine *Engine) Step() TickResult {
	engine.tunables = ActiveTunables()
	engine.physicsTunables = physics.ActiveTunables()
	// ……其余不变
```

Engine 的构造函数中同样初始化这两个字段（用 `ActiveTunables()` / `physics.ActiveTunables()`），使未经 `Step` 就被调用的方法也有可用快照。

- [ ] **Step 5: 把各读取点切到快照**

逐点替换，全部从 `engine.tunables` / `engine.physicsTunables` 取值；被引擎方法调用的自由函数以参数接收所需字段，**不要在自由函数内部再调 `ActiveTunables()`**，那会破坏"同 tick 一份快照"。

| 文件:行 | 原 | 改为 |
| --- | --- | --- |
| `engine.go:739` | `physics.EyeHeight` | `engine.physicsTunables.EyeHeight` |
| `engine.go:774` | `interactionReach` | `engine.tunables.InteractionReach` |
| `container.go:54` | `interactionReach` | 由调用方传入的 `reach` 参数 |
| `container.go:68`、`:162` | `physics.EyeHeight` | `engine.physicsTunables.EyeHeight` |
| `container.go:73` | `interactionReach` | `engine.tunables.InteractionReach` |
| `mining.go:114` | `physics.EyeHeight` | `engine.physicsTunables.EyeHeight` |
| `mining.go:118` | `interactionReach` | `engine.tunables.InteractionReach` |
| `mining.go:236`、`:266`、`:305` | `DropPickupDelayTicks` | `engine.tunables.DropPickupDelayTicks` |
| `drop.go:92` | `DropLifetimeTicks` | 传入的 `lifetimeTicks` 参数 |
| `drop.go:166` | `dropPickupRange` | 传入的 `pickupRange` 参数 |
| `drop.go:368` | `PlayerDropPickupDelayTicks` | `engine.tunables.PlayerDropPickupDelayTicks` |
| `death.go:152` | `PlayerDropPickupDelayTicks` | `engine.tunables.PlayerDropPickupDelayTicks` |
| `health_regen.go:19`、`:22` | `RegenDelayTicks` / `RegenIntervalTicks` | 传入的两个参数 |
| `furnace.go:61` | `core.FurnaceBurnTicks` | 传入的 `burnTicks` 参数 |
| `furnace.go:65` | `core.FurnaceSmeltTicks` | 传入的 `smeltTicks` 参数 |
| `spawn.go:23-25` | `spawnRadius` | 传入的 `radius` 参数 |

`spawn.go:23` 的容量计算 `make([]spawnColumn, 0, (radius*2+1)*(radius*2+1))` 随参数变化，因此**必须**依赖 Task 6 的区间钳制把 `SpawnRadius` 限制在 `1..64`，否则一个大数会造成一次巨额分配。在该处加注释指明这一依赖。

- [ ] **Step 6: 跑测试确认转绿**

Run: `go test ./internal/sim -race -count=1`
Expected: PASS。**既有 sim 测试必须全部通过且不需要修改期望**。

- [ ] **Step 7: 全仓回归**

Run: `go test ./... -race`
Expected: PASS

- [ ] **Step 8: 提交**

```bash
git add internal/sim
git commit -m "feat: 增加模拟参数运行时快照管道"
```

---

## Task 6: `internal/config` 配置加载与保存

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Modify: `internal/archcheck/deps_test.go`（`allowed` 表新增 `internal/config`）

**Interfaces:**
- Consumes: `physics.Tunables`、`physics.DefaultTunables`、`sim.Tunables`、`sim.DefaultTunables`、`logging.Config`、`logging.ParseLevel`
- Produces:
  - `type Render struct { ViewDistance int; FovDegrees float32; MouseSensitivity float32 }`
  - `type Config struct { Version int; Logging logging.Config; Physics physics.Tunables; Sim sim.Tunables; Render Render }`
  - `func Defaults() Config`
  - `func DefaultPath() (string, error)`
  - `func Load(path string) (Config, error)`
  - `func (Config) Save(path string) error`
  - `func (Config) Apply()`（调用 `physics.SetTunables` 与 `sim.SetTunables`）
  - `type Field struct { Group, Name string; Min, Max, Step float64; ReadOnly bool }`
  - `func Fields() []Field`（面板与钳制共用的区间定义）

- [ ] **Step 1: 在 archcheck 白名单登记新包**

```go
	"internal/config":     {"internal/core", "internal/physics", "internal/sim", "internal/logging"},
```

> 刻意不含 `internal/render`：渲染组以纯数据 `Render` 结构体返回，由 `cmd/mcgo` 消费。这样 `mcgod` 导入 `config` 不会传递性拖入图形依赖，`TestMCGodHasNoGraphicsDependencies` 无需放宽。

- [ ] **Step 2: 写失败测试**

创建 `internal/config/config_test.go`：

```go
package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"minecraft-go/internal/config"
	"minecraft-go/internal/physics"
	"minecraft-go/internal/sim"
)

// 注意：config.Config 内嵌的 logging.Config 含 map 字段，因此 Config 整体
// **不可比较**，不能用 == 断言。涉及整体比较一律用 reflect.DeepEqual。
// 不含 map 的 physics.Tunables 与 sim.Tunables 仍可直接用 ==。

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("写测试配置: %v", err)
	}
	return path
}

func TestMissingFileYieldsDefaults(t *testing.T) {
	loaded, err := config.Load(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("文件不存在不应报错: %v", err)
	}
	if !reflect.DeepEqual(loaded, config.Defaults()) {
		t.Fatal("文件不存在时必须返回全默认配置")
	}
}

func TestMissingFileIsNotCreated(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "absent.json")
	if _, err := config.Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("Load 不得创建配置文件")
	}
}

func TestMissingFieldsFallBackToDefaults(t *testing.T) {
	path := writeConfig(t, `{"version":1,"physics":{"gravity":20}}`)
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Physics.Gravity != 20 {
		t.Fatalf("Gravity = %v，want 20", loaded.Physics.Gravity)
	}
	if loaded.Physics.JumpSpeed != physics.DefaultTunables().JumpSpeed {
		t.Fatal("未出现的字段必须保持默认值")
	}
	if loaded.Sim != sim.DefaultTunables() {
		t.Fatal("未出现的分组必须整组保持默认值")
	}
}

func TestOutOfRangeValuesAreClamped(t *testing.T) {
	path := writeConfig(t, `{"version":1,"sim":{"spawnRadius":100000}}`)
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("越界值必须钳制而不是报错: %v", err)
	}
	if loaded.Sim.SpawnRadius > 64 {
		t.Fatalf("SpawnRadius = %v，必须钳制到上界 64", loaded.Sim.SpawnRadius)
	}
}

func TestUnknownFieldsAreIgnored(t *testing.T) {
	path := writeConfig(t, `{"version":1,"physics":{"antigravity":true}}`)
	if _, err := config.Load(path); err != nil {
		t.Fatalf("未知字段必须忽略而不是报错: %v", err)
	}
}

func TestMalformedJSONFails(t *testing.T) {
	path := writeConfig(t, `{"version":1,`)
	if _, err := config.Load(path); err == nil {
		t.Fatal("JSON 语法错误必须报错")
	}
}

func TestUnknownVersionFails(t *testing.T) {
	path := writeConfig(t, `{"version":99}`)
	if _, err := config.Load(path); err == nil {
		t.Fatal("不认识的 version 必须报错")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	want := config.Defaults()
	want.Physics.Gravity = 24
	want.Render.FovDegrees = 90
	if err := want.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round-trip 不一致：%+v != %+v", got, want)
	}
}

func TestSaveIsAtomic(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.json")
	if err := config.Defaults().Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "config.json" {
		t.Fatalf("保存后目录必须只剩目标文件，实际 %v", entries)
	}
	// 保存产物必须是合法 JSON。
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var probe map[string]any
	if err := json.Unmarshal(body, &probe); err != nil {
		t.Fatalf("保存产物不是合法 JSON: %v", err)
	}
}

func TestFieldsCoverEveryTunable(t *testing.T) {
	fields := config.Fields()
	if len(fields) == 0 {
		t.Fatal("Fields 不得为空")
	}
	seen := make(map[string]bool, len(fields))
	for _, field := range fields {
		key := field.Group + "." + field.Name
		if seen[key] {
			t.Fatalf("重复字段 %s", key)
		}
		seen[key] = true
		if field.Min >= field.Max {
			t.Fatalf("%s 的区间非法：[%v, %v]", key, field.Min, field.Max)
		}
		if field.Step <= 0 {
			t.Fatalf("%s 的步长必须为正，实际 %v", key, field.Step)
		}
	}
	if !seen["render.viewDistance"] {
		t.Fatal("Fields 必须包含 render.viewDistance")
	}
	for _, field := range fields {
		if field.Group == "render" && field.Name == "viewDistance" && !field.ReadOnly {
			t.Fatal("viewDistance 必须标记为只读（重启生效）")
		}
	}
}

func TestApplySetsActiveTunables(t *testing.T) {
	t.Cleanup(func() { config.Defaults().Apply() })

	custom := config.Defaults()
	custom.Physics.Gravity = 24
	custom.Sim.InteractionReach = 4
	custom.Apply()

	if physics.ActiveTunables().Gravity != 24 {
		t.Fatal("Apply 必须写入 physics 生效参数")
	}
	if sim.ActiveTunables().InteractionReach != 4 {
		t.Fatal("Apply 必须写入 sim 生效参数")
	}
}
```

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./internal/config -count=1`
Expected: 编译失败，`no required module provides package minecraft-go/internal/config`。

- [ ] **Step 4: 实现 `internal/config/config.go`**

要点：

```go
// Package config 负责读写调参配置文件，并把生效值写入各包的运行时快照。
//
// 本包只应被 cmd 导入。internal 下其他包一律不得依赖它——否则一台机器上的本地
// 调参会污染性能基线比对与抓帧 golden 比对，让自动化验证的结论取决于开发者本机
// 的配置文件内容。该约束由 internal/archcheck 守住。
package config

// CurrentVersion 是本程序认识的配置文件版本。
const CurrentVersion = 1
```

- `Load(path)`：`os.ReadFile`；`os.IsNotExist` 时返回 `Defaults(), nil`；反序列化进一个已经填好 `Defaults()` 的中间结构体，使缺失字段天然保留默认值；**不启用** `DisallowUnknownFields`；`version` 缺失视为 `CurrentVersion`，不等于 `CurrentVersion` 时报错；随后逐字段钳制。
- 日志等级**必须先解码为字符串再经 `logging.ParseLevel` 转换**，不得让 `json.Unmarshal` 直接填进 `slog.Level`。`slog.Level` 自带 `UnmarshalText`，直接反序列化时遇到未知等级会让整个 `Load` 失败，而本设计要求未知等级落回默认并 `slog.Warn`——与"配置容错，只有语法错误和版本不符才报错"这条原则一致。因此中间结构体中该字段的类型是 `string`：

```go
type rawLogging struct {
	Default string            `json:"default"`
	Modules map[string]string `json:"modules"`
}
```
- 钳制与告警：遍历 `Fields()`，越界时钳到边界并 `slog.Warn("配置项越界已钳制", "field", key, "value", value, "clamped", clamped)`。
- 未知字段告警：反序列化到 `map[string]json.RawMessage` 做一次对照，对不在已知集合中的键 `slog.Warn`。
- `Save(path)`：`os.MkdirAll(filepath.Dir(path), 0o755)` → `os.CreateTemp` 同目录临时文件 → `json.MarshalIndent` 写入 → `Sync` → `Close` → `os.Rename`。失败路径删除临时文件。
- `Apply()`：`physics.SetTunables(c.Physics)` + `sim.SetTunables(c.Sim)`。**不碰 render 组**——它由 `cmd/mcgo` 自行消费。
- `Fields()`：返回全部可调项的区间与步长，`render.viewDistance` 的 `ReadOnly` 为 `true`。`sim.spawnRadius` 的区间必须是 `1..64`（Task 5 Step 5 的分配安全依赖这条）。

- [ ] **Step 5: 跑测试确认转绿**

Run: `go test ./internal/config ./internal/archcheck -race -count=1`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add internal/config internal/archcheck/deps_test.go
git commit -m "feat: 增加调参配置文件的加载、钳制与原子保存"
```

---

## Task 7: 删除旧导出常量并加 archcheck 守卫

这是波及面最大的一步，因此单独成任务、单独一个回退点。

**Files:**
- Modify: `internal/physics/types.go:15-28`
- Modify: `internal/physics/tunables.go`（`DefaultTunables` 改引用未导出常量）
- Modify: `internal/sim/health_regen.go:5-9`、`internal/sim/drop.go:16-24`、`internal/sim/spawn.go:14`、`internal/sim/engine.go:25`
- Modify: `cmd/mcgo/main.go:354`
- Modify: `internal/archcheck/deps_test.go`（新增两条守卫测试）

**Interfaces:**
- Consumes: Task 4 与 Task 5 的 `ActiveTunables()`
- Produces: 可调参数的唯一读取入口是快照；由 archcheck 守住。

- [ ] **Step 1: 先写守卫测试（此时应当失败）**

在 `internal/archcheck/deps_test.go` 追加：

```go
// TestTunableConstantsAreNotExported 守住"可调参数只能经快照读取"这条不变量。
//
// 若某个可调参数同时以导出常量存在，任何一处漏改都会让编译期值与快照值并存：
// 例如相机读到编译期 EyeHeight、服务端射线读到快照值，玩家瞄准的方块与服务端
// 判定的方块就不是同一个，而且不会有任何报错。
func TestTunableConstantsAreNotExported(t *testing.T) {
	forbidden := map[string][]string{
		filepath.Join("internal", "physics"): {
			"EyeHeight", "StepHeight", "WalkSpeed", "GroundAcceleration",
			"GroundDeceleration", "AirAcceleration", "JumpSpeed", "Gravity",
			"TerminalFallSpeed",
		},
		filepath.Join("internal", "sim"): {
			"RegenDelayTicks", "RegenIntervalTicks", "DropPickupDelayTicks",
			"PlayerDropPickupDelayTicks", "DropLifetimeTicks",
		},
	}
	root := moduleRoot(t)
	for packageDirectory, names := range forbidden {
		files, err := filepath.Glob(filepath.Join(root, packageDirectory, "*.go"))
		if err != nil {
			t.Fatalf("枚举 %s: %v", packageDirectory, err)
		}
		banned := make(map[string]bool, len(names))
		for _, name := range names {
			banned[name] = true
		}
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				t.Fatalf("解析 %s: %v", path, err)
			}
			for _, declaration := range parsed.Decls {
				general, ok := declaration.(*ast.GenDecl)
				if !ok || (general.Tok != token.CONST && general.Tok != token.VAR) {
					continue
				}
				for _, specification := range general.Specs {
					value, ok := specification.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for _, name := range value.Names {
						if banned[name.Name] {
							t.Errorf("%s: 可调参数 %s 仍以导出常量暴露，唯一入口必须是 Tunables 快照",
								path, name.Name)
						}
					}
				}
			}
		}
	}
}

// TestOnlyCommandsImportConfig 守住"自动化验证不读用户配置"这条不变量。
func TestOnlyCommandsImportConfig(t *testing.T) {
	cmd := exec.Command("go", "list", "-f",
		"{{.ImportPath}}|{{join .Imports \" \"}}", "./internal/...")
	cmd.Dir = moduleRoot(t)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 || parts[0] == "minecraft-go/internal/config" {
			continue
		}
		for _, imported := range strings.Fields(parts[1]) {
			if imported == "minecraft-go/internal/config" {
				t.Errorf("%s 导入了 internal/config；只有 cmd 可以导入它，"+
					"否则本机配置会污染性能基线与抓帧 golden", parts[0])
			}
		}
	}
}
```

- [ ] **Step 2: 跑守卫测试确认它现在是红的**

Run: `go test ./internal/archcheck -run TunableConstants -count=1`
Expected: FAIL，列出 `internal/physics/types.go: 可调参数 Gravity 仍以导出常量暴露` 等九项。这证明守卫确实在工作。

- [ ] **Step 3: 把常量降为未导出**

`internal/physics/types.go` 的 const 块：

```go
const (
	FixedDelta        = 50 * time.Millisecond
	FixedDeltaSeconds = float32(0.05)
	PlayerWidth       = float32(0.6)
	PlayerHeight      = float32(1.8)
	CollisionEpsilon  = float32(1e-5)
	GroundProbe       = float32(1e-4)

	// 以下是可调参数的编译期默认值。唯一读取入口是 Tunables 快照，
	// 不得再以导出常量暴露——见 internal/archcheck 的 TestTunableConstantsAreNotExported。
	defaultEyeHeight          = float32(1.62)
	defaultStepHeight         = float32(0.6)
	defaultWalkSpeed          = float32(4.3)
	defaultGroundAcceleration = float32(40)
	defaultGroundDeceleration = float32(50)
	defaultAirAcceleration    = float32(8)
	defaultJumpSpeed          = float32(8.4)
	defaultGravity            = float32(32)
	defaultTerminalFallSpeed  = float32(78.4)
)
```

`internal/physics/tunables.go` 的 `DefaultTunables` 相应改为引用 `defaultEyeHeight` 等。

`internal/sim` 同理：`RegenDelayTicks` → `defaultRegenDelayTicks`，`DropLifetimeTicks` → `defaultDropLifetimeTicks`，等等；`interactionReach`、`spawnRadius`、`dropPickupRange` 本就未导出，改名为 `default*` 以保持一致。

- [ ] **Step 4: 修复因此断裂的外部读取点**

`cmd/mcgo/main.go:354`：

```go
	// 相机视线高度必须与服务端交互射线原点使用同一份参数，否则玩家瞄准的方块
	// 与服务端判定的方块不是同一个。
	a.camera.Pos = feet.Add(mgl32.Vec3{0, physics.ActiveTunables().EyeHeight, 0})
```

Run: `go build ./...`
Expected: 编译通过。若报出别处引用了被降级的常量，逐一改为快照读取——**这正是本任务的目的，每一处都是原本会静默错位的地方**。

- [ ] **Step 5: 处理测试中的引用**

既有测试若引用了被降级的常量（例如 `internal/sim/death_test.go` 之类），改为 `physics.DefaultTunables().X` 或 `sim.DefaultTunables().X`。**不要改测试的期望值本身**——只改取值方式。

Run: `go test ./... -race`
Expected: PASS

- [ ] **Step 6: 守卫测试转绿**

Run: `go test ./internal/archcheck -race -count=1`
Expected: PASS，两条新守卫都通过。

- [ ] **Step 7: 变异验证守卫**

在 `internal/physics/types.go` 临时加回 `const Gravity = float32(32)`，确认 `TestTunableConstantsAreNotExported` 变红；再在 `internal/sim/engine.go` 临时加 `import "minecraft-go/internal/config"` 与一处引用，确认 `TestOnlyCommandsImportConfig` 变红。**两条都必须变红**，否则守卫是空的。恢复后确认 `git diff` 干净。

- [ ] **Step 8: 提交**

```bash
git add internal/physics internal/sim cmd/mcgo internal/archcheck/deps_test.go
git commit -m "refactor: 可调参数只经快照读取，并加 archcheck 守卫"
```

---

## Task 8: cmd 接线（`--config`、`--dev`、日志装配）

**Files:**
- Modify: `cmd/mcgo/main.go:55-90`（flag 定义）、`main.go:176-195`（run 装配）
- Modify: `cmd/mcgod/main.go:25-70`（options 与 flag）、`:84-112`（run 装配）
- Modify: `cmd/mcgo/main_test.go`、`cmd/mcgod/main_test.go`

**Interfaces:**
- Consumes: `config.Load`、`config.Defaults`、`config.DefaultPath`、`(Config).Apply`、`logging.Install`
- Produces:
  - `mainOptions` 新增 `ConfigPath string` 与 `Dev bool`
  - `mcgod` 的 `options` 新增 `Config string`
  - `applicationOptions` 新增 `Dev bool` 与渲染组三个值

- [ ] **Step 1: 写失败测试**

`cmd/mcgo/main_test.go` 追加：

```go
func TestParseOptionsDefaultsDevOff(t *testing.T) {
	options, err := parseMainOptions([]string{})
	if err != nil {
		t.Fatalf("parseMainOptions: %v", err)
	}
	if options.Dev {
		t.Fatal("--dev 默认必须关闭")
	}
}

func TestParseOptionsAcceptsDevAndConfig(t *testing.T) {
	options, err := parseMainOptions([]string{"--dev", "--config", "/tmp/x.json"})
	if err != nil {
		t.Fatalf("parseMainOptions: %v", err)
	}
	if !options.Dev {
		t.Fatal("--dev 必须被解析")
	}
	if options.ConfigPath != "/tmp/x.json" {
		t.Fatalf("ConfigPath = %q", options.ConfigPath)
	}
}

// TestBenchmarkIgnoresUserConfig 守住"性能门禁不读本机配置"这条不变量。
func TestBenchmarkIgnoresUserConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	custom := config.Defaults()
	custom.Physics.Gravity = 1
	if err := custom.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	t.Cleanup(func() { config.Defaults().Apply() })

	effective, err := resolveConfig(mainOptions{
		ConfigPath: path,
		Application: applicationOptions{Benchmark: true},
	})
	if err != nil {
		t.Fatalf("resolveConfig: %v", err)
	}
	if effective.Physics.Gravity != config.Defaults().Physics.Gravity {
		t.Fatal("benchmark 路径必须使用编译默认值，不得读用户配置")
	}
}

func TestCaptureIgnoresUserConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	custom := config.Defaults()
	custom.Physics.Gravity = 1
	if err := custom.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	t.Cleanup(func() { config.Defaults().Apply() })

	effective, err := resolveConfig(mainOptions{ConfigPath: path, CaptureDir: "out"})
	if err != nil {
		t.Fatalf("resolveConfig: %v", err)
	}
	if effective.Physics.Gravity != config.Defaults().Physics.Gravity {
		t.Fatal("抓帧路径必须使用编译默认值，不得读用户配置")
	}
}
```

> `parseMainOptions` 是占位名。实现时以 `cmd/mcgo/main.go:55` 附近的既有解析函数实名为准（`grep -n "flag.NewFlagSet(\"mcgo\"" -A 3 cmd/mcgo/main.go`），测试用真名。

`cmd/mcgod/main_test.go` 追加：

```go
func TestParseOptionsAcceptsConfig(t *testing.T) {
	options, err := parseOptions([]string{"--config", "/tmp/x.json"})
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	if options.Config != "/tmp/x.json" {
		t.Fatalf("Config = %q", options.Config)
	}
}

func TestParseOptionsConfigDefaultsEmpty(t *testing.T) {
	options, err := parseOptions([]string{})
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	if options.Config != "" {
		t.Fatalf("Config 默认应为空（表示使用默认路径），实际 %q", options.Config)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/mcgo ./cmd/mcgod -run "Config|Dev" -count=1`
Expected: 编译失败，`options.Dev undefined` 等。

- [ ] **Step 3: 实现 `cmd/mcgo` 侧**

flag 定义处新增：

```go
	dev := flags.Bool("dev", false, "启用调试面板（F3 切换）")
	configPath := flags.String("config", "", "配置文件路径，留空使用默认路径")
```

新增 `resolveConfig`：

```go
// resolveConfig 决定本次运行的生效配置。
//
// benchmark 与抓帧路径强制使用编译默认值：这两条路径的产出会与基线比对，
// 若读入本机配置，结论就取决于开发者本机的配置文件内容而非代码。
func resolveConfig(options mainOptions) (config.Config, error) {
	if options.Application.Benchmark || options.CaptureDir != "" {
		return config.Defaults(), nil
	}
	path := options.ConfigPath
	if path == "" {
		var err error
		if path, err = config.DefaultPath(); err != nil {
			return config.Config{}, err
		}
	}
	return config.Load(path)
}
```

`run` 中在创建 application 之前：

```go
	effective, err := resolveConfig(options)
	if err != nil {
		return fmt.Errorf("加载配置: %w", err)
	}
	logging.Install(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}), effective.Logging)
	effective.Apply()
	options.Application.Dev = options.Dev
	options.Application.Render = effective.Render
```

> 内层 handler 的 `Level` 固定为 `LevelDebug`：过滤全部交给 `logging` 包的包装器，内层不得二次过滤，否则模块放宽会失效。

`applicationOptions` 增加 `Dev bool` 与 `Render config.Render` 字段；`app.go` 用 `Render.ViewDistance` 取代 `cmd/mcgo/main.go:27` 的 `viewDistance` 常量，用 `Render.FovDegrees` 取代 `app.go:453` 的 `mgl32.DegToRad(70)`，用 `Render.MouseSensitivity` 取代 `main.go:248` 的两处 `0.002`。

- [ ] **Step 4: 实现 `cmd/mcgod` 侧**

`options` 新增 `Config string`，flag 为 `--config`（默认空串表示用默认路径）。`run` 中在打开世界之前加载配置、装日志、`Apply()`。**忽略渲染组**——`mcgod` 不消费它。

`dependencies.logger` 保持存在（测试注入用），但默认值改为在 `logging.Install` 之后取 `slog.Default()`。

- [ ] **Step 5: 跑测试确认转绿**

Run: `go test ./cmd/mcgo ./cmd/mcgod -race -count=1`
Expected: PASS

- [ ] **Step 6: 确认 mcgod 仍无图形依赖**

Run: `go test ./internal/archcheck -run MCGod -count=1`
Expected: PASS

Run: `GOOS=linux CGO_ENABLED=0 go build ./cmd/mcgod`
Expected: 编译通过。

- [ ] **Step 7: 提交**

```bash
git add cmd/mcgo cmd/mcgod
git commit -m "feat: cmd 接入配置文件加载、日志分级装配与 --dev 门控"
```

---

## Task 9: 调试面板渲染器

**Files:**
- Create: `internal/render/debug_panel.go`
- Create: `internal/render/shader/debug_panel.wgsl`
- Create: `internal/render/debug_panel_test.go`

**Interfaces:**
- Consumes: `GlyphSource`（`internal/render/name_tag.go:49`）、`UploadBudget`、`gfx.Device`
- Produces:
  - `type PanelRow struct { Label, Value string; ReadOnly, Selected bool }`
  - `type PanelReadout struct { FrameMillis float64; Position mgl32.Vec3; Yaw, Pitch float32; Tick uint64; WorldTime uint64; LoadedChunks int; Mode string }`
  - `func NewDebugPanelRenderer(dev gfx.Device, colorFormat gfx.TextureFormat, atlas GlyphSource) *DebugPanelRenderer`
  - `func (*DebugPanelRenderer) Prepare(visible bool, readout PanelReadout, rows []PanelRow, width, height uint32, budget *UploadBudget) error`
  - `func (*DebugPanelRenderer) Render(encoder gfx.CommandEncoder, target gfx.TextureView)`
  - `func (*DebugPanelRenderer) Release()`

- [ ] **Step 1: 写 shader**

`internal/render/shader/debug_panel.wgsl` 与 `hotbar.wgsl` 结构完全一致（同样的 `Viewport`、`Instance`、`quads`/`glyphs` 两个 storage 数组、`quad_vs`/`quad_fs`/`glyph_vs`/`glyph_fs` 四个入口）。直接复制 `hotbar.wgsl` 并把首行注释改为"调试面板：屏幕空间实例化矩形与字形"。

> **不共用 `hotbar.wgsl`（已经用户裁定，评审若标为重复请引用本条）**：`hotbar.wgsl` 本身不含容量常量，技术上可以共用；这里选择各持一份，是为了让 HUD 与调试面板的呈现方式可以各自演进——改其中一个的混合模式、加一条描边或换 UV 语义时，不会波及另一个。代价是两份约 67 行的重复，这是明确接受的取舍。

- [ ] **Step 2: 写失败测试**

创建 `internal/render/debug_panel_test.go`。参考 `internal/render/hotbar_test.go` 的假设备（fake device）与假字形源模式——先 `grep -n "type fake\|func newFake" internal/render/hotbar_test.go` 找到既有辅助类型并复用，**不要新造一套**。

```go
func TestDebugPanelInvisibleProducesNoInstances(t *testing.T) {
	renderer, _ := newTestPanelRenderer(t)
	if err := renderer.Prepare(false, PanelReadout{}, samplePanelRows(), 1280, 720, nil); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got := renderer.QuadCount(); got != 0 {
		t.Fatalf("关闭时不得产出实例，实际 %d 个矩形", got)
	}
	if got := renderer.GlyphCount(); got != 0 {
		t.Fatalf("关闭时不得产出字形，实际 %d 个", got)
	}
}

func TestDebugPanelRespectsRowCap(t *testing.T) {
	renderer, _ := newTestPanelRenderer(t)
	rows := make([]PanelRow, maxPanelRows*3)
	for i := range rows {
		rows[i] = PanelRow{Label: "参数", Value: "1.0"}
	}
	if err := renderer.Prepare(true, PanelReadout{}, rows, 1280, 720, nil); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got := renderer.QuadCount(); got > maxPanelQuads {
		t.Fatalf("矩形数 %d 超过上限 %d", got, maxPanelQuads)
	}
	if got := renderer.GlyphCount(); got > maxPanelGlyphs {
		t.Fatalf("字形数 %d 超过上限 %d", got, maxPanelGlyphs)
	}
}

func TestDebugPanelTruncatesLongText(t *testing.T) {
	renderer, _ := newTestPanelRenderer(t)
	rows := []PanelRow{{Label: strings.Repeat("超长标签", 100), Value: strings.Repeat("9", 100)}}
	if err := renderer.Prepare(true, PanelReadout{}, rows, 1280, 720, nil); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got := renderer.GlyphCount(); got > maxPanelGlyphs {
		t.Fatalf("超长文本必须截断，字形数 %d 超过上限 %d", got, maxPanelGlyphs)
	}
}

func TestDebugPanelReadOnlyRowUsesDimColor(t *testing.T) {
	renderer, _ := newTestPanelRenderer(t)
	rows := []PanelRow{{Label: "重力", Value: "32", ReadOnly: true}}
	if err := renderer.Prepare(true, PanelReadout{}, rows, 1280, 720, nil); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !renderer.hasDimmedGlyph() {
		t.Fatal("只读行必须以暗色绘制，以便一眼区分不可编辑的参数")
	}
}

func TestDebugPanelSelectedRowHasHighlight(t *testing.T) {
	renderer, _ := newTestPanelRenderer(t)
	rows := []PanelRow{{Label: "重力", Value: "32"}, {Label: "跳跃", Value: "8.4", Selected: true}}
	before := 0
	if err := renderer.Prepare(true, PanelReadout{}, rows[:1], 1280, 720, nil); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	before = renderer.QuadCount()
	if err := renderer.Prepare(true, PanelReadout{}, rows, 1280, 720, nil); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if renderer.QuadCount() <= before+1 {
		t.Fatal("选中行必须额外产出高亮矩形")
	}
}
```

> `QuadCount()`、`GlyphCount()`、`hasDimmedGlyph()` 是为测试暴露的只读访问器，写在 `debug_panel.go` 中并标注"仅供测试断言布局用"，或写成 `debug_panel_internal_test.go` 里直接读私有字段——参照 `internal/render/hiz_internal_test.go` 的既有做法二选一，保持与仓库一致。

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./internal/render -run DebugPanel -count=1`
Expected: 编译失败，`undefined: NewDebugPanelRenderer`。

- [ ] **Step 4: 实现 `internal/render/debug_panel.go`**

容量常量按最坏布局固定：

```go
const (
	// maxPanelRows 是参数行的固定上限。config.Fields() 目前约 20 项，
	// 留出余量到 64；超出部分不绘制，测试守住这条上限。
	maxPanelRows = 64
	// maxPanelReadoutRows 是顶部读数行数：帧时、坐标、朝向、tick、时刻、区块数、模式。
	maxPanelReadoutRows = 7
	// 每行最多的字形数：标签与数值各截断到 maxPanelRunesPerSide。
	maxPanelRunesPerSide = 24

	maxPanelQuads = 1 + maxPanelRows // 面板背景 + 每行至多一个选中高亮
	maxPanelGlyphs = (maxPanelRows + maxPanelReadoutRows) * maxPanelRunesPerSide * 2

	panelInstanceBytes  = 48
	panelViewportOffset = 0
	panelViewportBytes  = 16
	panelQuadOffset     = 256
	panelQuadSize       = maxPanelQuads * panelInstanceBytes
	panelGlyphOffset    = (panelQuadOffset + panelQuadSize + 255) &^ 255
	panelGlyphSize      = maxPanelGlyphs * panelInstanceBytes
	panelUploadBytes    = panelGlyphOffset + panelGlyphSize
)
```

结构、构造函数、`Prepare`、`Render`、`Release` 全部照搬 `internal/render/hotbar.go:98-252` 的形态：同样的 `dynamic` buffer + 两条 pipeline + 一个 bind group，`Prepare` 里 `atlas.Request(...)` → `atlas.FlushUploads(budget)` → 布局 → `encode*`，`Render` 里 `len(quads) == 0` 时直接返回。

`Prepare` 的第一件事：

```go
	renderer.layout.quads = renderer.layout.quads[:0]
	renderer.layout.glyphs = renderer.layout.glyphs[:0]
	if !visible {
		return nil
	}
```

字形请求：把本帧要画的所有文本拼一次交给 `atlas.Request`。文本按 `maxPanelRunesPerSide` 以 rune 为单位截断（不是按字节，中文标签会被切坏）。

只读行用暗色（例如 `[4]float32{0.5, 0.5, 0.5, 1}`），可编辑行用亮色，选中行额外产出一个高亮背景矩形。

- [ ] **Step 5: 跑测试确认转绿**

Run: `go test ./internal/render -race -count=1`
Expected: PASS

- [ ] **Step 6: 确认面板关闭时零开销**

Run: `go test ./internal/render -bench . -run '^$' -count=1`
Expected: 既有 benchmark 无退化。若 `internal/render/bench_test.go` 中没有覆盖 HUD 路径的用例，补一个 `BenchmarkDebugPanelHidden`，断言 `Prepare(false, ...)` 不分配：

```go
func BenchmarkDebugPanelHidden(b *testing.B) {
	renderer, _ := newBenchPanelRenderer(b)
	rows := samplePanelRows()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := renderer.Prepare(false, PanelReadout{}, rows, 1280, 720, nil); err != nil {
			b.Fatal(err)
		}
	}
}
```

Expected: `0 allocs/op`。

- [ ] **Step 7: 提交**

```bash
git add internal/render/debug_panel.go internal/render/debug_panel_test.go internal/render/shader/debug_panel.wgsl
git commit -m "feat: 增加调试面板渲染器"
```

---

## Task 10: 面板交互接线

**Files:**
- Modify: `internal/client/window.go:17-59`（`Key` 枚举与 `glfwKeys` 表）
- Create: `cmd/mcgo/debug_panel.go`
- Create: `cmd/mcgo/debug_panel_test.go`
- Modify: `cmd/mcgo/app.go`（渲染器字段、构造、`Prepare`/`Render` 接线、`Release`）
- Modify: `cmd/mcgo/main.go`（`runInteractive` 的按键处理）

**Interfaces:**
- Consumes: `render.NewDebugPanelRenderer`、`render.PanelRow`、`render.PanelReadout`、`config.Fields`、`config.Config`、`physics.SetTunables`、`sim.SetTunables`
- Produces:
  - `type panelState struct { visible bool; selected int; effective config.Config }`
  - `func newPanelState(effective config.Config) *panelState`
  - `func (*panelState) rows(remote bool) []render.PanelRow`
  - `func (*panelState) handleKeys(keys panelKeys, remote bool) (changed bool)`
  - `func (*panelState) save(path string) error`

- [ ] **Step 1: 扩充按键枚举**

`internal/client/window.go` 的 `Key` 常量块尾部追加 `KeyF3`、`KeyF5`、`KeyF6`、`KeyUp`、`KeyDown`、`KeyLeft`、`KeyRight`、`KeyEnter`、`KeyLeftAlt`，并在 `glfwKeys` 表中登记对应的 `glfw.KeyF3`、`glfw.KeyF5`、`glfw.KeyF6`、`glfw.KeyUp`、`glfw.KeyDown`、`glfw.KeyLeft`、`glfw.KeyRight`、`glfw.KeyEnter`、`glfw.KeyLeftAlt`。

> 追加在末尾以免改变既有常量的 iota 取值。`glfwKeys` 是索引数组，顺序必须与枚举一致。

- [ ] **Step 2: 写失败测试**

`cmd/mcgo/debug_panel_test.go`。**这些测试全部是纯逻辑的，不创建窗口、不初始化 GPU**——按键以结构体形式注入：

```go
func TestPanelRowsMarkAuthoritativeGroupsReadOnlyWhenRemote(t *testing.T) {
	state := newPanelState(config.Defaults())
	for _, row := range state.rows(true) {
		if strings.HasPrefix(row.Label, "physics.") || strings.HasPrefix(row.Label, "sim.") {
			if !row.ReadOnly {
				t.Fatalf("联机时 %s 必须只读", row.Label)
			}
		}
	}
}

func TestPanelRowsAllowAuthoritativeGroupsWhenLocal(t *testing.T) {
	state := newPanelState(config.Defaults())
	editable := 0
	for _, row := range state.rows(false) {
		if strings.HasPrefix(row.Label, "physics.") && !row.ReadOnly {
			editable++
		}
	}
	if editable == 0 {
		t.Fatal("单机时物理组必须可编辑")
	}
}

func TestPanelViewDistanceIsAlwaysReadOnly(t *testing.T) {
	state := newPanelState(config.Defaults())
	for _, remote := range []bool{false, true} {
		for _, row := range state.rows(remote) {
			if row.Label == "render.viewDistance" && !row.ReadOnly {
				t.Fatalf("viewDistance 在 remote=%v 下也必须只读（重启生效）", remote)
			}
		}
	}
}

func TestPanelArrowAdjustsSelectedValue(t *testing.T) {
	state := newPanelState(config.Defaults())
	state.visible = true
	state.selectFieldForTest(t, "physics.gravity")

	before := state.effective.Physics.Gravity
	state.handleKeys(panelKeys{Right: true}, false)
	if state.effective.Physics.Gravity <= before {
		t.Fatalf("右方向键必须增大取值：%v -> %v", before, state.effective.Physics.Gravity)
	}
	state.handleKeys(panelKeys{Left: true}, false)
	if state.effective.Physics.Gravity != before {
		t.Fatalf("左方向键必须还原一步：%v，want %v", state.effective.Physics.Gravity, before)
	}
}

func TestPanelShiftCoarseAndAltFine(t *testing.T) {
	state := newPanelState(config.Defaults())
	state.visible = true
	state.selectFieldForTest(t, "physics.gravity")
	base := state.effective.Physics.Gravity

	state.handleKeys(panelKeys{Right: true}, false)
	fine := state.effective.Physics.Gravity - base
	state.effective.Physics.Gravity = base

	state.handleKeys(panelKeys{Right: true, Shift: true}, false)
	coarse := state.effective.Physics.Gravity - base
	if coarse <= fine {
		t.Fatalf("Shift 必须是粗调：coarse=%v fine=%v", coarse, fine)
	}
}

func TestPanelRejectsEditsOnReadOnlyRow(t *testing.T) {
	state := newPanelState(config.Defaults())
	state.visible = true
	state.selectFieldForTest(t, "physics.gravity")
	before := state.effective.Physics.Gravity
	state.handleKeys(panelKeys{Right: true}, true) // remote=true
	if state.effective.Physics.Gravity != before {
		t.Fatal("联机时不得修改权威参数")
	}
}

func TestPanelEnterResetsRowToDefault(t *testing.T) {
	state := newPanelState(config.Defaults())
	state.visible = true
	state.selectFieldForTest(t, "physics.gravity")
	state.handleKeys(panelKeys{Right: true}, false)
	state.handleKeys(panelKeys{Enter: true}, false)
	if state.effective.Physics.Gravity != config.Defaults().Physics.Gravity {
		t.Fatal("Enter 必须把当前行重置为默认值")
	}
}

func TestPanelClampsAtBounds(t *testing.T) {
	state := newPanelState(config.Defaults())
	state.visible = true
	state.selectFieldForTest(t, "sim.spawnRadius")
	for i := 0; i < 10000; i++ {
		state.handleKeys(panelKeys{Right: true, Shift: true}, false)
	}
	if state.effective.Sim.SpawnRadius > 64 {
		t.Fatalf("SpawnRadius = %v，必须钳在上界 64", state.effective.Sim.SpawnRadius)
	}
}

func TestPanelNavigationSkipsReadOnlyRows(t *testing.T) {
	state := newPanelState(config.Defaults())
	state.visible = true
	state.selected = 0
	for i := 0; i < 200; i++ {
		state.handleKeys(panelKeys{Down: true}, true)
		if state.rows(true)[state.selected].ReadOnly {
			t.Fatal("导航必须跳过只读行")
		}
	}
}

func TestPanelSaveWritesFile(t *testing.T) {
	state := newPanelState(config.Defaults())
	path := filepath.Join(t.TempDir(), "config.json")
	if err := state.save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("保存后文件必须存在: %v", err)
	}
}
```

> `selectFieldForTest(t, name)` 是测试辅助方法，按 `Group + "." + Name` 查找并设置 `selected`；找不到时 `t.Fatalf`。写在 `debug_panel_test.go` 中。

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./cmd/mcgo -run Panel -count=1`
Expected: 编译失败，`undefined: newPanelState`。

- [ ] **Step 4: 实现 `cmd/mcgo/debug_panel.go`**

```go
// panelKeys 是本帧的面板按键边沿状态。以结构体注入而非直接查询窗口，
// 使面板逻辑可以在不创建窗口、不初始化 GPU 的情况下被测试。
type panelKeys struct {
	Toggle bool // F3
	Up, Down, Left, Right bool
	Enter bool // 重置当前行
	Save  bool // F5
	ResetAll bool // F6
	Shift bool // ×10 粗调
	Alt   bool // ×0.1 细调
}
```

`rows(remote bool)` 遍历 `config.Fields()`，为每项生成 `render.PanelRow`：`Label` 用 `Group + "." + Name`，`Value` 按字段类型格式化；`ReadOnly` 为 `field.ReadOnly || (remote && (field.Group == "physics" || field.Group == "sim"))`。

`handleKeys`：`Toggle` 切换 `visible`；不可见时直接返回。`Up`/`Down` 移动 `selected` 并跳过只读行；`Left`/`Right` 按 `field.Step` 增减，`Shift` 乘 10、`Alt` 乘 0.1，结果钳到 `[field.Min, field.Max]`；只读行忽略增减；`Enter` 重置当前行；`ResetAll` 整体重置为 `config.Defaults()`。任何改动后返回 `changed = true`。

调用方在 `changed` 为真时调用 `physics.SetTunables(state.effective.Physics)` 与 `sim.SetTunables(state.effective.Sim)`——**联机时这两行也照常执行，因为联机下这两组的值根本没被改动**（`handleKeys` 已拒绝）。渲染组的值由调用方直接读 `state.effective.Render` 应用到相机与灵敏度。

- [ ] **Step 5: 接进 `cmd/mcgo/app.go`**

按 `hotbarRenderer` 的完全相同模式加 `debugPanelRenderer *render.DebugPanelRenderer` 字段：构造（`app.go:511` 附近）、`Release`（`:541`、`:781` 两处）、`Prepare`（`:947` 附近，在 hotbar 之后）、`Render`（`:1017` 之后，面板是最上层）。

**仅当 `options.Dev` 为真时才创建面板渲染器**；为假时字段保持 `nil`，所有接线处按既有的 `if a.hotbarRenderer != nil` 模式做 nil 判断。

- [ ] **Step 6: 接进 `cmd/mcgo/main.go` 的 `runInteractive`**

在既有的 `escapeDown` 边沿检测模式旁边，加一组面板按键的边沿检测（沿用 `xxxWasDown` 局部变量的写法），组装 `panelKeys` 并调用 `handleKeys`。面板可见时**必须吞掉方向键**，不要让它们同时驱动玩家移动。

`remote` 参数取自 application 已有的连接模式判断（`grep -n "Benchmark\|remote\|Connect" cmd/mcgo/app.go` 找到既有的单机/联机区分字段）。

- [ ] **Step 7: 跑测试确认转绿**

Run: `go test ./cmd/mcgo ./internal/client -race -count=1`
Expected: PASS

- [ ] **Step 8: 确认没有引入窗口依赖的测试**

Run: `go test ./cmd/mcgo -run Panel -count=1 -v`
Expected: 全部通过且不弹出任何窗口。若测试触发了 GLFW 初始化，说明面板逻辑与窗口耦合了，退回 Step 4 把窗口查询移到调用方。

- [ ] **Step 9: 提交**

```bash
git add internal/client/window.go cmd/mcgo
git commit -m "feat: 接入调试面板的按键交互与渲染"
```

---

## Task 11: 收尾门禁与文档

**Files:**
- Modify: `README.md`（新增配置文件与 `--dev` 的说明段落）
- Modify: `openspec/changes/logging-and-debug-tuning/tasks.md`（勾选全部任务）

- [ ] **Step 1: 全量门禁**

```bash
gofmt -l .
go vet ./...
go test ./... -race
go test ./internal/archcheck -count=1
```

Expected: `gofmt -l .` 无输出；`go vet` 无输出；测试全部 PASS。

- [ ] **Step 2: 性能门禁**

Run: `go test ./internal/render ./internal/sim ./internal/physics -bench . -run '^$' -count=1`
Expected: 与改动前基线相比无退化。**记录改前改后的数值**——物理快照是每固定步一次原子读，若固定步 benchmark 出现可测量的变慢，说明快照被放在了比预期更内层的位置，退回 Task 4 检查。

按 `cmd/perfcheck` 的既有用法比对基线（`go run ./cmd/perfcheck -baseline <基线> -current <本次>`），确认无超阈退化。

- [ ] **Step 3: 跨平台构建**

```bash
GOOS=linux CGO_ENABLED=0 go build ./cmd/mcgod
go build ./...
```

Expected: 均通过。

- [ ] **Step 4: 人工验收（仅在用户明确要求时执行）**

自动测试不得启动前台窗口，因此以下步骤**只在用户要求人工验收时**手动执行：

1. `go run ./cmd/mcgo --dev`，按 F3 打开面板，确认读数区更新、方向键可调物理参数、Shift/Alt 步长不同、Enter 重置、F5 保存。
2. 确认 `~/Library/Application Support/minecraft-go/config.json` 已生成且内容合法。
3. 不带 `--dev` 重启，确认 F3 无反应，但上一步保存的参数**仍然生效**（这是设计 §3.1 的语义）。
4. 起 `go run ./cmd/mcgod` 并用 `go run ./cmd/mcgo --dev --connect <地址>` 连上，确认面板中 physics 与 sim 两组灰显只读、render 组仍可调。

- [ ] **Step 5: 更新 README**

在 README 中新增一节，说明配置文件路径与三个分组、`--config` 与 `--dev` 旗标、日志等级配置方式，以及"配置文件始终生效，`--dev` 只控制面板可见性"这条语义。

- [ ] **Step 6: 勾选 OpenSpec 任务并校验**

Run: `openspec validate --all --strict --no-interactive`
Expected: 通过。

- [ ] **Step 7: 提交**

```bash
git add README.md openspec/changes/logging-and-debug-tuning/tasks.md
git commit -m "docs: 补齐配置文件与调试面板说明并勾选任务"
```

---

## 归档

全部任务完成并通过门禁后，按 `docs/openspec.md` 的流程归档 change，把 `module-scoped-logging` 与 `tunable-constants` 两条能力合入 `openspec/specs/`。归档前再次确认任务状态、实现与 delta specs 三者一致。
