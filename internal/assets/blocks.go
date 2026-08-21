package assets

import (
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/mesh"
	"github.com/channing771/mornlea/internal/world"
)

const (
	LayerStone uint16 = iota
	LayerDirt
	LayerGrassTop
	LayerGrassSide
	LayerBedrock
	LayerStoneBrick
	LayerCoalOre
	LayerIronOre
	LayerFurnace
	LayerIronBlock
	LayerChest
	LayerLightBlock
	LayerLeaves
	LayerGlass
	LayerCobblestone
	LayerSmoothStone
	LayerSand
	LayerGravel
	LayerOakLogSide
	LayerOakLogTop
	LayerOakPlanks
	LayerBrick
	LayerWhiteWool
	LayerRoofTile
	LayerClay
	LayerSnowTop
	LayerSnowSide
	LayerMossyCobblestone
	// LayerWater 是 8 个流体编号共用的材质层。它必须独立于任何固体层：
	// 上传路径正是按 material 把水面 quad 分流到半透明 water pass 的
	// （见 internal/render.SectionScheduler），共用石头层等于水面被画进
	// 不透明 terrain pass。
	LayerWater
	layerCount
)

// Registry 是方块属性与材质的注册表。
type Registry struct {
	layers       [layerCount][]byte
	meshSnapshot mesh.RegistrySnapshot
}

// NewRegistry 构造注册表并生成全部占位材质。
func NewRegistry() *Registry {
	r := &Registry{}
	r.layers[LayerStone] = stoneTexture()
	r.layers[LayerDirt] = dirtTexture()
	r.layers[LayerGrassTop] = grassTopTexture()
	r.layers[LayerGrassSide] = grassSideTexture()
	r.layers[LayerBedrock] = noisyTexture(rgb{R: 60, G: 60, B: 64}, 28, 0x3F19)
	r.layers[LayerStoneBrick] = stoneBrickTexture()
	r.layers[LayerCoalOre] = oreTexture(rgb{R: 38, G: 40, B: 44})
	r.layers[LayerIronOre] = oreTexture(rgb{R: 194, G: 140, B: 104})
	r.layers[LayerFurnace] = furnaceTexture()
	r.layers[LayerIronBlock] = ironBlockTexture()
	r.layers[LayerChest] = chestTexture()
	r.layers[LayerLightBlock] = lightBlockTexture()
	r.layers[LayerLeaves] = leavesTexture()
	r.layers[LayerGlass] = glassTexture()
	r.layers[LayerCobblestone] = cobblestoneTexture()
	r.layers[LayerSmoothStone] = smoothStoneTexture()
	r.layers[LayerSand] = sandTexture()
	r.layers[LayerGravel] = gravelTexture()
	r.layers[LayerOakLogSide] = oakLogSideTexture()
	r.layers[LayerOakLogTop] = oakLogTopTexture()
	r.layers[LayerOakPlanks] = oakPlanksTexture()
	r.layers[LayerBrick] = brickTexture()
	r.layers[LayerWhiteWool] = whiteWoolTexture()
	r.layers[LayerRoofTile] = roofTileTexture()
	r.layers[LayerClay] = clayTexture()
	r.layers[LayerSnowTop] = snowTopTexture()
	r.layers[LayerSnowSide] = snowSideTexture()
	r.layers[LayerMossyCobblestone] = mossyCobblestoneTexture()
	r.layers[LayerWater] = waterTexture()
	// ids 覆盖 core 的全部已注册方块编号，上界一律用独占哨兵 core.BlockIDMax
	// 表达——写死某个具体末位编号（历史上写过 WaterLevel7ID）会在追加新编号时
	// 静默退化成子集，新方块就永远进不了快照。Rust 侧的
	// RegistryView::face_visible 只做位图查表、缺条目一律判不可见，漏掉谁就等于
	// 谁永远不出面（流体当年正是这样差点画不出水）。
	// 条目数必须不超过 internal/mesh.nativeMaxRegistryEntries 与 Rust 的
	// MAX_REGISTRY_ENTRIES（今天是 45 <= 48）。
	ids := make([]world.BlockID, 0, int(core.BlockIDMax))
	for id := core.AirID; id < core.BlockIDMax; id++ {
		ids = append(ids, id)
	}
	snapshot, err := mesh.BuildRegistrySnapshot(ids, r)
	if err != nil {
		panic("assets: 构建 mesh registry snapshot: " + err.Error())
	}
	r.meshSnapshot = snapshot
	return r
}

