> 完整调查记录、被排除的假说与被否决的替代方案见
> `docs/superpowers/specs/2026-08-07-session-disconnect-reason-design.md`。
> 本文件只记录实现选择。

## 为什么接线而不是加日志

最初的方向是"给服务端关闭路径补日志"。它解决不了问题：服务端 `slog` 直写 stderr，与 `go test` 的按测试缓冲不在一个通道上；而故障注入测试会故意制造几十条「连接登录失败」，与真实的一条在日志里无法区分。要让日志可归属，得改动每个测试的装配方式。

接上 `Disconnect` 之后原因直接进入客户端 `Recv` 返回的 `RemoteError`——也就是测试自己的失败信息。这比"让日志能归属"小一个数量级，且复用了既有的 `RemoteError`（登录拒绝路径上已被广泛使用与断言）。

## 为什么白名单是唯一安全的形态

`writeLoop` 在 `endpoint.Send` 失败或 panic 时会调用 `fail`。若默认发送，`sendDisconnect` 会在 `writeLoop` 的调用栈内再次调用 `endpoint.Send`——既徒劳（socket 已坏），又构成重入。

白名单让这两种情况自然落在不发送的一侧，**无需任何额外的"是否来自 writer"判断**。这是它相对黑名单的关键优势：黑名单需要显式识别写失败路径，而写失败的错误形态是开放的（来自底层 `net` 包），列不全。

三个具名原因分别由心跳 goroutine（`errHeartbeatTimeout`）与 reader goroutine（`errInvalidHeartbeatReply`、`errUnknownClientMessage`）触发，**没有一个来自 `writeLoop`，也没有一个在热路径上**。

## 为什么 errSessionOutboxFull 被移出白名单

原设计把它列为第四个具名原因，实施时证伪。`enqueue` 在 outbox 满时**同步**调用 `fail`，而它的注释写着「enqueue 永不等待 writer」——若发送同步等 200ms 就打破了这条不变量，既有测试 `TestSessionFullOutboxClosesWithoutBlocking` 会确定性失败。`enqueue` 位于每 tick、每会话、每消息的发布路径上，阻塞它的代价远大于一条诊断信息。

三条独立理由方向一致：它在热路径上（另外三个都不在）；outbox 满恰恰意味着客户端没在消费、发送按构造送不到；它是四个原因里唯一已有服务端日志的，可诊断性本来就不缺。

这印证了白名单原则本身——**连接不可用时不发送**。outbox 满与"writer 自身 Send 失败"同类，原设计没把这条原则贯彻到底。

## 两个不使用的码

- `DisconnectInternalError`：没有具名原因映射到它，硬凑用途是 YAGNI。
- `DisconnectServerShutdown`：关服走 `detachSessionLocked(id, generation, nil)`，`cause` 为 `nil`，根本不经过带具名错误的 `fail`。覆盖它要改关服路径本身，而关服不是本次三条失败的形态。设计自审时发现并移出范围。

## 并发与期限

`sendDisconnect` 从心跳/reader/发布 goroutine 调用 `endpoint.Send`，与 `writeLoop` 的并发 Send 由 `tcpStream.writeOwner` 这个 gate 串行化；`acquire(ctx)` 尊重上下文，因此 200ms 的期限**同时兜住了等锁的时间**，不需要额外机制。

必须在 `shutdown()` 之前发送：`shutdown` 会 `cancel()` 会话上下文并 `endpoint.Close()`。也正因如此不能用 `current.ctx`——它即将被取消——而要用独立的短期限上下文。

直接调 `endpoint.Send` 而不走 outbox：`errSessionOutboxFull` 本身就意味着 outbox 已满，`enqueue` 必然失败。

期限取 200ms：它是上界而非等待。socket 正常时写入立即返回，只有对端已死、缓冲区满、或 `writeLoop` 正持有 gate 时才会走到。`session.fail` 处在慢客户端清理与关服的公共路径上，这个值必须小到不产生可感知停顿。

## 被否决的替代方案

**给关闭路径补日志**：见上，解决不了归属问题。

**用黑名单排除已知的不该发送的原因**：写失败的错误形态开放，列不全；且新增关闭原因时默认放行到风险最大的一侧。

**新增一个专用的断开原因协议包**：协议里已经有 `Disconnect{Code, Message}`，codec 与客户端处理都已完备。新增等于重复造轮子并触发协议版本变更。

**在 `shutdown()` 内部发送**：`shutdown` 已经 `cancel` 了上下文并关闭 endpoint，发不出去；且 `shutdown` 也被非失败路径调用，会把发送扩散到不该发的地方。

**为 `errSessionOutboxFull` 改成异步发送以避开阻塞**：异步会与 `shutdown()` 关闭 endpoint 竞争，要保证顺序就得延后 `shutdown`，等于把阻塞换个位置。而该原因本就徒劳且已有日志，直接移出白名单更省。
