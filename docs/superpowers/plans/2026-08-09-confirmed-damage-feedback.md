# 权威受伤红屏反馈 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 图形客户端仅在确认生命值下降时显示 `180ms` 的红色边缘遮罩，并在零强度路径保持零额外 pass、零每帧分配。

**Architecture:** `cmd/mcgo` 持有只消费 `Predictor.Health()` 的纯呈现状态机，`internal/render` 持有一个固定 uniform、固定 alpha-blend pipeline 的全屏三角形 renderer。application 每帧先更新归一化强度，再按“世界与实体 → 受伤遮罩 → HUD → 调试面板”的顺序编码；协议、存档、模拟与既有 HUD 均不改变。

**Tech Stack:** Go 1.26、项目内 `internal/gfx` WebGPU 抽象、WGSL、OpenSpec `spec-driven`、标准库 `testing`。

## Global Constraints

- 服务端仍是生命值唯一权威；客户端 MUST NOT 根据碰撞、落差或本地预测触发反馈。
- 首次 ready、生命值不变或增加、Predictor not-ready MUST NOT 开始新反馈；not-ready 与会话清理必须清除旧反馈。
- 固定持续时间 `180ms`；新伤害当前帧输出强度 `1`，后续帧线性淡出；连续伤害重置完整持续时间。
- shader 线性 RGB 固定为 `vec3f(0.65, 0.0, 0.0)`；alpha 固定为 `0.30 * strength * edgeFactor`，其中 `edgeFactor = 1 - smoothstep(0, 0.35, edgeDistance)`。
- overlay 必须位于 name tag 之后、HUD 与调试面板之前；强度 `<=0` 或 NaN 时不得写 uniform、创建 pass 或 draw。
- 不新增依赖、配置项、二进制资源、通用 effect manager、中间纹理、goroutine、锁或异步 I/O。
- 本任务不得修改任何协议、schema、metadata 或 scenario 版本常量。当前规划基线是协议 v13、玩家 schema v5、区块 schema v6、metadata v2、scenario v14；若执行或合并前 M4N 已提升其中版本，必须保留 M4N 值，绝不能降级到本段旧基线。
- 不修改 `internal/render/hotbar.go`、terrain shader、材质/mesher、`cmd/mcgo/capture.go` 或视觉 golden。
- Go 注释、OpenSpec、README 与测试说明使用中文；Go/WGSL 标识符和既有技术术语保留英文。
- 自动测试不得启动或聚焦前台窗口；headless GPU 测试只在确实没有 adapter 时跳过。
- 保留 `midscene_run/log/` 下三份用户日志改动；所有 `git add` 必须精确点名本任务文件。
- 实现执行开始时用 `superpowers:using-git-worktrees` 从本规划提交建立独立 worktree；不得复用 M4N 工作树。

## 文件映射

| 文件 | 职责 |
| --- | --- |
| `openspec/changes/confirmed-damage-feedback/.openspec.yaml` | 固定 `spec-driven` schema 与创建日期 |
| `openspec/changes/confirmed-damage-feedback/proposal.md` | 说明为什么做、范围、兼容性与受影响包 |
| `openspec/changes/confirmed-damage-feedback/specs/authoritative-health/spec.md` | 修订旧的“不得新增 pipeline/shader”约束，写入可观察反馈契约 |
| `openspec/changes/confirmed-damage-feedback/design.md` | 冻结状态所有权、绘制顺序、资源与并行边界 |
| `openspec/changes/confirmed-damage-feedback/tasks.md` | 与本计划 Task 2..4 一一对应的执行清单 |
| `cmd/mcgo/damage_feedback.go` | 纯确认生命值基线与 `180ms` 淡出状态机 |
| `cmd/mcgo/damage_feedback_test.go` | 状态机、application 接线、会话 reset 与绘制顺序测试 |
| `internal/render/damage_overlay.go` | 固定资源的 overlay renderer 与幂等释放 |
| `internal/render/shader/damage_overlay.wgsl` | 全屏三角形与固定红色边缘渐变 |
| `internal/render/damage_overlay_test.go` | fake gfx、headless 像素与 hidden benchmark |
| `cmd/mcgo/app.go` | renderer 构造/释放、状态更新和绘制顺序接线 |
| `cmd/mcgo/app_test.go` | 共用 render fixture 与资源生命周期断言 |
| `README.md` | 把“尚无伤害动画反馈”更新为实际能力 |

---

### Task 1: 建立并冻结 OpenSpec change

**Files:**
- Create: `openspec/changes/confirmed-damage-feedback/.openspec.yaml`
- Create: `openspec/changes/confirmed-damage-feedback/proposal.md`
- Create: `openspec/changes/confirmed-damage-feedback/specs/authoritative-health/spec.md`
- Create: `openspec/changes/confirmed-damage-feedback/design.md`
- Create: `openspec/changes/confirmed-damage-feedback/tasks.md`

**Interfaces:**
- Consumes: 已批准设计 `docs/superpowers/specs/2026-08-09-confirmed-damage-feedback-design.md`；主规格 `openspec/specs/authoritative-health/spec.md`。
- Produces: active change `confirmed-damage-feedback`；Task 2、3 只能实现其 `tasks.md`，不得扩大到音效、死亡 UI、预测或通用后处理。

- [ ] **Step 1: 创建 change 元数据和 proposal**

用 `apply_patch` 创建 `.openspec.yaml`：

```yaml
schema: spec-driven
created: 2026-08-09
```

创建 `proposal.md`，内容固定为：

```markdown
## Why

M4M 已有服务端权威生命值与确认生命 HUD，但确认生命值下降时没有即时画面反馈，玩家在移动中难以察觉刚刚受伤。该变更以一个客户端本地呈现闭环补足反馈，同时保持服务端权威、协议和存档不变。

## What Changes

- 客户端只比较 `Predictor.Health()` 的确认值；首次同步、回复、满血重生和 not-ready 不触发。
- 确认生命值下降时显示 `180ms` 红色边缘遮罩，连续下降重置计时。
- 新增一个固定资源、单全屏三角形的 alpha-blend renderer；零强度不提交 pass。
- 遮罩绘制在世界/name tag 之后、HUD/调试面板之前。
- README 更新当前能力；不更新共享 capture golden。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `authoritative-health`：允许专用受伤反馈 pipeline，并增加仅由确认生命值下降触发的时序、层级和 reset 契约。

## Impact

影响 `cmd/mcgo`、`internal/render` 与 README。无协议、存档、模拟、配置、并发或第三方依赖变化；非激活热路径只增加 O(1) 状态更新和一次分支，不新增 pass、GPU 写入或堆分配。M4N 唯一可能重叠的是 README 当前状态句，生产代码文件不重叠。
```

