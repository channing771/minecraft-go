//! 离屏 wgpu 世界渲染器(R2a)。
//!
//! 本模块是 Go `internal/render` 世界地形路径的平行 Rust 实现:纯离屏、
//! 不接窗口 surface,生产客户端仍由 Go 渲染;双后端图像对照门禁在 Go 侧
//! 测试中执行。GPU 数据流逐一镜像 Go 版:全局 face 池(packed u64 face)、
//! origin 槽位、32 字节 section record、cull compute 写 visible instances
//! 与 indirect args、单次 indexed indirect draw、sky 全屏三角与 HiZ
//! 金字塔遮挡。uniform 布局、clear 值与 pass 顺序保持一致,保证同输入
//! 同图像。
//!
//! 约束:
//! - color `Bgra8UnormSrgb`(Go capture 同格式),depth `Depth32Float`;
//! - mesh packed face 字节只在 section 变脏时过境一次;
//! - 每帧一次 [`OffscreenRenderer::render_frame`],帧内无逐 pass FFI。

pub mod entity;
pub mod pool;
pub mod shaders;

use std::collections::HashMap;

use entity::EntityPass;
use pool::{Alloc, Pool};

/// 离屏 color 格式,必须与 Go capture 的 `FormatBGRA8UnormSrgb` 一致。
pub const COLOR_FORMAT: wgpu::TextureFormat = wgpu::TextureFormat::Bgra8UnormSrgb;
/// 深度格式,与 Go 渲染器的 `FormatDepth32Float` 一致。
pub const DEPTH_FORMAT: wgpu::TextureFormat = wgpu::TextureFormat::Depth32Float;

// 容量与 Go 渲染器默认值一致(pool 面数、origin 槽位)。
const POOL_FACES: u32 = 4_500_000;
const ORIGIN_SLOTS: u32 = 128 * 1024;
/// 每个 pool face 8 字节(packed u64);cull 后每个可见实例 16 字节。
const BYTES_PER_POOL_FACE: u64 = 8;
const BYTES_PER_VISIBLE_FACE: u64 = 16;
/// section record 布局:origin vec4<i32> + face_offset/count/origin_idx/pad。
const SECTION_RECORD_BYTES: usize = 32;
/// 材质 atlas:16×16 像素、5 级 mip,与 Go `internal/assets` 一致。
pub const ATLAS_TEX_SIZE: u32 = 16;
/// atlas mip 层数。
pub const ATLAS_MIPS: u32 = 5;
/// section Y 槽位的世界基准:SectionPos.Y 从 0 起,世界 Y 从 core.MinY 起。
const WORLD_MIN_Y: i32 = -64;
/// 字形图集边长(像素,R8),与 Go `glyphAtlasSize` 一致。
pub const GLYPH_ATLAS_SIZE: u32 = 1024;

/// 渲染器创建失败的稳定原因,FFI 层转错误状态码。
#[derive(Debug)]
pub enum RenderCreateError {
    /// 本机无可用 GPU 适配器(CI 容器常见),调用方应跳过而非失败。
    Adapter,
    /// 设备创建失败。
    Device,
}

/// 一帧渲染输入:相机、昼夜与 Go 侧算好的可见 section 列表。
/// 字段语义与 Go `render.Camera` 一致。
#[derive(Default)]
pub struct FrameInput {
    /// 视图投影矩阵(列主序,与 mgl32 内存布局一致)。
    pub view_proj: [f32; 16],
    /// 视图投影逆矩阵,sky pass 使用。
    pub view_proj_inv: [f32; 16],
    /// 相机世界位置。
    pub pos: [f32; 3],
    /// 昼夜亮度 `0..1`。
    pub daylight: f32,
    /// 太阳方向。
    pub sun_direction: [f32; 3],
    /// 星空可见度。
    pub star_visibility: f32,
    /// 天空背景色(render pass clear 值)。
    pub sky_color: [f32; 4],
    /// 云宏观偏移(u32 环绕计数)。
    pub cloud_macro_x: u32,
    /// 云局部偏移。
    pub cloud_local: f32,
    /// 可见 section 位置(BFS+frustum 结果),渲染按此构建候选 record。
    pub visible: Vec<(i32, i32, i32)>,
    /// avatar instance 字节流(布局与 Go encodeAvatarPartsInto 一致);
    /// 空表示本帧无 avatar。
    pub avatar_instances: Vec<u8>,
    /// 掉落物 instance 字节流(与 avatar 同布局);空表示本帧无掉落物。
    pub drop_instances: Vec<u8>,
    /// 目标方块轮廓参数字节;空表示本帧无轮廓。
    pub outline: Vec<u8>,
    /// 伤害红边强度(0 表示不绘制)。
    pub overlay_strength: f32,
    /// 名牌 billboard 顶点流;空表示本帧无名牌。
    pub name_tag_vertices: Vec<u8>,
    /// HUD 屏幕空间顶点流;空表示本帧无 HUD。
    pub hud_vertices: Vec<u8>,
    /// 调试面板顶点流;空表示本帧无面板。
    pub debug_vertices: Vec<u8>,
}

impl FrameInput {
    /// 纯地形帧(v1 语义):全部 pass 段为空。
    pub fn empty_passes(&self) -> bool {
        self.avatar_instances.is_empty()
            && self.drop_instances.is_empty()
            && self.outline.is_empty()
            && self.overlay_strength == 0.0
            && self.name_tag_vertices.is_empty()
            && self.hud_vertices.is_empty()
            && self.debug_vertices.is_empty()
    }
}

/// 一个已上传 section:池内分配、origin 槽位与面数。
struct SectionSlot {
    alloc: Alloc,
    origin_idx: u32,
    face_count: u32,
}

/// HiZ 金字塔,镜像 Go `hiz.go`。
struct HiZ {
    full_view: wgpu::TextureView,
    views: Vec<wgpu::TextureView>,
    copy_pipeline: wgpu::ComputePipeline,
    build_pipeline: wgpu::ComputePipeline,
    copy_uniforms: wgpu::Buffer,
    copy_layout: wgpu::BindGroupLayout,
    build_binds: Vec<wgpu::BindGroup>,
    viewport_w: u32,
    viewport_h: u32,
    padded_w: u32,
    padded_h: u32,
    levels: u32,
    valid: bool,
}

fn bits_needed(mut v: u32) -> u32 {
    let mut n = 0;
    while v > 0 {
        n += 1;
        v >>= 1;
    }
    n
}

