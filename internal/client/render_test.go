//go:build darwin

package client

import (
	"errors"
	"testing"
)

// TestRendererRoundtripOrSkip 走一遍 create→atlas→section→frame→readback:
// 无 GPU 适配器时跳过(与 gfx.NewHeadlessDevice 约定一致)。
func TestRendererRoundtripOrSkip(t *testing.T) {
	renderer, err := NewRenderer(32, 16)
	if errors.Is(err, ErrNoGPUAdapter) {
		t.Skip("无 GPU 适配器")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer renderer.Close()

	// 单 layer 合法 atlas:16²+8²+4²+2²+1² 像素 × 4 字节。
	perLayer := 0
	for size := 16; size >= 1; size /= 2 {
		perLayer += size * size * 4
	}
	renderer.UploadAtlas(1, make([]byte, perLayer))
	renderer.UploadSection(0, 5, 0, make([]byte, 32), make([]byte, 16))
	renderer.DropSection(9, 9, 9)

	frame := RenderFrame{Daylight: 1, SkyColor: [4]float32{0.2, 0.4, 1, 1}}
	for i := 0; i < 4; i++ {
		frame.ViewProj[i*4+i] = 1
		frame.ViewProjInv[i*4+i] = 1
	}
	frame.Visible = [][3]int32{{0, 5, 0}}
	renderer.RenderFrame(frame)
	first := renderer.Readback()
	if len(first) != 32*16*4 {
		t.Fatalf("readback 长度=%d", len(first))
	}
	nonZero := false
	for _, b := range first {
		if b != 0 {
			nonZero = true
			break
		}
	}
	if !nonZero {
		t.Fatal("渲染后回读不应全零")
	}
	renderer.RenderFrame(frame)
	second := renderer.Readback()
	if string(first) != string(second) {
		t.Fatal("同输入两帧必须逐字节一致")
	}
}

// TestEncodeRenderFrameLayout 锁定 render_frame ABI 编码布局。
func TestEncodeRenderFrameLayout(t *testing.T) {
	frame := RenderFrame{
		Daylight:       0.5,
		StarVisibility: 0.25,
		CloudMacroX:    7,
		Visible:        [][3]int32{{1, 2, 3}, {-1, 0, -3}},
	}
	out := EncodeRenderFrame(frame)
	if len(out) != renderFrameHeaderBytes+2*12 {
		t.Fatalf("编码长度=%d", len(out))
	}
	if out[184] != 2 {
		t.Fatalf("visible count 编码=%d", out[184])
	}
	// 第二条 record 的 z=-3(小端补码)。
	z := int32(uint32(out[renderFrameHeaderBytes+20]) |
		uint32(out[renderFrameHeaderBytes+21])<<8 |
		uint32(out[renderFrameHeaderBytes+22])<<16 |
		uint32(out[renderFrameHeaderBytes+23])<<24)
	if z != -3 {
		t.Fatalf("visible[1].z=%d", z)
	}
}

// TestEncodeRenderFrameV2Segments 锁定 v2 TLV 段编码:layout 版本、段序
// 与 EncodeQuadSegment 计数。
func TestEncodeRenderFrameV2Segments(t *testing.T) {
	frame := RenderFrame{
		AvatarInstances: make([]byte, 160),
		OverlayStrength: 0.5,
		HUDSegment:      EncodeQuadSegment(make([]byte, 16), make([]byte, 96), nil, 48),
	}
	out := EncodeRenderFrame(frame)
	if got := out[188]; got != 2 {
		t.Fatalf("layout=%d,想要 2", got)
	}
	cursor := renderFrameHeaderBytes
	readU32 := func() uint32 {
		v := uint32(out[cursor]) | uint32(out[cursor+1])<<8 |
			uint32(out[cursor+2])<<16 | uint32(out[cursor+3])<<24
		cursor += 4
		return v
	}
	if tag, length := readU32(), readU32(); tag != 1 || length != 160 {
		t.Fatalf("首段 tag=%d len=%d", tag, length)
	}
	cursor += 160
	if tag, length := readU32(), readU32(); tag != 4 || length != 4 {
		t.Fatalf("overlay 段 tag=%d len=%d", tag, length)
	}
	cursor += 4
	if tag := readU32(); tag != 6 {
		t.Fatalf("HUD 段 tag=%d", tag)
	}
	length := readU32()
	if int(length) != 16+8+96 {
		t.Fatalf("HUD 段 len=%d", length)
	}
	// EncodeQuadSegment 的计数字段:quad 2 个(96/48)、glyph 0。
	if got := out[cursor+16]; got != 2 {
		t.Fatalf("quad count=%d", got)
	}
	// 纯地形帧保持 layout 0。
	plain := EncodeRenderFrame(RenderFrame{})
	if plain[188] != 0 || len(plain) != renderFrameHeaderBytes {
		t.Fatalf("纯地形帧 layout=%d len=%d", plain[188], len(plain))
	}
}