- [ ] **Step 2: 写入 authoritative-health delta spec**

主规格现有“图形客户端显示权威生命值”要求明确写着 `MUST NOT 新增渲染 pipeline、着色器`，与已批准设计冲突。不得只追加新 Requirement；必须在 `## MODIFIED Requirements` 中完整替换该 Requirement，保留原场景并移除旧禁令：

```markdown
## MODIFIED Requirements

### Requirement: 图形客户端显示权威生命值

图形客户端 SHALL 显示服务端确认的生命值，客户端 MUST NOT 预测伤害或回复。客户端已有上一份确认生命值基线且新确认值更低时，系统 SHALL 立即显示红色屏幕边缘反馈；反馈 MUST 持续 `180ms` 并按剩余时间线性淡出，连续确认下降 MUST 重新开始完整持续时间。首次确认值、生命值不变或增加、Predictor not-ready MUST NOT 开始新反馈；not-ready 与会话清理 MUST 清除旧反馈。反馈 MUST 覆盖世界画面但位于生命值、背包、容器 HUD 与调试面板下方。

#### Scenario: 显示服务端确认值

- **GIVEN** 客户端收到生命值为 12 的权威玩家状态
- **WHEN** 客户端绘制 HUD
- **THEN** HUD MUST 显示 12
- **AND** 在收到该状态之前 MUST NOT 显示预测值

#### Scenario: 首次确认只建立基线

- **GIVEN** Predictor 尚未 ready
- **WHEN** 客户端首次收到生命值为 12 的 ready 权威状态
- **THEN** HUD MUST 显示 12
- **AND** 受伤反馈 MUST NOT 出现

#### Scenario: 确认生命值下降立即触发并线性淡出

- **GIVEN** 客户端上一份确认生命值为 12
- **WHEN** 新确认生命值变为 7
- **THEN** 当前帧屏幕边缘反馈透明度 MUST 为峰值
- **AND** 经过 90ms 且没有新伤害时，屏幕边缘反馈透明度 MUST 为峰值的一半
- **AND** 经过完整 180ms 后反馈 MUST 消失

#### Scenario: 连续确认伤害重置计时

- **GIVEN** 一次确认伤害的反馈已淡出 90ms
- **WHEN** 确认生命值再次下降
- **THEN** 当前帧屏幕边缘反馈透明度 MUST 恢复峰值
- **AND** MUST 再保持一个完整 180ms 的淡出周期

#### Scenario: 回复与重生不误触发

- **GIVEN** 客户端已有一份确认生命值基线
- **WHEN** 新确认生命值不变或增加
- **THEN** 系统 MUST NOT 开始新的受伤反馈

#### Scenario: not-ready 清除旧反馈

- **GIVEN** 受伤反馈仍在显示
- **WHEN** Predictor 变为 not-ready 或客户端会话被清理
- **THEN** 反馈 MUST 立即消失
- **AND** 下一次 ready 的首份生命值 MUST 只建立新基线

#### Scenario: 反馈不染色 HUD

- **GIVEN** 受伤反馈、生命 HUD、容器 HUD 与调试面板同时可见
- **WHEN** 客户端绘制一帧
- **THEN** 世界画面边缘 MUST 显示红色反馈
- **AND** 生命 HUD、容器 HUD 与调试面板 MUST 保持原色且清晰可读

#### Scenario: 自动验证不打开窗口

- **GIVEN** CI 或开发者运行生命值、反馈或渲染测试
- **WHEN** 自动验证执行
- **THEN** 测试 MUST 可在不启动或聚焦交互式游戏窗口的情况下完成
```

- [ ] **Step 3: 写入 OpenSpec design 与 tasks**

`design.md` 只写实现选择，固定以下结构和决策：

```markdown
# 确认受伤红屏反馈设计

## 状态与数据流

`cmd/mcgo` 持有 `damageFeedback`，只消费 `Predictor.Health()`；`internal/render` 只消费归一化强度，不知道生命值语义。正常帧顺序为 drain → receiver 错误检查 → 更新反馈 → renderFrame。会话清理主动 reset。

## 时序

固定持续时间为 180ms。首次 ready 只建立基线；确认下降当帧返回 1 且不扣 elapsed；其余帧仅在 elapsed>0 时衰减；elapsed>=remaining 清零；连续下降重置。增加与不变不启动新效果，已有效果继续衰减。

## renderer

`DamageOverlayRenderer` 持有一个 16 字节 uniform、一个 bind group 与一个 alpha-blend pipeline。WGSL 用全屏三角形和固定 `vec3f(0.65,0,0)`；alpha 为 `0.30 * strength * (1-smoothstep(0,0.35,edgeDistance))`。强度<=0 或 NaN 时直接返回；活动时一次 uniform 写、一个 pass、一次三顶点 draw。Release 幂等。

## 生命周期与绘制顺序

application 在 item-drop renderer 后、可选 debug-panel renderer 前创建 overlay，并按逆序释放。绘制顺序固定为 terrain/实体/name tag → overlay → HUD → debug panel。构造失败沿用 application 既有 errors.Join 与逆序清理。

## 并行、兼容与性能

生产代码只修改 app.go 并新增独立文件，不触碰 M4N 的 hotbar、terrain、assets、mesh 或 capture。README 可能有一行文本冲突，后合入方重放该句。本变更不写任何协议、schema、metadata 或 scenario 版本常量；当前分支上的 v13/v5/v6/v2/v14 保持原值，若先合入 M4N 则保持 M4N 的新值。非激活路径不得提交 pass、写 GPU 或分配。

## 被否决方案

- 爱心闪烁会修改 M4N 的 hotbar 文件且不够醒目。
- 通用 effect/post-processing 系统没有第二个消费者。
- 本地预测受伤破坏服务端唯一权威。
- capture golden 与 M4N 共享文件；本变更改用独立 headless 像素测试。
```

`tasks.md` 固定为：

```markdown
# 确认受伤红屏反馈任务

## 1. 确认生命值反馈状态机

- [ ] 1.1 在 `cmd/mcgo/damage_feedback_test.go` 覆盖首次基线、确认下降、回复、不变、连续下降、elapsed 边界与 reset。
- [ ] 1.2 在 `cmd/mcgo/damage_feedback.go` 实现固定 180ms 的最小值类型状态机。
- [ ] 1.3 运行 `go test ./cmd/mcgo -run '^TestDamageFeedback' -race -count=1`。

## 2. overlay renderer 与 application 接线

- [ ] 2.1 为固定资源、零强度路径、钳制、三顶点 draw、幂等释放和 headless 像素写失败测试。
- [ ] 2.2 实现 `DamageOverlayRenderer` 与固定 WGSL，不引入通用特效框架。
- [ ] 2.3 接入 application 构造、逆序释放、会话 reset、frame 更新和 name tag/HUD 间的绘制顺序。
- [ ] 2.4 更新共享 test fixture、生命周期断言与 README 当前能力。
- [ ] 2.5 运行 `go test ./cmd/mcgo ./internal/render -race -count=1` 与 hidden benchmark。

## 3. 验证与归档

- [ ] 3.1 运行架构、全仓 race、vet、gofmt 与 OpenSpec strict 门禁。
- [ ] 3.2 确认协议、存档、scenario、capture 与 golden 均未变化，工作区只剩用户日志。
- [ ] 3.3 归档 `confirmed-damage-feedback` 并再次严格验证主规格。
```

