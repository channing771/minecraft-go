package render

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/gfx"
)

func TestEntityKeySeparatesEqualPlayerAndCompanionBytes(t *testing.T) {
	id := [16]byte{0: 0x12, 6: 0x40, 8: 0x80, 15: 1}
	player := EntityKey{Kind: EntityPlayer, ID: id}
	companion := EntityKey{Kind: EntityCompanion, ID: id}
	target := EntityKey{Kind: EntityTarget, ID: id}
	if player == companion || player == target || companion == target {
		t.Fatalf("相同 ID bytes 的实体键发生冲突: %v %v %v", player, companion, target)
	}
	keys := []EntityKey{
		{Kind: EntityCompanion, ID: [16]byte{15: 2}},
		target,
		{Kind: EntityPlayer, ID: [16]byte{15: 2}},
		player,
	}
	slices.SortFunc(keys, compareEntityKeys)
	want := []EntityKey{
		{Kind: EntityPlayer, ID: [16]byte{15: 2}},
		player,
		{Kind: EntityCompanion, ID: [16]byte{15: 2}},
		target,
	}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("实体键顺序=%v，想要 %v", keys, want)
	}
}

func TestAvatarPlayerPaletteVectorsRemainUnchanged(t *testing.T) {
	tests := []struct {
		id   core.PlayerID
		want [4]float32
	}{
		{testAvatarID(1), [4]float32{0.72, 0.42, 0.82, 0.9}},
		{testAvatarID(2), [4]float32{0.28, 0.78, 0.68, 0.9}},
		{core.PlayerID{0: 0x12, 6: 0x40, 8: 0x80, 15: 1}, [4]float32{0.68, 0.42, 0.28, 0.9}},
	}
	for _, test := range tests {
		if got := AvatarColor(test.id); got != test.want {
			t.Fatalf("AvatarColor(%x)=%v，想要固定玩家颜色 %v", test.id, got, test.want)
		}
	}
	playerKey := EntityKey{Kind: EntityPlayer, ID: [16]byte(tests[0].id)}
	companionKey := EntityKey{Kind: EntityCompanion, ID: playerKey.ID}
	if got := avatarColor(playerKey); got != tests[0].want {
		t.Fatalf("玩家 EntityKey 颜色=%v，想要 %v", got, tests[0].want)
	}
	if avatarColor(companionKey) == tests[0].want {
		t.Fatal("伙伴与相同 bytes 的玩家共享了颜色域")
	}
}

func TestAvatarRendererAcceptsElevenAndRejectsTwelveBeforeGPUWrite(t *testing.T) {
	dev := &avatarTestDevice{}
	renderer := NewAvatarRenderer(dev, gfx.FormatRGBA8Unorm, gfx.FormatDepth32Float)
	defer renderer.Release()
	encoder := &avatarTestEncoder{}
	upload := dev.bufferByLabel(t, "avatar dynamic upload")
	if got, want := upload.desc.Size, uint64(5556); got != want {
		t.Fatalf("dynamic upload size=%d，想要 %d", got, want)
	}
	if err := renderer.Render(encoder, avatarTestView{}, avatarTestView{}, Camera{}, makeTestAvatars(11)); err != nil {
		t.Fatalf("11 个 Avatar Render: %v", err)
	}
	if got, want := len(renderer.parts), 66; got != want {
		t.Fatalf("parts=%d，想要 %d", got, want)
	}
	if got, want := len(upload.lastWrite), 5556; got != want {
		t.Fatalf("upload bytes=%d，想要 %d", got, want)
	}
	if avatarInstanceSize != 5280 || avatarIndirectOffset != 5536 || avatarUploadBytes != 5556 {
		t.Fatalf("Avatar 布局=%d/%d/%d，想要 5280/5536/5556", avatarInstanceSize, avatarIndirectOffset, avatarUploadBytes)
	}
	wantOrdered := append([]Avatar(nil), renderer.ordered...)
	wantParts := append([]avatarPart(nil), renderer.parts...)
	wantUpload := append([]byte(nil), renderer.upload...)
	upload.lastWrite = nil
	encoder.passes = nil
	if err := renderer.Render(encoder, avatarTestView{}, avatarTestView{}, Camera{}, makeTestAvatars(12)); err == nil {
		t.Fatal("12 个 Avatar 未被拒绝")
	}
	if len(upload.lastWrite) != 0 || len(encoder.passes) != 0 {
		t.Fatalf("overflow 后 GPU write/pass=%d/%d，想要 0/0", len(upload.lastWrite), len(encoder.passes))
	}
	if !reflect.DeepEqual(renderer.ordered, wantOrdered) ||
		!reflect.DeepEqual(renderer.parts, wantParts) ||
		!reflect.DeepEqual(renderer.upload, wantUpload) {
		t.Fatal("overflow 改变了 AvatarRenderer 上一帧状态")
	}
}

