package main

import (
	"fmt"
	"image"
	"math"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/lod"
)

// captureShellTopY 是远环壳内容在世界 Y 轴上的解析上界,用作近处不变
// 断言的截止推导输入:worldgen 地表高度 = 海平面 64 + fbm×振幅 48
// (见 internal/worldgen oracle 的 `oracleSeaLevel`/`oracleTerrainAmp`),
// |fbm| ≤ 1 故高度 ≤ 112;壳的断差裙边只向下延伸、不抬高上界。取解析
// 界而非实测最值,让断言对任意种子都成立——若未来 worldgen 振幅改变,
// 本常量必须同步。
const captureShellTopY = 64 + 48

// captureShellBottomY 是远环壳内容在世界 Y 轴上的解析下界(与上界
// 对称:海平面 64 − 振幅 48 = 16),供双侧保护的下行截止推导:仰角
// 低于 atan2(`captureShellBottomY` − camY, 距离) 的像素同样不可能包含
// 壳,近场地表(下半屏)由此受保护。
const captureShellBottomY = 64 - 48

// lodTileBlocks 是远环 tile 的世界边长(block),与 app_lod.go 的
// `lodTileChunks`×16 同值(4 chunk × 16)。client 渲染器的同名常量
// LOD_TILE_WORLD_BLOCKS 也是 64;这里只做距离推导,不镜像容量类契约,
// 数值耦合由 5.2 的半径推导测试锚定。
const lodTileBlocks = 64

// lodMinShellDistance 返回相机到远环壳最近可能的水平距离:排除内盘
// (切比雪夫 ≤ inner−1 的 tile)是 block 方块
// [64×(cx−inner+1), 64×(cx+inner))²,相机到壳区(内盘之外)的最近点
// 必在该方块某条边上,距离 = 相机到四边垂直距离的最小值。inner ≤ 0
// 或相机已在内盘之外(理论不可达:相机永远在自己的 tile 内)时返回 0,
// 由断言按退化形态拒绝(fail-closed),不会静默放宽。
func lodMinShellDistance(pos mgl32.Vec3, center lod.TilePos, inner int) float32 {
	if inner <= 0 {
		return 0
	}
	minX := float32((int64(center.X) - int64(inner) + 1) * lodTileBlocks)
	maxX := float32((int64(center.X) + int64(inner)) * lodTileBlocks)
	minZ := float32((int64(center.Z) - int64(inner) + 1) * lodTileBlocks)
	maxZ := float32((int64(center.Z) + int64(inner)) * lodTileBlocks)
	dx := min(pos.X()-minX, maxX-pos.X())
	dz := min(pos.Z()-minZ, maxZ-pos.Z())
	if dx < 0 || dz < 0 {
		return 0
	}
	return min(dx, dz)
}

// lodMaxShellDistance 返回相机到远环壳最远可能的欧氏距离:切比雪夫
// 半径 ≤ outer 的 tile 在每条轴上距离中心 tile 原点不超过
// (outer+1)×64 block(相机恒在中心 tile 内),对角方向取 √2 倍。
// 供下行截止在「相机低于壳下界」形态下取最小仰角之用(正高度差在
// 最远距离处仰角最小)。
func lodMaxShellDistance(outer int) float64 {
	if outer < 0 {
		return 0
	}
	return math.Sqrt2 * float64(int64(outer)+1) * lodTileBlocks
}

