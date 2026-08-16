// 本文件实现 go_to 的确定性有界网格寻路。归属裁决记录在变更 design.md：
// 寻路留在 Go（整数运算 + 固定邻居展开序保证可重放；消费的体素数据在 Go 侧，
// 迁 Rust 反而制造 FFI 数据往返）。全部函数为纯计算：输入是不可变网格快照，
// 输出是值，没有共享状态、goroutine 或 I/O——"只在 worker goroutine 执行"
// 由纯函数性质保证，由 Task 6 的编排接线。
package companion

import (
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/channing771/mornlea/internal/core"
)

// 寻路窗口几何：以伙伴为中心水平 ±16（含）、垂直 ±4（含）。水平半径覆盖
// Planner 观察快照的同级范围，垂直半径更小——寻路只关心站立面附近的地形，
// 放大垂直窗口只会增加无效节点预算消耗。
const (
	// PathWindowHorizontalRadius 是寻路窗口的水平半径（含），单位格。
	PathWindowHorizontalRadius = 16
	// PathWindowVerticalRadius 是寻路窗口的垂直半径（含），单位格。
	PathWindowVerticalRadius = 4
)

// MaxPathNodes 是单次寻路最多考察（弹出并展开）的节点数。上限来自 M5 设计
// §9.2 的硬门禁：预算耗尽返回失败而不是无界消耗内存或时间。测试用 4097 长
// 直走廊锁定精确边界——目标恰是第 4097 个被考察节点，本上限必须在考察它
// 之前终止。
const MaxPathNodes = 4096

// maxPathGridCells 是网格快照的总量上限。生产窗口（33×9×33）只占用 9801 格；
// 上限取 2^17 是为了容纳预算边界测试使用的 4097 长走廊（36,873 格），同时
// 拒绝误把整张地图拷进内存的构造——拷贝世界的成本应当显式失败而不是悄悄
// 变成一次巨型分配。
const maxPathGridCells = 1 << 17

// 四类整数移动的代价。平移与下落一格同价（都是普通一步）；跳上一格与跨一
// 格间隙更贵（2）：前者要蓄力，后者一次性跨过两格距离。注意跨隙代价 = 2×
// 平移代价是刻意设计——它让"隔格跳走"与"逐格走"在等距直线上 g 值打平，
// 由固定展开序决定取舍，这正是重放一致性的可观察结果（见走廊测试注释）。
const (
	pathCostFlat    int32 = 1
	pathCostJumpUp  int32 = 2
	pathCostFallOne int32 = 1
	pathCostGapJump int32 = 2
)

// ErrPathUnreachable 表示窗口内不存在可行路径（含端点非法/出窗/非站立格、
// open 集耗尽）。ErrPathBudgetExceeded 表示节点预算耗尽。二者互斥可判别，
// 调用方（Task Runner）对它们采取相同的"冷却重算再试"策略。
var (
	ErrPathUnreachable    = errors.New("companion: 寻路不可达")
	ErrPathBudgetExceeded = errors.New("companion: 寻路节点预算耗尽")
)

// PathCell 是寻路网格中的一个格子坐标。字段与 core.BlockPos 同构但语义不同：
// 它表示"actor 站立占用的 feet 格"，不与任何世界数据结构共享内存。
type PathCell struct {
	X int32
	Y int32
	Z int32
}

// PathWindow 描述以 Center 为中心的寻路窗口：水平 ±PathWindowHorizontalRadius、
// 垂直 ±PathWindowVerticalRadius（均含端点）。生产侧在 tick 边界用它裁出
// 不可变网格快照。
type PathWindow struct {
	Center PathCell
}

// Contains 报告 cell 是否落在窗口内（各轴 |offset| ≤ 对应半径）。
func (w PathWindow) Contains(cell PathCell) bool {
	dx := cell.X - w.Center.X
	dy := cell.Y - w.Center.Y
	dz := cell.Z - w.Center.Z
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	if dz < 0 {
		dz = -dz
	}
	return dx <= PathWindowHorizontalRadius && dy <= PathWindowVerticalRadius && dz <= PathWindowHorizontalRadius
}

// Origin 返回窗口最小角的格子坐标。
func (w PathWindow) Origin() PathCell {
	return PathCell{
		X: w.Center.X - PathWindowHorizontalRadius,
		Y: w.Center.Y - PathWindowVerticalRadius,
		Z: w.Center.Z - PathWindowHorizontalRadius,
	}
}

// Size 返回窗口三轴边长（半径含端点 → 2r+1）。
func (w PathWindow) Size() (int32, int32, int32) {
	horizontal := 2*PathWindowHorizontalRadius + 1
	vertical := 2*PathWindowVerticalRadius + 1
	return int32(horizontal), int32(vertical), int32(horizontal)
}

