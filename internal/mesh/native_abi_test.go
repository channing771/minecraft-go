//go:build cgo && (darwin || linux)

package mesh

import "testing"

const nativeOutputCanary = uint64(0xd15e_a5ed_f00d_cafe)

func TestNativeABIVersionMatchesGo(t *testing.T) {
	if got := nativeABIVersion(); got != nativeABIVersionCurrent {
		t.Fatalf("native ABI version=%d，想要 %d", got, nativeABIVersionCurrent)
	}
}

func TestNativeMeshBridgeDoesNotAllocate(t *testing.T) {
	input := make([]byte, maxNativeInputBytes)
	n := fullyLoadedAirNeighborhood()
	n.Center.Blocks.Set(8, 8, 8, 2)
	length, err := encodeNativeInput(input, n, (internalTestRegistry{}).MeshSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	input = input[:length]
	scratch := make([]uint64, (nativeScratchBytes+7)/8)
	output := make([]uint64, maxNativeQuads)

	allocs := testing.AllocsPerRun(1000, func() {
		status, count := nativeMeshSectionVersion(nativeABIVersionCurrent, input, scratch, output)
		if status != nativeStatusOK || count == 0 {
			panic("native mesh 调用失败")
		}
	})
	if allocs != 0 {
		t.Fatalf("native mesh bridge allocations = %v, want 0", allocs)
	}
}

func FuzzNativeMeshRejectsMalformedInput(f *testing.F) {
	valid := make([]byte, maxNativeInputBytes)
	length, err := encodeNativeInput(valid, fullyLoadedAirNeighborhood(), (internalTestRegistry{}).MeshSnapshot())
	if err != nil {
		f.Fatal(err)
	}
	valid = valid[:length]
	f.Add([]byte{})
	f.Add([]byte("MGM1"))
	f.Add(valid[:len(valid)-1])
	f.Add(valid)
	f.Fuzz(func(t *testing.T, input []byte) {
		status, count, output := callNativeRawForTest(input, nativeABIVersionCurrent)
		if status != nativeStatusOK && count != 0 {
			t.Fatalf("status=%v published partial count=%d", status, count)
		}
		if count < 0 || count > maxNativeQuads {
			t.Fatalf("status=%v count=%d 超出 0..%d", status, count, maxNativeQuads)
		}
		if output[maxNativeQuads] != nativeOutputCanary {
			t.Fatal("native 写出了 output capacity")
		}
		for i, packed := range output[count:maxNativeQuads] {
			if packed != nativeOutputCanary {
				t.Fatalf("native 在已发布 count=%d 之后写入 output[%d]", count, count+i)
			}
		}
	})
}

func callNativeRawForTest(input []byte, version uint32) (nativeStatus, int, []uint64) {
	scratch := make([]uint64, (nativeScratchBytes+7)/8)
	output := make([]uint64, maxNativeQuads+1)
	for i := range output {
		output[i] = nativeOutputCanary
	}
	status, count := nativeMeshSectionVersion(version, input, scratch, output[:maxNativeQuads])
	return status, count, output
}
