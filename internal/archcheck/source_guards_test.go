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

// TestTunableConstantsAreNotExported 守住"可调参数只能经快照读取"这条不变量。
//
// 若某个可调参数同时以导出常量存在，任何一处漏改都会让编译期值与快照值并存：
// 例如相机读到编译期 EyeHeight、服务端射线读到快照值，玩家瞄准的方块与服务端
// 判定的方块就不是同一个，而且不会有任何报错。
func TestTunableConstantsAreNotExported(t *testing.T) {
	forbidden := map[string][]string{
		filepath.Join("internal", "physics"): {
			"EyeHeight", "StepHeight", "WalkSpeed", "GroundAcceleration",
			"GroundDeceleration", "AirAcceleration", "JumpSpeed", "Gravity",
			"TerminalFallSpeed",
		},
		filepath.Join("internal", "sim"): {
			"RegenDelayTicks", "RegenIntervalTicks", "DropPickupDelayTicks",
			"PlayerDropPickupDelayTicks", "DropLifetimeTicks",
			"InteractionReach", "DropPickupRange", "SpawnRadius",
		},
	}
	root := moduleRoot(t)
	for packageDirectory, names := range forbidden {
		files, err := filepath.Glob(filepath.Join(root, packageDirectory, "*.go"))
		if err != nil {
			t.Fatalf("枚举 %s: %v", packageDirectory, err)
		}
		// filepath.Glob 对不存在的目录返回 (nil, nil)：包一旦改名或移动，
		// 这条守卫就会静默变成空循环并永远通过，因此必须显式要求扫到文件。
		if len(files) == 0 {
			t.Fatalf("%s 下没有 Go 源文件：包被改名或移动后本守卫会静默失效", packageDirectory)
		}
		banned := make(map[string]bool, len(names))
		for _, name := range names {
			banned[name] = true
		}
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				t.Fatalf("解析 %s: %v", path, err)
			}
			for _, declaration := range parsed.Decls {
				general, ok := declaration.(*ast.GenDecl)
				if !ok || (general.Tok != token.CONST && general.Tok != token.VAR) {
					continue
				}
				for _, specification := range general.Specs {
					value, ok := specification.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for _, name := range value.Names {
						if banned[name.Name] {
							t.Errorf("%s: 可调参数 %s 仍以导出常量暴露，唯一入口必须是 Tunables 快照", path, name.Name)
						}
					}
				}
			}
		}
	}
}

// TestTunableDefaultsAreOnlyReadInTunablesFile 守住"可调参数的唯一读取入口是
// Tunables 快照"这条不变量的另一半。
//
// TestTunableConstantsAreNotExported 只看声明是否导出，对"某个生产读取点直接
// 读了未导出的 defaultXxx"完全失明——评审实测过：把 internal/sim 的
// InteractionReach、DropLifetimeTicks、SpawnRadius、RegenDelay/IntervalTicks
// 与 internal/physics 的 StepHeight 读取点逐一改回 defaultXxx，既有测试全绿。
// 那种状态下配置文件与调试面板改不动这些参数，而且不会有任何报错，正是设计
// §3.4 要防的静默错位。
//
// 因此这里额外要求：除去常量声明本身与各包的 tunables.go（DefaultTunables 在
// 那里组装默认快照），任何非测试文件都不得再出现 defaultXxx 标识符。
func TestTunableDefaultsAreOnlyReadInTunablesFile(t *testing.T) {
	root := moduleRoot(t)
	for _, packageDirectory := range []string{
		filepath.Join("internal", "physics"),
		filepath.Join("internal", "sim"),
	} {
		files, err := filepath.Glob(filepath.Join(root, packageDirectory, "*.go"))
		if err != nil {
			t.Fatalf("枚举 %s: %v", packageDirectory, err)
		}
		if len(files) == 0 {
			t.Fatalf("%s 下没有 Go 源文件：包被改名或移动后本守卫会静默失效", packageDirectory)
		}
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") || filepath.Base(path) == "tunables.go" {
				continue
			}
			fileSet := token.NewFileSet()
			parsed, err := parser.ParseFile(fileSet, path, nil, 0)
			if err != nil {
				t.Fatalf("解析 %s: %v", path, err)
			}
			declarationNames := make(map[*ast.Ident]bool)
			for _, declaration := range parsed.Decls {
				general, ok := declaration.(*ast.GenDecl)
				if !ok {
					continue
				}
				for _, specification := range general.Specs {
					if value, ok := specification.(*ast.ValueSpec); ok {
						for _, name := range value.Names {
							declarationNames[name] = true
						}
					}
				}
			}
			ast.Inspect(parsed, func(node ast.Node) bool {
				identifier, ok := node.(*ast.Ident)
				if !ok || declarationNames[identifier] || !isTunableDefaultName(identifier.Name) {
					return true
				}
				t.Errorf("%s: 读取了编译期默认值 %s。可调参数的唯一读取入口必须是 Tunables 快照"+
					"（physics 用 Step 入口的快照，sim 用 engine.tunables）；直接读 default* 会让"+
					"该处永远停在编译期默认值，配置文件与调试面板改不动它，而且不会有任何报错。"+
					"default* 只允许出现在自己的声明处与 tunables.go 的 DefaultTunables 中。",
					fileSet.Position(identifier.Pos()), identifier.Name)
				return true
			})
		}
	}
}

