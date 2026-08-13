package archcheck_test

import (
	"bytes"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const (
	modulePath                       = "github.com/channing771/mornlea"
	expectedLegacyIdentityAllowances = 41
	expectedLegacyIdentityMatches    = 45
)

var (
	legacyDataDirectory  = identityToken("minecraft", "-go")
	legacyBackupIdentity = identityToken(".mc", "go-world-backup-v1.json")
	currentIdentityRoots = []string{
		"go.mod",
		"cmd",
		"internal",
		"engine/Cargo.toml",
		"engine/Cargo.lock",
		"engine/crates",
		"engine/include",
		"Makefile",
		".github/workflows/ci.yml",
		".codex/hooks.json",
		"scripts/agent-hooks",
		".gitignore",
	}
	forbiddenCurrentIdentity = []string{
		identityToken("module minecraft", "-go"),
		identityToken(`"minecraft`, `-go/internal/`),
		identityToken("github.com/channing771/minecraft", "-go"),
		identityToken("cmd/mc", "go"),
		identityToken("cmd/mc", "god"),
		identityToken("bin/mc", "go"),
		identityToken("mc", "go_mesh"),
		identityToken("libmc", "go_mesh"),
		identityToken("mc", "go_engine"),
		identityToken("MC", "GO_ENGINE_"),
		identityToken("MC", "GO_STATUS_"),
		identityToken("MINECRAFT", "_GO_"),
		identityToken("MC", "GOD_"),
	}
	legacyIdentityPattern = regexp.MustCompile(identityToken("(?i)(?:minecraft[-_]", "go|mc", "go)"))
)

func identityToken(parts ...string) string {
	return strings.Join(parts, "")
}

type legacyIdentityAllowance struct {
	path     string
	literal  string
	owner    string
	expected int
}

var legacyIdentityAllowances = []legacyIdentityAllowance{
	{"internal/config/config.go", legacyDataDirectory, "defaultPaths", 1},
	{"internal/config/migration_test.go", legacyDataDirectory, "TestLoadDefaultUsesMornleaCurrentAndMinecraftGoLegacy", 1},
	{"internal/config/migration_test.go", legacyDataDirectory, "TestLoadDefaultPrefersExistingMornleaConfig", 1},
	{"internal/config/migration_test.go", legacyDataDirectory, "TestLoadDefaultMigratesLegacyConfigAndPreservesSource", 1},
	{"internal/config/migration_test.go", legacyDataDirectory, "TestLoadDefaultRejectsInvalidAuthoritativeConfig", 1},
	{"internal/config/migration_test.go", legacyDataDirectory, "TestLoadDefaultRejectsInvalidLegacyConfigWithoutCreatingCurrent", 1},
	{"internal/config/migration_test.go", legacyDataDirectory, "TestLoadDefaultMissingBothReturnsDefaultsWithoutCreatingFile", 1},
	{"internal/config/migration_test.go", legacyDataDirectory, "TestLoadDefaultRejectsUnsafeParentPermissions", 1},
	{"internal/config/migration_test.go", legacyDataDirectory, "TestLoadDefaultRejectsUnsafeTargetPermissions", 1},
	{"internal/config/migration_test.go", legacyDataDirectory, "TestLoadDefaultRejectsSymlinkTarget", 1},
	{"internal/config/migration_test.go", legacyDataDirectory, "TestLoadDefaultRejectsSameInodeSymlinkInsertedBeforeOpen", 1},
	{"internal/config/migration_test.go", legacyDataDirectory, "TestLoadDefaultRejectsTargetReplacedAfterPathValidation", 1},
	{"internal/config/migration_test.go", legacyDataDirectory, "TestLoadDefaultReadsConcurrentWinnerWithoutOverwritingIt", 1},
	{"internal/config/migration_test.go", legacyDataDirectory, "TestLoadDefaultRejectsUnsafeConcurrentWinnerPermissions", 1},
	{"internal/config/migration_test.go", legacyDataDirectory, "TestLoadDefaultPublishFailurePreservesLegacyAndCleansTemporary", 1},
	{"internal/config/migration_test.go", legacyDataDirectory, "TestLoadDefaultDirectorySyncFailureIncludesTargetAndDoesNotLog", 1},
	{"internal/config/migration_test.go", legacyDataDirectory, "TestLoadDefaultLogsOnlySuccessfulMigrationPublisher", 3},
	{"internal/profile/profile.go", legacyDataDirectory, "defaultPaths", 1},
	{"internal/profile/profile_test.go", legacyDataDirectory, "TestLoadOrCreateRejectsInsecureParentBeforeRenaming", 1},
	{"internal/profile/profile_test.go", legacyDataDirectory, "TestLoadOrCreateKeepsIDWhenNameChanges", 1},
	{"internal/profile/profile_test.go", legacyDataDirectory, "TestLoadOrCreateDefaultPrefersExistingMornleaProfile", 1},
	{"internal/profile/profile_test.go", legacyDataDirectory, "TestLoadOrCreateDefaultMigratesLegacyProfileExactly", 1},
	{"internal/profile/profile_test.go", legacyDataDirectory, "TestLoadOrCreateDefaultMigrationAppliesRequestedName", 1},
	{"internal/profile/profile_test.go", legacyDataDirectory, "TestLoadOrCreateDefaultRejectsInvalidAuthoritativeProfile", 1},
	{"internal/profile/profile_test.go", legacyDataDirectory, "TestLoadOrCreateCustomPathSkipsDefaultMigration", 1},
	{"internal/profile/profile_test.go", legacyDataDirectory, "TestLoadOrCreateDefaultMissingBothCreatesSingleUUIDv4", 1},
	{"internal/profile/profile_test.go", legacyDataDirectory, "TestLoadOrCreateDefaultConcurrentCreationReturnsSingleWinner", 1},
	{"internal/profile/profile_test.go", legacyDataDirectory, "TestLoadOrCreateDefaultReadsConcurrentMigrationWinner", 1},
	{"internal/profile/profile_test.go", legacyDataDirectory, "TestLoadOrCreateDefaultPublishFailureDoesNotGenerateIdentity", 1},
	{"internal/profile/profile_test.go", legacyDataDirectory, "TestLoadOrCreateDefaultRejectsUnsafeParentPermissions", 1},
	{"internal/profile/profile_test.go", legacyDataDirectory, "TestLoadOrCreateDefaultRejectsUnsafeTargetPermissions", 1},
	{"internal/profile/profile_test.go", legacyDataDirectory, "TestLoadOrCreateDefaultRejectsTargetReplacedAfterPathValidation", 1},
	{"internal/profile/profile_test.go", legacyDataDirectory, "TestLoadOrCreateDefaultRejectsSameInodeSymlinkInsertedBeforeOpen", 1},
	{"internal/profile/profile_test.go", legacyDataDirectory, "TestLoadOrCreateDefaultLogsOnlySuccessfulMigrationPublisher", 3},
	{"cmd/mornlea/run_test.go", legacyDataDirectory, "legacyDataPath", 1},
	{"cmd/mornlea-server/main_test.go", legacyDataDirectory, "legacyConfigPath", 1},
	{"internal/storage/backup.go", legacyBackupIdentity, "backupIdentityName", 1},
	{"internal/storage/backup_test.go", legacyBackupIdentity, "TestWorldBackupCopiesCompleteWorldAndReusesMatchingBackup", 1},
	{"internal/storage/backup_test.go", legacyBackupIdentity, "TestWorldBackupRejectsEveryMismatchedIdentityField", 1},
	{"internal/storage/backup_test.go", legacyBackupIdentity, "TestWorldBackupRejectsOversizedIdentity", 1},
	{"internal/storage/backup_test.go", legacyBackupIdentity, "readWorldBackupIdentity", 1},
}

type sourceStringLiteral struct {
	value string
	owner string
	start int
	end   int
}

type goIdentityFile struct {
	relative string
	source   []byte
	parsed   *ast.File
}

type goIdentityScanner struct {
	fileSet  *token.FileSet
	packages map[string][]goIdentityFile
}

func newGoIdentityScanner() *goIdentityScanner {
	return &goIdentityScanner{
		fileSet:  token.NewFileSet(),
		packages: make(map[string][]goIdentityFile),
	}
}

func TestMornleaCurrentIdentity(t *testing.T) {
	if root := os.Getenv("MORNLEA_IDENTITY_TEST_ROOT"); root != "" {
		actual := make([]int, len(legacyIdentityAllowances))
		goScanner := newGoIdentityScanner()
		scanCurrentIdentityRoot(t, root, "cmd", actual, goScanner)
		goScanner.scanConstants(t, actual)
		return
	}

	root := moduleRoot(t)
	goModule, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("读取 go.mod: %v", err)
	}
	if !strings.HasPrefix(string(goModule), "module "+modulePath+"\n") {
		t.Errorf("go.mod module 必须是 %s", modulePath)
	}

	actual := make([]int, len(legacyIdentityAllowances))
	goScanner := newGoIdentityScanner()
	if len(legacyIdentityAllowances) != expectedLegacyIdentityAllowances {
		t.Fatalf("旧数据身份 allowlist tuple 数 = %d，期望 %d", len(legacyIdentityAllowances), expectedLegacyIdentityAllowances)
	}
	expectedMatches := 0
	for index, allowance := range legacyIdentityAllowances {
		if allowance.expected <= 0 {
			t.Fatalf("allowlist[%d] 的 expected 必须为正数", index)
		}
		expectedMatches += allowance.expected
	}
	if expectedMatches != expectedLegacyIdentityMatches {
		t.Fatalf("旧数据身份 allowlist match 总数 = %d，期望 %d", expectedMatches, expectedLegacyIdentityMatches)
	}
	for _, relative := range currentIdentityRoots {
		scanCurrentIdentityRoot(t, root, filepath.FromSlash(relative), actual, goScanner)
	}
	goScanner.scanConstants(t, actual)
	for index, allowance := range legacyIdentityAllowances {
		if actual[index] != allowance.expected {
			t.Errorf("旧数据身份 allowlist 计数错误：%s %s.%s = %d，期望 %d", allowance.path, allowance.literal, allowance.owner, actual[index], allowance.expected)
		}
	}
	testCurrentIdentityMutations(t)
}

