use crate::input::MeshInput;
use crate::light::LightScratch;
use crate::quad::{FULL_FLUID_HEIGHT, Face, Quad};

#[derive(Debug, Eq, PartialEq)]
pub(crate) enum MeshError {
    OutputOverflow,
}

#[derive(Copy, Clone, Default, Eq, PartialEq)]
struct MaskCell {
    used: bool,
    material: u16,
    ao: u8,
    light: u8,
    /// 该面所属方块是否是流体：为真时禁止贪心合并（见 mesh_section 的说明）。
    fluid: bool,
    /// 四个顶点的 4-bit 高度原值，顺序见 `Quad::corners`。
    corners: [u8; 4],
}

const FACES: [Face; 6] = [
    Face::NegX,
    Face::PosX,
    Face::NegY,
    Face::PosY,
    Face::NegZ,
    Face::PosZ,
];

/// 植物材质层的闭区间：`material ∈ [FIRST, LAST]` 即判定该格是植物。
///
/// 「一格是不是植物」以 **material 为准**（design D8）：判别不占任何 quad 位，
/// 而 quad 布局只剩 bit 63 一个空闲位且必须留空。registry 条目布局同样一字不改
/// （engine ABI 仍是 v5），没有新增「是不是植物」的属性字节。
///
/// 数值的真值源在 Go 侧 `internal/assets` 的 `LayerWheat0..LayerWheat7`，并由
/// `internal/mesh` 的 `PlantMaterialFirst/PlantMaterialLast` 复述一份。三处没有
/// 共享常量也没有生成步骤，只能人手同步——Go 两处相等由
/// `TestPlantMaterialLayersMatchMeshContract` 钉住，跨语言一致由真的把小麦喂进
/// 本 mesher 的 `TestNativeOracleParityWheatCrossPlanes` 兜底。在 Go 的层枚举里
/// 往小麦**之前**插层会整体平移这段区间，那两条守卫就是唯一会报警的地方。
const PLANT_MATERIAL_FIRST: u16 = 31;
const PLANT_MATERIAL_LAST: u16 = 38;

/// 单个植物格产生的面实例数**固定上界**。
///
/// 两条对角线 × 正反两面 = 4，`plant-visual-presentation` 把「每格面实例数 MUST
/// 有固定上界」写成 MUST。4 小于普通方块的 6，所以整段的输出上界不变，Go 侧的
/// `maxNativeQuads = 6 * BlocksPerSection` 依旧覆盖最坏情况。
const PLANT_QUADS_PER_CELL: usize = 4;

/// 植物格的四条 quad：对角线 A 正/背，对角线 B 正/背。次序固定，Go oracle 逐条对齐。
const PLANT_QUADS: [(Face, bool); PLANT_QUADS_PER_CELL] = [
    (Face::PlantDiagA, false),
    (Face::PlantDiagA, true),
    (Face::PlantDiagB, false),
    (Face::PlantDiagB, true),
];

pub(crate) fn mesh_section(
    input: &MeshInput<'_>,
    light: &LightScratch<'_>,
    output: &mut [u64],
) -> Result<usize, MeshError> {
    if center_is_air(input) {
        return Ok(0);
    }

    let mut count = 0;
    for face in FACES {
        let axis = (face as usize) >> 1;
        let u = (axis + 1) % 3;
        let v = (axis + 2) % 3;
        let step = if (face as u8) & 1 == 1 { 1 } else { -1 };

        for slice in 0..16 {
            let mut mask = [MaskCell::default(); 256];
            let mut any = false;
            for vi in 0..16 {
                for ui in 0..16 {
                    let mut p = [0; 3];
                    p[axis] = slice;
                    p[u] = ui;
                    p[v] = vi;
                    let id = input.block(p[0], p[1], p[2]);
                    let mut q = p;
                    q[axis] += step;
                    if !input
                        .registry
                        .face_visible(id, input.block(q[0], q[1], q[2]))
                    {
                        continue;
                    }
                    let Some(material) = input.registry.material(id, face as usize) else {
                        continue;
                    };
                    let fluid = input.registry.fluid_height(id).is_some();
                    mask[(vi * 16 + ui) as usize] = MaskCell {
                        used: true,
                        material,
                        ao: compute_ao(input, p, axis, u, v, step),
                        light: light.at(q[0], q[1], q[2]),
                        fluid,
                        corners: if fluid {
                            fluid_corners(input, p, face, axis, u, v)
                        } else {
                            [0; 4]
                        },
                    };
                    any = true;
                }
            }
            if !any {
                continue;
            }

            for vi in 0..16 {
                let mut ui = 0;
                while ui < 16 {
                    let cell = mask[vi * 16 + ui];
                    if !cell.used {
                        ui += 1;
                        continue;
                    }

                    // 水面按 1×1 出面，不贪心合并：角高度逐格不同，合并会把相邻格
                    // 的高度抹平成一张平面；而且合并后 w/h 那 8 bit 已被角高度占用，
                    // 尺寸再也无法表达（见 Quad::corners 的位布局）。
                    let mut width = 1;
                    let mut height = 1;
                    if !cell.fluid {
                        while ui + width < 16 && mask[vi * 16 + ui + width] == cell {
                            width += 1;
                        }
                        'grow: while vi + height < 16 {
                            for offset in 0..width {
                                if mask[(vi + height) * 16 + ui + offset] != cell {
                                    break 'grow;
                                }
                            }
                            height += 1;
                        }
                    }
                    for dv in 0..height {
                        for du in 0..width {
                            mask[(vi + dv) * 16 + ui + du] = MaskCell::default();
                        }
                    }

                    let Some(slot) = output.get_mut(count) else {
                        return Err(MeshError::OutputOverflow);
                    };
                    let mut p = [0; 3];
                    p[axis] = slice;
                    p[u] = ui as i32;
                    p[v] = vi as i32;
                    *slot = Quad {
                        x: p[0] as u8,
                        y: p[1] as u8,
                        z: p[2] as u8,
                        w: width as u8,
                        h: height as u8,
                        face,
                        material: cell.material,
                        ao: cell.ao,
                        light: cell.light,
                        corners: cell.corners,
                        back: false,
                    }
                    .pack();
                    count += 1;
                    ui += width;
                }
            }
        }
    }

    count = mesh_plants(input, light, output, count)?;
    Ok(count)
}

