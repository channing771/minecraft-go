# 服务端关闭会话时告知原因 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让服务端在因四个具名原因关闭已建立的会话前，尽力发出协议已定义但从未被使用的 `Disconnect{Code, Message}`，使客户端的失败信息直接携带断开原因。

**Architecture:** 把"原因 → `DisconnectCode`"的映射抽成纯函数（白名单形态，可单测）；`session.fail` 在 `shutdown()` 之前用一个独立的 200ms 上下文直接调 `endpoint.Send` 发出该包，失败即放弃。客户端与协议零改动——两者早已完整实现。

**Tech Stack:** Go 1.26，标准库 `context` / `errors` / `time`。不新增任何依赖。

## 本变更不修任何红灯

它不改变任何通过/失败的条件，只让下一次 `transport closed` 自己说出原因。三条 CI 红灯会继续红。成功判据见设计文档 §8——**不能用"CI 变绿"验证**。

## Global Constraints

- Go 命令一律经 `zsh -ic 'gvm use go1.26.0 >/dev/null && <cmd>'` 执行。
- **测试一律前台跑**，用 Bash 工具的 `timeout` 参数给足时间（`internal/server` 全包 `-race` 约 115 秒，给 `timeout: 600000`）。不要用 `run_in_background`，不要用 monitor。
- **协议零改动。** `internal/network/` 下一个字都不改：`Disconnect`、`DisconnectCode`、codec、客户端 `clientPlayEndpoint.Recv` 全都已实现，本变更只是接上服务端这一端。
- **白名单，不是黑名单。** 只对四个具名原因发送：`errHeartbeatTimeout`、`errInvalidHeartbeatReply`、`errUnknownClientMessage`、`errSessionOutboxFull`。其余一律不发。
- **`DisconnectServerShutdown` 与 `DisconnectInternalError` 本变更不使用**，理由见设计文档 §5.2。
- **`session.fail` 的既有语义不得改变**：`failOnce` 只执行一次、`shutdown()` 仍被调用、`detach` 仍以同一个 err 异步触发。
- **不得阻塞关闭路径。** 发送用独立上下文、200ms 期限、错误一律忽略。
- 代码注释与 GoDoc 用中文；Go 标识符保留英文。Go 代码必须经 `gofmt`。
- `git add` 必须逐个点名文件。工作树里的 `midscene_run/log/*.log` 与 `mcgo` 可执行文件是无关噪声，**绝不能提交**，不要用 `git add .`。

## 两个已核实的前提（不必重新验证）

1. **写是串行化的。** `tcpStream.Send` 内部 `writeOwner.acquire(ctx)`，与 `writeLoop` 的并发 Send 由该 gate 串行；`acquire` 尊重 ctx，因此 200ms 期限同时兜住了等锁的时间。
2. **白名单天然排除重入。** `writeLoop` 只会以「`Send` 返回的错误」或「panic 错误」调用 `fail`，这两者都不在白名单内，因此不会出现"在 writeLoop 调用栈里再发一次"的情况。

---

### Task 1: 原因到断开码的映射（纯函数）

**Files:**
- Modify: `internal/server/session.go`（新增 `disconnectCodeFor`）
- Test: `internal/server/session_test.go`

**Interfaces:**
- Produces: `disconnectCodeFor(err error) (network.DisconnectCode, bool)`——第二个返回值为 `false` 时表示不应发送。Task 2 调用它。

- [ ] **Step 1: 写失败测试**

在 `internal/server/session_test.go` 末尾加入：

