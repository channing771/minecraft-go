package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
)

type OpenOptions struct {
	Create Metadata
}

type worldFiles struct {
	root     string
	metadata Metadata
	lock     *flock.Flock
}

func openWorldFiles(ctx context.Context, root string, options OpenOptions) (*worldFiles, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create world directory %q: %w", root, err)
	}

	guard := flock.New(filepath.Join(root, "world.lock"))
	locked, err := guard.TryLock()
	if err != nil {
		return nil, fmt.Errorf("lock world %q: %w", root, err)
	}
	if !locked {
		return nil, fmt.Errorf("%w: %s", ErrWorldLocked, root)
	}
	release := true
	defer func() {
		if release {
			_ = guard.Unlock()
		}
	}()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ensurePlayersDirectory(filepath.Join(root, "players")); err != nil {
		return nil, fmt.Errorf("prepare players directory: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	metadataPath := filepath.Join(root, "world.meta")
	encoded, err := os.ReadFile(metadataPath)
	if err == nil {
		metadata, decodeErr := decodeMetadata(encoded)
		if decodeErr != nil {
			return nil, fmt.Errorf("read world metadata %q: %w", metadataPath, decodeErr)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		release = false
		return &worldFiles{root: root, metadata: metadata, lock: guard}, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read world metadata %q: %w", metadataPath, err)
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	encoded, err = encodeMetadata(options.Create)
	if err != nil {
		return nil, fmt.Errorf("encode world metadata: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := replaceFileAtomically(
		metadataPath, ".world.meta.tmp-*", encoded, 0o600,
	); err != nil {
		return nil, fmt.Errorf("create world metadata %q: %w", metadataPath, err)
	}

	release = false
	return &worldFiles{root: root, metadata: options.Create, lock: guard}, nil
}

func ensurePlayersDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(path, 0o755); err != nil {
			return fmt.Errorf("create %q: %w", path, err)
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return fmt.Errorf("inspect %q: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%q is not a real directory", path)
	}
	return nil
}

func (files *worldFiles) close() error {
	if files == nil || files.lock == nil {
		return nil
	}
	if err := files.lock.Unlock(); err != nil {
		return fmt.Errorf("unlock world %q: %w", files.root, err)
	}
	files.lock = nil
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
