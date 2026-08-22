package fluid

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

// 本文件是 OpenSpec change authoritative-fluid 任务组 3「决策验证关口」的性质
// 测试集。它证明（或证伪）design.md D5「待更新队列不持久化、重启用边界重扫
// 恢复」成立的唯一依据——**平衡态必须是边界重扫的不动点**——以及推进确定性、
// 预算等价与有限收敛三条相关性质。
//
// 全部测试都写在 package fluid 内：evalCell、lessPos、sortItems、item 等均未
// 导出，为写外部测试包而把内部符号导出会把实现细节变成公开 API。
//
// 所有测试只依赖 tick 计数与逐格状态断言，不依赖 wall-clock、time.Sleep 或
// goroutine 调度——本组测的就是确定性，任何时间相关的输入都会污染结论。

// ---------------------------------------------------------------------------
// 通用测试工具
// ---------------------------------------------------------------------------

// snapshot 复制内存世界的全部非空气格，供两次运行之间做逐格比较。
//
// memWorld.SetBlock 写入 AirID 时会删除记录，因此快照里「缺失的键」与「显式
// 空气」等价；diffWorlds 依赖 map 零值恰好是 core.AirID 这一点来统一处理。
func snapshot(w *memWorld) map[core.BlockPos]core.BlockID {
	out := make(map[core.BlockPos]core.BlockID, len(w.blocks))
	for pos, id := range w.blocks {
		out[pos] = id
	}
	return out
}

// diffWorlds 逐格比较两个世界快照，返回按 lessPos 全序排序的差异描述。
// 返回空切片表示两者逐格一致。排序是为了让失败信息可复现——否则 map 遍历
// 顺序会让同一个 bug 每次打印出不同的"第一处差异"。
func diffWorlds(a, b map[core.BlockPos]core.BlockID) []string {
	seen := make(map[core.BlockPos]struct{}, len(a)+len(b))
	positions := make([]core.BlockPos, 0, len(a)+len(b))
	for pos := range a {
		if _, ok := seen[pos]; !ok {
			seen[pos] = struct{}{}
			positions = append(positions, pos)
		}
	}
	for pos := range b {
		if _, ok := seen[pos]; !ok {
			seen[pos] = struct{}{}
			positions = append(positions, pos)
		}
	}
	sort.Slice(positions, func(i, j int) bool { return lessPos(positions[i], positions[j]) })

	diffs := make([]string, 0)
	for _, pos := range positions {
		if a[pos] != b[pos] {
			diffs = append(diffs, fmt.Sprintf("%+v: A=%d B=%d", pos, a[pos], b[pos]))
		}
	}
	return diffs
}

// reportDiff 把 diffWorlds 的结果裁剪成一段可读的失败信息（最多列出 10 处）。
func reportDiff(diffs []string) string {
	if len(diffs) <= 10 {
		return fmt.Sprintf("共 %d 处差异：%v", len(diffs), diffs)
	}
	return fmt.Sprintf("共 %d 处差异，前 10 处：%v", len(diffs), diffs[:10])
}

// fluidPositions 返回世界中全部流体格，按 lessPos 全序排序。
func fluidPositions(w *memWorld) []core.BlockPos {
	out := make([]core.BlockPos, 0, len(w.blocks))
	for pos, id := range w.blocks {
		if core.IsFluid(id) {
			out = append(out, pos)
		}
	}
	sort.Slice(out, func(i, j int) bool { return lessPos(out[i], out[j]) })
	return out
}

// rescanEnqueue 实现 design.md D5 的「边界重扫」：把世界中所有流体格**及其
// 空气邻居**入队。这是队列不持久化时重启后唯一的恢复路径。
//
// 刻意写在测试里而不是给 Queue 加方法：重扫需要遍历一个区块的全部格，那是
// 上层 sim 的区块存储才有的能力，本包的 FluidWorld 只暴露单格读写。
//
// 返回入队的项数，供调用方断言重扫确实做了事（防止"重扫入队 0 项"导致后续
// 的"零变更"断言空转通过）。
func rescanEnqueue(w *memWorld, q *Queue, now, delay uint64) int {
	// 按全序遍历而不是直接遍历 map：入队顺序本不影响结果（见
	// TestOrderIndependence_PerTickChangesMatch），但确定的遍历顺序让重扫本身
	// 可复现，失败时更好定位。
	for _, pos := range fluidPositions(w) {
		q.Enqueue(pos, now, delay)
		for _, n := range sixNeighbors(pos) {
			if w.BlockAt(n) == core.AirID {
				q.Enqueue(n, now, delay)
			}
		}
	}
	return q.Len()
}

// dueCount 返回队列中 dueTick <= now 的项数，即本 tick 到期、且受预算截断的
// 项数。只供测试判定「预算是否真的成为了约束」，避免用 len(changed) 做代理
// （变更数远小于处理数）。
//
// 遍历 q.order 而不是从前的 q.pending：任务组 10b 修复轮 2 把队列内容的存放处
// 从 map[BlockPos]uint64 换成了索引最小堆，order 就是队列内容本身（每个排队位置
// 恰好一条记录），因此这个计数与从前逐字等价——问的仍是「有多少项到期」。
func dueCount(q *Queue, now uint64) int {
	n := 0
	for _, it := range q.order {
		if it.dueTick <= now {
			n++
		}
	}
	return n
}

