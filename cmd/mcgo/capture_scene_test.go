package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/assets"
	"minecraft-go/internal/client"
	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
)

func TestCaptureSkylightTunnelFixtureUsesMirrorAndMesher(t *testing.T) {
	mesher := client.NewMesher(assets.NewRegistry(), 1)
	t.Cleanup(mesher.Close)
	app := &application{mirror: client.NewMirror(), mesher: mesher}

	if err := prepareSkylightTunnel(app); err != nil {
		t.Fatal(err)
	}
	for z := int32(-1); z <= 1; z++ {
		for x := int32(-1); x <= 1; x++ {
			chunk, ok := app.mirror.Chunk(core.Overworld, core.ChunkPos{X: x, Z: z})
			if !ok || chunk.Revision != 2 {
				t.Fatalf("chunk (%d,%d) = (%v,%v)，想要 revision 2", x, z, chunk, ok)
			}
		}
	}
	for _, tc := range []struct {
		name     string
		position core.BlockPos
		want     core.BlockID
	}{
		{name: "入口露天", position: core.BlockPos{X: 0, Y: 5, Z: 4}, want: core.AirID},
		{name: "入口地面", position: core.BlockPos{X: 0, Y: 0, Z: 4}, want: core.StoneID},
		{name: "入口侧墙", position: core.BlockPos{X: -3, Y: 2, Z: 4}, want: core.StoneID},
		{name: "入口后屋顶", position: core.BlockPos{X: 0, Y: 5, Z: 3}, want: core.StoneID},
		{name: "深处空气", position: core.BlockPos{X: 0, Y: 2, Z: -15}, want: core.AirID},
		{name: "通道后墙", position: core.BlockPos{X: 0, Y: 2, Z: -16}, want: core.StoneID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, loaded := app.mirror.BlockAt(core.Overworld, tc.position)
			if !loaded || got != tc.want {
				t.Fatalf("BlockAt(%+v) = (%d,%v)，想要 (%d,true)", tc.position, got, loaded, tc.want)
			}
		})
	}
	if got := app.mesher.Stats().DirtySections; got != 9*core.SectionsPerChunk {
		t.Fatalf("dirty sections = %d，想要 %d", got, 9*core.SectionsPerChunk)
	}
}

func TestCaptureSettled(t *testing.T) {
	tests := []struct {
		name    string
		stats   client.MesherStats
		pending int
		want    bool
	}{
		{name: "全部归零", want: true},
		{name: "仍有 dirty", stats: client.MesherStats{DirtySections: 1}},
		{name: "仍有 queued", stats: client.MesherStats{QueuedJobs: 1}},
		{name: "仍有 in-flight", stats: client.MesherStats{InFlightJobs: 1}},
		{name: "仍有 ready", stats: client.MesherStats{ReadyResults: 1}},
		{name: "仍有上传", pending: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := captureSettled(tc.stats, tc.pending); got != tc.want {
				t.Fatalf("captureSettled(%+v, %d) = %v，想要 %v", tc.stats, tc.pending, got, tc.want)
			}
		})
	}
}