func TestBuildAvatarPartsIsBounded(t *testing.T) {
	avatars := makeTestAvatars(11)
	parts := buildAvatarParts(nil, avatars)
	if got, want := len(parts), 66; got != want {
		t.Fatalf("parts=%d want=%d", got, want)
	}

	// The input is deliberately reversed. The first emitted body must belong
	// to the lexicographically smallest PlayerID at x=1.
	firstBounds := avatarPartsBounds(parts[:avatarPartsPerBody])
	assertVec3Near(t, firstBounds.min, mgl32.Vec3{0.7, 2, 2.7})
	assertVec3Near(t, firstBounds.max, mgl32.Vec3{1.3, 3.8, 3.3})
	if got := avatarPartsBounds(parts[60:66]); got.min[0] < 10.69 || got.max[0] > 11.31 {
		t.Fatalf("第十一个 Avatar bounds=%+v；未保留排序后的 ID 1..11", got)
	}
}

func TestBuildAvatarPartsMakesSixAnchoredCuboids(t *testing.T) {
	position := mgl32.Vec3{4, 5, 6}
	parts := buildAvatarParts(nil, []Avatar{{
		Key:      testEntityKey(testAvatarID(1)),
		Position: position,
	}})
	if got, want := len(parts), 6; got != want {
		t.Fatalf("parts=%d want=%d", got, want)
	}

	wantSizes := []mgl32.Vec3{
		{0.6, 0.4, 0.6},                    // head
		{0.4, 0.7, 0.25},                   // torso
		{0.1, 0.7, 0.25}, {0.1, 0.7, 0.25}, // arms
		{0.18, 0.7, 0.25}, {0.18, 0.7, 0.25}, // legs
	}
	for i, part := range parts {
		bounds := transformedUnitCubeBounds(part.transform)
		assertVec3Near(t, bounds.max.Sub(bounds.min), wantSizes[i])
		assertAxisAligned(t, part.transform)
	}

	bounds := avatarPartsBounds(parts)
	assertVec3Near(t, bounds.min, position.Add(mgl32.Vec3{-0.3, 0, -0.3}))
	assertVec3Near(t, bounds.max, position.Add(mgl32.Vec3{0.3, 1.8, 0.3}))
}

func TestBuildAvatarPartsAppliesBodyYawAndHeadPitch(t *testing.T) {
	parts := buildAvatarParts(nil, []Avatar{{
		Key:   testEntityKey(testAvatarID(1)),
		Yaw:   float32(math.Pi / 2),
		Pitch: float32(math.Pi / 4),
	}})

	torsoX := transformedDirection(parts[1].transform, mgl32.Vec3{1, 0, 0})
	if math.Abs(float64(torsoX[0])) > 1e-5 || math.Abs(float64(torsoX[2])) < 0.39 {
		t.Fatalf("torso local X after yaw = %v; want world Z axis with length 0.4", torsoX)
	}
	torsoY := transformedDirection(parts[1].transform, mgl32.Vec3{0, 1, 0})
	if math.Abs(float64(torsoY[2])) > 1e-5 {
		t.Fatalf("torso local Y after yaw = %v; pitch leaked into body", torsoY)
	}
	headY := transformedDirection(parts[0].transform, mgl32.Vec3{0, 1, 0})
	if math.Abs(float64(headY[0])) < 0.25 {
		t.Fatalf("head local Y after yaw and pitch = %v; pitch was not applied", headY)
	}
}

