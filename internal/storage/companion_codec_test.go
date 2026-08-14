package storage

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
)

func fixtureCompanionID(last byte) companion.ID {
	return companion.ID{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x46, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, last}
}

func fixtureCompanionBodies() []companion.Body {
	stoneFull, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	ironFull, _ := core.ItemMaxDurability(core.ItemIronPickaxe)
	high := companion.Body{
		ID: fixtureCompanionID(2), Dimension: core.Overworld,
		Position: [3]float32{-12.5, 70, 3.25}, Yaw: 1.25, Pitch: -0.5,
	}
	high.Inventory.Hotbar.Selected = 4
	high.Inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 64}
	high.Inventory.Hotbar.Slots[4] = core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: stoneFull}
	high.Inventory.Backpack[0] = core.ItemStack{Item: core.ItemOakLog, Count: 7}

	low := companion.Body{
		ID: fixtureCompanionID(1), Dimension: core.Overworld,
		Position: [3]float32{8.5, 65, -9.75}, Yaw: -2.5, Pitch: 0.75,
	}
	low.Inventory.Hotbar.Selected = 2
	low.Inventory.Hotbar.Slots[2] = core.ItemStack{Item: core.ItemGlass, Count: 12}
	low.Inventory.Backpack[7] = core.ItemStack{Item: core.ItemIronPickaxe, Count: 1, Durability: ironFull}
	low.Inventory.Backpack[core.BackpackSlots-1] = core.ItemStack{Item: core.ItemDirt, Count: 5}
	return []companion.Body{high, low}
}

func TestCompanionCodecV1RoundTripAndGolden(t *testing.T) {
	if maxCompanionFileLength != 14176 {
		t.Fatalf("max companion file length=%d，想要 14176", maxCompanionFileLength)
	}
	input := fixtureCompanionBodies()
	before := append([]companion.Body(nil), input...)
	encoded, err := encodeCompanions(CompanionSave{Revision: 19, Records: input})
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) != 32+2*221 {
		t.Fatalf("encoded length=%d，想要 %d", len(encoded), 32+2*221)
	}
	if !reflect.DeepEqual(input, before) {
		t.Fatalf("编码修改调用者 records：got=%+v want=%+v", input, before)
	}
	wantFirstID := fixtureCompanionID(1)
	if !bytes.Equal(encoded[32:48], wantFirstID[:]) {
		t.Fatalf("首条 ID=%x，想要 canonical 最小 ID", encoded[32:48])
	}
	got, err := decodeCompanions(encoded)
	if err != nil {
		t.Fatal(err)
	}
	wantRecords := []companion.Body{input[1], input[0]}
	if got.Revision != 19 || !reflect.DeepEqual(got.Records, wantRecords) {
		t.Fatalf("decode=%+v，想要 revision=19 records=%+v", got, wantRecords)
	}

	path := filepath.Join("testdata", "companions-v1.bin")
	if *updateStorageFixtures {
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, encoded) {
		t.Fatal("companions v1 fixture drift；需要升级 schema")
	}
	expected := got
	expected.Records = append([]companion.Body(nil), got.Records...)
	clear(encoded)
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("修改输入 bytes 后 decode 结果=%+v，想要保持 %+v", got, expected)
	}
}

func TestCompanionCodecAcceptsMaximumStoredRecords(t *testing.T) {
	records := make([]companion.Body, companion.MaxStored)
	for index := range records {
		records[index].ID = fixtureCompanionID(byte(index))
	}
	encoded, err := encodeCompanions(CompanionSave{Revision: 23, Records: records})
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) != 14176 {
		t.Fatalf("64 条记录长度=%d，想要 14176", len(encoded))
	}
	got, err := decodeCompanions(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != 23 || len(got.Records) != 64 || !reflect.DeepEqual(got.Records, records) {
		t.Fatalf("64 条记录 decode=%+v", got)
	}
	if _, err := decodeCompanions(append(encoded, 0)); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("14,177-byte decode error=%v，想要 ErrCorrupt", err)
	}
}

