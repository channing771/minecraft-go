//go:build darwin

package gfx

import _ "embed"

// ShaderTestDouble 是 headless compute 测试使用的着色器。
//
//go:embed shader/testdouble.wgsl
var ShaderTestDouble string

// ShaderSpikeCull 由 compute shader 筛选实例并填写 indirect 参数。
//
//go:embed shader/spike_cull.wgsl
var ShaderSpikeCull string

// ShaderSpikeDraw 使用 compute shader 生成的可见实例列表绘制方块。
//
//go:embed shader/spike_draw.wgsl
var ShaderSpikeDraw string

// NewHeadlessDevice 创建一个不带 surface 的设备，用于测试与离线计算。
// 本机无可用适配器时返回 error，调用方（测试）应据此 skip 而非 fail——
// CI 容器里常常没有 GPU。
func NewHeadlessDevice() (Device, error) {
	device, _, err := newDevice(NativeWindowHandle{Kind: HandleNone}, 0, 0)
	return device, err
}
