package lod

import (
	"cmp"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/nativeabi"
)

// TileSink 是远环 tile 的上传与释放出口,形状镜像近环 `render.SectionSink`
// 的 `UploadSection`/`DropSection`(本包不得 import internal/render,只复刻
// 形状);由 cmd/mornlea 的接线层(任务 5.2)适配到 client ABI v6 的
// render_upload_lod_tile / render_drop_lod_tile(变基重编:tile 出口原编号
// v5,main 的 water pass 占用 v5 后顺延为 v6)。quads 为 20 字节 LE
// quad 字节流,接收方只读、发送后不可变。
type TileSink interface {
	// `UploadLodTile` 整体上传一个 tile 的壳 quad 流;重复上传同 tile 即
	// 整体替换(与近环 section 的覆盖语义一致)。
	UploadLodTile(x, z int32, quads []byte)
	// `DropLodTile` 释放一个已上传 tile 的 GPU 资源。
	DropLodTile(x, z int32)
}

// TilePos 是远环 tile 在 tile 坐标系(每 tile 固定 4×4 chunk)中的坐标;
// 中心与半径均以该坐标系表达(切比雪夫距离),chunk→tile 换算留在接线
// 层(任务 5.2)。复用 `core.ChunkPos` 的 {X, Z} 形状,不引入第二套坐标类型。
type TilePos = core.ChunkPos

const (
	// schedulerRequestCapacity 是派发请求通道的容量:既能吸收一帧派发,
	// 又限制 worker 在途生成的 tile 数(在途内存上界 = 容量 × 步长静态
	// 上界字节)。
	schedulerRequestCapacity = 8
	// schedulerResultCapacity 是结果通道的容量:worker 领先帧线程时最多
	// 积压的结果条数(最坏 8 × 62720B ≈ 0.5MB @step 2),同时构成单帧
	// 上传冲刷量的天然上界。
	schedulerResultCapacity = 8
)

// Scheduler 调度远环 tile 壳的生成与上传,语义镜像近环
// `render.SectionScheduler`(该包不可 import,此处按其语义独立实现):
// pending map 覆盖最新请求、按与中心的切比雪夫距离升序冲刷、
// DropOutside 释放 [inner, outer] 带外的 pending 与已上传 tile;生成调用
// 与上传共用一个独立 LOD 帧预算(绝不与近环共享)。壳生成在独立 worker
// goroutine 内执行(镜像 render 字形 worker 模型),结果字节切片跨
// goroutine 交接后视为不可变。
//
// 并发约束:pending/inflight/uploaded/budget 与全部公开调度方法只允许
// 帧线程(渲染循环)调用;worker goroutine 只触碰 requests/results 两个
// 通道与构造时注入的 generate 闭包(其捕获的 header 副本构造后只读),
// 两侧除通道外无共享可变状态。生命周期经 `Close` 关闭,幂等且不泄漏
// goroutine。
type Scheduler struct {
	sink   TileSink
	budget *frameBudget
	// bound 是步长静态上界字节数:派发计费单位,亦即单次 `LodShell` 调用
	// 的输出与分配上界(确定性的最坏足迹,与地形内容无关)。
	bound int

	pending  map[TilePos]struct{} // 已入队未派发(pending 覆盖合并最新请求)
	inflight map[TilePos]struct{} // 已派发未冲刷(`DropOutside` 放弃的条目会被移除)
	uploaded map[TilePos]struct{} // 已上传 sink(重新入队的替换结果到达前保留)
	keys     []TilePos            // 冲刷排序的复用缓冲(镜像 SectionScheduler.keys)

	requests  chan TilePos
	results   chan genResult
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
	closed    atomic.Bool
}