```go
func TestDisconnectCodeForMapsNamedCauses(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want network.DisconnectCode
	}{
		{"心跳超时", errHeartbeatTimeout, network.DisconnectTimeout},
		{"心跳回复无效", errInvalidHeartbeatReply, network.DisconnectProtocolViolation},
		{"未知客户端消息", errUnknownClientMessage, network.DisconnectProtocolViolation},
		{"outbox 已满", errSessionOutboxFull, network.DisconnectSlowClient},
	} {
		t.Run(test.name, func(t *testing.T) {
			code, ok := disconnectCodeFor(test.err)
			if !ok {
				t.Fatalf("具名原因 %v 未被映射", test.err)
			}
			if code != test.want {
				t.Fatalf("code = %d, want %d", code, test.want)
			}
		})
	}
}

func TestDisconnectCodeForRejectsEverythingElse(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{"nil", nil},
		{"客户端已关闭", network.ErrClosed},
		{"上下文取消", context.Canceled},
		{"包装了 ErrClosed 的错误", fmt.Errorf("writer: %w", network.ErrClosed)},
		{"writer 写失败", errors.New("server: write failed")},
		{"writer panic", errors.New("server: session writer panic")},
	} {
		t.Run(test.name, func(t *testing.T) {
			if code, ok := disconnectCodeFor(test.err); ok {
				t.Fatalf("非白名单原因 %v 被映射成 code=%d", test.err, code)
			}
		})
	}
}

func TestDisconnectCodeForUnwrapsNamedCauses(t *testing.T) {
	wrapped := fmt.Errorf("session %d: %w", testSessionID, errHeartbeatTimeout)
	code, ok := disconnectCodeFor(wrapped)
	if !ok || code != network.DisconnectTimeout {
		t.Fatalf("包装后的具名原因未被识别: code=%d ok=%v", code, ok)
	}
}
```

`import` 需要补 `errors` 与 `fmt`（`context`、`network`、`testing` 已在）。

- [ ] **Step 2: 跑测试确认失败原因是断言而非编译错误**

先加一个最小桩让它能编译：

```go
func disconnectCodeFor(err error) (network.DisconnectCode, bool) {
	return 0, false
}
```

Run: `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "^TestDisconnectCodeFor" -count=1' 2>&1 | tail -5`

Expected: `TestDisconnectCodeForMapsNamedCauses` 与 `TestDisconnectCodeForUnwrapsNamedCauses` FAIL（报"具名原因 ... 未被映射"），`TestDisconnectCodeForRejectsEverythingElse` 通过（桩恰好满足它）。**若失败原因是编译错误，先补桩再重跑**——编译失败不算有效的红。

- [ ] **Step 3: 实现映射**

放在 `session.go` 的 `fail` 之前：

```go
// disconnectCodeFor 把会话关闭原因映射成协议的 DisconnectCode。
// 第二个返回值为 false 表示不应向客户端发送断开原因。
//
// 这里刻意用白名单而不是黑名单：只有四个具名原因会发送。黑名单会在将来
// 新增关闭原因时默认放行，而"默认发送"恰恰是风险所在——writeLoop 自身
// Send 失败或 panic 时也会调用 fail，此时 socket 已不可信，再发一次不仅
// 徒劳，还会在 writeLoop 的调用栈内构成重入。白名单让这两种情况自然落在
// 不发送的一侧，无需额外判断。
//
// network.ErrClosed 与 context.Canceled 同样不发送：客户端已经走了，
// 发了没人收。
//
// DisconnectServerShutdown 与 DisconnectInternalError 不在此表内：
// 关服走 detachSessionLocked(id, generation, nil)，cause 为 nil，
// 根本不经过带具名错误的 fail；而没有任何具名原因映射到 InternalError。
func disconnectCodeFor(err error) (network.DisconnectCode, bool) {
	switch {
	case err == nil:
		return 0, false
	case errors.Is(err, errHeartbeatTimeout):
		return network.DisconnectTimeout, true
	case errors.Is(err, errInvalidHeartbeatReply):
		return network.DisconnectProtocolViolation, true
	case errors.Is(err, errUnknownClientMessage):
		return network.DisconnectProtocolViolation, true
	case errors.Is(err, errSessionOutboxFull):
		return network.DisconnectSlowClient, true
	default:
		return 0, false
	}
}
```

- [ ] **Step 4: 跑测试确认全绿**

Run: `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "^TestDisconnectCodeFor" -count=1' 2>&1 | tail -3`

Expected: `ok`

- [ ] **Step 5: 变异验证——证明白名单测试真的在守边界**

