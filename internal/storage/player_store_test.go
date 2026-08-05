package storage

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"minecraft-go/internal/core"
)

func TestPlayerStoreContract(t *testing.T) {
	implementations := []struct {
		name string
		open func(*testing.T) PlayerStore
	}{
		{
			name: "memory",
			open: func(t *testing.T) PlayerStore {
				t.Helper()
				return NewMemory(Metadata{FormatVersion: 2, Seed: 42})
			},
		},
		{
			name: "disk",
			open: func(t *testing.T) PlayerStore {
				t.Helper()
				store, err := OpenDisk(context.Background(), t.TempDir(), OpenOptions{
					Create: Metadata{FormatVersion: 2, Seed: 42},
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
			},
		},
	}

	for _, implementation := range implementations {
		t.Run(implementation.name, func(t *testing.T) {
			testPlayerStoreContract(t, implementation.open)
		})
	}
}

func testPlayerStoreContract(t *testing.T, open func(*testing.T) PlayerStore) {
	t.Helper()

	t.Run("missing wraps ErrPlayerNotFound", func(t *testing.T) {
		store := open(t)
		if _, err := store.LoadPlayer(context.Background(), fixturePlayerID()); !errors.Is(err, ErrPlayerNotFound) {
			t.Fatalf("missing LoadPlayer error = %v, want errors.Is(ErrPlayerNotFound)", err)
		}
	})

	t.Run("first save returns its revision and round trips", func(t *testing.T) {
		store := open(t)
		want := fixturePlayerSave(fixturePlayerID(), 1)
		revision, err := store.SavePlayer(context.Background(), want)
		if err != nil || revision != 1 {
			t.Fatalf("first SavePlayer revision = %d, error = %v", revision, err)
		}
		got, err := store.LoadPlayer(context.Background(), want.PlayerID)
		if err != nil {
			t.Fatal(err)
		}
		assertStoredPlayerMatchesSave(t, got, want)
	})

	t.Run("same revision and same canonical bytes is idempotent", func(t *testing.T) {
		store := open(t)
		save := fixturePlayerSave(fixturePlayerID(), 3)
		if revision, err := store.SavePlayer(context.Background(), save); err != nil || revision != 3 {
			t.Fatalf("initial SavePlayer revision = %d, error = %v", revision, err)
		}
		if revision, err := store.SavePlayer(context.Background(), clonePlayerSave(save)); err != nil || revision != 3 {
			t.Fatalf("idempotent SavePlayer revision = %d, error = %v", revision, err)
		}
	})

	t.Run("same revision and different canonical bytes conflicts", func(t *testing.T) {
		store := open(t)
		save := fixturePlayerSave(fixturePlayerID(), 4)
		if _, err := store.SavePlayer(context.Background(), save); err != nil {
			t.Fatal(err)
		}
		conflict := clonePlayerSave(save)
		conflict.DisplayName = "Alex"
		if _, err := store.SavePlayer(context.Background(), conflict); !errors.Is(err, ErrRevisionConflict) {
			t.Fatalf("same-revision different-content error = %v, want ErrRevisionConflict", err)
		}
		got, err := store.LoadPlayer(context.Background(), save.PlayerID)
		if err != nil {
			t.Fatal(err)
		}
		assertStoredPlayerMatchesSave(t, got, save)
	})

	t.Run("lower revision conflicts without changing stored value", func(t *testing.T) {
		store := open(t)
		higher := fixturePlayerSave(fixturePlayerID(), 5)
		if _, err := store.SavePlayer(context.Background(), higher); err != nil {
			t.Fatal(err)
		}
		lower := clonePlayerSave(higher)
		lower.Revision = 4
		lower.DisplayName = "Alex"
		if _, err := store.SavePlayer(context.Background(), lower); !errors.Is(err, ErrRevisionConflict) {
			t.Fatalf("lower-revision error = %v, want ErrRevisionConflict", err)
		}
		got, err := store.LoadPlayer(context.Background(), higher.PlayerID)
		if err != nil {
			t.Fatal(err)
		}
		assertStoredPlayerMatchesSave(t, got, higher)
	})

	t.Run("higher revision replaces the complete value", func(t *testing.T) {
		store := open(t)
		first := fixturePlayerSave(fixturePlayerID(), 1)
		if _, err := store.SavePlayer(context.Background(), first); err != nil {
			t.Fatal(err)
		}
		higher := clonePlayerSave(first)
		higher.Revision = 2
		higher.DisplayName = "Alex"
		higher.Current.Position = [3]float32{9, 80, -11}
		if revision, err := store.SavePlayer(context.Background(), higher); err != nil || revision != 2 {
			t.Fatalf("higher SavePlayer revision = %d, error = %v", revision, err)
		}
		got, err := store.LoadPlayer(context.Background(), first.PlayerID)
		if err != nil {
			t.Fatal(err)
		}
		assertStoredPlayerMatchesSave(t, got, higher)
	})

	t.Run("canceled load and save do not mutate", func(t *testing.T) {
		store := open(t)
		first := fixturePlayerSave(fixturePlayerID(), 1)
		if _, err := store.SavePlayer(context.Background(), first); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := store.LoadPlayer(ctx, first.PlayerID); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled LoadPlayer error = %v, want context.Canceled", err)
		}
		higher := clonePlayerSave(first)
		higher.Revision = 2
		higher.DisplayName = "Alex"
		if _, err := store.SavePlayer(ctx, higher); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled SavePlayer error = %v, want context.Canceled", err)
		}
		got, err := store.LoadPlayer(context.Background(), first.PlayerID)
		if err != nil {
			t.Fatal(err)
		}
		assertStoredPlayerMatchesSave(t, got, first)
	})

	t.Run("save input and load result do not alias stored Safe", func(t *testing.T) {
		store := open(t)
		save := fixturePlayerSave(fixturePlayerID(), 1)
		wantSafe := *save.Safe
		if _, err := store.SavePlayer(context.Background(), save); err != nil {
			t.Fatal(err)
		}
		save.Safe.Position[0] = 999

		loaded, err := store.LoadPlayer(context.Background(), save.PlayerID)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.Safe == nil || *loaded.Safe != wantSafe {
			t.Fatalf("loaded Safe = %+v, want %+v", loaded.Safe, wantSafe)
		}
		loaded.Safe.Position[1] = 998
		again, err := store.LoadPlayer(context.Background(), save.PlayerID)
		if err != nil {
			t.Fatal(err)
		}
		if again.Safe == nil || *again.Safe != wantSafe {
			t.Fatalf("second loaded Safe = %+v, want %+v", again.Safe, wantSafe)
		}
	})

	t.Run("zero revision validation cannot create a player", func(t *testing.T) {
		store := open(t)
		save := fixturePlayerSave(fixturePlayerID(), 0)
		if _, err := store.SavePlayer(context.Background(), save); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("zero-revision SavePlayer error = %v, want ErrCorrupt", err)
		}
		if _, err := store.LoadPlayer(context.Background(), save.PlayerID); !errors.Is(err, ErrPlayerNotFound) {
			t.Fatalf("LoadPlayer after invalid save error = %v, want ErrPlayerNotFound", err)
		}
	})
}

