package archcheck_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeEngineBridgeBoundary(t *testing.T) {
	root := moduleRoot(t)
	bridge := filepath.Join(root, "internal", "nativeabi")
	if info, err := os.Stat(bridge); err != nil || !info.IsDir() {
		t.Fatalf("native engine bridge %s 不存在", bridge)
	}
	files := goFiles(t, filepath.Join(root, "internal"))
	files = append(files, goFiles(t, filepath.Join(root, "cmd"))...)

	for _, relative := range []string{
		"mornlea_engine.h",
		"-lmornlea_engine",
		"C.mornlea_",
	} {
		for _, path := range files {
			contents, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("读取 %s: %v", path, err)
			}
			if strings.Contains(string(contents), relative) && !strings.HasPrefix(path, bridge+string(filepath.Separator)) {
				t.Errorf("%s 只允许 internal/nativeabi 接触，发现于 %s", relative, path)
			}
		}
	}
}

func goFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("枚举 %s: %v", root, err)
	}
	return files
}

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
			if strings.Contains(imported, "webgpu") && parts[0] != "github.com/channing771/mornlea/internal/gfx" {
				t.Errorf("%s 直接 import 了 WebGPU 绑定，只有 internal/gfx 可以", parts[0])
			}
		}
	}
}

func TestMornleaServerHasNoGraphicsDependencies(t *testing.T) {
	root := filepath.Join(moduleRoot(t), "cmd", "mornlea-server")
	files, err := filepath.Glob(filepath.Join(root, "*.go"))
	if err != nil {
		t.Fatalf("枚举 Mornlea server Go 文件: %v", err)
	}
	if len(files) == 0 {
		t.Fatalf("Mornlea server must contain Go source files")
	}
	for _, path := range files {
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("解析 %s: %v", path, err)
		}
		for _, imported := range parsed.Imports {
			path := strings.Trim(imported.Path.Value, "\"")
			for _, forbidden := range []string{"github.com/channing771/mornlea/internal/client", "github.com/channing771/mornlea/internal/mesh", "github.com/channing771/mornlea/internal/render", "github.com/channing771/mornlea/internal/gfx", "github.com/go-gl/glfw", "github.com/oliverbestmann/webgpu", "golang.org/x/image", "golang.org/x/image/font"} {
				if strings.HasPrefix(path, forbidden) {
					t.Errorf("%s imports forbidden graphics dependency %s", path, imported.Path.Value)
				}
			}
		}
	}

	command := exec.Command("go", "list", "-f", "{{.ImportPath}}", "-deps", "./cmd/mornlea-server")
	command.Dir = moduleRoot(t)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("枚举 Mornlea server 传递依赖: %v", err)
	}
	foundNativeABI := false
	for _, dependency := range strings.Fields(string(output)) {
		if dependency == "github.com/channing771/mornlea/internal/nativeabi" {
			foundNativeABI = true
		}
		for _, forbidden := range []string{"github.com/channing771/mornlea/internal/client", "github.com/channing771/mornlea/internal/mesh", "github.com/channing771/mornlea/internal/render", "github.com/channing771/mornlea/internal/gfx", "github.com/go-gl/glfw", "github.com/oliverbestmann/webgpu", "golang.org/x/image", "golang.org/x/image/font"} {
			if dependency == forbidden || strings.HasPrefix(dependency, forbidden+"/") {
				t.Errorf("Mornlea server transitively depends on forbidden graphics package %s", dependency)
			}
		}
	}
	if !foundNativeABI {
		t.Error("Mornlea server 依赖闭包必须包含 internal/nativeabi")
	}
}

func TestPhysicsUsesOnlyNativeCollision(t *testing.T) {
	root := filepath.Join(moduleRoot(t), "internal", "physics")

	foundNativeABI := false
	nativePhysicsStepCalls := 0
	for _, path := range goFiles(t, root) {
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatalf("解析 %s: %v", path, err)
		}
		for _, imported := range parsed.Imports {
			if strings.Trim(imported.Path.Value, "\"") == "github.com/channing771/mornlea/internal/nativeabi" {
				foundNativeABI = true
			}
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.SelectorExpr:
				if packageName, ok := node.X.(*ast.Ident); ok && packageName.Name == "nativeabi" && node.Sel.Name == "PhysicsStep" {
					nativePhysicsStepCalls++
				}
			case *ast.Ident:
				if node.Name == "resolveMove" || node.Name == "clipAxis" || node.Name == "resolveStepMove" {
					t.Errorf("%s 保留生产 Go collision resolver %s", path, node.Name)
				}
			}
			return true
		})
	}
	if !foundNativeABI {
		t.Error("internal/physics 必须直接依赖 internal/nativeabi")
	}
	if nativePhysicsStepCalls != 1 || !topLevelDeclarationNamesIn(t, root, "*.go")["Step"] {
		t.Error("internal/physics 必须由 Step 单点编码并调用 native physics_step")
	}
}

func TestCoreUsesOnlyNativeRaycast(t *testing.T) {
	root := filepath.Join(moduleRoot(t), "internal", "core")
	raycastPath := filepath.Join(root, "raycast.go")
	foundNativeABI := false
	nativeCalls := 0
	for _, path := range goFiles(t, root) {
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatalf("解析 %s: %v", path, err)
		}
		if path == raycastPath {
			for _, imported := range parsed.Imports {
				if strings.Trim(imported.Path.Value, "\"") == "github.com/channing771/mornlea/internal/nativeabi" {
					foundNativeABI = true
				}
			}
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.SelectorExpr:
				if packageName, ok := node.X.(*ast.Ident); ok && packageName.Name == "nativeabi" && node.Sel.Name == "RaycastBatch" {
					nativeCalls++
					if path != raycastPath {
						t.Errorf("%s 不允许调用 nativeabi.RaycastBatch", path)
					}
				}
			case *ast.Ident:
				if node.Name == "tDelta" || node.Name == "tMax" || node.Name == "entryFace" {
					t.Errorf("%s 保留生产 Go DDA %s", path, node.Name)
				}
			}
			return true
		})
	}
	if !foundNativeABI || nativeCalls != 1 {
		t.Error("internal/core.RaycastBlocks 必须直接调用 internal/nativeabi.RaycastBatch")
	}
}
