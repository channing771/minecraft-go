// 本文件覆盖有界确定性寻路（pathfind.go）与路径重算策略（pathfind_policy.go）：
// 相同快照重放一致、固定邻居展开序、4096 节点预算（含 4096/4097 精确边界）、
// 四种整数移动（平移/跳上一格/下落一格/跨一格间隙）、不修改任何方块、区块
// revision 透传、失效冷却重算与三连失败终止。全部为纯 CPU 测试，不启动窗口、
// 不做任何 I/O。
package companion

import (
	"errors"
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

// pathLegend 是 ASCII 层字面量网格的 rune→方块映射：'.' 空气、'#' 石头。
var pathLegend = map[rune]core.BlockID{'.': core.AirID, '#': core.StoneID}

// pathTestTable 是测试阻挡表：只有空气可通过，其余一切（含未知编号）阻挡。
var pathTestTable = NewPathBlockTable(map[core.BlockID]bool{core.AirID: true})

// mustPathGrid 执行构造闭包并拆开结果，失败即 Fatal。收闭包而不是
// (PathGrid, error) 对，是因为 Go 不允许把多返回值混进参数表。
func mustPathGrid(t *testing.T, build func() (PathGrid, error)) PathGrid {
	t.Helper()
	grid, err := build()
	if err != nil {
		t.Fatalf("构造寻路网格失败: %v", err)
	}
	return grid
}

// 哨兵错误断言 helper wantErrorIs 的定义位于 planner_test.go：本文件原本与
// planner 测试各持一份逐字相同的副本，按并集语义合并后由两个文件共用。

// assertPathCells 断言坐标序列逐点相等。
func assertPathCells(t *testing.T, got, want []PathCell) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("坐标序列长度 = %d，want %d（got %v）", len(got), len(want), got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("坐标 %d = %+v，want %+v（got %v）", index, got[index], want[index], got)
		}
	}
}

// TestPathfindWindowBounds 锁定窗口几何：水平 ±16（含）、垂直 ±4（含）、
// 原点与三轴边长（33×9×33）。
func TestPathfindWindowBounds(t *testing.T) {
	if PathWindowHorizontalRadius != 16 || PathWindowVerticalRadius != 4 {
		t.Fatalf("窗口半径常量被改动: %d/%d", PathWindowHorizontalRadius, PathWindowVerticalRadius)
	}
	window := PathWindow{Center: PathCell{X: 0, Y: 64, Z: 0}}
	for _, cell := range []PathCell{
		{X: 16, Y: 68, Z: 16}, {X: -16, Y: 60, Z: -16},
		{X: 0, Y: 64, Z: 0}, {X: 16, Y: 64, Z: -16}, {X: 0, Y: 68, Z: 0},
	} {
		if !window.Contains(cell) {
			t.Fatalf("%+v 应在窗口内", cell)
		}
	}
	for _, cell := range []PathCell{
		{X: 17, Y: 64, Z: 0}, {X: -17, Y: 64, Z: 0},
		{X: 0, Y: 69, Z: 0}, {X: 0, Y: 59, Z: 0},
		{X: 0, Y: 64, Z: 17}, {X: 16, Y: 69, Z: 16},
	} {
		if window.Contains(cell) {
			t.Fatalf("%+v 应在窗口外", cell)
		}
	}
	if origin := window.Origin(); origin != (PathCell{X: -16, Y: 60, Z: -16}) {
		t.Fatalf("窗口原点 = %+v", origin)
	}
	sizeX, sizeY, sizeZ := window.Size()
	if sizeX != 33 || sizeY != 9 || sizeZ != 33 {
		t.Fatalf("窗口尺寸 = %d×%d×%d，want 33×9×33", sizeX, sizeY, sizeZ)
	}
}