// NewScheduler 以生产生成路径(`GenerateShell` → `nativeabi.LodShell`)构造
// 调度器:header 为登录播种的 worldgen `MGW1` header(构造时定长拷贝,
// 调用方后续修改不影响生成),step 为列合并步长,bytesPerFrame 为 LOD
// 独立帧预算(默认与近环同量级,数值只记录不门禁)。sink 为 nil、header
// 长度或步长非法时返回带上下文的错误。
func NewScheduler(sink TileSink, header []byte, step uint32, bytesPerFrame uint32) (*Scheduler, error) {
	if sink == nil {
		return nil, fmt.Errorf("lod: 构造 Scheduler 的 TileSink 为 nil")
	}
	if len(header) != worldgenHeaderBytes {
		return nil, fmt.Errorf("lod: header 长度 %d 非法，想要 %d", len(header), worldgenHeaderBytes)
	}
	if !ValidStep(step) {
		return nil, fmt.Errorf("lod: 步长 %d 非法，合法值 2/4/8", step)
	}
	// header 定长拷贝归调度器所有:构造后调用方对原切片的任何修改都
	// 不会影响确定性生成(跨帧长期持有的输入必须摆脱外部别名)。
	owned := slices.Clone(header)
	// 生成错误只剩 header/step 违约一种可能,而二者已在上方校验,闭包
	// 内的错误分支不可达;engine 侧失败(版本、tile 越界等编程错误)由
	// nativeabi 绑定以稳定中文文案 panic,镜像 worldgen 生产路径。此处
	// 保底 panic 而非静默丢 tile。
	generate := func(tile TilePos) []byte {
		shell, err := GenerateShell(owned, tile, step)
		if err != nil {
			panic(fmt.Sprintf("lod: tile (%d,%d) 生成请求违约(构造时已校验，不可达): %v", tile.X, tile.Z, err))
		}
		return shell
	}
	return newScheduler(sink, step, bytesPerFrame, generate)
}

