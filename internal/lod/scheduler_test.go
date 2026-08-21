package lod

// 本文件是任务 5.1 Scheduler 的行为测试:环形入队、pending 覆盖、距离
// 升序冲刷、独立帧预算耗尽即停、DropOutside 释放与 sink drop、worker
// 交接切片不可变(-race)与关闭安全。生成路径经 newScheduler 注入确定性
// 替身,不依赖 cdylib 时序;native 真路径另有一条确定性比对测试。

import (
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/nativeabi"
)

// fakeShellBytes 是测试替身壳流的固定字节数(预算按步长静态上界计费,
// 替身长度不影响预算语义,只影响内容校验)。
const fakeShellBytes = 40

// schedulerUpload 记录一次 tile 上传(保留透传切片,供 cap 与内容断言)。
type schedulerUpload struct {
	tile  TilePos
	quads []byte
}

// recordingSink 记录上传与释放调用,供顺序、次数与内容断言;只在帧线程
// (测试主 goroutine)被调用,无并发。
type recordingSink struct {
	uploads []schedulerUpload
	drops   []TilePos
}

func (r *recordingSink) UploadLodTile(x, z int32, quads []byte) {
	r.uploads = append(r.uploads, schedulerUpload{tile: TilePos{X: x, Z: z}, quads: quads})
}

func (r *recordingSink) DropLodTile(x, z int32) {
	r.drops = append(r.drops, TilePos{X: x, Z: z})
}

// patternShell 生成与 tile 内容相关的确定性壳字节(替身 generate 与
// 不可变交接测试的共享 pattern)。
func patternShell(tile TilePos) []byte {
	shell := make([]byte, fakeShellBytes)
	for i := range shell {
		shell[i] = byte(i*7 + int(tile.X)*3 + int(tile.Z)*11)
	}
	return shell
}

// fakeGenerate 是默认测试替身:全新分配、逐 tile 可区分。
func fakeGenerate(tile TilePos) []byte { return patternShell(tile) }

// newTestScheduler 以注入 generate 构造调度器(步长固定 4,与预算计费的
// 静态上界 16000 字节对应);Close 挂到 t.Cleanup。
func newTestScheduler(t *testing.T, bytesPerFrame uint32, generate func(TilePos) []byte) (*Scheduler, *recordingSink) {
	t.Helper()
	sink := &recordingSink{}
	scheduler, err := newScheduler(sink, 4, bytesPerFrame, generate)
	if err != nil {
		t.Fatalf("构造测试调度器失败: %v", err)
	}
	t.Cleanup(scheduler.Close)
	return scheduler, sink
}

// flushUntilIdle 反复 BeginFrame+FlushUploads 直到 Busy 归零(带超时),
// 供"全部上传完成"类断言收敛。
func flushUntilIdle(t *testing.T, s *Scheduler, center TilePos) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for s.Busy() > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("冲刷未收敛: Busy=%d pending=%d", s.Busy(), s.PendingUploads())
		}
		s.BeginFrame()
		s.FlushUploads(center)
		time.Sleep(time.Millisecond)
	}
}

