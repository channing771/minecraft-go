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

	"github.com/channing771/mornlea/internal/assets"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/gfx"
	"github.com/channing771/mornlea/internal/mesh"
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

// Mutation killed: 将 MacroX 放回 f32 lane 或忽略该字段会破坏完整 u32 hash 偏移。
func TestSkyHeadlessCloudMacroUniformIsTypedUint(t *testing.T) {
	want := [][2]uint32{
		{1, 0x07030bbc},
		{0x007fffff, 0xaa591afe},
		{0x7f800000, 0x8e83660f},
		{0x7fc00001, 0x3d029f3d},
		{0xff800000, 0x66e9ccc9},
		{math.MaxUint32, 0x5f47970e},
	}
	values := make([]uint32, len(want))
	for index := range want {
		values[index] = want[index][0]
	}
	got := skyCloudMacroUniformReadback(t, values)
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("production Sky.cloud_macro_x/hash[%d]=%#x，想要 %#x", index, got[index], want[index])
		}
	}
}

func skyCloudMacroUniformReadback(t *testing.T, values []uint32) [][2]uint32 {
	t.Helper()
	device, err := gfx.NewHeadlessDevice()
	if err != nil {
		if skyHeadlessAdapterUnavailable(err) {
			t.Skipf("本机无可用 GPU 适配器: %v", err)
		}
		t.Fatalf("创建 headless GPU device: %v", err)
	}
	defer device.Release()

	shader := device.CreateShaderModule(skyShader + `
struct CloudMacroOutput {
    value: vec2u,
};

@group(1) @binding(0) var<storage, read_write> cloud_macro_output: CloudMacroOutput;

@compute @workgroup_size(1)
fn cloud_macro_uniform_readback() {
    cloud_macro_output.value = vec2u(sky.cloud_macro_x, cloud_hash(vec2i(0, 0), sky.cloud_macro_x));
}
`)
	defer shader.Release()
	skyLayout := gfx.BindGroupLayout{Label: "cloud macro test sky layout", Entries: []gfx.BindGroupLayoutEntry{
		{Binding: 0, Type: gfx.BindingUniformBuffer, VisibleIn: gfx.StageCompute},
	}}
	outputLayout := gfx.BindGroupLayout{Label: "cloud macro test output layout", Entries: []gfx.BindGroupLayoutEntry{
		{Binding: 0, Type: gfx.BindingStorageBufferRW, VisibleIn: gfx.StageCompute},
	}}
	pipeline := device.CreateComputePipeline(gfx.ComputePipelineDesc{
		Label: "cloud macro uniform readback", Shader: shader, Entry: "cloud_macro_uniform_readback",
		BindGroups: []gfx.BindGroupLayout{skyLayout, outputLayout},
	})
	defer pipeline.Release()

	got := make([][2]uint32, len(values))
	for index, value := range values {
		uniform := device.CreateBuffer(gfx.BufferDesc{Label: "cloud macro test sky uniform", Size: 112, Usage: gfx.BufferUsageUniform | gfx.BufferUsageCopyDst})
		data := make([]byte, 112)
		binary.LittleEndian.PutUint32(data[84:88], value)
		uniform.Write(0, data)
		output := device.CreateBuffer(gfx.BufferDesc{Label: "cloud macro test output", Size: 8, Usage: gfx.BufferUsageStorage | gfx.BufferUsageCopySrc})
		skyBind := device.CreateBindGroup(gfx.BindGroupDesc{Label: "cloud macro test sky bind", Layout: skyLayout, Entries: []gfx.BindGroupEntry{{Binding: 0, Buffer: uniform}}})
		outputBind := device.CreateBindGroup(gfx.BindGroupDesc{Label: "cloud macro test output bind", Layout: outputLayout, Entries: []gfx.BindGroupEntry{{Binding: 0, Buffer: output}}})
		encoder := device.CreateCommandEncoder()
		pass := encoder.BeginComputePass("cloud macro uniform readback")
		pass.SetPipeline(pipeline)
		pass.SetBindGroup(0, skyBind)
		pass.SetBindGroup(1, outputBind)
		pass.Dispatch(1, 1, 1)
		pass.End()
		command := encoder.Finish()
		device.Submit(command)
		command.Release()
		device.Poll(true)
		readback := output.ReadBack()
		if len(readback) != 8 {
			t.Fatalf("MacroX readback bytes=%d，想要 8", len(readback))
		}
		got[index] = [2]uint32{binary.LittleEndian.Uint32(readback), binary.LittleEndian.Uint32(readback[4:])}
		outputBind.Release()
		skyBind.Release()
		output.Release()
		uniform.Release()
	}
	return got
}

