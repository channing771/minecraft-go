#ifndef MORNLEA_ENGINE_H
#define MORNLEA_ENGINE_H

#include <stddef.h>
#include <stdint.h>

/* ABI v6:新增 `mornlea_lod_shell` 远环壳出口(rust-engine-lod-shell 变更),
 * 既有入口签名与语义不变。变基重编说明:该出口在旧基线上原编号 v4,main
 * 合并 fluid 系列已占用 v4(worldgen 注水)与 v5(mesh registry 扩容),故
 * 顺延重编为 v6;engine 与 Go 侧是同一不可跨版本混装的 release unit。
 * ABI v5:mesh `MGM1` 输入的 registry 条目上限由 27 扩到 35,流体的 8 个方块
 * 编号随之进入 registry 快照(见 input.rs 的 MAX_REGISTRY_ENTRIES);同一版本内
 * 单条 registry 条目由 16 字节扩到 18 字节,末尾追加 `fluid_height`(该格孤立时
 * 的 4-bit 流体高度原值,0 为「非流体」哨兵)与 `light_attenuation`(天空光穿过
 * 该方块的额外衰减)两个每方块字节。engine 与 Go 侧是同一不可跨版本混装的
 * release unit。
 * ABI v4:worldgen `MGW1` header 的材料表由 13 项扩到 14 项(末项 water,
 * 占用 v3 的 reserved 槽,header 总长仍为 564 字节)。 */
#define MORNLEA_ENGINE_ABI_VERSION 6u

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

/*
 * mornlea_lod_shell:确定性远环 LOD 壳生成(无状态纯函数)。
 *
 * 输入 580 字节 = 与 mornlea_worldgen_chunk 完全一致的 MGW1 header(564)
 * + tile_x i32(564)+ tile_z i32(568)+ columns u32(572,必须等于 64)
 * + lod_step u32(576,合法值 2/4/8);tile 覆盖 [tile_x*64, tile_x*64+64)
 * × [tile_z*64, tile_z*64+64) 列。
 *
 * 输出为壳 quad 字节流(LE)。单 quad 20 字节:x i32(0)| z i32(4)|
 * y i32(8)| w u16(12)| d u16(14)| face u8(16,取值见 [`LodFace`])|
 * material u16(17)| shade u8(19)。face 共 5 值:顶面 top + 四向侧裙
 * side(0=top、1=neg_x、2=pos_x、3=neg_z、4=pos_z);着色权重:顶面 255、
 * ±Z 204、±X 153。位布局与 engine crate lod.rs 的 encode_shell 定稿一致。
 *
 * 容量语义(两段式探测):output_capacity 不足时返回
 * MORNLEA_STATUS_OUTPUT_OVERFLOW 并把所需字节数写入 *output_len(输出
 * 缓冲不写入任何字节);调用方扩容(≥所需)后重试即成功,成功时
 * *output_len 为实际写入字节数。其余状态语义与既有导出一致:abi_version
 * 不匹配返回 MORNLEA_STATUS_ABI_VERSION;指针、范围或重叠违约返回
 * MORNLEA_STATUS_INVALID_ARGUMENT;输入内容违约(长度、header、列数、
 * 步长、越界 tile)返回 MORNLEA_STATUS_INPUT;Rust panic 收敛为
 * MORNLEA_STATUS_PANIC。除两段式 overflow 报告所需容量外,失败路径
 * *output_len 恒为 0 且输出缓冲原样。
 */
uint32_t mornlea_lod_shell(
    uint32_t abi_version,
    const uint8_t *input,
    size_t input_len,
    uint8_t *output,
    size_t output_capacity,
    size_t *output_len);

#endif
