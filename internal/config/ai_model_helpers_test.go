package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeConfig 把 body 写进临时目录里的 config.json 并返回路径。
// ai_model_test.go 位于内部测试包（package config），无法复用外部测试包
// config_test 里的同名辅助函数，这里提供行为一致的一份，避免测试样板漂移。
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("写测试配置: %v", err)
	}
	return path
}
