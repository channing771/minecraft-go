package sim

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/fluid"
	"github.com/channing771/mornlea/internal/world"
)

// fluidFlatChunk 生成流体测试用的平坦区块：y=0 一层草方块地面，其余全部空气。
// 测试里的水一律放在 y>=1，因此不会向世界底部下落，观察点集中在水平铺开、
// 区块接缝与推进范围边界这三件事上。
func fluidFlatChunk(pos core.ChunkPos) *world.Chunk {
	chunk := world.NewChunk(pos)
	for x := range core.SectionSize {
		for z := range core.SectionSize {
			chunk.SetBlock(x, 0, z, core.GrassID)
		}
	}
	chunk.Compact()
	return chunk
}

// fluidSeedChunk 在平坦区块上写入 seed 中落在该区块内的方块，并重新压缩。
// 种子方块必须在区块生成时就写进去，区块进入推进范围时的边界重扫才能扫到它们
// ——这正是重启后靠重扫恢复推进的真实路径。
func fluidSeedChunk(pos core.ChunkPos, seed map[core.BlockPos]core.BlockID) *world.Chunk {
	chunk := fluidFlatChunk(pos)
	for position, id := range seed {
		if position.Chunk() != pos {
			continue
		}
		x, _, z := position.Local()
		chunk.SetBlock(x, position.Y, z, id)
	}
	chunk.Compact()
	return chunk
}

// readyFluidPlayer 构造一名 active 玩家与围绕其出生区块的一片已 Ready 平坦世界。
// viewRadius 取 DropInterestRadius，使订阅范围与流体推进范围（活动兴趣区块）重合。
// withhold 返回 true 的区块**不**提交生成结果，它们的 key 会被收集后原样返回，
// 供测试在稍后手动补交，从而制造「相邻区块晚一步进入推进范围」的场景。
func readyFluidPlayer(
	t *testing.T,
	seed map[core.BlockPos]core.BlockID,
	withhold func(core.ChunkPos) bool,
) (*Engine, SessionID, []core.ChunkKey) {
	t.Helper()
	engine := NewEngine(DropInterestRadius, 0)
	const session = SessionID(1)
	engine.RegisterSession(session, core.Overworld, core.ChunkPos{})
	withheld := make([]core.ChunkKey, 0)
	for range 12 {
		result := engine.Step()
		for _, key := range result.Acquire {
			engine.SubmitAcquired(AcquiredChunk{Key: key, Missing: true})
		}
		for _, key := range result.Generate {
			if withhold != nil && withhold(key.Pos) {
				withheld = append(withheld, key)
				continue
			}
			engine.SubmitGenerated(GeneratedChunk{
				Dimension: key.Dimension,
				Pos:       key.Pos,
				Chunk:     fluidSeedChunk(key.Pos, seed),
			})
		}
	}
	if player, ok := engine.Player(session); !ok || !player.Ready {
		t.Fatalf("玩家未 Ready: %+v", player)
	}
	return engine, session, withheld
}

// fluidBlockAt 读取主世界某格的权威方块，区块未就绪时直接失败。
func fluidBlockAt(t *testing.T, engine *Engine, position core.BlockPos) core.BlockID {
	t.Helper()
	block, ready := engine.dimensions[core.Overworld].BlockAt(position)
	if !ready {
		t.Fatalf("读取 %+v 时区块未就绪", position)
	}
	return block
}

// overworldFluidQueue 返回主世界的流体待更新队列，未创建时直接失败——测试里
// 走到这一步说明连一次入队都没发生过，断言会失去意义。
func overworldFluidQueue(t *testing.T, engine *Engine) *fluid.Queue {
	t.Helper()
	queue := engine.fluidQueues[core.Overworld]
	if queue == nil {
		t.Fatal("主世界的流体队列尚未创建")
	}
	return queue
}

// TestFluidQueuesArePerDimension 锁定硬约束「每个维度一个 Queue」。
// internal/fluid 的处理全序用 (ChunkKey, y, z, x) 近似，而 core.BlockPos 不带
// 维度；两个维度共用一个 Queue 会让不同维度的同坐标格比较为相等，全序退化成
// 偏序，确定性静默失效。
func TestFluidQueuesArePerDimension(t *testing.T) {
	engine := NewEngine(0, 0)
	overworld := engine.fluidQueue(core.Overworld)
	if again := engine.fluidQueue(core.Overworld); again != overworld {
		t.Fatal("同一维度两次取到了不同的队列实例")
	}
	other := engine.fluidQueue(core.Overworld + 1)
	if other == overworld {
		t.Fatal("两个维度共用了同一个 Queue 实例")
	}
	overworld.Enqueue(core.BlockPos{X: 1, Y: 2, Z: 3}, 0, 0)
	if other.Len() != 0 {
		t.Fatalf("另一维度的队列被污染，Len=%d，想要 0", other.Len())
	}
}

