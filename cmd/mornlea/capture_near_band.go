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
// (见 internal/worldgen oracle 的 oracleSeaLevel/oracleTerrainAmp),
// |fbm| ≤ 1 故高度 ≤ 112;壳的断差裙边只向下延伸,不抬高上界。取解析
// 上界而非实测最大值,让断言对任意种子都成立——若未来 worldgen 振幅
// 改变,本常量必须同步。
const captureShellTopY = 64 + 48

// lodTileBlocks 是远环 tile 的世界边长(block),与 app_lod.go 的
// lodTileChunks×16 同值(4 chunk × 16)。client 渲染器的同名常量
// LOD_TILE_WORLD_BLOCKS 也是 64;这里只做距离推导,不镜像容量类契约,
// 数值耦合由 5.2 的半径推导测试锚定。
const lodTileBlocks = 64

// lodMinShellDistance 返回相机到远环壳最近可能的水平距离:排除内盘
// (切比雪夫 ≤ inner−1 的 tile)是 block 方块
// [64×(cx−inner+1), 64×(cx+inner))²,相机到壳区(内盘之外)的最近点
// 必在该方块某条边上,距离 = 相机到四边垂直距离的最小值。inner ≤ 0
// 或相机已在内盘之外(理论不可达:相机永远在自己的 tile 内)时返回 0,
// 让断言退化到最保守形态,不会漏判。
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

// nearBandGuard 是「近处像素不变」断言(spec delta「golden 更新仅限
// 远景带」)的可执行形态:golden 重生成时,新帧与旧基线在受保护行上
// 必须逐字节一致,差异只允许出现在远景带。
//
// 受保护行的推导是纯几何的、对任意种子成立:壳内容只出现在远环带
// (水平距离 ≥ lodMinShellDistance),高度 ≤ captureShellTopY,因此壳
// 可能出现的最大仰角为 cut = atan((shellTop − camY)/minShellDist);
// 仰角严格大于 cut 的像素不可能包含壳(只可能是天空、云或近环内容,
// 三者都不受 LOD 影响),必须与旧基线逐字节一致。相机高于壳上界时壳
// 恒在地平线之下、但随距离无限趋近地平线,上确界为 0,此时保护全部
// 仰角 ≥ 0 的像素。未接线 LOD 的运行没有壳,全图都必须逐字节一致
// (LOD-off 对照实测即逐字节一致)。
type nearBandGuard struct {
	camera client.Camera
	// shellDist 是相机到壳的最近水平距离;>0 才参与截止推导。
	shellDist float32
	// shellWired 标记本次运行是否接线了远环(未接线时全图保护)。
	shellWired bool
}

// newNearBandGuard 按抓帧时的相机位姿与远环装配事实构造断言。center
// 是远环带中心 tile(a.lodTileCenter),inner 是 lodNearTileRadius 推导的
// 内半径;shellWired 为假(禁用/benchmark 路径)时其余参数不参与。
func newNearBandGuard(
	camera client.Camera, center lod.TilePos, inner int, shellWired bool,
) nearBandGuard {
	if !shellWired {
		return nearBandGuard{camera: camera, shellWired: false}
	}
	return nearBandGuard{
		camera:     camera,
		shellDist:  lodMinShellDistance(camera.Pos, center, inner),
		shellWired: true,
	}
}

// shellCutElevation 返回壳内容可能出现的最大仰角(弧度,水平线之上
// 为正)。推导见类型注释;shellDist 非法(≤0)时返回 π/2,即只有天顶
// 附近受保护——最保守的合法退化。
func (g nearBandGuard) shellCutElevation() float64 {
	rise := float64(captureShellTopY) - float64(g.camera.Pos.Y())
	if rise <= 0 {
		return 0
	}
	if g.shellDist <= 0 {
		return math.Pi / 2
	}
	return math.Atan2(rise, float64(g.shellDist))
}

// protectedRowCount 返回从画面顶部起连续受保护的行数。相机无横滚
// (LookAt up=(0,1,0)),每行是等仰角线且仰角随行号单调递减,因此
// 受保护行是从顶部开始的连续区间,逐行求仰角到首个不满足者为止。
func (g nearBandGuard) protectedRowCount(width, height int) int {
	if width <= 0 || height <= 0 {
		return 0
	}
	if !g.shellWired {
		return height
	}
	cut := g.shellCutElevation()
	// 逆投影取行方向向量:任意 NDC z 都落在同一视线上,取 0 即可;
	// 方向 = 逆投影像点 − 相机位置,与深度约定(GL [−1,1] 或 wgpu [0,1])
	// 无关。每行只算一次(64×360 图共 360 次矩阵乘法,可忽略)。
	inverse := g.camera.ViewProj().Inv()
	for y := 0; y < height; y++ {
		ndcY := 1 - 2*float32(y)/float32(height)
		point := mgl32.TransformCoordinate(mgl32.Vec3{0, ndcY, 0}, inverse)
		dir := point.Sub(g.camera.Pos)
		elevation := math.Asin(float64(dir.Y()) / float64(dir.Len()))
		if elevation <= cut {
			return y
		}
	}
	return height
}

// assertUnchanged 断言 fresh 与 old 在受保护行上 RGB 逐字节一致(alpha
// 在抓帧时恒为 255,无信息量)。违反时返回带场景名、受保护行数与首个
// 差异像素的错误;尺寸不匹配同样视为违反(基线形态变了必须显式重立)。
func (g nearBandGuard) assertUnchanged(scene string, old, fresh *image.NRGBA) error {
	if old.Bounds() != fresh.Bounds() {
		return fmt.Errorf("近处不变断言(%s): 图像尺寸不匹配 old=%v fresh=%v",
			scene, old.Bounds(), fresh.Bounds())
	}
	rows := g.protectedRowCount(fresh.Bounds().Dx(), fresh.Bounds().Dy())
	firstX, firstY, diffPixels := -1, -1, 0
	for y := 0; y < rows; y++ {
		for x := 0; x < fresh.Bounds().Dx(); x++ {
			oi, fi := old.PixOffset(x, y), fresh.PixOffset(x, y)
			if old.Pix[oi] != fresh.Pix[fi] ||
				old.Pix[oi+1] != fresh.Pix[fi+1] ||
				old.Pix[oi+2] != fresh.Pix[fi+2] {
				diffPixels++
				if firstX < 0 {
					firstX, firstY = x, y
				}
			}
		}
	}
	if diffPixels > 0 {
		return fmt.Errorf(
			"近处不变断言(%s): 受保护前 %d 行内差异像素 %d,首个在 (%d,%d)",
			scene, rows, diffPixels, firstX, firstY)
	}
	return nil
}
