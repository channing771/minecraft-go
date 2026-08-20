use std::mem::{align_of, size_of};

use crate::collision::{COLLISION_STEP_HEIGHT_OFFSET, resolve_collision};
use crate::greedy::{MeshError as GreedyError, center_is_air, mesh_section};
use crate::input::{InputError, MeshInput};
use crate::light::{LIGHT_VOLUME, LightScratch, MeshError as LightError, build_light};
use crate::raycast::{
    RAYCAST_CURSOR_BYTES, RAYCAST_INPUT_BYTES, RAYCAST_OUTPUT_BYTES, RaycastBatch, raycast_batch,
    raycast_cursor_overflow_is_valid,
};
use crate::step::{STEP_HEADER_BYTES, STEP_OUTPUT_BYTES, physics_step};
use crate::worldgen::{
    CHUNK_VOLUME, WORLDGEN_CHUNK_OUTPUT_BYTES, WORLDGEN_PROBE_OUTPUT_RECORD_BYTES,
    parse_chunk_input, parse_probe_input, run_probe,
};

pub(crate) const ABI_VERSION: u32 = 5;

// 输入长度校验委托给 step::step_input_is_valid（内部使用 STEP_HEADER_BYTES），此常量保留供 ABI 文档对齐。
#[allow(dead_code)]
const PHYSICS_STEP_HEADER_BYTES: usize = STEP_HEADER_BYTES;
const PHYSICS_STEP_OUTPUT_BYTES: usize = STEP_OUTPUT_BYTES;

fn physics_step_input_is_valid(bytes: &[u8]) -> bool {
    crate::step::step_input_is_valid(bytes)
}

pub(crate) const MORNLEA_STATUS_OK: u32 = 0;
pub(crate) const MORNLEA_STATUS_ABI_VERSION: u32 = 1;
pub(crate) const MORNLEA_STATUS_INVALID_ARGUMENT: u32 = 2;
pub(crate) const MORNLEA_STATUS_INPUT: u32 = 3;
pub(crate) const MORNLEA_STATUS_SCRATCH: u32 = 4;
pub(crate) const MORNLEA_STATUS_REGISTRY: u32 = 5;
pub(crate) const MORNLEA_STATUS_EMISSION: u32 = 6;
pub(crate) const MORNLEA_STATUS_OUTPUT_OVERFLOW: u32 = 7;
pub(crate) const MORNLEA_STATUS_QUEUE_OVERFLOW: u32 = 8;
pub(crate) const MORNLEA_STATUS_PANIC: u32 = 9;

const SCRATCH_PADDING: usize =
    (align_of::<u32>() - LIGHT_VOLUME % align_of::<u32>()) % align_of::<u32>();
const SCRATCH_BYTES: usize = LIGHT_VOLUME + SCRATCH_PADDING + LIGHT_VOLUME * 4;
const OUTPUT_CAPACITY: usize = 6 * 4096;
const COLLISION_HEADER_BYTES: usize = 64;
const COLLISION_CELL_BYTES: usize = 196;
const COLLISION_OUTPUT_BYTES: usize = 16;
const COLLISION_MAX_CELLS: usize = 4096;

fn input_range_is_valid(input: *const u8, input_len: usize) -> bool {
    input_len <= isize::MAX as usize && input.addr().checked_add(input_len).is_some()
}

fn scratch_range_is_valid(scratch: *mut u8, scratch_len: usize) -> bool {
    scratch_len >= SCRATCH_BYTES
        && SCRATCH_BYTES <= isize::MAX as usize
        && scratch.addr().checked_add(SCRATCH_BYTES).is_some()
        && (scratch as usize).is_multiple_of(align_of::<u64>())
}

fn output_range_is_valid(output: *mut u64, output_capacity: usize) -> bool {
    output_capacity
        .checked_mul(size_of::<u64>())
        .is_some_and(|bytes| {
            bytes <= isize::MAX as usize && output.addr().checked_add(bytes).is_some()
        })
}

fn ranges_overlap(left: usize, left_len: usize, right: usize, right_len: usize) -> bool {
    left < right + right_len && right < left + left_len
}

fn byte_range_is_valid(pointer: *const u8, length: usize) -> bool {
    length <= isize::MAX as usize && pointer.addr().checked_add(length).is_some()
}

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

fn collision_input_is_valid(bytes: &[u8]) -> bool {
    if bytes.len() < COLLISION_HEADER_BYTES
        || &bytes[0..4] != b"MGC1"
        || read_u32(bytes, 4) != 1
        || !bytes[33..36].iter().all(|&value| value == 0)
        || bytes[32] > 1
    {
        return false;
    }
    for offset in [8, 12, 16, 20, 24, 28, COLLISION_STEP_HEIGHT_OFFSET] {
        if !read_f32(bytes, offset).is_finite() {
            return false;
        }
    }

    let dimensions = [
        read_u32(bytes, 52),
        read_u32(bytes, 56),
        read_u32(bytes, 60),
    ];
    if dimensions.contains(&0) {
        return false;
    }
    let Some(cell_count) = (dimensions[0] as usize)
        .checked_mul(dimensions[1] as usize)
        .and_then(|value| value.checked_mul(dimensions[2] as usize))
    else {
        return false;
    };
    if cell_count > COLLISION_MAX_CELLS {
        return false;
    }
    let Some(expected_length) = cell_count
        .checked_mul(COLLISION_CELL_BYTES)
        .and_then(|cell_bytes| COLLISION_HEADER_BYTES.checked_add(cell_bytes))
    else {
        return false;
    };
    if expected_length != bytes.len() {
        return false;
    }
    for (axis, dimension) in dimensions.into_iter().enumerate() {
        let origin = read_i32(bytes, 40 + axis * 4);
        if origin.checked_add((dimension - 1) as i32).is_none() {
            return false;
        }
    }
    if !collision_prism_covers_input(bytes, dimensions) {
        return false;
    }
    for cell in bytes[COLLISION_HEADER_BYTES..].chunks_exact(COLLISION_CELL_BYTES) {
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

fn collision_prism_covers_input(bytes: &[u8], dimensions: [u32; 3]) -> bool {
    const HALF_WIDTH: f32 = 0.3;
    const PLAYER_HEIGHT: f32 = 1.8;
    const EPSILON: f32 = 1e-5;
    const GROUND_PROBE: f32 = 1e-4;
    let position = [read_f32(bytes, 8), read_f32(bytes, 12), read_f32(bytes, 16)];
    let displacement = [
        read_f32(bytes, 20),
        read_f32(bytes, 24),
        read_f32(bytes, 28),
    ];
    let step_height = read_f32(bytes, COLLISION_STEP_HEIGHT_OFFSET);
    let minimum = [
        position[0].min(position[0] + displacement[0]) - HALF_WIDTH - EPSILON,
        position[1] + 0_f32.min(displacement[1]).min(step_height) - GROUND_PROBE - EPSILON,
        position[2].min(position[2] + displacement[2]) - HALF_WIDTH - EPSILON,
    ];
    let maximum = [
        position[0].max(position[0] + displacement[0]) + HALF_WIDTH + EPSILON,
        position[1] + 0_f32.max(displacement[1]).max(step_height) + PLAYER_HEIGHT + EPSILON,
        position[2].max(position[2] + displacement[2]) + HALF_WIDTH + EPSILON,
    ];
    for axis in 0..3 {
        if !minimum[axis].is_finite() || !maximum[axis].is_finite() {
            return false;
        }
        let required_minimum = minimum[axis].floor() as i64;
        let required_maximum = maximum[axis].floor() as i64;
        let prism_minimum = read_i32(bytes, 40 + axis * 4) as i64;
        let prism_maximum = prism_minimum + dimensions[axis] as i64 - 1;
        if required_minimum < i32::MIN as i64
            || required_maximum > i32::MAX as i64
            || prism_minimum > required_minimum
            || prism_maximum < required_maximum
        {
            return false;
        }
    }
    true
}

fn catch_collision(
    operation: impl FnOnce() -> Result<[u8; COLLISION_OUTPUT_BYTES], u32>,
) -> Result<[u8; COLLISION_OUTPUT_BYTES], u32> {
    match std::panic::catch_unwind(std::panic::AssertUnwindSafe(operation)) {
        Ok(result) => result,
        Err(_) => Err(MORNLEA_STATUS_PANIC),
    }
}

fn catch_and_publish(
    output_len: &mut usize,
    operation: impl FnOnce() -> Result<usize, u32>,
) -> u32 {
    *output_len = 0;
    match std::panic::catch_unwind(std::panic::AssertUnwindSafe(operation)) {
        Ok(Ok(count)) => {
            *output_len = count;
            MORNLEA_STATUS_OK
        }
        Ok(Err(status)) => status,
        Err(_) => MORNLEA_STATUS_PANIC,
    }
}

unsafe fn light_scratch_from_raw<'a>(scratch: *mut u8) -> LightScratch<'a> {
    // SAFETY: 调用者已验证起始地址、精确布局长度与可写性；两个切片由 split_at_mut 保证不重叠。
    let bytes = unsafe { std::slice::from_raw_parts_mut(scratch, SCRATCH_BYTES) };
    let (levels, rest) = bytes.split_at_mut(LIGHT_VOLUME);
    let (_, queue_bytes) = rest.split_at_mut(SCRATCH_PADDING);
    let queue_ptr = queue_bytes.as_mut_ptr().cast::<u32>();
    debug_assert!((queue_ptr as usize).is_multiple_of(align_of::<u32>()));
    // SAFETY: queue 起点已按 u32 对齐，剩余区域恰好容纳 LIGHT_VOLUME 个 u32。
    let queue = unsafe { std::slice::from_raw_parts_mut(queue_ptr, LIGHT_VOLUME) };
    LightScratch::new(levels, queue)
}

#[unsafe(no_mangle)]
pub extern "C" fn mornlea_engine_abi_version() -> u32 {
    ABI_VERSION
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn mornlea_mesh_section(
    abi_version: u32,
    input: *const u8,
    input_len: usize,
    scratch: *mut u8,
    scratch_len: usize,
    output: *mut u64,
    output_capacity: usize,
    output_len: *mut usize,
) -> u32 {
    if output_len.is_null()
        || !(output_len as usize).is_multiple_of(align_of::<usize>())
        || output_len.addr().checked_add(size_of::<usize>()).is_none()
    {
        return MORNLEA_STATUS_INVALID_ARGUMENT;
    }
    // SAFETY: 指针已检查非空且满足 usize 对齐，调用期间独占写入一个值。
    unsafe { output_len.write(0) };
    if abi_version != ABI_VERSION {
        return MORNLEA_STATUS_ABI_VERSION;
    }
    if input.is_null() || scratch.is_null() || output.is_null() {
        return MORNLEA_STATUS_INVALID_ARGUMENT;
    }
    if !input_range_is_valid(input, input_len) {
        return MORNLEA_STATUS_INPUT;
    }
    if !scratch_range_is_valid(scratch, scratch_len) {
        return MORNLEA_STATUS_SCRATCH;
    }
    if output_capacity < OUTPUT_CAPACITY {
        return MORNLEA_STATUS_OUTPUT_OVERFLOW;
    }
    if !(output as usize).is_multiple_of(align_of::<u64>())
        || !output_range_is_valid(output, output_capacity)
    {
        return MORNLEA_STATUS_INVALID_ARGUMENT;
    }
    let output_bytes = output_capacity * size_of::<u64>();
    if ranges_overlap(scratch.addr(), SCRATCH_BYTES, input.addr(), input_len)
        || ranges_overlap(scratch.addr(), SCRATCH_BYTES, output.addr(), output_bytes)
        || ranges_overlap(
            scratch.addr(),
            SCRATCH_BYTES,
            output_len.addr(),
            size_of::<usize>(),
        )
    {
        return MORNLEA_STATUS_SCRATCH;
    }
    if ranges_overlap(input.addr(), input_len, output.addr(), output_bytes)
        || ranges_overlap(
            input.addr(),
            input_len,
            output_len.addr(),
            size_of::<usize>(),
        )
        || ranges_overlap(
            output.addr(),
            output_bytes,
            output_len.addr(),
            size_of::<usize>(),
        )
    {
        return MORNLEA_STATUS_INVALID_ARGUMENT;
    }

    // SAFETY: output_len 已验证非空、对齐、地址范围和与其他 buffer 不重叠。
    let published = unsafe { &mut *output_len };
    catch_and_publish(published, || {
        // SAFETY: input 非空，范围不超过 isize::MAX 且地址加法不回绕；调用方声明其可读。
        let bytes = unsafe { std::slice::from_raw_parts(input, input_len) };
        let input = MeshInput::parse_structural(bytes).map_err(|error| match error {
            InputError::Input => MORNLEA_STATUS_INPUT,
            InputError::Registry => MORNLEA_STATUS_REGISTRY,
            InputError::Emission => MORNLEA_STATUS_EMISSION,
        })?;
        if center_is_air(&input) {
            return Ok(0);
        }
        input.validate_registry(true).map_err(|error| match error {
            InputError::Input => MORNLEA_STATUS_INPUT,
            InputError::Registry => MORNLEA_STATUS_REGISTRY,
            InputError::Emission => MORNLEA_STATUS_EMISSION,
        })?;
        // SAFETY: scratch 在进入 catch_unwind 前已通过对齐、长度和地址范围检查。
        let mut scratch = unsafe { light_scratch_from_raw(scratch) };
        build_light(&input, &input.registry, &mut scratch).map_err(|error| match error {
            LightError::EmissionOutOfRange => MORNLEA_STATUS_EMISSION,
            LightError::QueueOverflow => MORNLEA_STATUS_QUEUE_OVERFLOW,
        })?;
        // SAFETY: output 非空、对齐、范围有效，且不与 input、scratch 或 output_len 重叠。
        let output = unsafe { std::slice::from_raw_parts_mut(output, output_capacity) };
        mesh_section(&input, &scratch, output).map_err(|error| match error {
            GreedyError::OutputOverflow => MORNLEA_STATUS_OUTPUT_OVERFLOW,
        })
    })
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn mornlea_collision_resolve(
    abi_version: u32,
    input: *const u8,
    input_len: usize,
    output: *mut u8,
    output_len: usize,
) -> u32 {
    // SAFETY: C 调用方提供原始缓冲区；helper 会在解引用前验证指针、范围、长度与重叠。
    unsafe {
        collision_resolve_with(
            abi_version,
            input,
            input_len,
            output,
            output_len,
            resolve_collision,
        )
    }
}

unsafe fn collision_resolve_with(
    abi_version: u32,
    input: *const u8,
    input_len: usize,
    output: *mut u8,
    output_len: usize,
    resolver: impl FnOnce(&[u8]) -> [u8; COLLISION_OUTPUT_BYTES],
) -> u32 {
    if abi_version != ABI_VERSION {
        return MORNLEA_STATUS_ABI_VERSION;
    }
    if input.is_null() || output.is_null() {
        return MORNLEA_STATUS_INVALID_ARGUMENT;
    }
    if output_len < COLLISION_OUTPUT_BYTES {
        return MORNLEA_STATUS_OUTPUT_OVERFLOW;
    }
    if output_len != COLLISION_OUTPUT_BYTES
        || !byte_range_is_valid(input, input_len)
        || !byte_range_is_valid(output, output_len)
    {
        return MORNLEA_STATUS_INVALID_ARGUMENT;
    }
    if ranges_overlap(input.addr(), input_len, output.addr(), output_len) {
        return MORNLEA_STATUS_INVALID_ARGUMENT;
    }

    let result = catch_collision(|| {
        // SAFETY: input 非空，范围不超过 isize::MAX，地址加法不回绕且不与 output 重叠。
        let bytes = unsafe { std::slice::from_raw_parts(input, input_len) };
        if !collision_input_is_valid(bytes) {
            return Err(MORNLEA_STATUS_INPUT);
        }
        Ok(resolver(bytes))
    });
    match result {
        Ok(result) => {
            // SAFETY: output 非空、范围有效且与 input 不重叠；只在完整成功后一次发布。
            unsafe { std::ptr::copy_nonoverlapping(result.as_ptr(), output, result.len()) };
            MORNLEA_STATUS_OK
        }
        Err(status) => status,
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn mornlea_physics_step(
    abi_version: u32,
    input: *const u8,
    input_len: usize,
    output: *mut u8,
    output_len: usize,
) -> u32 {
    // SAFETY: C 调用方提供原始缓冲区；helper 会在解引用前验证指针、范围、长度与重叠。
    unsafe { physics_step_with(abi_version, input, input_len, output, output_len) }
}

unsafe fn physics_step_with(
    abi_version: u32,
    input: *const u8,
    input_len: usize,
    output: *mut u8,
    output_len: usize,
) -> u32 {
    if abi_version != ABI_VERSION {
        return MORNLEA_STATUS_ABI_VERSION;
    }
    if input.is_null() || output.is_null() {
        return MORNLEA_STATUS_INVALID_ARGUMENT;
    }
    if output_len < PHYSICS_STEP_OUTPUT_BYTES {
        return MORNLEA_STATUS_OUTPUT_OVERFLOW;
    }
    if output_len != PHYSICS_STEP_OUTPUT_BYTES
        || !byte_range_is_valid(input, input_len)
        || !byte_range_is_valid(output, output_len)
    {
        return MORNLEA_STATUS_INVALID_ARGUMENT;
    }
    if ranges_overlap(input.addr(), input_len, output.addr(), output_len) {
        return MORNLEA_STATUS_INVALID_ARGUMENT;
    }

    let result = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
        // SAFETY: input 非空，范围不超过 isize::MAX，地址加法不回绕且不与 output 重叠。
        let bytes = unsafe { std::slice::from_raw_parts(input, input_len) };
        if !physics_step_input_is_valid(bytes) {
            return Err(MORNLEA_STATUS_INPUT);
        }
        physics_step(bytes).map_err(|_| MORNLEA_STATUS_INPUT)
    }));
    match result {
        Ok(Ok(result)) => {
            // SAFETY: output 非空、范围有效且与 input 不重叠；只在完整成功后一次发布。
            unsafe { std::ptr::copy_nonoverlapping(result.as_ptr(), output, result.len()) };
            MORNLEA_STATUS_OK
        }
        Ok(Err(status)) => status,
        Err(_) => MORNLEA_STATUS_PANIC,
    }
}

/// 生成整区块的 worldgen 生产入口。
///
/// 输入为 `MGW1` header + chunk 坐标(共 572 字节),输出为 dense
/// `[y−min_y][lz][lx]` 布局的 98304 个 u16 LE(196608 字节)。任何输入
/// 违约返回错误状态且不修改输出缓冲;结果只在完整成功后一次发布。
#[unsafe(no_mangle)]
pub unsafe extern "C" fn mornlea_worldgen_chunk(
    abi_version: u32,
    input: *const u8,
    input_len: usize,
    output: *mut u8,
    output_len: usize,
) -> u32 {
    // SAFETY: C 调用方提供原始缓冲区；helper 会在解引用前验证指针、范围、长度与重叠。
    unsafe { worldgen_chunk_with(abi_version, input, input_len, output, output_len) }
}

unsafe fn worldgen_chunk_with(
    abi_version: u32,
    input: *const u8,
    input_len: usize,
    output: *mut u8,
    output_len: usize,
) -> u32 {
    if abi_version != ABI_VERSION {
        return MORNLEA_STATUS_ABI_VERSION;
    }
    if input.is_null() || output.is_null() {
        return MORNLEA_STATUS_INVALID_ARGUMENT;
    }
    if output_len < WORLDGEN_CHUNK_OUTPUT_BYTES {
        return MORNLEA_STATUS_OUTPUT_OVERFLOW;
    }
    if output_len != WORLDGEN_CHUNK_OUTPUT_BYTES
        || !byte_range_is_valid(input, input_len)
        || !byte_range_is_valid(output, output_len)
    {
        return MORNLEA_STATUS_INVALID_ARGUMENT;
    }
    if ranges_overlap(input.addr(), input_len, output.addr(), output_len) {
        return MORNLEA_STATUS_INVALID_ARGUMENT;
    }

    let result = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
        // SAFETY: input 非空，范围不超过 isize::MAX，地址加法不回绕且不与 output 重叠。
        let bytes = unsafe { std::slice::from_raw_parts(input, input_len) };
        let (params, chunk_x, chunk_z) = parse_chunk_input(bytes).ok_or(MORNLEA_STATUS_INPUT)?;
        // 先在本地缓冲生成，成功后一次拷贝，保证失败路径不触碰调用方输出。
        let mut dense = vec![0u16; CHUNK_VOLUME];
        params.generate_chunk(chunk_x, chunk_z, &mut dense);
        let mut encoded = vec![0u8; WORLDGEN_CHUNK_OUTPUT_BYTES];
        for (chunk, value) in encoded.chunks_exact_mut(2).zip(dense.iter()) {
            chunk.copy_from_slice(&value.to_le_bytes());
        }
        Ok::<Vec<u8>, u32>(encoded)
    }));
    match result {
        Ok(Ok(encoded)) => {
            // SAFETY: output 非空、范围有效且与 input 不重叠；只在完整成功后一次发布。
            unsafe { std::ptr::copy_nonoverlapping(encoded.as_ptr(), output, encoded.len()) };
            MORNLEA_STATUS_OK
        }
        Ok(Err(status)) => status,
        Err(_) => MORNLEA_STATUS_PANIC,
    }
}

