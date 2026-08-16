//! worldgen:世界生成的唯一生产内核。
//!
//! 本模块逐条镜像旧 Go `internal/worldgen` 实现:2D Perlin/fbm 高度图、
//! 地表分层(草/土/石/基岩/雪/沙/黏土/砂砾)、splitmix 系整数矿石哈希与
//! 8×8 候选格橡树。差分门禁要求与 Go oracle 同种子逐位一致,因此:
//!
//! - 浮点只使用 IEEE 正确舍入的基本运算(加/乘/除/floor/截断转换),
//!   禁止 `mul_add` 与任何重结合;运算顺序与 Go 源码逐条对应。
//! - perm 表与材料 ID 由调用方(Go)传入:随机源语义与 block 注册表的
//!   所有权留在 Go,engine 不内置 RNG、不硬编码 BlockID。
//! - 整数运算全部按 Go 的补码回绕语义使用 wrapping 系列。

/// 世界 Y 下界,必须与 Go `core.MinY` 一致;header 校验强制相等。
pub(crate) const WORLD_MIN_Y: i32 = -64;
/// 世界 Y 上界(开区间),必须与 Go `core.MaxY` 一致;header 校验强制相等。
pub(crate) const WORLD_MAX_Y: i32 = 320;
/// 区块边长(X/Z 方向 16 格),与 Go `core.SectionSize` 一致。
pub(crate) const SECTION_SIZE: i32 = 16;
/// 区块世界坐标位移量,与 Go `core.SectionShift` 一致。
pub(crate) const SECTION_SHIFT: u32 = 4;
/// 单区块 dense 输出的 u16 数量:16×16×(320−(−64)) = 98304。
pub(crate) const CHUNK_VOLUME: usize =
    (SECTION_SIZE as usize) * (SECTION_SIZE as usize) * ((WORLD_MAX_Y - WORLD_MIN_Y) as usize);

// 地形常量,与 Go 版逐字一致。
const SEA_LEVEL: f64 = 64.0;
/// 海平面的整数 Y 值,注水判定用。单独写成 i32 常量而不是由 `SEA_LEVEL`
/// 转换,是为了避免在常量上下文里做浮点转换;`sea_level_constants_agree`
/// 测试钉死两者一致。
const SEA_LEVEL_Y: i32 = 64;
const TERRAIN_AMP: f64 = 48.0;
const TERRAIN_SCALE: f64 = 1.0 / 256.0;
const OCTAVES: usize = 5;
const LACUNARITY: f64 = 2.0;
const GAIN: f64 = 0.5;
const SOIL_DEPTH: i32 = 4;

const SNOW_LINE: i32 = 88;
const SAND_LINE: i32 = 62;
const CLAY_NOISE_SCALE: f64 = 1.0 / 96.0;
const CLAY_NOISE_OFFSET_X: i32 = 417;
const CLAY_NOISE_OFFSET_Z: i32 = -193;
const CLAY_NOISE_THRESHOLD: f64 = 0.18;
const GRAVEL_NOISE_SCALE: f64 = 1.0 / 72.0;
const GRAVEL_NOISE_OFFSET_X: i32 = -271;
const GRAVEL_NOISE_OFFSET_Z: i32 = 613;
const GRAVEL_NOISE_THRESHOLD: f64 = 0.22;
const GRAVEL_MAX_DEPTH: i32 = 10;

const COAL_MAX_Y: i32 = 96;
const IRON_MAX_Y: i32 = 48;
const COAL_ODDS: u64 = 2048;
const IRON_ODDS: u64 = 4096;
const COAL_SALT: u64 = 0x9E37_79B9_7F4A_7C15;
const IRON_SALT: u64 = 0xC2B2_AE3D_27D4_EB4F;

const OAK_TREE_CELL_SHIFT: u32 = 3;
const OAK_TREE_SALT: u64 = 0xA24B_AED4_963E_E407;

/// 调用方传入的方块材料表;engine 不硬编码任何 BlockID。
///
/// 14 项必须两两互异,**唯一例外是 `water` 允许等于 `air`**:air 是空判定
/// 哨兵,其余 ID 在分层/矿石/树逻辑中参与等值比较,重复 ID 会破坏与 Go
/// 语义的对应关系,由 FFI 层拒绝。
///
/// `water == air` 是 Go 侧 `fluidEnabled` 关闭时的门控编码(design D6):
/// water 只被写入、从不参与等值比较,填 air 编号即让注水步退化为把空气
/// 写回空气,生成结果与未引入流体的基线逐位一致,Rust 侧因此不需要任何
/// 开关分支。
#[derive(Clone, Copy)]
pub(crate) struct Materials {
    pub air: u16,
    pub stone: u16,
    pub dirt: u16,
    pub grass: u16,
    pub bedrock: u16,
    pub snow: u16,
    pub sand: u16,
    pub clay: u16,
    pub gravel: u16,
    pub iron_ore: u16,
    pub coal_ore: u16,
    pub oak_log: u16,
    pub leaves: u16,
    /// 海平面注水写入的方块;等于 `air` 时注水整体退化为空操作。
    pub water: u16,
}

impl Materials {
    /// 按 header 编码顺序展开为数组,供互异性校验使用。
    pub(crate) fn as_array(&self) -> [u16; 14] {
        [
            self.air,
            self.stone,
            self.dirt,
            self.grass,
            self.bedrock,
            self.snow,
            self.sand,
            self.clay,
            self.gravel,
            self.iron_ore,
            self.coal_ore,
            self.oak_log,
            self.leaves,
            self.water,
        ]
    }
}

