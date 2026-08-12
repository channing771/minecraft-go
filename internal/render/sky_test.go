package render

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/assets"
	"minecraft-go/internal/core"
	"minecraft-go/internal/gfx"
	"minecraft-go/internal/mesh"
)

const skyHeadlessSize = 64

func TestSkyHeadlessOnlySkipsUnavailableAdapter(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"adapter unavailable", errors.New("gfx: 请求 adapter 失败: no suitable adapter found"), true},
		{"device request", errors.New("gfx: 请求 device 失败: device lost"), false},
		{"other initialization", errors.New("gfx: unexpected initialization failure"), false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := skyHeadlessAdapterUnavailable(test.err); got != test.want {
				t.Fatalf("skyHeadlessAdapterUnavailable(%q)=%v want=%v", test.err, got, test.want)
			}
		})
	}
}

func skyHeadlessAdapterUnavailable(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "gfx: 请求 adapter 失败:")
}

func TestSkyPipelineConfiguration(t *testing.T) {
	device := &skyTestDevice{}
	renderer := newRenderer(device, assets.NewRegistry(), gfx.FormatBGRA8UnormSrgb, 16, 1024, 4)
	defer renderer.Release()

	pipeline := device.renderPipeline(t, "sky")
	if pipeline.desc.ColorFormat != gfx.FormatBGRA8UnormSrgb {
		t.Fatalf("sky color format=%v want=%v", pipeline.desc.ColorFormat, gfx.FormatBGRA8UnormSrgb)
	}
	if pipeline.desc.DepthFormat != gfx.FormatDepth32Float {
		t.Fatalf("sky depth format=%v want=%v", pipeline.desc.DepthFormat, gfx.FormatDepth32Float)
	}
	if pipeline.desc.DepthWrite {
		t.Fatal("sky depth write=true want=false")
	}
	if got := len(pipeline.desc.BindGroups); got != 1 {
		t.Fatalf("sky bind-group layouts=%d want=1", got)
	}
	entries := pipeline.desc.BindGroups[0].Entries
	if got := len(entries); got != 1 {
		t.Fatalf("sky bindings=%d want=1", got)
	}
	if got := entries[0]; got.Binding != 0 || got.Type != gfx.BindingUniformBuffer ||
		got.VisibleIn != gfx.StageVertex|gfx.StageFragment {
		t.Fatalf("sky binding=%+v want binding 0 uniform visible in vertex+fragment", got)
	}

	uniform := device.buffer(t, "sky uniform")
	if uniform.desc.Size != 112 {
		t.Fatalf("sky uniform bytes=%d want=112", uniform.desc.Size)
	}
	if want := gfx.BufferUsageUniform | gfx.BufferUsageCopyDst; uniform.desc.Usage != want {
		t.Fatalf("sky uniform usage=%v want=%v", uniform.desc.Usage, want)
	}
	bind := device.bindGroup(t, "sky resources")
	if got := len(bind.desc.Entries); got != 1 || bind.desc.Entries[0].Binding != 0 || bind.desc.Entries[0].Buffer != uniform {
		t.Fatalf("sky bind entries=%+v want one uniform binding", bind.desc.Entries)
	}
}

func TestSkyDrawsFullscreenTriangleBeforeTerrain(t *testing.T) {
	device := &skyTestDevice{}
	renderer := newRenderer(device, assets.NewRegistry(), gfx.FormatRGBA8Unorm, 16, 1024, 4)
	defer renderer.Release()
	encoder := &skyTestEncoder{}

	renderer.Render(encoder, &skyTestView{}, &skyTestView{}, Camera{ViewProj: mgl32.Ident4()})

	if got := len(encoder.passes); got != 1 {
		t.Fatalf("render passes=%d want=1", got)
	}
	want := []string{"pipeline:sky", "draw:sky:3:1", "pipeline:terrain", "draw:terrain:indirect"}
	if got := fmt.Sprint(encoder.passes[0].commands); got != fmt.Sprint(want) {
		t.Fatalf("draw commands=%v want=%v", encoder.passes[0].commands, want)
	}
}