- [ ] **Step 4: 严格验证 change**

Run:

```bash
openspec status --change confirmed-damage-feedback --json
openspec validate confirmed-damage-feedback --strict --no-interactive
openspec validate --all --strict --no-interactive
```

Expected: status 显示 proposal/specs/design/tasks 全部 complete；三条命令 exit 0；delta 只有 `authoritative-health` 的一条 MODIFIED Requirement。

- [ ] **Step 5: 提交 OpenSpec 产物**

```bash
git add openspec/changes/confirmed-damage-feedback
git diff --cached --check
git commit -m "spec: 冻结确认受伤红屏反馈契约"
```

Expected: 提交只包含五类 change 产物；三份 `midscene_run/log/` 文件保持未暂存。

---

### Task 2: 实现确认生命值反馈状态机

**Files:**
- Create: `cmd/mcgo/damage_feedback.go`
- Create: `cmd/mcgo/damage_feedback_test.go`
- Modify: `openspec/changes/confirmed-damage-feedback/tasks.md`

**Interfaces:**
- Consumes: `health uint8` 与 `ready bool` 来自 `client.Predictor.Health()`；`elapsed time.Duration` 来自 application `frame`。
- Produces: `const damageFeedbackDuration = 180 * time.Millisecond`；`type damageFeedback`；`func (*damageFeedback) Update(uint8, bool, time.Duration) float32`；`func (*damageFeedback) Reset()`。

- [ ] **Step 1: 写状态机失败测试**

创建带 `//go:build darwin` 的 `cmd/mcgo/damage_feedback_test.go`：

```go
//go:build darwin

package main

import (
	"testing"
	"time"
)

func TestDamageFeedbackUsesOnlyConfirmedDecrease(t *testing.T) {
	var feedback damageFeedback
	if got := feedback.Update(12, true, time.Second); got != 0 {
		t.Fatalf("首次确认强度=%v，想要 0", got)
	}
	if got := feedback.Update(7, true, time.Second); got != 1 {
		t.Fatalf("确认下降当帧强度=%v，想要 1", got)
	}
	if got := feedback.Update(8, true, 90*time.Millisecond); got != 0.5 {
		t.Fatalf("回复且淡出 90ms 强度=%v，想要 0.5", got)
	}
	if got := feedback.Update(8, true, 90*time.Millisecond); got != 0 {
		t.Fatalf("完整 180ms 后强度=%v，想要 0", got)
	}
}

func TestDamageFeedbackRepeatedDamageRestartsFullDuration(t *testing.T) {
	var feedback damageFeedback
	feedback.Update(20, true, 0)
	feedback.Update(15, true, 0)
	if got := feedback.Update(15, true, 90*time.Millisecond); got != 0.5 {
		t.Fatalf("首次伤害淡出强度=%v，想要 0.5", got)
	}
	if got := feedback.Update(10, true, time.Second); got != 1 {
		t.Fatalf("连续伤害当帧强度=%v，想要重新为 1", got)
	}
	if got := feedback.Update(10, true, 179*time.Millisecond); got <= 0 {
		t.Fatalf("重置后 179ms 强度=%v，想要仍大于 0", got)
	}
}

func TestDamageFeedbackElapsedBoundsAndReset(t *testing.T) {
	var feedback damageFeedback
	feedback.Update(20, true, 0)
	feedback.Update(10, true, 0)
	if got := feedback.Update(10, true, -time.Second); got != 1 {
		t.Fatalf("负 elapsed 强度=%v，想要保持 1", got)
	}
	if got := feedback.Update(10, false, 0); got != 0 {
		t.Fatalf("not-ready 强度=%v，想要 0", got)
	}
	if got := feedback.Update(4, true, 0); got != 0 {
		t.Fatalf("reset 后首次 ready 强度=%v，想要 0", got)
	}
	feedback.Update(2, true, 0)
	feedback.Reset()
	if feedback != (damageFeedback{}) {
		t.Fatalf("显式 Reset 后状态=%+v，想要零值", feedback)
	}
}
```

- [ ] **Step 2: 运行 RED**

Run:

```bash
go test ./cmd/mcgo -run '^TestDamageFeedback' -count=1
```

Expected: FAIL，`damageFeedback` 与其方法尚未定义；失败不能来自现有日志或前台窗口。

- [ ] **Step 3: 写最小状态机实现**

创建 `cmd/mcgo/damage_feedback.go`：

```go
//go:build darwin

package main

import "time"

const damageFeedbackDuration = 180 * time.Millisecond

// damageFeedback 只根据确认生命值维护本地呈现计时，不预测任何伤害。
type damageFeedback struct {
	hasHealth bool
	health    uint8
	remaining time.Duration
}

// Update 接收本帧确认生命值并返回 0..1 的遮罩强度。
func (feedback *damageFeedback) Update(
	health uint8,
	ready bool,
	elapsed time.Duration,
) float32 {
	if !ready {
		feedback.Reset()
		return 0
	}
	if !feedback.hasHealth {
		feedback.hasHealth = true
		feedback.health = health
		return 0
	}
	damaged := health < feedback.health
	feedback.health = health
	if damaged {
		feedback.remaining = damageFeedbackDuration
		return 1
	}
	if elapsed > 0 {
		if elapsed >= feedback.remaining {
			feedback.remaining = 0
		} else {
			feedback.remaining -= elapsed
		}
	}
	return float32(feedback.remaining) / float32(damageFeedbackDuration)
}

// Reset 清除当前会话的确认基线与呈现计时。
func (feedback *damageFeedback) Reset() {
	*feedback = damageFeedback{}
}
```

- [ ] **Step 4: 运行 GREEN、race 与最小 mutation check**

```bash
gofmt -w cmd/mcgo/damage_feedback.go cmd/mcgo/damage_feedback_test.go
go test ./cmd/mcgo -run '^TestDamageFeedback' -race -count=1
```

Expected: PASS。

临时把 `damaged := health < feedback.health` 改成 `>`，重复测试必须 FAIL；立即恢复 `<` 并再次确认 PASS。mutation 只在工作树执行，不提交错误版本。

