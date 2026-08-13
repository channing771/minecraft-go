package render

import (
	"testing"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/gfx"
)

func testItemDrops(count int) []ItemDrop {
	drops := make([]ItemDrop, count)
	items := [3]core.ItemID{core.ItemStone, core.ItemDirt, core.ItemGrass}
	for index := range drops {
		drops[index] = ItemDrop{
			ID: core.DropID{
				Dimension:  core.Overworld,
				Chunk:      core.ChunkPos{X: int32(index / core.DropsPerChunk)},
				Slot:       uint8(index % core.DropsPerChunk),
				Generation: 1,
			},
			Block: core.BlockPos{X: int32(index), Y: 3, Z: int32(index)},
			Item:  items[index%len(items)],
		}
	}
	return drops
}

// Mutation killed: exceeding the fixed instance budget or rendering unknown
// items would break the固定 800 实例上限。
func TestItemDropPartsStayWithinFixedCapacity(t *testing.T) {
	parts := buildItemDropParts(nil, 0, testItemDrops(maxItemDrops+16))
	if len(parts) != maxItemDrops {
		t.Fatalf("实例数 = %d，想要固定上限 %d", len(parts), maxItemDrops)
	}

	unknown := []ItemDrop{{
		ID:   core.DropID{Slot: 0, Generation: 1},
		Item: core.ItemID(4242),
	}}
	if got := buildItemDropParts(nil, 0, unknown); len(got) != 0 {
		t.Fatalf("未注册物品产生了实例: %+v", got)
	}
}

// Mutation killed: swapping item colors would render the wrong swatch.
func TestItemDropColorsMatchProceduralBlocks(t *testing.T) {
	for _, item := range []core.ItemID{core.ItemStone, core.ItemDirt, core.ItemGrass} {
		got, ok := itemDropColor(item)
		if !ok || got != ItemColor(item) {
			t.Fatalf("物品 %d 颜色 = %v, %v，想要与 HUD 一致", item, got, ok)
		}
	}
	if _, ok := itemDropColor(core.ItemNone); ok {
		t.Fatal("空物品得到了颜色")
	}
}

// Mutation killed: deriving animation from wall-clock time or mutating the
// mirror would break determinism across identical ticks.
func TestItemDropAnimationIsDeterministicAndTickDriven(t *testing.T) {
	drops := testItemDrops(2)
	first := append([]avatarPart(nil), buildItemDropParts(nil, 7, drops)...)
	repeat := buildItemDropParts(nil, 7, drops)
	if len(first) != len(repeat) {
		t.Fatalf("同一 tick 实例数不同: %d vs %d", len(first), len(repeat))
	}
	for index := range first {
		if first[index] != repeat[index] {
			t.Fatalf("同一 tick 实例 %d 不稳定", index)
		}
	}

	later := buildItemDropParts(nil, 8, drops)
	if later[0] == first[0] {
		t.Fatal("server tick 前进后动画相位未变化")
	}
	// 权威方块位置不受动画影响。
	if drops[0].Block != (core.BlockPos{X: 0, Y: 3, Z: 0}) {
		t.Fatalf("动画改写了权威位置: %+v", drops[0].Block)
	}
}

// Mutation killed: distinct drops sharing a phase would visibly synchronise.
func TestItemDropPhaseVariesWithStableID(t *testing.T) {
	base := core.DropID{Dimension: core.Overworld, Slot: 1, Generation: 1}
	other := base
	other.Slot = 2
	if dropAnimationPhase(3, base) == dropAnimationPhase(3, other) {
		t.Fatal("不同槽位得到相同动画相位")
	}
}

// Mutation killed: dropping the depth attachment, changing pass order, or
// splitting the upload changes the captured descriptors.
func TestItemDropRendererUsesSingleUploadAndIndirectDraw(t *testing.T) {
	dev := &avatarTestDevice{}
	renderer := NewItemDropRenderer(dev, gfx.FormatRGBA8Unorm, gfx.FormatDepth32Float)
	defer renderer.Release()

	upload := dev.bufferByLabel(t, "item drop dynamic upload")
	if got, want := upload.desc.Size, uint64(dropUploadBytes); got != want {
		t.Fatalf("dynamic upload size=%d want=%d", got, want)
	}
	if desc := dev.pipelineDesc; desc.DepthFormat != gfx.FormatDepth32Float || !desc.DepthWrite {
		t.Fatalf("pipeline=%+v；想要写深度的实体 pass", desc)
	}

	encoder := &avatarTestEncoder{}
	renderer.Render(
		encoder, avatarTestView{}, avatarTestView{}, Camera{}, 5, testItemDrops(40),
	)
	if got, want := len(encoder.passes), 1; got != want {
		t.Fatalf("passes=%d want=%d", got, want)
	}
	pass := encoder.passes[0]
	if pass.desc.Label != "item drop pass" || pass.desc.LoadClear || !pass.ended {
		t.Fatalf("pass 描述=%+v", pass.desc)
	}
	if !pass.setIndex || pass.draws != 1 {
		t.Fatalf("想要一次 indirect indexed draw: setIndex=%v draws=%d", pass.setIndex, pass.draws)
	}
	if len(upload.lastWrite) != dropUploadBytes {
		t.Fatalf("单次上传字节=%d want=%d", len(upload.lastWrite), dropUploadBytes)
	}
}

