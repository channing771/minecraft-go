//go:build cgo && (darwin || linux)

// Package nativeabi 提供唯一的 Rust engine C ABI 入口。
package nativeabi

/*
#cgo CFLAGS: -I${SRCDIR}/../../engine/include
#cgo LDFLAGS: -L${SRCDIR}/../../engine/target/release -lmornlea_engine -Wl,-rpath,${SRCDIR}/../../engine/target/release
#cgo noescape mornlea_engine_abi_version
#cgo nocallback mornlea_engine_abi_version
#cgo noescape mornlea_mesh_section
#cgo nocallback mornlea_mesh_section
#cgo noescape mornlea_collision_resolve
#cgo nocallback mornlea_collision_resolve
#cgo noescape mornlea_raycast_batch
#cgo nocallback mornlea_raycast_batch
#cgo noescape mornlea_physics_step
#cgo nocallback mornlea_physics_step
#cgo noescape mornlea_worldgen_chunk
#cgo nocallback mornlea_worldgen_chunk
#cgo noescape mornlea_worldgen_probe
#cgo nocallback mornlea_worldgen_probe
#cgo noescape mornlea_lod_shell
#cgo nocallback mornlea_lod_shell
#include "mornlea_engine.h"
*/
import "C"

import (
	"encoding/binary"
	"unsafe"
)

// Status 是 engine C ABI 返回的状态码。
type Status uint32

const (
	ABIVersion                   = uint32(C.MORNLEA_ENGINE_ABI_VERSION)
	StatusOK              Status = Status(C.MORNLEA_STATUS_OK)
	StatusABIVersion      Status = Status(C.MORNLEA_STATUS_ABI_VERSION)
	StatusInvalidArgument Status = Status(C.MORNLEA_STATUS_INVALID_ARGUMENT)
	StatusInput           Status = Status(C.MORNLEA_STATUS_INPUT)
	StatusScratch         Status = Status(C.MORNLEA_STATUS_SCRATCH)
	StatusRegistry        Status = Status(C.MORNLEA_STATUS_REGISTRY)
	StatusEmission        Status = Status(C.MORNLEA_STATUS_EMISSION)
	StatusOutputOverflow  Status = Status(C.MORNLEA_STATUS_OUTPUT_OVERFLOW)
	StatusQueueOverflow   Status = Status(C.MORNLEA_STATUS_QUEUE_OVERFLOW)
	StatusPanic           Status = Status(C.MORNLEA_STATUS_PANIC)
)

// EngineABIVersion 返回当前 engine 导出的 ABI 版本。
func EngineABIVersion() uint32 {
	return uint32(C.mornlea_engine_abi_version())
}

// MeshSection 把调用方拥有的 mesh ABI 缓冲区传给 engine。
func MeshSection(version uint32, input []byte, scratch, output []uint64) (Status, int) {
	var outputLen C.size_t
	status := C.mornlea_mesh_section(
		C.uint32_t(version),
		(*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(input))),
		C.size_t(len(input)),
		(*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(scratch))),
		C.size_t(len(scratch)*8),
		(*C.uint64_t)(unsafe.Pointer(unsafe.SliceData(output))),
		C.size_t(len(output)),
		&outputLen,
	)
	return Status(status), int(outputLen)
}

// CollisionResolve 把调用方拥有的 collision ABI 缓冲区传给 engine。
func CollisionResolve(input, output []byte) {
	status := collisionResolveVersion(ABIVersion, input, output)
	if status != StatusOK {
		panic(collisionStatusPanicText(status))
	}
}

func collisionResolveVersion(version uint32, input, output []byte) Status {
	return Status(C.mornlea_collision_resolve(
		C.uint32_t(version),
		(*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(input))),
		C.size_t(len(input)),
		(*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(output))),
		C.size_t(len(output)),
	))
}

func collisionStatusPanicText(status Status) string {
	switch status {
	case StatusABIVersion:
		return "nativeabi: collision ABI 版本不匹配"
	case StatusInvalidArgument:
		return "nativeabi: collision 参数非法"
	case StatusInput:
		return "nativeabi: collision 输入非法"
	case StatusOutputOverflow:
		return "nativeabi: collision output 过短"
	case StatusPanic:
		return "nativeabi: collision Rust panic"
	default:
		return "nativeabi: collision 未知状态"
	}
}

// PhysicsStep 把调用方拥有的 physics step ABI 缓冲区传给 engine。
func PhysicsStep(input, output []byte) {
	status := physicsStepVersion(ABIVersion, input, output)
	if status != StatusOK {
		panic(physicsStepStatusPanicText(status))
	}
}

func physicsStepVersion(version uint32, input, output []byte) Status {
	return Status(C.mornlea_physics_step(
		C.uint32_t(version),
		(*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(input))),
		C.size_t(len(input)),
		(*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(output))),
		C.size_t(len(output)),
	))
}

