package lod

// 本文件是远环壳的 oracle 差分与结构测试(oracle 保留方案,不写第二套
// Go 壳 mesh 实现):(a) 高度差分——以 mornlea_worldgen_probe 逐列采样,
// 断言壳窗高 == 窗内 max(worldgen 列高);(b) 结构性质——每个 tile 的
// 顶面恰好覆盖全部列一次,断差处裙边闭合(含 tile 边界,经相邻 tile 的
// 裙边共同闭合);(c) 与 engine 侧确定性 golden fixture 逐字节一致。

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/nativeabi"
)

// probeBatchLimit 是 engine worldgen probe 单批最大记录数(64)。
const probeBatchLimit = 64

// testAirMaterial 是恒等材料表的 air 项(第 0 项)。生产 worldgen 高度域
// 不会产出 air 窗口;air 分支只是语义完备,测试中仅防御性跳过。
const testAirMaterial = uint16(0)

// probeRecord 是一条 worldgen 单点查询记录:mode + wx/wy/wz。
type probeRecord struct {
	mode    uint32
	x, y, z int32
}

// testProbe 批量执行 worldgen 单点查询,返回每条 8 字节结果记录
// (height i32 | block u16 | reserved u16);native 失败以 panic 收敛。
func testProbe(t *testing.T, header []byte, records []probeRecord) [][8]byte {
	t.Helper()
	results := make([][8]byte, 0, len(records))
	for start := 0; start < len(records); start += probeBatchLimit {
		end := min(start+probeBatchLimit, len(records))
		input := slices.Clone(header)
		input = binary.LittleEndian.AppendUint32(input, uint32(end-start))
		for _, record := range records[start:end] {
			input = binary.LittleEndian.AppendUint32(input, record.mode)
			input = binary.LittleEndian.AppendUint32(input, uint32(record.x))
			input = binary.LittleEndian.AppendUint32(input, uint32(record.y))
			input = binary.LittleEndian.AppendUint32(input, uint32(record.z))
		}
		output := make([]byte, 8*(end-start))
		nativeabi.WorldgenProbe(input, output)
		for index := range end - start {
			var record [8]byte
			copy(record[:], output[index*8:(index+1)*8])
			results = append(results, record)
		}
	}
	return results
}

// testWindow 是 oracle 侧的步长窗聚合值(top + 表层材质)。
type testWindow struct {
	top      int32
	material uint16
}

// testField 是 tile 的窗口场(含边界外一圈),以全局窗口坐标 gi/gj ∈
// −1..=n 索引;只作 oracle 对照物,不是生产类型。
type testField struct {
	step         int32
	baseX, baseZ int32
	n            int
	cells        []testWindow
}

// window 取全局窗口坐标 (gi, gj) 处的聚合值;调用方保证范围。
func (f *testField) window(gi, gj int) testWindow {
	return f.cells[(gj+1)*(f.n+2)+(gi+1)]
}

