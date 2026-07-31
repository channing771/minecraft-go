package profile

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"minecraft-go/internal/core"
)

func TestLoadOrCreateCreatesPrivateV1Profile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "minecraft-go", "profile.json")
	name := "  Chen  "
	got, err := LoadOrCreate(Options{
		Path:          path,
		RequestedName: &name,
		Random:        bytes.NewReader([]byte("0123456789abcdef")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != CurrentVersion || !got.PlayerID.Valid() || got.DisplayName != "Chen" {
		t.Fatalf("profile = %+v", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file permission = %#o, want 0600", info.Mode().Perm())
	}
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if dir.Mode().Perm()&^fs.FileMode(0o700) != 0 {
		t.Fatalf("directory permissions %#o are wider than 0700", dir.Mode().Perm())
	}
}

func TestLoadOrCreateUsesDefaultNameWhenCreatingWithoutRequest(t *testing.T) {
	profile, err := LoadOrCreate(Options{
		Path:   filepath.Join(t.TempDir(), "minecraft-go", "profile.json"),
		Random: bytes.NewReader([]byte("0123456789abcdef")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if profile.DisplayName != "Player" || !profile.PlayerID.Valid() {
		t.Fatalf("profile = %+v, want default Player name and a valid ID", profile)
	}
}

func TestLoadOrCreateRejectsInsecureExistingParentWithoutChangingIt(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "external-parent")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "profile.json")
	name := "Chen"
	_, err := LoadOrCreate(Options{
		Path:          path,
		RequestedName: &name,
		Random:        bytes.NewReader([]byte("0123456789abcdef")),
	})
	if err == nil {
		t.Fatal("LoadOrCreate created a profile in an insecure existing parent")
	}
	if !strings.Contains(err.Error(), parent) {
		t.Fatalf("error = %q, want path %q", err, parent)
	}
	info, err := os.Stat(parent)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("parent permissions = %#o, want unchanged 0755", info.Mode().Perm())
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("profile unexpectedly exists or cannot be inspected: %v", err)
	}
}

func TestLoadOrCreateRejectsInsecureParentBeforeRenaming(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "minecraft-go")
	path := filepath.Join(parent, "profile.json")
	firstName := "Chen"
	first, err := LoadOrCreate(Options{
		Path:          path,
		RequestedName: &firstName,
		Random:        bytes.NewReader([]byte("0123456789abcdef")),
	})
	if err != nil {
		t.Fatal(err)
	}
	oldContents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o755); err != nil {
		t.Fatal(err)
	}

	secondName := "Alex"
	_, err = LoadOrCreate(Options{Path: path, RequestedName: &secondName})
	if err == nil {
		t.Fatal("LoadOrCreate renamed a profile in an insecure parent")
	}
	if !strings.Contains(err.Error(), parent) {
		t.Fatalf("error = %q, want path %q", err, parent)
	}
	info, err := os.Stat(parent)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("parent permissions = %#o, want unchanged 0755", info.Mode().Perm())
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(contents, oldContents) {
		t.Fatalf("profile was overwritten: got %q want %q", contents, oldContents)
	}
	loaded, err := LoadOrCreate(Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PlayerID != first.PlayerID || loaded.DisplayName != first.DisplayName {
		t.Fatalf("profile changed: first=%+v loaded=%+v", first, loaded)
	}
}

func TestLoadOrCreateKeepsIDWhenNameChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "minecraft-go", "profile.json")
	firstName := "Chen"
	first, err := LoadOrCreate(Options{
		Path: path, RequestedName: &firstName,
		Random: bytes.NewReader([]byte("0123456789abcdef")),
	})
	if err != nil {
		t.Fatal(err)
	}
	secondName := "Alex"
	second, err := LoadOrCreate(Options{Path: path, RequestedName: &secondName})
	if err != nil {
		t.Fatal(err)
	}
	if second.PlayerID != first.PlayerID || second.DisplayName != "Alex" {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
}

func TestLoadOrCreateRejectsInvalidExistingProfileWithoutOverwriting(t *testing.T) {
	validID := "00112233-4455-4677-8899-aabbccddeeff"
	cases := map[string]string{
		"damaged JSON":    "{",
		"duplicate field": `{"version":1,"version":1,"player_id":"` + validID + `","display_name":"Chen"}`,
		"unknown field":   `{"version":1,"player_id":"` + validID + `","display_name":"Chen","extra":true}`,
		"future version":  `{"version":2,"player_id":"` + validID + `","display_name":"Chen"}`,
		"non-v4 ID":       `{"version":1,"player_id":"00112233-4455-3677-8899-aabbccddeeff","display_name":"Chen"}`,
	}
	for name, contents := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "profile.json")
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			requestedName := "Alex"
			if _, err := LoadOrCreate(Options{Path: path, RequestedName: &requestedName}); err == nil {
				t.Fatal("LoadOrCreate accepted invalid profile")
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != contents {
				t.Fatalf("invalid profile was overwritten: got %q want %q", got, contents)
			}
		})
	}
}

func TestWriteProfileLeavesExistingFileWhenRenameFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile.json")
	const old = "old profile"
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	errRename := errors.New("rename failed")
	err := writeProfileAtomicallyWithHooks(path, []byte("new profile"), atomicWriteHooks{
		rename: func(string, string) error { return errRename },
		openDirectory: func(string) (profileDirectory, error) {
			return nil, errors.New("must not open directory after failed rename")
		},
	})
	if !errors.Is(err, errRename) {
		t.Fatalf("error = %v, want rename failure", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != old {
		t.Fatalf("old file = %q, want %q", got, old)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".profile.tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary files not cleaned up: %s", strings.Join(matches, ", "))
	}
}

func TestDecodeProfileRejectsMissingRequiredField(t *testing.T) {
	_, err := decodeProfile([]byte(`{"version":1,"display_name":"Chen"}`))
	if err == nil {
		t.Fatal("decodeProfile accepted missing player_id")
	}
}

func TestProfileRoundTripHasCanonicalID(t *testing.T) {
	id, err := core.ParsePlayerID("00112233-4455-4677-8899-aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := encodeProfile(Profile{Version: CurrentVersion, PlayerID: id, DisplayName: "Chen"})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeProfile(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != (Profile{Version: CurrentVersion, PlayerID: id, DisplayName: "Chen"}) {
		t.Fatalf("decoded = %+v", decoded)
	}
}