// TestFluidWorldTreatsOutOfScopeAsUnreplaceable 锁定硬约束「推进范围外与未加载
// 的格必须表现为不可替换，而不是空气」。internal/fluid 的收敛结论建立在封闭盆地
// 上；边界外一旦读作空气，水就把边界当成有底洞，永久外流、永不收敛，并持续吃掉
// 每 tick 预算。
func TestFluidWorldTreatsOutOfScopeAsUnreplaceable(t *testing.T) {
	// 哨兵本身的性质：BarrierID 必须既不是空气也不是流体，否则下面的断言全部
	// 失去意义（这正是评审反复抓到的"哨兵值悄悄变成合法值"那一类问题）。
	if core.BarrierID == core.AirID || core.IsFluid(core.BarrierID) {
		t.Fatalf("BarrierID=%d 不再是非空气非流体的实心哨兵", core.BarrierID)
	}

	engine, _, _ := readyFluidPlayer(t, nil, nil)
	dimension := engine.dimensions[core.Overworld]

	// 手工载入一个远在推进范围之外、但确实持有真实水方块的区块。
	outsidePos := core.ChunkPos{X: 9}
	outsideWater := core.BlockPos{X: 9 << core.SectionShift, Y: 5, Z: 0}
	if !dimension.BeginGeneration(outsidePos) {
		t.Fatal("范围外区块未开始生成")
	}
	if err := dimension.ApplyGenerated(outsidePos, fluidSeedChunk(
		outsidePos, map[core.BlockPos]core.BlockID{outsideWater: core.WaterSourceID},
	)); err != nil {
		t.Fatal(err)
	}
	if _, inScope := engine.fluidScope[(core.ChunkKey{Dimension: core.Overworld, Pos: outsidePos})]; inScope {
		t.Fatal("测试前提被破坏：范围外区块竟在推进范围内")
	}

	adapter := &fluidWorld{
		engine:    engine,
		id:        core.Overworld,
		dimension: dimension,
		scope:     engine.fluidScope,
		pending:   make(map[core.ChunkKey]*pendingChunkChanges),
	}

	// 正向对照：范围内的空气必须读作空气且可替换，证明适配器不是对一切位置
	// 都返回哨兵。
	inScopeAir := core.BlockPos{X: 1, Y: 5, Z: 1}
	if got := adapter.BlockAt(inScopeAir); got != core.AirID {
		t.Fatalf("范围内空气读作 %d，想要 %d", got, core.AirID)
	}
	if !fluid.Replaceable(adapter.BlockAt(inScopeAir), 1) {
		t.Fatal("范围内的空气必须可替换")
	}

	unreplaceable := []struct {
		name     string
		position core.BlockPos
	}{
		{"范围外区块里的真实水方块", outsideWater},
		{"范围外区块里的空气", core.BlockPos{X: 9<<core.SectionShift + 3, Y: 5, Z: 3}},
		{"从未加载过的区块", core.BlockPos{X: 400, Y: 5, Z: 400}},
		{"世界底面之下", core.BlockPos{X: 1, Y: core.MinY - 1, Z: 1}},
		{"世界顶面之上", core.BlockPos{X: 1, Y: core.MaxY, Z: 1}},
	}
	for _, item := range unreplaceable {
		got := adapter.BlockAt(item.position)
		if got != core.BarrierID {
			t.Fatalf("%s 读作 %d，想要哨兵 %d", item.name, got, core.BarrierID)
		}
		for level := uint8(1); level <= 7; level++ {
			if fluid.Replaceable(got, level) {
				t.Fatalf("%s 在等级 %d 下被判定为可替换", item.name, level)
			}
		}
	}

	// 范围外的写入必须被丢弃，且不得登记任何区块变更。
	adapter.SetBlock(outsideWater, core.AirID)
	if got := fluidBlockAt(t, engine, outsideWater); got != core.WaterSourceID {
		t.Fatalf("范围外的格被改写成 %d", got)
	}
	if len(adapter.pending) != 0 {
		t.Fatalf("范围外的写入登记了区块变更: %+v", adapter.pending)
	}
}

