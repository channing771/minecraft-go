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

	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/server"
	"github.com/channing771/mornlea/internal/storage"
)

const mornleaServerProcessStartupDeadline = 30 * time.Second

func TestMornleaServerProcess(t *testing.T) {
	if os.Getenv("MORNLEA_SERVER_PROCESS") != "1" {
		return
	}
	args := os.Args
	for index, arg := range args {
		if arg == "--" {
			args = args[index+1:]
			break
		}
	}
	if os.Getenv("MORNLEA_SERVER_PROCESS_FAIL_SAVE") == "1" {
		err := run(context.Background(), args, dependencies{
			openDisk: func(context.Context, string, storage.OpenOptions) (storage.WorldStore, error) {
				return storage.NewMemory(storage.Metadata{FormatVersion: 2}), nil
			},
			listenTCP: func(string) (network.Listener, error) {
				return mornleaServerTestListener{}, nil
			},
			newHost: func(context.Context, server.Config, server.Generator, storage.WorldStore) (mornleaServerHost, error) {
				return &mornleaServerTestHost{runErr: errors.New("save failed")}, nil
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

func TestMornleaServerProcessReleasesWorldLockAfterSIGTERM(t *testing.T) {
	world := filepath.Join(t.TempDir(), "world")
	command := mornleaServerProcessCommand(t, world)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_, _ = command.Process.Wait()
		}
	}()
	metadataPath := filepath.Join(world, "world.meta")
	for deadline := time.Now().Add(mornleaServerProcessStartupDeadline); ; time.Sleep(20 * time.Millisecond) {
		_, err := os.Stat(metadataPath)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("inspect world metadata: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("server never created world metadata: %v", err)
		}
	}
	store, err := storage.OpenDisk(context.Background(), world, storage.OpenOptions{})
	if err == nil {
		_ = store.Close()
		t.Fatal("server did not hold world lock after creating metadata")
	}
	if !errors.Is(err, storage.ErrWorldLocked) {
		t.Fatalf("open running world: %v", err)
	}
	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("SIGTERM process exit=%v, want zero", err)
	}
	store, err = storage.OpenDisk(context.Background(), world, storage.OpenOptions{})
	if err != nil {
		t.Fatalf("world lock remained after SIGTERM: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMornleaServerProcessSaveFailureExitsNonzero(t *testing.T) {
	command := mornleaServerProcessCommand(t, filepath.Join(t.TempDir(), "world"))
	command.Env = append(command.Env, "MORNLEA_SERVER_PROCESS_FAIL_SAVE=1")
	err := command.Run()
	if err == nil {
		t.Fatal("save failure helper exited zero")
	}
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() == 0 {
		t.Fatalf("save failure exit=%v, want nonzero process exit", err)
	}
}

func mornleaServerProcessCommand(t *testing.T, world string) *exec.Cmd {
	t.Helper()
	// 子进程复用父进程 args（TestMornleaServerProcess 里的直接 run() 调用与 runSignal
	// 都读同一份 args），所以只要在这里给 --config 指向一个不存在的路径，两条
	// 路径都不会去读开发者本机的 config.json——理由同 absentConfigArgs。
	args := append(
		[]string{"-test.run=^TestMornleaServerProcess$", "--", "--listen", "127.0.0.1:0", "--world", world},
		absentConfigArgs(t)...,
	)
	command := exec.Command(os.Args[0], args...)
	command.Env = append(os.Environ(), "MORNLEA_SERVER_PROCESS=1")
	command.Stderr = os.Stderr
	command.Stdout = os.Stdout
	return command
}