func TestBuildAvatarPartsPreservesIdentityColorWithPartShading(t *testing.T) {
	parts := buildAvatarParts(nil, []Avatar{{Key: testEntityKey(testAvatarID(1))}})
	base := AvatarColor(testAvatarID(1))
	for channel := 0; channel < 3; channel++ {
		if parts[0].color[channel] <= base[channel] {
			t.Fatalf("head channel %d=%f base=%f; want brighter head", channel, parts[0].color[channel], base[channel])
		}
		if parts[1].color[channel] != base[channel] {
			t.Fatalf("torso channel %d=%f base=%f", channel, parts[1].color[channel], base[channel])
		}
		for part := 2; part < avatarPartsPerBody; part++ {
			if parts[part].color[channel] >= base[channel] {
				t.Fatalf("limb %d channel %d=%f base=%f; want darker limb", part, channel, parts[part].color[channel], base[channel])
			}
		}
	}
}

func TestBuildAvatarPartsKeepsAllPalettePartChannelsInRange(t *testing.T) {
	// These final bytes map, in order, to FNV-1a palette slots 0 through 15.
	paletteSlotIDs := [...]byte{8, 3, 14, 9, 4, 15, 10, 5, 16, 11, 6, 1, 12, 7, 2, 13}
	seenColors := make(map[[4]float32]struct{}, len(paletteSlotIDs))
	for slot, lastByte := range paletteSlotIDs {
		playerID := testAvatarID(lastByte)
		base := AvatarColor(playerID)
		seenColors[base] = struct{}{}
		parts := buildAvatarParts(nil, []Avatar{{Key: testEntityKey(playerID)}})
		if got, want := len(parts), avatarPartsPerBody; got != want {
			t.Fatalf("palette slot %d parts=%d want=%d", slot, got, want)
		}
		for partIndex, part := range parts {
			for channel, value := range part.color {
				if value < 0.2 || value > 0.9 {
					t.Fatalf("palette slot %d part %d channel %d=%f outside [0.2,0.9]", slot, partIndex, channel, value)
				}
			}
		}
		for channel := 0; channel < 3; channel++ {
			if parts[0].color[channel] <= base[channel] {
				t.Fatalf("palette slot %d head channel %d=%f base=%f; want brighter head", slot, channel, parts[0].color[channel], base[channel])
			}
			if parts[1].color[channel] != base[channel] {
				t.Fatalf("palette slot %d torso channel %d=%f base=%f", slot, channel, parts[1].color[channel], base[channel])
			}
			for partIndex := 2; partIndex < avatarPartsPerBody; partIndex++ {
				if parts[partIndex].color[channel] >= base[channel] {
					t.Fatalf("palette slot %d limb %d channel %d=%f base=%f; want darker limb", slot, partIndex, channel, parts[partIndex].color[channel], base[channel])
				}
			}
		}
	}
	if got, want := len(seenColors), len(paletteSlotIDs); got != want {
		t.Fatalf("distinct palette colors=%d want=%d", got, want)
	}
}

func TestAvatarShadeClampsRGBToPartColorRange(t *testing.T) {
	if got, want := avatarShade([4]float32{0.21, 0.89, 0.5, 0.9}, 0.5), ([4]float32{0.2, 0.445, 0.25, 0.9}); got != want {
		t.Fatalf("dark shade=%v want=%v", got, want)
	}
	if got, want := avatarShade([4]float32{0.21, 0.89, 0.5, 0.9}, 2), ([4]float32{0.42, 0.9, 0.9, 0.9}); got != want {
		t.Fatalf("bright shade=%v want=%v", got, want)
	}
}

