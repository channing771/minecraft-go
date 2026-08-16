// Package lod 承载远环 LOD 壳的领域类型、请求编码与 quad 流解码。
//
// 远环壳由 Rust `mornlea_engine` 的 `mornlea_lod_shell` 独占生产(确定性
// 纯函数:同 perm + 同 tile + 同步长 → 全平台逐位一致输出);本包只负责
// 壳请求编码(复用 worldgen `MGW1` header + tile 尾部)、20 字节 LE quad
// 流解码与生成入口,不实现第二套 Go 壳算法。差分与结构 oracle 测试在
// 本包测试内以 `mornlea_worldgen_probe` 为对照(oracle 保留方案)。
// 远环 tile 的排队、预算化生成与上传调度由本包 Scheduler 与其 worker
// goroutine 承担(scheduler.go/worker.go/budget.go)。
package lod

import (
	"encoding/binary"
	"fmt"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/nativeabi"
)

// worldgenHeaderBytes 是与 `mornlea_worldgen_chunk` 完全一致的 `MGW1`
// header 字节数;由 ABI 输入总长减去 16 字节 tile 尾部推导,避免第二处
// 魔法数(internal/worldgen 的编码是权威实现,本常量只做长度契约)。
const worldgenHeaderBytes = nativeabi.LodShellInputBytes - 16

const (
	// TileColumns 是远环 tile 的固定列数(4×4 chunk = 64×64 列)。
	TileColumns = nativeabi.LodShellTileColumns
	// QuadBytes 是壳流中单个 quad 的编码字节数(x/z/y i32 + w/d u16 +
	// face u8 + material u16 + shade u8,LE)。
	QuadBytes = nativeabi.LodShellQuadBytes
)

const (
	// ShadeTop 是顶面着色权重:天空光满档 × 法线朝向权重 1.0。
	ShadeTop uint8 = 255
	// ShadeSideX 是 ±X 侧裙着色权重(0.6 × 255)。
	ShadeSideX uint8 = 153
	// ShadeSideZ 是 ±Z 侧裙着色权重(0.8 × 255),与 X 向取不同权重
	// 以保留斜坡的方向立体感;三值属于确定性契约的一部分。
	ShadeSideZ uint8 = 204
)

// Face 是壳面朝向:远环只有顶面与四向侧裙,无底面(纯地表壳)。
type Face uint8

const (
	// FaceTop 顶面,法线 +Y。
	FaceTop Face = 0
	// FaceNegX 侧裙,法线 −X(裙边属于其东侧的高窗口)。
	FaceNegX Face = 1
	// FacePosX 侧裙,法线 +X(裙边属于其西侧的高窗口)。
	FacePosX Face = 2
	// FaceNegZ 侧裙,法线 −Z。
	FaceNegZ Face = 3
	// FacePosZ 侧裙,法线 +Z。
	FacePosZ Face = 4
)

// ValidStep 报告 step 是否为合法列合并步长({2,4,8});64 对三者均整除,
// 窗口网格全局对齐,相邻 tile 的窗口边界重合。
func ValidStep(step uint32) bool {
	switch step {
	case 2, 4, 8:
		return true
	default:
		return false
	}
}

// Quad 是单个远环壳 quad:世界坐标大四边形,不复用近环 section 的
// 4-bit 局部编码(装不下远环的世界坐标大 quad)。字段语义与 engine
// `lod.rs` 的 `LodQuad` 逐字对应:顶面覆盖方块列 [X, X+W) × [Z, Z+D),
// 可见面平面 = Y+1;侧裙的 Y 为裙边最低方块行(低侧 top+1),竖直跨度
// D 恰好衔接两侧地表平面。
type Quad struct {
	// X 世界 X(block):顶面为覆盖区起始列;侧裙为裙边所在边界面的方块列。
	X int32
	// Z 世界 Z(block):顶面为覆盖区起始行;侧裙语义同 X 的 Z 向镜像。
	Z int32
	// Y 方块 Y:顶面为最高实心方块;侧裙为裙边最低方块行。
	Y int32
	// W 跨度(block):顶面为 X 跨度;侧裙为墙面水平跨度。
	W uint16
	// D 跨度(block):顶面为 Z 跨度;侧裙为竖直跨度(断差块数)。
	D uint16
	// Face 面朝向。
	Face Face
	// Material 材质 ID(最高列 worldgen 表层材质)。
	Material uint16
	// Shade 着色权重(ShadeTop/ShadeSideX/ShadeSideZ 之一)。
	Shade uint8
}

