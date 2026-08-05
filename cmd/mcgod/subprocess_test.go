package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"minecraft-go/internal/network"
	"minecraft-go/internal/server"
	"minecraft-go/internal/storage"
)

func TestMCGodProcess(t *testing.T) {
	if os.Getenv("MCGOD_PROCESS") != "1" {
		return
	}
	args := os.Args
	for index, arg := range args {
		if arg == "--" {
			args = args[index+1:]
			break
		}
	}
	if os.Getenv("MCGOD_PROCESS_FAIL_SAVE") == "1" {
		err := run(context.Background(), args, dependencies{
			openDisk: func(context.Context, string, storage.OpenOptions) (storage.WorldStore, error) {
				return storage.NewMemory(storage.Metadata{FormatVersion: 2}), nil
			},
			listenTCP: func(string) (network.Listener, error) {
				return mcgodTestListener{}, nil
			},
			newHost: func(server.Config, server.Generator, storage.WorldStore) mcgodHost {
				return &mcgodTestHost{runErr: errors.New("save failed")}
			},
		})
		if err == nil {
			os.Exit(0)
		}
		os.Exit(1)
	}
	if err := runSignal(args); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestMCGodProcessReleasesWorldLockAfterSIGTERM(t *testing.T) {
	world := filepath.Join(t.TempDir(), "world")
	command := mcgodProcessCommand(t, world)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_, _ = command.Process.Wait()
		}
	}()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		store, err := storage.OpenDisk(context.Background(), world, storage.OpenOptions{})
		if errors.Is(err, storage.ErrWorldLocked) {
			break
		}
		if err == nil {
			_ = store.Close()
		}
		if time.Now().After(deadline) {
			t.Fatalf("server never acquired world lock: %v", err)
		}
	}
	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("SIGTERM process exit=%v, want zero", err)
	}
	store, err := storage.OpenDisk(context.Background(), world, storage.OpenOptions{})
	if err != nil {
		t.Fatalf("world lock remained after SIGTERM: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMCGodProcessSaveFailureExitsNonzero(t *testing.T) {
	command := mcgodProcessCommand(t, filepath.Join(t.TempDir(), "world"))
	command.Env = append(command.Env, "MCGOD_PROCESS_FAIL_SAVE=1")
	err := command.Run()
	if err == nil {
		t.Fatal("save failure helper exited zero")
	}
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() == 0 {
		t.Fatalf("save failure exit=%v, want nonzero process exit", err)
	}
}

func mcgodProcessCommand(t *testing.T, world string) *exec.Cmd {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestMCGodProcess$", "--", "--listen", "127.0.0.1:0", "--world", world)
	command.Env = append(os.Environ(), "MCGOD_PROCESS=1")
	command.Stderr = os.Stderr
	command.Stdout = os.Stdout
	return command
}