/// 单点查询的 worldgen 生产入口(batch,最多 64 条)。
///
/// 输入为 `MGW1` header + record_count + 每条 16 字节的查询记录;输出为
/// 每条 8 字节(height + block + reserved)。输出长度必须与记录数精确
/// 匹配;任何输入违约返回错误状态且不修改输出缓冲。
#[unsafe(no_mangle)]
pub unsafe extern "C" fn mornlea_worldgen_probe(
    abi_version: u32,
    input: *const u8,
    input_len: usize,
    output: *mut u8,
    output_len: usize,
) -> u32 {
    // SAFETY: C 调用方提供原始缓冲区；helper 会在解引用前验证指针、范围、长度与重叠。
    unsafe { worldgen_probe_with(abi_version, input, input_len, output, output_len) }
}

unsafe fn worldgen_probe_with(
    abi_version: u32,
    input: *const u8,
    input_len: usize,
    output: *mut u8,
    output_len: usize,
) -> u32 {
    if abi_version != ABI_VERSION {
        return MORNLEA_STATUS_ABI_VERSION;
    }
    if input.is_null() || output.is_null() {
        return MORNLEA_STATUS_INVALID_ARGUMENT;
    }
    if !byte_range_is_valid(input, input_len) || !byte_range_is_valid(output, output_len) {
        return MORNLEA_STATUS_INVALID_ARGUMENT;
    }
    if ranges_overlap(input.addr(), input_len, output.addr(), output_len) {
        return MORNLEA_STATUS_INVALID_ARGUMENT;
    }

    let result = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
        // SAFETY: input 非空，范围不超过 isize::MAX，地址加法不回绕且不与 output 重叠。
        let bytes = unsafe { std::slice::from_raw_parts(input, input_len) };
        let (params, records) = parse_probe_input(bytes).ok_or(MORNLEA_STATUS_INPUT)?;
        let needed = records.len() * WORLDGEN_PROBE_OUTPUT_RECORD_BYTES;
        if output_len < needed {
            return Err(MORNLEA_STATUS_OUTPUT_OVERFLOW);
        }
        if output_len != needed {
            return Err(MORNLEA_STATUS_INVALID_ARGUMENT);
        }
        let mut encoded = vec![0u8; needed];
        run_probe(&params, &records, &mut encoded);
        Ok::<Vec<u8>, u32>(encoded)
    }));
    match result {
        Ok(Ok(encoded)) => {
            // SAFETY: output 非空、范围有效且与 input 不重叠；只在完整成功后一次发布。
            unsafe { std::ptr::copy_nonoverlapping(encoded.as_ptr(), output, encoded.len()) };
            MORNLEA_STATUS_OK
        }
        Ok(Err(status)) => status,
        Err(_) => MORNLEA_STATUS_PANIC,
    }
}

fn raycast_input_is_valid(bytes: &[u8]) -> bool {
    if bytes.len() != RAYCAST_INPUT_BYTES
        || &bytes[0..4] != b"MGR1"
        || read_u32(bytes, 4) != 1
        || !bytes[36..40].iter().all(|&value| value == 0)
    {
        return false;
    }
    let origin_and_direction_are_finite = (8..32)
        .step_by(4)
        .all(|offset| read_f32(bytes, offset).is_finite());
    let direction_is_nonzero = (20..32)
        .step_by(4)
        .any(|offset| read_f32(bytes, offset) != 0.0);
    let maximum = read_f32(bytes, 32);
    origin_and_direction_are_finite && direction_is_nonzero && maximum.is_finite() && maximum > 0.0
}

fn raycast_cursor_is_valid(input: &[u8], bytes: &[u8]) -> bool {
    if bytes.len() != RAYCAST_CURSOR_BYTES
        || &bytes[0..4] != b"MRC1"
        || read_u32(bytes, 4) != 1
        || bytes[8] > 2
        || !bytes[9..12].iter().all(|&value| value == 0)
        || !bytes[60..64].iter().all(|&value| value == 0)
    {
        return false;
    }
    if bytes[8] == 0 {
        return bytes[12..].iter().all(|&value| value == 0);
    }
    for axis in 0..3 {
        let component = read_f32(input, 20 + axis * 4);
        let step = read_i32(bytes, 24 + axis * 4);
        let expected_step = if component > 0.0 {
            1
        } else if component < 0.0 {
            -1
        } else {
            0
        };
        let delta = read_f32(bytes, 36 + axis * 4);
        let expected_delta = if component == 0.0 {
            f32::INFINITY
        } else {
            1.0 / component.abs()
        };
        let maximum = read_f32(bytes, 48 + axis * 4);
        if step != expected_step
            || delta.to_bits() != expected_delta.to_bits()
            || (!maximum.is_finite()
                && maximum != f32::INFINITY
                && !raycast_cursor_overflow_is_valid(input, axis, maximum))
            || (component == 0.0 && (delta != f32::INFINITY || maximum != f32::INFINITY))
        {
            return false;
        }
    }
    true
}

