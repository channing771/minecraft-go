struct Camera {
    view_proj: mat4x4f,
    right:     vec4f,
    up:        vec4f,
};

struct QuadInstance {
    anchor_center: vec4f,
    rect:          vec4f,
    uv_rect:       vec4f,
    color:         vec4f,
};

@group(0) @binding(0) var<uniform> camera: Camera;
@group(0) @binding(1) var<storage, read> glyphs: array<QuadInstance>;
@group(0) @binding(2) var<storage, read> backgrounds: array<QuadInstance>;
@group(0) @binding(3) var glyph_atlas: texture_2d<f32>;
@group(0) @binding(4) var glyph_sampler: sampler;

struct VsOut {
    @builtin(position) clip:  vec4f,
    @location(0)       uv:    vec2f,
    @location(1)       color: vec4f,
};

fn quad_corner(vertex_index: u32) -> vec2f {
    switch vertex_index {
        case 0u: { return vec2f(0.0, 0.0); }
        case 1u: { return vec2f(1.0, 0.0); }
        case 2u: { return vec2f(1.0, 1.0); }
        case 3u: { return vec2f(0.0, 0.0); }
        case 4u: { return vec2f(1.0, 1.0); }
        default: { return vec2f(0.0, 1.0); }
    }
}

fn make_quad(instance: QuadInstance, vertex_index: u32) -> VsOut {
    let corner = quad_corner(vertex_index);
    let pixel = vec2f(
        instance.rect.x + corner.x * instance.rect.z - instance.anchor_center.w,
        instance.rect.y + corner.y * instance.rect.w,
    );
    let pixel_scale = 0.01;
    let world = instance.anchor_center.xyz
        + camera.right.xyz * (pixel.x * pixel_scale)
        - camera.up.xyz * (pixel.y * pixel_scale);

    var out: VsOut;
    out.clip = camera.view_proj * vec4f(world, 1.0);
    out.uv = mix(instance.uv_rect.xy, instance.uv_rect.zw, corner);
    out.color = instance.color;
    return out;
}

@vertex
fn background_vs(
    @builtin(vertex_index) vertex_index: u32,
    @builtin(instance_index) instance_index: u32,
) -> VsOut {
    return make_quad(backgrounds[instance_index], vertex_index);
}

@fragment
fn background_fs(in: VsOut) -> @location(0) vec4f {
    return in.color;
}

@vertex
fn glyph_vs(
    @builtin(vertex_index) vertex_index: u32,
    @builtin(instance_index) instance_index: u32,
) -> VsOut {
    return make_quad(glyphs[instance_index], vertex_index);
}

@fragment
fn glyph_fs(in: VsOut) -> @location(0) vec4f {
    let coverage = textureSample(glyph_atlas, glyph_sampler, in.uv).r;
    return vec4f(in.color.rgb, in.color.a * coverage);
}
