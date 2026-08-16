package render

import (
	"bytes"
	"encoding/binary"
	"math"
	"slices"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
)

const (
	maxAvatars          = 11
	avatarPartsPerBody  = 6
	maxAvatarParts      = maxAvatars * avatarPartsPerBody
	avatarInstanceBytes = 80

	avatarCameraOffset   = 0
	avatarCameraBytes    = 80
	avatarInstanceOffset = 256
	avatarInstanceSize   = maxAvatarParts * avatarInstanceBytes
	avatarIndirectOffset = avatarInstanceOffset + avatarInstanceSize
	avatarIndirectBytes  = 20
	avatarUploadBytes    = avatarIndirectOffset + avatarIndirectBytes
)

// EntityKind 区分共享渲染通道中的实体身份域。
type EntityKind uint8

const (
	// EntityPlayer 表示玩家身份域。
	EntityPlayer EntityKind = 1
	// EntityCompanion 表示伙伴身份域。
	EntityCompanion EntityKind = 2
	// EntityTarget 表示当前方块目标名牌域。
	EntityTarget EntityKind = 3
)

// EntityKey 由身份域和独立的 16-byte ID 组成。
type EntityKey struct {
	Kind EntityKind
	ID   [16]byte
}

func compareEntityKeys(left, right EntityKey) int {
	if left.Kind < right.Kind {
		return -1
	}
	if left.Kind > right.Kind {
		return 1
	}
	return bytes.Compare(left.ID[:], right.ID[:])
}

// Avatar 是远端玩家或伙伴渲染所需的插值后姿态。
type Avatar struct {
	Key      EntityKey
	Position mgl32.Vec3
	Yaw      float32
	Pitch    float32
}

type avatarPart struct {
	transform mgl32.Mat4
	color     [4]float32
}

func encodeAvatarPartsInto(dst []byte, parts []avatarPart) {
	for partIndex, part := range parts {
		offset := partIndex * avatarInstanceBytes
		for index, value := range part.transform {
			binary.LittleEndian.PutUint32(dst[offset+index*4:], math.Float32bits(value))
		}
		for index, value := range part.color {
			binary.LittleEndian.PutUint32(dst[offset+64+index*4:], math.Float32bits(value))
		}
	}
}

// encodeAvatarCameraInto 写入视图投影矩阵，并在其后追加本帧固定 daylight。
func encodeAvatarFloat32sInto(dst []byte, values []float32) {
	for index, value := range values {
		binary.LittleEndian.PutUint32(dst[index*4:], math.Float32bits(value))
	}
}

func buildAvatarParts(dst []avatarPart, avatars []Avatar) []avatarPart {
	return buildOrderedAvatarParts(dst, orderedAvatarsInto(nil, avatars))
}

func orderedAvatarsInto(dst []Avatar, avatars []Avatar) []Avatar {
	ordered := append(dst, avatars...)
	slices.SortFunc(ordered, func(left, right Avatar) int {
		return compareEntityKeys(left.Key, right.Key)
	})
	return ordered
}

func buildOrderedAvatarParts(dst []avatarPart, ordered []Avatar) []avatarPart {
	for _, avatar := range ordered {
		root := mgl32.Translate3D(avatar.Position[0], avatar.Position[1], avatar.Position[2]).Mul4(
			mgl32.HomogRotate3DY(avatar.Yaw),
		)
		base := avatarColor(avatar.Key)
		head := root.Mul4(mgl32.Translate3D(0, 1.4, 0)).
			Mul4(mgl32.HomogRotate3DX(avatar.Pitch)).
			Mul4(mgl32.Translate3D(0, 0.2, 0)).
			Mul4(mgl32.Scale3D(0.6, 0.4, 0.6))
		dst = append(dst,
			avatarPart{transform: head, color: avatarShade(base, 1.12)},
			avatarCuboid(root, mgl32.Vec3{0, 1.05, 0}, mgl32.Vec3{0.4, 0.7, 0.25}, base),
			avatarCuboid(root, mgl32.Vec3{-0.25, 1.05, 0}, mgl32.Vec3{0.1, 0.7, 0.25}, avatarShade(base, 0.82)),
			avatarCuboid(root, mgl32.Vec3{0.25, 1.05, 0}, mgl32.Vec3{0.1, 0.7, 0.25}, avatarShade(base, 0.82)),
			avatarCuboid(root, mgl32.Vec3{-0.1, 0.35, 0}, mgl32.Vec3{0.18, 0.7, 0.25}, avatarShade(base, 0.82)),
			avatarCuboid(root, mgl32.Vec3{0.1, 0.35, 0}, mgl32.Vec3{0.18, 0.7, 0.25}, avatarShade(base, 0.82)),
		)
	}
	return dst
}

