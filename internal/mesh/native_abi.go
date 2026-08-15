//go:build cgo && (darwin || linux)

package mesh

import "github.com/channing771/mornlea/internal/nativeabi"

const nativeABIVersionCurrent = nativeabi.ABIVersion

type nativeStatus = nativeabi.Status

const (
	nativeStatusOK              = nativeabi.StatusOK
	nativeStatusABIVersion      = nativeabi.StatusABIVersion
	nativeStatusInvalidArgument = nativeabi.StatusInvalidArgument
	nativeStatusInput           = nativeabi.StatusInput
	nativeStatusScratch         = nativeabi.StatusScratch
	nativeStatusRegistry        = nativeabi.StatusRegistry
	nativeStatusEmission        = nativeabi.StatusEmission
	nativeStatusOutputOverflow  = nativeabi.StatusOutputOverflow
	nativeStatusQueueOverflow   = nativeabi.StatusQueueOverflow
	nativeStatusPanic           = nativeabi.StatusPanic
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
	return nativeabi.EngineABIVersion()
}

func nativeMeshSection(input []byte, scratch []uint64, output []uint64) (nativeStatus, int) {
	return nativeMeshSectionVersion(nativeABIVersionCurrent, input, scratch, output)
}

func nativeMeshSectionVersion(version uint32, input []byte, scratch []uint64, output []uint64) (nativeStatus, int) {
	return nativeabi.MeshSection(version, input, scratch, output)
}
