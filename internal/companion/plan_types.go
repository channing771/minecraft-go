// 本文件定义 Planner 的输入快照与输出计划值类型。快照由服务端在权威 tick
// 边界构造（属后续任务），本文件只负责类型、字段边界与确定性排序；计划类型
// 的严格解码路径在 planner.go。
package companion

import (
	"bytes"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/channing771/mornlea/internal/core"
)

// 规划输入与计划输出的有界常量。全部上限都在构造/解码边界一次性强制，保证
// 快照与计划的内存占用与序列化长度不随世界规模无界增长。
const (
	// MaxPlanCommandBytes 是快照内玩家原始指令的 UTF-8 字节上限，与网络聊天
	// 指令的既有输入上限对齐。
	MaxPlanCommandBytes = 1024
	// MaxPlanSummaryBytes 是计划 summary 的 UTF-8 字节上限；summary 是模型
	// 自由文本，必须在解码边界截断其最大长度。
	MaxPlanSummaryBytes = 512
	// MaxPlanTaskStatusBytes 是伙伴当前任务状态摘要的 UTF-8 字节上限。
	MaxPlanTaskStatusBytes = 96
	// MaxPlanExposedBlocks 是环境摘要可携带的暴露/特殊方块上限（spec：最多
	// 256 个按坐标排序的方块条目）。
	MaxPlanExposedBlocks = 256
	// planEnvRadiusBlocks 是环境摘要的水平半径（spec：伙伴周围水平 16 格）。
	planEnvRadiusBlocks = 16
	// planEnvVerticalBlocks 是环境摘要的垂直半径（spec：伙伴周围垂直 8 格）。
	// 它同时是 mine 步骤观察窗口判定的垂直 ±8 数值界（见
	// planInObservationWindow）。
	planEnvVerticalBlocks = 8
	// MaxPlanOnlinePlayers 是快照在线玩家集合的上限，与服务器八名玩家的会话
	// 上限对齐；校验拒绝超界构造，BoundOnlinePlayers 的截断只是防御性内存界。
	MaxPlanOnlinePlayers = 8
	// MaxPlanHeightSamples 是高度样本条数上限：水平半径 16 格覆盖
	// (2*16+1)^2 = 1089 列。垂直 8 格范围由方块 Y 坐标自身表达，不设独立字段。
	MaxPlanHeightSamples = (2*planEnvRadiusBlocks + 1) * (2*planEnvRadiusBlocks + 1)
	// MaxPlanChunkRevisions 是快照可携带的区块 revision 上限，对应伙伴 3×3
	// 区块兴趣范围（水平 16 格半径最多横跨 3×3 区块）。
	MaxPlanChunkRevisions = 9
)

// PlanBlock 是环境摘要中的一个暴露或特殊方块条目。
type PlanBlock struct {
	// Pos 是方块的世界坐标；Block 不是空气（空气列由 PlanHeight 表达）。
	Pos   core.BlockPos `json:"pos"`
	Block core.BlockID  `json:"block"`
}

// PlanHeight 是环境摘要中的一列地表高度样本。Height 为 core.MinY-1 表示空列
// （该列在世界边界内没有任何方块），其余取值必须是真实方块 Y 坐标。
type PlanHeight struct {
	X      int32 `json:"x"`
	Z      int32 `json:"z"`
	Height int32 `json:"height"`
}

// ChunkRevision 记录快照引用的一个区块的内容 revision，供寻路结果失效判定。
type ChunkRevision struct {
	Chunk    core.ChunkPos `json:"chunk"`
	Revision uint64        `json:"revision"`
}

// PlanPlayer 是发令玩家在快照时刻的稳定事实。
type PlanPlayer struct {
	ID core.PlayerID `json:"id"`
	// Position、Yaw、Pitch 是玩家世界坐标与朝向，全部分量必须有限。
	Position [3]float32 `json:"position"`
	Yaw      float32    `json:"yaw"`
	Pitch    float32    `json:"pitch"`
	// LookHit 是玩家视线命中的方块；HasLookHit 为 false 时 LookHit 无意义。
	LookHit    core.BlockPos `json:"lookHit"`
	HasLookHit bool          `json:"hasLookHit"`
}