func TestSkyReleaseIsIdempotent(t *testing.T) {
	device := &skyTestDevice{}
	renderer := newRenderer(device, assets.NewRegistry(), gfx.FormatRGBA8Unorm, 16, 1024, 4)
	uniform := device.buffer(t, "sky uniform")
	pipeline := device.renderPipeline(t, "sky")
	bind := device.bindGroup(t, "sky resources")

	renderer.Release()
	renderer.Release()

	for name, releases := range map[string]int{
		"uniform":  uniform.releases,
		"pipeline": pipeline.releases,
		"bind":     bind.releases,
	} {
		if releases != 1 {
			t.Errorf("sky %s release calls=%d want=1", name, releases)
		}
	}
}

func TestSkyUniformLayoutAndUpload(t *testing.T) {
	device := &skyTestDevice{}
	renderer := newRenderer(device, assets.NewRegistry(), gfx.FormatRGBA8Unorm, 16, 1024, 4)
	defer renderer.Release()
	viewProjInv := mgl32.Mat4{
		1, 2, 3, 4,
		5, 6, 7, 8,
		9, 10, 11, 12,
		13, 14, 15, 16,
	}

	renderer.Render(&skyTestEncoder{}, &skyTestView{}, &skyTestView{}, Camera{
		ViewProj:       mgl32.Ident4(),
		ViewProjInv:    viewProjInv,
		Pos:            mgl32.Vec3{24, 64, -168},
		CloudOffset:    CloudOffset{Local: 1.5, MacroX: 7},
		SunDirection:   [3]float32{0.25, 0.5, 0.75},
		Daylight:       0.8,
		StarVisibility: 0.6,
	})

	terrainWrites := device.buffer(t, "terrain camera").writes
	if got := len(terrainWrites); got != 1 || len(terrainWrites[0]) != 80 {
		t.Fatalf("terrain uniform writes/bytes=%d/%d want=1/80", got, writeBytes(terrainWrites))
	}
	skyWrites := device.buffer(t, "sky uniform").writes
	if got := len(skyWrites); got != 1 || len(skyWrites[0]) != 112 {
		t.Fatalf("sky uniform writes/bytes=%d/%d want=1/112", got, writeBytes(skyWrites))
	}
	data := skyWrites[0]
	for index, want := range viewProjInv {
		if got := float32At(data, index*4); got != want {
			t.Fatalf("sky inverse matrix[%d]=%v want=%v", index, got, want)
		}
	}
	for offset, want := range map[int]float32{
		64:  0.25,
		68:  0.5,
		72:  0.75,
		76:  0.8,
		80:  0.6,
		96:  24,
		100: 64,
		104: -168,
		108: 1.5,
	} {
		if got := float32At(data, offset); got != want {
			t.Fatalf("sky uniform float at %d=%v want=%v", offset, got, want)
		}
	}
	if got := binary.LittleEndian.Uint32(data[84:88]); got != 7 {
		t.Fatalf("sky macro X offset=%d want=7", got)
	}
	for offset, value := range data[88:96] {
		if value != 0 {
			t.Fatalf("sky padding byte at %d=%d want=0", offset+88, value)
		}
	}
}

func TestSkyHeadlessCloudHashFixedSample(t *testing.T) {
	lowBits, active, filled, _, _ := skyCloudFixedSample(t)
	if want := [4]uint32{72, 69, 62, 53}; lowBits != want {
		t.Fatalf("生产 WGSL 固定 macro 样本 low2=%v，想要 %v", lowBits, want)
	}
	if active != 184 || filled != 920 {
		t.Fatalf("生产 WGSL 固定 macro 样本 active/filled=%d/%d，想要 184/920", active, filled)
	}
	if got := float64(filled) / (256 * 16); got != 0.224609375 {
		t.Fatalf("生产 WGSL 固定 macro 样本覆盖率=%v，想要 0.224609375", got)
	}
}

func TestSkyHeadlessCloudMaskFixedGrid(t *testing.T) {
	_, _, _, got, asymmetric := skyCloudFixedSample(t)
	if want := [8]uint8{2, 71, 226, 64}; got != want {
		t.Fatalf("生产 cloud_mask 的固定 8x8 输出=%v，想要 %v", got, want)
	}
	if want := [4]uint8{0, 2, 7, 2}; asymmetric != want {
		t.Fatalf("生产 cloud_mask 的非对称 center 4x4 输出=%v，想要 %v", asymmetric, want)
	}
}

