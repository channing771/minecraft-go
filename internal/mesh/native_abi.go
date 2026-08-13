//go:build darwin && cgo

package mesh

/*
#cgo CFLAGS: -I${SRCDIR}/../../engine/include
#cgo LDFLAGS: -L${SRCDIR}/../../engine/target/release -lmornlea_mesh -Wl,-rpath,${SRCDIR}/../../engine/target/release
#include "mornlea_engine.h"
*/
import "C"

import "unsafe"

const nativeABIVersionCurrent = uint32(C.MORNLEA_ENGINE_ABI_VERSION)

type nativeStatus uint32

const (
	nativeStatusOK              = nativeStatus(C.MORNLEA_STATUS_OK)
	nativeStatusABIVersion      = nativeStatus(C.MORNLEA_STATUS_ABI_VERSION)
	nativeStatusInvalidArgument = nativeStatus(C.MORNLEA_STATUS_INVALID_ARGUMENT)
	nativeStatusInput           = nativeStatus(C.MORNLEA_STATUS_INPUT)
	nativeStatusScratch         = nativeStatus(C.MORNLEA_STATUS_SCRATCH)
	nativeStatusRegistry        = nativeStatus(C.MORNLEA_STATUS_REGISTRY)
	nativeStatusEmission        = nativeStatus(C.MORNLEA_STATUS_EMISSION)
	nativeStatusOutputOverflow  = nativeStatus(C.MORNLEA_STATUS_OUTPUT_OVERFLOW)
	nativeStatusQueueOverflow   = nativeStatus(C.MORNLEA_STATUS_QUEUE_OVERFLOW)
	nativeStatusPanic           = nativeStatus(C.MORNLEA_STATUS_PANIC)
)

var nativeStatusPanicTexts = [...]string{
	nativeStatusABIVersion:      "mesh: native ABI 版本不匹配",
	nativeStatusInvalidArgument: "mesh: native 参数非法",
	nativeStatusInput:           "mesh: native 输入非法",
	nativeStatusScratch:         "mesh: native scratch 非法",
	nativeStatusRegistry:        "mesh: registry snapshot 非法",
	nativeStatusEmission:        "mesh: 方块发光等级超过 15",
	nativeStatusOutputOverflow:  "mesh: 四边形输出溢出",
	nativeStatusQueueOverflow:   "mesh: 光照内部队列溢出",
	nativeStatusPanic:           "mesh: Rust 网格内部 panic",
}

func nativeStatusPanicText(status nativeStatus) string {
	if int(status) < len(nativeStatusPanicTexts) && nativeStatusPanicTexts[status] != "" {
		return nativeStatusPanicTexts[status]
	}
	return "mesh: native 返回未知状态"
}

func nativeABIVersion() uint32 {
	return uint32(C.mornlea_engine_abi_version())
}

func nativeMeshSection(input []byte, scratch []uint64, output []uint64) (nativeStatus, int) {
	return nativeMeshSectionVersion(nativeABIVersionCurrent, input, scratch, output)
}

func nativeMeshSectionVersion(version uint32, input []byte, scratch []uint64, output []uint64) (nativeStatus, int) {
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
	return nativeStatus(status), int(outputLen)
}
