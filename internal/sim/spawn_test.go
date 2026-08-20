package sim

import (
	"reflect"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/physics"
	"github.com/channing771/mornlea/internal/world"
)

func TestSpawnCandidatesOrderByDistanceThenXZ(t *testing.T) {
	got := spawnCandidates(core.ChunkPos{}, defaultSpawnRadius)
	wantFirst := []spawnColumn{
		{X: 0, Z: 0},
		{X: -1, Z: 0},
		{X: 0, Z: -1},
		{X: 0, Z: 1},
		{X: 1, Z: 0},
		{X: -1, Z: -1},
		{X: -1, Z: 1},
		{X: 1, Z: -1},
		{X: 1, Z: 1},
	}
	wantLast := []spawnColumn{
		{X: -16, Z: -16},
		{X: -16, Z: 16},
		{X: 16, Z: -16},
		{X: 16, Z: 16},
	}
	if len(got) != 33*33 || !reflect.DeepEqual(got[:len(wantFirst)], wantFirst) ||
		!reflect.DeepEqual(got[len(got)-len(wantLast):], wantLast) {
		t.Fatalf("候选顺序或半径错误: len=%d first=%+v last=%+v", len(got), got[:len(wantFirst)], got[len(got)-len(wantLast):])
	}

	offset := spawnCandidates(core.ChunkPos{X: 2, Z: -3}, defaultSpawnRadius)
	if offset[0] != (spawnColumn{X: 32, Z: -48}) {
		t.Fatalf("anchor 偏移后的首候选=%+v", offset[0])
	}
}

func TestSpawnWaitsForEarlierUnknownCandidate(t *testing.T) {
	engine := NewEngine(0, 0)
	engine.RegisterSession(1, core.Overworld, core.ChunkPos{})
	requested := engine.Step()
	for _, key := range requested.Acquire {
		engine.SubmitAcquired(AcquiredChunk{Key: key, Missing: true})
	}
	engine.Step()

	laterChunk := world.NewChunk(core.ChunkPos{X: -1})
	laterChunk.SetBlock(15, 0, 0, core.GrassID)
	loadSpawnTestChunk(t, engine.dimensions[core.Overworld], laterChunk)
	if player := onlyInternalPlayer(t, engine.Step()); player.Ready {
		t.Fatalf("较早候选仍 unknown 时跳到了较晚 surface: %+v", player)
	}

	engine.SubmitGenerated(GeneratedChunk{
		Dimension: core.Overworld,
		Pos:       core.ChunkPos{},
		Chunk:     spawnTestChunk(core.ChunkPos{}, core.BlockPos{}),
	})
	player := onlyInternalPlayer(t, engine.Step())
	if !player.Ready || player.State.Position != (mgl32.Vec3{0.5, 1, 0.5}) {
		t.Fatalf("较早候选 Ready 后 spawn=%+v", player)
	}
}