- [ ] **Step 5: 勾选 OpenSpec 对应任务并提交**

把 `tasks.md` 的 1.1..1.3 改为 `[x]`，然后：

```bash
git add cmd/mcgo/damage_feedback.go cmd/mcgo/damage_feedback_test.go openspec/changes/confirmed-damage-feedback/tasks.md
git diff --cached --check
git commit -m "feat: 增加确认生命值受伤反馈计时"
```

Expected: 提交不包含 renderer、app 接线、README 或用户日志。

---

### Task 3: 实现 overlay renderer 并接入 application

**Files:**
- Create: `internal/render/damage_overlay.go`
- Create: `internal/render/shader/damage_overlay.wgsl`
- Create: `internal/render/damage_overlay_test.go`
- Modify: `cmd/mcgo/damage_feedback_test.go`
- Modify: `cmd/mcgo/app.go:55-155,315-345,520-590,580-615,827-910,920-935,1060-1090`
- Modify: `cmd/mcgo/app_test.go:260-370,400-475,570-610`
- Modify: `README.md:115-125`
- Modify: `openspec/changes/confirmed-damage-feedback/tasks.md`

**Interfaces:**
- Consumes: Task 2 的 `damageFeedback.Update`/`Reset`；`gfx.Device`、`gfx.CommandEncoder`、`gfx.TextureView`；application 的 `elapsed` 与 `Predictor.Health()`。
- Produces: `func render.NewDamageOverlayRenderer(gfx.Device, gfx.TextureFormat) *render.DamageOverlayRenderer`；`func (*render.DamageOverlayRenderer) Render(gfx.CommandEncoder, gfx.TextureView, float32)`；`func (*render.DamageOverlayRenderer) Release()`；application 字段 `damageStrength float32`。

- [ ] **Step 1: 写 renderer 的 fake gfx 失败测试**

创建带 `//go:build darwin` 的 `internal/render/damage_overlay_test.go`。复用同包 `skyTestDevice`、`skyTestBuffer`、`skyTestRenderPipeline`、`skyTestBindGroup`，新增只捕获 pass descriptor 的最小 encoder：

```go
//go:build darwin

package render

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"

	"minecraft-go/internal/gfx"
)

type damageOverlayTestEncoder struct {
	descs []gfx.RenderPassDesc
	pass  skyTestPass
}

func (encoder *damageOverlayTestEncoder) BeginRenderPass(desc gfx.RenderPassDesc) gfx.RenderPass {
	encoder.descs = append(encoder.descs, desc)
	return &encoder.pass
}
func (*damageOverlayTestEncoder) BeginComputePass(string) gfx.ComputePass {
	panic("unexpected compute pass")
}
func (*damageOverlayTestEncoder) CopyBufferToBuffer(gfx.Buffer, uint64, gfx.Buffer, uint64, uint64) {
	panic("unexpected buffer copy")
}
func (*damageOverlayTestEncoder) Finish() gfx.CommandBuffer { panic("unexpected finish") }

func TestDamageOverlayUsesFixedResourcesAndOneTriangle(t *testing.T) {
	device := &skyTestDevice{}
	renderer := NewDamageOverlayRenderer(device, gfx.FormatRGBA8Unorm)
	uniform := device.buffer(t, "damage overlay uniform")
	pipeline := device.renderPipeline(t, "damage overlay")
	bind := device.bindGroup(t, "damage overlay resources")

	if uniform.desc.Size != 16 || uniform.desc.Usage != gfx.BufferUsageUniform|gfx.BufferUsageCopyDst {
		t.Fatalf("uniform=%+v，想要固定 16 字节 Uniform|CopyDst", uniform.desc)
	}
	if pipeline.desc.Blend != gfx.BlendAlpha || pipeline.desc.DepthFormat != gfx.FormatUndefined || pipeline.desc.DepthWrite {
		t.Fatalf("pipeline=%+v，想要无 depth 的 alpha blend", pipeline.desc)
	}
	if len(bind.desc.Entries) != 1 || bind.desc.Entries[0].Buffer != uniform || bind.desc.Entries[0].Size != 16 {
		t.Fatalf("bind entries=%+v，想要一个完整 uniform 绑定", bind.desc.Entries)
	}

	renderer.Render(nil, nil, 0)
	renderer.Render(nil, nil, -1)
	renderer.Render(nil, nil, float32(math.NaN()))
	if len(uniform.writes) != 0 {
		t.Fatalf("非活动强度产生 %d 次 uniform 写入", len(uniform.writes))
	}

	encoder := &damageOverlayTestEncoder{}
	target := &skyTestView{}
	renderer.Render(encoder, target, 2)
	if len(encoder.descs) != 1 || encoder.descs[0].Label != "damage overlay pass" || encoder.descs[0].LoadClear || encoder.descs[0].ColorView != target {
		t.Fatalf("pass descriptors=%+v，想要保留目标的单 pass", encoder.descs)
	}
	if got := encoder.pass.commands; len(got) != 2 || got[0] != "pipeline:damage overlay" || got[1] != "draw:damage overlay:3:1" {
		t.Fatalf("commands=%v，想要一次三顶点 draw", got)
	}
	if len(uniform.writes) != 1 || len(uniform.writes[0]) != 16 {
		t.Fatalf("uniform writes=%d bytes=%d，想要 1/16", len(uniform.writes), writeBytes(uniform.writes))
	}
	if got := math.Float32frombits(binary.LittleEndian.Uint32(uniform.writes[0][:4])); got != 1 {
		t.Fatalf("钳制后 strength=%v，想要 1", got)
	}
	if !bytes.Equal(uniform.writes[0][4:], make([]byte, 12)) {
		t.Fatalf("uniform padding=%v，想要全零", uniform.writes[0][4:])
	}

	renderer.Release()
	renderer.Release()
	if uniform.releases != 1 || pipeline.releases != 1 || bind.releases != 1 {
		t.Fatalf("release counts=%d/%d/%d，想要各 1", uniform.releases, pipeline.releases, bind.releases)
	}
}
```

- [ ] **Step 2: 写 headless 像素测试和 hidden benchmark**

在同一测试文件追加：

