package storage

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"minecraft-go/internal/core"
)

func TestWorldBackupCopiesCompleteWorldAndReusesMatchingBackup(t *testing.T) {
	store, source, destination := newWorldBackupFixture(t)

	temporaryPaths := []string{
		filepath.Join(source, ".world.meta.tmp-ignore"),
		filepath.Join(source, "players", ".player.tmp-ignore"),
		filepath.Join(source, "dimensions", ".scan.tmp-ignore", "entry"),
	}
	for _, path := range temporaryPaths {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("创建临时条目目录失败: %v", err)
		}
		if err := os.WriteFile(path, []byte("temporary"), 0o600); err != nil {
			t.Fatalf("写入临时条目失败: %v", err)
		}
	}

	if err := store.Backup(context.Background(), destination); err != nil {
		t.Fatalf("创建世界备份失败: %v", err)
	}

	for _, relativePath := range []string{
		"world.meta",
		filepath.Join("players", "player-one.player"),
		filepath.Join("dimensions", "0", "regions", "r.-2.1.region"),
	} {
		assertSameFileContents(t, filepath.Join(source, relativePath), filepath.Join(destination, relativePath))
	}
	for _, relativePath := range []string{
		"world.lock",
		".world.meta.tmp-ignore",
		filepath.Join("players", ".player.tmp-ignore"),
		filepath.Join("dimensions", ".scan.tmp-ignore"),
	} {
		if _, err := os.Lstat(filepath.Join(destination, relativePath)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("备份不应包含 %q，Lstat 错误: %v", relativePath, err)
		}
	}

	identityData, err := os.ReadFile(filepath.Join(destination, ".mcgo-world-backup-v1.json"))
	if err != nil {
		t.Fatalf("读取备份身份失败: %v", err)
	}
	var identity struct {
		Source           string `json:"source"`
		Seed             int64  `json:"seed"`
		MigrationVersion int    `json:"migration_version"`
	}
	if err := json.Unmarshal(identityData, &identity); err != nil {
		t.Fatalf("解析备份身份失败: %v", err)
	}
	absoluteSource, err := filepath.Abs(source)
	if err != nil {
		t.Fatalf("规范源路径失败: %v", err)
	}
	wantIdentity := struct {
		Source           string `json:"source"`
		Seed             int64  `json:"seed"`
		MigrationVersion int    `json:"migration_version"`
	}{
		Source:           absoluteSource,
		Seed:             42,
		MigrationVersion: 1,
	}
	if identity != wantIdentity {
		t.Fatalf("备份身份 = %+v，期望 %+v", identity, wantIdentity)
	}

	if err := store.Backup(context.Background(), destination); err != nil {
		t.Fatalf("复用身份匹配的世界备份失败: %v", err)
	}
}

func TestWorldBackupRejectsDestinationInsideSource(t *testing.T) {
	store, source, _ := newWorldBackupFixture(t)
	destination := filepath.Join(source, "backup")

	if err := store.Backup(context.Background(), destination); err == nil {
		t.Fatal("目标位于源世界内部时 Backup() 未返回错误")
	}
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("拒绝内部目标后目标不应存在，Lstat 错误: %v", err)
	}
}

func TestWorldBackupRejectsDestinationAliasedInsideSource(t *testing.T) {
	store, source, _ := newWorldBackupFixture(t)
	alias := filepath.Join(filepath.Dir(source), "world-alias")
	if err := os.Symlink(source, alias); err != nil {
		t.Skipf("当前文件系统不支持符号链接: %v", err)
	}
	destination := filepath.Join(alias, "backup")

	if err := store.Backup(context.Background(), destination); err == nil {
		t.Fatal("目标经符号链接指向源世界内部时 Backup() 未返回错误")
	}
	if _, err := os.Lstat(filepath.Join(source, "backup")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("拒绝路径别名后目标不应存在，Lstat 错误: %v", err)
	}
}

