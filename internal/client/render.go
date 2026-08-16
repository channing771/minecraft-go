//go:build darwin

package client

// 本文件是 `mornlea_client` render ABI(client ABI v2)的 Go 绑定:R2a 的
// 离屏 Rust 渲染器只被双后端对照测试与后续期使用,生产渲染仍是 Go 路径。
// 链接与 include 标志在 window.go 的 cgo 序言中声明,此处只补 render
// 入口的逃逸与回调指令。

/*
#cgo noescape mornlea_client_render_create
#cgo nocallback mornlea_client_render_create
#cgo noescape mornlea_client_render_destroy
#cgo nocallback mornlea_client_render_destroy
#cgo noescape mornlea_client_render_upload_atlas
#cgo nocallback mornlea_client_render_upload_atlas
#cgo noescape mornlea_client_render_upload_section
#cgo nocallback mornlea_client_render_upload_section
#cgo noescape mornlea_client_render_drop_section
#cgo nocallback mornlea_client_render_drop_section
#cgo noescape mornlea_client_render_frame
#cgo nocallback mornlea_client_render_frame
#cgo noescape mornlea_client_render_readback
#cgo nocallback mornlea_client_render_readback
#cgo noescape mornlea_client_render_upload_glyph_rect
#cgo nocallback mornlea_client_render_upload_glyph_rect
#cgo noescape mornlea_client_render_upload_hud_atlas
#cgo nocallback mornlea_client_render_upload_hud_atlas
#include "mornlea_client.h"
*/
import "C"

import (
	"encoding/binary"
	"errors"
	"math"
	"unsafe"
)

// renderFrameHeaderBytes 是 render_frame 输入的固定头部字节数,
// 必须与 Rust `FRAME_HEADER_BYTES` 一致。
const renderFrameHeaderBytes = 192

// ErrNoGPUAdapter 表示本机无可用 GPU 适配器;测试应据此 skip 而非 fail。
var ErrNoGPUAdapter = errors.New("client: 本机无可用 GPU 适配器")

// Renderer 是 Rust 离屏渲染器的句柄封装。所有方法限创建线程调用。
type Renderer struct {
	handle uint64
	width  int
	height int
	closed bool
	// frameCalls 统计 RenderFrame 触发的 FFI 次数,供"每帧一次"断言。
	frameCalls int
	// uploadCalls 统计 section 上传 FFI 次数,供"无变化不上传"断言。
	uploadCalls int
}

// RenderFrame 是一帧渲染输入,字段语义与 render.Camera 一致。
type RenderFrame struct {
	ViewProj       [16]float32
	ViewProjInv    [16]float32
	Pos            [3]float32
	Daylight       float32
	SunDirection   [3]float32
	StarVisibility float32
	SkyColor       [4]float32
	CloudMacroX    uint32
	CloudLocal     float32
	// Visible 是 Go BFS+frustum 算出的可见 section 位置(X, Y, Z)。
	Visible [][3]int32
	// 以下 pass 段为空表示该 pass 本帧缺席;任一非空时帧按 layout v2 编码。
	// AvatarInstances 是 80 字节/实例的 avatar 字节流(render 包编码)。
	AvatarInstances []byte
	// DropInstances 是 80 字节/实例的掉落物字节流。
	DropInstances []byte
	// OutlineInstances 是 12×80 字节的目标方块轮廓实例流。
	OutlineInstances []byte
	// OverlayStrength 是伤害红边强度(>0 才绘制)。
	OverlayStrength float32
	// NameTagSegment/HUDSegment/DebugSegment 是各文本 pass 的
	// [uniform][aCount][bCount][a][b] 段字节(EncodeQuadSegment 产物)。
	NameTagSegment []byte
	HUDSegment     []byte
	DebugSegment   []byte
}

// EncodeQuadSegment 组装文本类 pass 段:[uniform][aCount u32][bCount u32]
// [streamA][streamB];instanceBytes 用于从字节数推回实例数。
func EncodeQuadSegment(uniform, streamA, streamB []byte, instanceBytes int) []byte {
	out := make([]byte, 0, len(uniform)+8+len(streamA)+len(streamB))
	out = append(out, uniform...)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(streamA)/instanceBytes))
	out = binary.LittleEndian.AppendUint32(out, uint32(len(streamB)/instanceBytes))
	out = append(out, streamA...)
	out = append(out, streamB...)
	return out
}

