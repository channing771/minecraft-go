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

fn face_shade(face: u32) -> f32 {
    switch face {
        case 3u: { return 1.00; }
        case 2u: { return 0.50; }
        case 0u, 1u: { return 0.68; }
        default: { return 0.84; }
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
    let w     = f32(((lo >> 12u) & 0xFu) + 1u);
    let h     = f32(((lo >> 16u) & 0xFu) + 1u);
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

    let local = vec3f(x, y, z)
        + axis_vec(axis) * positive
        + axis_vec(ua) * (cu[vi] * w)
        + axis_vec(va) * (cv[vi] * h);

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
    out.uv    = vec2f(cu[vi] * w, cv[vi] * h);
    out.layer = f32(mat);
    out.shade = face_shade(face) * ao_factor * base;
    return out;
}

@fragment
fn fs_main(in: VsOut) -> @location(0) vec4f {
    let c = textureSample(atlas, atlas_smp, in.uv, i32(in.layer));
    return vec4f(c.rgb * in.shade, 1.0);
}
