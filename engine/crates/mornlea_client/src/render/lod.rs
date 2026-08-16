//! 远环 LOD 壳 pass(client ABI v5)。
//!
//! 远环由 engine `mornlea_lod_shell` 生产的确定性纯地表壳构成:每 tile
//! (4×4 chunk = 64×64 列)一条 20 字节/quad 的世界坐标 quad 流。本模块
//! 在 CPU 侧把 quad 流装配为世界坐标顶点缓冲(不复用近环 section 的
//! 4-bit 局部网格编码——装不下世界坐标大 quad),GPU 侧用独立管线绘制:
//! 顶点按世界坐标推导 terrain UV(与近环 `terrain.wgsl` 的 `face_uv`
//! 同源),片元按相机距离做距离雾并向天空色 mix(最外缘带全雾)。
//!
//! 帧序裁决:天空 → 远环 → 近环 terrain → 实体/HUD;远环写深度,近环
//! 以更近的深度自然覆盖。剔除:v1 仅 tile 级 CPU 视锥剔除(每帧 ≤ 数十
//! tile),不进 HiZ/GPU culling——远环深度只会让下一帧 HiZ 的遮挡判定
//! 更保守(更少剔除),不会误剔除近环 section。

use std::collections::BTreeMap;

use super::shaders;

/// 壳流中单个 quad 的字节数,与 engine `lod.rs` 的 `encode_shell` 定稿
/// 布局逐字对齐:x i32(0)| z i32(4)| y i32(8)| w u16(12)| d u16(14)|
/// face u8(16)| material u16(17)| shade u8(19)。
pub const LOD_QUAD_BYTES: usize = 20;

/// tile 世界边长(block):4×4 chunk = 64×64 列,与 engine `mornlea_lod_shell`
/// 的 tile 原点语义一致(tile 覆盖 [tile_x×64, tile_x×64+64) 列)。
pub const LOD_TILE_WORLD_BLOCKS: i32 = 64;

/// tile 表容量上界:覆盖 `lodFarMultiplier` 8 × `viewDistance` 32 的全环
/// (半径 256 chunk = 64 tile,π×64² ≈ 12.9k tile);超出视为编程错误,
/// 返回容量不足由 FFI 层转为 CAPACITY。
pub const MAX_LOD_TILES: usize = 16384;

/// 单顶点字节数:world vec3f(12)| layer u32 | shade u32 | axis u32。
const LOD_VERTEX_BYTES: usize = 24;

/// 远环相机 uniform(96 字节):布局与近环 terrain camera 同源
/// (view_proj + pos/daylight),额外携带雾目标色(= 本帧天空色,随昼夜
/// 变化,不新增独立昼夜状态)。
const LOD_UNIFORM_BYTES: usize = 96;

/// 解码后的单个壳 quad(字段为渲染装配所需的数值形式)。
struct DecodedQuad {
    x: f32,
    y: f32,
    z: f32,
    w: f32,
    d: f32,
    face: u8,
    material: u16,
    shade: u8,
}

/// 解码一个 20 字节壳 quad;face 超出 0..=4 返回 None(engine 定稿只有
/// 顶面 + 四向侧裙共 5 值,其余取值视为非法流)。
fn decode_quad(bytes: &[u8]) -> Option<DecodedQuad> {
    let face = bytes[16];
    if face > 4 {
        return None;
    }
    Some(DecodedQuad {
        x: i32::from_le_bytes(bytes[0..4].try_into().unwrap()) as f32,
        y: i32::from_le_bytes(bytes[8..12].try_into().unwrap()) as f32,
        z: i32::from_le_bytes(bytes[4..8].try_into().unwrap()) as f32,
        w: u16::from_le_bytes(bytes[12..14].try_into().unwrap()) as f32,
        d: u16::from_le_bytes(bytes[14..16].try_into().unwrap()) as f32,
        face,
        material: u16::from_le_bytes(bytes[17..19].try_into().unwrap()),
        shade: bytes[19],
    })
}