/// 单次 worldgen 调用的全部确定性输入:seed、材料表与 Go 播种的 perm 表。
pub(crate) struct WorldgenParams {
    pub seed: i64,
    pub materials: Materials,
    /// 512 项 Perlin 置换表;u8 取值域即合法域,索引 `perm[perm[i]+j]` 恒在界内。
    pub perm: [u8; 512],
}

/// Perlin 六次插值曲线 6t⁵−15t⁴+10t³,与 Go `fade` 逐条一致。
fn fade(t: f64) -> f64 {
    t * t * t * (t * (t * 6.0 - 15.0) + 10.0)
}

fn lerp(a: f64, b: f64, t: f64) -> f64 {
    a + t * (b - a)
}

/// 从哈希低两位取 2D 梯度方向并与偏移做点积,与 Go `grad2` 一致。
fn grad2(h: u8, x: f64, y: f64) -> f64 {
    match h & 3 {
        0 => x + y,
        1 => -x + y,
        2 => x - y,
        _ => -x - y,
    }
}

impl WorldgenParams {
    /// 2D Perlin 噪声,大致落在 [−1,1];运算顺序逐条镜像 Go `perlin.at`。
    fn noise_at(&self, x: f64, z: f64) -> f64 {
        let fx = x.floor();
        let fz = z.floor();
        // Go 侧为 `int(fx) & 255`:floor 后的 f64 截断为 64 位整数再取低 8 位;
        // 输入范围内截断不饱和,两侧结果一致。
        let xi = ((fx as i64) & 255) as usize;
        let zi = ((fz as i64) & 255) as usize;
        let xf = x - fx;
        let zf = z - fz;
        let u = fade(xf);
        let v = fade(zf);

        let perm = &self.perm;
        let aa = perm[perm[xi] as usize + zi];
        let ab = perm[perm[xi] as usize + zi + 1];
        let ba = perm[perm[xi + 1] as usize + zi];
        let bb = perm[perm[xi + 1] as usize + zi + 1];

        let x1 = lerp(grad2(aa, xf, zf), grad2(ba, xf - 1.0, zf), u);
        let x2 = lerp(grad2(ab, xf, zf - 1.0), grad2(bb, xf - 1.0, zf - 1.0), u);
        lerp(x1, x2, v)
    }

    /// 分形布朗运动,倍频叠加顺序与 Go `fbm` 一致(sum/norm 的除法最后执行)。
    fn fbm(&self, x: f64, z: f64) -> f64 {
        let mut sum = 0.0f64;
        let mut norm = 0.0f64;
        let mut amp = 1.0f64;
        let mut freq = 1.0f64;
        for _ in 0..OCTAVES {
            sum += self.noise_at(x * freq, z * freq) * amp;
            norm += amp;
            freq *= LACUNARITY;
            amp *= GAIN;
        }
        sum / norm
    }

    /// 世界坐标 (wx,wz) 处最高实心方块的 Y,与 Go `HeightAt` 一致(不截断上限)。
    pub(crate) fn height_at(&self, wx: i32, wz: i32) -> i32 {
        let n = self.fbm(f64::from(wx) * TERRAIN_SCALE, f64::from(wz) * TERRAIN_SCALE);
        // Go 为 int32(seaLevel + n*terrainAmp):f64 截断向零;高度域远离 i32 界,
        // 截断不饱和。
        (SEA_LEVEL + n * TERRAIN_AMP) as i32
    }

    /// 基础地层判定,与 Go `terrainBlockAt`(自由函数)一致。
    fn terrain_layer(&self, y: i32, height: i32) -> u16 {
        let m = &self.materials;
        if !(WORLD_MIN_Y..WORLD_MAX_Y).contains(&y) || y > height {
            m.air
        } else if y == WORLD_MIN_Y {
            m.bedrock
        } else if y == height {
            m.grass
        } else if y > height - SOIL_DEPTH {
            m.dirt
        } else {
            m.stone
        }
    }

    /// 自然材料分层(雪/沙/黏土/砂砾),与 Go `naturalBlockAt` 一致。
    ///
    /// 坐标加偏移按 Go int32 补码回绕语义使用 wrapping_add。
    fn natural_block_at(&self, x: i32, y: i32, z: i32, height: i32) -> u16 {
        let m = self.materials;
        let base = self.terrain_layer(y, height);
        if base == m.air || base == m.bedrock {
            return base;
        }

        let depth = height - y;
        if depth == 0 && height >= SNOW_LINE {
            return m.snow;
        }
        if height <= SAND_LINE && (0..SOIL_DEPTH).contains(&depth) {
            if depth >= 2
                && self.noise_at(
                    f64::from(x.wrapping_add(CLAY_NOISE_OFFSET_X)) * CLAY_NOISE_SCALE,
                    f64::from(z.wrapping_add(CLAY_NOISE_OFFSET_Z)) * CLAY_NOISE_SCALE,
                ) > CLAY_NOISE_THRESHOLD
            {
                return m.clay;
            }
            return m.sand;
        }
        if base == m.stone
            && depth <= GRAVEL_MAX_DEPTH
            && self.noise_at(
                f64::from(x.wrapping_add(GRAVEL_NOISE_OFFSET_X)) * GRAVEL_NOISE_SCALE,
                f64::from(z.wrapping_add(GRAVEL_NOISE_OFFSET_Z)) * GRAVEL_NOISE_SCALE,
            ) > GRAVEL_NOISE_THRESHOLD
        {
            return m.gravel;
        }
        base
    }