func TestDiskPlayerUsesCanonicalIDPathsWithoutCollisionAndMode0600(t *testing.T) {
	root := t.TempDir()
	store := openPlayerDisk(t, root)
	firstID := fixturePlayerID()
	secondID := firstID
	secondID[15] = 0xfe
	first := fixturePlayerSave(firstID, 1)
	second := fixturePlayerSave(secondID, 1)
	second.DisplayName = "Alex"

	for _, save := range []PlayerSave{first, second} {
		if _, err := store.SavePlayer(context.Background(), save); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []PlayerSave{first, second} {
		got, err := store.LoadPlayer(context.Background(), want.PlayerID)
		if err != nil {
			t.Fatal(err)
		}
		assertStoredPlayerMatchesSave(t, got, want)
	}

	entries, err := os.ReadDir(filepath.Join(root, "players"))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	wantNames := []string{firstID.String() + ".player", secondID.String() + ".player"}
	sort.Strings(wantNames)
	if !reflect.DeepEqual(names, wantNames) {
		t.Fatalf("player filenames = %v, want canonical names %v", names, wantNames)
	}
	for _, name := range wantNames {
		info, err := os.Stat(filepath.Join(root, "players", name))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("%s mode = %#o, want 0600", name, got)
		}
	}
}

func TestDiskPlayerReopenPreservesRevisionAndValue(t *testing.T) {
	root := t.TempDir()
	first := fixturePlayerSave(fixturePlayerID(), 7)
	store := openPlayerDisk(t, root)
	if _, err := store.SavePlayer(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := openPlayerDisk(t, root)
	got, err := reopened.LoadPlayer(context.Background(), first.PlayerID)
	if err != nil {
		t.Fatal(err)
	}
	assertStoredPlayerMatchesSave(t, got, first)
	higher := clonePlayerSave(first)
	higher.Revision = 8
	higher.DisplayName = "Alex"
	if revision, err := reopened.SavePlayer(context.Background(), higher); err != nil || revision != 8 {
		t.Fatalf("reopened SavePlayer revision = %d, error = %v", revision, err)
	}
	got, err = reopened.LoadPlayer(context.Background(), first.PlayerID)
	if err != nil {
		t.Fatal(err)
	}
	assertStoredPlayerMatchesSave(t, got, higher)
}

func TestDiskPlayerSaveDoesNotOverwriteCorruptOrFutureFile(t *testing.T) {
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
				binary.LittleEndian.PutUint32(encoded[8:12], currentPlayerSchema+1)
			},
			wantErr: ErrFutureVersion,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			id := fixturePlayerID()
			store := openPlayerDisk(t, root)
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "players", id.String()+".player")
			before, err := encodePlayer(fixturePlayerSave(id, 1))
			if err != nil {
				t.Fatal(err)
			}
			tc.mutate(before)
			if err := os.WriteFile(path, before, 0o600); err != nil {
				t.Fatal(err)
			}

			reopened := openPlayerDisk(t, root)
			if _, err := reopened.SavePlayer(
				context.Background(), fixturePlayerSave(id, 2),
			); !errors.Is(err, tc.wantErr) {
				t.Fatalf("SavePlayer error = %v, want %v", err, tc.wantErr)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, before) {
				t.Fatal("failed SavePlayer overwrote invalid canonical file")
			}
		})
	}
}

