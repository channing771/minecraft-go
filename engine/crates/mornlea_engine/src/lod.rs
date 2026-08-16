//! lod:确定性远环 LOD 壳生成(模块级核心,FFI 出口由任务 2.1 接线)。
//!
//! 对每个 tile(固定 4×4 chunk = 64×64 列)按 `lodStep` 列聚合为窗口:
//! 窗高取窗内列高 **max**(保守遮挡:矮列被高列挡住,远环绝不漏视到
//! "下方");材质取最高列的 worldgen 表层材质。同材质等高的相邻窗贪心
//! 合并为一个顶面 quad;相邻窗高度断差处由高侧窗口生成朝向断差方向的
//! 侧裙 quad——接缝在构造上不可能裂开(裙边永远补齐断差),无需
//! quadtree 式跨级缝合。
//!
//! 确定性契约:同 perm + 同 tile + 同步长 → 全平台逐位一致输出。窗口值
//! 完全由 worldgen 纯函数推导(复用 [`WorldgenParams`],本模块不重写
//! 噪声/高度公式);quad 流顺序由固定遍历序决定;编码只用整数小端字节。
//!
//! 坐标约定(与近环 quad 同源):`y` 为方块坐标,渲染可见面平面 = y+1;
//! 顶面 quad 覆盖方块列 [x, x+w) × [z, z+d);侧裙 `y` 为裙边最低方块行,
//! 竖直跨度 `d` 恰好衔接两侧地表平面(方块行 低侧 top+1 ..= 高侧 top)。

use crate::worldgen::{
    WORLD_MAX_Y, WORLDGEN_HEADER_BYTES, WorldgenParams, parse_header, read_i32, read_u32,
};

/// tile 固定列数:4×4 chunk = 64×64 列,与 design 裁决一致。
pub(crate) const LOD_TILE_COLUMNS: i32 = 64;
/// 合法步长集合 {2,4,8};64 对三者均整除,窗口网格全局对齐(相邻 tile
/// 的窗口边界重合,跨 tile 断差才能由相邻 tile 独立补齐)。
const LOD_STEPS: [u32; 3] = [2, 4, 8];

/// LOD 壳入口输入字节数:与 `mornlea_worldgen_chunk` 完全一致的 `MGW1`
/// header(564)+ tile_x i32(564)+ tile_z i32(568)+ columns u32(572,
/// 必须等于 64)+ lod_step u32(576,合法值 2/4/8)。
pub(crate) const LOD_SHELL_INPUT_BYTES: usize = WORLDGEN_HEADER_BYTES + 16;
/// 输出流中单个壳 quad 的字节数,布局见 [`encode_shell`]。
pub(crate) const LOD_SHELL_QUAD_BYTES: usize = 20;

/// 顶面着色权重:天空光满档 × 法线朝向权重 1.0,量化为 255。
///
/// 设计裁决:远环不做光照传播,着色 = 天空光满档 × 法线朝向权重(斜坡
/// 的竖直墙视觉上变暗);昼夜 tint 由渲染侧统一计算,不内置第二套昼夜。
const SHADE_TOP: u8 = 255;
/// ±X 侧裙权重:0.6 × 255 = 153;与 ±Z 取不同权重以保留斜坡的方向立体感。
const SHADE_SIDE_X: u8 = 153;
/// ±Z 侧裙权重:0.8 × 255 = 204;数值约定俗成(顶 1.0 / Z 向 0.8 / X 向
/// 0.6),编码为常量后属于确定性契约的一部分。
const SHADE_SIDE_Z: u8 = 204;

/// 壳面朝向:远环只有顶面与四向侧裙,无底面(纯地表壳,不产生地下面)。
#[derive(Copy, Clone, Debug, Eq, PartialEq)]
#[repr(u8)]
pub(crate) enum LodFace {
    /// 顶面,法线 +Y。
    Top = 0,
    /// 侧裙,法线 −X(裙边属于其东侧的高窗口)。
    NegX = 1,
    /// 侧裙,法线 +X(裙边属于其西侧的高窗口)。
    PosX = 2,
    /// 侧裙,法线 −Z。
    NegZ = 3,
    /// 侧裙,法线 +Z。
    PosZ = 4,
}

