package profile

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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
	_, err := writeProfileAtomicallyWithHooks(path, []byte("new profile"), atomicWriteHooks{
		publish: func(string, string) (bool, error) { return false, errRename },
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

func TestLoadOrCreateDefaultUsesMornleaCurrentAndMinecraftGoLegacy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir: %v", err)
	}
	current := filepath.Join(base, "mornlea", "profile.json")
	legacy := filepath.Join(base, "minecraft-go", "profile.json")
	body := []byte(`{"version":1,"player_id":"00112233-4455-4677-8899-aabbccddeeff","display_name":"Chen"}`)
	writeProfileTestFile(t, current, body, 0o700, 0o600)

	got, err := LoadOrCreate(Options{})
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	want := Profile{Version: CurrentVersion, PlayerID: mustPlayerID(t, "00112233-4455-4677-8899-aabbccddeeff"), DisplayName: "Chen"}
	if got != want {
		t.Fatalf("LoadOrCreate = %+v，want %+v", got, want)
	}

	publicPath, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	if publicPath != legacy {
		t.Fatalf("Task 5 的 DefaultPath = %q，want 旧路径 %q", publicPath, legacy)
	}
}

func TestLoadOrCreateDefaultPrefersExistingMornleaProfile(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "mornlea", "profile.json")
	legacy := filepath.Join(root, "minecraft-go", "profile.json")
	currentBody := []byte(`{"version":1,"player_id":"00112233-4455-4677-8899-aabbccddeeff","display_name":"Chen"}`)
	writeProfileTestFile(t, current, currentBody, 0o700, 0o600)
	writeProfileTestFile(t, legacy, []byte(`{"version":`), 0o700, 0o600)

	got, err := loadOrCreateDefaultFromPaths(current, legacy, Options{}, publishProfileExclusively)
	if err != nil {
		t.Fatalf("loadOrCreateDefaultFromPaths: %v", err)
	}
	want := Profile{Version: CurrentVersion, PlayerID: mustPlayerID(t, "00112233-4455-4677-8899-aabbccddeeff"), DisplayName: "Chen"}
	if got != want {
		t.Fatalf("新 profile = %+v，want %+v", got, want)
	}
}

func TestLoadOrCreateDefaultMigratesLegacyProfileExactly(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "mornlea", "profile.json")
	legacy := filepath.Join(root, "minecraft-go", "profile.json")
	legacyBody := []byte("{\n  \"version\": 1,\n  \"player_id\": \"00112233-4455-4677-8899-aabbccddeeff\",\n  \"display_name\": \"Chen\"\n}\n")
	writeProfileTestFile(t, legacy, legacyBody, 0o700, 0o600)

	got, err := loadOrCreateDefaultFromPaths(current, legacy, Options{}, publishProfileExclusively)
	if err != nil {
		t.Fatalf("loadOrCreateDefaultFromPaths: %v", err)
	}
	want := Profile{Version: CurrentVersion, PlayerID: mustPlayerID(t, "00112233-4455-4677-8899-aabbccddeeff"), DisplayName: "Chen"}
	if got != want {
		t.Fatalf("迁移 profile = %+v，want %+v", got, want)
	}
	assertProfileFileBytes(t, current, []byte(`{"version":1,"player_id":"00112233-4455-4677-8899-aabbccddeeff","display_name":"Chen"}`))
	assertProfileFileMode(t, current, 0o600)
	assertProfileFileMode(t, filepath.Dir(current), 0o700)
	assertProfileFileBytes(t, legacy, legacyBody)
}

