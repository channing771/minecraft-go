## Why

CI 上有一类红灯 `wait ready Recv: network: transport closed`，在已观察到的 8 条失败里占 3 条（`TestTCPPlayerAndWorldFailureMatrix.../corrupt_player_rejects_only_that_identity` 0.02s、`TestHealthSevenSurvivesDiskRestart` 1.46s、`TestCraftingSurvivesV2DiskRestartAndReconnectOrder` 0.12s）。

**根因至今未知，而且以当前代码状态查不出来。**

调查确立了两件事：`transport closed` 意味着**服务端关闭了连接**（客户端 `failIO` 读到 EOF 才返回 `ErrClosed`），不是等待超时；且登录**已经成功**（`dialIntegrationClient` 内部完成 `LoginClient` 并在失败时 `t.Fatalf`），断开发生在登录成功之后、Ready 之前。四个假说已用证据排除，本地精确到失败子测试跑 100 次全过。

查不下去的原因有两条，叠加起来致命：服务端关闭时不告诉客户端理由；而服务端日志直写 stderr、无法归属到具体测试——故障注入测试会故意制造几十条一模一样的告警。

**真正的缺口是：协议早已完整定义带原因的断开，但服务端从不发送。** `Disconnect{Code, Message}` 与五个 `DisconnectCode` 在 `internal/network/packet.go` 中定义齐全，codec 有编解码单测与十六进制 golden，客户端 `clientPlayEndpoint.Recv` 也已完整处理并返回带 code 的 `RemoteError`——唯独服务端这一端没接线。

接上之后原因直接进入客户端的错误、也就是测试自己的失败信息，"让服务端日志能归属到测试"这个大工程因此**不必做**。

## What Changes

- 新增纯函数 `disconnectCodeFor`，把三个具名关闭原因映射成 `DisconnectCode`：`errHeartbeatTimeout` → `DisconnectTimeout`、`errInvalidHeartbeatReply` 与 `errUnknownClientMessage` → `DisconnectProtocolViolation`。
- `session.fail` 在 `shutdown()` 之前尽力发出该包，用独立的 200ms 上下文直接调 `endpoint.Send`，错误一律忽略。

**协议零改动，客户端零改动**——两者早已完整实现。无版本变更，无迁移。

**明确不做**：白名单之外的任何原因都不发送。`network.ErrClosed` 与 `context.Canceled` 表示客户端已经走了；writer 自身 `Send` 失败或 panic 时 socket 已不可信，且 `writeLoop` 正在其调用栈内，发送会构成重入。黑名单形态会在将来新增关闭原因时默认放行，恰好放行到风险最大的地方。

**明确不做**：`errSessionOutboxFull` 不在白名单内（实施中证伪后移出）。`enqueue` 在 outbox 满时**同步**调用 `fail`，若发送同步等 200ms 会打破它写在注释里的不变量「永不等待 writer」，而它位于每 tick、每会话、每消息的发布热路径上。三条理由方向一致：它在热路径上；outbox 满意味着客户端没在消费、发送按构造徒劳；它是唯一已有服务端日志（`slog.Warn("慢客户端 outbox 已满，关闭 session")`）的原因。这也印证了白名单原则本身——连接不可用时不发送。

**明确不做**：`DisconnectSlowClient`、`DisconnectServerShutdown` 与 `DisconnectInternalError` 不使用。关服走 `detachSessionLocked(id, generation, nil)`、`cause` 为 `nil`，根本不经过带具名错误的 `fail`；而没有任何具名原因映射到 `InternalError`。

**明确不做**：不修任何 `transport closed` 失败。本变更让下一次失败自己说出原因，三条 CI 红灯会继续红。

## Capabilities

### New Capabilities

- `session-disconnect-reason`: 服务端主动关闭已建立会话时向客户端告知原因的行为契约。

### Modified Capabilities

（无。）

## Impact

- `internal/server/session.go`：新增 `disconnectCodeFor` 与 `(*session).sendDisconnect`，`fail` 增加一行调用。
- `internal/server/session_test.go`：映射的白名单单测（含变异验证）、`fail` 的发送与不发送单测、既有语义不变的单测。
- `internal/network/`：**零改动**。协议、codec、golden、客户端处理全都已存在。
- 依赖：不新增任何依赖。
- 并发：`sendDisconnect` 从心跳/reader goroutine 调 `endpoint.Send`，与 `writeLoop` 的并发 Send 由 `tcpStream.writeOwner` 串行；`acquire` 尊重 ctx，200ms 期限同时兜住等锁时间。必须以 `-race` 全包验证。
- 性能：`session.fail` 处在慢客户端清理与关服的公共路径上，发送必须不产生可感知停顿。
- **已知的独立缺口（记录不修）**：`host.go` 的 accept 循环在 `acquirePreLogin` 失败时 `continue`，既不关流也不记日志；`hostPreLoginCapacity = 16` 而仓库里有 `connectTask16ConcurrentClients` 这样并发数正好等于 16 的测试助手，零余量。本次三条失败已排除该路径（登录已成功），但值得单独查。
