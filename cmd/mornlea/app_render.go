//go:build darwin

package main

import (
	"fmt"
	"math"
	"slices"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/gfx"
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

func cameraChunk(pos mgl32.Vec3) core.ChunkPos {
	return core.BlockPos{
		X: int32(math.Floor(float64(pos.X()))),
		Z: int32(math.Floor(float64(pos.Z()))),
	}.Chunk()
}

type depthTarget struct {
	texture       gfx.Texture
	view          gfx.TextureView
	width, height uint32
}

func newDepthTarget(dev gfx.Device, width, height uint32) *depthTarget {
	texture := dev.CreateTexture(gfx.TextureDesc{
		Label:     "main depth",
		Width:     width,
		Height:    height,
		Format:    gfx.FormatDepth32Float,
		Dimension: gfx.TextureDimension2D,
		Usage:     gfx.TextureUsageRenderTarget | gfx.TextureUsageBinding,
	})
	view := texture.View(gfx.TextureViewDesc{
		Dimension: gfx.TextureViewDimension2D,
		Aspect:    gfx.AspectDepthOnly,
	})
	return &depthTarget{
		texture: texture,
		view:    view,
		width:   width,
		height:  height,
	}
}

func (d *depthTarget) Release() {
	if d.view != nil {
		d.view.Release()
		d.view = nil
	}
	if d.texture != nil {
		d.texture.Release()
		d.texture = nil
	}
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