// newScheduler 以注入的 generate 构造调度器(生产闭包见 `NewScheduler`,
// 测试注入确定性替身;两者共用同一装配与 worker 模型)。
func newScheduler(sink TileSink, step uint32, bytesPerFrame uint32, generate func(TilePos) []byte) (*Scheduler, error) {
	if sink == nil {
		return nil, fmt.Errorf("lod: 构造 Scheduler 的 TileSink 为 nil")
	}
	if !ValidStep(step) {
		return nil, fmt.Errorf("lod: 步长 %d 非法，合法值 2/4/8", step)
	}
	bound, ok := nativeabi.LodShellOutputBoundBytes(step)
	if !ok {
		return nil, fmt.Errorf("lod: 步长 %d 无静态输出上界", step)
	}
	scheduler := &Scheduler{
		sink:     sink,
		budget:   newFrameBudget(bytesPerFrame),
		bound:    bound,
		pending:  make(map[TilePos]struct{}),
		inflight: make(map[TilePos]struct{}),
		uploaded: make(map[TilePos]struct{}),
		requests: make(chan TilePos, schedulerRequestCapacity),
		results:  make(chan genResult, schedulerResultCapacity),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	go scheduler.runWorker(generate)
	return scheduler, nil
}

// BeginFrame 重置 LOD 帧预算(渲染循环每帧先于 `FlushUploads` 调用)。
func (s *Scheduler) BeginFrame() {
	if s.closed.Load() {
		return
	}
	s.budget.beginFrame()
}

// QueueTile 排队(重新)生成一个 tile,是 pending 覆盖语义的 tile 形态:
// 壳输出对 (header, tile, step) 确定,重复请求与既有请求等价,合并为
// 单条 pending、不产生第二次派发;已上传 tile 重新入队会在冲刷时重新
// 生成并整体替换(镜像 render_upload_lod_tile 的替换语义);生成途中
// (inflight)的重复请求被吸收。关闭后为安全 no-op。
func (s *Scheduler) QueueTile(tile TilePos) {
	if s.closed.Load() {
		return
	}
	if _, ok := s.inflight[tile]; ok {
		return
	}
	s.pending[tile] = struct{}{}
}

// QueueRing 把以 center 为中心、切比雪夫距离在 [inner, outer] 闭区间的
// 全部 tile 入队(登录播种远环带与跨 tile 边界的增量入队共用一个入口):
// 已 pending / inflight / uploaded 的 tile 跳过,天然幂等,重复调用只补
// 新进入范围的 tile。
//
// inner 是近环排除半径(Ruling 19):近 mesh 覆盖之内的 tile 不入队。
// 原因是壳的步长窗高取 max,系统性高于精细地表,若在近环内渲染会
// poke 出地表、以更近的深度遮挡近处 mesh——capture 的「近处像素不变」
// 门禁因此在构造上依赖内盘排除。inner 由调用方按 `viewDistance` 推导
// (cmd/mornlea 的 `lodNearTileRadius`,保证壳最小覆盖块 ≥ 近 mesh 覆盖
// 半径、与近 mesh 零重叠);inner=0 退化为全盘入队(旧语义)。
// inner < 0 或 outer < inner 不入队任何 tile。关闭后为安全 no-op。
func (s *Scheduler) QueueRing(center TilePos, inner, outer int) {
	if s.closed.Load() || inner < 0 || outer < inner {
		return
	}
	for dz := -outer; dz <= outer; dz++ {
		for dx := -outer; dx <= outer; dx++ {
			// 切比雪夫距离 = max(|dx|, |dz|);内盘(距离 < inner)跳过。
			if max(max(-dx, dx), max(-dz, dz)) < inner {
				continue
			}
			tile := TilePos{X: center.X + int32(dx), Z: center.Z + int32(dz)}
			if _, ok := s.inflight[tile]; ok {
				continue
			}
			if _, ok := s.uploaded[tile]; ok {
				continue
			}
			s.pending[tile] = struct{}{}
		}
	}
}

// FlushUploads 冲刷一帧:先非阻塞排空 worker 已就绪的结果并上传(FIFO
// 交接,单 worker 下交接顺序即派发顺序的距离升序),再按与 center 的
// 切比雪夫距离升序(并列按 X/Z 字典序,保证跨帧确定性)把 pending 派发
// 给 worker;每次派发按步长静态上界字节数从 LOD 帧预算计费——该上界
// 即单次 `LodShell` 调用的分配与输出上界,生成与上传共用这一个预算——
// 预算耗尽即停,剩余 tile 留在 pending 等下一帧。关闭后为安全 no-op。
func (s *Scheduler) FlushUploads(center TilePos) {
	if s.closed.Load() {
		return
	}
	s.drainResults()
	s.dispatchPending(center)
}

// drainResults 非阻塞排空结果通道并逐条上传(仅帧线程调用)。
func (s *Scheduler) drainResults() {
	for {
		select {
		case result := <-s.results:
			s.acceptResult(result)
		default:
			return
		}
	}
}

// acceptResult 处理一条生成结果:`DropOutside` 已放弃(不在 inflight)的
// tile 直接丢弃不上传;空壳镜像近环语义立即下沉为 drop;正常结果整体
// 上传并登记 uploaded。result.quads 自通道接收后只读(发送后不可变纪律),
// 透传给 `TileSink` 后仍归调度器视为不可变。
//
// 放弃后又重新入队派发的 tile 可能先收到旧的在途结果:壳输出对
// (header, tile, step) 确定,旧结果与新结果逐字节一致,先到先传、后到
// 丢弃,最终恰好一次上传正确内容,无需按内容判新旧。
func (s *Scheduler) acceptResult(result genResult) {
	if _, wanted := s.inflight[result.tile]; !wanted {
		return
	}
	delete(s.inflight, result.tile)
	if len(result.quads) == 0 {
		if _, ok := s.uploaded[result.tile]; ok {
			delete(s.uploaded, result.tile)
			s.sink.DropLodTile(result.tile.X, result.tile.Z)
		}
		return
	}
	s.uploaded[result.tile] = struct{}{}
	s.sink.UploadLodTile(result.tile.X, result.tile.Z, result.quads)
}

// dispatchPending 按距离升序把 pending 派发给 worker(仅帧线程调用)。
func (s *Scheduler) dispatchPending(center TilePos) {
	s.keys = s.keys[:0]
	for tile := range s.pending {
		s.keys = append(s.keys, tile)
	}
	slices.SortFunc(s.keys, func(a, b TilePos) int {
		if c := cmp.Compare(tileChebyshev(a, center), tileChebyshev(b, center)); c != 0 {
			return c
		}
		if c := cmp.Compare(a.X, b.X); c != 0 {
			return c
		}
		return cmp.Compare(a.Z, b.Z)
	})
	// 请求通道只有帧线程一个发送方,空位数在派发循环内精确有效;单帧
	// 派发量限制为当前空位,保证下方发送永不阻塞帧线程(worker 慢时
	// tile 留在 pending,下一帧再派发)。
	limit := cap(s.requests) - len(s.requests)
	dispatched := 0
dispatch:
	for _, tile := range s.keys {
		if dispatched >= limit {
			break
		}
		if !s.budget.tryConsume(uint32(s.bound)) {
			break // 预算耗尽即停
		}
		select {
		case s.requests <- tile:
			delete(s.pending, tile)
			s.inflight[tile] = struct{}{}
			dispatched++
		default:
			// 理论不可达(单发送方已保证空位);防御性保底,避免未来
			// 引入第二个发送方时阻塞帧线程。预算多扣一个上界字节,
			// 只会保守,不会超额。
			break dispatch
		}
	}
}

// PendingUploads 返回尚未派发的 tile 数(镜像 `SectionScheduler` 的同名
// 语义,供测试与收敛循环)。
func (s *Scheduler) PendingUploads() int { return len(s.pending) }

// Busy 返回仍未完成的 tile 总数(未派发 + 生成中 + 已就绪未冲刷),供
// 收敛循环与就绪判断;`DropOutside` 放弃的在途 tile 在下一次冲刷时归零。
func (s *Scheduler) Busy() int {
	return len(s.pending) + len(s.inflight) + len(s.results)
}

// DropOutside 丢弃不在 [inner, outer] 带内的 tile:切比雪夫距离 > outer
// 或 < inner 的 pending 被丢弃、已上传的触发 `TileSink` 的 `DropLodTile` 释放
// GPU 资源,并把带外的在途生成从登记中移除——其结果到达冲刷时会被直接
// 丢弃,不上传已放弃的 tile。内半径语义与 `QueueRing` 对称(Ruling 19):
// 玩家跨 tile 边界后,曾在外带、现已落入近 mesh 覆盖的 tile 必须连同
// GPU 资源让位,否则内盘残留壳会在近 mesh 之上 poke 出地表,近环零
// 重叠只在静止时成立。关闭后为安全 no-op。
func (s *Scheduler) DropOutside(center TilePos, inner, outer int) {
	if s.closed.Load() {
		return
	}
	// inner/outer 均为调用方推导的非负值;防御性钳制让非法/空区间
	// (含负外半径)退化为全释放,与旧语义「负半径丢弃一切」一致,不会
	// 误保留任何 tile:lo=hi=−1 时切比雪夫距离(恒 ≥ 0)全部判定界外。
	lo, hi := int64(inner), int64(outer)
	if hi < lo {
		lo, hi = -1, -1
	}
	outOfBand := func(tile TilePos) bool {
		d := tileChebyshev(tile, center)
		return d < lo || d > hi
	}
	for tile := range s.pending {
		if outOfBand(tile) {
			delete(s.pending, tile)
		}
	}
	for tile := range s.inflight {
		if outOfBand(tile) {
			delete(s.inflight, tile)
		}
	}
	for tile := range s.uploaded {
		if outOfBand(tile) {
			delete(s.uploaded, tile)
			s.sink.DropLodTile(tile.X, tile.Z)
		}
	}
}

// Close 停止 worker 并等待其退出:幂等,关闭后所有公开方法为安全
// no-op、不再产生任何 sink 调用。正在执行的生成必须有限返回
// (nativeabi 契约保证),`Close` 会等当前生成完成;已派发但未生成的
// 请求与通道中未冲刷的结果一并作废,不触发上传。
func (s *Scheduler) Close() {
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		close(s.stop)
	})
	<-s.done
}

// tileChebyshev 返回两 tile 的切比雪夫距离;差值经 int64 计算避免
// int32 减法在极端坐标下的溢出。
func tileChebyshev(a, b TilePos) int64 {
	dx := int64(a.X) - int64(b.X)
	dz := int64(a.Z) - int64(b.Z)
	if dx < 0 {
		dx = -dx
	}
	if dz < 0 {
		dz = -dz
	}
	if dx > dz {
		return dx
	}
	return dz
}
