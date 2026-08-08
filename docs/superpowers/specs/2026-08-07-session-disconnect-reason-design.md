# 服务端关闭会话时告知原因

日期：2026-08-07
状态：设计已批准，待实施

## 1. 起因

CI 上有一类红灯：`wait ready Recv: network: transport closed`，在已观察到的 8 条 CI 失败里占 3 条：

| 运行 | 测试 | 耗时 | 断言 |
| --- | --- | --- | --- |
| 31169485835 | `TestTCPPlayerAndWorldFailureMatrix.../corrupt_player_rejects_only_that_identity` | 0.02s | `wait ready Recv: network: transport closed` |
| 31197146703 | `TestHealthSevenSurvivesDiskRestart` | 1.46s | 同上 |
| 31197258080 | `TestCraftingSurvivesV2DiskRestartAndReconnectOrder` | 0.12s | 同上 |

**根因至今未知，而且以当前的代码状态查不出来。** 本设计不修这些失败，而是补上查明它们所必需的信息通道。

## 2. 调查记录：确立了什么，排除了什么

### 已确立

**`transport closed` 的含义是确定的。** 客户端 `tcpStream.failIO`（`internal/network/stream.go`）在读到 `io.EOF` / `io.ErrUnexpectedEOF` / `io.ErrClosedPipe` / TCP 关闭错误时返回 `ErrClosed`。因此这个症状意味着**服务端关闭了连接**，不是客户端等待超时——"再多等一会儿"这一类解释被排除。

**登录是成功的。** 三个失败点都在 `waitClientReadyFor`，而 `dialIntegrationClient` 内部调用 `network.LoginClient` 并在失败时 `t.Fatalf`。所以连接在断开时**已是一个完整的协议端点**，断开发生在登录成功之后、Ready 之前。

### 用证据排除的假说

| 假说 | 排除依据 |
| --- | --- |
| 心跳超时 | `DefaultConfig` 为 interval 5s / timeout 15s，而三次失败在 0.02s–1.46s |
| 子测试之间 pre-login 槽位泄漏 | 每个子测试各自 `startDiskHost`，且测试用 `waitForPreLoginCount` 守着槽位数 |
| accept 期槽位耗尽（`acquirePreLogin` 满则立刻关连接） | 登录已成功，客户端早已过了 pre-login 阶段 |
| 服务端关会话的错误被静默吞掉 | 追到 `fail(err)` → `SessionExit{Err}` → `collectSessionExit` → `acceptStream` 返回值 → `host.go` 的 `slog.Warn("连接登录失败")`，这一层有覆盖 |

**本地复现不出**：精确到失败子测试 `-count=100` 全过（33 秒）。

### 为什么查不下去

两个原因叠加：

1. **服务端关闭连接时不告诉客户端原因。** 客户端只能拿到无差别的 `transport closed`。
2. **服务端日志无法归属到具体测试。** 服务端 `slog` 直写 stderr，与 `go test` 的按测试缓冲不在一个通道上；而故障注入测试会故意制造几十条「连接登录失败」，与真实的一条在日志里长得一模一样。

第 1 条是可以直接消除的，且消除它之后第 2 条**不再需要解决**——原因会直接出现在测试自己的失败信息里。

## 3. 真正的缺口：一个建好却没接线的机制

协议**已经完整定义**了带原因的断开：

```go
// internal/network/packet.go
type DisconnectCode uint8

const (
	DisconnectProtocolViolation DisconnectCode = iota + 1
	DisconnectTimeout
	DisconnectServerShutdown
	DisconnectSlowClient
	DisconnectInternalError
)

type Disconnect struct {
	Code    DisconnectCode
	Message string
}
```

而且：

- **codec 已实现且有 golden 覆盖**（`codec_test.go` 有编解码用例与十六进制 golden）。
- **客户端已完整处理**（`internal/network/transport.go` 的 `clientPlayEndpoint.Recv`）：