// TestPathfindGridConstruction 覆盖网格构造边界与不可变性：一次性拷贝（构造后
// 不再读取世界）、revision 归一（排序/去重/独立副本）、寻路不修改任何方块、
// 结果 revision 独立于网格，以及全部构造失败路径。
func TestPathfindGridConstruction(t *testing.T) {
	calls := 0
	fetch := func(x, y, z int32) (core.BlockID, bool) {
		calls++
		if y == 63 {
			return core.StoneID, true
		}
		return core.AirID, true
	}
	revisions := []ChunkRevision{
		{Chunk: core.ChunkPos{X: 1, Z: 0}, Revision: 2},
		{Chunk: core.ChunkPos{X: 0, Z: 0}, Revision: 1},
		{Chunk: core.ChunkPos{X: 1, Z: 0}, Revision: 2},
	}
	grid := mustPathGrid(t, func() (PathGrid, error) {
		return NewPathGrid(core.BlockPos{X: 0, Y: 63, Z: 0}, 3, 3, 3,
			pathTestTable, fetch, revisions)
	})
	if calls != 27 {
		t.Fatalf("构造读取方块数 = %d，want 27（一次性拷贝）", calls)
	}
	wantRevisions := []ChunkRevision{
		{Chunk: core.ChunkPos{X: 0, Z: 0}, Revision: 1},
		{Chunk: core.ChunkPos{X: 1, Z: 0}, Revision: 2},
	}
	assertPathRevisions(t, grid.revisions, wantRevisions)
	// 归一结果是独立副本：修改调用方切片不影响网格。
	revisions[0].Revision = 99
	assertPathRevisions(t, grid.revisions, wantRevisions)

	// 寻路（成功与失败各一次）不修改任何方块，也不再触碰世界读取。
	before := append([]core.BlockID(nil), grid.blocks...)
	start, goal := PathCell{X: 0, Y: 64, Z: 0}, PathCell{X: 2, Y: 64, Z: 2}
	result, err := FindPath(grid, start, goal)
	if err != nil {
		t.Fatalf("平地寻路失败: %v", err)
	}
	if _, err := FindPath(grid, start, PathCell{X: 0, Y: 63, Z: 0}); !errors.Is(err, ErrPathUnreachable) {
		t.Fatalf("非站立格目标应不可达: %v", err)
	}
	for index := range before {
		if grid.blocks[index] != before[index] {
			t.Fatalf("寻路修改了方块 %d", index)
		}
	}
	if calls != 27 {
		t.Fatalf("寻路期间仍读取世界: %d", calls)
	}

	// 结果携带 revision 的独立副本：改结果不影响网格与后续结果。
	assertPathRevisions(t, result.Revisions, wantRevisions)
	result.Revisions[0].Revision = 777
	again, err := FindPath(grid, start, goal)
	if err != nil {
		t.Fatalf("重放寻路失败: %v", err)
	}
	assertPathRevisions(t, again.Revisions, wantRevisions)
	assertPathCells(t, again.Waypoints, result.Waypoints)

	// 构造失败矩阵：非正尺寸、超上限（读取任何方块前拒绝）、冲突 revision、
	// revision 超上限、读取失败、缺回调、层字面量非法。
	okFetch := func(x, y, z int32) (core.BlockID, bool) { return core.AirID, true }
	if _, err := NewPathGrid(core.BlockPos{}, 0, 3, 3, pathTestTable, okFetch, nil); err == nil {
		t.Fatal("非正尺寸被接受")
	}
	if _, err := NewPathGrid(core.BlockPos{}, 1000, 1000, 1, pathTestTable, okFetch, nil); err == nil {
		t.Fatal("超上限网格被接受")
	}
	conflict := []ChunkRevision{
		{Chunk: core.ChunkPos{X: 0, Z: 0}, Revision: 1},
		{Chunk: core.ChunkPos{X: 0, Z: 0}, Revision: 2},
	}
	if _, err := NewPathGrid(core.BlockPos{}, 1, 1, 1, pathTestTable, okFetch, conflict); err == nil {
		t.Fatal("冲突 revision 被接受")
	}
	over := make([]ChunkRevision, MaxPlanChunkRevisions+1)
	if _, err := NewPathGrid(core.BlockPos{}, 1, 1, 1, pathTestTable, okFetch, over); err == nil {
		t.Fatal("revision 超上限被接受")
	}
	failFetch := func(x, y, z int32) (core.BlockID, bool) { return 0, false }
	if _, err := NewPathGrid(core.BlockPos{}, 1, 1, 1, pathTestTable, failFetch, nil); err == nil {
		t.Fatal("读取失败被接受")
	}
	if _, err := NewPathGrid(core.BlockPos{}, 1, 1, 1, pathTestTable, nil, nil); err == nil {
		t.Fatal("缺回调被接受")
	}
	if _, err := NewPathGridFromLayers(core.BlockPos{}, pathTestTable, nil, pathLegend, nil); err == nil {
		t.Fatal("空层被接受")
	}
	if _, err := NewPathGridFromLayers(core.BlockPos{}, pathTestTable,
		[][]string{{"..", "."}}, pathLegend, nil); err == nil {
		t.Fatal("行宽不一致被接受")
	}
	if _, err := NewPathGridFromLayers(core.BlockPos{}, pathTestTable,
		[][]string{{".x"}}, pathLegend, nil); err == nil {
		t.Fatal("未知 rune 被接受")
	}
}