func TestCompanionCodecRejectsCRCTruncationFutureVersionAndOversizedRecords(t *testing.T) {
	valid, err := encodeCompanions(CompanionSave{Revision: 7, Records: fixtureCompanionBodies()})
	if err != nil {
		t.Fatal(err)
	}
	badFloat := func(offset int) []byte {
		payload := bytes.Clone(valid)
		binary.LittleEndian.PutUint32(payload[offset:], math.Float32bits(float32(math.NaN())))
		repairCompanionCRC(payload)
		return payload
	}
	tests := []struct {
		name    string
		payload func() []byte
		want    error
	}{
		{"magic", func() []byte { p := bytes.Clone(valid); p[0] ^= 1; return p }, ErrCorrupt},
		{"old envelope", func() []byte { p := bytes.Clone(valid); binary.LittleEndian.PutUint32(p[4:], 0); return p }, ErrCorrupt},
		{"future envelope", func() []byte { p := bytes.Clone(valid); binary.LittleEndian.PutUint32(p[4:], 2); return p }, ErrFutureVersion},
		{"old schema", func() []byte { p := bytes.Clone(valid); binary.LittleEndian.PutUint32(p[8:], 0); return p }, ErrCorrupt},
		{"future schema", func() []byte { p := bytes.Clone(valid); binary.LittleEndian.PutUint32(p[8:], 2); return p }, ErrFutureVersion},
		{"zero revision", func() []byte { p := bytes.Clone(valid); clear(p[12:20]); repairCompanionCRC(p); return p }, ErrCorrupt},
		{"payload length", func() []byte { p := bytes.Clone(valid); binary.LittleEndian.PutUint32(p[24:], 1); return p }, ErrCorrupt},
		{"CRC", func() []byte { p := bytes.Clone(valid); p[28] ^= 1; return p }, ErrCorrupt},
		{"truncation", func() []byte { return bytes.Clone(valid[:len(valid)-1]) }, ErrCorrupt},
		{"trailing byte", func() []byte { return append(bytes.Clone(valid), 0) }, ErrCorrupt},
		{"invalid ID", func() []byte { p := bytes.Clone(valid); clear(p[32:48]); repairCompanionCRC(p); return p }, ErrCorrupt},
		{"invalid dimension", func() []byte {
			p := bytes.Clone(valid)
			binary.LittleEndian.PutUint32(p[48:], 1)
			repairCompanionCRC(p)
			return p
		}, ErrCorrupt},
		{"position", func() []byte { return badFloat(52) }, ErrCorrupt},
		{"yaw", func() []byte { return badFloat(64) }, ErrCorrupt},
		{"pitch", func() []byte { return badFloat(68) }, ErrCorrupt},
		{"pitch outside range", func() []byte {
			p := bytes.Clone(valid)
			binary.LittleEndian.PutUint32(p[68:], math.Float32bits(2))
			repairCompanionCRC(p)
			return p
		}, ErrCorrupt},
		{"selected slot", func() []byte { p := bytes.Clone(valid); p[72] = core.HotbarSlots; repairCompanionCRC(p); return p }, ErrCorrupt},
		{"invalid inventory", func() []byte {
			p := bytes.Clone(valid)
			binary.LittleEndian.PutUint16(p[73:], 4242)
			p[75] = 1
			repairCompanionCRC(p)
			return p
		}, ErrCorrupt},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeCompanions(tc.payload())
			if !errors.Is(err, tc.want) {
				t.Fatalf("decode error=%v，想要 %v", err, tc.want)
			}
		})
	}

	oversized := make([]byte, 32)
	copy(oversized, "MCAI")
	binary.LittleEndian.PutUint32(oversized[4:], 1)
	binary.LittleEndian.PutUint32(oversized[8:], 1)
	binary.LittleEndian.PutUint64(oversized[12:], 1)
	binary.LittleEndian.PutUint32(oversized[20:], companion.MaxStored+1)
	binary.LittleEndian.PutUint32(oversized[24:], (companion.MaxStored+1)*221)
	_, err = decodeCompanions(oversized)
	if !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), "count") {
		t.Fatalf("oversized count error=%v，想要分配前 count 门禁", err)
	}

	tooMany := make([]companion.Body, companion.MaxStored+1)
	for i := range tooMany {
		tooMany[i] = fixtureCompanionBodies()[0]
	}
	if _, err := encodeCompanions(CompanionSave{Revision: 1, Records: tooMany}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("encode 65 records error=%v，想要 ErrCorrupt", err)
	}
	if _, err := encodeCompanions(CompanionSave{Records: fixtureCompanionBodies()[:1]}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("encode zero revision error=%v，想要 ErrCorrupt", err)
	}
	invalidBody := fixtureCompanionBodies()[0]
	invalidBody.Position[0] = float32(math.Inf(1))
	if _, err := encodeCompanions(CompanionSave{Revision: 1, Records: []companion.Body{invalidBody}}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("encode invalid body error=%v，想要 ErrCorrupt", err)
	}
}