// PathBlockTable 记录哪些方块编号对寻路可通过。生产构造由 Task 6 从既有材料
// 表导出（玻璃/树叶等可通过、实体方块阻挡）；本包不 import world，阻挡语义
// 由调用方注入，测试自备最小表。
type PathBlockTable struct {
	passableIDs map[core.BlockID]bool
}

// NewPathBlockTable 用"编号 → 是否可通过"映射构造阻挡表；nil 映射表示一切
// 阻挡（安全缺省）。构造后映射被复制，调用方后续修改不影响表。
func NewPathBlockTable(passable map[core.BlockID]bool) PathBlockTable {
	copied := make(map[core.BlockID]bool, len(passable))
	for id, ok := range passable {
		copied[id] = ok
	}
	return PathBlockTable{passableIDs: copied}
}

// passable 报告编号是否可通过；未知编号视为阻挡（缺省安全）。
func (t PathBlockTable) passable(id core.BlockID) bool {
	return t.passableIDs[id]
}

// PathGrid 是寻路消费的不可变方块快照：构造时一次性拷贝窗口内全部方块，
// 之后 FindPath/expand 只读内部切片，绝不回调世界读取。revisions 是窗口覆盖
// 区块的（归一化后）内容版本，随结果透传给重验方。
type PathGrid struct {
	origin    core.BlockPos
	sizeX     int32
	sizeY     int32
	sizeZ     int32
	blocks    []core.BlockID
	revisions []ChunkRevision
	table     PathBlockTable
}

// NewPathGrid 构造网格快照：按 origin 与三轴尺寸一次性调用 fetch 读取每个
// 格子（每格恰好一次，不越界探测），拷贝进内部切片后不再触碰世界。全部
// 校验（尺寸、revision 归一化）在读取任何方块之前完成，失败路径零世界读。
//
// revisions 会按 (X,Z) 排序、去重并复制为独立副本；同一区块出现不同
// revision 视为调用方数据竞争，直接报错。
func NewPathGrid(
	origin core.BlockPos,
	sizeX, sizeY, sizeZ int32,
	table PathBlockTable,
	fetch func(x, y, z int32) (core.BlockID, bool),
	revisions []ChunkRevision,
) (PathGrid, error) {
	if sizeX <= 0 || sizeY <= 0 || sizeZ <= 0 {
		return PathGrid{}, fmt.Errorf("companion: 网格尺寸非正 %d×%d×%d", sizeX, sizeY, sizeZ)
	}
	total := int64(sizeX) * int64(sizeY) * int64(sizeZ)
	if total > maxPathGridCells {
		return PathGrid{}, fmt.Errorf("companion: 网格总量 %d 超上限 %d", total, maxPathGridCells)
	}
	if fetch == nil {
		return PathGrid{}, errors.New("companion: 缺少方块读取回调")
	}
	normalized, err := normalizeChunkRevisions(revisions)
	if err != nil {
		return PathGrid{}, err
	}
	grid := PathGrid{
		origin:    origin,
		sizeX:     sizeX,
		sizeY:     sizeY,
		sizeZ:     sizeZ,
		blocks:    make([]core.BlockID, total),
		revisions: normalized,
		table:     table,
	}
	index := 0
	for x := origin.X; x < origin.X+sizeX; x++ {
		for z := origin.Z; z < origin.Z+sizeZ; z++ {
			for y := origin.Y; y < origin.Y+sizeY; y++ {
				id, ok := fetch(x, y, z)
				if !ok {
					return PathGrid{}, fmt.Errorf("companion: 读取方块 (%d,%d,%d) 失败", x, y, z)
				}
				grid.blocks[index] = id
				index++
			}
		}
	}
	return grid, nil
}

// NewPathGridFromLayers 从 ASCII 层字面量构造网格（测试夹具主入口）：
// layers[0] 是最底层（origin.Y），每层是一个 string 行的切片，行内字符按
// legend 映射为方块编号；所有层必须同形，未知字符直接报错。
func NewPathGridFromLayers(
	origin core.BlockPos,
	table PathBlockTable,
	layers [][]string,
	legend map[rune]core.BlockID,
	revisions []ChunkRevision,
) (PathGrid, error) {
	if len(layers) == 0 || len(layers[0]) == 0 {
		return PathGrid{}, errors.New("companion: 空层字面量")
	}
	rows := int32(len(layers[0]))
	columns := int32(len(layers[0][0]))
	for _, layer := range layers {
		if int32(len(layer)) != rows {
			return PathGrid{}, errors.New("companion: 层字面量行数不一致")
		}
		for _, row := range layer {
			if int32(len(row)) != columns {
				return PathGrid{}, errors.New("companion: 层字面量行宽不一致")
			}
		}
	}
	return NewPathGrid(origin, columns, int32(len(layers)), rows, table,
		func(x, y, z int32) (core.BlockID, bool) {
			layer := layers[y-origin.Y]
			row := layer[z-origin.Z]
			id, ok := legend[rune(row[x-origin.X])]
			return id, ok
		}, revisions)
}