// `nearBandGuard` 是「近处像素不变」断言(spec delta「golden 更新仅限
// 远景带」)的可执行形态:golden 重生成前,同一当前 registry 的 LOD-off
// 与 LOD-on control 帧在受保护行上必须逐字节一致,差异只允许出现在远景带。
//
// 双侧保护都是纯几何推导、对任意种子成立。壳内容只出现在远环带
// (水平距离 ∈ [`lodMinShellDistance`, `lodMaxShellDistance`]),高度 ∈
// [`captureShellBottomY`, `captureShellTopY`],因此壳可能出现的仰角区间为
// (bottomCut, topCut]:
//
//   - 上行截止 topCut:相机低于壳上界时 = atan((shellTop − camY)/minDist)
//     (最近距离上的最高壳);相机不低于壳上界时壳恒在地平线之下、随
//     距离无限趋近地平线,上确界为 0。仰角严格大于 topCut 的像素不可能
//     包含壳(天空、云或高处的近环内容),LOD-off/on control 必须逐字节一致。
//   - 下行截止 bottomCut:相机高于壳下界时 = atan((shellBottom − camY)/
//     minDist)——负高度差在最近距离处仰角最低(注意:这里必须用
//     minDist 而不是 maxDist,最近处的最低壳片比远处的更低);相机
//     不高于壳下界时 = atan((shellBottom − camY)/maxDist)(正高度差
//     在最远处仰角最低)。仰角严格低于 bottomCut 的像素(下半屏的
//     近场地表)同样不可能包含壳,两张 control 帧必须逐字节一致——这正是修复循环
//     第 1 轮补上的半边:只保护顶部行时,内盘壳 poke 出地表遮挡近处
//     mesh 那类回归(差异遍布下半屏)会被静默固化进 golden。
//
// 相机无横滚(LookAt up=(0,1,0)),每行是等仰角线且仰角随行号单调
// 递减,受保护行因此是从顶部与底部各自开始的连续区间,中间的
// [topRows, height−bottomRows) 是唯一允许差异的真远景带。两个截止
// 恒满足 bottomCut < topCut(高度下界 ≤ 上界、minDist ≤ maxDist),
// 区间不重叠。未接线 LOD 的运行没有壳,全图都必须逐字节一致
// (LOD-off 对照实测即逐字节一致);壳距离推导退化(≤0)时无法证明
// 任何行无壳,按 fail-closed 拒绝更新基线。
type nearBandGuard struct {
	camera client.Camera
	// `shellDist` 是相机到壳的最近水平距离;>0 才参与截止推导。
	shellDist float32
	// `maxShellDist` 是相机到壳的最远欧氏距离,供下行截止使用。
	maxShellDist float64
	// `shellWired` 标记本次运行是否接线了远环(未接线时全图保护)。
	shellWired bool
}

// newNearBandGuard 按抓帧时的相机位姿与远环装配事实构造断言。center
// 是远环带中心 tile(字段 `lodTileCenter`),inner 是 `lodNearTileRadius` 推导的
// 内半径,outer 是 `lodFarTileRadius` 推导的远环外半径;`shellWired` 为假
// (禁用/benchmark 路径)时其余参数不参与。
func newNearBandGuard(
	camera client.Camera, center lod.TilePos, inner, outer int, shellWired bool,
) nearBandGuard {
	if !shellWired {
		return nearBandGuard{camera: camera, shellWired: false}
	}
	return nearBandGuard{
		camera:       camera,
		shellDist:    lodMinShellDistance(camera.Pos, center, inner),
		maxShellDist: lodMaxShellDistance(outer),
		shellWired:   true,
	}
}

// shellCutElevation 返回壳内容可能出现的最大仰角(弧度,水平线之上
// 为正)。推导见类型注释;调用方(`assertUnchanged`)已先行排除
// `shellDist` ≤ 0 的退化形态。
func (g nearBandGuard) shellCutElevation() float64 {
	rise := float64(captureShellTopY) - float64(g.camera.Pos.Y())
	if rise <= 0 {
		return 0
	}
	return math.Atan2(rise, float64(g.shellDist))
}

// shellBottomElevation 返回壳内容可能出现的最小仰角(弧度)。相机高于
// 壳下界时负高度差在最近距离处最低(minDist);相机不高于壳下界时
// 正高度差在最远距离处最低(maxDist)。推导见类型注释。
func (g nearBandGuard) shellBottomElevation() float64 {
	sink := float64(captureShellBottomY) - float64(g.camera.Pos.Y())
	if sink >= 0 {
		return math.Atan2(sink, g.maxShellDist)
	}
	return math.Atan2(sink, float64(g.shellDist))
}

