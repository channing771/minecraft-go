//! 离屏 wgpu 世界渲染器(R2a)。
//!
//! 本模块是 Go `internal/render` 世界地形路径的平行 Rust 实现:纯离屏、
//! 不接窗口 surface,生产客户端仍由 Go 渲染;双后端图像对照门禁在 Go 侧
//! 测试中执行。GPU 数据流逐一镜像 Go 版:全局 face 池(packed u64 face)、
//! origin 槽位、32 字节 section record、cull compute 写 visible instances
//! 与 indirect args、单次 indexed indirect draw、sky 全屏三角与 HiZ
//! 金字塔遮挡。uniform 布局、clear 值与 pass 顺序保持一致,保证同输入
//! 同图像。远环 LOD 壳 pass(v6)绘制在天空与近环 terrain 之间:世界
//! 坐标大 quad + 距离雾 + tile 级 CPU 视锥剔除,不进 HiZ/GPU culling。
//!
//! 约束:
//! - color `Bgra8UnormSrgb`(Go capture 同格式),depth `Depth32Float`;
//! - mesh packed face 字节只在 section 变脏时过境一次;
//! - 每帧一次 [`OffscreenRenderer::render_frame`],帧内无逐 pass FFI。

pub mod entity;
pub mod lod;
#[cfg(test)]
mod plant_tests;
pub mod pool;
pub mod quads;
pub mod shaders;
#[cfg(test)]
mod water_tests;

use std::collections::HashMap;

use entity::{EntityPass, EntityPipelineKind};
use lod::{Frustum, LodPass};
use pool::{Alloc, Pool};
use quads::{QuadPass, QuadPassConfig};

/// 离屏 color 格式,必须与 Go capture 的 `FormatBGRA8UnormSrgb` 一致。
pub const COLOR_FORMAT: wgpu::TextureFormat = wgpu::TextureFormat::Bgra8UnormSrgb;
/// 深度格式,与 Go 渲染器的 `FormatDepth32Float` 一致。
pub const DEPTH_FORMAT: wgpu::TextureFormat = wgpu::TextureFormat::Depth32Float;

// 容量与 Go 渲染器默认值一致(pool 面数、origin 槽位)。
const POOL_FACES: u32 = 4_500_000;
/// 水面实例池容量(条)。水面不贪心合并、按 1×1 出面,主导项是海面的顶面:
/// 视距 32 → 视半径 33 → (2×33+1)² = 4489 个区块,每区块 16×16 = 256 列,
/// 全海世界的顶面上界 4489×256 ≈ 1.15M 条;岸线与水下地形边缘再贡献侧面。
/// 取 2Mi 条 = 32 MiB 固定显存,约为该上界的 1.8 倍。
///
/// 这个值曾是 512Ki,依据是"一个区段的水面上界 6×256 = 1536 条"——那个算式
/// 错了(区段有 16³ = 4096 格,不是 256 格),而且区段上界本来就推不出整视距的
/// 总量。fluidEnabled 默认打开后,默认世界的实测峰值就在 50 万条上方、恰好
/// 越过 512Ki,抓帧在加载阶段直接以 STATUS_CAPACITY 崩溃。
///
/// 池仍是**一次性**创建的固定容量——water pass MUST NOT 引入每帧动态资源
/// (`voxel-visual-presentation` MODIFIED 写死的边界);上限本身是硬约束,
/// 超出时 upload_section 返回 false 并由调用方以 STATUS_CAPACITY 报错,
/// 不做静默截断。
const WATER_POOL_INSTANCES: u32 = 2 * 1024 * 1024;
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

/// 一帧渲染的结果:输入违约、surface 不可用跳帧、或成功。
#[derive(Debug, PartialEq, Eq)]
pub enum FrameResult {
    /// 输入违约,未触碰任何 GPU 状态。
    Invalid,
    /// 窗口 surface 获取失败(遮挡/过期),本帧跳过。
    Skipped,
    /// 渲染完成(窗口模式已 present)。
    Rendered,
}

/// 渲染目标模式:离屏纹理或窗口 surface。
enum TargetMode {
    /// 离屏:固定 color 纹理,支持 readback。
    Offscreen {
        color: wgpu::Texture,
        color_view: wgpu::TextureView,
    },
    /// 窗口:每帧 acquire surface 纹理并 present;不支持 readback。
    Windowed {
        surface: wgpu::Surface<'static>,
        config: wgpu::SurfaceConfiguration,
        /// 持有窗口共享所有权,保证 surface 生命周期内窗口存活。
        _window: std::sync::Arc<winit::window::Window>,
    },
}

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
#[derive(Default, Clone)]
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
    /// 相机浸没时的全屏水色叠加 RGBA(A <= 0 表示不绘制)。
    ///
    /// 它与 `overlay_strength` 共用同一条全屏三角管线,只是 uniform 的 edge 位
    /// 不同(水色全屏均匀,红边走边缘渐变),因此不新增任何绘制管线。
    pub water_tint: [f32; 4],
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
            && self.water_tint[3] == 0.0
            && self.name_tag_vertices.is_empty()
            && self.hud_vertices.is_empty()
            && self.debug_vertices.is_empty()
    }
}

/// 一个已上传 section:两条流各自的池内分配、共用的 origin 槽位与条数。
///
/// 不透明面与水面分属两个池:前者接 GPU culling 走单次 indirect draw,后者
/// 走独立的半透明 water pass。任一侧可以为空——只有水面的区段(地形在相邻
/// 区段)完全正常,不得被当成空区段丢弃。
struct SectionSlot {
    /// 不透明面在 `faces` 池中的分配;只有水面的区段为 None。
    alloc: Option<Alloc>,
    origin_idx: u32,
    face_count: u32,
    /// 水面实例在 `water_instances` 池中的分配;无水面为 None。
    water: Option<Alloc>,
    water_count: u32,
}