func testCurrentIdentityMutations(t *testing.T) {
	t.Helper()
	mutations := map[string]func(*testing.T, string){
		"command path": func(t *testing.T, root string) {
			writeIdentityMutationFile(t, root, filepath.Join("cmd", identityToken("mc", "go"), "main.go"), "package main\n")
		},
		"symlink path": func(t *testing.T, root string) {
			writeIdentityMutationFile(t, root, filepath.Join("cmd", "target"), "package main\n")
			if err := os.Symlink("target", filepath.Join(root, "cmd", identityToken("mc", "go-wrapper"))); err != nil {
				t.Fatal(err)
			}
		},
		"escaped literal": func(t *testing.T, root string) {
			writeIdentityMutationFile(t, root, filepath.Join("cmd", "escaped.go"), `package sample
const artifact = "\x6d\x63\x67\x6f_mesh"
`)
		},
		"escaped import": func(t *testing.T, root string) {
			writeIdentityMutationFile(t, root, filepath.Join("cmd", "escaped_import.go"), `package sample
import _ "github.com/channing771/minecraft\x2dgo/internal/core"
`)
		},
		"binary expression": func(t *testing.T, root string) {
			writeIdentityMutationFile(t, root, filepath.Join("cmd", "binary.go"), `package sample
const artifact = "mc" + "go_mesh"
`)
		},
		"constant identifier chain": func(t *testing.T, root string) {
			writeIdentityMutationFile(t, root, filepath.Join("cmd", "chain.go"), `package sample
const prefix = "mc"
const identity = prefix + "go"
const artifact = identity + "_mesh"
`)
		},
		"cross-file constant identifier chain": func(t *testing.T, root string) {
			writeIdentityMutationFile(t, root, filepath.Join("cmd", "prefix.go"), `package sample
const prefix = "mc"
`)
			writeIdentityMutationFile(t, root, filepath.Join("cmd", "artifact.go"), `package sample
const artifact = prefix + "go_mesh"
`)
		},
		"invalid Go source": func(t *testing.T, root string) {
			writeIdentityMutationFile(t, root, filepath.Join("cmd", "invalid.go"), "package sample\nfunc (")
		},
	}
	for name, setup := range mutations {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			setup(t, root)
			command := exec.Command(os.Args[0], "-test.run=^TestMornleaCurrentIdentity$")
			command.Env = append(os.Environ(), "MORNLEA_IDENTITY_TEST_ROOT="+root)
			if output, err := command.CombinedOutput(); err == nil {
				t.Errorf("identity guard 接受了 mutation\n%s", output)
			}
		})
	}
}

