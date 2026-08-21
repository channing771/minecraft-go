package mesh_test

import (
	"testing"

	"github.com/channing771/mornlea/internal/assets"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/mesh"
	"github.com/channing771/mornlea/internal/world"
)

// plantSampleX / plantSampleZ 是被观察格在中心区块里的局部水平坐标。
const (
	plantSampleX = 8
	plantSampleZ = 8
	// plantFloorY 是石头地基，plantFarmlandY 是耕地，plantSampleY 是被观察格。
	plantFloorY    = 63
	plantFarmlandY = 64
	plantSampleY   = 65
	// plantLidY 是「遮蔽」变体里压在被观察格上方两格的石头盖。
	plantLidY = 67
)

// plantWorld 造一个 3×3 区块的观察台，被观察格 (8, 65, 8) 放 sample。
//
// 关键结构是**把被观察格封成只朝上开口的死胡同**：y=65 那一层在它四周放石头墙，
// 下方是不透明耕地。于是
//
//   - 它自己那格的派生光照只可能来自正上方，恒比上方格低一级；
//   - 它是不是不透明**不会**改变上方格的光照——光进不去也出不来。
//
// 第二条正是本文件全部光照断言的支点：把 sample 从小麦换成石头，上方格的光照
// 逐位不变，于是那块石头的**顶面**就是一把独立的尺，量的正是「植物应当采样的
// 那一格」。不这样封口的话，换材质会同时改变光场，两次读数就没有可比性了。
//
// lid 为真时在上方两格 (8, 67, 8) 盖一块石头，把上方格从满天空光压暗。
func plantWorld(t *testing.T, sample world.BlockID, lid bool) *world.Neighborhood {
	t.Helper()
	chunks := make(map[core.ChunkPos]*world.Chunk, 9)
	for cx := int32(-1); cx <= 1; cx++ {
		for cz := int32(-1); cz <= 1; cz++ {
			pos := core.ChunkPos{X: cx, Z: cz}
			chunk := world.NewChunk(pos)
			for lz := 0; lz < core.SectionSize; lz++ {
				for lx := 0; lx < core.SectionSize; lx++ {
					chunk.SetBlock(lx, plantFloorY, lz, core.StoneID)
					chunk.SetBlock(lx, plantFarmlandY, lz, core.FarmlandWetID)
				}
			}
			chunks[pos] = chunk
		}
	}
	center := chunks[core.ChunkPos{}]
	for _, d := range [4][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
		center.SetBlock(plantSampleX+d[0], plantSampleY, plantSampleZ+d[1], core.StoneID)
	}
	center.SetBlock(plantSampleX, plantSampleY, plantSampleZ, sample)
	if lid {
		center.SetBlock(plantSampleX, plantLidY, plantSampleZ, core.StoneID)
	}

	sectionIndex := int(plantFarmlandY-core.MinY) >> core.SectionShift
	n := world.NeighborhoodAt(func(pos core.ChunkPos) *world.Chunk { return chunks[pos] },
		core.ChunkPos{}, sectionIndex)
	if n == nil {
		t.Fatal("NeighborhoodAt 返回 nil")
	}
	return n
}

// plantLocalY 把世界 y 换算成 plantWorld 那个区段里的局部 y。
func plantLocalY(worldY int) int { return (worldY - core.MinY) & core.SectionMask }

// plantQuads 取出被观察格产生的全部植物 quad。
func plantQuads(quads []mesh.Quad) []mesh.Quad {
	var out []mesh.Quad
	for _, quad := range quads {
		if quad.Face.Plant() {
			out = append(out, quad)
		}
	}
	return out
}