// PlanCompanion 是被指挥伙伴在快照时刻的稳定事实。
type PlanCompanion struct {
	ID ID `json:"id"`
	// Position、Yaw、Pitch 是伙伴世界坐标与朝向，全部分量必须有限。
	Position [3]float32 `json:"position"`
	Yaw      float32    `json:"yaw"`
	Pitch    float32    `json:"pitch"`
	// Inventory 是伙伴的 36 格完整权威物品状态。
	Inventory core.Inventory `json:"inventory"`
	// TaskStatus 是当前任务状态摘要（例如「空闲」），不含模型自由文本。
	TaskStatus string `json:"taskStatus"`
}

// PlanSnapshot 是一次规划的不可变观察快照值类型。
//
// 快照在权威 tick 边界一次性构造，发送给 worker 后视为不可变；全部字段有界
// （见各 Max* 常量），且绝不包含 API key、其他玩家聊天或存档路径——key 只在
// PlannerClient 内部使用，聊天快照之外的内容不进入规划输入。json tag 供
// PlannerClient 做确定性序列化，字段顺序由结构体声明顺序固定。
type PlanSnapshot struct {
	// Command 是玩家的原始指令文本（不含 @伙伴名 寻址前缀）。
	Command string `json:"command"`
	// Issuer 是发令玩家事实。
	Issuer PlanPlayer `json:"issuer"`
	// Companion 是被指挥伙伴事实。
	Companion PlanCompanion `json:"companion"`
	// ExposedBlocks 是伙伴周围按 (X,Y,Z) 严格升序的暴露/特殊方块，至多
	// MaxPlanExposedBlocks 条，由 BoundExposedBlocks 生成。
	ExposedBlocks []PlanBlock `json:"exposedBlocks"`
	// Heights 是按 (X,Z) 严格升序的地表高度样本，至多 MaxPlanHeightSamples 条。
	Heights []PlanHeight `json:"heights"`
	// ChunkRevisions 是按 (X,Z) 严格升序的相关区块 revision，至多
	// MaxPlanChunkRevisions 条。
	ChunkRevisions []ChunkRevision `json:"chunkRevisions"`
	// OnlinePlayers 是快照时刻全部在线玩家的稳定事实，按 ID 严格升序且至多
	// MaxPlanOnlinePlayers 名（M5C），供 follow 步骤的目标校验。集合在构造时
	// 一次性拷贝，worker 读取期间不随会话变化。
	OnlinePlayers []PlanPlayer `json:"onlinePlayers"`
	// WorldTimeTicks 是快照时刻的权威世界时间（0..23999 昼夜 tick）。
	WorldTimeTicks uint64 `json:"worldTimeTicks"`
}

