//! 输入状态机与快照编码:本模块是纯状态逻辑,不创建真实窗口,可无头单测。
//!
//! ## 快照布局(layout v1,小端,固定 4160 字节)
//!
//! | 偏移 | 内容 |
//! |------|------|
//! | 0    | layout version u32(=1) |
//! | 4    | flags u32:bit0 should_close、bit1 text_overflow |
//! | 8    | 键位 bitmask u64(位序 = Go `client.Key` 的 iota 值) |
//! | 16   | 鼠标键位 u32:bit0 主键、bit1 副键 |
//! | 20   | 文本字符数 u32 |
//! | 24   | cursor_x f64(逻辑坐标,GLFW GetCursorPos 语义) |
//! | 32   | cursor_y f64 |
//! | 40   | framebuffer 宽/高 u32×2(物理像素) |
//! | 48   | content 宽/高 u32×2(逻辑点) |
//! | 56   | reserved 8 字节(必须为零) |
//! | 64   | 文本段:1024 × u32 Unicode code point |
//!
//! 文本队列语义与旧 GLFW 实现一致:上限 1024,溢出置标志并丢弃超出部分;
//! 每次快照编码后队列清空(drain 语义在 Rust 侧完成,Go 只读快照)。

use winit::keyboard::KeyCode;

/// 快照固定头部字节数。
pub const SNAPSHOT_HEADER_BYTES: usize = 64;
/// 文本队列上限(字符数),与旧 Go 实现的 `[1024]rune` 一致。
pub const TEXT_CAPACITY: usize = 1024;
/// 快照总字节数:头部 + 1024×u32 文本段。
pub const SNAPSHOT_BYTES: usize = SNAPSHOT_HEADER_BYTES + TEXT_CAPACITY * 4;
/// 快照布局版本。
pub const SNAPSHOT_LAYOUT_VERSION: u32 = 1;

/// 把 winit 物理键码映射为键位 bitmask 的位序。
///
/// 位序必须与 Go `internal/client.Key` 常量的 iota 值逐一对应;新增按键只能
/// 追加更高位,不得改变既有位序(与旧实现"追加保持 iota"的约束同源)。
pub fn key_bit(code: KeyCode) -> Option<u32> {
    Some(match code {
        KeyCode::KeyW => 0,
        KeyCode::KeyA => 1,
        KeyCode::KeyS => 2,
        KeyCode::KeyD => 3,
        KeyCode::Space => 4,
        KeyCode::ShiftLeft => 5,
        KeyCode::ControlLeft => 6,
        KeyCode::Escape => 7,
        KeyCode::Digit1 => 8,
        KeyCode::Digit2 => 9,
        KeyCode::Digit3 => 10,
        KeyCode::Digit4 => 11,
        KeyCode::Digit5 => 12,
        KeyCode::Digit6 => 13,
        KeyCode::Digit7 => 14,
        KeyCode::Digit8 => 15,
        KeyCode::Digit9 => 16,
        KeyCode::KeyE => 17,
        KeyCode::KeyQ => 18,
        KeyCode::F3 => 19,
        KeyCode::F5 => 20,
        KeyCode::F6 => 21,
        KeyCode::ArrowUp => 22,
        KeyCode::ArrowDown => 23,
        KeyCode::ArrowLeft => 24,
        KeyCode::ArrowRight => 25,
        KeyCode::Enter => 26,
        KeyCode::AltLeft => 27,
        KeyCode::Backspace => 28,
        _ => return None,
    })
}

/// 输入状态机:事件在此累积,`encode_snapshot` 输出并排空文本队列。
///
/// 光标语义:未捕获时报告窗口绝对逻辑坐标;捕获期间改由 MouseMotion delta
/// 在虚拟坐标上累计合成,保证越过屏幕边界后仍连续单调(GLFW
/// `CursorDisabled` 的等价语义)。
pub struct InputState {
    keys: u64,
    mouse: u32,
    /// 绝对光标位置(逻辑坐标),未捕获时的报告来源。
    cursor_x: f64,
    cursor_y: f64,
    /// 捕获期间的虚拟光标位置,由相对位移累计。
    virtual_x: f64,
    virtual_y: f64,
    captured: bool,
    framebuffer_width: u32,
    framebuffer_height: u32,
    content_width: u32,
    content_height: u32,
    should_close: bool,
    text: [u32; TEXT_CAPACITY],
    text_len: usize,
    text_overflow: bool,
}

