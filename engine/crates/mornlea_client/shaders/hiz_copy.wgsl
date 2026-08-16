struct CopyUniforms {
    viewport: vec2u,
};

@group(0) @binding(0) var src: texture_depth_2d;
@group(0) @binding(1) var dst: texture_storage_2d<r32float, write>;
@group(0) @binding(2) var<uniform> u: CopyUniforms;

@compute @workgroup_size(8, 8)
fn cs_main(@builtin(global_invocation_id) gid: vec3u) {
    let size = textureDimensions(dst);
    if (gid.x >= size.x || gid.y >= size.y) {
        return;
    }
    var d = 1.0;
    if (gid.x < u.viewport.x && gid.y < u.viewport.y) {
        d = textureLoad(src, vec2i(gid.xy), 0);
    }
    textureStore(dst, vec2i(gid.xy), vec4f(d, 0.0, 0.0, 1.0));
}
