// 全屏叠加：一条管线同时服务「确认受伤的红色屏幕边缘」与「相机浸没时的水色叠加」。
//
// 两者只有 uniform 不同：edge = 1 走边缘渐变（伤害红边，与本文件历史行为逐位一致），
// edge = 0 走全屏均匀覆盖（水下水色）。刻意不为水色另开一条管线——规格要求
// 水下视觉不得引入新的绘制管线。

struct ScreenTint {
    /// 叠加色 RGB 与最大不透明度 A。
    color: vec4<f32>,
    /// 1 = 边缘渐变，0 = 全屏均匀；中间值线性插值。
    edge: f32,
    _pad0: f32,
    _pad1: f32,
    _pad2: f32,
};

@group(0) @binding(0) var<uniform> overlay: ScreenTint;

struct VsOut {
    @builtin(position) clip: vec4<f32>,
    @location(0) uv: vec2<f32>,
};

@vertex
fn vs_main(@builtin(vertex_index) vertex_index: u32) -> VsOut {
    var positions = array<vec2<f32>, 3>(
        vec2<f32>(-1.0, -1.0),
        vec2<f32>(3.0, -1.0),
        vec2<f32>(-1.0, 3.0),
    );
    let position = positions[vertex_index];
    var out: VsOut;
    out.clip = vec4<f32>(position, 0.0, 1.0);
    out.uv = vec2<f32>(position.x * 0.5 + 0.5, 0.5 - position.y * 0.5);
    return out;
}

@fragment
fn fs_main(in: VsOut) -> @location(0) vec4<f32> {
    let edge_distance = min(
        min(in.uv.x, 1.0 - in.uv.x),
        min(in.uv.y, 1.0 - in.uv.y),
    );
    let edge_factor = 1.0 - smoothstep(0.0, 0.35, edge_distance);
    let factor = mix(1.0, edge_factor, overlay.edge);
    return vec4<f32>(overlay.color.rgb, overlay.color.a * factor);
}