impl Default for InputState {
    fn default() -> Self {
        Self::new()
    }
}

impl InputState {
    /// 新建空状态;尺寸由窗口创建/Resized 事件填充。
    pub fn new() -> Self {
        Self {
            keys: 0,
            mouse: 0,
            cursor_x: 0.0,
            cursor_y: 0.0,
            virtual_x: 0.0,
            virtual_y: 0.0,
            captured: false,
            framebuffer_width: 0,
            framebuffer_height: 0,
            content_width: 0,
            content_height: 0,
            should_close: false,
            text: [0; TEXT_CAPACITY],
            text_len: 0,
            text_overflow: false,
        }
    }

    /// 处理键盘事件;repeat 与首次按下同样置位(GLFW `Press|Repeat` 语义)。
    pub fn key_event(&mut self, code: KeyCode, pressed: bool) {
        if let Some(bit) = key_bit(code) {
            if pressed {
                self.keys |= 1 << bit;
            } else {
                self.keys &= !(1 << bit);
            }
        }
    }

    /// 处理鼠标主/副键;`primary` 为 true 表示主键。
    pub fn mouse_button(&mut self, primary: bool, pressed: bool) {
        let bit = if primary { 0 } else { 1 };
        if pressed {
            self.mouse |= 1 << bit;
        } else {
            self.mouse &= !(1 << bit);
        }
    }

    /// 绝对光标移动(逻辑坐标);捕获期间忽略,以免与 delta 合成冲突。
    pub fn cursor_moved(&mut self, x: f64, y: f64) {
        if !self.captured {
            self.cursor_x = x;
            self.cursor_y = y;
        }
    }

    /// 相对位移(DeviceEvent::MouseMotion);只在捕获期间参与虚拟坐标累计。
    pub fn mouse_delta(&mut self, dx: f64, dy: f64) {
        if self.captured {
            self.virtual_x += dx;
            self.virtual_y += dy;
        }
    }

    /// 切换捕获状态;进入捕获时虚拟坐标从当前绝对位置续起,退出时把绝对
    /// 位置对齐虚拟位置,保证两种模式切换处视角输入无跳变。
    pub fn set_captured(&mut self, captured: bool) {
        if captured == self.captured {
            return;
        }
        if captured {
            self.virtual_x = self.cursor_x;
            self.virtual_y = self.cursor_y;
        } else {
            self.cursor_x = self.virtual_x;
            self.cursor_y = self.virtual_y;
        }
        self.captured = captured;
    }

    /// 当前是否处于捕获状态。
    pub fn captured(&self) -> bool {
        self.captured
    }

    /// 记录窗口尺寸(framebuffer 为物理像素,content 为逻辑点)。
    pub fn set_sizes(&mut self, fb_w: u32, fb_h: u32, content_w: u32, content_h: u32) {
        self.framebuffer_width = fb_w;
        self.framebuffer_height = fb_h;
        self.content_width = content_w;
        self.content_height = content_h;
    }

    /// 记录关闭请求(CloseRequested)。
    pub fn request_close(&mut self) {
        self.should_close = true;
    }

    /// 撤销关闭请求(Go `CancelClose` 语义)。
    pub fn cancel_close(&mut self) {
        self.should_close = false;
    }

    /// 追加一个文本字符;队列满时置 overflow 并丢弃(旧实现语义)。
    pub fn push_text(&mut self, ch: char) {
        if self.text_len == TEXT_CAPACITY {
            self.text_overflow = true;
            return;
        }
        self.text[self.text_len] = ch as u32;
        self.text_len += 1;
    }

