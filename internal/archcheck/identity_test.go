package archcheck_test

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const (
	modulePath           = "github.com/channing771/mornlea"
	legacyDataDirectory  = "minecraft" + "-go"
	legacyBackupIdentity = ".mc" + "go-world-backup-v1.json"
)

var (
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
		"module minecraft" + "-go",
		`"minecraft` + `-go/internal/`,
		"github.com/channing771/minecraft" + "-go",
		"cmd/mc" + "go",
		"cmd/mc" + "god",
		"bin/mc" + "go",
		"mc" + "go_mesh",
		"libmc" + "go_mesh",
		"mc" + "go_engine",
		"MC" + "GO_ENGINE_",
		"MC" + "GO_STATUS_",
		"MINECRAFT" + "_GO_",
		"MC" + "GOD_",
	}
	legacyIdentityPattern = regexp.MustCompile("(?i)(?:minecraft[-_]" + "go|mc" + "go)")
)

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
	{"internal/profile/profile_test.go", legacyDataDirectory, "TestLoadOrCreateCreatesPrivateV1Profile", 1},
	{"internal/profile/profile_test.go", legacyDataDirectory, "TestLoadOrCreateUsesDefaultNameWhenCreatingWithoutRequest", 1},
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

func TestMornleaCurrentIdentity(t *testing.T) {
	root := moduleRoot(t)
	goModule, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("读取 go.mod: %v", err)
	}
	if !strings.HasPrefix(string(goModule), "module "+modulePath+"\n") {
		t.Errorf("go.mod module 必须是 %s", modulePath)
	}

	actual := make([]int, len(legacyIdentityAllowances))
	for index, allowance := range legacyIdentityAllowances {
		if allowance.expected <= 0 {
			t.Fatalf("allowlist[%d] 的 expected 必须为正数", index)
		}
	}
	for _, relative := range currentIdentityRoots {
		scanCurrentIdentityRoot(t, root, filepath.FromSlash(relative), actual)
	}
	for index, allowance := range legacyIdentityAllowances {
		if actual[index] != allowance.expected {
			t.Errorf("旧数据身份 allowlist 计数错误：%s %s.%s = %d，期望 %d", allowance.path, allowance.literal, allowance.owner, actual[index], allowance.expected)
		}
	}
}

func scanCurrentIdentityRoot(t *testing.T, root, relative string, actual []int) {
	t.Helper()
	path := filepath.Join(root, relative)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("读取身份扫描根 %s: %v", relative, err)
	}
	if !info.IsDir() {
		scanCurrentIdentityFile(t, root, path, actual)
		return
	}

	files := 0
	err = filepath.WalkDir(path, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
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
		scanCurrentIdentityFile(t, root, path, actual)
		return nil
	})
	if err != nil {
		t.Fatalf("扫描身份根 %s: %v", relative, err)
	}
	if files == 0 {
		t.Fatalf("身份扫描根 %s 没有普通文件", relative)
	}
}

func scanCurrentIdentityFile(t *testing.T, root, path string, actual []int) {
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

	for _, forbidden := range forbiddenCurrentIdentity {
		if bytes.Contains(source, []byte(forbidden)) {
			t.Errorf("%s 包含禁止的当前技术身份 %q", relative, forbidden)
		}
	}
	matches := legacyIdentityPattern.FindAllIndex(source, -1)
	if len(matches) == 0 {
		return
	}
	literals := parseSourceStringLiterals(t, path, source)
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

func parseSourceStringLiterals(t *testing.T, path string, source []byte) []sourceStringLiteral {
	t.Helper()
	if filepath.Ext(path) != ".go" {
		return nil
	}
	fileSet := token.NewFileSet()
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
	return literals
}