impl HiZ {
    fn new(device: &wgpu::Device, queue: &wgpu::Queue, w: u32, h: u32) -> Self {
        let padded_w = w.max(1).next_power_of_two();
        let padded_h = h.max(1).next_power_of_two();
        let levels = bits_needed(padded_w.max(padded_h));
        let tex = device.create_texture(&wgpu::TextureDescriptor {
            label: Some("hi-z pyramid"),
            size: wgpu::Extent3d {
                width: padded_w,
                height: padded_h,
                depth_or_array_layers: 1,
            },
            mip_level_count: levels,
            sample_count: 1,
            dimension: wgpu::TextureDimension::D2,
            format: wgpu::TextureFormat::R32Float,
            usage: wgpu::TextureUsages::TEXTURE_BINDING | wgpu::TextureUsages::STORAGE_BINDING,
            view_formats: &[],
        });
        let full_view = tex.create_view(&wgpu::TextureViewDescriptor::default());
        let views: Vec<_> = (0..levels)
            .map(|level| {
                tex.create_view(&wgpu::TextureViewDescriptor {
                    base_mip_level: level,
                    mip_level_count: Some(1),
                    ..Default::default()
                })
            })
            .collect();

        let copy_uniforms = device.create_buffer(&wgpu::BufferDescriptor {
            label: Some("hi-z copy uniforms"),
            size: 8,
            usage: wgpu::BufferUsages::UNIFORM | wgpu::BufferUsages::COPY_DST,
            mapped_at_creation: false,
        });
        queue.write_buffer(&copy_uniforms, 0, &{
            let mut bytes = [0u8; 8];
            bytes[0..4].copy_from_slice(&w.to_le_bytes());
            bytes[4..8].copy_from_slice(&h.to_le_bytes());
            bytes
        });

        let copy_layout = device.create_bind_group_layout(&wgpu::BindGroupLayoutDescriptor {
            label: Some("hi-z copy layout"),
            entries: &[
                wgpu::BindGroupLayoutEntry {
                    binding: 0,
                    visibility: wgpu::ShaderStages::COMPUTE,
                    ty: wgpu::BindingType::Texture {
                        sample_type: wgpu::TextureSampleType::Depth,
                        view_dimension: wgpu::TextureViewDimension::D2,
                        multisampled: false,
                    },
                    count: None,
                },
                wgpu::BindGroupLayoutEntry {
                    binding: 1,
                    visibility: wgpu::ShaderStages::COMPUTE,
                    ty: wgpu::BindingType::StorageTexture {
                        access: wgpu::StorageTextureAccess::WriteOnly,
                        format: wgpu::TextureFormat::R32Float,
                        view_dimension: wgpu::TextureViewDimension::D2,
                    },
                    count: None,
                },
                buffer_layout_entry(
                    2,
                    wgpu::ShaderStages::COMPUTE,
                    wgpu::BufferBindingType::Uniform,
                ),
            ],
        });
        let copy_pipeline =
            make_compute_pipeline(device, "hi-z copy depth", shaders::HIZ_COPY, &copy_layout);

        let build_layout = device.create_bind_group_layout(&wgpu::BindGroupLayoutDescriptor {
            label: Some("hi-z build layout"),
            entries: &[
                wgpu::BindGroupLayoutEntry {
                    binding: 0,
                    visibility: wgpu::ShaderStages::COMPUTE,
                    ty: wgpu::BindingType::Texture {
                        sample_type: wgpu::TextureSampleType::Float { filterable: false },
                        view_dimension: wgpu::TextureViewDimension::D2,
                        multisampled: false,
                    },
                    count: None,
                },
                wgpu::BindGroupLayoutEntry {
                    binding: 1,
                    visibility: wgpu::ShaderStages::COMPUTE,
                    ty: wgpu::BindingType::StorageTexture {
                        access: wgpu::StorageTextureAccess::WriteOnly,
                        format: wgpu::TextureFormat::R32Float,
                        view_dimension: wgpu::TextureViewDimension::D2,
                    },
                    count: None,
                },
            ],
        });
        let build_pipeline =
            make_compute_pipeline(device, "hi-z reduce", shaders::HIZ_BUILD, &build_layout);
        let build_binds: Vec<_> = (1..levels as usize)
            .map(|level| {
                device.create_bind_group(&wgpu::BindGroupDescriptor {
                    label: Some("hi-z reduce level"),
                    layout: &build_layout,
                    entries: &[
                        wgpu::BindGroupEntry {
                            binding: 0,
                            resource: wgpu::BindingResource::TextureView(&views[level - 1]),
                        },
                        wgpu::BindGroupEntry {
                            binding: 1,
                            resource: wgpu::BindingResource::TextureView(&views[level]),
                        },
                    ],
                })
            })
            .collect();

        Self {
            full_view,
            views,
            copy_pipeline,
            build_pipeline,
            copy_uniforms,
            copy_layout,
            build_binds,
            viewport_w: w,
            viewport_h: h,
            padded_w,
            padded_h,
            levels,
            valid: false,
        }
    }

    /// 在 terrain pass 之后录制,生成供下一帧使用的金字塔;镜像 Go `build`。
    fn build(
        &mut self,
        device: &wgpu::Device,
        encoder: &mut wgpu::CommandEncoder,
        depth: &wgpu::TextureView,
    ) {
        let copy_bind = device.create_bind_group(&wgpu::BindGroupDescriptor {
            label: Some("hi-z depth source"),
            layout: &self.copy_layout,
            entries: &[
                wgpu::BindGroupEntry {
                    binding: 0,
                    resource: wgpu::BindingResource::TextureView(depth),
                },
                wgpu::BindGroupEntry {
                    binding: 1,
                    resource: wgpu::BindingResource::TextureView(&self.views[0]),
                },
                wgpu::BindGroupEntry {
                    binding: 2,
                    resource: self.copy_uniforms.as_entire_binding(),
                },
            ],
        });
        {
            let mut pass = encoder.begin_compute_pass(&wgpu::ComputePassDescriptor {
                label: Some("hi-z copy pass"),
                timestamp_writes: None,
            });
            pass.set_pipeline(&self.copy_pipeline);
            pass.set_bind_group(0, &copy_bind, &[]);
            pass.dispatch_workgroups(self.padded_w.div_ceil(8), self.padded_h.div_ceil(8), 1);
        }
        let mut w = self.padded_w;
        let mut h = self.padded_h;
        for bind in &self.build_binds {
            w = (w / 2).max(1);
            h = (h / 2).max(1);
            let mut pass = encoder.begin_compute_pass(&wgpu::ComputePassDescriptor {
                label: Some("hi-z reduce pass"),
                timestamp_writes: None,
            });
            pass.set_pipeline(&self.build_pipeline);
            pass.set_bind_group(0, bind, &[]);
            pass.dispatch_workgroups(w.div_ceil(8), h.div_ceil(8), 1);
        }
        self.valid = true;
    }
}

/// 离屏世界渲染器。
pub struct OffscreenRenderer {
    device: wgpu::Device,
    queue: wgpu::Queue,
    width: u32,
    height: u32,
    color: wgpu::Texture,
    color_view: wgpu::TextureView,
    depth_view: wgpu::TextureView,

    faces: wgpu::Buffer,
    instances: wgpu::Buffer,
    origins: wgpu::Buffer,
    camera: wgpu::Buffer,
    sky_camera: wgpu::Buffer,
    indirect: wgpu::Buffer,
    index: wgpu::Buffer,
    zero_args: wgpu::Buffer,

    terrain_layout: wgpu::BindGroupLayout,
    terrain_pipeline: wgpu::RenderPipeline,
    terrain_bind: Option<wgpu::BindGroup>,
    sampler: wgpu::Sampler,
    sky_pipeline: wgpu::RenderPipeline,
    sky_bind: wgpu::BindGroup,

    cull_pipeline: wgpu::ComputePipeline,
    cull_layout: wgpu::BindGroupLayout,
    cull_uniforms: wgpu::Buffer,
    cull_sections: wgpu::Buffer,
    cull_bind: wgpu::BindGroup,
    dummy_hiz_view: wgpu::TextureView,
    cull_uses_hiz: bool,