把 `default: return 0, false` 临时改成 `default: return network.DisconnectInternalError, true`，重跑 Step 4 的命令。

Expected: `TestDisconnectCodeForRejectsEverythingElse` 变红。**若仍然通过，说明该测试没有真正守住白名单边界，必须加强断言再继续**——这条测试是本变更防重入的唯一自动化保障。

恢复后 Run: `git diff --stat internal/server/session.go` 确认改回原样。

- [ ] **Step 6: 提交**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go vet ./internal/server'
gofmt -l internal/server
git add internal/server/session.go internal/server/session_test.go
git commit -m "feat: 增加会话关闭原因到断开码的白名单映射"
```

---

### Task 2: 在 `session.fail` 中尽力发出断开原因

**Files:**
- Modify: `internal/server/session.go`（新增 `sendDisconnect`，修改 `fail`）
- Test: `internal/server/session_test.go`

**Interfaces:**
- Consumes: Task 1 的 `disconnectCodeFor(err error) (network.DisconnectCode, bool)`。
- Produces: `(*session).sendDisconnect(err error)`——尽力而为，无返回值。

- [ ] **Step 1: 写失败测试**

`recordingServerEndpoint`（已存在于 `session_test.go`）会记录所有被 Send 的消息，用它断言发或不发。加入：

```go
func recordedDisconnect(endpoint *recordingServerEndpoint) (network.Disconnect, bool) {
	endpoint.mu.Lock()
	defer endpoint.mu.Unlock()
	for _, message := range endpoint.messages {
		if disconnect, ok := message.(network.Disconnect); ok {
			return disconnect, true
		}
	}
	return network.Disconnect{}, false
}

func TestSessionFailSendsDisconnectForNamedCause(t *testing.T) {
	endpoint := &recordingServerEndpoint{}
	var workers sync.WaitGroup
	session := newObserverSession(
		context.Background(), testSessionID, 1, endpoint, 4, &workers, nil,
	)

	session.fail(errHeartbeatTimeout)
	workers.Wait()

	disconnect, ok := recordedDisconnect(endpoint)
	if !ok {
		t.Fatal("具名原因关闭时未发出 Disconnect")
	}
	if disconnect.Code != network.DisconnectTimeout {
		t.Fatalf("Disconnect.Code = %d, want %d", disconnect.Code, network.DisconnectTimeout)
	}
	if !strings.Contains(disconnect.Message, errHeartbeatTimeout.Error()) {
		t.Fatalf("Disconnect.Message = %q，未包含原因", disconnect.Message)
	}
}

func TestSessionFailSkipsDisconnectForClientGone(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{"客户端已关闭", network.ErrClosed},
		{"上下文取消", context.Canceled},
		{"writer 写失败", errors.New("server: write failed")},
	} {
		t.Run(test.name, func(t *testing.T) {
			endpoint := &recordingServerEndpoint{}
			var workers sync.WaitGroup
			session := newObserverSession(
				context.Background(), testSessionID, 1, endpoint, 4, &workers, nil,
			)

			session.fail(test.err)
			workers.Wait()

			if disconnect, ok := recordedDisconnect(endpoint); ok {
				t.Fatalf("非白名单原因发出了 Disconnect: %+v", disconnect)
			}
		})
	}
}

func TestSessionFailKeepsExistingSemantics(t *testing.T) {
	endpoint := &recordingServerEndpoint{}
	var workers sync.WaitGroup
	detached := make(chan error, 4)
	session := newObserverSession(
		context.Background(), testSessionID, 1, endpoint, 4, &workers,
		func(_ sim.SessionID, _ uint64, cause error) bool {
			detached <- cause
			return true
		},
	)

	session.fail(errHeartbeatTimeout)
	session.fail(errUnknownClientMessage) // failOnce：第二次必须无效
	workers.Wait()

	select {
	case cause := <-detached:
		if !errors.Is(cause, errHeartbeatTimeout) {
			t.Fatalf("detach cause = %v，想要首个原因", cause)
		}
	case <-time.After(waitDeadline):
		t.Fatal("detach 未被触发")
	}
	select {
	case cause := <-detached:
		t.Fatalf("failOnce 失效，detach 被触发第二次: %v", cause)
	case <-time.After(200 * time.Millisecond):
	}
	if !session.closed() {
		t.Fatal("fail 之后 session 未标记为已关闭")
	}
}
```

`import` 需要补 `strings` 与 `minecraft-go/internal/sim`（其余已在）。

- [ ] **Step 2: 跑测试确认失败原因是断言而非编译错误**

Run: `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "^TestSessionFail" -count=1' 2>&1 | tail -6`

