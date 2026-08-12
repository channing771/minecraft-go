// 固定 9 格快捷栏 HUD：屏幕空间实例化矩形与数字字形。

struct Viewport {
    size: vec2<f32>,
    _pad: vec2<f32>,
};

struct Instance {
    // rect 是像素坐标下的 x, y, width, height，原点在左上角。
    rect: vec4<f32>,
    uv: vec4<f32>,
    color: vec4<f32>,
};

@group(0) @binding(0) var<uniform> viewport: Viewport;
@group(0) @binding(1) var<storage, read> quads: array<Instance>;
@group(0) @binding(2) var<storage, read> glyphs: array<Instance>;
@group(0) @binding(3) var atlas: texture_2d<f32>;
@group(0) @binding(4) var atlas_sampler: sampler;
@group(0) @binding(5) var hud_atlas: texture_2d<f32>;

struct VsOut {
    @builtin(position) clip: vec4<f32>,
    @location(0) uv: vec2<f32>,
    @location(1) color: vec4<f32>,
    @location(2) textured: f32,
};

fn corner(vi: u32) -> vec2<f32> {
    var cu = array<f32, 6>(0.0, 1.0, 1.0, 0.0, 1.0, 0.0);
    var cv = array<f32, 6>(0.0, 0.0, 1.0, 0.0, 1.0, 1.0);
    return vec2<f32>(cu[vi], cv[vi]);
}

fn screen_quad(instance: Instance, vi: u32) -> VsOut {
    let unit = corner(vi);
    let pixel = instance.rect.xy + unit * instance.rect.zw;
    let ndc = vec2<f32>(
        pixel.x / viewport.size.x * 2.0 - 1.0,
        1.0 - pixel.y / viewport.size.y * 2.0,
    );
    var out: VsOut;
    out.clip = vec4<f32>(ndc, 0.0, 1.0);
    out.uv = mix(instance.uv.xy, instance.uv.zw, unit);
    out.color = instance.color;
    out.textured = select(0.0, 1.0, instance.uv.z > instance.uv.x);
    return out;
}

@vertex
fn quad_vs(@builtin(vertex_index) vi: u32, @builtin(instance_index) ii: u32) -> VsOut {
    return screen_quad(quads[ii], vi);
}

@fragment
fn quad_fs(in: VsOut) -> @location(0) vec4<f32> {
    if in.textured > 0.5 {
        return textureSample(hud_atlas, atlas_sampler, in.uv) * in.color;
    }
    return in.color;
}

@vertex
fn glyph_vs(@builtin(vertex_index) vi: u32, @builtin(instance_index) ii: u32) -> VsOut {
    return screen_quad(glyphs[ii], vi);
}

@fragment
fn glyph_fs(in: VsOut) -> @location(0) vec4<f32> {
    let coverage = textureSample(atlas, atlas_sampler, in.uv).r;
    return vec4<f32>(in.color.rgb, in.color.a * coverage);
}
