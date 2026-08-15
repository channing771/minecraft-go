//! physics step ABI 的输入解析、校验、积分与输出编码。
//!
//! 输入布局与偏移以 `docs/superpowers/specs/2026-08-15-rust-engine-physics-step-design.md`
//! 第 4 节为准：128 字节 header（magic MGP1 + layout v1）+ 每 cell 196 字节。

pub(crate) const STEP_HEADER_BYTES: usize = 128;
pub(crate) const STEP_OUTPUT_BYTES: usize = 32;
pub(crate) const STEP_MAX_CELLS: usize = 4096;

const CELL_BYTES: usize = 196;

fn read_u32(bytes: &[u8], offset: usize) -> u32 {
    u32::from_le_bytes(
        bytes[offset..offset + 4]
            .try_into()
            .expect("validated range"),
    )
}

fn read_i32(bytes: &[u8], offset: usize) -> i32 {
    i32::from_le_bytes(
        bytes[offset..offset + 4]
            .try_into()
            .expect("validated range"),
    )
}

fn read_f32(bytes: &[u8], offset: usize) -> f32 {
    f32::from_bits(read_u32(bytes, offset))
}

pub(crate) struct StepInput<'a> {
    pub(crate) bytes: &'a [u8],
    pub(crate) position: [f32; 3],
    pub(crate) velocity: [f32; 3],
    pub(crate) on_ground: bool,
    pub(crate) jump: bool,
    pub(crate) move_x: i8,
    pub(crate) move_z: i8,
    pub(crate) yaw_sin: f32,
    pub(crate) yaw_cos: f32,
    pub(crate) fixed_delta_seconds: f32,
    pub(crate) step_height: f32,
    pub(crate) walk_speed: f32,
    pub(crate) ground_acceleration: f32,
    pub(crate) ground_deceleration: f32,
    pub(crate) air_acceleration: f32,
    pub(crate) jump_speed: f32,
    pub(crate) gravity: f32,
    pub(crate) terminal_fall_speed: f32,
    pub(crate) sweep_min: [f32; 3],
    pub(crate) sweep_max: [f32; 3],
    pub(crate) origin: [i32; 3],
    pub(crate) dimensions: [u32; 3],
}

impl<'a> StepInput<'a> {
    pub(crate) fn decode(bytes: &'a [u8]) -> Self {
        let mut tunables = [0.0f32; 8];
        for (index, slot) in tunables.iter_mut().enumerate() {
            *slot = read_f32(bytes, 48 + index * 4);
        }
        let mut sweep_min = [0.0f32; 3];
        let mut sweep_max = [0.0f32; 3];
        for axis in 0..3 {
            sweep_min[axis] = read_f32(bytes, 80 + axis * 8);
            sweep_max[axis] = read_f32(bytes, 84 + axis * 8);
        }
        let mut origin = [0i32; 3];
        let mut dimensions = [0u32; 3];
        for axis in 0..3 {
            origin[axis] = read_i32(bytes, 104 + axis * 4);
            dimensions[axis] = read_u32(bytes, 116 + axis * 4);
        }
        Self {
            bytes: &bytes[STEP_HEADER_BYTES..],
            position: [read_f32(bytes, 8), read_f32(bytes, 12), read_f32(bytes, 16)],
            velocity: [
                read_f32(bytes, 20),
                read_f32(bytes, 24),
                read_f32(bytes, 28),
            ],
            on_ground: bytes[32] == 1,
            jump: bytes[33] == 1,
            move_x: bytes[34] as i8,
            move_z: bytes[35] as i8,
            yaw_sin: read_f32(bytes, 36),
            yaw_cos: read_f32(bytes, 40),
            fixed_delta_seconds: read_f32(bytes, 44),
            step_height: tunables[0],
            walk_speed: tunables[1],
            ground_acceleration: tunables[2],
            ground_deceleration: tunables[3],
            air_acceleration: tunables[4],
            jump_speed: tunables[5],
            gravity: tunables[6],
            terminal_fall_speed: tunables[7],
            sweep_min,
            sweep_max,
            origin,
            dimensions,
        }
    }
}

