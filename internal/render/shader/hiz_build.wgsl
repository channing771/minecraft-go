@group(0) @binding(0) var src: texture_2d<f32>;
@group(0) @binding(1) var dst: texture_storage_2d<r32float, write>;

@compute @workgroup_size(8, 8)
fn cs_main(@builtin(global_invocation_id) gid: vec3u) {
    let dst_size = textureDimensions(dst);
    if (gid.x >= dst_size.x || gid.y >= dst_size.y) {
        return;
    }
    let p = vec2i(gid.xy) * 2;
    let d0 = textureLoad(src, p + vec2i(0, 0), 0).r;
    let d1 = textureLoad(src, p + vec2i(1, 0), 0).r;
    let d2 = textureLoad(src, p + vec2i(0, 1), 0).r;
    let d3 = textureLoad(src, p + vec2i(1, 1), 0).r;
    textureStore(dst, vec2i(gid.xy),
        vec4f(max(max(d0, d1), max(d2, d3)), 0.0, 0.0, 1.0));
}
