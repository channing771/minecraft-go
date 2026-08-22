package physics_test

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/physics"
)

var fullCube = core.AABB{Max: mgl32.Vec3{1, 1, 1}}

type boxSource map[core.BlockPos][]core.AABB

func boxes(entries ...boxEntry) boxSource {
	world := make(boxSource, len(entries))
	for _, entry := range entries {
		world[entry.position] = entry.boxes
	}
	return world
}

type boxEntry struct {
	position core.BlockPos
	boxes    []core.AABB
}

func block(x, y, z int32, collisionBoxes ...core.AABB) boxEntry {
	return boxEntry{position: core.BlockPos{X: x, Y: y, Z: z}, boxes: collisionBoxes}
}

func (s boxSource) CollisionBoxes(position core.BlockPos) physics.CollisionBoxSet {
	set := physics.CollisionBoxSet{Loaded: true}
	for i, box := range s[position] {
		set.Boxes[i] = box
		set.Count++
	}
	return set
}

type unknownSource struct{ position core.BlockPos }

func unknownAt(position core.BlockPos) unknownSource { return unknownSource{position: position} }

func (s unknownSource) CollisionBoxes(position core.BlockPos) physics.CollisionBoxSet {
	if position == s.position {
		return physics.CollisionBoxSet{}
	}
	return physics.CollisionBoxSet{Loaded: true}
}

func TestCollisionStopsOnFloorAndWall(t *testing.T) {
	world := boxes(
		block(0, 0, 0, fullCube),
		block(1, 1, 0, fullCube),
	)
	state := physics.State{
		Position: mgl32.Vec3{0.5, 1.2, 0.5},
		Velocity: mgl32.Vec3{10, -10, 0},
	}
	got := physics.Step(state, physics.Input{}, world).State
	if math.Abs(float64(got.Position.Y()-1)) > 1e-5 || !got.OnGround {
		t.Fatalf("未落在 y=1: %+v", got)
	}
	if got.Position.X() > 0.7+1e-5 || got.Velocity.X() != 0 {
		t.Fatalf("穿过 x=1 墙: %+v", got)
	}
}

func TestUnknownBlockIsClosedBoundary(t *testing.T) {
	world := unknownAt(core.BlockPos{X: 1, Y: 1, Z: 0})
	got := physics.Step(physics.State{
		Position: mgl32.Vec3{0.5, 1, 0.5},
		Velocity: mgl32.Vec3{10, 0, 0},
		OnGround: true,
	}, physics.Input{}, world)
	if !got.HitUnknown || got.State.Position.X() > 0.7+1e-5 {
		t.Fatalf("unknown 未阻挡: %+v", got)
	}
}

func TestCutoutBlocksUseFullCollision(t *testing.T) {
	for _, id := range []core.BlockID{core.GlassID, core.LeavesID} {
		boxes := physics.BlockCollisionBoxes(id, true)
		if !boxes.Loaded || boxes.Count != 1 || boxes.Boxes[0] != fullCube {
			t.Fatalf("BlockCollisionBoxes(%d, true) = %+v，想要完整方块碰撞", id, boxes)
		}
	}
}

// TestFluidBlocksHaveNoCollision 锁定 spec Scenario「流体不阻挡通行与光照」：
// 8 个流体编号与空气同形状——已加载但零碰撞体。
func TestFluidBlocksHaveNoCollision(t *testing.T) {
	for id := core.WaterSourceID; id <= core.WaterLevel7ID; id++ {
		boxes := physics.BlockCollisionBoxes(id, true)
		if !boxes.Loaded || boxes.Count != 0 {
			t.Fatalf("BlockCollisionBoxes(%d, true) = %+v，想要 (Loaded:true, Count:0)", id, boxes)
		}
	}
}

// idSource 按方块 ID 而非直接给碰撞体建模一个只读世界，经
// physics.BlockCollisionBoxes 转换——用来在 Step 级别端到端验证「实体的碰撞体
// 与流体格重叠时可自由穿行」。
type idSource map[core.BlockPos]core.BlockID

func (s idSource) CollisionBoxes(position core.BlockPos) physics.CollisionBoxSet {
	return physics.BlockCollisionBoxes(s[position], true)
}

// TestEntityPassesThroughFluidBlock 端到端验证：同一堵墙，石头会挡住实体，
// 换成流体（水源）则实体可自由穿行——覆盖 spec Scenario「流体不阻挡通行与
// 光照」的「实体的碰撞体与流体格重叠时可自由穿行」这一半。
func TestEntityPassesThroughFluidBlock(t *testing.T) {
	stoneWorld := idSource{{X: 1, Y: 1, Z: 0}: core.StoneID}
	blocked := physics.Step(physics.State{
		Position: mgl32.Vec3{0.5, 1, 0.5},
		Velocity: mgl32.Vec3{10, 0, 0},
		OnGround: true,
	}, physics.Input{}, stoneWorld).State
	if blocked.Position.X() > 0.7+1e-5 {
		t.Fatalf("石头墙未阻挡实体: %+v", blocked)
	}

	waterWorld := idSource{{X: 1, Y: 1, Z: 0}: core.WaterSourceID}
	passed := physics.Step(physics.State{
		Position: mgl32.Vec3{0.5, 1, 0.5},
		Velocity: mgl32.Vec3{10, 0, 0},
		OnGround: true,
	}, physics.Input{}, waterWorld).State
	if passed.Position.X() <= 0.7+1e-5 {
		t.Fatalf("流体阻挡了实体穿行: %+v", passed)
	}
}

