package render

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/gfx"
)

func BenchmarkRemoteAvatarNameTag(b *testing.B) {
	dev, err := gfx.NewHeadlessDevice()
	if err != nil {
		b.Skipf("本机无可用 GPU 适配器: %v", err)
	}
	defer dev.Release()
	color := dev.CreateTexture(gfx.TextureDesc{
		Label: "multiplayer benchmark color", Width: 256, Height: 256,
		Format: gfx.FormatRGBA8Unorm, Usage: gfx.TextureUsageRenderTarget,
	})
	defer color.Release()
	colorView := color.View(gfx.TextureViewDesc{})
	defer colorView.Release()
	depth := dev.CreateTexture(gfx.TextureDesc{
		Label: "multiplayer benchmark depth", Width: 256, Height: 256,
		Format: gfx.FormatDepth32Float, Usage: gfx.TextureUsageRenderTarget,
	})
	defer depth.Release()
	depthView := depth.View(gfx.TextureViewDesc{Aspect: gfx.AspectDepthOnly})
	defer depthView.Release()
	atlas, err := NewGlyphAtlas(dev)
	if err != nil {
		b.Fatal(err)
	}
	defer atlas.Release()
	avatarRenderer := NewAvatarRenderer(dev, gfx.FormatRGBA8Unorm, gfx.FormatDepth32Float)
	defer avatarRenderer.Release()
	tagRenderer := NewNameTagRenderer(dev, gfx.FormatRGBA8Unorm, gfx.FormatDepth32Float, atlas)
	defer tagRenderer.Release()

	names := [...]string{"星野", "月河", "云山", "海界", "星河", "月海", "云野"}
	avatars := make([]Avatar, len(names))
	tags := make([]NameTag, len(names), maxNameTags)
	for index, name := range names {
		id := core.PlayerID{0, 0, 0, byte(index + 1), 0, 0, 0x40, byte(index + 1), 0x80, 0, 0, 0, 0, 0, 0, byte(index + 1)}
		position := mgl32.Vec3{float32(index-3) * 0.2, -0.9, 0}
		avatars[index] = Avatar{PlayerID: id, Position: position}
		tags[index] = NameTag{PlayerID: id, Text: name, Anchor: position.Add(mgl32.Vec3{0, 2.05, 0})}
	}
	budget := NewUploadBudget(1 << 20)
	if err := tagRenderer.Prepare(tags, budget); err != nil {
		b.Fatal(err)
	}
	clearEncoder := dev.CreateCommandEncoder()
	clear := clearEncoder.BeginRenderPass(gfx.RenderPassDesc{
		Label: "multiplayer benchmark clear", ColorView: colorView, DepthView: depthView, LoadClear: true,
	})
	clear.End()
	clearCommand := clearEncoder.Finish()
	dev.Submit(clearCommand)
	clearCommand.Release()
	dev.Poll(true)

	camera := Camera{ViewProj: mgl32.Ident4()}
	billboard := BillboardCamera{
		ViewProj: mgl32.Ident4(), Right: mgl32.Vec3{1, 0, 0}, Up: mgl32.Vec3{0, 1, 0},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		budget.BeginFrame()
		if err := tagRenderer.Prepare(tags, budget); err != nil {
			b.Fatal(err)
		}
		encoder := dev.CreateCommandEncoder()
		avatarRenderer.Render(encoder, colorView, depthView, camera, avatars)
		tagRenderer.Render(encoder, colorView, depthView, billboard)
		command := encoder.Finish()
		dev.Submit(command)
		command.Release()
	}
	b.StopTimer()
	dev.Poll(true)
}
