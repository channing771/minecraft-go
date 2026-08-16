//! mornlea_client 的 C ABI 出口。
//!
//! 契约:
//! - ABI version 1;所有入口第一个参数是调用方期望的 ABI 版本,不匹配立即
//!   返回 `MORNLEA_CLIENT_STATUS_ABI_VERSION`。
//! - 窗口句柄存放在 thread-local 表中:句柄只在创建线程有效,跨线程调用
//!   查不到句柄而返回 `MORNLEA_CLIENT_STATUS_WINDOW`——这同时兜住了 winit
//!   macOS 的主线程约束(Go 侧已 `LockOSThread`)。
//! - 任何校验失败都不写调用方缓冲;Rust panic 被 catch_unwind 拦截并转为
//!   `MORNLEA_CLIENT_STATUS_PANIC`,不跨 FFI 边界展开。

use std::cell::RefCell;
use std::collections::HashMap;

use crate::input::SNAPSHOT_BYTES;
use crate::window::ClientWindow;

/// 当前 client ABI 版本。
pub const CLIENT_ABI_VERSION: u32 = 2;

/// 调用成功。
pub const MORNLEA_CLIENT_STATUS_OK: u32 = 0;
/// ABI 版本不匹配。
pub const MORNLEA_CLIENT_STATUS_ABI_VERSION: u32 = 1;
/// 指针/长度/参数非法。
pub const MORNLEA_CLIENT_STATUS_INVALID_ARGUMENT: u32 = 2;
/// 窗口句柄无效、已销毁、跨线程访问或窗口系统操作失败。
pub const MORNLEA_CLIENT_STATUS_WINDOW: u32 = 3;
/// Rust 侧 panic 被拦截。
pub const MORNLEA_CLIENT_STATUS_PANIC: u32 = 4;

thread_local! {
    /// 本线程的活动窗口表;key 是对外句柄。thread-local 使句柄天然绑定
    /// 创建线程,跨线程访问表现为"句柄不存在"。
    static WINDOWS: RefCell<HashMap<u64, ClientWindow>> = RefCell::new(HashMap::new());
    /// 句柄分配计数;0 保留为无效句柄。
    static NEXT_HANDLE: RefCell<u64> = const { RefCell::new(1) };
}

/// 把闭包包进 catch_unwind,panic 一律折叠为 PANIC 状态码。
fn catch(operation: impl FnOnce() -> u32) -> u32 {
    match std::panic::catch_unwind(std::panic::AssertUnwindSafe(operation)) {
        Ok(status) => status,
        Err(_) => MORNLEA_CLIENT_STATUS_PANIC,
    }
}

/// 对句柄指向的窗口执行操作;句柄缺失返回 WINDOW 状态。
fn with_window(handle: u64, operation: impl FnOnce(&mut ClientWindow) -> u32) -> u32 {
    WINDOWS.with(|windows| match windows.borrow_mut().get_mut(&handle) {
        Some(window) => operation(window),
        None => MORNLEA_CLIENT_STATUS_WINDOW,
    })
}

/// 返回当前 client ABI 版本。
#[unsafe(no_mangle)]
pub extern "C" fn mornlea_client_abi_version() -> u32 {
    CLIENT_ABI_VERSION
}

/// 创建窗口并写出句柄。
///
/// `title`/`title_len` 必须是合法 UTF-8;失败时 `out_handle` 不被修改。
#[unsafe(no_mangle)]
pub unsafe extern "C" fn mornlea_client_window_create(
    abi_version: u32,
    width: u32,
    height: u32,
    title: *const u8,
    title_len: usize,
    out_handle: *mut u64,
) -> u32 {
    if abi_version != CLIENT_ABI_VERSION {
        return MORNLEA_CLIENT_STATUS_ABI_VERSION;
    }
    if title.is_null() || out_handle.is_null() || width == 0 || height == 0 {
        return MORNLEA_CLIENT_STATUS_INVALID_ARGUMENT;
    }
    catch(|| {
        // SAFETY: title 非空,调用方保证 title_len 字节可读。
        let bytes = unsafe { std::slice::from_raw_parts(title, title_len) };
        let Ok(title) = std::str::from_utf8(bytes) else {
            return MORNLEA_CLIENT_STATUS_INVALID_ARGUMENT;
        };
        let Ok(window) = ClientWindow::create(width, height, title.to_owned()) else {
            return MORNLEA_CLIENT_STATUS_WINDOW;
        };
        let handle = NEXT_HANDLE.with(|next| {
            let mut next = next.borrow_mut();
            let handle = *next;
            *next += 1;
            handle
        });
        WINDOWS.with(|windows| windows.borrow_mut().insert(handle, window));
        // SAFETY: out_handle 已判非空,只在完整成功后写一次。
        unsafe { out_handle.write(handle) };
        MORNLEA_CLIENT_STATUS_OK
    })
}