    /// 地层 + 矿石替换,与 Go `generatedBlockAt` 一致:矿石只替换石头,铁优先于煤。
    fn generated_block_at(&self, x: i32, y: i32, z: i32, height: i32) -> u16 {
        let m = self.materials;
        let base = self.natural_block_at(x, y, z, height);
        if base != m.stone {
            return base;
        }
        if y < IRON_MAX_Y && ore_hash(self.seed, x, y, z, IRON_SALT).is_multiple_of(IRON_ODDS) {
            return m.iron_ore;
        }
        if y < COAL_MAX_Y && ore_hash(self.seed, x, y, z, COAL_SALT).is_multiple_of(COAL_ODDS) {
            return m.coal_ore;
        }
        base
    }

    /// 单点地形查询,与 Go `TerrainBlockAt` 一致:Y 界外为 air,高度截断到 MaxY−1。
    pub(crate) fn terrain_block_at(&self, x: i32, y: i32, z: i32) -> u16 {
        if !(WORLD_MIN_Y..WORLD_MAX_Y).contains(&y) {
            return self.materials.air;
        }
        let mut height = self.height_at(x, z);
        if height >= WORLD_MAX_Y {
            height = WORLD_MAX_Y - 1;
        }
        self.generated_block_at(x, y, z, height)
    }

    /// 单点基础方块查询:地形非空优先,空气处叠加橡树,仍为空气时叠加海水。
    ///
    /// 注水排在最后一层,与 `generate_chunk` 里"注水在 apply_oak_trees 之后"
    /// 的顺序对应:只有最终仍是空气的格才注水,因此地表分层、矿石与橡树的
    /// 结果完全不受影响。
    pub(crate) fn base_block_at(&self, x: i32, y: i32, z: i32) -> u16 {
        let base = self.terrain_block_at(x, y, z);
        if base != self.materials.air {
            return base;
        }
        let tree = self.tree_block_at(x, y, z);
        if tree != self.materials.air {
            return tree;
        }
        self.sea_block_at(y)
    }

    /// 海平面注水的单点形式:世界高度范围内且 `y <= SEA_LEVEL_Y` 时为 water,
    /// 否则为 air。Y 界外不注水,与 `generate_chunk` 只写 `[MIN_Y, MAX_Y)` 一致。
    fn sea_block_at(&self, y: i32) -> u16 {
        if (WORLD_MIN_Y..=SEA_LEVEL_Y).contains(&y) {
            self.materials.water
        } else {
            self.materials.air
        }
    }

    /// 返回固定候选格中的有效橡树,与 Go `oakTreeForCell` 一致。
    ///
    /// 有效性校验使用未截断的 surface 高度,顺序:根格必须是草、树冠不越界、
    /// 树干路径必须全空。
    fn oak_tree_for_cell(&self, cell_x: i32, cell_z: i32) -> Option<OakTree> {
        let hash = ore_hash(self.seed, cell_x, 0, cell_z, OAK_TREE_SALT);
        if hash & 1 != 0 {
            return None;
        }
        let x = (cell_x << OAK_TREE_CELL_SHIFT).wrapping_add(((hash >> 1) & 7) as i32);
        let z = (cell_z << OAK_TREE_CELL_SHIFT).wrapping_add(((hash >> 4) & 7) as i32);
        let height = (4 + (hash >> 7) % 3) as i32;
        let surface = self.height_at(x, z);
        let root_y = surface + 1;
        if self.generated_block_at(x, surface, z, surface) != self.materials.grass
            || root_y + height >= WORLD_MAX_Y
        {
            return None;
        }
        for y in root_y..root_y + height {
            if self.generated_block_at(x, y, z, surface) != self.materials.air {
                return None;
            }
        }
        Some(OakTree {
            root_x: x,
            root_y,
            root_z: z,
            height,
        })
    }

    /// 单点橡树查询:合并全部可能覆盖 (x,y,z) 的候选树,原木优先,与 Go
    /// `treeBlockAt` 的 cellZ 外层、cellX 内层遍历顺序一致。
    fn tree_block_at(&self, x: i32, y: i32, z: i32) -> u16 {
        let m = self.materials;
        let mut leaf = false;
        let cell_z_min = (z - 2) >> OAK_TREE_CELL_SHIFT;
        let cell_z_max = (z + 2) >> OAK_TREE_CELL_SHIFT;
        let cell_x_min = (x - 2) >> OAK_TREE_CELL_SHIFT;
        let cell_x_max = (x + 2) >> OAK_TREE_CELL_SHIFT;
        for cell_z in cell_z_min..=cell_z_max {
            for cell_x in cell_x_min..=cell_x_max {
                let Some(tree) = self.oak_tree_for_cell(cell_x, cell_z) else {
                    continue;
                };
                let block = oak_tree_block_at(&tree, &self.materials, x, y, z);
                if block == m.oak_log {
                    return m.oak_log;
                }
                if block == m.leaves {
                    leaf = true;
                }
            }
        }
        if leaf { m.leaves } else { m.air }
    }

