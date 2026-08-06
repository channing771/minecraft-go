struct Sky {
    view_proj_inv:   mat4x4f,
    sun_daylight:    vec4f,
    star_visibility: vec4f,
};

@group(0) @binding(0) var<uniform> sky: Sky;

struct VsOut {
    @builtin(position) clip: vec4f,
    @location(0) ndc: vec2f,
};

@vertex
fn vs_main(@builtin(vertex_index) vertex_index: u32) -> VsOut {
    let positions = array<vec2f, 3>(
        vec2f(-1.0, -1.0),
        vec2f(3.0, -1.0),
        vec2f(-1.0, 3.0),
    );
    let position = positions[vertex_index];
    var out: VsOut;
    out.clip = vec4f(position, 0.9999, 1.0);
    out.ndc = position;
    return out;
}

fn world_direction(ndc: vec2f) -> vec3f {
    let right = sky.view_proj_inv[0].xyz;
    let up = sky.view_proj_inv[1].xyz;
    let backward = normalize(cross(right, up));
    return normalize(right * ndc.x + up * ndc.y - backward);
}

fn hash_cell(cell: vec3u) -> u32 {
    var value = cell.x * 1664525u + cell.y * 1013904223u + cell.z * 374761393u;
    value = (value ^ (value >> 16u)) * 2246822519u;
    value = (value ^ (value >> 13u)) * 3266489917u;
    return value ^ (value >> 16u);
}

fn star_face_uv(direction: vec3f) -> vec3f {
    let axis = abs(direction);
    if (axis.x >= axis.y && axis.x >= axis.z) {
        let face = select(0.0, 1.0, direction.x < 0.0);
        return vec3f(direction.zy / axis.x, face);
    }
    if (axis.y >= axis.z) {
        let face = select(2.0, 3.0, direction.y < 0.0);
        return vec3f(direction.xz / axis.y, face);
    }
    let face = select(4.0, 5.0, direction.z < 0.0);
    return vec3f(direction.xy / axis.z, face);
}

fn star_light(direction: vec3f) -> f32 {
    let fixed_direction = normalize(round(direction * 1024.0) / 1024.0);
    let surface = star_face_uv(fixed_direction);
    let grid = (surface.xy * 0.5 + 0.5) * 64.0;
    let cell = vec2u(clamp(floor(grid), vec2f(0.0), vec2f(63.0)));
    let hash = hash_cell(vec3u(cell, u32(surface.z)));
    if ((hash & 255u) >= 20u) {
        return 0.0;
    }
    let center = vec2f(
        f32((hash >> 8u) & 255u),
        f32((hash >> 16u) & 255u)
    ) / 255.0;
    let radius = distance(fract(grid), center);
    let point = 1.0 - smoothstep(0.18, 0.30, radius);
    let brightness = 0.65 + 0.35 * f32((hash >> 24u) & 255u) / 255.0;
    return point * brightness;
}

@fragment
fn fs_main(in: VsOut) -> @location(0) vec4f {
    let direction = world_direction(in.ndc);
    let elevation = smoothstep(0.0, 1.0, clamp(direction.y, 0.0, 1.0));
    let day = mix(
        vec3f(0.72, 0.82, 0.95),
        vec3f(0.42, 0.68, 0.92),
        elevation
    );
    let night = mix(
        vec3f(0.06, 0.07, 0.12),
        vec3f(0.02, 0.03, 0.08),
        elevation
    );
    var color = mix(night, day, clamp(sky.sun_daylight.w, 0.0, 1.0));

    let star_visibility = clamp(sky.star_visibility.x, 0.0, 1.0);
    var stars = 0.0;
    if (star_visibility > 0.0 && direction.y > 0.0) {
        stars = star_light(direction)
            * star_visibility
            * smoothstep(0.0, 0.08, direction.y);
    }
    color += vec3f(stars * 0.9);

    let sun_direction = normalize(sky.sun_daylight.xyz);
    let moon_direction = -sun_direction;
    let disc_inner = 0.9993908;
    let disc_outer = 0.9992290;
    let sun_disc = smoothstep(disc_outer, disc_inner, dot(direction, sun_direction))
        * select(0.0, 1.0, sun_direction.y > 0.0);
    let moon_disc = smoothstep(disc_outer, disc_inner, dot(direction, moon_direction))
        * select(0.0, 1.0, moon_direction.y > 0.0);
    color = mix(color, vec3f(0.72, 0.80, 0.95), moon_disc);
    color = mix(color, vec3f(1.0, 0.92, 0.68), sun_disc);
    return vec4f(clamp(color, vec3f(0.0), vec3f(1.0)), 1.0);
}
