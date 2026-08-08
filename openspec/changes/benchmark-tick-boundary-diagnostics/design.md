> 完整证据链与被否决的替代方案见
> `docs/superpowers/specs/2026-08-07-benchmark-tick-boundary-diagnostics-design.md`。
> 本文件只记录实现选择。

## 为什么这 50 毫秒不是被测试侧的采样吃掉的

这是读代码得到的结构事实，不是推测。`benchmarkServerInputDeadline` 在收到 tick 信号后**立刻**执行，位于 `readStats()` 与 `readRSS()` **之前**：

```go
select {
case signal = <-epoch.signals:          // ← 收到信号
case <-ctx.Done():
    return result, ctx.Err()
}
...
inputDeadline, err = benchmarkServerInputDeadline(signal)   // ← CI 上四条报错走这里
...
stats := readStats()                    // ← 采样在报错之后
```

CI 上那四条报错的文本正是 `benchmarkServerInputDeadline` 的返回值被 `fmt.Errorf("measured tick %d: %w", ...)` 包裹后的形态。

因此 50 毫秒消耗在「服务端调度 tick」到「测试 goroutine 得以从 channel 取出该信号」之间，测试侧此时还没做任何采样工作。这把候选机制从"任何环节"收窄到三个。

## 为什么队列深度是最有判别力的一项

`epoch.signals` 是**有缓冲**的，发送用 `select`/`default` 非阻塞，溢出只置 `overflow` 标志。这意味着服务端永远不会因测试侧落后而被阻塞——落后只表现为信号在缓冲里堆积。

于是：

- **深度 > 0** ⇒ 缓冲里还压着别的信号 ⇒ 测试 goroutine 确实落后了 ⇒ 取出的信号 `scheduled` 已经陈旧 ⇒ 测量无效。
- **深度 = 0** ⇒ 测试侧是及时的 ⇒ 时间花在服务端侧 ⇒ 由 `duration` 与"调度→发布"区分是 tick 自身慢还是回调链路被晾。

**已知的不精确**：取的是取出信号**之后**的 `len(epoch.signals)`，与"取出那一刻缓冲里有多少"差一个。方向与判别力不受影响——大于 0 仍然坐实测试侧落后。该说明写进 `formatTickBoundaryOverrun` 的注释，避免日后误读为精确队列长度。

## 为什么把格式化抽成纯函数

这段代码在开发机上**永远不会执行**：CI 的失败形态本地复现不出来（前一变更已实测确认 `GOMAXPROCS=1` 是悬崖不是梯度，满载多核也复现不出）。

若把格式化埋在探针运行路径里，它就没有任何验证手段——等到 CI 真的红了才发现分解算错或字段填反，那时已经浪费了一次宝贵的失败样本。抽成 `formatTickBoundaryOverrun(signal, now, queueDepth) string` 后可直接以构造输入单测。

附带好处：分解逻辑与探针运行解耦，下一个变更做"测量无效 vs 越界"分离时可以复用同一份判据。

## 被否决的替代方案

**临时改小 `fixedBenchmarkFrameDuration` 来触发失败做验证**：设计自审时发现并否决。该常量还被 `probe.roster.Advance`（`multiplayer_benchmark.go:199`）与客户端帧预算（`benchmark.go:681`）使用，改它会连带改变无关行为，验证结果不可信。

**直接做"测量无效 vs 越界"的分离**：它的正当性完全取决于"50 毫秒是被环境吃掉的"这个前提，而该前提目前没有证据。若数据显示是服务端自己晚了，这个方案会把真性能问题盖住。前一个变更正是这样栽的——从代码结构推出一个自洽的故事就动手。

**放宽 50ms**：违反"不得放宽既有性能门禁"，且 50ms 就是 tick 周期本身，改它等于改变测量的含义。

**在 observer 回调里记录更多上下文（如 goroutine 数、GC 统计）**：YAGNI。三个候选机制用四段时间加一个队列深度就能区分；等数据显示不够再加。
