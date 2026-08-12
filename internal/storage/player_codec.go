package storage

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"math"

	"minecraft-go/internal/core"
)

const (
	currentPlayerSchema   uint32 = 6
	playerEnvelopeVersion uint32 = 1
	playerEnvelopeLength         = 44
	maxPlayerPayload      uint32 = 1 << 20
	// legacyPlayerHotbarBytes 是 schema v2/v3 的固定快捷栏负载长度。
	legacyPlayerHotbarBytes = 1 + core.HotbarSlots*3
	// legacyPlayerBackpackBytes 是 schema v3 的固定背包负载长度。
	legacyPlayerBackpackBytes = core.BackpackSlots * 3
	// playerHotbarBytes 是 schema v4 的固定快捷栏负载长度。
	playerHotbarBytes = 1 + core.HotbarSlots*5
	// playerBackpackBytes 是 schema v4 的固定背包负载长度。
	playerBackpackBytes = core.BackpackSlots * 5
)

var (
	playerEnvelopeMagic = [4]byte{'M', 'C', 'P', 'L'}
	playerCRCTable      = crc32.MakeTable(crc32.Castagnoli)
)

// encodePlayer serializes one player as the stable MCPL v1 envelope.
func encodePlayer(save PlayerSave) ([]byte, error) {
	if err := validatePlayerSave(save); err != nil {
		return nil, err
	}
	payload, err := encodePlayerV5(save)
	if err != nil {
		return nil, err
	}
	if uint64(len(payload)) > uint64(maxPlayerPayload) {
		return nil, fmt.Errorf("%w: player payload exceeds %d bytes", ErrCorrupt, maxPlayerPayload)
	}
	encoded := make([]byte, 0, playerEnvelopeLength+len(payload))
	encoded = append(encoded, playerEnvelopeMagic[:]...)
	encoded = appendU32(encoded, playerEnvelopeVersion)
	encoded = appendU32(encoded, currentPlayerSchema)
	encoded = append(encoded, save.PlayerID[:]...)
	encoded = appendU64(encoded, save.Revision)
	encoded = appendU32(encoded, uint32(len(payload)))
	crcStart := len(encoded)
	encoded = appendU32(encoded, 0)
	hasher := crc32.New(playerCRCTable)
	_, _ = hasher.Write(encoded[8:crcStart])
	_, _ = hasher.Write(payload)
	binary.LittleEndian.PutUint32(encoded[crcStart:], hasher.Sum32())
	encoded = append(encoded, payload...)
	return encoded, nil
}

func decodePlayer(wantID core.PlayerID, data []byte) (StoredPlayer, error) {
	if !wantID.Valid() {
		return StoredPlayer{}, fmt.Errorf("%w: invalid requested player ID", ErrCorrupt)
	}
	envelope := byteDecoder{data: data}
	if err := envelope.magic(playerEnvelopeMagic); err != nil {
		return StoredPlayer{}, corrupt("player envelope magic", err)
	}
	version, err := envelope.u32()
	if err != nil {
		return StoredPlayer{}, corrupt("player envelope version", err)
	}
	if version != playerEnvelopeVersion {
		if version > playerEnvelopeVersion {
			return StoredPlayer{}, fmt.Errorf("%w: player envelope version %d", ErrFutureVersion, version)
		}
		return StoredPlayer{}, fmt.Errorf("%w: unsupported player envelope version %d", ErrCorrupt, version)
	}
	schema, err := envelope.u32()
	if err != nil {
		return StoredPlayer{}, corrupt("player schema", err)
	}
	if schema > currentPlayerSchema {
		return StoredPlayer{}, fmt.Errorf("%w: player schema %d", ErrFutureVersion, schema)
	}
	if schema < oldestPlayerSchema {
		return StoredPlayer{}, fmt.Errorf("%w: unsupported player schema %d", ErrCorrupt, schema)
	}
	encodedID, err := envelope.take(len(wantID))
	if err != nil {
		return StoredPlayer{}, corrupt("player ID", err)
	}
	var playerID core.PlayerID
	copy(playerID[:], encodedID)
	if !playerID.Valid() || playerID != wantID {
		return StoredPlayer{}, fmt.Errorf("%w: player ID does not match request", ErrCorrupt)
	}
	revision, err := envelope.u64()
	if err != nil {
		return StoredPlayer{}, corrupt("player revision", err)
	}
	if revision == 0 {
		return StoredPlayer{}, fmt.Errorf("%w: zero player revision", ErrCorrupt)
	}
	payloadLength, err := envelope.u32()
	if err != nil {
		return StoredPlayer{}, corrupt("player payload length", err)
	}
	if payloadLength > maxPlayerPayload {
		return StoredPlayer{}, fmt.Errorf("%w: player payload length %d exceeds limit", ErrCorrupt, payloadLength)
	}
	wantCRC, err := envelope.u32()
	if err != nil {
		return StoredPlayer{}, corrupt("player CRC32C", err)
	}
	if uint64(envelope.remaining()) != uint64(payloadLength) {
		return StoredPlayer{}, fmt.Errorf("%w: player payload length does not match envelope", ErrCorrupt)
	}
	payload, err := envelope.take(int(payloadLength))
	if err != nil {
		return StoredPlayer{}, corrupt("player payload", err)
	}
	hasher := crc32.New(playerCRCTable)
	_, _ = hasher.Write(data[8:40])
	_, _ = hasher.Write(payload)
	if hasher.Sum32() != wantCRC {
		return StoredPlayer{}, fmt.Errorf("%w: player CRC32C", ErrCorrupt)
	}
	dto, err := decodePlayerPayload(schema, playerID, revision, payload)
	if err != nil {
		return StoredPlayer{}, err
	}
	dto, migrated, err := migratePlayer(schema, dto)
	if err != nil {
		return StoredPlayer{}, err
	}
	if err := validatePlayerDTO(dto); err != nil {
		return StoredPlayer{}, err
	}
	stored := StoredPlayer{
		PlayerID: dto.PlayerID, Revision: dto.Revision, DisplayName: dto.DisplayName,
		Current: dto.Current, Yaw: dto.Yaw, Pitch: dto.Pitch, Inventory: dto.Inventory,
		Health:       dto.Health,
		NeedsRewrite: migrated,
	}
	if dto.Safe != nil {
		safe := *dto.Safe
		stored.Safe = &safe
	}
	return stored, nil
}