/// 销毁窗口;重复销毁返回 WINDOW 状态。
#[unsafe(no_mangle)]
pub extern "C" fn mornlea_client_window_destroy(abi_version: u32, handle: u64) -> u32 {
    if abi_version != CLIENT_ABI_VERSION {
        return MORNLEA_CLIENT_STATUS_ABI_VERSION;
    }
    catch(|| {
        WINDOWS.with(|windows| match windows.borrow_mut().remove(&handle) {
            Some(window) => {
                drop(window);
                MORNLEA_CLIENT_STATUS_OK
            }
            None => MORNLEA_CLIENT_STATUS_WINDOW,
        })
    })
}

/// 每帧一次:泵事件并写出输入快照;`out_len` 必须恰为快照字节数,
/// 校验失败不触碰输出缓冲。
#[unsafe(no_mangle)]
pub unsafe extern "C" fn mornlea_client_window_poll(
    abi_version: u32,
    handle: u64,
    out: *mut u8,
    out_len: usize,
) -> u32 {
    if abi_version != CLIENT_ABI_VERSION {
        return MORNLEA_CLIENT_STATUS_ABI_VERSION;
    }
    if out.is_null() || out_len != SNAPSHOT_BYTES {
        return MORNLEA_CLIENT_STATUS_INVALID_ARGUMENT;
    }
    catch(|| {
        with_window(handle, |window| {
            // SAFETY: out 非空且长度已校验为 SNAPSHOT_BYTES,调用方保证可写。
            let out = unsafe { std::slice::from_raw_parts_mut(out, out_len) };
            window.poll(out);
            MORNLEA_CLIENT_STATUS_OK
        })
    })
}

/// 切换光标捕获;`captured` 只接受 0/1。
#[unsafe(no_mangle)]
pub extern "C" fn mornlea_client_window_set_cursor_captured(
    abi_version: u32,
    handle: u64,
    captured: u8,
) -> u32 {
    if abi_version != CLIENT_ABI_VERSION {
        return MORNLEA_CLIENT_STATUS_ABI_VERSION;
    }
    if captured > 1 {
        return MORNLEA_CLIENT_STATUS_INVALID_ARGUMENT;
    }
    catch(|| {
        with_window(handle, |window| {
            window.set_cursor_captured(captured == 1);
            MORNLEA_CLIENT_STATUS_OK
        })
    })
}

/// 请求修改 content 尺寸(逻辑点)。
#[unsafe(no_mangle)]
pub extern "C" fn mornlea_client_window_set_content_size(
    abi_version: u32,
    handle: u64,
    width: u32,
    height: u32,
) -> u32 {
    if abi_version != CLIENT_ABI_VERSION {
        return MORNLEA_CLIENT_STATUS_ABI_VERSION;
    }
    if width == 0 || height == 0 {
        return MORNLEA_CLIENT_STATUS_INVALID_ARGUMENT;
    }
    catch(|| {
        with_window(handle, |window| {
            window.set_content_size(width, height);
            MORNLEA_CLIENT_STATUS_OK
        })
    })
}

/// 设置窗口置顶;`floating` 只接受 0/1。
#[unsafe(no_mangle)]
pub extern "C" fn mornlea_client_window_set_floating(
    abi_version: u32,
    handle: u64,
    floating: u8,
) -> u32 {
    if abi_version != CLIENT_ABI_VERSION {
        return MORNLEA_CLIENT_STATUS_ABI_VERSION;
    }
    if floating > 1 {
        return MORNLEA_CLIENT_STATUS_INVALID_ARGUMENT;
    }
    catch(|| {
        with_window(handle, |window| {
            window.set_floating(floating == 1);
            MORNLEA_CLIENT_STATUS_OK
        })
    })
}