// TestFluidRescanWakesFluidAcrossChunkBoundary 锁定硬约束「重扫必须覆盖跨区块
// 边界另一侧的流体格」。
//
// 场景：水源在区块 (0,0) 内铺到 x=15 后，因为区块 (1,0) 尚未就绪（被读作实心）
// 而静止、并从队列中排空；随后区块 (1,0) 就绪并进入推进范围。此时唯一能让水
// 继续流过接缝的，就是对新进入范围的区块做重扫时，同时扫到相邻区块贴着它那一层
// 边界平面上的流体格。只扫本区块内部的实现在这里会让水面永久卡在 x=15。
//
// 尾部同时验证 D5 的「平衡态是重扫的不动点」，且在接缝处也成立。
func TestFluidRescanWakesFluidAcrossChunkBoundary(t *testing.T) {
	source := core.BlockPos{X: 12, Y: 1, Z: 8}
	seed := map[core.BlockPos]core.BlockID{source: core.WaterSourceID}
	late := core.ChunkPos{X: 1}
	engine, _, withheld := readyFluidPlayer(t, seed, func(pos core.ChunkPos) bool {
		return pos == late
	})
	if len(withheld) != 1 || withheld[0].Pos != late {
		t.Fatalf("延后就绪的区块=%+v，想要恰好 %+v", withheld, late)
	}

	for range 200 {
		engine.Step()
	}
	// 水在本区块内铺到 x=15（距源 3 格 ⇒ 等级 3），并被区块边界挡住。
	if got := fluidBlockAt(t, engine, core.BlockPos{X: 15, Y: 1, Z: 8}); got != core.WaterLevel3ID {
		t.Fatalf("接缝内侧 (15,1,8)=%d，想要 %d", got, core.WaterLevel3ID)
	}
	// 边界必须是"封闭"的：队列彻底排空，说明水没有在边界上反复向外写。
	// 若把范围外读作空气，这个 Len 会永远大于 0（每 tick 写不进去又重新入队）。
	if got := overworldFluidQueue(t, engine).Len(); got != 0 {
		t.Fatalf("水面静止后待更新队列仍有 %d 项，边界上存在假开口", got)
	}

	for _, key := range withheld {
		engine.SubmitGenerated(GeneratedChunk{
			Dimension: key.Dimension,
			Pos:       key.Pos,
			Chunk:     fluidSeedChunk(key.Pos, seed),
		})
	}
	for range 200 {
		engine.Step()
	}
	acrossSeam := []struct {
		position core.BlockPos
		want     core.BlockID
	}{
		{core.BlockPos{X: 16, Y: 1, Z: 8}, core.WaterLevel4ID},
		{core.BlockPos{X: 17, Y: 1, Z: 8}, core.WaterLevel5ID},
		{core.BlockPos{X: 18, Y: 1, Z: 8}, core.WaterLevel6ID},
		{core.BlockPos{X: 19, Y: 1, Z: 8}, core.WaterLevel7ID},
		{core.BlockPos{X: 20, Y: 1, Z: 8}, core.AirID},
	}
	for _, item := range acrossSeam {
		if got := fluidBlockAt(t, engine, item.position); got != item.want {
			t.Fatalf("接缝外侧 %+v=%d，想要 %d", item.position, got, item.want)
		}
	}

	// D5：清空队列并让全部区块重新走一遍边界重扫（等价于重启后的恢复路径），
	// 平衡态必须是不动点——接缝处也不例外。
	overworldFluidQueue(t, engine).Clear()
	clear(engine.fluidScope)
	for tick := range 40 {
		result := engine.Step()
		for _, batch := range result.Changes {
			for _, change := range batch.Changes {
				t.Fatalf("重扫后第 %d tick 仍产生方块变更 %+v", tick, change)
			}
		}
	}
}