func TestAvatarColorUsesStableFNVPalette(t *testing.T) {
	tests := []struct {
		id   core.PlayerID
		want [4]float32
	}{
		{testAvatarID(1), [4]float32{0.72, 0.42, 0.82, 0.9}},
		{testAvatarID(2), [4]float32{0.28, 0.78, 0.68, 0.9}},
	}
	for _, tt := range tests {
		if got := AvatarColor(tt.id); got != tt.want {
			t.Fatalf("AvatarColor(%s)=%v want=%v", tt.id, got, tt.want)
		}
		for channel, value := range tt.want {
			if value < 0.2 || value > 0.9 {
				t.Fatalf("palette channel %d=%f outside [0.2,0.9]", channel, value)
			}
		}
	}
	if tests[0].want == tests[1].want {
		t.Fatal("distinct test IDs unexpectedly share a palette color")
	}
}

func TestAvatarColorIsStableAcrossProcesses(t *testing.T) {
	want := fmt.Sprint(AvatarColor(testAvatarID(1)))
	for run := 0; run < 2; run++ {
		cmd := exec.Command(os.Args[0], "-test.run=^TestAvatarColorProcessHelper$")
		cmd.Env = append(os.Environ(), "MORNLEA_AVATAR_COLOR_HELPER=1")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("helper process %d: %v\n%s", run, err, output)
		}
		got, _, _ := strings.Cut(strings.TrimSpace(string(output)), "\n")
		if got != want {
			t.Fatalf("helper process %d color=%q want=%q", run, got, want)
		}
	}
}

func TestAvatarColorProcessHelper(t *testing.T) {
	if os.Getenv("MORNLEA_AVATAR_COLOR_HELPER") != "1" {
		return
	}
	fmt.Println(AvatarColor(testAvatarID(1)))
}

func TestAvatarRendererUsesFixedStorageAndDepthPass(t *testing.T) {
	dev := &avatarTestDevice{}
	renderer := NewAvatarRenderer(dev, gfx.FormatRGBA8Unorm, gfx.FormatDepth32Float)

	upload := dev.bufferByLabel(t, "avatar dynamic upload")
	if got, want := upload.desc.Size, uint64(5556); got != want {
		t.Fatalf("dynamic upload size=%d want=%d", got, want)
	}
	if want := gfx.BufferUsageUniform | gfx.BufferUsageStorage | gfx.BufferUsageIndirect | gfx.BufferUsageCopyDst; upload.desc.Usage != want {
		t.Fatalf("dynamic upload usage=%v want=%v", upload.desc.Usage, want)
	}
	if got, want := len(dev.buffers), 3; got != want {
		t.Fatalf("constructor buffers=%d want=%d", got, want)
	}
	if dev.pipelineDesc.Blend != gfx.BlendReplace || !dev.pipelineDesc.DepthWrite ||
		dev.pipelineDesc.DepthFormat != gfx.FormatDepth32Float {
		t.Fatalf("avatar pipeline=%+v; want replace blend and depth less/write pipeline", dev.pipelineDesc)
	}

	encoder := &avatarTestEncoder{}
	if err := renderer.Render(encoder, avatarTestView{}, avatarTestView{}, Camera{}, nil); err != nil {
		t.Fatal(err)
	}
	if len(encoder.passes) != 0 {
		t.Fatalf("zero avatars created %d render passes", len(encoder.passes))
	}

	if err := renderer.Render(encoder, avatarTestView{}, avatarTestView{}, Camera{}, makeTestAvatars(11)); err != nil {
		t.Fatal(err)
	}
	if got, want := len(dev.buffers), 3; got != want {
		t.Fatalf("render resized GPU buffers: got=%d want=%d", got, want)
	}
	if got, want := len(encoder.passes), 1; got != want {
		t.Fatalf("avatar passes=%d want=%d", got, want)
	}
	pass := encoder.passes[0]
	if pass.desc.Label != "avatar pass" || pass.desc.LoadClear {
		t.Fatalf("pass descriptor=%+v; want label avatar pass and load existing attachments", pass.desc)
	}
	if pass.draws != 1 || !pass.ended || !pass.setIndex {
		t.Fatalf("encoded pass=%+v; want one indexed draw and End", pass)
	}
	args := upload.lastWrite[5536:5556]
	if got := binary.LittleEndian.Uint32(args[0:4]); got != 36 {
		t.Fatalf("index count=%d want=36", got)
	}
	if got := binary.LittleEndian.Uint32(args[4:8]); got != 66 {
		t.Fatalf("instance count=%d want=66", got)
	}
}