func TestBoundedPlayerFileReaderRejectsLimitPlusOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversized.player")
	oversized := bytes.Repeat(
		[]byte{0x5a}, playerEnvelopeLength+int(maxPlayerPayload)+1,
	)
	if err := os.WriteFile(path, oversized, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPlayerFile(path); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("readPlayerFile oversized error = %v, want ErrCorrupt", err)
	}
}

func TestDiskPlayerOversizedCanonicalFileIsCorruptAndNeverOverwritten(t *testing.T) {
	root := t.TempDir()
	id := fixturePlayerID()
	store := openPlayerDisk(t, root)
	path := filepath.Join(root, "players", id.String()+".player")
	oversized := bytes.Repeat(
		[]byte{0x5a}, playerEnvelopeLength+int(maxPlayerPayload)+1,
	)
	if err := os.WriteFile(path, oversized, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := store.LoadPlayer(context.Background(), id); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("LoadPlayer oversized error = %v, want ErrCorrupt", err)
	}
	if _, err := store.SavePlayer(
		context.Background(), fixturePlayerSave(id, 2),
	); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("SavePlayer over oversized file error = %v, want ErrCorrupt", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, oversized) {
		t.Fatal("SavePlayer overwrote oversized canonical file")
	}
}

func TestDiskPlayerRejectsInvalidIDBeforeConstructingPath(t *testing.T) {
	root := t.TempDir()
	store := openPlayerDisk(t, root)
	if _, err := store.LoadPlayer(context.Background(), core.PlayerID{}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("invalid-ID LoadPlayer error = %v, want ErrCorrupt", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "players"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("invalid player ID touched files: %v", entries)
	}
}

func TestDiskPlayerAtomicFailuresPreserveCompleteCanonicalFileAndCleanTemp(t *testing.T) {
	tests := []struct {
		name        string
		configure   func(error) atomicReplaceHooks
		wantNewFile bool
	}{
		{
			name: "temporary write",
			configure: func(injected error) atomicReplaceHooks {
				return atomicReplaceHooks{
					createTemp: playerFaultCreateTemp(injected, nil, nil),
				}
			},
		},
		{
			name: "temporary sync",
			configure: func(injected error) atomicReplaceHooks {
				return atomicReplaceHooks{
					createTemp: playerFaultCreateTemp(nil, injected, nil),
				}
			},
		},
		{
			name: "temporary close",
			configure: func(injected error) atomicReplaceHooks {
				return atomicReplaceHooks{
					createTemp: playerFaultCreateTemp(nil, nil, injected),
				}
			},
		},
		{
			name: "rename",
			configure: func(injected error) atomicReplaceHooks {
				return atomicReplaceHooks{
					rename: func(string, string) error { return injected },
				}
			},
		},
		{
			name: "directory sync after rename",
			configure: func(injected error) atomicReplaceHooks {
				return atomicReplaceHooks{
					openDirectory: func(path string) (metadataDirectory, error) {
						directory, err := os.Open(path)
						if err != nil {
							return nil, err
						}
						return &playerFaultDirectory{File: directory, syncErr: injected}, nil
					},
				}
			},
			wantNewFile: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			id := fixturePlayerID()
			first := fixturePlayerSave(id, 1)
			higher := clonePlayerSave(first)
			higher.Revision = 2
			higher.DisplayName = "Alex"
			store := openPlayerDisk(t, root)
			if _, err := store.SavePlayer(context.Background(), first); err != nil {
				t.Fatal(err)
			}

			injected := errors.New("injected " + tc.name)
			store.playerReplaceHooks = tc.configure(injected)
			if _, err := store.SavePlayer(context.Background(), higher); !errors.Is(err, injected) {
				t.Fatalf("SavePlayer error = %v, want injected error", err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}

			matches, err := filepath.Glob(
				filepath.Join(root, "players", "."+id.String()+".player.tmp-*"),
			)
			if err != nil {
				t.Fatal(err)
			}
			if len(matches) != 0 {
				t.Fatalf("temporary player files leaked: %v", matches)
			}
			reopened := openPlayerDisk(t, root)
			got, err := reopened.LoadPlayer(context.Background(), id)
			if err != nil {
				t.Fatalf("LoadPlayer after injected failure: %v", err)
			}
			if tc.wantNewFile {
				assertStoredPlayerMatchesSave(t, got, higher)
			} else {
				assertStoredPlayerMatchesSave(t, got, first)
			}
		})
	}
}

func TestDiskPlayerAtomicCancelDuringTempCompletionPreservesOldFile(t *testing.T) {
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
			id := fixturePlayerID()
			first := fixturePlayerSave(id, 1)
			higher := clonePlayerSave(first)
			higher.Revision = 2
			higher.DisplayName = "Alex"
			store := openPlayerDisk(t, root)
			if _, err := store.SavePlayer(context.Background(), first); err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			store.playerReplaceHooks = atomicReplaceHooks{
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
			if revision, err := store.SavePlayer(ctx, higher); !errors.Is(err, context.Canceled) {
				t.Errorf("SavePlayer revision = %d, error = %v, want context.Canceled", revision, err)
			}

			got, err := store.LoadPlayer(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			assertStoredPlayerMatchesSave(t, got, first)
			matches, err := filepath.Glob(
				filepath.Join(root, "players", "."+id.String()+".player.tmp-*"),
			)
			if err != nil {
				t.Fatal(err)
			}
			if len(matches) != 0 {
				t.Fatalf("temporary player files leaked after cancellation: %v", matches)
			}
		})
	}
}