/// quad 四角的世界坐标与 UV 面轴(与近环 `face_uv` 的 axis 语义一致:
/// X 面 0、Y 面(顶面)1、Z 面 2)。
///
/// 平面语义(与 engine `build_shell` 的生成逻辑互逆):
/// - 顶面覆盖方块列 [x, x+w) × [z, z+d),可见平面在 Y = y+1;
/// - 侧裙是竖直墙面:PosX 在 X = x+1、NegX 在 X = x(编码 x 是裙边所在
///   边界面的方块列),Z 向墙面镜像;水平跨度 w 沿墙的行方向,竖直跨度
///   d 自 y(低侧 top+1)延伸到 y+d(高侧地表平面)。
fn quad_corners(quad: &DecodedQuad) -> Option<([[f32; 3]; 4], u32)> {
    let (x, y, z) = (quad.x, quad.y, quad.z);
    let (w, d) = (quad.w, quad.d);
    match quad.face {
        0 => Some((
            [
                [x, y + 1.0, z],
                [x + w, y + 1.0, z],
                [x + w, y + 1.0, z + d],
                [x, y + 1.0, z + d],
            ],
            1,
        )),
        1 => Some((
            [[x, y, z], [x, y, z + w], [x, y + d, z + w], [x, y + d, z]],
            0,
        )),
        2 => Some((
            [
                [x + 1.0, y, z],
                [x + 1.0, y, z + w],
                [x + 1.0, y + d, z + w],
                [x + 1.0, y + d, z],
            ],
            0,
        )),
        3 => Some((
            [[x, y, z], [x + w, y, z], [x + w, y + d, z], [x, y + d, z]],
            2,
        )),
        4 => Some((
            [
                [x, y, z + 1.0],
                [x + w, y, z + 1.0],
                [x + w, y + d, z + 1.0],
                [x, y + d, z + 1.0],
            ],
            2,
        )),
        _ => None,
    }
}

/// 追加一个顶点:world vec3f + layer/shade/axis 各 u32(LE)。
fn push_vertex(out: &mut Vec<u8>, world: [f32; 3], layer: u32, shade: u32, axis: u32) {
    for value in world {
        out.extend_from_slice(&value.to_le_bytes());
    }
    out.extend_from_slice(&layer.to_le_bytes());
    out.extend_from_slice(&shade.to_le_bytes());
    out.extend_from_slice(&axis.to_le_bytes());
}

/// 把壳 quad 流装配为顶点缓冲字节(每 quad 两个三角形共 6 顶点)并推导
/// tile 的 Y 界(X/Z 界由 tile 坐标推导,不需要扫流)。流非法(长度非
/// 20 的倍数或 face 越界)返回 None,调用方拒绝上传且不触碰 GPU 状态。
pub(crate) fn build_tile_vertices(quads: &[u8]) -> Option<(Vec<u8>, [f32; 2])> {
    if !quads.len().is_multiple_of(LOD_QUAD_BYTES) {
        return None;
    }
    let mut vertices = Vec::with_capacity(quads.len() / LOD_QUAD_BYTES * 6 * LOD_VERTEX_BYTES);
    let mut bounds = [f32::INFINITY, f32::NEG_INFINITY];
    for quad_bytes in quads.chunks_exact(LOD_QUAD_BYTES) {
        let quad = decode_quad(quad_bytes)?;
        let (corners, axis) = quad_corners(&quad)?;
        for index in [0usize, 1, 2, 0, 2, 3] {
            push_vertex(
                &mut vertices,
                corners[index],
                u32::from(quad.material),
                u32::from(quad.shade),
                axis,
            );
        }
        // Y 界:顶面是 y+1 平面;侧裙自 y 延伸到 y+d。
        let (lo, hi) = if quad.face == 0 {
            (quad.y + 1.0, quad.y + 1.0)
        } else {
            (quad.y, quad.y + quad.d)
        };
        bounds[0] = bounds[0].min(lo);
        bounds[1] = bounds[1].max(hi);
    }
    Some((vertices, bounds))
}

/// 从列主序 view_proj(mgl32 内存布局,m[c×4+r] = 第 r 行第 c 列)提取
/// 六个视锥平面(Gribb–Hartmann 行组合)。平面约定 a·x+b·y+c·z+d ≥ 0
/// 为内侧;near 面取 z ≥ −1,比 wgpu 的 NDC z ∈ [0,1] 更宽松,只会
/// 少剔除、不会误剔除可见 tile。
pub struct Frustum {
    planes: [[f32; 4]; 6],
}