```go
case Disconnect:
    _ = endpoint.stream.Close()
    return nil, &RemoteError{State: StatePlay, Code: uint8(packet.Code), Message: packet.Message}
```

**唯独服务端从不发送它。** 全仓库对 `Disconnect{...}` 的构造只出现在 codec 解码路径与包注册表里。

五个码与服务端实际的关闭原因近乎一一对应：

| `DisconnectCode` | 服务端关闭原因 |
| --- | --- |
| `DisconnectTimeout` | `errHeartbeatTimeout` |
| `DisconnectProtocolViolation` | `errInvalidHeartbeatReply`、`errUnknownClientMessage` |
| `DisconnectSlowClient` | （实施中移出，见 §5.5） |
| `DisconnectServerShutdown` | （本变更不使用，见 §5.2） |
| `DisconnectInternalError` | （本变更不使用，见 §5.2） |

**这不是"缺少诊断能力"，是现成的能力没接上最后一根线。**

## 4. 改什么

**只改服务端一处**：`session.fail(err)` 在关闭前尽力发一个 `Disconnect`。

```go
func (current *session) fail(err error) {
	current.failOnce.Do(func() {
		current.sendDisconnect(err)   // 尽力而为
		current.shutdown()
		if current.detach != nil {
			go current.detach(current.id, current.generation, err)
		}
	})
}
```

**客户端零改动**——已经接好了。

**协议零改动**——包已在协议内，无版本变更，无迁移。

## 5. 五个必须定死的实现约束

### 5.1 必须在 `shutdown()` 之前发，且不能用 `current.ctx`

`shutdown()` 会 `cancel()` 会话上下文并 `endpoint.Close()`。发送必须发生在它之前，且要用一个**独立的短期限上下文**（`current.ctx` 此刻即将或已被取消）。

### 5.2 用白名单而不是黑名单

只对**三个具名原因**发送：`errHeartbeatTimeout`、`errInvalidHeartbeatReply`、`errUnknownClientMessage`。

（原设计含第四个 `errSessionOutboxFull`，实施中被证伪并移出，见 §5.5。）

其余一律不发，包括：

- `network.ErrClosed` / `context.Canceled`——**客户端已经走了**，发了没人收。
- **writer 自身 `Send` 失败导致的 `fail`**——socket 已经坏了，再发一次只是徒劳，而且 `writeLoop` 正在其调用栈内，会构成重入。
- **writer panic**——同上，且状态不可信。

白名单是这里唯一安全的形态：黑名单会在将来新增关闭原因时默认放行，而"默认发送"恰恰是重入风险所在。

**两个码在本变更中不使用**：

- `DisconnectInternalError`——没有具名原因映射到它，硬凑用途是 YAGNI。
- `DisconnectServerShutdown`——关服走的是 `detachSessionLocked(id, generation, nil)`，`cause` 为 `nil`，**根本不经过带具名错误的 `fail()`**；要覆盖它得改关服路径本身，而关服不是本次三条失败的形态。设计自审时发现这一点并移出范围。

### 5.3 绕开 outbox 直接发

必须直接调 `endpoint.Send` 而不是 `enqueue`：`enqueue` 只是把消息放进 outbox，关闭在即时 writer 未必还能消费它。

### 5.5 `errSessionOutboxFull` 必须移出白名单（实施中发现）

原设计把它列为第四个具名原因。**实施时证伪：它会阻塞发布热路径。**

`enqueue` 在 outbox 满时**同步**调用 `fail`：

```go
// enqueue 永不等待 writer；满队列会关闭慢 session。
default:
    current.mu.Unlock()
    slog.Warn("慢客户端 outbox 已满，关闭 session", "session", current.id)
    current.fail(errSessionOutboxFull)
```