func TestAvatarRendererHeadlessDraw(t *testing.T) {
	dev, err := gfx.NewHeadlessDevice()
	if err != nil {
		t.Skipf("本机无可用 GPU 适配器: %v", err)
	}
	defer dev.Release()

	color := dev.CreateTexture(gfx.TextureDesc{
		Label: "avatar test color", Width: 64, Height: 64,
		Format: gfx.FormatRGBA8Unorm, Usage: gfx.TextureUsageRenderTarget,
	})
	defer color.Release()
	colorView := color.View(gfx.TextureViewDesc{})
	defer colorView.Release()
	depth := dev.CreateTexture(gfx.TextureDesc{
		Label: "avatar test depth", Width: 64, Height: 64,
		Format: gfx.FormatDepth32Float, Usage: gfx.TextureUsageRenderTarget,
	})
	defer depth.Release()
	depthView := depth.View(gfx.TextureViewDesc{Aspect: gfx.AspectDepthOnly})
	defer depthView.Release()

	renderer := NewAvatarRenderer(dev, gfx.FormatRGBA8Unorm, gfx.FormatDepth32Float)
	defer renderer.Release()
	encoder := dev.CreateCommandEncoder()
	clear := encoder.BeginRenderPass(gfx.RenderPassDesc{
		Label: "terrain clear", ColorView: colorView, DepthView: depthView, LoadClear: true,
	})
	clear.End()
	if err := renderer.Render(encoder, colorView, depthView, Camera{ViewProj: mgl32.Ident4()}, []Avatar{{
		Key: testEntityKey(testAvatarID(1)), Position: mgl32.Vec3{0, -0.9, 0},
	}}); err != nil {
		t.Fatal(err)
	}
	commands := encoder.Finish()
	dev.Submit(commands)
	commands.Release()
	dev.Poll(true)
}

func TestAvatarRendererReleaseIsIdempotent(t *testing.T) {
	upload := &avatarReleaseBuffer{}
	vertices := &avatarReleaseBuffer{}
	indices := &avatarReleaseBuffer{}
	pipeline := &avatarReleasePipeline{}
	bind := &avatarReleaseBindGroup{}
	renderer := &AvatarRenderer{
		dynamic: upload, vertices: vertices, indices: indices,
		pipeline: pipeline, bind: bind,
	}

	renderer.Release()
	renderer.Release()
	for name, got := range map[string]int{
		"dynamic": upload.releases, "vertices": vertices.releases,
		"indices": indices.releases, "pipeline": pipeline.releases,
		"bind": bind.releases,
	} {
		if got != 1 {
			t.Errorf("%s release calls=%d want=1", name, got)
		}
	}
}

func makeTestAvatars(count int) []Avatar {
	avatars := make([]Avatar, count)
	for i := range avatars {
		last := byte(count - i)
		avatars[i] = Avatar{
			Key:      testEntityKey(testAvatarID(last)),
			Position: mgl32.Vec3{float32(last), 2, 3},
		}
	}
	return avatars
}