// normalizeChunkRevisions 归一化 revision 列表：按 (X,Z) 排序、同块同版本去重、
// 同块不同版本报冲突、数量超 MaxPlanChunkRevisions 报错；返回独立副本。
func normalizeChunkRevisions(revisions []ChunkRevision) ([]ChunkRevision, error) {
	if len(revisions) > MaxPlanChunkRevisions {
		return nil, fmt.Errorf("companion: revision 数 %d 超上限 %d", len(revisions), MaxPlanChunkRevisions)
	}
	copied := slices.Clone(revisions)
	sort.Slice(copied, func(i, j int) bool {
		if copied[i].Chunk.X != copied[j].Chunk.X {
			return copied[i].Chunk.X < copied[j].Chunk.X
		}
		return copied[i].Chunk.Z < copied[j].Chunk.Z
	})
	unique := copied[:0]
	for index, revision := range copied {
		if index > 0 && unique[len(unique)-1].Chunk == revision.Chunk {
			if unique[len(unique)-1].Revision != revision.Revision {
				return nil, fmt.Errorf("companion: 区块 %+v revision 冲突", revision.Chunk)
			}
			continue
		}
		unique = append(unique, revision)
	}
	return unique, nil
}

// blockAt 从拷贝切片读取方块；越界返回 ok=false（窗口外视为未知，一律不可
// 通过——宁可少找路也不猜测未见过的地形）。
func (g PathGrid) blockAt(x, y, z int32) (core.BlockID, bool) {
	localX := x - g.origin.X
	localY := y - g.origin.Y
	localZ := z - g.origin.Z
	if localX < 0 || localX >= g.sizeX || localY < 0 || localY >= g.sizeY || localZ < 0 || localZ >= g.sizeZ {
		return 0, false
	}
	index := (int(localX)*int(g.sizeZ)+int(localZ))*int(g.sizeY) + int(localY)
	return g.blocks[index], true
}

// passable 报告格子是否可通过：必须在网格内且阻挡表放行。
func (g PathGrid) passable(x, y, z int32) bool {
	id, ok := g.blockAt(x, y, z)
	return ok && g.table.passable(id)
}

// standing 判定站立格：feet 与 head 可通过、正下方支撑格在网格内且阻挡。
// 支撑格越界（窗口底缘之下）不算站立——支撑是"看见的实体方块"，不能靠
// 未知地形假设。
func (g PathGrid) standing(cell PathCell) bool {
	if !g.passable(cell.X, cell.Y, cell.Z) || !g.passable(cell.X, cell.Y+1, cell.Z) {
		return false
	}
	support, ok := g.blockAt(cell.X, cell.Y-1, cell.Z)
	return ok && !g.table.passable(support)
}

// expand 按固定顺序展开 cell 的合法移动。方向序固定为 -X、+X、-Z、+Z；方向内
// 先一步移动（平移、跳上一格、下落一格）后跨一格间隙。这个顺序是重放一致性
// 的一部分：A* 的并列取舍由它决定，测试用白盒断言锁定，改动它就是破坏存档
// 可重放语义的破坏性变更。
func (g PathGrid) expand(cell PathCell, emit func(target PathCell, cost int32)) {
	// 方向序：-X、+X、-Z、+Z（与规格承诺逐字一致）。
	directions := [4][2]int32{{-1, 0}, {1, 0}, {0, -1}, {0, 1}}
	for _, direction := range directions {
		dx, dz := direction[0], direction[1]
		// 平移：目标站立即可。
		flat := PathCell{X: cell.X + dx, Y: cell.Y, Z: cell.Z + dz}
		if g.standing(flat) {
			emit(flat, pathCostFlat)
		}
		// 跳上一格：目标站立，且当前 head 上方一格（起跳净空）可通过——
		// 净空被堵时禁止起跳，绝不挖改方块开路。
		jump := PathCell{X: cell.X + dx, Y: cell.Y + 1, Z: cell.Z + dz}
		if g.standing(jump) && g.passable(cell.X, cell.Y+2, cell.Z) {
			emit(jump, pathCostJumpUp)
		}
		// 下落一格：只允许一格落差。
		drop := PathCell{X: cell.X + dx, Y: cell.Y - 1, Z: cell.Z + dz}
		if g.standing(drop) {
			emit(drop, pathCostFallOne)
		}
		// 跨一格水平间隙：中间格的 feet 与 head 都可通过（是"洞"不是"墙"），
		// 落点必须站立；只允许同层跨越一格。
		gap := PathCell{X: cell.X + 2*dx, Y: cell.Y, Z: cell.Z + 2*dz}
		if g.standing(gap) &&
			g.passable(cell.X+dx, cell.Y, cell.Z+dz) &&
			g.passable(cell.X+dx, cell.Y+1, cell.Z+dz) {
			emit(gap, pathCostGapJump)
		}
	}
}