// TestNativeOracleParityWheatCrossPlanes 是植物交叉斜面的跨语言门禁。
//
// 三件事一起守：
//
//  1. Rust mesher 与 Go oracle 逐条逐位一致（两份独立实现，只共享规则）；
//  2. Go 侧的植物 material 区间与 Rust 的 PLANT_MATERIAL_FIRST/LAST 真的对齐——
//     assets 的层枚举一旦在小麦之前插层，Rust 会认不出这格是植物、一条 quad 都
//     不出，`len(plant) != 4` 立刻红。这是那对硬编码常量**唯一**的机械守卫；
//  3. 作物确实贡献了几何（对照组是同一夹具里的石头），否则一致性断言会退化成
//     「两侧都不出面」的恒真——任务组 1 的评审实测过这种假绿。
func TestNativeOracleParityWheatCrossPlanes(t *testing.T) {
	registry := assets.NewRegistry()
	wheat := assertNativeOracleParity(t, plantWorld(t, core.WheatStage5ID, false), registry)
	stone := assertNativeOracleParity(t, plantWorld(t, core.StoneID, false), registry)

	plant := plantQuads(wheat)
	if len(plant) != 4 {
		t.Fatalf("作物产生了 %d 条交叉斜面，想要 4；"+
			"若为 0，多半是 assets 的 LayerWheat0..7 与 Rust 的 "+
			"PLANT_MATERIAL_FIRST/PLANT_MATERIAL_LAST 已经错位", len(plant))
	}
	if got := len(plantQuads(stone)); got != 0 {
		t.Fatalf("石头对照组产生了 %d 条交叉斜面，想要 0", got)
	}

	wantMat := registry.Material(core.WheatStage5ID, mesh.FaceNegX)
	seen := map[[2]any]bool{}
	for _, quad := range plant {
		if int(quad.X) != plantSampleX || int(quad.Z) != plantSampleZ ||
			int(quad.Y) != plantLocalY(plantSampleY) {
			t.Fatalf("交叉斜面 %+v 不在被观察格上", quad)
		}
		if quad.W != 1 || quad.H != 1 {
			t.Fatalf("交叉斜面 %+v 被合并成 %dx%d：植物 MUST NOT 参与贪心合并", quad, quad.W, quad.H)
		}
		if quad.Mat != wantMat {
			t.Fatalf("交叉斜面 material=%d，想要 %d", quad.Mat, wantMat)
		}
		if quad.Corners != ([4]uint8{}) {
			t.Fatalf("交叉斜面 %+v 带了角高度", quad)
		}
		seen[[2]any{quad.Face, quad.Back}] = true
	}
	// 四条必须是「两条对角线 × 正反两面」，而不是同一条重复四次——后者在数量
	// 断言下同样成立，却会让任一水平视角剔掉整整一半几何。
	if len(seen) != 4 {
		t.Fatalf("四条交叉斜面只覆盖了 %d 种 (对角线, 正背) 组合，想要 4", len(seen))
	}

	// 作物**没有**轴向面：它的六个面在 assets.FaceVisible 里一律不可见。
	for _, quad := range wheat {
		if quad.Mat == wantMat && !quad.Face.Plant() {
			t.Fatalf("作物出了轴向面 %+v", quad)
		}
	}
	// 反方向必须仍然出面：石头墙朝向作物的那一面在两组夹具里都在。作物若被写成
	// 不透明，这一条会连同上面的天空光断言一起红。
	if !hasFaceToward(wheat, plantSampleX-1, plantLocalY(plantSampleY), plantSampleZ, mesh.FacePosX) {
		t.Fatal("石头墙朝向作物的面消失了：作物不该遮挡邻居出面")
	}
}

// hasFaceToward 报告某格是否产生了指定朝向的面。
func hasFaceToward(quads []mesh.Quad, x, y, z int, face mesh.Face) bool {
	for _, quad := range quads {
		if quad.Face == face && int(quad.X) == x && int(quad.Y) == y && int(quad.Z) == z {
			return true
		}
	}
	return false
}

// TestPlantLightComesFromTheCellAbove 覆盖 plant-visual-presentation 的两条光照
// Scenario，以及「作物下方的耕地仍被照亮」。
//
// 尺子是**同一夹具里把作物换成石头**后那块石头的顶面：顶面采的正是它上方那一格，
// 而被观察格封成了只朝上开口的死胡同，换材质不会改变上方格的光照（见 plantWorld）。
// 于是「作物的光照 == 上方格的光照」成了一条可以直接读数的位置性断言。
//
// 三个读数必须两两不同，否则断言退化：
//
//   - 上方格（作物应当采样的）——露天时 15；
//   - 作物自己那格（写错采样点最可能落到的地方）——恒比上方低一级；
//   - 遮蔽变体的上方格——必须低于 15，否则「等于该相邻格的值」在露天与遮蔽下
//     读数相同，取错采样点照样绿。
func TestPlantLightComesFromTheCellAbove(t *testing.T) {
	registry := assets.NewRegistry()
	localY := plantLocalY(plantSampleY)

	measure := func(lid bool) (crop, above, own uint8) {
		t.Helper()
		wheat := mesh.MeshSection(plantWorld(t, core.WheatStage5ID, lid), registry, mesh.NewLightScratch())
		stone := mesh.MeshSection(plantWorld(t, core.StoneID, lid), registry, mesh.NewLightScratch())
		plant := plantQuads(wheat)
		if len(plant) == 0 {
			t.Fatal("作物没有产生交叉斜面")
		}
		for _, quad := range plant[1:] {
			if quad.Light != plant[0].Light {
				t.Fatalf("同一格的四条交叉斜面光照不一致：%d vs %d", quad.Light, plant[0].Light)
			}
		}
		// 石头顶面采的是它上方那一格，正是作物应当采样的位置。
		above = topFaceLightAt(t, stone, plantSampleX, localY, plantSampleZ)
		// 耕地顶面采的是被观察格自己——作物在场时，这就是作物那一格的派生光照。
		own = topFaceLightAt(t, wheat, plantSampleX, plantLocalY(plantFarmlandY), plantSampleZ)
		return plant[0].Light, above, own
	}

	openCrop, openAbove, openOwn := measure(false)
	if skyLight(openAbove) != 15 {
		t.Fatalf("露天上方格天空光=%d，夹具本身没有满天空光，后面的断言无从谈起", skyLight(openAbove))
	}
	if skyLight(openCrop) != 15 {
		t.Fatalf("露天作物天空光=%d，想要 15", skyLight(openCrop))
	}
	// 位置性支点：作物自己那格严格更暗，因此「取上方格」与「取自身格」读数不同。
	if skyLight(openOwn) >= skyLight(openCrop) {
		t.Fatalf("作物自身格天空光=%d 未低于上方格 %d，两个采样点无法区分，"+
			"本用例对采样点写错不敏感", skyLight(openOwn), skyLight(openCrop))
	}
	// Scenario「作物下方的耕地仍被照亮」：耕地顶面采的就是作物那一格。
	if skyLight(openOwn) == 0 {
		t.Fatal("作物下方的耕地顶面天空光=0：作物把光挡死了，它必须与玻璃、树叶同类")
	}

	shadedCrop, shadedAbove, _ := measure(true)
	if skyLight(shadedAbove) == 0 || skyLight(shadedAbove) >= 15 {
		t.Fatalf("遮蔽变体上方格天空光=%d，想要 0 < 值 < 15", skyLight(shadedAbove))
	}
	if shadedCrop != shadedAbove {
		t.Fatalf("被遮蔽作物光照=%#02x，上方格=%#02x：必须相等", shadedCrop, shadedAbove)
	}
}