// Opaque 返回方块是否完全不透明。实现 mesh.Registry。
// 流体（IsFluid）与玻璃、树叶一样是透明方块。
//
// 这里的 `!core.IsFluid(id)` 是一条与 mesh snapshot 范围无关、恒成立的事实，
// **不得删除**：internal/mesh/visibility.go 的 ComputeConnectivity 洪水填充
// 直接拿活体 Section 的方块数据调用本函数，那条路径根本不经过快照。若删掉
// 这处排除，整片水会被当成实心遮挡体，区段面连通性塌成全不可达，进而错误
// 剔除水体后方的整批区段。守卫见 internal/mesh 的
// TestConnectivityTreatsFluidAsTransparentOnLiveSectionData。
func (r *Registry) Opaque(id world.BlockID) bool {
	return core.RegisteredBlock(id) && id != core.AirID && id != core.GlassID &&
		id != core.LeavesID && !core.IsFluid(id)
}

// FaceVisible 返回当前方块朝向相邻方块的面是否可绘制。实现 mesh.Registry。
//
// 本函数是全系统唯一的出面规则来源：它在 BuildRegistrySnapshot 里被调用一次，
// 把结果烘焙成 Visibility 位图（见 internal/mesh/registry.go），Rust 的
// RegistryView::face_visible 只是对这张位图查表，自己不含任何规则。因此流体的
// 出面规则也只能写在这里，且由既有的通用判定自然导出：
//
//   - 流体 → 流体：adjacent 非空气且 Opaque(流体)=false，落到 `return r.Opaque(id)`
//     即 false，水体内部不产生面；
//   - 流体 → 空气：直接 true，水面出几何；
//   - 流体 → 不透明方块（含头顶压着实心方块的情形）：被 `r.Opaque(adjacent)` 拦下，
//     不可见；
//   - 不透明方块 → 流体：落到 `return r.Opaque(id)` 即 true，水下地形不会消失。
//
// 历史注意：流体尚未纳入 mesh snapshot ids 范围时，这里曾对 id 与 adjacent 两侧
// 各有一处 `core.IsFluid(...)` 补偿分支，用来跟 Rust 的「缺条目即不出面」对齐。
// 流体入快照后它们已随之删除；若被误加回来，水的每一对 (id,adjacent) 都会被烘焙
// 成永久不可见、水彻底画不出来，而这件事**不会**让任何既有断言变红——守卫是
// internal/assets 的 TestFluidFaceVisibilityRules 与 internal/mesh 的
// TestNativeOracleParityWaterSurface。
func (r *Registry) FaceVisible(id, adjacent world.BlockID) bool {
	if !core.RegisteredBlock(id) || id == core.AirID ||
		!core.RegisteredBlock(adjacent) || r.Opaque(adjacent) {
		return false
	}
	if adjacent == core.AirID {
		return true
	}
	return r.Opaque(id)
}