func TestWalkingOffLedgeClearsGroundInSameStep(t *testing.T) {
	world := boxes(block(0, 0, 0, fullCube))
	got := physics.Step(physics.State{
		Position: mgl32.Vec3{1.25, 1, 0.5},
		Velocity: mgl32.Vec3{4.3, 0, 0},
		OnGround: true,
	}, physics.Input{MoveX: 1}, world).State
	if got.OnGround {
		t.Fatalf("离开悬崖后仍 OnGround: %+v", got)
	}
}

func TestCollisionStopsAtCeilingAndClearsUpwardVelocity(t *testing.T) {
	world := boxes(block(0, 2, 0, fullCube))
	got := physics.Step(physics.State{
		Position: mgl32.Vec3{0.5, 0.1, 0.5},
		Velocity: mgl32.Vec3{0, 10, 0},
	}, physics.Input{}, world).State
	if math.Abs(float64(got.Position.Y()-0.2)) > 1e-5 || got.Velocity.Y() != 0 || got.OnGround {
		t.Fatalf("穿过天花板或未清除上升速度: %+v", got)
	}
}

func TestCollisionHandlesNegativeWorldCoordinates(t *testing.T) {
	world := boxes(
		block(0, 0, 0, fullCube),
		block(-1, 1, 0, fullCube),
	)
	got := physics.Step(physics.State{
		Position: mgl32.Vec3{0.5, 1, 0.5},
		Velocity: mgl32.Vec3{-30, 0, 0},
		OnGround: true,
	}, physics.Input{}, world).State
	if math.Abs(float64(got.Position.X()-0.3)) > 1e-5 || got.Velocity.X() != 0 {
		t.Fatalf("穿过负坐标方块: %+v", got)
	}
}

func TestCollisionResolvesCornerInYXZOrder(t *testing.T) {
	world := boxes(
		block(0, 0, 0, fullCube),
		block(1, 1, 0, fullCube),
		block(0, 1, 1, fullCube),
	)
	got := physics.Step(physics.State{
		Position: mgl32.Vec3{0.5, 1.2, 0.5},
		Velocity: mgl32.Vec3{10, -10, 10},
		OnGround: true,
	}, physics.Input{}, world).State
	want := mgl32.Vec3{0.7, 1, 0.7}
	if !got.Position.ApproxEqualThreshold(want, 1e-5) || got.Velocity != (mgl32.Vec3{}) || !got.OnGround {
		t.Fatalf("角落结果=%+v，想要位置=%v 且三个轴速度归零", got, want)
	}
}

// TestCropBlocksHaveNoCollision 锁定 spec Scenario「玩家穿过作物」的编号一半：
// 8 个作物编号与空气、流体同形状——已加载但零碰撞体。
func TestCropBlocksHaveNoCollision(t *testing.T) {
	for id := core.WheatStage0ID; id <= core.WheatStage7ID; id++ {
		boxes := physics.BlockCollisionBoxes(id, true)
		if !boxes.Loaded || boxes.Count != 0 {
			t.Fatalf("BlockCollisionBoxes(%d, true) = %+v，想要 (Loaded:true, Count:0)", id, boxes)
		}
	}
}

// farmlandCube 是耕地碰撞体的期望形状：与完整方块只差顶面的 1/16。
var farmlandCube = core.AABB{Max: mgl32.Vec3{1, 0.9375, 1}}

// TestFarmlandCollisionIsFifteenSixteenthsTall 锁定 spec Requirement「作物不提供
// 碰撞体，耕地略低于满方块」的耕地一半：两个耕地编号都是单盒，水平面与完整方块
// 相同，顶面恰在 15/16。数值写死是刻意的——15/16 是可观察契约（站上去比满方块
// 低 1/16），不是实现细节。
func TestFarmlandCollisionIsFifteenSixteenthsTall(t *testing.T) {
	for _, id := range []core.BlockID{core.FarmlandDryID, core.FarmlandWetID} {
		boxes := physics.BlockCollisionBoxes(id, true)
		if !boxes.Loaded || boxes.Count != 1 || boxes.Boxes[0] != farmlandCube {
			t.Fatalf("BlockCollisionBoxes(%d, true) = %+v，想要单盒 %+v", id, boxes, farmlandCube)
		}
	}
}

