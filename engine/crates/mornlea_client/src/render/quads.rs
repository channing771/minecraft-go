//! 双流 instanced quad pass:名牌(billboard)、HUD 与调试面板的共同骨架。
//!
//! 三个 Go renderer 的 GPU 结构同构:uniform(billboard 相机 96B 或
//! viewport 16B)+ 两条 instanced 流(背景/quad 与字形,各 `Draw(6, N)`)+
//! 字形图集(可选第二贴图:HUD 图集),alpha blend。本模块以配置参数逐一
//! 镜像 Go 的缓冲布局(uniform@0、流 A@256、流 B 对齐 256)与管线状态;
//! CPU 布局留 Go,实例字节经 frame v2 pass 段过境。

/// 一个双流 quad pass 的静态配置,取值必须与对应 Go renderer 常量一致。
pub struct QuadPassConfig {
    pub label: &'static str,
    /// uniform 字节数(名牌 96,HUD/面板 16)。
    pub uniform_bytes: usize,
    /// 每实例字节数(名牌 64,HUD/面板 48)。
    pub instance_bytes: usize,
    /// 流 A(背景/quad)与流 B(字形)的实例容量。
    pub stream_a_cap: usize,
    pub stream_b_cap: usize,
    /// 两条流的着色器入口。
    pub entry_a: (&'static str, &'static str),
    pub entry_b: (&'static str, &'static str),
    /// 名牌 pass 挂深度附件(只读);HUD/面板为纯颜色。
    pub uses_depth: bool,
    /// HUD 需要第二贴图(binding 5)。
    pub second_texture: bool,
    /// 采样器过滤:HUD 用 Nearest,名牌/面板用 Linear。
    pub nearest_sampler: bool,
}

/// 双流 quad pass 的 GPU 资源。
pub struct QuadPass {
    config: QuadPassConfig,
    dynamic: wgpu::Buffer,
    layout: wgpu::BindGroupLayout,
    pipeline_a: wgpu::RenderPipeline,
    pipeline_b: wgpu::RenderPipeline,
    sampler: wgpu::Sampler,
    bind: Option<wgpu::BindGroup>,
    stream_a_offset: usize,
    stream_b_offset: usize,
}