// Validate 校验快照的全部不变量：指令与任务状态摘要的编码和长度、身份有效
// 性、浮点有限性、四类列表（环境方块/高度样本/区块 revision/在线玩家集合）
// 的数量/顺序/去重/取值范围与背包规范性。
//
// 非法快照是 server 侧构造缺陷而不是模型失败，因此这里返回的错误不携带
// Planner 哨兵类别；PlannerClient.Plan 在发起任何请求前调用本方法。
func (s PlanSnapshot) Validate() error {
	if err := validatePlanText("快照指令", s.Command, MaxPlanCommandBytes, true); err != nil {
		return err
	}
	if !s.Issuer.ID.Valid() {
		return fmt.Errorf("companion: 快照发令玩家 ID 无效")
	}
	if !finite32(s.Issuer.Position[0], s.Issuer.Position[1], s.Issuer.Position[2],
		s.Issuer.Yaw, s.Issuer.Pitch) {
		return fmt.Errorf("companion: 快照发令玩家位置或朝向不是有限值")
	}
	if s.Issuer.HasLookHit && !validPlanBlockY(s.Issuer.LookHit.Y) {
		return fmt.Errorf("companion: 快照发令玩家视线命中方块 Y=%d 越界", s.Issuer.LookHit.Y)
	}
	if !s.Companion.ID.Valid() {
		return fmt.Errorf("companion: 快照伙伴 ID 无效")
	}
	if !finite32(s.Companion.Position[0], s.Companion.Position[1], s.Companion.Position[2],
		s.Companion.Yaw, s.Companion.Pitch) {
		return fmt.Errorf("companion: 快照伙伴位置或朝向不是有限值")
	}
	if !s.Companion.Inventory.Valid() {
		return fmt.Errorf("companion: 快照伙伴背包非法")
	}
	if err := validatePlanText("快照任务状态摘要", s.Companion.TaskStatus, MaxPlanTaskStatusBytes, false); err != nil {
		return err
	}
	if len(s.ExposedBlocks) > MaxPlanExposedBlocks {
		return fmt.Errorf("companion: 快照环境方块数 %d 超过上限 %d",
			len(s.ExposedBlocks), MaxPlanExposedBlocks)
	}
	for index, block := range s.ExposedBlocks {
		if block.Block == core.AirID || !core.RegisteredBlock(block.Block) {
			return fmt.Errorf("companion: 快照环境方块[%d] 编号 %d 非法（空气或未注册）", index, block.Block)
		}
		if !validPlanBlockY(block.Pos.Y) {
			return fmt.Errorf("companion: 快照环境方块[%d] Y=%d 越界", index, block.Pos.Y)
		}
		if index > 0 && !planBlockAfter(block.Pos, s.ExposedBlocks[index-1].Pos) {
			return fmt.Errorf("companion: 快照环境方块[%d] 未按 (X,Y,Z) 严格升序", index)
		}
	}
	if len(s.Heights) > MaxPlanHeightSamples {
		return fmt.Errorf("companion: 快照高度样本数 %d 超过上限 %d",
			len(s.Heights), MaxPlanHeightSamples)
	}
	for index, height := range s.Heights {
		// core.MinY-1 是空列哨兵，其余取值必须是 [MinY, MaxY) 内的真实方块 Y。
		if height.Height != core.MinY-1 && !validPlanBlockY(height.Height) {
			return fmt.Errorf("companion: 快照高度样本[%d] Height=%d 越界", index, height.Height)
		}
		if index > 0 {
			previous := s.Heights[index-1]
			if (previous.X > height.X) || (previous.X == height.X && previous.Z >= height.Z) {
				return fmt.Errorf("companion: 快照高度样本[%d] 未按 (X,Z) 严格升序", index)
			}
		}
	}
	if len(s.ChunkRevisions) > MaxPlanChunkRevisions {
		return fmt.Errorf("companion: 快照区块 revision 数 %d 超过上限 %d",
			len(s.ChunkRevisions), MaxPlanChunkRevisions)
	}
	for index, revision := range s.ChunkRevisions {
		if index > 0 {
			previous := s.ChunkRevisions[index-1]
			if (previous.Chunk.X > revision.Chunk.X) ||
				(previous.Chunk.X == revision.Chunk.X && previous.Chunk.Z >= revision.Chunk.Z) {
				return fmt.Errorf("companion: 快照区块 revision[%d] 未按 (X,Z) 严格升序", index)
			}
		}
	}
	if len(s.OnlinePlayers) > MaxPlanOnlinePlayers {
		return fmt.Errorf("companion: 快照在线玩家数 %d 超过上限 %d",
			len(s.OnlinePlayers), MaxPlanOnlinePlayers)
	}
	for index, player := range s.OnlinePlayers {
		// 在线玩家条目复用 PlanPlayer 值类型：全部字段的不变量与发令玩家一致，
		// 额外要求按 ID 严格升序（同一玩家只在集合出现一次）。
		if !player.ID.Valid() {
			return fmt.Errorf("companion: 快照在线玩家[%d] ID 无效", index)
		}
		if !finite32(player.Position[0], player.Position[1], player.Position[2],
			player.Yaw, player.Pitch) {
			return fmt.Errorf("companion: 快照在线玩家[%d] 位置或朝向不是有限值", index)
		}
		if player.HasLookHit && !validPlanBlockY(player.LookHit.Y) {
			return fmt.Errorf("companion: 快照在线玩家[%d] 视线命中方块 Y=%d 越界",
				index, player.LookHit.Y)
		}
		if index > 0 && bytes.Compare(s.OnlinePlayers[index-1].ID[:], player.ID[:]) >= 0 {
			return fmt.Errorf("companion: 快照在线玩家[%d] 未按 ID 严格升序或重复", index)
		}
	}
	return nil
}