func TestSkyHeadlessPixels(t *testing.T) {
	device, err := gfx.NewHeadlessDevice()
	if err != nil {
		if skyHeadlessAdapterUnavailable(err) {
			t.Skipf("本机无可用 GPU 适配器: %v", err)
		}
		t.Fatalf("创建 headless GPU device: %v", err)
	}
	defer device.Release()
	renderer := newRenderer(device, assets.NewRegistry(), gfx.FormatRGBA8Unorm, 64, 1024, 8)
	defer renderer.Release()

	zenithPosition := mgl32.Vec3{0, 0, 0}
	zenithDirection := mgl32.Vec3{0, 1, 0}
	t.Run("正午天顶太阳为四度暖白圆盘", func(t *testing.T) {
		pixels := renderSkyHeadless(t, device, renderer, skyCameraAt(zenithPosition, zenithDirection, 6000))
		center := skyPixel(pixels, skyHeadlessSize/2, skyHeadlessSize/2)
		corner := skyPixel(pixels, 4, 4)
		if center[0] < 220 || center[1] < 180 || center[0] <= corner[0]+40 {
			t.Fatalf("noon center=%v corner=%v want warm bright sun", center, corner)
		}
		diameter := 0
		for x := 0; x < skyHeadlessSize; x++ {
			pixel := skyPixel(pixels, x, skyHeadlessSize/2)
			if pixel[0] > 180 && pixel[0] > pixel[2]+20 {
				diameter++
			}
		}
		if diameter < 3 || diameter > 7 {
			t.Fatalf("sun diameter=%d pixels want 3..7 for 4 degrees", diameter)
		}
	})

	t.Run("午夜天顶显示月亮和星空", func(t *testing.T) {
		pixels := renderSkyHeadless(t, device, renderer, skyCameraAt(zenithPosition, zenithDirection, 18000))
		center := skyPixel(pixels, skyHeadlessSize/2, skyHeadlessSize/2)
		corner := skyPixel(pixels, 4, 4)
		if center[2] < 200 || center[0] < 140 || skyBrightness(center) <= skyBrightness(corner)+100 {
			t.Fatalf("midnight center=%v corner=%v want cold bright moon", center, corner)
		}
		if stars := countSkyStars(pixels, true); stars < 4 {
			t.Fatalf("midnight star pixels=%d want>=4", stars)
		}
	})

	t.Run("云层上方相机平移不改变天体", func(t *testing.T) {
		first := renderSkyHeadless(t, device, renderer, skyCameraAt(mgl32.Vec3{0, 193, 0}, zenithDirection, 18000))
		moved := renderSkyHeadless(t, device, renderer, skyCameraAt(mgl32.Vec3{4, 193, 2}, zenithDirection, 18000))
		if !bytes.Equal(first, moved) {
			differences, bounds := skyPixelDifferences(first, moved)
			t.Fatalf("translated camera changed %d sky pixels in %v", differences, bounds)
		}
	})

	t.Run("旋转改变星图且往返恢复", func(t *testing.T) {
		position := mgl32.Vec3{1, 2, 3}
		north := mgl32.Vec3{0, 0, -1}
		east := mgl32.Vec3{1, 0, 0}
		first := renderSkyHeadless(t, device, renderer, skyCameraAt(position, north, 18000))
		rotated := renderSkyHeadless(t, device, renderer, skyCameraAt(position, east, 18000))
		returned := renderSkyHeadless(t, device, renderer, skyCameraAt(position, north, 18000))
		if bytes.Equal(first, rotated) {
			t.Fatal("rotated camera kept identical star pixels")
		}
		if !bytes.Equal(first, returned) {
			t.Fatal("returned camera did not restore identical sky pixels")
		}
		if top, bottom := countSkyHalfStars(first, true), countSkyHalfStars(first, false); top < 2 || bottom != 0 {
			t.Fatalf("horizon star mask top/bottom=%d/%d want top>=2 bottom=0", top, bottom)
		}
	})

	t.Run("地形覆盖天体", func(t *testing.T) {
		position := mgl32.Vec3{2, 0.5, 0.5}
		camera := skyCameraAt(position, mgl32.Vec3{-1, 0, 0}, 11900)
		angle := float32(math.Pi / 180)
		camera.SunDirection = [3]float32{-float32(math.Cos(float64(angle))), float32(math.Sin(float64(angle))), 0}
		skyOnly := renderSkyHeadless(t, device, renderer, camera)
		renderer.QueueSection(core.SectionPos{Y: 4}, []mesh.Quad{{
			W: 1, H: 1, Face: mesh.FacePosX, Mat: 0, AO: 0xff, Light: 0xf0,
		}})
		renderer.BeginFrame()
		renderer.FlushUploads(core.ChunkPos{})
		withTerrain := renderSkyHeadless(t, device, renderer, camera)
		skyCenter := skyPixel(skyOnly, skyHeadlessSize/2, skyHeadlessSize/2)
		terrainCenter := skyPixel(withTerrain, skyHeadlessSize/2, skyHeadlessSize/2)
		if skyCenter[0] < 180 {
			t.Fatalf("sky-only center=%v want visible sun", skyCenter)
		}
		if terrainCenter == skyCenter || terrainCenter[0] >= 180 {
			differences, bounds := skyPixelDifferences(skyOnly, withTerrain)
			t.Fatalf("terrain center=%v sky center=%v differences=%d bounds=%v stats=%+v want terrain coverage",
				terrainCenter, skyCenter, differences, bounds, renderer.LastFrameStats())
		}
	})
}

