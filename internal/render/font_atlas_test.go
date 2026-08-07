//go:build darwin

package render

import (
	"errors"
	"image"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"

	"minecraft-go/internal/gfx"
)

func TestGlyphAtlasConstructsTofuAndBoundedQueues(t *testing.T) {
	dev := &glyphTestDevice{}
	renderFace, workerFace := &glyphTestFace{}, &glyphTestFace{}
	atlas, err := newGlyphAtlasWith(dev, func() (font.Face, font.Face, error) {
		return renderFace, workerFace, nil
	}, &glyphTestRasterizer{})
	if err != nil {
		t.Fatal(err)
	}
	defer atlas.Release()

	if renderFace == workerFace {
		t.Fatal("factory returned one shared face")
	}
	if cap(atlas.requests) != 1024 || cap(atlas.results) != 32 {
		t.Fatalf("queue capacities = %d/%d, want 1024/32", cap(atlas.requests), cap(atlas.results))
	}
	if dev.desc.Width != 1024 || dev.desc.Height != 1024 || dev.desc.Format != gfx.FormatR8Unorm {
		t.Fatalf("texture descriptor = %#v, want 1024x1024 R8", dev.desc)
	}
	writes := dev.texture.snapshotWrites()
	if len(writes) != 1 {
		t.Fatalf("constructor writes = %d, want synchronous tofu write", len(writes))
	}
	assertGlyphWrite(t, writes[0], 0, 0)
	if allZero(writes[0].pixels) {
		t.Fatal("tofu upload is empty")
	}
	if got := atlas.Glyph('未'); got.Slot != 0 {
		t.Fatalf("unknown glyph slot = %d, want tofu slot 0", got.Slot)
	}
}

func TestGlyphAtlasRequestDeduplicatesAndWorkerIsFIFO(t *testing.T) {
	dev := &glyphTestDevice{}
	renderFace, workerFace := &glyphTestFace{}, &glyphTestFace{}
	raster := &glyphTestRasterizer{}
	atlas := mustGlyphTestAtlas(t, dev, renderFace, workerFace, raster)

	atlas.Request("中A中VAA")
	waitForResultCount(t, atlas, 3)
	for i := 0; i < 3; i++ {
		flushOneGlyph(t, atlas)
	}

	if got := raster.snapshotRunes(); string(got) != "中AV" {
		t.Fatalf("worker order = %q, want %q", string(got), "中AV")
	}
	for _, face := range raster.snapshotFaces() {
		if face != workerFace {
			t.Fatalf("worker used face %p, want worker face %p", face, workerFace)
		}
	}
	if got := []uint16{atlas.Glyph('中').Slot, atlas.Glyph('A').Slot, atlas.Glyph('V').Slot}; got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Fatalf("slots = %v, want [1 2 3]", got)
	}

	if got := atlas.Kern('A', 'V'); got != 1.5 {
		t.Fatalf("kern = %v, want 1.5", got)
	}
	if renderFace.kernCalls.Load() != 1 || workerFace.kernCalls.Load() != 0 {
		t.Fatalf("kern calls render/worker = %d/%d, want 1/0", renderFace.kernCalls.Load(), workerFace.kernCalls.Load())
	}
}

func TestGlyphAtlasRequestFullQueueCanRetry(t *testing.T) {
	dev := &glyphTestDevice{}
	renderFace, workerFace := &glyphTestFace{}, &glyphTestFace{}
	raster := newBlockingGlyphTestRasterizer()
	atlas := mustGlyphTestAtlas(t, dev, renderFace, workerFace, raster)

	atlas.Request("a")
	select {
	case <-raster.started:
	case <-time.After(time.Second):
		t.Fatal("worker did not enter rasterizer")
	}
	runes := make([]rune, 1025)
	for i := range runes {
		runes[i] = rune(0x1000 + i)
	}
	atlas.Request(string(runes))
	last := runes[len(runes)-1]
	atlas.mu.Lock()
	_, registeredWhileFull := atlas.requested[last]
	atlas.mu.Unlock()
	if registeredWhileFull {
		t.Fatal("request rejected by full queue was registered")
	}

	close(raster.unblock)
	waitUntil(t, func() bool { return len(atlas.requests) < cap(atlas.requests) })
	atlas.Request(string(last))
	atlas.mu.Lock()
	_, registeredAfterRetry := atlas.requested[last]
	atlas.mu.Unlock()
	if !registeredAfterRetry {
		t.Fatal("request was not registered after queue space became available")
	}
}

