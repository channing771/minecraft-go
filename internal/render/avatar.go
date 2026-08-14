package render

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"fmt"
	"math"
	"slices"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/gfx"
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

//go:embed shader/avatar.wgsl
var avatarShader string

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

// AvatarRenderer 管理固定容量的统一实体实例与独立渲染 pass。
type AvatarRenderer struct {
	dynamic  gfx.Buffer
	vertices gfx.Buffer
	indices  gfx.Buffer
	pipeline gfx.RenderPipeline
	bind     gfx.BindGroup
	parts    []avatarPart
	ordered  []Avatar
	upload   []byte
}

// NewAvatarRenderer 一次性创建最多十一个远端实体所需的固定 GPU 资源。
func NewAvatarRenderer(dev gfx.Device, colorFormat, depthFormat gfx.TextureFormat) *AvatarRenderer {
	renderer := &AvatarRenderer{
		parts:   make([]avatarPart, 0, maxAvatarParts),
		ordered: make([]Avatar, 0, maxAvatars),
		upload:  make([]byte, avatarUploadBytes),
	}
	renderer.dynamic = dev.CreateBuffer(gfx.BufferDesc{
		Label: "avatar dynamic upload",
		Size:  avatarUploadBytes,
		Usage: gfx.BufferUsageUniform | gfx.BufferUsageStorage |
			gfx.BufferUsageIndirect | gfx.BufferUsageCopyDst,
	})
	renderer.vertices = dev.CreateBuffer(gfx.BufferDesc{
		Label: "avatar cube vertices",
		Size:  uint64(len(avatarCubeVertices) * 4),
		Usage: gfx.BufferUsageVertex | gfx.BufferUsageCopyDst,
	})
	renderer.vertices.Write(0, avatarFloat32Bytes(avatarCubeVertices))
	renderer.indices = dev.CreateBuffer(gfx.BufferDesc{
		Label: "avatar cube indices",
		Size:  uint64(len(avatarCubeIndices) * 4),
		Usage: gfx.BufferUsageIndex | gfx.BufferUsageCopyDst,
	})
	renderer.indices.Write(0, uint32sToBytes(avatarCubeIndices))
	layout := gfx.BindGroupLayout{
		Label: "avatar layout",
		Entries: []gfx.BindGroupLayoutEntry{
			{Binding: 0, Type: gfx.BindingUniformBuffer, VisibleIn: gfx.StageVertex},
			{Binding: 1, Type: gfx.BindingStorageBufferRO, VisibleIn: gfx.StageVertex},
		},
	}
	module := dev.CreateShaderModule(avatarShader)
	renderer.pipeline = dev.CreateRenderPipeline(gfx.RenderPipelineDesc{
		Label:         "avatar",
		Shader:        module,
		VertexEntry:   "vs_main",
		FragmentEntry: "fs_main",
		Buffers: []gfx.VertexBufferLayout{{
			ArrayStride: 12,
			Attributes: []gfx.VertexAttribute{{
				ShaderLocation: 0,
				Format:         gfx.VertexFormatFloat32x3,
			}},
		}},
		BindGroups:  []gfx.BindGroupLayout{layout},
		ColorFormat: colorFormat,
		DepthFormat: depthFormat,
		DepthWrite:  true,
		Blend:       gfx.BlendReplace,
	})
	module.Release()
	renderer.bind = dev.CreateBindGroup(gfx.BindGroupDesc{
		Label:  "avatar resources",
		Layout: layout,
		Entries: []gfx.BindGroupEntry{
			{
				Binding: 0, Buffer: renderer.dynamic,
				Offset: avatarCameraOffset, Size: avatarCameraBytes,
			},
			{
				Binding: 1, Buffer: renderer.dynamic,
				Offset: avatarInstanceOffset, Size: avatarInstanceSize,
			},
		},
	})
	return renderer
}