func skyCameraAt(position, direction mgl32.Vec3, phase uint64) Camera {
	up := mgl32.Vec3{0, 1, 0}
	if float32(math.Abs(float64(direction.Dot(up)))) > 0.99 {
		up = mgl32.Vec3{0, 0, -1}
	}
	viewProj := core.Perspective(mgl32.DegToRad(60), 1, 0.1, 100).
		Mul4(mgl32.LookAtV(position, position.Add(direction), up))
	dayNight := DayNightAt(phase)
	return Camera{
		ViewProj:       viewProj,
		ViewProjInv:    viewProj.Inv(),
		Pos:            position,
		CloudOffset:    CloudOffsetAt(phase),
		SunDirection:   dayNight.SunDirection,
		Daylight:       dayNight.Daylight,
		StarVisibility: dayNight.StarVisibility,
		SkyColor:       dayNight.ClearColor,
	}
}

func BenchmarkSkyRender(b *testing.B) {
	device := &skyTestDevice{}
	renderer := newRenderer(device, assets.NewRegistry(), gfx.FormatRGBA8Unorm, 16, 1024, 4)
	b.Cleanup(renderer.Release)
	for _, buffer := range device.buffers {
		buffer.discardWrites = true
	}
	encoder := &skyTestEncoder{discardCommands: true}
	camera := skyCameraAt(mgl32.Vec3{115, 64, -152}, mgl32.Vec3{0, 1, 0}, 6000)
	renderer.Render(encoder, nil, nil, camera)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		renderer.Render(encoder, nil, nil, camera)
	}
}