impl QuadPass {
    /// 创建 pass;bind group 由 [`QuadPass::rebuild_bind`] 在贴图就绪后构建。
    pub fn new(
        device: &wgpu::Device,
        shader: &wgpu::ShaderModule,
        config: QuadPassConfig,
        color_format: wgpu::TextureFormat,
        depth_format: wgpu::TextureFormat,
    ) -> Self {
        let stream_a_offset = 256usize;
        let stream_a_size = config.stream_a_cap * config.instance_bytes;
        // 流 B 偏移对齐 256(镜像 Go 的 (offset+size+255)&^255)。
        let stream_b_offset = (stream_a_offset + stream_a_size + 255) & !255;
        let stream_b_size = config.stream_b_cap * config.instance_bytes;
        let dynamic = device.create_buffer(&wgpu::BufferDescriptor {
            label: Some(config.label),
            size: (stream_b_offset + stream_b_size) as u64,
            usage: wgpu::BufferUsages::UNIFORM
                | wgpu::BufferUsages::STORAGE
                | wgpu::BufferUsages::COPY_DST,
            mapped_at_creation: false,
        });

        let mut entries = vec![
            wgpu::BindGroupLayoutEntry {
                binding: 0,
                visibility: wgpu::ShaderStages::VERTEX,
                ty: wgpu::BindingType::Buffer {
                    ty: wgpu::BufferBindingType::Uniform,
                    has_dynamic_offset: false,
                    min_binding_size: None,
                },
                count: None,
            },
            wgpu::BindGroupLayoutEntry {
                binding: 1,
                visibility: wgpu::ShaderStages::VERTEX,
                ty: wgpu::BindingType::Buffer {
                    ty: wgpu::BufferBindingType::Storage { read_only: true },
                    has_dynamic_offset: false,
                    min_binding_size: None,
                },
                count: None,
            },
            wgpu::BindGroupLayoutEntry {
                binding: 2,
                visibility: wgpu::ShaderStages::VERTEX,
                ty: wgpu::BindingType::Buffer {
                    ty: wgpu::BufferBindingType::Storage { read_only: true },
                    has_dynamic_offset: false,
                    min_binding_size: None,
                },
                count: None,
            },
            wgpu::BindGroupLayoutEntry {
                binding: 3,
                visibility: wgpu::ShaderStages::FRAGMENT,
                ty: wgpu::BindingType::Texture {
                    sample_type: wgpu::TextureSampleType::Float { filterable: true },
                    view_dimension: wgpu::TextureViewDimension::D2,
                    multisampled: false,
                },
                count: None,
            },
            wgpu::BindGroupLayoutEntry {
                binding: 4,
                visibility: wgpu::ShaderStages::FRAGMENT,
                ty: wgpu::BindingType::Sampler(wgpu::SamplerBindingType::Filtering),
                count: None,
            },
        ];
        if config.second_texture {
            entries.push(wgpu::BindGroupLayoutEntry {
                binding: 5,
                visibility: wgpu::ShaderStages::FRAGMENT,
                ty: wgpu::BindingType::Texture {
                    sample_type: wgpu::TextureSampleType::Float { filterable: true },
                    view_dimension: wgpu::TextureViewDimension::D2,
                    multisampled: false,
                },
                count: None,
            });
        }
        let layout = device.create_bind_group_layout(&wgpu::BindGroupLayoutDescriptor {
            label: Some(config.label),
            entries: &entries,
        });
        let pipeline_layout = device.create_pipeline_layout(&wgpu::PipelineLayoutDescriptor {
            label: None,
            bind_group_layouts: &[Some(&layout)],
            immediate_size: 0,
        });
        let make_pipeline = |label: &str, vs: &str, fs: &str| {
            device.create_render_pipeline(&wgpu::RenderPipelineDescriptor {
                label: Some(label),
                layout: Some(&pipeline_layout),
                vertex: wgpu::VertexState {
                    module: shader,
                    entry_point: Some(vs),
                    compilation_options: Default::default(),
                    buffers: &[],
                },
                fragment: Some(wgpu::FragmentState {
                    module: shader,
                    entry_point: Some(fs),
                    compilation_options: Default::default(),
                    targets: &[Some(wgpu::ColorTargetState {
                        format: color_format,
                        blend: Some(wgpu::BlendState::ALPHA_BLENDING),
                        write_mask: wgpu::ColorWrites::ALL,
                    })],
                }),
                primitive: wgpu::PrimitiveState {
                    topology: wgpu::PrimitiveTopology::TriangleList,
                    front_face: wgpu::FrontFace::Ccw,
                    cull_mode: None,
                    ..Default::default()
                },
                depth_stencil: if config.uses_depth {
                    Some(wgpu::DepthStencilState {
                        format: depth_format,
                        depth_write_enabled: Some(false),
                        depth_compare: Some(wgpu::CompareFunction::Less),
                        stencil: wgpu::StencilState::default(),
                        bias: wgpu::DepthBiasState::default(),
                    })
                } else {
                    None
                },
                multisample: wgpu::MultisampleState::default(),
                multiview_mask: None,
                cache: None,
            })
        };
        let pipeline_a = make_pipeline(config.label, config.entry_a.0, config.entry_a.1);
        let pipeline_b = make_pipeline(config.label, config.entry_b.0, config.entry_b.1);
        let (filter, mipmap) = if config.nearest_sampler {
            (wgpu::FilterMode::Nearest, wgpu::MipmapFilterMode::Nearest)
        } else {
            (wgpu::FilterMode::Linear, wgpu::MipmapFilterMode::Nearest)
        };
        let sampler = device.create_sampler(&wgpu::SamplerDescriptor {
            label: Some(config.label),
            address_mode_u: wgpu::AddressMode::ClampToEdge,
            address_mode_v: wgpu::AddressMode::ClampToEdge,
            address_mode_w: wgpu::AddressMode::ClampToEdge,
            mag_filter: filter,
            min_filter: filter,
            mipmap_filter: mipmap,
            ..Default::default()
        });
        Self {
            config,
            dynamic,
            layout,
            pipeline_a,
            pipeline_b,
            sampler,
            bind: None,
            stream_a_offset,
            stream_b_offset,
        }
    }