    /// 生成整区块 dense 数组,布局 `[y−min_y][lz][lx]`,与 Go `GenerateChunk`
    /// 的写入集合逐位一致:地形只写到截断后的地表高度,其余保持 air。
    pub(crate) fn generate_chunk(&self, chunk_x: i32, chunk_z: i32, dense: &mut [u16]) {
        debug_assert_eq!(dense.len(), CHUNK_VOLUME);
        dense.fill(self.materials.air);
        let base_x = chunk_x << SECTION_SHIFT;
        let base_z = chunk_z << SECTION_SHIFT;

        for lz in 0..SECTION_SIZE {
            for lx in 0..SECTION_SIZE {
                let wx = base_x + lx;
                let wz = base_z + lz;
                let mut h = self.height_at(wx, wz);
                if h >= WORLD_MAX_Y {
                    h = WORLD_MAX_Y - 1;
                }
                for y in WORLD_MIN_Y..=h {
                    dense[dense_index(lx, y, lz)] = self.generated_block_at(wx, y, wz, h);
                }
            }
        }
        self.apply_oak_trees(chunk_x, chunk_z, dense);
        self.flood_sea_level(dense);
    }

    /// 海平面注水:把海平面及以下**仍为空气**的格改写为 `materials.water`。
    ///
    /// 必须排在 `apply_oak_trees` 之后:注水只填最终空气格,不参与任何分层、
    /// 矿石或树木判定,因此这三者的生成结果逐位不变。
    ///
    /// 无门控分支:Go 侧关闭 `fluidEnabled` 时 `materials.water == materials.air`,
    /// 本步逐格把空气写回空气,输出与未引入流体的基线逐位一致(design D6)。
    fn flood_sea_level(&self, dense: &mut [u16]) {
        let m = self.materials;
        const LAYER_CELLS: usize = (SECTION_SIZE as usize) * (SECTION_SIZE as usize);
        for y in WORLD_MIN_Y..=SEA_LEVEL_Y {
            let layer = (y - WORLD_MIN_Y) as usize * LAYER_CELLS;
            for cell in &mut dense[layer..layer + LAYER_CELLS] {
                if *cell == m.air {
                    *cell = m.water;
                }
            }
        }
    }

    /// 把覆盖当前区块的有效候选树写入 dense 数组,与 Go `applyOakTrees` 一致:
    /// 树按 cellZ 外层、cellX 内层顺序应用;单棵树按 y/z/x 顺序写入;
    /// 原木可覆盖空气与树叶,树叶仅覆盖空气。
    fn apply_oak_trees(&self, chunk_x: i32, chunk_z: i32, dense: &mut [u16]) {
        let m = self.materials;
        let base_x = chunk_x << SECTION_SHIFT;
        let base_z = chunk_z << SECTION_SHIFT;
        let cell_z_min = (base_z - 2) >> OAK_TREE_CELL_SHIFT;
        let cell_z_max = (base_z + SECTION_SIZE + 1) >> OAK_TREE_CELL_SHIFT;
        let cell_x_min = (base_x - 2) >> OAK_TREE_CELL_SHIFT;
        let cell_x_max = (base_x + SECTION_SIZE + 1) >> OAK_TREE_CELL_SHIFT;
        for cell_z in cell_z_min..=cell_z_max {
            for cell_x in cell_x_min..=cell_x_max {
                let Some(tree) = self.oak_tree_for_cell(cell_x, cell_z) else {
                    continue;
                };
                for y in tree.root_y..=tree.root_y + tree.height {
                    for z in tree.root_z - 2..=tree.root_z + 2 {
                        for x in tree.root_x - 2..=tree.root_x + 2 {
                            // 与 Go `pos.Chunk() != chunk.Pos` 判定等价:
                            // 世界坐标算术右移 4 即 floor 除 16。
                            if (x >> SECTION_SHIFT) != chunk_x
                                || (z >> SECTION_SHIFT) != chunk_z
                                || !(WORLD_MIN_Y..WORLD_MAX_Y).contains(&y)
                            {
                                continue;
                            }
                            let block = oak_tree_block_at(&tree, &m, x, y, z);
                            if block == m.air {
                                continue;
                            }
                            let index =
                                dense_index(x & (SECTION_SIZE - 1), y, z & (SECTION_SIZE - 1));
                            let current = dense[index];
                            if block == m.oak_log && (current == m.air || current == m.leaves) {
                                dense[index] = block;
                            }
                            if block == m.leaves && current == m.air {
                                dense[index] = block;
                            }
                        }
                    }
                }
            }
        }
    }
}

/// 候选橡树:根方块世界坐标与树干高度。
struct OakTree {
    root_x: i32,
    root_y: i32,
    root_z: i32,
    height: i32,
}

/// 树形在指定世界坐标的方块,树干优先于树叶,与 Go `oakTreeBlockAt` 一致。
fn oak_tree_block_at(tree: &OakTree, m: &Materials, x: i32, y: i32, z: i32) -> u16 {
    if tree.root_y < WORLD_MIN_Y || tree.root_y + tree.height >= WORLD_MAX_Y {
        return m.air;
    }
    let top_y = tree.root_y + tree.height - 1;
    if x == tree.root_x && z == tree.root_z && (tree.root_y..=top_y).contains(&y) {
        return m.oak_log;
    }
    let dx = (x - tree.root_x).abs();
    let dz = (z - tree.root_z).abs();
    match y - top_y {
        -2 | -1 if dx <= 2 && dz <= 2 && !(dx == 2 && dz == 2) => m.leaves,
        0 if dx <= 1 && dz <= 1 => m.leaves,
        1 if dx + dz <= 1 => m.leaves,
        _ => m.air,
    }
}