// requireNoExamineLimitHits 断言 q 从构造到现在，Advance 里那条探视上界守卫
// （advanceExamineLimit）一次都没触发过。
//
// 索引堆下它**应当恒为 0**：每次弹出都消耗一格预算，探视数天然封在 budget+1
// 以内。这条断言存在的意义不是「验证现在是 0」，而是给那条守卫一个**信号**——
// 守卫本身在生产路径上只 break 不 panic（权威 tick 上硬失败比轻微吞吐下降糟得多），
// 若没有人断言这个计数，它触发时现场只会表现为一声不响的吞吐损失。放在大场景
// 测试里而不是只放在新写的小用例里，是为了让真实规模的推进路径也覆盖到。
func requireNoExamineLimitHits(t *testing.T, q *Queue, name string) {
	t.Helper()
	if q.advanceExamineLimitHits != 0 {
		t.Fatalf("%s：Advance 的探视上界守卫触发了 %d 次——它本应永不触发，"+
			"说明 Queue 的双射不变量已被破坏（弹出的条目没有消耗预算）",
			name, q.advanceExamineLimitHits)
	}
}

// advanceToFixedPoint 反复推进直到不动点：某个 tick 既不产生任何变更、队列又
// 为空。返回到达不动点后的下一个 tick 编号与消耗的 tick 数。
//
// maxTicks 是硬上界，超过即 t.Fatalf——绝不写成无限循环，否则振荡缺陷会表现
// 成测试挂死而不是失败。
func advanceToFixedPoint(t *testing.T, q *Queue, w FluidWorld, start uint64, budget int, delay uint64, maxTicks int) (uint64, int) {
	t.Helper()
	now := start
	for i := 0; i < maxTicks; i++ {
		changed := q.Advance(now, w, budget, delay)
		now++
		if len(changed) == 0 && q.Len() == 0 {
			return now, i + 1
		}
	}
	t.Fatalf("推进 %d tick 后仍未到达不动点：队列剩余 %d 项（疑似振荡）", maxTicks, q.Len())
	return now, maxTicks
}

// assertNoLevelOverflow 断言世界中不存在「流体等级越界」写出的方块编号。
//
// evalCell 用 core.WaterSourceID+nextLevel 算出水平传播的目标编号，若「水平
// 传播上界」的 nextLevel > 7 守卫失效，就会写出 WaterSourceID+8 及其之后的
// 编号——这条廉价不变量把那种越界写入变成显式失败，而不是悄悄污染世界。
//
// 判据两次被削弱过，两次都必须记住：
//
//  1. 遍历范围原先是 fluidPositions(w)，而它按 core.IsFluid 过滤——越界写出的
//     编号恰恰不是流体，于是压根不会进入遍历，断言**恒真**。现在改为遍历世界
//     里的全部格。
//  2. 判据原先是 !core.RegisteredBlock(id)。农业编号追加之后
//     WaterSourceID+8 == FarmlandDryID **已经是已注册方块**，越界写入不再会被
//     RegisteredBlock 拒绝。现在改为白名单：本包的夹具只放空气、石头与流体，
//     出现其它任何编号都只可能来自流体规则的越界写入。
func assertNoLevelOverflow(t *testing.T, w *memWorld, label string) {
	t.Helper()
	for _, pos := range allPositions(w) {
		id := w.BlockAt(pos)
		if id == core.AirID || id == core.StoneID || core.IsFluid(id) {
			continue
		}
		t.Fatalf("%s：位置 %+v 出现非法方块编号 %d（夹具只放空气/石头/流体，"+
			"其余只可能来自流体等级越界写入）", label, pos, id)
	}
}

// allPositions 返回世界中全部**已记录**的格（含非流体），按 lessPos 全序排序。
// 与 fluidPositions 的差别正是 assertNoLevelOverflow 需要的：越界写出的编号不
// 是流体，只有遍历全部格才能看见它。
func allPositions(w *memWorld) []core.BlockPos {
	out := make([]core.BlockPos, 0, len(w.blocks))
	for pos := range w.blocks {
		out = append(out, pos)
	}
	sort.Slice(out, func(i, j int) bool { return lessPos(out[i], out[j]) })
	return out
}

// ---------------------------------------------------------------------------
// 测试地形构造
// ---------------------------------------------------------------------------

// fillBox 在闭区间 [x0,x1]×[y0,y1]×[z0,z1] 内写入 id。
func fillBox(w *memWorld, x0, y0, z0, x1, y1, z1 int32, id core.BlockID) {
	for x := x0; x <= x1; x++ {
		for y := y0; y <= y1; y++ {
			for z := z0; z <= z1; z++ {
				w.SetBlock(core.BlockPos{X: x, Y: y, Z: z}, id)
			}
		}
	}
}