若 `fail` 里的 `sendDisconnect` 同步等 200ms，`enqueue` 就被间接阻塞 200ms——**直接打破它写在注释里的不变量「永不等待 writer」**，而 `TestSessionFullOutboxClosesWithoutBlocking` 正是守这条不变量的既有测试，会确定性失败。`enqueue` 位于每 tick、每会话、每消息的发布路径上，阻塞它的代价远大于一条诊断信息。

移出它有三条独立理由，方向一致：

1. **它在热路径上**，而另外三个原因分别来自心跳 goroutine 与 reader goroutine，都不在。
2. **发送按构造是徒劳的**：outbox 满恰恰意味着客户端没在消费，`Disconnect` 送不到。
3. **它是四个原因里唯一已有服务端日志的**（`slog.Warn("慢客户端 outbox 已满，关闭 session")`），可诊断性本来就不缺。

这也印证了白名单原则本身：**连接不可用时不发送**。outbox 满正是连接不可用的一种表现，与"writer 自身 Send 失败"同类。原设计把它列入白名单，是没有把这条原则贯彻到底。

### 5.4 失败即放弃，绝不阻塞关闭

发送出错一律忽略。期限取 **200ms**：足够本机与局域网写出一个几十字节的帧，又不会让关闭路径出现可感知的停顿。该值是上界而非等待——socket 正常时写入是立即返回的，只有对端已死或缓冲区满才会走到期限。

关闭路径的时延不得因此增加到可测量的程度，这是硬要求：`session.fail` 处在慢客户端清理与关服的公共路径上。

## 6. 为什么这个形状是对的

- **不需要解决日志归属**：原因直接进入客户端 `Recv` 返回的 `RemoteError`，也就是测试自己的失败信息。这比"让服务端日志能对应到测试"小一个数量级。
- **不需要新增协议能力**：包、codec、golden、客户端处理全都在。
- **不需要新增诊断代码路径**：复用既有的 `RemoteError`，它已经在登录拒绝路径上被广泛使用与断言。

## 7. 明确的非目标

- **本变更不修任何 `transport closed` 失败。** 它让下一次失败自己说出原因。三条 CI 红灯会继续红，直到拿到原因后再判根因。
- 不改 50ms tick 边界那条线（另 4 条失败，诊断已就位、等 CI 数据）。
- 不解决"服务端日志无法归属到测试"——接上 Disconnect 之后它不再是这条线的阻塞项。
- 不给 `acquirePreLogin` 的静默丢弃补日志。那条路径**已被证据排除**在本次失败之外（登录已成功），为它施工属于据假说扩大范围；记录为已知的独立缺口。

## 8. 成功判据

**不能用"CI 变绿"验证**——本变更不修任何东西。

1. 服务端对三种具名原因确实发出对应的 `DisconnectCode`，客户端 `Recv` 返回的 `RemoteError` 携带该 code 与 message。
2. 白名单之外的原因（`ErrClosed`、`Canceled`、writer 写失败、panic）**不发送**，且关闭路径行为与改动前一致。
3. 关闭路径不因此变慢；`session.fail` 的既有语义（`failOnce`、`shutdown`、`detach`）不变。
4. 下一次 CI 上出现该类失败时，测试的失败信息**可能**给出断开原因——不保证一定给出。三个白名单原因里 `errHeartbeatTimeout` 已被 §2 的证据排除（15s 超时 vs 已观测到的 0.02–1.46s 失败），实际能命中的只剩两个 reader goroutine 的协议违规（`errInvalidHeartbeatReply`、`errUnknownClientMessage`）；而登录后服务端主动关闭连接最可能走的那条路——`endpointReader` 把 `Recv` 返回的错误原样传给 `fail`（见 `internal/server/session.go` 的 `endpointReader`）——本身不在白名单内，届时仍会呈现为裸的 `transport closed`。**若下一次失败仍是裸的 `transport closed`，说明关闭走的是白名单之外的路径，这本身就是有价值的信息**，应据此定位具体路径并显式列举，不得把白名单改为默认允许。

