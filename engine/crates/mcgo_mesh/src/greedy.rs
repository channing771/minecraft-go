use crate::input::MeshInput;
use crate::light::LightScratch;
use crate::quad::{Face, Quad};

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
}

const FACES: [Face; 6] = [
    Face::NegX,
    Face::PosX,
    Face::NegY,
    Face::PosY,
    Face::NegZ,
    Face::PosZ,
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
                    mask[(vi * 16 + ui) as usize] = MaskCell {
                        used: true,
                        material,
                        ao: compute_ao(input, p, axis, u, v, step),
                        light: light.at(q[0], q[1], q[2]),
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

                    let mut width = 1;
                    while ui + width < 16 && mask[vi * 16 + ui + width] == cell {
                        width += 1;
                    }
                    let mut height = 1;
                    'grow: while vi + height < 16 {
                        for offset in 0..width {
                            if mask[(vi + height) * 16 + ui + offset] != cell {
                                break 'grow;
                            }
                        }
                        height += 1;
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
                    }
                    .pack();
                    count += 1;
                    ui += width;
                }
            }
        }
    }
    Ok(count)
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
    use super::{MeshError, compute_ao, mesh_section};
    use crate::input::{MeshInput, tests::valid_input};
    use crate::light::{LIGHT_VOLUME, LightScratch};

    const BLOCKS_OFFSET: usize = 16;
    const BLOCKS_BYTES: usize = 27 * 4096 * 2;
    const HEIGHTS_PRESENT_OFFSET: usize = BLOCKS_OFFSET + BLOCKS_BYTES;
    const HEIGHTS_BYTES: usize = 9 + 9 * 256 * 2;
    const REGISTRY_OFFSET: usize = HEIGHTS_PRESENT_OFFSET + HEIGHTS_BYTES;
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

    fn base_input() -> Vec<u8> {
        let mut bytes = valid_input();
        bytes[4..8].copy_from_slice(&0_i32.to_le_bytes());
        bytes[BLOCKS_OFFSET..BLOCKS_OFFSET + BLOCKS_BYTES].fill(0);
        bytes[HEIGHTS_PRESENT_OFFSET..REGISTRY_OFFSET].fill(0);
        bytes[REGISTRY_OFFSET + 2 * 16 + 3] = 0;
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
        let visibility = REGISTRY_OFFSET + 3 * 16;
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