pub(crate) fn step_input_is_valid(bytes: &[u8]) -> bool {
    if bytes.len() < STEP_HEADER_BYTES
        || &bytes[0..4] != b"MGP1"
        || read_u32(bytes, 4) != 1
        || bytes[32] > 1
        || bytes[33] > 1
        || !(-1..=1).contains(&(bytes[34] as i8))
        || !(-1..=1).contains(&(bytes[35] as i8))
    {
        return false;
    }
    // position/velocity（8..32）、yaw_sin/yaw_cos/dt（36/40/44）、tunables 与 sweep bounds（48..104）必须全部有限
    for offset in (8..32)
        .step_by(4)
        .chain((36..=44).step_by(4))
        .chain((48..104).step_by(4))
    {
        if !read_f32(bytes, offset).is_finite() {
            return false;
        }
    }
    for axis in 0..3 {
        if read_f32(bytes, 80 + axis * 8) > read_f32(bytes, 84 + axis * 8) {
            return false;
        }
    }
    let mut cell_count: usize = 1;
    for axis in 0..3 {
        let dimension = read_u32(bytes, 116 + axis * 4);
        let origin = read_i32(bytes, 104 + axis * 4);
        if dimension == 0 || origin.checked_add((dimension - 1) as i32).is_none() {
            return false;
        }
        let Some(next) = cell_count.checked_mul(dimension as usize) else {
            return false;
        };
        cell_count = next;
    }
    if cell_count > STEP_MAX_CELLS {
        return false;
    }
    let Some(expected_length) = STEP_HEADER_BYTES.checked_add(cell_count * CELL_BYTES) else {
        return false;
    };
    if expected_length != bytes.len() {
        return false;
    }
    for cell in bytes[STEP_HEADER_BYTES..].chunks_exact(CELL_BYTES) {
        if cell[0] > 1 || cell[1] > 8 || cell[2] != 0 || cell[3] != 0 {
            return false;
        }
        for box_index in 0..cell[1] as usize {
            let box_offset = 4 + box_index * 24;
            for component in 0..6 {
                if !read_f32(cell, box_offset + component * 4).is_finite() {
                    return false;
                }
            }
        }
    }
    true
}

#[cfg(test)]
mod tests {
    use super::{STEP_HEADER_BYTES, StepInput, step_input_is_valid};

    const CELL_BYTES: usize = 196;

    fn write_f32(bytes: &mut [u8], offset: usize, value: f32) {
        bytes[offset..offset + 4].copy_from_slice(&value.to_bits().to_le_bytes());
    }

    fn valid_step_bytes() -> Vec<u8> {
        let mut bytes = vec![0u8; STEP_HEADER_BYTES + CELL_BYTES];
        bytes[0..4].copy_from_slice(b"MGP1");
        bytes[4..8].copy_from_slice(&1u32.to_le_bytes());
        write_f32(&mut bytes, 8, 0.5); // position x
        write_f32(&mut bytes, 12, 1.0); // position y
        write_f32(&mut bytes, 16, 0.5); // position z
        write_f32(&mut bytes, 20, 0.0); // velocity x
        write_f32(&mut bytes, 24, -1.6); // velocity y
        write_f32(&mut bytes, 28, 0.0); // velocity z
        bytes[32] = 1; // on_ground
        bytes[34] = 1; // move_x
        write_f32(&mut bytes, 36, 0.0); // yaw_sin
        write_f32(&mut bytes, 40, 1.0); // yaw_cos
        write_f32(&mut bytes, 44, 0.05); // fixed_delta_seconds
        for (index, value) in [0.6f32, 4.3, 40.0, 50.0, 8.0, 8.4, 32.0, 78.4]
            .iter()
            .enumerate()
        {
            write_f32(&mut bytes, 48 + index * 4, *value);
        }
        write_f32(&mut bytes, 80, 0.0); // dx_min
        write_f32(&mut bytes, 84, 4.3 * 0.05); // dx_max
        write_f32(&mut bytes, 88, -1.6 * 0.05); // dy_min
        write_f32(&mut bytes, 92, 0.05); // dy_max
        write_f32(&mut bytes, 96, 0.0); // dz_min
        write_f32(&mut bytes, 100, 0.0); // dz_max
        for index in 0..3 {
            bytes[104 + index * 4..108 + index * 4].copy_from_slice(&0u32.to_le_bytes()); // origin
            bytes[116 + index * 4..120 + index * 4].copy_from_slice(&1u32.to_le_bytes()); // dimensions
        }
        bytes[STEP_HEADER_BYTES] = 1; // cell loaded
        bytes
    }

    #[test]
    fn accepts_valid_input() {
        assert!(step_input_is_valid(&valid_step_bytes()));
    }

    #[test]
    fn rejects_bad_magic() {
        let mut bytes = valid_step_bytes();
        bytes[0] = b'X';
        assert!(!step_input_is_valid(&bytes));
    }

    #[test]
    fn rejects_bad_layout() {
        let mut bytes = valid_step_bytes();
        bytes[4] = 0;
        assert!(!step_input_is_valid(&bytes));
    }

    #[test]
    fn rejects_move_out_of_range() {
        let mut bytes = valid_step_bytes();
        bytes[34] = 2; // move_x = 2
        assert!(!step_input_is_valid(&bytes));
    }

    #[test]
    fn rejects_non_finite_tunable() {
        let mut bytes = valid_step_bytes();
        write_f32(&mut bytes, 48, f32::NAN);
        assert!(!step_input_is_valid(&bytes));
    }

    #[test]
    fn rejects_non_finite_sweep_bounds() {
        let mut bytes = valid_step_bytes();
        write_f32(&mut bytes, 84, f32::INFINITY);
        assert!(!step_input_is_valid(&bytes));
    }

