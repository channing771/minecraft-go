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
#include "mornlea_engine.h"
*/
import "C"

import "unsafe"

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