func renderSkyHeadless(t *testing.T, device gfx.Device, renderer *Renderer, camera Camera) []byte {
	t.Helper()
	color := device.CreateTexture(gfx.TextureDesc{
		Label: "sky headless color", Width: skyHeadlessSize, Height: skyHeadlessSize,
		Format: gfx.FormatRGBA8Unorm,
		Usage:  gfx.TextureUsageRenderTarget | gfx.TextureUsageBinding,
	})
	defer color.Release()
	colorView := color.View(gfx.TextureViewDesc{Dimension: gfx.TextureViewDimension2D})
	defer colorView.Release()
	depth := device.CreateTexture(gfx.TextureDesc{
		Label: "sky headless depth", Width: skyHeadlessSize, Height: skyHeadlessSize,
		Format: gfx.FormatDepth32Float, Usage: gfx.TextureUsageRenderTarget,
	})
	defer depth.Release()
	depthView := depth.View(gfx.TextureViewDesc{
		Dimension: gfx.TextureViewDimension2D,
		Aspect:    gfx.AspectDepthOnly,
	})
	defer depthView.Release()

	output := device.CreateBuffer(gfx.BufferDesc{
		Label: "sky pixel output", Size: skyHeadlessSize * skyHeadlessSize * 4,
		Usage: gfx.BufferUsageStorage | gfx.BufferUsageCopySrc,
	})
	defer output.Release()
	layout := gfx.BindGroupLayout{
		Label: "sky pixel readback layout",
		Entries: []gfx.BindGroupLayoutEntry{
			{
				Binding: 0, Type: gfx.BindingSampledTextureFloat,
				VisibleIn: gfx.StageCompute, ViewDimension: gfx.TextureViewDimension2D,
			},
			{Binding: 1, Type: gfx.BindingStorageBufferRW, VisibleIn: gfx.StageCompute},
		},
	}
	module := device.CreateShaderModule(skyPixelReadbackShader)
	pipeline := device.CreateComputePipeline(gfx.ComputePipelineDesc{
		Label: "sky pixel readback", Shader: module, Entry: "cs_main",
		BindGroups: []gfx.BindGroupLayout{layout},
	})
	module.Release()
	defer pipeline.Release()
	bind := device.CreateBindGroup(gfx.BindGroupDesc{
		Label: "sky pixel readback resources", Layout: layout,
		Entries: []gfx.BindGroupEntry{
			{Binding: 0, Texture: colorView},
			{Binding: 1, Buffer: output},
		},
	})
	defer bind.Release()

	encoder := device.CreateCommandEncoder()
	renderer.Render(encoder, colorView, depthView, camera)
	pass := encoder.BeginComputePass("sky pixel readback pass")
	pass.SetPipeline(pipeline)
	pass.SetBindGroup(0, bind)
	pass.Dispatch(skyHeadlessSize/8, skyHeadlessSize/8, 1)
	pass.End()
	commands := encoder.Finish()
	device.Submit(commands)
	commands.Release()
	device.Poll(true)
	return output.ReadBack()
}

func skyPixel(pixels []byte, x, y int) [4]byte {
	offset := (y*skyHeadlessSize + x) * 4
	return [4]byte{pixels[offset], pixels[offset+1], pixels[offset+2], pixels[offset+3]}
}

func skyBrightness(pixel [4]byte) int {
	return int(pixel[0]) + int(pixel[1]) + int(pixel[2])
}