func TestLoadOrCreateDefaultMigrationAppliesRequestedName(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "mornlea", "profile.json")
	legacy := filepath.Join(root, "minecraft-go", "profile.json")
	legacyBody := []byte(`{"version":1,"player_id":"00112233-4455-4677-8899-aabbccddeeff","display_name":"Chen"}`)
	writeProfileTestFile(t, legacy, legacyBody, 0o700, 0o600)
	requested := "Alex"

	got, err := loadOrCreateDefaultFromPaths(current, legacy, Options{RequestedName: &requested}, publishProfileExclusively)
	if err != nil {
		t.Fatalf("loadOrCreateDefaultFromPaths: %v", err)
	}
	want := Profile{Version: CurrentVersion, PlayerID: mustPlayerID(t, "00112233-4455-4677-8899-aabbccddeeff"), DisplayName: "Alex"}
	if got != want {
		t.Fatalf("迁移 profile = %+v，want %+v", got, want)
	}
	assertProfileFileBytes(t, current, []byte(`{"version":1,"player_id":"00112233-4455-4677-8899-aabbccddeeff","display_name":"Alex"}`))
	assertProfileFileBytes(t, legacy, legacyBody)
}

func TestLoadOrCreateDefaultRejectsInvalidAuthoritativeProfile(t *testing.T) {
	for _, test := range []struct {
		name         string
		writeCurrent bool
	}{
		{name: "current", writeCurrent: true},
		{name: "legacy"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			current := filepath.Join(root, "mornlea", "profile.json")
			legacy := filepath.Join(root, "minecraft-go", "profile.json")
			invalidPath := legacy
			if test.writeCurrent {
				invalidPath = current
				writeProfileTestFile(t, legacy, []byte(`{"version":1,"player_id":"00112233-4455-4677-8899-aabbccddeeff","display_name":"Legacy"}`), 0o700, 0o600)
			}
			invalidBody := []byte(`{"version":`)
			writeProfileTestFile(t, invalidPath, invalidBody, 0o700, 0o600)
			random := &profileCountingReader{}

			_, err := loadOrCreateDefaultFromPaths(current, legacy, Options{Random: random}, publishProfileExclusively)
			if err == nil || !strings.Contains(err.Error(), invalidPath) {
				t.Fatalf("非法权威 profile 错误 = %v，必须包含路径 %q", err, invalidPath)
			}
			if random.reads != 0 {
				t.Fatalf("非法权威 profile 后 Random 读取次数 = %d，want 0", random.reads)
			}
			assertProfileFileBytes(t, invalidPath, invalidBody)
			if !test.writeCurrent {
				assertProfilePathMissing(t, current)
			}
		})
	}
}

func TestLoadOrCreateCustomPathSkipsDefaultMigration(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "mornlea", "profile.json")
	legacy := filepath.Join(root, "minecraft-go", "profile.json")
	custom := filepath.Join(root, "custom", "profile.json")
	legacyBody := []byte(`{"version":1,"player_id":"00112233-4455-4677-8899-aabbccddeeff","display_name":"Legacy"}`)
	writeProfileTestFile(t, legacy, legacyBody, 0o700, 0o600)

	got, err := LoadOrCreate(Options{Path: custom, Random: bytes.NewReader([]byte("custom-profile-id"))})
	if err != nil {
		t.Fatalf("LoadOrCreate custom: %v", err)
	}
	if got.PlayerID == mustPlayerID(t, "00112233-4455-4677-8899-aabbccddeeff") || got.DisplayName != "Player" {
		t.Fatalf("自定义 profile = %+v，不应来自 legacy", got)
	}
	assertProfilePathMissing(t, current)
	assertProfileFileBytes(t, legacy, legacyBody)
}

func TestLoadOrCreateDefaultMissingBothCreatesSingleUUIDv4(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "mornlea", "profile.json")
	legacy := filepath.Join(root, "minecraft-go", "profile.json")
	random := &profileCountingReader{}

	got, err := loadOrCreateDefaultFromPaths(current, legacy, Options{Random: random}, publishProfileExclusively)
	if err != nil {
		t.Fatalf("loadOrCreateDefaultFromPaths: %v", err)
	}
	want := Profile{Version: CurrentVersion, PlayerID: mustPlayerID(t, "00000000-0000-4000-8000-000000000000"), DisplayName: "Player"}
	if got != want {
		t.Fatalf("首次创建 profile = %+v，want %+v", got, want)
	}
	if random.reads != 1 {
		t.Fatalf("Random 读取次数 = %d，want 1", random.reads)
	}
	assertProfileFileBytes(t, current, []byte(`{"version":1,"player_id":"00000000-0000-4000-8000-000000000000","display_name":"Player"}`))
	assertProfileFileMode(t, current, 0o600)
	assertProfileFileMode(t, filepath.Dir(current), 0o700)
	assertNoProfileTemps(t, filepath.Dir(current))
}

