package main

import (
	"image"
	"testing"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/lod"
)

// graySolid 复用既有 `solidNRGBA` 生成一张全部像素同灰度的图,供近处不变
// 断言的合成图测试(`solidNRGBA` 定义在 visual_compare_test.go)。
func graySolid(width, height int, value uint8) *image.NRGBA {
	return solidNRGBA(width, height, value, value, value)
}

// repaintRow 把一行像素改成另一颜色(模拟该行内容变化)。
func repaintRow(img *image.NRGBA, y int, value uint8) {
	for x := 0; x < img.Bounds().Dx(); x++ {
		i := img.PixOffset(x, y)
		img.Pix[i], img.Pix[i+1], img.Pix[i+2] = value, value, value
	}
}

// nearBandTestCamera 构造 64×64、pitch 0、FOV 90° 的相机:每行仰角
// = atan(1 − 2r/64)。
//
// 常规形态(posY = −140,相机低于壳下界 16):inner=9、outer=24、相机
// block (0,·,0) → 最近壳距 512、最远壳距 √2×25×64 ≈ 2262.7。上行截止
// = atan(252/512) ≈ 26.20°(rise = 112−(−140) = 252),落在行 16
// (atan 0.5 ≈ 26.57°,受保护)与行 17(atan 0.469 ≈ 25.13°)之间;
// 下行截止 = atan(156/2262.7) ≈ 3.94°(sink = 16−(−140) = 156,相机
// 低于壳下界故取最远距离),落在行 29(≈ 3.58°,远景带)与行 30
// (≈ 4.40°,受保护)之间。受保护区间 = 顶部行 0..16 + 底部行 30..63,
// 远景带 = 行 17..29。
//
// 高位形态(posY = 60,相机高于壳下界、低于壳上界,镜像真实出生点
// 相机):上行截止 = atan(52/512) ≈ 5.80°(行 29 ≈ 5.35° 为首个远景
// 带行);下行截止 = atan((16−60)/512) ≈ −4.92°(行 35 ≈ −5.35° 为
// 首个底部受保护行)。远景带 = 行 29..34,底部行 35..63 是近场地表。
func nearBandTestCamera(posY float32) client.Camera {
	return client.Camera{
		Pos:    [3]float32{0, posY, 0},
		Yaw:    0,
		Pitch:  0,
		FovY:   90 * (3.141592653589793 / 180),
		Aspect: 1,
		// Near/Far 必须合法:透视矩阵对 near=0 会除零退化,逆投影随之
		// 失真,仰角全部不可信。
		Near: 0.1,
		Far:  1000,
	}
}

func TestLodMinShellDistance(t *testing.T) {
	const inner = 9
	cases := []struct {
		name   string
		pos    [3]float32
		center lod.TilePos
		want   float32
	}{
		// 排除内盘(切比雪夫 ≤8 的 tile)覆盖 block 方块 [−512, 576)²;
		// 相机到壳区的最近水平距离 = 到内盘四边的垂直距离最小值。
		{"近西缘", [3]float32{0, 64, 0}, lod.TilePos{X: 0, Z: 0}, 512},
		// 两轴都参与:dx = 576−575 = 1、dz = −500−(−512) = 12,取 min = 1。
		{"偏东北", [3]float32{575, 64, -500}, lod.TilePos{X: 0, Z: 0}, 1},
		{"非原点环心", [3]float32{64, 64, 64}, lod.TilePos{X: 1, Z: 1}, 512},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			camera := nearBandTestCamera(64)
			camera.Pos[0], camera.Pos[2] = testCase.pos[0], testCase.pos[2]
			got := lodMinShellDistance(camera.Pos, testCase.center, inner)
			if diff := got - testCase.want; diff > 0.5 || diff < -0.5 {
				t.Fatalf("lodMinShellDistance(%v, %v, %d) = %v, want %v",
					camera.Pos, testCase.center, inner, got, testCase.want)
			}
		})
	}
	// inner ≤ 1 时相机所在 tile 即被排除,最近壳距退化为相机到自身 tile
	// 边界的距离(0..32);内盘外相机(理论不可达)按 0 处理,断言按
	// 退化形态 fail-closed 拒绝,不会静默放宽。
	camera := nearBandTestCamera(64)
	camera.Pos[0], camera.Pos[2] = 32, 32
	if got := lodMinShellDistance(camera.Pos, lod.TilePos{}, 1); got < 0 || got > 32 {
		t.Fatalf("inner=1 的最近壳距 = %v, want ∈ [0,32]", got)
	}
}

func TestLodMaxShellDistance(t *testing.T) {
	// outer=24:每轴最远 (24+1)×64 = 1600,对角 √2×1600 ≈ 2262.7。
	got := lodMaxShellDistance(24)
	if want := 1.4142135623730951 * 25 * 64; got < want-0.5 || got > want+0.5 {
		t.Fatalf("lodMaxShellDistance(24) = %v, want %v", got, want)
	}
	if got := lodMaxShellDistance(-1); got != 0 {
		t.Fatalf("负外半径 = %v, want 0", got)
	}
}