Expected: `TestSessionFailSendsDisconnectForNamedCause` FAIL（报"具名原因关闭时未发出 Disconnect"）；另两条通过（改动前本来就不发、语义本来就对）。

- [ ] **Step 3: 实现 `sendDisconnect`**

放在 `fail` 之前：

```go
// sessionDisconnectSendTimeout 是发送断开原因的上界。
//
// 它是上界而非等待：socket 正常时写入立即返回，只有对端已死、缓冲区满、
// 或 writeLoop 正持有 tcpStream 的 writeOwner 时才会走到期限。session.fail
// 处在慢客户端清理与关服的公共路径上，因此这个值必须小到不产生可感知的停顿。
const sessionDisconnectSendTimeout = 200 * time.Millisecond

// sendDisconnect 在关闭前尽力告知客户端断开原因，失败一律忽略。
//
// 必须在 shutdown() 之前调用：shutdown 会 cancel 会话上下文并关闭 endpoint，
// 之后就发不出任何东西了。也正因如此，这里不能用 current.ctx——它即将被取消——
// 而要用一个独立的短期限上下文。
//
// 直接调 endpoint.Send 而不走 outbox：errSessionOutboxFull 本身就意味着
// outbox 已满，enqueue 必然失败。
func (current *session) sendDisconnect(err error) {
	code, ok := disconnectCodeFor(err)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(
		context.Background(), sessionDisconnectSendTimeout,
	)
	defer cancel()
	_ = current.endpoint.Send(ctx, network.Disconnect{
		Code:    code,
		Message: err.Error(),
	})
}
```

- [ ] **Step 4: 接进 `fail`**

```go
func (current *session) fail(err error) {
	current.failOnce.Do(func() {
		current.sendDisconnect(err)
		current.shutdown()
		if current.detach != nil {
			go current.detach(current.id, current.generation, err)
		}
	})
}
```

**除新增的那一行外，`fail` 一个字不改**：`failOnce`、`shutdown()`、`detach` 的顺序与形态保持原样。

- [ ] **Step 5: 跑测试确认全绿**

Run: `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "^TestSessionFail|^TestDisconnectCodeFor" -count=1' 2>&1 | tail -3`

Expected: `ok`

- [ ] **Step 6: 全包回归与竞态检查**

Run: `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -race -count=1' 2>&1 | tail -3`（前台，`timeout: 600000`）

Expected: `ok`。**这一步是本任务最重要的验证**：`sendDisconnect` 从心跳/reader goroutine 调 `endpoint.Send`，与 `writeLoop` 的并发 Send 必须由 `tcpStream.writeOwner` 正确串行。`-race` 报出任何 endpoint 相关的竞态即停手报告。

把改前改后的耗时都写进报告（改前基线约 115 秒）。**显著变长说明某处在等满 200ms 期限**，那意味着有非预期路径在发送。

- [ ] **Step 7: 确认 `internal/network` 零改动**

Run: `git diff --stat internal/network/`

Expected: **无输出**。协议、codec、客户端处理全都已存在，本变更不该碰它们。