// BoundOnlinePlayers 把在线玩家列表归一为快照可携带形式：按 ID 严格升序排序、
// ID 去重，并保留前 MaxPlanOnlinePlayers 名。排序与截断都是确定性的，同一集合
// 以任意输入顺序进入得到完全相同的结果；输入切片不被改动，返回值是独立副本。
//
// 服务器会话上限与快照上限同为八名，截断只作为防御性内存界（保证构造不随
// 玩家数无界增长），在正常生产路径永远触发不到，绝不承担丢弃真实在线玩家的
// 语义——在线玩家的完整性由会话上限保证。
func BoundOnlinePlayers(players []PlanPlayer) []PlanPlayer {
	ordered := make([]PlanPlayer, len(players))
	copy(ordered, players)
	// core.PlayerID 是数组类型，没有原生 <；按 16 字节 memcmp 序排序，
	// 全序且与十六进制文本形式的前缀序一致，排序结果确定。
	sort.Slice(ordered, func(i, j int) bool {
		return bytes.Compare(ordered[i].ID[:], ordered[j].ID[:]) < 0
	})
	deduped := ordered[:0]
	for index, player := range ordered {
		if index > 0 && player.ID == ordered[index-1].ID {
			continue
		}
		deduped = append(deduped, player)
	}
	if len(deduped) > MaxPlanOnlinePlayers {
		deduped = deduped[:MaxPlanOnlinePlayers]
	}
	return deduped
}

// BoundExposedBlocks 把观察到的暴露/特殊方块列表归一为快照可携带形式：按
// (X,Y,Z) 严格升序排序、坐标去重，并保留前 MaxPlanExposedBlocks 个。工作量为
// O(n log n)，不随范围方块总数无界增长；输入切片不被改动，返回值是独立副本。
//
// 排序与截断都是确定性的：同一集合以任意输入顺序进入得到完全相同的结果。
// 比较器在坐标之外用 BlockID 作最终 tiebreaker——体素世界里同一坐标只有一个
// 方块，重复坐标只可能来自上游缺陷，tiebreaker 保证即便如此结果也唯一。
func BoundExposedBlocks(blocks []PlanBlock) []PlanBlock {
	ordered := make([]PlanBlock, len(blocks))
	copy(ordered, blocks)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Pos != ordered[j].Pos {
			return planBlockAfter(ordered[j].Pos, ordered[i].Pos)
		}
		return ordered[i].Block < ordered[j].Block
	})
	ordered = dedupPlanBlocks(ordered)
	if len(ordered) > MaxPlanExposedBlocks {
		ordered = ordered[:MaxPlanExposedBlocks]
	}
	return ordered
}

// dedupPlanBlocks 去除排序后相邻的重复坐标条目（保留首次出现者），保证输出
// 严格升序。输入必须已按 (X,Y,Z) 升序。
func dedupPlanBlocks(blocks []PlanBlock) []PlanBlock {
	result := blocks[:0]
	for index, block := range blocks {
		if index > 0 && block.Pos == blocks[index-1].Pos {
			continue
		}
		result = append(result, block)
	}
	return result
}