func TestCaptureSkylightTunnelSceneFixesPresentationState(t *testing.T) {
	var scene captureScene
	for _, candidate := range captureScenes {
		if candidate.Name == "skylight-tunnel" {
			scene = candidate
			break
		}
	}
	if scene.Name == "" || scene.Prepare == nil {
		t.Fatal("capture 场景缺少完整 skylight-tunnel")
	}
	t.Run("空远端玩家列表", func(t *testing.T) {
		app := &application{remotePlayers: client.NewRemotePlayers()}
		if err := scene.Apply(app); err != nil {
			t.Fatalf("空列表应用场景: %v", err)
		}
	})

	remotePlayers := client.NewRemotePlayers()
	for index, playerID := range []core.PlayerID{
		{6: 0x40, 8: 0x80, 15: 1},
		{0: 0x12, 6: 0x40, 8: 0x80, 15: 2},
	} {
		if err := remotePlayers.Apply(network.RemotePlayerSpawn{
			PlayerID: playerID, DisplayName: "测试Player", ServerTick: uint64(index + 1),
			Position: mgl32.Vec3{0.5, 2, 0.5},
		}); err != nil {
			t.Fatal(err)
		}
	}
	app := &application{
		remotePlayers: remotePlayers,
		panel:         &panelState{visible: true},
		inventoryOpen: true,
	}
	inventory := core.Inventory{}
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 1}
	if err := app.inventory.Apply(network.InventoryState{Inventory: inventory}); err != nil {
		t.Fatal(err)
	}

	if err := scene.Apply(app); err != nil {
		t.Fatal(err)
	}
	if app.worldTimeTicks != 6000 {
		t.Fatalf("world time = %d，想要 6000", app.worldTimeTicks)
	}
	if app.camera.Pos != (mgl32.Vec3{0.5, 2.8, 8.5}) || app.camera.Yaw != 0 || app.camera.Pitch != -0.04 {
		t.Fatalf("camera = %+v yaw=%v pitch=%v", app.camera.Pos, app.camera.Yaw, app.camera.Pitch)
	}
	if got, confirmed := app.inventory.State(); !confirmed || got != (core.Inventory{}) {
		t.Fatalf("inventory = %+v confirmed=%v，想要已确认空物品栏", got, confirmed)
	}
	if got := remotePlayers.Presentations(); len(got) != 0 {
		t.Fatalf("远端玩家未清空: %+v", got)
	}
	if app.inventoryOpen || app.panel.visible {
		t.Fatalf("上个场景的界面状态未清空: inventoryOpen=%v panelVisible=%v",
			app.inventoryOpen, app.panel.visible)
	}
}

func TestBlockLightRoomIsLastCaptureScene(t *testing.T) {
	got := captureScenes[len(captureScenes)-1]
	if got.Name != "block-light-room" || got.Prepare == nil || got.Apply == nil {
		t.Fatalf("末场景=%+v，想要完整 block-light-room", got)
	}
}

func TestPrepareBlockLightRoomUsesMirrorAndMesher(t *testing.T) {
	airMesher := client.NewMesher(assets.NewRegistry(), 1)
	t.Cleanup(airMesher.Close)
	app := &application{mirror: client.NewMirror(), mesher: airMesher}

	if err := prepareCaptureAirNeighborhood(app); err != nil {
		t.Fatal(err)
	}
	roomMesher := client.NewMesher(assets.NewRegistry(), 1)
	t.Cleanup(roomMesher.Close)
	app.mesher = roomMesher
	if got := roomMesher.Stats().DirtySections; got != 0 {
		t.Fatalf("施加房间变化前 dirty sections = %d，想要 0", got)
	}
	if err := applyCaptureBlockLightRoomChanges(app); err != nil {
		t.Fatal(err)
	}
	for z := int32(-1); z <= 1; z++ {
		for x := int32(-1); x <= 1; x++ {
			chunk, ok := app.mirror.Chunk(core.Overworld, core.ChunkPos{X: x, Z: z})
			if !ok || chunk.Revision != 2 {
				t.Fatalf("chunk (%d,%d) = (%v,%v)，想要 revision 2", x, z, chunk, ok)
			}
		}
	}
	for y := int32(0); y <= 6; y++ {
		for z := int32(-10); z <= 2; z++ {
			for x := int32(-6); x <= 6; x++ {
				position := core.BlockPos{X: x, Y: y, Z: z}
				want := core.AirID
				if y == 0 || y == 6 || x == -6 || x == 6 || z == -10 || z == 2 {
					want = core.StoneID
				}
				if position == (core.BlockPos{X: 0, Y: 3, Z: -4}) {
					want = core.LightBlockID
				}
				got, loaded := app.mirror.BlockAt(core.Overworld, position)
				if !loaded || got != want {
					t.Fatalf("BlockAt(%+v) = (%d,%v)，想要 (%d,true)", position, got, loaded, want)
				}
			}
		}
	}
	for _, position := range []core.BlockPos{
		{X: -7, Y: 3, Z: -4},
		{X: 7, Y: 3, Z: -4},
		{X: 0, Y: 3, Z: -11},
		{X: 0, Y: 3, Z: 3},
	} {
		if got, loaded := app.mirror.BlockAt(core.Overworld, position); !loaded || got != core.AirID {
			t.Fatalf("房外 BlockAt(%+v) = (%d,%v)，想要 (AirID,true)", position, got, loaded)
		}
	}
	// 空列顶 -65 到地板 y=0 的变化加传播半径 16，覆盖 Y=-64..16，
	// 即每个已加载邻区 6 个 section；房间后续方块不会扩大这个范围。
	const wantDirtySections = 9 * 6
	if got := roomMesher.Stats().DirtySections; got != wantDirtySections {
		t.Fatalf("dirty sections = %d，想要 %d", got, wantDirtySections)
	}
}

