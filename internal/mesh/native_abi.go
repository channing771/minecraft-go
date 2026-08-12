//go:build darwin && cgo

package mesh

/*
#cgo CFLAGS: -I${SRCDIR}/../../engine/include
#cgo LDFLAGS: ${SRCDIR}/../../engine/target/release/libmcgo_mesh.a
#include "mcgo_engine.h"
*/
import "C"

const nativeABIVersionCurrent = uint32(C.MCGO_ENGINE_ABI_VERSION)

func nativeABIVersion() uint32 {
	return uint32(C.mcgo_engine_abi_version())
}
