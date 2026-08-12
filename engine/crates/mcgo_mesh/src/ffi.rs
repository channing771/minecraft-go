use std::mem::{align_of, size_of};

use crate::greedy::{MeshError as GreedyError, center_is_air, mesh_section};
use crate::input::{InputError, MeshInput};
use crate::light::{LIGHT_VOLUME, LightScratch, MeshError as LightError, build_light};

pub(crate) const ABI_VERSION: u32 = 1;

pub(crate) const MCGO_STATUS_OK: u32 = 0;
pub(crate) const MCGO_STATUS_ABI_VERSION: u32 = 1;
pub(crate) const MCGO_STATUS_INVALID_ARGUMENT: u32 = 2;
pub(crate) const MCGO_STATUS_INPUT: u32 = 3;
pub(crate) const MCGO_STATUS_SCRATCH: u32 = 4;
pub(crate) const MCGO_STATUS_REGISTRY: u32 = 5;
pub(crate) const MCGO_STATUS_EMISSION: u32 = 6;
pub(crate) const MCGO_STATUS_OUTPUT_OVERFLOW: u32 = 7;
pub(crate) const MCGO_STATUS_QUEUE_OVERFLOW: u32 = 8;
pub(crate) const MCGO_STATUS_PANIC: u32 = 9;

const SCRATCH_PADDING: usize =
    (align_of::<u32>() - LIGHT_VOLUME % align_of::<u32>()) % align_of::<u32>();
const SCRATCH_BYTES: usize = LIGHT_VOLUME + SCRATCH_PADDING + LIGHT_VOLUME * 4;
const OUTPUT_CAPACITY: usize = 6 * 4096;

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