// planBlockAfter 报告 pos 是否按 (X,Y,Z) 字典序严格大于 previous。
func planBlockAfter(pos, previous core.BlockPos) bool {
	if pos.X != previous.X {
		return pos.X > previous.X
	}
	if pos.Y != previous.Y {
		return pos.Y > previous.Y
	}
	return pos.Z > previous.Z
}

// validPlanBlockY 报告方块 Y 是否在世界竖直边界 [core.MinY, core.MaxY) 内。
// 世界边界常量复用 core 的权威定义，不另造魔法数。
func validPlanBlockY(y int32) bool {
	return y >= core.MinY && y < core.MaxY
}

// validatePlanText 校验快照内模型/玩家可见文本字段：必须是合法 UTF-8、不含
// 控制字符、长度不超过 maxBytes；requireNonEmpty 为 true 时还要求非空白。
func validatePlanText(field, value string, maxBytes int, requireNonEmpty bool) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("companion: %s不是合法 UTF-8", field)
	}
	if len(value) > maxBytes {
		return fmt.Errorf("companion: %s长 %d 字节超过上限 %d", field, len(value), maxBytes)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("companion: %s含控制字符", field)
		}
	}
	if requireNonEmpty && strings.TrimSpace(value) == "" {
		return fmt.Errorf("companion: %s为空", field)
	}
	return nil
}

// finite32 报告全部 float32 分量是否都是有限值（非 NaN、非 Inf）。快照里的
// 位置与朝向来自权威模拟，正常情况下永远有限；显式校验防止上游缺陷把 NaN
// 送进规划输入并被序列化成非法 JSON。
func finite32(values ...float32) bool {
	for _, value := range values {
		asFloat64 := float64(value)
		if math.IsNaN(asFloat64) || math.IsInf(asFloat64, 0) {
			return false
		}
	}
	return true
}

// PlanStepKind 标识计划步骤类型。M5C 的交付全集是 go_to/follow/mine/place
// 四种；任何其他类型在解码层直接拒绝，绝不翻译成任何模拟动作。枚举值一经
// 交付即为协议/存档稳定值（schema v3 按 kind 变长编码），不得重排。
type PlanStepKind uint8

const (
	// PlanStepGoTo 是「走向整数方块坐标」步骤：执行侧由确定性寻路与权威物理
	// 决定实际路径与位移，LLM 从不选择每 tick 输入。
	PlanStepGoTo PlanStepKind = iota + 1
	// PlanStepFollow 是「持续跟随指定玩家」步骤：目标必须是快照在线玩家集合
	// 中的玩家，且必须是计划的最后一步——follow 没有自然终点，任何排在它
	// 之后的步骤都无从执行。
	PlanStepFollow
	// PlanStepMine 是「采掘整数方块坐标处的方块」步骤：目标必须位于伙伴观察
	// 窗口内且是具有单一掉落、非容器的普通方块；执行侧复用玩家的采掘计时
	// 与工具规则。
	PlanStepMine
	// PlanStepPlace 是「在整数方块坐标放置一个方块」步骤：方块名必须来自
	// 固定注册表且快照背包显示伙伴持有对应物品；执行侧复用玩家的放置校验
	// 并在同一权威 tick 内扣料与写方块。
	PlanStepPlace
)

// PlanStep 是计划中的一个原子步骤。字段按 kind 使用：go_to/mine 使用 X/Y/Z
// 坐标（X/Z 是任意 int32 世界坐标，Y 必须在世界竖直边界 [core.MinY,
// core.MaxY) 内）；place 在坐标之外使用 Block（解码时把固定注册表中的方块名
// 归一为 core.BlockID）；follow 只使用 PlayerID（解码时把 canonical UUIDv4
// 文本归一为 core.PlayerID），坐标字段保持零值。未使用字段在解码输出中恒为
// 零值；持久化编码按 kind 变长，只写专属字段。
type PlanStep struct {
	Kind     PlanStepKind
	X        int32
	Y        int32
	Z        int32
	Block    core.BlockID
	PlayerID core.PlayerID
}