/// 远环壳 quad:世界坐标大四边形,不复用近环 section 的 4-bit 局部编码
/// (X/Y/Z 各 4 bit、W/H ≤16 装不下远环的世界坐标大 quad)。
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) struct LodQuad {
    /// 世界 X(block):顶面为覆盖区起始列;侧裙为裙边所在边界面的方块列
    /// (PosX 面 = x+1 平面,NegX 面 = x 平面)。
    pub x: i32,
    /// 世界 Z(block):顶面为覆盖区起始行;侧裙语义同 `x` 的 Z 向镜像。
    pub z: i32,
    /// 方块 Y:顶面为最高实心方块;侧裙为裙边最低方块行(低侧 top+1)。
    pub y: i32,
    /// 跨度(block):顶面 w = X 跨度(≤64);侧裙 w = 墙面水平跨度。
    pub w: u16,
    /// 跨度(block):顶面 d = Z 跨度(≤64);侧裙 d = 竖直跨度(断差块数)。
    pub d: u16,
    /// 面朝向。
    pub face: LodFace,
    /// 材质 ID(最高列 worldgen 表层材质)。
    pub material: u16,
    /// 着色权重(0..255)。
    pub shade: u8,
}

/// 单次 LOD 壳请求:worldgen 参数 + tile 原点(chunk 坐标)+ 步长。
pub(crate) struct LodShellRequest {
    /// worldgen 确定性输入(seed、材料表、perm 播种)。
    pub params: WorldgenParams,
    /// tile 原点 chunk 坐标 X(tile 覆盖 [tile_x*64, tile_x*64+64) 列)。
    pub tile_x: i32,
    /// tile 原点 chunk 坐标 Z。
    pub tile_z: i32,
    /// 列合并步长,已验证 ∈ {2,4,8}。
    pub step: u32,
}

/// 步长窗聚合结果:`top` 为窗内最高列高(截断到世界 Y 上界内),material
/// 为该列的 worldgen 表层材质;material == air 表示无地表窗口(生产
/// worldgen 高度域远离 Y 界不会出现,仅为空 tile 语义完备)。
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
struct LodWindow {
    top: i32,
    material: u16,
}

/// 窗口场:tile 内 N×N 窗口加边界外一圈,以全局窗口坐标 gi/gj ∈ −1..=N
/// 索引;边界外一圈只参与断差裙边比较,不产生顶面 quad。
struct WindowField {
    step: i32,
    base_x: i32,
    base_z: i32,
    /// 每轴内部窗口数 N = 64/step。
    n: usize,
    /// (N+2)×(N+2) 个窗口,下标 (gj+1)*(N+2)+(gi+1)。
    cells: Vec<LodWindow>,
}

impl WindowField {
    /// 取全局窗口坐标 (gi, gj) 处的窗口;调用方保证 ∈ −1..=N。
    fn window(&self, gi: i32, gj: i32) -> LodWindow {
        self.cells[((gj + 1) as usize) * (self.n + 2) + (gi + 1) as usize]
    }
}