    /// (重)建 bind group;`second` 只在 `second_texture` 配置为真时使用。
    pub fn rebuild_bind(
        &mut self,
        device: &wgpu::Device,
        glyph_view: &wgpu::TextureView,
        second: Option<&wgpu::TextureView>,
    ) {
        let uniform_size = (self.config.uniform_bytes as u64).try_into().unwrap();
        let a_size = ((self.config.stream_a_cap * self.config.instance_bytes) as u64)
            .try_into()
            .unwrap();
        let b_size = ((self.config.stream_b_cap * self.config.instance_bytes) as u64)
            .try_into()
            .unwrap();
        let mut entries = vec![
            wgpu::BindGroupEntry {
                binding: 0,
                resource: wgpu::BindingResource::Buffer(wgpu::BufferBinding {
                    buffer: &self.dynamic,
                    offset: 0,
                    size: Some(uniform_size),
                }),
            },
            wgpu::BindGroupEntry {
                binding: 1,
                resource: wgpu::BindingResource::Buffer(wgpu::BufferBinding {
                    buffer: &self.dynamic,
                    offset: self.stream_a_offset as u64,
                    size: Some(a_size),
                }),
            },
            wgpu::BindGroupEntry {
                binding: 2,
                resource: wgpu::BindingResource::Buffer(wgpu::BufferBinding {
                    buffer: &self.dynamic,
                    offset: self.stream_b_offset as u64,
                    size: Some(b_size),
                }),
            },
            wgpu::BindGroupEntry {
                binding: 3,
                resource: wgpu::BindingResource::TextureView(glyph_view),
            },
            wgpu::BindGroupEntry {
                binding: 4,
                resource: wgpu::BindingResource::Sampler(&self.sampler),
            },
        ];
        if self.config.second_texture {
            let Some(second) = second else {
                return;
            };
            entries.push(wgpu::BindGroupEntry {
                binding: 5,
                resource: wgpu::BindingResource::TextureView(second),
            });
        }
        self.bind = Some(device.create_bind_group(&wgpu::BindGroupDescriptor {
            label: Some(self.config.label),
            layout: &self.layout,
            entries: &entries,
        }));
    }

    /// 解析 pass 段 payload:`[uniform][a_count u32][b_count u32][a][b]`,
    /// 长度与容量精确校验;合法返回 (uniform, a, b) 三段视图。
    pub fn parse_segment<'a>(&self, payload: &'a [u8]) -> Option<(&'a [u8], &'a [u8], &'a [u8])> {
        let head = self.config.uniform_bytes + 8;
        if payload.len() < head {
            return None;
        }
        let uniform = &payload[..self.config.uniform_bytes];
        let a_count = u32::from_le_bytes(
            payload[self.config.uniform_bytes..self.config.uniform_bytes + 4]
                .try_into()
                .unwrap(),
        ) as usize;
        let b_count = u32::from_le_bytes(
            payload[self.config.uniform_bytes + 4..head]
                .try_into()
                .unwrap(),
        ) as usize;
        if a_count > self.config.stream_a_cap || b_count > self.config.stream_b_cap {
            return None;
        }
        let a_bytes = a_count * self.config.instance_bytes;
        let b_bytes = b_count * self.config.instance_bytes;
        if payload.len() != head + a_bytes + b_bytes {
            return None;
        }
        let a = &payload[head..head + a_bytes];
        let b = &payload[head + a_bytes..];
        Some((uniform, a, b))
    }

    /// 上传 uniform 与两条流并录制 pass;`depth` 只在 `uses_depth` 时提供。
    #[allow(clippy::too_many_arguments)]
    pub fn upload_and_record(
        &self,
        queue: &wgpu::Queue,
        encoder: &mut wgpu::CommandEncoder,
        color: &wgpu::TextureView,
        depth: Option<&wgpu::TextureView>,
        uniform: &[u8],
        stream_a: &[u8],
        stream_b: &[u8],
    ) {
        let Some(bind) = &self.bind else {
            return;
        };
        let a_count = (stream_a.len() / self.config.instance_bytes) as u32;
        let b_count = (stream_b.len() / self.config.instance_bytes) as u32;
        if a_count == 0 && b_count == 0 {
            return;
        }
        queue.write_buffer(&self.dynamic, 0, uniform);
        if !stream_a.is_empty() {
            queue.write_buffer(&self.dynamic, self.stream_a_offset as u64, stream_a);
        }
        if !stream_b.is_empty() {
            queue.write_buffer(&self.dynamic, self.stream_b_offset as u64, stream_b);
        }
        let depth_attachment = depth.map(|view| wgpu::RenderPassDepthStencilAttachment {
            view,
            depth_ops: Some(wgpu::Operations {
                load: wgpu::LoadOp::Load,
                store: wgpu::StoreOp::Store,
            }),
            stencil_ops: None,
        });
        let mut pass = encoder.begin_render_pass(&wgpu::RenderPassDescriptor {
            label: Some(self.config.label),
            color_attachments: &[Some(wgpu::RenderPassColorAttachment {
                view: color,
                depth_slice: None,
                resolve_target: None,
                ops: wgpu::Operations {
                    load: wgpu::LoadOp::Load,
                    store: wgpu::StoreOp::Store,
                },
            })],
            depth_stencil_attachment: depth_attachment,
            occlusion_query_set: None,
            timestamp_writes: None,
            multiview_mask: None,
        });
        pass.set_bind_group(0, bind, &[]);
        if a_count != 0 {
            pass.set_pipeline(&self.pipeline_a);
            pass.draw(0..6, 0..a_count);
        }
        if b_count != 0 {
            pass.set_pipeline(&self.pipeline_b);
            pass.draw(0..6, 0..b_count);
        }
    }
}