func writeIdentityMutationFile(t *testing.T, root, relative, source string) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
}

func scanCurrentIdentityRoot(t *testing.T, root, relative string, actual []int, goScanner *goIdentityScanner) {
	t.Helper()
	path := filepath.Join(root, relative)
	scanCurrentIdentityPath(t, relative)
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("读取身份扫描根 %s: %v", relative, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return
	}
	if !info.IsDir() {
		scanCurrentIdentityFile(t, root, path, actual, goScanner)
		return
	}

	files := 0
	err = filepath.WalkDir(path, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		entryRelative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		scanCurrentIdentityPath(t, filepath.ToSlash(entryRelative))
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		files++
		scanCurrentIdentityFile(t, root, path, actual, goScanner)
		return nil
	})
	if err != nil {
		t.Fatalf("扫描身份根 %s: %v", relative, err)
	}
	if files == 0 {
		t.Fatalf("身份扫描根 %s 没有普通文件", relative)
	}
}

func scanCurrentIdentityPath(t *testing.T, relative string) {
	t.Helper()
	for _, forbidden := range forbiddenCurrentIdentity {
		if strings.Contains(relative, forbidden) {
			t.Errorf("路径 %s 包含禁止的当前技术身份 %q", relative, forbidden)
		}
	}
	if match := legacyIdentityPattern.FindString(relative); match != "" {
		t.Errorf("路径 %s 包含未获允许的旧身份 %q", relative, match)
	}
}

