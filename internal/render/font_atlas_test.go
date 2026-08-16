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
)

func TestGlyphAtlasConstructsTofuAndBoundedQueues(t *testing.T) {
	sink := &glyphTestSink{}
	renderFace, workerFace := &glyphTestFace{}, &glyphTestFace{}
	atlas, err := newGlyphAtlasSink(sink, func() (font.Face, font.Face, error) {
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
	writes := sink.snapshotWrites()
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
	renderFace, workerFace := &glyphTestFace{}, &glyphTestFace{}
	raster := &glyphTestRasterizer{}
	atlas := mustGlyphTestAtlas(t, &glyphTestSink{}, renderFace, workerFace, raster)

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
	raster := newBlockingGlyphTestRasterizer()
	atlas := mustGlyphTestAtlas(t, &glyphTestSink{}, &glyphTestFace{}, &glyphTestFace{}, raster)

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
	sink := &glyphTestSink{}
	atlas := mustGlyphTestAtlas(t, sink, &glyphTestFace{}, &glyphTestFace{}, &glyphTestRasterizer{})
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
	if got := len(sink.snapshotWrites()); got != 1 {
		t.Fatalf("writes with insufficient budget = %d, want only tofu", got)
	}

	budget.BeginFrame()
	if err := atlas.FlushUploads(budget); err != nil {
		t.Fatal(err)
	}
	writes := sink.snapshotWrites()
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
	atlas := mustGlyphTestAtlas(t, &glyphTestSink{}, &glyphTestFace{}, &glyphTestFace{}, raster)
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
	sink := &glyphTestSink{}
	raster := &glyphTestRasterizer{missingFor: map[rune]bool{'?': true}}
	atlas := mustGlyphTestAtlas(t, sink, &glyphTestFace{}, &glyphTestFace{}, raster)
	atlas.Request("?")
	waitForResultCount(t, atlas, 1)

	budget := NewUploadBudget(1024)
	if err := atlas.FlushUploads(budget); err != nil {
		t.Fatal(err)
	}
	if got := atlas.Glyph('?').Slot; got != 0 {
		t.Fatalf("missing glyph slot = %d, want tofu", got)
	}
	if atlas.nextSlot != 1 || budget.spent != 0 || len(sink.snapshotWrites()) != 1 {
		t.Fatalf("missing glyph changed slot/budget/writes = %d/%d/%d, want 1/0/1", atlas.nextSlot, budget.spent, len(sink.snapshotWrites()))
	}
	atlas.Request("?")
	if len(atlas.requests) != 0 {
		t.Fatalf("stable missing glyph was requeued: queue length = %d", len(atlas.requests))
	}
}

func TestGlyphAtlasExhaustionPermanentlyUsesTofu(t *testing.T) {
	atlas := mustGlyphTestAtlas(t, &glyphTestSink{}, &glyphTestFace{}, &glyphTestFace{}, &glyphTestRasterizer{})
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
	atlas := mustGlyphTestAtlas(t, &glyphTestSink{}, &glyphTestFace{}, &glyphTestFace{}, &glyphTestRasterizer{})
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
	if atlas.renderFace != nil {
		t.Fatal("renderFace was not cleared by Release")
	}
}

func TestGlyphAtlasConcurrentReleaseWaitsForTeardown(t *testing.T) {
	raster := newBlockingGlyphTestRasterizer()
	atlas := mustGlyphTestAtlasNoCleanup(t, &glyphTestSink{}, &glyphTestFace{}, &glyphTestFace{}, raster)
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
}

func TestGlyphAtlasEmbeddedFont(t *testing.T) {
	sink := &glyphTestSink{}
	atlas, err := NewGlyphAtlasWithSink(sink)
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
	beforeWrites := len(sink.snapshotWrites())
	atlas.Request(string(missing))
	waitForResultCount(t, atlas, 1)
	missingBudget := NewUploadBudget(1024)
	if err := atlas.FlushUploads(missingBudget); err != nil {
		t.Fatalf("FlushUploads missing rune: %v", err)
	}
	if got := atlas.Glyph(missing).Slot; got != 0 {
		t.Fatalf("missing glyph slot = %d, want tofu", got)
	}
	if atlas.nextSlot != beforeSlot || missingBudget.spent != 0 || len(sink.snapshotWrites()) != beforeWrites {
		t.Fatalf("missing glyph changed slot/budget/writes = %d/%d/%d, want %d/0/%d", atlas.nextSlot, missingBudget.spent, len(sink.snapshotWrites()), beforeSlot, beforeWrites)
	}
	atlas.Request(string(missing))
	if len(atlas.requests) != 0 {
		t.Fatalf("real missing glyph was requeued: queue length = %d", len(atlas.requests))
	}
	if kern := atlas.Kern('A', 'V'); math.IsNaN(float64(kern)) || math.IsInf(float64(kern), 0) {
		t.Fatalf("kern(A,V) = %v, want finite", kern)
	}
	nonzeroUploads := 0
	for _, write := range sink.snapshotWrites()[1:] {
		if !allZero(write.pixels) {
			nonzeroUploads++
		}
	}
	if nonzeroUploads < 2 {
		t.Fatalf("nonzero real glyph uploads = %d, want at least 2", nonzeroUploads)
	}
}

// TestGlyphUVCoversInkNotWholeCell 守住字形 UV 与四边形的尺度一致性。
//
// 全部消费方（hotbar、name tag、调试面板）统一按 Glyph.Width × Glyph.Height 画
// 四边形，并直接用 Glyph 的 UV 采样图集。因此 UV 覆盖的图集区域必须与四边形
// 等尺寸——只覆盖 ink 本身，而不是整个 glyphCellSize×glyphCellSize 的格子。
//
// 若 UV 覆盖整格，整格（绝大部分是空白）会被压进 ink 大小的四边形，压缩比为
// Width/glyphCellSize。实测 'w' 约 0.56 只是变细，而 'i' 约 0.09 会把 2.9px 的
// 墨迹缩成 0.26px 而彻底消失——表现为窄字符成片丢失、宽字符偏细，名牌与 HUD
// 数字同样受影响。
func TestGlyphUVCoversInkNotWholeCell(t *testing.T) {
	atlas, err := NewGlyphAtlasWithSink(&glyphTestSink{})
	if err != nil {
		t.Fatal(err)
	}
	defer atlas.Release()

	// 刻意混合最窄（i r t .）与较宽（w S 6）的字形：缺陷的严重度与字宽成反比，
	// 只取宽字形会让偏差落在容差内而漏掉。
	const probe = "irt.wS6"
	atlas.Request(probe)
	for _, char := range probe {
		waitUntil(t, func() bool {
			budget := NewUploadBudget(1024)
			if err := atlas.FlushUploads(budget); err != nil {
				t.Fatalf("FlushUploads: %v", err)
			}
			return atlas.Glyph(char).Slot != 0
		})
	}

	for _, char := range probe {
		glyph := atlas.Glyph(char)
		if glyph.Slot == 0 {
			t.Fatalf("%q 落到了 tofu，无法校验 UV", char)
		}
		uvWidth := float64(glyph.U1-glyph.U0) * float64(glyphAtlasSize)
		uvHeight := float64(glyph.V1-glyph.V0) * float64(glyphAtlasSize)
		// 容差 1px：UV 落在图集的整数像素边界上，而 Width/Height 是分数 ink 尺寸。
		if math.Abs(uvWidth-float64(glyph.Width)) > 1 {
			t.Errorf("%q: UV 宽 %.2fpx 与四边形宽 %.2fpx 不符（差 %.2fpx）——"+
				"整格宽为 %d，UV 很可能覆盖了整格而非 ink",
				char, uvWidth, glyph.Width, uvWidth-float64(glyph.Width), glyphCellSize)
		}
		if math.Abs(uvHeight-float64(glyph.Height)) > 1 {
			t.Errorf("%q: UV 高 %.2fpx 与四边形高 %.2fpx 不符（差 %.2fpx）",
				char, uvHeight, glyph.Height, uvHeight-float64(glyph.Height))
		}
	}
}

func mustGlyphTestAtlas(t *testing.T, sink *glyphTestSink, renderFace, workerFace font.Face, raster glyphRasterizer) *GlyphAtlas {
	t.Helper()
	atlas := mustGlyphTestAtlasNoCleanup(t, sink, renderFace, workerFace, raster)
	t.Cleanup(atlas.Release)
	return atlas
}

func mustGlyphTestAtlasNoCleanup(t *testing.T, sink *glyphTestSink, renderFace, workerFace font.Face, raster glyphRasterizer) *GlyphAtlas {
	t.Helper()
	atlas, err := newGlyphAtlasSink(sink, func() (font.Face, font.Face, error) {
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
	if write.x != x || write.y != y || write.width != 32 || write.height != 32 || len(write.pixels) != 1024 {
		t.Fatalf("WriteGlyphRect = %#v, want origin=%d,%d, 32x32, 1024 bytes", write, x, y)
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

// glyphTestWrite 记录一次 WriteGlyphRect 调用的矩形与像素。
type glyphTestWrite struct {
	x, y, width, height uint32
	pixels              []byte
}

// glyphTestSink 是记录型 GlyphSink,替代旧 gfx 纹理 fake。
type glyphTestSink struct {
	mu     sync.Mutex
	writes []glyphTestWrite
}

func (s *glyphTestSink) WriteGlyphRect(x, y, width, height uint32, pixels []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes = append(s.writes, glyphTestWrite{x, y, width, height, append([]byte(nil), pixels...)})
}

func (s *glyphTestSink) snapshotWrites() []glyphTestWrite {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]glyphTestWrite(nil), s.writes...)
}
