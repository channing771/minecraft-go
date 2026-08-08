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
| `DisconnectSlowClient` | `errSessionOutboxFull` |
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

## 5. 四个必须定死的实现约束

### 5.1 必须在 `shutdown()` 之前发，且不能用 `current.ctx`

`shutdown()` 会 `cancel()` 会话上下文并 `endpoint.Close()`。发送必须发生在它之前，且要用一个**独立的短期限上下文**（`current.ctx` 此刻即将或已被取消）。

### 5.2 用白名单而不是黑名单

只对**四个具名原因**发送：`errHeartbeatTimeout`、`errInvalidHeartbeatReply`、`errUnknownClientMessage`、`errSessionOutboxFull`。

其余一律不发，包括：

- `network.ErrClosed` / `context.Canceled`——**客户端已经走了**，发了没人收。
- **writer 自身 `Send` 失败导致的 `fail`**——socket 已经坏了，再发一次只是徒劳，而且 `writeLoop` 正在其调用栈内，会构成重入。
- **writer panic**——同上，且状态不可信。

白名单是这里唯一安全的形态：黑名单会在将来新增关闭原因时默认放行，而"默认发送"恰恰是重入风险所在。

**两个码在本变更中不使用**：

- `DisconnectInternalError`——没有具名原因映射到它，硬凑用途是 YAGNI。
- `DisconnectServerShutdown`——关服走的是 `detachSessionLocked(id, generation, nil)`，`cause` 为 `nil`，**根本不经过带具名错误的 `fail()`**；要覆盖它得改关服路径本身，而关服不是本次三条失败的形态。设计自审时发现这一点并移出范围。

### 5.3 绕开 outbox 直接发

`errSessionOutboxFull` 本身就意味着 outbox 满了，走 `enqueue` 必然失败。必须直接调 `endpoint.Send`。

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

1. 服务端对四种具名原因确实发出对应的 `DisconnectCode`，客户端 `Recv` 返回的 `RemoteError` 携带该 code 与 message。
2. 白名单之外的原因（`ErrClosed`、`Canceled`、writer 写失败、panic）**不发送**，且关闭路径行为与改动前一致。
3. 关闭路径不因此变慢；`session.fail` 的既有语义（`failOnce`、`shutdown`、`detach`）不变。
4. 下一次 CI 上出现该类失败时，测试的失败信息直接给出断开原因。

第 4 条只能等。前三条必须有自动化测试——**尤其是第 2 条**：一个"该不发时发了"的缺陷会在写失败路径上造成重入，而那条路径在本地几乎不会走到。

## 9. 已知的独立缺口（记录，不在本变更内修）

`internal/server/host.go` 的 accept 循环：

```go
streamID, err := h.acquirePreLogin(stream)
if err != nil {
    continue        // 关了流，零日志
}
```

`acquirePreLogin` 在 pre-login 槽位（容量 16）耗尽时立刻关闭连接并返回，调用方 `continue`——`slog.Warn("连接登录失败")` 在 `acceptStream` 的 goroutine 里，这条路径根本走不到。客户端会看到"连上即被关、无任何解释"。

本次三条失败已排除该路径（登录已成功），但仓库里存在 `connectTask16ConcurrentClients` 这样**并发数正好等于容量 16** 的测试助手，零余量。这与 ScenarioV7 采样预算"跑在下限上"是同一种形状，值得单独查。