func scanCurrentIdentityFile(t *testing.T, root, path string, actual []int, goScanner *goIdentityScanner) {
	t.Helper()
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s: %v", path, err)
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatalf("计算 %s 相对路径: %v", path, err)
	}
	relative = filepath.ToSlash(relative)
	var (
		parsed   *ast.File
		literals []sourceStringLiteral
	)
	if filepath.Ext(path) == ".go" {
		parsed, literals = parseSourceStringLiterals(t, goScanner.fileSet, path, source)
		scanGoImports(t, relative, goScanner.fileSet, parsed)
		key := filepath.Dir(relative) + "\x00" + parsed.Name.Name
		goScanner.packages[key] = append(goScanner.packages[key], goIdentityFile{
			relative: relative,
			source:   source,
			parsed:   parsed,
		})
	}

	for _, forbidden := range forbiddenCurrentIdentity {
		if bytes.Contains(source, []byte(forbidden)) {
			t.Errorf("%s 包含禁止的当前技术身份 %q", relative, forbidden)
		}
	}
	matches := legacyIdentityPattern.FindAllIndex(source, -1)
	for _, match := range matches {
		consumed := false
		for index, allowance := range legacyIdentityAllowances {
			if allowance.path != relative {
				continue
			}
			for _, literal := range literals {
				if literal.value == allowance.literal && literal.owner == allowance.owner && match[0] >= literal.start && match[1] <= literal.end {
					actual[index]++
					consumed = true
					break
				}
			}
			if consumed {
				break
			}
		}
		if !consumed {
			line := bytes.Count(source[:match[0]], []byte{'\n'}) + 1
			t.Errorf("%s:%d 包含未获允许的旧身份 %q", relative, line, source[match[0]:match[1]])
		}
	}
}

func parseSourceStringLiterals(t *testing.T, fileSet *token.FileSet, path string, source []byte) (*ast.File, []sourceStringLiteral) {
	t.Helper()
	parsed, err := parser.ParseFile(fileSet, path, source, 0)
	if err != nil {
		t.Fatalf("解析 %s: %v", path, err)
	}
	tokenFile := fileSet.File(parsed.Pos())
	var literals []sourceStringLiteral
	appendLiterals := func(node ast.Node, owner string) {
		ast.Inspect(node, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatalf("解析 %s 字符串 literal: %v", path, err)
			}
			literals = append(literals, sourceStringLiteral{
				value: value,
				owner: owner,
				start: tokenFile.Offset(literal.Pos()),
				end:   tokenFile.Offset(literal.End()),
			})
			return true
		})
	}
	for _, declaration := range parsed.Decls {
		switch declaration := declaration.(type) {
		case *ast.FuncDecl:
			appendLiterals(declaration, declaration.Name.Name)
		case *ast.GenDecl:
			for _, specification := range declaration.Specs {
				value, ok := specification.(*ast.ValueSpec)
				if !ok || len(value.Names) == 0 {
					continue
				}
				appendLiterals(value, value.Names[0].Name)
			}
		}
	}
	return parsed, literals
}

