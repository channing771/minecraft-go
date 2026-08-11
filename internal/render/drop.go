package render

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/gfx"
	"minecraft-go/internal/world"
)

const (
	// maxItemDrops 是 HUD 之外掉落物渲染的固定 CPU/GPU 容量。
	maxItemDrops = core.MaxSessionDrops

	dropInstanceOffset = 256
	dropInstanceSize   = maxItemDrops * avatarInstanceBytes
	dropIndirectOffset = dropInstanceOffset + dropInstanceSize
	dropUploadBytes    = dropIndirectOffset + avatarIndirectBytes

	dropCubeSize     = float32(0.25)
	dropFloatHeight  = float32(0.08)
	dropSpinPeriod   = 80
	dropFloatPeriod  = 48
	dropBaseAltitude = float32(0.5)
)

// ItemDrop 是一个权威掉落物的渲染输入；位置由方块位置决定。
type ItemDrop struct {
	ID    core.DropID
	Block core.BlockPos
	Item  core.ItemID
}

// ItemDropRenderer 以固定容量绘制地面掉落物。
// 浮动与旋转只由 server tick 与稳定 ID 推导，不写回任何镜像。
type ItemDropRenderer struct {
	dynamic  gfx.Buffer
	vertices gfx.Buffer
	indices  gfx.Buffer
	pipeline gfx.RenderPipeline
	bind     gfx.BindGroup
	parts    []avatarPart
	upload   []byte
}

func NewItemDropRenderer(
	dev gfx.Device,
	colorFormat, depthFormat gfx.TextureFormat,
) *ItemDropRenderer {
	renderer := &ItemDropRenderer{
		parts:  make([]avatarPart, 0, maxItemDrops),
		upload: make([]byte, dropUploadBytes),
	}
	renderer.dynamic = dev.CreateBuffer(gfx.BufferDesc{
		Label: "item drop dynamic upload",
		Size:  dropUploadBytes,
		Usage: gfx.BufferUsageUniform | gfx.BufferUsageStorage |
			gfx.BufferUsageIndirect | gfx.BufferUsageCopyDst,
	})
	renderer.vertices = dev.CreateBuffer(gfx.BufferDesc{
		Label: "item drop cube vertices",
		Size:  uint64(len(avatarCubeVertices) * 4),
		Usage: gfx.BufferUsageVertex | gfx.BufferUsageCopyDst,
	})
	renderer.vertices.Write(0, avatarFloat32Bytes(avatarCubeVertices))
	renderer.indices = dev.CreateBuffer(gfx.BufferDesc{
		Label: "item drop cube indices",
		Size:  uint64(len(avatarCubeIndices) * 4),
		Usage: gfx.BufferUsageIndex | gfx.BufferUsageCopyDst,
	})
	renderer.indices.Write(0, uint32sToBytes(avatarCubeIndices))
	layout := gfx.BindGroupLayout{
		Label: "item drop layout",
		Entries: []gfx.BindGroupLayoutEntry{
			{Binding: 0, Type: gfx.BindingUniformBuffer, VisibleIn: gfx.StageVertex},
			{Binding: 1, Type: gfx.BindingStorageBufferRO, VisibleIn: gfx.StageVertex},
		},
	}
	module := dev.CreateShaderModule(avatarShader)
	renderer.pipeline = dev.CreateRenderPipeline(gfx.RenderPipelineDesc{
		Label:         "item drop",
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
		Label:  "item drop resources",
		Layout: layout,
		Entries: []gfx.BindGroupEntry{
			{
				Binding: 0, Buffer: renderer.dynamic,
				Offset: avatarCameraOffset, Size: avatarCameraBytes,
			},
			{
				Binding: 1, Buffer: renderer.dynamic,
				Offset: dropInstanceOffset, Size: dropInstanceSize,
			},
		},
	})
	return renderer
}

// Render 在 terrain 与 avatar 之后编码固定容量的掉落物 pass。
func (renderer *ItemDropRenderer) Render(
	encoder gfx.CommandEncoder,
	target, depth gfx.TextureView,
	camera Camera,
	serverTick uint64,
	drops []ItemDrop,
) {
	renderer.parts = buildItemDropParts(renderer.parts[:0], serverTick, drops)
	if len(renderer.parts) == 0 {
		return
	}
	encodeAvatarPartsInto(
		renderer.upload[dropInstanceOffset:dropIndirectOffset], renderer.parts,
	)
	encodeAvatarCameraInto(
		renderer.upload[avatarCameraOffset:avatarCameraBytes], camera,
	)
	encodeAvatarUint32sInto(renderer.upload[dropIndirectOffset:dropUploadBytes], []uint32{
		uint32(len(avatarCubeIndices)), uint32(len(renderer.parts)), 0, 0, 0,
	})
	renderer.dynamic.Write(0, renderer.upload)

	pass := encoder.BeginRenderPass(gfx.RenderPassDesc{
		Label:     "item drop pass",
		ColorView: target,
		DepthView: depth,
		LoadClear: false,
	})
	pass.SetPipeline(renderer.pipeline)
	pass.SetBindGroup(0, renderer.bind)
	pass.SetVertexBuffer(0, renderer.vertices, 0)
	pass.SetIndexBuffer(renderer.indices, 0)
	pass.DrawIndexedIndirect(renderer.dynamic, dropIndirectOffset)
	pass.End()
}