// TestPlantQuadsSurviveTheUploadRoundTrip 钉住植物编码在 Rust→Go→GPU 这条路上无损。
//
// Rust 打包、Go 解包、上传前再打包，任一环节丢位都会让顶点摆错位置。Back 位与
// face 6/7 共用 W/H 那 8 bit，是最容易被旧的 `w-1`/`h-1` 解码吃掉的地方。
func TestPlantQuadsSurviveTheUploadRoundTrip(t *testing.T) {
	quads := mesh.MeshSection(plantWorld(t, core.WheatStage7ID, false), assets.NewRegistry(), mesh.NewLightScratch())
	plant := plantQuads(quads)
	if len(plant) != 4 {
		t.Fatalf("交叉斜面=%d，想要 4", len(plant))
	}
	backs := 0
	for _, quad := range plant {
		packed := quad.Pack()
		if packed>>63 != 0 {
			t.Fatalf("植物 quad 占用了必须留空的 bit 63：%#016x", packed)
		}
		if got := mesh.UnpackQuad(packed); got != quad {
			t.Fatalf("往返不一致:\n实际 %+v\n期望 %+v", got, quad)
		}
		if quad.Back {
			backs++
		}
	}
	if backs != 2 {
		t.Fatalf("四条交叉斜面里有 %d 条背面，想要 2", backs)
	}
}

// TestPlantQuadEncodingRejectsIllegalCombinations 覆盖 quad.go 作为 native 结果
// 信任边界的三条拒绝规则。
func TestPlantQuadEncodingRejectsIllegalCombinations(t *testing.T) {
	plantMat := mesh.PlantMaterialFirst
	for _, tt := range []struct {
		name   string
		packed uint64
	}{
		{
			// face 6 落在非植物 material 上：着色器会把一块普通方块画成穿模斜板。
			"face6 非植物 material",
			mesh.Quad{X: 1, Y: 1, Z: 1, W: 1, H: 1, Face: mesh.FacePlantDiagA, Mat: plantMat}.Pack() &^
				(uint64(0xFFFF) << 23),
		},
		{
			// 保留位 13..19 非零。
			"植物保留位非零",
			mesh.Quad{X: 1, Y: 1, Z: 1, W: 1, H: 1, Face: mesh.FacePlantDiagB, Mat: plantMat}.Pack() | 1<<13,
		},
		{
			// 植物 material 落在轴向 face 上,而且还是一条贪心合并过的 5×4:
			// 着色器按 `face >= 6` 判别,会把它当普通方块画成一整块石板。
			// 这是 `face ∈ {6,7} ⟺ material ∈ 植物区间` 的**反方向**,只强制一半
			// 等于着色器那条"按 face 判别与按 material 判别等价"的前提不成立。
			"植物 material 配轴向 face",
			// Pack 现在同样拒绝这个组合,所以只能先打包一条 Mat=0 的合法 quad,
			// 再把植物 material 按位或进去。
			mesh.Quad{X: 1, Y: 2, Z: 3, W: 5, H: 4, Face: mesh.FacePosY}.Pack() | uint64(plantMat)<<23,
		},
		{
			// bit 63 必须永远空着。
			"占用 bit 63",
			mesh.Quad{X: 1, Y: 1, Z: 1, W: 1, H: 1, Face: mesh.FacePosY, Mat: 3}.Pack() | 1<<63,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("非法编码 %#016x 未被拒绝", tt.packed)
				}
			}()
			mesh.UnpackQuad(tt.packed)
		})
	}

	// 合法编码必须**不**被拒绝，否则上面三条只是在陈述「解包总是 panic」。
	legal := mesh.Quad{X: 2, Y: 3, Z: 4, W: 1, H: 1, Face: mesh.FacePlantDiagB, Mat: mesh.PlantMaterialLast, Back: true}
	if got := mesh.UnpackQuad(legal.Pack()); got != legal {
		t.Fatalf("合法植物 quad 往返不一致:\n实际 %+v\n期望 %+v", got, legal)
	}
}