func physicsStepStatusPanicText(status Status) string {
	switch status {
	case StatusABIVersion:
		return "nativeabi: physics step ABI 版本不匹配"
	case StatusInvalidArgument:
		return "nativeabi: physics step 参数非法"
	case StatusInput:
		return "nativeabi: physics step 输入非法"
	case StatusOutputOverflow:
		return "nativeabi: physics step output 过短"
	case StatusPanic:
		return "nativeabi: physics step Rust panic"
	default:
		return "nativeabi: physics step 未知状态"
	}
}

// WorldgenChunk 把调用方拥有的 worldgen chunk ABI 缓冲区传给 engine。
//
// input 为 `MGW1` header + chunk 坐标,output 为 196608 字节 dense 数组;
// 任何非 OK 状态都以稳定中文文案 panic,且 engine 保证失败时不触碰 output。
func WorldgenChunk(input, output []byte) {
	status := worldgenChunkVersion(ABIVersion, input, output)
	if status != StatusOK {
		panic(worldgenStatusPanicText("chunk", status))
	}
}

func worldgenChunkVersion(version uint32, input, output []byte) Status {
	return Status(C.mornlea_worldgen_chunk(
		C.uint32_t(version),
		(*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(input))),
		C.size_t(len(input)),
		(*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(output))),
		C.size_t(len(output)),
	))
}

// WorldgenProbe 把调用方拥有的 worldgen 单点查询 batch 缓冲区传给 engine。
//
// input 为 `MGW1` header + record_count + 每条 16 字节记录(最多 64 条),
// output 为每条 8 字节的结果;任何非 OK 状态都以稳定中文文案 panic。
func WorldgenProbe(input, output []byte) {
	status := worldgenProbeVersion(ABIVersion, input, output)
	if status != StatusOK {
		panic(worldgenStatusPanicText("probe", status))
	}
}

func worldgenProbeVersion(version uint32, input, output []byte) Status {
	return Status(C.mornlea_worldgen_probe(
		C.uint32_t(version),
		(*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(input))),
		C.size_t(len(input)),
		(*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(output))),
		C.size_t(len(output)),
	))
}

func worldgenStatusPanicText(entry string, status Status) string {
	switch status {
	case StatusABIVersion:
		return "nativeabi: worldgen " + entry + " ABI 版本不匹配"
	case StatusInvalidArgument:
		return "nativeabi: worldgen " + entry + " 参数非法"
	case StatusInput:
		return "nativeabi: worldgen " + entry + " 输入非法"
	case StatusOutputOverflow:
		return "nativeabi: worldgen " + entry + " output 过短"
	case StatusPanic:
		return "nativeabi: worldgen " + entry + " Rust panic"
	default:
		return "nativeabi: worldgen " + entry + " 未知状态"
	}
}

// lod shell ABI 布局常量,与 engine `lod.rs` 的
// LOD_SHELL_INPUT_BYTES/LOD_SHELL_QUAD_BYTES/LOD_TILE_COLUMNS 逐字一致。
const (
	// LodShellInputBytes 是 `mornlea_lod_shell` 入口输入字节数:与
	// `mornlea_worldgen_chunk` 完全一致的 MGW1 header(564)+ tile_x i32 +
	// tile_z i32 + columns u32(固定 64)+ lod_step u32(合法值 2/4/8)。
	LodShellInputBytes = 580
	// LodShellQuadBytes 是输出流中单个壳 quad 的字节数:x/z/y i32 +
	// w/d u16 + face u8 + material u16 + shade u8(LE)。
	LodShellQuadBytes = 20
	// LodShellTileColumns 是 tile 固定列数:4×4 chunk = 64×64 列。
	LodShellTileColumns = 64
)

// LodShellOutputBoundBytes 返回给定步长下固定 tile 壳输出的静态最坏字节数。
//
// 最坏 quad 数 = 3N²+2N(N = 64/step):顶面最坏每窗一个(N²),X/Z 两向
// 断差裙边各 N×(N+1) 对(tile 边界对外一圈也参与)。该上界只依赖固定
// 输入形状,与地形内容无关,是"按步长静态上界一次性预分配"策略的依据:
// 正常路径一次调用成功,避免两段式探测的双跑生成。非法步长返回 false。
func LodShellOutputBoundBytes(step uint32) (int, bool) {
	switch step {
	case 2, 4, 8:
		n := uint64(LodShellTileColumns / step)
		quads := 3*n*n + 2*n
		return int(quads * LodShellQuadBytes), true
	default:
		return 0, false
	}
}