// TestFluidOutsideInterestRangeHoldsAndResumes 覆盖 spec 的两个 Scenario：
// 「兴趣范围外不推进」与「区块重新进入兴趣范围后恢复推进」。
//
// 用一对**紧挨着、只隔一条区块边界**的同款孤立流动水做差分对照：
// (47,5,8) 在区块 (2,0) 内、处于推进范围；(48,5,8) 在区块 (3,0) 内、处于范围外。
// 两格由区块 (2,0) 进入推进范围时的同一次边界重扫排进同一个队列、在同一个 tick
// 被同一次 Advance 取出，唯一的差别就是是否落在推进范围内。因此"一个消失、
// 一个原封不动"只能归因于范围约束本身：若把范围过滤去掉，两格会一起消失。
func TestFluidOutsideInterestRangeHoldsAndResumes(t *testing.T) {
	// 孤立的流动水：上方不是流体、四周没有等级更小的流体邻居，一旦被推进就
	// 必然在下一次求值中消失。两格互为水平邻居但等级相同，谁也不支撑谁。
	inside := core.BlockPos{X: 3<<core.SectionShift - 1, Y: 5, Z: 8}
	outside := core.BlockPos{X: 3 << core.SectionShift, Y: 5, Z: 8}
	outsidePos := core.ChunkPos{X: 3}
	seed := map[core.BlockPos]core.BlockID{
		inside:  core.WaterLevel1ID,
		outside: core.WaterLevel1ID,
	}

	engine := NewEngine(DropInterestRadius, 0)
	const session = SessionID(1)
	dimension := engine.dimensions[core.Overworld]
	if !dimension.BeginGeneration(outsidePos) {
		t.Fatal("范围外区块未开始生成")
	}
	if err := dimension.ApplyGenerated(outsidePos, fluidSeedChunk(outsidePos, seed)); err != nil {
		t.Fatal(err)
	}
	engine.RegisterSession(session, core.Overworld, core.ChunkPos{})
	for range 12 {
		result := engine.Step()
		for _, key := range result.Acquire {
			engine.SubmitAcquired(AcquiredChunk{Key: key, Missing: true})
		}
		for _, key := range result.Generate {
			engine.SubmitGenerated(GeneratedChunk{
				Dimension: key.Dimension, Pos: key.Pos, Chunk: fluidSeedChunk(key.Pos, seed),
			})
		}
	}
	if player, ok := engine.Player(session); !ok || !player.Ready {
		t.Fatalf("玩家未 Ready: %+v", player)
	}

	outsideKey := core.ChunkKey{Dimension: core.Overworld, Pos: outsidePos}
	if _, inScope := engine.fluidScope[outsideKey]; inScope {
		t.Fatal("测试前提被破坏：区块 (3,0) 竟在初始推进范围内")
	}
	for range 60 {
		engine.Step()
	}
	if info, ok := dimension.Info(outsidePos); !ok || info.State != ChunkReady {
		t.Fatalf("范围外区块被卸载了: %+v", info)
	}
	// 对照组：范围内的同款孤立水必须已经消失，证明重扫与推进确实作用到了
	// 这条接缝上，"范围外那一格没变化"因此不是空转。
	if got := fluidBlockAt(t, engine, inside); got != core.AirID {
		t.Fatalf("范围内的对照格 %+v=%d，想要已收敛为空气", inside, got)
	}
	if got := fluidBlockAt(t, engine, outside); got != core.WaterLevel1ID {
		t.Fatalf("范围外的流体格被推进成 %d，想要保持 %d", got, core.WaterLevel1ID)
	}

	// 让玩家走进区块 (3,0)，该区块重新进入活动兴趣范围。
	engine.sessions[session].player.state.Position = mgl32.Vec3{
		float32(3<<core.SectionShift) + 8.5, 1, 8.5,
	}
	for range 60 {
		engine.Step()
	}
	if _, inScope := engine.fluidScope[outsideKey]; !inScope {
		t.Fatal("区块 (3,0) 没有重新进入推进范围")
	}
	if got := fluidBlockAt(t, engine, outside); got != core.AirID {
		t.Fatalf("重新进入范围后流体格=%d，想要收敛为空气", got)
	}
}

// TestBlockRemovalEnqueuesNeighbouringFluid 验证方块写入点确实接上了流体入队：
// 采掘、放置、伙伴放置与伙伴采掘全部经由 recordChange 落地，入队钩子挂在那里。
//
// 水源用 SetBlockForTest 直接写进世界，绕过了 recordChange，也错过了区块进入
// 推进范围时的重扫，因此在采掘完成之前它必须一直是孤立的一格——采掘写入之后
// 水开始扩散，才说明入队钩子真的生效了。
func TestBlockRemovalEnqueuesNeighbouringFluid(t *testing.T) {
	engine, _, targets := readyMiningPlayers(t, 1)
	target := targets[0]
	source := core.BlockPos{X: target.X, Y: target.Y, Z: target.Z - 1}
	behind := core.BlockPos{X: target.X, Y: target.Y, Z: target.Z - 2}
	engine.SetBlockForTest(source, core.WaterSourceID)

	for range 10 {
		engine.Step()
	}
	if got := fluidBlockAt(t, engine, target); got != core.StoneID {
		t.Fatalf("采掘尚未完成时目标已变成 %d", got)
	}
	if got := fluidBlockAt(t, engine, behind); got != core.AirID {
		t.Fatalf("采掘完成前水源就扩散到了 %+v (=%d)，测试前提被破坏", behind, got)
	}

	for range 120 {
		engine.Step()
	}
	if got := fluidBlockAt(t, engine, target); got != core.WaterLevel1ID {
		t.Fatalf("采掘出的空位 %+v=%d，想要被水填充为 %d", target, got, core.WaterLevel1ID)
	}
	if got := fluidBlockAt(t, engine, behind); got != core.WaterLevel1ID {
		t.Fatalf("水源背面 %+v=%d，想要 %d", behind, got, core.WaterLevel1ID)
	}
}