func TestPendingSpawnGenerateRetainActivateAndForget(t *testing.T) {
	engine := NewEngine(0, 0)
	const sessionID = SessionID(1)
	anchor := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{}}
	target := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -1}}
	engine.RegisterSession(sessionID, core.Overworld, anchor.Pos)

	acquiredAnchor := engine.Step()
	if player := onlyInternalPlayer(t, acquiredAnchor); player.Ready {
		t.Fatalf("生成 anchor 前 player=%+v，想要 PendingSpawn", player)
	}
	if !reflect.DeepEqual(acquiredAnchor.Acquire, []core.ChunkKey{anchor}) {
		t.Fatalf("首次 Acquire=%+v，想要 [%+v]", acquiredAnchor.Acquire, anchor)
	}
	engine.SubmitAcquired(AcquiredChunk{Key: anchor, Missing: true})
	generatedAnchor := engine.Step()
	if !reflect.DeepEqual(generatedAnchor.Generate, []core.ChunkKey{anchor}) {
		t.Fatalf("首次 Generate=%+v，想要 [%+v]", generatedAnchor.Generate, anchor)
	}

	engine.SubmitGenerated(GeneratedChunk{
		Dimension: core.Overworld,
		Pos:       anchor.Pos,
		Chunk:     world.NewChunk(anchor.Pos),
	})
	retained := engine.Step()
	if player := onlyInternalPlayer(t, retained); player.Ready {
		t.Fatalf("空 anchor 后 player=%+v，想要继续 PendingSpawn", player)
	}
	if !reflect.DeepEqual(retained.Ready, []core.ChunkKey{anchor}) ||
		!reflect.DeepEqual(retained.Acquire, []core.ChunkKey{target}) {
		t.Fatalf("retain tick Ready=%+v Acquire=%+v", retained.Ready, retained.Acquire)
	}
	session := engine.sessions[sessionID]
	if _, ok := session.wanted[anchor]; !ok {
		t.Fatalf("PendingSpawn 未保留 anchor: wanted=%+v", session.wanted)
	}
	if _, ok := session.wanted[target]; !ok {
		t.Fatalf("PendingSpawn 未保留 target: wanted=%+v", session.wanted)
	}
	if _, ok := session.player.spawnWanted[anchor.Pos]; !ok {
		t.Fatalf("spawnWanted 未记录 anchor: %+v", session.player.spawnWanted)
	}
	if _, ok := session.player.spawnWanted[target.Pos]; !ok {
		t.Fatalf("spawnWanted 未记录 target: %+v", session.player.spawnWanted)
	}

	engine.SubmitAcquired(AcquiredChunk{Key: target, Missing: true})
	generatedTarget := engine.Step()
	if !reflect.DeepEqual(generatedTarget.Generate, []core.ChunkKey{target}) {
		t.Fatalf("target Generate=%+v", generatedTarget.Generate)
	}
	engine.SubmitGenerated(GeneratedChunk{
		Dimension: core.Overworld,
		Pos:       target.Pos,
		Chunk: spawnTestChunk(target.Pos, core.BlockPos{
			X: -1,
			Y: 0,
			Z: 0,
		}),
	})
	activated := engine.Step()
	player := onlyInternalPlayer(t, activated)
	if !player.Ready || !player.Reset ||
		player.State.Position != (mgl32.Vec3{-0.5, 1, 0.5}) {
		t.Fatalf("target Ready 后 player=%+v，想要 Active reset", player)
	}
	if !reflect.DeepEqual(activated.Ready, []core.ChunkKey{target}) ||
		!reflect.DeepEqual(activated.Forget[sessionID], []core.ChunkKey{anchor}) {
		t.Fatalf("activate tick Ready=%+v Forget=%+v", activated.Ready, activated.Forget)
	}
	if !reflect.DeepEqual(session.wanted, map[core.ChunkKey]struct{}{target: {}}) {
		t.Fatalf("Active subscription wanted=%+v，想要仅 target", session.wanted)
	}
	if !reflect.DeepEqual(engine.wanted, map[core.ChunkKey]struct{}{target: {}}) {
		t.Fatalf("Active union wanted=%+v，想要仅 target", engine.wanted)
	}
	record := engine.dimensions[core.Overworld].records[anchor.Pos]
	if record == nil || record.State != ChunkUnloading || record.Chunk == nil ||
		!record.UnloadRequested || !record.Dirty() {
		t.Fatalf("activate forget 后未保留待持久 anchor: %+v", record)
	}
}

func TestExhaustedSpawnRetriesOnlyAfterRevisionChange(t *testing.T) {
	engine := NewEngine(0, 0)
	engine.RegisterSession(1, core.Overworld, core.ChunkPos{})
	dimension := engine.dimensions[core.Overworld]
	for x := int32(-1); x <= 1; x++ {
		for z := int32(-1); z <= 1; z++ {
			loadSpawnTestChunk(t, dimension, world.NewChunk(core.ChunkPos{X: x, Z: z}))
		}
	}

	if player := onlyInternalPlayer(t, engine.Step()); player.Ready {
		t.Fatalf("全空气候选不应 Ready: %+v", player)
	}
	record := dimension.records[core.ChunkPos{}]
	record.Chunk.SetBlock(0, 0, 0, core.GrassID)
	if player := onlyInternalPlayer(t, engine.Step()); player.Ready {
		t.Fatalf("revision 未变却重新扫描: %+v", player)
	}

	record.Revision++
	player := onlyInternalPlayer(t, engine.Step())
	if !player.Ready || player.State.Position != (mgl32.Vec3{0.5, 1, 0.5}) {
		t.Fatalf("revision 改变后未从首候选重试: %+v", player)
	}
}

func onlyInternalPlayer(t *testing.T, result TickResult) PlayerUpdate {
	t.Helper()
	if len(result.Players) != 1 {
		t.Fatalf("Players=%+v，想要恰好一个", result.Players)
	}
	return result.Players[0]
}

func loadSpawnTestChunk(t *testing.T, dimension *Dimension, chunk *world.Chunk) {
	t.Helper()
	if !dimension.BeginGeneration(chunk.Pos) {
		t.Fatalf("区块 %+v 未开始生成", chunk.Pos)
	}
	if err := dimension.ApplyGenerated(chunk.Pos, chunk); err != nil {
		t.Fatal(err)
	}
}

func spawnTestChunk(pos core.ChunkPos, support core.BlockPos) *world.Chunk {
	chunk := world.NewChunk(pos)
	x, _, z := support.Local()
	chunk.SetBlock(x, support.Y, z, core.GrassID)
	return chunk
}