    hiz: HiZ,

    /// avatar pass(11 具身体 × 6 部件容量)。
    avatar_pass: EntityPass,
    /// 掉落物 pass(800 实例容量,与 avatar 同 shader)。
    drop_pass: EntityPass,

    /// 字形图集(R8,增量矩形上传);内容全部来自 Go 光栅化 worker。
    glyph_atlas: wgpu::Texture,
    /// HUD 图集;None 表示尚未上传。
    hud_atlas: Option<wgpu::Texture>,

    pool: Pool,
    sections: HashMap<(i32, i32, i32), SectionSlot>,
    next_origin: u32,
    free_origins: Vec<u32>,

    have_last_camera: bool,
    last_pos: [f32; 3],
    last_view_proj: [f32; 16],
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

        let make_target = |format, usage, label: &str| {
            device.create_texture(&wgpu::TextureDescriptor {
                label: Some(label),
                size: wgpu::Extent3d {
                    width,
                    height,
                    depth_or_array_layers: 1,
                },
                mip_level_count: 1,
                sample_count: 1,
                dimension: wgpu::TextureDimension::D2,
                format,
                usage,
                view_formats: &[],
            })
        };
        let color = make_target(
            COLOR_FORMAT,
            wgpu::TextureUsages::RENDER_ATTACHMENT | wgpu::TextureUsages::COPY_SRC,
            "offscreen color",
        );
        let depth = make_target(
            DEPTH_FORMAT,
            wgpu::TextureUsages::RENDER_ATTACHMENT | wgpu::TextureUsages::TEXTURE_BINDING,
            "offscreen depth",
        );
        let color_view = color.create_view(&wgpu::TextureViewDescriptor::default());
        let depth_view = depth.create_view(&wgpu::TextureViewDescriptor::default());

        use wgpu::BufferUsages as BU;
        let make_buffer = |size: u64, usage, label: &str| {
            device.create_buffer(&wgpu::BufferDescriptor {
                label: Some(label),
                size,
                usage,
                mapped_at_creation: false,
            })
        };
        let faces = make_buffer(
            u64::from(POOL_FACES) * BYTES_PER_POOL_FACE,
            BU::STORAGE | BU::COPY_DST | BU::COPY_SRC,
            "terrain face pool",
        );
        let instances = make_buffer(
            u64::from(POOL_FACES) * BYTES_PER_VISIBLE_FACE,
            BU::STORAGE | BU::COPY_DST,
            "terrain visible instances",
        );
        let origins = make_buffer(
            u64::from(ORIGIN_SLOTS) * 16,
            BU::STORAGE | BU::COPY_DST,
            "terrain section origins",
        );
        let camera = make_buffer(80, BU::UNIFORM | BU::COPY_DST, "terrain camera");
        let sky_camera = make_buffer(112, BU::UNIFORM | BU::COPY_DST, "sky uniform");
        let indirect = make_buffer(
            20,
            BU::INDIRECT | BU::STORAGE | BU::COPY_DST,
            "terrain indirect args",
        );
        let index = make_buffer(24, BU::INDEX | BU::COPY_DST, "terrain quad indices");
        queue.write_buffer(&index, 0, &u32s_to_bytes(&[0, 1, 2, 0, 2, 3]));
        let zero_args = make_buffer(
            20,
            BU::COPY_SRC | BU::COPY_DST,
            "terrain zero indirect template",
        );
        queue.write_buffer(&zero_args, 0, &u32s_to_bytes(&[6, 0, 0, 0, 0]));

