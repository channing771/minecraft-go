package archcheck_test

import (
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestOnlyTCPImplementationImportsNet(t *testing.T) {
	root := filepath.Join(moduleRoot(t), "internal", "network")
	files, err := filepath.Glob(filepath.Join(root, "*.go"))
	if err != nil {
		t.Fatalf("枚举 network Go 文件: %v", err)
	}
	for _, path := range files {
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("解析 %s: %v", path, err)
		}
		for _, imported := range parsed.Imports {
			if imported.Path.Value == `"net"` && filepath.Base(path) != "tcp.go" {
				t.Errorf("%s imports net; only tcp.go may import net", path)
			}
		}
	}
}

func TestLegacyPlayerAuthorityMessagesAreGone(t *testing.T) {
	root := moduleRoot(t)
	forbidden := map[string]struct{}{
		"SetViewCenter":   {},
		"BreakRay":        {},
		"PlaceRay":        {},
		"CommandBreakRay": {},
		"CommandPlaceRay": {},
		"localSessionID":  {},
	}
	for _, sourceRoot := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(
			filepath.Join(root, sourceRoot),
			func(path string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if entry.IsDir() || filepath.Ext(path) != ".go" ||
					path == filepath.Join(root, "internal", "archcheck", "deps_test.go") {
					return nil
				}
				source, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				files := token.NewFileSet()
				var sourceScanner scanner.Scanner
				sourceScanner.Init(files.AddFile(path, -1, len(source)), source, nil, 0)
				for {
					position, kind, literal := sourceScanner.Scan()
					if kind == token.EOF {
						break
					}
					if kind == token.IDENT {
						if _, retired := forbidden[literal]; retired {
							t.Errorf("%s: legacy player authority identifier %s", files.Position(position), literal)
						}
					}
				}
				return nil
			},
		)
		if err != nil {
			t.Fatalf("扫描 %s Go 源码: %v", sourceRoot, err)
		}
	}
}

func TestMcgoUsesLoginStreamsInsteadOfAttachedServerEndpoints(t *testing.T) {
	path := filepath.Join(moduleRoot(t), "cmd", "mcgo", "app.go")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, legacy := range []string{"server.NewEmbedded(", "server.NewEmbeddedMemory(", "server.New("} {
		if strings.Contains(string(source), legacy) {
			t.Errorf("%s retains legacy attached-server constructor %s", path, legacy)
		}
	}
	if !strings.Contains(string(source), "network.NewMemoryStreamPair") ||
		!strings.Contains(string(source), "network.LoginClient") {
		t.Errorf("%s must assemble local connections through a stream login", path)
	}
}

func TestMcgoBenchmarkTCPPathUsesTheSharedLoginStateMachine(t *testing.T) {
	path := filepath.Join(moduleRoot(t), "cmd", "mcgo", "app.go")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"network.ListenTCP(",
		"network.BeginServerLogin(",
		"network.LoginClient(",
		"running.AttachTrustedObserver",
	} {
		if !strings.Contains(string(source), required) {
			t.Errorf("%s benchmark TCP path must contain %s", path, required)
		}
	}
}

func TestServerProductionDoesNotDeclareLegacyAttachedWorldWrappers(t *testing.T) {
	root := filepath.Join(moduleRoot(t), "internal", "server")
	for _, file := range []string{"generator.go", "server.go"} {
		path := filepath.Join(root, file)
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok {
				continue
			}
			switch function.Name.Name {
			case "New", "NewMemory", "NewEmbedded", "NewEmbeddedMemory":
				t.Errorf("%s retains legacy server constructor %s", path, function.Name.Name)
			case "PlayerState":
				if function.Recv != nil && function.Type.Params.NumFields() == 0 {
					t.Errorf("%s retains legacy no-argument PlayerState", path)
				}
			}
		}
	}
}