/// 用世界种子、三维坐标和 salt 生成稳定 64 位混合值,与 Go `oreHash` 一致。
///
/// Go 侧表达式 `hash ^= uint64(v) + K + hash<<6 + hash>>2` 为一串 uint64
/// 回绕加法后再异或,此处逐项用 wrapping_add 镜像。
fn ore_hash(seed: i64, x: i32, y: i32, z: i32, salt: u64) -> u64 {
    let mut hash = (seed as u64) ^ salt;
    for value in [i64::from(x), i64::from(y), i64::from(z)] {
        hash ^= (value as u64)
            .wrapping_add(0x9E37_79B9_7F4A_7C15)
            .wrapping_add(hash << 6)
            .wrapping_add(hash >> 2);
        hash = hash.wrapping_mul(0xFF51_AFD7_ED55_8CCD);
        hash ^= hash >> 33;
    }
    hash = hash.wrapping_mul(0xC4CE_B9FE_1A85_EC53);
    hash ^= hash >> 33;
    hash
}

/// dense 数组下标:`[y−min_y][lz][lx]` 布局,y 在外层便于 Go 顺序回写。
pub(crate) fn dense_index(lx: i32, y: i32, lz: i32) -> usize {
    let layer = (y - WORLD_MIN_Y) as usize;
    layer * (SECTION_SIZE as usize) * (SECTION_SIZE as usize)
        + (lz as usize) * (SECTION_SIZE as usize)
        + lx as usize
}

// ---- ABI 编码常量与解析 ----
//
// 两个 worldgen 入口共用 magic `MGW1` 的 564 字节 header:
// magic(4) + layout version(4) + seed(8) + min_y(4) + max_y(4) +
// 材料表 14×u16(28) + perm 512×u8(512)。
//
// engine ABI v4 把材料表从 13 项扩到 14 项(末项 water),新增的 u16 正当占用
// v3 预留的 reserved 槽(偏移 50):该偏移的语义由"保留、必须为 0"变成"water
// 的方块编号",header 总长与 perm 偏移不变,但**布局语义确实变了**,因此
// layout version 1 → 2——它是独立于 ABI 版本号的带内第二道混装防线。
//
// **不再保留空槽是刻意选择,不是漏了**:新增一个 reserved 槽本身就要把 perm
// 往后挪,而 reserved 的意义是推迟这个代价、不是提前支付;何况下一次扩字段
// 必然同样改动材料表布局、必然升 ABI 版本,而 ABI 版本号每次调用都校验,
// 混装在那一步就被挡住。空槽在一个本来就不兼容的版本里买不到兼容性。
// chunk 入口追加 chunk_x/chunk_z(8);probe 入口追加 record_count(4) 与
// 每条 16 字节的查询记录(mode + wx/wy/wz)。

/// 共用 header 字节数。
pub(crate) const WORLDGEN_HEADER_BYTES: usize = 564;
/// chunk 入口输入总字节数:header + chunk_x/chunk_z。
pub(crate) const WORLDGEN_CHUNK_INPUT_BYTES: usize = WORLDGEN_HEADER_BYTES + 8;
/// chunk 入口输出字节数:98304 个 u16 LE。
pub(crate) const WORLDGEN_CHUNK_OUTPUT_BYTES: usize = CHUNK_VOLUME * 2;
/// probe 入口单批最大记录数,沿用 raycast 的 64-record batch 约定。
pub(crate) const WORLDGEN_PROBE_MAX_RECORDS: usize = 64;
/// probe 输入记录字节数:mode(4) + wx/wy/wz(12)。
pub(crate) const WORLDGEN_PROBE_RECORD_BYTES: usize = 16;
/// probe 输出记录字节数:height(4) + block(2) + reserved(2)。
pub(crate) const WORLDGEN_PROBE_OUTPUT_RECORD_BYTES: usize = 8;

fn read_u16(bytes: &[u8], offset: usize) -> u16 {
    u16::from_le_bytes([bytes[offset], bytes[offset + 1]])
}

/// 从字节流读取小端 u32;lod 模块解析 tile 输入时共用(避免第二份解码)。
pub(crate) fn read_u32(bytes: &[u8], offset: usize) -> u32 {
    u32::from_le_bytes(bytes[offset..offset + 4].try_into().unwrap())
}

/// 从字节流读取小端 i32;lod 模块解析 tile 坐标与列数时共用。
pub(crate) fn read_i32(bytes: &[u8], offset: usize) -> i32 {
    i32::from_le_bytes(bytes[offset..offset + 4].try_into().unwrap())
}

fn read_i64(bytes: &[u8], offset: usize) -> i64 {
    i64::from_le_bytes(bytes[offset..offset + 8].try_into().unwrap())
}

