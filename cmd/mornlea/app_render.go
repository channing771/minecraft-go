//go:build darwin

package main

import (
	"fmt"
	"math"
	"slices"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/render"
)

func remoteRenderPresentations(presentations []client.RemotePresentation) ([]render.Avatar, []render.NameTag) {
	ordered := append([]client.RemotePresentation(nil), presentations...)
	slices.SortFunc(ordered, func(left, right client.RemotePresentation) int {
		return slices.Compare(left.PlayerID[:], right.PlayerID[:])
	})
	return remoteRenderPresentationsSortedInto(
		make([]render.Avatar, 0, len(ordered)),
		make([]render.NameTag, 0, maxFrameNameTags),
		ordered,
	)
}

func (a *application) appendCurrentBlockTarget(
	tags []render.NameTag,
) ([]render.NameTag, render.BlockOutline) {
	target, ok := a.currentBlockTarget()
	if !ok {
		return tags, render.BlockOutline{}
	}
	tags = append(tags, render.NameTag{
		Key:  render.EntityKey{Kind: render.EntityTarget},
		Text: target.Name,
		Anchor: mgl32.Vec3{
			float32(target.Position.X) + 0.5,
			float32(target.Position.Y) + 1.15,
			float32(target.Position.Z) + 0.5,
		},
	})
	return tags, render.BlockOutline{Visible: true, Position: target.Position}
}

func remoteRenderPresentationsSortedInto(
	avatars []render.Avatar,
	tags []render.NameTag,
	ordered []client.RemotePresentation,
) ([]render.Avatar, []render.NameTag) {
	for _, presentation := range ordered {
		key := render.EntityKey{Kind: render.EntityPlayer, ID: [16]byte(presentation.PlayerID)}
		avatars = append(avatars, render.Avatar{
			Key: key, Position: presentation.Position,
			Yaw: presentation.Yaw, Pitch: presentation.Pitch,
		})
		tags = append(tags, render.NameTag{
			Key:    key,
			Text:   presentation.DisplayName,
			Anchor: presentation.Position.Add(mgl32.Vec3{0, 2.05, 0}),
		})
	}
	return avatars, tags
}

func appendCompanionRenderPresentationsInto(
	avatars []render.Avatar,
	tags []render.NameTag,
	presentations []client.CompanionPresentation,
) ([]render.Avatar, []render.NameTag) {
	for _, presentation := range presentations {
		key := render.EntityKey{Kind: render.EntityCompanion, ID: [16]byte(presentation.ID)}
		avatars = append(avatars, render.Avatar{
			Key: key, Position: presentation.Position,
			Yaw: presentation.Yaw, Pitch: presentation.Pitch,
		})
		tags = append(tags, render.NameTag{
			Key: key, Text: presentation.Name,
			Anchor: presentation.Position.Add(mgl32.Vec3{0, 2.05, 0}),
		})
	}
	return avatars, tags
}

func validateEntityPresentationCounts(avatars []render.Avatar, tags []render.NameTag) error {
	if len(avatars) > maxFrameAvatars {
		return fmt.Errorf("avatar count %d exceeds %d", len(avatars), maxFrameAvatars)
	}
	if len(tags) > maxFrameNameTags {
		return fmt.Errorf("name tag count %d exceeds %d", len(tags), maxFrameNameTags)
	}
	return nil
}

func (a *application) framebufferLabel() string {
	width, height := a.framebufferSize()
	return fmt.Sprintf("%dx%d", width, height)
}

func (a *application) framebufferSize() (int, int) {
	if a.window != nil {
		return a.window.FramebufferSize()
	}
	return a.frameWidth, a.frameHeight
}

// cameraSectionPos 返回相机所在 section(Y 槽位钳制),复刻旧渲染器的
// cameraSection 语义,供可见性 BFS 起点使用。
func cameraSectionPos(pos mgl32.Vec3) core.SectionPos {
	block := core.BlockPos{
		X: int32(math.Floor(float64(pos[0]))),
		Y: int32(math.Floor(float64(pos[1]))),
		Z: int32(math.Floor(float64(pos[2]))),
	}
	y := int32(block.SectionIndex())
	if y < 0 {
		y = 0
	} else if y >= core.SectionsPerChunk {
		y = core.SectionsPerChunk - 1
	}
	return core.SectionPos{X: block.Chunk().X, Y: y, Z: block.Chunk().Z}
}

func cameraChunk(pos mgl32.Vec3) core.ChunkPos {
	return core.BlockPos{
		X: int32(math.Floor(float64(pos.X()))),
		Z: int32(math.Floor(float64(pos.Z()))),
	}.Chunk()
}

// appendItemDropInstances 把只读镜像转换为渲染实例，复用调用方切片。
func appendItemDropInstances(
	dst []render.ItemDrop,
	drops []client.ItemDropPresentation,
) []render.ItemDrop {
	for _, drop := range drops {
		block, ok := render.ItemDropBlock(drop.ID.Chunk, drop.BlockIndex)
		if !ok {
			continue
		}
		dst = append(dst, render.ItemDrop{ID: drop.ID, Block: block, Item: drop.Item})
	}
	return dst
}