func skyCloudFixedSample(t *testing.T) ([4]uint32, uint32, uint32, [8]uint8, [4]uint8) {
	t.Helper()
	device, err := gfx.NewHeadlessDevice()
	if err != nil {
		if skyHeadlessAdapterUnavailable(err) {
			t.Skipf("本机无可用 GPU 适配器: %v", err)
		}
		t.Fatalf("创建 headless GPU device: %v", err)
	}
	defer device.Release()

	uniform := device.CreateBuffer(gfx.BufferDesc{
		Label: "cloud hash test sky uniform", Size: 112, Usage: gfx.BufferUsageUniform | gfx.BufferUsageCopyDst,
	})
	defer uniform.Release()
	uniformData := make([]byte, 112)
	binary.LittleEndian.PutUint32(uniformData[100:104], math.Float32bits(64))
	uniform.Write(0, uniformData)
	output := device.CreateBuffer(gfx.BufferDesc{
		Label: "cloud hash test output", Size: 4096 * 4, Usage: gfx.BufferUsageStorage | gfx.BufferUsageCopySrc,
	})
	defer output.Release()
	shader := device.CreateShaderModule(skyShader + `
struct CloudHashOutput {
    values: array<u32, 4096>,
};

@group(1) @binding(0) var<storage, read_write> cloud_hash_output: CloudHashOutput;

@compute @workgroup_size(64)
fn cloud_hash_fixed_sample(@builtin(global_invocation_id) id: vec3u) {
    if (id.x >= 4096u) {
        return;
    }
    let cell = vec2i(i32(id.x % 64u) - 32, i32(id.x / 64u) - 32);
    let direction = vec3f((f32(cell.x) * 16.0 + 8.0) / 128.0, 1.0, (f32(cell.y) * 16.0 + 8.0) / 128.0);
    var result = select(0u, 1u, cloud_mask(direction) > 0.5);
    let macro_cell = vec2i(floor(vec2f(cell) / 4.0));
    if (all(cell == macro_cell * 4)) {
        result = result | ((cloud_hash(macro_cell, 0u) & 3u) << 1u) | 8u;
    }
    cloud_hash_output.values[id.x] = result;
}
`)
	defer shader.Release()

	skyLayout := gfx.BindGroupLayout{Label: "cloud hash test sky layout", Entries: []gfx.BindGroupLayoutEntry{
		{Binding: 0, Type: gfx.BindingUniformBuffer, VisibleIn: gfx.StageVertex | gfx.StageFragment | gfx.StageCompute},
	}}
	outputLayout := gfx.BindGroupLayout{Label: "cloud hash test output layout", Entries: []gfx.BindGroupLayoutEntry{
		{Binding: 0, Type: gfx.BindingStorageBufferRW, VisibleIn: gfx.StageCompute},
	}}
	pipeline := device.CreateComputePipeline(gfx.ComputePipelineDesc{
		Label: "cloud hash fixed sample", Shader: shader, Entry: "cloud_hash_fixed_sample",
		BindGroups: []gfx.BindGroupLayout{skyLayout, outputLayout},
	})
	defer pipeline.Release()
	skyBind := device.CreateBindGroup(gfx.BindGroupDesc{
		Label: "cloud hash test sky bind", Layout: skyLayout, Entries: []gfx.BindGroupEntry{{Binding: 0, Buffer: uniform}},
	})
	defer skyBind.Release()
	outputBind := device.CreateBindGroup(gfx.BindGroupDesc{
		Label: "cloud hash test output bind", Layout: outputLayout, Entries: []gfx.BindGroupEntry{{Binding: 0, Buffer: output}},
	})
	defer outputBind.Release()

	encoder := device.CreateCommandEncoder()
	pass := encoder.BeginComputePass("cloud hash fixed sample")
	pass.SetPipeline(pipeline)
	pass.SetBindGroup(0, skyBind)
	pass.SetBindGroup(1, outputBind)
	pass.Dispatch(64, 1, 1)
	pass.End()
	command := encoder.Finish()
	device.Submit(command)
	command.Release()
	device.Poll(true)

	data := output.ReadBack()
	if len(data) != 4096*4 {
		t.Fatalf("生产 WGSL cloud mask readback bytes=%d，想要 %d", len(data), 4096*4)
	}
	var lowBits [4]uint32
	var active, filled uint32
	var activeMasks [16][16]bool
	var grid [8]uint8
	var asymmetric [4]uint8
	for z := 0; z < 64; z++ {
		for x := 0; x < 64; x++ {
			value := binary.LittleEndian.Uint32(data[(z*64+x)*4:])
			filled += value & 1
			activeMasks[z/4][x/4] = activeMasks[z/4][x/4] || value&1 != 0
			if x%4 == 0 && z%4 == 0 {
				lowBits[(value>>1)&3]++
			}
			if x >= 28 && x < 36 && z >= 28 && z < 36 && value&1 != 0 {
				grid[z-28] |= 1 << (x - 28)
			}
			if x >= 4 && x < 8 && z < 4 && value&1 != 0 {
				asymmetric[z] |= 1 << (x - 4)
			}
		}
	}
	for _, row := range activeMasks {
		for _, isActive := range row {
			if isActive {
				active++
			}
		}
	}
	return lowBits, active, filled, grid, asymmetric
}

func countSkyStars(pixels []byte, excludeCenter bool) int {
	count := 0
	for y := 0; y < skyHeadlessSize; y++ {
		for x := 0; x < skyHeadlessSize; x++ {
			if excludeCenter && (x-skyHeadlessSize/2)*(x-skyHeadlessSize/2)+(y-skyHeadlessSize/2)*(y-skyHeadlessSize/2) < 64 {
				continue
			}
			pixel := skyPixel(pixels, x, y)
			if pixel[0] > 80 && pixel[1] > 80 && pixel[2] > 80 {
				count++
			}
		}
	}
	return count
}