// Material 返回方块某个面的材质层号。实现 mesh.Registry。
func (r *Registry) Material(id world.BlockID, f mesh.Face) uint16 {
	switch id {
	case core.StoneID:
		return LayerStone
	case core.DirtID:
		return LayerDirt
	case core.BedrockID:
		return LayerBedrock
	case core.StoneBrickID:
		return LayerStoneBrick
	case core.CoalOreID:
		return LayerCoalOre
	case core.IronOreID:
		return LayerIronOre
	case core.FurnaceID:
		return LayerFurnace
	case core.IronBlockID:
		return LayerIronBlock
	case core.ChestID:
		return LayerChest
	case core.LightBlockID:
		return LayerLightBlock
	case core.LeavesID:
		return LayerLeaves
	case core.GlassID:
		return LayerGlass
	case core.CobblestoneID:
		return LayerCobblestone
	case core.SmoothStoneID:
		return LayerSmoothStone
	case core.SandID:
		return LayerSand
	case core.GravelID:
		return LayerGravel
	case core.OakLogID:
		if f == mesh.FacePosY || f == mesh.FaceNegY {
			return LayerOakLogTop
		}
		return LayerOakLogSide
	case core.OakPlanksID:
		return LayerOakPlanks
	case core.BrickID:
		return LayerBrick
	case core.WhiteWoolID:
		return LayerWhiteWool
	case core.RoofTileID:
		return LayerRoofTile
	case core.ClayID:
		return LayerClay
	case core.SnowBlockID:
		if f == mesh.FacePosY {
			return LayerSnowTop
		}
		return LayerSnowSide
	case core.MossyCobblestoneID:
		return LayerMossyCobblestone
	case core.GrassID:
		switch f {
		case mesh.FacePosY:
			return LayerGrassTop
		case mesh.FaceNegY:
			return LayerDirt
		default:
			return LayerGrassSide
		}
	default:
		// 8 个流体编号共用同一个水材质层：mesh 的 registry 条目每方块只有
		// 6 个 material，塞不下等级；等级信息走独立的 FluidHeight 字段。
		if core.IsFluid(id) {
			return LayerWater
		}
		return LayerStone
	}
}

// Emission 返回方块固定发出的方块光等级。实现 mesh.Registry。
func (r *Registry) Emission(id world.BlockID) uint8 {
	if id == core.LightBlockID {
		return 15
	}
	return 0
}

// fluidSourceHeightRaw 是源方块（level 0）的 4-bit 高度原值。
//
// 取 14 而非 15：实际高度是 (h_raw+1)/16，14 即 15/16，让源方块的水面比方块顶面
// 略低一线，水柱内部再由「上方是流体则取满格 15」补齐（见 mesh 的角高度推导）。
// 于是 h_raw(level) = 14 - level，最弱的 level 7 仍有 7 即半格高度，不会退化成
// 零面积的水面。
const fluidSourceHeightRaw = 14

// FluidHeight 返回方块孤立时的 4-bit 流体高度原值 h_raw。实现 mesh.Registry。
//
// 非流体返回 0 这个哨兵值——真流体的 h_raw 恒在 7..=14，0 不会与之冲突。
// 本函数是「流体等级 → 高度」映射的**唯一**真值源：它被烘焙进 mesh registry
// 快照送给 Rust，Rust 只消费高度、不知道等级。
func (r *Registry) FluidHeight(id world.BlockID) uint8 {
	if !core.IsFluid(id) {
		return 0
	}
	return fluidSourceHeightRaw - core.FluidLevel(id)
}

// LightAttenuation 返回天空光穿过该方块时的额外衰减。实现 mesh.Registry。
//
// 流体额外衰减 1、其余方块 0。值经 registry 快照送过 ABI 边界，由 Rust 的天空光
// BFS 逐步查表扣减——竖直向下穿过流体因此不再无损。方块光模型不消费本值：水与
// 玻璃一样直接阻断。
func (r *Registry) LightAttenuation(id world.BlockID) uint8 {
	if core.IsFluid(id) {
		return 1
	}
	return 0
}

// MeshSnapshot 返回构造时冻结的网格 registry 快照。
func (r *Registry) MeshSnapshot() mesh.RegistrySnapshot { return r.meshSnapshot }

func (r *Registry) LayerCount() int { return int(layerCount) }

func (r *Registry) LayerRGBA(layer int) []byte { return r.layers[layer] }

var _ mesh.Registry = (*Registry)(nil)
