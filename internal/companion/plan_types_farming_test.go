package companion

import (
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

// TestPlanMineableBlockRejectsFarmingBlocks 锁定 planner 契约层对农业方块的
// **显式**拒绝（design.md D7 / Ruling 5）。它与 internal/sim 的
// companionMineableBlock 是同一规则的两处实现（companion 不得依赖 sim，依赖
// 方向相反），两处必须同时拒绝：只守其中一处的话，另一处会独自放行。
//
// 前置守卫是这条用例的承重墙：十个农业方块在 core.BlockDrop 里都有单一产物
// 登记，"必须有单一 BlockDrop"这条通用判据会**放行**它们。因此本用例断言的是
// "农业方块被显式拒绝"这个事实本身，不是"多掉落被拒"的副产品——把显式拒绝
// 删掉，或者把成熟小麦的多掉落放宽进 core，本用例都必须红。
func TestPlanMineableBlockRejectsFarmingBlocks(t *testing.T) {
	for _, block := range []core.BlockID{
		core.WheatStage0ID, core.WheatStage1ID, core.WheatStage2ID, core.WheatStage3ID,
		core.WheatStage4ID, core.WheatStage5ID, core.WheatStage6ID, core.WheatStage7ID,
		core.FarmlandDryID, core.FarmlandWetID,
	} {
		if _, ok := core.BlockDrop(block); !ok {
			t.Fatalf("农业方块 %d 已不在 core.BlockDrop 中，本用例的前提失效", block)
		}
		if planMineableBlock(block) {
			t.Fatalf("planMineableBlock(%d) = true，伙伴必须被显式拒绝", block)
		}
	}
}