// protectedRowSpans 返回受保护行区间:顶部 [0, topRows) 与底部
// [height−bottomRows, height)。相机无横滚,每行是等仰角线且仰角随
// 行号单调递减,因此两侧受保护行各自连续,从顶部/底部首个越界行即
// 停;每个方向每行只做一次矩阵乘法(64×360 图共 720 次,可忽略)。
// 未接线形态由 `assertUnchanged` 走全图比较,不经过本函数。
func (g nearBandGuard) protectedRowSpans(height int) (topRows, bottomRows int) {
	if height <= 0 {
		return 0, 0
	}
	topCut, bottomCut := g.shellCutElevation(), g.shellBottomElevation()
	// 逆投影取行方向向量:任意 NDC z 都落在同一视线上,取 0 即可;
	// 方向 = 逆投影像点 − 相机位置,与深度约定(GL [−1,1] 或 wgpu [0,1])
	// 无关。
	inverse := g.camera.ViewProj().Inv()
	elevation := func(y int) float64 {
		ndcY := 1 - 2*float32(y)/float32(height)
		point := mgl32.TransformCoordinate(mgl32.Vec3{0, ndcY, 0}, inverse)
		dir := point.Sub(g.camera.Pos)
		return math.Asin(float64(dir.Y()) / float64(dir.Len()))
	}
	for y := 0; y < height && elevation(y) > topCut; y++ {
		topRows = y + 1
	}
	for y := height - 1; y >= 0 && elevation(y) < bottomCut; y-- {
		bottomRows++
	}
	// 双侧区间按推导不重叠(bottomCut < topCut);防御钳制保证即便
	// 浮点异常也不会把中段挤出画面。
	if overlap := topRows + bottomRows - height; overlap > 0 {
		bottomRows -= overlap
	}
	return topRows, bottomRows
}

// `assertUnchanged` 断言当前同 registry 的 `lodOff` 与 `lodOn` control 帧在
// 受保护行(顶部仰角高于上行截止、底部仰角低于下行截止)上 RGB 逐字节一致
// (alpha 在抓帧时恒为 255,无信息量)。违反时返回带场景名、受保护区间与首个差异像素的错误;尺寸
// 不匹配同样视为违反(control 形态不等价);壳距离推导退化时 fail-closed
// 直接拒绝。
func (g nearBandGuard) assertUnchanged(scene string, lodOff, lodOn *image.NRGBA) error {
	if lodOff.Bounds() != lodOn.Bounds() {
		return fmt.Errorf("近处不变断言(%s): 图像尺寸不匹配 lodOff=%v lodOn=%v",
			scene, lodOff.Bounds(), lodOn.Bounds())
	}
	if !g.shellWired {
		diffPixels, firstX, firstY := compareNRGBARows(lodOff, lodOn, 0, lodOn.Bounds().Dy())
		if diffPixels > 0 {
			return fmt.Errorf(
				"近处不变断言(%s): 未接线 LOD 要求全图一致,差异像素 %d,首个在 (%d,%d)",
				scene, diffPixels, firstX, firstY)
		}
		return nil
	}
	if g.shellDist <= 0 {
		return fmt.Errorf(
			"近处不变断言(%s): 壳最近距离推导退化(shellDist=%v),无法证明任何行无壳,拒绝更新基线",
			scene, g.shellDist)
	}
	height := lodOn.Bounds().Dy()
	topRows, bottomRows := g.protectedRowSpans(height)
	diffPixels, firstX, firstY := compareNRGBARows(lodOff, lodOn, 0, topRows)
	if diffPixels == 0 && bottomRows > 0 {
		diffPixels, firstX, firstY = compareNRGBARows(lodOff, lodOn, height-bottomRows, height)
	}
	if diffPixels > 0 {
		return fmt.Errorf(
			"近处不变断言(%s): 受保护区间(顶部 %d 行 + 底部 %d 行)内差异像素 %d,首个在 (%d,%d)",
			scene, topRows, bottomRows, diffPixels, firstX, firstY)
	}
	return nil
}

// `compareNRGBARows` 在行区间 [y0, y1) 内逐像素比较 RGB,返回差异像素数
// 与首个差异位置(无差异时 firstX < 0)。
func compareNRGBARows(lodOff, lodOn *image.NRGBA, y0, y1 int) (diffPixels, firstX, firstY int) {
	firstX, firstY = -1, -1
	for y := y0; y < y1; y++ {
		for x := 0; x < lodOn.Bounds().Dx(); x++ {
			offIndex, onIndex := lodOff.PixOffset(x, y), lodOn.PixOffset(x, y)
			if lodOff.Pix[offIndex] != lodOn.Pix[onIndex] ||
				lodOff.Pix[offIndex+1] != lodOn.Pix[onIndex+1] ||
				lodOff.Pix[offIndex+2] != lodOn.Pix[onIndex+2] {
				diffPixels++
				if firstX < 0 {
					firstX, firstY = x, y
				}
			}
		}
	}
	return diffPixels, firstX, firstY
}