```go
const damageOverlayHeadlessSize = 64

func TestDamageOverlayHeadlessPixels(t *testing.T) {
	device, err := gfx.NewHeadlessDevice()
	if err != nil {
		if skyHeadlessAdapterUnavailable(err) {
			t.Skipf("本机无可用 GPU adapter: %v", err)
		}
		t.Fatalf("创建 headless GPU device: %v", err)
	}
	defer device.Release()
	renderer := NewDamageOverlayRenderer(device, gfx.FormatRGBA8Unorm)
	defer renderer.Release()

	base := renderDamageOverlayHeadless(t, device, renderer, 0)
	got := renderDamageOverlayHeadless(t, device, renderer, 1)
	baseCenter := damageOverlayPixel(base, 32, 32)
	gotCenter := damageOverlayPixel(got, 32, 32)
	if gotCenter != baseCenter {
		t.Fatalf("中心像素=%v，想要保持底图 %v", gotCenter, baseCenter)
	}
	baseEdge := damageOverlayPixel(base, 0, 32)
	gotEdge := damageOverlayPixel(got, 0, 32)
	if int(gotEdge[0])-int(baseEdge[0]) < 35 || gotEdge[0] <= gotEdge[1]+35 {
		t.Fatalf("边缘像素 base=%v got=%v，想要明显红色增量", baseEdge, gotEdge)
	}
}

func renderDamageOverlayHeadless(
	t *testing.T,
	device gfx.Device,
	renderer *DamageOverlayRenderer,
	strength float32,
) []byte {
	t.Helper()
	color := device.CreateTexture(gfx.TextureDesc{
		Label: "damage overlay headless color",
		Width: damageOverlayHeadlessSize, Height: damageOverlayHeadlessSize,
		Format: gfx.FormatRGBA8Unorm,
		Usage: gfx.TextureUsageRenderTarget | gfx.TextureUsageCopySrc,
	})
	defer color.Release()
	view := color.View(gfx.TextureViewDesc{Dimension: gfx.TextureViewDimension2D})
	defer view.Release()
	encoder := device.CreateCommandEncoder()
	clearPass := encoder.BeginRenderPass(gfx.RenderPassDesc{
		Label: "damage overlay test clear", ColorView: view,
		ClearColor: [4]float32{0.1, 0.1, 0.1, 1}, LoadClear: true,
	})
	clearPass.End()
	renderer.Render(encoder, view, strength)
	commands := encoder.Finish()
	device.Submit(commands)
	commands.Release()
	device.Poll(true)
	return color.ReadLayer(0, 0)
}

func damageOverlayPixel(pixels []byte, x, y int) [4]byte {
	offset := (y*damageOverlayHeadlessSize + x) * 4
	return [4]byte{pixels[offset], pixels[offset+1], pixels[offset+2], pixels[offset+3]}
}

func BenchmarkDamageOverlayHidden(b *testing.B) {
	device := &skyTestDevice{}
	renderer := NewDamageOverlayRenderer(device, gfx.FormatRGBA8Unorm)
	b.Cleanup(renderer.Release)
	b.ReportAllocs()
	for b.Loop() {
		renderer.Render(nil, nil, 0)
	}
}
```

- [ ] **Step 3: 写 application 接线与生命周期失败测试**

在 `cmd/mcgo/damage_feedback_test.go` 追加所需 imports：`encoding/binary`、`math`、`reflect`、`slices`、`github.com/go-gl/mathgl/mgl32`、`internal/client`、`internal/config`、`internal/core`、`internal/gfx`、`internal/network`、`internal/render`。追加两个测试和一个 helper：

```go
func applyDamageFeedbackHealth(t *testing.T, app *application, tick uint64, health uint8, ready bool) {
	t.Helper()
	if _, err := app.predictor.ApplyPlayerState(network.PlayerState{
		ServerTick: tick, Dimension: core.Overworld,
		Position: mgl32.Vec3{0.5, 10, 0.5}, OnGround: true,
		Ready: ready, Health: health,
	}, client.MirrorCollisionSource{Mirror: app.mirror, Dimension: core.Overworld}); err != nil {
		t.Fatalf("应用 health=%d ready=%v: %v", health, ready, err)
	}
}

func TestApplicationDamageOverlayUsesConfirmedHealthAndStaysBelowHUD(t *testing.T) {
	glyphs := &integrationGlyphSource{}
	app, device := newRemoteRenderApplication(t, glyphs)
	app.debugPanelRenderer = render.NewDebugPanelRenderer(device, gfx.FormatRGBA8Unorm, glyphs)
	app.panel = newPanelStateFromActive(config.Defaults().Render)
	app.panel.visible = true
	if err := app.remotePlayers.Apply(remoteSpawn(1, "Remote-1", 1, mgl32.Vec3{1, 2, 3})); err != nil {
		t.Fatal(err)
	}
	if err := app.inventory.Apply(network.InventoryState{Inventory: core.Inventory{}}); err != nil {
		t.Fatal(err)
	}
	if err := app.predictor.Begin(network.PlayerState{
		ServerTick: 1, Dimension: core.Overworld,
		Position: mgl32.Vec3{0.5, 10, 0.5}, OnGround: true,
		Ready: true, Health: 12,
	}); err != nil {
		t.Fatal(err)
	}
	if rendered, err := app.frame(0, 1, 0); err != nil || !rendered {
		t.Fatalf("建立基线 frame=(%v,%v)", rendered, err)
	}
	if contains := slices.Contains(device.lastPasses(), "damage overlay pass"); contains {
		t.Fatalf("首次确认误画 overlay: %v", device.lastPasses())
	}

	applyDamageFeedbackHealth(t, app, 2, 7, true)
	device.resetPasses()
	if rendered, err := app.frame(0, 1, time.Second); err != nil || !rendered {
		t.Fatalf("确认受伤 frame=(%v,%v)", rendered, err)
	}
	want := []string{
		"terrain pass", "avatar pass", "name-tag pass", "damage overlay pass",
		"hotbar pass", "debug panel pass",
	}
	if got := device.lastPasses(); !reflect.DeepEqual(got, want) {
		t.Fatalf("passes=%v，想要 %v", got, want)
	}
	strength := math.Float32frombits(binary.LittleEndian.Uint32(
		device.bufferByLabel(t, "damage overlay uniform").lastWrite[:4],
	))
	if strength != 1 {
		t.Fatalf("受伤当帧 strength=%v，想要 1", strength)
	}

	applyDamageFeedbackHealth(t, app, 3, 8, true)
	device.resetPasses()
	if rendered, err := app.frame(0, 1, 90*time.Millisecond); err != nil || !rendered {
		t.Fatalf("回复期间淡出 frame=(%v,%v)", rendered, err)
	}
	strength = math.Float32frombits(binary.LittleEndian.Uint32(
		device.bufferByLabel(t, "damage overlay uniform").lastWrite[:4],
	))
	if strength != 0.5 {
		t.Fatalf("回复不得重启反馈，90ms strength=%v，想要 0.5", strength)
	}

	applyDamageFeedbackHealth(t, app, 4, 0, false)
	device.resetPasses()
	if rendered, err := app.frame(0, 1, 0); err != nil || !rendered {
		t.Fatalf("not-ready frame=(%v,%v)", rendered, err)
	}
	if slices.Contains(device.lastPasses(), "damage overlay pass") {
		t.Fatalf("not-ready 后仍画 overlay: %v", device.lastPasses())
	}
}

func TestDamageFeedbackResetsWithClientSession(t *testing.T) {
	app, _ := newInteractiveTestApplication(t)
	app.damageFeedback.Update(20, true, 0)
	app.damageStrength = app.damageFeedback.Update(10, true, 0)
	app.closeClientSession(nil)
	if app.damageFeedback != (damageFeedback{}) || app.damageStrength != 0 {
		t.Fatalf("会话清理后 feedback=%+v strength=%v，想要零值", app.damageFeedback, app.damageStrength)
	}
}
```