func TestBlockLightRoomApplyResetsSharedPresentationState(t *testing.T) {
	scene := captureScenes[len(captureScenes)-1]
	remotePlayers := client.NewRemotePlayers()
	if err := remotePlayers.Apply(network.RemotePlayerSpawn{
		PlayerID: core.PlayerID{6: 0x40, 8: 0x80, 15: 1}, DisplayName: "测试Player",
		ServerTick: 1, Position: mgl32.Vec3{0.5, 2, 0.5},
	}); err != nil {
		t.Fatal(err)
	}
	app := &application{
		remotePlayers: remotePlayers,
		panel:         &panelState{visible: true},
		inventoryOpen: true,
	}
	inventory := core.Inventory{}
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 1}
	if err := app.inventory.Apply(network.InventoryState{Inventory: inventory}); err != nil {
		t.Fatal(err)
	}
	if err := app.furnace.Apply(network.FurnaceState{Furnace: core.FurnaceRef{
		Dimension: core.Overworld, Generation: 1,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := app.chest.Apply(network.ChestState{Chest: core.ContainerRef{
		Dimension: core.Overworld, Kind: core.ContainerKindChest, Generation: 1,
	}}); err != nil {
		t.Fatal(err)
	}

	if err := scene.Apply(app); err != nil {
		t.Fatal(err)
	}
	if app.worldTimeTicks != 18000 {
		t.Fatalf("world time = %d，想要 18000", app.worldTimeTicks)
	}
	if app.camera.Pos != (mgl32.Vec3{0.5, 2.8, 0.5}) || app.camera.Yaw != 0 || app.camera.Pitch != 0 {
		t.Fatalf("camera = %+v yaw=%v pitch=%v", app.camera.Pos, app.camera.Yaw, app.camera.Pitch)
	}
	if got, confirmed := app.inventory.State(); !confirmed || got != (core.Inventory{}) {
		t.Fatalf("inventory = %+v confirmed=%v，想要已确认空物品栏", got, confirmed)
	}
	if got := app.remotePlayers.Presentations(); len(got) != 0 {
		t.Fatalf("远端玩家未清空: %+v", got)
	}
	if _, opened := app.furnace.State(); opened {
		t.Fatal("熔炉状态未清空")
	}
	if _, opened := app.chest.State(); opened {
		t.Fatal("箱子状态未清空")
	}
	if app.inventoryOpen || app.panel.visible {
		t.Fatalf("共享界面状态未清空: inventoryOpen=%v panelVisible=%v",
			app.inventoryOpen, app.panel.visible)
	}
}

func TestCaptureSkylightTunnelUnsettledErrorNamesScene(t *testing.T) {
	app, _ := newRemoteRenderApplication(t, &integrationGlyphSource{})
	sections := make([]network.SectionData, core.SectionsPerChunk)
	for y := range sections {
		sections[y] = network.SectionData{
			Y: int32(y), Storage: network.SectionSingle, Single: core.AirID,
		}
	}
	oldTimeout := captureSettleTimeout
	captureSettleTimeout = 0
	t.Cleanup(func() { captureSettleTimeout = oldTimeout })
	scene := captureScene{
		Name: "skylight-tunnel",
		Prepare: func(app *application) error {
			update, err := app.mirror.Apply(network.ChunkSnapshot{
				Dimension: core.Overworld, Revision: 1, Sections: sections,
			})
			if err != nil {
				return err
			}
			release := app.mesher.BlockForTest(update.Dirty[0])
			t.Cleanup(release)
			app.mesher.MarkDirty(update.Dirty...)
			return nil
		},
		Apply: func(*application) error { return nil },
	}
	dir := t.TempDir()
	err := captureOne(app, dir, scene, false)
	if err == nil || !strings.Contains(err.Error(), scene.Name) {
		t.Fatalf("未收敛错误 = %v，想要包含场景名 %q", err, scene.Name)
	}
	if _, statErr := os.Stat(filepath.Join(dir, scene.Name+".png")); !os.IsNotExist(statErr) {
		t.Fatalf("未收敛场景不应写图，statErr = %v", statErr)
	}
}
