//go:build darwin

package render

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
)

// BenchmarkRemoteAvatarNameTag 度量多人热路径的 CPU 半部:名牌布局
// (Prepare + FrameStreams)与 avatar 实例编码。GPU pass 自 R2c 起由
// Rust 渲染器执行,不在本 bench 范围;数值只记录,不作门禁。
func BenchmarkRemoteAvatarNameTag(b *testing.B) {
	atlas, err := NewGlyphAtlasWithSink(&glyphTestSink{})
	if err != nil {
		b.Fatal(err)
	}
	defer atlas.Release()
	tagRenderer := NewNameTagLayouter(atlas)

	names := [...]string{"星野", "月河", "云山", "海界", "星河", "月海", "云野"}
	avatars := make([]Avatar, len(names))
	tags := make([]NameTag, len(names), maxNameTags)
	for index, name := range names {
		id := core.PlayerID{0, 0, 0, byte(index + 1), 0, 0, 0x40, byte(index + 1), 0x80, 0, 0, 0, 0, 0, 0, byte(index + 1)}
		position := mgl32.Vec3{float32(index-3) * 0.2, -0.9, 0}
		key := testEntityKey(id)
		avatars[index] = Avatar{Key: key, Position: position}
		tags[index] = NameTag{Key: key, Text: name, Anchor: position.Add(mgl32.Vec3{0, 2.05, 0})}
	}
	budget := NewUploadBudget(1 << 20)
	if err := tagRenderer.Prepare(tags, budget); err != nil {
		b.Fatal(err)
	}

	billboard := BillboardCamera{
		ViewProj: mgl32.Ident4(), Right: mgl32.Vec3{1, 0, 0}, Up: mgl32.Vec3{0, 1, 0},
	}
	var encoder InstanceEncoder
	var avatarStream, billboardBytes []byte
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		budget.BeginFrame()
		if err := tagRenderer.Prepare(tags, budget); err != nil {
			b.Fatal(err)
		}
		backgrounds, glyphs := tagRenderer.FrameStreams()
		avatarStream = encoder.EncodeAvatarInstances(avatarStream[:0], avatars)
		billboardBytes = EncodeBillboardCameraBytes(billboardBytes[:0], billboard)
		if len(backgrounds) == 0 || len(glyphs) == 0 || len(avatarStream) == 0 {
			b.Fatal("empty frame streams")
		}
	}
}
