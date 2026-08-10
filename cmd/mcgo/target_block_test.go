//go:build darwin

package main

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/client"
	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/world"
)

func TestCurrentBlockTargetHitsRegisteredBlockWithinSixBlocks(t *testing.T) {
	app := newTargetBlockApplication(t, true, core.ChunkPos{}, core.ChunkPos{Z: -1})
	position := core.BlockPos{X: 0, Y: 3, Z: -3}
	setTargetMirrorBlock(t, app.mirror, position, core.BrickID)

	got, ok := app.currentBlockTarget()
	want := blockTarget{Position: position, Name: "砖块"}
	if !ok || got != want {
		t.Fatalf("currentBlockTarget() = %+v, %v，想要 %+v, true", got, ok, want)
	}
}

func TestCurrentBlockTargetRejectsInvalidTargetsAndUI(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T) *application
	}{
		{
			name: "超过六格",
			setup: func(t *testing.T) *application {
				app := newTargetBlockApplication(t, true, core.ChunkPos{}, core.ChunkPos{Z: -1})
				setTargetMirrorBlock(t, app.mirror, core.BlockPos{X: 0, Y: 3, Z: -5}, core.BrickID)
				return app
			},
		},
		{
			name: "路径中缺失区块",
			setup: func(t *testing.T) *application {
				app := newTargetBlockApplication(t, true, core.ChunkPos{Z: -1})
				app.camera.Pos = mgl32.Vec3{0.5, 3.5, 0.5}
				setTargetMirrorBlock(t, app.mirror, core.BlockPos{X: 0, Y: 3, Z: -1}, core.BrickID)
				return app
			},
		},
		{
			name: "未知方块阻断路径",
			setup: func(t *testing.T) *application {
				app := newTargetBlockApplication(t, true, core.ChunkPos{}, core.ChunkPos{Z: -1})
				setTargetMirrorBlock(t, app.mirror, core.BlockPos{X: 0, Y: 3, Z: 0}, core.MossyCobblestoneID+1)
				setTargetMirrorBlock(t, app.mirror, core.BlockPos{X: 0, Y: 3, Z: -1}, core.BrickID)
				return app
			},
		},
		{
			name: "全空气",
			setup: func(t *testing.T) *application {
				return newTargetBlockApplication(t, true, core.ChunkPos{}, core.ChunkPos{Z: -1})
			},
		},
		{
			name: "Predictor 未就绪",
			setup: func(t *testing.T) *application {
				app := newTargetBlockApplication(t, false, core.ChunkPos{}, core.ChunkPos{Z: -1})
				setTargetMirrorBlock(t, app.mirror, core.BlockPos{X: 0, Y: 3, Z: -3}, core.BrickID)
				return app
			},
		},
		{
			name: "背包打开",
			setup: func(t *testing.T) *application {
				app := targetBlockHitApplication(t)
				app.inventoryOpen = true
				return app
			},
		},
		{
			name: "熔炉打开",
			setup: func(t *testing.T) *application {
				app := targetBlockHitApplication(t)
				if err := app.furnace.Apply(network.FurnaceState{Furnace: core.FurnaceRef{Generation: 1}}); err != nil {
					t.Fatal(err)
				}
				return app
			},
		},
		{
			name: "箱子打开",
			setup: func(t *testing.T) *application {
				app := targetBlockHitApplication(t)
				if err := app.chest.Apply(network.ChestState{Chest: core.ContainerRef{Kind: core.ContainerKindChest, Generation: 1}}); err != nil {
					t.Fatal(err)
				}
				return app
			},
		},
		{
			name: "调试面板可见",
			setup: func(t *testing.T) *application {
				app := targetBlockHitApplication(t)
				app.panel = &panelState{visible: true}
				return app
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got, ok := test.setup(t).currentBlockTarget(); ok || got != (blockTarget{}) {
				t.Fatalf("currentBlockTarget() = %+v, %v，想要零值, false", got, ok)
			}
		})
	}
}

func targetBlockHitApplication(t *testing.T) *application {
	t.Helper()
	app := newTargetBlockApplication(t, true, core.ChunkPos{}, core.ChunkPos{Z: -1})
	setTargetMirrorBlock(t, app.mirror, core.BlockPos{X: 0, Y: 3, Z: -3}, core.BrickID)
	return app
}

func newTargetBlockApplication(t *testing.T, ready bool, chunks ...core.ChunkPos) *application {
	t.Helper()
	app := &application{
		mirror:    client.NewMirror(),
		predictor: client.NewPredictor(),
		camera:    client.Camera{Pos: mgl32.Vec3{0.5, 3.5, 2.5}},
	}
	for _, position := range chunks {
		applyTargetMirrorChunk(t, app.mirror, world.NewChunk(position))
	}
	if ready {
		if err := app.predictor.Begin(network.PlayerState{
			ServerTick: 1,
			Dimension:  core.Overworld,
			Ready:      true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	return app
}

func applyTargetMirrorChunk(t *testing.T, mirror *client.Mirror, chunk *world.Chunk) {
	t.Helper()
	sections := make([]network.SectionData, core.SectionsPerChunk)
	for index := range sections {
		snapshot := chunk.Section(index).Blocks.Snapshot()
		sections[index] = network.SectionData{
			Y:       int32(index),
			Storage: network.SectionStorage(snapshot.Kind),
			Single:  snapshot.Single,
			Bits:    snapshot.Bits,
			Palette: append([]core.BlockID(nil), snapshot.Palette...),
			Packed:  append([]uint64(nil), snapshot.Packed...),
		}
	}
	if _, err := mirror.Apply(network.ChunkSnapshot{
		Dimension: core.Overworld,
		Chunk:     chunk.Pos,
		Revision:  1,
		Sections:  sections,
	}); err != nil {
		t.Fatal(err)
	}
}

func setTargetMirrorBlock(t *testing.T, mirror *client.Mirror, position core.BlockPos, id core.BlockID) {
	t.Helper()
	chunk, ok := mirror.Chunk(core.Overworld, position.Chunk())
	if !ok {
		t.Fatalf("测试区块 %+v 未加载", position.Chunk())
	}
	x, _, z := position.Local()
	chunk.Chunk.SetBlock(x, position.Y, z, id)
}