// assertPathRevisions 断言 revision 列表逐条相等。
func assertPathRevisions(t *testing.T, got, want []ChunkRevision) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("revision 数 = %d，want %d（got %v）", len(got), len(want), got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("revision[%d] = %+v，want %+v", index, got[index], want[index])
		}
	}
}

// TestPathfindReplayDeterministic 验证重放一致：同一确定性地形的三次独立构造
// 与寻路（两次同网格、一次重建网格）返回完全相同的路径；不可达输入的重放同样
// 得到同一失败哨兵。
func TestPathfindReplayDeterministic(t *testing.T) {
	// 8×8 平台：满足 (x%2==0 && z%3==1) 的列抽掉地板作障碍，保底保留 z=0
	// 整行与 x=7 整列，起终点必然连通；地形是纯函数，构造天然确定。
	terrain := func(x, y, z int32) (core.BlockID, bool) {
		if y == 63 && (z == 0 || x == 7 || !(x%2 == 0 && z%3 == 1)) {
			return core.StoneID, true
		}
		return core.AirID, true
	}
	build := func() PathGrid {
		return mustPathGrid(t, func() (PathGrid, error) {
			return NewPathGrid(core.BlockPos{X: 0, Y: 62, Z: 0}, 8, 4, 8,
				pathTestTable, terrain, []ChunkRevision{{Chunk: core.ChunkPos{X: 0, Z: 0}, Revision: 3}})
		})
	}
	start, goal := PathCell{X: 0, Y: 64, Z: 0}, PathCell{X: 7, Y: 64, Z: 7}
	first, err := FindPath(build(), start, goal)
	if err != nil {
		t.Fatalf("确定性地形寻路失败: %v", err)
	}
	second, err := FindPath(build(), start, goal)
	if err != nil {
		t.Fatalf("重放寻路失败: %v", err)
	}
	replay, err := FindPath(build(), start, goal)
	if err != nil {
		t.Fatalf("重建网格重放失败: %v", err)
	}
	assertPathCells(t, second.Waypoints, first.Waypoints)
	assertPathCells(t, replay.Waypoints, first.Waypoints)
	// 曼哈顿下界按跨隙计算：14 格水平距离最少 7 次移动（跨隙一次覆盖两格），
	// 加起点共 8 个路径点；少于它说明路径没有真正贯穿地图。
	if len(first.Waypoints) < 8 {
		t.Fatalf("路径点数 %d 少于跨隙曼哈顿下界 8", len(first.Waypoints))
	}

	// 失败同样可重放：四格宽断崖（跨隙只允许一格）三次都得到同一哨兵。
	blocked := func(x, y, z int32) (core.BlockID, bool) {
		if y == 63 && (x < 2 || x > 5) {
			return core.StoneID, true
		}
		return core.AirID, true
	}
	blockedGrid := mustPathGrid(t, func() (PathGrid, error) {
		return NewPathGrid(core.BlockPos{X: 0, Y: 62, Z: 0}, 8, 4, 8,
			pathTestTable, blocked, nil)
	})
	for attempt := 0; attempt < 3; attempt++ {
		_, err := FindPath(blockedGrid, start, goal)
		wantErrorIs(t, err, ErrPathUnreachable, ErrPathBudgetExceeded)
	}
}

