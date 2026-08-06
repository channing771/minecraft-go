//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBenchmarkHeapProfile(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		directory := t.TempDir()
		t.Chdir(directory)
		if err := writeBenchmarkHeapProfile("", "post-still"); err != nil {
			t.Fatalf("空 prefix 写 heap profile: %v", err)
		}
		entries, err := os.ReadDir(directory)
		if err != nil {
			t.Fatalf("读取临时目录: %v", err)
		}
		if len(entries) != 0 {
			t.Fatalf("空 prefix 创建了文件: %v", entries)
		}
	})

	t.Run("writes private profile", func(t *testing.T) {
		prefix := filepath.Join(t.TempDir(), "heap")
		const stage = "post-still"
		if err := writeBenchmarkHeapProfile(prefix, stage); err != nil {
			t.Fatalf("写 heap profile: %v", err)
		}
		info, err := os.Stat(prefix + "-" + stage + ".pprof")
		if err != nil {
			t.Fatalf("读取 heap profile: %v", err)
		}
		if info.Size() == 0 {
			t.Fatal("heap profile 为空")
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("heap profile 权限 = %04o，期望 0600", got)
		}
	})

	t.Run("refuses duplicate path", func(t *testing.T) {
		prefix := filepath.Join(t.TempDir(), "heap")
		const stage = "post-flying"
		path := prefix + "-" + stage + ".pprof"
		if err := os.WriteFile(path, []byte("existing"), 0o600); err != nil {
			t.Fatalf("创建占位文件: %v", err)
		}
		err := writeBenchmarkHeapProfile(prefix, stage)
		if err == nil {
			t.Fatal("重复路径未返回错误")
		}
		if !strings.Contains(err.Error(), stage) || !strings.Contains(err.Error(), path) {
			t.Fatalf("重复路径错误未包含 stage 和 path: %v", err)
		}
	})
}
