//go:build darwin

package client

import (
	"encoding/binary"
	"errors"
	"testing"
)

// encodeLodQuad 编码一个 20 字节壳 quad(LE,布局与 engine encode_shell
// 一致:x/z/y i32 + w/d u16 + face u8 + material u16 + shade u8)。
func encodeLodQuad(x, z, y int32, w, d uint16, face uint8, material uint16, shade uint8) []byte {
	out := make([]byte, 20)
	binary.LittleEndian.PutUint32(out[0:], uint32(x))
	binary.LittleEndian.PutUint32(out[4:], uint32(z))
	binary.LittleEndian.PutUint32(out[8:], uint32(y))
	binary.LittleEndian.PutUint16(out[12:], w)
	binary.LittleEndian.PutUint16(out[14:], d)
	out[16] = face
	binary.LittleEndian.PutUint16(out[17:], material)
	out[19] = shade
	return out
}

// TestUploadLodTileRejectsBadLength 不依赖 GPU:非法 quad 长度在渲染器
// 查找之前被 Rust 入口拒绝,Go 侧表现为 check panic。
func TestUploadLodTileRejectsBadLength(t *testing.T) {
	renderer := &Renderer{handle: 0xF00D}
	defer func() {
		if recover() == nil {
			t.Fatal("非法 quad 长度必须触发 panic")
		}
	}()
	renderer.UploadLodTile(0, 0, make([]byte, 21))
}

// TestUploadLodTileRejectsBadFace 在真实渲染器上验证 face 越界的流被拒
// 并转 INVALID_ARGUMENT(check panic);无 GPU 适配器时跳过。
func TestUploadLodTileRejectsBadFace(t *testing.T) {
	renderer, err := NewRenderer(16, 16)
	if errors.Is(err, ErrNoGPUAdapter) {
		t.Skip("无 GPU 适配器")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer renderer.Close()
	quad := encodeLodQuad(0, 0, 70, 8, 8, 5, 0, 255)
	defer func() {
		if recover() == nil {
			t.Fatal("face 越界必须触发 panic")
		}
	}()
	renderer.UploadLodTile(0, 0, quad)
}

// TestRendererLodTileRoundtripOrSkip 走一遍 upload(替换)→drop(幂等)→
// 帧渲染:无 GPU 适配器时跳过;渲染成功且两帧输出逐字节稳定。
func TestRendererLodTileRoundtripOrSkip(t *testing.T) {
	renderer, err := NewRenderer(32, 16)
	if errors.Is(err, ErrNoGPUAdapter) {
		t.Skip("无 GPU 适配器")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer renderer.Close()

	perLayer := 0
	for size := 16; size >= 1; size /= 2 {
		perLayer += size * size * 4
	}
	renderer.UploadAtlas(1, make([]byte, perLayer))
	quad := encodeLodQuad(0, 0, 70, 8, 8, 0, 0, 255)
	// 整 tile 替换语义:重复上传同 tile 是整体替换,渲染器侧无 Go 可见
	// 计数,这里只验证两次上传后帧路径保持稳定。
	renderer.UploadLodTile(0, 0, quad)
	renderer.UploadLodTile(0, 0, quad)
	renderer.DropLodTile(9, 9) // 未知 tile 幂等。

	frame := RenderFrame{Daylight: 1, SkyColor: [4]float32{0.2, 0.4, 1, 1}}
	for i := 0; i < 4; i++ {
		frame.ViewProj[i*4+i] = 1
		frame.ViewProjInv[i*4+i] = 1
	}
	if !renderer.RenderFrame(frame) {
		t.Fatal("离屏渲染不应跳帧")
	}
	first := renderer.Readback()
	if len(first) != 32*16*4 {
		t.Fatalf("readback 长度=%d", len(first))
	}
	// 空流等价 drop;tile 在恒等相机的视锥外,上传与丢弃都不改变图像,
	// 两帧仍必须逐字节一致(帧路径稳定)。
	renderer.UploadLodTile(0, 0, nil)
	if !renderer.RenderFrame(frame) {
		t.Fatal("离屏渲染不应跳帧")
	}
	second := renderer.Readback()
	if string(first) != string(second) {
		t.Fatal("同输入两帧必须逐字节一致")
	}
}
