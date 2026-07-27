struct SectionRec {
    origin:      vec4i,
    face_offset: u32,
    face_count:  u32,
    origin_idx:  u32,
    _pad:        u32,
};

struct CullUniforms {
    cam_pos: vec4f,
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

fn axis_vec(axis: u32) -> vec3f {
    if (axis == 0u) { return vec3f(1.0, 0.0, 0.0); }
    if (axis == 1u) { return vec3f(0.0, 1.0, 0.0); }
    return vec3f(0.0, 0.0, 1.0);
}

@compute @workgroup_size(64)
fn cs_main(
    @builtin(workgroup_id)        wg:  vec3u,
    @builtin(local_invocation_id) lid: vec3u,
) {
    let sec = sections[wg.x];

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