// testSampleField 以 worldgen probe 采样窗口场,复刻 engine sample_window
// 的 oracle 定义:窗高 = 窗内截断列高 max(逐列截断到 MaxY−1,与
// generate_chunk 的写入高度一致),材质 = 首个达到 max 的列(z 外层、
// x 内层扫描序)的 worldgen 表层材质。并列最高取首个达到 max 的列是
// 确定性契约的一部分,不是对实现细节的偶然复制。
func testSampleField(t *testing.T, header []byte, tile core.ChunkPos, step uint32) *testField {
	t.Helper()
	n := int(TileColumns / step)
	s := int32(step)
	baseX, baseZ := tile.X*TileColumns, tile.Z*TileColumns

	// 1) 全场列高度采样:窗口 gi,gj ∈ −1..=n,窗内列 (lx,lz) ∈ 0..s。
	type columnRef struct {
		x, z int32
		cell int // 所属窗口在 cells 中的下标
	}
	columns := make([]columnRef, 0, (n+2)*(n+2)*int(s)*int(s))
	for gj := -1; gj <= n; gj++ {
		for gi := -1; gi <= n; gi++ {
			for lz := int32(0); lz < s; lz++ {
				for lx := int32(0); lx < s; lx++ {
					columns = append(columns, columnRef{
						x:    baseX + int32(gi)*s + lx,
						z:    baseZ + int32(gj)*s + lz,
						cell: (gj+1)*(n+2) + (gi + 1),
					})
				}
			}
		}
	}
	heightRecords := make([]probeRecord, len(columns))
	for index, column := range columns {
		heightRecords[index] = probeRecord{mode: 0, x: column.x, y: 0, z: column.z}
	}
	heights := testProbe(t, header, heightRecords)

	// 2) 每窗取 max:严格大于才刷新(首个达到 max 的列胜出)。初始
	// 哨兵取 i32 最小值,与 engine sample_window 一致——若从 0 起算,
	// 全 0 高度窗会被误判为从未刷新。
	type winner struct {
		top       int32
		x, z      int32
		refreshed bool
	}
	winners := make([]winner, (n+2)*(n+2))
	for index := range winners {
		winners[index].top = math.MinInt32
	}
	for index, column := range columns {
		height := int32(binary.LittleEndian.Uint32(heights[index][0:4]))
		if height >= core.MaxY {
			height = core.MaxY - 1
		}
		w := &winners[column.cell]
		if height > w.top {
			*w = winner{top: height, x: column.x, z: column.z, refreshed: true}
		}
	}

	// 3) 各窗 argmax 列的表层材质(mode 1 = 不叠加橡树的地形方块);
	// 未刷新的空窗口与 engine 一致取 air,不发起材质查询。
	cells := make([]testWindow, len(winners))
	type materialRef struct {
		cell int
		x, z int32
		y    int32
	}
	pending := make([]materialRef, 0, len(winners))
	for cell, w := range winners {
		if w.refreshed {
			pending = append(pending, materialRef{cell: cell, x: w.x, y: w.top, z: w.z})
		} else {
			cells[cell] = testWindow{top: math.MinInt32, material: testAirMaterial}
		}
	}
	materialRecords := make([]probeRecord, len(pending))
	for index, ref := range pending {
		materialRecords[index] = probeRecord{mode: 1, x: ref.x, y: ref.y, z: ref.z}
	}
	for index, record := range testProbe(t, header, materialRecords) {
		cells[pending[index].cell] = testWindow{
			top:      winners[pending[index].cell].top,
			material: binary.LittleEndian.Uint16(record[4:6]),
		}
	}

	// 海平面钳制镜像(Ruling 22,与 engine clamp_window_to_sea_level 同一
	// 规则):有地表且固体顶面低于海平面的窗口,顶面钳到海平面、材质取
	// header 材料表的 water(偏移 50);门控编码(water == air,偏移 24)
	// 时整体跳过,与注水门控引入前的行为逐位一致。空窗口(哨兵未刷新)
	// 不钳制。
	const seaLevelY = 64
	airMaterial := binary.LittleEndian.Uint16(header[24:26])
	waterMaterial := binary.LittleEndian.Uint16(header[50:52])
	if waterMaterial != airMaterial {
		for cell, window := range cells {
			if window.top != math.MinInt32 && window.top < seaLevelY {
				cells[cell] = testWindow{top: seaLevelY, material: waterMaterial}
			}
		}
	}
	return &testField{step: s, baseX: baseX, baseZ: baseZ, n: n, cells: cells}
}

// testGenerateQuads 生成 tile 壳并解码为 Quad 流(差分/结构测试的公共入口)。
func testGenerateQuads(t *testing.T, header []byte, tile core.ChunkPos, step uint32) []Quad {
	t.Helper()
	shell, err := GenerateShell(header, tile, step)
	if err != nil {
		t.Fatalf("生成 tile (%d,%d) step %d 失败: %v", tile.X, tile.Z, step, err)
	}
	quads, err := DecodeQuads(shell)
	if err != nil {
		t.Fatalf("解码 tile (%d,%d) step %d 失败: %v", tile.X, tile.Z, step, err)
	}
	return quads
}