/// 请求聚焦窗口。
#[unsafe(no_mangle)]
pub extern "C" fn mornlea_client_window_focus(abi_version: u32, handle: u64) -> u32 {
    if abi_version != CLIENT_ABI_VERSION {
        return MORNLEA_CLIENT_STATUS_ABI_VERSION;
    }
    catch(|| {
        with_window(handle, |window| {
            window.focus();
            MORNLEA_CLIENT_STATUS_OK
        })
    })
}

/// 撤销关闭请求。
#[unsafe(no_mangle)]
pub extern "C" fn mornlea_client_window_cancel_close(abi_version: u32, handle: u64) -> u32 {
    if abi_version != CLIENT_ABI_VERSION {
        return MORNLEA_CLIENT_STATUS_ABI_VERSION;
    }
    catch(|| {
        with_window(handle, |window| {
            window.cancel_close();
            MORNLEA_CLIENT_STATUS_OK
        })
    })
}

/// 写出 NSWindow 指针供 gfx 创建 Metal surface;失败不触碰输出。
#[unsafe(no_mangle)]
pub unsafe extern "C" fn mornlea_client_window_ns_window(
    abi_version: u32,
    handle: u64,
    out_ns_window: *mut usize,
) -> u32 {
    if abi_version != CLIENT_ABI_VERSION {
        return MORNLEA_CLIENT_STATUS_ABI_VERSION;
    }
    if out_ns_window.is_null() {
        return MORNLEA_CLIENT_STATUS_INVALID_ARGUMENT;
    }
    catch(|| {
        with_window(handle, |window| match window.ns_window() {
            Some(pointer) => {
                // SAFETY: out_ns_window 已判非空,只在完整成功后写一次。
                unsafe { out_ns_window.write(pointer) };
                MORNLEA_CLIENT_STATUS_OK
            }
            None => MORNLEA_CLIENT_STATUS_WINDOW,
        })
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    // 真实窗口不进自动测试(仓库纪律);这里只验证不依赖窗口系统的
    // 校验拒绝路径:ABI 版本、参数校验与无效句柄。

    #[test]
    fn abi_version_is_two() {
        assert_eq!(mornlea_client_abi_version(), 2);
    }

    #[test]
    fn wrong_abi_version_is_rejected_everywhere() {
        let mut out_handle = 0u64;
        // SAFETY: 指针来自有效局部变量。
        let create = unsafe {
            mornlea_client_window_create(
                CLIENT_ABI_VERSION + 1,
                100,
                100,
                b"t".as_ptr(),
                1,
                &mut out_handle,
            )
        };
        assert_eq!(create, MORNLEA_CLIENT_STATUS_ABI_VERSION);
        assert_eq!(out_handle, 0);
        assert_eq!(
            mornlea_client_window_destroy(CLIENT_ABI_VERSION + 1, 1),
            MORNLEA_CLIENT_STATUS_ABI_VERSION
        );
        let mut snapshot = [0u8; SNAPSHOT_BYTES];
        // SAFETY: 同上。
        let poll = unsafe {
            mornlea_client_window_poll(
                CLIENT_ABI_VERSION + 1,
                1,
                snapshot.as_mut_ptr(),
                snapshot.len(),
            )
        };
        assert_eq!(poll, MORNLEA_CLIENT_STATUS_ABI_VERSION);
    }

    #[test]
    fn invalid_arguments_are_rejected_without_writes() {
        let mut out_handle = 42u64;
        // SAFETY: 除被测的空 title 外其余指针有效。
        let create = unsafe {
            mornlea_client_window_create(
                CLIENT_ABI_VERSION,
                100,
                100,
                std::ptr::null(),
                0,
                &mut out_handle,
            )
        };
        assert_eq!(create, MORNLEA_CLIENT_STATUS_INVALID_ARGUMENT);
        assert_eq!(out_handle, 42, "失败调用不得写 out_handle");

        let mut snapshot = [0xAAu8; SNAPSHOT_BYTES];
        // SAFETY: 长度刻意错一字节,入口必须在写入前拒绝。
        let poll = unsafe {
            mornlea_client_window_poll(
                CLIENT_ABI_VERSION,
                1,
                snapshot.as_mut_ptr(),
                snapshot.len() - 1,
            )
        };
        assert_eq!(poll, MORNLEA_CLIENT_STATUS_INVALID_ARGUMENT);
        assert!(
            snapshot.iter().all(|&b| b == 0xAA),
            "失败调用不得写快照缓冲"
        );

        assert_eq!(
            mornlea_client_window_set_cursor_captured(CLIENT_ABI_VERSION, 1, 2),
            MORNLEA_CLIENT_STATUS_INVALID_ARGUMENT
        );
        assert_eq!(
            mornlea_client_window_set_content_size(CLIENT_ABI_VERSION, 1, 0, 100),
            MORNLEA_CLIENT_STATUS_INVALID_ARGUMENT
        );
    }

    #[test]
    fn unknown_handle_is_rejected_as_window_error() {
        let mut snapshot = [0u8; SNAPSHOT_BYTES];
        // SAFETY: 指针有效,句柄在本线程表中不存在。
        let poll = unsafe {
            mornlea_client_window_poll(
                CLIENT_ABI_VERSION,
                0xDEAD,
                snapshot.as_mut_ptr(),
                snapshot.len(),
            )
        };
        assert_eq!(poll, MORNLEA_CLIENT_STATUS_WINDOW);
        assert_eq!(
            mornlea_client_window_destroy(CLIENT_ABI_VERSION, 0xDEAD),
            MORNLEA_CLIENT_STATUS_WINDOW
        );
        assert_eq!(
            mornlea_client_window_focus(CLIENT_ABI_VERSION, 0xDEAD),
            MORNLEA_CLIENT_STATUS_WINDOW
        );
        let mut ns_window = 7usize;
        // SAFETY: 同上。
        let status =
            unsafe { mornlea_client_window_ns_window(CLIENT_ABI_VERSION, 0xDEAD, &mut ns_window) };
        assert_eq!(status, MORNLEA_CLIENT_STATUS_WINDOW);
        assert_eq!(ns_window, 7, "失败调用不得写 ns_window 输出");
    }
}

// ---- render 入口族(client ABI v2)----

use crate::render::{FrameInput, OffscreenRenderer, RenderCreateError};

/// 本机无可用 GPU 适配器;调用方(测试)应据此跳过而非失败。
pub const MORNLEA_CLIENT_STATUS_ADAPTER: u32 = 5;
/// 渲染资源容量不足(face 池或 origin 槽位耗尽)。
pub const MORNLEA_CLIENT_STATUS_CAPACITY: u32 = 6;

/// render_frame 输入的固定头部字节数;其后是 visible_count×12 的 section 列表。
const FRAME_HEADER_BYTES: usize = 192;

thread_local! {
    /// 本线程的活动渲染器表;与窗口表同构,句柄天然绑定创建线程。
    static RENDERERS: RefCell<HashMap<u64, OffscreenRenderer>> = RefCell::new(HashMap::new());
}

fn with_renderer(handle: u64, operation: impl FnOnce(&mut OffscreenRenderer) -> u32) -> u32 {
    RENDERERS.with(|renderers| match renderers.borrow_mut().get_mut(&handle) {
        Some(renderer) => operation(renderer),
        None => MORNLEA_CLIENT_STATUS_WINDOW,
    })
}

/// 创建离屏渲染器并写出句柄;无 GPU 适配器返回 ADAPTER 状态。
#[unsafe(no_mangle)]
pub unsafe extern "C" fn mornlea_client_render_create(
    abi_version: u32,
    width: u32,
    height: u32,
    out_handle: *mut u64,
) -> u32 {
    if abi_version != CLIENT_ABI_VERSION {
        return MORNLEA_CLIENT_STATUS_ABI_VERSION;
    }
    if out_handle.is_null() || width == 0 || height == 0 {
        return MORNLEA_CLIENT_STATUS_INVALID_ARGUMENT;
    }
    catch(|| {
        let renderer = match OffscreenRenderer::new(width, height) {
            Ok(renderer) => renderer,
            Err(RenderCreateError::Adapter) => return MORNLEA_CLIENT_STATUS_ADAPTER,
            Err(RenderCreateError::Device) => return MORNLEA_CLIENT_STATUS_WINDOW,
        };
        let handle = NEXT_HANDLE.with(|next| {
            let mut next = next.borrow_mut();
            let handle = *next;
            *next += 1;
            handle
        });
        RENDERERS.with(|renderers| renderers.borrow_mut().insert(handle, renderer));
        // SAFETY: out_handle 已判非空,只在完整成功后写一次。
        unsafe { out_handle.write(handle) };
        MORNLEA_CLIENT_STATUS_OK
    })
}

/// 销毁渲染器;重复销毁返回 WINDOW 状态。
#[unsafe(no_mangle)]
pub extern "C" fn mornlea_client_render_destroy(abi_version: u32, handle: u64) -> u32 {
    if abi_version != CLIENT_ABI_VERSION {
        return MORNLEA_CLIENT_STATUS_ABI_VERSION;
    }
    catch(|| {
        RENDERERS.with(|renderers| match renderers.borrow_mut().remove(&handle) {
            Some(renderer) => {
                drop(renderer);
                MORNLEA_CLIENT_STATUS_OK
            }
            None => MORNLEA_CLIENT_STATUS_WINDOW,
        })
    })
}

/// 上传材质 atlas(逐 layer、逐 mip 的 RGBA 字节,长度必须精确匹配)。
#[unsafe(no_mangle)]
pub unsafe extern "C" fn mornlea_client_render_upload_atlas(
    abi_version: u32,
    handle: u64,
    layers: u32,
    pixels: *const u8,
    pixels_len: usize,
) -> u32 {
    if abi_version != CLIENT_ABI_VERSION {
        return MORNLEA_CLIENT_STATUS_ABI_VERSION;
    }
    if pixels.is_null() || layers == 0 {
        return MORNLEA_CLIENT_STATUS_INVALID_ARGUMENT;
    }
    catch(|| {
        with_renderer(handle, |renderer| {
            // SAFETY: pixels 非空,调用方保证 pixels_len 字节可读。
            let data = unsafe { std::slice::from_raw_parts(pixels, pixels_len) };
            if renderer.upload_atlas(layers, data) {
                MORNLEA_CLIENT_STATUS_OK
            } else {
                MORNLEA_CLIENT_STATUS_INVALID_ARGUMENT
            }
        })
    })
}

/// 上传/替换一个 section 的 packed face 字节(8 的倍数;空等价 drop)。
#[unsafe(no_mangle)]
pub unsafe extern "C" fn mornlea_client_render_upload_section(
    abi_version: u32,
    handle: u64,
    section_x: i32,
    section_y: i32,
    section_z: i32,
    quads: *const u8,
    quads_len: usize,
) -> u32 {
    if abi_version != CLIENT_ABI_VERSION {
        return MORNLEA_CLIENT_STATUS_ABI_VERSION;
    }
    if !quads_len.is_multiple_of(8) || (quads.is_null() && quads_len != 0) {
        return MORNLEA_CLIENT_STATUS_INVALID_ARGUMENT;
    }
    catch(|| {
        with_renderer(handle, |renderer| {
            let data = if quads_len == 0 {
                &[][..]
            } else {
                // SAFETY: quads 非空,调用方保证 quads_len 字节可读。
                unsafe { std::slice::from_raw_parts(quads, quads_len) }
            };
            if renderer.upload_section((section_x, section_y, section_z), data) {
                MORNLEA_CLIENT_STATUS_OK
            } else {
                MORNLEA_CLIENT_STATUS_CAPACITY
            }
        })
    })
}

/// 丢弃一个 section(幂等)。
#[unsafe(no_mangle)]
pub extern "C" fn mornlea_client_render_drop_section(
    abi_version: u32,
    handle: u64,
    section_x: i32,
    section_y: i32,
    section_z: i32,
) -> u32 {
    if abi_version != CLIENT_ABI_VERSION {
        return MORNLEA_CLIENT_STATUS_ABI_VERSION;
    }
    catch(|| {
        with_renderer(handle, |renderer| {
            renderer.drop_section((section_x, section_y, section_z));
            MORNLEA_CLIENT_STATUS_OK
        })
    })
}

/// 解析 render_frame 输入;违约返回 None。
fn parse_frame(bytes: &[u8]) -> Option<FrameInput> {
    if bytes.len() < FRAME_HEADER_BYTES {
        return None;
    }
    let read_f32 =
        |offset: usize| f32::from_le_bytes(bytes[offset..offset + 4].try_into().unwrap());
    let read_u32 =
        |offset: usize| u32::from_le_bytes(bytes[offset..offset + 4].try_into().unwrap());
    let mut view_proj = [0f32; 16];
    let mut view_proj_inv = [0f32; 16];
    for i in 0..16 {
        view_proj[i] = read_f32(i * 4);
        view_proj_inv[i] = read_f32(64 + i * 4);
    }
    let visible_count = read_u32(184) as usize;
    if read_u32(188) != 0
        || bytes.len() != FRAME_HEADER_BYTES + visible_count * 12
        || visible_count > 128 * 1024
    {
        return None;
    }
    let mut visible = Vec::with_capacity(visible_count);
    for index in 0..visible_count {
        let offset = FRAME_HEADER_BYTES + index * 12;
        let read_i32 = |o: usize| i32::from_le_bytes(bytes[o..o + 4].try_into().unwrap());
        visible.push((read_i32(offset), read_i32(offset + 4), read_i32(offset + 8)));
    }
    Some(FrameInput {
        view_proj,
        view_proj_inv,
        pos: [read_f32(128), read_f32(132), read_f32(136)],
        daylight: read_f32(140),
        sun_direction: [read_f32(144), read_f32(148), read_f32(152)],
        star_visibility: read_f32(156),
        sky_color: [read_f32(160), read_f32(164), read_f32(168), read_f32(172)],
        cloud_macro_x: read_u32(176),
        cloud_local: read_f32(180),
        visible,
    })
}

/// 渲染一帧(每帧一次;帧输入为固定头 + 可见 section 列表)。
#[unsafe(no_mangle)]
pub unsafe extern "C" fn mornlea_client_render_frame(
    abi_version: u32,
    handle: u64,
    frame: *const u8,
    frame_len: usize,
) -> u32 {
    if abi_version != CLIENT_ABI_VERSION {
        return MORNLEA_CLIENT_STATUS_ABI_VERSION;
    }
    if frame.is_null() {
        return MORNLEA_CLIENT_STATUS_INVALID_ARGUMENT;
    }
    catch(|| {
        // SAFETY: frame 非空,调用方保证 frame_len 字节可读。
        let bytes = unsafe { std::slice::from_raw_parts(frame, frame_len) };
        let Some(input) = parse_frame(bytes) else {
            return MORNLEA_CLIENT_STATUS_INVALID_ARGUMENT;
        };
        with_renderer(handle, |renderer| {
            renderer.render_frame(&input);
            MORNLEA_CLIENT_STATUS_OK
        })
    })
}

/// 阻塞回读离屏 BGRA 图像;`out_len` 必须恰为 width×height×4。
#[unsafe(no_mangle)]
pub unsafe extern "C" fn mornlea_client_render_readback(
    abi_version: u32,
    handle: u64,
    out: *mut u8,
    out_len: usize,
) -> u32 {
    if abi_version != CLIENT_ABI_VERSION {
        return MORNLEA_CLIENT_STATUS_ABI_VERSION;
    }
    if out.is_null() {
        return MORNLEA_CLIENT_STATUS_INVALID_ARGUMENT;
    }
    catch(|| {
        with_renderer(handle, |renderer| {
            if out_len != renderer.output_bytes() {
                return MORNLEA_CLIENT_STATUS_INVALID_ARGUMENT;
            }
            // SAFETY: out 非空且长度已校验,调用方保证可写。
            let out = unsafe { std::slice::from_raw_parts_mut(out, out_len) };
            renderer.readback(out);
            MORNLEA_CLIENT_STATUS_OK
        })
    })
}

#[cfg(test)]
mod render_ffi_tests {
    use super::*;

    #[test]
    fn render_entries_reject_bad_abi_and_arguments() {
        let mut handle = 7u64;
        // SAFETY: 指针来自有效局部变量。
        let create =
            unsafe { mornlea_client_render_create(CLIENT_ABI_VERSION + 1, 64, 64, &mut handle) };
        assert_eq!(create, MORNLEA_CLIENT_STATUS_ABI_VERSION);
        assert_eq!(handle, 7, "失败调用不得写句柄");
        // SAFETY: 同上;宽为零必须拒绝。
        let zero = unsafe { mornlea_client_render_create(CLIENT_ABI_VERSION, 0, 64, &mut handle) };
        assert_eq!(zero, MORNLEA_CLIENT_STATUS_INVALID_ARGUMENT);

        // quads 长度非 8 的倍数必须拒绝。
        let quads = [0u8; 9];
        // SAFETY: 同上。
        let misaligned = unsafe {
            mornlea_client_render_upload_section(
                CLIENT_ABI_VERSION,
                0xBEEF,
                0,
                0,
                0,
                quads.as_ptr(),
                quads.len(),
            )
        };
        assert_eq!(misaligned, MORNLEA_CLIENT_STATUS_INVALID_ARGUMENT);

        // 未知句柄一律 WINDOW。
        assert_eq!(
            mornlea_client_render_destroy(CLIENT_ABI_VERSION, 0xBEEF),
            MORNLEA_CLIENT_STATUS_WINDOW
        );
        let frame = [0u8; FRAME_HEADER_BYTES];
        // SAFETY: 同上。
        let status = unsafe {
            mornlea_client_render_frame(CLIENT_ABI_VERSION, 0xBEEF, frame.as_ptr(), frame.len())
        };
        assert_eq!(status, MORNLEA_CLIENT_STATUS_WINDOW);
    }

    #[test]
    fn render_roundtrip_or_skip_without_adapter() {
        let mut handle = 0u64;
        // SAFETY: 指针来自有效局部变量。
        let create =
            unsafe { mornlea_client_render_create(CLIENT_ABI_VERSION, 32, 16, &mut handle) };
        if create == MORNLEA_CLIENT_STATUS_ADAPTER {
            return; // 无 GPU 环境跳过。
        }
        assert_eq!(create, MORNLEA_CLIENT_STATUS_OK);

        let mut frame = [0u8; FRAME_HEADER_BYTES];
        // 恒等矩阵 + daylight=1。
        for i in 0..4 {
            let one = 1.0f32.to_le_bytes();
            frame[i * 16 + i * 4..i * 16 + i * 4 + 4].copy_from_slice(&one);
            frame[64 + i * 16 + i * 4..64 + i * 16 + i * 4 + 4].copy_from_slice(&one);
        }
        // SAFETY: 同上。
        let status = unsafe {
            mornlea_client_render_frame(CLIENT_ABI_VERSION, handle, frame.as_ptr(), frame.len())
        };
        assert_eq!(status, MORNLEA_CLIENT_STATUS_OK);

        let mut out = vec![0u8; 32 * 16 * 4];
        // SAFETY: 同上。
        let readback = unsafe {
            mornlea_client_render_readback(CLIENT_ABI_VERSION, handle, out.as_mut_ptr(), out.len())
        };
        assert_eq!(readback, MORNLEA_CLIENT_STATUS_OK);
        assert!(out.iter().any(|&b| b != 0));

        // 回读缓冲长度不符必须拒绝且不触碰缓冲。
        let mut short = vec![0xAAu8; 32 * 16 * 4 - 1];
        // SAFETY: 同上。
        let bad = unsafe {
            mornlea_client_render_readback(
                CLIENT_ABI_VERSION,
                handle,
                short.as_mut_ptr(),
                short.len(),
            )
        };
        assert_eq!(bad, MORNLEA_CLIENT_STATUS_INVALID_ARGUMENT);
        assert!(short.iter().all(|&b| b == 0xAA));

        assert_eq!(
            mornlea_client_render_destroy(CLIENT_ABI_VERSION, handle),
            MORNLEA_CLIENT_STATUS_OK
        );
        assert_eq!(
            mornlea_client_render_destroy(CLIENT_ABI_VERSION, handle),
            MORNLEA_CLIENT_STATUS_WINDOW
        );
    }
}