// encodePlayerV5 在 v4 负载末尾追加 1 字节生命值。
func encodePlayerV5(save PlayerSave) ([]byte, error) {
	payload, err := encodePlayerV4(save)
	if err != nil {
		return nil, err
	}
	return append(payload, save.Health), nil
}

// encodePlayerV4 把快捷栏与背包编码为包含耐久的固定负载。
func encodePlayerV4(save PlayerSave) ([]byte, error) {
	name := []byte(save.DisplayName)
	payload := make([]byte, 0,
		4+len(name)+4+3*4+4+4+1+4+3*4+playerHotbarBytes+playerBackpackBytes)
	payload = appendU32(payload, uint32(len(name)))
	payload = append(payload, name...)
	payload = appendPlayerLocation(payload, save.Current)
	payload = appendF32(payload, save.Yaw)
	payload = appendF32(payload, save.Pitch)
	if save.Safe == nil {
		payload = append(payload, 0)
	} else {
		payload = append(payload, 1)
		payload = appendPlayerLocation(payload, *save.Safe)
	}
	payload = appendPlayerHotbar(payload, save.Inventory.Hotbar)
	for _, stack := range save.Inventory.Backpack {
		payload = appendPlayerStack(payload, stack)
	}
	return payload, nil
}

func appendPlayerHotbar(dst []byte, hotbar core.Hotbar) []byte {
	dst = append(dst, hotbar.Selected)
	for _, stack := range hotbar.Slots {
		dst = appendPlayerStack(dst, stack)
	}
	return dst
}

func appendPlayerStack(dst []byte, stack core.ItemStack) []byte {
	dst = binary.LittleEndian.AppendUint16(dst, uint16(stack.Item))
	dst = append(dst, stack.Count)
	return binary.LittleEndian.AppendUint16(dst, stack.Durability)
}

func decodePlayerStack(decoder *byteDecoder) (core.ItemStack, error) {
	item, err := decoder.u16()
	if err != nil {
		return core.ItemStack{}, err
	}
	count, err := decoder.u8()
	if err != nil {
		return core.ItemStack{}, err
	}
	durability, err := decoder.u16()
	if err != nil {
		return core.ItemStack{}, err
	}
	return core.ItemStack{Item: core.ItemID(item), Count: count, Durability: durability}, nil
}

func decodeLegacyPlayerStack(decoder *byteDecoder) (core.ItemStack, error) {
	item, err := decoder.u16()
	if err != nil {
		return core.ItemStack{}, err
	}
	count, err := decoder.u8()
	if err != nil {
		return core.ItemStack{}, err
	}
	return core.ItemStack{Item: core.ItemID(item), Count: count}, nil
}