/// 解析并校验 LOD 壳入口输入;任何违约(长度、header、列数、步长、tile
/// 原点乘 64 溢出)返回 None,由 FFI 层转为对应 status。
pub(crate) fn parse_lod_input(bytes: &[u8]) -> Option<LodShellRequest> {
    if bytes.len() != LOD_SHELL_INPUT_BYTES {
        return None;
    }
    let params = parse_header(bytes)?;
    let tile_x = read_i32(bytes, WORLDGEN_HEADER_BYTES);
    let tile_z = read_i32(bytes, WORLDGEN_HEADER_BYTES + 4);
    let columns = read_u32(bytes, WORLDGEN_HEADER_BYTES + 8);
    let step = read_u32(bytes, WORLDGEN_HEADER_BYTES + 12);
    if columns != LOD_TILE_COLUMNS as u32 || !LOD_STEPS.contains(&step) {
        return None;
    }
    // 越界 tile 拒绝:原点 × 64(base)是 tile 内一切世界坐标推导的基数。
    // 窗口场连同边界外一圈实际访问的坐标区间是 [base − 8, base + 64 + 7]
    // (边界环 gi = −1 按最大步长 8 向外扩;gi = n 的窗口起点是 base + 64,
    // 窗内列再 +step−1,step ≤ 8)。由于 base 是 64 的倍数而 step ≤ 8 < 64,
    // 在 64 倍数网格上 "base + 64 ≤ i32::MAX" 与 "base + 64 + step − 1 ≤
    // i32::MAX" 之间不存在格点,checked_add(64) 通过即隐含后者;负方向由
    // 对 base 本身的 checked_sub(8) 精确兜住最小访问 base − 8(注意不能与
    // 正向校验链式串联——那会错检 base + 56)。任一环节失败都说明该 tile
    // 无法在 i32 世界坐标内表示,由 FFI 层转为 INPUT。
    let max_step = LOD_STEPS[2] as i32;
    for tile in [tile_x, tile_z] {
        let base = tile.checked_mul(LOD_TILE_COLUMNS)?;
        base.checked_add(LOD_TILE_COLUMNS)?;
        base.checked_sub(max_step)?;
    }
    Some(LodShellRequest {
        params,
        tile_x,
        tile_z,
        step,
    })
}

/// 采样单个 step×step 窗口:窗高取窗内列高 max(保守遮挡),材质取最高
/// 列的 worldgen 表层材质。
///
/// 高度截断到 WORLD_MAX_Y−1,与 `generate_chunk` 的写入高度一致;并列
/// 最高时取首个达到 max 的列(z 外层、x 内层扫描序,与 `generate_chunk`
/// 遍历序同源),保证全平台确定。表层查询只在刷新 max 时执行,避免窗内
/// 每列都付一次 natural/ore 判定成本。最高列低于 WORLD_MIN_Y 时表层查询
/// 自然返回 air(生产 worldgen 高度域远离 Y 界不会出现,仅为空 tile 语义
/// 完备)。
fn sample_window(params: &WorldgenParams, base_x: i32, base_z: i32, step: i32) -> LodWindow {
    let mut best = LodWindow {
        top: i32::MIN,
        material: params.materials.air,
    };
    for lz in 0..step {
        for lx in 0..step {
            let wx = base_x + lx;
            let wz = base_z + lz;
            let mut height = params.height_at(wx, wz);
            if height >= WORLD_MAX_Y {
                height = WORLD_MAX_Y - 1;
            }
            if height > best.top {
                best = LodWindow {
                    top: height,
                    material: params.terrain_block_at(wx, height, wz),
                };
            }
        }
    }
    best
}

/// 采样 tile 的窗口场(含边界外一圈)。
///
/// 边界外一圈只用于断差裙边的高侧判定:相邻 tile 以同一 worldgen 纯函数
/// 重算同一边界窗口,两侧逐位一致,因此跨 tile 断差恰由高侧 tile 独立
/// 补齐,不重复也不遗漏。
fn sample_field(params: &WorldgenParams, tile_x: i32, tile_z: i32, step: i32) -> WindowField {
    let n = (LOD_TILE_COLUMNS / step) as usize;
    let base_x = tile_x * LOD_TILE_COLUMNS;
    let base_z = tile_z * LOD_TILE_COLUMNS;
    let mut cells = Vec::with_capacity((n + 2) * (n + 2));
    for gj in -1..=(n as i32) {
        for gi in -1..=(n as i32) {
            cells.push(sample_window(
                params,
                base_x + gi * step,
                base_z + gj * step,
                step,
            ));
        }
    }
    WindowField {
        step,
        base_x,
        base_z,
        n,
        cells,
    }
}

