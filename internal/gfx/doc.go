// Package gfx 封装所有与 WebGPU 绑定相关的调用。
//
// 架构约束（自 M0 第一行代码起生效）：
//   - 整个工程中，只有本包可以 import WebGPU 绑定（github.com/oliverbestmann/webgpu/wgpu）。
//   - 本包不 import 任何窗口库（GLFW）。它只接收一个平台相关的原生窗口句柄，
//     从而与窗口系统解耦。
//
// M0 阶段本包只有一个 Probe 类型，用于验证 macOS/Metal 上的设备与 surface
// 创建链路。Task 2 会把它演化成完整的设备抽象。
package gfx
