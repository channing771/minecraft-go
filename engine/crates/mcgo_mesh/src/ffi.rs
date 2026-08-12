use std::mem::align_of;

use crate::input::{InputError, MeshInput};

pub(crate) const ABI_VERSION: u32 = 1;

pub(crate) const MCGO_STATUS_OK: u32 = 0;
pub(crate) const MCGO_STATUS_ABI_VERSION: u32 = 1;
pub(crate) const MCGO_STATUS_INVALID_ARGUMENT: u32 = 2;
pub(crate) const MCGO_STATUS_INPUT: u32 = 3;
pub(crate) const MCGO_STATUS_SCRATCH: u32 = 4;
pub(crate) const MCGO_STATUS_REGISTRY: u32 = 5;
pub(crate) const MCGO_STATUS_EMISSION: u32 = 6;
pub(crate) const MCGO_STATUS_OUTPUT_OVERFLOW: u32 = 7;
#[allow(dead_code)] // Task 4 的有界光照队列开始返回该状态。
pub(crate) const MCGO_STATUS_QUEUE_OVERFLOW: u32 = 8;
pub(crate) const MCGO_STATUS_PANIC: u32 = 9;

const SCRATCH_BYTES: usize = 48 * 48 * 48 * 5;
const OUTPUT_CAPACITY: usize = 6 * 4096;

fn input_range_is_valid(input: *const u8, input_len: usize) -> bool {
    input_len <= isize::MAX as usize && input.addr().checked_add(input_len).is_some()
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
    if output_len.is_null() || !(output_len as usize).is_multiple_of(align_of::<usize>()) {
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
    if scratch_len < SCRATCH_BYTES || !(scratch as usize).is_multiple_of(align_of::<u32>()) {
        return MCGO_STATUS_SCRATCH;
    }
    if output_capacity < OUTPUT_CAPACITY {
        return MCGO_STATUS_OUTPUT_OVERFLOW;
    }
    if !(output as usize).is_multiple_of(align_of::<u64>()) {
        return MCGO_STATUS_INVALID_ARGUMENT;
    }

    std::panic::catch_unwind(|| {
        // SAFETY: input 非空，范围不超过 isize::MAX 且地址加法不回绕；调用方声明其可读。
        let bytes = unsafe { std::slice::from_raw_parts(input, input_len) };
        MeshInput::parse(bytes)
            .map(|_| MCGO_STATUS_OK)
            .unwrap_or_else(|error| match error {
                InputError::Input => MCGO_STATUS_INPUT,
                InputError::Registry => MCGO_STATUS_REGISTRY,
                InputError::Emission => MCGO_STATUS_EMISSION,
            })
    })
    .unwrap_or(MCGO_STATUS_PANIC)
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
    use super::{
        MCGO_STATUS_ABI_VERSION, MCGO_STATUS_EMISSION, MCGO_STATUS_INPUT,
        MCGO_STATUS_INVALID_ARGUMENT, MCGO_STATUS_OK, MCGO_STATUS_OUTPUT_OVERFLOW,
        MCGO_STATUS_PANIC, MCGO_STATUS_QUEUE_OVERFLOW, MCGO_STATUS_REGISTRY, MCGO_STATUS_SCRATCH,
        input_range_is_valid, mcgo_mesh_section,
    };
    use crate::input::tests::valid_input;

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
        let mut scratch = vec![0_u32; (48 * 48 * 48 * 5) / 4 + 1];
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
}