func TestSessionLifecycleResponsibilitiesLiveInSessionFile(t *testing.T) {
	root := filepath.Join(moduleRoot(t), "internal", "server")
	sessionDeclarations := topLevelDeclarationNames(
		t,
		filepath.Join(root, "session.go"),
	)
	serverDeclarations := topLevelDeclarationNames(
		t,
		filepath.Join(root, "server.go"),
	)
	wantSessionFile := []string{
		"inputCapacity",
		"trustedObserverSessionID",
		"SessionSpec",
		"SessionExit",
		"incomingCommand",
		"trustedObserverCenter",
		"appliedTrustedObserverCenter",
		"attachSessionLocked",
		"detachSessionLocked",
		"attachTrustedObserverLocked",
		"detachTrustedObserverLocked",
		"setTrustedObserverCenterLocked",
		"appliedTrustedObserverCenterLocked",
		"endpointReader",
		"translateClientMessage",
		"enqueueIncoming",
		"drainIncoming",
		"drainTrustedObserverCenter",
		"sortedSessionIDsLocked",
	}
	for _, name := range wantSessionFile {
		if !sessionDeclarations[name] {
			t.Errorf("session.go must own session lifecycle declaration %s", name)
		}
		if serverDeclarations[name] {
			t.Errorf("server.go must not own session lifecycle declaration %s", name)
		}
	}
}

func topLevelDeclarationNames(t *testing.T, path string) map[string]bool {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("解析 %s: %v", path, err)
	}
	names := make(map[string]bool)
	for _, declaration := range parsed.Decls {
		switch declaration := declaration.(type) {
		case *ast.FuncDecl:
			names[declaration.Name.Name] = true
		case *ast.GenDecl:
			for _, specification := range declaration.Specs {
				switch specification := specification.(type) {
				case *ast.TypeSpec:
					names[specification.Name.Name] = true
				case *ast.ValueSpec:
					for _, name := range specification.Names {
						names[name.Name] = true
					}
				}
			}
		}
	}
	return names
}

// allowed 列出每个内部包允许直接依赖的内部包。
var allowed = map[string][]string{
	"internal/archcheck":  {},
	"internal/core":       {},
	"internal/physics":    {"internal/core"},
	"internal/gfx":        {},
	"internal/logging":    {},
	"internal/gfx/shader": {},
	"internal/network":    {"internal/core"},
	"internal/profile":    {"internal/core"},
	"internal/sim":        {"internal/core", "internal/physics", "internal/world"},
	"internal/storage":    {"internal/core", "internal/world"},
	"internal/world":      {"internal/core"},
	"internal/worldgen":   {"internal/core", "internal/world"},
	"internal/mesh":       {"internal/core", "internal/world"},
	"internal/assets": {
		"internal/core", "internal/world", "internal/mesh", "internal/worldgen", "internal/gfx",
	},
	"internal/render": {
		"internal/core", "internal/world", "internal/mesh", "internal/assets", "internal/gfx",
	},
	"internal/server": {
		"internal/core", "internal/network", "internal/world",
		"internal/worldgen", "internal/sim", "internal/storage",
	},
	"internal/client": {
		"internal/core", "internal/physics", "internal/network", "internal/world", "internal/mesh", "internal/assets",
		"internal/render", "internal/gfx",
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
			for _, forbidden := range []string{
				"minecraft-go/internal/client", "minecraft-go/internal/render", "minecraft-go/internal/gfx",
				"github.com/go-gl/glfw", "github.com/oliverbestmann/webgpu",
				"golang.org/x/image", "golang.org/x/image/font",
			} {
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
		for _, forbidden := range []string{
			"minecraft-go/internal/client", "minecraft-go/internal/render", "minecraft-go/internal/gfx",
			"github.com/go-gl/glfw", "github.com/oliverbestmann/webgpu",
			"golang.org/x/image", "golang.org/x/image/font",
		} {
			if dependency == forbidden || strings.HasPrefix(dependency, forbidden+"/") {
				t.Errorf("mcgod transitively depends on forbidden graphics package %s", dependency)
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
