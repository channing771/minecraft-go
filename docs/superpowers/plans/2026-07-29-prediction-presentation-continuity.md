# 玩家预测展示连续性实施计划

> **供代理执行：** 必须使用 `superpowers:subagent-driven-development`（推荐）或
> `superpowers:executing-plans`，按任务逐项实施。所有步骤使用复选框跟踪。

**目标：** 消除服务端确认预测输入时造成的 20Hz 展示位置跳变，使移动和跳跃在高帧率
渲染下保持连续，同时保留现有权威收敛与大幅纠正语义。

**架构：** 只在 `client.Predictor` 的展示层修复连续性。权威状态、输入历史和共享物理
仍按原方式重建；和解后根据最终状态误差选择“剩余固定步连续性衰减”“100ms 小纠正”
或“立即跳转”，不修改服务端、协议、渲染和存档。

**技术栈：** Go 1.26.0、`mgl32.Vec3`、共享固定步物理、Go `testing`、race detector。

## 全局约束

- 每条 Go 命令必须通过 `zsh -ic 'gvm use go1.26.0 >/dev/null && ...'` 执行；不得下载 Go。
- 只修改预测展示连续性，不修改服务端 tick、物理常量、网络协议、存档和渲染。
- 显式 reset、维度变化和 `>=0.5` 格纠正继续立即跳转。
- `[1/128, 0.5)` 格真实纠正继续使用 100ms 衰减。
- 低于 `1/128` 格但展示基准发生变化时，ack 前后第一帧位置必须完全连续。
- 设计、计划、测试和代码最终只形成一个提交。

---

### Task 1：保持确认边界的展示位置连续

**文件：**

- 修改：`internal/client/predictor.go:198-292`
- 修改：`internal/client/predictor_test.go:712-870`
- 创建：`docs/superpowers/specs/2026-07-29-prediction-presentation-continuity-design.md`
- 创建：`docs/superpowers/plans/2026-07-29-prediction-presentation-continuity.md`

**接口：**

- 消费：`Predictor.ApplyPlayerState(network.PlayerState, physics.CollisionSource)`、
  `Predictor.PresentationPosition(time.Duration)`、`physics.FixedDelta`。
- 产出：不新增公开 API；`ApplyPlayerState` 在低于纠正阈值的 ack 中保持展示连续，并用
  `displayOffset` 在剩余固定步内收敛。

- [x] **Step 1：新增精确 ack 的失败测试**

在 `internal/client/predictor_test.go` 增加：

```go
func TestExactAckPreservesRemainingInterpolation(t *testing.T) {
	p := readyPredictor(t)
	advanceSteps(t, p, 1, Control{MoveX: 1})
	p.accumulator = physics.FixedDelta / 2
	before, _ := p.PresentationPosition(0)

	if _, err := p.ApplyPlayerState(nextAuthority(p), flatClientWorld{}); err != nil {
		t.Fatal(err)
	}
	after, _ := p.PresentationPosition(0)
	if !after.ApproxEqualThreshold(before, 1e-6) {
		t.Fatalf("精确 ack 后展示跳变: before=%v after=%v", before, after)
	}
	if p.HistoryLen() != 0 || p.correctionRemaining != physics.FixedDelta/2 {
		t.Fatalf("ack 后 history=%d remaining=%v", p.HistoryLen(), p.correctionRemaining)
	}

	end, _ := p.PresentationPosition(physics.FixedDelta / 2)
	state, _ := p.State()
	if !end.ApproxEqualThreshold(state.Position, 1e-6) ||
		p.correctionRemaining != 0 || p.displayOffset != (mgl32.Vec3{}) {
		t.Fatalf("剩余固定步后未收敛: end=%v state=%v offset=%v remaining=%v",
			end, state.Position, p.displayOffset, p.correctionRemaining)
	}
}
```

再增加高频帧轨迹测试，水平移动和跳跃共用同一断言：