// buildItemDropParts 按输入顺序构建最多 maxItemDrops 个立方体实例。
func buildItemDropParts(dst []avatarPart, serverTick uint64, drops []ItemDrop) []avatarPart {
	for _, drop := range drops {
		if len(dst) == maxItemDrops {
			break
		}
		color, ok := itemDropColor(drop.Item)
		if !ok {
			continue
		}
		phase := dropAnimationPhase(serverTick, drop.ID)
		center := mgl32.Vec3{
			float32(drop.Block.X) + 0.5,
			float32(drop.Block.Y) + dropBaseAltitude +
				dropFloatHeight*float32(math.Sin(float64(phase.float))),
			float32(drop.Block.Z) + 0.5,
		}
		transform := mgl32.Translate3D(center.X(), center.Y(), center.Z()).
			Mul4(mgl32.HomogRotate3DY(phase.spin)).
			Mul4(mgl32.Scale3D(dropCubeSize, dropCubeSize, dropCubeSize))
		dst = append(dst, avatarPart{transform: transform, color: color})
	}
	return dst
}

type dropPhase struct {
	spin  float32
	float float32
}

// dropAnimationPhase 由 server tick 与稳定 ID 混合得到确定性相位。
func dropAnimationPhase(serverTick uint64, id core.DropID) dropPhase {
	offset := uint64(id.Generation)*31 + uint64(id.Slot)*7 +
		uint64(uint32(id.Chunk.X))*3 + uint64(uint32(id.Chunk.Z))
	spinTick := (serverTick + offset) % dropSpinPeriod
	floatTick := (serverTick + offset) % dropFloatPeriod
	return dropPhase{
		spin:  2 * math.Pi * float32(spinTick) / dropSpinPeriod,
		float: 2 * math.Pi * float32(floatTick) / dropFloatPeriod,
	}
}

// ItemColor 返回 HUD 与掉落物共享的稳定物品基色。
func ItemColor(item core.ItemID) [4]float32 {
	switch item {
	case core.ItemStone:
		return [4]float32{128.0 / 255, 128.0 / 255, 128.0 / 255, 1}
	case core.ItemDirt:
		return [4]float32{134.0 / 255, 96.0 / 255, 67.0 / 255, 1}
	case core.ItemGrass:
		return [4]float32{88.0 / 255, 140.0 / 255, 60.0 / 255, 1}
	case core.ItemStoneBrick:
		return [4]float32{122.0 / 255, 118.0 / 255, 112.0 / 255, 1}
	case core.ItemCoal:
		return [4]float32{38.0 / 255, 38.0 / 255, 40.0 / 255, 1}
	case core.ItemRawIron:
		return [4]float32{196.0 / 255, 154.0 / 255, 118.0 / 255, 1}
	case core.ItemIronIngot:
		return [4]float32{220.0 / 255, 220.0 / 255, 224.0 / 255, 1}
	case core.ItemFurnace:
		return [4]float32{88.0 / 255, 86.0 / 255, 88.0 / 255, 1}
	case core.ItemIronBlock:
		return [4]float32{214.0 / 255, 214.0 / 255, 216.0 / 255, 1}
	case core.ItemChest:
		return [4]float32{156.0 / 255, 108.0 / 255, 58.0 / 255, 1}
	case core.ItemStonePickaxe:
		return [4]float32{104.0 / 255, 112.0 / 255, 120.0 / 255, 1}
	case core.ItemIronPickaxe:
		return [4]float32{190.0 / 255, 198.0 / 255, 210.0 / 255, 1}
	case core.ItemBrokenStonePickaxe:
		return [4]float32{66.0 / 255, 60.0 / 255, 58.0 / 255, 1}
	case core.ItemBrokenIronPickaxe:
		return [4]float32{96.0 / 255, 88.0 / 255, 92.0 / 255, 1}
	default:
		return [4]float32{}
	}
}

// itemDropColor 复用与程序化方块一致的稳定基色。
func itemDropColor(item core.ItemID) ([4]float32, bool) {
	if !core.RegisteredItem(item) {
		return [4]float32{}, false
	}
	return ItemColor(item), true
}

// ItemDropBlock 把掉落物的区块内索引还原为世界方块位置。
func ItemDropBlock(chunk core.ChunkPos, blockIndex uint32) (core.BlockPos, bool) {
	return world.BlockPosFromChunkIndex(chunk, blockIndex)
}

// Release 释放渲染器自有的 GPU 句柄。
func (renderer *ItemDropRenderer) Release() {
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
