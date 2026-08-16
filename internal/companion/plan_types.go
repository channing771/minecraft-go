// 本文件定义 Planner 的输入快照与输出计划值类型。快照由服务端在权威 tick
// 边界构造（属后续任务），本文件只负责类型、字段边界与确定性排序；计划类型
// 的严格解码路径在 planner.go。
package companion

import (
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
	// WorldTimeTicks 是快照时刻的权威世界时间（0..23999 昼夜 tick）。
	WorldTimeTicks uint64 `json:"worldTimeTicks"`
}

// Validate 校验快照的全部不变量：指令与任务状态摘要的编码和长度、身份有效
// 性、浮点有限性、三类列表的数量/顺序/去重/取值范围与背包规范性。
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
	return nil
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

// PlanStepKind 标识计划步骤类型。M5B 只交付 go_to 一种；follow/mine/place
// 等未交付类型在解码层直接拒绝，绝不翻译成任何模拟动作。
type PlanStepKind uint8

const (
	// PlanStepGoTo 是「走向整数方块坐标」步骤：执行侧由确定性寻路与权威物理
	// 决定实际路径与位移，LLM 从不选择每 tick 输入。
	PlanStepGoTo PlanStepKind = iota + 1
)

// PlanStep 是计划中的一个原子步骤。M5B 的全部步骤都是 go_to：X/Z 是任意
// int32 世界坐标，Y 必须在世界竖直边界 [core.MinY, core.MaxY) 内。
type PlanStep struct {
	Kind PlanStepKind
	X    int32
	Y    int32
	Z    int32
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

// Validate 校验计划不变量：summary 是规范有界文本、steps 非空且每步都是
// 合法 go_to（kind 唯一、坐标为有限整数、Y 在世界边界内）。任何违例都意味着
// 模型输出了不可执行的非法计划。
func (p Plan) Validate() error {
	if err := validatePlanText("计划 summary", p.Summary, MaxPlanSummaryBytes, true); err != nil {
		return err
	}
	if len(p.Steps) == 0 {
		return fmt.Errorf("companion: 计划 steps 为空")
	}
	for index, step := range p.Steps {
		if step.Kind != PlanStepGoTo {
			return fmt.Errorf("companion: 计划 steps[%d] kind %d 不是已交付的 go_to", index, step.Kind)
		}
		if !validPlanBlockY(step.Y) {
			return fmt.Errorf("companion: 计划 steps[%d] Y=%d 超出世界竖直边界", index, step.Y)
		}
	}
	return nil
}