```go
func TestExactAcksKeepHighRateMovementAndJumpContinuous(t *testing.T) {
	for _, test := range []struct {
		name    string
		control Control
	}{
		{name: "move", control: Control{MoveX: 1}},
		{name: "jump", control: Control{Jump: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := readyPredictor(t)
			sequence := p.maxSentInput
			previous, _ := p.PresentationPosition(0)
			const frameElapsed = 5 * time.Millisecond

			for frame := range 80 {
				if err := p.Advance(
					frameElapsed,
					test.control,
					flatClientWorld{},
					func() uint64 { sequence++; return sequence },
					func(network.PlayerInput) error { return nil },
				); err != nil {
					t.Fatalf("frame %d Advance: %v", frame, err)
				}
				if p.HistoryLen() != 0 {
					if _, err := p.ApplyPlayerState(nextAuthority(p), flatClientWorld{}); err != nil {
						t.Fatalf("frame %d ack: %v", frame, err)
					}
				}
				position, _ := p.PresentationPosition(frameElapsed)
				if delta := position.Sub(previous).Len(); delta > 0.06 {
					t.Fatalf("frame %d 展示跳变 %.6f: previous=%v position=%v",
						frame, delta, previous, position)
				}
				previous = position
			}
		})
	}
}
```

把原 `TestSmallCorrectionThresholdDoesNotSmoothSubthresholdError` 的低于 `1/128`
分支改为断言“ack 后位置连续，并在当前固定步剩余时间内收敛”；`=1/128` 分支继续
断言 100ms 纠正。

- [x] **Step 2：运行聚焦测试并确认真实 RED**

运行：

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/client -run "TestExactAck|TestSmallCorrectionThreshold" -count=1 -v'
```

预期：失败。现有代码在 `errorDistance < 1/128` 时清空 `displayOffset`，所以精确 ack
后的展示位置会直接跳到 `current`，且 `correctionRemaining` 为零。

- [x] **Step 3：实现低阈值展示连续性**

在 `Predictor.ApplyPlayerState` 捕获原纠正剩余时间，并将现有二分逻辑改为三分逻辑：

```go
oldDisplayed := p.presentationPositionNoAdvance()
oldPredicted := p.current.Position
oldCorrectionRemaining := p.correctionRemaining

// 保留现有 authority 应用、ack 删除和未确认输入重放。

errorDistance := p.current.Position.Sub(oldPredicted).Len()
switch {
case errorDistance >= 0.5:
	p.displayOffset = mgl32.Vec3{}
	p.correctionRemaining = 0
case errorDistance >= 1.0/128:
	p.displayOffset = oldDisplayed.Sub(p.interpolatedPosition())
	p.correctionRemaining = 100 * time.Millisecond
default:
	p.displayOffset = oldDisplayed.Sub(p.interpolatedPosition())
	if p.displayOffset == (mgl32.Vec3{}) {
		p.correctionRemaining = 0
		break
	}
	remainingStep := max(time.Duration(0), physics.FixedDelta-p.accumulator)
	p.correctionRemaining = max(oldCorrectionRemaining, remainingStep)
}
```

不得改变 authority、`previous/current`、history、accumulator 或网络确认语义。

- [x] **Step 4：运行聚焦 GREEN 与既有预测器回归**

运行：

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/client -run "TestExactAck|TestSmallCorrection|TestLargeCorrection|TestApplyPlayerState|TestPresentationPosition" -race -count=20 -v'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "TestAuthoritativePlayer" -race -count=10 -v'
```

预期：全部通过；race detector 无报告。

- [x] **Step 5：完成全仓验证**

运行：

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/client ./cmd/mcgo -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go vet ./...'
zsh -ic 'gvm use go1.26.0 >/dev/null && test -z "$(gofmt -l .)"'
git diff --check
```

预期：全部退出零，gofmt 不输出文件，工作区仅包含本任务设计、计划、测试和实现。

- [x] **Step 6：前台临时世界复验**

使用 `mktemp -d` 创建 `/tmp` 下临时世界，只运行交互模式，不接触默认世界。连续移动和
跳跃至少 30 秒，确认鼠标转向、平移和跳跃均连续；关闭窗口后确认应用正常 Shutdown，
再把临时世界移动到废纸篓以便恢复。

- [x] **Step 7：请求独立代码审查并修复有效发现**

审查范围为本任务完整 diff；重点核对精确 ack、低阈值纠正、既有纠正剩余时间、reset
和大幅 snap。每个接受的问题先补失败测试，再做最小修复并重跑受影响包 race 测试。

- [x] **Step 8：单提交**

```bash
git add internal/client/predictor.go internal/client/predictor_test.go \
  docs/superpowers/specs/2026-07-29-prediction-presentation-continuity-design.md \
  docs/superpowers/plans/2026-07-29-prediction-presentation-continuity.md
git commit -m "fix: 保持玩家预测展示连续"
```

提交后运行 `git status --short`，预期无输出。