/// 对窗口场生成壳 quad 流:先顶面贪心合并,后 X 向、Z 向断差裙边。
///
/// quad 顺序属于确定性契约:顶面按 Z 外、X 内的行主序贪心生长(先沿 X
/// 扩宽、再沿 Z 扩深,与近环 greedy 同策略);裙边 X 向先于 Z 向,均按
/// 行主序遍历窗口对。合并键 = (top, material):等高同材质才合并,顶面
/// 着色恒为满档,不参与键。
fn build_shell(field: &WindowField, air: u16) -> Vec<LodQuad> {
    let n = field.n;
    let step = field.step;
    let mut quads = Vec::new();

    // 顶面贪心合并:claimed 标记防止跨 quad 重复覆盖;无地表窗口(air)
    // 既不作为起点也不可被并入,天然成为合并屏障。
    let mut claimed = vec![false; n * n];
    for j in 0..n {
        for i in 0..n {
            if claimed[j * n + i] {
                continue;
            }
            let key = field.window(i as i32, j as i32);
            if key.material == air {
                continue;
            }
            let mut width = 1;
            while i + width < n
                && !claimed[j * n + i + width]
                && field.window((i + width) as i32, j as i32) == key
            {
                width += 1;
            }
            let mut depth = 1;
            'grow: while j + depth < n {
                for k in 0..width {
                    let index = (j + depth) * n + i + k;
                    if claimed[index] || field.window((i + k) as i32, (j + depth) as i32) != key {
                        break 'grow;
                    }
                }
                depth += 1;
            }
            for dj in 0..depth {
                for di in 0..width {
                    claimed[(j + dj) * n + i + di] = true;
                }
            }
            quads.push(LodQuad {
                x: field.base_x + (i as i32) * step,
                z: field.base_z + (j as i32) * step,
                y: key.top,
                w: (width * step as usize) as u16,
                d: (depth * step as usize) as u16,
                face: LodFace::Top,
                material: key.material,
                shade: SHADE_TOP,
            });
        }
    }

    // X 向断差:窗口对 (g, g+1) 覆盖全部内部相邻对与 tile 西/东边界对
    // (g = −1 与 g = N−1);只有高侧在 tile 内才由本 tile 出裙边。
    for j in 0..n {
        for g in -1..(n as i32) {
            let a = field.window(g, j as i32);
            let b = field.window(g + 1, j as i32);
            if a.material == air || b.material == air {
                continue;
            }
            // 两窗共享的 X 边界(方块列坐标):a 东缘平面 = x+1,b 西缘
            // 平面 = x,二者指向同一位置。
            let edge_x = field.base_x + (g + 1) * step;
            let z = field.base_z + (j as i32) * step;
            if a.top > b.top && g >= 0 {
                quads.push(LodQuad {
                    x: edge_x - 1,
                    z,
                    y: b.top + 1,
                    w: step as u16,
                    d: (a.top - b.top) as u16,
                    face: LodFace::PosX,
                    material: a.material,
                    shade: SHADE_SIDE_X,
                });
            } else if b.top > a.top && g + 1 < n as i32 {
                quads.push(LodQuad {
                    x: edge_x,
                    z,
                    y: a.top + 1,
                    w: step as u16,
                    d: (b.top - a.top) as u16,
                    face: LodFace::NegX,
                    material: b.material,
                    shade: SHADE_SIDE_X,
                });
            }
        }
    }

    // Z 向断差:与 X 向镜像,裙边 shade 取 ±Z 档以保留方向立体感。
    for i in 0..n {
        for g in -1..(n as i32) {
            let a = field.window(i as i32, g);
            let b = field.window(i as i32, g + 1);
            if a.material == air || b.material == air {
                continue;
            }
            let edge_z = field.base_z + (g + 1) * step;
            let x = field.base_x + (i as i32) * step;
            if a.top > b.top && g >= 0 {
                quads.push(LodQuad {
                    x,
                    z: edge_z - 1,
                    y: b.top + 1,
                    w: step as u16,
                    d: (a.top - b.top) as u16,
                    face: LodFace::PosZ,
                    material: a.material,
                    shade: SHADE_SIDE_Z,
                });
            } else if b.top > a.top && g + 1 < n as i32 {
                quads.push(LodQuad {
                    x,
                    z: edge_z,
                    y: a.top + 1,
                    w: step as u16,
                    d: (b.top - a.top) as u16,
                    face: LodFace::NegZ,
                    material: b.material,
                    shade: SHADE_SIDE_Z,
                });
            }
        }
    }
    quads
}