func TestGlyphAtlasFlushRetainsPendingUntilBudgetAvailable(t *testing.T) {
	dev := &glyphTestDevice{}
	atlas := mustGlyphTestAtlas(t, dev, &glyphTestFace{}, &glyphTestFace{}, &glyphTestRasterizer{})
	atlas.Request("A")
	waitForResultCount(t, atlas, 1)

	budget := NewUploadBudget(1024)
	if !budget.TryConsume(1) {
		t.Fatal("failed to pre-consume budget")
	}
	if err := atlas.FlushUploads(budget); err != nil {
		t.Fatal(err)
	}
	if atlas.pendingUpload == nil {
		t.Fatal("result was not retained when budget was insufficient")
	}
	if got := len(dev.texture.snapshotWrites()); got != 1 {
		t.Fatalf("writes with insufficient budget = %d, want only tofu", got)
	}

	budget.BeginFrame()
	if err := atlas.FlushUploads(budget); err != nil {
		t.Fatal(err)
	}
	writes := dev.texture.snapshotWrites()
	if len(writes) != 2 {
		t.Fatalf("writes after replenishing budget = %d, want 2", len(writes))
	}
	assertGlyphWrite(t, writes[1], 32, 0)
	if budget.spent != 1024 {
		t.Fatalf("glyph upload spent = %d, want 1024", budget.spent)
	}
}

func TestGlyphAtlasRasterErrorDoesNotConsumeBudget(t *testing.T) {
	wantErr := errors.New("synthetic raster failure")
	raster := &glyphTestRasterizer{errFor: map[rune]error{'!': wantErr}}
	atlas := mustGlyphTestAtlas(t, &glyphTestDevice{}, &glyphTestFace{}, &glyphTestFace{}, raster)
	atlas.Request("!")
	waitForResultCount(t, atlas, 1)
	budget := NewUploadBudget(1024)
	err := atlas.FlushUploads(budget)
	if !errors.Is(err, wantErr) {
		t.Fatalf("FlushUploads error = %v, want wrapped sentinel", err)
	}
	if budget.spent != 0 {
		t.Fatalf("raster error spent = %d, want 0", budget.spent)
	}
}

func TestGlyphAtlasMissingResultUsesTofuWithoutSlotOrUpload(t *testing.T) {
	dev := &glyphTestDevice{}
	raster := &glyphTestRasterizer{missingFor: map[rune]bool{'?': true}}
	atlas := mustGlyphTestAtlas(t, dev, &glyphTestFace{}, &glyphTestFace{}, raster)
	atlas.Request("?")
	waitForResultCount(t, atlas, 1)

	budget := NewUploadBudget(1024)
	if err := atlas.FlushUploads(budget); err != nil {
		t.Fatal(err)
	}
	if got := atlas.Glyph('?').Slot; got != 0 {
		t.Fatalf("missing glyph slot = %d, want tofu", got)
	}
	if atlas.nextSlot != 1 || budget.spent != 0 || len(dev.texture.snapshotWrites()) != 1 {
		t.Fatalf("missing glyph changed slot/budget/writes = %d/%d/%d, want 1/0/1", atlas.nextSlot, budget.spent, len(dev.texture.snapshotWrites()))
	}
	atlas.Request("?")
	if len(atlas.requests) != 0 {
		t.Fatalf("stable missing glyph was requeued: queue length = %d", len(atlas.requests))
	}
}

func TestGlyphAtlasExhaustionPermanentlyUsesTofu(t *testing.T) {
	atlas := mustGlyphTestAtlas(t, &glyphTestDevice{}, &glyphTestFace{}, &glyphTestFace{}, &glyphTestRasterizer{})
	runes := make([]rune, 1024)
	for i := range runes {
		runes[i] = rune(0x2000 + i)
	}
	atlas.Request(string(runes))
	for i := 0; i < len(runes); i++ {
		flushOneGlyph(t, atlas)
	}
	if got := atlas.Glyph(runes[1022]).Slot; got != 1023 {
		t.Fatalf("last lifetime slot = %d, want 1023", got)
	}
	if got := atlas.Glyph(runes[1023]).Slot; got != 0 {
		t.Fatalf("first exhausted glyph slot = %d, want tofu", got)
	}
	atlas.Request("新")
	if got := atlas.Glyph('新').Slot; got != 0 {
		t.Fatalf("post-exhaustion glyph slot = %d, want tofu", got)
	}
	if len(atlas.requests) != 0 {
		t.Fatalf("post-exhaustion request queue length = %d, want 0", len(atlas.requests))
	}
}

