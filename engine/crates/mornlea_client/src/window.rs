//! winit 窗口封装:事件循环泵、事件到 [`InputState`] 的桥接与低频窗口操作。
//!
//! 控制权模型:Go 主线程每帧调用 [`ClientWindow::poll`],内部以零超时
//! `pump_app_events` 处理完积压事件后立即返回并编码快照;winit 从不拥有
//! 主循环。本模块依赖真实窗口系统,不做单元测试;可无头验证的逻辑全部
//! 位于 [`crate::input`]。

use std::sync::Arc;
use std::time::Duration;

use winit::application::ApplicationHandler;
use winit::dpi::LogicalSize;
use winit::event::{DeviceEvent, DeviceId, ElementState, Ime, MouseButton, WindowEvent};
use winit::event_loop::{ActiveEventLoop, EventLoop};
use winit::keyboard::PhysicalKey;
use winit::platform::pump_events::EventLoopExtPumpEvents;
use winit::window::{CursorGrabMode, Window, WindowId, WindowLevel};

use crate::input::InputState;

/// 窗口创建失败的稳定原因,FFI 层统一转为错误状态码。
#[derive(Debug)]
pub enum CreateError {
    /// 事件循环创建失败(通常是非主线程或重复创建)。
    EventLoop,
    /// 泵完首轮事件后窗口仍未建立。
    Window,
}

/// ApplicationHandler 实现:把 winit 事件写入输入状态机。
struct App {
    window: Option<Arc<Window>>,
    input: InputState,
    title: String,
    width: u32,
    height: u32,
    /// IME 组合是否激活;激活期间按键的 `text` 不直接入队,以 `Ime::Commit`
    /// 为准,避免组合过程中的重复字符。
    ime_active: bool,
}

impl App {
    fn new(width: u32, height: u32, title: String) -> Self {
        Self {
            window: None,
            input: InputState::new(),
            title,
            width,
            height,
            ime_active: false,
        }
    }

    /// 从窗口读取当前物理/逻辑尺寸并写入状态机。
    fn refresh_sizes(&mut self) {
        if let Some(window) = &self.window {
            let physical = window.inner_size();
            let logical: LogicalSize<u32> = physical.to_logical(window.scale_factor());
            self.input.set_sizes(
                physical.width,
                physical.height,
                logical.width,
                logical.height,
            );
        }
    }
}

impl ApplicationHandler for App {
    fn resumed(&mut self, event_loop: &ActiveEventLoop) {
        if self.window.is_some() {
            return;
        }
        let attributes = Window::default_attributes()
            .with_title(self.title.clone())
            .with_inner_size(LogicalSize::new(self.width, self.height));
        match event_loop.create_window(attributes) {
            Ok(window) => {
                // 聊天需要 IME 提交的 Unicode 文本(GLFW char 回调的等价物)。
                window.set_ime_allowed(true);
                // Arc 包装:渲染器 surface 需要共享窗口所有权(wgpu
                // create_surface 的 'static 约束)。
                self.window = Some(Arc::new(window));
                self.refresh_sizes();
            }
            Err(_) => {
                // 创建失败留给 create() 以 window 缺失判定,不在回调里 panic。
            }
        }
    }

    fn window_event(
        &mut self,
        _event_loop: &ActiveEventLoop,
        _window_id: WindowId,
        event: WindowEvent,
    ) {
        match event {
            WindowEvent::CloseRequested => self.input.request_close(),
            WindowEvent::Resized(_) | WindowEvent::ScaleFactorChanged { .. } => {
                self.refresh_sizes();
            }
            WindowEvent::KeyboardInput { event, .. } => {
                if let PhysicalKey::Code(code) = event.physical_key {
                    self.input.key_event(code, event.state.is_pressed());
                }
                // 非 IME 路径的字符输入:与 GLFW char 回调域一致,过滤控制字符。
                if !self.ime_active
                    && event.state.is_pressed()
                    && let Some(text) = event.text.as_ref()
                {
                    for ch in text.chars().filter(|ch| !ch.is_control()) {
                        self.input.push_text(ch);
                    }
                }
            }
            WindowEvent::Ime(ime) => match ime {
                Ime::Enabled => self.ime_active = true,
                Ime::Disabled => self.ime_active = false,
                Ime::Commit(text) => {
                    for ch in text.chars().filter(|ch| !ch.is_control()) {
                        self.input.push_text(ch);
                    }
                }
                Ime::Preedit(..) => {}
            },
            WindowEvent::CursorMoved { position, .. } => {
                if let Some(window) = &self.window {
                    let logical = position.to_logical::<f64>(window.scale_factor());
                    self.input.cursor_moved(logical.x, logical.y);
                }
            }
            WindowEvent::MouseInput { state, button, .. } => {
                let pressed = state == ElementState::Pressed;
                match button {
                    MouseButton::Left => self.input.mouse_button(true, pressed),
                    MouseButton::Right => self.input.mouse_button(false, pressed),
                    _ => {}
                }
            }
            _ => {}
        }
    }

