// 水面 pass：与 terrain 共享 camera uniform、section origin 表、材质 atlas 与
// 世界坐标 UV，只在两点上不同：
//
//  1. 顶点解码。水面 quad 借走 `w`/`h` 那 8 bit 装角高度（见下），因此**不得**
//     沿用 terrain 的 `w-1`/`h-1` 解码——那会把每片水面画成 8×8 到 16×16 的
//     巨型石板。水面本就不贪心合并，`w`/`h` 恒为 1。
//  2. 片元输出保留材质 alpha 交给 alpha blend，而不是 cutout 的 discard。
//
// 实例布局与 cull compute 写出的 visible 实例逐字节相同（`vec4u(lo, hi,
// origin_idx, 0)`），只是由 CPU 在区段上传时一次写好：水面不接 GPU culling。

struct Camera {
    view_proj: mat4x4f,
    // cam_pos.xyz 是相机位置，cam_pos.w 是本帧固定昼夜亮度 daylight。
    cam_pos:   vec4f,
};

@group(0) @binding(0) var<uniform>       camera:    Camera;
@group(0) @binding(1) var<storage, read> instances: array<vec4u>;
@group(0) @binding(2) var<storage, read> origins:   array<vec4i>;
@group(0) @binding(3) var                atlas:     texture_2d_array<f32>;
@group(0) @binding(4) var                atlas_smp: sampler;

struct VsOut {
    @builtin(position) clip:  vec4f,
    @location(0)       uv:    vec2f,
    @location(1)       layer: f32,
    @location(2)       shade: f32,
};

fn axis_vec(axis: u32) -> vec3f {
    if (axis == 0u) { return vec3f(1.0, 0.0, 0.0); }
    if (axis == 1u) { return vec3f(0.0, 1.0, 0.0); }
    return vec3f(0.0, 0.0, 1.0);
}

fn face_uv(world: vec3f, axis: u32) -> vec2f {
    if (axis == 0u) { return vec2f(world.y, world.z); }
    if (axis == 1u) { return vec2f(world.z, world.x); }
    return vec2f(world.x, world.y);
}

fn face_shade(face: u32) -> f32 {
    switch face {
        case 3u: { return 1.00; }
        case 2u: { return 0.50; }
        case 0u, 1u: { return 0.68; }
        default: { return 0.84; }
    }
}

// corner_height 取出第 vi 个顶点的 4-bit 角高度原值。
//
// 位布局与 engine 的 `quad.rs`（SHIFT_W / SHIFT_H / SHIFT_CORNER2 /
// SHIFT_CORNER3）逐位对应：
//
//   | quad 位 | 内容   | 本着色器读法          |
//   |---------|--------|-----------------------|
//   | 12..15  | 角 0   | lo >> 12              |
//   | 16..19  | 角 1   | lo >> 16              |
//   | 55..58  | 角 2   | hi >> 23（55 - 32）   |
//   | 59..62  | 角 3   | hi >> 27（59 - 32）   |
//
// 角顺序与 `Quad::corners` 一致，即局部 (u,v) 的 (0,0) (1,0) (1,1) (0,1)——
// 正是下面 cu/cv 表的顺序，于是顶点索引 vi 直接就是角索引。
fn corner_height(lo: u32, hi: u32, vi: u32) -> u32 {
    switch vi {
        case 0u:  { return (lo >> 12u) & 0xFu; }
        case 1u:  { return (lo >> 16u) & 0xFu; }
        case 2u:  { return (hi >> 23u) & 0xFu; }
        default:  { return (hi >> 27u) & 0xFu; }
    }
}

@vertex
fn vs_main(
    @builtin(vertex_index)   vi: u32,
    @builtin(instance_index) ii: u32,
) -> VsOut {
    let inst = instances[ii];
    let lo = inst.x;
    let hi = inst.y;

    let x     = f32( lo          & 0xFu);
    let y     = f32((lo >>  4u) & 0xFu);
    let z     = f32((lo >>  8u) & 0xFu);
    let face  =      (lo >> 20u) & 0x7u;
    let mat   = ((lo >> 23u) & 0x1FFu) | ((hi & 0x7Fu) << 9u);
    let ao    = (hi >>  7u) & 0xFFu;
    let light = (hi >> 15u) & 0xFFu;

    let axis = face >> 1u;
    let positive = f32(face & 1u);
    let ua = (axis + 1u) % 3u;
    let va = (axis + 2u) % 3u;

    var cu = array<f32, 4>(0.0, 1.0, 1.0, 0.0);
    var cv = array<f32, 4>(0.0, 0.0, 1.0, 1.0);

    // 水面 quad 恒为 1×1：bit 12..19 装的是角高度，不是尺寸。
    var local = vec3f(x, y, z)
        + axis_vec(axis) * positive
        + axis_vec(ua) * cu[vi]
        + axis_vec(va) * cv[vi];

    // 角高度非零 ⟺ 该顶点落在这一格的顶面那一层（侧面的下沿顶点与底面顶点
    // 语义上在方块底面、记 0，engine 侧 `fluid_corners` 保证真流体高度恒 >= 7）。
    // 实际高度是 (raw+1)/16：raw=15 即满格，raw=7 即半格。
    let raw = corner_height(lo, hi, vi);
    if (raw != 0u) {
        local.y = y + (f32(raw) + 1.0) / 16.0;
    }

    let o = origins[inst.z];
    let world = vec3f(o.xyz) + local;
    let ao_level = f32((ao >> (vi * 2u)) & 0x3u);
    let ao_factor = 0.55 + 0.45 * (ao_level / 3.0);
    let sky = f32((light >> 4u) & 0xFu) / 15.0;
    let block = f32(light & 0xFu) / 15.0;
    let daylight = clamp(camera.cam_pos.w, 0.0, 1.0);
    let sky_base = 0.08 + sky * (daylight - 0.08);
    let base = max(sky_base, block);

    var out: VsOut;
    out.clip  = camera.view_proj * vec4f(world, 1.0);
    out.uv    = face_uv(world, axis);
    out.layer = f32(mat);
    out.shade = face_shade(face) * ao_factor * base;
    return out;
}

@fragment
fn fs_main(in: VsOut) -> @location(0) vec4f {
    let c = textureSample(atlas, atlas_smp, in.uv, i32(in.layer));
    // 保留材质 alpha 交给 alpha blend：水面是半透明的，不是 cutout。
    return vec4f(c.rgb * in.shade, c.a);
}