/// 解析并校验共用 header;任何违约返回 None(FFI 层转为 StatusInput)。
///
/// 校验项:magic/layout 精确匹配、Y 范围必须与内核常量一致(防止 Go/Rust
/// 世界高度漂移)、材料表 14 项两两互异(air 是哨兵,重复 ID 会破坏与 Go
/// 语义的对应关系)。perm 为 u8,取值域即合法域。
///
/// v3 的 `reserved != 0` 校验随字段一起消失:偏移 50 已被 water 正当占用,
/// 校验对象不复存在。混装由 ABI 版本号与 layout version 两道拦截。
///
/// 互异性的**唯一豁免**是 `water == air`:这是 Go 侧 `fluidEnabled` 关闭时
/// 的门控编码(design D6),water 只被写入、从不参与等值比较,取 air 编号
/// 即让注水退化为空操作。water 与其余 12 项重复仍然拒绝——那只可能是
/// Go/Rust 材料表漂移。
pub(crate) fn parse_header(bytes: &[u8]) -> Option<WorldgenParams> {
    if bytes.len() < WORLDGEN_HEADER_BYTES
        || &bytes[0..4] != b"MGW1"
        || read_u32(bytes, 4) != 2
        || read_i32(bytes, 16) != WORLD_MIN_Y
        || read_i32(bytes, 20) != WORLD_MAX_Y
    {
        return None;
    }
    let seed = read_i64(bytes, 8);
    let materials = Materials {
        air: read_u16(bytes, 24),
        stone: read_u16(bytes, 26),
        dirt: read_u16(bytes, 28),
        grass: read_u16(bytes, 30),
        bedrock: read_u16(bytes, 32),
        snow: read_u16(bytes, 34),
        sand: read_u16(bytes, 36),
        clay: read_u16(bytes, 38),
        gravel: read_u16(bytes, 40),
        iron_ore: read_u16(bytes, 42),
        coal_ore: read_u16(bytes, 44),
        oak_log: read_u16(bytes, 46),
        leaves: read_u16(bytes, 48),
        water: read_u16(bytes, 50),
    };
    let ids = materials.as_array();
    // as_array 的顺序:0 = air,13 = water。(0, 13) 这一对是门控豁免。
    const AIR_INDEX: usize = 0;
    const WATER_INDEX: usize = 13;
    for i in 0..ids.len() {
        for j in i + 1..ids.len() {
            if ids[i] == ids[j] && !(i == AIR_INDEX && j == WATER_INDEX) {
                return None;
            }
        }
    }
    let mut perm = [0u8; 512];
    perm.copy_from_slice(&bytes[52..WORLDGEN_HEADER_BYTES]);
    Some(WorldgenParams {
        seed,
        materials,
        perm,
    })
}

/// 解析 chunk 入口输入,返回参数与区块坐标。
pub(crate) fn parse_chunk_input(bytes: &[u8]) -> Option<(WorldgenParams, i32, i32)> {
    if bytes.len() != WORLDGEN_CHUNK_INPUT_BYTES {
        return None;
    }
    let params = parse_header(bytes)?;
    let chunk_x = read_i32(bytes, WORLDGEN_HEADER_BYTES);
    let chunk_z = read_i32(bytes, WORLDGEN_HEADER_BYTES + 4);
    Some((params, chunk_x, chunk_z))
}

/// 单条 probe 查询记录。mode:0=HeightAt,1=TerrainBlockAt,2=BaseBlockAt。
pub(crate) struct ProbeRecord {
    pub mode: u32,
    pub wx: i32,
    pub wy: i32,
    pub wz: i32,
}

/// 解析 probe 入口输入,返回参数与查询记录;record_count 必须在 1..=64,
/// 长度必须与记录数精确匹配,mode 越界拒绝。
pub(crate) fn parse_probe_input(bytes: &[u8]) -> Option<(WorldgenParams, Vec<ProbeRecord>)> {
    if bytes.len() < WORLDGEN_HEADER_BYTES + 4 {
        return None;
    }
    let count = read_u32(bytes, WORLDGEN_HEADER_BYTES) as usize;
    if count == 0
        || count > WORLDGEN_PROBE_MAX_RECORDS
        || bytes.len() != WORLDGEN_HEADER_BYTES + 4 + count * WORLDGEN_PROBE_RECORD_BYTES
    {
        return None;
    }
    let params = parse_header(bytes)?;
    let mut records = Vec::with_capacity(count);
    for index in 0..count {
        let offset = WORLDGEN_HEADER_BYTES + 4 + index * WORLDGEN_PROBE_RECORD_BYTES;
        let mode = read_u32(bytes, offset);
        if mode > 2 {
            return None;
        }
        records.push(ProbeRecord {
            mode,
            wx: read_i32(bytes, offset + 4),
            wy: read_i32(bytes, offset + 8),
            wz: read_i32(bytes, offset + 12),
        });
    }
    Some((params, records))
}

