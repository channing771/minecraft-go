package worldgen

import "testing"

// perm 播种是 Go 侧保留的唯一确定性计算:同种子必须逐字节稳定,
// 且表本身必须是 0..255 的合法置换重复两遍。
func TestPermTableIsDeterministicPermutation(t *testing.T) {
	a := permTable(42)
	b := permTable(42)
	if a != b {
		t.Fatal("同种子 perm 表不一致")
	}
	var seen [256]bool
	for i := 0; i < 256; i++ {
		if a[i] != a[i+256] {
			t.Fatalf("perm[%d]=%d 与 perm[%d]=%d 不一致,应重复两遍", i, a[i], i+256, a[i+256])
		}
		if seen[a[i]] {
			t.Fatalf("perm 值 %d 重复,不是合法置换", a[i])
		}
		seen[a[i]] = true
	}
}

func TestPermTableDiffersBySeed(t *testing.T) {
	if permTable(1) == permTable(2) {
		t.Fatal("不同种子 perm 表相同,种子未生效")
	}
}
