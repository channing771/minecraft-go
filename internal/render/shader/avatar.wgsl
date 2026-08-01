struct Camera {
    view_proj: mat4x4f,
};

struct AvatarInstance {
    transform: mat4x4f,
    color:     vec4f,
};

@group(0) @binding(0) var<uniform>       camera:    Camera;
@group(0) @binding(1) var<storage, read> instances: array<AvatarInstance>;

struct VsOut {
    @builtin(position) clip:   vec4f,
    @location(0)       color:  vec4f,
    @location(1)       normal: vec3f,
};

fn cube_normal(face: u32) -> vec3f {
    switch face {
        case 0u: { return vec3f( 1.0,  0.0,  0.0); }
        case 1u: { return vec3f(-1.0,  0.0,  0.0); }
        case 2u: { return vec3f( 0.0,  1.0,  0.0); }
        case 3u: { return vec3f( 0.0, -1.0,  0.0); }
        case 4u: { return vec3f( 0.0,  0.0,  1.0); }
        default: { return vec3f( 0.0,  0.0, -1.0); }
    }
}

@vertex
fn vs_main(
    @location(0)             local: vec3f,
    @builtin(vertex_index)   vi:    u32,
    @builtin(instance_index) ii:    u32,
) -> VsOut {
    let instance = instances[ii];
    let world = instance.transform * vec4f(local, 1.0);

    var out: VsOut;
    out.clip = camera.view_proj * world;
    out.color = instance.color;
    out.normal = normalize(mat3x3f(
        instance.transform[0].xyz,
        instance.transform[1].xyz,
        instance.transform[2].xyz,
    ) * cube_normal(vi / 4u));
    return out;
}

@fragment
fn fs_main(in: VsOut) -> @location(0) vec4f {
    let light_dir = normalize(vec3f(0.35, 0.85, 0.4));
    let diffuse = 0.58 + 0.42 * max(dot(in.normal, light_dir), 0.0);
    return vec4f(in.color.rgb * diffuse, in.color.a);
}