// TestSpawnSkipsSubmergedColumn 钉死「水下地表不是落脚点」。
//
// 夹具必须让候选列真的淹在水里：首候选 (0,0) 的地面在 y=0，其上 y=1..3 灌满
// 水源；玩家站在 (0.5,1,0.5) 时身体 AABB（y∈[1,2.8]）与这些水格正相交，是
// 组 6 溺水结算认定的浸没态。次近候选 (-1,0) 的地面同样在 y=0 但头顶无水，
// 出生点必须落到那里。
//
// 流体零碰撞体，因此 playerBoundsAreFree 对两列的读数完全相同——「改对改错
// 读数相同」的空转在这里靠水的存在与否本身区分，不是靠碰撞体。
func TestSpawnSkipsSubmergedColumn(t *testing.T) {
	engine := NewEngine(0, 0)
	engine.RegisterSession(1, core.Overworld, core.ChunkPos{})
	dimension := engine.dimensions[core.Overworld]

	chunk := world.NewChunk(core.ChunkPos{})
	chunk.SetBlock(0, 0, 0, core.GrassID)
	for y := int32(1); y <= 3; y++ {
		chunk.SetBlock(0, y, 0, core.WaterSourceID)
	}
	loadSpawnTestChunk(t, dimension, chunk)
	negative := world.NewChunk(core.ChunkPos{X: -1})
	negative.SetBlock(15, 0, 0, core.GrassID)
	loadSpawnTestChunk(t, dimension, negative)

	player := onlyInternalPlayer(t, engine.Step())
	if !player.Ready {
		t.Fatalf("玩家未 Ready: %+v", player)
	}
	if got := player.State.Position; got != (mgl32.Vec3{-0.5, 1, 0.5}) {
		t.Fatalf("出生点=%v，想要跳过水下的 (0.5,1,0.5) 落到 (-0.5,1,0.5)", got)
	}

	// 夹具承重守卫排在真实断言之后：被跳过那一列必须真的有水，且水下地表在
	// 碰撞意义上确实"可站立"（否则跳过的原因是别的，本用例就没测到流体）。
	for y := int32(1); y <= 3; y++ {
		position := core.BlockPos{X: 0, Y: y, Z: 0}
		block, ready := dimension.BlockAt(position)
		if !ready || !core.IsFluid(block) {
			t.Fatalf("夹具失效：%+v=%d ready=%v，不是流体", position, block, ready)
		}
	}
	source := dimensionCollisionSource{dimension: dimension}
	free, ready := playerBoundsAreFree(mgl32.Vec3{0.5, 1, 0.5}, source)
	if !ready || !free {
		t.Fatalf("夹具失效：水下候选 free=%v ready=%v，想要碰撞判定认为可站立", free, ready)
	}
}

// spawnLadderChunk 造一块「草地 y=0 + 水源 y=1..depth」的区块；depth<1 表示不放水。
func spawnLadderChunk(pos core.ChunkPos, depth int32) *world.Chunk {
	chunk := world.NewChunk(pos)
	for x := range core.SectionSize {
		for z := range core.SectionSize {
			chunk.SetBlock(x, 0, z, core.GrassID)
			for y := int32(1); y <= depth; y++ {
				chunk.SetBlock(x, y, z, core.WaterSourceID)
			}
		}
	}
	chunk.Compact()
	return chunk
}

// spawnLadderColumn 把某一列改写成指定水深（先清空 y=1..4 再灌 depth 格）。
func spawnLadderColumn(chunk *world.Chunk, column core.BlockPos, depth int32) {
	x, _, z := column.Local()
	for y := int32(1); y <= 4; y++ {
		block := core.AirID
		if y <= depth {
			block = core.WaterSourceID
		}
		chunk.SetBlock(x, y, z, block)
	}
	chunk.Compact()
}

// spawnLadderEngine 把玩家的全部候选区块都铺成 depth 格深的水，再按 overrides
// 改写个别列的水深，然后推进到出生完成。
func spawnLadderEngine(
	t *testing.T,
	depth int32,
	overrides map[core.BlockPos]int32,
) (*Engine, PlayerUpdate) {
	t.Helper()
	engine := NewEngine(0, 0)
	engine.RegisterSession(1, core.Overworld, core.ChunkPos{})
	dimension := engine.dimensions[core.Overworld]
	chunks := make(map[core.ChunkPos]*world.Chunk)
	for _, pos := range engine.sessions[1].player.candidateChunks {
		chunks[pos] = spawnLadderChunk(pos, depth)
	}
	for column, columnDepth := range overrides {
		chunk, ok := chunks[column.Chunk()]
		if !ok {
			t.Fatalf("override 列 %+v 不在候选区块内", column)
		}
		spawnLadderColumn(chunk, column, columnDepth)
	}
	for _, pos := range engine.sessions[1].player.candidateChunks {
		loadSpawnTestChunk(t, dimension, chunks[pos])
	}
	return engine, onlyInternalPlayer(t, engine.Step())
}