func avatarCuboid(root mgl32.Mat4, center, size mgl32.Vec3, color [4]float32) avatarPart {
	return avatarPart{
		transform: root.Mul4(mgl32.Translate3D(center[0], center[1], center[2])).
			Mul4(mgl32.Scale3D(size[0], size[1], size[2])),
		color: color,
	}
}

func avatarShade(color [4]float32, factor float32) [4]float32 {
	for channel := 0; channel < 3; channel++ {
		color[channel] = max(0.2, min(color[channel]*factor, 0.9))
	}
	return color
}

var avatarPalette = [...][4]float32{
	{0.82, 0.34, 0.30, 0.9},
	{0.32, 0.62, 0.86, 0.9},
	{0.38, 0.72, 0.36, 0.9},
	{0.88, 0.66, 0.28, 0.9},
	{0.68, 0.42, 0.28, 0.9},
	{0.34, 0.76, 0.84, 0.9},
	{0.86, 0.46, 0.68, 0.9},
	{0.54, 0.50, 0.88, 0.9},
	{0.82, 0.54, 0.26, 0.9},
	{0.42, 0.70, 0.58, 0.9},
	{0.88, 0.40, 0.42, 0.9},
	{0.72, 0.42, 0.82, 0.9},
	{0.46, 0.64, 0.84, 0.9},
	{0.76, 0.72, 0.30, 0.9},
	{0.28, 0.78, 0.68, 0.9},
	{0.84, 0.48, 0.32, 0.9},
}

// AvatarColor 用 PlayerID 的全部 16 bytes 计算稳定的 FNV-1a 调色板索引。
func AvatarColor(playerID core.PlayerID) [4]float32 {
	const (
		offset32 = uint32(2166136261)
		prime32  = uint32(16777619)
	)
	hash := offset32
	for _, value := range playerID {
		hash ^= uint32(value)
		hash *= prime32
	}
	return avatarPalette[hash%uint32(len(avatarPalette))]
}

func avatarColor(key EntityKey) [4]float32 {
	if key.Kind == EntityPlayer {
		return AvatarColor(core.PlayerID(key.ID))
	}
	if key.Kind != EntityCompanion {
		panic("render: avatar color requires player or companion key")
	}
	const (
		offset32             = uint32(2166136261)
		prime32              = uint32(16777619)
		companionColorDomain = "companion:"
	)
	hash := offset32
	for index := range len(companionColorDomain) {
		hash ^= uint32(companionColorDomain[index])
		hash *= prime32
	}
	for _, value := range key.ID {
		hash ^= uint32(value)
		hash *= prime32
	}
	return avatarPalette[hash%uint32(len(avatarPalette))]
}

func avatarPartBytes(parts []avatarPart) []byte {
	out := make([]byte, len(parts)*avatarInstanceBytes)
	for partIndex, part := range parts {
		offset := partIndex * avatarInstanceBytes
		for index, value := range part.transform {
			binary.LittleEndian.PutUint32(out[offset+index*4:], math.Float32bits(value))
		}
		for index, value := range part.color {
			binary.LittleEndian.PutUint32(out[offset+64+index*4:], math.Float32bits(value))
		}
	}
	return out
}

func avatarFloat32Bytes(values []float32) []byte {
	out := make([]byte, len(values)*4)
	for index, value := range values {
		binary.LittleEndian.PutUint32(out[index*4:], math.Float32bits(value))
	}
	return out
}
