package main

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/assets"
	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/worldgen"
)

// 杀死变异：遗漏场景、改变其顺序、种子/区块/时间/相机或绕过 mirror/mesher
// 都会改变此固定夹具或其可观察结果。
func TestCaptureOakGrove(t *testing.T) {
	if got := captureScenes[len(captureScenes)-1].Name; got != "oak-grove" {
		t.Fatalf("最后一个 capture 场景=%q，想要 oak-grove", got)
	}
	scene := captureScenes[len(captureScenes)-1]
	if scene.Prepare == nil || scene.Apply == nil {
		t.Fatalf("oak-grove 场景不完整: %+v", scene)
	}
	if scene.WarmupFrames != 8 {
		t.Fatalf("oak-grove 预热帧=%d，想要 8", scene.WarmupFrames)
	}

	mesher := client.NewMesher(assets.NewRegistry(), 1)
	t.Cleanup(mesher.Close)
	app := &application{mirror: client.NewMirror(), mesher: mesher}
	if err := scene.Prepare(app); err != nil {
		t.Fatalf("准备 oak-grove: %v", err)
	}

	generator := worldgen.New(42)
	counts := map[core.BlockID]int{}
	for z := int32(-1); z <= 1; z++ {
		for x := int32(-1); x <= 1; x++ {
			position := core.ChunkPos{X: x, Z: z}
			want := generator.GenerateChunk(position)
			gotHash, gotRevision, loaded := app.mirror.Hash(core.Overworld, position)
			if !loaded || gotRevision != 1 || gotHash != want.Hash() {
				t.Fatalf("chunk (%d,%d) hash/revision/loaded=(%x,%d,%v)，想要 (%x,1,true)",
					x, z, gotHash, gotRevision, loaded, want.Hash())
			}
			for y := int32(core.MinY); y < core.MaxY; y++ {
				for localZ := int32(0); localZ < core.SectionSize; localZ++ {
					for localX := int32(0); localX < core.SectionSize; localX++ {
						block, blockLoaded := app.mirror.BlockAt(core.Overworld, core.BlockPos{
							X: x*core.SectionSize + localX, Y: y, Z: z*core.SectionSize + localZ,
						})
						if !blockLoaded {
							t.Fatalf("oak-grove mirror 未加载 chunk=(%d,%d) 的方块", x, z)
						}
						counts[block]++
					}
				}
			}
		}
	}
	for _, block := range []core.BlockID{core.GrassID, core.OakLogID, core.LeavesID} {
		if counts[block] == 0 {
			t.Fatalf("oak-grove 缺少方块 %d", block)
		}
	}
	if got := mesher.Stats().DirtySections; got == 0 {
		t.Fatal("oak-grove 通过 mirror 装入后 mesher 没有 dirty section")
	}

	stateApp := &application{remotePlayers: client.NewRemotePlayers(), panel: &panelState{visible: true}}
	if err := scene.Apply(stateApp); err != nil {
		t.Fatalf("应用 oak-grove: %v", err)
	}
	cameraCell := core.BlockPos{
		X: int32(math.Floor(float64(stateApp.camera.Pos[0]))),
		Y: int32(math.Floor(float64(stateApp.camera.Pos[1]))),
		Z: int32(math.Floor(float64(stateApp.camera.Pos[2]))),
	}
	block, loaded := app.mirror.BlockAt(core.Overworld, cameraCell)
	if !loaded || block != core.AirID {
		t.Fatalf("oak-grove 相机格 %+v loaded/block=%v/%d，想要 true/%d",
			cameraCell, loaded, block, core.AirID)
	}
	hit, found, err := core.RaycastBlocks(
		stateApp.camera.Pos,
		stateApp.camera.Forward(),
		6,
		func(position core.BlockPos) (bool, error) {
			block, loaded := app.mirror.BlockAt(core.Overworld, position)
			if !loaded {
				t.Fatalf("oak-grove 射线命中未加载方块 %+v", position)
			}
			return block != core.AirID, nil
		},
	)
	if err != nil || found {
		t.Fatalf("oak-grove 6 格目标射线 hit/found/err=%+v/%v/%v，想要零值/false/nil",
			hit, found, err)
	}
	if stateApp.worldTimeTicks != 6000 ||
		stateApp.camera.Pos != (mgl32.Vec3{-3.5, 75.5, 12.5}) ||
		stateApp.camera.Yaw != 0 || stateApp.camera.Pitch != -0.38 {
		t.Fatalf("oak-grove 状态 time=%d camera=%+v yaw=%v pitch=%v",
			stateApp.worldTimeTicks, stateApp.camera.Pos, stateApp.camera.Yaw, stateApp.camera.Pitch)
	}
}