// waitForResults 轮询等待结果通道积累至少 n 条(白盒;len(channel) 与
// 通道收发同为同步操作,-race 下安全)。
func waitForResults(t *testing.T, s *Scheduler, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(s.results) < n {
		if time.Now().After(deadline) {
			t.Fatalf("结果就绪超时: %d/%d", len(s.results), n)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestFrameBudgetMirrorsUploadBudgetSemantics(t *testing.T) {
	budget := newFrameBudget(100)
	if !budget.tryConsume(60) || budget.spent != 60 {
		t.Fatalf("预算内消耗失败: spent=%d", budget.spent)
	}
	if budget.tryConsume(50) {
		t.Fatal("超预算请求(已消耗>0)应被拒绝")
	}
	if !budget.tryConsume(40) || budget.spent != 100 {
		t.Fatal("恰好用满应放行")
	}
	if budget.tryConsume(1) {
		t.Fatal("用满后应拒绝")
	}
	budget.beginFrame()
	if budget.spent != 0 || budget.exhausted {
		t.Fatal("帧重置失效")
	}
	// 首个超预算请求放行一次,避免预算小于单次请求时永久饥饿。
	starved := newFrameBudget(10)
	if !starved.tryConsume(50) {
		t.Fatal("首个超预算请求应放行一次")
	}
	if starved.tryConsume(1) {
		t.Fatal("放行后应视为耗尽")
	}
	starved.beginFrame()
	if !starved.tryConsume(10) {
		t.Fatal("新帧预算未恢复")
	}
}

func TestNewSchedulerValidatesArguments(t *testing.T) {
	sink := &recordingSink{}
	if _, err := NewScheduler(nil, testHeader(), 4, 1024); err == nil {
		t.Fatal("nil sink 未被拒绝")
	}
	if _, err := NewScheduler(sink, testHeader()[:563], 4, 1024); err == nil {
		t.Fatal("短 header 未被拒绝")
	}
	if _, err := NewScheduler(sink, testHeader(), 3, 1024); err == nil {
		t.Fatal("非法步长未被拒绝")
	}
	scheduler, err := NewScheduler(sink, testHeader(), 4, 1024)
	if err != nil {
		t.Fatalf("合法参数被拒绝: %v", err)
	}
	scheduler.Close()
}

func TestQueueRingEnqueuesRingIncrementally(t *testing.T) {
	s, _ := newTestScheduler(t, 4<<20, fakeGenerate)
	s.QueueRing(TilePos{X: 0, Z: 0}, 0, 1)
	if got := s.PendingUploads(); got != 9 {
		t.Fatalf("半径 1 环形入队 %d 块,想要 9", got)
	}
	s.QueueRing(TilePos{X: 0, Z: 0}, 0, 1) // 幂等:重复播种不重复入队
	if got := s.PendingUploads(); got != 9 {
		t.Fatalf("重复环形入队后 pending=%d,想要 9", got)
	}
	s.QueueRing(TilePos{X: 1, Z: 0}, 0, 1) // 跨 tile 边界:只增量入队新进入范围的 3 块
	if got := s.PendingUploads(); got != 12 {
		t.Fatalf("增量入队后 pending=%d,想要 12", got)
	}
	flushUntilIdle(t, s, TilePos{X: 1, Z: 0})
	s.QueueRing(TilePos{X: 0, Z: 0}, 0, 1) // 已上传 tile 不重复入队
	if got := s.PendingUploads(); got != 0 {
		t.Fatalf("已上传 tile 被重复入队: pending=%d", got)
	}

	s2, _ := newTestScheduler(t, 1024, fakeGenerate)
	s2.QueueRing(TilePos{X: 5, Z: -7}, 0, 0) // 半径 0 只入队中心
	if got := s2.PendingUploads(); got != 1 {
		t.Fatalf("半径 0 入队 %d 块,想要 1", got)
	}
	s2.QueueRing(TilePos{X: 5, Z: -7}, 0, -1) // 负外半径不入队
	if got := s2.PendingUploads(); got != 1 {
		t.Fatalf("负外半径入队后 pending=%d,想要 1", got)
	}
}

// TestQueueRingBandSkipsInnerDisk 锁住 Ruling 19 的带状入队语义:近环
// 之内的 tile(inner 半径内盘)不得入队——壳窗高取 max,系统性高于精细
// 地表,若在近环内渲染会 poke 出地表遮挡近处 mesh(capture 近处像素
// 不变门禁的构造性前提)。以默认几何 viewDistance=32 推导的 inner=9、
// outer=24 为代表:入队域恰为 (2×24+1)²−(2×8+1)²,且入队 tile 的最小
// 覆盖块 9×64=576 ≥ 近 mesh 覆盖半径 32×16=512,与近 mesh 零重叠。
func TestQueueRingBandSkipsInnerDisk(t *testing.T) {
	s, _ := newTestScheduler(t, 4<<20, fakeGenerate)
	const inner, outer = 9, 24
	s.QueueRing(TilePos{}, inner, outer)
	if got := s.PendingUploads(); got != 2401-289 {
		t.Fatalf("带状入队 %d 块,想要 %d((2×24+1)²−(2×8+1)²)", got, 2401-289)
	}
	for _, tile := range []TilePos{
		{X: 0, Z: 0}, {X: 8, Z: 0}, {X: 0, Z: -8}, {X: -8, Z: 8}, {X: 8, Z: -8},
	} {
		if _, ok := s.pending[tile]; ok {
			t.Fatalf("内盘 tile %v 被入队(近环零重叠被破坏)", tile)
		}
	}
	for _, tile := range []TilePos{
		{X: 9, Z: 0}, {X: 0, Z: -9}, {X: -9, Z: 9}, {X: 24, Z: 24}, {X: -24, Z: 0},
	} {
		if _, ok := s.pending[tile]; !ok {
			t.Fatalf("带内 tile %v 未入队", tile)
		}
	}
	if _, ok := s.pending[TilePos{X: 25, Z: 0}]; ok {
		t.Fatal("外半径外的 tile (25,0) 被入队")
	}

	// 非法区间(inner<0、outer<inner)一律不入队;inner=0 退化为全盘
	// (旧语义,QueueTile 直入的调用方仍可靠)。
	s2, _ := newTestScheduler(t, 4<<20, fakeGenerate)
	s2.QueueRing(TilePos{}, -1, 2)
	s2.QueueRing(TilePos{}, 3, 2)
	if got := s2.PendingUploads(); got != 0 {
		t.Fatalf("非法区间入队 %d 块,想要 0", got)
	}
	s2.QueueRing(TilePos{}, 0, 2)
	if got := s2.PendingUploads(); got != 25 {
		t.Fatalf("inner=0 全盘入队 %d 块,想要 25", got)
	}
}

func TestQueueTileCoalescesDuplicatesAndRequeuesAfterUpload(t *testing.T) {
	s, sink := newTestScheduler(t, 4<<20, fakeGenerate)
	tile := TilePos{X: 2, Z: -1}
	s.QueueTile(tile)
	s.QueueTile(tile) // pending 覆盖:重复请求合并为单条 pending
	if got := s.PendingUploads(); got != 1 {
		t.Fatalf("重复入队后 pending=%d,想要 1", got)
	}
	s.BeginFrame()
	s.FlushUploads(tile)
	if got := s.PendingUploads(); got != 0 {
		t.Fatalf("派发后 pending=%d,想要 0", got)
	}
	if len(s.inflight) != 1 {
		t.Fatalf("重复请求被派发 %d 次,想要 1", len(s.inflight))
	}
	s.QueueTile(tile) // 生成途中的重复请求被吸收
	if got := s.PendingUploads(); got != 0 {
		t.Fatalf("在途重复入队后 pending=%d,想要 0", got)
	}
	flushUntilIdle(t, s, tile)
	if len(sink.uploads) != 1 || sink.uploads[0].tile != tile {
		t.Fatalf("上传 %+v,想要单次 %v", sink.uploads, tile)
	}
	s.QueueTile(tile) // 已上传 tile 重新入队 → 重新生成并整体替换
	if got := s.PendingUploads(); got != 1 {
		t.Fatalf("重新入队后 pending=%d,想要 1", got)
	}
	flushUntilIdle(t, s, tile)
	if len(sink.uploads) != 2 {
		t.Fatalf("重新入队后上传 %d 次,想要 2(整体替换)", len(sink.uploads))
	}
}

func TestFlushUploadsDispatchesAndUploadsDistanceAscending(t *testing.T) {
	s, sink := newTestScheduler(t, 4<<20, fakeGenerate)
	tiles := []TilePos{
		{X: -2, Z: 1}, // 切比雪夫距离 2
		{X: 1, Z: 1},  // 距离 1
		{X: 0, Z: 0},  // 距离 0
		{X: -1, Z: 0}, // 距离 1
		{X: 0, Z: -2}, // 距离 2
	}
	for _, tile := range tiles {
		s.QueueTile(tile)
	}
	center := TilePos{X: 0, Z: 0}
	s.BeginFrame()
	s.FlushUploads(center) // 大预算:一次派发全部(按距离升序)
	waitForResults(t, s, len(tiles))
	s.BeginFrame()
	s.FlushUploads(center) // 排空结果:上传顺序 = 交接顺序 = 派发顺序
	want := []TilePos{
		{X: 0, Z: 0},  // 距离 0
		{X: -1, Z: 0}, // 距离 1,X 升序
		{X: 1, Z: 1},
		{X: -2, Z: 1}, // 距离 2,X 升序
		{X: 0, Z: -2},
	}
	if len(sink.uploads) != len(want) {
		t.Fatalf("上传 %d 次,想要 %d", len(sink.uploads), len(want))
	}
	for i, wantTile := range want {
		if got := sink.uploads[i].tile; got != wantTile {
			t.Fatalf("第 %d 次上传 %v != %v(距离升序失效)", i, got, wantTile)
		}
		if !slices.Equal(sink.uploads[i].quads, patternShell(wantTile)) {
			t.Fatalf("tile %v 上传内容失真", wantTile)
		}
	}
	if s.Busy() != 0 {
		t.Fatalf("冲刷后 Busy=%d,想要 0", s.Busy())
	}
}

func TestBudgetExhaustionStopsDispatchPerFrame(t *testing.T) {
	bound, ok := nativeabi.LodShellOutputBoundBytes(4) // step 4 静态上界 16000
	if !ok {
		t.Fatal("step 4 无静态上界")
	}
	// 预算 < 2×上界:每帧恰好派发 1 块,派发同步发生在帧线程,断言无时序依赖。
	s, _ := newTestScheduler(t, uint32(2*bound-1), fakeGenerate)
	for _, tile := range []TilePos{{X: 0, Z: 0}, {X: 3, Z: 3}, {X: -4, Z: 1}} {
		s.QueueTile(tile)
	}
	s.BeginFrame()
	s.FlushUploads(TilePos{})
	if _, ok := s.inflight[TilePos{X: 0, Z: 0}]; !ok {
		t.Fatal("预算受限时应优先派发最近的 tile")
	}
	for want := 1; want >= 0; want-- { // 首帧已派发 1 块,余下每帧恰 1 块
		s.BeginFrame()
		s.FlushUploads(TilePos{})
		if got := s.PendingUploads(); got != want {
			t.Fatalf("预算 %d 下每帧应只派发 1 块: pending=%d,想要 %d", 2*bound-1, got, want)
		}
	}
	flushUntilIdle(t, s, TilePos{})

	// 预算 ≥ 2×上界:每帧可派发 2 块。
	s2, _ := newTestScheduler(t, uint32(2*bound), fakeGenerate)
	for _, tile := range []TilePos{{X: 0, Z: 0}, {X: 1, Z: 0}, {X: 2, Z: 0}} {
		s2.QueueTile(tile)
	}
	s2.BeginFrame()
	s2.FlushUploads(TilePos{})
	if got := s2.PendingUploads(); got != 1 {
		t.Fatalf("预算 2×上界应派发 2 块: pending=%d,想要 1", got)
	}

	// 预算小于单块上界:防饥饿放行首个请求,每帧仍恰 1 块。
	s3, _ := newTestScheduler(t, 1, fakeGenerate)
	s3.QueueTile(TilePos{X: 0, Z: 0})
	s3.QueueTile(TilePos{X: 1, Z: 0})
	s3.BeginFrame()
	s3.FlushUploads(TilePos{})
	if got := s3.PendingUploads(); got != 1 {
		t.Fatalf("微型预算应放行首个请求: pending=%d,想要 1", got)
	}
}

func TestDropOutsideReleasesPendingUploadedAndInflight(t *testing.T) {
	// 场景 A:界外 pending 释放 + 在途结果到达后被放弃(不上传)。
	s, sink := newTestScheduler(t, 1, fakeGenerate) // 微型预算:首帧只派发最近的 (0,0)
	for _, tile := range []TilePos{{X: 0, Z: 0}, {X: 5, Z: 5}, {X: -6, Z: 1}} {
		s.QueueTile(tile)
	}
	s.BeginFrame()
	s.FlushUploads(TilePos{})
	if len(s.inflight) != 1 {
		t.Fatalf("微型预算下在途 %d 块,想要 1", len(s.inflight))
	}
	s.DropOutside(TilePos{X: 100, Z: 100}, 0, 0) // 中心移远:三块全部界外
	if got := s.PendingUploads(); got != 0 {
		t.Fatalf("界外 pending 未释放: %d", got)
	}
	if len(s.inflight) != 0 {
		t.Fatalf("界外在途未放弃: %d", len(s.inflight))
	}
	waitForResults(t, s, 1)
	s.BeginFrame()
	s.FlushUploads(TilePos{})
	if len(sink.uploads) != 0 {
		t.Fatalf("已放弃的 tile 被上传: %+v", sink.uploads)
	}
	if s.Busy() != 0 {
		t.Fatalf("放弃后 Busy=%d,想要 0", s.Busy())
	}

	// 场景 B:界外已上传 tile 触发 sink drop,界内保留。
	s2, sink2 := newTestScheduler(t, 4<<20, fakeGenerate)
	for _, tile := range []TilePos{{X: 0, Z: 0}, {X: 1, Z: 0}, {X: 2, Z: 0}} {
		s2.QueueTile(tile)
	}
	flushUntilIdle(t, s2, TilePos{})
	if len(sink2.uploads) != 3 {
		t.Fatalf("上传 %d 次,想要 3", len(sink2.uploads))
	}
	s2.DropOutside(TilePos{X: 0, Z: 0}, 0, 0)
	if len(sink2.drops) != 2 {
		t.Fatalf("界外已上传 tile 应触发 2 次 drop,得到 %v", sink2.drops)
	}
	if !slices.Contains(sink2.drops, TilePos{X: 1, Z: 0}) || !slices.Contains(sink2.drops, TilePos{X: 2, Z: 0}) {
		t.Fatalf("drop 目标错误: %v", sink2.drops)
	}
	if len(s2.uploaded) != 1 {
		t.Fatalf("界内已上传 tile 应保留 1 块,得到 %d", len(s2.uploaded))
	}
	if _, ok := s2.uploaded[TilePos{X: 0, Z: 0}]; !ok {
		t.Fatal("界内 tile (0,0) 被误删")
	}
}

// TestDropOutsideBandMaintainsZeroOverlapUnderMovement 锁住带语义的
// 内缘释放(Ruling 19 的移动不变量):玩家跨 tile 边界后,曾在外带、
// 现已落入新中心内盘的 tile 必须连同 GPU 资源一起让位——否则内盘残留
// 壳会在近 mesh 之上 poke 出地表,零重叠只在静止时成立。
func TestDropOutsideBandMaintainsZeroOverlapUnderMovement(t *testing.T) {
	s, sink := newTestScheduler(t, 4<<20, fakeGenerate)
	s.QueueRing(TilePos{}, 1, 2) // 带 [1,2]:25−1=24 块
	flushUntilIdle(t, s, TilePos{})
	if len(sink.uploads) != 24 {
		t.Fatalf("带状上传 %d 次,想要 24", len(sink.uploads))
	}
	// 中心移到 (1,0):tile (1,0) 的距离变为 0 < inner=1,必须释放;
	// (-2,0) 距离 3 > outer=2,同样释放;(2,0)/(0,0) 之外的带内块保留。
	s.DropOutside(TilePos{X: 1, Z: 0}, 1, 2)
	if !slices.Contains(sink.drops, TilePos{X: 1, Z: 0}) {
		t.Fatalf("移入内盘的 tile 未释放: %v", sink.drops)
	}
	if !slices.Contains(sink.drops, TilePos{X: -2, Z: 0}) {
		t.Fatalf("移出外半径的 tile 未释放: %v", sink.drops)
	}
	if _, ok := s.uploaded[TilePos{X: 1, Z: 0}]; ok {
		t.Fatal("内盘 tile 仍登记为 uploaded")
	}
	if _, ok := s.uploaded[TilePos{X: 2, Z: 0}]; !ok {
		t.Fatal("带内 tile (2,0) 被误删")
	}
	// 跨界增量入队与内缘释放共用同一区间语义:以新中心重播带 [1,2],
	// 只有新进入范围的 tile 被补入((0,0) 距新中心 1,属带内)。
	s.QueueRing(TilePos{X: 1, Z: 0}, 1, 2)
	if _, ok := s.pending[TilePos{X: 0, Z: 0}]; !ok {
		t.Fatal("新进入外带的 tile (0,0) 未被增量入队")
	}

	// pending 形态的内缘丢弃:未冲刷的 pending 落入内盘同样直接消失。
	s2, _ := newTestScheduler(t, 4<<20, fakeGenerate)
	s2.QueueTile(TilePos{})
	s2.DropOutside(TilePos{}, 1, 2)
	if got := s2.PendingUploads(); got != 0 {
		t.Fatalf("内盘 pending 未丢弃: %d", got)
	}

	// 防御钳制:负外半径与空区间(inner>outer)一律全释放(含中心),
	// 与旧「负半径丢弃一切」语义一致,不误保留任何 tile。
	s3, sink3 := newTestScheduler(t, 4<<20, fakeGenerate)
	s3.QueueTile(TilePos{})
	s3.QueueTile(TilePos{X: 1, Z: 0})
	flushUntilIdle(t, s3, TilePos{})
	s3.DropOutside(TilePos{}, 0, -1)
	s3.DropOutside(TilePos{}, 3, 2)
	if len(s3.uploaded) != 0 || len(sink3.drops) != 2 {
		t.Fatalf("非法区间未全释放: uploaded=%d drops=%v", len(s3.uploaded), sink3.drops)
	}
}

func TestEmptyShellResultDropsUploadedTile(t *testing.T) {
	var empty atomic.Bool // 主线程写、worker 读,以原子量显式交接
	generate := func(tile TilePos) []byte {
		if empty.Load() {
			return nil
		}
		return patternShell(tile)
	}
	s, sink := newTestScheduler(t, 4<<20, generate)
	tile := TilePos{X: 1, Z: 1}
	s.QueueTile(tile)
	flushUntilIdle(t, s, tile)
	if len(sink.uploads) != 1 {
		t.Fatalf("首次上传 %d 次,想要 1", len(sink.uploads))
	}
	empty.Store(true)
	s.QueueTile(tile) // 重新入队,此次生成为空壳
	flushUntilIdle(t, s, tile)
	if len(sink.uploads) != 1 {
		t.Fatalf("空壳被上传: %+v", sink.uploads)
	}
	if len(sink.drops) != 1 || sink.drops[0] != tile {
		t.Fatalf("空壳应下沉为 drop: %+v", sink.drops)
	}
	if len(s.uploaded) != 0 {
		t.Fatalf("空壳下沉后 uploaded=%d,想要 0", len(s.uploaded))
	}
}

func TestWorkerHandoffCopiesExactLengthSheddingBoundCapacity(t *testing.T) {
	bound, ok := nativeabi.LodShellOutputBoundBytes(4)
	if !ok {
		t.Fatal("step 4 无静态上界")
	}
	// 模拟 nativeabi.LodShell 的返回形态:cap=步长静态上界,len=实际写入。
	generate := func(tile TilePos) []byte {
		shell := make([]byte, bound)
		content := patternShell(tile)
		copy(shell, content)
		return shell[:len(content)]
	}
	s, sink := newTestScheduler(t, 4<<20, generate)
	center := TilePos{}
	for _, tile := range []TilePos{{X: 0, Z: 0}, {X: 1, Z: 0}, {X: 0, Z: 1}} {
		s.QueueTile(tile)
	}
	flushUntilIdle(t, s, center)
	if len(sink.uploads) != 3 {
		t.Fatalf("上传 %d 次,想要 3", len(sink.uploads))
	}
	for _, upload := range sink.uploads {
		if cap(upload.quads) != len(upload.quads) {
			t.Fatalf("tile %v 交接切片 cap=%d != len=%d,定长拷贝未生效(上界容量滞留)",
				upload.tile, cap(upload.quads), len(upload.quads))
		}
		if !slices.Equal(upload.quads, patternShell(upload.tile)) {
			t.Fatalf("tile %v 交接内容失真", upload.tile)
		}
	}
}

func TestWorkerHandoffSlicesImmutableAcrossGoroutines(t *testing.T) {
	// generate 复用同一底层缓冲并持续写入:若 worker 交接前未做定长
	// 拷贝,独立 reader goroutine 对第一次交接结果的持续读取与 worker
	// 的持续写入将构成真实交错的数据竞争,由 -race 捕获;定长拷贝后
	// 两侧内存无关,恒绿。内容校验同样以拷贝为前提:拷贝后 first.quads
	// 恒为第一次生成完成时刻的字节,别名时读到的是后续写入的搅动字节。
	const churnRounds = 200000
	shared := make([]byte, fakeShellBytes)
	entered := make(chan struct{}, 2) // worker 报告进入生成(缓冲防阻塞)
	proceed := make(chan struct{})    // 主线程逐次放行生成
	churnPattern := func(tile TilePos, round int, dst []byte) {
		for i := range dst {
			dst[i] = byte(i*7 + int(tile.X)*3 + int(tile.Z)*11 + round)
		}
	}
	firstTile, secondTile := TilePos{X: 1, Z: 2}, TilePos{X: 3, Z: 4}
	generate := func(tile TilePos) []byte {
		entered <- struct{}{}
		<-proceed
		for round := 0; round < churnRounds; round++ {
			churnPattern(tile, round, shared)
		}
		return shared
	}
	s, sink := newTestScheduler(t, 4<<20, generate)
	center := TilePos{}
	s.QueueTile(firstTile)
	s.QueueTile(secondTile)
	s.BeginFrame()
	s.FlushUploads(center)
	<-entered // worker 进入第一次生成
	proceed <- struct{}{}
	first := <-s.results // 白盒接收,精确控制交接时序
	<-entered            // worker 进入第二次生成,proceed 后将再次写 shared
	proceed <- struct{}{}
	// 独立 reader 与 worker 持续读写交错:别名时构成数据竞争,且逐轮
	// 校验和失稳;定长拷贝后内容恒定,逐轮相等。
	wantPattern := make([]byte, fakeShellBytes)
	churnPattern(firstTile, churnRounds-1, wantPattern) // 拷贝后内容 = 第一次生成的最后一轮
	wantPerRound := 0
	for _, b := range wantPattern {
		wantPerRound += int(b)
	}
	stable := true
	var readers sync.WaitGroup
	readers.Add(1)
	go func() {
		defer readers.Done()
		for round := 0; round < churnRounds; round++ {
			sum := 0
			for i := range first.quads {
				sum += int(first.quads[i])
			}
			if sum != wantPerRound {
				stable = false
			}
		}
	}()
	readers.Wait()
	if !stable {
		t.Fatal("第一次交接内容被后续生成搅动(逐轮校验和失稳)")
	}
	waitForResults(t, s, 1) // 等第二份结果就绪,再冲刷断言
	s.BeginFrame()
	s.FlushUploads(center) // 冲刷第二份结果
	if len(sink.uploads) != 1 || sink.uploads[0].tile != secondTile {
		t.Fatalf("第二份结果上传异常: %+v", sink.uploads)
	}
	wantSecond := make([]byte, fakeShellBytes)
	churnPattern(secondTile, churnRounds-1, wantSecond) // 第二次生成完成时的最终内容
	if !slices.Equal(sink.uploads[0].quads, wantSecond) {
		t.Fatal("第二次交接内容失真")
	}
}

func TestCloseIsIdempotentAndStopsWorker(t *testing.T) {
	release := make(chan struct{})
	generating := make(chan struct{}, 1)
	generate := func(tile TilePos) []byte {
		select {
		case generating <- struct{}{}:
		default:
		}
		<-release
		return patternShell(tile)
	}
	s, sink := newTestScheduler(t, 4<<20, generate)
	s.QueueTile(TilePos{X: 0, Z: 0})
	s.BeginFrame()
	s.FlushUploads(TilePos{}) // 派发,worker 进入生成并阻塞在 release
	<-generating
	close(release) // 放行:worker 完成生成交接结果后回到取请求循环
	s.Close()      // 停止并等待 worker 退出
	select {
	case <-s.done:
	default:
		t.Fatal("worker 未随 Close 退出")
	}
	s.Close()                        // 幂等
	s.QueueTile(TilePos{X: 1, Z: 1}) // 关闭后全部安全 no-op
	s.BeginFrame()
	s.FlushUploads(TilePos{})
	s.DropOutside(TilePos{}, 0, 1)
	if len(sink.uploads) != 0 || len(sink.drops) != 0 {
		t.Fatalf("关闭后产生 sink 调用: %+v / %+v", sink.uploads, sink.drops)
	}
}

func TestSchedulerUploadsDeterministicShellThroughNativePath(t *testing.T) {
	sink := &recordingSink{}
	s, err := NewScheduler(sink, testHeader(), 4, 4<<20)
	if err != nil {
		t.Fatalf("构造生产调度器失败: %v", err)
	}
	defer s.Close()
	tile := TilePos{X: -3, Z: 2}
	s.QueueTile(tile)
	flushUntilIdle(t, s, tile)
	want, err := GenerateShell(testHeader(), tile, 4)
	if err != nil {
		t.Fatalf("直接生成失败: %v", err)
	}
	if len(sink.uploads) != 1 || sink.uploads[0].tile != tile {
		t.Fatalf("native 路径上传异常: %+v", sink.uploads)
	}
	if !slices.Equal(sink.uploads[0].quads, want) {
		t.Fatal("经调度器上传的壳流与直接生成不一致")
	}
	if cap(sink.uploads[0].quads) != len(sink.uploads[0].quads) {
		t.Fatalf("native 路径交接未脱上界容量: cap=%d len=%d",
			cap(sink.uploads[0].quads), len(sink.uploads[0].quads))
	}
}
