package render

import (
	_ "embed"
	"encoding/binary"
	"math"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/gfx"
	"minecraft-go/internal/mesh"
)

//go:embed shader/cull.wgsl
var cullShader string

const sectionRecordBytes = 32

type culler struct {
	pipeline gfx.ComputePipeline
	uniforms gfx.Buffer
	sections gfx.Buffer
	bind     gfx.BindGroup
}

func newCuller(
	dev gfx.Device,
	faces, visible, args gfx.Buffer,
	maxCandidates uint32,
) *culler {
	c := &culler{}
	c.uniforms = dev.CreateBuffer(gfx.BufferDesc{
		Label: "cull uniforms",
		Size:  16,
		Usage: gfx.BufferUsageUniform | gfx.BufferUsageCopyDst,
	})
	c.sections = dev.CreateBuffer(gfx.BufferDesc{
		Label: "cull candidate sections",
		Size:  uint64(maxCandidates) * sectionRecordBytes,
		Usage: gfx.BufferUsageStorage | gfx.BufferUsageCopyDst,
	})

	layout := gfx.BindGroupLayout{
		Label: "terrain cull layout",
		Entries: []gfx.BindGroupLayoutEntry{
			{Binding: 0, Type: gfx.BindingUniformBuffer, VisibleIn: gfx.StageCompute},
			{Binding: 1, Type: gfx.BindingStorageBufferRO, VisibleIn: gfx.StageCompute},
			{Binding: 2, Type: gfx.BindingStorageBufferRO, VisibleIn: gfx.StageCompute},
			{Binding: 3, Type: gfx.BindingStorageBufferRW, VisibleIn: gfx.StageCompute},
			{Binding: 4, Type: gfx.BindingStorageBufferRW, VisibleIn: gfx.StageCompute},
		},
	}
	module := dev.CreateShaderModule(cullShader)
	c.pipeline = dev.CreateComputePipeline(gfx.ComputePipelineDesc{
		Label:      "terrain cull",
		Shader:     module,
		Entry:      "cs_main",
		BindGroups: []gfx.BindGroupLayout{layout},
	})
	module.Release()
	c.bind = dev.CreateBindGroup(gfx.BindGroupDesc{
		Label:  "terrain cull resources",
		Layout: layout,
		Entries: []gfx.BindGroupEntry{
			{Binding: 0, Buffer: c.uniforms},
			{Binding: 1, Buffer: c.sections},
			{Binding: 2, Buffer: faces},
			{Binding: 3, Buffer: visible},
			{Binding: 4, Buffer: args},
		},
	})
	return c
}

func (c *culler) dispatch(enc gfx.CommandEncoder, candidates int, camPos mgl32.Vec3) {
	if candidates == 0 {
		return
	}
	c.uniforms.Write(0, vec3UniformBytes(camPos))
	pass := enc.BeginComputePass("terrain cull pass")
	pass.SetPipeline(c.pipeline)
	pass.SetBindGroup(0, c.bind)
	pass.Dispatch(uint32(candidates), 1, 1)
	pass.End()
}

func (c *culler) Release() {
	if c.bind != nil {
		c.bind.Release()
	}
	if c.pipeline != nil {
		c.pipeline.Release()
	}
	if c.sections != nil {
		c.sections.Release()
	}
	if c.uniforms != nil {
		c.uniforms.Release()
	}
}

func vec3UniformBytes(v mgl32.Vec3) []byte {
	out := make([]byte, 16)
	binary.LittleEndian.PutUint32(out[0:], math.Float32bits(v[0]))
	binary.LittleEndian.PutUint32(out[4:], math.Float32bits(v[1]))
	binary.LittleEndian.PutUint32(out[8:], math.Float32bits(v[2]))
	return out
}

// RunCullForTest 把 quads 当作单个区段送入 GPU 剔除并读回存活实例。
func RunCullForTest(dev gfx.Device, quads []mesh.Quad, camPos mgl32.Vec3) []mesh.Quad {
	faceCount := max(len(quads), 1)
	faces := dev.CreateBuffer(gfx.BufferDesc{
		Label: "test cull faces",
		Size:  uint64(faceCount) * 8,
		Usage: gfx.BufferUsageStorage | gfx.BufferUsageCopyDst,
	})
	defer faces.Release()
	if len(quads) > 0 {
		packed := make([]uint64, len(quads))
		for i, q := range quads {
			packed[i] = q.Pack()
		}
		faces.Write(0, uint64sToBytes(packed))
	}

	visible := dev.CreateBuffer(gfx.BufferDesc{
		Label: "test cull visible",
		Size:  uint64(faceCount) * 16,
		Usage: gfx.BufferUsageStorage | gfx.BufferUsageCopySrc,
	})
	defer visible.Release()
	args := dev.CreateBuffer(gfx.BufferDesc{
		Label: "test cull args",
		Size:  20,
		Usage: gfx.BufferUsageStorage | gfx.BufferUsageIndirect |
			gfx.BufferUsageCopyDst | gfx.BufferUsageCopySrc,
	})
	defer args.Release()
	args.Write(0, uint32sToBytes([]uint32{6, 0, 0, 0, 0}))

	c := newCuller(dev, faces, visible, args, 1)
	defer c.Release()
	// origin vec4i + face_offset/count/origin_idx/pad。
	rec := make([]byte, sectionRecordBytes)
	binary.LittleEndian.PutUint32(rec[20:], uint32(len(quads)))
	c.sections.Write(0, rec)

	enc := dev.CreateCommandEncoder()
	c.dispatch(enc, 1, camPos)
	cmd := enc.Finish()
	dev.Submit(cmd)
	cmd.Release()
	dev.Poll(true)

	argsData := args.ReadBack()
	count := binary.LittleEndian.Uint32(argsData[4:])
	if count == 0 {
		return nil
	}
	data := visible.ReadBack()
	out := make([]mesh.Quad, count)
	for i := range out {
		off := i * 16
		packed := uint64(binary.LittleEndian.Uint32(data[off:])) |
			uint64(binary.LittleEndian.Uint32(data[off+4:]))<<32
		out[i] = mesh.UnpackQuad(packed)
	}
	return out
}