// newBasin 构造一个有底有墙的封闭盆地：底面在 y=floorY，四壁从 floorY+1 到
// topY，内部 [x0,x1]×[z0,z1] 为空气。
//
// 盆地必须封闭：memWorld 未写入的格一律读作空气，即"无限空气世界"，水一旦
// 越过边界就会沿垂直优先规则永远向下流，任何收敛断言都不可能成立。所有性质
// 测试的地形都建立在封闭盆地之上，这是测试地形的前提而不是被测性质。
func newBasin(x0, z0, x1, z1, floorY, topY int32) *memWorld {
	w := newMemWorld()
	fillBox(w, x0-1, floorY, z0-1, x1+1, floorY, z1+1, core.StoneID)
	for y := floorY + 1; y <= topY; y++ {
		fillBox(w, x0-1, y, z0-1, x1+1, y, z0-1, core.StoneID)
		fillBox(w, x0-1, y, z1+1, x1+1, y, z1+1, core.StoneID)
		fillBox(w, x0-1, y, z0-1, x0-1, y, z1+1, core.StoneID)
		fillBox(w, x1+1, y, z0-1, x1+1, y, z1+1, core.StoneID)
	}
	return w
}

// fluidFixture 是一个具名的初始水体形状。build 每次调用都返回全新世界，
// 使同一形状能被多个测试、以多种推进方式反复重建而互不干扰。
type fluidFixture struct {
	name  string
	build func() *memWorld
	// expectEmptyEquilibrium 声明该形状的平衡态应当**不含任何流体格**
	// （例如整片无支撑的悬空水最终全部消失）。3.1 用它替代按形状名字符串
	// 做例外分支——名字是展示用的，改名不该静默削弱断言；而且这个字段是
	// 双向断言的：声明为 false 却真的流干、声明为 true 却还剩水，都会失败。
	expectEmptyEquilibrium bool
}

// standardFixtures 返回覆盖 task-3-brief 要求的各类形状的固定测试水体：
// 平地铺开、溃坝、悬空水（无支撑流动水）、绕柱环状连通、窄缝下泄。
//
// 每个形状都放在封闭盆地里（理由见 newBasin）。
func standardFixtures() []fluidFixture {
	return []fluidFixture{
		{
			name: "平地单源",
			build: func() *memWorld {
				// 源位于盆地底面正中，四周是足够容纳 7 格铺开的空地。
				w := newBasin(0, 0, 20, 20, 0, 6)
				w.SetBlock(core.BlockPos{X: 10, Y: 1, Z: 10}, core.WaterSourceID)
				return w
			},
		},
		{
			name: "溃坝",
			build: func() *memWorld {
				// 一整面高处的源墙塌向盆地另一半：单 tick 到期项数远超预算，
				// 也是 TestBudgetEquivalence 的被测形状。
				w := newBasin(0, 0, 19, 19, 0, 16)
				fillBox(w, 0, 1, 0, 4, 12, 19, core.WaterSourceID)
				return w
			},
		},
		{
			name:                   "悬空无支撑流动水",
			expectEmptyEquilibrium: true,
			build: func() *memWorld {
				// 一批凭空放置、没有任何源支撑的流动水：按「流动方块失去支撑
				// 后消失」应当全部消失，是"生灭"路径的最小样本。
				w := newBasin(0, 0, 12, 12, 0, 10)
				for i := int32(0); i < 6; i++ {
					w.SetBlock(core.BlockPos{X: 2 + i, Y: 5, Z: 3}, core.WaterLevel3ID)
					w.SetBlock(core.BlockPos{X: 3, Y: 6 + i%3, Z: 2 + i}, core.WaterLevel6ID)
				}
				// 一根悬空水柱：柱顶失去支撑先消失，下面的格因「上方是流体」
				// 逐代才轮到自己；同时柱底按垂直优先向下生出新的流动水并在
				// 地面铺开。整片水最终必须全部消失，中间要经历十几代的生灭
				// 交替——这是本形状真正有价值的瞬态，也让 3.2 的各个切点都
				// 落在未平衡处。
				for y := int32(2); y <= 8; y++ {
					w.SetBlock(core.BlockPos{X: 9, Y: y, Z: 9}, core.WaterLevel2ID)
				}
				return w
			},
		},
		{
			name: "绕柱环状连通",
			build: func() *memWorld {
				// 底面中央一根石柱，水绕柱一圈重新汇合：两股等级不同的水流在
				// 柱子背面同 tick 写同一格，正是「同 tick 冲突写入取最强者」
				// 与环状拓扑振荡风险同时出现的形状。
				w := newBasin(0, 0, 14, 14, 0, 8)
				fillBox(w, 5, 1, 5, 9, 1, 9, core.StoneID)
				w.SetBlock(core.BlockPos{X: 7, Y: 1, Z: 2}, core.WaterSourceID)
				return w
			},
		},
		{
			name: "窄缝下泄",
			build: func() *memWorld {
				// 上层是一整块实心台面，只在一格开缝；水从台面流到缝口后
				// 灌入下层腔室，再在下层铺开。
				w := newBasin(0, 0, 16, 16, 0, 12)
				fillBox(w, 0, 6, 0, 16, 6, 16, core.StoneID)
				w.SetBlock(core.BlockPos{X: 8, Y: 6, Z: 8}, core.AirID)
				w.SetBlock(core.BlockPos{X: 3, Y: 7, Z: 8}, core.WaterSourceID)
				return w
			},
		},
	}
}