// NewRenderer 创建 Rust 离屏渲染器;无 GPU 适配器返回 ErrNoGPUAdapter。
func NewRenderer(width, height int) (*Renderer, error) {
	var handle C.uint64_t
	status := C.mornlea_client_render_create(
		C.MORNLEA_CLIENT_ABI_VERSION,
		C.uint32_t(width),
		C.uint32_t(height),
		&handle,
	)
	switch status {
	case C.MORNLEA_CLIENT_STATUS_OK:
		return &Renderer{handle: uint64(handle), width: width, height: height}, nil
	case C.MORNLEA_CLIENT_STATUS_ADAPTER:
		return nil, ErrNoGPUAdapter
	default:
		return nil, errors.New("client: render create " + renderStatusText(uint32(status)))
	}
}

// UploadAtlas 上传材质 atlas(assets.Registry.AtlasPixels 的字节流)。
func (r *Renderer) UploadAtlas(layers int, pixels []byte) {
	r.check("upload atlas", uint32(C.mornlea_client_render_upload_atlas(
		C.MORNLEA_CLIENT_ABI_VERSION,
		C.uint64_t(r.handle),
		C.uint32_t(layers),
		(*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(pixels))),
		C.size_t(len(pixels)),
	)))
}

// UploadSection 上传/替换一个 section 的 packed face 字节(空等价 drop)。
func (r *Renderer) UploadSection(x, y, z int32, packed []byte) {
	r.uploadCalls++
	var ptr *C.uint8_t
	if len(packed) > 0 {
		ptr = (*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(packed)))
	}
	r.check("upload section", uint32(C.mornlea_client_render_upload_section(
		C.MORNLEA_CLIENT_ABI_VERSION,
		C.uint64_t(r.handle),
		C.int32_t(x), C.int32_t(y), C.int32_t(z),
		ptr,
		C.size_t(len(packed)),
	)))
}

// DropSection 丢弃一个 section(幂等)。
func (r *Renderer) DropSection(x, y, z int32) {
	r.check("drop section", uint32(C.mornlea_client_render_drop_section(
		C.MORNLEA_CLIENT_ABI_VERSION,
		C.uint64_t(r.handle),
		C.int32_t(x), C.int32_t(y), C.int32_t(z),
	)))
}

// frame v2 的 TLV pass 段 tag,与 Rust 侧常量一致。
const (
	frameTagAvatar  = 1
	frameTagDrop    = 2
	frameTagOutline = 3
	frameTagOverlay = 4
	frameTagNameTag = 5
	frameTagHUD     = 6
	frameTagDebug   = 7
)

// hasPassSegments 报告本帧是否携带任一 pass 段(决定 layout 版本)。
func (frame RenderFrame) hasPassSegments() bool {
	return len(frame.AvatarInstances) > 0 || len(frame.DropInstances) > 0 ||
		len(frame.OutlineInstances) > 0 || frame.OverlayStrength > 0 ||
		len(frame.NameTagSegment) > 0 || len(frame.HUDSegment) > 0 ||
		len(frame.DebugSegment) > 0
}

// EncodeRenderFrame 把帧输入编码为 render_frame 的 ABI 字节:无 pass 段时
// 保持 layout 0(纯地形),否则 layout 2 并追加 TLV 段。
func EncodeRenderFrame(frame RenderFrame) []byte {
	out := make([]byte, renderFrameHeaderBytes+len(frame.Visible)*12)
	for i, v := range frame.ViewProj {
		binary.LittleEndian.PutUint32(out[i*4:], math.Float32bits(v))
	}
	for i, v := range frame.ViewProjInv {
		binary.LittleEndian.PutUint32(out[64+i*4:], math.Float32bits(v))
	}
	for i, v := range frame.Pos {
		binary.LittleEndian.PutUint32(out[128+i*4:], math.Float32bits(v))
	}
	binary.LittleEndian.PutUint32(out[140:], math.Float32bits(frame.Daylight))
	for i, v := range frame.SunDirection {
		binary.LittleEndian.PutUint32(out[144+i*4:], math.Float32bits(v))
	}
	binary.LittleEndian.PutUint32(out[156:], math.Float32bits(frame.StarVisibility))
	for i, v := range frame.SkyColor {
		binary.LittleEndian.PutUint32(out[160+i*4:], math.Float32bits(v))
	}
	binary.LittleEndian.PutUint32(out[176:], frame.CloudMacroX)
	binary.LittleEndian.PutUint32(out[180:], math.Float32bits(frame.CloudLocal))
	binary.LittleEndian.PutUint32(out[184:], uint32(len(frame.Visible)))
	for i, pos := range frame.Visible {
		offset := renderFrameHeaderBytes + i*12
		for j, v := range pos {
			binary.LittleEndian.PutUint32(out[offset+j*4:], uint32(v))
		}
	}
	if !frame.hasPassSegments() {
		return out
	}
	binary.LittleEndian.PutUint32(out[188:], 2)
	appendTLV := func(tag uint32, payload []byte) {
		if len(payload) == 0 {
			return
		}
		out = binary.LittleEndian.AppendUint32(out, tag)
		out = binary.LittleEndian.AppendUint32(out, uint32(len(payload)))
		out = append(out, payload...)
	}
	appendTLV(frameTagAvatar, frame.AvatarInstances)
	appendTLV(frameTagDrop, frame.DropInstances)
	appendTLV(frameTagOutline, frame.OutlineInstances)
	if frame.OverlayStrength > 0 {
		var strength [4]byte
		binary.LittleEndian.PutUint32(strength[:], math.Float32bits(frame.OverlayStrength))
		appendTLV(frameTagOverlay, strength[:])
	}
	appendTLV(frameTagNameTag, frame.NameTagSegment)
	appendTLV(frameTagHUD, frame.HUDSegment)
	appendTLV(frameTagDebug, frame.DebugSegment)
	return out
}

