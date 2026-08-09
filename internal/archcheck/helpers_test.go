package archcheck_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode"
)

func TestProductionGoSourceScansSplitFiles(t *testing.T) {
	dir := t.TempDir()
	for name, source := range map[string]string{
		"first.go":        "package sample\nfunc firstMarker() {}\n",
		"second.go":       "package sample\nfunc secondMarker() {}\n",
		"ignored_test.go": "package sample\nfunc ignoredMarker() {}\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got := productionGoSource(t, dir)
	if !strings.Contains(got, "firstMarker") || !strings.Contains(got, "secondMarker") {
		t.Fatalf("production source missed split file: %q", got)
	}
	if strings.Contains(got, "ignoredMarker") {
		t.Fatalf("production source included test file: %q", got)
	}
}

func TestTopLevelDeclarationNamesInScansSplitFiles(t *testing.T) {
	dir := t.TempDir()
	for name, source := range map[string]string{
		"session.go":              "package sample\nconst sessionMarker = 1\n",
		"session_reader.go":       "package sample\ntype sessionReaderMarker struct{}\n",
		"session_ignored_test.go": "package sample\nfunc sessionIgnoredMarker() {}\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	names := topLevelDeclarationNamesIn(t, dir, "session*.go")
	for _, name := range []string{"sessionMarker", "sessionReaderMarker"} {
		if !names[name] {
			t.Errorf("split production files missed declaration %s", name)
		}
	}
	if names["sessionIgnoredMarker"] {
		t.Errorf("test file declaration must be ignored")
	}
}

func productionGoSource(t *testing.T, directory string) string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("读取 %s: %v", directory, err)
	}
	slices.SortFunc(entries, func(left, right os.DirEntry) int {
		return strings.Compare(left.Name(), right.Name())
	})
	var source strings.Builder
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatalf("读取 %s: %v", name, err)
		}
		source.Write(data)
	}
	return source.String()
}

func topLevelDeclarationNamesIn(t *testing.T, directory, pattern string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("读取 %s: %v", directory, err)
	}
	slices.SortFunc(entries, func(left, right os.DirEntry) int {
		return strings.Compare(left.Name(), right.Name())
	})
	names := make(map[string]bool)
	for _, entry := range entries {
		name := entry.Name()
		matches, err := filepath.Match(pattern, name)
		if err != nil {
			t.Fatalf("匹配 %s: %v", pattern, err)
		}
		if entry.IsDir() || !matches || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(directory, name)
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatalf("解析 %s: %v", path, err)
		}
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
	}
	return names
}

// isTunableDefaultName 判断标识符是否形如 defaultXxx（default 后紧跟大写字母）。
func isTunableDefaultName(name string) bool {
	const prefix = "default"
	rest, ok := strings.CutPrefix(name, prefix)
	if !ok || rest == "" {
		return false
	}
	return unicode.IsUpper(rune(rest[0]))
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