func testAvatarID(last byte) core.PlayerID {
	return core.PlayerID{0, 1, 2, 3, 4, 5, 0x46, 7, 0x88, 9, 10, 11, 12, 13, 14, last}
}

func testEntityKey(id core.PlayerID) EntityKey {
	return EntityKey{Kind: EntityPlayer, ID: [16]byte(id)}
}

type avatarBounds struct{ min, max mgl32.Vec3 }

func avatarPartsBounds(parts []avatarPart) avatarBounds {
	bounds := avatarBounds{
		min: mgl32.Vec3{float32(math.Inf(1)), float32(math.Inf(1)), float32(math.Inf(1))},
		max: mgl32.Vec3{float32(math.Inf(-1)), float32(math.Inf(-1)), float32(math.Inf(-1))},
	}
	for _, part := range parts {
		partBounds := transformedUnitCubeBounds(part.transform)
		for axis := 0; axis < 3; axis++ {
			bounds.min[axis] = min(bounds.min[axis], partBounds.min[axis])
			bounds.max[axis] = max(bounds.max[axis], partBounds.max[axis])
		}
	}
	return bounds
}

func transformedUnitCubeBounds(transform mgl32.Mat4) avatarBounds {
	bounds := avatarBounds{
		min: mgl32.Vec3{float32(math.Inf(1)), float32(math.Inf(1)), float32(math.Inf(1))},
		max: mgl32.Vec3{float32(math.Inf(-1)), float32(math.Inf(-1)), float32(math.Inf(-1))},
	}
	for _, x := range []float32{-0.5, 0.5} {
		for _, y := range []float32{-0.5, 0.5} {
			for _, z := range []float32{-0.5, 0.5} {
				world := transform.Mul4x1(mgl32.Vec4{x, y, z, 1}).Vec3()
				for axis := 0; axis < 3; axis++ {
					bounds.min[axis] = min(bounds.min[axis], world[axis])
					bounds.max[axis] = max(bounds.max[axis], world[axis])
				}
			}
		}
	}
	return bounds
}

func transformedDirection(transform mgl32.Mat4, direction mgl32.Vec3) mgl32.Vec3 {
	return transform.Mul4x1(direction.Vec4(0)).Vec3()
}

func assertAxisAligned(t *testing.T, transform mgl32.Mat4) {
	t.Helper()
	for column := 0; column < 3; column++ {
		for row := 0; row < 3; row++ {
			if row != column && math.Abs(float64(transform[column*4+row])) > 1e-6 {
				t.Fatalf("transform=%v is not locally axis aligned", transform)
			}
		}
	}
}

func assertVec3Near(t *testing.T, got, want mgl32.Vec3) {
	t.Helper()
	if !got.ApproxEqualThreshold(want, 1e-5) {
		t.Fatalf("vec=%v want=%v", got, want)
	}
}

type avatarTestDevice struct {
	buffers      []*avatarTestBuffer
	pipelineDesc gfx.RenderPipelineDesc
	bindDesc     gfx.BindGroupDesc
}