// Plan 是 Planner 解码并验证后的执行计划。
//
// Summary 是模型生成的非空有界中文摘要（≤MaxPlanSummaryBytes 字节、非空白、
// 不含控制字符）；Steps 非空且按声明顺序执行。Summary 与任何模型输出一样
// 全部视为不可信数据：只做展示与持久化，绝不执行其中的代码、URL 或工具名。
type Plan struct {
	Summary string
	Steps   []PlanStep
}

// Validate 校验计划不变量：summary 是规范有界文本、steps 非空且每步都属于
// 交付全集四 kind 并满足结构约束（坐标步骤的 Y 在世界竖直边界内、place 的
// 方块在固定注册表值域、follow 的目标 ID 是有效 UUIDv4 且 follow 是最后一
// 步）。依赖规划快照的约束（follow 目标在线、mine 观察窗口、place 背包持有）
// 由解码路径对照快照另行校验。任何违例都意味着模型输出了不可执行的非法计划。
func (p Plan) Validate() error {
	if err := validatePlanText("计划 summary", p.Summary, MaxPlanSummaryBytes, true); err != nil {
		return err
	}
	return validPlanSteps(p.Steps)
}

// validPlanSteps 校验步骤序列的结构不变量：非空、每步 kind 属于交付全集且
// 专属载荷合法、follow 只能出现在最后一步。快照相关约束不在本函数——它同时
// 服务持久化恢复路径（RestoreCurrent），那里没有快照可对照；恢复路径与解码
// 路径共享同一套结构校验，防止两套规则漂移。
func validPlanSteps(steps []PlanStep) error {
	if len(steps) == 0 {
		return fmt.Errorf("companion: 计划 steps 为空")
	}
	for index, step := range steps {
		switch step.Kind {
		case PlanStepGoTo, PlanStepMine:
			if !validPlanBlockY(step.Y) {
				return fmt.Errorf("companion: 计划 steps[%d] Y=%d 超出世界竖直边界", index, step.Y)
			}
		case PlanStepPlace:
			if !validPlanBlockY(step.Y) {
				return fmt.Errorf("companion: 计划 steps[%d] Y=%d 超出世界竖直边界", index, step.Y)
			}
			if _, ok := planPlaceBlocks[step.Block]; !ok {
				return fmt.Errorf("companion: 计划 steps[%d] 方块 %d 不在 place 注册表", index, step.Block)
			}
		case PlanStepFollow:
			if !step.PlayerID.Valid() {
				return fmt.Errorf("companion: 计划 steps[%d] follow 目标 ID 无效", index)
			}
			if index != len(steps)-1 {
				return fmt.Errorf("companion: 计划 steps[%d] follow 不是最后一步", index)
			}
		default:
			return fmt.Errorf("companion: 计划 steps[%d] kind %d 不是已交付的步骤类型", index, step.Kind)
		}
	}
	return nil
}

// planPlaceItems 是 place 步骤方块名的固定注册表：名字 → 可放置出该方块的
// 物品。core 没有方块名字注册表（只有数值 ID 与 ItemPlacement 物品→方块映
// 射），因此本表就是 planner 契约的唯一命名权威：名字经表得到物品，再经
// core.ItemPlacement 归一为方块，注册表值域被测试双向锁定为 ItemPlacement
// 的真值域（名字 ↔ 可放置方块双射），两张表不可能漂移。
var planPlaceItems = map[string]core.ItemID{
	"stone":             core.ItemStone,
	"dirt":              core.ItemDirt,
	"grass":             core.ItemGrass,
	"stone_brick":       core.ItemStoneBrick,
	"furnace":           core.ItemFurnace,
	"iron_block":        core.ItemIronBlock,
	"chest":             core.ItemChest,
	"light_block":       core.ItemLightBlock,
	"cobblestone":       core.ItemCobblestone,
	"smooth_stone":      core.ItemSmoothStone,
	"sand":              core.ItemSand,
	"gravel":            core.ItemGravel,
	"oak_log":           core.ItemOakLog,
	"oak_planks":        core.ItemOakPlanks,
	"leaves":            core.ItemLeaves,
	"glass":             core.ItemGlass,
	"brick":             core.ItemBrick,
	"white_wool":        core.ItemWhiteWool,
	"roof_tile":         core.ItemRoofTile,
	"clay":              core.ItemClay,
	"snow_block":        core.ItemSnowBlock,
	"mossy_cobblestone": core.ItemMossyCobblestone,
}