func TestLoadOrCreateDefaultConcurrentCreationReturnsSingleWinner(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "mornlea", "profile.json")
	legacy := filepath.Join(root, "minecraft-go", "profile.json")
	barrier := make(chan struct{}, 2)
	release := make(chan struct{})
	var publishedCount atomic.Int32
	publish := func(path string, contents []byte) (bool, error) {
		barrier <- struct{}{}
		<-release
		published, err := publishProfileExclusively(path, contents)
		if published {
			publishedCount.Add(1)
		}
		return published, err
	}

	type result struct {
		profile Profile
		err     error
	}
	results := make(chan result, 2)
	for _, random := range [][]byte{[]byte("0000000000000000"), []byte("1111111111111111")} {
		random := random
		go func() {
			got, err := loadOrCreateDefaultFromPaths(current, legacy, Options{Random: bytes.NewReader(random)}, publish)
			results <- result{profile: got, err: err}
		}()
	}
	<-barrier
	<-barrier
	close(release)

	first := <-results
	second := <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("并发创建错误 = (%v, %v)", first.err, second.err)
	}
	if got := publishedCount.Load(); got != 1 {
		t.Fatalf("published=true 次数 = %d，want 1", got)
	}
	if first.profile != second.profile {
		t.Fatalf("并发调用返回不同 winner：%+v 与 %+v", first.profile, second.profile)
	}
	firstCandidate := mustPlayerID(t, "30303030-3030-4030-b030-303030303030")
	secondCandidate := mustPlayerID(t, "31313131-3131-4131-b131-313131313131")
	if first.profile.PlayerID != firstCandidate && first.profile.PlayerID != secondCandidate {
		t.Fatalf("winner PlayerID = %s，不是任一候选", first.profile.PlayerID)
	}
	contents, err := os.ReadFile(current)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", current, err)
	}
	disk, err := decodeProfile(contents)
	if err != nil {
		t.Fatalf("decodeProfile: %v", err)
	}
	if disk != first.profile {
		t.Fatalf("磁盘 winner = %+v，调用返回 %+v", disk, first.profile)
	}
	assertNoProfileTemps(t, filepath.Dir(current))
}

func TestLoadOrCreateDefaultReadsConcurrentMigrationWinner(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "mornlea", "profile.json")
	legacy := filepath.Join(root, "minecraft-go", "profile.json")
	legacyBody := []byte(`{"version":1,"player_id":"00112233-4455-4677-8899-aabbccddeeff","display_name":"Chen"}`)
	winnerBody := []byte(`{"version":1,"player_id":"ffeeddcc-bbaa-4988-8766-554433221100","display_name":"Winner"}`)
	writeProfileTestFile(t, legacy, legacyBody, 0o700, 0o600)

	got, err := loadOrCreateDefaultFromPaths(current, legacy, Options{}, func(path string, _ []byte) (bool, error) {
		writeProfileTestFile(t, path, winnerBody, 0o700, 0o600)
		return false, nil
	})
	if err != nil {
		t.Fatalf("loadOrCreateDefaultFromPaths: %v", err)
	}
	want := Profile{Version: CurrentVersion, PlayerID: mustPlayerID(t, "ffeeddcc-bbaa-4988-8766-554433221100"), DisplayName: "Winner"}
	if got != want {
		t.Fatalf("迁移 loser 返回 = %+v，want winner %+v", got, want)
	}
	assertProfileFileBytes(t, current, winnerBody)
	assertProfileFileBytes(t, legacy, legacyBody)
}