// oracleTileCases 是差分与结构测试共用的 tile×step 组合:默认档全覆盖,
// 另加一个正负混合坐标 tile 抽查。
var oracleTileCases = []struct {
	tile core.ChunkPos
	step uint32
}{
	{tile: core.ChunkPos{X: -3, Z: 2}, step: 2},
	{tile: core.ChunkPos{X: -3, Z: 2}, step: 4},
	{tile: core.ChunkPos{X: -3, Z: 2}, step: 8},
	{tile: core.ChunkPos{X: 7, Z: -5}, step: 4},
}

// TestShellMatchesEngineGoldenBytes 把 Go FFI 输出与 engine 侧确定性
// golden fixture 逐字节比对:同输入跨平台逐位一致的直接证据。fixture
// 与 engine lod.rs 的 golden_shell_bytes_are_stable 同源(seed 42、恒等
// perm、恒等材料表 0..=13、tile(−3,2)、step 4);v2 为 Ruling 22 海平面
// 钳制后的定版(v1 是钳制前、layout 1 输入的快照)。
func TestShellMatchesEngineGoldenBytes(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("无法定位 lod 测试文件")
	}
	goldenPath := filepath.Join(filepath.Dir(file), "..", "..",
		"engine", "crates", "mornlea_engine", "testdata", "lod-shell-seed42-step4-v2.bin")
	golden, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("读取 engine golden fixture 失败: %v", err)
	}
	shell, err := GenerateShell(testHeader(), core.ChunkPos{X: -3, Z: 2}, 4)
	if err != nil {
		t.Fatalf("生成壳失败: %v", err)
	}
	if !slices.Equal(shell, golden) {
		t.Fatalf("壳输出 %d 字节与 golden %d 字节不一致", len(shell), len(golden))
	}
	if len(golden)%QuadBytes != 0 || len(golden) == 0 {
		t.Fatalf("golden 长度 %d 非法", len(golden))
	}
}

// TestShellTopsCoverColumnsAndMatchProbeOracle 是 oracle 保留方案的差分
// 门禁:顶面 quad 重建的窗口网格必须与 probe 采样的窗口场逐窗一致
// (窗高 == 窗内 max 列高,材质 == 最高列表层材质),且顶面恰好覆盖
// 全部 64×64 列一次(贪心合并不重不漏)。
func TestShellTopsCoverColumnsAndMatchProbeOracle(t *testing.T) {
	header := testHeader()
	for _, tc := range oracleTileCases {
		t.Run(fmt.Sprintf("tile(%d,%d)/step%d", tc.tile.X, tc.tile.Z, tc.step), func(t *testing.T) {
			quads := testGenerateQuads(t, header, tc.tile, tc.step)
			field := testSampleField(t, header, tc.tile, tc.step)
			s := field.step

			coverage := make([]int, TileColumns*TileColumns)
			tops := 0
			for _, quad := range quads {
				if quad.Face != FaceTop {
					continue
				}
				tops++
				if quad.Shade != ShadeTop {
					t.Fatalf("顶面着色 %d != %d", quad.Shade, ShadeTop)
				}
				if quad.X < field.baseX || quad.Z < field.baseZ ||
					quad.X+int32(quad.W) > field.baseX+TileColumns ||
					quad.Z+int32(quad.D) > field.baseZ+TileColumns ||
					(quad.X-field.baseX)%s != 0 || (quad.Z-field.baseZ)%s != 0 ||
					quad.W%uint16(s) != 0 || quad.D%uint16(s) != 0 {
					t.Fatalf("顶面 quad 未按窗口网格对齐或越界: %+v", quad)
				}
				for dz := int32(0); dz < int32(quad.D); dz++ {
					for dx := int32(0); dx < int32(quad.W); dx++ {
						wx, wz := quad.X+dx, quad.Z+dz
						coverage[(wz-field.baseZ)*TileColumns+(wx-field.baseX)]++
						window := field.window(int((wx-field.baseX)/s), int((wz-field.baseZ)/s))
						if window.top != quad.Y || window.material != quad.Material {
							t.Fatalf("列 (%d,%d) 窗口值 (top=%d,mat=%d) != 顶面 quad (y=%d,mat=%d)",
								wx, wz, window.top, window.material, quad.Y, quad.Material)
						}
					}
				}
			}
			if tops == 0 {
				t.Fatal("壳流中没有顶面 quad")
			}
			for index, count := range coverage {
				if count != 1 {
					z, x := index/TileColumns, index%TileColumns
					t.Fatalf("列 (%d,%d) 被顶面覆盖 %d 次,想要恰好 1", field.baseX+int32(x), field.baseZ+int32(z), count)
				}
			}
		})
	}
}