impl Frustum {
    /// 从 view_proj 提取视锥。
    pub(crate) fn from_view_proj(view_proj: &[f32; 16]) -> Self {
        let row = |r: usize| {
            [
                view_proj[r],
                view_proj[4 + r],
                view_proj[8 + r],
                view_proj[12 + r],
            ]
        };
        let r0 = row(0);
        let r1 = row(1);
        let r2 = row(2);
        let r3 = row(3);
        let mut planes = [[0f32; 4]; 6];
        for i in 0..4 {
            planes[0][i] = r3[i] + r0[i]; // left
            planes[1][i] = r3[i] - r0[i]; // right
            planes[2][i] = r3[i] + r1[i]; // bottom
            planes[3][i] = r3[i] - r1[i]; // top
            planes[4][i] = r3[i] + r2[i]; // near
            planes[5][i] = r3[i] - r2[i]; // far
        }
        Self { planes }
    }

    /// AABB 相交测试:p-vertex(逐轴取平面法向正侧的角点)在任一平面
    /// 外侧即整个盒子在视锥外。
    pub(crate) fn intersects_aabb(&self, min: [f32; 3], max: [f32; 3]) -> bool {
        for plane in &self.planes {
            let px = if plane[0] > 0.0 { max[0] } else { min[0] };
            let py = if plane[1] > 0.0 { max[1] } else { min[1] };
            let pz = if plane[2] > 0.0 { max[2] } else { min[2] };
            if plane[0] * px + plane[1] * py + plane[2] * pz + plane[3] < 0.0 {
                return false;
            }
        }
        true
    }
}

/// 一个已上传的远环 tile:顶点缓冲、顶点数与 Y 界(X/Z 界由 tile 坐标
/// 推导,见 [`LodPass::record`])。
struct LodTile {
    vertices: wgpu::Buffer,
    vertex_count: u32,
    min_y: f32,
    max_y: f32,
}

/// 远环 tile 上传失败原因,FFI 层转为对应 status。
#[derive(Debug, PartialEq, Eq)]
pub enum LodUploadError {
    /// 壳流非法(长度非 20 倍数或 face 越界)。
    Invalid,
    /// tile 表容量耗尽(超出 [`MAX_LOD_TILES`])。
    Capacity,
}

/// 远环 pass 的 GPU 资源与 tile 表。bind group 在材质 atlas 上传后经
/// [`LodPass::rebuild_bind`] 建立(与近环 terrain 同一图集、同一采样器,
/// 保证远/近环贴图连续)。
pub struct LodPass {
    pipeline: wgpu::RenderPipeline,
    bind: Option<wgpu::BindGroup>,
    uniform: wgpu::Buffer,
    /// BTreeMap 保证 tile 遍历序确定(坐标升序),同输入同录制序;
    /// 不透明深度写入的最终图像本就与 draw 顺序无关,这里是双保险。
    tiles: BTreeMap<(i32, i32), LodTile>,
}