/// 生产入口:解析后的请求 → 壳 quad 流(世界坐标,顺序确定)。
pub(crate) fn lod_shell(request: &LodShellRequest) -> Vec<LodQuad> {
    let field = sample_field(
        &request.params,
        request.tile_x,
        request.tile_z,
        request.step as i32,
    );
    build_shell(&field, request.params.materials.air)
}

/// 把壳 quad 流追加编码为稳定字节流(LE),供 FFI 出口直接拷贝给调用方。
///
/// 单 quad 20 字节:x i32(0)| z i32(4)| y i32(8)| w u16(12)| d u16(14)|
/// face u8(16,取值见 [`LodFace`])| material u16(17)| shade u8(19)。
/// material 落在非对齐偏移,逐字节写入不做对齐假设。
pub(crate) fn encode_shell(quads: &[LodQuad], out: &mut Vec<u8>) {
    out.reserve(quads.len() * LOD_SHELL_QUAD_BYTES);
    for quad in quads {
        out.extend_from_slice(&quad.x.to_le_bytes());
        out.extend_from_slice(&quad.z.to_le_bytes());
        out.extend_from_slice(&quad.y.to_le_bytes());
        out.extend_from_slice(&quad.w.to_le_bytes());
        out.extend_from_slice(&quad.d.to_le_bytes());
        out.push(quad.face as u8);
        out.extend_from_slice(&quad.material.to_le_bytes());
        out.push(quad.shade);
    }
}

#[cfg(test)]
mod tests {
    use super::{
        LOD_SHELL_INPUT_BYTES, LOD_SHELL_QUAD_BYTES, LodFace, LodQuad, LodShellRequest, LodWindow,
        WindowField, build_shell, encode_shell, lod_shell, parse_lod_input, sample_field,
        sample_window,
    };
    use crate::worldgen::{Materials, WORLD_MAX_Y, WorldgenParams};

    /// 测试材料表:与 worldgen 测试同款,取值互异即可。
    fn materials() -> Materials {
        Materials {
            air: 0,
            stone: 1,
            dirt: 2,
            grass: 3,
            bedrock: 4,
            snow: 5,
            sand: 6,
            clay: 7,
            gravel: 8,
            iron_ore: 9,
            coal_ore: 10,
            oak_log: 11,
            leaves: 12,
        }
    }

    /// 恒等 perm 表 + 固定 seed 的 worldgen 参数(镜像 worldgen 测试)。
    fn params(seed: i64) -> WorldgenParams {
        let mut perm = [0u8; 512];
        for (i, entry) in perm.iter_mut().enumerate() {
            *entry = (i & 255) as u8;
        }
        WorldgenParams {
            seed,
            materials: materials(),
            perm,
        }
    }

    /// 构造 LOD 壳入口输入字节。
    fn lod_input(seed: i64, tile_x: i32, tile_z: i32, columns: u32, step: u32) -> Vec<u8> {
        let mut bytes = vec![0u8; LOD_SHELL_INPUT_BYTES];
        bytes[0..4].copy_from_slice(b"MGW1");
        bytes[4..8].copy_from_slice(&1u32.to_le_bytes());
        bytes[8..16].copy_from_slice(&seed.to_le_bytes());
        bytes[16..20].copy_from_slice(&(-64i32).to_le_bytes());
        bytes[20..24].copy_from_slice(&320i32.to_le_bytes());
        for index in 0..13u16 {
            bytes[24 + 2 * index as usize..26 + 2 * index as usize]
                .copy_from_slice(&index.to_le_bytes());
        }
        for index in 0..512 {
            bytes[52 + index] = (index & 255) as u8;
        }
        bytes[564..568].copy_from_slice(&tile_x.to_le_bytes());
        bytes[568..572].copy_from_slice(&tile_z.to_le_bytes());
        bytes[572..576].copy_from_slice(&columns.to_le_bytes());
        bytes[576..580].copy_from_slice(&step.to_le_bytes());
        bytes
    }