`slices`、`encoding/binary`、`math` 已在 `app_test.go`/其他测试中使用，但此文件必须显式 import 自己使用的包；不要依赖其他文件的 imports。

修改 `cmd/mcgo/app_test.go`：

1. `newRemoteRenderApplication` 的 application literal 增加：

```go
damageOverlayRenderer: render.NewDamageOverlayRenderer(dev, gfx.FormatRGBA8Unorm),
```

2. `TestApplicationCloseReleasesRemoteRenderersInOrder` 创建 `damage := render.NewDamageOverlayRenderer(...)`，放入 application，并把期望 release markers 的最前面增加 `"damage overlay resources"`。
3. 把 `TestApplicationConstructionFailureReleasesRemoteResourcesInReverse` 的注入失败点从 name-tag 后移到 damage overlay：依次记录并成功构造 atlas、avatar、name-tag、hotbar、item-drop，`newDamageOverlayRenderer` 返回 `wantErr`；期望构造顺序为 `atlas, avatar, name-tag, hotbar, item-drop, damage-overlay`，释放顺序以 `item drop resources` 开始，随后 hotbar、name-tag、avatar、glyph atlas、terrain、depth、device。这样删掉任一新清理分支都会失败。

正常 Close 测试中的新增部分固定为：

```go
damage := render.NewDamageOverlayRenderer(dev, gfx.FormatRGBA8Unorm)
app.damageOverlayRenderer = damage
markers := dev.releaseMarkers([]string{
	"damage overlay resources", "hotbar resources", "name-tag resources",
	"glyph-atlas texture", "avatar resources", "terrain resources",
	"main depth texture", "main color view", "main color texture", "device",
})
want := []string{
	"damage overlay resources", "hotbar resources", "name-tag resources",
	"glyph-atlas texture", "avatar resources", "terrain resources",
	"main depth texture", "main color view", "main color texture", "device",
}
```

构造失败测试保留现有连接/window/device 装配，只把 renderer 注入段替换/补全为：

```go
dependencies.newGlyphAtlas = func(device gfx.Device) (*render.GlyphAtlas, error) {
	constructionOrder = append(constructionOrder, "atlas")
	return render.NewGlyphAtlas(device)
}
dependencies.newAvatarRenderer = func(
	device gfx.Device,
	color, depth gfx.TextureFormat,
) (*render.AvatarRenderer, error) {
	constructionOrder = append(constructionOrder, "avatar")
	return render.NewAvatarRenderer(device, color, depth), nil
}
dependencies.newNameTagRenderer = func(
	device gfx.Device,
	color, depth gfx.TextureFormat,
	atlas render.GlyphSource,
) (*render.NameTagRenderer, error) {
	constructionOrder = append(constructionOrder, "name-tag")
	return render.NewNameTagRenderer(device, color, depth, atlas), nil
}
dependencies.newHotbarRenderer = func(
	device gfx.Device,
	color gfx.TextureFormat,
	atlas render.GlyphSource,
	blocks *assets.Registry,
) (*render.HotbarRenderer, error) {
	constructionOrder = append(constructionOrder, "hotbar")
	return render.NewHotbarRenderer(device, color, atlas, blocks), nil
}
dependencies.newItemDropRenderer = func(
	device gfx.Device,
	color, depth gfx.TextureFormat,
) (*render.ItemDropRenderer, error) {
	constructionOrder = append(constructionOrder, "item-drop")
	return render.NewItemDropRenderer(device, color, depth), nil
}
dependencies.newDamageOverlayRenderer = func(
	gfx.Device,
	gfx.TextureFormat,
) (*render.DamageOverlayRenderer, error) {
	constructionOrder = append(constructionOrder, "damage-overlay")
	return nil, wantErr
}

app, err := newApplicationWithDependencies(remoteConnectionOptions(), dependencies)
if app != nil || !errors.Is(err, wantErr) {
	t.Fatalf("construction result=(%v,%v), want nil/wrapped failure", app, err)
}
wantOrder := []string{"atlas", "avatar", "name-tag", "hotbar", "item-drop", "damage-overlay"}
if !reflect.DeepEqual(constructionOrder, wantOrder) {
	t.Fatalf("construction order=%v want=%v", constructionOrder, wantOrder)
}
wantMarkers := []string{
	"item drop resources", "hotbar resources", "name-tag resources",
	"avatar resources", "glyph-atlas texture", "terrain resources",
	"main depth texture", "device",
}
if markers := dev.releaseMarkers(wantMarkers); !reflect.DeepEqual(markers, wantMarkers) {
	t.Fatalf("failure release markers=%v want=%v; all=%v", markers, wantMarkers, dev.releases)
}
```

- [ ] **Step 4: 运行 RED**

```bash
go test ./internal/render -run '^TestDamageOverlay' -count=1
go test ./cmd/mcgo -run 'DamageOverlay|DamageFeedbackResets|ApplicationConstructionFailure|ApplicationCloseReleases' -count=1
```

Expected: 第一条因 `DamageOverlayRenderer` 未定义而编译失败；第二条因 application 尚无 renderer/strength 接线而失败。不得通过注释测试或给 fixture 加 nil 旁路变绿。

- [ ] **Step 5: 实现固定 WGSL**

创建 `internal/render/shader/damage_overlay.wgsl`：

```wgsl
// 确认受伤反馈：固定红色屏幕边缘渐变。

struct DamageOverlay {
    strength: f32,
    _pad: vec3<f32>,
};

@group(0) @binding(0) var<uniform> overlay: DamageOverlay;

struct VsOut {
    @builtin(position) clip: vec4<f32>,
    @location(0) uv: vec2<f32>,
};

@vertex
fn vs_main(@builtin(vertex_index) vertex_index: u32) -> VsOut {
    var positions = array<vec2<f32>, 3>(
        vec2<f32>(-1.0, -1.0),
        vec2<f32>(3.0, -1.0),
        vec2<f32>(-1.0, 3.0),
    );
    let position = positions[vertex_index];
    var out: VsOut;
    out.clip = vec4<f32>(position, 0.0, 1.0);
    out.uv = vec2<f32>(position.x * 0.5 + 0.5, 0.5 - position.y * 0.5);
    return out;
}

@fragment
fn fs_main(in: VsOut) -> @location(0) vec4<f32> {
    let edge_distance = min(
        min(in.uv.x, 1.0 - in.uv.x),
        min(in.uv.y, 1.0 - in.uv.y),
    );
    let edge_factor = 1.0 - smoothstep(0.0, 0.35, edge_distance);
    return vec4<f32>(0.65, 0.0, 0.0, 0.30 * overlay.strength * edge_factor);
}
```