// TestSeaLevelClampAssertsWaterWindowSemantics 是 Ruling 22 的高度差分
// 专项:以 worldgen probe 的原始列高(mode 0)为独立 oracle——不走
// testSampleField 的钳制镜像——对每个内部窗口计算未钳制的固体 max:
// max < 海平面的海盆窗在壳顶面里必须呈现为钳制后的 (海平面, 水材质),
// max >= 海平面的陆地窗保持探针高度不被抬升。全部 tile 的海盆窗总数
// 必须非零,防止夹具漂移让断言空转。
func TestSeaLevelClampAssertsWaterWindowSemantics(t *testing.T) {
	header := testHeader()
	airMaterial := binary.LittleEndian.Uint16(header[24:26])
	waterMaterial := binary.LittleEndian.Uint16(header[50:52])
	if waterMaterial == airMaterial {
		t.Fatal("夹具必须启用流体:water 材质与 air 相同(门控关闭态)")
	}
	const seaLevelY = 64
	basinWindows := 0
	for _, tc := range oracleTileCases {
		t.Run(fmt.Sprintf("tile(%d,%d)/step%d", tc.tile.X, tc.tile.Z, tc.step), func(t *testing.T) {
			s := int32(tc.step)
			baseX, baseZ := tc.tile.X*TileColumns, tc.tile.Z*TileColumns
			n := int(TileColumns / tc.step)

			// 1) 原始列高探针:内部窗口覆盖的全部列(不含边界外一圈,
			// 本测试只断言内部窗的顶面值)。
			type columnRef struct {
				x, z int32
				cell int
			}
			columns := make([]columnRef, 0, n*n*int(s)*int(s))
			for gj := 0; gj < n; gj++ {
				for gi := 0; gi < n; gi++ {
					for lz := int32(0); lz < s; lz++ {
						for lx := int32(0); lx < s; lx++ {
							columns = append(columns, columnRef{
								x:    baseX + int32(gi)*s + lx,
								z:    baseZ + int32(gj)*s + lz,
								cell: gj*n + gi,
							})
						}
					}
				}
			}
			heightRecords := make([]probeRecord, len(columns))
			for index, column := range columns {
				heightRecords[index] = probeRecord{mode: 0, x: column.x, y: 0, z: column.z}
			}
			rawMax := make([]int32, n*n)
			for index, record := range testProbe(t, header, heightRecords) {
				height := int32(binary.LittleEndian.Uint32(record[0:4]))
				if height >= core.MaxY {
					height = core.MaxY - 1
				}
				cell := columns[index].cell
				if height > rawMax[cell] {
					rawMax[cell] = height
				}
			}

			// 2) 壳顶面重建列 → 窗值映射(顶面贪心合并不重不漏,每列恰一次)。
			windowTop := make([]int32, n*n)
			windowMaterial := make([]uint16, n*n)
			for _, quad := range testGenerateQuads(t, header, tc.tile, tc.step) {
				if quad.Face != FaceTop {
					continue
				}
				for dz := int32(0); dz < int32(quad.D); dz++ {
					for dx := int32(0); dx < int32(quad.W); dx++ {
						wx, wz := quad.X+dx, quad.Z+dz
						cell := int((wz-baseZ)/s)*n + int((wx-baseX)/s)
						windowTop[cell] = quad.Y
						windowMaterial[cell] = quad.Material
					}
				}
			}

			// 3) 差分断言:海盆窗(原始 max < 海平面)→ (海平面, 水);陆地窗
			// → 原始 max,且不得被替换成水材质(顶面恰在海平面的陆地窗除外:
			// 其材质是地形表层,可能巧合等于水编号以外的任意地形值,断言只锁
			// 高度与「非水」)。
			for cell, want := range rawMax {
				if want < seaLevelY {
					basinWindows++
					if windowTop[cell] != seaLevelY || windowMaterial[cell] != waterMaterial {
						t.Fatalf("海盆窗 %d 原始 max=%d,壳窗值=(%d,%d),想要 (%d,%d)",
							cell, want, windowTop[cell], windowMaterial[cell], seaLevelY, waterMaterial)
					}
				} else if windowTop[cell] != want {
					t.Fatalf("陆地窗 %d 原始 max=%d,壳窗高=%d(钳制不得抬升陆地)",
						cell, want, windowTop[cell])
				} else if want == seaLevelY && windowMaterial[cell] == waterMaterial {
					t.Fatalf("陆地窗 %d 顶面恰在海平面却被替换成水材质", cell)
				}
			}
		})
	}
	if basinWindows == 0 {
		t.Fatal("夹具失效:全部 tile 都没有海盆窗,钳制断言空转")
	}
}

