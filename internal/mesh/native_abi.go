//go:build darwin && cgo

package mesh

/*
#cgo CFLAGS: -I${SRCDIR}/../../engine/include
#cgo LDFLAGS: -L${SRCDIR}/../../engine/target/release -lmcgo_mesh -Wl,-rpath,${SRCDIR}/../../engine/target/release
#include "mcgo_engine.h"
*/
import "C"

import "unsafe"

const nativeABIVersionCurrent = uint32(C.MCGO_ENGINE_ABI_VERSION)

type nativeStatus uint32

const (
	nativeStatusOK              = nativeStatus(C.MCGO_STATUS_OK)
	nativeStatusABIVersion      = nativeStatus(C.MCGO_STATUS_ABI_VERSION)
	nativeStatusInvalidArgument = nativeStatus(C.MCGO_STATUS_INVALID_ARGUMENT)
	nativeStatusInput           = nativeStatus(C.MCGO_STATUS_INPUT)
	nativeStatusScratch         = nativeStatus(C.MCGO_STATUS_SCRATCH)
	nativeStatusRegistry        = nativeStatus(C.MCGO_STATUS_REGISTRY)
	nativeStatusEmission        = nativeStatus(C.MCGO_STATUS_EMISSION)
	nativeStatusOutputOverflow  = nativeStatus(C.MCGO_STATUS_OUTPUT_OVERFLOW)
	nativeStatusQueueOverflow   = nativeStatus(C.MCGO_STATUS_QUEUE_OVERFLOW)
	nativeStatusPanic           = nativeStatus(C.MCGO_STATUS_PANIC)
)

func nativeABIVersion() uint32 {
	return uint32(C.mcgo_engine_abi_version())
}

func nativeMeshSection(input []byte, scratch []uint64, output []uint64) (nativeStatus, int) {
	var outputLen C.size_t
	status := C.mcgo_mesh_section(
		C.uint32_t(nativeABIVersionCurrent),
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