func TestCompanionCodecRejectsDuplicateOrUnsortedIDs(t *testing.T) {
	valid, err := encodeCompanions(CompanionSave{Revision: 3, Records: fixtureCompanionBodies()})
	if err != nil {
		t.Fatal(err)
	}
	duplicate := bytes.Clone(valid)
	copy(duplicate[32+221:32+221+16], duplicate[32:48])
	repairCompanionCRC(duplicate)
	unsorted := bytes.Clone(valid)
	first := bytes.Clone(unsorted[32:48])
	copy(unsorted[32:48], unsorted[32+221:32+221+16])
	copy(unsorted[32+221:32+221+16], first)
	repairCompanionCRC(unsorted)
	for name, payload := range map[string][]byte{"duplicate": duplicate, "unsorted": unsorted} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeCompanions(payload); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("decode error=%v，想要 ErrCorrupt", err)
			}
		})
	}

	input := fixtureCompanionBodies()
	input[1].ID = input[0].ID
	if _, err := encodeCompanions(CompanionSave{Revision: 1, Records: input}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("encode duplicate error=%v，想要 ErrCorrupt", err)
	}
}

func TestCompanionCodecDoesNotPersistNameTaskOrPersona(t *testing.T) {
	encoded, err := encodeCompanions(CompanionSave{Revision: 5, Records: fixtureCompanionBodies()[:1]})
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) != 32+221 {
		t.Fatalf("单记录文件长度=%d，想要固定 %d；v1 不应包含名称、任务或 persona", len(encoded), 32+221)
	}
	for _, forbidden := range [][]byte{[]byte("阿木"), []byte("挖石头"), []byte("persona")} {
		if bytes.Contains(encoded, forbidden) {
			t.Fatalf("v1 存档包含禁止字段 %q", forbidden)
		}
	}
}

func TestCompanionCodecV1PreservesWornToolDurability(t *testing.T) {
	body := fixtureCompanionBodies()[0]
	body.Inventory.Hotbar.Slots[4].Durability = 73
	body.Inventory.Backpack[3] = core.ItemStack{Item: core.ItemIronPickaxe, Count: 1, Durability: 149}
	encoded, err := encodeCompanions(CompanionSave{Revision: 9, Records: []companion.Body{body}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeCompanions(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Records) != 1 || got.Records[0].Inventory != body.Inventory {
		t.Fatalf("磨损工具往返 inventory=%+v，想要 %+v", got.Records, body.Inventory)
	}
}

func repairCompanionCRC(payload []byte) {
	hasher := crc32.New(crc32.MakeTable(crc32.Castagnoli))
	_, _ = hasher.Write(payload[8:28])
	_, _ = hasher.Write(payload[32:])
	binary.LittleEndian.PutUint32(payload[28:], hasher.Sum32())
}