/// is_plant 报告一格是不是植物，判据是它的 material 落在植物区间。
///
/// 取 face 0 的 material 即可：植物六个面共用同一层（交叉斜面没有"朝向"），
/// Go 侧 `assets.Registry.Material` 对作物的全部 face 返回同一个 `LayerWheatN`。
fn is_plant(input: &MeshInput<'_>, id: u16) -> bool {
    matches!(
        input.registry.material(id, 0),
        Some(material) if (PLANT_MATERIAL_FIRST..=PLANT_MATERIAL_LAST).contains(&material)
    )
}

/// mesh_plants 为区段里每个植物格补出交叉斜面，返回新的 quad 总数。
///
/// 植物格的六个轴向面已经被上面的循环挡掉了——出面规则的唯一真值源是 Go 的
/// `assets.Registry.FaceVisible`，它对作物一律返回 false，烘焙进可见性位图后
/// Rust 这边连 mask 都不会写进去。于是这里是植物几何的**全部**来源。
///
/// 三条规则：
///
///  - **每格恰好 4 条**（两条对角线 × 正反两面），`w = h = 1`、不参与贪心合并。
///    合并后 `w`/`h` 那 8 bit 已被正背标志占用，尺寸再也表达不出来；而且交叉斜面
///    的形状是逐格的，合并等于把相邻两株抹成一片更大的斜板。
///  - **光照取正上方相邻格 `(x, y+1, z)`**。交叉斜面长在方块内部，没有"相邻空气面"
///    可采——轴向面采的是 `p + step` 那一格，植物根本不出轴向面。取上方格既是
///    `plant-visual-presentation` 的明文规则，也让露天作物直接拿到满天空光 15：
///    植物自身那格在列顶图里就是列顶，`sky_light` 判的是"严格高于列顶"，因此
///    该格自身恒为 0，只有它上方那格才是 15。
///  - **AO 记满**（`0xff`，四角均无遮蔽）。AO 的计算前提是"面贴在方块表面、四周
///    有共面邻居"，交叉斜面两者都不满足，硬算只会得到与视角无关的脏阴影。
///
/// 枚举次序是 `y → z → x`（与区段方块的存储次序一致），保证输出确定。
fn mesh_plants(
    input: &MeshInput<'_>,
    light: &LightScratch<'_>,
    output: &mut [u64],
    mut count: usize,
) -> Result<usize, MeshError> {
    for y in 0..16 {
        for z in 0..16 {
            for x in 0..16 {
                let id = input.block(x, y, z);
                if !is_plant(input, id) {
                    continue;
                }
                let Some(material) = input.registry.material(id, 0) else {
                    continue;
                };
                let light_above = light.at(x, y + 1, z);
                let before = count;
                for (face, back) in PLANT_QUADS {
                    let Some(slot) = output.get_mut(count) else {
                        return Err(MeshError::OutputOverflow);
                    };
                    *slot = Quad {
                        x: x as u8,
                        y: y as u8,
                        z: z as u8,
                        w: 1,
                        h: 1,
                        face,
                        material,
                        ao: 0xff,
                        light: light_above,
                        corners: [0; 4],
                        back,
                    }
                    .pack();
                    count += 1;
                }
                // 固定上界是一条 MUST，就地钉死：任何"顺手多出一片"的改动都在
                // 这里当场炸，而不是等到显存预算或 capture 门禁上才显形。
                assert_eq!(
                    count - before,
                    PLANT_QUADS_PER_CELL,
                    "植物格 ({x},{y},{z}) 产生了 {} 条面实例，固定上界是 {PLANT_QUADS_PER_CELL}",
                    count - before
                );
            }
        }
    }
    Ok(count)
}

/// cell_height 返回一格流体的 4-bit 高度原值，非流体返回 `None`。
///
/// 规则（design D2，全整数、无浮点）：
///
/// - 上方也是流体 → 取满格 `FULL_FLUID_HEIGHT`（15），使水柱内部无斜面、与上格无缝；
/// - 否则取 registry 里烘焙好的 `h_raw`（Go 侧 `14 - level`，源 14、最弱 7）。
fn cell_height(input: &MeshInput<'_>, x: i32, y: i32, z: i32) -> Option<u8> {
    let raw = input.registry.fluid_height(input.block(x, y, z))?;
    if input
        .registry
        .fluid_height(input.block(x, y + 1, z))
        .is_some()
    {
        return Some(FULL_FLUID_HEIGHT);
    }
    Some(raw)
}

/// corner_height 返回顶点格 `(vx, vz)` 在第 `y` 层上的 4-bit 角高度。
///
/// 一个顶点被四列共享：`(vx-1,vz-1) (vx,vz-1) (vx-1,vz) (vx,vz)`。角高度取其中
/// **流体格** `h_raw` 的整数平均（向下取整）；四格中任一格上方也是流体则直接取满格
/// 15。整除是唯一的算术，**不引入任何浮点**（spec：呈现不得引入浮点不确定性）。
///
/// 因为结果只由顶点坐标决定，两个水平相邻的流体格在共享边上必然读出同一个值，
/// 斜面于是天然连续。四格全非流体时返回 0，调用方只在流体格上调用它，不会命中。
fn corner_height(input: &MeshInput<'_>, vx: i32, y: i32, vz: i32) -> u8 {
    let mut sum = 0_u32;
    let mut count = 0_u32;
    for (dx, dz) in [(-1, -1), (0, -1), (-1, 0), (0, 0)] {
        let Some(height) = cell_height(input, vx + dx, y, vz + dz) else {
            continue;
        };
        if height == FULL_FLUID_HEIGHT {
            return FULL_FLUID_HEIGHT;
        }
        sum += u32::from(height);
        count += 1;
    }
    if count == 0 {
        return 0;
    }
    (sum / count) as u8
}

