package storage

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

type closeableCompanionStore interface {
	CompanionStore
	Close() error
}

func TestCompanionStoreContract(t *testing.T) {
	implementations := []struct {
		name            string
		open            func(*testing.T) closeableCompanionStore
		closedAPIErrors bool
	}{
		{
			name: "memory",
			open: func(*testing.T) closeableCompanionStore {
				return NewMemory(Metadata{FormatVersion: currentMetadataVersion, Seed: 42})
			},
		},
		{
			name: "disk",
			open: func(t *testing.T) closeableCompanionStore {
				store, err := OpenDisk(context.Background(), t.TempDir(), OpenOptions{
					Create: Metadata{FormatVersion: currentMetadataVersion, Seed: 42},
				})
				if err != nil {
					t.Fatal(err)
				}
				return store
			},
			closedAPIErrors: true,
		},
	}

	for _, implementation := range implementations {
		t.Run(implementation.name, func(t *testing.T) {
			testCompanionStoreContract(t, implementation.open, implementation.closedAPIErrors)
		})
	}
}

func testCompanionStoreContract(
	t *testing.T,
	open func(*testing.T) closeableCompanionStore,
	closedAPIErrors bool,
) {
	t.Helper()

	t.Run("missing wraps ErrCompanionsNotFound", func(t *testing.T) {
		store := open(t)
		t.Cleanup(func() { _ = store.Close() })
		if _, err := store.LoadCompanions(context.Background()); !errors.Is(err, ErrCompanionsNotFound) {
			t.Fatalf("missing LoadCompanions error=%v，想要 ErrCompanionsNotFound", err)
		}
	})

	t.Run("round trip is canonical and does not alias", func(t *testing.T) {
		store := open(t)
		t.Cleanup(func() { _ = store.Close() })
		input := fixtureCompanionBodies()
		queues := fixtureCompanionQueues()
		if err := store.SaveCompanions(context.Background(), CompanionSave{
			Revision: 7, Records: input, Queues: queues,
		}); err != nil {
			t.Fatal(err)
		}
		input[0].Position[0] = 999
		queues[0].Current.PlanSteps[0].X = 999
		queues[0].Pending[0] = "被篡改"

		loaded, err := store.LoadCompanions(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		wantBodies := fixtureCompanionBodies()
		slices.Reverse(wantBodies)
		wantQueues := fixtureCompanionQueues()
		if loaded.Revision != 7 || !reflect.DeepEqual(loaded.Records, wantBodies) ||
			!reflect.DeepEqual(loaded.Queues, wantQueues) {
			t.Fatalf(
				"loaded=%+v，想要 revision=7 records=%+v queues=%+v",
				loaded, wantBodies, wantQueues,
			)
		}
		loaded.Records[0].Position[1] = 998
		loaded.Queues[0].Pending[1] = "污染"
		again, err := store.LoadCompanions(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if again.Revision != 7 || !reflect.DeepEqual(again.Records, wantBodies) ||
			!reflect.DeepEqual(again.Queues, wantQueues) {
			t.Fatalf("second load=%+v，想要保持 %+v/%+v", again, wantBodies, wantQueues)
		}
	})

	t.Run("revision idempotency and conflicts preserve the value", func(t *testing.T) {
		store := open(t)
		t.Cleanup(func() { _ = store.Close() })
		first := CompanionSave{Revision: 5, Records: fixtureCompanionBodies()}
		if err := store.SaveCompanions(context.Background(), first); err != nil {
			t.Fatal(err)
		}
		idempotent := CompanionSave{Revision: 5, Records: slices.Clone(first.Records)}
		slices.Reverse(idempotent.Records)
		if err := store.SaveCompanions(context.Background(), idempotent); err != nil {
			t.Fatalf("idempotent SaveCompanions error=%v", err)
		}
		conflict := CompanionSave{Revision: 5, Records: slices.Clone(first.Records)}
		conflict.Records[0].Position[0]++
		if err := store.SaveCompanions(context.Background(), conflict); !errors.Is(err, ErrRevisionConflict) {
			t.Fatalf("same-revision SaveCompanions error=%v，想要 ErrRevisionConflict", err)
		}
		lower := CompanionSave{Revision: 4, Records: slices.Clone(first.Records)}
		if err := store.SaveCompanions(context.Background(), lower); !errors.Is(err, ErrRevisionConflict) {
			t.Fatalf("lower-revision SaveCompanions error=%v，想要 ErrRevisionConflict", err)
		}
		got, err := store.LoadCompanions(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		wantRecords := slices.Clone(first.Records)
		slices.Reverse(wantRecords)
		if got.Revision != 5 || !reflect.DeepEqual(got.Records, wantRecords) {
			t.Fatalf("after conflicts loaded=%+v，想要保持 revision=5 records=%+v", got, wantRecords)
		}

		higher := CompanionSave{Revision: 6, Records: fixtureCompanionBodies()[:1]}
		if err := store.SaveCompanions(context.Background(), higher); err != nil {
			t.Fatal(err)
		}
		got, err = store.LoadCompanions(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if got.Revision != 6 || !reflect.DeepEqual(got.Records, higher.Records) {
			t.Fatalf("higher revision loaded=%+v，想要 %+v", got, higher)
		}
	})

	t.Run("canceled operations do not mutate", func(t *testing.T) {
		store := open(t)
		t.Cleanup(func() { _ = store.Close() })
		first := CompanionSave{Revision: 1, Records: fixtureCompanionBodies()}
		if err := store.SaveCompanions(context.Background(), first); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := store.LoadCompanions(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled LoadCompanions error=%v，想要 context.Canceled", err)
		}
		higher := CompanionSave{Revision: 2, Records: fixtureCompanionBodies()[:1]}
		if err := store.SaveCompanions(ctx, higher); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled SaveCompanions error=%v，想要 context.Canceled", err)
		}
		got, err := store.LoadCompanions(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		wantRecords := slices.Clone(first.Records)
		slices.Reverse(wantRecords)
		if got.Revision != 1 || !reflect.DeepEqual(got.Records, wantRecords) {
			t.Fatalf("after cancellation loaded=%+v，想要保持 revision=1 records=%+v", got, wantRecords)
		}
	})

	t.Run("close is idempotent", func(t *testing.T) {
		store := open(t)
		if err := store.Close(); err != nil {
			t.Fatalf("first Close=%v", err)
		}
		if err := store.Close(); err != nil {
			t.Fatalf("second Close=%v", err)
		}
		if !closedAPIErrors {
			return
		}
		if _, err := store.LoadCompanions(context.Background()); !errors.Is(err, os.ErrClosed) {
			t.Fatalf("LoadCompanions after Close error=%v，想要 os.ErrClosed", err)
		}
		if err := store.SaveCompanions(context.Background(), CompanionSave{
			Revision: 1, Records: fixtureCompanionBodies(),
		}); !errors.Is(err, os.ErrClosed) {
			t.Fatalf("SaveCompanions after Close error=%v，想要 os.ErrClosed", err)
		}
	})
}

func TestDiskCompanionAtomicReplaceKeepsOldFileOnFailure(t *testing.T) {
	tests := []struct {
		name      string
		configure func(error) atomicReplaceHooks
	}{
		{
			name: "temporary write",
			configure: func(injected error) atomicReplaceHooks {
				return atomicReplaceHooks{createTemp: playerFaultCreateTemp(injected, nil, nil)}
			},
		},
		{
			name: "temporary sync",
			configure: func(injected error) atomicReplaceHooks {
				return atomicReplaceHooks{createTemp: playerFaultCreateTemp(nil, injected, nil)}
			},
		},
		{
			name: "temporary close",
			configure: func(injected error) atomicReplaceHooks {
				return atomicReplaceHooks{createTemp: playerFaultCreateTemp(nil, nil, injected)}
			},
		},
		{
			name: "rename",
			configure: func(injected error) atomicReplaceHooks {
				return atomicReplaceHooks{rename: func(string, string) error { return injected }}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			store := openCompanionDisk(t, root)
			first := CompanionSave{
				Revision: 1,
				Records:  fixtureCompanionBodies(),
				Queues:   fixtureCompanionQueues(),
			}
			if err := store.SaveCompanions(context.Background(), first); err != nil {
				t.Fatal(err)
			}
			injected := errors.New("injected " + tc.name)
			store.companionReplaceHooks = tc.configure(injected)
			if err := store.SaveCompanions(context.Background(), CompanionSave{
				Revision: 2, Records: fixtureCompanionBodies()[:1],
			}); !errors.Is(err, injected) {
				t.Fatalf("SaveCompanions error=%v，想要 injected error", err)
			}
			got, err := store.LoadCompanions(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			wantRecords := fixtureCompanionBodies()
			slices.Reverse(wantRecords)
			if got.Revision != 1 || !reflect.DeepEqual(got.Records, wantRecords) ||
				!reflect.DeepEqual(got.Queues, fixtureCompanionQueues()) {
				t.Fatalf("after failed replace loaded=%+v，想要旧值（含任务载荷）", got)
			}
			matches, err := filepath.Glob(filepath.Join(root, ".companions.ai.tmp-*"))
			if err != nil {
				t.Fatal(err)
			}
			if len(matches) != 0 {
				t.Fatalf("failed replace leaked temporary files: %v", matches)
			}
		})
	}
}

func TestDiskCompanionAtomicReplaceReportsParentSyncFailureAfterPublish(t *testing.T) {
	root := t.TempDir()
	store := openCompanionDisk(t, root)
	if err := store.SaveCompanions(context.Background(), CompanionSave{
		Revision: 1, Records: fixtureCompanionBodies(),
	}); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected directory sync")
	store.companionReplaceHooks = atomicReplaceHooks{
		openDirectory: func(path string) (metadataDirectory, error) {
			directory, err := os.Open(path)
			if err != nil {
				return nil, err
			}
			return &playerFaultDirectory{File: directory, syncErr: injected}, nil
		},
	}
	if err := store.SaveCompanions(context.Background(), CompanionSave{
		Revision: 2, Records: fixtureCompanionBodies()[:1],
	}); !errors.Is(err, injected) {
		t.Fatalf("SaveCompanions error=%v，想要 parent Sync error", err)
	}
	got, err := store.LoadCompanions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != 2 || !reflect.DeepEqual(got.Records, fixtureCompanionBodies()[:1]) {
		t.Fatalf("rename 后正式文件=%+v，想要完整 revision 2", got)
	}
}

func TestDiskCompanionAtomicCancelDuringTempCompletionPreservesOldFile(t *testing.T) {
	tests := []struct {
		name   string
		cancel func(*cancelingPlayerFile, context.CancelFunc)
	}{
		{
			name: "sync",
			cancel: func(file *cancelingPlayerFile, cancel context.CancelFunc) {
				file.afterSync = cancel
			},
		},
		{
			name: "close",
			cancel: func(file *cancelingPlayerFile, cancel context.CancelFunc) {
				file.afterClose = cancel
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			store := openCompanionDisk(t, root)
			first := CompanionSave{Revision: 1, Records: fixtureCompanionBodies()}
			if err := store.SaveCompanions(context.Background(), first); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "companions.ai")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			store.companionReplaceHooks = atomicReplaceHooks{
				createTemp: func(directory, pattern string) (atomicReplaceFile, error) {
					file, err := os.CreateTemp(directory, pattern)
					if err != nil {
						return nil, err
					}
					wrapped := &cancelingPlayerFile{File: file}
					tc.cancel(wrapped, cancel)
					return wrapped, nil
				},
			}
			if err := store.SaveCompanions(ctx, CompanionSave{
				Revision: 2, Records: fixtureCompanionBodies()[:1],
			}); !errors.Is(err, context.Canceled) {
				t.Fatalf("SaveCompanions error=%v，想要 context.Canceled", err)
			}

			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, before) {
				t.Fatal("取消后的 SaveCompanions 改写了旧正式文件")
			}
			got, err := store.LoadCompanions(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			wantRecords := slices.Clone(first.Records)
			slices.Reverse(wantRecords)
			if got.Revision != 1 || !reflect.DeepEqual(got.Records, wantRecords) {
				t.Fatalf("取消后 loaded=%+v，想要旧值", got)
			}
			matches, err := filepath.Glob(filepath.Join(root, ".companions.ai.tmp-*"))
			if err != nil {
				t.Fatal(err)
			}
			if len(matches) != 0 {
				t.Fatalf("取消后残留伙伴临时文件: %v", matches)
			}
		})
	}
}

func TestDiskCompanionOversizedFileIsCorruptAndSaveDoesNotOverwriteIt(t *testing.T) {
	root := t.TempDir()
	store := openCompanionDisk(t, root)
	path := filepath.Join(root, "companions.ai")
	oversized := bytes.Repeat([]byte{0x5a}, maxCompanionFileLength+1)
	if err := os.WriteFile(path, oversized, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readCompanionFile(path); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("readCompanionFile error=%v，想要 ErrCorrupt", err)
	}
	if _, err := store.LoadCompanions(context.Background()); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("LoadCompanions error=%v，想要 ErrCorrupt", err)
	}
	if err := store.SaveCompanions(context.Background(), CompanionSave{
		Revision: 2, Records: fixtureCompanionBodies(),
	}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("SaveCompanions error=%v，想要 ErrCorrupt", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, oversized) {
		t.Fatal("SaveCompanions 覆写了超限正式文件")
	}
}

func TestDiskCompanionSaveDoesNotOverwriteCorruptOrFutureFile(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func([]byte)
		wantErr error
	}{
		{
			name:    "corrupt",
			mutate:  func(encoded []byte) { encoded[len(encoded)-1] ^= 0xff },
			wantErr: ErrCorrupt,
		},
		{
			name: "future",
			mutate: func(encoded []byte) {
				binary.LittleEndian.PutUint32(encoded[8:12], currentCompanionSchema+1)
			},
			wantErr: ErrFutureVersion,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			store := openCompanionDisk(t, root)
			path := filepath.Join(root, "companions.ai")
			before, err := encodeCompanions(CompanionSave{
				Revision: 1, Records: fixtureCompanionBodies(),
			})
			if err != nil {
				t.Fatal(err)
			}
			tc.mutate(before)
			if err := os.WriteFile(path, before, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := store.SaveCompanions(context.Background(), CompanionSave{
				Revision: 2, Records: fixtureCompanionBodies()[:1],
			}); !errors.Is(err, tc.wantErr) {
				t.Fatalf("SaveCompanions error=%v，想要 %v", err, tc.wantErr)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, before) {
				t.Fatal("SaveCompanions 覆写了非法正式文件")
			}
		})
	}
}

func openCompanionDisk(t *testing.T, root string) *DiskStore {
	t.Helper()
	store, err := OpenDisk(context.Background(), root, OpenOptions{
		Create: Metadata{FormatVersion: currentMetadataVersion, Seed: 42},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return store
}
