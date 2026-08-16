#ifndef MORNLEA_CLIENT_H
#define MORNLEA_CLIENT_H

#include <stddef.h>
#include <stdint.h>

#define MORNLEA_CLIENT_ABI_VERSION 3u

#define MORNLEA_CLIENT_STATUS_OK 0u
#define MORNLEA_CLIENT_STATUS_ABI_VERSION 1u
#define MORNLEA_CLIENT_STATUS_INVALID_ARGUMENT 2u
#define MORNLEA_CLIENT_STATUS_WINDOW 3u
#define MORNLEA_CLIENT_STATUS_PANIC 4u
#define MORNLEA_CLIENT_STATUS_ADAPTER 5u
#define MORNLEA_CLIENT_STATUS_CAPACITY 6u

/* 输入快照:64 字节头 + 1024 x u32 文本段,布局见 crate input 模块文档。 */
#define MORNLEA_CLIENT_SNAPSHOT_BYTES 4160u

uint32_t mornlea_client_abi_version(void);

uint32_t mornlea_client_window_create(
    uint32_t abi_version,
    uint32_t width,
    uint32_t height,
    const uint8_t *title,
    size_t title_len,
    uint64_t *out_handle);

uint32_t mornlea_client_window_destroy(uint32_t abi_version, uint64_t handle);

uint32_t mornlea_client_window_poll(
    uint32_t abi_version,
    uint64_t handle,
    uint8_t *out,
    size_t out_len);

uint32_t mornlea_client_window_set_cursor_captured(
    uint32_t abi_version,
    uint64_t handle,
    uint8_t captured);

uint32_t mornlea_client_window_set_content_size(
    uint32_t abi_version,
    uint64_t handle,
    uint32_t width,
    uint32_t height);

uint32_t mornlea_client_window_set_floating(
    uint32_t abi_version,
    uint64_t handle,
    uint8_t floating);

uint32_t mornlea_client_window_focus(uint32_t abi_version, uint64_t handle);

uint32_t mornlea_client_window_cancel_close(uint32_t abi_version, uint64_t handle);

uint32_t mornlea_client_window_ns_window(
    uint32_t abi_version,
    uint64_t handle,
    uintptr_t *out_ns_window);

uint32_t mornlea_client_render_create(
    uint32_t abi_version,
    uint32_t width,
    uint32_t height,
    uint64_t *out_handle);

uint32_t mornlea_client_render_destroy(uint32_t abi_version, uint64_t handle);

uint32_t mornlea_client_render_upload_atlas(
    uint32_t abi_version,
    uint64_t handle,
    uint32_t layers,
    const uint8_t *pixels,
    size_t pixels_len);

uint32_t mornlea_client_render_upload_section(
    uint32_t abi_version,
    uint64_t handle,
    int32_t section_x,
    int32_t section_y,
    int32_t section_z,
    const uint8_t *quads,
    size_t quads_len);

uint32_t mornlea_client_render_drop_section(
    uint32_t abi_version,
    uint64_t handle,
    int32_t section_x,
    int32_t section_y,
    int32_t section_z);

uint32_t mornlea_client_render_frame(
    uint32_t abi_version,
    uint64_t handle,
    const uint8_t *frame,
    size_t frame_len);

uint32_t mornlea_client_render_upload_glyph_rect(
    uint32_t abi_version,
    uint64_t handle,
    uint32_t x,
    uint32_t y,
    uint32_t width,
    uint32_t height,
    const uint8_t *pixels,
    size_t pixels_len);

uint32_t mornlea_client_render_upload_hud_atlas(
    uint32_t abi_version,
    uint64_t handle,
    uint32_t width,
    uint32_t height,
    const uint8_t *pixels,
    size_t pixels_len);

uint32_t mornlea_client_render_readback(
    uint32_t abi_version,
    uint64_t handle,
    uint8_t *out,
    size_t out_len);

#endif