    /// 以闭包按全局窗口坐标构造合成窗口场(含边界外一圈)。
    fn field(
        step: i32,
        n: usize,
        base_x: i32,
        base_z: i32,
        f: impl Fn(i32, i32) -> LodWindow,
    ) -> WindowField {
        let mut cells = Vec::with_capacity((n + 2) * (n + 2));
        for gj in -1..=(n as i32) {
            for gi in -1..=(n as i32) {
                cells.push(f(gi, gj));
            }
        }
        WindowField {
            step,
            base_x,
            base_z,
            n,
            cells,
        }
    }

    const GRASS: u16 = 3;
    const AIR: u16 = 0;

    fn grass(top: i32) -> LodWindow {
        LodWindow {
            top,
            material: GRASS,
        }
    }

    #[test]
    fn empty_tile_produces_no_quads() {
        // 空 tile:全部窗口无地表(air),壳流必须为空,编码为空字节。
        let air_field = field(4, 16, 0, 0, |_, _| LodWindow {
            top: 40,
            material: AIR,
        });
        assert!(build_shell(&air_field, AIR).is_empty());
        let mut encoded = Vec::new();
        encode_shell(&[], &mut encoded);
        assert!(encoded.is_empty());
    }

    #[test]
    fn uniform_tile_merges_to_single_top_quad() {
        // 单一材质 + 等高:整个 tile 贪心合并为 1 个顶面 quad,无裙边。
        let uniform = field(4, 16, 0, 0, |_, _| grass(70));
        let quads = build_shell(&uniform, AIR);
        assert_eq!(
            quads,
            vec![LodQuad {
                x: 0,
                z: 0,
                y: 70,
                w: 64,
                d: 64,
                face: LodFace::Top,
                material: GRASS,
                shade: 255,
            }]
        );
    }

    #[test]
    fn height_step_generates_closing_skirt() {
        // 断差闭合:左列窗口 top=80、右列 top=64(n=2, step=8);边界外
        // 一圈与相邻内部等高以隔离出唯一的内部断差。高侧东缘每行窗口各
        // 出一条 PosX 裙边(裙边不合并是既定取舍),竖直跨度精确衔接两侧
        // 地表平面。
        let stepped = field(8, 2, 0, 0, |gi, _| {
            let top = match gi {
                -1 => 80,
                0 => 80,
                _ => 64,
            };
            grass(top)
        });
        let quads = build_shell(&stepped, AIR);
        assert_eq!(quads.len(), 4, "{quads:?}");
        let tops: Vec<LodQuad> = quads
            .iter()
            .copied()
            .filter(|quad| quad.face == LodFace::Top)
            .collect();
        assert_eq!(
            tops,
            vec![
                LodQuad {
                    x: 0,
                    z: 0,
                    y: 80,
                    w: 8,
                    d: 16,
                    face: LodFace::Top,
                    material: GRASS,
                    shade: 255,
                },
                LodQuad {
                    x: 8,
                    z: 0,
                    y: 64,
                    w: 8,
                    d: 16,
                    face: LodFace::Top,
                    material: GRASS,
                    shade: 255,
                },
            ]
        );
        let skirts: Vec<LodQuad> = quads
            .iter()
            .copied()
            .filter(|quad| quad.face != LodFace::Top)
            .collect();
        let skirt = LodQuad {
            x: 7,
            z: 0,
            y: 65,
            w: 8,
            d: 16,
            face: LodFace::PosX,
            material: GRASS,
            shade: 153,
        };
        assert_eq!(skirts, vec![skirt, LodQuad { z: 8, ..skirt }]);
        // 构造闭合:裙边从低侧地表平面(64+1)恰好砌到高侧地表平面(80+1)。
        assert_eq!(skirts[0].y, 64 + 1);
        assert_eq!(skirts[0].y + skirts[0].d as i32, 80 + 1);
    }