/// fluid_corners 求一个流体面四个顶点的高度原值，顺序见 `Quad::corners`。
///
/// 只有落在该格顶面那一层（世界 y == `p[1] + 1`）的顶点才带高度；侧面的两个下顶点
/// 与底面的四个顶点都在方块底面，语义上高度为 0。顶面四角全部带高度，底面全 0
/// 因而按普通 1×1 quad 打包——它本来就是平的，没有可插值的东西。
fn fluid_corners(
    input: &MeshInput<'_>,
    p: [i32; 3],
    face: Face,
    axis: usize,
    u: usize,
    v: usize,
) -> [u8; 4] {
    let mut corners = [0; 4];
    for (index, [du, dv]) in [[-1, -1], [1, -1], [1, 1], [-1, 1]].into_iter().enumerate() {
        let mut vertex = p;
        // 正方向面贴在方块的 +1 边，负方向面贴在 0 边。
        vertex[axis] += i32::from((face as u8) & 1);
        vertex[u] += (du + 1) / 2;
        vertex[v] += (dv + 1) / 2;
        if vertex[1] == p[1] + 1 {
            corners[index] = corner_height(input, vertex[0], p[1], vertex[2]);
        }
    }
    corners
}

pub(crate) fn center_is_air(input: &MeshInput<'_>) -> bool {
    for y in 0..16 {
        for z in 0..16 {
            for x in 0..16 {
                if input.block(x, y, z) != input.air_id {
                    return false;
                }
            }
        }
    }
    true
}

fn compute_ao(
    input: &MeshInput<'_>,
    p: [i32; 3],
    axis: usize,
    u: usize,
    v: usize,
    step: i32,
) -> u8 {
    let mut base = p;
    base[axis] += step;
    let solid = |du: i32, dv: i32| {
        let mut q = base;
        q[u] += du;
        q[v] += dv;
        u8::from(input.registry.opaque(input.block(q[0], q[1], q[2])))
    };

    let mut ao = 0;
    for (index, [du, dv]) in [[-1, -1], [1, -1], [1, 1], [-1, 1]].into_iter().enumerate() {
        let side_u = solid(du, 0);
        let side_v = solid(0, dv);
        let level = if side_u == 1 && side_v == 1 {
            0
        } else {
            3 - side_u - side_v - solid(du, dv)
        };
        ao |= level << (index * 2);
    }
    ao
}

#[cfg(test)]
mod tests {
    use super::{
        MeshError, PLANT_MATERIAL_FIRST, PLANT_QUADS, PLANT_QUADS_PER_CELL, compute_ao,
        mesh_section,
    };
    use crate::input::{MeshInput, tests::valid_input};
    use crate::light::{LIGHT_VOLUME, LightScratch};
    use crate::quad::{Face, Quad};

    const BLOCKS_OFFSET: usize = 16;
    const BLOCKS_BYTES: usize = 27 * 4096 * 2;
    const HEIGHTS_PRESENT_OFFSET: usize = BLOCKS_OFFSET + BLOCKS_BYTES;
    const HEIGHTS_BYTES: usize = 9 + 9 * 256 * 2;
    const REGISTRY_OFFSET: usize = HEIGHTS_PRESENT_OFFSET + HEIGHTS_BYTES;
    const ENTRY_BYTES: usize = crate::input::tests::ENTRY_BYTES;
    const STONE_ID: u16 = 1;
    const GLASS_ID: u16 = 40000;

    #[test]
    fn isolated_block_produces_six_unit_quads() {
        let mut bytes = base_input();
        set_block(&mut bytes, 8, 8, 8, STONE_ID);
        let input = parse(bytes);
        let light = dark_light();
        let mut output = [0; 6];

        assert_eq!(mesh_section(&input, &light, &mut output), Ok(6));
        for (face, packed) in output.into_iter().enumerate() {
            assert_eq!((packed >> 12) & 0xff, 0);
            assert_eq!((packed >> 20) & 7, face as u64);
            assert_eq!((packed >> 39) & 0xff, 0xff);
        }
        assert_eq!(light.at(8, 8, 8), 0);
    }

    #[test]
    fn flat_sixteen_by_sixteen_top_merges_to_one_quad() {
        let mut bytes = base_input();
        fill_slab(&mut bytes, STONE_ID);
        let input = parse(bytes);
        let light = dark_light();
        let mut output = [0; 1];

        assert_eq!(mesh_section(&input, &light, &mut output), Ok(1));
        assert_eq!((output[0] >> 20) & 7, 3);
        assert_eq!((output[0] >> 12) & 0xf, 15);
        assert_eq!((output[0] >> 16) & 0xf, 15);
    }

    #[test]
    fn two_top_materials_do_not_merge() {
        let mut bytes = base_input();
        fill_slab(&mut bytes, STONE_ID);
        set_visibility(&mut bytes, 1, 1);
        for x in 8..16 {
            for z in 0..16 {
                set_block(&mut bytes, x, 0, z, GLASS_ID);
            }
        }
        let input = parse(bytes);
        let light = dark_light();
        let mut output = [0; 2];

        assert_eq!(mesh_section(&input, &light, &mut output), Ok(2));
        assert_eq!((output[0] >> 12) & 0xff, 0x7f);
        assert_eq!((output[1] >> 12) & 0xff, 0x7f);
        assert_ne!((output[0] >> 23) & 0xffff, (output[1] >> 23) & 0xffff);
    }

    #[test]
    fn stone_glass_boundary_keeps_only_stone_face() {
        let mut bytes = base_input();
        set_block(&mut bytes, 7, 8, 8, STONE_ID);
        set_block(&mut bytes, 8, 8, 8, GLASS_ID);
        let input = parse(bytes);
        let light = dark_light();
        let mut output = [0; 12];
        let count = mesh_section(&input, &light, &mut output).unwrap();

        let stone = output[..count]
            .iter()
            .any(|packed| packed & 0xf == 7 && (packed >> 20) & 7 == 1);
        let glass = output[..count]
            .iter()
            .any(|packed| packed & 0xf == 8 && (packed >> 20) & 7 == 0);
        assert!(stone);
        assert!(!glass);
    }

    #[test]
    fn missing_neighbor_blocks_the_boundary_face() {
        let mut bytes = base_input();
        set_block(&mut bytes, 0, 8, 8, STONE_ID);
        set_block(&mut bytes, -1, 8, 8, STONE_ID);
        let input = parse(bytes);
        let light = dark_light();
        let mut output = [0; 6];
        let count = mesh_section(&input, &light, &mut output).unwrap();

        assert_eq!(count, 5);
        assert!(
            !output[..count]
                .iter()
                .any(|packed| packed & 0xf == 0 && (packed >> 20) & 7 == 0)
        );
    }

