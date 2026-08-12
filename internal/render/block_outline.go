package render

import (
	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/gfx"
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

// BlockOutlineRenderer 管理固定十二实例的方块边轮廓资源。
type BlockOutlineRenderer struct {
	dynamic  gfx.Buffer
	vertices gfx.Buffer
	indices  gfx.Buffer
	pipeline gfx.RenderPipeline
	bind     gfx.BindGroup
	parts    []avatarPart
	upload   []byte
}

// NewBlockOutlineRenderer 创建固定容量且只读深度的透明轮廓管线。
func NewBlockOutlineRenderer(
	dev gfx.Device,
	colorFormat, depthFormat gfx.TextureFormat,
) *BlockOutlineRenderer {
	renderer := &BlockOutlineRenderer{
		parts:  make([]avatarPart, 0, blockOutlineParts),
		upload: make([]byte, blockOutlineUploadBytes),
	}
	renderer.dynamic = dev.CreateBuffer(gfx.BufferDesc{
		Label: "block outline dynamic upload",
		Size:  blockOutlineUploadBytes,
		Usage: gfx.BufferUsageUniform | gfx.BufferUsageStorage |
			gfx.BufferUsageIndirect | gfx.BufferUsageCopyDst,
	})
	renderer.vertices = dev.CreateBuffer(gfx.BufferDesc{
		Label: "block outline cube vertices",
		Size:  uint64(len(avatarCubeVertices) * 4),
		Usage: gfx.BufferUsageVertex | gfx.BufferUsageCopyDst,
	})
	renderer.vertices.Write(0, avatarFloat32Bytes(avatarCubeVertices))
	renderer.indices = dev.CreateBuffer(gfx.BufferDesc{
		Label: "block outline cube indices",
		Size:  uint64(len(avatarCubeIndices) * 4),
		Usage: gfx.BufferUsageIndex | gfx.BufferUsageCopyDst,
	})
	renderer.indices.Write(0, uint32sToBytes(avatarCubeIndices))
	layout := gfx.BindGroupLayout{
		Label: "block outline layout",
		Entries: []gfx.BindGroupLayoutEntry{
			{Binding: 0, Type: gfx.BindingUniformBuffer, VisibleIn: gfx.StageVertex},
			{Binding: 1, Type: gfx.BindingStorageBufferRO, VisibleIn: gfx.StageVertex},
		},
	}
	module := dev.CreateShaderModule(avatarShader)
	renderer.pipeline = dev.CreateRenderPipeline(gfx.RenderPipelineDesc{
		Label:         "block outline",
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
		BindGroups:            []gfx.BindGroupLayout{layout},
		ColorFormat:           colorFormat,
		DepthFormat:           depthFormat,
		DepthWrite:            false,
		DepthCompareLessEqual: true,
		Blend:                 gfx.BlendAlpha,
	})
	module.Release()
	renderer.bind = dev.CreateBindGroup(gfx.BindGroupDesc{
		Label:  "block outline resources",
		Layout: layout,
		Entries: []gfx.BindGroupEntry{
			{
				Binding: 0, Buffer: renderer.dynamic,
				Offset: avatarCameraOffset, Size: avatarCameraBytes,
			},
			{
				Binding: 1, Buffer: renderer.dynamic,
				Offset: blockOutlineInstanceOffset, Size: blockOutlineInstanceSize,
			},
		},
	})
	return renderer
}

// Render 在既有颜色和深度附件上绘制当前目标的十二条边。
func (renderer *BlockOutlineRenderer) Render(
	encoder gfx.CommandEncoder,
	target, depth gfx.TextureView,
	camera Camera,
	outline BlockOutline,
) {
	if !outline.Visible {
		return
	}
	renderer.parts = buildBlockOutlineParts(renderer.parts[:0], outline.Position)
	encodeAvatarPartsInto(
		renderer.upload[blockOutlineInstanceOffset:blockOutlineIndirectOffset],
		renderer.parts,
	)
	encodeAvatarCameraInto(
		renderer.upload[avatarCameraOffset:avatarCameraBytes], camera,
	)
	encodeAvatarUint32sInto(
		renderer.upload[blockOutlineIndirectOffset:blockOutlineUploadBytes],
		[]uint32{uint32(len(avatarCubeIndices)), blockOutlineParts, 0, 0, 0},
	)
	renderer.dynamic.Write(0, renderer.upload)

	pass := encoder.BeginRenderPass(gfx.RenderPassDesc{
		Label:     "block outline pass",
		ColorView: target,
		DepthView: depth,
		LoadClear: false,
	})
	pass.SetPipeline(renderer.pipeline)
	pass.SetBindGroup(0, renderer.bind)
	pass.SetVertexBuffer(0, renderer.vertices, 0)
	pass.SetIndexBuffer(renderer.indices, 0)
	pass.DrawIndexedIndirect(renderer.dynamic, blockOutlineIndirectOffset)
	pass.End()
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

// Release 幂等释放轮廓 renderer 自己持有的 GPU 资源。
func (renderer *BlockOutlineRenderer) Release() {
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
