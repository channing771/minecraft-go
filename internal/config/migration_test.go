package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestLoadDefaultUsesMornleaCurrentAndMinecraftGoLegacy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir: %v", err)
	}
	wantCurrent := filepath.Join(base, "mornlea", "config.json")
	wantLegacy := filepath.Join(base, "minecraft-go", "config.json")
	current, legacy, err := defaultPaths()
	if err != nil {
		t.Fatalf("defaultPaths: %v", err)
	}
	if current != wantCurrent || legacy != wantLegacy {
		t.Fatalf("defaultPaths = (%q, %q)，want (%q, %q)", current, legacy, wantCurrent, wantLegacy)
	}

	want := migrationConfig(24)
	writeMigrationFile(t, current, canonicalConfig(t, want), 0o700, 0o600)
	got, err := LoadDefault()
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LoadDefault = %+v，want %+v", got, want)
	}

	publicPath, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	if publicPath != wantLegacy {
		t.Fatalf("Task 4 的 DefaultPath = %q，want 旧路径 %q", publicPath, wantLegacy)
	}
}

func TestLoadDefaultPrefersExistingMornleaConfig(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "mornlea", "config.json")
	legacy := filepath.Join(root, "minecraft-go", "config.json")
	want := migrationConfig(24)
	writeMigrationFile(t, current, canonicalConfig(t, want), 0o700, 0o600)
	writeMigrationFile(t, legacy, []byte(`{"version":`), 0o700, 0o600)

	got, err := loadDefaultFromPaths(current, legacy, publishConfigExclusively)
	if err != nil {
		t.Fatalf("loadDefaultFromPaths: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("优先读取的新配置 = %+v，want %+v", got, want)
	}
}

func TestLoadDefaultMigratesLegacyConfigAndPreservesSource(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "mornlea", "config.json")
	legacy := filepath.Join(root, "minecraft-go", "config.json")
	want := migrationConfig(24)
	legacyBody := []byte(`{"version":1,"physics":{"gravity":24}}`)
	writeMigrationFile(t, legacy, legacyBody, 0o700, 0o600)

	got, err := loadDefaultFromPaths(current, legacy, publishConfigExclusively)
	if err != nil {
		t.Fatalf("loadDefaultFromPaths: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("迁移后配置 = %+v，want %+v", got, want)
	}
	assertFileBytes(t, current, canonicalConfig(t, want))
	assertFileMode(t, current, 0o600)
	assertFileMode(t, filepath.Dir(current), 0o700)
	assertFileBytes(t, legacy, legacyBody)
	assertNoMigrationTemps(t, filepath.Dir(current))
}

func TestLoadDefaultRejectsInvalidAuthoritativeConfig(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "mornlea", "config.json")
	legacy := filepath.Join(root, "minecraft-go", "config.json")
	currentBody := []byte(`{"version":`)
	legacyBody := canonicalConfig(t, migrationConfig(24))
	writeMigrationFile(t, current, currentBody, 0o700, 0o600)
	writeMigrationFile(t, legacy, legacyBody, 0o700, 0o600)

	_, err := loadDefaultFromPaths(current, legacy, publishConfigExclusively)
	if err == nil || !strings.Contains(err.Error(), current) {
		t.Fatalf("非法权威配置错误 = %v，必须包含新路径 %q", err, current)
	}
	assertFileBytes(t, current, currentBody)
	assertFileBytes(t, legacy, legacyBody)
	assertNoMigrationTemps(t, filepath.Dir(current))
}

func TestLoadDefaultRejectsInvalidLegacyConfigWithoutCreatingCurrent(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "mornlea", "config.json")
	legacy := filepath.Join(root, "minecraft-go", "config.json")
	legacyBody := []byte(`{"version":`)
	writeMigrationFile(t, legacy, legacyBody, 0o700, 0o600)

	_, err := loadDefaultFromPaths(current, legacy, publishConfigExclusively)
	if err == nil || !strings.Contains(err.Error(), legacy) {
		t.Fatalf("非法旧配置错误 = %v，必须包含旧路径 %q", err, legacy)
	}
	assertFileBytes(t, legacy, legacyBody)
	assertPathMissing(t, current)
	assertNoMigrationTemps(t, filepath.Dir(current))
}

func TestLoadDefaultMissingBothReturnsDefaultsWithoutCreatingFile(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "mornlea", "config.json")
	legacy := filepath.Join(root, "minecraft-go", "config.json")

	got, err := loadDefaultFromPaths(current, legacy, publishConfigExclusively)
	if err != nil {
		t.Fatalf("loadDefaultFromPaths: %v", err)
	}
	if !reflect.DeepEqual(got, Defaults()) {
		t.Fatalf("两边缺失时配置 = %+v，want Defaults", got)
	}
	assertPathMissing(t, current)
	assertPathMissing(t, filepath.Dir(current))
}