// Render 上传紧凑实例并在已有颜色/深度附件之上编码 avatar pass。
func (renderer *AvatarRenderer) Render(
	encoder gfx.CommandEncoder,
	target, depth gfx.TextureView,
	camera Camera,
	avatars []Avatar,
) error {
	if len(avatars) > maxAvatars {
		return fmt.Errorf("render: avatar count %d exceeds %d", len(avatars), maxAvatars)
	}
	renderer.ordered = orderedAvatarsInto(renderer.ordered[:0], avatars)
	renderer.parts = buildOrderedAvatarParts(renderer.parts[:0], renderer.ordered)
	if len(renderer.parts) == 0 {
		return nil
	}
	encodeAvatarPartsInto(renderer.upload[avatarInstanceOffset:avatarIndirectOffset], renderer.parts)
	encodeAvatarCameraInto(
		renderer.upload[avatarCameraOffset:avatarCameraBytes], camera,
	)
	encodeAvatarUint32sInto(renderer.upload[avatarIndirectOffset:avatarUploadBytes], []uint32{
		uint32(len(avatarCubeIndices)), uint32(len(renderer.parts)), 0, 0, 0,
	})
	renderer.dynamic.Write(0, renderer.upload)

	pass := encoder.BeginRenderPass(gfx.RenderPassDesc{
		Label:     "avatar pass",
		ColorView: target,
		DepthView: depth,
		LoadClear: false,
	})
	pass.SetPipeline(renderer.pipeline)
	pass.SetBindGroup(0, renderer.bind)
	pass.SetVertexBuffer(0, renderer.vertices, 0)
	pass.SetIndexBuffer(renderer.indices, 0)
	pass.DrawIndexedIndirect(renderer.dynamic, avatarIndirectOffset)
	pass.End()
	return nil
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
func encodeAvatarCameraInto(dst []byte, camera Camera) {
	encodeAvatarFloat32sInto(dst, camera.ViewProj[:])
	binary.LittleEndian.PutUint32(dst[64:], math.Float32bits(camera.Daylight))
	for index := 68; index < len(dst); index += 4 {
		binary.LittleEndian.PutUint32(dst[index:], 0)
	}
}

func encodeAvatarFloat32sInto(dst []byte, values []float32) {
	for index, value := range values {
		binary.LittleEndian.PutUint32(dst[index*4:], math.Float32bits(value))
	}
}

func encodeAvatarUint32sInto(dst []byte, values []uint32) {
	for index, value := range values {
		binary.LittleEndian.PutUint32(dst[index*4:], value)
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

// Release 只释放 AvatarRenderer 拥有的 GPU handle；重复调用是安全的。
func (renderer *AvatarRenderer) Release() {
	if renderer.bind != nil {
		renderer.bind.Release()
		renderer.bind = nil
	}
	if renderer.pipeline != nil {
		renderer.pipeline.Release()
		renderer.pipeline = nil
	}
	for _, buffer := range []*gfx.Buffer{
		&renderer.indices, &renderer.vertices, &renderer.dynamic,
	} {
		if *buffer != nil {
			(*buffer).Release()
			*buffer = nil
		}
	}
}

var avatarCubeVertices = []float32{
	// +X
	0.5, -0.5, -0.5, 0.5, 0.5, -0.5, 0.5, 0.5, 0.5, 0.5, -0.5, 0.5,
	// -X
	-0.5, -0.5, 0.5, -0.5, 0.5, 0.5, -0.5, 0.5, -0.5, -0.5, -0.5, -0.5,
	// +Y
	-0.5, 0.5, -0.5, -0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5, -0.5,
	// -Y
	-0.5, -0.5, 0.5, -0.5, -0.5, -0.5, 0.5, -0.5, -0.5, 0.5, -0.5, 0.5,
	// +Z
	0.5, -0.5, 0.5, 0.5, 0.5, 0.5, -0.5, 0.5, 0.5, -0.5, -0.5, 0.5,
	// -Z
	-0.5, -0.5, -0.5, -0.5, 0.5, -0.5, 0.5, 0.5, -0.5, 0.5, -0.5, -0.5,
}

var avatarCubeIndices = []uint32{
	0, 1, 2, 0, 2, 3,
	4, 5, 6, 4, 6, 7,
	8, 9, 10, 8, 10, 11,
	12, 13, 14, 12, 14, 15,
	16, 17, 18, 16, 18, 19,
	20, 21, 22, 20, 22, 23,
}
