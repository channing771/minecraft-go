//go:build darwin

package client

// 本文件是 `mornlea_client` render ABI 族的 Go 绑定(v2 引入,v6 增补
// 远环 tile 上传/丢弃入口与雾参数化 SetLodFog——tile 出口变基重编为 v6,
// main 的 water pass 占用 v5;雾 setter 随后由 v7 定版):R2a 的离屏
// Rust 渲染器只被双后端对照测试与后续期使用,生产渲染仍是 Go 路径。
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
#cgo noescape mornlea_client_render_upload_lod_tile
#cgo nocallback mornlea_client_render_upload_lod_tile
#cgo noescape mornlea_client_render_drop_lod_tile
#cgo nocallback mornlea_client_render_drop_lod_tile
#cgo noescape mornlea_client_render_set_lod_fog
#cgo nocallback mornlea_client_render_set_lod_fog
#cgo noescape mornlea_client_render_frame
#cgo nocallback mornlea_client_render_frame
#cgo noescape mornlea_client_render_readback
#cgo nocallback mornlea_client_render_readback
#cgo noescape mornlea_client_render_upload_glyph_rect
#cgo nocallback mornlea_client_render_upload_glyph_rect
#cgo noescape mornlea_client_render_upload_hud_atlas
#cgo nocallback mornlea_client_render_upload_hud_atlas
#cgo noescape mornlea_client_render_create_windowed
#cgo nocallback mornlea_client_render_create_windowed
#cgo noescape mornlea_client_render_resize
#cgo nocallback mornlea_client_render_resize
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
	// windowed 标记 surface 模式(不支持 Readback)。
	windowed bool
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
	// WaterTint 是相机浸没时的全屏水色叠加(RGBA)。A <= 0 表示本帧不叠加,
	// 帧字节与本变更之前逐位一致。它与 OverlayStrength 共用同一条全屏三角
	// 管线,只是 uniform 不同——不新增任何绘制管线。
	WaterTint [4]float32
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

// NewWindowedRenderer 为已创建的窗口建立 surface 渲染器;每帧渲染直接
// 呈现到窗口。必须与窗口同线程调用。
func NewWindowedRenderer(window *Window) (*Renderer, error) {
	var handle C.uint64_t
	status := C.mornlea_client_render_create_windowed(
		C.MORNLEA_CLIENT_ABI_VERSION,
		C.uint64_t(window.handle),
		&handle,
	)
	switch status {
	case C.MORNLEA_CLIENT_STATUS_OK:
		width, height := window.FramebufferSize()
		return &Renderer{handle: uint64(handle), width: width, height: height, windowed: true}, nil
	case C.MORNLEA_CLIENT_STATUS_ADAPTER:
		return nil, ErrNoGPUAdapter
	default:
		return nil, errors.New("client: windowed render create " + renderStatusText(uint32(status)))
	}
}

