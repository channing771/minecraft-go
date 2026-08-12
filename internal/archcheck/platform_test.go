package archcheck_test

import (
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestOnlyGfxImportsWebGPU(t *testing.T) {
	cmd := exec.Command("go", "list", "-f", "{{.ImportPath}}|{{join .Imports \" \"}}", "./...")
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

func TestMCGodHasNoGraphicsDependencies(t *testing.T) {
	root := filepath.Join(moduleRoot(t), "cmd", "mcgod")
	files, err := filepath.Glob(filepath.Join(root, "*.go"))
	if err != nil {
		t.Fatalf("枚举 mcgod Go 文件: %v", err)
	}
	if len(files) == 0 {
		t.Fatalf("mcgod must contain Go source files")
	}
	for _, path := range files {
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("解析 %s: %v", path, err)
		}
		for _, imported := range parsed.Imports {
			path := strings.Trim(imported.Path.Value, "\"")
			for _, forbidden := range []string{"minecraft-go/internal/client", "minecraft-go/internal/render", "minecraft-go/internal/gfx", "github.com/go-gl/glfw", "github.com/oliverbestmann/webgpu", "golang.org/x/image", "golang.org/x/image/font"} {
				if strings.HasPrefix(path, forbidden) {
					t.Errorf("%s imports forbidden graphics dependency %s", path, imported.Path.Value)
				}
			}
		}
	}

	command := exec.Command("go", "list", "-f", "{{.ImportPath}}", "-deps", "./cmd/mcgod")
	command.Dir = moduleRoot(t)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("枚举 mcgod 传递依赖: %v", err)
	}
	for _, dependency := range strings.Fields(string(output)) {
		for _, forbidden := range []string{"minecraft-go/internal/client", "minecraft-go/internal/render", "minecraft-go/internal/gfx", "github.com/go-gl/glfw", "github.com/oliverbestmann/webgpu", "golang.org/x/image", "golang.org/x/image/font"} {
			if dependency == forbidden || strings.HasPrefix(dependency, forbidden+"/") {
				t.Errorf("mcgod transitively depends on forbidden graphics package %s", dependency)
			}
		}
	}
}
