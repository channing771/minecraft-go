package fluid

import (
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

// TestEndToEnd_SourceSpreadsExactlySevenOnFlatGround 覆盖
// task-2-brief.md 2.5 与 spec Scenario「源沿地面铺开 7 格」：
// 一个源方块位于平整实心地面上、四周为空气，推进至平衡态后：
//   - 水平方向最远的流动方块距源恰好 7 格；
//   - 第 8 格及更远处保持空气。
//
// 沿单个水平方向验证即可代表性覆盖「水平方向最远」这一约束：evalCell 对
// 四个方向的规则完全对称（见 TestEvalCell_HorizontalSpreadFromSource），
// 这里额外验证 +X 与 -X 两个方向，确认对称性在多 tick 推进下也成立。
func TestEndToEnd_SourceSpreadsExactlySevenOnFlatGround(t *testing.T) {
	w := newMemWorld()
	source := core.BlockPos{X: 0, Y: 10, Z: 0}
	w.SetBlock(source, core.WaterSourceID)

	// 平整实心地面：源所在层向四周各铺开 16 格的正方形地面（覆盖到第 8 格
	// 之外，且 X/Z 两个方向都要铺满，而不只是一条线——否则 Z 方向的水平
	// 传播会因为下方是未定义的空气而触发垂直优先，向下漏水而不是沿地面
	// 水平铺开，干扰本测试要验证的水平扩散断言。
	for x := int32(-16); x <= 16; x++ {
		for z := int32(-16); z <= 16; z++ {
			w.SetBlock(core.BlockPos{X: x, Y: 9, Z: z}, core.StoneID)
		}
	}

	q := NewQueue()
	q.Enqueue(source, 0, 0)

	// 用远大于收敛所需的 tick 数推进到平衡态（每 tick 都判定“是否还有
	// 变化”，一旦连续无变化即认为已到平衡，提前退出）。budget 给得足够大，
	// 不让预算成为本测试的约束变量——本测试只验证规则本身的传播距离。
	const maxTicks = 200
	now := uint64(1)
	for now < maxTicks {
		changed := q.Advance(now, w, 1<<20, 5)
		now++
		if len(changed) == 0 && q.Len() == 0 {
			break
		}
	}
	if q.Len() != 0 {
		t.Fatalf("推进 %d tick 后队列仍非空（未到平衡态），Len()=%d", maxTicks, q.Len())
	}

	for x := int32(1); x <= 7; x++ {
		pos := core.BlockPos{X: x, Y: 10, Z: 0}
		want := core.WaterSourceID + core.BlockID(x)
		if got := w.BlockAt(pos); got != want {
			t.Fatalf("+X 方向第 %d 格应为等级 %d 的流动水，got %v want %v", x, x, got, want)
		}
	}
	for x := int32(-1); x >= -7; x-- {
		pos := core.BlockPos{X: x, Y: 10, Z: 0}
		want := core.WaterSourceID + core.BlockID(-x)
		if got := w.BlockAt(pos); got != want {
			t.Fatalf("-X 方向第 %d 格应为等级 %d 的流动水，got %v want %v", -x, -x, got, want)
		}
	}

	if got := w.BlockAt(core.BlockPos{X: 8, Y: 10, Z: 0}); got != core.AirID {
		t.Fatalf("+X 方向第 8 格应保持空气，got %v", got)
	}
	if got := w.BlockAt(core.BlockPos{X: -8, Y: 10, Z: 0}); got != core.AirID {
		t.Fatalf("-X 方向第 8 格应保持空气，got %v", got)
	}
	if got := w.BlockAt(core.BlockPos{X: 0, Y: 10, Z: 1}); got != core.WaterLevel1ID {
		t.Fatalf("Z 方向水平邻居也应铺开（等级 1），got %v", got)
	}
	if got := w.BlockAt(core.BlockPos{X: 0, Y: 10, Z: 7}); got != core.WaterLevel7ID {
		t.Fatalf("+Z 方向第 7 格应为等级 7，got %v", got)
	}
	if got := w.BlockAt(core.BlockPos{X: 0, Y: 10, Z: 8}); got != core.AirID {
		t.Fatalf("+Z 方向第 8 格应保持空气，got %v", got)
	}
}