type playerFaultFile struct {
	*os.File
	writeErr error
	syncErr  error
	closeErr error
}

type cancelingPlayerFile struct {
	*os.File
	afterSync  func()
	afterClose func()
}

func (file *cancelingPlayerFile) Sync() error {
	err := file.File.Sync()
	if file.afterSync != nil {
		file.afterSync()
	}
	return err
}

func (file *cancelingPlayerFile) Close() error {
	err := file.File.Close()
	if file.afterClose != nil {
		file.afterClose()
	}
	return err
}

func (file *playerFaultFile) Write(data []byte) (int, error) {
	if file.writeErr != nil {
		return 0, file.writeErr
	}
	return file.File.Write(data)
}

func (file *playerFaultFile) Sync() error {
	if file.syncErr != nil {
		return file.syncErr
	}
	return file.File.Sync()
}

func (file *playerFaultFile) Close() error {
	return errors.Join(file.File.Close(), file.closeErr)
}

func playerFaultCreateTemp(
	writeErr, syncErr, closeErr error,
) func(string, string) (atomicReplaceFile, error) {
	return func(directory, pattern string) (atomicReplaceFile, error) {
		file, err := os.CreateTemp(directory, pattern)
		if err != nil {
			return nil, err
		}
		return &playerFaultFile{
			File: file, writeErr: writeErr, syncErr: syncErr, closeErr: closeErr,
		}, nil
	}
}

type playerFaultDirectory struct {
	*os.File
	syncErr error
}

func (directory *playerFaultDirectory) Sync() error {
	if directory.syncErr != nil {
		return directory.syncErr
	}
	return directory.File.Sync()
}

func openPlayerDisk(t *testing.T, root string) *DiskStore {
	t.Helper()
	store, err := OpenDisk(context.Background(), root, OpenOptions{
		Create: Metadata{FormatVersion: 2, Seed: 42},
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

func clonePlayerSave(save PlayerSave) PlayerSave {
	cloned := save
	if save.Safe != nil {
		safe := *save.Safe
		cloned.Safe = &safe
	}
	return cloned
}

func assertStoredPlayerMatchesSave(t *testing.T, got StoredPlayer, want PlayerSave) {
	t.Helper()
	if got.PlayerID != want.PlayerID || got.Revision != want.Revision ||
		got.DisplayName != want.DisplayName || got.Current != want.Current ||
		got.Yaw != want.Yaw || got.Pitch != want.Pitch ||
		!reflect.DeepEqual(got.Safe, want.Safe) {
		t.Fatalf("stored player = %+v, want save %+v", got, want)
	}
	if got.NeedsRewrite {
		t.Fatal("canonical player unexpectedly needs rewrite")
	}
}