func countSkyHalfStars(pixels []byte, top bool) int {
	start, end := 0, skyHeadlessSize/2
	if !top {
		start, end = skyHeadlessSize/2, skyHeadlessSize
	}
	count := 0
	for y := start; y < end; y++ {
		for x := 0; x < skyHeadlessSize; x++ {
			pixel := skyPixel(pixels, x, y)
			if pixel[0] > 80 && pixel[1] > 80 && pixel[2] > 80 {
				count++
			}
		}
	}
	return count
}

func skyPixelDifferences(first, second []byte) (int, [4]int) {
	bounds := [4]int{skyHeadlessSize, skyHeadlessSize, -1, -1}
	count := 0
	for y := 0; y < skyHeadlessSize; y++ {
		for x := 0; x < skyHeadlessSize; x++ {
			offset := (y*skyHeadlessSize + x) * 4
			if bytes.Equal(first[offset:offset+4], second[offset:offset+4]) {
				continue
			}
			count++
			bounds[0] = min(bounds[0], x)
			bounds[1] = min(bounds[1], y)
			bounds[2] = max(bounds[2], x)
			bounds[3] = max(bounds[3], y)
		}
	}
	return count, bounds
}

const skyPixelReadbackShader = `
@group(0) @binding(0) var source: texture_2d<f32>;
@group(0) @binding(1) var<storage, read_write> output: array<u32>;

@compute @workgroup_size(8, 8)
fn cs_main(@builtin(global_invocation_id) id: vec3u) {
    if (id.x >= 64u || id.y >= 64u) {
        return;
    }
    let color = textureLoad(source, vec2i(id.xy), 0);
    output[id.y * 64u + id.x] = pack4x8unorm(color);
}
`

func writeBytes(writes [][]byte) int {
	if len(writes) == 0 {
		return 0
	}
	return len(writes[0])
}

type skyTestDevice struct {
	buffers         []*skyTestBuffer
	renderPipelines []*skyTestRenderPipeline
	bindGroups      []*skyTestBindGroup
}

func (device *skyTestDevice) CreateBuffer(desc gfx.BufferDesc) gfx.Buffer {
	buffer := &skyTestBuffer{desc: desc}
	device.buffers = append(device.buffers, buffer)
	return buffer
}

func (*skyTestDevice) CreateShaderModule(string) gfx.ShaderModule { return &skyTestShader{} }

func (device *skyTestDevice) CreateRenderPipeline(desc gfx.RenderPipelineDesc) gfx.RenderPipeline {
	pipeline := &skyTestRenderPipeline{desc: desc}
	device.renderPipelines = append(device.renderPipelines, pipeline)
	return pipeline
}

func (*skyTestDevice) CreateComputePipeline(gfx.ComputePipelineDesc) gfx.ComputePipeline {
	return &skyTestComputePipeline{}
}

func (device *skyTestDevice) CreateBindGroup(desc gfx.BindGroupDesc) gfx.BindGroup {
	group := &skyTestBindGroup{desc: desc}
	device.bindGroups = append(device.bindGroups, group)
	return group
}

func (*skyTestDevice) CreateTexture(desc gfx.TextureDesc) gfx.Texture {
	return &skyTestTexture{desc: desc}
}

func (*skyTestDevice) CreateSampler(gfx.SamplerDesc) gfx.Sampler { return &skyTestSampler{} }
func (*skyTestDevice) CreateCommandEncoder() gfx.CommandEncoder  { panic("unexpected encoder") }
func (*skyTestDevice) Submit(...gfx.CommandBuffer)               {}
func (*skyTestDevice) Poll(bool)                                 {}
func (*skyTestDevice) Release()                                  {}

func (device *skyTestDevice) renderPipeline(t *testing.T, label string) *skyTestRenderPipeline {
	t.Helper()
	for _, pipeline := range device.renderPipelines {
		if pipeline.desc.Label == label {
			return pipeline
		}
	}
	t.Fatalf("render pipeline %q was not created", label)
	return nil
}

func (device *skyTestDevice) buffer(t *testing.T, label string) *skyTestBuffer {
	t.Helper()
	for _, buffer := range device.buffers {
		if buffer.desc.Label == label {
			return buffer
		}
	}
	t.Fatalf("buffer %q was not created", label)
	return nil
}