func TestLoadOrCreateDefaultPublishFailureDoesNotGenerateIdentity(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "mornlea", "profile.json")
	legacy := filepath.Join(root, "minecraft-go", "profile.json")
	legacyBody := []byte(`{"version":1,"player_id":"00112233-4455-4677-8899-aabbccddeeff","display_name":"Chen"}`)
	writeProfileTestFile(t, legacy, legacyBody, 0o700, 0o600)
	random := &profileCountingReader{}
	sentinel := errors.New("publish failure")

	_, err := loadOrCreateDefaultFromPaths(current, legacy, Options{Random: random}, func(path string, contents []byte) (bool, error) {
		return writeProfileAtomicallyWithHooks(path, contents, atomicWriteHooks{
			publish: func(string, string) (bool, error) { return false, sentinel },
			openDirectory: func(string) (profileDirectory, error) {
				return nil, errors.New("发布失败后不得打开父目录")
			},
		})
	})
	if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), current) {
		t.Fatalf("发布失败错误 = %v，必须包含目标路径并匹配 sentinel", err)
	}
	if random.reads != 0 {
		t.Fatalf("迁移发布失败后 Random 读取次数 = %d，want 0", random.reads)
	}
	assertProfileFileBytes(t, legacy, legacyBody)
	assertProfilePathMissing(t, current)
	assertNoProfileTemps(t, filepath.Dir(current))
}

func TestLoadOrCreateDefaultRejectsUnsafeParentPermissions(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "mornlea", "profile.json")
	legacy := filepath.Join(root, "minecraft-go", "profile.json")
	legacyBody := []byte(`{"version":1,"player_id":"00112233-4455-4677-8899-aabbccddeeff","display_name":"Chen"}`)
	if err := os.MkdirAll(filepath.Dir(current), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.Chmod(filepath.Dir(current), 0o755); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	writeProfileTestFile(t, legacy, legacyBody, 0o700, 0o600)
	random := &profileCountingReader{}
	publishCalled := false

	_, err := loadOrCreateDefaultFromPaths(current, legacy, Options{Random: random}, func(string, []byte) (bool, error) {
		publishCalled = true
		return false, nil
	})
	if !errors.Is(err, fs.ErrPermission) || !strings.Contains(err.Error(), current) {
		t.Fatalf("不安全父目录错误 = %v，必须包含新路径并匹配 fs.ErrPermission", err)
	}
	if publishCalled || random.reads != 0 {
		t.Fatalf("不安全父目录进入后续流程：publish=%v Random=%d", publishCalled, random.reads)
	}
	assertProfileFileMode(t, filepath.Dir(current), 0o755)
	assertProfileFileBytes(t, legacy, legacyBody)
	assertProfilePathMissing(t, current)
	assertNoProfileTemps(t, filepath.Dir(current))
}

func TestLoadOrCreateDefaultRejectsUnsafeTargetPermissions(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(t *testing.T, current, legacy string) func(string, []byte) (bool, error)
	}{
		{
			name: "existing current",
			setup: func(t *testing.T, current, legacy string) func(string, []byte) (bool, error) {
				writeProfileTestFile(t, current, []byte(`{"version":`), 0o700, 0o644)
				writeProfileTestFile(t, legacy, []byte(`{"version":1,"player_id":"00112233-4455-4677-8899-aabbccddeeff","display_name":"Legacy"}`), 0o700, 0o600)
				return publishProfileExclusively
			},
		},
		{
			name: "concurrent winner",
			setup: func(t *testing.T, current, legacy string) func(string, []byte) (bool, error) {
				writeProfileTestFile(t, legacy, []byte(`{"version":1,"player_id":"00112233-4455-4677-8899-aabbccddeeff","display_name":"Legacy"}`), 0o700, 0o600)
				return func(path string, _ []byte) (bool, error) {
					writeProfileTestFile(t, path, []byte(`{"version":`), 0o700, 0o644)
					return false, nil
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			current := filepath.Join(root, "mornlea", "profile.json")
			legacy := filepath.Join(root, "minecraft-go", "profile.json")
			publish := test.setup(t, current, legacy)
			random := &profileCountingReader{}

			_, err := loadOrCreateDefaultFromPaths(current, legacy, Options{Random: random}, publish)
			if !errors.Is(err, fs.ErrPermission) || !strings.Contains(err.Error(), current) {
				t.Fatalf("不安全目标错误 = %v，必须包含新路径并匹配 fs.ErrPermission", err)
			}
			if random.reads != 0 {
				t.Fatalf("不安全目标 Random 读取次数 = %d，want 0", random.reads)
			}
			assertProfileFileMode(t, current, 0o644)
			assertNoProfileTemps(t, filepath.Dir(current))
		})
	}
}