/// reallocate 在一个池上为区段的新数据挑选分配,策略与既有 `upload_section`
/// 逐步等价:
///
/// - `required == 0` → 归还旧块并返回 `Some(None)`(该侧本次没有数据);
/// - 旧块够大 → 原地复用,不动池;
/// - 旧块不够 → 先尝试在**不归还**旧块的情况下分配(避免碎片化时白白丢块),
///   失败再归还旧块重试。
///
/// 返回 `None` 表示池容量确实不足,此时旧块已归还,调用方需回收 origin 槽位。
fn reallocate(pool: &mut Pool, old: Option<Alloc>, required: u32) -> Option<Option<Alloc>> {
    if required == 0 {
        if let Some(old) = old {
            pool.free(old);
        }
        return Some(None);
    }
    let Some(old) = old else {
        return pool.alloc(required).map(Some);
    };
    if required <= old.size {
        return Some(Some(old));
    }
    if let Some(fresh) = pool.alloc(required) {
        pool.free(old);
        return Some(Some(fresh));
    }
    pool.free(old);
    pool.alloc(required).map(Some)
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

/// sort_water_draws 把本帧的水面绘制表排成**由远及近**。
///
/// 三个约束都在这一行里:
///
/// - **方向**:`b` 在前、`a` 在后,即距离平方**降序**。alpha blend 下最后画的那层
///   主导,由远及近才能让近处的水覆在远处之上;反过来排会直接违反
///   `fluid-presentation` 的「MUST 按由远及近的顺序绘制」。
/// - **不分配**:必须是 `sort_unstable_by`。稳定排序(driftsort)在几百条以上会申请
///   一次临时缓冲,而 `voxel-visual-presentation` MODIFIED 写死了「预热后 MUST 不
///   产生每帧动态资源创建或堆分配」。守卫见 `water_draw_sort_does_not_allocate`。
/// - **确定性**:不稳定排序不保证等价元素的相对次序,因此用池内 `offset` 兜底比较。
///   等距区段的绘制次序于是仍然固定,capture golden 不会在两次运行之间抖动。
///
/// `total_cmp` 是 f32 上的全序,不会因 NaN 触发排序 panic。
fn sort_water_draws(draws: &mut [(f32, Alloc, u32)]) {
    draws.sort_unstable_by(|a, b| b.0.total_cmp(&a.0).then(a.1.offset.cmp(&b.1.offset)));
}

/// 离屏世界渲染器。
pub struct OffscreenRenderer {
    device: wgpu::Device,
    queue: wgpu::Queue,
    width: u32,
    height: u32,
    mode: TargetMode,
    depth_view: wgpu::TextureView,

    faces: wgpu::Buffer,
    instances: wgpu::Buffer,
    /// 水面实例缓冲(16 字节/条,布局与 cull 输出的 visible 实例一致:
    /// `vec4u(lo, hi, origin_idx, 0)`),由 CPU 在上传时一次写入。
    water_instances: wgpu::Buffer,
    origins: wgpu::Buffer,
    camera: wgpu::Buffer,
    sky_camera: wgpu::Buffer,
    indirect: wgpu::Buffer,
    index: wgpu::Buffer,
    zero_args: wgpu::Buffer,

    terrain_layout: wgpu::BindGroupLayout,
    terrain_pipeline: wgpu::RenderPipeline,
    terrain_bind: Option<wgpu::BindGroup>,
    /// water pass 管线:alpha blend、深度测试开、深度写关。
    water_pipeline: wgpu::RenderPipeline,
    /// water pass 的 bind group(与 terrain 同 layout,实例缓冲换成
    /// water_instances);None 表示 atlas 尚未上传。
    water_bind: Option<wgpu::BindGroup>,
    sampler: wgpu::Sampler,
    sky_pipeline: wgpu::RenderPipeline,
    sky_bind: wgpu::BindGroup,

    /// 远环 LOD pass(client ABI v6):世界坐标壳 quad + 距离雾,
    /// 帧序位于天空之后、近环 terrain 之前。
    lod_pass: LodPass,

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
    /// 方块轮廓 pass(12 实例,透明只读深度)。
    outline_pass: EntityPass,
    /// 伤害红边 uniform(16B,strength@0)。
    overlay_uniform: wgpu::Buffer,
    water_tint_uniform: wgpu::Buffer,
    /// 伤害红边全屏管线(无深度附件,alpha blend)。
    overlay_pipeline: wgpu::RenderPipeline,
    overlay_bind: wgpu::BindGroup,
    water_tint_bind: wgpu::BindGroup,

    /// 名牌 billboard pass(双流:背景 + 字形)。
    name_tag_pass: QuadPass,
    /// HUD pass(hotbar 家族;bind 在 HUD 图集上传后建立)。
    hud_pass: QuadPass,
    /// 调试面板 pass。
    debug_pass: QuadPass,

    /// 字形图集(R8,增量矩形上传);内容全部来自 Go 光栅化 worker。
    glyph_atlas: wgpu::Texture,
    /// 字形图集视图,供文本类 pass 共享。
    glyph_view: wgpu::TextureView,
    /// HUD 图集;None 表示尚未上传。
    hud_atlas: Option<wgpu::Texture>,

    pool: Pool,
    /// 水面实例池;与 `pool` 同一分配器,单位是实例条数。
    water_pool: Pool,
    /// 水面实例编码 scratch,跨上传复用以避免逐次堆分配。
    water_scratch: Vec<u8>,
    /// 本帧参与 water pass 的区段(距离平方,水面分配,条数),按由远及近排序;
    /// 跨帧复用,预热后不再分配。
    water_draws: Vec<(f32, Alloc, u32)>,
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
        Self::build_renderer(instance, None, width, height)
    }

    /// 创建窗口模式渲染器:从共享窗口建 wgpu surface 并按 FIFO 配置
    /// (镜像 Go surface 配置),其余管线与离屏一致。
    pub fn new_windowed(
        window: std::sync::Arc<winit::window::Window>,
        width: u32,
        height: u32,
    ) -> Result<Self, RenderCreateError> {
        let instance = wgpu::Instance::new(wgpu::InstanceDescriptor::new_without_display_handle());
        let surface = instance
            .create_surface(window.clone())
            .map_err(|_| RenderCreateError::Device)?;
        Self::build_renderer(instance, Some((surface, window)), width, height)
    }

    fn build_renderer(
        instance: wgpu::Instance,
        windowed: Option<(
            wgpu::Surface<'static>,
            std::sync::Arc<winit::window::Window>,
        )>,
        width: u32,
        height: u32,
    ) -> Result<Self, RenderCreateError> {
        let adapter = pollster::block_on(instance.request_adapter(&wgpu::RequestAdapterOptions {
            power_preference: wgpu::PowerPreference::HighPerformance,
            force_fallback_adapter: false,
            compatible_surface: windowed.as_ref().map(|(surface, _)| surface),
        }))
        .map_err(|_| RenderCreateError::Adapter)?;
        let (device, queue) = pollster::block_on(adapter.request_device(&wgpu::DeviceDescriptor {
            label: Some("mornlea renderer"),
            ..Default::default()
        }))
        .map_err(|_| RenderCreateError::Device)?;

        // 目标模式:窗口 surface(FIFO/BGRA sRGB,镜像 Go 配置)或离屏纹理。
        let mode = match windowed {
            Some((surface, window)) => {
                let config = wgpu::SurfaceConfiguration {
                    usage: wgpu::TextureUsages::RENDER_ATTACHMENT,
                    format: COLOR_FORMAT,
                    width,
                    height,
                    present_mode: wgpu::PresentMode::Fifo,
                    desired_maximum_frame_latency: 2,
                    alpha_mode: wgpu::CompositeAlphaMode::Auto,
                    view_formats: vec![],
                };
                surface.configure(&device, &config);
                TargetMode::Windowed {
                    surface,
                    config,
                    _window: window,
                }
            }
            None => {
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
                let color_view = color.create_view(&wgpu::TextureViewDescriptor::default());
                TargetMode::Offscreen { color, color_view }
            }
        };

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
        let depth = make_target(
            DEPTH_FORMAT,
            wgpu::TextureUsages::RENDER_ATTACHMENT | wgpu::TextureUsages::TEXTURE_BINDING,
            "offscreen depth",
        );
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
        let water_instances = make_buffer(
            u64::from(WATER_POOL_INSTANCES) * BYTES_PER_VISIBLE_FACE,
            BU::STORAGE | BU::COPY_DST,
            "water surface instances",
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
        let terrain_pipeline = make_render_pipeline(
            &device,
            "terrain",
            &terrain_module,
            &terrain_layout,
            true,
            wgpu::BlendState::REPLACE,
        );
        // water pass 复用 terrain 的 bind group layout(camera / 实例 / origin /
        // atlas / sampler 五项完全相同),只换实例缓冲与管线状态。
        let water_module = device.create_shader_module(wgpu::ShaderModuleDescriptor {
            label: Some("water"),
            source: wgpu::ShaderSource::Wgsl(shaders::WATER.into()),
        });
        let water_pipeline = make_render_pipeline(
            &device,
            "water",
            &water_module,
            &terrain_layout,
            false,
            wgpu::BlendState::ALPHA_BLENDING,
        );
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
        let sky_pipeline = make_render_pipeline(
            &device,
            "sky",
            &sky_module,
            &sky_layout,
            false,
            wgpu::BlendState::REPLACE,
        );
        let sky_bind = device.create_bind_group(&wgpu::BindGroupDescriptor {
            label: Some("sky resources"),
            layout: &sky_layout,
            entries: &[wgpu::BindGroupEntry {
                binding: 0,
                resource: sky_camera.as_entire_binding(),
            }],
        });

        // 远环 pass:世界坐标壳 quad 管线;bind group 随材质 atlas 建立。
        let lod_pass = LodPass::new(&device, COLOR_FORMAT);

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
            EntityPipelineKind::Opaque,
            COLOR_FORMAT,
            DEPTH_FORMAT,
        );
        let drop_pass = EntityPass::new(
            &device,
            &queue,
            &avatar_module,
            "item drop",
            entity::DROP_MAX_INSTANCES,
            EntityPipelineKind::Opaque,
            COLOR_FORMAT,
            DEPTH_FORMAT,
        );

        let outline_pass = EntityPass::new(
            &device,
            &queue,
            &avatar_module,
            "block outline",
            12,
            EntityPipelineKind::OutlineTranslucent,
            COLOR_FORMAT,
            DEPTH_FORMAT,
        );

        // 全屏叠加:无深度附件的全屏三角管线,镜像 Go damage_overlay.go。
        // 伤害红边与水下水色共用这一条管线与这一份 layout,各自持有一块 32 字节
        // uniform(vec4 颜色 + edge 位 + 三个 pad):同一帧里两者可能都要画,
        // 而一次提交内对同一块 buffer 的两次 write_buffer 只有最后一次生效,
        // 所以必须是两块 buffer,不是两条管线。
        let overlay_uniform = make_buffer(32, BU::UNIFORM | BU::COPY_DST, "screen tint uniform");
        let water_tint_uniform = make_buffer(32, BU::UNIFORM | BU::COPY_DST, "water tint uniform");
        let overlay_layout = device.create_bind_group_layout(&wgpu::BindGroupLayoutDescriptor {
            label: Some("screen tint layout"),
            entries: &[buffer_layout_entry(
                0,
                wgpu::ShaderStages::FRAGMENT,
                wgpu::BufferBindingType::Uniform,
            )],
        });
        let overlay_module = device.create_shader_module(wgpu::ShaderModuleDescriptor {
            label: Some("damage overlay"),
            source: wgpu::ShaderSource::Wgsl(shaders::DAMAGE_OVERLAY.into()),
        });
        let overlay_pipeline_layout =
            device.create_pipeline_layout(&wgpu::PipelineLayoutDescriptor {
                label: None,
                bind_group_layouts: &[Some(&overlay_layout)],
                immediate_size: 0,
            });
        let overlay_pipeline = device.create_render_pipeline(&wgpu::RenderPipelineDescriptor {
            label: Some("damage overlay"),
            layout: Some(&overlay_pipeline_layout),
            vertex: wgpu::VertexState {
                module: &overlay_module,
                entry_point: Some("vs_main"),
                compilation_options: Default::default(),
                buffers: &[],
            },
            fragment: Some(wgpu::FragmentState {
                module: &overlay_module,
                entry_point: Some("fs_main"),
                compilation_options: Default::default(),
                targets: &[Some(wgpu::ColorTargetState {
                    format: COLOR_FORMAT,
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
            depth_stencil: None,
            multisample: wgpu::MultisampleState::default(),
            multiview_mask: None,
            cache: None,
        });
        let overlay_bind = device.create_bind_group(&wgpu::BindGroupDescriptor {
            label: Some("damage overlay resources"),
            layout: &overlay_layout,
            entries: &[wgpu::BindGroupEntry {
                binding: 0,
                resource: overlay_uniform.as_entire_binding(),
            }],
        });
        let water_tint_bind = device.create_bind_group(&wgpu::BindGroupDescriptor {
            label: Some("water tint resources"),
            layout: &overlay_layout,
            entries: &[wgpu::BindGroupEntry {
                binding: 0,
                resource: water_tint_uniform.as_entire_binding(),
            }],
        });

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

        let glyph_view = glyph_atlas.create_view(&wgpu::TextureViewDescriptor::default());
        // 三个双流 quad pass;容量只作上界校验,不参与图像输出。
        let name_tag_module = device.create_shader_module(wgpu::ShaderModuleDescriptor {
            label: Some("name-tag"),
            source: wgpu::ShaderSource::Wgsl(shaders::NAME_TAG.into()),
        });
        let mut name_tag_pass = QuadPass::new(
            &device,
            &name_tag_module,
            QuadPassConfig {
                label: "name-tag pass",
                uniform_bytes: 96,
                instance_bytes: 64,
                stream_a_cap: 12,
                stream_b_cap: 12 * 32,
                entry_a: ("background_vs", "background_fs"),
                entry_b: ("glyph_vs", "glyph_fs"),
                uses_depth: true,
                second_texture: false,
                nearest_sampler: false,
                swap_streams: true,
            },
            COLOR_FORMAT,
            DEPTH_FORMAT,
        );
        name_tag_pass.rebuild_bind(&device, &glyph_view, None);
        let hud_module = device.create_shader_module(wgpu::ShaderModuleDescriptor {
            label: Some("hotbar"),
            source: wgpu::ShaderSource::Wgsl(shaders::HUD_HOTBAR.into()),
        });
        let hud_pass = QuadPass::new(
            &device,
            &hud_module,
            QuadPassConfig {
                label: "hotbar pass",
                uniform_bytes: 16,
                instance_bytes: 48,
                stream_a_cap: 4096,
                stream_b_cap: 8192,
                entry_a: ("quad_vs", "quad_fs"),
                entry_b: ("glyph_vs", "glyph_fs"),
                uses_depth: false,
                second_texture: true,
                nearest_sampler: true,
                swap_streams: false,
            },
            COLOR_FORMAT,
            DEPTH_FORMAT,
        );
        let debug_module = device.create_shader_module(wgpu::ShaderModuleDescriptor {
            label: Some("debug panel"),
            source: wgpu::ShaderSource::Wgsl(shaders::DEBUG_PANEL.into()),
        });
        let mut debug_pass = QuadPass::new(
            &device,
            &debug_module,
            QuadPassConfig {
                label: "debug panel pass",
                uniform_bytes: 16,
                instance_bytes: 48,
                stream_a_cap: 256,
                stream_b_cap: 8192,
                entry_a: ("quad_vs", "quad_fs"),
                entry_b: ("glyph_vs", "glyph_fs"),
                uses_depth: false,
                second_texture: false,
                nearest_sampler: false,
                swap_streams: false,
            },
            COLOR_FORMAT,
            DEPTH_FORMAT,
        );
        debug_pass.rebuild_bind(&device, &glyph_view, None);

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
            mode,
            depth_view,
            faces,
            instances,
            water_instances,
            origins,
            camera,
            sky_camera,
            indirect,
            index,
            zero_args,
            terrain_layout,
            terrain_pipeline,
            terrain_bind: None,
            water_pipeline,
            water_bind: None,
            sampler,
            sky_pipeline,
            sky_bind,
            lod_pass,
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
            outline_pass,
            name_tag_pass,
            hud_pass,
            debug_pass,
            overlay_uniform,
            water_tint_uniform,
            overlay_pipeline,
            overlay_bind,
            water_tint_bind,
            glyph_atlas,
            glyph_view,
            hud_atlas: None,
            pool: Pool::new(POOL_FACES),
            water_pool: Pool::new(WATER_POOL_INSTANCES),
            water_scratch: Vec::new(),
            water_draws: Vec::new(),
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
        // 远环与近环共用同一图集与采样器:世界坐标 UV 才能跨远/近环连续。
        self.lod_pass
            .rebuild_bind(&self.device, &atlas_view, &self.sampler);
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
        // water bind 与 terrain 同 layout、同 atlas,只把 binding 1 的实例缓冲
        // 换成 water_instances。两者在 atlas 上传时一次性建好,帧内不再创建
        // 任何绑定资源。
        self.water_bind = Some(self.device.create_bind_group(&wgpu::BindGroupDescriptor {
            label: Some("water resources"),
            layout: &self.terrain_layout,
            entries: &[
                wgpu::BindGroupEntry {
                    binding: 0,
                    resource: self.camera.as_entire_binding(),
                },
                wgpu::BindGroupEntry {
                    binding: 1,
                    resource: self.water_instances.as_entire_binding(),
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

    /// 上传/替换一个 section 的两条 packed face 流(均为 8 字节/面,已 Pack):
    /// `opaque` 走 GPU culling 与单次 indirect terrain draw,`water` 走独立的
    /// 半透明 water pass。两条都为空等价于 drop。
    ///
    /// 返回 false 表示某个池或 origin 槽位不足;此时该区段已完全退回未上传态,
    /// 不留半条流。
    pub fn upload_section(&mut self, pos: (i32, i32, i32), opaque: &[u8], water: &[u8]) -> bool {
        debug_assert_eq!(opaque.len() % 8, 0);
        debug_assert_eq!(water.len() % 8, 0);
        if opaque.is_empty() && water.is_empty() {
            self.drop_section(pos);
            return true;
        }
        let need_opaque = (opaque.len() / 8) as u32;
        let need_water = (water.len() / 8) as u32;
        // 先摘出旧记录:两条流各自 realloc,任一失败都要把另一条也回滚。
        let previous = self.sections.remove(&pos);
        let origin_idx = match &previous {
            Some(slot) => slot.origin_idx,
            None => match self.take_origin() {
                Some(idx) => idx,
                None => return false,
            },
        };
        let old_opaque = previous.as_ref().and_then(|slot| slot.alloc);
        let old_water = previous.as_ref().and_then(|slot| slot.water);
        let Some(alloc) = reallocate(&mut self.pool, old_opaque, need_opaque) else {
            if let Some(old) = old_water {
                self.water_pool.free(old);
            }
            self.free_origins.push(origin_idx);
            return false;
        };
        let Some(water_alloc) = reallocate(&mut self.water_pool, old_water, need_water) else {
            if let Some(new) = alloc {
                self.pool.free(new);
            }
            self.free_origins.push(origin_idx);
            return false;
        };

        if let Some(alloc) = alloc {
            self.queue.write_buffer(
                &self.faces,
                u64::from(alloc.offset) * BYTES_PER_POOL_FACE,
                opaque,
            );
        }
        if let Some(alloc) = water_alloc {
            // water pass 不接 culling,没有 cull compute 替它展开 origin,
            // 因此这里就把 8 字节的 quad 补成与 cull 输出同布局的 16 字节实例:
            // `vec4u(lo, hi, origin_idx, 0)`。水面几何只在区段重新网格化时变,
            // 这份展开随之只做一次,帧内零工作。
            self.water_scratch.clear();
            self.water_scratch
                .reserve(water.len() / 8 * BYTES_PER_VISIBLE_FACE as usize);
            for quad in water.chunks_exact(8) {
                self.water_scratch.extend_from_slice(quad);
                self.water_scratch
                    .extend_from_slice(&origin_idx.to_le_bytes());
                self.water_scratch.extend_from_slice(&0u32.to_le_bytes());
            }
            self.queue.write_buffer(
                &self.water_instances,
                u64::from(alloc.offset) * BYTES_PER_VISIBLE_FACE,
                &self.water_scratch,
            );
        }
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
                face_count: need_opaque,
                water: water_alloc,
                water_count: need_water,
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

    /// 丢弃一个 section;不存在时为幂等空操作。两条流的分配都要归还。
    pub fn drop_section(&mut self, pos: (i32, i32, i32)) {
        if let Some(slot) = self.sections.remove(&pos) {
            if let Some(alloc) = slot.alloc {
                self.pool.free(alloc);
            }
            if let Some(alloc) = slot.water {
                self.water_pool.free(alloc);
            }
            self.free_origins.push(slot.origin_idx);
        }
    }

    /// 上传/替换一个远环 tile 的壳 quad 字节流(20 字节/quad;空等价
    /// drop)。整 tile 替换语义:重复上传同 tile 即整体替换。失败时
    /// 不触碰任何既有 tile;失败原因见 [`lod::LodUploadError`]。
    pub fn upload_lod_tile(
        &mut self,
        tile: (i32, i32),
        quads: &[u8],
    ) -> Result<(), lod::LodUploadError> {
        self.lod_pass
            .upload_tile(&self.device, &self.queue, tile, quads)
    }

    /// 丢弃一个远环 tile;不存在时为幂等空操作。
    pub fn drop_lod_tile(&mut self, tile: (i32, i32)) {
        self.lod_pass.drop_tile(tile);
    }

    /// 设置远环距离雾参数(start 起雾距离、full 全雾距离,block);校验
    /// 契约与 FFI setter 出口一致(start > 0 且 full > start,NaN 拒绝),
    /// 非法参数返回 false 且不改变状态。默认 768/1152 锚定
    /// lodFarMultiplier=3 的默认几何;5.2 接线按配置推导后调用。
    pub fn set_lod_fog(&mut self, start: f32, full: f32) -> bool {
        self.lod_pass.set_fog(start, full)
    }

    /// 已上传远环 tile 数,供测试断言替换/丢弃语义。
    pub fn lod_tile_count(&self) -> usize {
        self.lod_pass.tile_count()
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
        let hud_view = texture.create_view(&wgpu::TextureViewDescriptor::default());
        self.hud_pass
            .rebuild_bind(&self.device, &self.glyph_view, Some(&hud_view));
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
    pub fn render_frame(&mut self, input: &FrameInput) -> FrameResult {
        // 语义校验先于任何 GPU 写入:非法 pass 段拒绝且不触碰 target。
        if !self.avatar_pass.instances_valid(&input.avatar_instances)
            || !self.drop_pass.instances_valid(&input.drop_instances)
            || !self.outline_pass.instances_valid(&input.outline)
            || input.overlay_strength.is_nan()
            || input.water_tint.iter().any(|value| value.is_nan())
        {
            return FrameResult::Invalid;
        }
        // 文本类段:非空时必须能按各自布局解析,失败在任何 GPU 写入前拒绝。
        let name_tag_segment = if input.name_tag_vertices.is_empty() {
            None
        } else {
            match self.name_tag_pass.parse_segment(&input.name_tag_vertices) {
                Some(parts) => Some(parts),
                None => return FrameResult::Invalid,
            }
        };
        let hud_segment = if input.hud_vertices.is_empty() {
            None
        } else {
            match self.hud_pass.parse_segment(&input.hud_vertices) {
                Some(parts) => Some(parts),
                None => return FrameResult::Invalid,
            }
        };
        let debug_segment = if input.debug_vertices.is_empty() {
            None
        } else {
            match self.debug_pass.parse_segment(&input.debug_vertices) {
                Some(parts) => Some(parts),
                None => return FrameResult::Invalid,
            }
        };
        // 窗口模式先获取 surface 纹理:失败(遮挡/过期)跳帧,镜像 Go
        // `Surface.Acquire` 返回 nil 的语义;离屏模式使用固定 color 纹理。
        let acquired = match &self.mode {
            TargetMode::Offscreen { .. } => None,
            TargetMode::Windowed { surface, .. } => match surface.get_current_texture() {
                // Suboptimal 仍可用本帧呈现;下一次 resize 会重配 surface。
                wgpu::CurrentSurfaceTexture::Success(frame)
                | wgpu::CurrentSurfaceTexture::Suboptimal(frame) => Some(frame),
                // Timeout/遮挡/其余状况:跳帧,镜像 Go Acquire 返回 nil。
                _ => return FrameResult::Skipped,
            },
        };
        let frame_view = match (&self.mode, &acquired) {
            (TargetMode::Offscreen { color_view, .. }, _) => color_view.clone(),
            (_, Some(frame)) => frame
                .texture
                .create_view(&wgpu::TextureViewDescriptor::default()),
            _ => unreachable!("windowed 模式必有已获取的 surface 纹理"),
        };

        // 构建候选 record(编码镜像 Go sectionRecords)。
        let mut records: Vec<u8> = Vec::with_capacity(input.visible.len() * SECTION_RECORD_BYTES);
        let mut candidates = 0u32;
        for pos in &input.visible {
            let Some(slot) = self.sections.get(pos) else {
                continue;
            };
            // 只有水面的区段对不透明 draw 没有贡献,不进候选 record。
            let Some(alloc) = slot.alloc else {
                continue;
            };
            for v in section_origin(*pos) {
                records.extend_from_slice(&v.to_le_bytes());
            }
            records.extend_from_slice(&alloc.offset.to_le_bytes());
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

        // 远环:无 tile 时完全跳过(uniform 与视锥都不产生),禁用远环的
        // 帧路径与既有行为一致;有 tile 时写相机 uniform 并提取视锥供
        // pass 内的 tile 级 CPU 剔除。
        let lod_frustum = if self.lod_pass.is_empty() {
            None
        } else {
            self.lod_pass.write_camera(
                &self.queue,
                &input.view_proj,
                &input.pos,
                input.daylight,
                &input.sky_color,
            );
            Some(Frustum::from_view_proj(&input.view_proj))
        };

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
        // 相机浸没时的背景替换:天空 pass 整个跳过,terrain pass 的 clear 色由
        // 天空色换成水色。water_visible 同时决定后面那层全屏水色叠加是否绘制,
        // 两处**必须**是同一个布尔:背景换成水色却不叠加(或反过来)都会让画面
        // 出现前后不一致的色偏。
        //
        // 这是"压低远处可见度"能读成**浑浊**而不是**一堵墙**的关键。可见半径在
        // 水下被压到几个区段,被裁掉的地方不画任何地形;而 clear 色原本是天空色、
        // 其上还盖着一整块天空三角,于是水下抬头看到的是晴空、云、太阳和星星,
        // 切边成为一条硬边。换成水色之后,裁掉的区域与远景水色连成一片,切边只
        // 剩"地形与水色之间的残余对比",由随后那层全屏水色叠加继续压低。
        //
        // 代价:浸没时看不到水面之上的天空(浅水抬头也是一片水色)。这是刻意接受
        // 的——水下能见度低本来就意味着看不见远处,而保留天空就必然保留那条硬边。
        let water_visible = input.water_tint[3] > 0.0;
        let clear_color = if water_visible {
            [
                input.water_tint[0],
                input.water_tint[1],
                input.water_tint[2],
                1.0,
            ]
        } else {
            input.sky_color
        };
        {
            let mut pass = encoder.begin_render_pass(&wgpu::RenderPassDescriptor {
                label: Some("terrain pass"),
                color_attachments: &[Some(wgpu::RenderPassColorAttachment {
                    view: &frame_view,
                    depth_slice: None,
                    resolve_target: None,
                    ops: wgpu::Operations {
                        load: wgpu::LoadOp::Clear(wgpu::Color {
                            r: f64::from(clear_color[0]),
                            g: f64::from(clear_color[1]),
                            b: f64::from(clear_color[2]),
                            a: f64::from(clear_color[3]),
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
            if !water_visible {
                pass.set_pipeline(&self.sky_pipeline);
                pass.set_bind_group(0, &self.sky_bind, &[]);
                pass.draw(0..3, 0..1);
                // 帧序裁决:天空 → 远环 → 近环 terrain。远环写深度,近环以
                // 更近的深度自然覆盖;远环深度只会让下一帧 HiZ 的遮挡判定
                // 更保守(更少剔除),不会误剔除近环 section。
                // 水下(water tint 激活)时天空被全屏水色替换,远环远景同样
                // 不可见,与天空一并跳过——远环壳不得从水色里透出。
                if let Some(frustum) = &lod_frustum {
                    self.lod_pass.record(&mut pass, frustum);
                }
            }
            if let Some(bind) = &self.terrain_bind {
                pass.set_pipeline(&self.terrain_pipeline);
                pass.set_bind_group(0, bind, &[]);
                pass.set_index_buffer(self.index.slice(..), wgpu::IndexFormat::Uint32);
                pass.draw_indexed_indirect(&self.indirect, 0);
            }
        }
        // water pass:排在 terrain pass 之后、HiZ build 之前(design D3)。
        //
        // 三条状态由管线固定:alpha blend、深度测试开、**深度写关**。深度写关是
        // 为了让视线上前后两片水面都可见——互相 depth-cull 会留下明显空洞。
        //
        // 排序粒度**止于区段**:按区段中心到相机的距离平方由远及近,区段内部按
        // 上传顺序绘制,MUST NOT 逐面排序(逐面排序是每帧的动态工作,与"预热后
        // 零分配"冲突)。water 不接 GPU culling,走普通 draw_indexed。
        //
        // water_draws 跨帧复用(take/放回只是为了避开 self 的借用冲突),预热后
        // 不再有堆分配。
        let mut draws = std::mem::take(&mut self.water_draws);
        draws.clear();
        for pos in &input.visible {
            let Some(slot) = self.sections.get(pos) else {
                continue;
            };
            let (Some(alloc), true) = (slot.water, slot.water_count > 0) else {
                continue;
            };
            let origin = section_origin(*pos);
            let center = [
                origin[0] as f32 + 8.0,
                origin[1] as f32 + 8.0,
                origin[2] as f32 + 8.0,
            ];
            let offset = sub3(center, input.pos);
            let distance2 = offset[0] * offset[0] + offset[1] * offset[1] + offset[2] * offset[2];
            draws.push((distance2, alloc, slot.water_count));
        }
        if let (Some(bind), false) = (&self.water_bind, draws.is_empty()) {
            sort_water_draws(&mut draws);
            let mut pass = encoder.begin_render_pass(&wgpu::RenderPassDescriptor {
                label: Some("water pass"),
                color_attachments: &[Some(wgpu::RenderPassColorAttachment {
                    view: &frame_view,
                    depth_slice: None,
                    resolve_target: None,
                    ops: wgpu::Operations {
                        load: wgpu::LoadOp::Load,
                        store: wgpu::StoreOp::Store,
                    },
                })],
                depth_stencil_attachment: Some(wgpu::RenderPassDepthStencilAttachment {
                    view: &self.depth_view,
                    depth_ops: Some(wgpu::Operations {
                        load: wgpu::LoadOp::Load,
                        store: wgpu::StoreOp::Store,
                    }),
                    stencil_ops: None,
                }),
                occlusion_query_set: None,
                timestamp_writes: None,
                multiview_mask: None,
            });
            pass.set_pipeline(&self.water_pipeline);
            pass.set_bind_group(0, bind, &[]);
            pass.set_index_buffer(self.index.slice(..), wgpu::IndexFormat::Uint32);
            for &(_, alloc, count) in &draws {
                // first_instance 直接就是实例在水面池中的起始下标:WebGPU 的
                // `@builtin(instance_index)` 从 firstInstance 起算。
                pass.draw_indexed(0..6, 0, alloc.offset..alloc.offset + count);
            }
        }
        self.water_draws = draws;
        self.hiz.build(&self.device, &mut encoder, &self.depth_view);
        // 实体 pass:顺序镜像 app_frame(avatar → item drop),空段跳过。
        if !input.avatar_instances.is_empty() {
            self.avatar_pass.upload(
                &self.queue,
                &input.view_proj,
                input.daylight,
                &input.avatar_instances,
            );
            self.avatar_pass
                .record(&mut encoder, &frame_view, &self.depth_view, "avatar pass");
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
                &frame_view,
                &self.depth_view,
                "item drop pass",
            );
        }
        // 轮廓 pass(Go 顺序:drop 之后、名牌之前)。
        if !input.outline.is_empty() {
            self.outline_pass.upload(
                &self.queue,
                &input.view_proj,
                input.daylight,
                &input.outline,
            );
            self.outline_pass.record(
                &mut encoder,
                &frame_view,
                &self.depth_view,
                "block outline pass",
            );
        }
        // 名牌(Go 顺序:outline 之后、overlay 之前)。
        if let Some((uniform, backgrounds, glyphs)) = name_tag_segment {
            self.name_tag_pass.upload_and_record(
                &self.queue,
                &mut encoder,
                &frame_view,
                Some(&self.depth_view),
                uniform,
                backgrounds,
                glyphs,
            );
        }
        // 全屏叠加(Go 顺序:名牌之后、HUD 之前):水下水色与伤害红边**共用一条
        // render pass 与一条管线**,只是各自的 uniform 不同。
        //
        // 两者合并成一个 pass 是有意的:src/render 下的 render pass 起始调用点
        // 总数是 water_tests 的一条硬门禁(voxel-visual-presentation 只放宽了恰好
        // 一个额外的半透明阶段),水下视觉不该消费那份额度。
        //
        // 绘制次序是「先水色、后红边」:水色是环境效果,必须垫在受伤反馈下面,
        // 否则红边会被水色冲淡。
        let damage_visible = input.overlay_strength > 0.0;
        if water_visible || damage_visible {
            if water_visible {
                let mut uniform = [0u8; 32];
                for (index, value) in input.water_tint.iter().enumerate() {
                    let clamped = value.clamp(0.0, 1.0);
                    uniform[index * 4..index * 4 + 4].copy_from_slice(&clamped.to_le_bytes());
                }
                // edge = 0:全屏均匀覆盖,不走边缘渐变。
                uniform[16..20].copy_from_slice(&0.0f32.to_le_bytes());
                self.queue
                    .write_buffer(&self.water_tint_uniform, 0, &uniform);
            }
            if damage_visible {
                // 固定红色、0.30 上限与 edge = 1 的边缘渐变:与本文件历史行为逐位
                // 一致,只是颜色与 edge 位从 shader 常量搬进了 uniform。
                // strength 钳制到 1(镜像 Go)。
                let strength = input.overlay_strength.min(1.0);
                let mut uniform = [0u8; 32];
                uniform[0..4].copy_from_slice(&0.65f32.to_le_bytes());
                uniform[12..16].copy_from_slice(&(0.30f32 * strength).to_le_bytes());
                uniform[16..20].copy_from_slice(&1.0f32.to_le_bytes());
                self.queue.write_buffer(&self.overlay_uniform, 0, &uniform);
            }
            let mut pass = encoder.begin_render_pass(&wgpu::RenderPassDescriptor {
                label: Some("screen tint pass"),
                color_attachments: &[Some(wgpu::RenderPassColorAttachment {
                    view: &frame_view,
                    depth_slice: None,
                    resolve_target: None,
                    ops: wgpu::Operations {
                        load: wgpu::LoadOp::Load,
                        store: wgpu::StoreOp::Store,
                    },
                })],
                depth_stencil_attachment: None,
                occlusion_query_set: None,
                timestamp_writes: None,
                multiview_mask: None,
            });
            pass.set_pipeline(&self.overlay_pipeline);
            if water_visible {
                pass.set_bind_group(0, &self.water_tint_bind, &[]);
                pass.draw(0..3, 0..1);
            }
            if damage_visible {
                pass.set_bind_group(0, &self.overlay_bind, &[]);
                pass.draw(0..3, 0..1);
            }
        }
        // HUD 与调试面板(Go 顺序:overlay 之后,面板最后)。
        if let Some((uniform, quads, glyphs)) = hud_segment {
            self.hud_pass.upload_and_record(
                &self.queue,
                &mut encoder,
                &frame_view,
                None,
                uniform,
                quads,
                glyphs,
            );
        }
        if let Some((uniform, quads, glyphs)) = debug_segment {
            self.debug_pass.upload_and_record(
                &self.queue,
                &mut encoder,
                &frame_view,
                None,
                uniform,
                quads,
                glyphs,
            );
        }
        self.last_pos = input.pos;
        self.last_view_proj = input.view_proj;
        self.have_last_camera = true;
        self.queue.submit([encoder.finish()]);
        if let Some(frame) = acquired {
            frame.present();
        }
        FrameResult::Rendered
    }

    /// 调整输出尺寸:重建 depth 与 HiZ,离屏重建 color,窗口重配 surface;
    /// HiZ 失效一帧(镜像 Go `Resize` 重置 haveLastCamera)。
    pub fn resize(&mut self, width: u32, height: u32) {
        if width == self.width && height == self.height {
            return;
        }
        self.width = width;
        self.height = height;
        let make_target = |device: &wgpu::Device, format, usage, label: &str| {
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
        let depth = make_target(
            &self.device,
            DEPTH_FORMAT,
            wgpu::TextureUsages::RENDER_ATTACHMENT | wgpu::TextureUsages::TEXTURE_BINDING,
            "offscreen depth",
        );
        self.depth_view = depth.create_view(&wgpu::TextureViewDescriptor::default());
        match &mut self.mode {
            TargetMode::Offscreen { color, color_view } => {
                *color = make_target(
                    &self.device,
                    COLOR_FORMAT,
                    wgpu::TextureUsages::RENDER_ATTACHMENT | wgpu::TextureUsages::COPY_SRC,
                    "offscreen color",
                );
                *color_view = color.create_view(&wgpu::TextureViewDescriptor::default());
            }
            TargetMode::Windowed {
                surface, config, ..
            } => {
                config.width = width;
                config.height = height;
                surface.configure(&self.device, config);
            }
        }
        self.hiz = HiZ::new(&self.device, &self.queue, width, height);
        self.cull_bind = make_cull_bind(
            &self.device,
            &self.cull_layout,
            &self.cull_uniforms,
            &self.cull_sections,
            &self.faces,
            &self.instances,
            &self.indirect,
            &self.dummy_hiz_view,
        );
        self.cull_uses_hiz = false;
        self.have_last_camera = false;
    }

    /// 阻塞回读离屏 color(BGRA,逐行紧密拼接);`out` 长度必须恰为
    /// width×height×4(FFI 层校验)。
    pub fn readback(&self, out: &mut [u8]) -> bool {
        let TargetMode::Offscreen { color, .. } = &self.mode else {
            return false;
        };
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
                texture: color,
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
        true
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
/// TriangleList、CCW、无背面剔除、depth compare Less。
///
/// `blend` 是唯一按 pass 变化的颜色状态:terrain/sky 用 REPLACE,water pass 用
/// ALPHA_BLENDING。water 另以 `depth_write = false` 关闭深度写——两片水面若互相
/// depth-cull 会在画面上留下明显空洞(design D3)。
fn make_render_pipeline(
    device: &wgpu::Device,
    label: &str,
    module: &wgpu::ShaderModule,
    layout: &wgpu::BindGroupLayout,
    depth_write: bool,
    blend: wgpu::BlendState,
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
                blend: Some(blend),
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

    /// 水面实例池必须覆盖"最大视距下全海世界的顶面"这一主导上界。
    ///
    /// 这条断言承的重是**位置性**而非存在性:它不是在问"常量存在吗",而是把
    /// 常量与一个独立算出的几何上界比大小。曾用的 512Ki 会让本测试变红——
    /// fluidEnabled 默认打开后,默认世界的实测峰值就越过了它,抓帧在加载阶段
    /// 以 STATUS_CAPACITY 崩溃。
    #[test]
    fn water_pool_covers_full_ocean_top_faces() {
        // 视距 32(config.Render.ViewDistance 默认值)→ 视半径 33 区块。
        const VIEW_RADIUS_CHUNKS: u32 = 33;
        const CHUNK_COLUMNS: u32 = 16 * 16;
        let chunks = (2 * VIEW_RADIUS_CHUNKS + 1).pow(2);
        let top_faces = chunks * CHUNK_COLUMNS;
        assert!(
            WATER_POOL_INSTANCES >= top_faces,
            "水面实例池 {} 条覆盖不了全海顶面上界 {} 条",
            WATER_POOL_INSTANCES,
            top_faces
        );
        // 同时钉住 wgpu 的 max_storage_buffer_binding_size(128 MiB)硬上限:
        // 实测超出会在 create_bind_group 阶段直接 validation error。
        assert!(
            u64::from(WATER_POOL_INSTANCES) * BYTES_PER_VISIBLE_FACE <= 128 * 1024 * 1024,
            "水面实例缓冲超出 128 MiB 绑定上限"
        );
    }

    #[test]
    fn offscreen_frame_and_readback_are_deterministic() {
        let Some(mut renderer) = renderer_or_skip(64, 32) else {
            return;
        };
        renderer.render_frame(&empty_frame());
        let mut first = vec![0u8; 64 * 32 * 4];
        assert!(renderer.readback(&mut first));
        assert!(first.iter().any(|&b| b != 0), "sky 渲染后回读不应全零");
        renderer.render_frame(&empty_frame());
        let mut second = vec![0u8; 64 * 32 * 4];
        assert!(renderer.readback(&mut second));
        assert_eq!(first, second, "同输入两帧必须逐字节一致");
    }

    #[test]
    fn section_upload_and_drop_mirror_pool_semantics() {
        let Some(mut renderer) = renderer_or_skip(16, 16) else {
            return;
        };
        assert!(renderer.upload_section((0, 4, 0), &[0u8; 16], &[]));
        assert!(renderer.upload_section((0, 5, 0), &[0u8; 8], &[]));
        assert_eq!(renderer.total_faces(), 3);
        // 覆盖上传复用旧槽。
        assert!(renderer.upload_section((0, 4, 0), &[0u8; 8], &[]));
        assert_eq!(renderer.total_faces(), 2);
        // 空数据等价 drop;未知位置 drop 幂等。
        assert!(renderer.upload_section((0, 5, 0), &[], &[]));
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
        assert!(renderer.upload_section((0, 5, 0), &[0u8; 64], &[]));
        let mut frame = empty_frame();
        frame.visible = vec![(0, 5, 0), (9, 9, 9)];
        renderer.render_frame(&frame);
        let mut first = vec![0u8; 64 * 64 * 4];
        assert!(renderer.readback(&mut first));
        // 第二帧相机不动:走 HiZ 启用路径,图像必须稳定。
        renderer.render_frame(&frame);
        let mut second = vec![0u8; 64 * 64 * 4];
        assert!(renderer.readback(&mut second));
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
        assert!(renderer.readback(&mut day_img));

        let mut night = empty_frame_pub();
        night.daylight = 0.05;
        night.star_visibility = 1.0;
        night.sky_color = [0.01, 0.01, 0.03, 1.0];
        renderer.render_frame(&night);
        let mut night_img = vec![0u8; 64 * 32 * 4];
        assert!(renderer.readback(&mut night_img));
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
    use super::FrameResult;
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
        assert_eq!(renderer.render_frame(&empty), FrameResult::Rendered);
        let mut base = vec![0u8; 64 * 64 * 4];
        assert!(renderer.readback(&mut base));

        // identity mat4(列主序)+ 红色。
        let mut instance = [0u8; 80];
        for i in 0..4 {
            instance[(i * 4 + i) * 4..(i * 4 + i) * 4 + 4].copy_from_slice(&1.0f32.to_le_bytes());
        }
        instance[64..68].copy_from_slice(&1.0f32.to_le_bytes());
        instance[76..80].copy_from_slice(&1.0f32.to_le_bytes());
        let mut frame = empty_frame_pub();
        frame.avatar_instances = instance.to_vec();
        assert_eq!(renderer.render_frame(&frame), FrameResult::Rendered);
        let mut with_avatar = vec![0u8; 64 * 64 * 4];
        assert!(renderer.readback(&mut with_avatar));
        assert_ne!(base, with_avatar, "avatar 实例必须改变图像");

        // 掉落物走同一路径:同一实例流经 drop 段也必须生效。
        let mut drop_frame = empty_frame_pub();
        drop_frame.drop_instances = instance.to_vec();
        assert_eq!(renderer.render_frame(&drop_frame), FrameResult::Rendered);
        let mut with_drop = vec![0u8; 64 * 64 * 4];
        assert!(renderer.readback(&mut with_drop));
        assert_ne!(base, with_drop, "掉落物实例必须改变图像");

        // 非 80 倍数与超容量拒绝,且 target 保持上一帧内容。
        let mut bad = empty_frame_pub();
        bad.avatar_instances = vec![0u8; 84];
        assert_eq!(renderer.render_frame(&bad), FrameResult::Invalid);
        let mut after_bad = vec![0u8; 64 * 64 * 4];
        assert!(renderer.readback(&mut after_bad));
        assert_eq!(with_drop, after_bad, "拒绝帧不得触碰 target");
        let mut oversized = empty_frame_pub();
        oversized.avatar_instances = vec![0u8; (entity::AVATAR_MAX_INSTANCES + 1) * 80];
        assert_eq!(renderer.render_frame(&oversized), FrameResult::Invalid);
    }
}

#[cfg(test)]
mod outline_overlay_tests {
    use super::FrameResult;
    use super::tests_support::*;

    /// 轮廓实例与伤害红边都必须改变图像;NaN 强度拒绝且 target 不变。
    #[test]
    fn outline_and_overlay_render_and_reject() {
        let Some(mut renderer) = renderer_or_skip_pub(64, 64) else {
            return;
        };
        let empty = empty_frame_pub();
        assert_eq!(renderer.render_frame(&empty), FrameResult::Rendered);
        let mut base = vec![0u8; 64 * 64 * 4];
        assert!(renderer.readback(&mut base));

        // identity 变换白色轮廓实例(alpha 0.86)。
        let mut instance = [0u8; 80];
        for i in 0..4 {
            instance[(i * 4 + i) * 4..(i * 4 + i) * 4 + 4].copy_from_slice(&1.0f32.to_le_bytes());
        }
        for c in 0..4 {
            instance[64 + c * 4..68 + c * 4].copy_from_slice(&0.86f32.to_le_bytes());
        }
        let mut outline_frame = empty_frame_pub();
        outline_frame.outline = instance.to_vec();
        assert_eq!(renderer.render_frame(&outline_frame), FrameResult::Rendered);
        let mut with_outline = vec![0u8; 64 * 64 * 4];
        assert!(renderer.readback(&mut with_outline));
        assert_ne!(base, with_outline, "轮廓必须改变图像");

        let mut overlay_frame = empty_frame_pub();
        overlay_frame.overlay_strength = 1.0;
        assert_eq!(renderer.render_frame(&overlay_frame), FrameResult::Rendered);
        let mut with_overlay = vec![0u8; 64 * 64 * 4];
        assert!(renderer.readback(&mut with_overlay));
        assert_ne!(base, with_overlay, "伤害红边必须改变图像");

        let mut nan_frame = empty_frame_pub();
        nan_frame.overlay_strength = f32::NAN;
        assert_eq!(
            renderer.render_frame(&nan_frame),
            FrameResult::Invalid,
            "NaN 强度必须拒绝"
        );
    }
}

#[cfg(test)]
mod text_pass_tests {
    use super::FrameResult;
    use super::tests_support::*;

    /// 文本段的结构校验:合法段(含零实例)通过,布局违约在渲染前拒绝。
    /// 视觉正确性由 Go 侧双后端整帧对照保证。
    #[test]
    fn text_segments_validate_before_render() {
        let Some(mut renderer) = renderer_or_skip_pub(32, 32) else {
            return;
        };
        // 名牌:96B 相机 + 1 背景 + 0 字形。
        let mut name_tag = vec![0u8; 96];
        name_tag.extend_from_slice(&1u32.to_le_bytes());
        name_tag.extend_from_slice(&0u32.to_le_bytes());
        name_tag.extend_from_slice(&[0u8; 64]);
        let mut frame = empty_frame_pub();
        frame.name_tag_vertices = name_tag;
        assert_eq!(
            renderer.render_frame(&frame),
            FrameResult::Rendered,
            "合法名牌段必须通过"
        );

        // HUD:16B viewport + 1 quad + 1 glyph(HUD 图集未上传时跳过绘制,
        // 但解析必须通过)。
        let mut hud = vec![0u8; 16];
        hud.extend_from_slice(&1u32.to_le_bytes());
        hud.extend_from_slice(&1u32.to_le_bytes());
        hud.extend_from_slice(&[0u8; 96]);
        let mut hud_frame = empty_frame_pub();
        hud_frame.hud_vertices = hud;
        assert_eq!(
            renderer.render_frame(&hud_frame),
            FrameResult::Rendered,
            "合法 HUD 段必须通过"
        );

        // 面板:声明计数与字节不符必须拒绝。
        let mut bad_debug = vec![0u8; 16];
        bad_debug.extend_from_slice(&2u32.to_le_bytes());
        bad_debug.extend_from_slice(&0u32.to_le_bytes());
        bad_debug.extend_from_slice(&[0u8; 48]);
        let mut bad_frame = empty_frame_pub();
        bad_frame.debug_vertices = bad_debug;
        assert_eq!(
            renderer.render_frame(&bad_frame),
            FrameResult::Invalid,
            "计数与字节不符必须拒绝"
        );

        // 名牌段短于相机头必须拒绝。
        let mut short_frame = empty_frame_pub();
        short_frame.name_tag_vertices = vec![0u8; 40];
        assert_eq!(renderer.render_frame(&short_frame), FrameResult::Invalid);
    }
}