// spawnTierAt 复算某个落脚点的档位，供夹具承重守卫使用。
func spawnTierAt(engine *Engine, position mgl32.Vec3) spawnTier {
	source := dimensionCollisionSource{dimension: engine.dimensions[core.Overworld]}
	bodyInFluid, eyeInFluid := physics.SubmersionFlags(position, source)
	switch {
	case eyeInFluid:
		return spawnTierSubmerged
	case bodyInFluid:
		return spawnTierEyeDry
	}
	return spawnTierDry
}

// TestSpawnFallsBackToSubmergedColumnWhenNoDryColumnExists 钉死降级阶梯的兜底：
// 整片候选范围都是深水（评审用真实 worldgen 复现的海洋形状）时，玩家必须仍能
// 出生在第 3 档，而不是永久停在 PendingSpawn。
//
// 「永远无法登录」比「出生在水里」严重得多，而后者可自救：玩家有浮力与持续
// 上浮，氧气 300 tick。
func TestSpawnFallsBackToSubmergedColumnWhenNoDryColumnExists(t *testing.T) {
	engine, player := spawnLadderEngine(t, 4, nil)
	if !player.Ready {
		t.Fatalf("全水候选范围下玩家未 Ready（永久 PendingSpawn）: %+v", player)
	}
	if got := player.State.Position; got != (mgl32.Vec3{0.5, 1, 0.5}) {
		t.Fatalf("全水兜底出生点=%v，想要首候选列的 (0.5,1,0.5)", got)
	}

	// 夹具承重守卫排在真实断言之后：出生点必须真的浸没到眼睛，否则这一档
	// 根本没被走到，用例退化成在测第 1 档。
	if tier := spawnTierAt(engine, player.State.Position); tier != spawnTierSubmerged {
		t.Fatalf("夹具失效：出生点 %v 的档位=%d，想要 spawnTierSubmerged(%d)",
			player.State.Position, tier, spawnTierSubmerged)
	}
}

// TestSpawnLadderPrefersDryThenShallow 钉死阶梯的优先级：**有干地时绝不选浅水**，
// 无干地时选浅水而不是深水。少了这条，阶梯会退化成"随便挑一个可站立格"。
//
// 优先级必须跨候选顺序成立：干地列 (0,-1) 排在深水首候选 (0,0) 与浅水列 (-1,0)
// **之后**，若实现按"先到先得"就会选中前面的水列。
func TestSpawnLadderPrefersDryThenShallow(t *testing.T) {
	shallow := core.BlockPos{X: -1, Z: 0}
	dry := core.BlockPos{X: 0, Z: -1}

	engine, player := spawnLadderEngine(t, 4, map[core.BlockPos]int32{
		shallow: 1,
		dry:     0,
	})
	if !player.Ready {
		t.Fatalf("玩家未 Ready: %+v", player)
	}
	if got := player.State.Position; got != (mgl32.Vec3{0.5, 1, -0.5}) {
		t.Fatalf("有干地时出生点=%v，想要干地列 (0.5,1,-0.5)", got)
	}
	if tier := spawnTierAt(engine, player.State.Position); tier != spawnTierDry {
		t.Fatalf("夹具失效：干地出生点 %v 的档位=%d，想要 spawnTierDry(%d)",
			player.State.Position, tier, spawnTierDry)
	}

	// 去掉干地列，浅水必须压过深水。
	shallowEngine, shallowPlayer := spawnLadderEngine(t, 4, map[core.BlockPos]int32{
		shallow: 1,
	})
	if !shallowPlayer.Ready {
		t.Fatalf("无干地时玩家未 Ready: %+v", shallowPlayer)
	}
	if got := shallowPlayer.State.Position; got != (mgl32.Vec3{-0.5, 1, 0.5}) {
		t.Fatalf("无干地时出生点=%v，想要浅水列 (-0.5,1,0.5)", got)
	}

	// 夹具承重守卫排在真实断言之后：浅水列必须真的是第 2 档、首候选列必须真的
	// 是第 3 档，两档不同这条优先级断言才有对照。
	if tier := spawnTierAt(shallowEngine, shallowPlayer.State.Position); tier != spawnTierEyeDry {
		t.Fatalf("夹具失效：浅水出生点 %v 的档位=%d，想要 spawnTierEyeDry(%d)",
			shallowPlayer.State.Position, tier, spawnTierEyeDry)
	}
	if tier := spawnTierAt(shallowEngine, mgl32.Vec3{0.5, 1, 0.5}); tier != spawnTierSubmerged {
		t.Fatalf("夹具失效：首候选列 (0.5,1,0.5) 的档位=%d，想要 spawnTierSubmerged(%d)",
			tier, spawnTierSubmerged)
	}
}