// cropWalkFloor 铺一条 x∈[-1,8]、y=0、z=0 的地面，供行走用例使用。
func cropWalkFloor(filler core.BlockID) idSource {
	world := idSource{}
	for x := int32(-1); x <= 8; x++ {
		world[core.BlockPos{X: x, Y: 0, Z: 0}] = core.DirtID
	}
	// 挡在移动路径正中的那一列：玩家身高 1.8，脚在 y=1 时身体覆盖 y=1 与 y=2，
	// 两格都要填，否则「换成石头」的对照组会从上半身穿过去而不是被挡住。
	world[core.BlockPos{X: 2, Y: 1, Z: 0}] = filler
	world[core.BlockPos{X: 2, Y: 2, Z: 0}] = filler
	return world
}

// walkEast 从 (0.5, 1, 0.5) 起持续按住 +X 走 tickCount 个固定步，返回末态。
func walkEast(world idSource, tickCount int) physics.State {
	state := physics.State{Position: mgl32.Vec3{0.5, 1, 0.5}, OnGround: true}
	for range tickCount {
		state = physics.Step(state, physics.Input{MoveX: 1}, world).State
	}
	return state
}

// TestPlayerWalksThroughCropUnchanged 端到端覆盖 spec Scenario「玩家穿过作物」：
// 同一条路径上摆一株作物，玩家的 20 tick 行走末态与路径上什么都没有时**逐位
// 相同**——这走的是权威积分的真实路径（Go 编码 prism → Rust 解析碰撞），不是
// 只断言 Count == 0。
//
// 石头对照组是本用例的反空转手段：它证明夹具里那一列**恰好挡在移动路径上**
// （差值非零）。没有它，一个作物压根不在路径上的夹具也会「通过」——存在性在
// 两种规则下同时成立，差值恒等。
func TestPlayerWalksThroughCropUnchanged(t *testing.T) {
	const ticks = 20
	empty := walkEast(cropWalkFloor(core.AirID), ticks)
	crop := walkEast(cropWalkFloor(core.WheatStage7ID), ticks)
	stone := walkEast(cropWalkFloor(core.StoneID), ticks)

	if crop != empty {
		t.Fatalf("作物改变了行走末态：作物=%+v 空路径=%+v", crop, empty)
	}
	// 反空转：石头必须在 x=2 前把玩家卡住（玩家半宽 0.3），而空路径必须早已
	// 越过那一列。两个界之间留着整整一格，任何「作物不在路径上」的夹具退化都
	// 会让下面两条之一变红。
	if stone.Position.X() > 1.7+physics.CollisionEpsilon {
		t.Fatalf("石头对照组未挡住玩家：x=%v，夹具没把方块摆在移动路径上", stone.Position.X())
	}
	if empty.Position.X() < 3 {
		t.Fatalf("空路径组只走了 x=%v，夹具的移动距离不足以穿过 x=2 那一列", empty.Position.X())
	}
}

// dropOnto 让玩家从 (centerX, 2, 0.5) 无初速下落 tickCount 个固定步，返回末态。
func dropOnto(world idSource, centerX float32, tickCount int) physics.State {
	state := physics.State{Position: mgl32.Vec3{centerX, 2, 0.5}}
	for range tickCount {
		state = physics.Step(state, physics.Input{}, world).State
	}
	return state
}

// TestStandingOnFarmlandIsOneSixteenthLowerThanFullBlock 端到端覆盖 spec
// Scenario「站上耕地低于站上完整方块」：同一高度上并排的耕地与泥土，分别站上去
// 的立足 Y **恰好**差 1/16。
//
// 断言的是差值而不是「更低」：两块都是满立方体时「更低」这类比较会退化成恒真
// 的存在性断言，而差值 == 1/16 在旧规则（耕地也是满立方体）下是 0，一定变红。
// 差值可以精确断言——1.0 与 0.9375 都是 f32 精确值，且落地钳位是
// Y_prev + (top − Y_prev) 这种 Sterbenz 精确减法，不引入舍入。
func TestStandingOnFarmlandIsOneSixteenthLowerThanFullBlock(t *testing.T) {
	const ticks = 20
	world := idSource{
		{X: 0, Y: 0, Z: 0}: core.DirtID,
		{X: 2, Y: 0, Z: 0}: core.FarmlandDryID,
	}
	onDirt := dropOnto(world, 0.5, ticks)
	onFarmland := dropOnto(world, 2.5, ticks)

	if !onDirt.OnGround || !onFarmland.OnGround {
		t.Fatalf("玩家未在 %d tick 内落地：泥土=%+v 耕地=%+v", ticks, onDirt, onFarmland)
	}
	if onDirt.Position.Y() != 1 {
		t.Fatalf("站在泥土上的立足 Y = %v，想要 1", onDirt.Position.Y())
	}
	if onFarmland.Position.Y() != 0.9375 {
		t.Fatalf("站在耕地上的立足 Y = %v，想要 0.9375", onFarmland.Position.Y())
	}
	if got := onDirt.Position.Y() - onFarmland.Position.Y(); got != 1.0/16 {
		t.Fatalf("耕地与完整方块的立足 Y 差值 = %v，想要恰好 1/16", got)
	}
}