- [ ] **Step 6: 实现最小 DamageOverlayRenderer**

创建 `internal/render/damage_overlay.go`：

```go
package render

import (
	_ "embed"
	"encoding/binary"
	"math"

	"minecraft-go/internal/gfx"
)

const damageOverlayUniformBytes = 16

//go:embed shader/damage_overlay.wgsl
var damageOverlayShader string

// DamageOverlayRenderer 绘制服务端确认受伤后的固定屏幕边缘渐变。
type DamageOverlayRenderer struct {
	uniform  gfx.Buffer
	pipeline gfx.RenderPipeline
	bind     gfx.BindGroup
	upload   [damageOverlayUniformBytes]byte
}

// NewDamageOverlayRenderer 创建一个不使用深度附件的固定透明管线。
func NewDamageOverlayRenderer(
	device gfx.Device,
	colorFormat gfx.TextureFormat,
) *DamageOverlayRenderer {
	renderer := &DamageOverlayRenderer{}
	renderer.uniform = device.CreateBuffer(gfx.BufferDesc{
		Label: "damage overlay uniform", Size: damageOverlayUniformBytes,
		Usage: gfx.BufferUsageUniform | gfx.BufferUsageCopyDst,
	})
	layout := gfx.BindGroupLayout{
		Label: "damage overlay layout",
		Entries: []gfx.BindGroupLayoutEntry{{
			Binding: 0, Type: gfx.BindingUniformBuffer, VisibleIn: gfx.StageFragment,
		}},
	}
	module := device.CreateShaderModule(damageOverlayShader)
	renderer.pipeline = device.CreateRenderPipeline(gfx.RenderPipelineDesc{
		Label: "damage overlay", Shader: module,
		VertexEntry: "vs_main", FragmentEntry: "fs_main",
		BindGroups: []gfx.BindGroupLayout{layout},
		ColorFormat: colorFormat, Blend: gfx.BlendAlpha,
	})
	module.Release()
	renderer.bind = device.CreateBindGroup(gfx.BindGroupDesc{
		Label: "damage overlay resources", Layout: layout,
		Entries: []gfx.BindGroupEntry{{
			Binding: 0, Buffer: renderer.uniform, Size: damageOverlayUniformBytes,
		}},
	})
	return renderer
}

// Render 在 HUD 之前绘制强度已钳制的全屏边缘反馈。
func (renderer *DamageOverlayRenderer) Render(
	encoder gfx.CommandEncoder,
	target gfx.TextureView,
	strength float32,
) {
	if !(strength > 0) { // 同时拒绝零、负值与 NaN。
		return
	}
	if strength > 1 {
		strength = 1
	}
	clear(renderer.upload[:])
	binary.LittleEndian.PutUint32(renderer.upload[:4], math.Float32bits(strength))
	renderer.uniform.Write(0, renderer.upload[:])
	pass := encoder.BeginRenderPass(gfx.RenderPassDesc{
		Label: "damage overlay pass", ColorView: target, LoadClear: false,
	})
	pass.SetPipeline(renderer.pipeline)
	pass.SetBindGroup(0, renderer.bind)
	pass.Draw(3, 1)
	pass.End()
}

// Release 幂等释放 renderer 自己持有的 GPU 资源。
func (renderer *DamageOverlayRenderer) Release() {
	if renderer.bind != nil {
		renderer.bind.Release()
		renderer.bind = nil
	}
	if renderer.pipeline != nil {
		renderer.pipeline.Release()
		renderer.pipeline = nil
	}
	if renderer.uniform != nil {
		renderer.uniform.Release()
		renderer.uniform = nil
	}
}
```

- [ ] **Step 7: 接入 application 生命周期、更新与绘制顺序**

在 `cmd/mcgo/app.go` 做以下精确接线：

1. `application` 在 `hotbarRenderer`/`debugPanelRenderer` 附近增加：

```go
damageOverlayRenderer *render.DamageOverlayRenderer
damageFeedback        damageFeedback
damageStrength        float32
```

2. `applicationDependencies` 增加：

```go
newDamageOverlayRenderer func(gfx.Device, gfx.TextureFormat) (*render.DamageOverlayRenderer, error)
```

3. 默认依赖使用现有“无错误 constructor 包成可注入错误”的模式：

```go
if dependencies.newDamageOverlayRenderer == nil {
	dependencies.newDamageOverlayRenderer = func(
		device gfx.Device,
		color gfx.TextureFormat,
	) (*render.DamageOverlayRenderer, error) {
		return render.NewDamageOverlayRenderer(device, color), nil
	}
}
```

4. 在 item-drop renderer 成功后、可选 debug-panel renderer 之前创建：

```go
app.damageOverlayRenderer, err = dependencies.newDamageOverlayRenderer(dev, colorFormat)
if err != nil {
	app.releaseRemoteConstructionResources()
	return nil, errors.Join(fmt.Errorf("创建受伤反馈渲染器: %w", err), app.Close())
}
```

5. `releaseRemoteConstructionResources` 与 `releaseOwnedResources` 都按逆序在 item-drop/hotbar 之前释放 damage overlay；前者释放后必须置 nil：

```go
if a.damageOverlayRenderer != nil {
	a.damageOverlayRenderer.Release()
	a.damageOverlayRenderer = nil // releaseOwnedResources 不需要置 nil，沿用现有模式。
}
```

6. `frame` 通过 receiver 错误检查后、remote interpolation 前更新：

```go
health, ready := a.predictor.Health()
a.damageStrength = a.damageFeedback.Update(health, ready, elapsed)
```

7. `closeClientSession` 的 `clientCloseOnce.Do` 内与其他镜像 reset 同步执行：

```go
a.damageFeedback.Reset()
a.damageStrength = 0
```

8. `renderFrame` 在 name-tag timing 记录结束后、hotbar HUD 前调用：

```go
a.damageOverlayRenderer.Render(encoder, target, a.damageStrength)
```

生产 application 始终构造该 renderer；不要加 `if a.damageOverlayRenderer != nil` 来掩盖 fixture 或生命周期漏接。

- [ ] **Step 8: 更新 README 与 OpenSpec checkbox**

把 README 当前句：

