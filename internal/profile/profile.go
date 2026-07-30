// Package profile 管理本机玩家的全局档案。
package profile

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unicode/utf8"

	"minecraft-go/internal/core"
)

// CurrentVersion 是当前磁盘档案格式版本。
const CurrentVersion uint32 = 1

// Profile 是本机玩家的稳定身份和显示昵称。
type Profile struct {
	Version     uint32
	PlayerID    core.PlayerID
	DisplayName string
}

// Options 配置 LoadOrCreate 的档案位置和首次创建资料。
type Options struct {
	Path          string
	RequestedName *string
	Random        io.Reader
}

// DefaultPath 返回本机全局档案的默认路径。
func DefaultPath() (string, error) {
	configDirectory, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("profile: user config directory: %w", err)
	}
	return filepath.Join(configDirectory, "minecraft-go", "profile.json"), nil
}

// LoadOrCreate 读取本机档案，或以新 UUIDv4 创建一个。
func LoadOrCreate(options Options) (Profile, error) {
	path := options.Path
	if path == "" {
		var err error
		path, err = DefaultPath()
		if err != nil {
			return Profile{}, err
		}
	}

	contents, err := os.ReadFile(path)
	if err == nil {
		profile, err := decodeProfile(contents)
		if err != nil {
			return Profile{}, fmt.Errorf("profile: decode %s: %w", path, err)
		}
		if options.RequestedName == nil {
			return profile, nil
		}
		name, err := core.NormalizeDisplayName(*options.RequestedName)
		if err != nil {
			return Profile{}, err
		}
		if name == profile.DisplayName {
			return profile, nil
		}
		if err := ensureProfileParent(filepath.Dir(path)); err != nil {
			return Profile{}, err
		}
		profile.DisplayName = name
		if err := saveProfile(path, profile); err != nil {
			return Profile{}, err
		}
		return profile, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Profile{}, fmt.Errorf("profile: read %s: %w", path, err)
	}
	name := "Player"
	if options.RequestedName != nil {
		name, err = core.NormalizeDisplayName(*options.RequestedName)
		if err != nil {
			return Profile{}, err
		}
	}
	if err := ensureProfileParent(filepath.Dir(path)); err != nil {
		return Profile{}, err
	}
	id, err := newPlayerID(options.Random)
	if err != nil {
		return Profile{}, err
	}
	profile := Profile{Version: CurrentVersion, PlayerID: id, DisplayName: name}
	if err := saveProfile(path, profile); err != nil {
		return Profile{}, err
	}
	return profile, nil
}

func ensureProfileParent(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("profile: create directory %s: %w", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("profile: inspect directory %s: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("profile: parent path %s is not a directory", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("profile: parent directory %s has insecure permissions %#o; restrict it to 0700", path, info.Mode().Perm())
	}
	return nil
}

func newPlayerID(source io.Reader) (core.PlayerID, error) {
	if source == nil {
		source = rand.Reader
	}
	var id core.PlayerID
	if _, err := io.ReadFull(source, id[:]); err != nil {
		return core.PlayerID{}, fmt.Errorf("profile: generate player ID: %w", err)
	}
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	return id, nil
}

func saveProfile(path string, profile Profile) error {
	contents, err := encodeProfile(profile)
	if err != nil {
		return err
	}
	if err := writeProfileAtomically(path, contents); err != nil {
		return fmt.Errorf("profile: save %s: %w", path, err)
	}
	return nil
}

func encodeProfile(profile Profile) ([]byte, error) {
	if profile.Version != CurrentVersion {
		return nil, fmt.Errorf("profile: unsupported version %d", profile.Version)
	}
	if !profile.PlayerID.Valid() {
		return nil, errors.New("profile: invalid player ID")
	}
	name, err := core.NormalizeDisplayName(profile.DisplayName)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Version     uint32 `json:"version"`
		PlayerID    string `json:"player_id"`
		DisplayName string `json:"display_name"`
	}{profile.Version, profile.PlayerID.String(), name})
}

func decodeProfile(contents []byte) (Profile, error) {
	if !utf8.Valid(contents) {
		return Profile{}, errors.New("profile: JSON is not UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	token, err := decoder.Token()
	if err != nil {
		return Profile{}, fmt.Errorf("profile: JSON object: %w", err)
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return Profile{}, errors.New("profile: JSON value is not an object")
	}

	var (
		profile      Profile
		playerIDText string
		seenVersion  bool
		seenPlayerID bool
		seenName     bool
	)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return Profile{}, fmt.Errorf("profile: JSON field name: %w", err)
		}
		field, ok := token.(string)
		if !ok {
			return Profile{}, errors.New("profile: JSON object key is not a string")
		}
		switch field {
		case "version":
			if seenVersion {
				return Profile{}, errors.New("profile: duplicate version")
			}
			seenVersion = true
			if err := decoder.Decode(&profile.Version); err != nil {
				return Profile{}, fmt.Errorf("profile: version: %w", err)
			}
		case "player_id":
			if seenPlayerID {
				return Profile{}, errors.New("profile: duplicate player_id")
			}
			seenPlayerID = true
			if err := decoder.Decode(&playerIDText); err != nil {
				return Profile{}, fmt.Errorf("profile: player_id: %w", err)
			}
		case "display_name":
			if seenName {
				return Profile{}, errors.New("profile: duplicate display_name")
			}
			seenName = true
			if err := decoder.Decode(&profile.DisplayName); err != nil {
				return Profile{}, fmt.Errorf("profile: display_name: %w", err)
			}
		default:
			return Profile{}, fmt.Errorf("profile: unknown field %q", field)
		}
	}
	token, err = decoder.Token()
	if err != nil {
		return Profile{}, fmt.Errorf("profile: JSON object end: %w", err)
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '}' {
		return Profile{}, errors.New("profile: JSON object is not closed")
	}
	if token, err = decoder.Token(); err != io.EOF {
		if err != nil {
			return Profile{}, fmt.Errorf("profile: trailing JSON: %w", err)
		}
		return Profile{}, fmt.Errorf("profile: trailing JSON token %v", token)
	}
	if !seenVersion || !seenPlayerID || !seenName {
		return Profile{}, errors.New("profile: missing required field")
	}
	if profile.Version > CurrentVersion {
		return Profile{}, fmt.Errorf("profile: future version %d", profile.Version)
	}
	if profile.Version != CurrentVersion {
		return Profile{}, fmt.Errorf("profile: unsupported version %d", profile.Version)
	}
	playerID, err := core.ParsePlayerID(playerIDText)
	if err != nil {
		return Profile{}, fmt.Errorf("profile: player_id: %w", err)
	}
	profile.PlayerID = playerID
	profile.DisplayName, err = core.NormalizeDisplayName(profile.DisplayName)
	if err != nil {
		return Profile{}, err
	}
	return profile, nil
}