func TestGlyphAtlasReleaseCancelsBlockedWorkerAndIsConcurrentIdempotent(t *testing.T) {
	dev := &glyphTestDevice{}
	atlas := mustGlyphTestAtlas(t, dev, &glyphTestFace{}, &glyphTestFace{}, &glyphTestRasterizer{})
	runes := make([]rune, 34)
	for i := range runes {
		runes[i] = rune(0x3000 + i)
	}
	atlas.Request(string(runes))
	waitForResultCount(t, atlas, 32)

	done := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				atlas.Release()
			}()
		}
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Release blocked behind full result queue")
	}
	atlas.Request("after release")
	if dev.texture.releaseCalls.Load() != 1 || dev.texture.view.releaseCalls.Load() != 1 {
		t.Fatalf("texture/view releases = %d/%d, want 1/1", dev.texture.releaseCalls.Load(), dev.texture.view.releaseCalls.Load())
	}
}

func TestGlyphAtlasConcurrentReleaseWaitsForTeardown(t *testing.T) {
	dev := &glyphTestDevice{}
	raster := newBlockingGlyphTestRasterizer()
	atlas := mustGlyphTestAtlasNoCleanup(t, dev, &glyphTestFace{}, &glyphTestFace{}, raster)
	atlas.Request("a")
	select {
	case <-raster.started:
	case <-time.After(time.Second):
		t.Fatal("worker did not enter rasterizer")
	}

	firstDone := make(chan struct{})
	go func() {
		atlas.Release()
		close(firstDone)
	}()
	waitUntil(t, func() bool {
		atlas.mu.Lock()
		defer atlas.mu.Unlock()
		return atlas.released
	})
	secondDone := make(chan struct{})
	go func() {
		atlas.Release()
		close(secondDone)
	}()

	secondReturnedEarly := false
	select {
	case <-secondDone:
		secondReturnedEarly = true
	case <-time.After(20 * time.Millisecond):
	}
	close(raster.unblock)
	for name, done := range map[string]<-chan struct{}{"first": firstDone, "second": secondDone} {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("%s Release did not finish after worker unblocked", name)
		}
	}
	if secondReturnedEarly {
		t.Fatal("concurrent Release returned before the shared teardown completed")
	}
	if dev.texture.releaseCalls.Load() != 1 || dev.texture.view.releaseCalls.Load() != 1 {
		t.Fatalf("texture/view releases = %d/%d, want 1/1", dev.texture.releaseCalls.Load(), dev.texture.view.releaseCalls.Load())
	}
}

func TestGlyphAtlasEmbeddedFont(t *testing.T) {
	dev := &glyphTestDevice{}
	atlas, err := NewGlyphAtlas(dev)
	if err != nil {
		t.Fatal(err)
	}
	defer atlas.Release()

	atlas.Request("中AV")
	for _, r := range []rune("中AV") {
		waitUntil(t, func() bool {
			budget := NewUploadBudget(1024)
			if err := atlas.FlushUploads(budget); err != nil {
				t.Fatalf("FlushUploads: %v", err)
			}
			return atlas.Glyph(r).Slot != 0
		})
	}
	for _, r := range []rune("中A") {
		glyph := atlas.Glyph(r)
		if glyph.Slot == 0 || glyph.Advance <= 0 || glyph.Width <= 0 || glyph.Height <= 0 {
			t.Fatalf("glyph %q = %#v, want nonzero slot/metrics", r, glyph)
		}
	}
	missing := rune(0x10ffff)
	beforeSlot := atlas.nextSlot
	beforeWrites := len(dev.texture.snapshotWrites())
	atlas.Request(string(missing))
	waitForResultCount(t, atlas, 1)
	missingBudget := NewUploadBudget(1024)
	if err := atlas.FlushUploads(missingBudget); err != nil {
		t.Fatalf("FlushUploads missing rune: %v", err)
	}
	if got := atlas.Glyph(missing).Slot; got != 0 {
		t.Fatalf("missing glyph slot = %d, want tofu", got)
	}
	if atlas.nextSlot != beforeSlot || missingBudget.spent != 0 || len(dev.texture.snapshotWrites()) != beforeWrites {
		t.Fatalf("missing glyph changed slot/budget/writes = %d/%d/%d, want %d/0/%d", atlas.nextSlot, missingBudget.spent, len(dev.texture.snapshotWrites()), beforeSlot, beforeWrites)
	}
	atlas.Request(string(missing))
	if len(atlas.requests) != 0 {
		t.Fatalf("real missing glyph was requeued: queue length = %d", len(atlas.requests))
	}
	if kern := atlas.Kern('A', 'V'); math.IsNaN(float64(kern)) || math.IsInf(float64(kern), 0) {
		t.Fatalf("kern(A,V) = %v, want finite", kern)
	}
	nonzeroUploads := 0
	for _, write := range dev.texture.snapshotWrites()[1:] {
		if !allZero(write.pixels) {
			nonzeroUploads++
		}
	}
	if nonzeroUploads < 2 {
		t.Fatalf("nonzero real glyph uploads = %d, want at least 2", nonzeroUploads)
	}
}