func TestLoadOrCreateDefaultRejectsTargetReplacedAfterPathValidation(t *testing.T) {
	for _, test := range defaultProfileReadRaceCases() {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			current := filepath.Join(root, "mornlea", "profile.json")
			legacy := filepath.Join(root, "minecraft-go", "profile.json")
			publish := test.setup(t, current, legacy)
			replacement := filepath.Join(root, "replacement.json")
			replacementBody := []byte(`{"version":1,"player_id":"ffeeddcc-bbaa-4988-8766-554433221100","display_name":"Replacement"}`)
			writeProfileTestFile(t, replacement, replacementBody, 0o700, 0o600)
			swapped := false

			_, err := loadOrCreateDefaultFromPathsWithOpen(current, legacy, Options{}, publish, func(path string) (*os.File, error) {
				if !swapped {
					swapped = true
					if err := os.Rename(replacement, path); err != nil {
						t.Fatalf("换入 0600 different inode: %v", err)
					}
				}
				return os.Open(path)
			})
			if !errors.Is(err, fs.ErrPermission) || !strings.Contains(err.Error(), current) {
				t.Fatalf("different-inode 置换错误 = %v，必须包含新路径并匹配 fs.ErrPermission", err)
			}
			if !swapped {
				t.Fatal("测试必须在 pathname 校验后换入 0600 different inode")
			}
			assertProfileFileBytes(t, current, replacementBody)
			assertProfileFileMode(t, current, 0o600)
		})
	}
}

func TestLoadOrCreateDefaultRejectsSameInodeSymlinkInsertedBeforeOpen(t *testing.T) {
	for _, test := range defaultProfileReadRaceCases() {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			current := filepath.Join(root, "mornlea", "profile.json")
			legacy := filepath.Join(root, "minecraft-go", "profile.json")
			publish := test.setup(t, current, legacy)
			original := current + ".original"
			swapped := false

			_, err := loadOrCreateDefaultFromPathsWithOpen(current, legacy, Options{}, publish, func(path string) (*os.File, error) {
				if !swapped {
					swapped = true
					if err := os.Rename(path, original); err != nil {
						t.Fatalf("移走已校验 inode: %v", err)
					}
					if err := os.Symlink(original, path); err != nil {
						t.Fatalf("换入 same-inode symlink: %v", err)
					}
				}
				return os.Open(path)
			})
			if !errors.Is(err, fs.ErrPermission) || !strings.Contains(err.Error(), current) {
				t.Fatalf("same-inode symlink 置换错误 = %v，必须包含新路径并匹配 fs.ErrPermission", err)
			}
			if !swapped {
				t.Fatal("测试必须在 pre-Lstat 后、打开前换入 same-inode symlink")
			}
			info, statErr := os.Lstat(current)
			if statErr != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("新路径必须是 symlink，Lstat = (%v, %v)", info, statErr)
			}
			assertProfileFileMode(t, original, 0o600)
		})
	}
}