func (d *avatarTestDevice) CreateBuffer(desc gfx.BufferDesc) gfx.Buffer {
	b := &avatarTestBuffer{desc: desc}
	d.buffers = append(d.buffers, b)
	return b
}
func (*avatarTestDevice) CreateShaderModule(string) gfx.ShaderModule { return &avatarTestShader{} }
func (d *avatarTestDevice) CreateRenderPipeline(desc gfx.RenderPipelineDesc) gfx.RenderPipeline {
	d.pipelineDesc = desc
	return &avatarReleasePipeline{}
}
func (*avatarTestDevice) CreateComputePipeline(gfx.ComputePipelineDesc) gfx.ComputePipeline {
	panic("unexpected compute pipeline")
}
func (d *avatarTestDevice) CreateBindGroup(desc gfx.BindGroupDesc) gfx.BindGroup {
	d.bindDesc = desc
	return &avatarReleaseBindGroup{}
}
func (*avatarTestDevice) CreateTexture(gfx.TextureDesc) gfx.Texture { panic("unexpected texture") }
func (*avatarTestDevice) CreateSampler(gfx.SamplerDesc) gfx.Sampler { panic("unexpected sampler") }
func (*avatarTestDevice) CreateCommandEncoder() gfx.CommandEncoder  { panic("unexpected encoder") }
func (*avatarTestDevice) Submit(...gfx.CommandBuffer)               {}
func (*avatarTestDevice) Poll(bool)                                 {}
func (*avatarTestDevice) Release()                                  {}

func (d *avatarTestDevice) bufferByLabel(t *testing.T, label string) *avatarTestBuffer {
	t.Helper()
	for _, buffer := range d.buffers {
		if buffer.desc.Label == label {
			return buffer
		}
	}
	t.Fatalf("buffer %q was not created", label)
	return nil
}

type avatarTestBuffer struct {
	desc      gfx.BufferDesc
	lastWrite []byte
	releases  int
}

func (b *avatarTestBuffer) Size() uint64 { return b.desc.Size }
func (b *avatarTestBuffer) Write(_ uint64, data []byte) {
	b.lastWrite = append(b.lastWrite[:0], data...)
}
func (*avatarTestBuffer) ReadBack() []byte { panic("unexpected readback") }
func (b *avatarTestBuffer) Release()       { b.releases++ }

type avatarTestShader struct{ releases int }

func (s *avatarTestShader) Release() { s.releases++ }

type avatarTestEncoder struct{ passes []*avatarTestPass }

func (e *avatarTestEncoder) BeginRenderPass(desc gfx.RenderPassDesc) gfx.RenderPass {
	pass := &avatarTestPass{desc: desc}
	e.passes = append(e.passes, pass)
	return pass
}
func (*avatarTestEncoder) BeginComputePass(string) gfx.ComputePass { panic("unexpected compute pass") }
func (*avatarTestEncoder) CopyBufferToBuffer(gfx.Buffer, uint64, gfx.Buffer, uint64, uint64) {
	panic("unexpected buffer copy")
}
func (*avatarTestEncoder) Finish() gfx.CommandBuffer { panic("unexpected finish") }

type avatarTestPass struct {
	desc     gfx.RenderPassDesc
	draws    int
	ended    bool
	setIndex bool
}

func (*avatarTestPass) SetPipeline(gfx.RenderPipeline)             {}
func (*avatarTestPass) SetBindGroup(uint32, gfx.BindGroup)         {}
func (*avatarTestPass) SetVertexBuffer(uint32, gfx.Buffer, uint64) {}
func (p *avatarTestPass) SetIndexBuffer(gfx.Buffer, uint64)        { p.setIndex = true }
func (p *avatarTestPass) DrawIndexedIndirect(gfx.Buffer, uint64)   { p.draws++ }
func (*avatarTestPass) Draw(uint32, uint32)                        { panic("unexpected direct draw") }
func (p *avatarTestPass) End()                                     { p.ended = true }

type avatarTestView struct{}

func (avatarTestView) Release() {}

type avatarReleaseBuffer struct{ releases int }

func (*avatarReleaseBuffer) Size() uint64         { return 0 }
func (*avatarReleaseBuffer) Write(uint64, []byte) {}
func (*avatarReleaseBuffer) ReadBack() []byte     { return nil }
func (b *avatarReleaseBuffer) Release()           { b.releases++ }

type avatarReleasePipeline struct{ releases int }

func (p *avatarReleasePipeline) Release() { p.releases++ }

type avatarReleaseBindGroup struct{ releases int }

func (b *avatarReleaseBindGroup) Release() { b.releases++ }