// skirtKey 索引一条裙边:X 向以 (可见平面 x, 行起点 z) 为键,Z 向以
// (可见平面 z, 列起点 x) 为键;键使用世界坐标,跨 tile 唯一。
type skirtKey struct {
	plane int32
	row   int32
}

// testSkirtIndex 为一组 tile 的壳建立裙边索引,并对每条裙边做窗口网格
// 对齐校验(平面/行落在步长网格上、水平跨度恰为一个步长)。
func testSkirtIndex(t *testing.T, header []byte, tiles []core.ChunkPos, step uint32) (xSkirts, zSkirts map[skirtKey][]Quad) {
	t.Helper()
	xSkirts = make(map[skirtKey][]Quad)
	zSkirts = make(map[skirtKey][]Quad)
	s := int32(step)
	for _, tile := range tiles {
		baseX, baseZ := tile.X*TileColumns, tile.Z*TileColumns
		for _, quad := range testGenerateQuads(t, header, tile, step) {
			switch quad.Face {
			case FaceTop:
				continue
			case FacePosX, FaceNegX:
				plane := quad.X
				if quad.Face == FacePosX {
					plane = quad.X + 1
				}
				if quad.W != uint16(s) ||
					(plane-baseX)%s != 0 || plane < baseX || plane > baseX+TileColumns ||
					(quad.Z-baseZ)%s != 0 || quad.Z < baseZ || quad.Z >= baseZ+TileColumns {
					t.Fatalf("X 向裙边未按窗口网格对齐: tile(%d,%d) %+v", tile.X, tile.Z, quad)
				}
				xSkirts[skirtKey{plane: plane, row: quad.Z}] = append(xSkirts[skirtKey{plane: plane, row: quad.Z}], quad)
			case FaceNegZ, FacePosZ:
				plane := quad.Z
				if quad.Face == FacePosZ {
					plane = quad.Z + 1
				}
				if quad.W != uint16(s) ||
					(plane-baseZ)%s != 0 || plane < baseZ || plane > baseZ+TileColumns ||
					(quad.X-baseX)%s != 0 || quad.X < baseX || quad.X >= baseX+TileColumns {
					t.Fatalf("Z 向裙边未按窗口网格对齐: tile(%d,%d) %+v", tile.X, tile.Z, quad)
				}
				zSkirts[skirtKey{plane: plane, row: quad.X}] = append(zSkirts[skirtKey{plane: plane, row: quad.X}], quad)
			}
		}
	}
	return xSkirts, zSkirts
}

