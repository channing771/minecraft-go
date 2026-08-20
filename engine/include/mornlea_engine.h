#ifndef MORNLEA_ENGINE_H
#define MORNLEA_ENGINE_H

#include <stddef.h>
#include <stdint.h>

/* ABI v5:mesh `MGM1` 输入的 registry 条目上限由 27 扩到 35,流体的 8 个方块
 * 编号随之进入 registry 快照(见 input.rs 的 MAX_REGISTRY_ENTRIES)。engine 与
 * Go 侧是同一不可跨版本混装的 release unit。
 * ABI v4:worldgen `MGW1` header 的材料表由 13 项扩到 14 项(末项 water,
 * 占用 v3 的 reserved 槽,header 总长仍为 564 字节)。 */
#define MORNLEA_ENGINE_ABI_VERSION 5u

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

uint32_t mornlea_raycast_batch(
    uint32_t abi_version,
    const uint8_t *input,
    size_t input_len,
    uint8_t *cursor,
    size_t cursor_len,
    uint8_t *output,
    size_t output_len,
    size_t *output_count,
    uint8_t *done);

uint32_t mornlea_physics_step(
    uint32_t abi_version,
    const uint8_t *input,
    size_t input_len,
    uint8_t *output,
    size_t output_len);

uint32_t mornlea_worldgen_chunk(
    uint32_t abi_version,
    const uint8_t *input,
    size_t input_len,
    uint8_t *output,
    size_t output_len);

uint32_t mornlea_worldgen_probe(
    uint32_t abi_version,
    const uint8_t *input,
    size_t input_len,
    uint8_t *output,
    size_t output_len);

#endif
