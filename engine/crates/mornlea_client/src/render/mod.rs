//! 离屏 wgpu 世界渲染器(R2a)。
//!
//! 本模块是 Go `internal/render` 世界地形路径的平行 Rust 实现:纯离屏、
//! 不接窗口 surface,生产客户端仍由 Go 渲染;双后端图像对照门禁在 Go 侧
//! 测试中执行。设计约束:
//!
//! - color target 为 `Bgra8UnormSrgb`(与 Go capture 离屏格式一致),
//!   depth 为 `Depth32Float`;
//! - mesh 紧凑 Quad 字节只在 section 变脏时过境一次;
//! - 每帧一次 `render_frame`,帧内无逐 pass/逐资源 FFI。

pub mod shaders;

use std::collections::HashMap;

/// 离屏 color 格式,必须与 Go capture 的 `FormatBGRA8UnormSrgb` 一致。
pub const COLOR_FORMAT: wgpu::TextureFormat = wgpu::TextureFormat::Bgra8UnormSrgb;
/// 深度格式,与 Go 渲染器的 `FormatDepth32Float` 一致。
pub const DEPTH_FORMAT: wgpu::TextureFormat = wgpu::TextureFormat::Depth32Float;

/// 渲染器创建失败的稳定原因,FFI 层转错误状态码。
#[derive(Debug)]
pub enum RenderCreateError {
    /// 本机无可用 GPU 适配器(CI 容器常见),调用方应跳过而非失败。
    Adapter,
    /// 设备创建失败。
    Device,
}

/// 一个已上传的 section:紧凑 Quad 缓冲与实例数。
struct SectionSlot {
    /// GPU 上的紧凑 Quad 字节(8 字节/quad)。
    #[allow(dead_code)] // Task 1.2 绘制路径消费。
    quads: wgpu::Buffer,
    quad_count: u32,
}

/// 离屏世界渲染器:device/queue、渲染目标与 section 存储。
pub struct OffscreenRenderer {
    device: wgpu::Device,
    queue: wgpu::Queue,
    width: u32,
    height: u32,
    color: wgpu::Texture,
    #[allow(dead_code)] // Task 1.2 起被 pass 消费。
    color_view: wgpu::TextureView,
    #[allow(dead_code)]
    depth: wgpu::Texture,
    #[allow(dead_code)]
    depth_view: wgpu::TextureView,
    sections: HashMap<(i32, i32, i32), SectionSlot>,
}

impl OffscreenRenderer {
    /// 创建离屏渲染器;无适配器时返回 [`RenderCreateError::Adapter`]。
    pub fn new(width: u32, height: u32) -> Result<Self, RenderCreateError> {
        // 离屏渲染不需要 display handle。
        let instance = wgpu::Instance::new(wgpu::InstanceDescriptor::new_without_display_handle());
        let adapter = pollster::block_on(instance.request_adapter(&wgpu::RequestAdapterOptions {
            power_preference: wgpu::PowerPreference::HighPerformance,
            force_fallback_adapter: false,
            compatible_surface: None,
        }))
        .map_err(|_| RenderCreateError::Adapter)?;
        let (device, queue) = pollster::block_on(adapter.request_device(&wgpu::DeviceDescriptor {
            label: Some("mornlea offscreen"),
            ..Default::default()
        }))
        .map_err(|_| RenderCreateError::Device)?;

        let color = device.create_texture(&wgpu::TextureDescriptor {
            label: Some("offscreen color"),
            size: wgpu::Extent3d {
                width,
                height,
                depth_or_array_layers: 1,
            },
            mip_level_count: 1,
            sample_count: 1,
            dimension: wgpu::TextureDimension::D2,
            format: COLOR_FORMAT,
            usage: wgpu::TextureUsages::RENDER_ATTACHMENT | wgpu::TextureUsages::COPY_SRC,
            view_formats: &[],
        });
        let depth = device.create_texture(&wgpu::TextureDescriptor {
            label: Some("offscreen depth"),
            size: wgpu::Extent3d {
                width,
                height,
                depth_or_array_layers: 1,
            },
            mip_level_count: 1,
            sample_count: 1,
            dimension: wgpu::TextureDimension::D2,
            format: DEPTH_FORMAT,
            usage: wgpu::TextureUsages::RENDER_ATTACHMENT | wgpu::TextureUsages::TEXTURE_BINDING,
            view_formats: &[],
        });
        let color_view = color.create_view(&wgpu::TextureViewDescriptor::default());
        let depth_view = depth.create_view(&wgpu::TextureViewDescriptor::default());
        Ok(Self {
            device,
            queue,
            width,
            height,
            color,
            color_view,
            depth,
            depth_view,
            sections: HashMap::new(),
        })
    }

    /// 上传/替换一个 section 的紧凑 Quad 字节(长度必须是 8 的倍数,
    /// 由 FFI 层校验);空数据等价于 drop。
    pub fn upload_section(&mut self, pos: (i32, i32, i32), quads: &[u8]) {
        debug_assert_eq!(quads.len() % 8, 0);
        if quads.is_empty() {
            self.sections.remove(&pos);
            return;
        }
        let buffer = self.device.create_buffer(&wgpu::BufferDescriptor {
            label: Some("section quads"),
            size: quads.len() as u64,
            usage: wgpu::BufferUsages::STORAGE | wgpu::BufferUsages::COPY_DST,
            mapped_at_creation: false,
        });
        self.queue.write_buffer(&buffer, 0, quads);
        self.sections.insert(
            pos,
            SectionSlot {
                quads: buffer,
                quad_count: (quads.len() / 8) as u32,
            },
        );
    }