fn catch_and_publish(
    output_len: &mut usize,
    operation: impl FnOnce() -> Result<usize, u32>,
) -> u32 {
    *output_len = 0;
    match std::panic::catch_unwind(std::panic::AssertUnwindSafe(operation)) {
        Ok(Ok(count)) => {
            *output_len = count;
            MCGO_STATUS_OK
        }
        Ok(Err(status)) => status,
        Err(_) => MCGO_STATUS_PANIC,
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
pub extern "C" fn mcgo_engine_abi_version() -> u32 {
    ABI_VERSION
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn mcgo_mesh_section(
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
        return MCGO_STATUS_INVALID_ARGUMENT;
    }
    // SAFETY: 指针已检查非空且满足 usize 对齐，调用期间独占写入一个值。
    unsafe { output_len.write(0) };
    if abi_version != ABI_VERSION {
        return MCGO_STATUS_ABI_VERSION;
    }
    if input.is_null() || scratch.is_null() || output.is_null() {
        return MCGO_STATUS_INVALID_ARGUMENT;
    }
    if !input_range_is_valid(input, input_len) {
        return MCGO_STATUS_INPUT;
    }
    if !scratch_range_is_valid(scratch, scratch_len) {
        return MCGO_STATUS_SCRATCH;
    }
    if output_capacity < OUTPUT_CAPACITY {
        return MCGO_STATUS_OUTPUT_OVERFLOW;
    }
    if !(output as usize).is_multiple_of(align_of::<u64>())
        || !output_range_is_valid(output, output_capacity)
    {
        return MCGO_STATUS_INVALID_ARGUMENT;
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
        return MCGO_STATUS_SCRATCH;
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
        return MCGO_STATUS_INVALID_ARGUMENT;
    }

    // SAFETY: output_len 已验证非空、对齐、地址范围和与其他 buffer 不重叠。
    let published = unsafe { &mut *output_len };
    catch_and_publish(published, || {
        // SAFETY: input 非空，范围不超过 isize::MAX 且地址加法不回绕；调用方声明其可读。
        let bytes = unsafe { std::slice::from_raw_parts(input, input_len) };
        let input = MeshInput::parse(bytes).map_err(|error| match error {
            InputError::Input => MCGO_STATUS_INPUT,
            InputError::Registry => MCGO_STATUS_REGISTRY,
            InputError::Emission => MCGO_STATUS_EMISSION,
        })?;
        if center_is_air(&input) {
            return Ok(0);
        }
        // SAFETY: scratch 在进入 catch_unwind 前已通过对齐、长度和地址范围检查。
        let mut scratch = unsafe { light_scratch_from_raw(scratch) };
        build_light(&input, &input.registry, &mut scratch).map_err(|error| match error {
            LightError::EmissionOutOfRange => MCGO_STATUS_EMISSION,
            LightError::QueueOverflow => MCGO_STATUS_QUEUE_OVERFLOW,
        })?;
        // SAFETY: output 非空、对齐、范围有效，且不与 input、scratch 或 output_len 重叠。
        let output = unsafe { std::slice::from_raw_parts_mut(output, output_capacity) };
        mesh_section(&input, &scratch, output).map_err(|error| match error {
            GreedyError::OutputOverflow => MCGO_STATUS_OUTPUT_OVERFLOW,
        })
    })
}

#[cfg(test)]
mod mesh_tests {
    use super::*;

    #[test]
    fn exported_version_is_one() {
        assert_eq!(mcgo_engine_abi_version(), 1);
    }
}
#[cfg(test)]
mod tests {
    use std::mem::{align_of, size_of};

    use super::{
        MCGO_STATUS_ABI_VERSION, MCGO_STATUS_EMISSION, MCGO_STATUS_INPUT,
        MCGO_STATUS_INVALID_ARGUMENT, MCGO_STATUS_OK, MCGO_STATUS_OUTPUT_OVERFLOW,
        MCGO_STATUS_PANIC, MCGO_STATUS_QUEUE_OVERFLOW, MCGO_STATUS_REGISTRY, MCGO_STATUS_SCRATCH,
        SCRATCH_BYTES, catch_and_publish, input_range_is_valid, mcgo_mesh_section,
        output_range_is_valid, scratch_range_is_valid,
    };
    use crate::input::tests::valid_input;

    #[test]
    fn caught_panic_keeps_output_count_zero() {
        let mut output_len = usize::MAX;
        let status = catch_and_publish(&mut output_len, || -> Result<usize, u32> {
            panic!("测试 panic")
        });

        assert_eq!(status, MCGO_STATUS_PANIC);
        assert_eq!(output_len, 0);
    }

    #[test]
    fn valid_input_returns_ok_and_zero_quads() {
        let input = valid_input();
        let mut scratch = vec![0_u32; (48 * 48 * 48 * 5) / 4];
        let mut output = vec![0_u64; 6 * 4096];
        let mut output_len = usize::MAX;
        let status = unsafe {
            mcgo_mesh_section(
                1,
                input.as_ptr(),
                input.len(),
                scratch.as_mut_ptr().cast(),
                scratch.len() * 4,
                output.as_mut_ptr(),
                output.len(),
                &mut output_len,
            )
        };
        assert_eq!(status, MCGO_STATUS_OK);
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
            mcgo_mesh_section(
                1,
                input.as_ptr(),
                input.len(),
                scratch.as_mut_ptr().cast(),
                scratch.len() * 4,
                output.as_mut_ptr(),
                output.len(),
                &mut output_len,
            )
        };

        assert_eq!(status, MCGO_STATUS_OK);
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
            mcgo_mesh_section(
                1,
                input.as_ptr(),
                input.len(),
                scratch.as_mut_ptr().cast(),
                scratch.len() * 4,
                output.as_mut_ptr(),
                output.len(),
                &mut output_len,
            )
        };

        assert_eq!(status, MCGO_STATUS_OK);
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
            mcgo_mesh_section(
                1,
                input.as_ptr(),
                isize::MAX as usize + 1,
                scratch.as_mut_ptr().cast(),
                scratch.len() * 4,
                output.as_mut_ptr(),
                output.len(),
                &mut output_len,
            )
        };
        assert_eq!(status, MCGO_STATUS_INPUT);
        assert_eq!(output_len, 0);
    }

    #[test]
    fn wrapping_input_range_returns_input_atomically() {
        let mut scratch = vec![0_u32; (48 * 48 * 48 * 5) / 4];
        let mut output = vec![0_u64; 6 * 4096];
        let mut output_len = usize::MAX;
        // SAFETY: 被测入口在构造 slice 前拒绝地址加一发生回绕的范围。
        let status = unsafe {
            mcgo_mesh_section(
                1,
                std::ptr::without_provenance(usize::MAX),
                1,
                scratch.as_mut_ptr().cast(),
                scratch.len() * 4,
                output.as_mut_ptr(),
                output.len(),
                &mut output_len,
            )
        };
        assert_eq!(status, MCGO_STATUS_INPUT);
        assert_eq!(output_len, 0);
    }

    #[test]
    fn status_numbers_match_the_c_abi() {
        assert_eq!(
            [
                MCGO_STATUS_OK,
                MCGO_STATUS_ABI_VERSION,
                MCGO_STATUS_INVALID_ARGUMENT,
                MCGO_STATUS_INPUT,
                MCGO_STATUS_SCRATCH,
                MCGO_STATUS_REGISTRY,
                MCGO_STATUS_EMISSION,
                MCGO_STATUS_OUTPUT_OVERFLOW,
                MCGO_STATUS_QUEUE_OVERFLOW,
                MCGO_STATUS_PANIC,
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
            mcgo_mesh_section(
                2,
                input.as_ptr(),
                input.len(),
                scratch.as_mut_ptr().cast(),
                scratch.len() * 4,
                output.as_mut_ptr(),
                output.len(),
                &mut output_len,
            )
        };
        assert_eq!(status, MCGO_STATUS_ABI_VERSION);
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
                MCGO_STATUS_INPUT,
            ),
            (
                "long input",
                {
                    let mut long = input.clone();
                    long.push(0);
                    long
                },
                MCGO_STATUS_INPUT,
            ),
            (
                "malformed registry",
                {
                    let mut malformed = input.clone();
                    malformed[REGISTRY_OFFSET + 16..REGISTRY_OFFSET + 18]
                        .copy_from_slice(&0_u16.to_le_bytes());
                    malformed
                },
                MCGO_STATUS_REGISTRY,
            ),
            (
                "overbright emission",
                {
                    let mut overbright = input.clone();
                    overbright[REGISTRY_OFFSET + 2 * 16 + 3] = 16;
                    overbright
                },
                MCGO_STATUS_EMISSION,
            ),
        ];
        for (name, case, want) in cases.drain(..) {
            let mut output_len = usize::MAX;
            // SAFETY: 本测试提供有效对齐的独占缓冲区，长度均与切片一致。
            let status = unsafe {
                mcgo_mesh_section(
                    1,
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
            mcgo_mesh_section(
                1,
                std::ptr::null(),
                input.len(),
                scratch.as_mut_ptr().cast(),
                scratch.len() * 4,
                output.as_mut_ptr(),
                output.len(),
                &mut output_len,
            )
        };
        assert_eq!(null_input, MCGO_STATUS_INVALID_ARGUMENT);
        assert_eq!(output_len, 0);

        output_len = usize::MAX;
        // SAFETY: scratch 指针有效但长度被刻意缩短一个字节。
        let short_scratch = unsafe {
            mcgo_mesh_section(
                1,
                input.as_ptr(),
                input.len(),
                scratch.as_mut_ptr().cast(),
                scratch.len() * 4 - 1,
                output.as_mut_ptr(),
                output.len(),
                &mut output_len,
            )
        };
        assert_eq!(short_scratch, MCGO_STATUS_SCRATCH);
        assert_eq!(output_len, 0);

        output_len = usize::MAX;
        // SAFETY: output 指针有效且对齐，capacity 被刻意缩短一个元素。
        let short_output = unsafe {
            mcgo_mesh_section(
                1,
                input.as_ptr(),
                input.len(),
                scratch.as_mut_ptr().cast(),
                scratch.len() * 4,
                output.as_mut_ptr(),
                output.len() - 1,
                &mut output_len,
            )
        };
        assert_eq!(short_output, MCGO_STATUS_OUTPUT_OVERFLOW);
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
            mcgo_mesh_section(
                1,
                input.as_ptr(),
                input.len(),
                scratch.as_mut_ptr().cast(),
                48 * 48 * 48 * 5,
                output.as_mut_ptr(),
                6 * 4096,
                std::ptr::null_mut(),
            )
        };
        assert_eq!(null_output_len, MCGO_STATUS_INVALID_ARGUMENT);

        // SAFETY: 除被测的空 scratch 指针外，其余指针均有效且对齐。
        let null_scratch = unsafe {
            mcgo_mesh_section(
                1,
                input.as_ptr(),
                input.len(),
                std::ptr::null_mut(),
                48 * 48 * 48 * 5,
                output.as_mut_ptr(),
                6 * 4096,
                &mut output_len,
            )
        };
        assert_eq!(null_scratch, MCGO_STATUS_INVALID_ARGUMENT);
        assert_eq!(output_len, 0);

        output_len = usize::MAX;
        // SAFETY: 除被测的空 output 指针外，其余指针均有效且对齐。
        let null_output = unsafe {
            mcgo_mesh_section(
                1,
                input.as_ptr(),
                input.len(),
                scratch.as_mut_ptr().cast(),
                48 * 48 * 48 * 5,
                std::ptr::null_mut(),
                6 * 4096,
                &mut output_len,
            )
        };
        assert_eq!(null_output, MCGO_STATUS_INVALID_ARGUMENT);
        assert_eq!(output_len, 0);

        output_len = usize::MAX;
        // SAFETY: scratch 分配足够大；加一字节只用于验证未对齐检查，函数不会解引用。
        let misaligned_scratch = unsafe {
            mcgo_mesh_section(
                1,
                input.as_ptr(),
                input.len(),
                scratch.as_mut_ptr().cast::<u8>().add(1),
                48 * 48 * 48 * 5,
                output.as_mut_ptr(),
                6 * 4096,
                &mut output_len,
            )
        };
        assert_eq!(misaligned_scratch, MCGO_STATUS_SCRATCH);
        assert_eq!(output_len, 0);

        output_len = usize::MAX;
        // SAFETY: scratch 额外分配了一个 u64；加四字节仅用于验证 8-byte 对齐检查。
        let four_byte_aligned_scratch = unsafe {
            mcgo_mesh_section(
                1,
                input.as_ptr(),
                input.len(),
                scratch.as_mut_ptr().cast::<u8>().add(4),
                48 * 48 * 48 * 5,
                output.as_mut_ptr(),
                6 * 4096,
                &mut output_len,
            )
        };
        assert_eq!(four_byte_aligned_scratch, MCGO_STATUS_SCRATCH);
        assert_eq!(output_len, 0);

        output_len = usize::MAX;
        // SAFETY: output 分配足够大；加一字节只用于验证未对齐检查，函数不会解引用。
        let misaligned_output = unsafe {
            mcgo_mesh_section(
                1,
                input.as_ptr(),
                input.len(),
                scratch.as_mut_ptr().cast(),
                48 * 48 * 48 * 5,
                output.as_mut_ptr().cast::<u8>().add(1).cast(),
                6 * 4096,
                &mut output_len,
            )
        };
        assert_eq!(misaligned_output, MCGO_STATUS_INVALID_ARGUMENT);
        assert_eq!(output_len, 0);

        let mut output_len_storage = [usize::MAX, usize::MAX];
        // SAFETY: output_len 分配足够大；加一字节只用于验证未对齐检查，函数不会解引用。
        let misaligned_output_len = unsafe {
            mcgo_mesh_section(
                1,
                input.as_ptr(),
                input.len(),
                scratch.as_mut_ptr().cast(),
                48 * 48 * 48 * 5,
                output.as_mut_ptr(),
                6 * 4096,
                output_len_storage.as_mut_ptr().cast::<u8>().add(1).cast(),
            )
        };
        assert_eq!(misaligned_output_len, MCGO_STATUS_INVALID_ARGUMENT);
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
            mcgo_mesh_section(
                1,
                input.as_ptr(),
                input.len(),
                scratch.as_mut_ptr().cast(),
                SCRATCH_BYTES,
                output.as_mut_ptr(),
                output.len(),
                std::ptr::without_provenance_mut(aligned_max),
            )
        };

        assert_eq!(status, MCGO_STATUS_INVALID_ARGUMENT);
    }

    #[test]
    fn overlapping_scratch_and_output_are_rejected_atomically() {
        let input = valid_input();
        let mut shared = vec![0_u64; (48_usize * 48 * 48 * 5).div_ceil(8)];
        let mut output_len = usize::MAX;

        // SAFETY: 共享 buffer 容量同时满足 scratch 与 output；入口应在创建任何 Rust slice 前拒绝重叠。
        let status = unsafe {
            mcgo_mesh_section(
                1,
                input.as_ptr(),
                input.len(),
                shared.as_mut_ptr().cast(),
                48 * 48 * 48 * 5,
                shared.as_mut_ptr(),
                6 * 4096,
                &mut output_len,
            )
        };

        assert_eq!(status, MCGO_STATUS_SCRATCH);
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
            mcgo_mesh_section(
                1,
                shared.as_ptr().cast(),
                input.len(),
                scratch.as_mut_ptr().cast(),
                SCRATCH_BYTES,
                shared.as_mut_ptr(),
                6 * 4096,
                &mut output_len,
            )
        };

        assert_eq!(status, MCGO_STATUS_INVALID_ARGUMENT);
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
            mcgo_mesh_section(
                1,
                shared_input.as_ptr().cast(),
                input.len(),
                scratch.as_mut_ptr().cast(),
                SCRATCH_BYTES,
                output.as_mut_ptr(),
                output.len(),
                shared_input.as_mut_ptr(),
            )
        };
        assert_eq!(input_status, MCGO_STATUS_INVALID_ARGUMENT);
        assert_eq!(shared_input[0], 0);

        // SAFETY: output_len 刻意指向 output；入口必须在构造 output slice 前拒绝别名。
        let output_status = unsafe {
            mcgo_mesh_section(
                1,
                input.as_ptr(),
                input.len(),
                scratch.as_mut_ptr().cast(),
                SCRATCH_BYTES,
                output.as_mut_ptr(),
                output.len(),
                output.as_mut_ptr().cast(),
            )
        };
        assert_eq!(output_status, MCGO_STATUS_INVALID_ARGUMENT);
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
            mcgo_mesh_section(
                1,
                shared_input_ptr,
                input.len(),
                shared_input_ptr,
                SCRATCH_BYTES,
                output.as_mut_ptr(),
                output.len(),
                &mut output_len,
            )
        };
        assert_eq!(input_status, MCGO_STATUS_SCRATCH);
        assert_eq!(output_len, 0);

        let mut shared_output_len = vec![usize::MAX; SCRATCH_BYTES.div_ceil(size_of::<usize>())];
        let shared_output_len_ptr = shared_output_len.as_mut_ptr();
        // SAFETY: 除被测的 scratch/output_len 重叠外，其余指针与容量都有效；入口先将 output_len 原子清零再拒绝重叠。
        let output_len_status = unsafe {
            mcgo_mesh_section(
                1,
                input.as_ptr(),
                input.len(),
                shared_output_len.as_mut_ptr().cast(),
                SCRATCH_BYTES,
                output.as_mut_ptr(),
                output.len(),
                shared_output_len_ptr,
            )
        };
        assert_eq!(output_len_status, MCGO_STATUS_SCRATCH);
        assert_eq!(shared_output_len[0], 0);
    }
}