func TestLoadDefaultRejectsUnsafeParentPermissions(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "mornlea", "config.json")
	legacy := filepath.Join(root, "minecraft-go", "config.json")
	legacyBody := canonicalConfig(t, migrationConfig(24))
	if err := os.MkdirAll(filepath.Dir(current), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.Chmod(filepath.Dir(current), 0o755); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	writeMigrationFile(t, legacy, legacyBody, 0o700, 0o600)

	called := false
	_, err := loadDefaultFromPaths(current, legacy, func(string, []byte) (bool, error) {
		called = true
		return false, nil
	})
	if !errors.Is(err, fs.ErrPermission) || !strings.Contains(err.Error(), current) {
		t.Fatalf("不安全父目录错误 = %v，必须包含新路径并匹配 fs.ErrPermission", err)
	}
	if called {
		t.Fatal("不安全父目录不得进入发布流程")
	}
	assertFileMode(t, filepath.Dir(current), 0o755)
	assertFileBytes(t, legacy, legacyBody)
	assertPathMissing(t, current)
	assertNoMigrationTemps(t, filepath.Dir(current))
}

func TestLoadDefaultRejectsUnsafeTargetPermissions(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "mornlea", "config.json")
	legacy := filepath.Join(root, "minecraft-go", "config.json")
	currentBody := canonicalConfig(t, migrationConfig(24))
	writeMigrationFile(t, current, currentBody, 0o700, 0o644)
	writeMigrationFile(t, legacy, []byte(`{"version":`), 0o700, 0o600)

	_, err := loadDefaultFromPaths(current, legacy, publishConfigExclusively)
	if !errors.Is(err, fs.ErrPermission) || !strings.Contains(err.Error(), current) {
		t.Fatalf("不安全目标错误 = %v，必须包含新路径并匹配 fs.ErrPermission", err)
	}
	assertFileBytes(t, current, currentBody)
	assertFileMode(t, current, 0o644)
	assertNoMigrationTemps(t, filepath.Dir(current))
}

func TestLoadDefaultReadsConcurrentWinnerWithoutOverwritingIt(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "mornlea", "config.json")
	legacy := filepath.Join(root, "minecraft-go", "config.json")
	legacyBody := canonicalConfig(t, migrationConfig(24))
	winner := migrationConfig(31)
	winnerBody := canonicalConfig(t, winner)
	writeMigrationFile(t, legacy, legacyBody, 0o700, 0o600)

	got, err := loadDefaultFromPaths(current, legacy, func(path string, _ []byte) (bool, error) {
		writeMigrationFile(t, path, winnerBody, 0o700, 0o600)
		return false, nil
	})
	if err != nil {
		t.Fatalf("loadDefaultFromPaths: %v", err)
	}
	if !reflect.DeepEqual(got, winner) {
		t.Fatalf("并发 loser 返回 = %+v，want winner %+v", got, winner)
	}
	assertFileBytes(t, current, winnerBody)
	assertFileBytes(t, legacy, legacyBody)
	assertNoMigrationTemps(t, filepath.Dir(current))
}

func TestLoadDefaultRejectsUnsafeConcurrentWinnerPermissions(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "mornlea", "config.json")
	legacy := filepath.Join(root, "minecraft-go", "config.json")
	legacyBody := canonicalConfig(t, migrationConfig(24))
	winnerBody := canonicalConfig(t, migrationConfig(31))
	writeMigrationFile(t, legacy, legacyBody, 0o700, 0o600)

	_, err := loadDefaultFromPaths(current, legacy, func(path string, _ []byte) (bool, error) {
		writeMigrationFile(t, path, winnerBody, 0o700, 0o644)
		return false, nil
	})
	if !errors.Is(err, fs.ErrPermission) || !strings.Contains(err.Error(), current) {
		t.Fatalf("不安全并发赢家错误 = %v，必须包含新路径并匹配 fs.ErrPermission", err)
	}
	assertFileBytes(t, current, winnerBody)
	assertFileMode(t, current, 0o644)
	assertFileBytes(t, legacy, legacyBody)
	assertNoMigrationTemps(t, filepath.Dir(current))
}

