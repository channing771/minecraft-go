struct Camera {
    view_proj: mat4x4f,
};

@group(0) @binding(0) var<uniform> camera: Camera;
@group(0) @binding(1) var<storage, read> visible: array<u32>;

struct VsOut {
    @builtin(position) pos:   vec4f,
    @location(0)       color: vec3f,
};

@vertex
fn vs_main(
    @builtin(vertex_index)   vi: u32,
    @builtin(instance_index) ii: u32,
) -> VsOut {
    // 一个正方形的 4 个角，配合 6 个索引组成两个三角形。
    var corner = array<vec2f, 4>(
        vec2f(0.0, 0.0), vec2f(1.0, 0.0),
        vec2f(1.0, 1.0), vec2f(0.0, 1.0),
    );

    let id = visible[ii];
    // 沿 X 轴排开，方块索引直接当世界坐标。
    let origin = vec3f(f32(id) * 1.5, 0.0, 0.0);
    let local  = vec3f(corner[vi].x, corner[vi].y, 0.0);

    var out: VsOut;
    out.pos   = camera.view_proj * vec4f(origin + local, 1.0);
    out.color = vec3f(f32(id) / 128.0, 0.5, 1.0 - f32(id) / 128.0);
    return out;
}

@fragment
fn fs_main(in: VsOut) -> @location(0) vec4f {
    return vec4f(in.color, 1.0);
}