func (device *skyTestDevice) bindGroup(t *testing.T, label string) *skyTestBindGroup {
	t.Helper()
	for _, group := range device.bindGroups {
		if group.desc.Label == label {
			return group
		}
	}
	t.Fatalf("bind group %q was not created", label)
	return nil
}

type skyTestBuffer struct {
	desc          gfx.BufferDesc
	writes        [][]byte
	discardWrites bool
	releases      int
}

func (buffer *skyTestBuffer) Size() uint64 { return buffer.desc.Size }
func (buffer *skyTestBuffer) Write(_ uint64, data []byte) {
	if buffer.discardWrites {
		return
	}
	buffer.writes = append(buffer.writes, append([]byte(nil), data...))
}
func (*skyTestBuffer) ReadBack() []byte { panic("unexpected readback") }
func (buffer *skyTestBuffer) Release()  { buffer.releases++ }

type skyTestShader struct{ releases int }

func (shader *skyTestShader) Release() { shader.releases++ }

type skyTestRenderPipeline struct {
	desc     gfx.RenderPipelineDesc
	releases int
}

func (pipeline *skyTestRenderPipeline) Release() { pipeline.releases++ }

type skyTestComputePipeline struct{ releases int }

func (pipeline *skyTestComputePipeline) Release() { pipeline.releases++ }

type skyTestBindGroup struct {
	desc     gfx.BindGroupDesc
	releases int
}

func (group *skyTestBindGroup) Release() { group.releases++ }

type skyTestTexture struct {
	desc     gfx.TextureDesc
	releases int
}

func (*skyTestTexture) View(gfx.TextureViewDesc) gfx.TextureView { return &skyTestView{} }
func (*skyTestTexture) WriteLayer(uint32, uint32, []byte)        {}
func (*skyTestTexture) WriteRegion(uint32, uint32, uint32, uint32, uint32, uint32, []byte) {
}
func (*skyTestTexture) ReadLayer(uint32, uint32) []byte { return nil }
func (texture *skyTestTexture) Release()                { texture.releases++ }

type skyTestView struct{ releases int }

func (view *skyTestView) Release() { view.releases++ }

type skyTestSampler struct{ releases int }

func (sampler *skyTestSampler) Release() { sampler.releases++ }

type skyTestEncoder struct {
	passes          []*skyTestPass
	discardCommands bool
	discardPass     skyTestPass
}

func (encoder *skyTestEncoder) BeginRenderPass(gfx.RenderPassDesc) gfx.RenderPass {
	if encoder.discardCommands {
		encoder.discardPass.discardCommands = true
		return &encoder.discardPass
	}
	pass := &skyTestPass{}
	encoder.passes = append(encoder.passes, pass)
	return pass
}

func (*skyTestEncoder) BeginComputePass(string) gfx.ComputePass { panic("unexpected compute pass") }
func (*skyTestEncoder) CopyBufferToBuffer(gfx.Buffer, uint64, gfx.Buffer, uint64, uint64) {
}
func (*skyTestEncoder) Finish() gfx.CommandBuffer { panic("unexpected finish") }

type skyTestPass struct {
	commands        []string
	current         string
	discardCommands bool
}

func (pass *skyTestPass) SetPipeline(pipeline gfx.RenderPipeline) {
	if pass.discardCommands {
		return
	}
	pass.current = pipeline.(*skyTestRenderPipeline).desc.Label
	pass.commands = append(pass.commands, "pipeline:"+pass.current)
}

func (*skyTestPass) SetBindGroup(uint32, gfx.BindGroup)         {}
func (*skyTestPass) SetVertexBuffer(uint32, gfx.Buffer, uint64) {}
func (*skyTestPass) SetIndexBuffer(gfx.Buffer, uint64)          {}
func (pass *skyTestPass) DrawIndexedIndirect(gfx.Buffer, uint64) {
	if pass.discardCommands {
		return
	}
	pass.commands = append(pass.commands, "draw:"+pass.current+":indirect")
}
func (pass *skyTestPass) Draw(vertices, instances uint32) {
	if pass.discardCommands {
		return
	}
	pass.commands = append(pass.commands, fmt.Sprintf("draw:%s:%d:%d", pass.current, vertices, instances))
}
func (*skyTestPass) End() {}
