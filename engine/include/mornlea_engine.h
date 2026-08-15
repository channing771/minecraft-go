#ifndef MORNLEA_ENGINE_H
#define MORNLEA_ENGINE_H

#include <stddef.h>
#include <stdint.h>

#define MORNLEA_ENGINE_ABI_VERSION 1u

#define MORNLEA_STATUS_OK 0u
#define MORNLEA_STATUS_ABI_VERSION 1u
#define MORNLEA_STATUS_INVALID_ARGUMENT 2u
#define MORNLEA_STATUS_INPUT 3u
#define MORNLEA_STATUS_SCRATCH 4u
#define MORNLEA_STATUS_REGISTRY 5u
#define MORNLEA_STATUS_EMISSION 6u
#define MORNLEA_STATUS_OUTPUT_OVERFLOW 7u
#define MORNLEA_STATUS_QUEUE_OVERFLOW 8u
#define MORNLEA_STATUS_PANIC 9u

uint32_t mornlea_engine_abi_version(void);

uint32_t mornlea_mesh_section(
    uint32_t abi_version,
    const uint8_t *input,
    size_t input_len,
    uint8_t *scratch,
    size_t scratch_len,
    uint64_t *output,
    size_t output_capacity,
    size_t *output_len);

uint32_t mornlea_collision_resolve(
    uint32_t abi_version,
    const uint8_t *input,
    size_t input_len,
    uint8_t *output,
    size_t output_len);

#endif