    #[test]
    fn boundary_skirt_owned_by_taller_side() {
        // 边界窗:tile 西侧边界外窗口更低(64),内部边缘(80)生成朝外
        // NegX 裙边;东侧边界外更高(96),本 tile 不生成(邻居 tile 拥有)。
        let bounded = field(8, 2, 0, 0, |gi, _| {
            let top = match gi {
                -1 => 64,
                2 => 96,
                _ => 80,
            };
            grass(top)
        });
        let quads = build_shell(&bounded, AIR);
        let skirts: Vec<LodQuad> = quads
            .iter()
            .copied()
            .filter(|quad| quad.face != LodFace::Top)
            .collect();
        let skirt = LodQuad {
            x: 0,
            z: 0,
            y: 65,
            w: 8,
            d: 16,
            face: LodFace::NegX,
            material: GRASS,
            shade: 153,
        };
        // 每行窗口各一条朝外裙边;东侧边界外更高,本 tile 不出(邻居拥有)。
        assert_eq!(skirts, vec![skirt, LodQuad { z: 8, ..skirt }]);
        // 内部等高 → 顶面仍合并为单 quad。
        assert_eq!(quads.len(), 3, "{quads:?}");
    }

    #[test]
    fn parse_rejects_invalid_lod_input() {
        let valid = lod_input(42, -3, 2, 64, 4);
        assert!(parse_lod_input(&valid).is_some());
        // 长度不精确匹配(过长/过短)。
        let mut long = valid.clone();
        long.push(0);
        assert!(parse_lod_input(&long).is_none());
        assert!(parse_lod_input(&valid[..valid.len() - 1]).is_none());
        // header 违约复用 worldgen 校验(magic)。
        let mut bad_magic = valid.clone();
        bad_magic[0] = b'X';
        assert!(parse_lod_input(&bad_magic).is_none());
        // 列数必须固定 64。
        assert!(parse_lod_input(&lod_input(42, 0, 0, 63, 4)).is_none());
        assert!(parse_lod_input(&lod_input(42, 0, 0, 128, 4)).is_none());
        // 步长只接受 2/4/8。
        for step in [0u32, 1, 3, 5, 6, 7, 16, 64] {
            assert!(parse_lod_input(&lod_input(42, 0, 0, 64, step)).is_none());
        }
        // tile 原点 × 64 溢出 i32(越界 tile)。
        assert!(parse_lod_input(&lod_input(42, i32::MAX, 0, 64, 4)).is_none());
        assert!(parse_lod_input(&lod_input(42, 0, i32::MIN, 64, 4)).is_none());
    }

    /// 极值 tile 邻域(真实 overflow 门禁):窗口场连同边界外一圈会访问
    /// [base−8, base+64+step−1] 的世界坐标,校验链必须把该区间整体锁在
    /// i32 界内——仅 checked_mul(64) 会放过 base+64 或 base−8 溢出的 tile,
    /// 导致 debug panic / release 回绕两种构建结果分叉。
    #[test]
    fn parse_rejects_extreme_tiles_at_i32_boundary() {
        // 正向:33554430 = (i32::MAX − 64 − 7)/64 是最大合法 tile(base =
        // 2147483520,边界环 +64 与窗内 +step−1 后仍 ≤ i32::MAX);
        // 33554431 = 2²⁵−1 通过 checked_mul(64) 但 base+64 = 2³¹ 溢出。
        assert!(parse_lod_input(&lod_input(42, 33554430, 0, 64, 8)).is_some());
        assert!(parse_lod_input(&lod_input(42, 33554431, 0, 64, 4)).is_none());
        assert!(parse_lod_input(&lod_input(42, 0, 33554431, 64, 2)).is_none());
        // 负向:−33554431 合法(base = i32::MIN + 64,边界环 −8 不溢出);
        // −33554432 的 base = i32::MIN 恰通过 checked_mul(64),但边界环
        // gi = −1 × 最大步长 8 会下溢。
        assert!(parse_lod_input(&lod_input(42, -33554431, 0, 64, 8)).is_some());
        assert!(parse_lod_input(&lod_input(42, -33554432, 0, 64, 2)).is_none());
        assert!(parse_lod_input(&lod_input(42, 0, -33554432, 64, 8)).is_none());
        // 合法极值 tile 必须真正可生成(边界环每个坐标都可计算、不 panic),
        // 编码长度保持 20 字节对齐。
        let request = parse_lod_input(&lod_input(42, -33554431, 33554430, 64, 8)).unwrap();
        let mut encoded = Vec::new();
        encode_shell(&lod_shell(&request), &mut encoded);
        assert_eq!(encoded.len() % LOD_SHELL_QUAD_BYTES, 0);
    }

