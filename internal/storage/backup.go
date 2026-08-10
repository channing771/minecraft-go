package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	backupIdentityName     = ".mcgo-world-backup-v1.json"
	backupMigrationVersion = 1
)

type backupIdentity struct {
	Source           string `json:"source"`
	Seed             int64  `json:"seed"`
	MigrationVersion int    `json:"migration_version"`
}

type backupDirectory struct {
	path string
	mode fs.FileMode
}

// Backup 在当前世界锁和存储锁内创建可验证的完整目录备份。
func (store *DiskStore) Backup(ctx context.Context, destination string) error {
	if store.closing.Load() {
		return os.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closing.Load() || store.closed {
		return os.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	source, err := filepath.Abs(store.files.root)
	if err != nil {
		return fmt.Errorf("resolve world path %q: %w", store.files.root, err)
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("resolve backup path %q: %w", destination, err)
	}
	comparisonSource, err := filepath.EvalSymlinks(source)
	if err != nil {
		return fmt.Errorf("resolve world path aliases %q: %w", source, err)
	}
	comparisonParent, err := filepath.EvalSymlinks(filepath.Dir(destination))
	if err != nil {
		return fmt.Errorf("resolve backup parent aliases %q: %w", destination, err)
	}
	comparisonDestination := filepath.Join(comparisonParent, filepath.Base(destination))
	inside, err := pathInside(comparisonSource, comparisonDestination)
	if err != nil {
		return fmt.Errorf("compare world and backup paths: %w", err)
	}
	if inside {
		return fmt.Errorf("backup path %q is inside world %q", destination, source)
	}

	identity := backupIdentity{
		Source:           source,
		Seed:             store.files.metadata.Seed,
		MigrationVersion: backupMigrationVersion,
	}
	if exists, err := matchingBackup(destination, identity); err != nil {
		return err
	} else if exists {
		return nil
	}

	temporary, err := os.MkdirTemp(filepath.Dir(destination), "."+filepath.Base(destination)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary backup beside %q: %w", destination, err)
	}
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.RemoveAll(temporary)
		}
	}()

	directories, err := copyWorldBackup(ctx, source, temporary)
	if err != nil {
		return err
	}
	identityData, err := json.Marshal(identity)
	if err != nil {
		return fmt.Errorf("encode backup identity: %w", err)
	}
	identityData = append(identityData, '\n')
	if err := writeBackupFile(filepath.Join(temporary, backupIdentityName), identityData, 0o600); err != nil {
		return fmt.Errorf("write backup identity: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for index := len(directories) - 1; index >= 0; index-- {
		directory := directories[index]
		if err := os.Chmod(directory.path, directory.mode.Perm()); err != nil {
			return fmt.Errorf("set backup directory mode %q: %w", directory.path, err)
		}
		if err := syncDirectory(directory.path); err != nil {
			return fmt.Errorf("sync backup directory %q: %w", directory.path, err)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("backup destination %q appeared while copying", destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect backup destination %q: %w", destination, err)
	}
	if err := os.Rename(temporary, destination); err != nil {
		return fmt.Errorf("publish backup %q: %w", destination, err)
	}
	removeTemporary = false
	if err := syncDirectory(filepath.Dir(destination)); err != nil {
		return fmt.Errorf("sync backup parent for %q: %w", destination, err)
	}
	return nil
}

func pathInside(parent, candidate string) (bool, error) {
	relative, err := filepath.Rel(parent, candidate)
	if err != nil {
		return false, err
	}
	return relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)), nil
}

func matchingBackup(destination string, want backupIdentity) (bool, error) {
	info, err := os.Lstat(destination)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect backup destination %q: %w", destination, err)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("backup destination %q is not a real directory", destination)
	}

	identityPath := filepath.Join(destination, backupIdentityName)
	identityInfo, err := os.Lstat(identityPath)
	if err != nil {
		return false, fmt.Errorf("inspect backup identity %q: %w", identityPath, err)
	}
	if !identityInfo.Mode().IsRegular() {
		return false, fmt.Errorf("backup identity %q is not a regular file", identityPath)
	}
	data, err := os.ReadFile(identityPath)
	if err != nil {
		return false, fmt.Errorf("read backup identity %q: %w", identityPath, err)
	}
	var got backupIdentity
	if err := json.Unmarshal(data, &got); err != nil {
		return false, fmt.Errorf("decode backup identity %q: %w", identityPath, err)
	}
	if got != want {
		return false, fmt.Errorf("backup destination %q belongs to another world or migration", destination)
	}
	return true, nil
}

func copyWorldBackup(ctx context.Context, source, destination string) ([]backupDirectory, error) {
	directories := make([]backupDirectory, 0, 8)
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("world entry %q is a symlink", path)
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "world.lock" {
			return nil
		}
		matchedTemporary, err := filepath.Match(".*.tmp-*", entry.Name())
		if err != nil {
			return err
		}
		if relative != "." && matchedTemporary {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}
		target := destination
		if relative != "." {
			target = filepath.Join(destination, relative)
		}
		if info.IsDir() {
			if relative != "." {
				if err := os.Mkdir(target, 0o700); err != nil {
					return err
				}
			}
			directories = append(directories, backupDirectory{path: target, mode: info.Mode()})
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("world entry %q is not a regular file or directory", path)
		}
		if err := copyBackupFile(ctx, path, target, info.Mode()); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("copy world %q: %w", source, err)
	}
	return directories, nil
}

func copyBackupFile(ctx context.Context, source, destination string, mode fs.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open source file %q: %w", source, err)
	}
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
	if err != nil {
		_ = input.Close()
		return fmt.Errorf("create backup file %q: %w", destination, err)
	}
	_, copyErr := io.Copy(output, contextReader{ctx: ctx, reader: input})
	var syncErr error
	if copyErr == nil {
		syncErr = output.Sync()
	}
	closeErr := errors.Join(output.Close(), input.Close())
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		return fmt.Errorf("copy %q to %q: %w", source, destination, err)
	}
	if err := os.Chmod(destination, mode.Perm()); err != nil {
		return fmt.Errorf("set backup file mode %q: %w", destination, err)
	}
	return nil
}

func writeBackupFile(path string, data []byte, mode fs.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	var syncErr error
	if writeErr == nil {
		syncErr = file.Sync()
	}
	return errors.Join(writeErr, syncErr, file.Close())
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader contextReader) Read(data []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(data)
}