// TestPathfindNeighborOrderLocked 锁定固定邻居展开序：方向序 -X、+X、-Z、+Z
// （白盒逐移动断言），方向内一步移动先于跨一格间隙；黑盒钻石平台验证等价
// 最短路的固定取舍（tie-break = (f, 固定展开序入队序)）。
func TestPathfindNeighborOrderLocked(t *testing.T) {
	// 3×3 平台中心格：四个同层平移全部成立且只有平移成立（跳/落/跨隙目标都
	// 越出网格），展开顺序必须是 -X、+X、-Z、+Z。
	plateau := mustPathGrid(t, func() (PathGrid, error) {
		return NewPathGridFromLayers(core.BlockPos{X: 0, Y: 63, Z: 0},
			pathTestTable, [][]string{
				{"###", "###", "###"},
				{"...", "...", "..."},
				{"...", "...", "..."},
			}, pathLegend, nil)
	})
	var flatOrder []PathCell
	plateau.expand(PathCell{X: 1, Y: 64, Z: 1}, func(target PathCell, cost int32) {
		if cost != pathCostFlat {
			t.Fatalf("平台中心出现非平移移动 %+v 代价 %d", target, cost)
		}
		flatOrder = append(flatOrder, target)
	})
	assertPathCells(t, flatOrder, []PathCell{
		{X: 0, Y: 64, Z: 1}, {X: 2, Y: 64, Z: 1},
		{X: 1, Y: 64, Z: 0}, {X: 1, Y: 64, Z: 2},
	})

	// 单方向 +X 上平移与跨隙可同时成立（跨隙中格不要求支撑）：先平移后跨隙。
	hopField := mustPathGrid(t, func() (PathGrid, error) {
		return NewPathGrid(core.BlockPos{X: 0, Y: 63, Z: 0}, 4, 3, 1,
			pathTestTable, func(x, y, z int32) (core.BlockID, bool) {
				if y == 63 {
					return core.StoneID, true
				}
				return core.AirID, true
			}, nil)
	})
	var moves []PathCell
	var costs []int32
	hopField.expand(PathCell{X: 0, Y: 64, Z: 0}, func(target PathCell, cost int32) {
		moves = append(moves, target)
		costs = append(costs, cost)
	})
	assertPathCells(t, moves, []PathCell{{X: 1, Y: 64, Z: 0}, {X: 2, Y: 64, Z: 0}})
	if len(costs) != 2 || costs[0] != pathCostFlat || costs[1] != pathCostGapJump {
		t.Fatalf("移动代价 = %v，want [%d %d]", costs, pathCostFlat, pathCostGapJump)
	}

	// 黑盒钻石平台：两条等价最短路必须固定选 X 轴先展开的那条，镜像同理。
	diamond := mustPathGrid(t, func() (PathGrid, error) {
		return NewPathGridFromLayers(core.BlockPos{X: 0, Y: 63, Z: 0},
			pathTestTable, [][]string{
				{"##", "##"},
				{"..", ".."},
				{"..", ".."},
			}, pathLegend, nil)
	})
	result, err := FindPath(diamond, PathCell{X: 0, Y: 64, Z: 0}, PathCell{X: 1, Y: 64, Z: 1})
	if err != nil {
		t.Fatalf("钻石平台寻路失败: %v", err)
	}
	assertPathCells(t, result.Waypoints, []PathCell{
		{X: 0, Y: 64, Z: 0}, {X: 1, Y: 64, Z: 0}, {X: 1, Y: 64, Z: 1},
	})
	mirror, err := FindPath(diamond, PathCell{X: 1, Y: 64, Z: 0}, PathCell{X: 0, Y: 64, Z: 1})
	if err != nil {
		t.Fatalf("钻石平台镜像寻路失败: %v", err)
	}
	assertPathCells(t, mirror.Waypoints, []PathCell{
		{X: 1, Y: 64, Z: 0}, {X: 0, Y: 64, Z: 0}, {X: 0, Y: 64, Z: 1},
	})
}

