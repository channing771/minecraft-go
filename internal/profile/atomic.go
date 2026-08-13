package profile

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

type profileDirectory interface {
	Sync() error
	Close() error
}

type atomicWriteHooks struct {
	publish       func(string, string) (bool, error)
	openDirectory func(string) (profileDirectory, error)
}

func writeProfileAtomically(path string, contents []byte) error {
	_, err := writeProfileAtomicallyWithHooks(path, contents, atomicWriteHooks{
		publish: func(temp, target string) (bool, error) {
			return true, os.Rename(temp, target)
		},
		openDirectory: openProfileDirectory,
	})
	return err
}

func publishProfileExclusively(path string, contents []byte) (bool, error) {
	if err := ensureDefaultProfileParent(path); err != nil {
		return false, err
	}
	return writeProfileAtomicallyWithHooks(path, contents, atomicWriteHooks{
		publish: func(temp, target string) (bool, error) {
			if err := os.Link(temp, target); errors.Is(err, fs.ErrExist) {
				return false, nil
			} else if err != nil {
				return false, err
			}
			return true, nil
		},
		openDirectory: openProfileDirectory,
	})
}

func writeProfileAtomicallyWithHooks(path string, contents []byte, hooks atomicWriteHooks) (bool, error) {
	parent := filepath.Dir(path)
	temporary, err := os.CreateTemp(parent, ".profile.tmp-*")
	if err != nil {
		return false, fmt.Errorf("profile: create temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()

	if err := temporary.Chmod(0o600); err != nil {
		return false, fmt.Errorf("profile: chmod temporary file: %w", err)
	}
	for remaining := contents; len(remaining) > 0; {
		written, err := temporary.Write(remaining)
		if err != nil {
			return false, fmt.Errorf("profile: write temporary file: %w", err)
		}
		if written == 0 {
			return false, fmt.Errorf("profile: write temporary file: %w", io.ErrShortWrite)
		}
		remaining = remaining[written:]
	}
	if err := temporary.Sync(); err != nil {
		return false, fmt.Errorf("profile: sync temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return false, fmt.Errorf("profile: close temporary file: %w", err)
	}
	published, err := hooks.publish(temporaryPath, path)
	if err != nil {
		return false, fmt.Errorf("profile: publish file: %w", err)
	}
	if err := os.Remove(temporaryPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return published, fmt.Errorf("profile: remove temporary file: %w", err)
	}

	directory, err := hooks.openDirectory(parent)
	if err != nil {
		return published, fmt.Errorf("profile: open parent directory: %w", err)
	}
	if err := errors.Join(directory.Sync(), directory.Close()); err != nil {
		return published, fmt.Errorf("profile: sync parent directory: %w", err)
	}
	return published, nil
}

func openProfileDirectory(path string) (profileDirectory, error) {
	return os.Open(path)
}