    #[test]
    fn occluded_top_face_preserves_go_corner_order() {
        let mut bytes = base_input();
        set_block(&mut bytes, 8, 8, 8, STONE_ID);
        set_block(&mut bytes, 7, 9, 8, STONE_ID);
        set_block(&mut bytes, 8, 9, 7, STONE_ID);
        let input = parse(bytes);
        let light = dark_light();
        let mut output = [0; 18];
        let count = mesh_section(&input, &light, &mut output).unwrap();

        let top = output[..count]
            .iter()
            .copied()
            .find(|packed| {
                packed & 0xf == 8
                    && (packed >> 4) & 0xf == 8
                    && (packed >> 8) & 0xf == 8
                    && (packed >> 20) & 7 == 3
            })
            .unwrap();
        assert_eq!((top >> 39) & 0xff, 0xb8);
    }

    #[test]
    fn asymmetric_ao_distinguishes_all_four_corners() {
        let mut bytes = base_input();
        set_block(&mut bytes, 8, 8, 8, STONE_ID);
        set_block(&mut bytes, 7, 9, 8, STONE_ID);
        set_block(&mut bytes, 8, 9, 9, STONE_ID);
        set_block(&mut bytes, 7, 9, 7, STONE_ID);
        let input = parse(bytes);

        let ao = compute_ao(&input, [8, 8, 8], 1, 2, 0, 1);

        assert_eq!(ao, 0xe1);
        assert_eq!(
            [ao & 3, (ao >> 2) & 3, (ao >> 4) & 3, (ao >> 6) & 3],
            [1, 0, 2, 3]
        );
    }

    #[test]
    fn asymmetric_fixture_preserves_complete_face_slice_row_order() {
        let mut bytes = base_input();
        set_block(&mut bytes, 2, 3, 4, STONE_ID);
        set_block(&mut bytes, 5, 1, 7, STONE_ID);
        set_block(&mut bytes, 2, 8, 1, GLASS_ID);
        let input = parse(bytes);
        let light = dark_light();
        let mut output = [0; 18];

        assert_eq!(mesh_section(&input, &light, &mut output), Ok(18));
        assert_eq!(
            output,
            [
                0x007fce20000182,
                0x007f8000800432,
                0x007f8000800715,
                0x007fce20900182,
                0x007f8001100432,
                0x007f8001100715,
                0x007f8001a00715,
                0x007f8001a00432,
                0x007fce21200182,
                0x007f8002300715,
                0x007f8002300432,
                0x007fce21b00182,
                0x007fce22400182,
                0x007f8002c00432,
                0x007f8002c00715,
                0x007fce22d00182,
                0x007f8003500432,
                0x007f8003500715,
            ]
        );
    }

    #[test]
    fn packed_light_samples_each_asymmetric_adjacent_cell() {
        let mut bytes = base_input();
        set_block(&mut bytes, 8, 8, 8, STONE_ID);
        let input = parse(bytes);
        let mut levels = vec![0; LIGHT_VOLUME];
        levels[54168] = 0x12;
        levels[58776] = 0x34;
        levels[56424] = 0x56;
        levels[56520] = 0x78;
        levels[56471] = 0x9a;
        levels[56473] = 0xbc;
        let mut queue = [];
        let light = LightScratch::new(&mut levels, &mut queue);
        let mut output = [0; 6];

        assert_eq!(mesh_section(&input, &light, &mut output), Ok(6));
        assert_eq!(
            output.map(|packed| ((packed >> 47) & 0xff) as u8),
            [0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc]
        );
    }

    #[test]
    fn one_short_output_reports_overflow() {
        let mut bytes = base_input();
        set_block(&mut bytes, 8, 8, 8, STONE_ID);
        let input = parse(bytes);
        let light = dark_light();
        let mut output = [0; 5];

        assert_eq!(
            mesh_section(&input, &light, &mut output),
            Err(MeshError::OutputOverflow)
        );
    }

    #[test]
    fn uniform_air_returns_before_reading_light() {
        let input = parse(base_input());
        let mut levels = [];
        let mut queue = [];
        let light = LightScratch::new(&mut levels, &mut queue);
        let mut output = [0; 1];

        assert_eq!(mesh_section(&input, &light, &mut output), Ok(0));
    }

    // ---- 斜水面几何 ----------------------------------------------------
    //
    // 水的 registry 夹具：id 0 = 空气、id 1 = 石头（同时是 barrier）、
    // id 10..=17 = 流体等级 0..7，fluid_height 取 14 - level（h_raw）。
    // 可见性位图按 assets.FaceVisible 的规则烘焙：石头对空气与水出面，
    // 水只对空气出面（水—水、水—石头都不出面）。

    const WATER_COUNT: usize = 8;
    const WATER_ENTRIES: usize = 2 + WATER_COUNT;
    const WATER_BASE_ID: u16 = 10;

    /// water_id 返回等级 level 对应的方块编号。
    fn water_id(level: u8) -> u16 {
        WATER_BASE_ID + u16::from(level)
    }

    /// water_raw 是等级 level 孤立时的 4-bit 高度原值：源 14、最弱 7。
    fn water_raw(level: u8) -> u8 {
        14 - level
    }