// TestOnlyCommandsImportConfig 守住"自动化验证不读用户配置"这条不变量。
func TestOnlyCommandsImportConfig(t *testing.T) {
	cmd := exec.Command("go", "list", "-f", "{{.ImportPath}}|{{join .Imports \" \"}} {{join .TestImports \" \"}} {{join .XTestImports \" \"}}", "./internal/...")
	cmd.Dir = moduleRoot(t)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "|", 2)
		// internal/config 的外部测试包（package config_test）导入自身，需要整体跳过，
		// 否则会自触发；这条豁免只对 config 包本身生效。
		if len(parts) != 2 || parts[0] == "minecraft-go/internal/config" {
			continue
		}
		for _, imported := range strings.Fields(parts[1]) {
			if imported == "minecraft-go/internal/config" {
				t.Errorf("%s 导入了 internal/config；只有 cmd 可以导入它，否则本机配置会污染性能基线与抓帧 golden", parts[0])
			}
		}
	}
}

func TestOnlyTCPImplementationImportsNet(t *testing.T) {
	root := filepath.Join(moduleRoot(t), "internal", "network")
	files, err := filepath.Glob(filepath.Join(root, "*.go"))
	if err != nil {
		t.Fatalf("枚举 network Go 文件: %v", err)
	}
	// 同 TestTunableConstantsAreNotExported：Glob 对不存在的目录静默返回空。
	if len(files) == 0 {
		t.Fatalf("%s 下没有 Go 源文件：包被改名或移动后本守卫会静默失效", root)
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
		"SetViewCenter": {}, "BreakRay": {}, "PlaceRay": {}, "CommandBreakRay": {}, "CommandPlaceRay": {}, "localSessionID": {},
	}
	for _, sourceRoot := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, sourceRoot), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || filepath.Ext(path) != ".go" || path == filepath.Join(root, "internal", "archcheck", "source_guards_test.go") {
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
		})
		if err != nil {
			t.Fatalf("扫描 %s Go 源码: %v", sourceRoot, err)
		}
	}
}

func TestMcgoUsesLoginStreamsInsteadOfAttachedServerEndpoints(t *testing.T) {
	path := filepath.Join(moduleRoot(t), "cmd", "mcgo")
	source := productionGoSource(t, path)
	for _, legacy := range []string{"server.NewEmbedded(", "server.NewEmbeddedMemory(", "server.New("} {
		if strings.Contains(source, legacy) {
			t.Errorf("%s retains legacy attached-server constructor %s", path, legacy)
		}
	}
	if !strings.Contains(source, "network.NewMemoryStreamPair") || !strings.Contains(source, "network.LoginClient") {
		t.Errorf("%s must assemble local connections through a stream login", path)
	}
}

func TestMcgoBenchmarkTCPPathUsesTheSharedLoginStateMachine(t *testing.T) {
	path := filepath.Join(moduleRoot(t), "cmd", "mcgo")
	source := productionGoSource(t, path)
	for _, required := range []string{"network.ListenTCP(", "network.BeginServerLogin(", "network.LoginClient(", "running.AttachTrustedObserver"} {
		if !strings.Contains(source, required) {
			t.Errorf("%s benchmark TCP path must contain %s", path, required)
		}
	}
}

func TestServerProductionDoesNotDeclareLegacyAttachedWorldWrappers(t *testing.T) {
	root := filepath.Join(moduleRoot(t), "internal", "server")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("读取 %s: %v", root, err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(root, name)
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

func TestSessionLifecycleResponsibilitiesStayInSessionFiles(t *testing.T) {
	root := filepath.Join(moduleRoot(t), "internal", "server")
	sessionDeclarations := topLevelDeclarationNamesIn(t, root, "session*.go")
	serverDeclarations := topLevelDeclarationNamesIn(t, root, "server.go")
	wantSessionFile := []string{
		"inputCapacity", "trustedObserverSessionID", "SessionSpec", "SessionExit", "incomingCommand", "trustedObserverCenter", "appliedTrustedObserverCenter",
		"attachSessionLocked", "detachSessionLocked", "attachTrustedObserverLocked", "detachTrustedObserverLocked", "setTrustedObserverCenterLocked", "appliedTrustedObserverCenterLocked",
		"endpointReader", "translateClientMessage", "enqueueIncoming", "drainIncoming", "drainTrustedObserverCenter", "sortedSessionIDsLocked",
	}
	for _, name := range wantSessionFile {
		if !sessionDeclarations[name] {
			t.Errorf("session*.go must own session lifecycle declaration %s", name)
		}
		if serverDeclarations[name] {
			t.Errorf("server.go must not own session lifecycle declaration %s", name)
		}
	}
}