func TestLoadDefaultPublishFailurePreservesLegacyAndCleansTemporary(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "mornlea", "config.json")
	legacy := filepath.Join(root, "minecraft-go", "config.json")
	legacyBody := canonicalConfig(t, migrationConfig(24))
	writeMigrationFile(t, legacy, legacyBody, 0o700, 0o600)
	sentinel := errors.New("link failure")

	published, err := publishConfigExclusivelyWithLink(current, legacyBody, func(temp, target string) error {
		if filepath.Dir(temp) != filepath.Dir(current) {
			t.Errorf("临时文件目录 = %q，want %q", filepath.Dir(temp), filepath.Dir(current))
		}
		if target != current {
			t.Errorf("link 目标 = %q，want %q", target, current)
		}
		assertFileBytes(t, temp, legacyBody)
		assertFileMode(t, temp, 0o600)
		return sentinel
	})
	if published || !errors.Is(err, sentinel) || !strings.Contains(err.Error(), current) {
		t.Fatalf("publish = (%v, %v)，want false、目标路径和 sentinel", published, err)
	}
	assertFileBytes(t, legacy, legacyBody)
	assertPathMissing(t, current)
	assertFileMode(t, filepath.Dir(current), 0o700)
	assertNoMigrationTemps(t, filepath.Dir(current))
}

func TestPublishConfigExclusivelyAllowsExactlyOneConcurrentWinner(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "mornlea", "config.json")
	bodies := [][]byte{
		canonicalConfig(t, migrationConfig(24)),
		canonicalConfig(t, migrationConfig(31)),
	}
	type outcome struct {
		published bool
		err       error
		body      []byte
	}
	start := make(chan struct{})
	results := make(chan outcome, len(bodies))
	var ready sync.WaitGroup
	ready.Add(len(bodies))
	for _, body := range bodies {
		body := body
		go func() {
			ready.Done()
			<-start
			published, err := publishConfigExclusively(current, body)
			results <- outcome{published: published, err: err, body: body}
		}()
	}
	ready.Wait()
	close(start)

	publishedCount := 0
	var winnerBody []byte
	for range bodies {
		result := <-results
		if result.err != nil {
			t.Fatalf("publishConfigExclusively: %v", result.err)
		}
		if result.published {
			publishedCount++
			winnerBody = result.body
		}
	}
	if publishedCount != 1 {
		t.Fatalf("published=true 次数 = %d，want 1", publishedCount)
	}
	assertFileBytes(t, current, winnerBody)
	assertFileMode(t, current, 0o600)
	assertFileMode(t, filepath.Dir(current), 0o700)
	assertNoMigrationTemps(t, filepath.Dir(current))
}

func TestLoadDefaultLogsOnlySuccessfulMigrationPublisher(t *testing.T) {
	previous := slog.Default()
	var records bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&records, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	root := t.TempDir()
	current := filepath.Join(root, "publisher", "mornlea", "config.json")
	legacy := filepath.Join(root, "publisher", "minecraft-go", "config.json")
	body := canonicalConfig(t, migrationConfig(24))
	writeMigrationFile(t, legacy, body, 0o700, 0o600)
	if _, err := loadDefaultFromPaths(current, legacy, publishConfigExclusively); err != nil {
		t.Fatalf("publisher loadDefaultFromPaths: %v", err)
	}

	loserCurrent := filepath.Join(root, "loser", "mornlea", "config.json")
	loserLegacy := filepath.Join(root, "loser", "minecraft-go", "config.json")
	writeMigrationFile(t, loserLegacy, body, 0o700, 0o600)
	if _, err := loadDefaultFromPaths(loserCurrent, loserLegacy, func(path string, contents []byte) (bool, error) {
		writeMigrationFile(t, path, contents, 0o700, 0o600)
		return false, nil
	}); err != nil {
		t.Fatalf("loser loadDefaultFromPaths: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(records.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("迁移成功日志条数 = %d，want 1；日志=%q", len(lines), records.String())
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatalf("解析迁移日志: %v", err)
	}
	if record["legacy_path"] != legacy || record["current_path"] != current {
		t.Fatalf("迁移日志路径 = (%v, %v)，want (%q, %q)",
			record["legacy_path"], record["current_path"], legacy, current)
	}
}

func migrationConfig(gravity float32) Config {
	cfg := Defaults()
	cfg.Physics.Gravity = gravity
	return cfg
}

func canonicalConfig(t *testing.T, cfg Config) []byte {
	t.Helper()
	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	return body
}

func writeMigrationFile(t *testing.T, path string, body []byte, dirMode, fileMode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	if err := os.Chmod(filepath.Dir(path), dirMode); err != nil {
		t.Fatalf("Chmod(%s): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, body, fileMode); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
	if err := os.Chmod(path, fileMode); err != nil {
		t.Fatalf("Chmod(%s): %v", path, err)
	}
}

func assertFileBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s 内容 = %q，want %q", path, got, want)
	}
}

func assertFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s): %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s 权限 = %04o，want %04o", path, got, want)
	}
}

func assertPathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("%s 必须不存在，Stat err = %v", path, err)
	}
}

func assertNoMigrationTemps(t *testing.T, directory string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(directory, "config-*.json.tmp"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("遗留配置临时文件: %v", matches)
	}
}