    fn water_input() -> Vec<u8> {
        let mut bytes = vec![0; REGISTRY_OFFSET + WATER_ENTRIES * ENTRY_BYTES + WATER_ENTRIES * 8];
        bytes[0..4].copy_from_slice(b"MGM1");
        bytes[8..10].copy_from_slice(&(WATER_ENTRIES as u16).to_le_bytes());
        bytes[10..12].copy_from_slice(&1_u16.to_le_bytes());
        bytes[12..14].copy_from_slice(&0_u16.to_le_bytes());
        bytes[14..16].copy_from_slice(&1_u16.to_le_bytes());

        let mut write_entry = |index: usize, id: u16, opaque: bool, fluid_height: u8| {
            let entry = REGISTRY_OFFSET + index * ENTRY_BYTES;
            bytes[entry..entry + 2].copy_from_slice(&id.to_le_bytes());
            bytes[entry + 2] = u8::from(opaque);
            for face in 0..6 {
                let material = if id == 1 { 1_u16 } else { 100 };
                bytes[entry + 4 + face * 2..entry + 6 + face * 2]
                    .copy_from_slice(&material.to_le_bytes());
            }
            bytes[entry + 16] = fluid_height;
            bytes[entry + 17] = u8::from(fluid_height != 0);
        };
        write_entry(0, 0, false, 0);
        write_entry(1, 1, true, 0);
        for level in 0..WATER_COUNT {
            write_entry(
                2 + level,
                water_id(level as u8),
                false,
                water_raw(level as u8),
            );
        }

        // 可见性行：石头 = 对空气(列 0)与全部水(列 2..=9)出面；水 = 只对空气出面。
        let stone_row: u64 = 1 | (((1_u64 << WATER_COUNT) - 1) << 2);
        for index in 0..WATER_ENTRIES {
            let row: u64 = match index {
                0 => 0,
                1 => stone_row,
                _ => 1,
            };
            let offset = REGISTRY_OFFSET + WATER_ENTRIES * ENTRY_BYTES + index * 8;
            bytes[offset..offset + 8].copy_from_slice(&row.to_le_bytes());
        }
        bytes
    }

    /// mesh_water 网格化一份水夹具并解包成 Quad。
    fn mesh_water(bytes: Vec<u8>) -> Vec<Quad> {
        let input = parse(bytes);
        let light = dark_light();
        let mut output = vec![0; 6 * 4096];
        let count = mesh_section(&input, &light, &mut output).unwrap();
        output[..count].iter().copied().map(Quad::unpack).collect()
    }

    /// top_face 取指定格的顶面 quad。
    fn top_face(quads: &[Quad], x: u8, y: u8, z: u8) -> Quad {
        *quads
            .iter()
            .find(|quad| quad.face == Face::PosY && quad.x == x && quad.y == y && quad.z == z)
            .unwrap_or_else(|| panic!("缺少 ({x},{y},{z}) 的顶面"))
    }

    /// neg_x_face 取指定格朝 -X 的侧面 quad。
    fn neg_x_face(quads: &[Quad], x: u8, y: u8, z: u8) -> Quad {
        *quads
            .iter()
            .find(|quad| quad.face == Face::NegX && quad.x == x && quad.y == y && quad.z == z)
            .unwrap_or_else(|| panic!("缺少 ({x},{y},{z}) 的 -X 侧面"))
    }

    /// 单格高度：对 8 个流体编号穷举断言孤立水格的四角高度恒等于 h_raw = 14 - level。
    ///
    /// 孤立格四周无水，角高度的「四列平均」退化为该格自身的 h_raw，于是这条断言
    /// 同时钉住了 h_raw 的映射与平均的退化情形。
    #[test]
    fn isolated_cell_height_is_fourteen_minus_level() {
        for level in 0..8_u8 {
            let mut bytes = water_input();
            set_block(&mut bytes, 8, 8, 8, water_id(level));
            let quads = mesh_water(bytes);
            let top = top_face(&quads, 8, 8, 8);
            assert_eq!(
                top.corners,
                [water_raw(level); 4],
                "level={level} 的孤立水格四角应全为 h_raw"
            );
            assert_eq!((top.w, top.h), (1, 1), "水面必须按 1×1 出面");
        }
        // 最弱等级仍有半格（7 即 8/16），不会退化成零面积的水面。
        assert_eq!(water_raw(7), 7);
    }

    /// 水柱内部没有斜面：上方也是流体的格，其侧面上沿四角取满格 15 且彼此相等。
    ///
    /// 该格的顶面被「水—水不出面」规则挡掉，可观察的是侧面上沿——上沿与上格底面
    /// 齐平即水柱内部无缝。
    #[test]
    fn water_column_interior_is_full_height() {
        let mut bytes = water_input();
        // 故意用最弱等级：若「上方是流体则取满格」的规则丢失，高度会掉到 7。
        set_block(&mut bytes, 8, 8, 8, water_id(7));
        set_block(&mut bytes, 8, 9, 8, water_id(7));
        let quads = mesh_water(bytes);

        let side = neg_x_face(&quads, 8, 8, 8);
        assert_eq!(side.corners, [0, 15, 15, 0], "水柱内部侧面上沿应满格");
        assert!(
            !quads
                .iter()
                .any(|quad| quad.face == Face::PosY && quad.y == 8),
            "水柱内部不应出顶面"
        );

        // 顶格上方是空气，回到 h_raw = 7，与内部的 15 形成可分辨的对照。
        assert_eq!(top_face(&quads, 8, 9, 8).corners, [7; 4]);
    }

