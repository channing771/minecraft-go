#ifndef MCGO_ENGINE_H
#define MCGO_ENGINE_H

#include <stddef.h>
#include <stdint.h>

#define MCGO_ENGINE_ABI_VERSION 1u

#define MCGO_STATUS_OK 0u
#define MCGO_STATUS_ABI_VERSION 1u
#define MCGO_STATUS_INVALID_ARGUMENT 2u
#define MCGO_STATUS_INPUT 3u
#define MCGO_STATUS_SCRATCH 4u
#define MCGO_STATUS_REGISTRY 5u
#define MCGO_STATUS_EMISSION 6u
#define MCGO_STATUS_OUTPUT_OVERFLOW 7u
#define MCGO_STATUS_QUEUE_OVERFLOW 8u
#define MCGO_STATUS_PANIC 9u

uint32_t mcgo_engine_abi_version(void);

uint32_t mcgo_mesh_section(
    uint32_t abi_version,
    const uint8_t *input,
    size_t input_len,
    uint8_t *scratch,
    size_t scratch_len,
    uint64_t *output,
    size_t output_capacity,
    size_t *output_len);

#endif
