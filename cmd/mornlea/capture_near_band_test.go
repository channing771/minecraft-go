package main

import (
	"image"
	"testing"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/lod"
)

// graySolid 复用既有 solidNRGBA 生成一张全部像素同灰度的图,供近处不变
// 断言的合成图测试(solidNRGBA 定义在 visual_compare_test.go)。
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
// = atan(1 − 2r/64)。配合 camY = −140、inner=9、相机 block (0,·,0)
// (最近壳距 512)得壳截止仰角 atan(252/512) ≈ 26.2°,落在行 16
// (atan 0.5 ≈ 26.57°)与行 17(atan 0.469 ≈ 25.13°)之间且两侧留有
// 裕量——行 0..16 属于近处带(必须逐字节一致),行 17+ 属于远景带
// (允许差异)。
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
	// 边界的距离(0..32);内盘外相机(理论不可达)按 0 处理,断言只会
	// 更保守,不会漏判。
	camera := nearBandTestCamera(64)
	camera.Pos[0], camera.Pos[2] = 32, 32
	if got := lodMinShellDistance(camera.Pos, lod.TilePos{}, 1); got < 0 || got > 32 {
		t.Fatalf("inner=1 的最近壳距 = %v, want ∈ [0,32]", got)
	}
}

func TestNearBandGuardCutElevation(t *testing.T) {
	guard := newNearBandGuard(
		nearBandTestCamera(-140), lod.TilePos{X: 0, Z: 0}, 9, true,
	)
	if !guard.shellWired {
		t.Fatal("接线形态的 guard 必须标记 shellWired")
	}
	// 截止仰角 atan(256/512) = atan(0.5);高于它的行受保护。
	rows := guard.protectedRowCount(64, 64)
	// 行 16 仰角恰等于截止值(不严格大于,不受保护),行 0..15 受保护。
	if rows != 17 {
		t.Fatalf("受保护行数 = %d, want 17(截止线在行 16/17 之间)", rows)
	}
}

func TestNearBandGuardPassesWhenOnlyFarBandDiffers(t *testing.T) {
	guard := newNearBandGuard(
		nearBandTestCamera(-140), lod.TilePos{X: 0, Z: 0}, 9, true,
	)
	old, fresh := graySolid(64, 64, 40), graySolid(64, 64, 40)
	repaintRow(fresh, 17, 200) // 截止线之下的行:远景带,允许差异
	repaintRow(fresh, 40, 200) // 深入远景带
	repaintRow(fresh, 63, 200)
	if err := guard.assertUnchanged("scene", old, fresh); err != nil {
		t.Fatalf("远景带差异不应触发断言: %v", err)
	}
}

func TestNearBandGuardFailsWhenNearBandDiffers(t *testing.T) {
	guard := newNearBandGuard(
		nearBandTestCamera(-140), lod.TilePos{X: 0, Z: 0}, 9, true,
	)
	old, fresh := graySolid(64, 64, 40), graySolid(64, 64, 40)
	repaintRow(fresh, 16, 200)
	repaintRow(fresh, 0, 210)
	err := guard.assertUnchanged("scene", old, fresh)
	if err == nil {
		t.Fatal("近处带差异未被断言捕获")
	}
	want := "scene"
	if !containsSubstr(err.Error(), want) {
		t.Fatalf("错误信息应包含场景名 %q: %v", want, err)
	}
}

func TestNearBandGuardRequiresFullEqualityWithoutShell(t *testing.T) {
	guard := newNearBandGuard(
		nearBandTestCamera(-140), lod.TilePos{X: 0, Z: 0}, 9, false,
	)
	old, fresh := graySolid(64, 64, 40), graySolid(64, 64, 40)
	repaintRow(fresh, 63, 200) // 未接线 LOD:全图任意差异都不可接受
	if err := guard.assertUnchanged("scene", old, fresh); err == nil {
		t.Fatal("无壳形态下的差异未被断言捕获")
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