// Mutation killed: rendering an empty mirror emits an observable pass.
func TestItemDropRendererSkipsEmptyMirror(t *testing.T) {
	renderer := NewItemDropRenderer(&avatarTestDevice{}, gfx.FormatRGBA8Unorm, gfx.FormatDepth32Float)
	defer renderer.Release()
	encoder := &avatarTestEncoder{}
	renderer.Render(encoder, avatarTestView{}, avatarTestView{}, Camera{}, 1, nil)
	if len(encoder.passes) != 0 {
		t.Fatalf("空镜像 passes=%d want=0", len(encoder.passes))
	}
}

// Mutation killed: leaking an owned handle or failing idempotency changes an
// exact release count.
func TestItemDropRendererReleaseIsIdempotent(t *testing.T) {
	dev := &avatarTestDevice{}
	renderer := NewItemDropRenderer(dev, gfx.FormatRGBA8Unorm, gfx.FormatDepth32Float)
	renderer.Release()
	renderer.Release()

	for _, buffer := range dev.buffers {
		if buffer.releases != 1 {
			t.Errorf("buffer %q releases=%d want=1", buffer.desc.Label, buffer.releases)
		}
	}
}

// Mutation killed: reallocating instance storage per frame would make the
// warmed 800 项路径分配。
func TestItemDropPartsReuseStorageWithoutAllocating(t *testing.T) {
	renderer := &ItemDropRenderer{
		parts:  make([]avatarPart, 0, maxItemDrops),
		upload: make([]byte, dropUploadBytes),
	}
	drops := testItemDrops(maxItemDrops)
	renderer.parts = buildItemDropParts(renderer.parts[:0], 1, drops)

	if allocations := testing.AllocsPerRun(50, func() {
		renderer.parts = buildItemDropParts(renderer.parts[:0], 2, drops)
	}); allocations != 0 {
		t.Fatalf("warmed 掉落物实例构建分配=%v，想要 0", allocations)
	}
}

// Mutation killed: invalid bindings or attachment formats trigger a WebGPU
// validation panic during submit/poll.
func TestItemDropRendererHeadlessDraw(t *testing.T) {
	dev, err := gfx.NewHeadlessDevice()
	if err != nil {
		t.Skipf("本机无可用 GPU 适配器: %v", err)
	}
	defer dev.Release()

	color := dev.CreateTexture(gfx.TextureDesc{
		Label: "item drop test color", Width: 64, Height: 64,
		Format: gfx.FormatRGBA8Unorm, Usage: gfx.TextureUsageRenderTarget,
	})
	defer color.Release()
	colorView := color.View(gfx.TextureViewDesc{})
	defer colorView.Release()
	depth := dev.CreateTexture(gfx.TextureDesc{
		Label: "item drop test depth", Width: 64, Height: 64,
		Format: gfx.FormatDepth32Float, Usage: gfx.TextureUsageRenderTarget,
	})
	defer depth.Release()
	depthView := depth.View(gfx.TextureViewDesc{Aspect: gfx.AspectDepthOnly})
	defer depthView.Release()

	renderer := NewItemDropRenderer(dev, gfx.FormatRGBA8Unorm, gfx.FormatDepth32Float)
	defer renderer.Release()

	encoder := dev.CreateCommandEncoder()
	clear := encoder.BeginRenderPass(gfx.RenderPassDesc{
		Label: "item drop test clear", ColorView: colorView, DepthView: depthView, LoadClear: true,
	})
	clear.End()
	renderer.Render(encoder, colorView, depthView, Camera{}, 9, testItemDrops(maxItemDrops))
	commands := encoder.Finish()
	dev.Submit(commands)
	commands.Release()
	dev.Poll(true)
}

func TestItemDropColorCoversRegisteredNonPlaceableItems(t *testing.T) {
	for _, item := range []core.ItemID{
		core.ItemCoal, core.ItemRawIron, core.ItemIronIngot,
		core.ItemFurnace, core.ItemIronBlock, core.ItemChest,
		core.ItemBrokenStonePickaxe, core.ItemBrokenIronPickaxe,
	} {
		color, ok := itemDropColor(item)
		if !ok {
			t.Fatalf("已注册物品 %d 无法绘制掉落物", item)
		}
		if color == ([4]float32{}) {
			t.Fatalf("物品 %d 的掉落物颜色为零值", item)
		}
	}
	if _, ok := itemDropColor(core.ItemNone); ok {
		t.Fatal("空物品被绘制")
	}
	if _, ok := itemDropColor(core.ItemID(4242)); ok {
		t.Fatal("未知物品被绘制")
	}
}