// AppendShellInput 把 worldgen `MGW1` header 与 tile 请求编码为
// `mornlea_lod_shell` 输入,追加到 dst 之后返回;输入布局与
// `mornlea_worldgen_chunk` 完全一致(header 原样透传 + tile_x/tile_z
// i32 + columns u32(固定 64)+ lod_step u32)。header 长度与步长违约
// 返回带上下文的错误;tile 原点范围由 engine 裁决(越界返回 INPUT)。
func AppendShellInput(dst []byte, header []byte, tile core.ChunkPos, step uint32) ([]byte, error) {
	if len(header) != worldgenHeaderBytes {
		return nil, fmt.Errorf("lod: header 长度 %d 非法，想要 %d", len(header), worldgenHeaderBytes)
	}
	if !ValidStep(step) {
		return nil, fmt.Errorf("lod: 步长 %d 非法，合法值 2/4/8", step)
	}
	dst = append(dst, header...)
	dst = binary.LittleEndian.AppendUint32(dst, uint32(tile.X))
	dst = binary.LittleEndian.AppendUint32(dst, uint32(tile.Z))
	dst = binary.LittleEndian.AppendUint32(dst, uint32(TileColumns))
	dst = binary.LittleEndian.AppendUint32(dst, step)
	return dst, nil
}

// GenerateShell 生成一个远环 tile 的壳 quad 字节流(未解码,可直接供
// 渲染上传消费)。header 与 AppendShellInput 同源;返回切片调用方只读。
// 请求编码失败返回错误;engine 侧失败(版本、tile 越界等编程错误)由
// nativeabi 绑定以稳定中文文案 panic,镜像 worldgen 生产路径。
func GenerateShell(header []byte, tile core.ChunkPos, step uint32) ([]byte, error) {
	input, err := AppendShellInput(nil, header, tile, step)
	if err != nil {
		return nil, fmt.Errorf("lod: 编码 tile (%d,%d) 壳请求失败: %w", tile.X, tile.Z, err)
	}
	return nativeabi.LodShell(input), nil
}

// DecodeQuads 把 `mornlea_lod_shell` 输出的 quad 字节流解码为 Quad 切片,
// 供校验与 oracle 测试消费(渲染上传直接使用原始字节流,不经解码)。
// 长度不是 QuadBytes 的倍数或 face 越界时返回带上下文的错误。
func DecodeQuads(shell []byte) ([]Quad, error) {
	if len(shell)%QuadBytes != 0 {
		return nil, fmt.Errorf("lod: 壳流长度 %d 不是 %d 的倍数", len(shell), QuadBytes)
	}
	quads := make([]Quad, len(shell)/QuadBytes)
	for index := range quads {
		offset := index * QuadBytes
		quad := &quads[index]
		quad.X = int32(binary.LittleEndian.Uint32(shell[offset:]))
		quad.Z = int32(binary.LittleEndian.Uint32(shell[offset+4:]))
		quad.Y = int32(binary.LittleEndian.Uint32(shell[offset+8:]))
		quad.W = binary.LittleEndian.Uint16(shell[offset+12:])
		quad.D = binary.LittleEndian.Uint16(shell[offset+14:])
		quad.Face = Face(shell[offset+16])
		quad.Material = binary.LittleEndian.Uint16(shell[offset+17:])
		quad.Shade = shell[offset+19]
		if quad.Face > FacePosZ {
			return nil, fmt.Errorf("lod: quad %d face=%d 超出 0..4", index, quad.Face)
		}
	}
	return quads, nil
}
