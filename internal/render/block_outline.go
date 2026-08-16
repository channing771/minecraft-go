package render

import (
	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
)

const (
	blockOutlineParts          = 12
	blockOutlineExpand float32 = 0.003
	blockOutlineWidth  float32 = 0.018
	blockOutlineAlpha  float32 = 0.86

	blockOutlineInstanceOffset = 256
	blockOutlineInstanceSize   = blockOutlineParts * avatarInstanceBytes
	blockOutlineIndirectOffset = blockOutlineInstanceOffset + blockOutlineInstanceSize
	blockOutlineUploadBytes    = blockOutlineIndirectOffset + avatarIndirectBytes
)

// BlockOutline 是当前帧的目标方块轮廓输入。
type BlockOutline struct {
	Visible  bool
	Position core.BlockPos
}

func buildBlockOutlineParts(dst []avatarPart, position core.BlockPos) []avatarPart {
	root := mgl32.Translate3D(float32(position.X), float32(position.Y), float32(position.Z))
	long := float32(1) + 2*blockOutlineExpand
	low := blockOutlineWidth/2 - blockOutlineExpand
	high := 1 + blockOutlineExpand - blockOutlineWidth/2
	edges := [...]float32{low, high}
	color := [4]float32{1, 1, 1, blockOutlineAlpha}
	for _, first := range edges {
		for _, second := range edges {
			dst = append(dst,
				avatarCuboid(root, mgl32.Vec3{0.5, first, second}, mgl32.Vec3{long, blockOutlineWidth, blockOutlineWidth}, color),
				avatarCuboid(root, mgl32.Vec3{first, 0.5, second}, mgl32.Vec3{blockOutlineWidth, long, blockOutlineWidth}, color),
				avatarCuboid(root, mgl32.Vec3{first, second, 0.5}, mgl32.Vec3{blockOutlineWidth, blockOutlineWidth, long}, color),
			)
		}
	}
	return dst
}
