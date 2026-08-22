// 远环 LOD 壳 pass:世界坐标大 quad + 距离雾。
//
// 帧序裁决:天空之后、近环 terrain 之前;本 pass 写深度,近环以更近的
// 深度自然覆盖。顶点是 CPU 侧从 20 字节壳 quad 流装配的世界坐标角点
// (布局见 render/lod.rs),不经过近环的 section 网格编码。

struct LodCamera {
    view_proj: mat4x4f,
    // cam_pos.xyz 是相机位置，cam_pos.w 是本帧昼夜亮度 daylight
    //（与近环 terrain 共用同一语义，不新增独立昼夜状态）。
    cam_pos:   vec4f,
    // fog_color 是雾目标色 = 本帧天空色（随昼夜变化，与 clear 色同源）。
    fog_color: vec4f,
    // fog 是距离雾参数（Ruling 14 参数化）：fog.x = 起雾距离、
    // fog.y = 全雾距离，由渲染器可设状态写入；默认 768/1152 锚定
    // lodFarMultiplier=3 的默认几何（推导见 render/lod.rs 的
    // DEFAULT_FOG_*），非默认倍率由上层按配置推导后经 setter 设置。
    fog:       vec2f,
};

@group(0) @binding(0) var<uniform>   camera:   LodCamera;
@group(0) @binding(1) var            atlas:    texture_2d_array<f32>;
@group(0) @binding(2) var            atlas_smp: sampler;

struct VsOut {
    @builtin(position) clip:  vec4f,
    @location(0)       world: vec3f,
    @location(1)       uv:    vec2f,
    @location(2)       layer: f32,
    @location(3)       shade: f32,
};

// 与近环 terrain.wgsl 的 face_uv 逐字同源：按面轴取世界坐标分量推导
// terrain UV。远环 quad 无 UV 字段，UV 推导落在渲染侧且语义与近环一致
//（世界坐标 UV，同一图集同一采样器），保证远/近环地表贴图连续。
fn face_uv(world: vec3f, axis: u32) -> vec2f {
    if (axis == 0u) { return vec2f(world.y, world.z); }
    if (axis == 1u) { return vec2f(world.z, world.x); }
    return vec2f(world.x, world.y);
}

@vertex
fn vs_main(
    @location(0) world_in: vec3f,
    @location(1) layer_in: u32,
    @location(2) shade_in: u32,
    @location(3) axis_in:  u32,
) -> VsOut {
    // 昼夜 tint 与近环同源：远环不做光照传播，天空光固定满档
    //（sky = 15/15、block = 0），代入近环公式
    // base = max(0.08 + sky × (daylight − 0.08), block) 即得 daylight。
    let daylight = clamp(camera.cam_pos.w, 0.0, 1.0);
    let shade = f32(shade_in) / 255.0 * daylight;
    var out: VsOut;
    out.clip  = camera.view_proj * vec4f(world_in, 1.0);
    out.world = world_in;
    out.uv    = face_uv(world_in, axis_in);
    out.layer = f32(layer_in);
    out.shade = shade;
    return out;
}

@fragment
fn fs_main(in: VsOut) -> @location(0) vec4f {
    let c = textureSample(atlas, atlas_smp, in.uv, i32(in.layer));
    if (c.a < 0.5) { discard; }
    // 距离雾：按相机世界距离向天空色 mix；超出全雾距离（camera.fog.y）
    // 的最外缘带 fog == 1，完全呈现天空色。近环 v1 不雾化，雾只存在于
    // 本 pass。setter 出口已保证 full > start > 0，分母恒正。
    let dist = distance(in.world, camera.cam_pos.xyz);
    let fog = clamp((dist - camera.fog.x) / (camera.fog.y - camera.fog.x), 0.0, 1.0);
    return vec4f(mix(c.rgb * in.shade, camera.fog_color.rgb, fog), 1.0);
}