- [ ] **Step 8: 提交**

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go vet ./internal/server'
gofmt -l internal/server
git add internal/server/session.go internal/server/session_test.go
git commit -m "feat: 会话因具名原因关闭时告知客户端断开原因"
```

---

### Task 3: 收尾门禁与文档

**Files:**
- Modify: `docs/superpowers/specs/2026-08-07-session-disconnect-reason-design.md`（回填实测）
- Modify: `openspec/changes/session-disconnect-reason/tasks.md`（勾选）

- [ ] **Step 1: 回填设计文档**

在设计文档 §8 后补一节「实测结果」，写入：Task 1 Step 5 变异验证的实际输出（哪条断言变红）、Task 2 Step 6 的 `-race` 结果与改前改后耗时、Task 2 Step 7 的 `internal/network` 空 diff 确认。

文档里任何与实测不符的表述一并订正。

- [ ] **Step 2: 确认改动范围**

Run: `git diff --stat main...HEAD`

Expected: 只有 `internal/server/session.go`、`internal/server/session_test.go`、以及 docs 与 openspec 文件。**出现 `internal/network/` 下任何文件都必须停手报告**——那意味着协议被动了，而本变更的前提是协议已完备。

- [ ] **Step 3: 收尾门禁**

前台逐条跑，每条 `timeout: 600000`：

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race'
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
zsh -ic 'gvm use go1.26.0 >/dev/null && go vet ./...'
gofmt -l .
git diff --check
openspec validate --all --strict --no-interactive
```

全部必须干净。若 `go test ./... -race` 出现失败，先看是不是 `TestDropSurvivesShutdownAndRestart`——那是已知的既有偶发挂起。

- [ ] **Step 4: 提交**

```bash
git add docs/superpowers/specs/2026-08-07-session-disconnect-reason-design.md \
        openspec/changes/session-disconnect-reason/tasks.md
git commit -m "docs: 回填断开原因的实测结果"
```

- [ ] **Step 5: 报告下一步**

明确报告：**本变更不会让 CI 变绿**，三条 `transport closed` 红灯会继续红。下一步是等 CI 上再次出现该类失败、从失败信息里读出 `RemoteError` 携带的断开码，再判根因。

若下一次失败**仍然**是无原因的 `transport closed`（没有 `RemoteError`），那说明关闭走的是白名单之外的路径——那本身就是有价值的信息，应据此扩大白名单或另查，**而不是**默认放行所有原因。

---

## Self-Review

**规格覆盖**

| 设计文档章节 | 对应任务 |
| --- | --- |
| §4 只改服务端一处 | Task 2 Step 4 |
| §5.1 在 `shutdown()` 之前发、用独立上下文 | Task 2 Step 3 |
| §5.2 白名单而非黑名单 | Task 1 全部；Task 1 Step 5 的变异验证守边界 |
| §5.3 绕开 outbox 直接发 | Task 2 Step 3 |
| §5.4 失败即放弃、200ms 期限 | Task 2 Step 3 的常量与注释 |
| §8 判据 1（发出对应 code 与 message） | Task 2 Step 1 的 `TestSessionFailSendsDisconnectForNamedCause` |
| §8 判据 2（白名单外不发送） | Task 1 Step 1/5、Task 2 Step 1 的 `TestSessionFailSkipsDisconnectForClientGone` |
| §8 判据 3（关闭路径不变慢、既有语义不变） | Task 2 Step 1 的 `TestSessionFailKeepsExistingSemantics`、Step 6 的耗时对比 |
| 协议零改动 | Task 2 Step 7、Task 3 Step 2 |

**已知的计划风险**

- Task 2 Step 1 的三个测试都用 `recordingServerEndpoint`，它的 `Send` 永远成功。因此"发送失败即放弃"这条路径**没有被自动化覆盖**——它只在真实 socket 死掉时才走到。这是有意的取舍：为它造一个会失败的 endpoint 假体只能证明 `_ =` 忽略了返回值，而那从代码上一眼可见。若评审认为需要覆盖，`newBlockingServerEndpoint()` 是现成的起点。
- `TestSessionFailKeepsExistingSemantics` 用 200ms 等待"detach 没有第二次触发"。这是**缺席断言**，不得替换成活性等待常量——按仓库既有的期限五分类，缺席断言不参与期限治理。
- Task 2 Step 6 的耗时哨兵不精确：机器负载波动也会影响 `-race` 全包耗时。实现者应把改前改后两个数字都记下来，由评审判断差异是否可疑。
