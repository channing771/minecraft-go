package lod

import (
	"encoding/binary"
	"slices"
	"testing"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/nativeabi"
)

// testHeader 构造与 engine lod.rs 单测同源的 `MGW1` header:seed 42、
// 恒等材料表 0..=13(末项 water=13,layout 2——变基后与 main 的注水
// worldgen 同一布局,水窗因此按 Ruling 22 钳制到海平面)、恒等 perm
// (与 nativeabi 测试的 testLodShellHeader 逐字节一致),保证 oracle
// 测试与 Rust golden fixture 输入严格同源。
func testHeader() []byte {
	header := make([]byte, 564)
	copy(header[:4], "MGW1")
	binary.LittleEndian.PutUint32(header[4:8], 2)
	binary.LittleEndian.PutUint64(header[8:16], 42)
	minY := int32(-64)
	binary.LittleEndian.PutUint32(header[16:20], uint32(minY))
	binary.LittleEndian.PutUint32(header[20:24], 320)
	for index := uint16(0); index < 14; index++ {
		binary.LittleEndian.PutUint16(header[24+2*int(index):26+2*int(index)], index)
	}
	for index := 0; index < 512; index++ {
		header[52+index] = byte(index & 255)
	}
	return header
}

func TestAppendShellInputEncodesTailLayout(t *testing.T) {
	input, err := AppendShellInput(nil, testHeader(), core.ChunkPos{X: -3, Z: 2}, 4)
	if err != nil {
		t.Fatalf("编码失败: %v", err)
	}
	if len(input) != nativeabi.LodShellInputBytes {
		t.Fatalf("输入长度 %d，想要 %d", len(input), nativeabi.LodShellInputBytes)
	}
	if got := input[:564]; !slices.Equal(got, testHeader()) {
		t.Fatal("header 未原样透传")
	}
	tileX := int32(binary.LittleEndian.Uint32(input[564:568]))
	tileZ := int32(binary.LittleEndian.Uint32(input[568:572]))
	columns := binary.LittleEndian.Uint32(input[572:576])
	step := binary.LittleEndian.Uint32(input[576:580])
	if tileX != -3 || tileZ != 2 || columns != uint32(TileColumns) || step != 4 {
		t.Fatalf("尾部字段 tile=(%d,%d) columns=%d step=%d，想要 (-3,2)/%d/4", tileX, tileZ, columns, step, TileColumns)
	}

	// dst 前缀保留:append 语义与既有 worldgen 编码一致。
	dst := []byte{0xaa, 0xbb}
	appended, err := AppendShellInput(dst, testHeader(), core.ChunkPos{X: 0, Z: 0}, 2)
	if err != nil || len(appended) != 2+nativeabi.LodShellInputBytes || appended[0] != 0xaa || appended[1] != 0xbb {
		t.Fatalf("append 语义失效: len=%d err=%v", len(appended), err)
	}
}

func TestAppendShellInputRejectsInvalidArguments(t *testing.T) {
	for _, tt := range []struct {
		name   string
		header []byte
		step   uint32
	}{
		{"short header", testHeader()[:563], 4},
		{"long header", append(slices.Clone(testHeader()), 0), 4},
		{"step zero", testHeader(), 0},
		{"step one", testHeader(), 1},
		{"step three", testHeader(), 3},
		{"step sixteen", testHeader(), 16},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := AppendShellInput(nil, tt.header, core.ChunkPos{}, tt.step); err == nil {
				t.Fatal("非法参数未被拒绝")
			}
		})
	}
	for _, step := range []uint32{2, 4, 8} {
		if _, err := AppendShellInput(nil, testHeader(), core.ChunkPos{}, step); err != nil {
			t.Fatalf("合法步长 %d 被拒绝: %v", step, err)
		}
	}
}

// encodeQuadForTest 按 engine encode_shell 的 20 字节 LE 布局手工编码。
func encodeQuadForTest(q Quad) []byte {
	encoded := make([]byte, QuadBytes)
	binary.LittleEndian.PutUint32(encoded[0:4], uint32(q.X))
	binary.LittleEndian.PutUint32(encoded[4:8], uint32(q.Z))
	binary.LittleEndian.PutUint32(encoded[8:12], uint32(q.Y))
	binary.LittleEndian.PutUint16(encoded[12:14], q.W)
	binary.LittleEndian.PutUint16(encoded[14:16], q.D)
	encoded[16] = byte(q.Face)
	binary.LittleEndian.PutUint16(encoded[17:19], q.Material)
	encoded[19] = q.Shade
	return encoded
}

func TestDecodeQuadsRoundTripsEncodedLayout(t *testing.T) {
	want := []Quad{
		{X: -192, Z: 128, Y: 70, W: 64, D: 64, Face: FaceTop, Material: 3, Shade: ShadeTop},
		{X: 7, Z: 0, Y: 65, W: 8, D: 16, Face: FacePosX, Material: 3, Shade: ShadeSideX},
		{X: 0, Z: 8, Y: 41, W: 4, D: 2, Face: FaceNegZ, Material: 6, Shade: ShadeSideZ},
		{X: 63, Z: 63, Y: 100, W: 2, D: 1, Face: FaceNegX, Material: 12, Shade: ShadeSideX},
		{X: 0, Z: 0, Y: 0, W: 1, D: 1, Face: FacePosZ, Material: 0, Shade: ShadeSideZ},
	}
	var stream []byte
	for _, q := range want {
		stream = append(stream, encodeQuadForTest(q)...)
	}
	got, err := DecodeQuads(stream)
	if err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	if !slices.EqualFunc(got, want, func(a, b Quad) bool { return a == b }) {
		t.Fatalf("解码结果 %+v != %+v", got, want)
	}
	if quads, err := DecodeQuads(nil); err != nil || len(quads) != 0 {
		t.Fatalf("空流解码=%d/%v，想要 0/nil", len(quads), err)
	}
}

func TestDecodeQuadsRejectsMalformedStreams(t *testing.T) {
	valid := encodeQuadForTest(Quad{X: 0, Z: 0, Y: 1, W: 2, D: 2, Face: FaceTop, Material: 1, Shade: ShadeTop})
	if _, err := DecodeQuads(valid[:19]); err == nil {
		t.Fatal("短流未被拒绝")
	}
	if _, err := DecodeQuads(append(slices.Clone(valid), 0)); err == nil {
		t.Fatal("长流未被拒绝")
	}
	badFace := slices.Clone(valid)
	badFace[16] = 5
	if _, err := DecodeQuads(badFace); err == nil {
		t.Fatal("非法 face 未被拒绝")
	}
}

func TestValidStepAndConstants(t *testing.T) {
	for _, step := range []uint32{2, 4, 8} {
		if !ValidStep(step) {
			t.Fatalf("合法步长 %d 被拒绝", step)
		}
	}
	for _, step := range []uint32{0, 1, 3, 5, 6, 7, 16, 64} {
		if ValidStep(step) {
			t.Fatalf("非法步长 %d 被接受", step)
		}
	}
	if TileColumns != nativeabi.LodShellTileColumns || QuadBytes != nativeabi.LodShellQuadBytes {
		t.Fatalf("常量与 ABI 不一致: columns=%d quadBytes=%d", TileColumns, QuadBytes)
	}
	if ShadeTop != 255 || ShadeSideX != 153 || ShadeSideZ != 204 {
		t.Fatalf("着色权重常量错误: %d/%d/%d", ShadeTop, ShadeSideX, ShadeSideZ)
	}
}
