> 完整步骤级代码见 `docs/superpowers/plans/2026-08-07-session-disconnect-reason.md`。
> 本文件是可勾选的执行顺序与验证命令。三个任务组与该计划的 Task 1–3 一一对应。

> **本变更不修任何红灯。** 三条 `transport closed` 会继续红，它只让下一次失败自己说出原因。
> 因此**不能用"CI 变绿"验证**。

> **协议与客户端零改动。** `internal/network/` 下任何文件出现在 diff 里都要停手报告。

## 1. 原因到断开码的白名单映射

- [x] 1.1 先写失败测试：四个具名原因各自映射到正确的 `DisconnectCode`；`nil`/`ErrClosed`/`Canceled`/包装了 `ErrClosed` 的错误/writer 写失败/writer panic 一律不映射；包装后的具名原因仍被识别。
- [x] 1.2 确认失败原因是断言而非编译错误——先加最小桩再跑。
- [x] 1.3 实现 `disconnectCodeFor(err error) (network.DisconnectCode, bool)`，用 `errors.Is` 逐个匹配四个具名原因，`default` 返回不发送。注释写明为什么是白名单而非黑名单。
- [x] 1.4 验证转绿。
- [x] 1.5 **变异验证**：把 `default` 改成"允许发送"，确认拒绝用例变红——**仍然通过说明白名单边界没被守住，必须加强断言**。这条测试是本变更防重入的唯一自动化保障。恢复后 `git diff` 干净。提交 `feat: 增加会话关闭原因到断开码的白名单映射`。

## 2. 在 session.fail 中尽力发出断开原因

- [x] 2.1 先写失败测试：具名原因关闭时 `recordingServerEndpoint` 收到带正确 code 与 message 的 `Disconnect`；`ErrClosed`/`Canceled`/writer 写失败关闭时**不得**收到；`failOnce` 只生效一次、`detach` 携带首个原因、`closed()` 为真。
- [x] 2.2 确认失败原因是断言而非编译错误。
- [x] 2.3 实现 `(*session).sendDisconnect(err error)`：白名单判定、独立的 200ms 上下文、直接调 `endpoint.Send`、错误忽略。常量注释写明 200ms 是上界而非等待，且覆盖等 `writeOwner` 的时间。
- [x] 2.4 接进 `fail`——**除新增的一行外，`failOnce`/`shutdown()`/`detach` 的顺序与形态一个字不改**。
- [x] 2.5 验证转绿。
- [x] 2.6 **`-race` 全包回归**：`sendDisconnect` 从心跳/reader goroutine 调 Send，与 `writeLoop` 并发；报出任何 endpoint 相关竞态即停手报告。记录改前改后耗时（改前基线约 115 秒）——**显著变长说明有非预期路径在等满 200ms**。
- [x] 2.7 确认 `git diff --stat internal/network/` **无输出**。提交 `feat: 会话因具名原因关闭时告知客户端断开原因`。

## 3. 收尾门禁与文档

- [x] 3.1 回填设计文档：变异验证的实际输出、`-race` 结果与改前改后耗时、`internal/network` 空 diff 确认。任何与实测不符的表述一并订正。
- [x] 3.2 确认改动范围：`git diff --stat main...HEAD` 只应含 `internal/server/session.go`、`internal/server/session_test.go` 与 docs/openspec 文件。**出现 `internal/network/` 下任何文件必须停手报告。**
- [x] 3.3 收尾门禁：`go test ./... -race`、`go test ./internal/archcheck -count=1`、`go vet ./...`、`gofmt -l .` 无输出、`git diff --check` 无输出、`openspec validate --all --strict --no-interactive`。若 `-race` 失败先看是不是已知的 `TestDropSurvivesShutdownAndRestart` 偶发挂起。
- [x] 3.4 提交 `docs: 回填断开原因的实测结果`。
- [x] 3.5 **明确报告本变更不会让 CI 变绿**。下一步：等 CI 上再次出现该类失败、从失败信息读出 `RemoteError` 携带的断开码再判根因。**若失败仍然不带原因，说明关闭走的是白名单之外的路径——那本身是有价值的信息，应据此定位具体路径并显式列举，不得把白名单改为默认允许。**