// FrameCalls 返回累计的 RenderFrame FFI 调用次数。
func (r *Renderer) FrameCalls() int { return r.frameCalls }

// UploadCalls 返回累计的 section 上传 FFI 调用次数。
func (r *Renderer) UploadCalls() int { return r.uploadCalls }

// RenderFrame 渲染一帧;每帧恰好一次 render FFI 调用。
func (r *Renderer) RenderFrame(frame RenderFrame) {
	r.frameCalls++
	encoded := EncodeRenderFrame(frame)
	r.check("frame", uint32(C.mornlea_client_render_frame(
		C.MORNLEA_CLIENT_ABI_VERSION,
		C.uint64_t(r.handle),
		(*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(encoded))),
		C.size_t(len(encoded)),
	)))
}

// UploadGlyphRect 上传字形图集的一块 R8 矩形(与 Go GlyphAtlas 同字节)。
func (r *Renderer) UploadGlyphRect(x, y, width, height int, pixels []byte) {
	r.check("upload glyph rect", uint32(C.mornlea_client_render_upload_glyph_rect(
		C.MORNLEA_CLIENT_ABI_VERSION,
		C.uint64_t(r.handle),
		C.uint32_t(x), C.uint32_t(y), C.uint32_t(width), C.uint32_t(height),
		(*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(pixels))),
		C.size_t(len(pixels)),
	)))
}

// UploadHUDAtlas 上传 HUD 图集(一次性 RGBA)。
func (r *Renderer) UploadHUDAtlas(width, height int, pixels []byte) {
	r.check("upload hud atlas", uint32(C.mornlea_client_render_upload_hud_atlas(
		C.MORNLEA_CLIENT_ABI_VERSION,
		C.uint64_t(r.handle),
		C.uint32_t(width), C.uint32_t(height),
		(*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(pixels))),
		C.size_t(len(pixels)),
	)))
}

// Readback 阻塞回读离屏 BGRA 图像(width×height×4 字节)。
func (r *Renderer) Readback() []byte {
	out := make([]byte, r.width*r.height*4)
	r.check("readback", uint32(C.mornlea_client_render_readback(
		C.MORNLEA_CLIENT_ABI_VERSION,
		C.uint64_t(r.handle),
		(*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(out))),
		C.size_t(len(out)),
	)))
	return out
}

// Close 销毁渲染器;重复调用安全。
func (r *Renderer) Close() {
	if r.closed {
		return
	}
	r.closed = true
	r.check("destroy", uint32(C.mornlea_client_render_destroy(
		C.MORNLEA_CLIENT_ABI_VERSION, C.uint64_t(r.handle))))
}

func (r *Renderer) check(operation string, status uint32) {
	if status != uint32(C.MORNLEA_CLIENT_STATUS_OK) {
		panic("client: render " + operation + " " + renderStatusText(status))
	}
}

func renderStatusText(status uint32) string {
	switch status {
	case uint32(C.MORNLEA_CLIENT_STATUS_ABI_VERSION):
		return "client ABI 版本不匹配"
	case uint32(C.MORNLEA_CLIENT_STATUS_INVALID_ARGUMENT):
		return "client 参数非法"
	case uint32(C.MORNLEA_CLIENT_STATUS_WINDOW):
		return "client 句柄无效或资源操作失败"
	case uint32(C.MORNLEA_CLIENT_STATUS_PANIC):
		return "client Rust panic"
	case uint32(C.MORNLEA_CLIENT_STATUS_ADAPTER):
		return "本机无可用 GPU 适配器"
	case uint32(C.MORNLEA_CLIENT_STATUS_CAPACITY):
		return "渲染资源容量不足"
	default:
		return "client 未知状态"
	}
}
