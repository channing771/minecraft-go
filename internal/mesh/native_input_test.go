package mesh

import (
	"encoding/binary"
	"reflect"
	"slices"
	"testing"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/world"
)

type snapshotTestReader struct {
	materialOffset uint16
}

func (*snapshotTestReader) Opaque(id world.BlockID) bool { return id == core.StoneID }

func (*snapshotTestReader) FaceVisible(id, adjacent world.BlockID) bool {
	return id == core.StoneID && adjacent == core.AirID
}

func (r *snapshotTestReader) Material(id world.BlockID, face Face) uint16 {
	return uint16(id)*100 + uint16(face) + r.materialOffset
}

func (*snapshotTestReader) Emission(id world.BlockID) uint8 {
	if id == core.GlassID {
		return 7
	}
	return 0
}

func TestBuildRegistrySnapshotSortsAndFreezesVisibility(t *testing.T) {
	snapshot, err := BuildRegistrySnapshot(
		[]world.BlockID{core.StoneID, core.AirID, core.GlassID},
		internalTestRegistry{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := []world.BlockID{snapshot.Blocks[0].ID, snapshot.Blocks[1].ID, snapshot.Blocks[2].ID}; !slices.Equal(got, []world.BlockID{core.AirID, core.StoneID, core.GlassID}) {
		t.Fatalf("snapshot IDs=%v", got)
	}
	if !snapshot.FaceVisible(core.StoneID, core.AirID) {
		t.Fatal("stone -> air 应可见")
	}
}

func TestBuildRegistrySnapshotRejectsDuplicateIDs(t *testing.T) {
	_, err := BuildRegistrySnapshot([]world.BlockID{core.AirID, core.AirID}, internalTestRegistry{})
	if err == nil {
		t.Fatal("重复 block ID 未被拒绝")
	}
}

func TestBuildRegistrySnapshotCopiesAndFreezesProperties(t *testing.T) {
	ids := []world.BlockID{core.StoneID, core.AirID, core.GlassID}
	reader := &snapshotTestReader{}
	snapshot, err := BuildRegistrySnapshot(ids, reader)
	if err != nil {
		t.Fatal(err)
	}
	reader.materialOffset = 50

	if !slices.Equal(ids, []world.BlockID{core.StoneID, core.AirID, core.GlassID}) {
		t.Fatalf("输入 IDs 被修改为 %v", ids)
	}
	wantBlocks := []BlockProperties{
		{ID: core.AirID, Materials: [6]uint16{0, 1, 2, 3, 4, 5}},
		{ID: core.StoneID, Opaque: true, Materials: [6]uint16{200, 201, 202, 203, 204, 205}},
		{ID: core.GlassID, Emission: 7, Materials: [6]uint16{2000, 2001, 2002, 2003, 2004, 2005}},
	}
	if !reflect.DeepEqual(snapshot.Blocks, wantBlocks) {
		t.Fatalf("snapshot blocks=%+v，想要 %+v", snapshot.Blocks, wantBlocks)
	}
	if !slices.Equal(snapshot.Visibility, []uint64{0, 1, 0}) {
		t.Fatalf("snapshot visibility=%v，想要 [0 1 0]", snapshot.Visibility)
	}
	// 这里用 MossyCobblestoneID+1（=WaterSourceID）不依赖它是否已注册：
	// 本用例的 snapshot 只装了 {Stone,Air,Glass} 三项，MossyCobblestoneID+1
	// 无论现在是否已注册为流体，始终不在这个 snapshot 的 Blocks 列表里，
	// RegistrySnapshot.FaceVisible 只按「是否在列表里」判定，与
	// core.RegisteredBlock 无关，「缺失 ID 不应有可见面」的断言依然成立。
	if snapshot.FaceVisible(core.MossyCobblestoneID+1, core.AirID) ||
		snapshot.FaceVisible(core.StoneID, core.MossyCobblestoneID+1) {
		t.Fatal("缺失 ID 不应有可见面")
	}
}

func TestBuildRegistrySnapshotRejectsEmissionAboveFifteen(t *testing.T) {
	_, err := BuildRegistrySnapshot(
		[]world.BlockID{core.AirID, core.BarrierID, core.LightBlockID},
		overbrightRegistry{},
	)
	if err == nil {
		t.Fatal("Emission=16 未被拒绝")
	}
}

func TestEncodeNativeInputUsesExactLittleEndianLayout(t *testing.T) {
	n := fullyLoadedAirNeighborhood()
	n.SectionY = 2
	n.Center.Blocks.Set(1, 2, 3, 0x1234)
	n.Around[1][1][1].Blocks.Set(1, 2, 3, 0x7777)
	n.Around[2][0][1] = nil
	n.HeightsPresent[2][0] = true
	n.Heights[2][0][5<<4|3] = -17
	snapshot := RegistrySnapshot{
		Blocks: []BlockProperties{
			{ID: core.AirID, Materials: [6]uint16{1, 2, 3, 4, 5, 6}},
			{ID: core.BarrierID, Opaque: true, Materials: [6]uint16{10, 11, 12, 13, 14, 15}},
			{ID: 40000, Emission: 7, Materials: [6]uint16{20, 21, 22, 23, 24, 25}},
		},
		Visibility: []uint64{2, 5, 1},
	}
	dst := make([]byte, 300000)
	length, err := encodeNativeInput(dst, n, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if length != 225889 {
		t.Fatalf("input length=%d，想要 225889", length)
	}
	if got := string(dst[0:4]); got != "MGM1" {
		t.Fatalf("magic=%q，想要 MGM1", got)
	}
	if got := int32(binary.LittleEndian.Uint32(dst[4:8])); got != -32 {
		t.Fatalf("sectionOriginY=%d，想要 -32", got)
	}
	if got := binary.LittleEndian.Uint16(dst[8:10]); got != 3 {
		t.Fatalf("registryCount=%d，想要 3", got)
	}
	if got := binary.LittleEndian.Uint16(dst[10:12]); got != 1 {
		t.Fatalf("registryWordsPerRow=%d，想要 1", got)
	}
	if got := binary.LittleEndian.Uint16(dst[12:14]); got != uint16(core.AirID) {
		t.Fatalf("airID=%d，想要 %d", got, core.AirID)
	}
	if got := binary.LittleEndian.Uint16(dst[14:16]); got != uint16(core.BarrierID) {
		t.Fatalf("barrierID=%d，想要 %d", got, core.BarrierID)
	}

	centerCell := 16 + (((1*3+1)*3+1)*4096+((2<<8)|(3<<4)|1))*2
	if got := binary.LittleEndian.Uint16(dst[centerCell:]); got != 0x1234 {
		t.Fatalf("center block=%#x，想要 0x1234", got)
	}
	missingCell := 16 + (((2*3+0)*3+1)*4096+((7<<8)|(8<<4)|9))*2
	if got := binary.LittleEndian.Uint16(dst[missingCell:]); got != uint16(core.BarrierID) {
		t.Fatalf("missing block=%d，想要 BarrierID", got)
	}

	const heightsPresentOffset = 221200
	if got := dst[heightsPresentOffset+2*3+0]; got != 1 {
		t.Fatalf("height presence=%d，想要 1", got)
	}
	const heightsOffset = 221209
	heightCell := heightsOffset + ((2*3+0)*256+(5<<4)+3)*2
	if got := int16(binary.LittleEndian.Uint16(dst[heightCell:])); got != -17 {
		t.Fatalf("height=%d，想要 -17", got)
	}

	const registryOffset = 225817
	if got := binary.LittleEndian.Uint16(dst[registryOffset+32:]); got != 40000 {
		t.Fatalf("third registry ID=%d，想要 40000", got)
	}
	if got := dst[registryOffset+35]; got != 7 {
		t.Fatalf("third registry emission=%d，想要 7", got)
	}
	if got := binary.LittleEndian.Uint16(dst[registryOffset+36:]); got != 20 {
		t.Fatalf("third registry material[0]=%d，想要 20", got)
	}
	if got := binary.LittleEndian.Uint64(dst[registryOffset+48+8:]); got != 5 {
		t.Fatalf("visibility row 1=%d，想要 5", got)
	}
}

// TestEncodeNativeInputAcceptsRegistryAtCapacity 断言编码器接受正好装满
// nativeMaxRegistryEntries 的快照，且末位 ID 允许远离连续区间（不假设 ID 连续）。
// 上限本身与 Rust 的 MAX_REGISTRY_ENTRIES 是否一致，由跨语言的 native parity
// 测试兜底：那里真的把 35 条 assets 快照喂进 Rust，两侧对不上会被直接拒绝。
func TestEncodeNativeInputAcceptsRegistryAtCapacity(t *testing.T) {
	blocks := make([]BlockProperties, nativeMaxRegistryEntries)
	for i := range nativeMaxRegistryEntries - 1 {
		blocks[i].ID = world.BlockID(i)
	}
	blocks[nativeMaxRegistryEntries-1].ID = 40000
	snapshot := RegistrySnapshot{Blocks: blocks, Visibility: make([]uint64, nativeMaxRegistryEntries)}
	if _, err := encodeNativeInput(make([]byte, 300000), fullyLoadedAirNeighborhood(), snapshot); err != nil {
		t.Fatalf("装满 %d 条的 snapshot 被拒绝: %v", nativeMaxRegistryEntries, err)
	}
}

func TestEncodeNativeInputZerosMissingHeightMaps(t *testing.T) {
	n := fullyLoadedAirNeighborhood()
	n.HeightsPresent[2][1] = false
	n.Heights[2][1][4<<4|3] = 123
	snapshot := (internalTestRegistry{}).MeshSnapshot()
	dst := make([]byte, maxNativeInputBytes)
	for i := range dst {
		dst[i] = 0xff
	}
	if _, err := encodeNativeInput(dst, n, snapshot); err != nil {
		t.Fatal(err)
	}
	const heightsOffset = 225817 - nativeHeightColumns*core.SectionSize*core.SectionSize*2
	heightCell := heightsOffset + ((2*3+1)*256+(4<<4)+3)*2
	if got := binary.LittleEndian.Uint16(dst[heightCell:]); got != 0 {
		t.Fatalf("缺失 height map 值=%d，想要 0", got)
	}
}

func TestEncodeNativeInputRejectsInvalidInputs(t *testing.T) {
	valid := RegistrySnapshot{
		Blocks:     []BlockProperties{{ID: core.AirID}, {ID: core.BarrierID}},
		Visibility: []uint64{0, 0},
	}
	tooMany := make([]BlockProperties, nativeMaxRegistryEntries+1)
	for i := range tooMany {
		tooMany[i].ID = world.BlockID(i)
	}
	tests := []struct {
		name     string
		dst      []byte
		n        *world.Neighborhood
		snapshot RegistrySnapshot
	}{
		{"nil neighborhood", make([]byte, 300000), nil, valid},
		{"nil center", make([]byte, 300000), &world.Neighborhood{}, valid},
		{"negative section", make([]byte, 300000), &world.Neighborhood{Center: world.NewSection(), SectionY: -1}, valid},
		{"high section", make([]byte, 300000), &world.Neighborhood{Center: world.NewSection(), SectionY: core.SectionsPerChunk}, valid},
		{"empty registry", make([]byte, 300000), fullyLoadedAirNeighborhood(), RegistrySnapshot{}},
		{"missing air", make([]byte, 300000), fullyLoadedAirNeighborhood(), RegistrySnapshot{Blocks: []BlockProperties{{ID: core.BarrierID}}, Visibility: []uint64{0}}},
		{"missing barrier", make([]byte, 300000), fullyLoadedAirNeighborhood(), RegistrySnapshot{Blocks: []BlockProperties{{ID: core.AirID}}, Visibility: []uint64{0}}},
		{"unsorted registry", make([]byte, 300000), fullyLoadedAirNeighborhood(), RegistrySnapshot{Blocks: []BlockProperties{{ID: core.BarrierID}, {ID: core.AirID}}, Visibility: []uint64{0, 0}}},
		{"duplicate registry", make([]byte, 300000), fullyLoadedAirNeighborhood(), RegistrySnapshot{Blocks: []BlockProperties{{ID: core.AirID}, {ID: core.AirID}, {ID: core.BarrierID}}, Visibility: []uint64{0, 0, 0}}},
		{"too many registry entries", make([]byte, 300000), fullyLoadedAirNeighborhood(), RegistrySnapshot{Blocks: tooMany, Visibility: make([]uint64, nativeMaxRegistryEntries+1)}},
		{"bad visibility size", make([]byte, 300000), fullyLoadedAirNeighborhood(), RegistrySnapshot{Blocks: valid.Blocks, Visibility: []uint64{0}}},
		{"overbright emission", make([]byte, 300000), fullyLoadedAirNeighborhood(), RegistrySnapshot{Blocks: []BlockProperties{{ID: core.AirID}, {ID: core.BarrierID, Emission: 16}}, Visibility: []uint64{0, 0}}},
		{"short destination", make([]byte, 16), fullyLoadedAirNeighborhood(), valid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := encodeNativeInput(tt.dst, tt.n, tt.snapshot); err == nil {
				t.Fatal("非法输入未被拒绝")
			}
		})
	}
}

func TestNativeInputValidAirNeighborhoodReturnsZeroQuads(t *testing.T) {
	n := fullyLoadedAirNeighborhood()
	status, count := callNativeForTest(t, n, (internalTestRegistry{}).MeshSnapshot())
	if status != nativeStatusOK || count != 0 {
		t.Fatalf("status=%v count=%d，想要 OK/0", status, count)
	}
}

func TestNativeInputStatusNumbersMatchABI(t *testing.T) {
	got := []nativeStatus{
		nativeStatusOK,
		nativeStatusABIVersion,
		nativeStatusInvalidArgument,
		nativeStatusInput,
		nativeStatusScratch,
		nativeStatusRegistry,
		nativeStatusEmission,
		nativeStatusOutputOverflow,
		nativeStatusQueueOverflow,
		nativeStatusPanic,
	}
	want := []nativeStatus{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	if !slices.Equal(got, want) {
		t.Fatalf("native status=%v，想要 %v", got, want)
	}
}

func callNativeForTest(t *testing.T, n *world.Neighborhood, snapshot RegistrySnapshot) (nativeStatus, int) {
	t.Helper()
	input := make([]byte, maxNativeInputBytes)
	length, err := encodeNativeInput(input, n, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	scratch := make([]uint64, (nativeScratchBytes+7)/8)
	output := make([]uint64, maxNativeQuads)
	return nativeMeshSection(input[:length], scratch, output)
}