/// 执行一批 probe 查询,把结果按输出布局写入 out(每条 8 字节)。
///
/// mode 0 写 height 字段,mode 1/2 写 block 字段;未使用字段保持零,
/// 保证输出字节完全由输入决定。
pub(crate) fn run_probe(params: &WorldgenParams, records: &[ProbeRecord], out: &mut [u8]) {
    debug_assert_eq!(
        out.len(),
        records.len() * WORLDGEN_PROBE_OUTPUT_RECORD_BYTES
    );
    for (index, record) in records.iter().enumerate() {
        let offset = index * WORLDGEN_PROBE_OUTPUT_RECORD_BYTES;
        let mut height = 0i32;
        let mut block = 0u16;
        match record.mode {
            0 => height = params.height_at(record.wx, record.wz),
            1 => block = params.terrain_block_at(record.wx, record.wy, record.wz),
            _ => block = params.base_block_at(record.wx, record.wy, record.wz),
        }
        out[offset..offset + 4].copy_from_slice(&height.to_le_bytes());
        out[offset + 4..offset + 6].copy_from_slice(&block.to_le_bytes());
        out[offset + 6] = 0;
        out[offset + 7] = 0;
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// 测试材料表:取值互异即可,具体数值不影响结构断言。
    /// water 取 13(与 air 不同)代表门控开启态。
    fn materials() -> Materials {
        materials_with_water(13)
    }

    /// 指定 water 编号的测试材料表:传 0(= air)即门控关闭态。
    fn materials_with_water(water: u16) -> Materials {
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
            water,
        }
    }

    /// 恒等 perm 表足以驱动结构性断言(确定性、分层、树形)。
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

    /// 打乱的 perm 表。恒等 perm 在整数格点噪声恒为 0,地形近乎平面、
    /// 海平面以下没有空气格,注水断言会退化成空断言;这里用确定性的
    /// LCG 洗牌造出真实起伏的地形。
    fn shuffled_perm(seed: u64) -> [u8; 512] {
        let mut base: [u8; 256] = std::array::from_fn(|i| i as u8);
        let mut state = seed | 1;
        for i in (1..256usize).rev() {
            state = state
                .wrapping_mul(6364136223846793005)
                .wrapping_add(1442695040888963407);
            let j = (state >> 33) as usize % (i + 1);
            base.swap(i, j);
        }
        std::array::from_fn(|i| base[i & 255])
    }

    /// 起伏地形 + 指定 water 编号的参数;注水相关测试统一用它。
    fn params_water(seed: i64, water: u16) -> WorldgenParams {
        WorldgenParams {
            seed,
            materials: materials_with_water(water),
            perm: shuffled_perm(seed as u64),
        }
    }

    #[test]
    fn perlin_is_zero_at_lattice_points() {
        let p = params(1);
        for i in -8..8 {
            assert_eq!(p.noise_at(f64::from(i), f64::from(i * 3)), 0.0);
        }
    }

    #[test]
    fn generate_chunk_is_deterministic() {
        let p = params(42);
        let mut a = vec![0u16; CHUNK_VOLUME];
        let mut b = vec![0u16; CHUNK_VOLUME];
        p.generate_chunk(-1, 2, &mut a);
        p.generate_chunk(-1, 2, &mut b);
        assert_eq!(a, b);
    }

    #[test]
    fn chunk_matches_pointwise_base_block() {
        let p = params(7);
        let mut dense = vec![0u16; CHUNK_VOLUME];
        p.generate_chunk(1, -1, &mut dense);
        for y in WORLD_MIN_Y..WORLD_MAX_Y {
            for lz in 0..SECTION_SIZE {
                for lx in 0..SECTION_SIZE {
                    let wx = (1 << SECTION_SHIFT) + lx;
                    let wz = (-1 << SECTION_SHIFT) + lz;
                    assert_eq!(
                        dense[dense_index(lx, y, lz)],
                        p.base_block_at(wx, y, wz),
                        "({wx},{y},{wz})"
                    );
                }
            }
        }
    }

    #[test]
    fn terrain_layers_follow_go_rules() {
        let p = params(3);
        // 底层是基岩,地表是草或雪/沙系,高度之上是空气。
        assert_eq!(p.terrain_layer(WORLD_MIN_Y, 80), p.materials.bedrock);
        assert_eq!(p.terrain_layer(90, 80), p.materials.air);
        assert_eq!(p.terrain_layer(80, 80), p.materials.grass);
        assert_eq!(p.terrain_layer(78, 80), p.materials.dirt);
        assert_eq!(p.terrain_layer(60, 80), p.materials.stone);
    }

    #[test]
    fn ore_hash_is_stable_and_salt_sensitive() {
        let a = ore_hash(42, 1, 2, 3, COAL_SALT);
        assert_eq!(a, ore_hash(42, 1, 2, 3, COAL_SALT));
        assert_ne!(a, ore_hash(42, 1, 2, 3, IRON_SALT));
        assert_ne!(a, ore_hash(43, 1, 2, 3, COAL_SALT));
    }

    #[test]
    fn tree_canopy_shape_is_log_priority() {
        let tree = OakTree {
            root_x: 0,
            root_y: 100,
            root_z: 0,
            height: 4,
        };
        let m = materials();
        // 树干整列是原木,冠顶十字是树叶,冠层角落空缺。
        assert_eq!(oak_tree_block_at(&tree, &m, 0, 100, 0), m.oak_log);
        assert_eq!(oak_tree_block_at(&tree, &m, 0, 103, 0), m.oak_log);
        assert_eq!(oak_tree_block_at(&tree, &m, 1, 104, 0), m.leaves);
        assert_eq!(oak_tree_block_at(&tree, &m, 1, 104, 1), m.air);
        assert_eq!(oak_tree_block_at(&tree, &m, 2, 102, 2), m.air);
        assert_eq!(oak_tree_block_at(&tree, &m, 2, 102, 1), m.leaves);
    }
    #[test]
    fn sea_level_constants_agree() {
        // SEA_LEVEL_Y 是 SEA_LEVEL 的整数副本,漂移会让注水高度与地形高度脱节。
        assert_eq!(f64::from(SEA_LEVEL_Y), SEA_LEVEL);
    }

    /// 生成"干"(water = air,门控关闭)与"湿"(water = 13,门控开启)两份
    /// 同种子同区块 dense,并返回被注水改写的格数。
    fn dry_and_wet(seed: i64, cx: i32, cz: i32) -> (Vec<u16>, Vec<u16>, usize) {
        let mut dry = vec![0u16; CHUNK_VOLUME];
        let mut wet = vec![0u16; CHUNK_VOLUME];
        params_water(seed, 0).generate_chunk(cx, cz, &mut dry);
        params_water(seed, 13).generate_chunk(cx, cz, &mut wet);
        let changed = dry.iter().zip(&wet).filter(|(a, b)| a != b).count();
        (dry, wet, changed)
    }

    #[test]
    fn flooding_only_replaces_air_at_or_below_sea_level() {
        let (dry, wet, changed) = dry_and_wet(42, 3, -5);
        // 夹具前提:这个区块必须真的有海平面以下的空气格,否则下面全是空断言。
        assert!(changed > 0, "夹具失效:该区块没有可注水的格");
        for y in WORLD_MIN_Y..WORLD_MAX_Y {
            for lz in 0..SECTION_SIZE {
                for lx in 0..SECTION_SIZE {
                    let index = dense_index(lx, y, lz);
                    let expected = if dry[index] == 0 && y <= SEA_LEVEL_Y {
                        13
                    } else {
                        dry[index]
                    };
                    assert_eq!(wet[index], expected, "({lx},{y},{lz})");
                }
            }
        }
    }

    #[test]
    fn flooding_preserves_terrain_ore_and_trees() {
        let (dry, wet, changed) = dry_and_wet(42, 3, -5);
        assert!(changed > 0, "夹具失效:该区块没有可注水的格");
        // 分层(石/土/草/基岩/雪/沙/黏土/砂砾)、矿石与树木的每一格都必须原样保留。
        let mut seen_ore = false;
        let mut seen_tree = false;
        for (index, &block) in dry.iter().enumerate() {
            if block == 0 {
                continue;
            }
            assert_eq!(wet[index], block, "注水改写了非空气格 index={index}");
            seen_ore |= block == 9 || block == 10;
            seen_tree |= block == 11 || block == 12;
        }
        // 夹具前提:该区块必须真的含矿石与树木,否则"不受影响"是空断言。
        assert!(seen_ore, "夹具失效:该区块没有矿石");
        assert!(seen_tree, "夹具失效:该区块没有树木");
    }

    #[test]
    fn gate_off_leaves_every_floodable_cell_as_air() {
        // 门控关闭(water = air)时,注水必须整体退化为空操作:开启态被注水的
        // **每一格**在关闭态都必须仍是空气,且输出里不允许出现 13 号方块。
        //
        // 这条断言的对象是"内核是否老老实实用 materials.water 写入":一旦
        // flood_sea_level 绕过材料表硬编码水的编号,Go 侧的门控(water 填 air)
        // 就被架空,关闭态会长出水,本测试立刻变红。
        let (dry, wet, changed) = dry_and_wet(42, 3, -5);
        // 先断言"关闭态没有水",再断言夹具非空:顺序如此是为了让内核硬编码
        // 水编号这类真实故障报出"关闭态出现了水",而不是被后面的夹具守卫
        // 抢先报成"夹具失效"(硬编码会让两态输出相同,changed 归零)。
        assert!(!dry.contains(&13), "门控关闭时输出里出现了水");
        assert!(changed > 0, "夹具失效:该区块没有可注水的格");
        let mut checked = 0;
        for (index, &block) in wet.iter().enumerate() {
            if block == 13 {
                assert_eq!(dry[index], 0, "门控关闭时 index={index} 本应仍是空气");
                checked += 1;
            }
        }
        assert_eq!(checked, changed, "两态差异应当恰好是被注水的格");
    }

    #[test]
    fn chunk_and_pointwise_agree_on_water() {
        let p = params_water(7, 13);
        let mut dense = vec![0u16; CHUNK_VOLUME];
        p.generate_chunk(1, -1, &mut dense);
        let mut water_cells = 0;
        for y in WORLD_MIN_Y..WORLD_MAX_Y {
            for lz in 0..SECTION_SIZE {
                for lx in 0..SECTION_SIZE {
                    let wx = (1 << SECTION_SHIFT) + lx;
                    let wz = (-1 << SECTION_SHIFT) + lz;
                    let block = dense[dense_index(lx, y, lz)];
                    assert_eq!(block, p.base_block_at(wx, y, wz), "({wx},{y},{wz})");
                    if block == 13 {
                        water_cells += 1;
                        assert!(y <= SEA_LEVEL_Y, "海平面以上出现水 ({wx},{y},{wz})");
                    }
                }
            }
        }
        assert!(water_cells > 0, "夹具失效:该区块没有水");
    }

    #[test]
    fn header_allows_water_equal_to_air_but_rejects_other_duplicates() {
        // 门控关闭时 Go 侧把 water 填成 air 编号,header 必须接受。
        let mut bytes = vec![0u8; WORLDGEN_HEADER_BYTES];
        bytes[0..4].copy_from_slice(b"MGW1");
        bytes[4..8].copy_from_slice(&2u32.to_le_bytes());
        bytes[16..20].copy_from_slice(&WORLD_MIN_Y.to_le_bytes());
        bytes[20..24].copy_from_slice(&WORLD_MAX_Y.to_le_bytes());
        for index in 0..13usize {
            let id = (index as u16) + 1;
            bytes[24 + index * 2..26 + index * 2].copy_from_slice(&id.to_le_bytes());
        }
        // air = 1,water = 1:门控关闭态,必须通过。
        bytes[50..52].copy_from_slice(&1u16.to_le_bytes());
        assert!(parse_header(&bytes).is_some());
        // water = 14:门控开启态,必须通过。
        bytes[50..52].copy_from_slice(&14u16.to_le_bytes());
        assert!(parse_header(&bytes).is_some());
        // water = stone(2):这只可能是材料表漂移,必须拒绝。
        bytes[50..52].copy_from_slice(&2u16.to_le_bytes());
        assert!(parse_header(&bytes).is_none());
    }
}
