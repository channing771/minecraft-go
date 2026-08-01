package profile

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type profileDirectory interface {
	Sync() error
	Close() error
}

type atomicWriteHooks struct {
	rename        func(string, string) error
	openDirectory func(string) (profileDirectory, error)
}

func writeProfileAtomically(path string, contents []byte) error {
	return writeProfileAtomicallyWithHooks(path, contents, atomicWriteHooks{
		rename:        os.Rename,
		openDirectory: openProfileDirectory,
	})
}

func writeProfileAtomicallyWithHooks(path string, contents []byte, hooks atomicWriteHooks) error {
	parent := filepath.Dir(path)
	temporary, err := os.CreateTemp(parent, ".profile.tmp-*")
	if err != nil {
		return fmt.Errorf("profile: create temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		_ = temporary.Close()
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()

	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("profile: chmod temporary file: %w", err)
	}
	for remaining := contents; len(remaining) > 0; {
		written, err := temporary.Write(remaining)
		if err != nil {
			return fmt.Errorf("profile: write temporary file: %w", err)
		}
		if written == 0 {
			return fmt.Errorf("profile: write temporary file: %w", io.ErrShortWrite)
		}
		remaining = remaining[written:]
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("profile: sync temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("profile: close temporary file: %w", err)
	}
	if err := hooks.rename(temporaryPath, path); err != nil {
		return fmt.Errorf("profile: replace file: %w", err)
	}
	removeTemporary = false

	directory, err := hooks.openDirectory(parent)
	if err != nil {
		return fmt.Errorf("profile: open parent directory: %w", err)
	}
	if err := errors.Join(directory.Sync(), directory.Close()); err != nil {
		return fmt.Errorf("profile: sync parent directory: %w", err)
	}
	return nil
}

func openProfileDirectory(path string) (profileDirectory, error) {
	return os.Open(path)
}