// TestPathfindFlatCorridorAndStartEqualsGoal 验证直线走廊的确定性路径与
// 起终点重合的单点路径。注意：平移与跨隙同价时固定取舍会得到隔格跳走的路径
// （重放一致且每步都是合法移动），这正是 tie-break 的可观察结果。
func TestPathfindFlatCorridorAndStartEqualsGoal(t *testing.T) {
	corridor := mustPathGrid(t, func() (PathGrid, error) {
		return NewPathGridFromLayers(core.BlockPos{X: 0, Y: 63, Z: 0},
			pathTestTable, [][]string{{"#####"}, {"....."}, {"....."}}, pathLegend, nil)
	})
	start, goal := PathCell{X: 0, Y: 64, Z: 0}, PathCell{X: 4, Y: 64, Z: 0}
	result, err := FindPath(corridor, start, goal)
	if err != nil {
		t.Fatalf("直走廊寻路失败: %v", err)
	}
	assertPathCells(t, result.Waypoints, []PathCell{
		{X: 0, Y: 64, Z: 0}, {X: 2, Y: 64, Z: 0}, {X: 4, Y: 64, Z: 0},
	})
	same, err := FindPath(corridor, start, start)
	if err != nil {
		t.Fatalf("起终点重合寻路失败: %v", err)
	}
	assertPathCells(t, same.Waypoints, []PathCell{start})
}

// TestPathfindGapCross 验证跨一格水平间隙：中格不要求支撑、落点必须有支撑、
// 只允许同层跨越一格；两格间隙与中格 head 被堵都必须不可达（绝不挖改方块）。
func TestPathfindGapCross(t *testing.T) {
	gapFloor := func(floored func(x int32) bool) func(x, y, z int32) (core.BlockID, bool) {
		return func(x, y, z int32) (core.BlockID, bool) {
			if y == 63 && floored(x) {
				return core.StoneID, true
			}
			return core.AirID, true
		}
	}
	// 一格间隙：x=1 列无地板，落点 x=2 有支撑——单次跨隙直达。
	oneGap := mustPathGrid(t, func() (PathGrid, error) {
		return NewPathGrid(core.BlockPos{X: 0, Y: 63, Z: 0}, 3, 3, 1,
			pathTestTable, gapFloor(func(x int32) bool { return x == 0 || x == 2 }), nil)
	})
	result, err := FindPath(oneGap, PathCell{X: 0, Y: 64, Z: 0}, PathCell{X: 2, Y: 64, Z: 0})
	if err != nil {
		t.Fatalf("跨一格间隙失败: %v", err)
	}
	assertPathCells(t, result.Waypoints, []PathCell{{X: 0, Y: 64, Z: 0}, {X: 2, Y: 64, Z: 0}})

	// 两格间隙超出模型（跨隙只允许一格）。
	twoGap := mustPathGrid(t, func() (PathGrid, error) {
		return NewPathGrid(core.BlockPos{X: 0, Y: 63, Z: 0}, 4, 3, 1,
			pathTestTable, gapFloor(func(x int32) bool { return x == 0 || x == 3 }), nil)
	})
	_, err = FindPath(twoGap, PathCell{X: 0, Y: 64, Z: 0}, PathCell{X: 3, Y: 64, Z: 0})
	wantErrorIs(t, err, ErrPathUnreachable, ErrPathBudgetExceeded)

	// 中格 head 被堵时不可跨越。
	blockedHead := mustPathGrid(t, func() (PathGrid, error) {
		return NewPathGrid(core.BlockPos{X: 0, Y: 63, Z: 0}, 3, 3, 1,
			pathTestTable, func(x, y, z int32) (core.BlockID, bool) {
				if (y == 63 && (x == 0 || x == 2)) || (x == 1 && y == 65) {
					return core.StoneID, true
				}
				return core.AirID, true
			}, nil)
	})
	_, err = FindPath(blockedHead, PathCell{X: 0, Y: 64, Z: 0}, PathCell{X: 2, Y: 64, Z: 0})
	wantErrorIs(t, err, ErrPathUnreachable, ErrPathBudgetExceeded)
}