fn raycast_metadata_is_valid(output_count: *mut usize, done: *mut u8) -> bool {
    !output_count.is_null()
        && (output_count as usize).is_multiple_of(align_of::<usize>())
        && output_count
            .addr()
            .checked_add(size_of::<usize>())
            .is_some()
        && !done.is_null()
        && done.addr().checked_add(size_of::<u8>()).is_some()
        && !ranges_overlap(
            output_count.addr(),
            size_of::<usize>(),
            done.addr(),
            size_of::<u8>(),
        )
}

fn raycast_metadata_overlaps_buffer(
    output_count: *mut usize,
    done: *mut u8,
    pointer: *const u8,
    length: usize,
) -> bool {
    !pointer.is_null()
        && byte_range_is_valid(pointer, length)
        && (ranges_overlap(
            output_count.addr(),
            size_of::<usize>(),
            pointer.addr(),
            length,
        ) || ranges_overlap(done.addr(), size_of::<u8>(), pointer.addr(), length))
}

fn catch_raycast(
    operation: impl FnOnce() -> Result<RaycastBatch, u32>,
) -> Result<RaycastBatch, u32> {
    match std::panic::catch_unwind(std::panic::AssertUnwindSafe(operation)) {
        Ok(result) => result,
        Err(_) => Err(MORNLEA_STATUS_PANIC),
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn mornlea_raycast_batch(
    abi_version: u32,
    input: *const u8,
    input_len: usize,
    cursor: *mut u8,
    cursor_len: usize,
    output: *mut u8,
    output_len: usize,
    output_count: *mut usize,
    done: *mut u8,
) -> u32 {
    // SAFETY: C 调用方提供原始缓冲区；helper 会在解引用前验证全部指针、范围、长度与重叠。
    unsafe {
        raycast_batch_with(
            abi_version,
            input,
            input_len,
            cursor,
            cursor_len,
            output,
            output_len,
            output_count,
            done,
            raycast_batch,
        )
    }
}

#[allow(clippy::too_many_arguments)]
unsafe fn raycast_batch_with(
    abi_version: u32,
    input: *const u8,
    input_len: usize,
    cursor: *mut u8,
    cursor_len: usize,
    output: *mut u8,
    output_len: usize,
    output_count: *mut usize,
    done: *mut u8,
    resolver: impl FnOnce(&[u8], &[u8]) -> RaycastBatch,
) -> u32 {
    if !raycast_metadata_is_valid(output_count, done) {
        return MORNLEA_STATUS_INVALID_ARGUMENT;
    }
    let input_fixed_range_is_valid =
        input.is_null() || byte_range_is_valid(input, RAYCAST_INPUT_BYTES);
    let cursor_fixed_range_is_valid =
        cursor.is_null() || byte_range_is_valid(cursor, RAYCAST_CURSOR_BYTES);
    let output_fixed_range_is_valid =
        output.is_null() || byte_range_is_valid(output, RAYCAST_OUTPUT_BYTES);
    if raycast_metadata_overlaps_buffer(output_count, done, input, RAYCAST_INPUT_BYTES)
        || raycast_metadata_overlaps_buffer(output_count, done, cursor, RAYCAST_CURSOR_BYTES)
        || raycast_metadata_overlaps_buffer(output_count, done, output, RAYCAST_OUTPUT_BYTES)
    {
        return MORNLEA_STATUS_INVALID_ARGUMENT;
    }
    // SAFETY: 两个 metadata 指针已验证非空、对齐、范围有效且彼此及与 caller buffer 不重叠。
    unsafe {
        output_count.write(0);
        done.write(0);
    }
    if !input_fixed_range_is_valid || !cursor_fixed_range_is_valid || !output_fixed_range_is_valid {
        return MORNLEA_STATUS_INVALID_ARGUMENT;
    }
    if abi_version != ABI_VERSION {
        return MORNLEA_STATUS_ABI_VERSION;
    }
    if input.is_null() || cursor.is_null() || output.is_null() {
        return MORNLEA_STATUS_INVALID_ARGUMENT;
    }
    if input_len != RAYCAST_INPUT_BYTES || cursor_len != RAYCAST_CURSOR_BYTES {
        return MORNLEA_STATUS_INPUT;
    }
    if output_len < RAYCAST_OUTPUT_BYTES {
        return MORNLEA_STATUS_OUTPUT_OVERFLOW;
    }
    if output_len != RAYCAST_OUTPUT_BYTES
        || !byte_range_is_valid(input, input_len)
        || !byte_range_is_valid(cursor, cursor_len)
        || !byte_range_is_valid(output, output_len)
    {
        return MORNLEA_STATUS_INVALID_ARGUMENT;
    }
    if ranges_overlap(input.addr(), input_len, cursor.addr(), cursor_len)
        || ranges_overlap(input.addr(), input_len, output.addr(), output_len)
        || ranges_overlap(cursor.addr(), cursor_len, output.addr(), output_len)
    {
        return MORNLEA_STATUS_INVALID_ARGUMENT;
    }

    let result = catch_raycast(|| {
        // SAFETY: 三个 buffer 均非空、范围有效且互不重叠；这里只建立调用期借用。
        let input_bytes = unsafe { std::slice::from_raw_parts(input, input_len) };
        // SAFETY: cursor 非空、范围有效且与其他 buffer 不重叠；resolver 只读取本地视图。
        let cursor_bytes = unsafe { std::slice::from_raw_parts(cursor, cursor_len) };
        if !raycast_input_is_valid(input_bytes)
            || !raycast_cursor_is_valid(input_bytes, cursor_bytes)
        {
            return Err(MORNLEA_STATUS_INPUT);
        }
        Ok(resolver(input_bytes, cursor_bytes))
    });
    match result {
        Ok(result) => {
            debug_assert!(result.count <= 64);
            // SAFETY: cursor/output 非空、范围有效且互不重叠；结果先完整位于 Rust local storage。
            unsafe {
                std::ptr::copy_nonoverlapping(result.cursor.as_ptr(), cursor, result.cursor.len());
                std::ptr::copy_nonoverlapping(result.output.as_ptr(), output, result.output.len());
                output_count.write(result.count);
                done.write(u8::from(result.done));
            }
            MORNLEA_STATUS_OK
        }
        Err(status) => status,
    }
}

#[cfg(test)]
mod mesh_tests {
    use super::*;

    #[test]
    fn exported_version_is_five() {
        // engine ABI v5:mesh registry 条目上限 27 → 35(流体进入 registry 快照)。
        assert_eq!(mornlea_engine_abi_version(), 5);
    }
}
#[cfg(test)]
mod tests {
    use std::mem::{align_of, size_of};

    use super::{
        ABI_VERSION, COLLISION_CELL_BYTES, COLLISION_HEADER_BYTES, COLLISION_MAX_CELLS,
        COLLISION_OUTPUT_BYTES, COLLISION_STEP_HEIGHT_OFFSET, MORNLEA_STATUS_ABI_VERSION,
        MORNLEA_STATUS_EMISSION, MORNLEA_STATUS_INPUT, MORNLEA_STATUS_INVALID_ARGUMENT,
        MORNLEA_STATUS_OK, MORNLEA_STATUS_OUTPUT_OVERFLOW, MORNLEA_STATUS_PANIC,
        MORNLEA_STATUS_QUEUE_OVERFLOW, MORNLEA_STATUS_REGISTRY, MORNLEA_STATUS_SCRATCH,
        SCRATCH_BYTES, catch_and_publish, catch_collision, collision_resolve_with,
        input_range_is_valid, mornlea_collision_resolve, mornlea_mesh_section,
        output_range_is_valid, raycast_batch_with, read_f32, read_i32, read_u32,
        scratch_range_is_valid,
    };
    use crate::input::tests::valid_input;
    use crate::raycast::{
        RAYCAST_CURSOR_BYTES, RAYCAST_INPUT_BYTES, RAYCAST_OUTPUT_BYTES, RaycastBatch,
        raycast_batch,
    };

    #[test]
    fn caught_panic_keeps_output_count_zero() {
        let mut output_len = usize::MAX;
        let status = catch_and_publish(&mut output_len, || -> Result<usize, u32> {
            panic!("测试 panic")
        });

        assert_eq!(status, MORNLEA_STATUS_PANIC);
        assert_eq!(output_len, 0);
    }

    #[test]
    fn collision_layout_v1_is_stable() {
        assert_eq!(COLLISION_HEADER_BYTES, 64);
        assert_eq!(COLLISION_CELL_BYTES, 196);
        assert_eq!(COLLISION_OUTPUT_BYTES, 16);
        assert_eq!(
            COLLISION_HEADER_BYTES + COLLISION_MAX_CELLS * COLLISION_CELL_BYTES,
            802_880
        );

        let header = [
            0x4d, 0x47, 0x43, 0x31, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0xa0, 0x3f, 0x00, 0x00,
            0x20, 0xc0, 0x00, 0x00, 0x70, 0x40, 0x00, 0x00, 0x90, 0xc0, 0x00, 0x00, 0xa8, 0x40,
            0x00, 0x00, 0xd8, 0xc0, 0x01, 0x00, 0x00, 0x00, 0x9a, 0x99, 0x19, 0x3f, 0xf9, 0xff,
            0xff, 0xff, 0x08, 0x00, 0x00, 0x00, 0xf7, 0xff, 0xff, 0xff, 0x01, 0x00, 0x00, 0x00,
            0x02, 0x00, 0x00, 0x00, 0x03, 0x00, 0x00, 0x00,
        ];
        assert_eq!(&header[0..4], b"MGC1");
        assert_eq!(read_u32(&header, 4), 1);
        assert_eq!(
            [
                read_f32(&header, 8).to_bits(),
                read_f32(&header, 12).to_bits(),
                read_f32(&header, 16).to_bits()
            ],
            [1.25_f32.to_bits(), (-2.5_f32).to_bits(), 3.75_f32.to_bits()]
        );
        assert_eq!(
            [
                read_f32(&header, 20).to_bits(),
                read_f32(&header, 24).to_bits(),
                read_f32(&header, 28).to_bits()
            ],
            [
                (-4.5_f32).to_bits(),
                5.25_f32.to_bits(),
                (-6.75_f32).to_bits()
            ]
        );
        assert_eq!(&header[32..36], &[1, 0, 0, 0]);
        assert_eq!(COLLISION_STEP_HEIGHT_OFFSET, 36);
        assert_eq!(
            read_f32(&header, COLLISION_STEP_HEIGHT_OFFSET).to_bits(),
            0.6_f32.to_bits()
        );
        assert_eq!(
            [
                read_i32(&header, 40),
                read_i32(&header, 44),
                read_i32(&header, 48)
            ],
            [-7, 8, -9]
        );
        assert_eq!(
            [
                read_u32(&header, 52),
                read_u32(&header, 56),
                read_u32(&header, 60)
            ],
            [1, 2, 3]
        );

        let cell_prefix = [
            0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x80, 0xbe, 0x00, 0x00, 0x00, 0x3e, 0x00, 0x00,
            0x00, 0x3f, 0x00, 0x00, 0xa0, 0x3f, 0x00, 0x00, 0x60, 0x3f, 0x00, 0x00, 0xc0, 0x3f,
        ];
        assert_eq!(&cell_prefix[0..4], &[1, 1, 0, 0]);
        for (index, want) in [-0.25_f32, 0.125, 0.5, 1.25, 0.875, 1.5]
            .into_iter()
            .enumerate()
        {
            assert_eq!(
                read_f32(&cell_prefix, 4 + index * 4).to_bits(),
                want.to_bits()
            );
        }

        let output = [
            0x00, 0x00, 0xa0, 0x3f, 0x00, 0x00, 0x20, 0xc0, 0x00, 0x00, 0x70, 0x40, 0x05, 0x01,
            0x00, 0x01,
        ];
        assert_eq!(read_f32(&output, 0).to_bits(), 1.25_f32.to_bits());
        assert_eq!(read_f32(&output, 4).to_bits(), (-2.5_f32).to_bits());
        assert_eq!(read_f32(&output, 8).to_bits(), 3.75_f32.to_bits());
        assert_eq!(&output[12..16], &[5, 1, 0, 1]);
    }

    #[test]
    fn collision_panic_is_contained_without_result() {
        let result = catch_collision(|| -> Result<[u8; COLLISION_OUTPUT_BYTES], u32> {
            panic!("测试 panic")
        });
        assert_eq!(result, Err(MORNLEA_STATUS_PANIC));
    }

    #[test]
    fn collision_panic_through_publish_path_keeps_caller_output_unchanged() {
        let mut input = [0_u8; 64 + 4 * 196];
        input[0..4].copy_from_slice(b"MGC1");
        input[4..8].copy_from_slice(&1_u32.to_le_bytes());
        for (offset, value) in [(8, 0.5_f32), (12, 1.0), (16, 0.5), (36, 0.6)] {
            input[offset..offset + 4].copy_from_slice(&value.to_bits().to_le_bytes());
        }
        input[52..56].copy_from_slice(&1_u32.to_le_bytes());
        input[56..60].copy_from_slice(&4_u32.to_le_bytes());
        input[60..64].copy_from_slice(&1_u32.to_le_bytes());
        let mut caller_output = [0xa5_u8; COLLISION_OUTPUT_BYTES + 2];

        let status = unsafe {
            collision_resolve_with(
                ABI_VERSION,
                input.as_ptr(),
                input.len(),
                caller_output[1..].as_mut_ptr(),
                COLLISION_OUTPUT_BYTES,
                |_| -> [u8; COLLISION_OUTPUT_BYTES] { panic!("测试 panic") },
            )
        };

        assert_eq!(status, MORNLEA_STATUS_PANIC);
        assert_eq!(caller_output, [0xa5; COLLISION_OUTPUT_BYTES + 2]);
    }

    #[test]
    fn raycast_panic_through_publish_path_is_atomic() {
        let input = valid_raycast_input();
        let mut cursor_arena = [0xa5_u8; RAYCAST_CURSOR_BYTES + 2];
        cursor_arena[1..1 + RAYCAST_CURSOR_BYTES].copy_from_slice(&fresh_raycast_cursor());
        let mut output_arena = [0xa5_u8; RAYCAST_OUTPUT_BYTES + 2];
        let before_cursor = cursor_arena;
        let before_output = output_arena;
        let mut count = usize::MAX;
        let mut done = 0xff;

        let status = unsafe {
            raycast_batch_with(
                ABI_VERSION,
                input.as_ptr(),
                input.len(),
                cursor_arena[1..].as_mut_ptr(),
                RAYCAST_CURSOR_BYTES,
                output_arena[1..].as_mut_ptr(),
                RAYCAST_OUTPUT_BYTES,
                &mut count,
                &mut done,
                |_, _| -> RaycastBatch { panic!("测试 panic") },
            )
        };

        assert_eq!(status, MORNLEA_STATUS_PANIC);
        assert_eq!((count, done), (0, 0));
        assert_eq!(cursor_arena, before_cursor);
        assert_eq!(output_arena, before_output);
    }

    #[test]
    fn raycast_success_publishes_local_cursor_and_output_once() {
        let input = valid_raycast_input();
        let mut cursor_arena = [0xa5_u8; RAYCAST_CURSOR_BYTES + 2];
        cursor_arena[1..1 + RAYCAST_CURSOR_BYTES].copy_from_slice(&fresh_raycast_cursor());
        let mut output_arena = [0xa5_u8; RAYCAST_OUTPUT_BYTES + 2];
        let mut count = usize::MAX;
        let mut done = 0xff;

        let status = unsafe {
            raycast_batch_with(
                ABI_VERSION,
                input.as_ptr(),
                input.len(),
                cursor_arena[1..].as_mut_ptr(),
                RAYCAST_CURSOR_BYTES,
                output_arena[1..].as_mut_ptr(),
                RAYCAST_OUTPUT_BYTES,
                &mut count,
                &mut done,
                |_, _| {
                    let mut cursor = fresh_raycast_cursor();
                    cursor[8] = 2;
                    let mut output = [0_u8; RAYCAST_OUTPUT_BYTES];
                    output[12] = 0xff;
                    RaycastBatch {
                        cursor,
                        output,
                        count: 1,
                        done: true,
                    }
                },
            )
        };

        assert_eq!(status, MORNLEA_STATUS_OK);
        assert_eq!((count, done), (1, 1));
        assert_eq!(cursor_arena[0], 0xa5);
        assert_eq!(cursor_arena[RAYCAST_CURSOR_BYTES + 1], 0xa5);
        assert_eq!(cursor_arena[9], 2);
        assert_eq!(output_arena[0], 0xa5);
        assert_eq!(output_arena[RAYCAST_OUTPUT_BYTES + 1], 0xa5);
        assert_eq!(output_arena[13], 0xff);
    }

    #[test]
    fn null_raycast_buffer_clears_metadata_before_invalid_argument() {
        for buffer in [
            RaycastBuffer::Input,
            RaycastBuffer::Cursor,
            RaycastBuffer::Output,
        ] {
            let input = valid_raycast_input();
            let mut cursor = fresh_raycast_cursor();
            let mut output = [0xa5_u8; RAYCAST_OUTPUT_BYTES];
            let mut count = usize::MAX;
            let mut done = 0xff;
            let input_pointer = if matches!(buffer, RaycastBuffer::Input) {
                std::ptr::null()
            } else {
                input.as_ptr()
            };
            let cursor_pointer = if matches!(buffer, RaycastBuffer::Cursor) {
                std::ptr::null_mut()
            } else {
                cursor.as_mut_ptr()
            };
            let output_pointer = if matches!(buffer, RaycastBuffer::Output) {
                std::ptr::null_mut()
            } else {
                output.as_mut_ptr()
            };

            let status = unsafe {
                super::mornlea_raycast_batch(
                    ABI_VERSION,
                    input_pointer,
                    input.len(),
                    cursor_pointer,
                    cursor.len(),
                    output_pointer,
                    output.len(),
                    &mut count,
                    &mut done,
                )
            };

            assert_eq!((count, done), (0, 0), "{buffer:?}");
            assert_eq!(status, MORNLEA_STATUS_INVALID_ARGUMENT, "{buffer:?}");
        }
    }

    #[test]
    fn wrapping_raycast_buffer_clears_metadata_without_publishing() {
        for buffer in [
            RaycastBuffer::Input,
            RaycastBuffer::Cursor,
            RaycastBuffer::Output,
        ] {
            let input = valid_raycast_input();
            let mut cursor = fresh_raycast_cursor();
            let mut output = [0xa5_u8; RAYCAST_OUTPUT_BYTES];
            let before_input = input;
            let before_cursor = cursor;
            let before_output = output;
            let mut count = usize::MAX;
            let mut done = 0xff;
            let input_pointer = if matches!(buffer, RaycastBuffer::Input) {
                std::ptr::without_provenance::<u8>(usize::MAX)
            } else {
                input.as_ptr()
            };
            let cursor_pointer = if matches!(buffer, RaycastBuffer::Cursor) {
                std::ptr::without_provenance_mut::<u8>(usize::MAX)
            } else {
                cursor.as_mut_ptr()
            };
            let output_pointer = if matches!(buffer, RaycastBuffer::Output) {
                std::ptr::without_provenance_mut::<u8>(usize::MAX)
            } else {
                output.as_mut_ptr()
            };

            let status = unsafe {
                super::mornlea_raycast_batch(
                    ABI_VERSION,
                    input_pointer,
                    RAYCAST_INPUT_BYTES,
                    cursor_pointer,
                    RAYCAST_CURSOR_BYTES,
                    output_pointer,
                    RAYCAST_OUTPUT_BYTES,
                    &mut count,
                    &mut done,
                )
            };

            assert_eq!(status, MORNLEA_STATUS_INVALID_ARGUMENT, "{buffer:?}");
            assert_eq!((count, done), (0, 0), "{buffer:?}");
            assert_eq!(input, before_input, "{buffer:?}");
            assert_eq!(cursor, before_cursor, "{buffer:?}");
            assert_eq!(output, before_output, "{buffer:?}");
        }
    }

    #[test]
    fn raycast_metadata_alias_wins_over_a_wrapping_other_buffer() {
        let input = valid_raycast_input();
        let mut cursor = AlignedBytes(fresh_raycast_cursor());
        let mut output = AlignedBytes([0xa5_u8; RAYCAST_OUTPUT_BYTES]);
        let before_input = input;
        let before_cursor = cursor.0;
        let before_output = output.0;
        let mut done = 0xff;

        let status = unsafe {
            super::mornlea_raycast_batch(
                ABI_VERSION,
                std::ptr::without_provenance::<u8>(usize::MAX),
                RAYCAST_INPUT_BYTES,
                cursor.0.as_mut_ptr(),
                RAYCAST_CURSOR_BYTES,
                output.0.as_mut_ptr(),
                RAYCAST_OUTPUT_BYTES,
                cursor.0.as_mut_ptr().cast::<usize>(),
                &mut done,
            )
        };

        assert_eq!(status, MORNLEA_STATUS_INVALID_ARGUMENT);
        assert_eq!(done, 0xff);
        assert_eq!(input, before_input);
        assert_eq!(cursor.0, before_cursor);
        assert_eq!(output.0, before_output);
    }

    #[test]
    fn invalid_raycast_metadata_is_not_partially_cleared() {
        let input = valid_raycast_input();
        let mut cursor = fresh_raycast_cursor();
        let mut output = [0xa5_u8; RAYCAST_OUTPUT_BYTES];
        let before_cursor = cursor;
        let before_output = output;
        let mut count = usize::MAX;

        let status = unsafe {
            super::mornlea_raycast_batch(
                ABI_VERSION,
                input.as_ptr(),
                input.len(),
                cursor.as_mut_ptr(),
                cursor.len(),
                output.as_mut_ptr(),
                output.len(),
                &mut count,
                std::ptr::null_mut(),
            )
        };

        assert_eq!(status, MORNLEA_STATUS_INVALID_ARGUMENT);
        assert_eq!(count, usize::MAX);
        assert_eq!(cursor, before_cursor);
        assert_eq!(output, before_output);
    }

    #[test]
    fn overlapping_raycast_metadata_is_not_partially_cleared() {
        let input = valid_raycast_input();
        let mut cursor = fresh_raycast_cursor();
        let mut output = [0xa5_u8; RAYCAST_OUTPUT_BYTES];
        let before_cursor = cursor;
        let before_output = output;
        let mut count = usize::MAX;

        let status = unsafe {
            super::mornlea_raycast_batch(
                ABI_VERSION,
                input.as_ptr(),
                input.len(),
                cursor.as_mut_ptr(),
                cursor.len(),
                output.as_mut_ptr(),
                output.len(),
                &mut count,
                (&mut count as *mut usize).cast(),
            )
        };

        assert_eq!(status, MORNLEA_STATUS_INVALID_ARGUMENT);
        assert_eq!(count, usize::MAX);
        assert_eq!(cursor, before_cursor);
        assert_eq!(output, before_output);
    }

    #[test]
    fn raycast_metadata_overlapping_cursor_is_not_published() {
        #[repr(align(8))]
        struct AlignedCursor([u8; RAYCAST_CURSOR_BYTES]);

        let input = valid_raycast_input();
        let mut cursor = AlignedCursor(fresh_raycast_cursor());
        let mut output = [0xa5_u8; RAYCAST_OUTPUT_BYTES];
        let before_cursor = cursor.0;
        let before_output = output;
        let mut done = 0xff;

        let status = unsafe {
            super::mornlea_raycast_batch(
                ABI_VERSION,
                input.as_ptr(),
                input.len(),
                cursor.0.as_mut_ptr(),
                cursor.0.len(),
                output.as_mut_ptr(),
                output.len(),
                cursor.0.as_mut_ptr().cast(),
                &mut done,
            )
        };

        assert_eq!(status, MORNLEA_STATUS_INVALID_ARGUMENT);
        assert_eq!(done, 0xff);
        assert_eq!(cursor.0, before_cursor);
        assert_eq!(output, before_output);
    }

    #[test]
    fn malformed_raycast_input_and_cursor_matrix_is_atomic() {
        let valid_input = valid_raycast_input();
        let fresh = fresh_raycast_cursor();
        let mut active_input = valid_input;
        active_input[32..36].copy_from_slice(&130.0_f32.to_bits().to_le_bytes());
        let active_result = raycast_batch(&active_input, &fresh);
        assert_eq!((active_result.count, active_result.done), (64, false));
        let done_result = raycast_batch(&valid_input, &fresh);
        assert!(done_result.done);

        let mut cases = Vec::new();
        push_raycast_mutation(&mut cases, "input magic", valid_input, fresh, |input, _| {
            input[0] = b'X';
        });
        push_raycast_mutation(
            &mut cases,
            "input layout",
            valid_input,
            fresh,
            |input, _| {
                input[4] = 2;
            },
        );
        push_raycast_mutation(
            &mut cases,
            "input reserved",
            valid_input,
            fresh,
            |input, _| {
                input[36] = 1;
            },
        );
        push_raycast_mutation(
            &mut cases,
            "input origin NaN",
            valid_input,
            fresh,
            |input, _| {
                write_test_f32(input, 8, f32::NAN);
            },
        );
        push_raycast_mutation(
            &mut cases,
            "input direction NaN",
            valid_input,
            fresh,
            |input, _| {
                write_test_f32(input, 20, f32::NAN);
            },
        );
        push_raycast_mutation(
            &mut cases,
            "input zero direction",
            valid_input,
            fresh,
            |input, _| {
                input[20..32].fill(0);
            },
        );
        push_raycast_mutation(
            &mut cases,
            "input maximum NaN",
            valid_input,
            fresh,
            |input, _| {
                write_test_f32(input, 32, f32::NAN);
            },
        );
        push_raycast_mutation(
            &mut cases,
            "input maximum zero",
            valid_input,
            fresh,
            |input, _| {
                write_test_f32(input, 32, 0.0);
            },
        );
        push_raycast_mutation(
            &mut cases,
            "cursor magic",
            valid_input,
            fresh,
            |_, cursor| {
                cursor[0] = b'X';
            },
        );
        push_raycast_mutation(
            &mut cases,
            "cursor layout",
            valid_input,
            fresh,
            |_, cursor| {
                cursor[4] = 2;
            },
        );
        push_raycast_mutation(
            &mut cases,
            "cursor state 3",
            valid_input,
            fresh,
            |_, cursor| {
                cursor[8] = 3;
            },
        );
        push_raycast_mutation(
            &mut cases,
            "cursor header reserved",
            valid_input,
            fresh,
            |_, cursor| {
                cursor[9] = 1;
            },
        );
        push_raycast_mutation(
            &mut cases,
            "cursor tail reserved",
            valid_input,
            fresh,
            |_, cursor| {
                cursor[60] = 1;
            },
        );
        push_raycast_mutation(
            &mut cases,
            "fresh nonzero payload",
            valid_input,
            fresh,
            |_, cursor| {
                cursor[12] = 1;
            },
        );

        for (state_name, input, base) in [
            ("active", active_input, active_result.cursor),
            ("done", valid_input, done_result.cursor),
        ] {
            push_raycast_mutation(
                &mut cases,
                &format!("{state_name} step"),
                input,
                base,
                |_, cursor| {
                    cursor[24..28].fill(0);
                },
            );
            push_raycast_mutation(
                &mut cases,
                &format!("{state_name} delta NaN"),
                input,
                base,
                |_, cursor| {
                    write_test_f32(cursor, 36, f32::NAN);
                },
            );
            push_raycast_mutation(
                &mut cases,
                &format!("{state_name} delta -Inf"),
                input,
                base,
                |_, cursor| {
                    write_test_f32(cursor, 36, f32::NEG_INFINITY);
                },
            );
            push_raycast_mutation(
                &mut cases,
                &format!("{state_name} delta zero"),
                input,
                base,
                |_, cursor| {
                    write_test_f32(cursor, 36, 0.0);
                },
            );
            push_raycast_mutation(
                &mut cases,
                &format!("{state_name} delta finite mismatch"),
                input,
                base,
                |_, cursor| {
                    write_test_f32(cursor, 36, 2.0);
                },
            );
            push_raycast_mutation(
                &mut cases,
                &format!("{state_name} maximum NaN"),
                input,
                base,
                |_, cursor| {
                    write_test_f32(cursor, 48, f32::NAN);
                },
            );
            push_raycast_mutation(
                &mut cases,
                &format!("{state_name} maximum -Inf"),
                input,
                base,
                |_, cursor| {
                    write_test_f32(cursor, 48, f32::NEG_INFINITY);
                },
            );
            push_raycast_mutation(
                &mut cases,
                &format!("{state_name} zero-axis delta finite"),
                input,
                base,
                |_, cursor| write_test_f32(cursor, 40, 1.0),
            );
            push_raycast_mutation(
                &mut cases,
                &format!("{state_name} zero-axis maximum finite"),
                input,
                base,
                |_, cursor| write_test_f32(cursor, 52, 1.0),
            );
        }

        for (name, input, cursor) in cases {
            assert_raycast_input_failure_is_atomic(&name, input, cursor);
        }
    }

    #[test]
    fn invalid_raycast_metadata_pointer_matrix_is_atomic() {
        for kind in [
            InvalidMetadataPointer::MisalignedCount,
            InvalidMetadataPointer::WrappingCount,
            InvalidMetadataPointer::WrappingDone,
        ] {
            assert_invalid_raycast_metadata_pointer_is_atomic(kind);
        }
    }

    #[test]
    fn raycast_metadata_buffer_overlap_matrix_is_atomic() {
        for metadata in [MetadataField::Count, MetadataField::Done] {
            for buffer in [
                RaycastBuffer::Input,
                RaycastBuffer::Cursor,
                RaycastBuffer::Output,
            ] {
                for length in [
                    RaycastBufferLength::Exact,
                    RaycastBufferLength::Zero,
                    RaycastBufferLength::Short,
                    RaycastBufferLength::Long,
                    RaycastBufferLength::Wrapping,
                ] {
                    assert_raycast_metadata_overlap_is_atomic(metadata, buffer, length);
                }
            }
        }
    }

    fn valid_raycast_input() -> [u8; RAYCAST_INPUT_BYTES] {
        let mut input = [0_u8; RAYCAST_INPUT_BYTES];
        input[0..4].copy_from_slice(b"MGR1");
        input[4..8].copy_from_slice(&1_u32.to_le_bytes());
        for (offset, value) in [(8, 0.5_f32), (12, -1.25), (16, 2.75), (20, 1.0), (32, 6.0)] {
            input[offset..offset + 4].copy_from_slice(&value.to_bits().to_le_bytes());
        }
        input
    }

    fn fresh_raycast_cursor() -> [u8; RAYCAST_CURSOR_BYTES] {
        let mut cursor = [0_u8; RAYCAST_CURSOR_BYTES];
        cursor[0..4].copy_from_slice(b"MRC1");
        cursor[4..8].copy_from_slice(&1_u32.to_le_bytes());
        cursor
    }

    fn push_raycast_mutation(
        cases: &mut Vec<(
            String,
            [u8; RAYCAST_INPUT_BYTES],
            [u8; RAYCAST_CURSOR_BYTES],
        )>,
        name: &str,
        mut input: [u8; RAYCAST_INPUT_BYTES],
        mut cursor: [u8; RAYCAST_CURSOR_BYTES],
        mutate: impl FnOnce(&mut [u8; RAYCAST_INPUT_BYTES], &mut [u8; RAYCAST_CURSOR_BYTES]),
    ) {
        mutate(&mut input, &mut cursor);
        cases.push((name.to_owned(), input, cursor));
    }

    fn write_test_f32<const N: usize>(bytes: &mut [u8; N], offset: usize, value: f32) {
        bytes[offset..offset + 4].copy_from_slice(&value.to_bits().to_le_bytes());
    }

    fn assert_raycast_input_failure_is_atomic(
        name: &str,
        input: [u8; RAYCAST_INPUT_BYTES],
        cursor: [u8; RAYCAST_CURSOR_BYTES],
    ) {
        let mut input_arena = [0xa5_u8; RAYCAST_INPUT_BYTES + 2];
        input_arena[1..1 + RAYCAST_INPUT_BYTES].copy_from_slice(&input);
        let mut cursor_arena = [0xa5_u8; RAYCAST_CURSOR_BYTES + 2];
        cursor_arena[1..1 + RAYCAST_CURSOR_BYTES].copy_from_slice(&cursor);
        let mut output_arena = [0xa5_u8; RAYCAST_OUTPUT_BYTES + 2];
        let before_input = input_arena;
        let before_cursor = cursor_arena;
        let before_output = output_arena;
        let mut count = usize::MAX;
        let mut done = 0xff;

        let status = unsafe {
            super::mornlea_raycast_batch(
                ABI_VERSION,
                input_arena[1..].as_ptr(),
                RAYCAST_INPUT_BYTES,
                cursor_arena[1..].as_mut_ptr(),
                RAYCAST_CURSOR_BYTES,
                output_arena[1..].as_mut_ptr(),
                RAYCAST_OUTPUT_BYTES,
                &mut count,
                &mut done,
            )
        };

        assert_eq!(status, MORNLEA_STATUS_INPUT, "{name}");
        assert_eq!((count, done), (0, 0), "{name}");
        assert_eq!(input_arena, before_input, "{name}");
        assert_eq!(cursor_arena, before_cursor, "{name}");
        assert_eq!(output_arena, before_output, "{name}");
    }

    #[derive(Clone, Copy, Debug)]
    enum InvalidMetadataPointer {
        MisalignedCount,
        WrappingCount,
        WrappingDone,
    }

    fn assert_invalid_raycast_metadata_pointer_is_atomic(kind: InvalidMetadataPointer) {
        let input = valid_raycast_input();
        let mut cursor_arena = [0xa5_u8; RAYCAST_CURSOR_BYTES + 2];
        cursor_arena[1..1 + RAYCAST_CURSOR_BYTES].copy_from_slice(&fresh_raycast_cursor());
        let mut output_arena = [0xa5_u8; RAYCAST_OUTPUT_BYTES + 2];
        let before_cursor = cursor_arena;
        let before_output = output_arena;
        let mut count = usize::MAX;
        let mut done = 0xff;
        let mut count_bytes = AlignedBytes([0xa5_u8; size_of::<usize>() + 1]);
        let before_count_bytes = count_bytes.0;
        let (count_pointer, done_pointer): (*mut usize, *mut u8) = match kind {
            InvalidMetadataPointer::MisalignedCount => (
                unsafe { count_bytes.0.as_mut_ptr().add(1).cast::<usize>() },
                &mut done,
            ),
            InvalidMetadataPointer::WrappingCount => (
                (usize::MAX & !(align_of::<usize>() - 1)) as *mut usize,
                &mut done,
            ),
            InvalidMetadataPointer::WrappingDone => (&mut count, usize::MAX as *mut u8),
        };

        let status = unsafe {
            super::mornlea_raycast_batch(
                ABI_VERSION,
                input.as_ptr(),
                input.len(),
                cursor_arena[1..].as_mut_ptr(),
                RAYCAST_CURSOR_BYTES,
                output_arena[1..].as_mut_ptr(),
                RAYCAST_OUTPUT_BYTES,
                count_pointer,
                done_pointer,
            )
        };

        assert_eq!(status, MORNLEA_STATUS_INVALID_ARGUMENT, "{kind:?}");
        assert_eq!(count, usize::MAX, "{kind:?}");
        assert_eq!(done, 0xff, "{kind:?}");
        assert_eq!(count_bytes.0, before_count_bytes, "{kind:?}");
        assert_eq!(cursor_arena, before_cursor, "{kind:?}");
        assert_eq!(output_arena, before_output, "{kind:?}");
    }

    #[derive(Clone, Copy, Debug)]
    enum MetadataField {
        Count,
        Done,
    }

    #[derive(Clone, Copy, Debug)]
    enum RaycastBuffer {
        Input,
        Cursor,
        Output,
    }

    #[derive(Clone, Copy, Debug)]
    enum RaycastBufferLength {
        Exact,
        Zero,
        Short,
        Long,
        Wrapping,
    }

    #[repr(align(8))]
    struct AlignedBytes<const N: usize>([u8; N]);

    fn raycast_buffer_length(buffer: RaycastBuffer, length: RaycastBufferLength) -> usize {
        let exact = match buffer {
            RaycastBuffer::Input => RAYCAST_INPUT_BYTES,
            RaycastBuffer::Cursor => RAYCAST_CURSOR_BYTES,
            RaycastBuffer::Output => RAYCAST_OUTPUT_BYTES,
        };
        match length {
            RaycastBufferLength::Exact => exact,
            RaycastBufferLength::Zero => 0,
            RaycastBufferLength::Short => exact - 1,
            RaycastBufferLength::Long => exact + 1,
            RaycastBufferLength::Wrapping => usize::MAX,
        }
    }

    fn assert_raycast_metadata_overlap_is_atomic(
        metadata: MetadataField,
        buffer: RaycastBuffer,
        length: RaycastBufferLength,
    ) {
        const CANARY_BYTES: usize = 8;
        let mut input_arena = AlignedBytes([0xa5_u8; RAYCAST_INPUT_BYTES + 2 * CANARY_BYTES]);
        input_arena.0[CANARY_BYTES..CANARY_BYTES + RAYCAST_INPUT_BYTES]
            .copy_from_slice(&valid_raycast_input());
        let mut cursor_arena = AlignedBytes([0xa5_u8; RAYCAST_CURSOR_BYTES + 2 * CANARY_BYTES]);
        cursor_arena.0[CANARY_BYTES..CANARY_BYTES + RAYCAST_CURSOR_BYTES]
            .copy_from_slice(&fresh_raycast_cursor());
        let mut output_arena = AlignedBytes([0xa5_u8; RAYCAST_OUTPUT_BYTES + 2 * CANARY_BYTES]);
        let before_input = input_arena.0;
        let before_cursor = cursor_arena.0;
        let before_output = output_arena.0;
        let input_pointer = unsafe { input_arena.0.as_mut_ptr().add(CANARY_BYTES) };
        let cursor_pointer = unsafe { cursor_arena.0.as_mut_ptr().add(CANARY_BYTES) };
        let output_pointer = unsafe { output_arena.0.as_mut_ptr().add(CANARY_BYTES) };
        let overlap_pointer = match buffer {
            RaycastBuffer::Input => input_pointer,
            RaycastBuffer::Cursor => cursor_pointer,
            RaycastBuffer::Output => output_pointer,
        };
        let mut count = usize::MAX;
        let mut done = 0xff;
        let count_pointer = match metadata {
            MetadataField::Count => overlap_pointer.cast::<usize>(),
            MetadataField::Done => &mut count,
        };
        let done_pointer = match metadata {
            MetadataField::Count => &mut done,
            MetadataField::Done => overlap_pointer,
        };
        let input_len = if matches!(buffer, RaycastBuffer::Input) {
            raycast_buffer_length(buffer, length)
        } else {
            RAYCAST_INPUT_BYTES
        };
        let cursor_len = if matches!(buffer, RaycastBuffer::Cursor) {
            raycast_buffer_length(buffer, length)
        } else {
            RAYCAST_CURSOR_BYTES
        };
        let output_len = if matches!(buffer, RaycastBuffer::Output) {
            raycast_buffer_length(buffer, length)
        } else {
            RAYCAST_OUTPUT_BYTES
        };

        let status = unsafe {
            super::mornlea_raycast_batch(
                ABI_VERSION,
                input_pointer,
                input_len,
                cursor_pointer,
                cursor_len,
                output_pointer,
                output_len,
                count_pointer,
                done_pointer,
            )
        };

        assert_eq!(
            input_arena.0, before_input,
            "{metadata:?}/{buffer:?}/{length:?}"
        );
        assert_eq!(
            cursor_arena.0, before_cursor,
            "{metadata:?}/{buffer:?}/{length:?}"
        );
        assert_eq!(
            output_arena.0, before_output,
            "{metadata:?}/{buffer:?}/{length:?}"
        );
        assert_eq!(count, usize::MAX, "{metadata:?}/{buffer:?}/{length:?}");
        assert_eq!(done, 0xff, "{metadata:?}/{buffer:?}/{length:?}");
        assert_eq!(
            status, MORNLEA_STATUS_INVALID_ARGUMENT,
            "{metadata:?}/{buffer:?}/{length:?}"
        );
    }

    #[test]
    fn malformed_collision_input_keeps_output_unchanged() {
        let mut input = [0_u8; 64 + 4 * 196];
        input[0..4].copy_from_slice(b"MGC1");
        input[4..8].copy_from_slice(&1_u32.to_le_bytes());
        for (offset, value) in [(8, 0.5_f32), (12, 1.0), (16, 0.5), (36, 0.6)] {
            input[offset..offset + 4].copy_from_slice(&value.to_bits().to_le_bytes());
        }
        input[32] = 1;
        input[33] = 1;
        input[52..56].copy_from_slice(&1_u32.to_le_bytes());
        input[56..60].copy_from_slice(&4_u32.to_le_bytes());
        input[60..64].copy_from_slice(&1_u32.to_le_bytes());
        let mut output = [0xa5_u8; 16];

        let status = unsafe {
            mornlea_collision_resolve(
                ABI_VERSION,
                input.as_ptr(),
                input.len(),
                output.as_mut_ptr(),
                output.len(),
            )
        };

        assert_eq!(status, MORNLEA_STATUS_INPUT);
        assert_eq!(output, [0xa5; 16]);
    }

    #[test]
    fn valid_input_returns_ok_and_zero_quads() {
        let input = valid_input();
        let mut scratch = vec![0_u32; (48 * 48 * 48 * 5) / 4];
        let mut output = vec![0_u64; 6 * 4096];
        let mut output_len = usize::MAX;
        let status = unsafe {
            mornlea_mesh_section(
                ABI_VERSION,
                input.as_ptr(),
                input.len(),
                scratch.as_mut_ptr().cast(),
                scratch.len() * 4,
                output.as_mut_ptr(),
                output.len(),
                &mut output_len,
            )
        };
        assert_eq!(status, MORNLEA_STATUS_OK);
        assert_eq!(output_len, 0);
    }

    #[test]
    fn uniform_air_returns_without_touching_light_scratch() {
        const BLOCKS_OFFSET: usize = 16;
        let mut input = valid_input();
        input[BLOCKS_OFFSET..BLOCKS_OFFSET + 27 * 4096 * 2].fill(0);
        let mut scratch = vec![0xa5a5_a5a5_u32; (48 * 48 * 48 * 5) / 4];
        let mut output = vec![0_u64; 6 * 4096];
        let mut output_len = usize::MAX;

        let status = unsafe {
            mornlea_mesh_section(
                ABI_VERSION,
                input.as_ptr(),
                input.len(),
                scratch.as_mut_ptr().cast(),
                scratch.len() * 4,
                output.as_mut_ptr(),
                output.len(),
                &mut output_len,
            )
        };

        assert_eq!(status, MORNLEA_STATUS_OK);
        assert_eq!(output_len, 0);
        assert!(scratch.iter().all(|&word| word == 0xa5a5_a5a5));
    }

    #[test]
    fn uniform_air_skips_unused_registry_semantics_and_light() {
        const BLOCKS_OFFSET: usize = 16;
        const BLOCKS_BYTES: usize = 27 * 4096 * 2;
        const REGISTRY_OFFSET: usize = BLOCKS_OFFSET + BLOCKS_BYTES + 9 + 9 * 256 * 2;
        let mut base = valid_input();
        base[BLOCKS_OFFSET..BLOCKS_OFFSET + BLOCKS_BYTES].fill(0);
        base[BLOCKS_OFFSET..BLOCKS_OFFSET + 2].copy_from_slice(&40000_u16.to_le_bytes());

        let cases = [
            ("overbright", {
                let mut input = base.clone();
                input[REGISTRY_OFFSET + 2 * 16 + 3] = 16;
                input
            }),
            ("bad opacity", {
                let mut input = base.clone();
                input[REGISTRY_OFFSET + 2] = 2;
                input
            }),
            ("duplicate id", {
                let mut input = base.clone();
                input[REGISTRY_OFFSET + 16..REGISTRY_OFFSET + 18]
                    .copy_from_slice(&0_u16.to_le_bytes());
                input
            }),
            ("same air and barrier", {
                let mut input = base.clone();
                input[14..16].copy_from_slice(&0_u16.to_le_bytes());
                input
            }),
            ("missing barrier", {
                let mut input = base;
                input[REGISTRY_OFFSET + 16..REGISTRY_OFFSET + 18]
                    .copy_from_slice(&2_u16.to_le_bytes());
                input
            }),
        ];

        for (name, input) in cases {
            let mut scratch = vec![0xa5a5_a5a5_u32; (48 * 48 * 48 * 5) / 4];
            let mut output = vec![0_u64; 6 * 4096];
            let mut output_len = usize::MAX;
            let status = unsafe {
                mornlea_mesh_section(
                    ABI_VERSION,
                    input.as_ptr(),
                    input.len(),
                    scratch.as_mut_ptr().cast(),
                    scratch.len() * 4,
                    output.as_mut_ptr(),
                    output.len(),
                    &mut output_len,
                )
            };

            assert_eq!(status, MORNLEA_STATUS_OK, "{name}");
            assert_eq!(output_len, 0, "{name}");
            assert!(scratch.iter().all(|&word| word == 0xa5a5_a5a5), "{name}");
        }
    }

    #[test]
    fn uniform_air_still_rejects_structural_presence_error() {
        const BLOCKS_OFFSET: usize = 16;
        const BLOCKS_BYTES: usize = 27 * 4096 * 2;
        let mut input = valid_input();
        input[BLOCKS_OFFSET..BLOCKS_OFFSET + BLOCKS_BYTES].fill(0);
        input[BLOCKS_OFFSET + BLOCKS_BYTES] = 2;
        let mut scratch = vec![0xa5a5_a5a5_u32; (48 * 48 * 48 * 5) / 4];
        let mut output = vec![0_u64; 6 * 4096];
        let mut output_len = usize::MAX;

        let status = unsafe {
            mornlea_mesh_section(
                ABI_VERSION,
                input.as_ptr(),
                input.len(),
                scratch.as_mut_ptr().cast(),
                scratch.len() * 4,
                output.as_mut_ptr(),
                output.len(),
                &mut output_len,
            )
        };

        assert_eq!(status, MORNLEA_STATUS_INPUT);
        assert_eq!(output_len, 0);
        assert!(scratch.iter().all(|&word| word == 0xa5a5_a5a5));
    }

    #[test]
    fn ffi_publishes_six_quads_only_after_complete_mesh() {
        const BLOCKS_OFFSET: usize = 16;
        const REGISTRY_OFFSET: usize = BLOCKS_OFFSET + 27 * 4096 * 2 + 9 + 9 * 256 * 2;
        let mut input = valid_input();
        input[BLOCKS_OFFSET..BLOCKS_OFFSET + 27 * 4096 * 2].fill(0);
        input[REGISTRY_OFFSET + 3 * 16..REGISTRY_OFFSET + 3 * 16 + 8]
            .copy_from_slice(&0_u64.to_le_bytes());
        let center = BLOCKS_OFFSET + (13 * 4096 + ((8 << 8) | (8 << 4) | 8)) * 2;
        input[center..center + 2].copy_from_slice(&1_u16.to_le_bytes());
        let mut scratch = vec![0_u32; (48 * 48 * 48 * 5) / 4];
        let mut output = vec![0_u64; 6 * 4096];
        let mut output_len = usize::MAX;

        let status = unsafe {
            mornlea_mesh_section(
                ABI_VERSION,
                input.as_ptr(),
                input.len(),
                scratch.as_mut_ptr().cast(),
                scratch.len() * 4,
                output.as_mut_ptr(),
                output.len(),
                &mut output_len,
            )
        };

        assert_eq!(status, MORNLEA_STATUS_OK);
        assert_eq!(output_len, 6);
        assert_eq!(
            output[..6]
                .iter()
                .map(|packed| (packed >> 20) & 7)
                .sum::<u64>(),
            15
        );
    }

    #[test]
    fn input_range_rejects_slice_size_overflow_and_address_wrap() {
        let byte = 0_u8;
        assert!(input_range_is_valid(&byte, 1));
        assert!(!input_range_is_valid(&byte, isize::MAX as usize + 1));
        assert!(!input_range_is_valid(
            std::ptr::without_provenance(usize::MAX),
            1
        ));
    }

    #[test]
    fn scratch_range_rejects_aligned_address_wrap() {
        let mut storage = vec![0_u64; SCRATCH_BYTES.div_ceil(size_of::<u64>())];
        assert!(scratch_range_is_valid(
            storage.as_mut_ptr().cast(),
            SCRATCH_BYTES
        ));

        let aligned_max = usize::MAX & !(align_of::<u64>() - 1);
        assert!(!scratch_range_is_valid(
            std::ptr::without_provenance_mut(aligned_max),
            SCRATCH_BYTES
        ));
    }

    #[test]
    fn output_range_rejects_address_and_capacity_overflow() {
        let mut output = 0_u64;
        assert!(output_range_is_valid(&mut output, 1));
        assert!(!output_range_is_valid(
            std::ptr::without_provenance_mut(usize::MAX),
            1
        ));
        assert!(!output_range_is_valid(&mut output, usize::MAX));
    }

    #[test]
    fn oversized_input_len_returns_input_atomically() {
        let input = valid_input();
        let mut scratch = vec![0_u32; (48 * 48 * 48 * 5) / 4];
        let mut output = vec![0_u64; 6 * 4096];
        let mut output_len = usize::MAX;
        // SAFETY: 被测入口在构造 slice 前拒绝超过 isize::MAX 的长度。
        let status = unsafe {
            mornlea_mesh_section(
                ABI_VERSION,
                input.as_ptr(),
                isize::MAX as usize + 1,
                scratch.as_mut_ptr().cast(),
                scratch.len() * 4,
                output.as_mut_ptr(),
                output.len(),
                &mut output_len,
            )
        };
        assert_eq!(status, MORNLEA_STATUS_INPUT);
        assert_eq!(output_len, 0);
    }

    #[test]
    fn wrapping_input_range_returns_input_atomically() {
        let mut scratch = vec![0_u32; (48 * 48 * 48 * 5) / 4];
        let mut output = vec![0_u64; 6 * 4096];
        let mut output_len = usize::MAX;
        // SAFETY: 被测入口在构造 slice 前拒绝地址加一发生回绕的范围。
        let status = unsafe {
            mornlea_mesh_section(
                ABI_VERSION,
                std::ptr::without_provenance(usize::MAX),
                1,
                scratch.as_mut_ptr().cast(),
                scratch.len() * 4,
                output.as_mut_ptr(),
                output.len(),
                &mut output_len,
            )
        };
        assert_eq!(status, MORNLEA_STATUS_INPUT);
        assert_eq!(output_len, 0);
    }

    #[test]
    fn status_numbers_match_the_c_abi() {
        assert_eq!(
            [
                MORNLEA_STATUS_OK,
                MORNLEA_STATUS_ABI_VERSION,
                MORNLEA_STATUS_INVALID_ARGUMENT,
                MORNLEA_STATUS_INPUT,
                MORNLEA_STATUS_SCRATCH,
                MORNLEA_STATUS_REGISTRY,
                MORNLEA_STATUS_EMISSION,
                MORNLEA_STATUS_OUTPUT_OVERFLOW,
                MORNLEA_STATUS_QUEUE_OVERFLOW,
                MORNLEA_STATUS_PANIC,
            ],
            [0, 1, 2, 3, 4, 5, 6, 7, 8, 9]
        );
    }

    #[test]
    fn abi_version_failure_is_atomic() {
        let input = valid_input();
        let mut scratch = vec![0_u32; (48 * 48 * 48 * 5) / 4];
        let mut output = vec![0_u64; 6 * 4096];
        let mut output_len = usize::MAX;
        let status = unsafe {
            mornlea_mesh_section(
                ABI_VERSION + 1,
                input.as_ptr(),
                input.len(),
                scratch.as_mut_ptr().cast(),
                scratch.len() * 4,
                output.as_mut_ptr(),
                output.len(),
                &mut output_len,
            )
        };
        assert_eq!(status, MORNLEA_STATUS_ABI_VERSION);
        assert_eq!(output_len, 0);
    }

    #[test]
    fn invalid_arguments_and_inputs_return_exact_atomic_statuses() {
        const REGISTRY_OFFSET: usize = 225817;
        let input = valid_input();
        let mut scratch = vec![0_u32; (48 * 48 * 48 * 5) / 4];
        let mut output = vec![0_u64; 6 * 4096];

        let mut cases = vec![
            (
                "short input",
                input[..input.len() - 1].to_vec(),
                MORNLEA_STATUS_INPUT,
            ),
            (
                "long input",
                {
                    let mut long = input.clone();
                    long.push(0);
                    long
                },
                MORNLEA_STATUS_INPUT,
            ),
            (
                "malformed registry",
                {
                    let mut malformed = input.clone();
                    malformed[REGISTRY_OFFSET + 16..REGISTRY_OFFSET + 18]
                        .copy_from_slice(&0_u16.to_le_bytes());
                    malformed
                },
                MORNLEA_STATUS_REGISTRY,
            ),
            (
                "overbright emission",
                {
                    let mut overbright = input.clone();
                    overbright[REGISTRY_OFFSET + 2 * 16 + 3] = 16;
                    overbright
                },
                MORNLEA_STATUS_EMISSION,
            ),
        ];
        for (name, case, want) in cases.drain(..) {
            let mut output_len = usize::MAX;
            // SAFETY: 本测试提供有效对齐的独占缓冲区，长度均与切片一致。
            let status = unsafe {
                mornlea_mesh_section(
                    ABI_VERSION,
                    case.as_ptr(),
                    case.len(),
                    scratch.as_mut_ptr().cast(),
                    scratch.len() * 4,
                    output.as_mut_ptr(),
                    output.len(),
                    &mut output_len,
                )
            };
            assert_eq!(status, want, "{name}");
            assert_eq!(output_len, 0, "{name}");
        }

        let mut output_len = usize::MAX;
        // SAFETY: 除被测的空 input 指针外，其余缓冲区均有效且对齐。
        let null_input = unsafe {
            mornlea_mesh_section(
                ABI_VERSION,
                std::ptr::null(),
                input.len(),
                scratch.as_mut_ptr().cast(),
                scratch.len() * 4,
                output.as_mut_ptr(),
                output.len(),
                &mut output_len,
            )
        };
        assert_eq!(null_input, MORNLEA_STATUS_INVALID_ARGUMENT);
        assert_eq!(output_len, 0);

        output_len = usize::MAX;
        // SAFETY: scratch 指针有效但长度被刻意缩短一个字节。
        let short_scratch = unsafe {
            mornlea_mesh_section(
                ABI_VERSION,
                input.as_ptr(),
                input.len(),
                scratch.as_mut_ptr().cast(),
                scratch.len() * 4 - 1,
                output.as_mut_ptr(),
                output.len(),
                &mut output_len,
            )
        };
        assert_eq!(short_scratch, MORNLEA_STATUS_SCRATCH);
        assert_eq!(output_len, 0);

        output_len = usize::MAX;
        // SAFETY: output 指针有效且对齐，capacity 被刻意缩短一个元素。
        let short_output = unsafe {
            mornlea_mesh_section(
                ABI_VERSION,
                input.as_ptr(),
                input.len(),
                scratch.as_mut_ptr().cast(),
                scratch.len() * 4,
                output.as_mut_ptr(),
                output.len() - 1,
                &mut output_len,
            )
        };
        assert_eq!(short_output, MORNLEA_STATUS_OUTPUT_OVERFLOW);
        assert_eq!(output_len, 0);
    }

    #[test]
    fn null_and_misaligned_buffers_are_rejected_atomically() {
        let input = valid_input();
        let mut scratch = vec![0_u64; (48 * 48 * 48 * 5) / 8 + 1];
        let mut output = vec![0_u64; 6 * 4096 + 1];
        let mut output_len = usize::MAX;

        // SAFETY: 除被测的空 output_len 指针外，其余指针均有效且对齐。
        let null_output_len = unsafe {
            mornlea_mesh_section(
                ABI_VERSION,
                input.as_ptr(),
                input.len(),
                scratch.as_mut_ptr().cast(),
                48 * 48 * 48 * 5,
                output.as_mut_ptr(),
                6 * 4096,
                std::ptr::null_mut(),
            )
        };
        assert_eq!(null_output_len, MORNLEA_STATUS_INVALID_ARGUMENT);

        // SAFETY: 除被测的空 scratch 指针外，其余指针均有效且对齐。
        let null_scratch = unsafe {
            mornlea_mesh_section(
                ABI_VERSION,
                input.as_ptr(),
                input.len(),
                std::ptr::null_mut(),
                48 * 48 * 48 * 5,
                output.as_mut_ptr(),
                6 * 4096,
                &mut output_len,
            )
        };
        assert_eq!(null_scratch, MORNLEA_STATUS_INVALID_ARGUMENT);
        assert_eq!(output_len, 0);

        output_len = usize::MAX;
        // SAFETY: 除被测的空 output 指针外，其余指针均有效且对齐。
        let null_output = unsafe {
            mornlea_mesh_section(
                ABI_VERSION,
                input.as_ptr(),
                input.len(),
                scratch.as_mut_ptr().cast(),
                48 * 48 * 48 * 5,
                std::ptr::null_mut(),
                6 * 4096,
                &mut output_len,
            )
        };
        assert_eq!(null_output, MORNLEA_STATUS_INVALID_ARGUMENT);
        assert_eq!(output_len, 0);

        output_len = usize::MAX;
        // SAFETY: scratch 分配足够大；加一字节只用于验证未对齐检查，函数不会解引用。
        let misaligned_scratch = unsafe {
            mornlea_mesh_section(
                ABI_VERSION,
                input.as_ptr(),
                input.len(),
                scratch.as_mut_ptr().cast::<u8>().add(1),
                48 * 48 * 48 * 5,
                output.as_mut_ptr(),
                6 * 4096,
                &mut output_len,
            )
        };
        assert_eq!(misaligned_scratch, MORNLEA_STATUS_SCRATCH);
        assert_eq!(output_len, 0);

        output_len = usize::MAX;
        // SAFETY: scratch 额外分配了一个 u64；加四字节仅用于验证 8-byte 对齐检查。
        let four_byte_aligned_scratch = unsafe {
            mornlea_mesh_section(
                ABI_VERSION,
                input.as_ptr(),
                input.len(),
                scratch.as_mut_ptr().cast::<u8>().add(4),
                48 * 48 * 48 * 5,
                output.as_mut_ptr(),
                6 * 4096,
                &mut output_len,
            )
        };
        assert_eq!(four_byte_aligned_scratch, MORNLEA_STATUS_SCRATCH);
        assert_eq!(output_len, 0);

        output_len = usize::MAX;
        // SAFETY: output 分配足够大；加一字节只用于验证未对齐检查，函数不会解引用。
        let misaligned_output = unsafe {
            mornlea_mesh_section(
                ABI_VERSION,
                input.as_ptr(),
                input.len(),
                scratch.as_mut_ptr().cast(),
                48 * 48 * 48 * 5,
                output.as_mut_ptr().cast::<u8>().add(1).cast(),
                6 * 4096,
                &mut output_len,
            )
        };
        assert_eq!(misaligned_output, MORNLEA_STATUS_INVALID_ARGUMENT);
        assert_eq!(output_len, 0);

        let mut output_len_storage = [usize::MAX, usize::MAX];
        // SAFETY: output_len 分配足够大；加一字节只用于验证未对齐检查，函数不会解引用。
        let misaligned_output_len = unsafe {
            mornlea_mesh_section(
                ABI_VERSION,
                input.as_ptr(),
                input.len(),
                scratch.as_mut_ptr().cast(),
                48 * 48 * 48 * 5,
                output.as_mut_ptr(),
                6 * 4096,
                output_len_storage.as_mut_ptr().cast::<u8>().add(1).cast(),
            )
        };
        assert_eq!(misaligned_output_len, MORNLEA_STATUS_INVALID_ARGUMENT);
        assert_eq!(output_len_storage, [usize::MAX, usize::MAX]);
    }

    #[test]
    fn aligned_wrapping_output_len_is_rejected_before_write() {
        let input = valid_input();
        let mut scratch = vec![0_u64; SCRATCH_BYTES.div_ceil(size_of::<u64>())];
        let mut output = vec![0_u64; 6 * 4096];
        let aligned_max = usize::MAX & !(align_of::<usize>() - 1);

        // SAFETY: output_len 为被测的对齐伪地址；入口必须在任何 write 前因地址回绕返回。
        let status = unsafe {
            mornlea_mesh_section(
                ABI_VERSION,
                input.as_ptr(),
                input.len(),
                scratch.as_mut_ptr().cast(),
                SCRATCH_BYTES,
                output.as_mut_ptr(),
                output.len(),
                std::ptr::without_provenance_mut(aligned_max),
            )
        };

        assert_eq!(status, MORNLEA_STATUS_INVALID_ARGUMENT);
    }

    #[test]
    fn overlapping_scratch_and_output_are_rejected_atomically() {
        let input = valid_input();
        let mut shared = vec![0_u64; (48_usize * 48 * 48 * 5).div_ceil(8)];
        let mut output_len = usize::MAX;

        // SAFETY: 共享 buffer 容量同时满足 scratch 与 output；入口应在创建任何 Rust slice 前拒绝重叠。
        let status = unsafe {
            mornlea_mesh_section(
                ABI_VERSION,
                input.as_ptr(),
                input.len(),
                shared.as_mut_ptr().cast(),
                48 * 48 * 48 * 5,
                shared.as_mut_ptr(),
                6 * 4096,
                &mut output_len,
            )
        };

        assert_eq!(status, MORNLEA_STATUS_SCRATCH);
        assert_eq!(output_len, 0);
    }

    #[test]
    fn overlapping_input_and_output_are_rejected_atomically() {
        let input = valid_input();
        let mut shared = vec![0_u64; input.len().div_ceil(size_of::<u64>())];
        // SAFETY: shared 的字节容量至少为 input.len()，这里只在调用前写入 encoded input。
        unsafe {
            std::slice::from_raw_parts_mut(shared.as_mut_ptr().cast::<u8>(), input.len())
                .copy_from_slice(&input);
        }
        let mut scratch = vec![0_u64; SCRATCH_BYTES.div_ceil(size_of::<u64>())];
        let mut output_len = usize::MAX;

        // SAFETY: 每个指针都有效且容量足够；被测入口必须在创建 slice 前拒绝 input/output 别名。
        let status = unsafe {
            mornlea_mesh_section(
                ABI_VERSION,
                shared.as_ptr().cast(),
                input.len(),
                scratch.as_mut_ptr().cast(),
                SCRATCH_BYTES,
                shared.as_mut_ptr(),
                6 * 4096,
                &mut output_len,
            )
        };

        assert_eq!(status, MORNLEA_STATUS_INVALID_ARGUMENT);
        assert_eq!(output_len, 0);
    }

    #[test]
    fn overlapping_output_len_with_input_or_output_is_rejected_atomically() {
        let input = valid_input();
        let mut shared_input = vec![0_usize; input.len().div_ceil(size_of::<usize>())];
        // SAFETY: shared_input 的字节容量覆盖完整 encoded input。
        unsafe {
            std::slice::from_raw_parts_mut(shared_input.as_mut_ptr().cast::<u8>(), input.len())
                .copy_from_slice(&input);
        }
        let mut scratch = vec![0_u64; SCRATCH_BYTES.div_ceil(size_of::<u64>())];
        let mut output = vec![0_u64; 6 * 4096];

        // SAFETY: output_len 刻意指向 input；入口可写零，但必须在构造 input slice 前拒绝别名。
        let input_status = unsafe {
            mornlea_mesh_section(
                ABI_VERSION,
                shared_input.as_ptr().cast(),
                input.len(),
                scratch.as_mut_ptr().cast(),
                SCRATCH_BYTES,
                output.as_mut_ptr(),
                output.len(),
                shared_input.as_mut_ptr(),
            )
        };
        assert_eq!(input_status, MORNLEA_STATUS_INVALID_ARGUMENT);
        assert_eq!(shared_input[0], 0);

        // SAFETY: output_len 刻意指向 output；入口必须在构造 output slice 前拒绝别名。
        let output_status = unsafe {
            mornlea_mesh_section(
                ABI_VERSION,
                input.as_ptr(),
                input.len(),
                scratch.as_mut_ptr().cast(),
                SCRATCH_BYTES,
                output.as_mut_ptr(),
                output.len(),
                output.as_mut_ptr().cast(),
            )
        };
        assert_eq!(output_status, MORNLEA_STATUS_INVALID_ARGUMENT);
        assert_eq!(output[0], 0);
    }

    #[test]
    fn overlapping_scratch_with_input_or_output_len_is_rejected_atomically() {
        let input = valid_input();
        let mut output = vec![0_u64; 6 * 4096];

        let mut shared_input = vec![0_u64; SCRATCH_BYTES.div_ceil(size_of::<u64>())];
        let shared_input_ptr = shared_input.as_mut_ptr().cast::<u8>();
        // SAFETY: shared_input 容量大于 input，只在调用前把有效 input 拷贝进该对齐 buffer。
        unsafe { std::slice::from_raw_parts_mut(shared_input_ptr, input.len()) }
            .copy_from_slice(&input);
        let mut output_len = usize::MAX;
        // SAFETY: 除被测的 input/scratch 重叠外，其余指针与容量都有效；入口应在构造 slice 前拒绝。
        let input_status = unsafe {
            mornlea_mesh_section(
                ABI_VERSION,
                shared_input_ptr,
                input.len(),
                shared_input_ptr,
                SCRATCH_BYTES,
                output.as_mut_ptr(),
                output.len(),
                &mut output_len,
            )
        };
        assert_eq!(input_status, MORNLEA_STATUS_SCRATCH);
        assert_eq!(output_len, 0);

        let mut shared_output_len = vec![usize::MAX; SCRATCH_BYTES.div_ceil(size_of::<usize>())];
        let shared_output_len_ptr = shared_output_len.as_mut_ptr();
        // SAFETY: 除被测的 scratch/output_len 重叠外，其余指针与容量都有效；入口先将 output_len 原子清零再拒绝重叠。
        let output_len_status = unsafe {
            mornlea_mesh_section(
                ABI_VERSION,
                input.as_ptr(),
                input.len(),
                shared_output_len.as_mut_ptr().cast(),
                SCRATCH_BYTES,
                output.as_mut_ptr(),
                output.len(),
                shared_output_len_ptr,
            )
        };
        assert_eq!(output_len_status, MORNLEA_STATUS_SCRATCH);
        assert_eq!(shared_output_len[0], 0);
    }

    use super::{WORLDGEN_CHUNK_OUTPUT_BYTES, mornlea_worldgen_chunk, mornlea_worldgen_probe};
    use crate::worldgen::{
        WORLDGEN_CHUNK_INPUT_BYTES, WORLDGEN_HEADER_BYTES, WORLDGEN_PROBE_RECORD_BYTES,
    };

    /// 构造一个合法的 worldgen header:seed 42、互异材料表 1..=14(末项 water)、恒等 perm。
    fn worldgen_header() -> Vec<u8> {
        let mut bytes = vec![0u8; WORLDGEN_HEADER_BYTES];
        bytes[0..4].copy_from_slice(b"MGW1");
        bytes[4..8].copy_from_slice(&2u32.to_le_bytes());
        bytes[8..16].copy_from_slice(&42i64.to_le_bytes());
        bytes[16..20].copy_from_slice(&(-64i32).to_le_bytes());
        bytes[20..24].copy_from_slice(&320i32.to_le_bytes());
        for (index, id) in (1u16..=14).enumerate() {
            // 材料表刻意避开 0:air=1 便于区分“输出缓冲原样”与“生成的空气”。
            bytes[24 + index * 2..26 + index * 2].copy_from_slice(&id.to_le_bytes());
        }
        for (index, entry) in bytes[52..WORLDGEN_HEADER_BYTES].iter_mut().enumerate() {
            *entry = (index & 255) as u8;
        }
        bytes
    }

    fn worldgen_chunk_input(chunk_x: i32, chunk_z: i32) -> Vec<u8> {
        let mut bytes = worldgen_header();
        bytes.extend_from_slice(&chunk_x.to_le_bytes());
        bytes.extend_from_slice(&chunk_z.to_le_bytes());
        bytes
    }

    fn worldgen_probe_input(records: &[(u32, i32, i32, i32)]) -> Vec<u8> {
        let mut bytes = worldgen_header();
        bytes.extend_from_slice(&(records.len() as u32).to_le_bytes());
        for &(mode, wx, wy, wz) in records {
            bytes.extend_from_slice(&mode.to_le_bytes());
            bytes.extend_from_slice(&wx.to_le_bytes());
            bytes.extend_from_slice(&wy.to_le_bytes());
            bytes.extend_from_slice(&wz.to_le_bytes());
        }
        bytes
    }

    #[test]
    fn worldgen_chunk_is_deterministic_and_wrong_abi_is_rejected() {
        let input = worldgen_chunk_input(0, 0);
        let mut first = vec![0u8; WORLDGEN_CHUNK_OUTPUT_BYTES];
        let mut second = vec![0u8; WORLDGEN_CHUNK_OUTPUT_BYTES];
        // SAFETY: 指针来自有效 Vec,长度与缓冲容量一致。
        let status_first = unsafe {
            mornlea_worldgen_chunk(
                ABI_VERSION,
                input.as_ptr(),
                input.len(),
                first.as_mut_ptr(),
                first.len(),
            )
        };
        // SAFETY: 同上。
        let status_second = unsafe {
            mornlea_worldgen_chunk(
                ABI_VERSION,
                input.as_ptr(),
                input.len(),
                second.as_mut_ptr(),
                second.len(),
            )
        };
        assert_eq!(status_first, MORNLEA_STATUS_OK);
        assert_eq!(status_second, MORNLEA_STATUS_OK);
        assert_eq!(first, second);
        // 生成结果必然包含基岩层(材料 5),不可能全零。
        assert!(first.iter().any(|&b| b != 0));

        // SAFETY: 同上;仅 abi_version 不匹配。
        let status_abi = unsafe {
            mornlea_worldgen_chunk(
                ABI_VERSION + 1,
                input.as_ptr(),
                input.len(),
                second.as_mut_ptr(),
                second.len(),
            )
        };
        assert_eq!(status_abi, MORNLEA_STATUS_ABI_VERSION);
    }

    #[test]
    fn worldgen_chunk_invalid_input_leaves_output_untouched() {
        let mut output = vec![0xAAu8; WORLDGEN_CHUNK_OUTPUT_BYTES];
        let canary = output.clone();

        let mut bad_magic = worldgen_chunk_input(0, 0);
        bad_magic[0] = b'X';
        let mut duplicate_material = worldgen_chunk_input(0, 0);
        // 把 dirt 改成与 stone 相同的 ID,触发材料表互异性校验。
        duplicate_material[26..28].copy_from_slice(&1u16.to_le_bytes());
        let mut wrong_min_y = worldgen_chunk_input(0, 0);
        wrong_min_y[16..20].copy_from_slice(&(-32i32).to_le_bytes());
        let truncated = worldgen_chunk_input(0, 0)[..WORLDGEN_CHUNK_INPUT_BYTES - 1].to_vec();

        for input in [&bad_magic, &duplicate_material, &wrong_min_y, &truncated] {
            // SAFETY: 指针来自有效 Vec,长度与缓冲容量一致。
            let status = unsafe {
                mornlea_worldgen_chunk(
                    ABI_VERSION,
                    input.as_ptr(),
                    input.len(),
                    output.as_mut_ptr(),
                    output.len(),
                )
            };
            assert_eq!(status, MORNLEA_STATUS_INPUT);
            assert_eq!(output, canary);
        }

        let valid = worldgen_chunk_input(0, 0);
        // SAFETY: 输出缓冲不足,入口应在写入前拒绝。
        let status_short = unsafe {
            mornlea_worldgen_chunk(
                ABI_VERSION,
                valid.as_ptr(),
                valid.len(),
                output.as_mut_ptr(),
                output.len() - 1,
            )
        };
        assert_eq!(status_short, MORNLEA_STATUS_OUTPUT_OVERFLOW);
        assert_eq!(output, canary);
    }

    #[test]
    fn worldgen_probe_matches_chunk_and_rejects_bad_records() {
        let chunk_input = worldgen_chunk_input(0, 0);
        let mut dense = vec![0u8; WORLDGEN_CHUNK_OUTPUT_BYTES];
        // SAFETY: 指针来自有效 Vec,长度与缓冲容量一致。
        let chunk_status = unsafe {
            mornlea_worldgen_chunk(
                ABI_VERSION,
                chunk_input.as_ptr(),
                chunk_input.len(),
                dense.as_mut_ptr(),
                dense.len(),
            )
        };
        assert_eq!(chunk_status, MORNLEA_STATUS_OK);

        // 探测区块内一根整列:mode 2(BaseBlockAt)必须与 dense 输出逐格一致。
        let mut records = Vec::new();
        for y in [-64i32, -20, 0, 64, 90, 319] {
            records.push((2u32, 3i32, y, 5i32));
        }
        records.push((0, 3, 0, 5));
        let probe_input = worldgen_probe_input(&records);
        let mut probe_out = vec![0u8; records.len() * 8];
        // SAFETY: 同上。
        let probe_status = unsafe {
            mornlea_worldgen_probe(
                ABI_VERSION,
                probe_input.as_ptr(),
                probe_input.len(),
                probe_out.as_mut_ptr(),
                probe_out.len(),
            )
        };
        assert_eq!(probe_status, MORNLEA_STATUS_OK);
        for (index, &(_, _, y, _)) in records[..records.len() - 1].iter().enumerate() {
            let block = u16::from_le_bytes([probe_out[index * 8 + 4], probe_out[index * 8 + 5]]);
            let dense_offset = (((y + 64) * 16 * 16 + 5 * 16 + 3) * 2) as usize;
            let expected = u16::from_le_bytes([dense[dense_offset], dense[dense_offset + 1]]);
            assert_eq!(block, expected, "y={y}");
        }
        let height = i32::from_le_bytes(
            probe_out[(records.len() - 1) * 8..(records.len() - 1) * 8 + 4]
                .try_into()
                .unwrap(),
        );
        // seed 42 的 (3,5) 高度必须落在地形振幅范围内。
        assert!((0..200).contains(&height), "height={height}");

        // mode 越界、record_count 与长度不符、输出长度不匹配都必须原样拒绝。
        let mut bad_mode = worldgen_probe_input(&[(3, 0, 0, 0)]);
        let mut out_one = vec![0xBBu8; 8];
        let canary_one = out_one.clone();
        // SAFETY: 同上。
        let status_mode = unsafe {
            mornlea_worldgen_probe(
                ABI_VERSION,
                bad_mode.as_ptr(),
                bad_mode.len(),
                out_one.as_mut_ptr(),
                out_one.len(),
            )
        };
        assert_eq!(status_mode, MORNLEA_STATUS_INPUT);
        assert_eq!(out_one, canary_one);

        bad_mode.truncate(WORLDGEN_HEADER_BYTES + 4 + WORLDGEN_PROBE_RECORD_BYTES - 1);
        // SAFETY: 同上。
        let status_truncated = unsafe {
            mornlea_worldgen_probe(
                ABI_VERSION,
                bad_mode.as_ptr(),
                bad_mode.len(),
                out_one.as_mut_ptr(),
                out_one.len(),
            )
        };
        assert_eq!(status_truncated, MORNLEA_STATUS_INPUT);
        assert_eq!(out_one, canary_one);

        let valid_one = worldgen_probe_input(&[(0, 0, 0, 0)]);
        // SAFETY: 输出缓冲不足,入口应在写入前拒绝。
        let status_short = unsafe {
            mornlea_worldgen_probe(
                ABI_VERSION,
                valid_one.as_ptr(),
                valid_one.len(),
                out_one.as_mut_ptr(),
                out_one.len() - 1,
            )
        };
        assert_eq!(status_short, MORNLEA_STATUS_OUTPUT_OVERFLOW);
        assert_eq!(out_one, canary_one);
    }
}