// seedFromFluid 把世界中全部流体格入队，作为"世界刚加载完"的起始状态。
// 与 rescanEnqueue 的区别是不额外入队空气邻居——这样基线运行与重扫运行的
// 入队集合不同，重扫路径不会退化成基线路径的复制品。
func seedFromFluid(w *memWorld, q *Queue, now, delay uint64) {
	for _, pos := range fluidPositions(w) {
		q.Enqueue(pos, now, delay)
	}
}

// 全部性质测试共用的推进参数。delay 取 5、budget 取 512 与 sim 的默认
// tunable 一致（design.md D3/D4），但本包不读取它们，一律显式传入。
const (
	testDelay       uint64 = 5
	testBudget             = 512
	unboundedBudget        = 1 << 24
	testMaxTicks           = 20000
)

// ---------------------------------------------------------------------------
// 3.1 重扫不动点——D5 的唯一依据
// ---------------------------------------------------------------------------

// TestRescanFixedPoint_EquilibriumProducesNoChanges 证明 spec Scenario
// 「平衡态是重扫的不动点」，也就是 design.md D5「待更新队列不持久化」成立的
// 全部依据：
//
//	水体推进至不再产生变更 → 清空待更新队列（模拟进程重启）→ 对全部流体格及
//	其空气邻居执行边界重扫 → 后续推进产生**零**方块变更，且世界逐格不变。
//
// 若本测试证伪，D5 作废，必须改为持久化待更新队列。
func TestRescanFixedPoint_EquilibriumProducesNoChanges(t *testing.T) {
	for _, fx := range standardFixtures() {
		t.Run(fx.name, func(t *testing.T) {
			w := fx.build()
			q := NewQueue()
			seedFromFluid(w, q, 0, 0)
			if q.Len() == 0 {
				t.Fatalf("测试地形不含任何流体格，断言会空转")
			}

			now, ticks := advanceToFixedPoint(t, q, w, 1, unboundedBudget, testDelay, testMaxTicks)
			assertNoLevelOverflow(t, w, "平衡态")
			before := snapshot(w)
			fluidCount := len(fluidPositions(w))
			// 双向核对形状声明：只有显式声明会流干的形状才允许平衡态无水
			// （否则后续重扫无从入手，断言空转）；声明了会流干却仍剩水，
			// 同样说明形状或规则跑偏了。
			if fluidCount == 0 && !fx.expectEmptyEquilibrium {
				t.Fatalf("平衡态不含任何流体格，重扫断言会空转")
			}
			if fluidCount != 0 && fx.expectEmptyEquilibrium {
				t.Fatalf("形状声明平衡态应当流干，实际仍有 %d 个流体格", fluidCount)
			}
			t.Logf("形状 %s：%d tick 到达平衡态，流体格 %d 个", fx.name, ticks, fluidCount)

			// 模拟进程重启：队列内容全部丢失。
			q.Clear()
			if q.Len() != 0 {
				t.Fatalf("Clear 之后队列仍有 %d 项", q.Len())
			}

			// 边界重扫：全部流体格 + 其空气邻居。
			enqueued := rescanEnqueue(w, q, now, 0)
			if fluidCount > 0 && enqueued == 0 {
				t.Fatalf("重扫未入队任何项，后续零变更断言会空转")
			}
			t.Logf("形状 %s：重扫入队 %d 项", fx.name, enqueued)

			// 重扫后的推进必须一格都不改。用 unboundedBudget 保证一次 Advance
			// 就能把全部重扫项处理掉，任何变更都会立刻暴露。
			for i := 0; i < testMaxTicks && q.Len() > 0; i++ {
				changed := q.Advance(now, w, unboundedBudget, testDelay)
				now++
				if len(changed) != 0 {
					t.Fatalf("重扫后第 %d 次推进产生了 %d 处变更（平衡态不是重扫的不动点）：%v",
						i+1, len(changed), changed[:min(len(changed), 10)])
				}
			}
			if q.Len() != 0 {
				t.Fatalf("重扫后队列未能排空，剩余 %d 项", q.Len())
			}

			if diffs := diffWorlds(before, snapshot(w)); len(diffs) != 0 {
				t.Fatalf("重扫后世界状态发生变化：%s", reportDiff(diffs))
			}

			requireNoExamineLimitHits(t, q, "形状 "+fx.name+" 的重扫不动点推进")
		})
	}
}