func TestSkyHeadlessCloudHashFixedSample(t *testing.T) {
	lowBits, active, filled, _, _ := skyCloudFixedSample(t)
	if want := [4]uint32{72, 69, 62, 53}; lowBits != want {
		t.Fatalf("生产 WGSL 固定 macro 样本 low2=%v，想要 %v", lowBits, want)
	}
	if active != 184 {
		t.Fatalf("生产 WGSL 固定 macro 样本 active=%d，想要 184", active)
	}
	if filled != 920 {
		t.Fatalf("生产 WGSL 固定 macro 样本 filled=%d，想要 920", filled)
	}
	if got := float64(filled) / (256 * 16); got != 0.224609375 {
		t.Fatalf("生产 WGSL 固定 macro 样本覆盖率=%v，想要 0.224609375", got)
	}
	activeTheory := 3.0 / 4.0
	coverageTheory := activeTheory * 5 / 16
	if activeTheory != 0.75 || coverageTheory != 0.234375 {
		t.Fatalf("理论 active/coverage=%v/%v，想要 0.75/0.234375", activeTheory, coverageTheory)
	}
}

func TestSkyHeadlessCloudMaskFixedGrid(t *testing.T) {
	_, _, _, got, _ := skyCloudFixedSample(t)
	if want := [8]uint8{2, 71, 226, 64}; got != want {
		t.Fatalf("生产 cloud_mask 的固定 8x8 输出=%v，想要 %v", got, want)
	}
}

func TestSkyHeadlessCloudMaskAsymmetricCenter(t *testing.T) {
	_, _, _, _, got := skyCloudFixedSample(t)
	if want := [4]uint8{0, 2, 7, 2}; got != want {
		t.Fatalf("生产 cloud_mask 的非对称 center 4x4 输出=%v，想要 %v", got, want)
	}
}

