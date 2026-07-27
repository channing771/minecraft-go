// DrawIndexedIndirect 的参数布局，字段顺序由 WebGPU 规范固定。
struct DrawIndexedIndirect {
    index_count:    u32,
    instance_count: atomic<u32>,
    first_index:    u32,
    base_vertex:    u32,
    first_instance: u32,
};

@group(0) @binding(0) var<storage, read_write> args: DrawIndexedIndirect;
@group(0) @binding(1) var<storage, read_write> visible: array<u32>;

// 筛选规则：偶数号候选通过。真实管线里这里换成视锥/遮挡/背面判定。
@compute @workgroup_size(64)
fn cs_main(@builtin(global_invocation_id) gid: vec3u) {
    let i = gid.x;
    if (i >= arrayLength(&visible)) {
        return;
    }
    if ((i & 1u) != 0u) {
        return;
    }
    // 原子累加得到本实例在紧凑输出中的槽位，同时累出总实例数。
    let slot = atomicAdd(&args.instance_count, 1u);
    visible[slot] = i;
}