        // terrain pipeline 与 bind group layout,镜像 Go `terrain layout`。
        let terrain_layout = device.create_bind_group_layout(&wgpu::BindGroupLayoutDescriptor {
            label: Some("terrain layout"),
            entries: &[
                buffer_layout_entry(
                    0,
                    wgpu::ShaderStages::VERTEX,
                    wgpu::BufferBindingType::Uniform,
                ),
                buffer_layout_entry(
                    1,
                    wgpu::ShaderStages::VERTEX,
                    wgpu::BufferBindingType::Storage { read_only: true },
                ),
                buffer_layout_entry(
                    2,
                    wgpu::ShaderStages::VERTEX,
                    wgpu::BufferBindingType::Storage { read_only: true },
                ),
                wgpu::BindGroupLayoutEntry {
                    binding: 3,
                    visibility: wgpu::ShaderStages::FRAGMENT,
                    ty: wgpu::BindingType::Texture {
                        sample_type: wgpu::TextureSampleType::Float { filterable: true },
                        view_dimension: wgpu::TextureViewDimension::D2Array,
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
            ],
        });
        let terrain_module = device.create_shader_module(wgpu::ShaderModuleDescriptor {
            label: Some("terrain"),
            source: wgpu::ShaderSource::Wgsl(shaders::TERRAIN.into()),
        });
        let terrain_pipeline =
            make_render_pipeline(&device, "terrain", &terrain_module, &terrain_layout, true);
        // 采样器参数与 Go `block-sampler` 一致。
        let sampler = device.create_sampler(&wgpu::SamplerDescriptor {
            label: Some("block-sampler"),
            address_mode_u: wgpu::AddressMode::Repeat,
            address_mode_v: wgpu::AddressMode::Repeat,
            address_mode_w: wgpu::AddressMode::Repeat,
            mag_filter: wgpu::FilterMode::Nearest,
            min_filter: wgpu::FilterMode::Linear,
            mipmap_filter: wgpu::MipmapFilterMode::Linear,
            ..Default::default()
        });

        // sky pipeline,镜像 Go `sky layout`(uniform 对 VS+FS 可见,不写深度)。
        let sky_layout = device.create_bind_group_layout(&wgpu::BindGroupLayoutDescriptor {
            label: Some("sky layout"),
            entries: &[buffer_layout_entry(
                0,
                wgpu::ShaderStages::VERTEX | wgpu::ShaderStages::FRAGMENT,
                wgpu::BufferBindingType::Uniform,
            )],
        });
        let sky_module = device.create_shader_module(wgpu::ShaderModuleDescriptor {
            label: Some("sky"),
            source: wgpu::ShaderSource::Wgsl(shaders::SKY.into()),
        });
        let sky_pipeline = make_render_pipeline(&device, "sky", &sky_module, &sky_layout, false);
        let sky_bind = device.create_bind_group(&wgpu::BindGroupDescriptor {
            label: Some("sky resources"),
            layout: &sky_layout,
            entries: &[wgpu::BindGroupEntry {
                binding: 0,
                resource: sky_camera.as_entire_binding(),
            }],
        });

        // cull compute,镜像 Go `terrain cull layout`。
        let cull_layout = device.create_bind_group_layout(&wgpu::BindGroupLayoutDescriptor {
            label: Some("terrain cull layout"),
            entries: &[
                buffer_layout_entry(
                    0,
                    wgpu::ShaderStages::COMPUTE,
                    wgpu::BufferBindingType::Uniform,
                ),
                buffer_layout_entry(
                    1,
                    wgpu::ShaderStages::COMPUTE,
                    wgpu::BufferBindingType::Storage { read_only: true },
                ),
                buffer_layout_entry(
                    2,
                    wgpu::ShaderStages::COMPUTE,
                    wgpu::BufferBindingType::Storage { read_only: true },
                ),
                buffer_layout_entry(
                    3,
                    wgpu::ShaderStages::COMPUTE,
                    wgpu::BufferBindingType::Storage { read_only: false },
                ),
                buffer_layout_entry(
                    4,
                    wgpu::ShaderStages::COMPUTE,
                    wgpu::BufferBindingType::Storage { read_only: false },
                ),
                wgpu::BindGroupLayoutEntry {
                    binding: 5,
                    visibility: wgpu::ShaderStages::COMPUTE,
                    ty: wgpu::BindingType::Texture {
                        sample_type: wgpu::TextureSampleType::Float { filterable: false },
                        view_dimension: wgpu::TextureViewDimension::D2,
                        multisampled: false,
                    },
                    count: None,
                },
            ],
        });
        let cull_pipeline =
            make_compute_pipeline(&device, "terrain cull", shaders::CULL, &cull_layout);
        let cull_uniforms = make_buffer(128, BU::UNIFORM | BU::COPY_DST, "cull uniforms");
        let cull_sections = make_buffer(
            u64::from(ORIGIN_SLOTS) * SECTION_RECORD_BYTES as u64,
            BU::STORAGE | BU::COPY_DST,
            "cull candidate sections",
        );
        // 1×1 值为 1.0 的 dummy HiZ,禁用帧绑定它(镜像 Go dummyHiZ)。
        let dummy_hiz = device.create_texture(&wgpu::TextureDescriptor {
            label: Some("cull dummy hi-z"),
            size: wgpu::Extent3d {
                width: 1,
                height: 1,
                depth_or_array_layers: 1,
            },
            mip_level_count: 1,
            sample_count: 1,
            dimension: wgpu::TextureDimension::D2,
            format: wgpu::TextureFormat::R32Float,
            usage: wgpu::TextureUsages::TEXTURE_BINDING | wgpu::TextureUsages::COPY_DST,
            view_formats: &[],
        });
        queue.write_texture(
            wgpu::TexelCopyTextureInfo {
                texture: &dummy_hiz,
                mip_level: 0,
                origin: wgpu::Origin3d::ZERO,
                aspect: wgpu::TextureAspect::All,
            },
            &1.0f32.to_le_bytes(),
            wgpu::TexelCopyBufferLayout {
                offset: 0,
                bytes_per_row: Some(4),
                rows_per_image: Some(1),
            },
            wgpu::Extent3d {
                width: 1,
                height: 1,
                depth_or_array_layers: 1,
            },
        );
        let dummy_hiz_view = dummy_hiz.create_view(&wgpu::TextureViewDescriptor::default());

        let avatar_module = device.create_shader_module(wgpu::ShaderModuleDescriptor {
            label: Some("avatar"),
            source: wgpu::ShaderSource::Wgsl(shaders::AVATAR.into()),
        });
        let avatar_pass = EntityPass::new(
            &device,
            &queue,
            &avatar_module,
            "avatar",
            entity::AVATAR_MAX_INSTANCES,
            COLOR_FORMAT,
            DEPTH_FORMAT,
        );
        let drop_pass = EntityPass::new(
            &device,
            &queue,
            &avatar_module,
            "item drop",
            entity::DROP_MAX_INSTANCES,
            COLOR_FORMAT,
            DEPTH_FORMAT,
        );

        let glyph_atlas = device.create_texture(&wgpu::TextureDescriptor {
            label: Some("glyph-atlas"),
            size: wgpu::Extent3d {
                width: GLYPH_ATLAS_SIZE,
                height: GLYPH_ATLAS_SIZE,
                depth_or_array_layers: 1,
            },
            mip_level_count: 1,
            sample_count: 1,
            dimension: wgpu::TextureDimension::D2,
            format: wgpu::TextureFormat::R8Unorm,
            usage: wgpu::TextureUsages::TEXTURE_BINDING | wgpu::TextureUsages::COPY_DST,
            view_formats: &[],
        });

        let hiz = HiZ::new(&device, &queue, width, height);
        let cull_bind = make_cull_bind(
            &device,
            &cull_layout,
            &cull_uniforms,
            &cull_sections,
            &faces,
            &instances,
            &indirect,
            &dummy_hiz_view,
        );

        Ok(Self {
            device,
            queue,
            width,
            height,
            color,
            color_view,
            depth_view,
            faces,
            instances,
            origins,
            camera,
            sky_camera,
            indirect,
            index,
            zero_args,
            terrain_layout,
            terrain_pipeline,
            terrain_bind: None,
            sampler,
            sky_pipeline,
            sky_bind,
            cull_pipeline,
            cull_layout,
            cull_uniforms,
            cull_sections,
            cull_bind,
            dummy_hiz_view,
            cull_uses_hiz: false,
            hiz,
            avatar_pass,
            drop_pass,
            glyph_atlas,
            hud_atlas: None,
            pool: Pool::new(POOL_FACES),
            sections: HashMap::new(),
            next_origin: 0,
            free_origins: Vec::new(),
            have_last_camera: false,
            last_pos: [0.0; 3],
            last_view_proj: [0.0; 16],
        })
    }

    /// 上传材质 atlas:`pixels` 为逐 layer、逐 mip 拼接的 RGBA 字节
    /// (与 Go `Registry.UploadTo` 写入 GPU 的字节完全一致)。
    pub fn upload_atlas(&mut self, layers: u32, pixels: &[u8]) -> bool {
        let bytes_per_layer: usize = (0..ATLAS_MIPS)
            .map(|mip| {
                let size = (ATLAS_TEX_SIZE >> mip).max(1) as usize;
                size * size * 4
            })
            .sum();
        if layers == 0 || pixels.len() != bytes_per_layer * layers as usize {
            return false;
        }
        let atlas = self.device.create_texture(&wgpu::TextureDescriptor {
            label: Some("block-textures"),
            size: wgpu::Extent3d {
                width: ATLAS_TEX_SIZE,
                height: ATLAS_TEX_SIZE,
                depth_or_array_layers: layers,
            },
            mip_level_count: ATLAS_MIPS,
            sample_count: 1,
            dimension: wgpu::TextureDimension::D2,
            format: wgpu::TextureFormat::Rgba8Unorm,
            usage: wgpu::TextureUsages::TEXTURE_BINDING | wgpu::TextureUsages::COPY_DST,
            view_formats: &[],
        });
        let mut offset = 0usize;
        for layer in 0..layers {
            for mip in 0..ATLAS_MIPS {
                let size = (ATLAS_TEX_SIZE >> mip).max(1);
                let bytes = (size * size * 4) as usize;
                self.queue.write_texture(
                    wgpu::TexelCopyTextureInfo {
                        texture: &atlas,
                        mip_level: mip,
                        origin: wgpu::Origin3d {
                            x: 0,
                            y: 0,
                            z: layer,
                        },
                        aspect: wgpu::TextureAspect::All,
                    },
                    &pixels[offset..offset + bytes],
                    wgpu::TexelCopyBufferLayout {
                        offset: 0,
                        bytes_per_row: Some(size * 4),
                        rows_per_image: Some(size),
                    },
                    wgpu::Extent3d {
                        width: size,
                        height: size,
                        depth_or_array_layers: 1,
                    },
                );
                offset += bytes;
            }
        }
        let atlas_view = atlas.create_view(&wgpu::TextureViewDescriptor {
            dimension: Some(wgpu::TextureViewDimension::D2Array),
            ..Default::default()
        });
        self.terrain_bind = Some(self.device.create_bind_group(&wgpu::BindGroupDescriptor {
            label: Some("terrain resources"),
            layout: &self.terrain_layout,
            entries: &[
                wgpu::BindGroupEntry {
                    binding: 0,
                    resource: self.camera.as_entire_binding(),
                },
                wgpu::BindGroupEntry {
                    binding: 1,
                    resource: self.instances.as_entire_binding(),
                },
                wgpu::BindGroupEntry {
                    binding: 2,
                    resource: self.origins.as_entire_binding(),
                },
                wgpu::BindGroupEntry {
                    binding: 3,
                    resource: wgpu::BindingResource::TextureView(&atlas_view),
                },
                wgpu::BindGroupEntry {
                    binding: 4,
                    resource: wgpu::BindingResource::Sampler(&self.sampler),
                },
            ],
        }));
        true
    }

    /// 上传/替换一个 section 的 packed face 字节(8 字节/面,已 Pack);
    /// 空数据等价于 drop。返回 false 表示池或 origin 槽位不足。
    /// 分配策略镜像 Go `uploadOne`。
    pub fn upload_section(&mut self, pos: (i32, i32, i32), packed: &[u8]) -> bool {
        debug_assert_eq!(packed.len() % 8, 0);
        if packed.is_empty() {
            self.drop_section(pos);
            return true;
        }
        let required = (packed.len() / 8) as u32;
        let old = self
            .sections
            .get(&pos)
            .map(|slot| (slot.origin_idx, slot.alloc));
        let (alloc, origin_idx) = match old {
            Some((origin_idx, old_alloc)) if required <= old_alloc.size => (old_alloc, origin_idx),
            Some((origin_idx, old_alloc)) => match self.pool.alloc(required) {
                Some(alloc) => {
                    self.pool.free(old_alloc);
                    (alloc, origin_idx)
                }
                None => {
                    self.pool.free(old_alloc);
                    self.sections.remove(&pos);
                    match self.pool.alloc(required) {
                        Some(alloc) => (alloc, origin_idx),
                        None => {
                            self.free_origins.push(origin_idx);
                            return false;
                        }
                    }
                }
            },
            None => {
                let Some(alloc) = self.pool.alloc(required) else {
                    return false;
                };
                match self.take_origin() {
                    Some(origin_idx) => (alloc, origin_idx),
                    None => {
                        self.pool.free(alloc);
                        return false;
                    }
                }
            }
        };
        self.queue.write_buffer(
            &self.faces,
            u64::from(alloc.offset) * BYTES_PER_POOL_FACE,
            packed,
        );
        let origin = section_origin(pos);
        let mut origin_bytes = [0u8; 16];
        for (i, v) in origin.iter().enumerate() {
            origin_bytes[i * 4..i * 4 + 4].copy_from_slice(&v.to_le_bytes());
        }
        self.queue
            .write_buffer(&self.origins, u64::from(origin_idx) * 16, &origin_bytes);
        self.sections.insert(
            pos,
            SectionSlot {
                alloc,
                origin_idx,
                face_count: required,
            },
        );
        true
    }

    fn take_origin(&mut self) -> Option<u32> {
        if let Some(idx) = self.free_origins.pop() {
            return Some(idx);
        }
        if self.next_origin >= ORIGIN_SLOTS {
            return None;
        }
        let idx = self.next_origin;
        self.next_origin += 1;
        Some(idx)
    }

    /// 丢弃一个 section;不存在时为幂等空操作。
    pub fn drop_section(&mut self, pos: (i32, i32, i32)) {
        if let Some(slot) = self.sections.remove(&pos) {
            self.pool.free(slot.alloc);
            self.free_origins.push(slot.origin_idx);
        }
    }

    /// 上传字形图集的一块 R8 矩形;越界或长度不符返回 false。
    /// 内容必须与 Go `GlyphAtlas` 写入自身纹理的字节一致(单源约定)。
    pub fn upload_glyph_rect(&mut self, x: u32, y: u32, w: u32, h: u32, pixels: &[u8]) -> bool {
        if w == 0
            || h == 0
            || x.checked_add(w).is_none_or(|edge| edge > GLYPH_ATLAS_SIZE)
            || y.checked_add(h).is_none_or(|edge| edge > GLYPH_ATLAS_SIZE)
            || pixels.len() != (w * h) as usize
        {
            return false;
        }
        self.queue.write_texture(
            wgpu::TexelCopyTextureInfo {
                texture: &self.glyph_atlas,
                mip_level: 0,
                origin: wgpu::Origin3d { x, y, z: 0 },
                aspect: wgpu::TextureAspect::All,
            },
            pixels,
            wgpu::TexelCopyBufferLayout {
                offset: 0,
                bytes_per_row: Some(w),
                rows_per_image: Some(h),
            },
            wgpu::Extent3d {
                width: w,
                height: h,
                depth_or_array_layers: 1,
            },
        );
        true
    }

    /// 上传 HUD 图集(一次性 RGBA;重复上传替换);长度不符返回 false。
    pub fn upload_hud_atlas(&mut self, width: u32, height: u32, pixels: &[u8]) -> bool {
        if width == 0 || height == 0 || pixels.len() != (width * height * 4) as usize {
            return false;
        }
        let texture = self.device.create_texture(&wgpu::TextureDescriptor {
            label: Some("hotbar texture atlas"),
            size: wgpu::Extent3d {
                width,
                height,
                depth_or_array_layers: 1,
            },
            mip_level_count: 1,
            sample_count: 1,
            dimension: wgpu::TextureDimension::D2,
            format: wgpu::TextureFormat::Rgba8Unorm,
            usage: wgpu::TextureUsages::TEXTURE_BINDING | wgpu::TextureUsages::COPY_DST,
            view_formats: &[],
        });
        self.queue.write_texture(
            wgpu::TexelCopyTextureInfo {
                texture: &texture,
                mip_level: 0,
                origin: wgpu::Origin3d::ZERO,
                aspect: wgpu::TextureAspect::All,
            },
            pixels,
            wgpu::TexelCopyBufferLayout {
                offset: 0,
                bytes_per_row: Some(width * 4),
                rows_per_image: Some(height),
            },
            wgpu::Extent3d {
                width,
                height,
                depth_or_array_layers: 1,
            },
        );
        self.hud_atlas = Some(texture);
        true
    }

    /// 输出图像的精确字节数(width×height×4),FFI 回读长度校验使用。
    pub fn output_bytes(&self) -> usize {
        (self.width * self.height * 4) as usize
    }

    /// 已上传 section 的面总数,供测试断言。
    pub fn total_faces(&self) -> u64 {
        self.sections
            .values()
            .map(|slot| u64::from(slot.face_count))
            .sum()
    }

    /// 渲染一帧,pass 顺序镜像 Go `Render`:
    /// 候选 record → uniform → 清零 indirect → cull(可选 HiZ)→
    /// render pass(clear 天空色 → sky → terrain indirect)→ HiZ build。
    pub fn render_frame(&mut self, input: &FrameInput) -> bool {
        // 语义校验先于任何 GPU 写入:非法 pass 段拒绝且不触碰 target。
        if !self.avatar_pass.instances_valid(&input.avatar_instances)
            || !self.drop_pass.instances_valid(&input.drop_instances)
        {
            return false;
        }
        // 构建候选 record(编码镜像 Go sectionRecords)。
        let mut records: Vec<u8> = Vec::with_capacity(input.visible.len() * SECTION_RECORD_BYTES);
        let mut candidates = 0u32;
        for pos in &input.visible {
            let Some(slot) = self.sections.get(pos) else {
                continue;
            };
            for v in section_origin(*pos) {
                records.extend_from_slice(&v.to_le_bytes());
            }
            records.extend_from_slice(&slot.alloc.offset.to_le_bytes());
            records.extend_from_slice(&slot.face_count.to_le_bytes());
            records.extend_from_slice(&slot.origin_idx.to_le_bytes());
            records.extend_from_slice(&0u32.to_le_bytes());
            candidates += 1;
        }
        if !records.is_empty() {
            self.queue.write_buffer(&self.cull_sections, 0, &records);
        }

        // camera / sky uniform(布局镜像 writeCameraBytes / writeSkyCameraBytes)。
        let mut camera_data = [0u8; 80];
        write_f32s(&mut camera_data, 0, &input.view_proj);
        write_f32s(
            &mut camera_data,
            64,
            &[input.pos[0], input.pos[1], input.pos[2], input.daylight],
        );
        self.queue.write_buffer(&self.camera, 0, &camera_data);
        let mut sky_data = [0u8; 112];
        write_f32s(&mut sky_data, 0, &input.view_proj_inv);
        write_f32s(
            &mut sky_data,
            64,
            &[
                input.sun_direction[0],
                input.sun_direction[1],
                input.sun_direction[2],
                input.daylight,
                input.star_visibility,
            ],
        );
        sky_data[84..88].copy_from_slice(&input.cloud_macro_x.to_le_bytes());
        write_f32s(
            &mut sky_data,
            96,
            &[input.pos[0], input.pos[1], input.pos[2], input.cloud_local],
        );
        self.queue.write_buffer(&self.sky_camera, 0, &sky_data);

        // HiZ 启用条件镜像 Go:金字塔有效且相机稳定(保守:任何可辨认的
        // 变化都禁用一帧,只会少剔除,不会制造破洞)。
        let camera_stable = self.have_last_camera
            && vec3_len(sub3(input.pos, self.last_pos)) <= 1.0
            && mat_approx_equal(&input.view_proj, &self.last_view_proj, 1e-5);
        let use_hiz = self.hiz.valid && camera_stable;

        // cull uniform(布局镜像 writeCullUniformBytes)。
        let mut cull_data = [0u8; 128];
        write_f32s(&mut cull_data, 0, &input.pos);
        write_f32s(&mut cull_data, 16, &input.view_proj);
        write_f32s(
            &mut cull_data,
            80,
            &[
                self.hiz.viewport_w as f32,
                self.hiz.viewport_h as f32,
                (self.hiz.levels - 1) as f32,
            ],
        );
        write_f32s(
            &mut cull_data,
            96,
            &[
                self.hiz.viewport_w as f32 / self.hiz.padded_w as f32,
                self.hiz.viewport_h as f32 / self.hiz.padded_h as f32,
            ],
        );
        if use_hiz {
            cull_data[112..116].copy_from_slice(&1u32.to_le_bytes());
        }
        self.queue.write_buffer(&self.cull_uniforms, 0, &cull_data);

        // cull bind:启用 HiZ 绑真金字塔,否则绑 dummy(镜像 rebuildBind)。
        if use_hiz != self.cull_uses_hiz {
            let hiz_view = if use_hiz {
                &self.hiz.full_view
            } else {
                &self.dummy_hiz_view
            };
            self.cull_bind = make_cull_bind(
                &self.device,
                &self.cull_layout,
                &self.cull_uniforms,
                &self.cull_sections,
                &self.faces,
                &self.instances,
                &self.indirect,
                hiz_view,
            );
            self.cull_uses_hiz = use_hiz;
        }

        let mut encoder = self
            .device
            .create_command_encoder(&wgpu::CommandEncoderDescriptor {
                label: Some("frame"),
            });
        encoder.copy_buffer_to_buffer(&self.zero_args, 0, &self.indirect, 0, 20);
        if candidates > 0 {
            let mut pass = encoder.begin_compute_pass(&wgpu::ComputePassDescriptor {
                label: Some("terrain cull pass"),
                timestamp_writes: None,
            });
            pass.set_pipeline(&self.cull_pipeline);
            pass.set_bind_group(0, &self.cull_bind, &[]);
            pass.dispatch_workgroups(candidates, 1, 1);
        }
        {
            let mut pass = encoder.begin_render_pass(&wgpu::RenderPassDescriptor {
                label: Some("terrain pass"),
                color_attachments: &[Some(wgpu::RenderPassColorAttachment {
                    view: &self.color_view,
                    depth_slice: None,
                    resolve_target: None,
                    ops: wgpu::Operations {
                        load: wgpu::LoadOp::Clear(wgpu::Color {
                            r: f64::from(input.sky_color[0]),
                            g: f64::from(input.sky_color[1]),
                            b: f64::from(input.sky_color[2]),
                            a: f64::from(input.sky_color[3]),
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
            pass.set_pipeline(&self.sky_pipeline);
            pass.set_bind_group(0, &self.sky_bind, &[]);
            pass.draw(0..3, 0..1);
            if let Some(bind) = &self.terrain_bind {
                pass.set_pipeline(&self.terrain_pipeline);
                pass.set_bind_group(0, bind, &[]);
                pass.set_index_buffer(self.index.slice(..), wgpu::IndexFormat::Uint32);
                pass.draw_indexed_indirect(&self.indirect, 0);
            }
        }
        self.hiz.build(&self.device, &mut encoder, &self.depth_view);
        // 实体 pass:顺序镜像 app_frame(avatar → item drop),空段跳过。
        if !input.avatar_instances.is_empty() {
            self.avatar_pass.upload(
                &self.queue,
                &input.view_proj,
                input.daylight,
                &input.avatar_instances,
            );
            self.avatar_pass.record(
                &mut encoder,
                &self.color_view,
                &self.depth_view,
                "avatar pass",
            );
        }
        if !input.drop_instances.is_empty() {
            self.drop_pass.upload(
                &self.queue,
                &input.view_proj,
                input.daylight,
                &input.drop_instances,
            );
            self.drop_pass.record(
                &mut encoder,
                &self.color_view,
                &self.depth_view,
                "item drop pass",
            );
        }
        self.last_pos = input.pos;
        self.last_view_proj = input.view_proj;
        self.have_last_camera = true;
        self.queue.submit([encoder.finish()]);
        true
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

/// section 最小角世界坐标,与 Go `SectionPos.MinCorner` 一致:
/// X/Z 直接 ×16,Y 槽位从 core.MinY 起。
fn section_origin(pos: (i32, i32, i32)) -> [i32; 4] {
    [pos.0 * 16, pos.1 * 16 + WORLD_MIN_Y, pos.2 * 16, 0]
}

/// 便捷:uniform/storage buffer 的 layout entry。
fn buffer_layout_entry(
    binding: u32,
    visibility: wgpu::ShaderStages,
    ty: wgpu::BufferBindingType,
) -> wgpu::BindGroupLayoutEntry {
    wgpu::BindGroupLayoutEntry {
        binding,
        visibility,
        ty: wgpu::BindingType::Buffer {
            ty,
            has_dynamic_offset: false,
            min_binding_size: None,
        },
        count: None,
    }
}

/// 渲染管线构造,状态镜像 Go `CreateRenderPipeline` 缺省语义:
/// TriangleList、CCW、无背面剔除、BlendReplace、depth compare Less。
fn make_render_pipeline(
    device: &wgpu::Device,
    label: &str,
    module: &wgpu::ShaderModule,
    layout: &wgpu::BindGroupLayout,
    depth_write: bool,
) -> wgpu::RenderPipeline {
    let pipeline_layout = device.create_pipeline_layout(&wgpu::PipelineLayoutDescriptor {
        label: None,
        bind_group_layouts: &[Some(layout)],
        immediate_size: 0,
    });
    device.create_render_pipeline(&wgpu::RenderPipelineDescriptor {
        label: Some(label),
        layout: Some(&pipeline_layout),
        vertex: wgpu::VertexState {
            module,
            entry_point: Some("vs_main"),
            compilation_options: Default::default(),
            buffers: &[],
        },
        fragment: Some(wgpu::FragmentState {
            module,
            entry_point: Some("fs_main"),
            compilation_options: Default::default(),
            targets: &[Some(wgpu::ColorTargetState {
                format: COLOR_FORMAT,
                blend: Some(wgpu::BlendState::REPLACE),
                write_mask: wgpu::ColorWrites::ALL,
            })],
        }),
        primitive: wgpu::PrimitiveState {
            topology: wgpu::PrimitiveTopology::TriangleList,
            front_face: wgpu::FrontFace::Ccw,
            cull_mode: None,
            ..Default::default()
        },
        depth_stencil: Some(wgpu::DepthStencilState {
            format: DEPTH_FORMAT,
            depth_write_enabled: Some(depth_write),
            depth_compare: Some(wgpu::CompareFunction::Less),
            stencil: wgpu::StencilState::default(),
            bias: wgpu::DepthBiasState::default(),
        }),
        multisample: wgpu::MultisampleState::default(),
        multiview_mask: None,
        cache: None,
    })
}

fn make_compute_pipeline(
    device: &wgpu::Device,
    label: &str,
    source: &str,
    layout: &wgpu::BindGroupLayout,
) -> wgpu::ComputePipeline {
    let module = device.create_shader_module(wgpu::ShaderModuleDescriptor {
        label: Some(label),
        source: wgpu::ShaderSource::Wgsl(source.into()),
    });
    let pipeline_layout = device.create_pipeline_layout(&wgpu::PipelineLayoutDescriptor {
        label: None,
        bind_group_layouts: &[Some(layout)],
        immediate_size: 0,
    });
    device.create_compute_pipeline(&wgpu::ComputePipelineDescriptor {
        label: Some(label),
        layout: Some(&pipeline_layout),
        module: &module,
        entry_point: Some("cs_main"),
        compilation_options: Default::default(),
        cache: None,
    })
}

#[allow(clippy::too_many_arguments)]
fn make_cull_bind(
    device: &wgpu::Device,
    layout: &wgpu::BindGroupLayout,
    uniforms: &wgpu::Buffer,
    sections: &wgpu::Buffer,
    faces: &wgpu::Buffer,
    visible: &wgpu::Buffer,
    args: &wgpu::Buffer,
    hiz_view: &wgpu::TextureView,
) -> wgpu::BindGroup {
    device.create_bind_group(&wgpu::BindGroupDescriptor {
        label: Some("terrain cull resources"),
        layout,
        entries: &[
            wgpu::BindGroupEntry {
                binding: 0,
                resource: uniforms.as_entire_binding(),
            },
            wgpu::BindGroupEntry {
                binding: 1,
                resource: sections.as_entire_binding(),
            },
            wgpu::BindGroupEntry {
                binding: 2,
                resource: faces.as_entire_binding(),
            },
            wgpu::BindGroupEntry {
                binding: 3,
                resource: visible.as_entire_binding(),
            },
            wgpu::BindGroupEntry {
                binding: 4,
                resource: args.as_entire_binding(),
            },
            wgpu::BindGroupEntry {
                binding: 5,
                resource: wgpu::BindingResource::TextureView(hiz_view),
            },
        ],
    })
}

fn write_f32s(out: &mut [u8], offset: usize, values: &[f32]) {
    for (i, v) in values.iter().enumerate() {
        out[offset + i * 4..offset + i * 4 + 4].copy_from_slice(&v.to_le_bytes());
    }
}

fn u32s_to_bytes(values: &[u32]) -> Vec<u8> {
    let mut out = Vec::with_capacity(values.len() * 4);
    for v in values {
        out.extend_from_slice(&v.to_le_bytes());
    }
    out
}

fn sub3(a: [f32; 3], b: [f32; 3]) -> [f32; 3] {
    [a[0] - b[0], a[1] - b[1], a[2] - b[2]]
}

fn vec3_len(v: [f32; 3]) -> f32 {
    (v[0] * v[0] + v[1] * v[1] + v[2] * v[2]).sqrt()
}

fn mat_approx_equal(a: &[f32; 16], b: &[f32; 16], epsilon: f32) -> bool {
    a.iter().zip(b).all(|(x, y)| (x - y).abs() <= epsilon)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn renderer_or_skip(width: u32, height: u32) -> Option<OffscreenRenderer> {
        // 与 Go NewHeadlessDevice 的约定一致:无适配器(CI 容器)跳过而非失败。
        OffscreenRenderer::new(width, height).ok()
    }

    fn empty_frame() -> FrameInput {
        let mut identity = [0.0f32; 16];
        for i in 0..4 {
            identity[i * 4 + i] = 1.0;
        }
        FrameInput {
            view_proj: identity,
            view_proj_inv: identity,
            pos: [0.0, 80.0, 0.0],
            daylight: 1.0,
            sun_direction: [0.0, 1.0, 0.0],
            star_visibility: 0.0,
            sky_color: [0.25, 0.5, 1.0, 1.0],
            cloud_macro_x: 0,
            cloud_local: 0.0,
            visible: Vec::new(),
            ..Default::default()
        }
    }

    #[test]
    fn offscreen_frame_and_readback_are_deterministic() {
        let Some(mut renderer) = renderer_or_skip(64, 32) else {
            return;
        };
        renderer.render_frame(&empty_frame());
        let mut first = vec![0u8; 64 * 32 * 4];
        renderer.readback(&mut first);
        assert!(first.iter().any(|&b| b != 0), "sky 渲染后回读不应全零");
        renderer.render_frame(&empty_frame());
        let mut second = vec![0u8; 64 * 32 * 4];
        renderer.readback(&mut second);
        assert_eq!(first, second, "同输入两帧必须逐字节一致");
    }

    #[test]
    fn section_upload_and_drop_mirror_pool_semantics() {
        let Some(mut renderer) = renderer_or_skip(16, 16) else {
            return;
        };
        assert!(renderer.upload_section((0, 4, 0), &[0u8; 16]));
        assert!(renderer.upload_section((0, 5, 0), &[0u8; 8]));
        assert_eq!(renderer.total_faces(), 3);
        // 覆盖上传复用旧槽。
        assert!(renderer.upload_section((0, 4, 0), &[0u8; 8]));
        assert_eq!(renderer.total_faces(), 2);
        // 空数据等价 drop;未知位置 drop 幂等。
        assert!(renderer.upload_section((0, 5, 0), &[]));
        renderer.drop_section((9, 9, 9));
        assert_eq!(renderer.total_faces(), 1);
    }

    #[test]
    fn atlas_upload_validates_length() {
        let Some(mut renderer) = renderer_or_skip(16, 16) else {
            return;
        };
        assert!(!renderer.upload_atlas(0, &[]));
        assert!(!renderer.upload_atlas(2, &[0u8; 10]));
        let bytes_per_layer: usize = (0..ATLAS_MIPS)
            .map(|m| {
                let s = (ATLAS_TEX_SIZE >> m).max(1) as usize;
                s * s * 4
            })
            .sum();
        assert!(renderer.upload_atlas(1, &vec![0u8; bytes_per_layer]));
    }

    #[test]
    fn frame_with_sections_and_hiz_second_frame_is_stable() {
        let Some(mut renderer) = renderer_or_skip(64, 64) else {
            return;
        };
        let bytes_per_layer: usize = (0..ATLAS_MIPS)
            .map(|m| {
                let s = (ATLAS_TEX_SIZE >> m).max(1) as usize;
                s * s * 4
            })
            .sum();
        assert!(renderer.upload_atlas(1, &vec![128u8; bytes_per_layer]));
        // 一个 section、若干 packed face(全零 face 也会经过 cull 与绘制
        // 路径,验证管线兼容性;图像正确性由 Go 侧双后端对照保证)。
        assert!(renderer.upload_section((0, 5, 0), &[0u8; 64]));
        let mut frame = empty_frame();
        frame.visible = vec![(0, 5, 0), (9, 9, 9)];
        renderer.render_frame(&frame);
        let mut first = vec![0u8; 64 * 64 * 4];
        renderer.readback(&mut first);
        // 第二帧相机不动:走 HiZ 启用路径,图像必须稳定。
        renderer.render_frame(&frame);
        let mut second = vec![0u8; 64 * 64 * 4];
        renderer.readback(&mut second);
        assert_eq!(first, second, "HiZ 启用帧不得改变图像");
    }
}

#[cfg(test)]
mod daylight_tests {
    use super::tests_support::*;

    /// 昼/夜两个时间点的 sky 输出必须不同:证明 daylight/star uniform
    /// 真实参与渲染,而非被常量折叠。
    #[test]
    fn day_and_night_sky_differ() {
        let Some(mut renderer) = renderer_or_skip_pub(64, 32) else {
            return;
        };
        let mut day = empty_frame_pub();
        day.daylight = 1.0;
        day.star_visibility = 0.0;
        renderer.render_frame(&day);
        let mut day_img = vec![0u8; 64 * 32 * 4];
        renderer.readback(&mut day_img);

        let mut night = empty_frame_pub();
        night.daylight = 0.05;
        night.star_visibility = 1.0;
        night.sky_color = [0.01, 0.01, 0.03, 1.0];
        renderer.render_frame(&night);
        let mut night_img = vec![0u8; 64 * 32 * 4];
        renderer.readback(&mut night_img);
        assert_ne!(day_img, night_img, "昼夜 sky 输出不应相同");
    }
}

#[cfg(test)]
pub(crate) mod tests_support {
    use super::*;

    /// 测试共享:创建渲染器,无适配器时返回 None(调用方跳过)。
    pub fn renderer_or_skip_pub(width: u32, height: u32) -> Option<OffscreenRenderer> {
        OffscreenRenderer::new(width, height).ok()
    }

    /// 测试共享:恒等矩阵的空帧输入。
    pub fn empty_frame_pub() -> FrameInput {
        let mut identity = [0.0f32; 16];
        for i in 0..4 {
            identity[i * 4 + i] = 1.0;
        }
        FrameInput {
            view_proj: identity,
            view_proj_inv: identity,
            pos: [0.0, 80.0, 0.0],
            daylight: 1.0,
            sun_direction: [0.0, 1.0, 0.0],
            star_visibility: 0.0,
            sky_color: [0.25, 0.5, 1.0, 1.0],
            cloud_macro_x: 0,
            cloud_local: 0.0,
            visible: Vec::new(),
            ..Default::default()
        }
    }
}

#[cfg(test)]
mod entity_tests {
    use super::entity;
    use super::tests_support::*;

    /// 单个红色 avatar 实例(identity 变换)在 identity 相机下必然覆盖
    /// 画面中心:实体 pass 输出必须改变图像,且非法 instance 段被拒绝。
    #[test]
    fn avatar_instances_render_and_invalid_lengths_reject() {
        let Some(mut renderer) = renderer_or_skip_pub(64, 64) else {
            return;
        };
        let empty = empty_frame_pub();
        assert!(renderer.render_frame(&empty));
        let mut base = vec![0u8; 64 * 64 * 4];
        renderer.readback(&mut base);

        // identity mat4(列主序)+ 红色。
        let mut instance = [0u8; 80];
        for i in 0..4 {
            instance[(i * 4 + i) * 4..(i * 4 + i) * 4 + 4].copy_from_slice(&1.0f32.to_le_bytes());
        }
        instance[64..68].copy_from_slice(&1.0f32.to_le_bytes());
        instance[76..80].copy_from_slice(&1.0f32.to_le_bytes());
        let mut frame = empty_frame_pub();
        frame.avatar_instances = instance.to_vec();
        assert!(renderer.render_frame(&frame));
        let mut with_avatar = vec![0u8; 64 * 64 * 4];
        renderer.readback(&mut with_avatar);
        assert_ne!(base, with_avatar, "avatar 实例必须改变图像");

        // 掉落物走同一路径:同一实例流经 drop 段也必须生效。
        let mut drop_frame = empty_frame_pub();
        drop_frame.drop_instances = instance.to_vec();
        assert!(renderer.render_frame(&drop_frame));
        let mut with_drop = vec![0u8; 64 * 64 * 4];
        renderer.readback(&mut with_drop);
        assert_ne!(base, with_drop, "掉落物实例必须改变图像");

        // 非 80 倍数与超容量拒绝,且 target 保持上一帧内容。
        let mut bad = empty_frame_pub();
        bad.avatar_instances = vec![0u8; 84];
        assert!(!renderer.render_frame(&bad));
        let mut after_bad = vec![0u8; 64 * 64 * 4];
        renderer.readback(&mut after_bad);
        assert_eq!(with_drop, after_bad, "拒绝帧不得触碰 target");
        let mut oversized = empty_frame_pub();
        oversized.avatar_instances = vec![0u8; (entity::AVATAR_MAX_INSTANCES + 1) * 80];
        assert!(!renderer.render_frame(&oversized));
    }
}