// pathNode 是 A* 的搜索节点：g 是已付代价，f = g + 曼哈顿启发（int32），ins
// 是入队序号（并列 f 的最终裁决），parent 是回溯路径用的节点下标。
type pathNode struct {
	cell   PathCell
	g      int32
	f      int32
	ins    int32
	parent int32
	closed bool
}

// PathResult 是一次成功寻路的产物。Waypoints 从起点到终点含两端；Revisions
// 是网格 revision 的独立副本——调用方改结果不影响网格与后续寻路。
type PathResult struct {
	Waypoints []PathCell
	Revisions []ChunkRevision
}

// FindPath 在不可变网格上执行整数 A*：相同快照与端点必然得到相同路径或相同
// 失败哨兵。端点必须是站立格（含在网格内），否则按不可达失败。节点考察
// （弹出）数超过 MaxPathNodes 时以预算耗尽失败。
//
// 启发式用 4 向曼哈顿距离（忽略 Y）：平移/跨隙每单位水平距离的代价恰为 1，
// 垂直移动不减少启发，因此可采纳且一致——已弹出节点无需重开。并列 f 按
// 入队序弹出，入队序由固定展开序决定，两层固定性叠加保证重放一致。
func FindPath(grid PathGrid, start, goal PathCell) (PathResult, error) {
	if !grid.standing(start) || !grid.standing(goal) {
		return PathResult{}, fmt.Errorf("%w: 端点不是合法站立格", ErrPathUnreachable)
	}
	if start == goal {
		return PathResult{
			Waypoints: []PathCell{start},
			Revisions: slices.Clone(grid.revisions),
		}, nil
	}

	heuristic := func(cell PathCell) int32 {
		dx := cell.X - goal.X
		dz := cell.Z - goal.Z
		if dx < 0 {
			dx = -dx
		}
		if dz < 0 {
			dz = -dz
		}
		return dx + dz
	}

	var nodes []pathNode
	indexOf := make(map[PathCell]int32)
	var open []int32
	expansions := 0

	// push 登记一个候选：新节点入队；已知节点只在 g 严格更优时改写（等价 g
	// 保留首个父节点——这正是固定取舍的来源，改写它会破坏走廊/钻石平台的
	// 锁定结果）。
	push := func(cell PathCell, g int32, parent int32) {
		if index, exists := indexOf[cell]; exists {
			if !nodes[index].closed && g < nodes[index].g {
				nodes[index].g = g
				nodes[index].f = g + heuristic(cell)
				nodes[index].parent = parent
			}
			return
		}
		indexOf[cell] = int32(len(nodes))
		nodes = append(nodes, pathNode{
			cell:   cell,
			g:      g,
			f:      g + heuristic(cell),
			ins:    int32(len(nodes)),
			parent: parent,
		})
		open = append(open, int32(len(nodes))-1)
	}
	push(start, 0, -1)

	for {
		// 弹出 (f, 入队序) 最小的未关闭节点。open 用线性扫描：窗口内节点
		// 量级（数百到数千）下常数最小且完全确定，堆反而引入比较器稳定性
		// 这类额外确定性负担。
		best := int32(-1)
		for _, index := range open {
			if nodes[index].closed {
				continue
			}
			if best == -1 || nodes[index].f < nodes[best].f ||
				(nodes[index].f == nodes[best].f && nodes[index].ins < nodes[best].ins) {
				best = index
			}
		}
		if best == -1 {
			return PathResult{}, fmt.Errorf("%w: 窗口内无可行路径", ErrPathUnreachable)
		}
		if expansions == MaxPathNodes {
			// 预算检查在弹出第 MaxPathNodes+1 个节点之前：4097 长走廊的
			// 目标恰是第 4097 个节点，必须在此终止而不是考察它。
			return PathResult{}, fmt.Errorf("%w: 考察数达 %d", ErrPathBudgetExceeded, MaxPathNodes)
		}
		nodes[best].closed = true
		expansions++
		if nodes[best].cell == goal {
			var reversed []PathCell
			for index := best; index != -1; index = nodes[index].parent {
				reversed = append(reversed, nodes[index].cell)
			}
			waypoints := make([]PathCell, len(reversed))
			for i, cell := range reversed {
				waypoints[len(reversed)-1-i] = cell
			}
			return PathResult{
				Waypoints: waypoints,
				Revisions: slices.Clone(grid.revisions),
			}, nil
		}
		nodeG := nodes[best].g
		grid.expand(nodes[best].cell, func(target PathCell, cost int32) {
			push(target, nodeG+cost, best)
		})
	}
}