func TestSkyHeadlessCloudCompositionOverStar(t *testing.T) {
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

	position := mgl32.Vec3{115, 64, -152}
	direction := mgl32.Vec3{0, 1, 0}
	clouds := renderSkyHeadless(t, device, renderer, skyCameraAt(position, direction, 18000))
	position[1] = 200
	withoutClouds := renderSkyHeadless(t, device, renderer, skyCameraAt(position, direction, 18000))
	const x, y = 43, 0
	if got, want := skyPixel(withoutClouds, x, y), [4]byte{153, 164, 184, 255}; max(
		math.Abs(float64(int(got[0])-int(want[0]))),
		math.Abs(float64(int(got[1])-int(want[1]))),
		math.Abs(float64(int(got[2])-int(want[2]))),
	) > 2 {
		t.Fatalf("固定 production star pixel=%v，想要 %v", got, want)
	}
	// 午夜 daylight=0.15，云色为 mix((.18,.22,.28),(.84,.88,.92),.15)。
	// 此 fixed full-mask star pixel 按 alpha .82 写入 UNORM 后为 86/96/112。
	if got, want := skyPixel(clouds, x, y), [4]byte{86, 96, 112, 255}; max(
		math.Abs(float64(int(got[0])-int(want[0]))),
		math.Abs(float64(int(got[1])-int(want[1]))),
		math.Abs(float64(int(got[2])-int(want[2]))),
	) > 2 {
		t.Fatalf("alpha .82 的 production cloud-over-star pixel=%v，想要 %v", got, want)
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

	zenithPosition := mgl32.Vec3{0, 200, 0}
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

	t.Run("世界锚定与时间偏移共同平移云层", func(t *testing.T) {
		firstCamera := skyCameraAt(mgl32.Vec3{40, 64, -152}, zenithDirection, 6000)
		firstCamera.CloudOffset = CloudOffset{}
		compensatedCamera := skyCameraAt(mgl32.Vec3{56, 64, -152}, zenithDirection, 6000)
		compensatedCamera.CloudOffset = CloudOffsetAt(16 * 80)
		movedCamera := skyCameraAt(mgl32.Vec3{56, 64, -152}, zenithDirection, 6000)
		movedCamera.CloudOffset = CloudOffset{}
		offsetCamera := skyCameraAt(mgl32.Vec3{40, 64, -152}, zenithDirection, 6000)
		offsetCamera.CloudOffset = CloudOffsetAt(16 * 80)

		first := renderSkyHeadless(t, device, renderer, firstCamera)
		compensated := renderSkyHeadless(t, device, renderer, compensatedCamera)
		moved := renderSkyHeadless(t, device, renderer, movedCamera)
		offset := renderSkyHeadless(t, device, renderer, offsetCamera)
		if !bytes.Equal(first, compensated) {
			differences, bounds := skyPixelDifferences(first, compensated)
			t.Fatalf("相机与云偏移共同东移后改变了 %d 个像素，范围 %v", differences, bounds)
		}
		if bytes.Equal(first, moved) {
			t.Fatal("只移动相机未改变云图案")
		}
		if bytes.Equal(first, offset) {
			t.Fatal("只移动云偏移未改变云图案")
		}
	})

	t.Run("仅沿 Z 轴平移改变云层", func(t *testing.T) {
		firstCamera := skyCameraAt(mgl32.Vec3{40, 64, -152}, zenithDirection, 6000)
		firstCamera.CloudOffset = CloudOffset{}
		movedCamera := skyCameraAt(mgl32.Vec3{40, 64, -72}, zenithDirection, 6000)
		movedCamera.CloudOffset = CloudOffset{}
		first := renderSkyHeadless(t, device, renderer, firstCamera)
		moved := renderSkyHeadless(t, device, renderer, movedCamera)
		if bytes.Equal(first, moved) {
			t.Fatal("只沿 Z 轴移动相机未改变云图案")
		}
		center := skyHeadlessSize / 2
		if skyPixel(first, center, center) == skyPixel(moved, center, center) {
			t.Fatal("只沿 Z 轴移动相机未改变中心 cloud mask")
		}
	})

	t.Run("云只绘制在层下方且存在正向交点", func(t *testing.T) {
		below := renderSkyHeadless(t, device, renderer, skyCameraAt(mgl32.Vec3{115, 64, -152}, zenithDirection, 6000))
		above := renderSkyHeadless(t, device, renderer, skyCameraAt(mgl32.Vec3{115, 200, -152}, zenithDirection, 6000))
		if bytes.Equal(below, above) {
			t.Fatal("云层下方天顶与上方无云参考完全相同")
		}
		for _, test := range []struct {
			name      string
			direction mgl32.Vec3
		}{
			{"平行", mgl32.Vec3{1, 0, 0}},
			{"阈值", mgl32.Vec3{1, 0.001, 0}},
			{"向下", mgl32.Vec3{0, -1, 0}},
		} {
			t.Run(test.name, func(t *testing.T) {
				lower := renderSkyHeadless(t, device, renderer, skyCameraAt(mgl32.Vec3{115, 64, -152}, test.direction, 6000))
				upper := renderSkyHeadless(t, device, renderer, skyCameraAt(mgl32.Vec3{115, 200, -152}, test.direction, 6000))
				center := skyHeadlessSize / 2
				if lowerPixel, upperPixel := skyPixel(lower, center, center), skyPixel(upper, center, center); lowerPixel != upperPixel {
					t.Fatalf("无正向交点的中心 ray 仍绘制云：lower=%v upper=%v", lowerPixel, upperPixel)
				}
			})
		}
	})

	t.Run("固定地平线 fade 逐渐增强", func(t *testing.T) {
		cloudDifference := func(slope float32) int {
			direction := mgl32.Vec3{0, slope, -1}.Normalize()
			distance := (192 - float32(64)) / direction[1]
			position := mgl32.Vec3{115, 64, -152 - direction[2]*distance}
			lower := renderSkyHeadless(t, device, renderer, skyCameraAt(position, direction, 6000))
			position[1] = 200
			upper := renderSkyHeadless(t, device, renderer, skyCameraAt(position, direction, 6000))
			maximum := 0
			for y := skyHeadlessSize/2 - 2; y <= skyHeadlessSize/2+2; y++ {
				for x := skyHeadlessSize/2 - 2; x <= skyHeadlessSize/2+2; x++ {
					maximum = max(maximum, skyPixelColorDifference(lower, upper, x, y))
				}
			}
			return maximum
		}
		partial, full := cloudDifference(0.04), cloudDifference(0.10)
		if partial <= 0 || full <= partial {
			t.Fatalf("地平线 cloud difference partial/full=%d/%d，想要 0 < partial < full", partial, full)
		}
	})

	t.Run("固定样本区域保持稀疏覆盖率", func(t *testing.T) {
		clouds := renderSkyHeadless(t, device, renderer, skyCameraAt(mgl32.Vec3{115, 80, -152}, zenithDirection, 6000))
		reference := renderSkyHeadless(t, device, renderer, skyCameraAt(mgl32.Vec3{115, 200, -152}, zenithDirection, 6000))
		differences, bounds := skyPixelDifferences(clouds, reference)
		coverage := float64(differences) / float64(skyHeadlessSize*skyHeadlessSize)
		if coverage < 0.20 || coverage > 0.30 {
			t.Fatalf("云像素=%d coverage=%v bounds=%v，想要 20%%..30%%", differences, coverage, bounds)
		}
		inactive := renderSkyHeadless(t, device, renderer, skyCameraAt(mgl32.Vec3{99, 64, 24}, zenithDirection, 6000))
		inactiveReference := renderSkyHeadless(t, device, renderer, skyCameraAt(mgl32.Vec3{99, 200, 24}, zenithDirection, 6000))
		center := skyHeadlessSize / 2
		if got, want := skyPixel(inactive, center, center), skyPixel(inactiveReference, center, center); got != want {
			t.Fatalf("hash low2=0 的 macro 中心仍绘制云：got=%v want=%v", got, want)
		}
	})

	t.Run("昼夜云在天体之后混合", func(t *testing.T) {
		noon := renderSkyHeadless(t, device, renderer, skyCameraAt(mgl32.Vec3{115, 64, -152}, zenithDirection, 6000))
		noonReference := renderSkyHeadless(t, device, renderer, skyCameraAt(mgl32.Vec3{115, 200, -152}, zenithDirection, 6000))
		midnight := renderSkyHeadless(t, device, renderer, skyCameraAt(mgl32.Vec3{265, 64, -152}, zenithDirection, 18000))
		midnightReference := renderSkyHeadless(t, device, renderer, skyCameraAt(mgl32.Vec3{265, 200, -152}, zenithDirection, 18000))

		noonCenter := skyPixel(noon, skyHeadlessSize/2, skyHeadlessSize/2)
		noonReferenceCenter := skyPixel(noonReference, skyHeadlessSize/2, skyHeadlessSize/2)
		if noonCenter == noonReferenceCenter || noonCenter[0] >= noonReferenceCenter[0] {
			t.Fatalf("正午云未遮挡太阳：cloud=%v reference=%v", noonCenter, noonReferenceCenter)
		}
		midnightCenter := skyPixel(midnight, skyHeadlessSize/2, skyHeadlessSize/2)
		midnightReferenceCenter := skyPixel(midnightReference, skyHeadlessSize/2, skyHeadlessSize/2)
		if midnightCenter == midnightReferenceCenter || skyBrightness(midnightCenter) >= skyBrightness(midnightReferenceCenter) {
			t.Fatalf("午夜云未遮挡月亮：cloud=%v reference=%v", midnightCenter, midnightReferenceCenter)
		}
		noonBrightness, noonCount := changedSkyBrightness(noon, noonReference)
		midnightBrightness, midnightCount := changedSkyBrightness(midnight, midnightReference)
		if noonCount == 0 || midnightCount == 0 || midnightBrightness >= noonBrightness {
			t.Fatalf("云层昼夜 brightness/count noon=%d/%d midnight=%d/%d，想要午夜更暗", noonBrightness, noonCount, midnightBrightness, midnightCount)
		}
		if stars := countSkyStars(midnight, true); stars < 4 {
			t.Fatalf("云外午夜 star pixels=%d，想要 >=4", stars)
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

	t.Run("地形覆盖云层", func(t *testing.T) {
		position := mgl32.Vec3{115.5, 0.5, -151.5}
		camera := skyCameraAt(position, zenithDirection, 6000)
		skyOnly := renderSkyHeadless(t, device, renderer, camera)
		reference := renderSkyHeadless(t, device, renderer, skyCameraAt(mgl32.Vec3{115.5, 200, -151.5}, zenithDirection, 6000))
		if skyPixel(skyOnly, skyHeadlessSize/2, skyHeadlessSize/2) == skyPixel(reference, skyHeadlessSize/2, skyHeadlessSize/2) {
			t.Fatal("地形测试中心没有云")
		}
		renderer.QueueSection(core.SectionPos{X: 7, Y: 4, Z: -10}, []mesh.Quad{{
			X: 3, Y: 1, Z: 8, W: 1, H: 1, Face: mesh.FaceNegY, Mat: 0, AO: 0xff, Light: 0xf0,
		}})
		renderer.BeginFrame()
		renderer.FlushUploads(core.ChunkPos{})
		withTerrain := renderSkyHeadless(t, device, renderer, camera)
		skyCenter := skyPixel(skyOnly, skyHeadlessSize/2, skyHeadlessSize/2)
		terrainCenter := skyPixel(withTerrain, skyHeadlessSize/2, skyHeadlessSize/2)
		if terrainCenter == skyCenter {
			differences, bounds := skyPixelDifferences(skyOnly, withTerrain)
			t.Fatalf("terrain center=%v sky center=%v differences=%d bounds=%v stats=%+v，想要地形覆盖云",
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

func skyPixelColorDifference(first, second []byte, x, y int) int {
	firstPixel, secondPixel := skyPixel(first, x, y), skyPixel(second, x, y)
	return max(int(firstPixel[0])-int(secondPixel[0]), int(secondPixel[0])-int(firstPixel[0])) +
		max(int(firstPixel[1])-int(secondPixel[1]), int(secondPixel[1])-int(firstPixel[1])) +
		max(int(firstPixel[2])-int(secondPixel[2]), int(secondPixel[2])-int(firstPixel[2]))
}

func changedSkyBrightness(pixels, reference []byte) (int, int) {
	total, count := 0, 0
	for y := 0; y < skyHeadlessSize; y++ {
		for x := 0; x < skyHeadlessSize; x++ {
			if skyPixel(pixels, x, y) == skyPixel(reference, x, y) {
				continue
			}
			total += skyBrightness(skyPixel(pixels, x, y))
			count++
		}
	}
	if count == 0 {
		return 0, 0
	}
	return total / count, count
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
