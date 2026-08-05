package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"minecraft-go/internal/core"
	"minecraft-go/internal/storage"
)

// openRestartWorld 打开一个磁盘世界并装配一个可 Step 的权威服务端。
func openRestartWorld(t *testing.T, root string, seed int64) (*Server, *storage.DiskStore) {
	t.Helper()
	store, err := storage.OpenDisk(context.Background(), root, storage.OpenOptions{Create: storage.Metadata{
		FormatVersion: 2, Seed: seed, SpawnDimension: core.Overworld,
	}})
	if err != nil {
		t.Fatalf("OpenDisk: %v", err)
	}
	config := DefaultConfig(seed)
	config.ViewRadius = 0
	config.Workers = 1
	config.SaveWorkers = 1
	running := NewWorld(config, playerTestGenerator{}, store)
	return running, store
}

func TestDiskWorldTimeContinuesAcrossRestart(t *testing.T) {
	root := t.TempDir()
	const seed int64 = 4242

	first, _ := openRestartWorld(t, root, seed)
	for range 7 {
		first.StepForTest()
	}
	want := first.engine.WorldTime() + 1 // 关服屏障会再走一个最终 tick
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := first.Shutdown(ctx); err != nil {
		t.Fatalf("首次关服：%v", err)
	}

	second, store := openRestartWorld(t, root, seed)
	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := second.Shutdown(shutdownCtx); err != nil {
			t.Errorf("清理关服：%v", err)
		}
	})
	if got := store.Metadata().WorldTimeTicks; got != want {
		t.Fatalf("重开后磁盘世界时间 = %d，想要 %d", got, want)
	}
	if got := second.engine.WorldTime(); got != want {
		t.Fatalf("重开后引擎世界时间 = %d，想要 %d", got, want)
	}
	// 首份权威状态从恢复值继续推进，而不是回到默认相位。
	if got := second.StepForTest().WorldTimeTicks; got != want+1 {
		t.Fatalf("重开后首个 tick 世界时间 = %d，想要 %d", got, want+1)
	}
}

func TestDiskLegacyMetadataStartsAtZeroAndUpgradesOnShutdown(t *testing.T) {
	root := t.TempDir()
	const seed int64 = -9

	// 先用当前程序建立世界，再把 world.meta 换成等价的 v1 字节。
	seedStore, err := storage.OpenDisk(context.Background(), root, storage.OpenOptions{Create: storage.Metadata{
		FormatVersion: 2, Seed: seed, SpawnDimension: core.Overworld,
	}})
	if err != nil {
		t.Fatalf("OpenDisk seed: %v", err)
	}
	if err := seedStore.Close(); err != nil {
		t.Fatalf("关闭种子 store：%v", err)
	}
	path := filepath.Join(root, "world.meta")
	if err := os.WriteFile(path, legacyMetadataBytesForTest(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	running, store := openRestartWorld(t, root, seed)
	if got := store.Metadata().WorldTimeTicks; got != 0 {
		t.Fatalf("v1 世界初始时间 = %d，想要 0", got)
	}
	if got := store.Metadata().Seed; got != seed {
		t.Fatalf("v1 世界种子 = %d，想要 %d", got, seed)
	}
	// 打开本身不得改写磁盘上的 v1 文件。
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if version := binary.LittleEndian.Uint32(onDisk[4:8]); version != 1 {
		t.Fatalf("打开后磁盘 metadata 版本 = %d，想要仍为 1", version)
	}

	for range 3 {
		running.StepForTest()
	}
	want := running.engine.WorldTime() + 1
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := running.Shutdown(ctx); err != nil {
		t.Fatalf("关服：%v", err)
	}

	onDisk, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if version := binary.LittleEndian.Uint32(onDisk[4:8]); version != 2 {
		t.Fatalf("关服后磁盘 metadata 版本 = %d，想要 2", version)
	}

	reopened, reopenedStore := openRestartWorld(t, root, seed)
	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := reopened.Shutdown(shutdownCtx); err != nil {
			t.Errorf("清理关服：%v", err)
		}
	})
	if got := reopenedStore.Metadata().WorldTimeTicks; got != want {
		t.Fatalf("迁移后重开世界时间 = %d，想要 %d", got, want)
	}
}