// Resize 调整渲染输出尺寸(窗口模式重配 surface,离屏重建 target)。
func (r *Renderer) Resize(width, height int) {
	r.width, r.height = width, height
	r.check("resize", uint32(C.mornlea_client_render_resize(
		C.MORNLEA_CLIENT_ABI_VERSION,
		C.uint64_t(r.handle),
		C.uint32_t(width), C.uint32_t(height),
	)))
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

// UploadSection 上传/替换一个 section 的两条 packed face 字节流(两条都空
// 等价 drop)。
//
// client ABI v5 起按 material 分流:opaque 是不透明与 cutout 面,water 是水面。
// 两条流的元素格式相同(8 字节 packed quad),分流只决定它们进哪条绘制路径。
func (r *Renderer) UploadSection(x, y, z int32, opaque, water []byte) {
	r.uploadCalls++
	var opaquePtr, waterPtr *C.uint8_t
	if len(opaque) > 0 {
		opaquePtr = (*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(opaque)))
	}
	if len(water) > 0 {
		waterPtr = (*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(water)))
	}
	r.check("upload section", uint32(C.mornlea_client_render_upload_section(
		C.MORNLEA_CLIENT_ABI_VERSION,
		C.uint64_t(r.handle),
		C.int32_t(x), C.int32_t(y), C.int32_t(z),
		opaquePtr,
		C.size_t(len(opaque)),
		waterPtr,
		C.size_t(len(water)),
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

// UploadLodTile 上传/替换一个远环 tile 的壳 quad 字节流(每 quad 20 字节
// LE,布局与 engine mornlea_lod_shell 输出逐字一致;空等价 drop)。整 tile
// 替换语义:重复上传同 tile 即整体替换。tile 坐标为 chunk 坐标,每 tile
// 覆盖 4×4 chunk;流非法或 tile 表容量耗尽时 panic(编程错误)。
func (r *Renderer) UploadLodTile(x, z int32, quads []byte) {
	var ptr *C.uint8_t
	if len(quads) > 0 {
		ptr = (*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(quads)))
	}
	r.check("upload lod tile", uint32(C.mornlea_client_render_upload_lod_tile(
		C.MORNLEA_CLIENT_ABI_VERSION,
		C.uint64_t(r.handle),
		C.int32_t(x), C.int32_t(z),
		ptr,
		C.size_t(len(quads)),
	)))
}

// DropLodTile 丢弃一个远环 tile(幂等)。
func (r *Renderer) DropLodTile(x, z int32) {
	r.check("drop lod tile", uint32(C.mornlea_client_render_drop_lod_tile(
		C.MORNLEA_CLIENT_ABI_VERSION,
		C.uint64_t(r.handle),
		C.int32_t(x), C.int32_t(z),
	)))
}

// SetLodFog 设置远环距离雾参数:start 起雾距离、full 全雾距离(block)。
// Rust 入口校验 start>0 且 full>start(NaN 拒绝),非法参数返回
// INVALID_ARGUMENT 并在本方法表现为 panic(编程错误);校验先于句柄
// 查找,不触碰渲染器状态。渲染器默认 768/1152 锚定 lodFarMultiplier=3
// 的默认几何(0.5/0.75 × 1536),非默认倍率的推导接线由 5.2 按配置
// 计算后调用本方法。
func (r *Renderer) SetLodFog(start, full float32) {
	r.check("set lod fog", uint32(C.mornlea_client_render_set_lod_fog(
		C.MORNLEA_CLIENT_ABI_VERSION,
		C.uint64_t(r.handle),
		C.float(start),
		C.float(full),
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
	// frameTagWater 是水下水色叠加段(4 个 f32:RGBA),client ABI v5 内的追加
	// TLV tag,不升 ABI 版本。
	frameTagWater = 8
)

// hasPassSegments 报告本帧是否携带任一 pass 段(决定 layout 版本)。
func (frame RenderFrame) hasPassSegments() bool {
	return len(frame.AvatarInstances) > 0 || len(frame.DropInstances) > 0 ||
		len(frame.OutlineInstances) > 0 || frame.OverlayStrength > 0 || frame.WaterTint[3] > 0 ||
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
	if frame.WaterTint[3] > 0 {
		var tint [16]byte
		for index, value := range frame.WaterTint {
			binary.LittleEndian.PutUint32(tint[index*4:], math.Float32bits(value))
		}
		appendTLV(frameTagWater, tint[:])
	}
	return out
}

// FrameCalls 返回累计的 RenderFrame FFI 调用次数。
func (r *Renderer) FrameCalls() int { return r.frameCalls }

// UploadCalls 返回累计的 section 上传 FFI 调用次数。
func (r *Renderer) UploadCalls() int { return r.uploadCalls }

// RenderFrame 渲染一帧;每帧恰好一次 render FFI 调用。返回 false 表示
// 窗口 surface 本帧不可用(遮挡/过期),调用方跳帧即可。
func (r *Renderer) RenderFrame(frame RenderFrame) bool {
	r.frameCalls++
	encoded := EncodeRenderFrame(frame)
	status := uint32(C.mornlea_client_render_frame(
		C.MORNLEA_CLIENT_ABI_VERSION,
		C.uint64_t(r.handle),
		(*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(encoded))),
		C.size_t(len(encoded)),
	))
	if status == uint32(C.MORNLEA_CLIENT_STATUS_SKIPPED) {
		return false
	}
	r.check("frame", status)
	return true
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
	case uint32(C.MORNLEA_CLIENT_STATUS_SKIPPED):
		return "surface 本帧不可用"
	default:
		return "client 未知状态"
	}
}