// TestPathfindJumpUpOneAndFallOne 验证跳上一格（要求当前 head 上方一格可通过）
// 与下落一格（只允许一格落差）；净空被堵或落差两格都必须不可达。
func TestPathfindJumpUpOneAndFallOne(t *testing.T) {
	// 台阶：x=0 列站 y=64，x=1 列站 y=65；一次跳上一格直达。
	steps := mustPathGrid(t, func() (PathGrid, error) {
		return NewPathGrid(core.BlockPos{X: 0, Y: 63, Z: 0}, 2, 4, 1,
			pathTestTable, func(x, y, z int32) (core.BlockID, bool) {
				if (x == 0 && y == 63) || (x == 1 && y == 64) {
					return core.StoneID, true
				}
				return core.AirID, true
			}, nil)
	})
	result, err := FindPath(steps, PathCell{X: 0, Y: 64, Z: 0}, PathCell{X: 1, Y: 65, Z: 0})
	if err != nil {
		t.Fatalf("跳上一格失败: %v", err)
	}
	assertPathCells(t, result.Waypoints, []PathCell{{X: 0, Y: 64, Z: 0}, {X: 1, Y: 65, Z: 0}})

	// 跳跃净空 (0,66,0) 被堵：跳跃被禁止且无绕行——绝不修改方块开路。
	blockedClearance := mustPathGrid(t, func() (PathGrid, error) {
		return NewPathGrid(core.BlockPos{X: 0, Y: 63, Z: 0}, 2, 4, 1,
			pathTestTable, func(x, y, z int32) (core.BlockID, bool) {
				if (x == 0 && y == 63) || (x == 1 && y == 64) || (x == 0 && y == 66) {
					return core.StoneID, true
				}
				return core.AirID, true
			}, nil)
	})
	_, err = FindPath(blockedClearance, PathCell{X: 0, Y: 64, Z: 0}, PathCell{X: 1, Y: 65, Z: 0})
	wantErrorIs(t, err, ErrPathUnreachable, ErrPathBudgetExceeded)

	// 下落一格：高台 x=0（站 y=65）落向 x=1（站 y=64）。
	descent := mustPathGrid(t, func() (PathGrid, error) {
		return NewPathGrid(core.BlockPos{X: 0, Y: 63, Z: 0}, 2, 4, 1,
			pathTestTable, func(x, y, z int32) (core.BlockID, bool) {
				if (x == 0 && y == 64) || (x == 1 && y == 63) {
					return core.StoneID, true
				}
				return core.AirID, true
			}, nil)
	})
	fall, err := FindPath(descent, PathCell{X: 0, Y: 65, Z: 0}, PathCell{X: 1, Y: 64, Z: 0})
	if err != nil {
		t.Fatalf("下落一格失败: %v", err)
	}
	assertPathCells(t, fall.Waypoints, []PathCell{{X: 0, Y: 65, Z: 0}, {X: 1, Y: 64, Z: 0}})

	// 两格落差超出模型（下落只允许一格）。
	descentTwo := mustPathGrid(t, func() (PathGrid, error) {
		return NewPathGrid(core.BlockPos{X: 0, Y: 63, Z: 0}, 2, 4, 1,
			pathTestTable, func(x, y, z int32) (core.BlockID, bool) {
				if (x == 0 && y == 65) || (x == 1 && y == 63) {
					return core.StoneID, true
				}
				return core.AirID, true
			}, nil)
	})
	_, err = FindPath(descentTwo, PathCell{X: 0, Y: 66, Z: 0}, PathCell{X: 1, Y: 64, Z: 0})
	wantErrorIs(t, err, ErrPathUnreachable, ErrPathBudgetExceeded)
}