func TestWorldBackupRejectsSymlink(t *testing.T) {
	store, source, destination := newWorldBackupFixture(t)
	target := filepath.Join(filepath.Dir(source), "outside")
	if err := os.WriteFile(target, []byte("outside"), 0o600); err != nil {
		t.Fatalf("写入符号链接目标失败: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(source, "players", "linked.player")); err != nil {
		t.Skipf("当前文件系统不支持符号链接: %v", err)
	}

	if err := store.Backup(context.Background(), destination); err == nil {
		t.Fatal("源世界含符号链接时 Backup() 未返回错误")
	}
	assertWorldBackupAbsent(t, destination)
	if got, err := os.ReadFile(target); err != nil || string(got) != "outside" {
		t.Fatalf("符号链接目标被改变: data=%q err=%v", got, err)
	}
}

func TestWorldBackupRejectsExistingOrdinaryDirectory(t *testing.T) {
	store, _, destination := newWorldBackupFixture(t)
	if err := os.Mkdir(destination, 0o755); err != nil {
		t.Fatalf("创建既有目标目录失败: %v", err)
	}
	bystander := filepath.Join(destination, "keep")
	if err := os.WriteFile(bystander, []byte("keep"), 0o600); err != nil {
		t.Fatalf("写入既有目标文件失败: %v", err)
	}

	if err := store.Backup(context.Background(), destination); err == nil {
		t.Fatal("既有普通目录缺少匹配身份时 Backup() 未返回错误")
	}
	if got, err := os.ReadFile(bystander); err != nil || string(got) != "keep" {
		t.Fatalf("既有目标目录被改变: data=%q err=%v", got, err)
	}
}

func TestWorldBackupCancellationLeavesSourceUnchanged(t *testing.T) {
	store, source, destination := newWorldBackupFixture(t)
	before := snapshotWorldBackupSource(t, source)
	bystander := filepath.Join(filepath.Dir(destination), ".backup.tmp-keep")
	if err := os.Mkdir(bystander, 0o755); err != nil {
		t.Fatalf("创建旁观临时目录失败: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.Backup(ctx, destination); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消 Backup() 错误 = %v，期望 context.Canceled", err)
	}

	after := snapshotWorldBackupSource(t, source)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("取消备份改变了源世界\nBefore: %#v\nAfter:  %#v", before, after)
	}
	assertWorldBackupAbsent(t, destination)
	if _, err := os.Stat(bystander); err != nil {
		t.Fatalf("备份清理删除了非本次创建的临时目录: %v", err)
	}
}

func TestWorldBackupCopyFailureLeavesSourceUnchanged(t *testing.T) {
	store, source, destination := newWorldBackupFixture(t)
	blocked := filepath.Join(source, "players", "blocked.player")
	if err := os.WriteFile(blocked, []byte("blocked"), 0o600); err != nil {
		t.Fatalf("写入受限文件失败: %v", err)
	}
	before := snapshotWorldBackupSource(t, source)
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatalf("限制源文件权限失败: %v", err)
	}

	err := store.Backup(context.Background(), destination)
	if restoreErr := os.Chmod(blocked, 0o600); restoreErr != nil {
		t.Fatalf("恢复源文件权限失败: %v", restoreErr)
	}
	if err == nil {
		t.Fatal("复制不可读文件时 Backup() 未返回错误")
	}

	after := snapshotWorldBackupSource(t, source)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("复制失败改变了源世界\nBefore: %#v\nAfter:  %#v", before, after)
	}
	assertWorldBackupAbsent(t, destination)
}

func newWorldBackupFixture(t *testing.T) (*DiskStore, string, string) {
	t.Helper()
	base := t.TempDir()
	source := filepath.Join(base, "world")
	destination := filepath.Join(base, "backup")
	store, err := OpenDisk(context.Background(), source, OpenOptions{
		Create: Metadata{FormatVersion: currentMetadataVersion, Seed: 42},
	})
	if err != nil {
		t.Fatalf("打开磁盘世界失败: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("关闭磁盘世界失败: %v", err)
		}
	})
	key := core.ChunkKey{Dimension: 0, Pos: core.ChunkPos{X: -33, Z: 34}}
	if _, err := store.SaveBatch(context.Background(), diskSavesFor([]core.ChunkKey{key}, 1)); err != nil {
		t.Fatalf("保存区块夹具失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "players", "player-one.player"), []byte("player-data"), 0o600); err != nil {
		t.Fatalf("写入玩家夹具失败: %v", err)
	}
	return store, source, destination
}

func assertSameFileContents(t *testing.T, source, destination string) {
	t.Helper()
	want, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("读取源文件 %q 失败: %v", source, err)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("读取备份文件 %q 失败: %v", destination, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("备份文件 %q 内容 = %q，期望 %q", destination, got, want)
	}
}

type worldBackupSnapshot struct {
	Mode fs.FileMode
	Data string
}

func snapshotWorldBackupSource(t *testing.T, root string) map[string]worldBackupSnapshot {
	t.Helper()
	snapshot := make(map[string]worldBackupSnapshot)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relativePath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		item := worldBackupSnapshot{Mode: info.Mode()}
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			item.Data = string(data)
		}
		snapshot[relativePath] = item
		return nil
	})
	if err != nil {
		t.Fatalf("快照源世界失败: %v", err)
	}
	return snapshot
}

func assertWorldBackupAbsent(t *testing.T, destination string) {
	t.Helper()
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("失败后正式备份目标不应存在，Lstat 错误: %v", err)
	}
}