    fn device_event(
        &mut self,
        _event_loop: &ActiveEventLoop,
        _device_id: DeviceId,
        event: DeviceEvent,
    ) {
        if let DeviceEvent::MouseMotion { delta: (dx, dy) } = event {
            self.input.mouse_delta(dx, dy);
        }
    }
}

/// 一个活动窗口:事件循环与状态机的组合,所有方法都必须在创建线程调用
/// (FFI 层以 thread-local 存储保证)。
pub struct ClientWindow {
    event_loop: EventLoop<()>,
    app: App,
}

impl ClientWindow {
    /// 创建窗口:建立事件循环并泵一轮事件以触发 `resumed` 中的窗口创建。
    pub fn create(width: u32, height: u32, title: String) -> Result<Self, CreateError> {
        let mut event_loop = EventLoop::new().map_err(|_| CreateError::EventLoop)?;
        let mut app = App::new(width, height, title);
        event_loop.pump_app_events(Some(Duration::ZERO), &mut app);
        if app.window.is_none() {
            return Err(CreateError::Window);
        }
        Ok(Self { event_loop, app })
    }

    /// 每帧一次:泵完积压事件并把输入快照编码进 `out`
    /// (长度必须为 [`crate::input::SNAPSHOT_BYTES`],由 FFI 层校验)。
    pub fn poll(&mut self, out: &mut [u8]) {
        self.event_loop
            .pump_app_events(Some(Duration::ZERO), &mut self.app);
        self.app.input.encode_snapshot(out);
    }

    /// 切换光标捕获:捕获时隐藏并锁定光标(失败降级 Confined),
    /// 释放时恢复;状态机同步切换虚拟/绝对坐标来源。
    pub fn set_cursor_captured(&mut self, captured: bool) {
        if captured == self.app.input.captured() {
            return;
        }
        if let Some(window) = &self.app.window {
            if captured {
                window.set_cursor_visible(false);
                if window.set_cursor_grab(CursorGrabMode::Locked).is_err() {
                    // macOS 正常支持 Locked;降级 Confined 仍保证不逃出窗口。
                    let _ = window.set_cursor_grab(CursorGrabMode::Confined);
                }
            } else {
                let _ = window.set_cursor_grab(CursorGrabMode::None);
                window.set_cursor_visible(true);
            }
        }
        self.app.input.set_captured(captured);
    }

    /// 请求修改 content 尺寸(逻辑点);实际生效经 Resized 事件回写快照。
    pub fn set_content_size(&mut self, width: u32, height: u32) {
        if let Some(window) = &self.app.window {
            let _ = window.request_inner_size(LogicalSize::new(width, height));
        }
        self.app.refresh_sizes();
    }

    /// 设置窗口置顶(Go `SetFloating` 语义)。
    pub fn set_floating(&mut self, floating: bool) {
        if let Some(window) = &self.app.window {
            let level = if floating {
                WindowLevel::AlwaysOnTop
            } else {
                WindowLevel::Normal
            };
            window.set_window_level(level);
        }
    }

    /// 请求聚焦窗口。
    pub fn focus(&mut self) {
        if let Some(window) = &self.app.window {
            window.focus_window();
        }
    }

    /// 撤销关闭请求。
    pub fn cancel_close(&mut self) {
        self.app.input.cancel_close();
    }

    /// 返回窗口的共享引用,供 windowed 渲染器创建 wgpu surface。
    pub fn shared_window(&self) -> Option<Arc<Window>> {
        self.app.window.clone()
    }

    /// 返回 NSWindow 指针供 gfx 创建 Metal surface。
    ///
    /// winit 的 raw-window-handle 只暴露 NSView;此处经 objc `[view window]`
    /// 取回 NSWindow,与旧 GLFW `GetCocoaWindow` 语义一致,gfx 零改动。
    pub fn ns_window(&self) -> Option<usize> {
        use raw_window_handle::{HasWindowHandle, RawWindowHandle};
        let window = self.app.window.as_ref()?;
        let handle = window.window_handle().ok()?.as_raw();
        let RawWindowHandle::AppKit(appkit) = handle else {
            return None;
        };
        let ns_view: *mut objc2::runtime::AnyObject = appkit.ns_view.as_ptr().cast();
        // SAFETY: ns_view 来自活动窗口的有效句柄,`window` 消息在主线程发送。
        let ns_window: *mut objc2::runtime::AnyObject =
            unsafe { objc2::msg_send![ns_view, window] };
        if ns_window.is_null() {
            return None;
        }
        Some(ns_window as usize)
    }
}