    /// 丢弃一个 section;不存在时为幂等空操作。
    pub fn drop_section(&mut self, pos: (i32, i32, i32)) {
        self.sections.remove(&pos);
    }

    /// 已上传 section 的 quad 总数,供测试与调用计数断言。
    pub fn total_quads(&self) -> u64 {
        self.sections
            .values()
            .map(|slot| u64::from(slot.quad_count))
            .sum()
    }

    /// 渲染一帧到离屏 target。Task 1.2 起实现真实 pass;当前为固定
    /// clear(与 Go 渲染器的天空 clear 值对齐前的占位),保证回读链路
    /// 可先行验证。
    pub fn render_frame(&mut self) {
        let mut encoder = self
            .device
            .create_command_encoder(&wgpu::CommandEncoderDescriptor {
                label: Some("frame"),
            });
        {
            let _pass = encoder.begin_render_pass(&wgpu::RenderPassDescriptor {
                label: Some("clear"),
                color_attachments: &[Some(wgpu::RenderPassColorAttachment {
                    view: &self.color_view,
                    depth_slice: None,
                    resolve_target: None,
                    ops: wgpu::Operations {
                        load: wgpu::LoadOp::Clear(wgpu::Color {
                            r: 0.25,
                            g: 0.5,
                            b: 1.0,
                            a: 1.0,
                        }),
                        store: wgpu::StoreOp::Store,
                    },
                })],
                depth_stencil_attachment: Some(wgpu::RenderPassDepthStencilAttachment {
                    view: &self.depth_view,
                    depth_ops: Some(wgpu::Operations {
                        load: wgpu::LoadOp::Clear(1.0),
                        store: wgpu::StoreOp::Store,
                    }),
                    stencil_ops: None,
                }),
                occlusion_query_set: None,
                timestamp_writes: None,
                multiview_mask: None,
            });
        }
        self.queue.submit([encoder.finish()]);
    }

    /// 阻塞回读离屏 color(BGRA,逐行紧密拼接);`out` 长度必须恰为
    /// width×height×4(FFI 层校验)。
    pub fn readback(&self, out: &mut [u8]) {
        debug_assert_eq!(out.len(), (self.width * self.height * 4) as usize);
        // WebGPU 要求 bytes_per_row 按 256 对齐;宽度不整除时按对齐行距
        // 拷出再紧缩。
        let unpadded = self.width * 4;
        let padded = unpadded.div_ceil(256) * 256;
        let buffer = self.device.create_buffer(&wgpu::BufferDescriptor {
            label: Some("readback"),
            size: u64::from(padded) * u64::from(self.height),
            usage: wgpu::BufferUsages::COPY_DST | wgpu::BufferUsages::MAP_READ,
            mapped_at_creation: false,
        });
        let mut encoder = self
            .device
            .create_command_encoder(&wgpu::CommandEncoderDescriptor {
                label: Some("readback"),
            });
        encoder.copy_texture_to_buffer(
            wgpu::TexelCopyTextureInfo {
                texture: &self.color,
                mip_level: 0,
                origin: wgpu::Origin3d::ZERO,
                aspect: wgpu::TextureAspect::All,
            },
            wgpu::TexelCopyBufferInfo {
                buffer: &buffer,
                layout: wgpu::TexelCopyBufferLayout {
                    offset: 0,
                    bytes_per_row: Some(padded),
                    rows_per_image: Some(self.height),
                },
            },
            wgpu::Extent3d {
                width: self.width,
                height: self.height,
                depth_or_array_layers: 1,
            },
        );
        self.queue.submit([encoder.finish()]);

        let slice = buffer.slice(..);
        let (sender, receiver) = std::sync::mpsc::channel();
        slice.map_async(wgpu::MapMode::Read, move |result| {
            let _ = sender.send(result);
        });
        let _ = self.device.poll(wgpu::PollType::wait_indefinitely());
        receiver
            .recv()
            .expect("readback map 回调丢失")
            .expect("readback map 失败");
        let data = slice.get_mapped_range();
        for row in 0..self.height as usize {
            let src = row * padded as usize;
            let dst = row * unpadded as usize;
            out[dst..dst + unpadded as usize].copy_from_slice(&data[src..src + unpadded as usize]);
        }
        drop(data);
        buffer.unmap();
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn renderer_or_skip(width: u32, height: u32) -> Option<OffscreenRenderer> {
        // 与 Go NewHeadlessDevice 的约定一致:无适配器(CI 容器)跳过而非失败。
        OffscreenRenderer::new(width, height).ok()
    }

    #[test]
    fn offscreen_clear_and_readback_roundtrip() {
        let Some(mut renderer) = renderer_or_skip(64, 32) else {
            return;
        };
        renderer.render_frame();
        let mut out = vec![0u8; 64 * 32 * 4];
        renderer.readback(&mut out);
        assert!(out.iter().any(|&b| b != 0), "clear 后回读不应全零");
        // 同输入两次渲染必须逐字节一致。
        renderer.render_frame();
        let mut second = vec![0u8; 64 * 32 * 4];
        renderer.readback(&mut second);
        assert_eq!(out, second);
    }

    #[test]
    fn section_upload_and_drop_are_idempotent() {
        let Some(mut renderer) = renderer_or_skip(16, 16) else {
            return;
        };
        renderer.upload_section((0, 0, 0), &[0u8; 16]);
        renderer.upload_section((0, 1, 0), &[0u8; 8]);
        assert_eq!(renderer.total_quads(), 3);
        renderer.upload_section((0, 0, 0), &[0u8; 8]);
        assert_eq!(renderer.total_quads(), 2);
        renderer.upload_section((0, 1, 0), &[]);
        renderer.drop_section((9, 9, 9));
        assert_eq!(renderer.total_quads(), 1);
    }
}
