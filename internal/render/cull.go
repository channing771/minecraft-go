package render

import (
	_ "embed"
	"encoding/binary"
	"math"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/gfx"
	"github.com/channing771/mornlea/internal/mesh"
)

//go:embed shader/cull.wgsl
var cullShader string

const sectionRecordBytes = 32

type culler struct {
	dev      gfx.Device
	pipeline gfx.ComputePipeline
	uniforms gfx.Buffer
	sections gfx.Buffer
	bind     gfx.BindGroup
	layout   gfx.BindGroupLayout

	faces, visible, args gfx.Buffer
	hizView              gfx.TextureView
	dummyHiZ             gfx.Texture
	dummyHiZView         gfx.TextureView
	uniformData          [128]byte
}

func newCuller(
	dev gfx.Device,
	faces, visible, args gfx.Buffer,
	maxCandidates uint32,
) *culler {
	c := &culler{dev: dev, faces: faces, visible: visible, args: args}
	c.uniforms = dev.CreateBuffer(gfx.BufferDesc{
		Label: "cull uniforms",
		Size:  128,
		Usage: gfx.BufferUsageUniform | gfx.BufferUsageCopyDst,
	})
	c.sections = dev.CreateBuffer(gfx.BufferDesc{
		Label: "cull candidate sections",
		Size:  uint64(maxCandidates) * sectionRecordBytes,
		Usage: gfx.BufferUsageStorage | gfx.BufferUsageCopyDst,
	})

	c.dummyHiZ = dev.CreateTexture(gfx.TextureDesc{
		Label: "cull dummy hi-z",
		Width: 1, Height: 1,
		Format:    gfx.FormatR32Float,
		Dimension: gfx.TextureDimension2D,
		Usage:     gfx.TextureUsageBinding | gfx.TextureUsageCopyDst,
	})
	one := make([]byte, 4)
	binary.LittleEndian.PutUint32(one, math.Float32bits(1))
	c.dummyHiZ.WriteLayer(0, 0, one)
	c.dummyHiZView = c.dummyHiZ.View(gfx.TextureViewDesc{
		Dimension: gfx.TextureViewDimension2D,
	})
	c.hizView = c.dummyHiZView

	c.layout = gfx.BindGroupLayout{
		Label: "terrain cull layout",
		Entries: []gfx.BindGroupLayoutEntry{
			{Binding: 0, Type: gfx.BindingUniformBuffer, VisibleIn: gfx.StageCompute},
			{Binding: 1, Type: gfx.BindingStorageBufferRO, VisibleIn: gfx.StageCompute},
			{Binding: 2, Type: gfx.BindingStorageBufferRO, VisibleIn: gfx.StageCompute},
			{Binding: 3, Type: gfx.BindingStorageBufferRW, VisibleIn: gfx.StageCompute},
			{Binding: 4, Type: gfx.BindingStorageBufferRW, VisibleIn: gfx.StageCompute},
			{
				Binding: 5, Type: gfx.BindingSampledTextureUnfilterableFloat,
				VisibleIn: gfx.StageCompute, ViewDimension: gfx.TextureViewDimension2D,
			},
		},
	}
	module := dev.CreateShaderModule(cullShader)
	c.pipeline = dev.CreateComputePipeline(gfx.ComputePipelineDesc{
		Label:      "terrain cull",
		Shader:     module,
		Entry:      "cs_main",
		BindGroups: []gfx.BindGroupLayout{c.layout},
	})
	module.Release()
	c.rebuildBind()
	return c
}

func (c *culler) rebuildBind() {
	if c.bind != nil {
		c.bind.Release()
	}
	c.bind = c.dev.CreateBindGroup(gfx.BindGroupDesc{
		Label:  "terrain cull resources",
		Layout: c.layout,
		Entries: []gfx.BindGroupEntry{
			{Binding: 0, Buffer: c.uniforms},
			{Binding: 1, Buffer: c.sections},
			{Binding: 2, Buffer: c.faces},
			{Binding: 3, Buffer: c.visible},
			{Binding: 4, Buffer: c.args},
			{Binding: 5, Texture: c.hizView},
		},
	})
}

func (c *culler) dispatch(enc gfx.CommandEncoder, candidates int, camPos mgl32.Vec3) {
	c.dispatchCamera(enc, candidates, Camera{
		ViewProj: mgl32.Ident4(),
		Pos:      camPos,
	}, nil, false)
}

func (c *culler) dispatchCamera(
	enc gfx.CommandEncoder,
	candidates int,
	cam Camera,
	z *hiZ,
	enableHiZ bool,
) {
	if candidates == 0 {
		return
	}
	if z != nil && c.hizView != z.fullView {
		c.hizView = z.fullView
		c.rebuildBind()
	}
	writeCullUniformBytes(c.uniformData[:], cam, z, enableHiZ)
	c.uniforms.Write(0, c.uniformData[:])
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
	if c.dummyHiZView != nil {
		c.dummyHiZView.Release()
	}
	if c.dummyHiZ != nil {
		c.dummyHiZ.Release()
	}
}

func writeCullUniformBytes(out []byte, cam Camera, z *hiZ, enabled bool) {
	clear(out)
	putFloat := func(offset int, value float32) {
		binary.LittleEndian.PutUint32(out[offset:], math.Float32bits(value))
	}
	putFloat(0, cam.Pos[0])
	putFloat(4, cam.Pos[1])
	putFloat(8, cam.Pos[2])
	for i, value := range cam.ViewProj {
		putFloat(16+i*4, value)
	}
	if z != nil {
		putFloat(80, float32(z.viewportW))
		putFloat(84, float32(z.viewportH))
		putFloat(88, float32(z.levels-1))
		putFloat(96, float32(z.viewportW)/float32(z.paddedW))
		putFloat(100, float32(z.viewportH)/float32(z.paddedH))
	}
	if enabled {
		binary.LittleEndian.PutUint32(out[112:], 1)
	}
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
