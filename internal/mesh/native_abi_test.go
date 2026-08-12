//go:build darwin && cgo

package mesh

import "testing"

func TestNativeABIVersionMatchesGo(t *testing.T) {
	if got := nativeABIVersion(); got != nativeABIVersionCurrent {
		t.Fatalf("native ABI version=%d，想要 %d", got, nativeABIVersionCurrent)
	}
}