func TestNearBandGuardCutElevation(t *testing.T) {
	guard := newNearBandGuard(
		nearBandTestCamera(-140), lod.TilePos{X: 0, Z: 0}, 9, 24, true,
	)
	if !guard.shellWired {
		t.Fatal("接线形态的 guard 必须标记 shellWired")
	}
	topRows, bottomRows := guard.protectedRowSpans(64)
	// 上行截止 atan(252/512) ≈ 26.20° 在行 16/17 之间 → 顶部受保护
	// 行 0..16;下行截止 atan(156/2262.7) ≈ 3.94° 在行 29/30 之间 →
	// 底部受保护行 30..63。
	if topRows != 17 {
		t.Fatalf("顶部受保护行数 = %d, want 17(截止线在行 16/17 之间)", topRows)
	}
	if bottomRows != 34 {
		t.Fatalf("底部受保护行数 = %d, want 34(截止线在行 29/30 之间)", bottomRows)
	}
}

func TestNearBandGuardPassesWhenOnlyFarBandDiffers(t *testing.T) {
	guard := newNearBandGuard(
		nearBandTestCamera(-140), lod.TilePos{X: 0, Z: 0}, 9, 24, true,
	)
	old, fresh := graySolid(64, 64, 40), graySolid(64, 64, 40)
	repaintRow(fresh, 17, 200) // 远景带上缘(仰角 25.13° < 上行截止)
	repaintRow(fresh, 24, 200) // 远景带中段
	repaintRow(fresh, 29, 200) // 远景带下缘(仰角 3.58° < 下行截止)
	if err := guard.assertUnchanged("scene", old, fresh); err != nil {
		t.Fatalf("远景带差异不应触发断言: %v", err)
	}
}

// TestNearBandGuardRejectsNearGroundRegression 镜像本任务 BLOCKED 阶段的
// 真实回归形态:内盘壳 poke 出地表、以更近深度遮挡近处 mesh,差异遍布
// 下半屏近场地表(修复前的单侧断言会静默放行并把回归固化进 golden)。
// 高位相机(60,眼平线上下的近景在下半屏)+ 底部行重绘 → 必须被拒绝。
func TestNearBandGuardRejectsNearGroundRegression(t *testing.T) {
	guard := newNearBandGuard(
		nearBandTestCamera(60), lod.TilePos{X: 0, Z: 0}, 9, 24, true,
	)
	topRows, bottomRows := guard.protectedRowSpans(64)
	// 上行截止 atan(52/512) ≈ 5.80° → 顶部行 0..28;下行截止
	// atan(−44/512) ≈ −4.92° → 底部行 35..63;远景带行 29..34。
	if topRows != 29 || bottomRows != 29 {
		t.Fatalf("受保护区间 = 顶部 %d + 底部 %d, want 29 + 29", topRows, bottomRows)
	}
	old, fresh := graySolid(64, 64, 40), graySolid(64, 64, 40)
	repaintRow(fresh, 50, 200) // 下半屏近场地表被壳遮挡类差异
	repaintRow(fresh, 63, 210) // 画面最底行
	err := guard.assertUnchanged("scene", old, fresh)
	if err == nil {
		t.Fatal("下半屏近场地表差异未被双侧断言捕获(BLOCKED 回归会复发)")
	}
	// 同一相机下,真正的远景带(行 29..34)差异仍被放行。
	old2, fresh2 := graySolid(64, 64, 40), graySolid(64, 64, 40)
	repaintRow(fresh2, 32, 200)
	if err := guard.assertUnchanged("scene", old2, fresh2); err != nil {
		t.Fatalf("远景带差异不应触发断言: %v", err)
	}
}

func TestNearBandGuardFailsWhenNearBandDiffers(t *testing.T) {
	guard := newNearBandGuard(
		nearBandTestCamera(60), lod.TilePos{X: 0, Z: 0}, 9, 24, true,
	)
	old, fresh := graySolid(64, 64, 40), graySolid(64, 64, 40)
	repaintRow(fresh, 10, 200) // 顶部受保护区(天空/高处近景)
	repaintRow(fresh, 0, 210)
	err := guard.assertUnchanged("scene", old, fresh)
	if err == nil {
		t.Fatal("顶部近处带差异未被断言捕获")
	}
	if !containsSubstr(err.Error(), "scene") {
		t.Fatalf("错误信息应包含场景名 %q: %v", "scene", err)
	}
}

func TestNearBandGuardRequiresFullEqualityWithoutShell(t *testing.T) {
	guard := newNearBandGuard(
		nearBandTestCamera(-140), lod.TilePos{X: 0, Z: 0}, 9, 24, false,
	)
	old, fresh := graySolid(64, 64, 40), graySolid(64, 64, 40)
	repaintRow(fresh, 63, 200) // 未接线 LOD:全图任意差异都不可接受
	if err := guard.assertUnchanged("scene", old, fresh); err == nil {
		t.Fatal("无壳形态下的差异未被断言捕获")
	}
}

// TestNearBandGuardFailsClosedOnDegenerateShellDistance 锁住退化形态的
// fail-closed:相机落在排除内盘之外(理论不可达)时壳最近距离推导为 0,
// 无法证明任何行无壳——必须拒绝更新基线,而不是静默放宽保护。
func TestNearBandGuardFailsClosedOnDegenerateShellDistance(t *testing.T) {
	camera := nearBandTestCamera(64)
	camera.Pos[0], camera.Pos[2] = 2000, 0 // 内盘外 → `shellDist` = 0
	guard := newNearBandGuard(camera, lod.TilePos{}, 9, 24, true)
	old, fresh := graySolid(64, 64, 40), graySolid(64, 64, 40)
	err := guard.assertUnchanged("scene", old, fresh)
	if err == nil {
		t.Fatal("退化形态必须 fail-closed 拒绝,即便两图逐字节一致")
	}
	if !containsSubstr(err.Error(), "退化") {
		t.Fatalf("错误信息应说明退化原因: %v", err)
	}
}

func containsSubstr(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