// planPlaceBlocks 是固定注册表的反向索引：可放置方块 → 对应物品。place 步骤
// 解码后的强类型校验（Block ∈ 注册表值域）与背包持有判定（持有该方块的对应
// 物品）都查这张表；它与 planPlaceItems 同源构造，天然保持一致。
var planPlaceBlocks = buildPlanPlaceBlocks()

// buildPlanPlaceBlocks 在初始化时从名字注册表反查 core.ItemPlacement 构造
// 方块 → 物品索引。注册表测试保证这是双射；此处对 ItemPlacement 失败仍做
// 防御性跳过，保证坏表只会让校验更严而不是 panic。
func buildPlanPlaceBlocks() map[core.BlockID]core.ItemID {
	blocks := make(map[core.BlockID]core.ItemID, len(planPlaceItems))
	for _, item := range planPlaceItems {
		block, ok := core.ItemPlacement(item)
		if !ok {
			continue
		}
		blocks[block] = item
	}
	return blocks
}

// planMineableBlock 报告 block 是否是 planner 契约允许的 mine 目标：必须具有
// 单一 core.BlockDrop 且不是箱子/熔炉——容器破坏会产出本体加内容物的多份
// 产物，超出「单一产物直入背包」的结算形状；无掉落的方块（如基岩）同样不可
// 作为目标。与 internal/sim 采掘完成分叉处的防御清单是同一规则的两处实现
// （companion 不得依赖 sim，依赖方向相反），两处必须保持一致。
func planMineableBlock(block core.BlockID) bool {
	if block == core.ChestID || block == core.FurnaceID {
		return false
	}
	_, ok := core.BlockDrop(block)
	return ok
}

// planInObservationWindow 报告 pos 是否落在伙伴的观察窗口内：水平
// planEnvRadiusBlocks 格、垂直 planEnvVerticalBlocks 格，与快照环境摘要的采集
// 窗口共用同一组数值界。
//
// 判定基准（控制器裁决）是窗口的数值界而不是 ExposedBlocks 的成员资格：
// ExposedBlocks 是被裁剪到 256 条的子集，窗口之内但未列入的方块仍应能被
// mine 指令表达，把成员资格当必要条件会让模型因快照裁剪而被误拒。目标是否
// 真的可采掘由执行侧的交互距离与方块校验兜底；方块类型契约只在目标恰好列入
// ExposedBlocks 时加强校验（见解码路径）。
func planInObservationWindow(companionPos [3]float32, pos core.BlockPos) bool {
	return math.Abs(float64(pos.X)-float64(companionPos[0])) <= planEnvRadiusBlocks &&
		math.Abs(float64(pos.Z)-float64(companionPos[2])) <= planEnvRadiusBlocks &&
		math.Abs(float64(pos.Y)-float64(companionPos[1])) <= planEnvVerticalBlocks
}

// planInventoryHolds 报告 36 格完整物品状态（快捷栏 + 背包的值快照）中是否
// 持有至少一个 item。place 契约只要求快照背包显示持有——执行侧扣料在同一
// tick 原子完成，数量不足由 action 语义拒绝，这里不做数量核对。
func planInventoryHolds(inventory core.Inventory, item core.ItemID) bool {
	if item == core.ItemNone {
		return false
	}
	for slot := uint8(0); slot < core.InventorySlots; slot++ {
		stack, _ := inventory.Slot(slot)
		if stack.Item == item && stack.Count >= 1 {
			return true
		}
	}
	return false
}