// TestRescanMidFlight_ConvergesToSameEquilibrium 证明 spec Scenario
// 「未平衡状态在重启后继续收敛」：水体在**尚未到达平衡态**时清空队列并执行
// 边界重扫，最终到达的平衡态与全程不清空队列时逐格一致。
//
// 这是 D5 的第二条依据：重启不只是"平衡态不被破坏"，还必须"未完成的推进能
// 从方块状态本身重建"。若不成立，重启会把世界永久卡在一个假平衡上。
func TestRescanMidFlight_ConvergesToSameEquilibrium(t *testing.T) {
	// 在多个切点处重启：delay=5 意味着每 5 tick 才推进一代，这些切点覆盖了
	// 「一代刚开始」「一代中途」「跨多代」三种情形。
	// 切点 0 表示「世界刚加载、一个 tick 都还没跑就重启」，同样必须收敛。
	cuts := []int{0, 1, 3, 6, 11, 17, 28}

	for _, fx := range standardFixtures() {
		t.Run(fx.name, func(t *testing.T) {
			// 基线：全程不清空队列，推进到平衡态。
			base := fx.build()
			baseQ := NewQueue()
			seedFromFluid(base, baseQ, 0, 0)
			advanceToFixedPoint(t, baseQ, base, 1, unboundedBudget, testDelay, testMaxTicks)
			want := snapshot(base)

			// nonTrivialCuts 统计**非零**切点里真正落在未平衡处的个数。
			//
			// 刻意排除 cut=0：那一刀恒定落在未平衡处（一个 tick 都没跑，队列
			// 必然满、状态必然不等于平衡态），把它计入会让守卫恒被满足，从而
			// 失去它被写出来的目的——将来有人改动形状、让 cut=1..28 全部落到
			// 平衡之后时，守卫必须报警。cut=0 本身是有意义的用例（世界刚加载
			// 就重启），保留参与断言，只是不计入这个计数。
			nonTrivialCuts := 0

			for _, cut := range cuts {
				w := fx.build()
				q := NewQueue()
				seedFromFluid(w, q, 0, 0)

				now := uint64(1)
				for i := 0; i < cut; i++ {
					q.Advance(now, w, unboundedBudget, testDelay)
					now++
				}

				pendingBefore := q.Len()
				midDiffers := len(diffWorlds(snapshot(w), want)) != 0
				if cut > 0 && pendingBefore > 0 && midDiffers {
					// 队列里还有真实待办、且当前状态确实不是最终平衡态——
					// 这一刀切在了未平衡处，丢弃的是真正会影响结果的工作。
					nonTrivialCuts++
				}

				// 模拟在未平衡时重启：队列全丢，只剩方块本身。
				q.Clear()
				rescanEnqueue(w, q, now, 0)

				advanceToFixedPoint(t, q, w, now, unboundedBudget, testDelay, testMaxTicks)
				assertNoLevelOverflow(t, w, fmt.Sprintf("切点 %d 的重扫平衡态", cut))

				if diffs := diffWorlds(want, snapshot(w)); len(diffs) != 0 {
					t.Fatalf("在第 %d tick 清空队列并重扫后到达的平衡态与基线不一致（丢弃前队列 %d 项）：%s",
						cut, pendingBefore, reportDiff(diffs))
				}

				requireNoExamineLimitHits(t, q, fmt.Sprintf("形状 %s 切点 %d 的重扫推进", fx.name, cut))
			}
			requireNoExamineLimitHits(t, baseQ, "形状 "+fx.name+" 的基线推进")

			// 要求至少 3 个非零切点落在未平衡处：1 个太容易被形状的细微调整
			// 蒙混过去，3 个意味着该形状的瞬态确实横跨了多个切点。
			const minNonTrivialCuts = 3
			if nonTrivialCuts < minNonTrivialCuts {
				t.Fatalf("只有 %d 个非零切点落在未平衡处（要求至少 %d 个），本测试未真正验证「未平衡态重启」",
					nonTrivialCuts, minNonTrivialCuts)
			}
			t.Logf("形状 %s：%d/%d 个非零切点落在未平衡处", fx.name, nonTrivialCuts, len(cuts)-1)
		})
	}
}

// ---------------------------------------------------------------------------
// 3.3 预算等价
// ---------------------------------------------------------------------------