// TestShellSkirtsCloseBreakBoundaries 是结构性质门禁:对窗口场中每条
// 相邻窗口边(含 tile 边界,边界断差由相邻 tile 的裙边闭合),等高边
// 不得有裙边,断差边必须恰好被一条裙边闭合——竖直跨度精确衔接两侧
// 地表平面([低侧+1, 高侧+1)),朝向断差方向,材质取高侧,着色取轴向
// 权重。遍历全部边界即证明无洞。
func TestShellSkirtsCloseBreakBoundaries(t *testing.T) {
	header := testHeader()
	for _, tc := range oracleTileCases {
		t.Run(fmt.Sprintf("tile(%d,%d)/step%d", tc.tile.X, tc.tile.Z, tc.step), func(t *testing.T) {
			field := testSampleField(t, header, tc.tile, tc.step)
			// 本 tile 与四邻:tile 边界断差的裙边由高侧 tile 独立生成,
			// 闭合性必须跨 tile 联合验证。
			neighbors := []core.ChunkPos{
				tc.tile,
				{X: tc.tile.X - 1, Z: tc.tile.Z},
				{X: tc.tile.X + 1, Z: tc.tile.Z},
				{X: tc.tile.X, Z: tc.tile.Z - 1},
				{X: tc.tile.X, Z: tc.tile.Z + 1},
			}
			xSkirts, zSkirts := testSkirtIndex(t, header, neighbors, tc.step)
			s := field.step

			// X 向边:窗口对 (a=window(k−1,j), b=window(k,j)),k ∈ 0..=n。
			for j := 0; j < field.n; j++ {
				for k := 0; k <= field.n; k++ {
					a := field.window(k-1, j)
					b := field.window(k, j)
					key := skirtKey{plane: field.baseX + int32(k)*s, row: field.baseZ + int32(j)*s}
					if a.material == testAirMaterial || b.material == testAirMaterial {
						continue
					}
					if a.top == b.top {
						if quads := xSkirts[key]; len(quads) != 0 {
							t.Fatalf("等高边 (k=%d,j=%d) 出现裙边 %+v", k, j, quads)
						}
						continue
					}
					high, low, wantFace := b, a, FaceNegX
					if a.top > b.top {
						high, low, wantFace = a, b, FacePosX
					}
					assertSkirtClosesBreak(t, xSkirts[key], wantFace, low, high, ShadeSideX,
						fmt.Sprintf("X 边 (k=%d,j=%d)", k, j))
				}
			}

			// Z 向边:窗口对 (a=window(i,k−1), b=window(i,k)),镜像 X 向。
			for i := 0; i < field.n; i++ {
				for k := 0; k <= field.n; k++ {
					a := field.window(i, k-1)
					b := field.window(i, k)
					key := skirtKey{plane: field.baseZ + int32(k)*s, row: field.baseX + int32(i)*s}
					if a.material == testAirMaterial || b.material == testAirMaterial {
						continue
					}
					if a.top == b.top {
						if quads := zSkirts[key]; len(quads) != 0 {
							t.Fatalf("等高边 (i=%d,k=%d) 出现裙边 %+v", i, k, quads)
						}
						continue
					}
					high, low, wantFace := b, a, FaceNegZ
					if a.top > b.top {
						high, low, wantFace = a, b, FacePosZ
					}
					assertSkirtClosesBreak(t, zSkirts[key], wantFace, low, high, ShadeSideZ,
						fmt.Sprintf("Z 边 (i=%d,k=%d)", i, k))
				}
			}
		})
	}
}

// assertSkirtClosesBreak 断言一条断差边恰好被一条裙边闭合:面朝低侧、
// 竖直跨度 [低侧+1, 高侧+1)、材质取高侧、着色取轴向权重。
func assertSkirtClosesBreak(t *testing.T, quads []Quad, wantFace Face, low, high testWindow, wantShade uint8, edge string) {
	t.Helper()
	if len(quads) != 1 {
		t.Fatalf("%s 裙边数量 %d,想要恰好 1(%+v)", edge, len(quads), quads)
	}
	quad := quads[0]
	if quad.Face != wantFace {
		t.Fatalf("%s 裙边朝向 %d != %d", edge, quad.Face, wantFace)
	}
	if quad.Y != low.top+1 || int32(quad.D) != high.top-low.top {
		t.Fatalf("%s 裙边竖直跨度 [y=%d,+%d) != [%d,%d)",
			edge, quad.Y, quad.D, low.top+1, high.top+1)
	}
	if quad.Material != high.material {
		t.Fatalf("%s 裙边材质 %d != 高侧 %d", edge, quad.Material, high.material)
	}
	if quad.Shade != wantShade {
		t.Fatalf("%s 裙边着色 %d != %d", edge, quad.Shade, wantShade)
	}
}