func mustGlyphTestAtlas(t *testing.T, dev *glyphTestDevice, renderFace, workerFace font.Face, raster glyphRasterizer) *GlyphAtlas {
	t.Helper()
	atlas := mustGlyphTestAtlasNoCleanup(t, dev, renderFace, workerFace, raster)
	t.Cleanup(atlas.Release)
	return atlas
}

func mustGlyphTestAtlasNoCleanup(t *testing.T, dev *glyphTestDevice, renderFace, workerFace font.Face, raster glyphRasterizer) *GlyphAtlas {
	t.Helper()
	atlas, err := newGlyphAtlasWith(dev, func() (font.Face, font.Face, error) {
		return renderFace, workerFace, nil
	}, raster)
	if err != nil {
		t.Fatal(err)
	}
	return atlas
}

func flushOneGlyph(t *testing.T, atlas *GlyphAtlas) {
	t.Helper()
	waitUntil(t, func() bool {
		before := atlas.nextSlot
		budget := NewUploadBudget(1024)
		if err := atlas.FlushUploads(budget); err != nil {
			t.Fatalf("FlushUploads: %v", err)
		}
		return atlas.nextSlot != before || atlas.exhausted
	})
}

func waitForResultCount(t *testing.T, atlas *GlyphAtlas, count int) {
	t.Helper()
	waitUntil(t, func() bool { return len(atlas.results) >= count })
}

func waitUntil(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for glyph worker")
		}
		time.Sleep(time.Millisecond)
	}
}

func assertGlyphWrite(t *testing.T, write glyphTestWrite, x, y uint32) {
	t.Helper()
	if write.layer != 0 || write.mip != 0 || write.x != x || write.y != y || write.width != 32 || write.height != 32 || len(write.pixels) != 1024 {
		t.Fatalf("WriteRegion = %#v, want layer/mip=0, origin=%d,%d, 32x32, 1024 bytes", write, x, y)
	}
}

func allZero(pixels []byte) bool {
	for _, pixel := range pixels {
		if pixel != 0 {
			return false
		}
	}
	return true
}

type glyphTestFace struct{ kernCalls atomic.Int32 }

func (*glyphTestFace) Close() error { return nil }
func (*glyphTestFace) Glyph(fixed.Point26_6, rune) (image.Rectangle, image.Image, image.Point, fixed.Int26_6, bool) {
	return image.Rectangle{}, nil, image.Point{}, 0, false
}
func (*glyphTestFace) GlyphBounds(rune) (fixed.Rectangle26_6, fixed.Int26_6, bool) {
	return fixed.Rectangle26_6{}, 0, false
}
func (*glyphTestFace) GlyphAdvance(rune) (fixed.Int26_6, bool) { return 0, false }
func (f *glyphTestFace) Kern(rune, rune) fixed.Int26_6 {
	f.kernCalls.Add(1)
	return fixed.Int26_6(1.5 * 64)
}
func (*glyphTestFace) Metrics() font.Metrics { return font.Metrics{} }

