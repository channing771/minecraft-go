package core

import (
	"encoding/hex"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// PlayerID 是稳定的 UUIDv4 玩家标识。
type PlayerID [16]byte

// ParsePlayerID 只接受标准、小写的 UUIDv4 文本形式。
func ParsePlayerID(value string) (PlayerID, error) {
	if len(value) != 36 {
		return PlayerID{}, errors.New("core: player ID is not canonical UUID text")
	}
	for _, index := range []int{8, 13, 18, 23} {
		if value[index] != '-' {
			return PlayerID{}, errors.New("core: player ID is not canonical UUID text")
		}
	}
	for index := range value {
		if value[index] == '-' && index != 8 && index != 13 && index != 18 && index != 23 {
			return PlayerID{}, errors.New("core: player ID is not canonical UUID text")
		}
	}
	encoded := value[:8] + value[9:13] + value[14:18] + value[19:23] + value[24:]
	if len(encoded) != 32 {
		return PlayerID{}, errors.New("core: player ID is not canonical UUID text")
	}
	if strings.ToLower(encoded) != encoded {
		return PlayerID{}, errors.New("core: player ID is not lowercase")
	}
	var id PlayerID
	decoded, err := hex.Decode(id[:], []byte(encoded))
	if err != nil || decoded != len(id) {
		return PlayerID{}, errors.New("core: player ID is not hexadecimal")
	}
	if !id.Valid() {
		return PlayerID{}, errors.New("core: player ID is not UUIDv4")
	}
	return id, nil
}

// String 返回 UUID 的标准小写文本形式。
func (id PlayerID) String() string {
	encoded := hex.EncodeToString(id[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

// Valid 报告该 ID 是否为非零 UUIDv4。
func (id PlayerID) Valid() bool {
	return id != (PlayerID{}) && id[6]>>4 == 4 && id[8]&0xc0 == 0x80
}

// NormalizeDisplayName 去除首尾空白并验证显示昵称。
func NormalizeDisplayName(name string) (string, error) {
	if !utf8.ValidString(name) {
		return "", errors.New("core: display name is not UTF-8")
	}
	name = strings.TrimSpace(name)
	count := utf8.RuneCountInString(name)
	if count < 1 || count > 32 || len(name) > 128 {
		return "", errors.New("core: display name length is outside 1..32")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", errors.New("core: display name contains control character")
		}
	}
	return name, nil
}
