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