    /// 把当前状态编码进 `out`(必须恰好 [`SNAPSHOT_BYTES`] 字节),
    /// 随后排空文本队列与 overflow 标志。
    pub fn encode_snapshot(&mut self, out: &mut [u8]) {
        assert_eq!(out.len(), SNAPSHOT_BYTES);
        out[..SNAPSHOT_HEADER_BYTES].fill(0);
        out[0..4].copy_from_slice(&SNAPSHOT_LAYOUT_VERSION.to_le_bytes());
        let mut flags = 0u32;
        if self.should_close {
            flags |= 1;
        }
        if self.text_overflow {
            flags |= 2;
        }
        out[4..8].copy_from_slice(&flags.to_le_bytes());
        out[8..16].copy_from_slice(&self.keys.to_le_bytes());
        out[16..20].copy_from_slice(&self.mouse.to_le_bytes());
        out[20..24].copy_from_slice(&(self.text_len as u32).to_le_bytes());
        let (x, y) = if self.captured {
            (self.virtual_x, self.virtual_y)
        } else {
            (self.cursor_x, self.cursor_y)
        };
        out[24..32].copy_from_slice(&x.to_le_bytes());
        out[32..40].copy_from_slice(&y.to_le_bytes());
        out[40..44].copy_from_slice(&self.framebuffer_width.to_le_bytes());
        out[44..48].copy_from_slice(&self.framebuffer_height.to_le_bytes());
        out[48..52].copy_from_slice(&self.content_width.to_le_bytes());
        out[52..56].copy_from_slice(&self.content_height.to_le_bytes());
        for (index, ch) in self.text[..self.text_len].iter().enumerate() {
            let offset = SNAPSHOT_HEADER_BYTES + index * 4;
            out[offset..offset + 4].copy_from_slice(&ch.to_le_bytes());
        }
        out[SNAPSHOT_HEADER_BYTES + self.text_len * 4..].fill(0);
        self.text_len = 0;
        self.text_overflow = false;
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn decode_u32(bytes: &[u8], offset: usize) -> u32 {
        u32::from_le_bytes(bytes[offset..offset + 4].try_into().unwrap())
    }

    fn decode_f64(bytes: &[u8], offset: usize) -> f64 {
        f64::from_le_bytes(bytes[offset..offset + 8].try_into().unwrap())
    }

    #[test]
    fn key_bits_match_go_iota_order() {
        // 锁定与 Go client.Key 常量一一对应的位序;任何重排都会破坏快照契约。
        let expected: [(KeyCode, u32); 29] = [
            (KeyCode::KeyW, 0),
            (KeyCode::KeyA, 1),
            (KeyCode::KeyS, 2),
            (KeyCode::KeyD, 3),
            (KeyCode::Space, 4),
            (KeyCode::ShiftLeft, 5),
            (KeyCode::ControlLeft, 6),
            (KeyCode::Escape, 7),
            (KeyCode::Digit1, 8),
            (KeyCode::Digit2, 9),
            (KeyCode::Digit3, 10),
            (KeyCode::Digit4, 11),
            (KeyCode::Digit5, 12),
            (KeyCode::Digit6, 13),
            (KeyCode::Digit7, 14),
            (KeyCode::Digit8, 15),
            (KeyCode::Digit9, 16),
            (KeyCode::KeyE, 17),
            (KeyCode::KeyQ, 18),
            (KeyCode::F3, 19),
            (KeyCode::F5, 20),
            (KeyCode::F6, 21),
            (KeyCode::ArrowUp, 22),
            (KeyCode::ArrowDown, 23),
            (KeyCode::ArrowLeft, 24),
            (KeyCode::ArrowRight, 25),
            (KeyCode::Enter, 26),
            (KeyCode::AltLeft, 27),
            (KeyCode::Backspace, 28),
        ];
        for (code, bit) in expected {
            assert_eq!(key_bit(code), Some(bit), "{code:?}");
        }
        assert_eq!(key_bit(KeyCode::KeyZ), None);
    }

    #[test]
    fn snapshot_encodes_state_and_drains_text() {
        let mut state = InputState::new();
        state.key_event(KeyCode::KeyW, true);
        state.key_event(KeyCode::Space, true);
        state.key_event(KeyCode::KeyW, false);
        state.mouse_button(true, true);
        state.cursor_moved(12.5, -3.25);
        state.set_sizes(2560, 1440, 1280, 720);
        state.push_text('你');
        state.push_text('A');

        let mut out = vec![0u8; SNAPSHOT_BYTES];
        state.encode_snapshot(&mut out);
        assert_eq!(decode_u32(&out, 0), SNAPSHOT_LAYOUT_VERSION);
        assert_eq!(decode_u32(&out, 4), 0);
        assert_eq!(u64::from_le_bytes(out[8..16].try_into().unwrap()), 1 << 4);
        assert_eq!(decode_u32(&out, 16), 1);
        assert_eq!(decode_u32(&out, 20), 2);
        assert_eq!(decode_f64(&out, 24), 12.5);
        assert_eq!(decode_f64(&out, 32), -3.25);
        assert_eq!(decode_u32(&out, 40), 2560);
        assert_eq!(decode_u32(&out, 48), 1280);
        assert_eq!(decode_u32(&out, 64), '你' as u32);
        assert_eq!(decode_u32(&out, 68), 'A' as u32);

        // 第二次快照:文本已排空,按键状态保持。
        state.encode_snapshot(&mut out);
        assert_eq!(decode_u32(&out, 20), 0);
        assert_eq!(u64::from_le_bytes(out[8..16].try_into().unwrap()), 1 << 4);
    }

    #[test]
    fn text_queue_is_bounded_with_overflow_flag() {
        let mut state = InputState::new();
        for _ in 0..TEXT_CAPACITY + 5 {
            state.push_text('x');
        }
        let mut out = vec![0u8; SNAPSHOT_BYTES];
        state.encode_snapshot(&mut out);
        assert_eq!(decode_u32(&out, 20), TEXT_CAPACITY as u32);
        assert_eq!(decode_u32(&out, 4) & 2, 2, "overflow 标志必须置位");

        // 排空后重新开始:无 overflow、计数归零。
        state.push_text('y');
        state.encode_snapshot(&mut out);
        assert_eq!(decode_u32(&out, 20), 1);
        assert_eq!(decode_u32(&out, 4) & 2, 0);
    }

    #[test]
    fn captured_cursor_is_continuous_across_toggle() {
        let mut state = InputState::new();
        state.cursor_moved(100.0, 200.0);
        state.set_captured(true);
        // 捕获期间绝对移动被忽略,delta 累计生效。
        state.cursor_moved(0.0, 0.0);
        state.mouse_delta(30.0, -10.0);
        state.mouse_delta(5.0, 5.0);
        let mut out = vec![0u8; SNAPSHOT_BYTES];
        state.encode_snapshot(&mut out);
        assert_eq!(decode_f64(&out, 24), 135.0);
        assert_eq!(decode_f64(&out, 32), 195.0);

        // 未捕获时 delta 不再生效,释放处无跳变。
        state.set_captured(false);
        state.mouse_delta(50.0, 50.0);
        state.encode_snapshot(&mut out);
        assert_eq!(decode_f64(&out, 24), 135.0);
        assert_eq!(decode_f64(&out, 32), 195.0);

        // 幂等:重复设置同一状态无副作用。
        state.set_captured(false);
        assert!(!state.captured());
    }

    #[test]
    fn close_request_and_cancel_roundtrip() {
        let mut state = InputState::new();
        state.request_close();
        let mut out = vec![0u8; SNAPSHOT_BYTES];
        state.encode_snapshot(&mut out);
        assert_eq!(decode_u32(&out, 4) & 1, 1);
        state.cancel_close();
        state.encode_snapshot(&mut out);
        assert_eq!(decode_u32(&out, 4) & 1, 0);
    }
}