    #[test]
    fn parse_accepts_valid_request_fields() {
        let request = parse_lod_input(&lod_input(-7, -3, 2, 64, 8)).unwrap();
        assert_eq!(request.params.seed, -7);
        assert_eq!((request.tile_x, request.tile_z), (-3, 2));
        assert_eq!(request.step, 8);
    }

    #[test]
    fn window_aggregates_match_worldgen() {
        // 复用核验:窗口 top == 窗内截断高度 max;material == 最高列(首
        // 个达到 max,z 外 x 内扫描序)的 terrain_block_at 表层结果。
        let p = params(11);
        let field = sample_field(&p, -1, -1, 8);
        for (gi, gj) in [(0, 0), (3, 5), (7, 7), (-1, 0), (5, -1)] {
            let base_x = -64 + gi * 8;
            let base_z = -64 + gj * 8;
            let mut expected = LodWindow {
                top: i32::MIN,
                material: AIR,
            };
            for lz in 0..8 {
                for lx in 0..8 {
                    let mut height = p.height_at(base_x + lx, base_z + lz);
                    if height >= WORLD_MAX_Y {
                        height = WORLD_MAX_Y - 1;
                    }
                    if height > expected.top {
                        expected = LodWindow {
                            top: height,
                            material: p.terrain_block_at(base_x + lx, height, base_z + lz),
                        };
                    }
                }
            }
            assert_eq!(
                sample_window(&p, base_x, base_z, 8),
                expected,
                "({gi},{gj})"
            );
            assert_eq!(field.window(gi, gj), expected, "({gi},{gj})");
        }
    }

    #[test]
    fn all_steps_generate_deterministic_nonempty_shells() {
        // 三档合法步长全部产出非空壳,且同输入两次运行逐字节一致。
        for step in [2u32, 4, 8] {
            let request = LodShellRequest {
                params: params(42),
                tile_x: -3,
                tile_z: 2,
                step,
            };
            let mut first = Vec::new();
            encode_shell(&lod_shell(&request), &mut first);
            assert!(!first.is_empty());
            assert_eq!(first.len() % LOD_SHELL_QUAD_BYTES, 0);
            let mut second = Vec::new();
            encode_shell(&lod_shell(&request), &mut second);
            assert_eq!(first, second);
        }
    }

    #[test]
    fn golden_shell_bytes_are_stable() {
        // 任务 1.2 确定性 golden:固定 perm/seed/tile/step 的输出字节快照。
        // 快照与 Go 侧 testdata/*.bin 金样同一惯例,以二进制 fixture 承载,
        // 避免在源码内展开上万字节常量;重新生成方式:临时用 fs::write 把
        // encoded 落盘后替换该文件(输入三要素在本测试内逐字钉死)。
        let request = parse_lod_input(&lod_input(42, -3, 2, 64, 4)).unwrap();
        let mut encoded = Vec::new();
        encode_shell(&lod_shell(&request), &mut encoded);
        assert_eq!(encoded.len(), GOLDEN_SHELL_BYTES.len());
        assert_eq!(encoded, GOLDEN_SHELL_BYTES);
        assert_eq!(GOLDEN_SHELL_BYTES.len() % LOD_SHELL_QUAD_BYTES, 0);
    }

    /// 固定 seed 42、恒等 perm、tile(−3,2)、step 4(默认档)的壳输出
    /// 字节快照:548 个 quad,共 10960 字节。
    const GOLDEN_SHELL_BYTES: &[u8] = include_bytes!("../testdata/lod-shell-seed42-step4-v1.bin");
}