func scanGoImports(t *testing.T, relative string, fileSet *token.FileSet, parsed *ast.File) {
	t.Helper()
	for _, specification := range parsed.Imports {
		path, err := strconv.Unquote(specification.Path.Value)
		if err != nil {
			t.Fatalf("解析 %s import path: %v", relative, err)
		}
		if containsLegacyIdentity(path) {
			t.Errorf("%s:%d 的 import path 包含未获允许的旧身份 %q", relative, fileSet.Position(specification.Path.Pos()).Line, path)
		}
	}
}

func (scanner *goIdentityScanner) scanConstants(t *testing.T, actual []int) {
	t.Helper()
	for _, files := range scanner.packages {
		parsed := make([]*ast.File, 0, len(files))
		for _, file := range files {
			parsed = append(parsed, file.parsed)
		}
		typeInfo := &types.Info{Types: make(map[ast.Expr]types.TypeAndValue)}
		configuration := types.Config{Error: func(error) {}}
		_, _ = configuration.Check(parsed[0].Name.Name, scanner.fileSet, parsed, typeInfo)
		for _, file := range files {
			scanGoConstantIdentities(t, file, scanner.fileSet, typeInfo, actual)
		}
	}
}

func scanGoConstantIdentities(t *testing.T, file goIdentityFile, fileSet *token.FileSet, typeInfo *types.Info, actual []int) {
	t.Helper()
	relative, source, parsed := file.relative, file.source, file.parsed
	definitionExpressions := make(map[ast.Expr]bool)
	ast.Inspect(parsed, func(node ast.Node) bool {
		value, ok := node.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for _, expression := range value.Values {
			definitionExpressions[expression] = true
		}
		return true
	})

	inspect := func(node ast.Node, owner string) {
		ast.Inspect(node, func(node ast.Node) bool {
			expression, ok := node.(ast.Expr)
			if !ok {
				return true
			}
			typeAndValue, ok := typeInfo.Types[expression]
			if !ok || typeAndValue.Value == nil || typeAndValue.Value.Kind() != constant.String {
				return true
			}
			value := constant.StringVal(typeAndValue.Value)
			if !containsLegacyIdentity(value) {
				return true
			}
			tokenFile := fileSet.File(expression.Pos())
			start := tokenFile.Offset(expression.Pos())
			end := tokenFile.Offset(expression.End())
			if legacyIdentityPattern.Match(source[start:end]) {
				return true
			}
			if _, isIdentifier := expression.(*ast.Ident); isIdentifier && !definitionExpressions[expression] {
				return true
			}
			if literal, ok := expression.(*ast.BasicLit); ok && literal.Kind == token.STRING {
				for index, allowance := range legacyIdentityAllowances {
					if allowance.path == relative && allowance.literal == value && allowance.owner == owner {
						actual[index]++
						return true
					}
				}
			}
			t.Errorf("%s:%d 的常量值包含未获允许的旧身份 %q", relative, fileSet.Position(expression.Pos()).Line, value)
			return true
		})
	}
	for _, declaration := range parsed.Decls {
		switch declaration := declaration.(type) {
		case *ast.FuncDecl:
			inspect(declaration, declaration.Name.Name)
		case *ast.GenDecl:
			for _, specification := range declaration.Specs {
				value, ok := specification.(*ast.ValueSpec)
				if !ok || len(value.Names) == 0 {
					continue
				}
				inspect(value, value.Names[0].Name)
			}
		}
	}
}

func containsLegacyIdentity(value string) bool {
	for _, forbidden := range forbiddenCurrentIdentity {
		if strings.Contains(value, forbidden) {
			return true
		}
	}
	return legacyIdentityPattern.MatchString(value)
}