    /// 相邻不同水位之间连续过渡：共享边两侧顶面高度相等，且落在
    /// `较低的孤立高度 <= shared < 较高的孤立高度`。
    ///
    /// 夹具让整个区段只有这两格是水，满足 spec 里「共享边两端点周围只有这两格是
    /// 流体」的 WHEN；并对**全部 56 组有序等级对**遍历，不依赖任何单一取值。
    ///
    /// 上界为什么必须严格：`floor((a+b)/2) < max(a,b)`（`a != b`）对全部等级对成立，
    /// 而 design D2 明文否决的 max 规则会让 `shared` 等于 `strong`——**上界严格是
    /// max 唯一过不去的那道门**。下界只能取非严格：`shared = weak + floor(d/2)`，
    /// 等级只差 1 时 `shared` 恰好等于较弱格的孤立高度（真实水体里最常见的构型），
    /// 写成严格大于就是一条假的普遍规律。
    #[test]
    fn adjacent_levels_share_one_continuous_edge_height() {
        let mut checked = 0;
        let mut strictly_above_weak = 0;
        for a in 0..8_u8 {
            for b in 0..8_u8 {
                if a == b {
                    continue;
                }
                let mut bytes = water_input();
                set_block(&mut bytes, 8, 8, 8, water_id(a));
                set_block(&mut bytes, 9, 8, 8, water_id(b));
                let quads = mesh_water(bytes);

                let left = top_face(&quads, 8, 8, 8);
                let right = top_face(&quads, 9, 8, 8);
                // 顶面 axis=1 时 u=z、v=x，四个角 (du,dv) 依次映射到 (dz,dx) =
                // (0,0) (1,0) (1,1) (0,1)。dx=1 的 index 2、3 落在左格的 x+1 边界，
                // 也就是右格 dx=0 的 index 1、0——它们是同一条共享边的同两个端点。
                assert_eq!(
                    left.corners[3], right.corners[0],
                    "共享边端点必须同高 (a={a}, b={b})"
                );
                assert_eq!(
                    left.corners[2], right.corners[1],
                    "共享边端点必须同高 (a={a}, b={b})"
                );

                let shared = left.corners[3];
                let (weak, strong) = (
                    water_raw(a).min(water_raw(b)),
                    water_raw(a).max(water_raw(b)),
                );
                assert!(
                    shared >= weak,
                    "共享边高度 {shared} 低于较低的孤立高度 {weak} (a={a}, b={b})"
                );
                assert!(
                    shared < strong,
                    "共享边高度 {shared} 未严格低于较高的孤立高度 {strong} (a={a}, b={b})"
                );
                // 远离共享边的一侧仍保留各自的孤立高度：这确实是一段斜面，
                // 而不是把两格整体抬平到同一高度。
                assert_eq!(left.corners[0], water_raw(a), "(a={a}, b={b})");
                assert_eq!(right.corners[2], water_raw(b), "(a={a}, b={b})");

                checked += 1;
                if shared > weak {
                    strictly_above_weak += 1;
                }
            }
        }
        assert_eq!(checked, 56, "必须遍历全部有序等级对");
        // 防空转守卫排在真实故障断言之后：若每一对都恰好 shared == weak，下界的
        // `>=` 就退化成恒等、失去分辨力。等级差 >= 2 的 42 组必须严格高于较低值，
        // 等级差 == 1 的 14 组必须恰好相等——这两个数字把整条分布钉死。
        assert_eq!(
            strictly_above_weak, 42,
            "严格高于较低孤立高度的等级对数不对，斜面分布已退化"
        );
    }

    /// 等级越弱高度越低：等级更大的格顶面高度 MUST NOT 高于等级更小的，源最高。
    ///
    /// 8 个等级各放在互不接触的格子里（间隔 2），四邻情况因此完全相同。
    #[test]
    fn weaker_levels_are_never_higher() {
        let mut bytes = water_input();
        for level in 0..8_u8 {
            set_block(&mut bytes, i32::from(level) * 2, 8, 8, water_id(level));
        }
        let quads = mesh_water(bytes);

        let mut previous = None;
        let mut source_height = 0;
        for level in 0..8_u8 {
            let top = top_face(&quads, level * 2, 8, 8);
            let height = top.corners[0];
            assert!(
                top.corners.iter().all(|&corner| corner == height),
                "孤立格四角应相等"
            );
            if let Some(previous) = previous {
                assert!(height <= previous, "level={level} 的高度反而升高了");
            }
            if level == 0 {
                source_height = height;
            }
            assert!(height <= source_height, "源方块必须是最高的");
            previous = Some(height);
        }
        assert!(
            source_height > top_face(&quads, 14, 8, 8).corners[0],
            "源与最弱等级必须真的不同高，否则单调性断言是恒真的"
        );
    }

    /// 高度派生是确定的：同一份方块数据重复生成，每个角逐次相同。
    #[test]
    fn corner_heights_are_deterministic_across_runs() {
        let mut bytes = water_input();
        // 造一片高低错落的水域，让绝大多数角都落在「多列平均」这条路径上。
        for x in 0..12 {
            for z in 0..12 {
                let level = ((x * 5 + z * 3) % 8) as u8;
                set_block(&mut bytes, x, 8, z, water_id(level));
            }
        }
        set_block(&mut bytes, 4, 9, 4, water_id(0));

        let first = mesh_water(bytes.clone());
        let second = mesh_water(bytes);
        assert_eq!(first.len(), second.len());
        assert!(
            first
                .iter()
                .zip(&second)
                .all(|(a, b)| a.corners == b.corners),
            "重复生成的角高度必须逐次相同"
        );
        // 防空转：这片水域必须真的出现过多种角高度，否则「相同」是恒真的。
        let mut seen: Vec<u8> = first
            .iter()
            .filter(|quad| quad.face == Face::PosY)
            .map(|quad| quad.corners[0])
            .collect();
        seen.sort_unstable();
        seen.dedup();
        assert!(
            seen.len() >= 3,
            "夹具退化：只出现了 {} 种角高度",
            seen.len()
        );
    }

    /// 水面不贪心合并：一整层同等级的水必须出 256 条 1×1 顶面，而非合并成 1 条。
    #[test]
    fn flat_water_surface_emits_unit_quads() {
        let mut bytes = water_input();
        for x in 0..16 {
            for z in 0..16 {
                set_block(&mut bytes, x, 8, z, water_id(0));
            }
        }
        let quads = mesh_water(bytes);
        let tops: Vec<Quad> = quads
            .iter()
            .copied()
            .filter(|quad| quad.face == Face::PosY && quad.y == 8)
            .collect();
        assert_eq!(tops.len(), 256);
        assert!(tops.iter().all(|quad| quad.w == 1 && quad.h == 1));
        assert!(tops.iter().all(|quad| quad.corners == [14; 4]));
    }

    // ---- 植物交叉斜面 --------------------------------------------------
    //
    // 植物的 registry 夹具：id 0 = 空气、id 1 = 石头（同时是 barrier）、
    // id 20 = 小麦（非不透明、material 落在植物区间、非流体、不额外衰减天空光）。
    // 可见性位图按 assets.FaceVisible 的规则烘焙：石头对空气与小麦都出面，
    // **小麦对谁都不出面**——轴向面一条不出，几何全部来自 mesh_plants。

    const PLANT_ENTRIES: usize = 3;
    const WHEAT_ID: u16 = 20;
    /// 夹具里小麦用的材质层，取植物区间的第一层。
    const WHEAT_MATERIAL: u16 = PLANT_MATERIAL_FIRST;

