// Package fluid 提供与权威模拟解耦的纯流体推进算法：单格流动规则求值与
// 待更新队列调度。本包只依赖 internal/core 与 internal/world，不依赖
// internal/sim/network/render/storage——流动延迟与每 tick 预算这两个 tunable
// 归 internal/sim 所有，一律由调用方在调用时以参数传入，本包不定义、不读取
// 任何隐藏默认值。
//
// 本包不持有世界，也不知道“权威 tick”“活动兴趣区块”这些概念；把流体接入
// 权威模拟（入队点、区块推进范围、广播）是上层 internal/sim 的职责。
package fluid

import "github.com/channing771/mornlea/internal/core"

// FluidWorld 是流动推进所需的最小世界视图：只按世界坐标读写单个方块。
//
// 刻意不暴露 world.Chunk、world.Section 或任何整块/整区段类型——流动规则只
// 需要逐格读写，接口越窄，越容易在测试里用一个内存 map 顶替，也越不会把
// sim/world 的内部结构泄漏进本包的公开 API。
type FluidWorld interface {
	// BlockAt 返回 pos 处当前的方块编号。
	BlockAt(pos core.BlockPos) core.BlockID
	// SetBlock 把 pos 处的方块写为 id。
	SetBlock(pos core.BlockPos, id core.BlockID)
}