```text
也没有床或自定义重生点、伤害动画音效与专门的死亡界面——死亡后直接在原地满血重生。
```

改为：

```text
也没有床或自定义重生点；确认生命值下降时已有短暂红色边缘反馈，但尚无音效与专门的死亡界面——死亡后直接在原地满血重生。
```

只改这一句；不提前写 M4N 材料基线。把 OpenSpec `tasks.md` 的 2.1..2.5 改为 `[x]`。

- [ ] **Step 9: 运行 GREEN、headless、race 与 benchmark**

```bash
gofmt -w cmd/mcgo/app.go cmd/mcgo/app_test.go cmd/mcgo/damage_feedback.go cmd/mcgo/damage_feedback_test.go internal/render/damage_overlay.go internal/render/damage_overlay_test.go
go test ./internal/render -run '^TestDamageOverlay' -count=1
go test ./cmd/mcgo -run 'DamageOverlay|DamageFeedback|ApplicationConstructionFailure|ApplicationCloseReleases' -count=1
go test ./cmd/mcgo ./internal/render -race -count=1
go test ./internal/render -run '^$' -bench '^BenchmarkDamageOverlayHidden$' -benchmem -count=5
```

Expected:

- 全部测试 exit 0，headless 测试只允许因无 adapter skip；
- benchmark 每轮 `0 B/op`、`0 allocs/op`，记录五次 `ns/op` 到最终交付，不设置性能失败阈值；
- 自动验证不打开前台窗口；
- `git diff --name-only cmd/mcgo/testdata/golden` 无输出。

临时把 `renderFrame` 中 overlay 调用移动到 hotbar 后，`TestApplicationDamageOverlayUsesConfirmedHealthAndStaysBelowHUD` 必须 FAIL；恢复原顺序并确认 PASS。

- [ ] **Step 10: 提交 renderer 与接线**

```bash
git add internal/render/damage_overlay.go internal/render/shader/damage_overlay.wgsl internal/render/damage_overlay_test.go cmd/mcgo/app.go cmd/mcgo/app_test.go cmd/mcgo/damage_feedback_test.go README.md openspec/changes/confirmed-damage-feedback/tasks.md
git diff --cached --check
git commit -m "feat: 显示权威受伤红屏反馈"
```

Expected: 提交不包含协议、存档、scenario、capture、golden、M4N 文件或用户日志。

---

### Task 4: 全仓验证并归档 OpenSpec change

**Files:**
- Modify: `openspec/changes/confirmed-damage-feedback/tasks.md`
- Create after archive: `openspec/changes/archive/2026-08-09-confirmed-damage-feedback/**`
- Modify after archive: `openspec/specs/authoritative-health/spec.md`

**Interfaces:**
- Consumes: Task 2/3 的两个独立实现提交与 active OpenSpec change。
- Produces: 严格通过、已归档的 `authoritative-health` 主规格；一份不改变版本/基线的最终验证记录。

- [ ] **Step 1: 核对实现范围和 active tasks**

```bash
git diff main...HEAD --name-only
git diff main...HEAD -- internal/network internal/storage internal/sim internal/render/hotbar.go internal/render/shader/terrain.wgsl cmd/mcgo/capture.go cmd/mcgo/testdata/golden
openspec status --change confirmed-damage-feedback --json
```

Expected: 第二条 diff 无输出；OpenSpec 1.* 与 2.* 全为 `[x]`，只有 3.* 尚未完成；不存在协议、schema、scenario、capture 或 golden 变化。

- [ ] **Step 2: 运行受影响包、架构和全仓门禁**

```bash
go test ./cmd/mcgo ./internal/render -race -count=1
go test ./internal/archcheck -count=1
go test ./... -race
go vet ./...
gofmt -l .
openspec validate --all --strict --no-interactive
```

Expected: 所有命令 exit 0，`gofmt -l .` 无输出。若真实测试、资源或 shader 错误失败，修根因；不得放宽门禁、跳过测试或更新 golden。

- [ ] **Step 3: 重跑性能记录并完成归档前清单**

```bash
go test ./internal/render -run '^$' -bench '^BenchmarkDamageOverlayHidden$' -benchmem -count=5
git diff --name-only cmd/mcgo/testdata/golden
git status --short
```

Expected: benchmark 五轮均为 `0 B/op`、`0 allocs/op`；golden diff 无输出；status 除本任务 OpenSpec task 勾选与三份用户日志外无意外文件。

把 `tasks.md` 的 3.1、3.2 改为 `[x]`。3.3 在 archive 成功后由归档提交体现，不在 archive 前虚假勾选；如果 OpenSpec archive 要求所有 checkbox 完成，则先勾选 3.3，并在 archive 失败时立即恢复为 `[ ]`。

- [ ] **Step 4: 归档并再次严格验证**

```bash
openspec validate confirmed-damage-feedback --strict --no-interactive
openspec archive confirmed-damage-feedback -y
openspec validate --all --strict --no-interactive
```

Expected: active change 移动到 `openspec/changes/archive/2026-08-09-confirmed-damage-feedback/`；主规格的旧“不得新增 pipeline/shader”句被完整替换为确认受伤反馈契约；三条命令 exit 0。

- [ ] **Step 5: 提交归档并复核工作区**

```bash
git add openspec/specs/authoritative-health/spec.md openspec/changes/archive/2026-08-09-confirmed-damage-feedback
git diff --cached --check
git commit -m "docs: 归档确认受伤红屏反馈规格"
git status --short
git log --oneline main..HEAD
```

Expected:

- 归档提交只包含主规格与归档 change；
- 工作区只剩三份 `midscene_run/log/` 用户日志；
- 提交序列依次包含设计、计划、OpenSpec、状态机、renderer 接线、归档；
- 最终交付明确报告测试、headless、benchmark、OpenSpec 结果和 README 一行潜在 M4N 合并处理，不自动推送或合并。

## 完成判定

- 确认生命值下降当帧强度为 1，180ms 线性归零；连续下降重置，首次/回复/not-ready 不误触发。
- 会话清理主动清除基线和强度，旧反馈不进入新会话。
- overlay 的固定颜色、渐变公式和绘制层级被 fake gfx 与 headless 像素测试钉住。
- 零/负/NaN 强度无 uniform 写、pass、draw；hidden benchmark 为 `0 B/op`、`0 allocs/op`。
- application 构造失败逆序清理、正常 Close 幂等释放新资源。
- 本任务对协议、玩家/区块 schema、metadata、scenario、capture 与 golden 的 diff 均为空；合并时保留目标分支已有版本。
- OpenSpec 主规格不再与专用 overlay pipeline 冲突，active change 已严格验证并归档。
- M4N 与本任务无生产代码文件重叠；唯一 README 文本冲突按后合入方重放一句处理。