// decodePlayerPayload 按 schema 解析 payload；v1 没有快捷栏字段。
func decodePlayerPayload(
	schema uint32,
	playerID core.PlayerID,
	revision uint64,
	data []byte,
) (playerDTO, error) {
	switch schema {
	case 1:
		return decodePlayerV1(playerID, revision, data)
	case 2:
		return decodePlayerV2(playerID, revision, data)
	case 3:
		return decodePlayerV3(playerID, revision, data)
	case 4:
		return decodePlayerV4(playerID, revision, data)
	case 5, 6:
		return decodePlayerV5(playerID, revision, data)
	default:
		return playerDTO{}, fmt.Errorf("%w: unsupported player schema %d", ErrCorrupt, schema)
	}
}

// decodePlayerV5 在 v4 解析结果之上追加 1 字节生命值。
func decodePlayerV5(playerID core.PlayerID, revision uint64, data []byte) (playerDTO, error) {
	if len(data) < 1 {
		return playerDTO{}, fmt.Errorf("%w: player payload is shorter than health", ErrCorrupt)
	}
	split := len(data) - 1
	dto, err := decodePlayerV4(playerID, revision, data[:split])
	if err != nil {
		return playerDTO{}, err
	}
	dto.Health = data[split]
	return dto, nil
}

func decodePlayerV4(playerID core.PlayerID, revision uint64, data []byte) (playerDTO, error) {
	inventoryBytes := playerHotbarBytes + playerBackpackBytes
	if len(data) < inventoryBytes {
		return playerDTO{}, fmt.Errorf("%w: player payload is shorter than the inventory", ErrCorrupt)
	}
	split := len(data) - inventoryBytes
	dto, err := decodePlayerV1(playerID, revision, data[:split])
	if err != nil {
		return playerDTO{}, err
	}
	decoder := byteDecoder{data: data[split:]}
	selected, err := decoder.u8()
	if err != nil {
		return playerDTO{}, corrupt("player hotbar selection", err)
	}
	dto.Inventory.Hotbar.Selected = selected
	for index := range dto.Inventory.Hotbar.Slots {
		stack, err := decodePlayerStack(&decoder)
		if err != nil {
			return playerDTO{}, corrupt("player hotbar slot", err)
		}
		dto.Inventory.Hotbar.Slots[index] = stack
	}
	for index := range dto.Inventory.Backpack {
		stack, err := decodePlayerStack(&decoder)
		if err != nil {
			return playerDTO{}, corrupt("player backpack slot", err)
		}
		dto.Inventory.Backpack[index] = stack
	}
	return dto, nil
}

func decodePlayerV3(playerID core.PlayerID, revision uint64, data []byte) (playerDTO, error) {
	if len(data) < legacyPlayerBackpackBytes {
		return playerDTO{}, fmt.Errorf("%w: player payload is shorter than the backpack", ErrCorrupt)
	}
	split := len(data) - legacyPlayerBackpackBytes
	dto, err := decodePlayerV2(playerID, revision, data[:split])
	if err != nil {
		return playerDTO{}, err
	}
	decoder := byteDecoder{data: data[split:]}
	for index := range dto.Inventory.Backpack {
		stack, err := decodeLegacyPlayerStack(&decoder)
		if err != nil {
			return playerDTO{}, corrupt("player backpack slot", err)
		}
		dto.Inventory.Backpack[index] = stack
	}
	return dto, nil
}

func decodePlayerV2(playerID core.PlayerID, revision uint64, data []byte) (playerDTO, error) {
	if len(data) < legacyPlayerHotbarBytes {
		return playerDTO{}, fmt.Errorf("%w: player payload is shorter than the hotbar", ErrCorrupt)
	}
	split := len(data) - legacyPlayerHotbarBytes
	dto, err := decodePlayerV1(playerID, revision, data[:split])
	if err != nil {
		return playerDTO{}, err
	}
	decoder := byteDecoder{data: data[split:]}
	selected, err := decoder.u8()
	if err != nil {
		return playerDTO{}, corrupt("player hotbar selection", err)
	}
	dto.Inventory.Hotbar.Selected = selected
	for index := range dto.Inventory.Hotbar.Slots {
		stack, err := decodeLegacyPlayerStack(&decoder)
		if err != nil {
			return playerDTO{}, corrupt("player hotbar slot", err)
		}
		dto.Inventory.Hotbar.Slots[index] = stack
	}
	return dto, nil
}