    fn plant_input() -> Vec<u8> {
        let mut bytes = vec![0; REGISTRY_OFFSET + PLANT_ENTRIES * ENTRY_BYTES + PLANT_ENTRIES * 8];
        bytes[0..4].copy_from_slice(b"MGM1");
        bytes[8..10].copy_from_slice(&(PLANT_ENTRIES as u16).to_le_bytes());
        bytes[10..12].copy_from_slice(&1_u16.to_le_bytes());
        bytes[12..14].copy_from_slice(&0_u16.to_le_bytes());
        bytes[14..16].copy_from_slice(&1_u16.to_le_bytes());

        let mut write_entry = |index: usize, id: u16, opaque: bool, material: u16| {
            let entry = REGISTRY_OFFSET + index * ENTRY_BYTES;
            bytes[entry..entry + 2].copy_from_slice(&id.to_le_bytes());
            bytes[entry + 2] = u8::from(opaque);
            for face in 0..6 {
                bytes[entry + 4 + face * 2..entry + 6 + face * 2]
                    .copy_from_slice(&material.to_le_bytes());
            }
        };
        write_entry(0, 0, false, 0);
        write_entry(1, 1, true, 1);
        write_entry(2, WHEAT_ID, false, WHEAT_MATERIAL);

        // 石头 = 对空气(列 0)与小麦(列 2)出面；空气与小麦整行为 0。
        for (index, row) in [0_u64, 1 | 1 << 2, 0].into_iter().enumerate() {
            let offset = REGISTRY_OFFSET + PLANT_ENTRIES * ENTRY_BYTES + index * 8;
            bytes[offset..offset + 8].copy_from_slice(&row.to_le_bytes());
        }
        bytes
    }

    /// mesh_with_light 网格化一份夹具并解包成 Quad，光照体积由调用方给定。
    fn mesh_with_light(bytes: Vec<u8>, levels: Vec<u8>) -> Vec<Quad> {
        let input = parse(bytes);
        let light = LightScratch::new(
            Box::leak(levels.into_boxed_slice()),
            Box::leak(vec![0; LIGHT_VOLUME].into_boxed_slice()),
        );
        let mut output = vec![0; 6 * 4096];
        let count = mesh_section(&input, &light, &mut output).unwrap();
        output[..count].iter().copied().map(Quad::unpack).collect()
    }

    /// mesh_plant_fixture 网格化一份植物夹具（全暗光照）。
    fn mesh_plant_fixture(bytes: Vec<u8>) -> Vec<Quad> {
        mesh_with_light(bytes, vec![0; LIGHT_VOLUME])
    }

    /// light_slot 复刻 `light::light_index`，供夹具直接写某一格的光照值。
    fn light_slot(x: i32, y: i32, z: i32) -> usize {
        ((x + 16) as usize * 48 + (y + 16) as usize) * 48 + (z + 16) as usize
    }

    /// 一株孤立作物恰好出 4 条交叉斜面，且**一条轴向面都不出**。
    ///
    /// 断言是位置性的：不只数条数，还逐条钉住 `(face, back)` 的次序、`w`/`h`
    /// 与 material。若植物退回轴向满面，face 会落回 0..5，这条立刻红。
    /// 末尾的对照断言承重——同一帧里紧挨着的石头**必须**照常出朝向作物的那一面，
    /// 否则"作物不出轴向面"可能只是"这个夹具什么都没画"。
    #[test]
    fn isolated_plant_emits_four_cross_quads_and_no_axial_face() {
        let mut bytes = plant_input();
        set_block(&mut bytes, 8, 8, 8, WHEAT_ID);
        set_block(&mut bytes, 7, 8, 8, 1);
        let quads = mesh_plant_fixture(bytes);

        let plant: Vec<Quad> = quads
            .iter()
            .copied()
            .filter(|quad| quad.material == WHEAT_MATERIAL)
            .collect();
        assert_eq!(plant.len(), PLANT_QUADS_PER_CELL, "每格必须恰好 4 条面实例");
        assert_eq!(
            plant
                .iter()
                .map(|quad| (quad.face, quad.back))
                .collect::<Vec<_>>(),
            PLANT_QUADS.to_vec(),
            "四条 quad 的对角线与正背次序必须固定"
        );
        for quad in &plant {
            assert_eq!((quad.x, quad.y, quad.z), (8, 8, 8));
            assert_eq!((quad.w, quad.h), (1, 1), "植物 quad 必须是 1×1");
            assert_eq!(quad.corners, [0; 4], "植物 quad 不带角高度");
            assert!(quad.face.plant(), "植物 quad 的 face 必须是 6 或 7");
        }
        // 石头朝 +X（也就是朝作物）的那一面必须还在：作物非不透明、不遮挡邻居出面。
        assert!(
            quads
                .iter()
                .any(|quad| quad.face == Face::PosX && quad.x == 7 && quad.material == 1),
            "夹具无效：作物旁的石头没有画出朝向作物的面，"
        );
    }

    /// 相邻作物不合并：一整层 16×16 作物必须出 256 × 4 条 1×1 quad。
    ///
    /// 对照组是同一片面积的石头板——它**会**合并成一条 16×16 顶面。两组放在一起，
    /// "不合并"才是一条位置性断言而不是"这里恰好只有一格"。
    #[test]
    fn adjacent_plants_never_merge() {
        let mut bytes = plant_input();
        for x in 0..16 {
            for z in 0..16 {
                set_block(&mut bytes, x, 8, z, WHEAT_ID);
            }
        }
        let quads = mesh_plant_fixture(bytes);
        let plant: Vec<Quad> = quads
            .iter()
            .copied()
            .filter(|quad| quad.material == WHEAT_MATERIAL)
            .collect();
        assert_eq!(plant.len(), 256 * PLANT_QUADS_PER_CELL);
        assert!(plant.iter().all(|quad| quad.w == 1 && quad.h == 1));

        // 每一格都必须各自拥有整整 4 条：数量对得上、分布却塌到某几格上，
        // 上面那条断言照样绿。
        let mut per_cell = std::collections::BTreeMap::new();
        for quad in &plant {
            *per_cell.entry((quad.x, quad.y, quad.z)).or_insert(0_usize) += 1;
        }
        assert_eq!(per_cell.len(), 256, "植物 quad 没有铺满 256 格");
        assert!(
            per_cell.values().all(|&n| n == PLANT_QUADS_PER_CELL),
            "存在每格面实例数不等于 {PLANT_QUADS_PER_CELL} 的格子"
        );

        // 对照组：同样一整层石头顶面会贪心合并成一条，说明"不合并"确实是
        // 植物独有的规则，而不是这个 mesher 根本不会合并。
        let mut stone_bytes = plant_input();
        for x in 0..16 {
            for z in 0..16 {
                set_block(&mut stone_bytes, x, 8, z, 1);
            }
        }
        let stone_tops = mesh_plant_fixture(stone_bytes)
            .into_iter()
            .filter(|quad| quad.face == Face::PosY && quad.y == 8)
            .count();
        assert_eq!(
            stone_tops, 1,
            "对照组的石头顶面没有合并，整条断言失去分辨力"
        );
    }