func TestLoadOrCreateDefaultLogsOnlySuccessfulMigrationPublisher(t *testing.T) {
	previous := slog.Default()
	var records bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&records, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	root := t.TempDir()
	current := filepath.Join(root, "publisher", "mornlea", "profile.json")
	legacy := filepath.Join(root, "publisher", "minecraft-go", "profile.json")
	body := []byte(`{"version":1,"player_id":"00112233-4455-4677-8899-aabbccddeeff","display_name":"Chen"}`)
	writeProfileTestFile(t, legacy, body, 0o700, 0o600)
	if _, err := loadOrCreateDefaultFromPaths(current, legacy, Options{}, publishProfileExclusively); err != nil {
		t.Fatalf("publisher migration: %v", err)
	}

	loserCurrent := filepath.Join(root, "loser", "mornlea", "profile.json")
	loserLegacy := filepath.Join(root, "loser", "minecraft-go", "profile.json")
	writeProfileTestFile(t, loserLegacy, body, 0o700, 0o600)
	if _, err := loadOrCreateDefaultFromPaths(loserCurrent, loserLegacy, Options{}, func(path string, contents []byte) (bool, error) {
		writeProfileTestFile(t, path, contents, 0o700, 0o600)
		return false, nil
	}); err != nil {
		t.Fatalf("loser migration: %v", err)
	}

	createdCurrent := filepath.Join(root, "created", "mornlea", "profile.json")
	createdLegacy := filepath.Join(root, "created", "minecraft-go", "profile.json")
	if _, err := loadOrCreateDefaultFromPaths(createdCurrent, createdLegacy, Options{Random: bytes.NewReader(make([]byte, 16))}, publishProfileExclusively); err != nil {
		t.Fatalf("missing-both creation: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(records.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("迁移成功日志条数 = %d，want 1；日志=%q", len(lines), records.String())
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatalf("解析迁移日志: %v", err)
	}
	if record["level"] != "INFO" || record["legacy_path"] != legacy || record["current_path"] != current {
		t.Fatalf("迁移日志 = %v，want INFO 与精确路径", record)
	}
}

type defaultProfileReadRaceCase struct {
	name  string
	setup func(t *testing.T, current, legacy string) func(string, []byte) (bool, error)
}

func defaultProfileReadRaceCases() []defaultProfileReadRaceCase {
	body := []byte(`{"version":1,"player_id":"00112233-4455-4677-8899-aabbccddeeff","display_name":"Chen"}`)
	return []defaultProfileReadRaceCase{
		{
			name: "existing current",
			setup: func(t *testing.T, current, legacy string) func(string, []byte) (bool, error) {
				writeProfileTestFile(t, current, body, 0o700, 0o600)
				writeProfileTestFile(t, legacy, []byte(`{"version":`), 0o700, 0o600)
				return publishProfileExclusively
			},
		},
		{
			name: "concurrent winner",
			setup: func(t *testing.T, current, legacy string) func(string, []byte) (bool, error) {
				writeProfileTestFile(t, legacy, body, 0o700, 0o600)
				return func(path string, _ []byte) (bool, error) {
					writeProfileTestFile(t, path, body, 0o700, 0o600)
					return false, nil
				}
			},
		},
	}
}

type profileCountingReader struct {
	reads int
}

func (r *profileCountingReader) Read(p []byte) (int, error) {
	r.reads++
	clear(p)
	return len(p), nil
}

func mustPlayerID(t *testing.T, text string) core.PlayerID {
	t.Helper()
	id, err := core.ParsePlayerID(text)
	if err != nil {
		t.Fatalf("ParsePlayerID(%q): %v", text, err)
	}
	return id
}

func writeProfileTestFile(t *testing.T, path string, body []byte, dirMode, fileMode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	if err := os.Chmod(filepath.Dir(path), dirMode); err != nil {
		t.Fatalf("Chmod(%s): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, body, fileMode); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
	if err := os.Chmod(path, fileMode); err != nil {
		t.Fatalf("Chmod(%s): %v", path, err)
	}
}

func assertProfileFileBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s 内容 = %q，want %q", path, got, want)
	}
}

func assertProfileFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s): %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s 权限 = %04o，want %04o", path, got, want)
	}
}

func assertProfilePathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s 必须不存在，Stat err = %v", path, err)
	}
}

func assertNoProfileTemps(t *testing.T, directory string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(directory, ".profile.tmp-*"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("遗留 profile 临时文件: %v", matches)
	}
}