// legacyMetadataBytesForTest 构造一份 CRC 有效的 metadata v1 文件字节。
// 生产代码只写 v2，v1 样本必须由测试自己保留。
func legacyMetadataBytesForTest(seed int64) []byte {
	encoded := make([]byte, 0, 36)
	encoded = append(encoded, 'M', 'C', 'G', 'M')
	encoded = binary.LittleEndian.AppendUint32(encoded, 1)  // FormatVersion
	encoded = binary.LittleEndian.AppendUint32(encoded, 20) // payload 长度
	encoded = binary.LittleEndian.AppendUint64(encoded, uint64(seed))
	encoded = binary.LittleEndian.AppendUint32(encoded, uint32(core.Overworld))
	encoded = binary.LittleEndian.AppendUint32(encoded, 0) // SpawnAnchor.X
	encoded = binary.LittleEndian.AppendUint32(encoded, 0) // SpawnAnchor.Z
	return binary.LittleEndian.AppendUint32(
		encoded, crc32.Checksum(encoded, crc32.MakeTable(crc32.Castagnoli)),
	)
}

func TestDiskAutosaveMigratesLegacyMetadataToV2(t *testing.T) {
	root := t.TempDir()
	const seed int64 = 77

	seedStore, err := storage.OpenDisk(context.Background(), root, storage.OpenOptions{Create: storage.Metadata{
		FormatVersion: 2, Seed: seed, SpawnDimension: core.Overworld,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := seedStore.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "world.meta")
	if err := os.WriteFile(path, legacyMetadataBytesForTest(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	running, store := openRestartWorld(t, root, seed)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := running.Shutdown(ctx); err != nil {
			t.Errorf("清理关服：%v", err)
		}
	})
	running.config.AutosaveTicks = 4

	// 不经关服，仅靠自动保存边界就必须把 v1 升级为 v2。
	deadline := time.Now().Add(5 * time.Second)
	for {
		running.StepForTest()
		onDisk, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if binary.LittleEndian.Uint32(onDisk[4:8]) == 2 {
			decoded := store.Metadata()
			if decoded.Seed != seed {
				t.Fatalf("迁移后种子 = %d，想要 %d", decoded.Seed, seed)
			}
			if decoded.WorldTimeTicks == 0 {
				t.Fatal("自动保存写出的世界时间仍为 0")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("自动保存没有把 metadata 升级为 v2")
		}
	}
}

func TestDiskMetadataSaveFailureKeepsOldFileAndFailsShutdown(t *testing.T) {
	root := t.TempDir()
	const seed int64 = 1234

	store, err := storage.OpenDisk(context.Background(), root, storage.OpenOptions{Create: storage.Metadata{
		FormatVersion: 2, Seed: seed, SpawnDimension: core.Overworld,
	}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "world.meta")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	injected := errors.New("injected metadata save failure")
	failing := &failingMetadataStore{DiskStore: store}
	failing.fail.Store(&injected)

	config := DefaultConfig(seed)
	config.ViewRadius = 0
	config.Workers = 1
	config.SaveWorkers = 1
	running := NewWorld(config, playerTestGenerator{}, failing)
	for range 3 {
		running.StepForTest()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := running.Shutdown(ctx); !errors.Is(err, injected) {
		t.Fatalf("关服错误 = %v，想要注入的 metadata 失败", err)
	}

	// 失败的原子替换必须保留完整旧文件，且世界仍归本进程所有。
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("失败的 metadata 保存改写了磁盘上的旧文件")
	}
	matches, err := filepath.Glob(filepath.Join(root, ".world.meta.tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("失败后残留临时文件 %v", matches)
	}

	// 修复后重试关服必须成功并写出最终时间。
	failing.fail.Store(nil)
	want := running.engine.WorldTime()
	if err := running.Shutdown(ctx); err != nil {
		t.Fatalf("重试关服：%v", err)
	}
	reopened, reopenedStore := openRestartWorld(t, root, seed)
	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := reopened.Shutdown(shutdownCtx); err != nil {
			t.Errorf("清理关服：%v", err)
		}
	})
	if got := reopenedStore.Metadata().WorldTimeTicks; got != want {
		t.Fatalf("重试关服后世界时间 = %d，想要 %d", got, want)
	}
}

// failingMetadataStore 只在 SaveMetadata 上注入可开关的失败，
// 其余行为完全委托给真实的 DiskStore。
type failingMetadataStore struct {
	*storage.DiskStore
	fail atomic.Pointer[error]
}

func (store *failingMetadataStore) SaveMetadata(
	ctx context.Context,
	metadata storage.Metadata,
) error {
	if injected := store.fail.Load(); injected != nil {
		return *injected
	}
	return store.DiskStore.SaveMetadata(ctx, metadata)
}