type glyphTestRasterizer struct {
	mu         sync.Mutex
	runes      []rune
	faces      []font.Face
	errFor     map[rune]error
	missingFor map[rune]bool
}

func (r *glyphTestRasterizer) Rasterize(face font.Face, char rune) (Glyph, []byte, bool, error) {
	r.mu.Lock()
	r.runes = append(r.runes, char)
	r.faces = append(r.faces, face)
	err := r.errFor[char]
	missing := r.missingFor[char]
	r.mu.Unlock()
	if err != nil {
		return Glyph{}, nil, false, err
	}
	if missing {
		return Glyph{}, nil, true, nil
	}
	pixels := make([]byte, 1024)
	pixels[0] = byte(char | 1)
	return Glyph{Advance: 10, BearingX: 1, BearingY: 8, Width: 8, Height: 9}, pixels, false, nil
}

func (r *glyphTestRasterizer) snapshotRunes() []rune {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]rune(nil), r.runes...)
}

func (r *glyphTestRasterizer) snapshotFaces() []font.Face {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]font.Face(nil), r.faces...)
}

type blockingGlyphTestRasterizer struct {
	glyphTestRasterizer
	started chan struct{}
	unblock chan struct{}
	once    sync.Once
}

func newBlockingGlyphTestRasterizer() *blockingGlyphTestRasterizer {
	return &blockingGlyphTestRasterizer{started: make(chan struct{}), unblock: make(chan struct{})}
}

func (r *blockingGlyphTestRasterizer) Rasterize(face font.Face, char rune) (Glyph, []byte, bool, error) {
	r.once.Do(func() {
		close(r.started)
		<-r.unblock
	})
	return r.glyphTestRasterizer.Rasterize(face, char)
}

type glyphTestDevice struct {
	desc    gfx.TextureDesc
	texture glyphTestTexture
}

func (*glyphTestDevice) CreateBuffer(gfx.BufferDesc) gfx.Buffer     { panic("unused") }
func (*glyphTestDevice) CreateShaderModule(string) gfx.ShaderModule { panic("unused") }
func (*glyphTestDevice) CreateRenderPipeline(gfx.RenderPipelineDesc) gfx.RenderPipeline {
	panic("unused")
}
func (*glyphTestDevice) CreateComputePipeline(gfx.ComputePipelineDesc) gfx.ComputePipeline {
	panic("unused")
}
func (*glyphTestDevice) CreateBindGroup(gfx.BindGroupDesc) gfx.BindGroup { panic("unused") }
func (d *glyphTestDevice) CreateTexture(desc gfx.TextureDesc) gfx.Texture {
	d.desc = desc
	return &d.texture
}
func (*glyphTestDevice) CreateSampler(gfx.SamplerDesc) gfx.Sampler { panic("unused") }
func (*glyphTestDevice) CreateCommandEncoder() gfx.CommandEncoder  { panic("unused") }
func (*glyphTestDevice) Submit(...gfx.CommandBuffer)               {}
func (*glyphTestDevice) Poll(bool)                                 {}
func (*glyphTestDevice) Release()                                  {}

type glyphTestWrite struct {
	layer, mip, x, y, width, height uint32
	pixels                          []byte
}

type glyphTestTexture struct {
	mu           sync.Mutex
	writes       []glyphTestWrite
	view         glyphTestView
	releaseCalls atomic.Int32
}

func (t *glyphTestTexture) View(gfx.TextureViewDesc) gfx.TextureView { return &t.view }
func (t *glyphTestTexture) WriteLayer(layer, mip uint32, pixels []byte) {
	t.WriteRegion(layer, mip, 0, 0, 1024, 1024, pixels)
}
func (t *glyphTestTexture) WriteRegion(layer, mip, x, y, width, height uint32, pixels []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.writes = append(t.writes, glyphTestWrite{layer, mip, x, y, width, height, append([]byte(nil), pixels...)})
}
func (t *glyphTestTexture) ReadLayer(uint32, uint32) []byte { return nil }
func (t *glyphTestTexture) Release()                        { t.releaseCalls.Add(1) }
func (t *glyphTestTexture) snapshotWrites() []glyphTestWrite {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]glyphTestWrite(nil), t.writes...)
}

type glyphTestView struct{ releaseCalls atomic.Int32 }

func (v *glyphTestView) Release() { v.releaseCalls.Add(1) }
