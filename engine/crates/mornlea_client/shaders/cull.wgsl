struct SectionRec {
    origin:      vec4i,
    face_offset: u32,
    face_count:  u32,
    origin_idx:  u32,
    _pad:        u32,
};

struct CullUniforms {
    cam_pos:      vec4f,
    view_proj:    mat4x4f,
    hiz_size:     vec4f,
    hiz_uv_scale: vec4f,
    hiz_enabled:  vec4u,
};

struct DrawArgs {
    index_count:    u32,
    instance_count: atomic<u32>,
    first_index:    u32,
    base_vertex:    u32,
    first_instance: u32,
};

@group(0) @binding(0) var<uniform>             u:        CullUniforms;
@group(0) @binding(1) var<storage, read>       sections: array<SectionRec>;
@group(0) @binding(2) var<storage, read>       faces:    array<u32>;
@group(0) @binding(3) var<storage, read_write> visible:  array<vec4u>;
@group(0) @binding(4) var<storage, read_write> args:     DrawArgs;
@group(0) @binding(5) var hiz: texture_2d<f32>;

var<workgroup> section_is_occluded: bool;

fn axis_vec(axis: u32) -> vec3f {
    if (axis == 0u) { return vec3f(1.0, 0.0, 0.0); }
    if (axis == 1u) { return vec3f(0.0, 1.0, 0.0); }
    return vec3f(0.0, 0.0, 1.0);
}

fn section_occluded(origin: vec3f) -> bool {
    var min_uv = vec2f( 1e30,  1e30);
    var max_uv = vec2f(-1e30, -1e30);
    var min_z = 1e30;

    for (var i = 0u; i < 8u; i++) {
        let corner = origin + vec3f(
            f32( i        & 1u) * 16.0,
            f32((i >> 1u) & 1u) * 16.0,
            f32((i >> 2u) & 1u) * 16.0,
        );
        let clip = u.view_proj * vec4f(corner, 1.0);
        if (clip.w <= 0.0) {
            return false;
        }
        let ndc = clip.xyz / clip.w;
        let uv = ndc.xy * vec2f(0.5, -0.5) + vec2f(0.5, 0.5);
        min_uv = min(min_uv, uv);
        max_uv = max(max_uv, uv);
        min_z = min(min_z, ndc.z);
    }

    min_uv = clamp(min_uv, vec2f(0.0), vec2f(1.0));
    max_uv = clamp(max_uv, vec2f(0.0), vec2f(1.0));
    let size_px = (max_uv - min_uv) * u.hiz_size.xy;
    let level = clamp(
        ceil(log2(max(max(size_px.x, size_px.y), 1.0))),
        0.0, u.hiz_size.z);

    let dim = vec2i(textureDimensions(hiz, u32(level)));
    let padded_min = min_uv * u.hiz_uv_scale.xy;
    let padded_max = max_uv * u.hiz_uv_scale.xy;
    let p0 = clamp(vec2i(floor(padded_min * vec2f(dim))), vec2i(0), dim - 1);
    let p1 = clamp(vec2i(floor(padded_max * vec2f(dim))), vec2i(0), dim - 1);
    let d00 = textureLoad(hiz, vec2i(p0.x, p0.y), i32(level)).r;
    let d10 = textureLoad(hiz, vec2i(p1.x, p0.y), i32(level)).r;
    let d01 = textureLoad(hiz, vec2i(p0.x, p1.y), i32(level)).r;
    let d11 = textureLoad(hiz, vec2i(p1.x, p1.y), i32(level)).r;
    let d = max(max(d00, d10), max(d01, d11));
    return min_z > d;
}

@compute @workgroup_size(64)
fn cs_main(
    @builtin(workgroup_id)        wg:  vec3u,
    @builtin(local_invocation_id) lid: vec3u,
) {
    let sec = sections[wg.x];
    if (lid.x == 0u) {
        section_is_occluded =
            u.hiz_enabled.x != 0u && section_occluded(vec3f(sec.origin.xyz));
    }
    workgroupBarrier();
    if (section_is_occluded) {
        return;
    }

    for (var i = lid.x; i < sec.face_count; i += 64u) {
        let base = (sec.face_offset + i) * 2u;
        let lo = faces[base];
        let hi = faces[base + 1u];

        let face = (lo >> 20u) & 0x7u;
        let axis = face >> 1u;
        var normal = axis_vec(axis);
        if ((face & 1u) == 0u) {
            normal = -normal;
        }

        let local = vec3f(
            f32( lo         & 0xFu),
            f32((lo >>  4u) & 0xFu),
            f32((lo >>  8u) & 0xFu),
        ) + normal * f32(face & 1u);
        let world = vec3f(sec.origin.xyz) + local;

        if (dot(normal, world - u.cam_pos.xyz) >= 0.0) {
            continue;
        }

        let slot = atomicAdd(&args.instance_count, 1u);
        visible[slot] = vec4u(lo, hi, sec.origin_idx, 0u);
    }
}