    #[test]
    fn rejects_inverted_sweep_bounds() {
        let mut bytes = valid_step_bytes();
        write_f32(&mut bytes, 84, -1.0); // dx_max < dx_min
        assert!(!step_input_is_valid(&bytes));
    }

    #[test]
    fn rejects_wrong_length() {
        let mut bytes = valid_step_bytes();
        bytes.pop();
        assert!(!step_input_is_valid(&bytes));
    }

    #[test]
    fn rejects_too_many_cells() {
        let mut bytes = valid_step_bytes();
        // dimensions 33×8×16 = 4224 > 4096
        for (index, dimension) in [33u32, 8, 16].iter().enumerate() {
            bytes[116 + index * 4..120 + index * 4].copy_from_slice(&dimension.to_le_bytes());
        }
        assert!(!step_input_is_valid(&bytes));
    }

    #[test]
    fn rejects_invalid_cell() {
        let mut bytes = valid_step_bytes();
        bytes[STEP_HEADER_BYTES + 1] = 9; // cell count > 8
        assert!(!step_input_is_valid(&bytes));
    }

    #[test]
    fn rejects_on_ground_out_of_range() {
        let mut bytes = valid_step_bytes();
        bytes[32] = 2; // on_ground = 2
        assert!(!step_input_is_valid(&bytes));
    }

    #[test]
    fn rejects_jump_out_of_range() {
        let mut bytes = valid_step_bytes();
        bytes[33] = 2; // jump = 2
        assert!(!step_input_is_valid(&bytes));
    }

    #[test]
    fn rejects_move_z_out_of_range() {
        let mut bytes = valid_step_bytes();
        bytes[35] = 2; // move_z = 2
        assert!(!step_input_is_valid(&bytes));
    }

    #[test]
    fn rejects_non_finite_position() {
        let mut bytes = valid_step_bytes();
        write_f32(&mut bytes, 8, f32::NAN); // position x
        assert!(!step_input_is_valid(&bytes));
    }

    #[test]
    fn rejects_non_finite_velocity() {
        let mut bytes = valid_step_bytes();
        write_f32(&mut bytes, 20, f32::INFINITY); // velocity x
        assert!(!step_input_is_valid(&bytes));
    }

    #[test]
    fn rejects_non_finite_yaw() {
        let mut bytes = valid_step_bytes();
        write_f32(&mut bytes, 36, f32::NAN); // yaw_sin
        assert!(!step_input_is_valid(&bytes));
    }

    #[test]
    fn rejects_non_finite_delta() {
        let mut bytes = valid_step_bytes();
        write_f32(&mut bytes, 44, f32::INFINITY); // fixed_delta_seconds
        assert!(!step_input_is_valid(&bytes));
    }

    #[test]
    fn rejects_zero_dimension() {
        let mut bytes = valid_step_bytes();
        bytes[116..120].copy_from_slice(&0u32.to_le_bytes()); // dimensions[0] = 0
        assert!(!step_input_is_valid(&bytes));
    }

    #[test]
    fn rejects_origin_overflow() {
        let mut bytes = valid_step_bytes();
        bytes[104..108].copy_from_slice(&i32::MAX.to_le_bytes()); // origin[0] = i32::MAX
        bytes[116..120].copy_from_slice(&2u32.to_le_bytes()); // dimensions[0] = 2
        assert!(!step_input_is_valid(&bytes));
    }

    #[test]
    fn rejects_unloaded_cell() {
        let mut bytes = valid_step_bytes();
        bytes[STEP_HEADER_BYTES] = 2; // cell loaded = 2
        assert!(!step_input_is_valid(&bytes));
    }

    #[test]
    fn rejects_nonzero_cell_reserved() {
        let mut bytes = valid_step_bytes();
        bytes[STEP_HEADER_BYTES + 2] = 1; // cell reserved byte != 0
        assert!(!step_input_is_valid(&bytes));
    }

    #[test]
    fn rejects_non_finite_box() {
        let mut bytes = valid_step_bytes();
        bytes[STEP_HEADER_BYTES + 1] = 1; // cell box count = 1
        write_f32(&mut bytes, STEP_HEADER_BYTES + 4, f32::NAN); // box[0] component 0
        assert!(!step_input_is_valid(&bytes));
    }

    #[test]
    fn decodes_fields() {
        let bytes = valid_step_bytes();
        let input = StepInput::decode(&bytes);
        assert_eq!(input.position, [0.5, 1.0, 0.5]);
        assert_eq!(input.velocity, [0.0, -1.6, 0.0]);
        assert!(input.on_ground);
        assert!(!input.jump);
        assert_eq!(input.move_x, 1);
        assert_eq!(input.move_z, 0);
        assert_eq!(input.yaw_sin, 0.0);
        assert_eq!(input.yaw_cos, 1.0);
        assert_eq!(input.fixed_delta_seconds, 0.05);
        assert_eq!(input.step_height, 0.6);
        assert_eq!(input.dimensions, [1, 1, 1]);
    }
}