// TestPathfindBudgetBoundaryExact 是预算上限的突变敏感锚点：一格宽直走廊迫使
// 弹出顺序线性，长度 4097 的走廊目标恰是第 4097 个被考察节点——上限 4096 时
// 必须在考察它之前以预算耗尽终止；把 MaxPathNodes 改成 4097 会得到成功路径而
// RED。长度 4095 的走廊目标在第 4095 个节点被考察，预算内成功。
func TestPathfindBudgetBoundaryExact(t *testing.T) {
	if MaxPathNodes != 4096 {
		t.Fatalf("节点预算常量 = %d，want 4096", MaxPathNodes)
	}
	corridor := func(length int32) PathGrid {
		return mustPathGrid(t, func() (PathGrid, error) {
			return NewPathGrid(core.BlockPos{X: 0, Y: 63, Z: -1}, length, 3, 3,
				pathTestTable, func(x, y, z int32) (core.BlockID, bool) {
					if y == 63 && z == 0 {
						return core.StoneID, true
					}
					return core.AirID, true
				}, nil)
		})
	}
	_, err := FindPath(corridor(4097), PathCell{X: 0, Y: 64, Z: 0}, PathCell{X: 4096, Y: 64, Z: 0})
	wantErrorIs(t, err, ErrPathBudgetExceeded, ErrPathUnreachable)

	result, err := FindPath(corridor(4095), PathCell{X: 0, Y: 64, Z: 0}, PathCell{X: 4094, Y: 64, Z: 0})
	if err != nil {
		t.Fatalf("预算内走廊寻路失败: %v", err)
	}
	if first, last := result.Waypoints[0], result.Waypoints[len(result.Waypoints)-1]; first != (PathCell{X: 0, Y: 64, Z: 0}) || last != (PathCell{X: 4094, Y: 64, Z: 0}) {
		t.Fatalf("路径端点异常: 首 %+v 末 %+v", first, last)
	}
}

// TestPathfindBudgetHaltsUnbounded 验证预算机制本身：可达站立格远超预算且目标
// 被两格宽断带隔开时，搜索必须在预算处停止并以预算耗尽失败，绝不无界耗尽
// 整张地图。
func TestPathfindBudgetHaltsUnbounded(t *testing.T) {
	grid := mustPathGrid(t, func() (PathGrid, error) {
		return NewPathGrid(core.BlockPos{X: 0, Y: 62, Z: 0}, 95, 4, 95,
			pathTestTable, func(x, y, z int32) (core.BlockID, bool) {
				if y == 63 && x != 46 && x != 47 {
					return core.StoneID, true
				}
				return core.AirID, true
			}, nil)
	})
	_, err := FindPath(grid, PathCell{X: 0, Y: 64, Z: 0}, PathCell{X: 94, Y: 64, Z: 94})
	wantErrorIs(t, err, ErrPathBudgetExceeded, ErrPathUnreachable)
}

// TestPathfindUnreachable 验证窗口内无路：两格宽护城河围住的目标塔在预算内
// 耗尽 open 集，按不可达失败且与预算哨兵可判别；窗外与非法端点同样不可达。
func TestPathfindUnreachable(t *testing.T) {
	walled := mustPathGrid(t, func() (PathGrid, error) {
		return NewPathGridFromLayers(core.BlockPos{X: 0, Y: 63, Z: 0},
			pathTestTable, [][]string{
				{
					"#######",
					"#.....#",
					"#.....#",
					"#..#..#",
					"#.....#",
					"#.....#",
					"#######",
				},
				{".......", ".......", ".......", ".......", ".......", ".......", "......."},
				{".......", ".......", ".......", ".......", ".......", ".......", "......."},
			}, pathLegend, nil)
	})
	_, err := FindPath(walled, PathCell{X: 0, Y: 64, Z: 0}, PathCell{X: 3, Y: 64, Z: 3})
	wantErrorIs(t, err, ErrPathUnreachable, ErrPathBudgetExceeded)

	_, err = FindPath(walled, PathCell{X: 99, Y: 64, Z: 0}, PathCell{X: 3, Y: 64, Z: 3})
	wantErrorIs(t, err, ErrPathUnreachable, ErrPathBudgetExceeded)
	_, err = FindPath(walled, PathCell{X: 0, Y: 64, Z: 0}, PathCell{X: 1, Y: 64, Z: 1})
	wantErrorIs(t, err, ErrPathUnreachable, ErrPathBudgetExceeded)
}