// LodShell 生成一个 LOD tile 的确定性远环壳 quad 字节流并返回写入部分。
//
// input 为 LodShellInputBytes 字节请求(布局见 LodShellInputBytes);返回
// 切片按步长静态上界一次性预分配(裁决:避免两段式探测的双跑生成),
// 调用方只读。OUTPUT_OVERFLOW 的扩容重试语义路径仍然保留(静态上界
// 理论上不可被超出,重试是对契约变更的防御);任何非 OK 状态以稳定
// 中文文案 panic,镜像 worldgen 绑定,engine 保证失败时不触碰输出缓冲。
func LodShell(input []byte) []byte {
	output := make([]byte, lodShellBoundBytes(input))
	return lodShellGenerate(ABIVersion, input, output)
}

// lodShellGenerate 在调用方给定的初始缓冲上生成壳流;容量不足时按两段式
// 探测报告的所需字节数扩容重试一次。确定性纯函数下同输入重试必成功,
// 若重试仍溢出说明 engine 破坏确定性契约,落入 overflow panic 文案。
func lodShellGenerate(version uint32, input, output []byte) []byte {
	status, outputLen := lodShellVersion(version, input, output)
	if status == StatusOutputOverflow {
		output = make([]byte, outputLen)
		status, outputLen = lodShellVersion(version, input, output)
	}
	if status != StatusOK {
		panic(lodShellStatusPanicText(status))
	}
	return output[:outputLen]
}

// lodShellBoundBytes 从输入尾部解析步长并返回对应静态上界;输入长度
// 非法或步长不合法时退化为最大档(step 2)上界,让 engine 裁决真实
// status,而不是在绑定层抢答。
func lodShellBoundBytes(input []byte) int {
	step := uint32(2)
	if len(input) == LodShellInputBytes {
		step = binary.LittleEndian.Uint32(input[LodShellInputBytes-4:])
	}
	if bound, ok := LodShellOutputBoundBytes(step); ok {
		return bound
	}
	bound, _ := LodShellOutputBoundBytes(2)
	return bound
}

// lodShellVersion 把调用方拥有的 LOD 壳输入与输出缓冲传给 engine。
//
// 返回 (status, outputLen):成功时 outputLen 为实际写入字节数;
// OUTPUT_OVERFLOW 时 outputLen 为所需字节数(输出缓冲不写入任何字节),
// 调用方扩容后重试;其余失败路径 outputLen 恒为 0。
func lodShellVersion(version uint32, input, output []byte) (Status, int) {
	var outputLen C.size_t
	status := C.mornlea_lod_shell(
		C.uint32_t(version),
		(*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(input))),
		C.size_t(len(input)),
		(*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(output))),
		C.size_t(len(output)),
		&outputLen,
	)
	return Status(status), int(outputLen)
}

func lodShellStatusPanicText(status Status) string {
	switch status {
	case StatusABIVersion:
		return "nativeabi: lod shell ABI 版本不匹配"
	case StatusInvalidArgument:
		return "nativeabi: lod shell 参数非法"
	case StatusInput:
		return "nativeabi: lod shell 输入非法"
	case StatusOutputOverflow:
		return "nativeabi: lod shell output 过短"
	case StatusPanic:
		return "nativeabi: lod shell Rust panic"
	default:
		return "nativeabi: lod shell 未知状态"
	}
}

// RaycastBatch 把调用方拥有的 raycast input、cursor 与 output 传给 engine。
func RaycastBatch(input, cursor, output []byte) (count int, done bool) {
	status, outputCount, rawDone := raycastBatchVersion(ABIVersion, input, cursor, output)
	return raycastBatchResult(status, outputCount, rawDone)
}

func raycastBatchVersion(
	version uint32,
	input, cursor, output []byte,
) (Status, uintptr, uint8) {
	outputCount := ^C.size_t(0)
	done := C.uint8_t(0xff)
	status := C.mornlea_raycast_batch(
		C.uint32_t(version),
		(*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(input))),
		C.size_t(len(input)),
		(*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(cursor))),
		C.size_t(len(cursor)),
		(*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(output))),
		C.size_t(len(output)),
		&outputCount,
		&done,
	)
	return Status(status), uintptr(outputCount), uint8(done)
}

func raycastBatchResult(status Status, count uintptr, rawDone uint8) (int, bool) {
	if status != StatusOK {
		panic(raycastStatusPanicText(status))
	}
	if count > 64 || rawDone > 1 || count == 0 && rawDone == 0 {
		panic("nativeabi: raycast success metadata 非法")
	}
	return int(count), rawDone == 1
}

func raycastStatusPanicText(status Status) string {
	switch status {
	case StatusABIVersion:
		return "nativeabi: raycast ABI 版本不匹配"
	case StatusInvalidArgument:
		return "nativeabi: raycast 参数非法"
	case StatusInput:
		return "nativeabi: raycast 输入非法"
	case StatusOutputOverflow:
		return "nativeabi: raycast output 过短"
	case StatusPanic:
		return "nativeabi: raycast Rust panic"
	default:
		return "nativeabi: raycast 未知状态"
	}
}
