package lod

// genResult 是 worker 交回帧线程的一条生成结果。
//
// 并发纪律:quads 在 worker 内按 len 定长拷贝后经 results 通道交接,
// 发送后视为不可变(既有消息纪律);帧线程接收后只读并透传给 `TileSink`。
type genResult struct {
	tile  TilePos
	quads []byte
}

// runWorker 是壳生成 worker,循环形态镜像 render 字形 worker:先查 stop,
// 再阻塞取请求,生成后阻塞回传,任一阶段 stop 优先退出。generate 由
// 构造注入(生产路径见 `NewScheduler` 的 `GenerateShell` 闭包,测试注入
// 确定性替身);闭包捕获的状态构造后只读,worker 与帧线程之间除
// requests/results/stop 通道外无任何共享可变状态。
func (s *Scheduler) runWorker(generate func(TilePos) []byte) {
	defer close(s.done)
	for {
		select {
		case <-s.stop:
			return
		default:
		}
		select {
		case <-s.stop:
			return
		case tile := <-s.requests:
			shell := generate(tile)
			// `nativeabi.LodShell` 的返回切片按步长静态上界一次性预分配,
			// 仅前 len 字节有效(step 2 时 len 10960 / cap 62720,约 6 倍
			// 容量滞留)。跨 goroutine 交接前必须按 len 显式定长拷贝:
			// 一方面让上界缓冲留在 worker 栈内尽快回收,避免整份 6 倍
			// 容量随结果切片长期滞留;另一方面使交接切片与生成缓冲彻底
			// 脱钩,从物理上保证"发送后不可变"——即便 generate 复用同一
			// 底层数组,接收侧也不会观察到后续写入。
			quads := make([]byte, len(shell))
			copy(quads, shell)
			select {
			case s.results <- genResult{tile: tile, quads: quads}:
			case <-s.stop:
				return
			}
		}
	}
}