// TestPathfindPolicyReplanCooldown 验证路径点重验与固定冷却：revision 全部一致
// 才可提交、任一失配或缺失即拒绝；失效后按固定 20 tick 冷却重算；方法为纯读
// 不改策略状态。
func TestPathfindPolicyReplanCooldown(t *testing.T) {
	if PathReplanCooldownTicks != 20 {
		t.Fatalf("重算冷却 = %d，want 20", PathReplanCooldownTicks)
	}
	result := PathResult{
		Waypoints: []PathCell{{X: 1, Y: 64, Z: 0}, {X: 2, Y: 64, Z: 0}},
		Revisions: []ChunkRevision{
			{Chunk: core.ChunkPos{X: 0, Z: 0}, Revision: 5},
			{Chunk: core.ChunkPos{X: 1, Z: 0}, Revision: 9},
		},
	}
	current := []ChunkRevision{
		{Chunk: core.ChunkPos{X: 0, Z: 0}, Revision: 5},
		{Chunk: core.ChunkPos{X: 1, Z: 0}, Revision: 9},
		{Chunk: core.ChunkPos{X: 2, Z: 0}, Revision: 100}, // 超集不影响：路径只关心自己的区块
	}
	var policy PathPolicy
	if !policy.ShouldUse(result, 0, current) || !policy.ShouldUse(result, 1, current) {
		t.Fatal("revision 一致时路径点应可用")
	}
	if policy.ShouldUse(result, 2, current) || policy.ShouldUse(result, -1, current) {
		t.Fatal("路径点索引越界应拒绝")
	}
	mutated := append([]ChunkRevision(nil), current...)
	mutated[1].Revision = 10
	if policy.ShouldUse(result, 1, mutated) {
		t.Fatal("revision 失配时路径点应拒绝")
	}
	missing := current[:1]
	if policy.ShouldUse(result, 1, missing) {
		t.Fatal("当前状态缺失路径区块时路径点应拒绝")
	}
	if got := policy.ReplanAfter(100); got != 120 {
		t.Fatalf("失效后重算 tick = %d，want 120", got)
	}
	// ShouldUse 是纯读：连续判定不推进失败计数。
	if policy.RecordFailure() {
		t.Fatal("纯读判定后不应立即达到终止阈值")
	}
}

// TestPathfindPolicyThreeStrikesTerminate 验证连续失败终止：第三次失败必须令
// 任务终止且此后保持终止；成功使用路径点清零计数，重新累计。
func TestPathfindPolicyThreeStrikesTerminate(t *testing.T) {
	if MaxConsecutiveReplans != 3 {
		t.Fatalf("连续重算上限 = %d，want 3", MaxConsecutiveReplans)
	}
	var policy PathPolicy
	if policy.RecordFailure() || policy.RecordFailure() {
		t.Fatal("前两次失败不应终止")
	}
	if !policy.RecordFailure() {
		t.Fatal("第三次失败必须终止")
	}
	if !policy.RecordFailure() {
		t.Fatal("终止后继续失败必须保持终止")
	}
	policy.RecordSuccess()
	if policy.RecordFailure() {
		t.Fatal("成功清零后重新计数，第一次失败不应终止")
	}
}

// BenchmarkPathfind 在标准 33×33×9 窗口上执行一次典型寻路（约 30 格曼哈顿
// 距离、稀疏立柱绕行）。数值只记录，不构成性能门禁。
func BenchmarkPathfind(b *testing.B) {
	window := PathWindow{Center: PathCell{X: 16, Y: 64, Z: 16}}
	sizeX, sizeY, sizeZ := window.Size()
	originCell := window.Origin()
	origin := core.BlockPos{X: originCell.X, Y: originCell.Y, Z: originCell.Z}
	grid, err := NewPathGrid(origin, sizeX, sizeY, sizeZ, pathTestTable,
		func(x, y, z int32) (core.BlockID, bool) {
			localY := y - origin.Y
			if localY == 2 {
				return core.StoneID, true
			}
			if (localY == 3 || localY == 4) && x%6 == 3 && z%5 == 2 {
				return core.StoneID, true
			}
			return core.AirID, true
		},
		[]ChunkRevision{
			{Chunk: core.ChunkPos{X: 0, Z: 0}, Revision: 1},
			{Chunk: core.ChunkPos{X: 1, Z: 1}, Revision: 4},
		})
	if err != nil {
		b.Fatalf("构造基准网格失败: %v", err)
	}
	start, goal := PathCell{X: 2, Y: 63, Z: 2}, PathCell{X: 30, Y: 63, Z: 30}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := FindPath(grid, start, goal); err != nil {
			b.Fatalf("基准寻路失败: %v", err)
		}
	}
}
