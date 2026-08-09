package render

import (
	_ "embed"
	"encoding/binary"
	"math"

	"minecraft-go/internal/gfx"
)

const damageOverlayUniformBytes = 16

//go:embed shader/damage_overlay.wgsl
var damageOverlayShader string

// DamageOverlayRenderer 绘制服务端确认受伤后的固定屏幕边缘渐变。
type DamageOverlayRenderer struct {
	uniform  gfx.Buffer
	pipeline gfx.RenderPipeline
	bind     gfx.BindGroup
	upload   [damageOverlayUniformBytes]byte
}

// NewDamageOverlayRenderer 创建一个不使用深度附件的固定透明管线。
func NewDamageOverlayRenderer(
	device gfx.Device,
	colorFormat gfx.TextureFormat,
) *DamageOverlayRenderer {
	renderer := &DamageOverlayRenderer{}
	renderer.uniform = device.CreateBuffer(gfx.BufferDesc{
		Label: "damage overlay uniform", Size: damageOverlayUniformBytes,
		Usage: gfx.BufferUsageUniform | gfx.BufferUsageCopyDst,
	})
	layout := gfx.BindGroupLayout{
		Label: "damage overlay layout",
		Entries: []gfx.BindGroupLayoutEntry{{
			Binding: 0, Type: gfx.BindingUniformBuffer, VisibleIn: gfx.StageFragment,
		}},
	}
	module := device.CreateShaderModule(damageOverlayShader)
	renderer.pipeline = device.CreateRenderPipeline(gfx.RenderPipelineDesc{
		Label: "damage overlay", Shader: module,
		VertexEntry: "vs_main", FragmentEntry: "fs_main",
		BindGroups:  []gfx.BindGroupLayout{layout},
		ColorFormat: colorFormat, Blend: gfx.BlendAlpha,
	})
	module.Release()
	renderer.bind = device.CreateBindGroup(gfx.BindGroupDesc{
		Label: "damage overlay resources", Layout: layout,
		Entries: []gfx.BindGroupEntry{{
			Binding: 0, Buffer: renderer.uniform, Size: damageOverlayUniformBytes,
		}},
	})
	return renderer
}

// Render 在 HUD 之前绘制强度已钳制的全屏边缘反馈。
func (renderer *DamageOverlayRenderer) Render(
	encoder gfx.CommandEncoder,
	target gfx.TextureView,
	strength float32,
) {
	if !(strength > 0) { // 同时拒绝零、负值与 NaN。
		return
	}
	if strength > 1 {
		strength = 1
	}
	clear(renderer.upload[:])
	binary.LittleEndian.PutUint32(renderer.upload[:4], math.Float32bits(strength))
	renderer.uniform.Write(0, renderer.upload[:])
	pass := encoder.BeginRenderPass(gfx.RenderPassDesc{
		Label: "damage overlay pass", ColorView: target, LoadClear: false,
	})
	pass.SetPipeline(renderer.pipeline)
	pass.SetBindGroup(0, renderer.bind)
	pass.Draw(3, 1)
	pass.End()
}

// Release 幂等释放 renderer 自己持有的 GPU 资源。
func (renderer *DamageOverlayRenderer) Release() {
	if renderer.bind != nil {
		renderer.bind.Release()
		renderer.bind = nil
	}
	if renderer.pipeline != nil {
		renderer.pipeline.Release()
		renderer.pipeline = nil
	}
	if renderer.uniform != nil {
		renderer.uniform.Release()
		renderer.uniform = nil
	}
}