第 4 条只能等。前三条必须有自动化测试——**尤其是第 2 条**：一个"该不发时发了"的缺陷会在写失败路径上造成重入，而那条路径在本地几乎不会走到。

## 9. 实测结果

本节记录实施与收尾阶段的实测数据；判据本身见 §8，这里只写"测到了什么"。

### Task 1 Step 5：变异验证

把 `disconnectCodeFor` 的 `default` 分支从 `return 0, false` 临时改成 `return network.DisconnectInternalError, true` 后重跑 `TestDisconnectCodeForRejectsEverythingElse`，6 个拒绝用例中有 5 个变红：`ErrClosed`（客户端已关闭）、`context.Canceled`（上下文取消）、包装了 `ErrClosed` 的错误、writer 写失败、writer panic。唯一没有变红的是 `nil` 用例——它由独立的 `err == nil` 分支在 `default` 之前拦下，天然不受这条变异影响，不构成断言盲区。变异已恢复，恢复后 `git diff --stat internal/server/session.go` 无输出。

结论：`TestDisconnectCodeForRejectsEverythingElse` 确实在守住"`default` 不允许发送"这条边界，无需加强断言。

### Task 2 Step 6：`-race` 全包回归与耗时对比

- 改前基线（`git stash` 到本变更前，独立跑两次确认）：约 115 秒（`1:55.17`）。
- 改后（`errSessionOutboxFull` 移出白名单后，独立跑两次）：
  - 第一次：`ok  minecraft-go/internal/server  114.783s`（wall `1:59.47`）
  - 第二次：`ok  minecraft-go/internal/server  116.270s`（wall `1:57.74`）
- 两次均干净通过，无 `DATA RACE`、无 `--- FAIL`，耗时与改前基线基本持平，未出现"显著变长"。

同一批验证还为 §5.5 的结论提供了实测依据：保留 `errSessionOutboxFull` 在白名单中的首轮实现下，`TestSessionFullOutboxClosesWithoutBlocking` 在三次独立重跑中均于 200–202ms 区间确定性失败；移出该原因后，该测试在 `-count=3` 下稳定通过（`ok  minecraft-go/internal/server  0.302s`），期望未被放宽。§5.5 的文字表述与这组实测一致，未发现需要改写之处。

### Task 2 Step 7：`internal/network` 零改动确认

Task 1、Task 2 两轮改动中 `git diff --stat internal/network/` 均无输出，`internal/network/` 全程未被触碰，与"协议零改动"的前提一致。

### 订正的表述

- §8 判据 1 原文写"服务端对四种具名原因……"，与实测不符——白名单最终只有三个具名原因（`errHeartbeatTimeout`、`errInvalidHeartbeatReply`、`errUnknownClientMessage`），`errSessionOutboxFull` 已被移出（见 §5.5）。已订正为"三种"。
- §5 标题原文"四个必须定死的实现约束"，与实际列出的 5.1–5.5 共五条约束（含实施中新增的 §5.5）不符。已订正为"五个"。

## 10. 已知的独立缺口（记录，不在本变更内修）

`internal/server/host.go` 的 accept 循环：

```go
streamID, err := h.acquirePreLogin(stream)
if err != nil {
    continue        // 关了流，零日志
}
```

`acquirePreLogin` 在 pre-login 槽位（容量 16）耗尽时立刻关闭连接并返回，调用方 `continue`——`slog.Warn("连接登录失败")` 在 `acceptStream` 的 goroutine 里，这条路径根本走不到。客户端会看到"连上即被关、无任何解释"。

本次三条失败已排除该路径（登录已成功），但仓库里存在 `connectTask16ConcurrentClients` 这样**并发数正好等于容量 16** 的测试助手，零余量。这与 ScenarioV7 采样预算"跑在下限上"是同一种形状，值得单独查。