impl LodPass {
    /// 创建 pass:管线状态镜像近环 terrain(TriangleList、CCW、无背面
    /// 剔除、BlendReplace、深度写开 + Less 比较),差别只在顶点布局——
    /// 世界坐标顶点缓冲而非 storage 实例拉取。
    pub fn new(device: &wgpu::Device, color_format: wgpu::TextureFormat) -> Self {
        let layout = device.create_bind_group_layout(&wgpu::BindGroupLayoutDescriptor {
            label: Some("lod layout"),
            entries: &[
                wgpu::BindGroupLayoutEntry {
                    binding: 0,
                    visibility: wgpu::ShaderStages::VERTEX | wgpu::ShaderStages::FRAGMENT,
                    ty: wgpu::BindingType::Buffer {
                        ty: wgpu::BufferBindingType::Uniform,
                        has_dynamic_offset: false,
                        min_binding_size: None,
                    },
                    count: None,
                },
                wgpu::BindGroupLayoutEntry {
                    binding: 1,
                    visibility: wgpu::ShaderStages::FRAGMENT,
                    ty: wgpu::BindingType::Texture {
                        sample_type: wgpu::TextureSampleType::Float { filterable: true },
                        view_dimension: wgpu::TextureViewDimension::D2Array,
                        multisampled: false,
                    },
                    count: None,
                },
                wgpu::BindGroupLayoutEntry {
                    binding: 2,
                    visibility: wgpu::ShaderStages::FRAGMENT,
                    ty: wgpu::BindingType::Sampler(wgpu::SamplerBindingType::Filtering),
                    count: None,
                },
            ],
        });
        let module = device.create_shader_module(wgpu::ShaderModuleDescriptor {
            label: Some("lod"),
            source: wgpu::ShaderSource::Wgsl(shaders::LOD.into()),
        });
        let pipeline_layout = device.create_pipeline_layout(&wgpu::PipelineLayoutDescriptor {
            label: None,
            bind_group_layouts: &[Some(&layout)],
            immediate_size: 0,
        });
        let pipeline = device.create_render_pipeline(&wgpu::RenderPipelineDescriptor {
            label: Some("lod"),
            layout: Some(&pipeline_layout),
            vertex: wgpu::VertexState {
                module: &module,
                entry_point: Some("vs_main"),
                compilation_options: Default::default(),
                buffers: &[wgpu::VertexBufferLayout {
                    array_stride: LOD_VERTEX_BYTES as u64,
                    step_mode: wgpu::VertexStepMode::Vertex,
                    attributes: &[
                        // world vec3f。
                        wgpu::VertexAttribute {
                            format: wgpu::VertexFormat::Float32x3,
                            offset: 0,
                            shader_location: 0,
                        },
                        // 材质 layer(u32;WGSL 转 f32)。
                        wgpu::VertexAttribute {
                            format: wgpu::VertexFormat::Uint32,
                            offset: 12,
                            shader_location: 1,
                        },
                        // 着色权重原始值(u32;WGSL 除以 255)。
                        wgpu::VertexAttribute {
                            format: wgpu::VertexFormat::Uint32,
                            offset: 16,
                            shader_location: 2,
                        },
                        // UV 面轴(0/1/2,与近环 face_uv 的 axis 同语义)。
                        wgpu::VertexAttribute {
                            format: wgpu::VertexFormat::Uint32,
                            offset: 20,
                            shader_location: 3,
                        },
                    ],
                }],
            },
            fragment: Some(wgpu::FragmentState {
                module: &module,
                entry_point: Some("fs_main"),
                compilation_options: Default::default(),
                targets: &[Some(wgpu::ColorTargetState {
                    format: color_format,
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
                format: super::DEPTH_FORMAT,
                depth_write_enabled: Some(true),
                depth_compare: Some(wgpu::CompareFunction::Less),
                stencil: wgpu::StencilState::default(),
                bias: wgpu::DepthBiasState::default(),
            }),
            multisample: wgpu::MultisampleState::default(),
            multiview_mask: None,
            cache: None,
        });
        let uniform = device.create_buffer(&wgpu::BufferDescriptor {
            label: Some("lod camera"),
            size: LOD_UNIFORM_BYTES as u64,
            usage: wgpu::BufferUsages::UNIFORM | wgpu::BufferUsages::COPY_DST,
            mapped_at_creation: false,
        });
        Self {
            pipeline,
            bind: None,
            uniform,
            tiles: BTreeMap::new(),
        }
    }

    /// (重)建 bind group:材质图集与采样器直接复用近环 terrain 的对象
    /// (同一图集、同一采样参数,世界坐标 UV 才能跨远/近环连续)。
    pub fn rebuild_bind(
        &mut self,
        device: &wgpu::Device,
        atlas_view: &wgpu::TextureView,
        sampler: &wgpu::Sampler,
    ) {
        self.bind = Some(device.create_bind_group(&wgpu::BindGroupDescriptor {
            label: Some("lod resources"),
            layout: &self.pipeline.get_bind_group_layout(0),
            entries: &[
                wgpu::BindGroupEntry {
                    binding: 0,
                    resource: self.uniform.as_entire_binding(),
                },
                wgpu::BindGroupEntry {
                    binding: 1,
                    resource: wgpu::BindingResource::TextureView(atlas_view),
                },
                wgpu::BindGroupEntry {
                    binding: 2,
                    resource: wgpu::BindingResource::Sampler(sampler),
                },
            ],
        }));
    }

    /// tile 表是否为空;空时渲染帧完全跳过本 pass(不写 uniform、不提取
    /// 视锥),保证禁用远环的帧路径与既有行为逐位一致。
    pub fn is_empty(&self) -> bool {
        self.tiles.is_empty()
    }

    /// 已上传 tile 数,供测试断言替换/丢弃语义。
    pub fn tile_count(&self) -> usize {
        self.tiles.len()
    }

    /// 上传/替换一个 tile 的壳 quad 流:整 tile 替换语义(复用近环 section
    /// 的覆盖语义,重复上传同 tile 即整体替换);空流等价 drop。失败时
    /// 不触碰任何已有 tile。
    pub fn upload_tile(
        &mut self,
        device: &wgpu::Device,
        queue: &wgpu::Queue,
        tile: (i32, i32),
        quads: &[u8],
    ) -> Result<(), LodUploadError> {
        if quads.is_empty() {
            self.tiles.remove(&tile);
            return Ok(());
        }
        let Some((vertices, bounds)) = build_tile_vertices(quads) else {
            return Err(LodUploadError::Invalid);
        };
        if !self.tiles.contains_key(&tile) && self.tiles.len() >= MAX_LOD_TILES {
            return Err(LodUploadError::Capacity);
        }
        let buffer = device.create_buffer(&wgpu::BufferDescriptor {
            label: Some("lod tile vertices"),
            size: vertices.len() as u64,
            usage: wgpu::BufferUsages::VERTEX | wgpu::BufferUsages::COPY_DST,
            mapped_at_creation: false,
        });
        queue.write_buffer(&buffer, 0, &vertices);
        // 替换语义:insert 覆盖旧 tile,旧缓冲随值替换被 drop 归还。
        self.tiles.insert(
            tile,
            LodTile {
                vertices: buffer,
                vertex_count: (vertices.len() / LOD_VERTEX_BYTES) as u32,
                min_y: bounds[0],
                max_y: bounds[1],
            },
        );
        Ok(())
    }

    /// 丢弃一个 tile;不存在时为幂等空操作。
    pub fn drop_tile(&mut self, tile: (i32, i32)) {
        self.tiles.remove(&tile);
    }

    /// 写入本帧相机 uniform:view_proj、相机位置 + 昼夜亮度(与近环
    /// terrain camera 同一语义)、雾目标色(= 本帧天空色)。
    pub fn write_camera(
        &self,
        queue: &wgpu::Queue,
        view_proj: &[f32; 16],
        pos: &[f32; 3],
        daylight: f32,
        fog_color: &[f32; 4],
    ) {
        let mut data = [0u8; LOD_UNIFORM_BYTES];
        for (i, v) in view_proj.iter().enumerate() {
            data[i * 4..i * 4 + 4].copy_from_slice(&v.to_le_bytes());
        }
        data[64..68].copy_from_slice(&pos[0].to_le_bytes());
        data[68..72].copy_from_slice(&pos[1].to_le_bytes());
        data[72..76].copy_from_slice(&pos[2].to_le_bytes());
        data[76..80].copy_from_slice(&daylight.to_le_bytes());
        for i in 0..4 {
            data[80 + i * 4..84 + i * 4].copy_from_slice(&fog_color[i].to_le_bytes());
        }
        queue.write_buffer(&self.uniform, 0, &data);
    }

    /// 在既有 render pass 内录制远环绘制:逐 tile CPU 视锥剔除后每 tile
    /// 一次非索引 draw。调用方保证此录制发生在天空之后、近环 terrain
    /// 之前(帧序裁决),且 pass 的深度附件已清空。
    pub fn record(&self, pass: &mut wgpu::RenderPass<'_>, frustum: &Frustum) {
        let Some(bind) = &self.bind else {
            return;
        };
        pass.set_pipeline(&self.pipeline);
        pass.set_bind_group(0, bind, &[]);
        let extent = LOD_TILE_WORLD_BLOCKS as f32;
        for ((tile_x, tile_z), tile) in &self.tiles {
            let base_x = (tile_x * LOD_TILE_WORLD_BLOCKS) as f32;
            let base_z = (tile_z * LOD_TILE_WORLD_BLOCKS) as f32;
            // tile 级视锥剔除:X/Z 用 tile 名义范围(壳几何构造上不越界),
            // Y 用装配时扫得的紧界。
            if !frustum.intersects_aabb(
                [base_x, tile.min_y, base_z],
                [base_x + extent, tile.max_y, base_z + extent],
            ) {
                continue;
            }
            pass.set_vertex_buffer(0, tile.vertices.slice(..));
            pass.draw(0..tile.vertex_count, 0..1);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::render::tests_support::{empty_frame_pub, renderer_or_skip_pub};
    use crate::render::{ATLAS_MIPS, ATLAS_TEX_SIZE, FrameResult};

    /// 编码一个 20 字节壳 quad(LE,与 engine `encode_shell` 同布局)。
    #[allow(clippy::too_many_arguments)]
    fn encode_quad(
        x: i32,
        z: i32,
        y: i32,
        w: u16,
        d: u16,
        face: u8,
        material: u16,
        shade: u8,
    ) -> [u8; 20] {
        let mut out = [0u8; 20];
        out[0..4].copy_from_slice(&x.to_le_bytes());
        out[4..8].copy_from_slice(&z.to_le_bytes());
        out[8..12].copy_from_slice(&y.to_le_bytes());
        out[12..14].copy_from_slice(&w.to_le_bytes());
        out[14..16].copy_from_slice(&d.to_le_bytes());
        out[16] = face;
        out[17..19].copy_from_slice(&material.to_le_bytes());
        out[19] = shade;
        out
    }

    fn read_f32(bytes: &[u8], index: usize) -> f32 {
        f32::from_le_bytes(bytes[index * 4..index * 4 + 4].try_into().unwrap())
    }

    fn read_u32(bytes: &[u8], index: usize) -> u32 {
        u32::from_le_bytes(bytes[index * 4..index * 4 + 4].try_into().unwrap())
    }

    #[test]
    fn build_tile_vertices_locks_layout_and_bounds() {
        // 顶面:覆盖 [10,10] 起 8×8 列,y=70 → 可见平面 71。
        let top = encode_quad(10, 10, 70, 8, 8, 0, 3, 255);
        // PosX 侧裙:x=31(平面 32),y=64,d=6,w=4。
        let skirt = encode_quad(31, 20, 64, 4, 6, 2, 1, 153);
        let mut stream = Vec::new();
        stream.extend_from_slice(&top);
        stream.extend_from_slice(&skirt);
        let (vertices, bounds) = build_tile_vertices(&stream).expect("合法流必须可装配");
        // 每 quad 6 顶点 × 24 字节。
        assert_eq!(vertices.len(), 2 * 6 * LOD_VERTEX_BYTES);
        // 首顶点 = 顶面角 (10, 71, 10),layer 3、shade 255、axis 1(顶面)。
        assert_eq!(read_f32(&vertices, 0), 10.0);
        assert_eq!(read_f32(&vertices, 1), 71.0);
        assert_eq!(read_f32(&vertices, 2), 10.0);
        assert_eq!(read_u32(&vertices, 3), 3);
        assert_eq!(read_u32(&vertices, 4), 255);
        assert_eq!(read_u32(&vertices, 5), 1);
        // 第 7 顶点起是侧裙首角:(32, 64, 20),axis 0(X 面)。
        let offset = 6 * LOD_VERTEX_BYTES;
        assert_eq!(read_f32(&vertices[offset..], 0), 32.0);
        assert_eq!(read_f32(&vertices[offset..], 1), 64.0);
        assert_eq!(read_f32(&vertices[offset..], 2), 20.0);
        assert_eq!(read_u32(&vertices[offset..], 5), 0);
        // Y 界:顶面 71;侧裙 [64, 70]。
        assert_eq!(bounds, [64.0, 71.0]);
    }

    #[test]
    fn build_tile_vertices_rejects_bad_streams() {
        assert!(build_tile_vertices(&[0u8; 21]).is_none(), "长度非 20 倍数");
        let mut bad_face = encode_quad(0, 0, 0, 1, 1, 0, 0, 255);
        bad_face[16] = 5;
        assert!(build_tile_vertices(&bad_face).is_none(), "face 越界");
        assert!(build_tile_vertices(&[]).is_some(), "空流合法(等价 drop)");
    }

    #[test]
    fn frustum_culls_tiles_outside_view() {
        // 恒等 view_proj:可见立方体 x/y ∈ [−1,1](near 面按 z ≥ −1 放宽)。
        let mut identity = [0f32; 16];
        for i in 0..4 {
            identity[i * 4 + i] = 1.0;
        }
        let frustum = Frustum::from_view_proj(&identity);
        assert!(frustum.intersects_aabb([-0.5, -0.5, -0.5], [0.5, 0.5, 0.5]));
        assert!(frustum.intersects_aabb([0.9, 0.9, 0.9], [2.0, 2.0, 2.0]));
        // 三轴各自完全在外侧的盒子都必须剔除。
        assert!(!frustum.intersects_aabb([2.0, 0.0, 0.0], [3.0, 1.0, 1.0]));
        assert!(!frustum.intersects_aabb([0.0, -3.0, 0.0], [1.0, -2.0, 1.0]));
        assert!(!frustum.intersects_aabb([0.0, 0.0, -3.0], [1.0, 1.0, -2.0]));
        // 缩放 0.05:可见立方体放大到 ±20。
        let mut scale = [0f32; 16];
        for i in 0..4 {
            scale[i * 4 + i] = 0.05;
        }
        let scaled = Frustum::from_view_proj(&scale);
        assert!(scaled.intersects_aabb([0.0, -10.0, 0.0], [30.0, 10.0, 60.0]));
        assert!(!scaled.intersects_aabb([30.0, -10.0, 0.0], [60.0, 10.0, 60.0]));
    }

    /// tile 生命周期:整 tile 替换、空流等价 drop、未知 tile drop 幂等、
    /// 非法 face 拒绝且不改变既有 tile。
    #[test]
    fn lod_tile_lifecycle_mirrors_section_semantics() {
        let Some(mut renderer) = renderer_or_skip_pub(16, 16) else {
            return;
        };
        let top = encode_quad(0, 0, 70, 8, 8, 0, 0, 255);
        assert!(renderer.upload_lod_tile((0, 0), &top).is_ok());
        assert_eq!(renderer.lod_tile_count(), 1);
        // 重复上传同 tile = 整体替换,不新增条目。
        assert!(renderer.upload_lod_tile((0, 0), &top).is_ok());
        assert_eq!(renderer.lod_tile_count(), 1);
        assert!(renderer.upload_lod_tile((1, -2), &top).is_ok());
        assert_eq!(renderer.lod_tile_count(), 2);
        // 空流等价 drop;未知 tile drop 幂等。
        assert!(renderer.upload_lod_tile((0, 0), &[]).is_ok());
        assert_eq!(renderer.lod_tile_count(), 1);
        renderer.drop_lod_tile((9, 9));
        renderer.drop_lod_tile((1, -2));
        assert_eq!(renderer.lod_tile_count(), 0);
        // 非法 face 拒绝。
        let mut bad = top;
        bad[16] = 5;
        assert_eq!(
            renderer.upload_lod_tile((0, 0), &bad),
            Err(LodUploadError::Invalid)
        );
        assert_eq!(renderer.lod_tile_count(), 0);
    }

    /// 远环必须真实参与渲染并保持确定性:近距无雾 tile 改变图像、两帧
    /// 逐字节一致、相机拉远到全雾距离后 quad 区域呈现天空色、昼夜
    /// 亮度改变 quad 区域颜色。
    #[test]
    fn lod_pass_renders_fog_and_daylight_deterministically() {
        let Some(mut renderer) = renderer_or_skip_pub(64, 64) else {
            return;
        };
        let bytes_per_layer: usize = (0..ATLAS_MIPS)
            .map(|mip| {
                let s = (ATLAS_TEX_SIZE >> mip).max(1) as usize;
                s * s * 4
            })
            .sum();
        assert!(renderer.upload_atlas(1, &vec![200u8; bytes_per_layer]));
        // NegZ 侧裙:x∈[0,8]、y∈[−4,4]、平面 z=0,在缩放 0.05 的视锥内
        // 投影为屏幕中央偏右的一块面积。
        let skirt = encode_quad(0, 0, -4, 8, 8, 3, 0, 153);
        assert!(renderer.upload_lod_tile((0, 0), &skirt).is_ok());

        let mut frame = empty_frame_pub();
        for i in 0..4 {
            frame.view_proj[i * 4 + i] = 0.05;
            frame.view_proj_inv[i * 4 + i] = 20.0;
        }
        frame.pos = [0.0, 80.0, 0.0]; // 距 quad ≈ 80,无雾。
        frame.daylight = 1.0;
        assert_eq!(renderer.render_frame(&frame), FrameResult::Rendered);
        let mut near = vec![0u8; 64 * 64 * 4];
        assert!(renderer.readback(&mut near));

        // 同输入第二帧必须逐字节一致(tile 遍历序确定 + 无状态漂移)。
        assert_eq!(renderer.render_frame(&frame), FrameResult::Rendered);
        let mut repeat = vec![0u8; 64 * 64 * 4];
        assert!(renderer.readback(&mut repeat));
        assert_eq!(near, repeat, "同输入两帧必须逐字节一致");

        // 昼夜 tint(同一 uniform 的 daylight)必须改变 quad 区域颜色。
        let mut night = frame.clone();
        night.daylight = 0.05;
        assert_eq!(renderer.render_frame(&night), FrameResult::Rendered);
        let mut night_img = vec![0u8; 64 * 64 * 4];
        assert!(renderer.readback(&mut night_img));

        // 相机沿 X 拉远到 >1152(全雾距离):view_proj 不变,唯一变化是
        // 雾距离,quad 区域应呈现天空色(0.25, 0.5, 1.0 的 sRGB 编码)。
        let mut far = frame.clone();
        far.pos = [1300.0, 80.0, 0.0];
        assert_eq!(renderer.render_frame(&far), FrameResult::Rendered);
        let mut far_img = vec![0u8; 64 * 64 * 4];
        assert!(renderer.readback(&mut far_img));

        // quad 投影区域:x_ndc ∈ [0, 0.4] → 列 32..58;y_ndc ∈ [−0.2, 0.2]
        // → 行 26..38;取严格内缩的采样窗。
        let region = |img: &[u8]| -> Vec<u8> {
            let mut out = Vec::new();
            for y in 28..36 {
                for x in 36..52 {
                    out.extend_from_slice(&img[(y * 64 + x) * 4..(y * 64 + x) * 4 + 4]);
                }
            }
            out
        };
        let near_region = region(&near);
        let night_region = region(&night_img);
        let far_region = region(&far_img);
        assert_ne!(near_region, night_region, "昼夜 tint 必须改变远环着色");
        assert_ne!(near_region, far_region, "距离雾必须改变远环着色");
        // 全雾区域 == 天空色:B=255(1.0),R/G 允许 ±2 的 sRGB 编码舍入。
        for px in far_region.chunks(4) {
            assert_eq!(px[0], 255, "全雾 B 分量应为 1.0");
            assert!((px[1] as i32 - 188).abs() <= 2, "全雾 G 分量应近 sRGB(0.5)");
            assert!(
                (px[2] as i32 - 137).abs() <= 2,
                "全雾 R 分量应近 sRGB(0.25)"
            );
            assert_eq!(px[3], 255);
        }
    }

    /// tile 级视锥剔除:视锥外的 tile 不得贡献像素。
    #[test]
    fn lod_tiles_outside_frustum_are_culled() {
        let Some(mut renderer) = renderer_or_skip_pub(64, 64) else {
            return;
        };
        let bytes_per_layer: usize = (0..ATLAS_MIPS)
            .map(|mip| {
                let s = (ATLAS_TEX_SIZE >> mip).max(1) as usize;
                s * s * 4
            })
            .sum();
        assert!(renderer.upload_atlas(1, &vec![200u8; bytes_per_layer]));
        // tile (0,0) 在缩放 0.05 的视锥内;tile (1000,0)(世界 x ≥ 64000)
        // 完全在视锥外,即使其 quad 几何被恶意放进视锥内也不得绘制——
        // 剔除以 tile 名义 AABB 为准。
        let skirt = encode_quad(0, 0, -4, 8, 8, 3, 0, 153);
        assert!(renderer.upload_lod_tile((0, 0), &skirt).is_ok());
        assert!(renderer.upload_lod_tile((1000, 0), &skirt).is_ok());

        let mut frame = empty_frame_pub();
        for i in 0..4 {
            frame.view_proj[i * 4 + i] = 0.05;
            frame.view_proj_inv[i * 4 + i] = 20.0;
        }
        assert_eq!(renderer.render_frame(&frame), FrameResult::Rendered);
        let mut with_outside = vec![0u8; 64 * 64 * 4];
        assert!(renderer.readback(&mut with_outside));

        renderer.drop_lod_tile((1000, 0));
        assert_eq!(renderer.render_frame(&frame), FrameResult::Rendered);
        let mut without_outside = vec![0u8; 64 * 64 * 4];
        assert!(renderer.readback(&mut without_outside));
        assert_eq!(
            with_outside, without_outside,
            "视锥外 tile 不得贡献任何像素"
        );
    }
}
