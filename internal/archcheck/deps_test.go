package archcheck_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// allowed 列出每个内部包允许直接依赖的内部包。
var allowed = map[string][]string{
	"internal/archcheck":  {},
	"internal/core":       {},
	"internal/gfx":        {},
	"internal/gfx/shader": {},
	"internal/network":    {"internal/core"},
	"internal/sim":        {"internal/core", "internal/world"},
	"internal/world":      {"internal/core"},
	"internal/worldgen":   {"internal/core", "internal/world"},
	"internal/mesh":       {"internal/core", "internal/world"},
	"internal/assets": {
		"internal/core", "internal/world", "internal/mesh", "internal/worldgen", "internal/gfx",
	},
	"internal/render": {
		"internal/core", "internal/world", "internal/mesh", "internal/assets", "internal/gfx",
	},
	"internal/client": {
		"internal/core", "internal/world", "internal/mesh", "internal/assets",
		"internal/worldgen", "internal/render", "internal/gfx",
	},
}

func TestInternalDependenciesAreOneWay(t *testing.T) {
	cmd := exec.Command("go", "list", "-f",
		"{{.ImportPath}}|{{join .Imports \" \"}}", "./internal/...")
	cmd.Dir = moduleRoot(t)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("枚举 internal 包失败: %v", err)
	}

	actual := make(map[string]bool)
	imports := make(map[string][]string)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "|", 2)
		pkg := localName(parts[0])
		actual[pkg] = true
		if _, ok := allowed[pkg]; !ok {
			t.Errorf("新增内部包 %s 未登记依赖白名单", pkg)
			continue
		}
		if len(parts) == 2 {
			imports[pkg] = strings.Fields(parts[1])
		}
	}
	for pkg := range allowed {
		if !actual[pkg] {
			t.Errorf("依赖白名单中的包 %s 不存在", pkg)
		}
	}

	for pkg, packageImports := range imports {
		allowSet := make(map[string]bool, len(allowed[pkg]))
		for _, dependency := range allowed[pkg] {
			allowSet[dependency] = true
		}
		for _, importPath := range packageImports {
			dependency := localName(importPath)
			if dependency == importPath {
				continue
			}
			if !allowSet[dependency] {
				t.Errorf("%s 不允许直接依赖 %s", pkg, dependency)
			}
		}
	}
}

func TestOnlyGfxImportsWebGPU(t *testing.T) {
	cmd := exec.Command("go", "list", "-f",
		"{{.ImportPath}}|{{join .Imports \" \"}}", "./...")
	cmd.Dir = moduleRoot(t)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list 失败: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			continue
		}
		for _, imported := range strings.Fields(parts[1]) {
			if strings.Contains(imported, "webgpu") && parts[0] != "minecraft-go/internal/gfx" {
				t.Errorf("%s 直接 import 了 WebGPU 绑定，只有 internal/gfx 可以", parts[0])
			}
		}
	}
}

func localName(importPath string) string {
	return strings.TrimPrefix(strings.TrimSpace(importPath), "minecraft-go/")
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Fatalf("查找 go.mod 失败: %v", err)
	}
	return filepath.Dir(strings.TrimSpace(string(out)))
}