    /// 光照取**正上方**相邻格，而不是植物自身那格、也不是任何侧邻。
    ///
    /// 夹具刻意让六个方向的光照两两不同：上方 0x9a、下方 0x34、四个侧邻各不相同、
    /// 植物自身 0x12。取错任何一个采样点，读数都会变——这正是"位置性"的要求。
    #[test]
    fn plant_light_is_sampled_from_the_cell_above() {
        let mut bytes = plant_input();
        set_block(&mut bytes, 8, 8, 8, WHEAT_ID);
        let mut levels = vec![0; LIGHT_VOLUME];
        levels[light_slot(8, 8, 8)] = 0x12;
        levels[light_slot(8, 9, 8)] = 0x9a;
        levels[light_slot(8, 7, 8)] = 0x34;
        levels[light_slot(7, 8, 8)] = 0x56;
        levels[light_slot(9, 8, 8)] = 0x78;
        levels[light_slot(8, 8, 7)] = 0xbc;
        levels[light_slot(8, 8, 9)] = 0xde;
        let quads = mesh_with_light(bytes, levels);

        let plant: Vec<Quad> = quads
            .iter()
            .copied()
            .filter(|quad| quad.material == WHEAT_MATERIAL)
            .collect();
        assert_eq!(plant.len(), PLANT_QUADS_PER_CELL);
        assert!(
            plant.iter().all(|quad| quad.light == 0x9a),
            "植物 quad 的光照必须取上方格 0x9a，实测 {:?}",
            plant.iter().map(|quad| quad.light).collect::<Vec<_>>()
        );
    }

    /// 输出缓冲差一条时，植物同样要报 OutputOverflow，而不是静默截断。
    #[test]
    fn plant_output_overflow_is_reported() {
        let mut bytes = plant_input();
        set_block(&mut bytes, 8, 8, 8, WHEAT_ID);
        let input = parse(bytes);
        let light = dark_light();
        let mut output = [0; PLANT_QUADS_PER_CELL - 1];
        assert_eq!(
            mesh_section(&input, &light, &mut output),
            Err(MeshError::OutputOverflow)
        );
    }

    /// 整段塞满作物时，面实例总数恰好是 `4 × 4096`，仍在 Go 侧
    /// `maxNativeQuads = 6 * BlocksPerSection` 之内。
    ///
    /// 这条是"固定上界"的最坏情况证据：上界一旦从 4 涨上去，这里立刻红。
    #[test]
    fn a_section_full_of_plants_stays_within_the_fixed_bound() {
        let mut bytes = plant_input();
        for y in 0..16 {
            for z in 0..16 {
                for x in 0..16 {
                    set_block(&mut bytes, x, y, z, WHEAT_ID);
                }
            }
        }
        let input = parse(bytes);
        let light = dark_light();
        let mut output = vec![0; 6 * 4096];
        let count = mesh_section(&input, &light, &mut output).unwrap();
        assert_eq!(count, PLANT_QUADS_PER_CELL * 4096);
        assert!(
            count <= 6 * 4096,
            "植物的每格上界超过了普通方块的 6，Go 侧 maxNativeQuads 不再覆盖最坏情况"
        );
    }

    fn base_input() -> Vec<u8> {
        let mut bytes = valid_input();
        bytes[4..8].copy_from_slice(&0_i32.to_le_bytes());
        bytes[BLOCKS_OFFSET..BLOCKS_OFFSET + BLOCKS_BYTES].fill(0);
        bytes[HEIGHTS_PRESENT_OFFSET..REGISTRY_OFFSET].fill(0);
        bytes[REGISTRY_OFFSET + 2 * ENTRY_BYTES + 3] = 0;
        bytes[REGISTRY_OFFSET + 2 * ENTRY_BYTES + 16] = 0;
        set_visibility(&mut bytes, 5, 1);
        bytes
    }

    fn fill_slab(bytes: &mut [u8], id: u16) {
        for x in -16..32 {
            for y in -16..=0 {
                for z in -16..32 {
                    set_block(bytes, x, y, z, id);
                }
            }
        }
    }

    fn set_visibility(bytes: &mut [u8], stone: u64, glass: u64) {
        let visibility = REGISTRY_OFFSET + 3 * ENTRY_BYTES;
        bytes[visibility..visibility + 8].copy_from_slice(&0_u64.to_le_bytes());
        bytes[visibility + 8..visibility + 16].copy_from_slice(&stone.to_le_bytes());
        bytes[visibility + 16..visibility + 24].copy_from_slice(&glass.to_le_bytes());
    }

    fn set_block(bytes: &mut [u8], x: i32, y: i32, z: i32, id: u16) {
        let (cx, lx) = neighbor_cell(x);
        let (cy, ly) = neighbor_cell(y);
        let (cz, lz) = neighbor_cell(z);
        let section = (cx * 3 + cy) * 3 + cz;
        let cell = (ly << 8) | (lz << 4) | lx;
        let offset = BLOCKS_OFFSET + (section * 4096 + cell) * 2;
        bytes[offset..offset + 2].copy_from_slice(&id.to_le_bytes());
    }

    fn neighbor_cell(value: i32) -> (usize, usize) {
        let shifted = value + 16;
        ((shifted >> 4) as usize, (shifted & 15) as usize)
    }

    fn parse(bytes: Vec<u8>) -> MeshInput<'static> {
        MeshInput::parse(Box::leak(bytes.into_boxed_slice())).unwrap()
    }

    fn dark_light() -> LightScratch<'static> {
        LightScratch::new(
            Box::leak(vec![0; LIGHT_VOLUME].into_boxed_slice()),
            Box::leak(vec![0; LIGHT_VOLUME].into_boxed_slice()),
        )
    }
}