func decodePlayerV1(playerID core.PlayerID, revision uint64, data []byte) (playerDTO, error) {
	decoder := byteDecoder{data: data}
	nameLength, err := decoder.u32()
	if err != nil {
		return playerDTO{}, corrupt("player name length", err)
	}
	if uint64(nameLength) > uint64(decoder.remaining()) {
		return playerDTO{}, fmt.Errorf("%w: player name length does not match payload", ErrCorrupt)
	}
	name, err := decoder.take(int(nameLength))
	if err != nil {
		return playerDTO{}, corrupt("player name", err)
	}
	current, err := decodePlayerLocation(&decoder)
	if err != nil {
		return playerDTO{}, corrupt("player current location", err)
	}
	yaw, err := decodeF32(&decoder)
	if err != nil {
		return playerDTO{}, corrupt("player yaw", err)
	}
	pitch, err := decodeF32(&decoder)
	if err != nil {
		return playerDTO{}, corrupt("player pitch", err)
	}
	hasSafe, err := decoder.u8()
	if err != nil {
		return playerDTO{}, corrupt("player safe flag", err)
	}
	dto := playerDTO{PlayerID: playerID, Revision: revision, DisplayName: string(name), Current: current, Yaw: yaw, Pitch: pitch}
	switch hasSafe {
	case 0:
	case 1:
		safe, err := decodePlayerLocation(&decoder)
		if err != nil {
			return playerDTO{}, corrupt("player safe location", err)
		}
		dto.Safe = &safe
	default:
		return playerDTO{}, fmt.Errorf("%w: invalid player safe flag %d", ErrCorrupt, hasSafe)
	}
	if decoder.remaining() != 0 {
		return playerDTO{}, fmt.Errorf("%w: trailing player payload bytes", ErrCorrupt)
	}
	return dto, nil
}

func appendPlayerLocation(dst []byte, location PlayerLocation) []byte {
	dst = appendU32(dst, uint32(location.Dimension))
	for _, position := range location.Position {
		dst = appendF32(dst, position)
	}
	return dst
}

func decodePlayerLocation(decoder *byteDecoder) (PlayerLocation, error) {
	dimension, err := decoder.u32()
	if err != nil {
		return PlayerLocation{}, err
	}
	location := PlayerLocation{Dimension: core.DimensionID(int32(dimension))}
	for index := range location.Position {
		position, err := decodeF32(decoder)
		if err != nil {
			return PlayerLocation{}, err
		}
		location.Position[index] = position
	}
	return location, nil
}

func appendF32(dst []byte, value float32) []byte {
	return binary.LittleEndian.AppendUint32(dst, math.Float32bits(value))
}

func decodeF32(decoder *byteDecoder) (float32, error) {
	bits, err := decoder.u32()
	if err != nil {
		return 0, err
	}
	return math.Float32frombits(bits), nil
}

func validatePlayerSave(save PlayerSave) error {
	return validatePlayerDTO(playerDTO{
		PlayerID: save.PlayerID, Revision: save.Revision, DisplayName: save.DisplayName,
		Current: save.Current, Yaw: save.Yaw, Pitch: save.Pitch, Safe: save.Safe,
		Inventory: save.Inventory, Health: save.Health,
	})
}

func validatePlayerDTO(dto playerDTO) error {
	if !dto.PlayerID.Valid() {
		return fmt.Errorf("%w: invalid player ID", ErrCorrupt)
	}
	if dto.Revision == 0 {
		return fmt.Errorf("%w: zero player revision", ErrCorrupt)
	}
	name, err := core.NormalizeDisplayName(dto.DisplayName)
	if err != nil || name != dto.DisplayName {
		return fmt.Errorf("%w: invalid player display name", ErrCorrupt)
	}
	if err := validatePlayerLocation(dto.Current); err != nil {
		return err
	}
	if !finitePlayerFloat(dto.Yaw) {
		return fmt.Errorf("%w: non-finite player yaw", ErrCorrupt)
	}
	if !finitePlayerFloat(dto.Pitch) || dto.Pitch < -math.Pi/2 || dto.Pitch > math.Pi/2 {
		return fmt.Errorf("%w: invalid player pitch", ErrCorrupt)
	}
	if dto.Safe != nil {
		if err := validatePlayerLocation(*dto.Safe); err != nil {
			return err
		}
	}
	if !dto.Inventory.Valid() {
		return fmt.Errorf("%w: invalid player inventory", ErrCorrupt)
	}
	if !core.ValidHealth(dto.Health) {
		return fmt.Errorf("%w: invalid player health", ErrCorrupt)
	}
	return nil
}

func validatePlayerLocation(location PlayerLocation) error {
	if location.Dimension != core.Overworld {
		return fmt.Errorf("%w: unsupported player dimension %d", ErrCorrupt, location.Dimension)
	}
	for _, position := range location.Position {
		if !finitePlayerFloat(position) {
			return fmt.Errorf("%w: non-finite player position", ErrCorrupt)
		}
	}
	return nil
}

func finitePlayerFloat(value float32) bool {
	return !math.IsNaN(float64(value)) && !math.IsInf(float64(value), 0)
}