// TestBudgetEquivalence_DamBreakSameFinalState 证明 spec Scenario
// 「预算不改变平衡态」与「待更新项不因预算丢失」：同一次溃坝在受限预算与
// 不受限预算下推进至无变更，最终状态逐格一致。
//
// 收敛所需 tick 数**不是**断言对象（受限预算必然更慢）；恰恰相反，测试断言
// 受限预算确实更慢，以此证明预算真的成为了约束——否则"两者一致"可能只是因为
// 溃坝规模从未触及预算上限，断言会空转。
func TestBudgetEquivalence_DamBreakSameFinalState(t *testing.T) {
	build := func() *memWorld {
		w := newBasin(0, 0, 19, 19, 0, 16)
		fillBox(w, 0, 1, 0, 4, 12, 19, core.WaterSourceID)
		return w
	}

	ref := build()
	refQ := NewQueue()
	seedFromFluid(ref, refQ, 0, 0)
	_, refTicks := advanceToFixedPoint(t, refQ, ref, 1, unboundedBudget, testDelay, testMaxTicks)
	want := snapshot(ref)
	assertNoLevelOverflow(t, ref, "不受限预算平衡态")
	t.Logf("不受限预算：%d tick 到达平衡态，流体格 %d 个", refTicks, len(fluidPositions(ref)))

	for _, budget := range []int{testBudget, 64} {
		t.Run(fmt.Sprintf("budget=%d", budget), func(t *testing.T) {
			w := build()
			q := NewQueue()
			seedFromFluid(w, q, 0, 0)
			_, ticks := advanceToFixedPoint(t, q, w, 1, budget, testDelay, testMaxTicks)
			assertNoLevelOverflow(t, w, fmt.Sprintf("budget=%d 平衡态", budget))
			t.Logf("budget=%d：%d tick 到达平衡态", budget, ticks)

			if ticks <= refTicks {
				t.Fatalf("受限预算 %d 的收敛 tick 数 %d 未超过不受限的 %d——预算从未成为约束，等价断言是空转",
					budget, ticks, refTicks)
			}
			if diffs := diffWorlds(want, snapshot(w)); len(diffs) != 0 {
				t.Fatalf("budget=%d 的平衡态与不受限预算不一致（超预算项被丢弃或顺延顺序被破坏）：%s",
					budget, reportDiff(diffs))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 3.4 入队序无关
// ---------------------------------------------------------------------------

// TestOrderIndependence_PerTickChangesMatch 证明 spec Scenario「入队顺序无关」
// 与「重复运行结果一致」：同一组待更新格以不同入队顺序推进相同 tick 数、相同
// 预算，**每一个 tick** 产生的变更集合逐格一致，最终状态也逐格一致。
//
// 关于本测试的证据强度，必须说清楚：Queue 以位置为键去重（现在是 Queue.index
// 这张 map），入队顺序在进入 Advance 之前就已经被结构性抹除；Advance 每 tick 又
// 从 Queue 内部那个按 lessItem 组织的最小堆里，按 (dueTick, lessPos) 全序取出
// 下一批。因此入队顺序**根本到不了合并步骤**，本测试几乎必然通过——它属于
// 「设计正确所以平凡为真」，**不是**确定性的主要论据。
//
// 它真正守住的回归面是：将来有人删掉或改坏「按全序取批」这件事（比如改成直接
// 遍历那张以位置为键的 map）时立刻报警。为了让这个回归面真的被覆盖，测试刻意用
// **受限预算**推进：只有预算截断本 tick 的到期项时，"处理哪些项"才依赖全序，
// map 遍历顺序的随机性才会体现为可观测的差异。用不受限预算测这条性质，是测不出
// 全序取批的。
func TestOrderIndependence_PerTickChangesMatch(t *testing.T) {
	// 受限预算：让本 tick 到期项的截断点依赖全序取批的结果。
	const orderBudget = 37
	const orderTicks = 400

	for _, fx := range standardFixtures() {
		t.Run(fx.name, func(t *testing.T) {
			seeds := fluidPositions(fx.build())
			if len(seeds) == 0 {
				t.Fatalf("测试地形不含任何流体格，断言会空转")
			}

			// 三种入队顺序：全序升序、全序降序、固定种子洗牌。三者的集合完全
			// 相同，只有 Enqueue 的调用次序不同。另外把升序再跑一遍，用来直接
			// 断言 spec Scenario「重复运行结果一致」——它此前只由「不同入队
			// 顺序结果一致」间接蕴含，没有字面覆盖。
			orders := map[string][]core.BlockPos{}
			asc := append([]core.BlockPos(nil), seeds...)
			orders["升序"] = asc

			desc := append([]core.BlockPos(nil), seeds...)
			for i, j := 0, len(desc)-1; i < j; i, j = i+1, j-1 {
				desc[i], desc[j] = desc[j], desc[i]
			}
			orders["降序"] = desc

			shuffled := append([]core.BlockPos(nil), seeds...)
			rng := rand.New(rand.NewSource(0x5EED))
			rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
			orders["固定种子洗牌"] = shuffled
			orders["升序·复跑"] = asc

			run := func(order []core.BlockPos) ([][]changedCell, map[core.BlockPos]core.BlockID, int) {
				w := fx.build()
				q := NewQueue()
				for _, pos := range order {
					q.Enqueue(pos, 0, 0)
				}
				perTick := make([][]changedCell, 0, orderTicks)
				now := uint64(1)
				maxDue := 0
				for i := 0; i < orderTicks; i++ {
					if n := dueCount(q, now); n > maxDue {
						maxDue = n
					}
					// 记录「位置 + 落地方块编号」而不只是位置：Advance 只返回
					// 位置，若同 tick 冲突写入的合并结果出错，位置集合完全相同、
					// 只有值不同，单比位置是看不出来的。
					changed := q.Advance(now, w, orderBudget, testDelay)
					cells := make([]changedCell, 0, len(changed))
					for _, pos := range changed {
						cells = append(cells, changedCell{pos: pos, id: w.BlockAt(pos)})
					}
					perTick = append(perTick, cells)
					now++
				}
				return perTick, snapshot(w), maxDue
			}

			refTicks, refState, refMaxDue := run(orders["升序"])

			// 非空转守卫一：参考运行必须真的产生过变更，否则逐 tick 比较的
			// 只是一串空列表。
			total := 0
			for _, changed := range refTicks {
				total += len(changed)
			}
			if total == 0 {
				t.Fatalf("参考运行 %d tick 内没有任何变更，逐 tick 断言是空转", orderTicks)
			}
			// 非空转守卫二：必须真的有某个 tick 的到期项数超过预算——只有
			// 那时取批才会被预算截断，「处理哪些项」才取决于全序，本测试才真的
			// 盖住了「取批不按全序」这个回归面。注意不能用
			// len(changed) 做代理：预算限制的是处理项数，而变更数只是其中
			// 真改变了值的子集，两者差一个数量级。
			if refMaxDue <= orderBudget {
				t.Fatalf("参考运行单 tick 最大到期项数 %d 未超过预算 %d，全序取批的截断路径未被覆盖",
					refMaxDue, orderBudget)
			}
			t.Logf("形状 %s：单 tick 最大到期项 %d（预算 %d）", fx.name, refMaxDue, orderBudget)
			t.Logf("形状 %s：%d tick 共 %d 处变更", fx.name, orderTicks, total)

			for _, name := range []string{"降序", "固定种子洗牌", "升序·复跑"} {
				gotTicks, gotState, _ := run(orders[name])
				if len(gotTicks) != len(refTicks) {
					t.Fatalf("入队顺序 %s：tick 数不一致 %d vs %d", name, len(gotTicks), len(refTicks))
				}
				for i := range refTicks {
					if len(gotTicks[i]) != len(refTicks[i]) {
						t.Fatalf("入队顺序 %s：第 %d tick 变更数不一致 %d vs %d",
							name, i+1, len(gotTicks[i]), len(refTicks[i]))
					}
					for j := range refTicks[i] {
						if gotTicks[i][j] != refTicks[i][j] {
							t.Fatalf("入队顺序 %s：第 %d tick 第 %d 项变更不一致 %+v vs %+v",
								name, i+1, j, gotTicks[i][j], refTicks[i][j])
						}
					}
				}
				if diffs := diffWorlds(refState, gotState); len(diffs) != 0 {
					t.Fatalf("入队顺序 %s：最终状态不一致：%s", name, reportDiff(diffs))
				}
			}
		})
	}
}

// changedCell 是「某 tick 变更了的一格」及其落地后的方块编号，供逐 tick 比较。
//
// 记录方块编号而不只是位置，是为了让「入队顺序无关」这条断言也覆盖到值：
// 同 tick 冲突写入的合并若出错，变更的位置集合完全相同、只有落地的编号不同。
type changedCell struct {
	pos core.BlockPos
	id  core.BlockID
}

// 变异验证留档：把 Advance 阶段二的 `if w.BlockAt(pos) != id` 过滤去掉之后，
// internal/fluid 全包测试仍然全绿。这不是覆盖漏洞，而是一个**等价变异**——
// 在当前规则集下，evalCell 产出的写入永远是真实变化：Replaceable 只允许写入
// 空气或**严格更弱**的流动水（同等级一律拒绝），自我消亡分支只在自身是流体时
// 写空气，strongerWrite 又只在候选之间取舍。因此不存在「写入值等于现状」的
// 候选，任何测试都无法把两者区分开。该过滤是防御性的（保护未来规则变化下
// 调用方的 dirty 标记与广播不被无变化写入污染），不是当前可达路径。

// ---------------------------------------------------------------------------
// 3.5 有限收敛（无振荡）
// ---------------------------------------------------------------------------

// randomWaterBody 用固定种子生成一片随机初始水体，覆盖 design.md Risk
// 「流动规则的存活判定产生振荡」点名的三类形状：
//   - **悬空水**：随机在空中撒下无支撑的流动水（等级随机），它们必须消失；
//   - **环状连通**：底面中央一根实心柱，水必须绕柱一圈重新汇合；
//   - **窄缝**：一道贯穿盆地的实心内墙，只随机开一格缝。
//
// 另外随机撒实心方块与源方块，制造不规则的分层与汇流。
func randomWaterBody(seed int64) *memWorld {
	rng := rand.New(rand.NewSource(seed))
	const (
		x0, z0    = int32(0), int32(0)
		x1, z1    = int32(13), int32(13)
		floorY    = int32(0)
		topY      = int32(11)
		interiorH = 10
	)
	w := newBasin(x0, z0, x1, z1, floorY, topY)

	// 环状连通：中央实心柱。
	fillBox(w, 6, floorY+1, 6, 7, floorY+3, 7, core.StoneID)

	// 窄缝用的两个随机量在这里抽取，保持随机序列与形状构造顺序解耦；
	// 内墙本身放到最后再砌（理由见下）。
	wallZ := z0 + 2 + int32(rng.Intn(3))
	gapX := x0 + int32(rng.Intn(int(x1-x0+1)))

	// 随机实心方块：制造不规则地形、窄通道与死角。
	for i := 0; i < 60; i++ {
		w.SetBlock(core.BlockPos{
			X: x0 + int32(rng.Intn(int(x1-x0+1))),
			Y: floorY + 1 + int32(rng.Intn(interiorH)),
			Z: z0 + int32(rng.Intn(int(z1-z0+1))),
		}, core.StoneID)
	}

	// 随机源方块。
	for i := 0; i < 12; i++ {
		pos := core.BlockPos{
			X: x0 + int32(rng.Intn(int(x1-x0+1))),
			Y: floorY + 1 + int32(rng.Intn(interiorH)),
			Z: z0 + int32(rng.Intn(int(z1-z0+1))),
		}
		w.SetBlock(pos, core.WaterSourceID)
	}

	// 悬空无支撑流动水：等级 1..7 随机。
	for i := 0; i < 40; i++ {
		pos := core.BlockPos{
			X: x0 + int32(rng.Intn(int(x1-x0+1))),
			Y: floorY + 1 + int32(rng.Intn(interiorH)),
			Z: z0 + int32(rng.Intn(int(z1-z0+1))),
		}
		// 只往**空气**里放：悬空水的语义就是"浮在空中、没有支撑"，覆写实心
		// 方块会一并把地形挖出洞来，让形状与注释不符（早期版本正是如此，
		// 内墙上会被随机挖出好几个缺口）。
		if w.BlockAt(pos) != core.AirID {
			continue
		}
		w.SetBlock(pos, core.WaterLevel1ID+core.BlockID(rng.Intn(7)))
	}

	// 窄缝：最后砌一道从底面直通盆地顶的实心内墙，只在 gapX 处留一格缝。
	//
	// 放在全部随机撒点之后砌，是为了让"只有一格缝"这句话真的成立：随机源
	// 方块会覆写实心方块，先砌墙的话会被随机撒点在墙上打出额外的开口。墙高
	// 取到 topY 而不是半高，否则水直接从墙顶越过，窄缝拓扑形同虚设。
	fillBox(w, x0, floorY+1, wallZ, x1, topY, wallZ, core.StoneID)
	w.SetBlock(core.BlockPos{X: gapX, Y: floorY + 1, Z: wallZ}, core.AirID)
	return w
}

// TestConvergeRandomWaterBodiesReachFixedPoint 证明 design.md Risk
// 「流动规则的存活判定产生振荡」已被缓解：一批固定种子生成的随机初始水体，
// 每一组都在明确的 tick 上界内到达不动点（某 tick 既无变更、队列又为空），
// 不出现反复生灭。
//
// 上界是硬失败条件而非无限循环：振荡的表现必须是测试失败，不能是测试挂死。
// 到达不动点后额外做一次边界重扫复检——把 3.1 的性质顺带覆盖到随机形状上，
// 随机形状比手写形状更容易碰到"看起来平衡、其实只是队列空了"的假平衡。
func TestConvergeRandomWaterBodiesReachFixedPoint(t *testing.T) {
	// 固定种子，结果完全可复现。
	seeds := []int64{1, 2, 3, 5, 8, 13, 21, 34}
	// 明确的 tick 上界：盆地内部约 13×13×10 格，delay=5 意味着每 5 tick
	// 推进一代，1500 tick 相当于 300 代，远超任何非振荡形状所需。
	const convergeMaxTicks = 1500

	for _, budget := range []int{unboundedBudget, testBudget} {
		for _, seed := range seeds {
			t.Run(fmt.Sprintf("seed=%d/budget=%d", seed, budget), func(t *testing.T) {
				w := randomWaterBody(seed)
				q := NewQueue()
				seedFromFluid(w, q, 0, 0)
				if q.Len() == 0 {
					t.Fatalf("随机水体不含流体格，断言会空转")
				}
				initialFluid := q.Len()

				now, ticks := advanceToFixedPoint(t, q, w, 1, budget, testDelay, convergeMaxTicks)
				assertNoLevelOverflow(t, w, fmt.Sprintf("seed=%d 平衡态", seed))
				t.Logf("seed=%d budget=%d：初始流体 %d 格，%d tick 到达不动点，平衡态流体 %d 格",
					seed, budget, initialFluid, ticks, len(fluidPositions(w)))

				// 复检：随机形状上的平衡态同样是边界重扫的不动点。
				before := snapshot(w)
				q.Clear()
				rescanEnqueue(w, q, now, 0)
				for i := 0; i < convergeMaxTicks && q.Len() > 0; i++ {
					if changed := q.Advance(now, w, unboundedBudget, testDelay); len(changed) != 0 {
						t.Fatalf("seed=%d：随机形状的平衡态不是重扫的不动点，第 %d 次推进产生 %d 处变更：%v",
							seed, i+1, len(changed), changed[:min(len(changed), 10)])
					}
					now++
				}
				if diffs := diffWorlds(before, snapshot(w)); len(diffs) != 0 {
					t.Fatalf("seed=%d：重扫后世界状态发生变化：%s", seed, reportDiff(diffs))
				}

				requireNoExamineLimitHits(t, q, fmt.Sprintf("seed=%d budget=%d 的随机水体推进", seed, budget))
			})
		}
	}
}
